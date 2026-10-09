package teammod

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// handleRelayOp is GET /api/relay/ops/{id} (debug).
func (m *Module) handleRelayOp(w http.ResponseWriter, r *http.Request) {
	op, ok, err := m.store.GetRelayOp(r.PathValue("id"))
	if err != nil {
		m.logf("[team] relay op %s: %v", r.PathValue("id"), err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such relay op", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, op)
}

// handleRelayReport is POST /api/relay/ops/{id}/report (spec §8.3): one
// transition from the mod, idempotent per (op, state). cleared needs
// new_session_id and writes the lineage (store, one tx) and moves the
// title (meta.db, right after); failed and cancelled need error (the
// reason). A state the op cannot reach is 409 bad_transition carrying the
// op as it is. A cleared the store refuses because it would corrupt the
// lineage (ErrBadRelayReport: new == old, the new session already heads
// another op's lineage, a cycle) is 400 bad_request with the store's
// reason, the op untouched. A report that moves the op to a terminal
// state while its approval row is still open (the mod's `cancelled
// --error compacted` mid-wait, spec §8.7 (c)) also closes that row as
// cancelled through the usual CAS — one `closed` event, the dialogs go
// away, `pdx relay wait` exits 12; afterClose then finds the op already
// there and is a no-op.
func (m *Module) handleRelayReport(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	id := r.PathValue("id")
	var req team.RelayReportRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	switch req.State {
	case team.RelayWriting, team.RelayWritten, team.RelayDone:
	case team.RelayClaimed:
		// Only the approval's close claims a self op (afterClose), and only
		// P6's claim route claims a member op: a report of `claimed` would
		// let a buggy or hostile mod step past the person's approval.
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "claimed is not reportable: a self op is claimed by its approval, a member op by pdx relay claim", nil)
		return
	case team.RelayCleared:
		if strings.TrimSpace(req.NewSessionID) == "" {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "new_session_id is required for cleared", nil)
			return
		}
	case team.RelayFailed, team.RelayCancelled:
		if strings.TrimSpace(req.Error) == "" {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "error (the reason) is required for failed and cancelled", nil)
			return
		}
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "unknown state", nil)
		return
	}
	rep := RelayReport{State: req.State, Reason: strings.TrimSpace(req.Error), At: m.now()}
	if req.State == team.RelayCleared {
		rep.NewSessionID = strings.TrimSpace(req.NewSessionID)
		rep.NewRef = ipeers.RefID(rep.NewSessionID)
		rep.Reason = ""
		if code, detail := m.checkClearedTarget(id, rep.NewSessionID); code != 0 {
			apiErr := team.ErrBadRequest
			if code == http.StatusServiceUnavailable {
				apiErr = team.ErrNotReady
			}
			m.writeErr(w, code, apiErr, detail, nil)
			return
		}
	}
	if rep.State.Terminal() {
		// A terminal report on an op still awaiting approval closes the
		// approval row FIRST, through the same CAS an approve uses, and the
		// winner's afterClose moves the op with this report's state and
		// reason. So either this report wins (row cancelled, op as reported)
		// or an approve did (row approved, op claimed — and the report
		// below then cancels/fails the claimed op, a legal step): the op is
		// never cancelled underneath an approve that is still to succeed.
		cur, ok, err := m.store.GetRelayOp(id)
		if err != nil {
			// Not knowing the op's state is not a licence to commit a
			// terminal state over a row that may still be open.
			m.logf("[team] relay report %s: read op: %v", id, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
		if ok && cur.State == team.RelayAwaitingApproval && cur.RequestID != "" {
			if m.beforeTerminalClose != nil {
				m.beforeTerminalClose(id) // test seam: the approve that races this report
			}
			_, won, err := m.closeAsWithOp(cur.RequestID, Close{State: team.StateCancelled, DecidedAt: m.now()}, rep)
			if err != nil && !errors.Is(err, ErrNoSuchApproval) {
				m.logf("[team] relay report %s: close request %s: %v", id, cur.RequestID, err)
				m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
				return
			}
			if !won {
				// Another close won the row (an approve, a deny, the
				// sweeper), and its afterClose may not have run yet. Bring
				// the op up to that row's verdict first — approved → claimed,
				// denied → cancelled{denied} — so this report is applied on
				// top of the person's decision, never underneath it. The
				// winner's own afterClose is then a no-op.
				m.createMu.Lock()
				_, _, rerr := m.reconcileAwaitingOp(cur)
				m.createMu.Unlock()
				if rerr != nil {
					m.logf("[team] relay report %s: reconcile after a lost close: %v", id, rerr)
					m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
					return
				}
			}
		}
	}
	op, res, err := m.store.ReportRelay(id, rep)
	if errors.Is(err, ErrNoSuchRelayOp) {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such relay op", nil)
		return
	}
	if errors.Is(err, ErrBadRelayReport) {
		// PR P5a-1a codex R2: the store refused a cleared that would
		// corrupt the lineage; the op is as it was. The caller's input is
		// what is wrong, so 400 with the reason — not a 500.
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
		return
	}
	if err != nil {
		m.logf("[team] relay report %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	switch res {
	case ReportBadTransition:
		m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrBadTransition, Detail: "state " + string(op.State) + " does not lead to " + string(req.State), Op: &op})
		return
	case ReportApplied:
		m.logf("[team] relay op %s → %s%s", id, op.State, reasonSuffix(op))
		m.afterReport(op)
		m.handoverNoticeAsync(op) // only on Applied, never on a re-send (Noop)
	case ReportNoop:
		// The idempotent re-send is also the retry of the follow-ups: a
		// title move or a row close that failed after the first report
		// committed runs again here (both are idempotent).
		m.afterReport(op)
	}
	m.writeJSON(w, http.StatusOK, op)
}

