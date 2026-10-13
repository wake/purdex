package resourcesmod

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// newPositionID is "pos_" and 8 lower-case hex digits: short, because an agent
// types it in `pdx lease cancel <id>`.
func newPositionID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "pos_" + hex.EncodeToString(b[:])
}

// createPositioned is the create path of a queue- or ask-mode request in mode
// lease (amendment A §2.4 P1, R1 – R3): the row is attached to the session's
// live position, which is created when it has none, in one transaction; then the
// pass runs, and the answer tells at once whether the command may run. Nothing
// here waits.
func (m *Module) createPositioned(w http.ResponseWriter, r *http.Request, req resources.LeaseRequest, row leaseRow,
	agentPID int, agentStart string, set resources.Settings, fallback bool) {
	if row.Mode == resources.CallQueue {
		row.LeaseUntil = 0 // nobody polls a queue-mode row (R1)
	}
	now := m.now()
	m.stateMu.Lock()
	got, adopted, err := m.adoptQueued(row)
	created := false
	if err == nil && !adopted {
		got, _, created, err = m.store.CreateAttached(positionRow{
			ID: newPositionID(), SessionID: row.SessionID, AgentPID: agentPID, AgentStart: agentStart, CreatedAt: now.UnixMilli(),
		}, row)
	}
	m.stateMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "create lease: "+err.Error())
		return
	}
	if !adopted && !created {
		// Another POST of the same client id won the insert: it is a replay after all.
		m.replay(w, r, req, got, set)
		return
	}
	if created {
		m.requestEvent() // a new request shows in the queue
	}
	m.admissionPass(r.Context(), got.ID)
	again, ok, err := m.store.Get(got.ID)
	if err != nil || !ok {
		writeErr(w, http.StatusInternalServerError, resources.ErrNotReady, "read lease after admission failed")
		return
	}
	if again.State == resources.StateWaiting && again.Mode == resources.CallQueue {
		if reason := m.queuedReasonOf(again); reason != again.QueuedReason {
			_ = m.store.SetQueuedReason(again.ID, reason)
		}
	}
	code := http.StatusCreated
	if adopted {
		code = http.StatusOK
	}
	writeJSON(w, code, m.leaseResponse(again, set, fallback))
}

// adoptQueued is R2: a queue-mode call that repeats the command and cwd of a
// waiting queue row of the same session takes that row over (same id, same
// place) instead of making another. adopted is false when there is nothing to
// take over. stateMu is held.
func (m *Module) adoptQueued(n leaseRow) (got leaseRow, adopted bool, err error) {
	if n.Mode != resources.CallQueue || n.Command == "" {
		return leaseRow{}, false, nil
	}
	pos, ok, err := m.store.LivePositionOf(n.SessionID)
	if err != nil || !ok {
		return leaseRow{}, false, err
	}
	rows, err := m.store.RowsOf(pos.ID)
	if err != nil {
		return leaseRow{}, false, err
	}
	for _, old := range rows {
		if old.State != resources.StateWaiting || old.Mode != resources.CallQueue || old.Command != n.Command || old.Cwd != n.Cwd {
			continue
		}
		won, err := m.store.AdoptQueued(old, n, 0)
		if err != nil {
			return leaseRow{}, false, err
		}
		if !won {
			continue
		}
		got, ok, err := m.store.Get(old.ID)
		if err != nil || !ok {
			return leaseRow{}, false, err
		}
		m.logf("[resources] lease %s adopted by a repeat of its command (session %s)", got.ID, got.SessionID)
		return got, true, nil
	}
	return leaseRow{}, false, nil
}

// endCommand ends a held row and applies amendment A §3.3 to its position, in
// one transaction (EndCommand). notice says the ending client shows the final
// notice to its session (DELETE notice=1); without it a limit that would warn
// treats the end as an ordinary one (idle, not warned), so a session is never
// released without having been told (V21). guard is an extra condition on the
// row (the sweeper's vanished check). It returns whether this call ended the
// row and the final notice text, if the end carries one. stateMu is held.
//
// A row with no position, or whose position is missing or ended, ends exactly as
// a plain lease always did (R5).
func (m *Module) endCommand(row leaseRow, reason string, now time.Time, notice bool, guard string, guardArgs ...any) (won bool, finalNotice string, err error) {
	nowMS := now.UnixMilli()
	e := commandEnd{Reason: reason, RowGuard: guard, RowGuardArgs: guardArgs}
	if row.PositionID != "" {
		pos, ok, err := m.store.PositionByID(row.PositionID)
		if err != nil {
			return false, "", err
		}
		if ok && pos.State != resources.PosEnded {
			e.PositionID = pos.ID
			rows, err := m.store.RowsOf(pos.ID)
			if err != nil {
				return false, "", err
			}
			otherHeld := false
			for _, r := range rows {
				if r.ID != row.ID && r.State == resources.StateHeld {
					otherHeld = true
				}
			}
			waiting, err := m.someoneWaitingFor(pos.ID, nowMS)
			if err != nil {
				return false, "", err
			}
			switch {
			case otherHeld:
				e.Outcome = outcomeCount // the last one to end decides (A §3.2)
			case pos.DoneRequested:
				e.Outcome, e.PosReason = outcomeEnd, resources.EndDone
			default:
				after, kind, warned := resources.AfterCommandEnd(pos.CommandsEnded+1,
					now.Sub(time.UnixMilli(pos.FirstStartedAt)), pos.Warned, waiting)
				if after == resources.AfterIdleWarn && !notice {
					after, kind, warned = resources.AfterIdle, resources.NoticeNone, false // rule 3 needs a release that shows it
				}
				switch after {
				case resources.AfterRelease:
					e.Outcome, e.NewPositionID = outcomeRelease, newPositionID()
				default:
					e.Outcome, e.WindowUntil = outcomeIdle, nowMS+resources.PositionWindow.Milliseconds()
					e.Warned, e.NewWarning, e.Notice = warned, after == resources.AfterIdleWarn, resources.FinalNotice(kind)
				}
			}
		}
	}
	res, err := m.store.EndCommand(row.ID, nowMS, e)
	if err != nil || !res.Ended {
		return false, "", err
	}
	return true, e.Notice, nil
}

// someoneWaitingFor says whether anyone but the position waits for the host:
// another position that is queued (a passed one included), or a wait-mode row
// that is still polled (Q5).
func (m *Module) someoneWaitingFor(positionID string, nowMS int64) (bool, error) {
	positions, err := m.store.LivePositions()
	if err != nil {
		return false, err
	}
	for _, p := range positions {
		if p.ID != positionID && p.State == resources.PosQueued {
			return true, nil
		}
	}
	waiting, err := m.store.Waiting()
	if err != nil {
		return false, err
	}
	for _, w := range waiting {
		if w.PositionID == "" && w.LeaseUntil > nowMS {
			return true, nil
		}
	}
	return false, nil
}
