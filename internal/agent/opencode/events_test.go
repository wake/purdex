package opencode_test

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/opencode"
)

// expectedOpenCodeEventNames lists the 8 hook events opencode's plugin wires
// (plan §1.4). Ordering matches the plan table.
var expectedOpenCodeEventNames = []string{
	"SessionStart",
	"UserPromptSubmit",
	"SubagentStart",
	"SubagentStop",
	"PermissionRequest",
	"Stop",
	"StopFailure",
	"SessionEnd",
}

// TestOpenCodeEvents_Count locks the declared event count at 8 (plan §1.4).
func TestOpenCodeEvents_Count(t *testing.T) {
	p := opencode.NewProvider()
	events := p.Events()
	if len(events) != 8 {
		t.Fatalf("opencode Events count = %d, want 8 (plan §1.4)", len(events))
	}
}

// TestOpenCodeEvents_NamesMatchExpected asserts the Event Name set.
func TestOpenCodeEvents_NamesMatchExpected(t *testing.T) {
	p := opencode.NewProvider()
	events := p.Events()

	got := make(map[string]bool, len(events))
	for _, e := range events {
		if got[e.Name] {
			t.Errorf("opencode Events contains duplicate Name %q", e.Name)
		}
		got[e.Name] = true
	}
	want := make(map[string]bool, len(expectedOpenCodeEventNames))
	for _, n := range expectedOpenCodeEventNames {
		want[n] = true
	}
	for n := range want {
		if !got[n] {
			t.Errorf("opencode Events missing required Name %q", n)
		}
	}
	for n := range got {
		if !want[n] {
			t.Errorf("opencode Events contains unexpected Name %q", n)
		}
	}
}

// TestOpenCodeEvents_DetailOnlyHaveEmptyEmitsStatus guards the empty-slice
// convention for detail-only SubagentStart/Stop.
func TestOpenCodeEvents_DetailOnlyHaveEmptyEmitsStatus(t *testing.T) {
	p := opencode.NewProvider()
	for _, e := range p.Events() {
		if e.Name != "SubagentStart" && e.Name != "SubagentStop" {
			continue
		}
		if e.EmitsStatus == nil {
			t.Errorf("opencode %s EmitsStatus is nil; want non-nil empty slice", e.Name)
		}
		if len(e.EmitsStatus) != 0 {
			t.Errorf("opencode %s EmitsStatus = %v, want empty", e.Name, e.EmitsStatus)
		}
	}
}

// TestOpenCodeEvents_DescriptionsNonEmpty asserts every Description is
// non-empty and emoji-free.
func TestOpenCodeEvents_DescriptionsNonEmpty(t *testing.T) {
	p := opencode.NewProvider()
	for _, e := range p.Events() {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("opencode %s Description is empty", e.Name)
			continue
		}
		for _, r := range e.Description {
			if r >= 0x1F300 && r <= 0x1FAFF {
				t.Errorf("opencode %s Description contains emoji rune %U: %q", e.Name, r, e.Description)
				break
			}
		}
	}
}

// TestOpenCodeEvents_FreshSliceDefensiveCopy guards the defensive-copy
// convention.
func TestOpenCodeEvents_FreshSliceDefensiveCopy(t *testing.T) {
	p := opencode.NewProvider()
	first := p.Events()
	if len(first) == 0 {
		t.Fatal("opencode Events returned empty slice")
	}
	first[0].Name = "__mutated__"
	if len(first[0].EmitsStatus) > 0 {
		first[0].EmitsStatus[0] = agent.Status("__mutated__")
	}
	second := p.Events()
	if second[0].Name == "__mutated__" {
		t.Errorf("opencode Events shares backing array; second call first Name mutated to %q", second[0].Name)
	}
	if len(second[0].EmitsStatus) > 0 && second[0].EmitsStatus[0] == "__mutated__" {
		t.Errorf("opencode Events shares EmitsStatus backing array across calls")
	}
}