// afterReport is what follows an op's state in the store: a cleared op
// moves its title; a done op moves it too (the last chance before the op
// leaves the active set that the boot reconciliation walks); a terminal op
// closes its approval row if that is still open. Every step is idempotent,
// so it runs on the first report and on every re-send of the same state.
func (m *Module) afterReport(op team.RelayOp) {
	if op.State == team.RelayCleared || op.State == team.RelayDone {
		m.moveTitle(op)
		// A cleared moved the team's lead or a member to the new session
		// (relay_store_report.go moveTeamRoles); done re-announces after a
		// restart between the two. Unchanged is free (the hash gate).
		m.rosterChanged()
	}
	if op.State.Terminal() && op.RequestID != "" {
		m.closeRequestOfReportedOp(op)
	}
}

// checkClearedTarget guards a cleared's new_session_id (PR #1716 attacker
// A-1 and the critic's TOCTOU): /clear keeps the Claude Code PROCESS and
// gives it a new session id, so the new id must be a live session on this
// host under the SAME process as the op's origin — a new id live under
// another process is someone else's conversation, whose title and ref this
// report would hijack (400). The registry learns the new id a moment after
// /clear (measured 2026-10-07: ~0.6 s after Enter, the SessionStart hook in
// the same second), so the handler waits up to clearedWait for it to
// appear; a new id the registry still does not know is 503 not_ready, which
// the mod retries at its next turn.complete (P5b-2) — the lineage is never
// written for a session this host cannot vouch for.
func (m *Module) checkClearedTarget(opID, newSessionID string) (code int, detail string) {
	op, ok, err := m.store.GetRelayOp(opID)
	if err != nil {
		return http.StatusServiceUnavailable, "team.db failed; retry: " + err.Error()
	}
	if !ok {
		return 0, "" // the store's 404 below
	}
	// The binding is the op's approval row's origin PID; without it there
	// is nothing to vouch for the new session, so the report is refused
	// rather than waved through (fail closed). A member op (P6) will bring
	// its own binding.
	if op.RequestID == "" {
		return http.StatusBadRequest, "relay op " + opID + " has no approval row to bind new_session_id to"
	}
	row, ok, err := m.store.Get(op.RequestID)
	if err != nil {
		return http.StatusServiceUnavailable, "team.db failed; retry: " + err.Error()
	}
	if !ok || row.Origin.PID == 0 {
		return http.StatusBadRequest, "relay op " + opID + ": its approval row (" + op.RequestID + ") or origin pid is missing; cannot bind new_session_id"
	}
	wantPID := row.Origin.PID
	deadline := time.Now().Add(m.clearedWait)
	for {
		target, live, err := m.origins.ResolveOriginBySession(newSessionID)
		if err != nil {
			return http.StatusServiceUnavailable, "registry unreadable; retry: " + err.Error()
		}
		if live {
			if target.PID != wantPID {
				return http.StatusBadRequest, "new_session_id " + newSessionID + " is live under another process (pid " + strconv.Itoa(target.PID) + ", the op's is " + strconv.Itoa(wantPID) + "); a cleared session keeps its process"
			}
			return 0, ""
		}
		if time.Now().After(deadline) {
			return http.StatusServiceUnavailable, "new_session_id " + newSessionID + " is not (yet) a live session on this host; retry"
		}
		time.Sleep(m.clearedPoll)
	}
}

