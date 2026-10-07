package teammod

// GET /api/team (spec §7.3, U20 (e)): the lead's team with what each
// member runs.

import (
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// handleTeam is GET /api/team?origin_inbox=<inbox>: the caller's live team
// and every member of it, in any state, oldest first.
func (m *Module) handleTeam(w http.ResponseWriter, r *http.Request) {
	t, ok := m.callerTeam(w, r.URL.Query().Get("origin_inbox"))
	if !ok {
		return
	}
	rows, err := m.store.MembersOf(t.ID)
	if err != nil {
		m.logf("[team] team %s: %v", t.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	v := team.TeamView{Team: t, Members: make([]team.Member, 0, len(rows))}
	for _, mr := range rows {
		v.Members = append(v.Members, m.memberView(mr))
	}
	m.writeJSON(w, http.StatusOK, v)
}

// callerTeam is the live team the inbox's session leads; false means an
// error was written: 503 (registry), 400 origin_unknown, 409 not_lead.
func (m *Module) callerTeam(w http.ResponseWriter, inbox string) (team.Team, bool) {
	origin, ok, err := m.origins.ResolveOrigin(inbox)
	if err != nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return team.Team{}, false
	}
	if !ok {
		m.writeErr(w, http.StatusBadRequest, team.ErrOriginUnknown, "origin_inbox is not a live Claude Code session on this host", nil)
		return team.Team{}, false
	}
	t, found, err := m.store.LiveTeamByLead(origin.SessionID)
	if err != nil {
		m.logf("[team] team of %s: %v", origin.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return team.Team{}, false
	}
	if !found {
		m.writeErr(w, http.StatusConflict, team.ErrNotLead, "this session leads no live team", nil)
		return team.Team{}, false
	}
	return t, true
}

// memberView is a member row in the wire's shape. Context is the agent
// module's live reading, else the one the sweeper persisted (spec §8.5),
// else absent: so its model and effort stay blank until the member's first
// statusline (U20 (e)). An active member's address is the registry's; any
// other's, or one the registry does not list, is <self alias>/<ref>.
func (m *Module) memberView(mr memberRow) team.Member {
	alias, _ := m.selfHost()
	v := team.Member{SessionID: mr.SessionID, Ref: mr.Ref, Address: alias + "/" + mr.Ref, TeamID: mr.TeamID,
		HostID: mr.HostID, Title: mr.Title, Cwd: mr.Cwd, TmuxSession: mr.TmuxSession, State: mr.State,
		Model: mr.Model, Effort: mr.Effort, Context: mr.Usage, SpawnOp: mr.SpawnOp, CreatedAt: mr.CreatedAt}
	if mr.State == team.MemberActive {
		if o, ok, err := m.origins.ResolveOriginBySession(mr.SessionID); err == nil && ok {
			v.Address = o.Address
		}
	}
	if m.usage != nil {
		if u, ok := m.usage.ContextUsage(mr.SessionID); ok {
			c := contextOf(u)
			v.Context = &c
		}
	}
	return v
}

func (m *Module) selfHost() (alias, hostID string) {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return m.core.Cfg.PeerAlias(), m.core.Cfg.HostID
}
