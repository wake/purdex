package teammod

import (
	"errors"
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// handleRelayDeny is POST /api/team/relays/deny (RT-2b T21, S-12): the lead declines the open ask of one of its members. The ask
// closes `withdrawn` / `denied` (no new state: the CHECK stays), under createMu and askMu as the accept and the compaction's
// withdrawal are. A member on another host is relay_unsupported (S-8); no open ask is 409 no_open_ask.
func (m *Module) handleRelayDeny(w http.ResponseWriter, r *http.Request) {
	var req team.RelayDenyRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	t, ok := m.callerTeam(w, req.OriginInbox)
	if !ok {
		return
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	fail := func(err error) {
		m.logf("[team] relay deny: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
	}
	mr, found, err := m.matchMember(t, req.Target)
	switch {
	case errors.Is(err, errRegistry):
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	case err != nil:
		fail(err)
		return
	}
	if !found || mr.State != team.MemberActive {
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, req.Target+" is no active member of team "+t.ID, nil)
		return
	}
	if m.isRemoteRow(mr) {
		m.writeErr(w, http.StatusConflict, team.ErrRelayUnsupported, "the member lives on another host; denying its ask is not supported yet", nil)
		return
	}
	m.askMu.Lock() // behind an ask notice that is on the wire (see sendAskNotice)
	ask, closed, err := m.store.DenyRelayAsk(mr.SessionID, m.now())
	m.askMu.Unlock()
	if err != nil {
		fail(err)
		return
	}
	if !closed {
		m.writeErr(w, http.StatusConflict, team.ErrNoOpenAsk, mr.Ref+" has no open relay ask", nil)
		return
	}
	m.logf("[team] relay ask %s of member %s denied by its lead", ask.ID, mr.Ref)
	m.askAnswered()
	m.writeJSON(w, http.StatusOK, team.RelayDenyResponse{AskID: ask.ID})
}
