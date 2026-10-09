package teammod

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// handleRelease is POST /api/team/release (adopt spec D-U24-3, plan PL-1d2): the lead lets an adopted (or any
// active) member go. The member's row becomes `released`, the session is an ordinary one again (its self relay
// follows the host switch, then its own pause), and the `released` notice is owed to it through the outbox
// (PL-1d1). Nothing of the session is touched — no tmux call, no signal. A member that is not active answers
// 200 with its row as it is (idempotent); one in the middle of a relay answers 409 relay_open with the op.
func (m *Module) handleRelease(w http.ResponseWriter, r *http.Request) {
	var req team.ReleaseRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	t, ok := m.callerTeam(w, req.OriginInbox)
	if !ok {
		return
	}
	mr, found, err := m.matchMember(t, req.Target)
	switch {
	case errors.Is(err, errRegistry):
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	case err != nil:
		m.logf("[team] release %q: %v", req.Target, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	case !found:
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, fmt.Sprintf("%q is no member of team %s", req.Target, t.ID), nil)
		return
	}
	if m.isRemoteRow(mr) {
		m.releaseRemote(w, t, mr)
		return
	}
	if mr.State != team.MemberActive {
		m.writeJSON(w, http.StatusOK, m.memberView(mr))
		return
	}
	failStore := func(err error) {
		m.logf("[team] release %s: %v", mr.SpawnOp, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
	}
	released, err := m.store.ReleaseMember(mr.SpawnOp, mr.SessionID, m.now())
	if err != nil {
		failStore(err)
		return
	}
	if released {
		m.logf("[team] member %s (%s) released from team %s", mr.Ref, mr.SessionID, t.ID)
		mr.State = team.MemberReleased
		m.kickNotices()
		m.rosterChanged()
		m.writeJSON(w, http.StatusOK, m.memberView(mr))
		return
	}
	// Refused by the statement's guard: a relay is open for the session, or the row changed since it was read.
	rows, err := m.store.MembersOf(t.ID)
	if err != nil {
		failStore(err)
		return
	}
	for _, now := range rows {
		if now.SpawnOp == mr.SpawnOp && now.State != team.MemberActive {
			m.writeJSON(w, http.StatusOK, m.memberView(now)) // another call let it go, or it ended, first
			return
		}
	}
	op, open, err := m.store.OpenRelayOpBySession(mr.SessionID)
	if err != nil {
		failStore(err)
		return
	}
	e := team.APIError{Error: team.ErrRelayOpen, Detail: "member " + mr.Ref + " is relaying or about to; it was not released"}
	if open {
		e.Op = &op
	}
	m.writeJSON(w, http.StatusConflict, e)
}
