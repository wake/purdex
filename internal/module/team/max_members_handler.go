package teammod

import (
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// handleMaxMembersPut changes a live team's member limit. The App's alone: the client kind is the caller's own claim and
// the remote address is logged — told, not enforced, as the unattended switch and the relay quota (the App and pdx share
// one host token). No pdx command writes it; the skill forbids an agent to.
func (m *Module) handleMaxMembersPut(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.MaxMembersPutRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	bad := func(why string) { m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, why, nil) }
	switch {
	case strings.TrimSpace(req.Client.Kind) != "app" || strings.TrimSpace(req.Client.Label) == "":
		bad(`client must be {"kind":"app","label":…}`)
		return
	case strings.TrimSpace(req.TeamID) == "":
		bad("team_id is required")
		return
	case req.MaxMembers < 1 || req.MaxMembers > team.MaxMaxMembers:
		bad("max_members must be an integer from 1 to 8")
		return
	}
	label := strings.TrimSpace(req.Client.Label)
	res, err := m.store.SetMaxMembers(req.TeamID, req.MaxMembers)
	if err != nil {
		m.logf("[team] max members of %s: %v", req.TeamID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	switch res.Outcome {
	case MaxNoTeam:
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no live team with that id", nil)
	case MaxBelowInUse:
		m.writeJSON(w, http.StatusConflict, team.MaxMembersRefusal{Error: team.ErrMaxBelowInUse, Detail: "max_members is below the places in use", InUse: res.InUse})
	default:
		m.logf("[team] max members of team %s set to %d (in use %d) by app %q from %s", req.TeamID, req.MaxMembers, res.InUse, label, r.RemoteAddr)
		m.rosterChanged()
		m.writeJSON(w, http.StatusOK, team.MaxMembersView{TeamID: req.TeamID, MaxMembers: req.MaxMembers, InUse: res.InUse})
	}
}
