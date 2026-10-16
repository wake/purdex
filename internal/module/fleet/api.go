package fleetmod

import (
	"encoding/json"
	"net/http"
	"time"

	leaf "github.com/wake/purdex/internal/fleet"
)

// FleetAnswer is GET /api/fleet (spec §6). Which fields are present depends on the role.
type FleetAnswer struct {
	Role    string     `json:"role"`
	Term    *int64     `json:"term,omitempty"`
	Primary string     `json:"primary,omitempty"` // standby: the primary's alias
	Reason  string     `json:"reason,omitempty"`  // off: why
	At      *time.Time `json:"at,omitempty"`
	Hosts   []HostJSON `json:"hosts,omitempty"`
}

// Routes registers the module's measuring-side routes. GET /api/fleet is open to device tokens (cmd/pdx/device_scope.go);
// the alias list is not in that list, so only the admin reaches it.
func (m *Measure) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fleet", m.handleFleet)
	mux.HandleFunc("GET /api/fleet/ssh-aliases", m.handleAliases)
}

// Answer is the body of GET /api/fleet for the role as it is now.
func (m *Measure) Answer() FleetAnswer {
	if m.d.Role == nil {
		return FleetAnswer{Role: leaf.RoleOff, Reason: "no role engine"}
	}
	st := m.d.Role.State()
	term := st.Term
	switch st.Role {
	case leaf.RolePrimary:
		at := m.clock.Now()
		return FleetAnswer{Role: st.Role, Term: &term, At: &at, Hosts: m.hostList()}
	case leaf.RoleStandby:
		return FleetAnswer{Role: st.Role, Term: &term, Primary: st.Primary}
	}
	return FleetAnswer{Role: leaf.RoleOff, Reason: st.Reason}
}

// hostList is every host in the fleet order, with its slow part (the queue summary, never the gate's own status).
func (m *Measure) hostList() []HostJSON {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]HostJSON, 0, len(m.order))
	for _, key := range m.order {
		h := m.hosts[key]
		status, code, lastOK := viewOK(h)
		out = append(out, HostJSON{
			ID: m.idOf(h), Kind: h.kind, Alias: h.fleetAlias, Name: h.name, Icon: h.icon, Colors: h.colors,
			IconWeight: h.iconWeight, OS: h.os, Status: status, Error: code, LastOKAt: lastOK, Fast: h.fast, Slow: h.slow,
		})
	}
	return out
}

func (m *Measure) handleFleet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, m.Answer())
}

func (m *Measure) handleAliases(w http.ResponseWriter, r *http.Request) {
	aliases, err := leaf.Aliases(m.d.Home)
	if err != nil {
		http.Error(w, "the SSH config could not be read", http.StatusInternalServerError)
		return
	}
	if aliases == nil {
		aliases = []string{}
	}
	writeJSON(w, http.StatusOK, struct {
		Aliases []string `json:"aliases"`
	}{aliases})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
