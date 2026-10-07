package teammod

import (
	"errors"
	"net/http"
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
	case team.RelayClaimed, team.RelayWriting, team.RelayWritten, team.RelayDone:
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
		if op.State == team.RelayCleared {
			m.moveTitle(op)
		}
		if op.State.Terminal() && op.RequestID != "" {
			m.closeRequestOfReportedOp(op)
		}
	}
	m.writeJSON(w, http.StatusOK, op)
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
