package opencode_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/opencode"
)

// TestOpenCodeSupportedStatuses asserts opencode.Provider implements
// StatusSupporter and declares the Phase 1 status set.
func TestOpenCodeSupportedStatuses(t *testing.T) {
	var p any = opencode.NewProvider()
	ss, ok := p.(agent.StatusSupporter)
	if !ok {
		t.Fatal("opencode.Provider must implement agent.StatusSupporter")
	}
	got := ss.SupportedStatuses()
	want := map[agent.Status]bool{
		agent.StatusRunning: true,
		agent.StatusWaiting: true,
		agent.StatusIdle:    true,
		agent.StatusError:   true,
		agent.StatusClear:   true,
	}
	if len(got) != len(want) {
		t.Fatalf("SupportedStatuses len = %d, want %d (got %v)", len(got), len(want), got)
	}
	seen := make(map[agent.Status]bool, len(got))
	for _, s := range got {
		if seen[s] {
			t.Fatalf("SupportedStatuses contains duplicate %q (got %v)", s, got)
		}
		seen[s] = true
		if !want[s] {
			t.Fatalf("SupportedStatuses contains unexpected %q (got %v)", s, got)
		}
	}
}
