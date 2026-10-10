package teammod

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// handleRootsPut changes a live team's spawn roots. The App's alone, as max-members: the client kind is the caller's own
// claim and the remote address is logged — told, not enforced. No pdx command writes it; the skill forbids an agent to.
// Check order: 400, 404, rev_mismatch, roots_in_use.
func (m *Module) handleRootsPut(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.RootsPutRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	bad := func(why, field string) {
		m.writeJSON(w, http.StatusBadRequest, team.RootsBadRequest{Error: team.ErrBadRequest, Detail: why, Field: field})
	}
	switch {
	case strings.TrimSpace(req.Client.Kind) != "app" || strings.TrimSpace(req.Client.Label) == "":
		bad(`client must be {"kind":"app","label":…}`, "")
		return
	case strings.TrimSpace(req.TeamID) == "":
		bad("team_id is required", "")
		return
	case len(req.Roots) < 1 || len(req.Roots) > team.MaxRoots:
		bad(fmt.Sprintf("roots must hold 1 to %d entries", team.MaxRoots), "")
		return
	}
	roots, err := config.CanonicalTeamRoots(req.Roots)
	if err != nil {
		var re *config.RootError
		field, why := "", err.Error()
		if errors.As(err, &re) {
			for i, in := range req.Roots {
				if in == re.Root {
					field = fmt.Sprintf("roots[%d]", i)
					break
				}
			}
			why = map[string]string{"not_absolute": "is not an absolute path", "not_found": "does not exist",
				"not_a_directory": "is not a directory"}[re.Reason]
			why = fmt.Sprintf("%q %s", re.Root, why)
		}
		bad(why, field)
		return
	}
	roots = collapseNested(roots)
	label := strings.TrimSpace(req.Client.Label)
	res, err := m.store.SetRoots(req.TeamID, req.IfRev, roots, m.hostID())
	if err != nil {
		m.logf("[team] roots of %s: %v", req.TeamID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	switch res.Outcome {
	case RootsNoTeam:
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no live team with that id", nil)
	case RootsStaleRev:
		m.writeJSON(w, http.StatusConflict, team.RootsRevMismatch{Error: team.ErrRevMismatch, Detail: "the roots were changed by another client",
			Roots: nonNil(res.Old), RootsRev: res.Rev})
	case RootsHeld:
		m.writeJSON(w, http.StatusConflict, team.RootsRefusal{Error: team.ErrRootsInUse, Detail: "a root being removed still has a member, a starting spawn or an open adoption under it", InUse: res.InUse})
	default:
		m.logf("[team] roots of %s: %v → %v (by app %q from %s)", req.TeamID, res.Old, roots, label, r.RemoteAddr)
		m.rosterChanged()
		m.writeJSON(w, http.StatusOK, team.RootsView{TeamID: req.TeamID, Roots: roots, RootsRev: res.Rev})
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// collapseNested drops every root that lies inside another root of the list (the outer one stays); order is kept. The list
// is already duplicate-free and canonical.
func collapseNested(roots []string) []string {
	out := make([]string, 0, len(roots))
	for i, r := range roots {
		inside := false
		for j, o := range roots {
			if i != j && within(r, o) {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, r)
		}
	}
	return out
}
