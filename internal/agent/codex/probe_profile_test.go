package codex_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	codex "github.com/wake/purdex/internal/agent/codex"
)

// CDX1 — TestCodexProvider_ProbeProfile is a characterization test pinning
// the codex agent's ProbeProfile values. codex CLI is an append-only TUI:
// top hash signals new turns; bottom contains spinner + elapsed timer
// (variable). TopLines=10 captures the recent turn cluster while keeping
// captures cheap. IdleStableTicks=3 matches the watch-loop default
// (BB-stable).
//
// If a future codex UI revision invalidates these tunings, RE-SAMPLE per
// spec §2.2.3 (docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md)
// and update values + this test together. Touching these values
// intentionally surfaces the change for review.
func TestCodexProvider_ProbeProfile(t *testing.T) {
	p := codex.NewProvider()
	got := p.ProbeProfile()
	want := agent.ProbeProfile{TopLines: 10, IdleStableTicks: 3}
	if got != want {
		t.Fatalf("ProbeProfile() = %+v, want %+v", got, want)
	}
}
