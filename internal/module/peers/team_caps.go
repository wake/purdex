// internal/module/peers/team_caps.go
package peers

import (
	"encoding/json"
	"net/http"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// teamKinds is what this daemon announces it applies (spec §3.1 rule 7): a lead host sends a kind only after reading
// it here, so this list is the switch that lets a remote lead act on this host. A kind is listed once a route applies it
// and the notice it owes is delivered (X3d): adopt, release, end, lead_moved, void, kill and spawn (X4a).
func teamKinds() []string {
	return []string{team.CommandAdopt, team.CommandRelease, team.CommandKill, team.CommandSpawn, team.CommandEnd, team.CommandLeadMoved, team.CommandVoid}
}

// teamFactKinds is what this daemon applies as a lead host on POST /api/peers/team/facts (X3b-2): `ended`. registered and
// spawn_failed join with X4; moved is reserved.
func teamFactKinds() []string { return []string{team.FactEnded} }

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
	caps := &ipeers.TeamCaps{Kinds: teamKinds(), FactKinds: teamFactKinds()}
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
