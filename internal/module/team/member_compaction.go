package teammod

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// handleMemberCompaction is POST /api/team/member-compaction: the caller's own local member only. Reset clears the
// override; Set merges its non-nil fields into the current one; neither only reads.
func (m *Module) handleMemberCompaction(w http.ResponseWriter, r *http.Request) {
	var req team.MemberCompactionRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if req.Reset && req.Set != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "reset and set cannot be combined", nil)
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
		m.logf("[team] member compaction %q: %v", req.Target, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	case !found:
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, fmt.Sprintf("%q is no member of team %s", req.Target, t.ID), nil)
		return
	}
	if m.isRemoteRow(mr) {
		m.writeErr(w, http.StatusConflict, team.ErrRemoteUnsupported, "a remote member's compaction settings cannot be changed from here", nil)
		return
	}
	fail := func(err error) {
		m.logf("[team] member compaction %s: %v", mr.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "role, switches or team.db unreadable; see the daemon log", nil)
	}
	if req.Reset || req.Set != nil {
		var next team.CompactionOverride // zero value for reset: all-nil removes the row
		if s := req.Set; s != nil {
			cur, err := m.store.CompactionOverride(mr.SessionID)
			if err != nil {
				fail(err)
				return
			}
			next = cur
			if s.Auto != nil {
				next.Auto = s.Auto
			}
			if s.Threshold != nil {
				next.Threshold = s.Threshold
			}
			if s.Idle != nil {
				next.Idle = s.Idle
			}
			if err := checkCompactionOverride(next); err != nil {
				m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
				return
			}
		}
		if err := m.store.SetCompactionOverride(mr.SessionID, next, m.now()); err != nil {
			fail(err)
			return
		}
	}
	v, err := m.compactionView(mr.SessionID)
	if err != nil {
		fail(err)
		return
	}
	mv := m.memberView(mr)
	m.writeJSON(w, http.StatusOK, team.MemberCompactionView{Address: mv.Address, Ref: mv.Ref, CompactionView: v})
}
