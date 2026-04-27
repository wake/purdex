package cc_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	cc "github.com/wake/purdex/internal/agent/cc"
)

// CC1 — TestCCProvider_ProbeProfile is a characterization test pinning the
// cc agent's ProbeProfile values. cc renders its ●● header + task-description
// block at the top of the pane; TopLines=12 captures that cluster while
// keeping captures cheap. IdleStableTicks=3 matches the watch-loop default
// (BB-stable).
//
// If a future cc UI revision invalidates these tunings, the implementer
// should TUNE TopLines DOWNWARD (less material to hash) rather than upward.
// Touching this test surfaces the change for review (Plan §2.4.1).
func TestCCProvider_ProbeProfile(t *testing.T) {
	p := cc.NewProvider(nil, nil, nil, nil)
	got := p.ProbeProfile()
	want := agent.ProbeProfile{TopLines: 12, IdleStableTicks: 3}
	if got != want {
		t.Fatalf("ProbeProfile() = %+v, want %+v", got, want)
	}
}
