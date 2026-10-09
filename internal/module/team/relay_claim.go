package teammod

import (
	"net/http"
	"strings"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// claim, seen and the op long-poll (plan v3 P6-2b-2, branch A). The member's mod calls claim when it takes the op
// (after its running turn, P6-6) and seen as soon as it has read the control message. Neither locks: the relay flag is
// the mod's to raise at the write turn (P6-3c).

// memberOpOf reads the op a claim or seen names and checks that the caller is its TARGET session. false means an
// error was written: 404 not_found, 409 bad_transition (not a member op), 409 not_your_op.
func (m *Module) memberOpOf(w http.ResponseWriter, id, sessionID string) (team.RelayOp, bool) {
	if strings.TrimSpace(sessionID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required", nil)
		return team.RelayOp{}, false
	}
	op, ok, err := m.store.GetRelayOp(id)
	if err != nil {
		m.logf("[team] relay op %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return team.RelayOp{}, false
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such relay op", nil)
		return team.RelayOp{}, false
	}
	if op.Kind != team.RelayKindMember {
		m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrBadTransition, Detail: "only a member op is claimed or seen this way", Op: &op})
		return team.RelayOp{}, false
	}
	if op.SessionID != sessionID {
		m.writeErr(w, http.StatusConflict, team.ErrNotYourOp, "this op is another session's", nil)
		return team.RelayOp{}, false
	}
	return op, true
}

// handleRelayClaim is POST /api/relay/ops/{id}/claim: requested → claimed, by the target session only (a
// compare-and-set in ReportRelay). A repeat on a claimed op answers the same 200. The answer carries the lead the
// member reports to: its live address, else <self alias>/<ref>.
func (m *Module) handleRelayClaim(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	id := r.PathValue("id")
	var req team.RelayClaimRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if _, ok := m.memberOpOf(w, id, req.SessionID); !ok {
		return
	}
	op, res, err := m.store.ReportRelay(id, RelayReport{State: team.RelayClaimed, At: m.now()})
	if err != nil {
		m.logf("[team] claim %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if res == ReportBadTransition {
		m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrBadTransition, Detail: "state " + string(op.State) + " does not lead to claimed", Op: &op})
		return
	}
	if res == ReportApplied {
		m.logf("[team] relay op %s → claimed", id)
	}
	m.writeJSON(w, http.StatusOK, team.RelayClaimResponse{Op: op, Lead: m.leadOf(op)})
}

// leadOf is who leads the op's team now, or nil when the team is gone.
func (m *Module) leadOf(op team.RelayOp) *team.RelayLead {
	t, ok, err := m.store.TeamByID(op.TeamID)
	if err != nil || !ok {
		return nil
	}
	ref := ipeers.RefID(t.LeadSessionID)
	alias, _ := m.selfHost()
	address := alias + "/" + ref
	if o, live, err := m.origins.ResolveOriginBySession(t.LeadSessionID); err == nil && live && o.Address != "" {
		address = o.Address
	}
	return &team.RelayLead{Address: address, Ref: ref, TeamID: t.ID}
}

// handleRelaySeen is POST /api/relay/ops/{id}/seen: the target's mod saw the control message. It sets seen_at once
// while the op is requested and answers the op either way; it moves no state and not updated_at.
func (m *Module) handleRelaySeen(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	id := r.PathValue("id")
	var req team.RelaySeenRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if _, ok := m.memberOpOf(w, id, req.SessionID); !ok {
		return
	}
	op, _, err := m.store.MarkRelaySeen(id, m.now())
	if err != nil {
		m.logf("[team] seen %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, op)
}

// handleRelayOp is GET /api/relay/ops/{id}?wait=N: the op as it is, and — while it is not terminal and wait > 0 —
// held until a committed change of the op wakes it (store.opChanged), the timer, the client or Stop, then read again.
func (m *Module) handleRelayOp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wait, err := pollWait(r.URL.Query().Get("wait"))
	if err != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
		return
	}
	ch := m.addWaiter(id) // before the read: a change between the read and the wait still wakes us
	defer m.removeWaiter(id, ch)
	op, ok, err := m.store.GetRelayOp(id)
	if err == nil && !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such relay op", nil)
		return
	}
	if err == nil && wait > 0 && !op.State.Terminal() {
		timer := time.NewTimer(time.Duration(wait) * time.Second)
		defer timer.Stop()
		select {
		case <-ch:
		case <-timer.C:
		case <-r.Context().Done():
		case <-m.stopCtx.Done():
		}
		if op, ok, err = m.store.GetRelayOp(id); err == nil && !ok {
			err = ErrNoSuchRelayOp
		}
	}
	if err != nil {
		m.logf("[team] relay op %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, op)
}
