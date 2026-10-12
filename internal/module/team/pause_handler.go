// internal/module/team/pause_handler.go — GET /api/team/paused (unattended quota pause spec §3.1, UP-1a).
package teammod

import (
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// handlePausedGet is GET /api/team/paused: the sessions that are paused now, earliest first. A session's ref and title are
// the registry's while it lists the session, else what team.db has (a member's row, the team's lead ref and recorded
// request); lead_ref is the ref of the team's lead ("" for a solo session, the session's own ref for a lead). A registry
// that cannot be read only costs the live values: the list is still answered from team.db.
func (m *Module) handlePausedGet(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	paused, err := m.store.PausedSessions()
	if err != nil {
		m.logf("[team] paused: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	ids := make([]string, len(paused))
	for i, p := range paused {
		ids[i] = p.SessionID
	}
	var origins map[string]team.Origin
	if len(ids) > 0 {
		if origins, err = m.origins.ResolveOriginsBySession(ids); err != nil {
			m.logf("[team] paused: registry unavailable, answering from team.db: %v", err)
			origins = nil
		}
	}
	alias, _ := m.selfHost()
	out := team.PausedView{Sessions: make([]team.PausedSession, 0, len(paused))}
	for _, p := range paused {
		ps := team.PausedSession{SessionID: p.SessionID, Role: p.Role, Causes: p.Causes, Since: p.Since, Bounced: p.Bounced}
		var t team.Team
		haveTeam := false
		if p.TeamID != "" {
			var terr error
			if t, haveTeam, terr = m.store.TeamByID(p.TeamID); terr != nil {
				m.logf("[team] paused: team %s: %v", p.TeamID, terr)
				haveTeam = false
			}
		}
		if o, ok := origins[p.SessionID]; ok {
			ps.Ref, ps.Title = o.Ref, o.Title // the registry's live values first
		}
		if ps.Ref == "" {
			ps.Ref = p.Ref // then what was recorded when the session was paused
		}
		if ps.Title == "" {
			ps.Title = p.Title
		}
		if haveTeam {
			switch p.Role {
			case team.PauseRoleLead:
				ps.LeadRef = t.LeadRef
				if ps.Ref == "" {
					ps.Ref = t.LeadRef
				}
				if ps.Title == "" {
					ps.Title = m.leadStoredSession(t, alias).Title
				}
			case team.PauseRoleMember:
				ps.LeadRef = t.LeadRef
				if ps.Ref == "" || ps.Title == "" {
					// Whatever state the row is in now (a lead's kill or release may be under way): the pause row outlives
					// it until it is released. An active row of the session wins over an older one.
					if rows, merr := m.store.MembersOf(p.TeamID); merr == nil {
						var pick *memberRow
						for i := range rows {
							if rows[i].SessionID == p.SessionID && (pick == nil || pick.State != team.MemberActive) {
								pick = &rows[i]
							}
						}
						if pick != nil {
							if ps.Ref == "" {
								ps.Ref = pick.Ref
							}
							if ps.Title == "" {
								ps.Title = pick.Title
							}
						}
					}
				}
			}
		}
		out.Sessions = append(out.Sessions, ps)
	}
	m.writeJSON(w, http.StatusOK, out)
}

// pauseEntryEnabled is the release gate of the unattended quota pause (spec §3.1, §5): while false, a begin that finds the
// chain out of quota while unattended opens the held card as it always did, and nothing is written to session_pauses or
// pause_outbox. UP-4 turned it on, with the mod that understands the 409 and the reask, and the team.pause.v1 capability: the whole
// pause ships at once. A compile-time constant on purpose — not a setting; tests close it through pauseEntryOn.
const pauseEntryEnabled = true

// pauseEntryOn is the value the code reads; tests turn the gate on through it (enablePauseEntry).
var pauseEntryOn = pauseEntryEnabled
