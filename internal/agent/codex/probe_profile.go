package codex

import "github.com/wake/purdex/internal/agent"

// ProbeProfile returns the watch parameters for the codex agent. codex CLI
// is an append-only TUI: top hash signals new turns; bottom contains
// spinner + elapsed timer (variable). TopLines=10 captures the recent
// turn cluster while keeping captures cheap. IdleStableTicks=3 matches
// the watch-loop default.
//
// If a future codex UI revision invalidates these tunings, RE-SAMPLE per
// spec §2.2.3 (docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md)
// and update values + CDX1 test together. Touching these values
// intentionally surfaces the change for review.
func (p *Provider) ProbeProfile() agent.ProbeProfile {
	return agent.ProbeProfile{
		TopLines:        10,
		IdleStableTicks: 3,
	}
}
