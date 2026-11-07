package teammod

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/team"
)

// compactSubmitter is the prompt queue as this route sees it (promptq.Queue).
type compactSubmitter interface {
	SubmitCompact(ctx context.Context, sessionID string) (promptq.Result, error)
}

// handleMemberCompaction is POST /api/team/member-compaction: the caller's own local, active member only. Reset clears
// the override; Set merges its non-nil fields into the current one; neither only reads.
func (m *Module) handleMemberCompaction(w http.ResponseWriter, r *http.Request) {
	var req team.MemberCompactionRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if req.Now && (req.Reset || req.Set != nil) {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "now cannot be combined with set or reset", nil)
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
	if mr.State != team.MemberActive {
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, req.Target+" is no active member of team "+t.ID, nil)
		return
	}
	if m.isRemoteRow(mr) {
		m.writeErr(w, http.StatusConflict, team.ErrRemoteUnsupported, "a remote member's compaction settings cannot be changed from here", nil)
		return
	}
	if req.Now {
		m.compactNow(w, r, mr)
		return
	}
	fail := func(err error) {
		m.logf("[team] member compaction %s: %v", mr.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "role, switches or team.db unreadable; see the daemon log", nil)
	}
	if req.Reset || req.Set != nil {
		if req.Set != nil {
			if err := checkCompactionOverride(*req.Set); err != nil {
				m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
				return
			}
		}
		// A relay's clear copies the override to the successor in one tx, and every relay op is created under createMu: holding it
		// across the check and the write means no relay can start and clear in between and leave the write on the predecessor.
		if !func() bool {
			m.createMu.Lock()
			defer m.createMu.Unlock()
			if op, open, err := m.memberRelayOpen(mr.SessionID); err != nil {
				fail(err)
				return false
			} else if open {
				m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrRelayOpen, Detail: "member " + mr.Ref + " is relaying; change its compaction settings after the relay", Op: &op})
				return false
			}
			if _, err := m.compactionView(mr.SessionID); err != nil { // nothing is written when the answer cannot be read back
				fail(err)
				return false
			}
			var err error
			if req.Set != nil {
				err = m.store.MergeCompactionOverride(mr.SessionID, *req.Set, m.now())
			} else {
				err = m.store.SetCompactionOverride(mr.SessionID, team.CompactionOverride{}, m.now())
			}
			if err != nil {
				fail(err)
				return false
			}
			return true
		}() {
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

// memberRelayOpen is the open relay op that has sessionID as its session, or as the session it cleared into and has not finished.
func (m *Module) memberRelayOpen(sessionID string) (team.RelayOp, bool, error) {
	if op, open, err := m.store.OpenRelayOpBySession(sessionID); err != nil || open {
		return op, open, err
	}
	return m.store.OpenRelayOpByNewSession(sessionID)
}

// compactNow is the `now` of the route: the member compacts now, or when its running turn ends. It writes nothing, so it takes no
// createMu (the queue waits on the member's mod); a relay that starts right after only races a compaction, which is harmless.
func (m *Module) compactNow(w http.ResponseWriter, r *http.Request, mr memberRow) {
	op, open, err := m.memberRelayOpen(mr.SessionID)
	if err != nil {
		m.logf("[team] member compaction %s: %v", mr.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if open {
		m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrRelayOpen, Detail: "member " + mr.Ref + " is relaying; compact it after the relay", Op: &op})
		return
	}
	var q compactSubmitter
	if m.core != nil && m.core.Registry != nil {
		if svc, ok := m.core.Registry.Get(promptq.Key); ok {
			q, _ = svc.(compactSubmitter)
		}
	}
	if q == nil {
		m.writeErr(w, http.StatusConflict, team.ErrCompactFailed, "the prompt queue is not available", nil)
		return
	}
	res, err := q.SubmitCompact(r.Context(), mr.SessionID)
	switch {
	case err == nil && res.Status == promptq.Accepted && (res.Reason == "now" || res.Reason == "turn_end"):
	case err == nil && res.Status == promptq.Dropped && res.Reason == "unsupported_kind":
		m.writeErr(w, http.StatusConflict, team.ErrCompactUnsupported, "the member's mod does not take a compact job; update the mod", nil)
		return
	default:
		m.logf("[team] member compaction now %s: %+v %v", mr.SessionID, res, err)
		m.writeErr(w, http.StatusConflict, team.ErrCompactFailed, "the member's mod did not take the compact job ("+res.Status+")", nil)
		return
	}
	v, err := m.compactionView(mr.SessionID)
	if err != nil {
		m.logf("[team] member compaction %s: %v", mr.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "role, switches or team.db unreadable; see the daemon log", nil)
		return
	}
	mv := m.memberView(mr)
	m.writeJSON(w, http.StatusOK, team.MemberCompactionView{Address: mv.Address, Ref: mv.Ref, CompactionView: v, Now: res.Reason})
}
