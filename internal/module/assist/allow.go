package assistmod

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/gate"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// An allowed entry is a team (by team id) or a session (by ref), plan contract 1.

type allowBody struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Host   string `json:"host"`
	Reason string `json:"reason"`
	By     string `json:"by"`
}

type resolveErr struct {
	status int
	code   string
	detail string
}

func (e *resolveErr) Error() string { return e.detail }

func teamName(t team.Team) string {
	switch {
	case t.TeamName != "":
		return t.TeamName
	case t.TeamLabel != "":
		return t.TeamLabel
	}
	return t.ID
}

// resolveTeam turns an id, a name that exactly one live team has, or the lead's address into the team's id and a display name.
func (m *Module) resolveTeam(arg string) (string, string, error) {
	if m.teams == nil {
		return "", "", &resolveErr{http.StatusServiceUnavailable, "not_ready", "the team module is not up"}
	}
	teams, err := m.teams.ListLiveTeams()
	if err != nil {
		return "", "", &resolveErr{http.StatusServiceUnavailable, "not_ready", err.Error()}
	}
	for _, t := range teams {
		if t.ID == arg {
			return t.ID, teamName(t), nil
		}
	}
	var named []team.Team
	for _, t := range teams {
		if t.TeamName == arg || t.TeamLabel == arg {
			named = append(named, t)
		}
	}
	if len(named) > 1 {
		ids := make([]string, len(named))
		for i, t := range named {
			ids[i] = t.ID
		}
		return "", "", &resolveErr{http.StatusConflict, "ambiguous", fmt.Sprintf("%d live teams are named %q: %s", len(named), arg, strings.Join(ids, ", "))}
	}
	if len(named) == 1 {
		return named[0].ID, teamName(named[0]), nil
	}
	if m.origins != nil {
		if os, err := m.origins.ListLiveOrigins(); err == nil {
			for _, o := range os {
				if o.Address != arg && o.Ref != arg {
					continue
				}
				for _, t := range teams {
					if t.LeadSessionID == o.SessionID {
						return t.ID, teamName(t), nil
					}
				}
			}
		}
	}
	return "", "", &resolveErr{http.StatusNotFound, "no_such_team", "no live team has this id, name or lead address: " + arg}
}

// resolveRef finds the live session a ref belongs to: the one whose current ref it is, else the live one whose relay chain holds it.
func (m *Module) resolveRef(ref string) (team.Origin, bool) {
	if m.origins == nil {
		return team.Origin{}, false
	}
	if o, ok, err := m.origins.ResolveOriginByRef(ref); err == nil && ok {
		return o, true
	}
	if m.teams == nil {
		return team.Origin{}, false
	}
	sids, err := m.teams.RefSuccessors(ref)
	if err != nil {
		return team.Origin{}, false
	}
	for _, sid := range sids {
		if o, ok, err := m.origins.ResolveOriginBySession(sid); err == nil && ok {
			return o, true
		}
	}
	return team.Origin{}, false
}

// resolveEntry validates a body into the stored (kind, id, shown name).
func (m *Module) resolveEntry(b allowBody, forDeny bool) (string, string, error) {
	switch b.Kind {
	case "team":
		id, name, err := m.resolveTeam(b.ID)
		var re *resolveErr
		if forDeny && errors.As(err, &re) && re.code == "no_such_team" {
			return b.ID, b.ID, nil // an ended team is removed by the id it was stored with; any other refusal stands
		}
		return id, name, err
	case "session":
		if !ipeers.IsRef(b.ID) {
			return "", "", &resolveErr{http.StatusBadRequest, "bad_ref", "a session is named by its ref, _ and six letters or digits"}
		}
		if forDeny {
			return b.ID, b.ID, nil
		}
		o, ok := m.resolveRef(b.ID)
		if !ok {
			return "", "", &resolveErr{http.StatusNotFound, "no_such_session", "no live session has the ref " + b.ID}
		}
		name := o.Address
		if name == "" {
			name = o.Ref
		}
		return b.ID, name, nil
	}
	return "", "", &resolveErr{http.StatusBadRequest, "bad_kind", "kind is team or session"}
}

