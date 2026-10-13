package resourcesmod

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

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
