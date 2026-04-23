package agent_test

import (
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/agent"
)

// TestDeriveSupportedStatuses_UnionAndSort builds the status union from a
// heterogeneous HookEventSpec slice (including a detail-only event with
// empty EmitsStatus) and asserts lexicographic Status-string ordering for
// deterministic output.
func TestDeriveSupportedStatuses_UnionAndSort(t *testing.T) {
	specs := []agent.HookEventSpec{
		{Name: "A", EmitsStatus: []agent.Status{agent.StatusRunning, agent.StatusWaiting}},
		{Name: "B", EmitsStatus: []agent.Status{agent.StatusIdle}},
		{Name: "C", EmitsStatus: []agent.Status{}}, // detail-only, should contribute nothing
	}
	got := agent.DeriveSupportedStatuses(specs)
	want := []agent.Status{agent.StatusIdle, agent.StatusRunning, agent.StatusWaiting}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveSupportedStatuses = %v, want %v (lex-sorted union)", got, want)
	}
}

// TestDeriveSupportedStatuses_DedupesDuplicates asserts that a Status
// appearing in multiple specs (and/or multiple times within one spec) is
// collapsed to a single occurrence in the output.
func TestDeriveSupportedStatuses_DedupesDuplicates(t *testing.T) {
	specs := []agent.HookEventSpec{
		{Name: "A", EmitsStatus: []agent.Status{agent.StatusWaiting, agent.StatusWaiting}},
		{Name: "B", EmitsStatus: []agent.Status{agent.StatusWaiting, agent.StatusIdle}},
		{Name: "C", EmitsStatus: []agent.Status{agent.StatusIdle}},
	}
	got := agent.DeriveSupportedStatuses(specs)
	want := []agent.Status{agent.StatusIdle, agent.StatusWaiting}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveSupportedStatuses = %v, want %v (deduped)", got, want)
	}
}

// TestDeriveSupportedStatuses_EmptyInput asserts the helper returns an empty
// but non-nil slice for an empty spec list, matching the defensive-copy
// convention used by Coverage (nil declarations normalise to empty slice).
func TestDeriveSupportedStatuses_EmptyInput(t *testing.T) {
	got := agent.DeriveSupportedStatuses(nil)
	if got == nil {
		t.Fatal("DeriveSupportedStatuses(nil) returned nil; want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("DeriveSupportedStatuses(nil) len = %d, want 0", len(got))
	}

	got = agent.DeriveSupportedStatuses([]agent.HookEventSpec{})
	if got == nil {
		t.Fatal("DeriveSupportedStatuses([]) returned nil; want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("DeriveSupportedStatuses([]) len = %d, want 0", len(got))
	}
}
