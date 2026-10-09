// internal/module/peers/team_caps.go
package peers

import (
	"encoding/json"
	"net/http"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
)

// teamKinds is what this daemon announces it applies. Empty until X3d: a
// route that applies a kind is what makes it announceable (spec §3.1 rule 7).
func teamKinds() []string { return []string{} }

// teamEntryFor is the live entry a host principal stands for, under the
// same binding as the other host routes (spec §6.1): present, verified, and
// still carrying the principal's host id. ok is false otherwise.
func teamEntryFor(hosts []config.PeerHost, p middleware.Principal) (config.PeerHost, bool) {
	if p.Kind != middleware.PrincipalHost || p.HostID == "" {
		return config.PeerHost{}, false
	}
	for _, h := range hosts {
		if h.Alias == p.Alias && h.HostID == p.HostID {
			return h, true
		}
	}
	return config.PeerHost{}, false
}

// teamCapsFor is the Envelope.Team value for the asking principal.
func teamCapsFor(hosts []config.PeerHost, p middleware.Principal, known bool) *ipeers.TeamCaps {
	caps := &ipeers.TeamCaps{Kinds: teamKinds()}
	if known {
		if h, ok := teamEntryFor(hosts, p); ok {
			caps.AllowTeam = h.AllowTeam
		}
	}
	return caps
}

// handleTeamRoots serves GET /api/peers/team/roots: the directories this
// host lets the asking lead host spawn in (X-U7). Only the host principal
// whose entry has AllowTeam on gets them.
func (m *Module) handleTeamRoots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	refuse := func(code string) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
	}
	p, ok := middleware.PrincipalFrom(r.Context())
	switch {
	case ok && p.Kind == middleware.PrincipalAdmin:
		refuse(ipeers.ErrAdminNotAllowed)
		return
	case !ok || p.Kind != middleware.PrincipalHost:
		refuse(ipeers.ErrHostUnverified)
		return
	}
	snap := m.configSnapshot()
	h, ok := teamEntryFor(snap.hosts, p)
	if !ok {
		refuse(ipeers.ErrHostUnverified)
		return
	}
	if !h.AllowTeam {
		refuse("host_not_allowed")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"host_id": snap.hostID,
		"roots":   append([]string{}, h.TeamRoots...),
	})
}