// closeRequestOfReportedOp closes the op's approval row when a report put
// the op in a terminal state while the row is still open (compacted mid-
// wait; a failed op whose row is somehow open). The same CAS as every
// close: a row already closed makes the CAS lose and nothing is broadcast.
// closeAs runs afterClose, which reports cancelled{abandoned} on an op that
// is already cancelled — ReportRelay answers ReportNoop for the same state,
// so the report's reason (compacted) stands and nothing is logged.
func (m *Module) closeRequestOfReportedOp(op team.RelayOp) {
	_, won, err := m.closeAs(op.RequestID, Close{State: team.StateCancelled, DecidedAt: m.now()})
	if err != nil && !errors.Is(err, ErrNoSuchApproval) {
		m.logf("[team] relay op %s %s: close request %s: %v", op.ID, op.State, op.RequestID, err)
		return
	}
	if won {
		m.logf("[team] relay op %s %s: request %s closed", op.ID, op.State, op.RequestID)
	}
}

// moveTitle carries the old session's title to the new one (spec §8.4).
// It runs after the lineage tx commits (meta.db is another database, so
// it cannot share the transaction) and is idempotent: a title already
// moved, or none to move, is a no-op; a failure is logged and the boot
// reconciliation retries it for every op still in cleared.
func (m *Module) moveTitle(op team.RelayOp) {
	if m.titles == nil || op.NewSessionID == "" {
		return
	}
	moved, err := m.titles.Move(op.SessionID, op.NewSessionID, time.UnixMilli(m.now()))
	if err != nil {
		m.logf("[team] relay op %s: move title %s → %s: %v", op.ID, op.SessionID, op.NewSessionID, err)
		return
	}
	if moved {
		m.logf("[team] relay op %s: title moved %s → %s", op.ID, op.SessionID, op.NewSessionID)
	}
}

// reconcileRelays runs at Start (spec §9.3, the part P5a owns: self ops).
// For every active op: an awaiting_approval op is re-derived from its
// approval row by reconcileAwaitingOp — the row is already closed → the
// op takes that row's verdict (afterClose missed it); the row is missing
// → cancelled{abandoned}; the row is still open → the op is genuinely
// open, and if its session is gone it is abandoned through the row's
// CAS, so the broadcast and the op follow as in the live path. A cleared
// op re-runs the title move (idempotent). Member ops in requested are
// P6's. Nothing here waits for a hook. It holds createMu, as every caller
// of reconcileAwaitingOp does, so a begin that races the boot sees the
// reconciled op.
func (m *Module) reconcileRelays() {
	ops, err := m.store.ListActiveRelayOps()
	if err != nil {
		m.logf("[team] boot: list relay ops: %v", err)
		return
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	now := m.now()
	// Open self_relay rows whose op is already terminal (a report's close
	// failed after the op committed): close them now, abandoned.
	if rows, err := m.store.ListOpen(); err != nil {
		m.logf("[team] boot: list open approvals: %v", err)
	} else {
		for _, row := range rows {
			if row.Kind != team.KindSelfRelay {
				continue
			}
			op, ok, err := m.store.RelayOpByRequest(row.ID)
			if err != nil || !ok || !op.State.Terminal() {
				continue
			}
			if _, won, err := m.closeAs(row.ID, Close{State: team.StateAbandoned, DecidedAt: now}); err != nil {
				m.logf("[team] boot: close approval %s of terminal relay op %s: %v", row.ID, op.ID, err)
			} else if won {
				m.logf("[team] boot: approval %s closed, its relay op %s is already %s", row.ID, op.ID, op.State)
			}
		}
	}
	for _, op := range ops {
		switch {
		case op.Kind == team.RelayKindSelf && op.State == team.RelayAwaitingApproval:
			cur, _, err := m.reconcileAwaitingOp(op)
			if err != nil {
				m.logf("[team] boot: relay op %s: approval %s: %v", op.ID, op.RequestID, err)
				continue
			}
			// Still awaiting after the step ⇔ the row is open. A live
			// session keeps waiting; a gone one is abandoned via the row.
			if cur.State != team.RelayAwaitingApproval || m.origins.LiveSession(cur.SessionID) {
				continue
			}
			if _, won, err := m.closeAs(cur.RequestID, Close{State: team.StateAbandoned, DecidedAt: now}); err != nil {
				m.logf("[team] boot: abandon approval %s of relay op %s: %v", cur.RequestID, cur.ID, err)
			} else if won {
				m.logf("[team] boot: approval %s abandoned, its session %s is gone (relay op %s)", cur.RequestID, cur.SessionID, cur.ID)
			}
		case op.State == team.RelayCleared:
			m.moveTitle(op)
		}
	}
}