func (m *Module) allowDeny(w http.ResponseWriter, r *http.Request, allow bool) {
	if !m.requireAdmin(w, r) {
		return
	}
	var b allowBody
	if !m.decode(w, r, &b) {
		return
	}
	if allow && !validLine(b.Reason, 200) {
		m.writeErr(w, http.StatusBadRequest, "bad_reason", "a reason is required: one line, at most 200 bytes")
		return
	}
	id, name, err := m.resolveEntry(b, !allow)
	var re *resolveErr
	if errors.As(err, &re) {
		m.writeErr(w, re.status, re.code, re.detail)
		return
	}
	res, err := gate.EditAssist(m.dir, func(f *gate.AssistFile) (bool, string, error) {
		if b.Host != "" {
			if _, ok := f.Hosts[b.Host]; !ok {
				return false, "", errNoHost
			}
		}
		at := -1
		for i, a := range f.Allowed {
			if a.Kind == b.Kind && a.ID == id && a.Host == b.Host {
				at = i
			}
		}
		switch {
		case allow && at >= 0:
			return false, "already", nil
		case allow:
			f.Allowed = append(f.Allowed, gate.AssistAllow{Kind: b.Kind, ID: id, Name: name, Host: b.Host, By: byOf(b.By), Reason: b.Reason, At: m.now()})
			return true, "allowed", nil
		case at < 0:
			return false, "no_such", nil
		}
		f.Allowed = append(f.Allowed[:at:at], f.Allowed[at+1:]...)
		return true, "removed", nil
	})
	if err != nil {
		m.writeEditErr(w, err)
		return
	}
	if res == "no_such" {
		m.writeJSON(w, http.StatusNotFound, ipeers.APIError{Error: "no_such", Detail: "that entry is not on the allowed list"})
		return
	}
	m.writeJSON(w, http.StatusOK, map[string]string{"result": res, "id": id, "name": name})
}

func (m *Module) handleAllow(w http.ResponseWriter, r *http.Request) { m.allowDeny(w, r, true) }
func (m *Module) handleDeny(w http.ResponseWriter, r *http.Request)  { m.allowDeny(w, r, false) }

// entriesOf are the allowed-list entries a session can match: its team (as lead or member) and the session entries that resolve to it.
func (m *Module) entriesOf(f gate.AssistFile, sessionID string) []gate.AssistEntry {
	var out []gate.AssistEntry
	if sessionID == "" {
		return out
	}
	if m.seats != nil {
		if s, err := m.seats.SeatOf(sessionID); err == nil && (s.Role == team.SeatLead || s.Role == team.SeatMember) && s.TeamID != "" {
			out = append(out, gate.AssistEntry{Kind: "team", ID: s.TeamID})
		}
	}
	for _, a := range f.Allowed {
		if a.Kind != "session" {
			continue
		}
		if o, ok := m.resolveRef(a.ID); ok && o.SessionID == sessionID {
			out = append(out, gate.AssistEntry{Kind: "session", ID: a.ID})
		}
	}
	return out
}

type hostRow struct {
	Host    string `json:"host"`
	On      bool   `json:"on"`
	Ready   bool   `json:"ready"`
	Allowed bool   `json:"allowed"`
	Usable  bool   `json:"usable"`
	Why     string `json:"why,omitempty"`
}

// handleHosts answers `pdx gate hosts`: for a session and the repo of a directory, every assist host by Usable.
func (m *Module) handleHosts(w http.ResponseWriter, r *http.Request) {
	if !m.requireAdmin(w, r) {
		return
	}
	f, err := gate.ReadAssist(m.dir)
	if err != nil {
		m.writeErr(w, http.StatusInternalServerError, "assist_error", err.Error())
		return
	}
	repo := gate.RepoOfDir(r.URL.Query().Get("cwd"))
	entries := m.entriesOf(f, r.URL.Query().Get("session"))
	ready := m.readyCopy()
	now := m.now()
	rows := []hostRow{}
	for _, alias := range sortedHosts(f) {
		h := f.Hosts[alias]
		row := hostRow{Host: alias, On: h.On, Allowed: gate.AllowedFor(f, alias, entries)}
		if repo == "" {
			row.Why = "this directory is not in a git repository"
		} else {
			row.Usable, row.Why = gate.Usable(f, ready, now, alias, entries, repo)
			row.Ready = row.Usable || row.Why == "not allowed"
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Host < rows[j].Host })
	m.writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "hosts": rows})
}
