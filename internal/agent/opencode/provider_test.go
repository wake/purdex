package opencode_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/opencode"
)

// TestOpenCodeSupportedStatuses_DerivesFromEvents asserts the post-Commit-4
// invariant that SupportedStatuses is computed from Events().EmitsStatus
// union rather than a hard-coded literal.
func TestOpenCodeSupportedStatuses_DerivesFromEvents(t *testing.T) {
	p := opencode.NewProvider()
	ss := any(p).(agent.StatusSupporter)
	got := ss.SupportedStatuses()

	gotSet := make(map[agent.Status]bool, len(got))
	for _, s := range got {
		gotSet[s] = true
	}
	wantSet := make(map[agent.Status]bool)
	for _, e := range p.Events() {
		for _, s := range e.EmitsStatus {
			wantSet[s] = true
		}
	}
	if len(gotSet) != len(wantSet) {
		t.Fatalf("SupportedStatuses=%v, events union=%v (len mismatch)", got, wantSet)
	}
	for s := range wantSet {
		if !gotSet[s] {
			t.Errorf("SupportedStatuses missing %q (from events union)", s)
		}
	}
	for s := range gotSet {
		if !wantSet[s] {
			t.Errorf("SupportedStatuses contains %q not in events union", s)
		}
	}
}

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
