package cc

import "github.com/wake/purdex/internal/agent"

// ProbeProfile returns the watch parameters for the cc agent. cc renders
// its ●● header + task-description block at the top of the pane, so a
// TopLines=12 capture covers the cluster while keeping the diff cheap.
// IdleStableTicks=3 matches the watch-loop default.
//
// If a future cc UI revision invalidates these tunings, prefer DECREASING
// TopLines (less material to hash) rather than increasing it. Touching
// these values intentionally surfaces the change for review (see
// CC1 / docs/specs/2026-04-27-lights-rebuild-phase-4a-1-plan.md §2.4.1).
func (p *Provider) ProbeProfile() agent.ProbeProfile {
	return agent.ProbeProfile{
		TopLines:        12,
		IdleStableTicks: 3,
	}
}
