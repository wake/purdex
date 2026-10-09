// internal/module/team/remote_members_handler.go
package teammod

import (
	"net/http"
	"strings"
	"unicode"

	"github.com/wake/purdex/internal/team"
)

// handleRemoteMembersGet is GET /api/team/remote-members (cross-host team spec §3.2, X2c): the sessions on THIS host
// that a lead on another host has made its members and that are still live, oldest first. The lead host's alias is
// the live config entry's for its host id ("" once it is no longer paired — exactly the rows the operator may want to
// end).
func (m *Module) handleRemoteMembersGet(w http.ResponseWriter, r *http.Request) {
	rows, err := m.store.ActiveRemoteMembers()
	if err != nil {
		m.logf("[team] remote members: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	alias := map[string]string{}
	m.core.CfgMu.RLock()
	for _, h := range m.core.Cfg.Peers.Hosts {
		if h.HostID != "" {
			alias[h.HostID] = h.Alias
		}
	}
	m.core.CfgMu.RUnlock()
	out := team.RemoteMembersResponse{Members: make([]team.RemoteMemberView, 0, len(rows))}
	for _, r := range rows {
		out.Members = append(out.Members, team.RemoteMemberView{MK: r.MK, MemberSessionID: r.MemberSessionID, Ref: r.Ref,
			Title: r.Title, Cwd: r.Cwd, TeamID: r.TeamID, TeamName: r.TeamName, LeadHostID: r.LeadHostID,
			LeadAlias: alias[r.LeadHostID], LeadAddress: r.LeadAddress, Origin: r.Origin, State: r.State, CreatedAt: r.CreatedAt})
	}
	m.writeJSON(w, http.StatusOK, out)
}

// handleRemoteMembersEnd is POST /api/team/remote-members/end {mk}: the operator on this host ends one remote
// member (active → ended{local_end}). The state change, the `ended` fact for the lead host and the notice for the
// member commit together; the role goes with the state, so the session's self relay is on again. An unknown mk is
// 404, one that is no longer live 409 not_live (with its state): the end is already true.
func (m *Module) handleRemoteMembersEnd(w http.ResponseWriter, r *http.Request) {
	var req team.RemoteMemberEndRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if req.MK == "" || len(req.MK) > maxCommandField || strings.IndexFunc(req.MK, func(r rune) bool { return !unicode.IsGraphic(r) }) >= 0 {
		// Printable only: controls and the Unicode line/paragraph separators would let an mk forge a log line.
		m.writeJSON(w, http.StatusBadRequest, team.RemoteMemberEndError{Error: team.ErrBadRequest, Detail: "mk is required (at most 256 bytes, printable characters only)"})
		return
	}
	res, err := m.store.EndRemoteMemberLocally(req.MK, m.newID(), m.newID(), m.now())
	if err != nil {
		m.logf("[team] end remote member %q: %v", req.MK, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	switch res {
	case remoteEndEnded:
		m.writeJSON(w, http.StatusOK, team.RemoteMemberEndResponse{MK: req.MK, State: remoteEnded})
	case remoteEndNotFound:
		m.writeJSON(w, http.StatusNotFound, team.RemoteMemberEndError{Error: "not_found", Detail: "no remote member with that mk"})
	default: // not live
		row, _, _ := m.store.RemoteMember(req.MK)
		m.writeJSON(w, http.StatusConflict, team.RemoteMemberEndError{Error: "not_live", Detail: "the member is no longer live", State: row.State})
	}
}
