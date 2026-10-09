package teammod

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// handleAppearancePut edits a live team's name, short label and colour (TR-1). All fields every time: the App sends the
// roster's current values with its changes, so a missing one is refused rather than read as "clear it". The client is
// recorded, not checked.
func (m *Module) handleAppearancePut(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.AppearancePutRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	bad := func(why string) { m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, why, nil) }
	if strings.TrimSpace(req.TeamID) == "" {
		bad("team_id is required")
		return
	}
	if req.TeamName == nil {
		bad("team_name is required (a string)")
		return
	}
	if req.TeamLabel == nil {
		bad("team_label is required (a string; empty derives it from the name)")
		return
	}
	color, err := parseTeamColor(req.TeamColor)
	if err != nil {
		bad(err.Error())
		return
	}
	name, err := team.NormaliseTeamName(*req.TeamName)
	if err != nil {
		bad("team_name: " + err.Error())
		return
	}
	label, err := team.NormaliseTeamLabel(*req.TeamLabel)
	if err != nil {
		bad("team_label: " + err.Error())
		return
	}
	if label == "" {
		label = team.DeriveTeamLabel(name) // the creation rule: empty means derived, not cleared
	}
	res, err := m.store.SetAppearance(req.TeamID, name, label, color)
	if err != nil {
		m.logf("[team] appearance of %s: %v", req.TeamID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	switch res.Outcome {
	case AppearanceNoTeam:
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no team with that id", nil)
	case AppearanceNotLive:
		m.writeErr(w, http.StatusConflict, team.ErrNotLive, "the team has ended", nil)
	default:
		m.logf("[team] appearance of team %s: name %q -> %q, label %q -> %q, colour %s -> %s by client %q from %s",
			req.TeamID, res.OldName, name, res.OldLabel, label, colourText(res.OldColor), colourText(color), clientKind(req.Client), r.RemoteAddr)
		m.rosterChanged()
		m.writeJSON(w, http.StatusOK, team.AppearanceView{TeamID: req.TeamID, TeamName: name, TeamLabel: label, TeamColor: color})
	}
}

// parseTeamColor reads team_color: absent or anything but an integer 0–7 or null is an error naming the field.
func parseTeamColor(raw json.RawMessage) (*int, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("team_color is required (0-%d, or null for automatic)", team.MaxTeamColor)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	n, err := strconv.Atoi(string(bytes.TrimSpace(raw)))
	if err != nil || n < 0 || n > team.MaxTeamColor {
		return nil, fmt.Errorf("team_color must be an integer from 0 to %d, or null", team.MaxTeamColor)
	}
	return &n, nil
}

func colourText(c *int) string {
	if c == nil {
		return "auto"
	}
	return strconv.Itoa(*c)
}

func clientKind(c team.Client) string {
	if k := strings.TrimSpace(c.Kind); k != "" {
		return k
	}
	return "unknown"
}
