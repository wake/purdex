package teammod

// The team roster (adopt spec D-U24-5, plan PL-1f′): every live team with
// its lead and its active members, for an App that has no session inbox.
// GET /api/team/roster answers it; roster_publish.go announces it as the
// team.roster host event. This file only materializes it.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// RosterRoute is the roster's route (GET): on the general chain, admin
// token only, like the other /api/team/* routes.
const RosterRoute = "/api/team/roster"

// rosterMemberOrigin is the origin of every roster member: the one writer
// of team_members rows today is the spawn runner. PL-1f″ replaces it with
// the team_members.origin column once adopt can write another value.
const rosterMemberOrigin = team.MemberOriginSpawned

// handleRosterGet is GET /api/team/roster: the roster as the event shows it.
func (m *Module) handleRosterGet(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	roster, err := m.buildRoster()
	switch {
	case errors.Is(err, errRegistry):
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
	case err != nil:
		m.logf("[team] roster: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
	default:
		m.writeJSON(w, http.StatusOK, roster)
	}
}

// buildRoster reads the roster: live teams oldest first, each with its
// active members in join order (killed, gone and ended-team rows are out).
// Every lead and active-member session is resolved against the registry in
// ONE call (ResolveOriginsBySession), after the rows are read: a build is a
// single registry read however many sessions the teams hold. A registry
// read failure is errRegistry, not "everyone is not live": the roster would
// flip to the stored values and back on a transient error.
func (m *Module) buildRoster() (team.Roster, error) {
	teams, err := m.store.ListLiveTeamsWithLeadUsage()
	if err != nil {
		return team.Roster{}, err
	}
	active := make([][]memberRow, len(teams))
	ids := make([]string, 0, 2*len(teams))
	for i, t := range teams {
		rows, err := m.store.MembersOf(t.ID)
		if err != nil {
			return team.Roster{}, err
		}
		ids = append(ids, t.LeadSessionID)
		for _, mr := range rows {
			if mr.State == team.MemberActive {
				active[i] = append(active[i], mr)
				ids = append(ids, mr.SessionID)
			}
		}
	}
	var origins map[string]team.Origin
	if len(ids) > 0 {
		if origins, err = m.origins.ResolveOriginsBySession(ids); err != nil {
			return team.Roster{}, fmt.Errorf("%w: %v", errRegistry, err)
		}
	}
	// One narrow read of every live team's open tasks, once (not per team, not per member).
	teamIDs := make([]string, len(teams))
	for i, t := range teams {
		teamIDs[i] = t.ID
	}
	tasks, err := m.store.OpenTaskBriefs(teamIDs)
	if err != nil {
		return team.Roster{}, err
	}
	alias, _ := m.selfHost()
	out := team.Roster{Teams: make([]team.TeamRoster, 0, len(teams))}
	for i, t := range teams {
		tr := team.TeamRoster{ID: t.ID, HostID: t.HostID, TeamName: t.TeamName, TeamLabel: t.TeamLabel, CreatedAt: t.CreatedAt,
			Lead: m.rosterLead(t.Team, origins, alias, t.leadUsage), Members: []team.RosterMember{}}
		for _, mr := range active[i] {
			s := rosterSession(origins, mr.SessionID, func() team.RosterSession {
				return team.RosterSession{SessionID: mr.SessionID, Ref: mr.Ref, Address: alias + "/" + mr.Ref,
					Title: mr.Title, TmuxSession: mr.TmuxSession}
			}, mr.Ref, alias)
			s.Model, s.Effort = mr.Model, mr.Effort // what it was spawned with
			s.Context = m.sessionContext(mr.SessionID, mr.Usage)
			rm := team.RosterMember{RosterSession: s, State: mr.State, Origin: rosterMemberOrigin, JoinedAt: mr.CreatedAt}
			if mr.SpawnOp != "" { // an adopted member has no key, so no tasks
				if cur, ok := currentTaskOf(tasks[t.ID][mr.SpawnOp]); ok {
					rm.Task = &team.RosterTask{ID: team.TaskDisplayID(t.ID, cur.Seq), Subject: cur.Subject, Status: cur.Status}
				}
			}
			tr.Members = append(tr.Members, rm)
		}
		out.Teams = append(out.Teams, tr)
	}
	return out, nil
}

// rosterLead is a team's lead: the live registry entry, else teams.lead_ref
// and what the lead's request recorded (its origin, decoded with the row).
// The recorded address is the request-time one, so it is used only while
// the lead's ref is still the one it was recorded with (a relay's cleared
// moves the lead to a new ref); otherwise it is <self alias>/<lead_ref>.
// persisted is the lead reading the sweeper stored on the team row.
func (m *Module) rosterLead(t team.Team, origins map[string]team.Origin, alias string, persisted *team.MemberContext) team.RosterSession {
	s := rosterSession(origins, t.LeadSessionID, func() team.RosterSession { return m.leadStoredSession(t, alias) }, t.LeadRef, alias)
	s.Context = m.sessionContext(t.LeadSessionID, persisted) // a lead has no spawn model / effort
	return s
}

// leadStoredSession is a team's lead as the database knows it, for a lead the
// registry does not list: teams.lead_ref, and what the lead's request
// recorded. The recorded address is the request-time one, so it is used only
// while the lead's ref is still the one it was recorded with; otherwise it is
// <alias>/<lead_ref>. Shared by the roster and by the report routes.
func (m *Module) leadStoredSession(t team.Team, alias string) team.RosterSession {
	s := team.RosterSession{SessionID: t.LeadSessionID, Ref: t.LeadRef, Address: alias + "/" + t.LeadRef}
	req, ok, err := m.store.Get(t.RequestID)
	if err != nil || !ok {
		if err != nil {
			m.logf("[team] roster: request row of team %s: %v", t.ID, err)
		}
		return s
	}
	if req.Origin.Ref == t.LeadRef && req.Origin.Address != "" {
		s.Address = req.Origin.Address
	}
	s.Title, s.Name, s.TmuxSession = req.Origin.Title, req.Origin.Name, tmuxName(req.Origin.Tmux)
	return s
}

// rosterSession is one session of the roster: the registry's origin when it
// lists the session (live:true), else stored() (live:false). ref and alias
// fill an address the registry left empty.
func rosterSession(origins map[string]team.Origin, sessionID string, stored func() team.RosterSession, ref, alias string) team.RosterSession {
	o, ok := origins[sessionID]
	if !ok {
		return stored()
	}
	s := team.RosterSession{SessionID: sessionID, Ref: o.Ref, Address: o.Address, Title: o.Title,
		Name: o.Name, TmuxSession: tmuxName(o.Tmux), Live: true}
	if s.Ref == "" {
		s.Ref = ref
	}
	if s.Address == "" {
		s.Address = alias + "/" + s.Ref
	}
	return s
}

// tmuxName is the tmux session NAME of an Origin.Tmux ("<session>:@<win>.%<pane>"):
// what precedes the first colon; "" when the session is not in tmux.
func tmuxName(origin string) string {
	name, _, _ := strings.Cut(origin, ":")
	return name
}
