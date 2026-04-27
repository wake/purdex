package opencode_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	opencode "github.com/wake/purdex/internal/agent/opencode"
)

// OCD1 — TestOpenCodeProvider_ProbeProfile is a characterization test
// pinning the opencode agent's ProbeProfile values. opencode TUI is
// structurally similar to codex CLI (append-only top, bottom prompt +
// spinner). TopLines=10 + IdleStableTicks=3 mirror codex profile values;
// per-agent rationale documented in spec §2.3.
//
// If a future opencode UI revision invalidates these tunings, RE-SAMPLE
// per spec §2.3.3 (docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md)
// and update values + this test together. Touching these values
// intentionally surfaces the change for review.
func TestOpenCodeProvider_ProbeProfile(t *testing.T) {
	p := opencode.NewProvider()
	got := p.ProbeProfile()
	want := agent.ProbeProfile{TopLines: 10, IdleStableTicks: 3}
	if got != want {
		t.Fatalf("ProbeProfile() = %+v, want %+v", got, want)
	}
}
