package opencode

import "github.com/wake/purdex/internal/agent"

// ProbeProfile returns the watch parameters for the opencode agent.
// opencode TUI is structurally similar to codex CLI: append-only top with
// new turns scrolling content; bottom prompt + spinner / status. TopLines=10
// captures the recent turn cluster while keeping captures cheap.
// IdleStableTicks=3 matches the watch-loop default. Profile values mirror
// codex (no observed structural divergence justifying separate tunings).
//
// If a future opencode UI revision invalidates these tunings, RE-SAMPLE
// per spec §2.3.3 (docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md)
// and update values + OCD1 test together. Touching these values
// intentionally surfaces the change for review.
func (p *Provider) ProbeProfile() agent.ProbeProfile {
	return agent.ProbeProfile{
		TopLines:        10,
		IdleStableTicks: 3,
	}
}
