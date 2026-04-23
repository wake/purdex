package cc

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
)

// expectedCCEventNames lists the 9 hook events cc's installer wires. Kept as
// a fixed slice to guard against silent drift in Events() ordering/contents.
// Order matches plan §1.4 cc table, which mirrors the installer's emit order.
var expectedCCEventNames = []string{
	"SessionStart",
	"UserPromptSubmit",
	"SubagentStart",
	"SubagentStop",
	"Stop",
	"StopFailure",
	"Notification",
	"PermissionRequest",
	"SessionEnd",
}

// TestCCEvents_Count locks the declared event count at 9 (plan §1.4).
func TestCCEvents_Count(t *testing.T) {
	p := NewProvider(nil, nil, nil, nil)
	events := p.Events()
	if len(events) != 9 {
		t.Fatalf("cc Events count = %d, want 9 (plan §1.4)", len(events))
	}
}

// TestCCEvents_NamesMatchExpected asserts the Event Name set matches §1.4.
func TestCCEvents_NamesMatchExpected(t *testing.T) {
	p := NewProvider(nil, nil, nil, nil)
	events := p.Events()

	got := make(map[string]bool, len(events))
	for _, e := range events {
		if got[e.Name] {
			t.Errorf("cc Events contains duplicate Name %q", e.Name)
		}
		got[e.Name] = true
	}
	want := make(map[string]bool, len(expectedCCEventNames))
	for _, n := range expectedCCEventNames {
		want[n] = true
	}
	for n := range want {
		if !got[n] {
			t.Errorf("cc Events missing required Name %q", n)
		}
	}
	for n := range got {
		if !want[n] {
			t.Errorf("cc Events contains unexpected Name %q", n)
		}
	}
}

// TestCCEvents_EmitsStatusForNotification asserts cc Notification is declared
// to emit the Waiting/Idle union covering its polymorphic sub-branches
// (permission_prompt / elicitation_dialog → Waiting; idle_prompt /
// auth_success → Idle) per plan §1.4.
func TestCCEvents_EmitsStatusForNotification(t *testing.T) {
	p := NewProvider(nil, nil, nil, nil)
	var spec *agent.HookEventSpec
	for i := range p.Events() {
		if p.Events()[i].Name == "Notification" {
			s := p.Events()[i]
			spec = &s
			break
		}
	}
	if spec == nil {
		t.Fatal("cc Events missing Notification entry")
	}
	got := make(map[agent.Status]bool, len(spec.EmitsStatus))
	for _, s := range spec.EmitsStatus {
		got[s] = true
	}
	for _, want := range []agent.Status{agent.StatusWaiting, agent.StatusIdle} {
		if !got[want] {
			t.Errorf("cc Notification EmitsStatus missing %q (got %v)", want, spec.EmitsStatus)
		}
	}
	if len(got) != 2 {
		t.Errorf("cc Notification EmitsStatus = %v, want exactly {Waiting, Idle}", spec.EmitsStatus)
	}
}

// TestCCEvents_DetailOnlyHaveEmptyEmitsStatus enforces detail-only events
// (SubagentStart/Stop) declare EmitsStatus as an empty slice — not nil — so
// drift tooling and Inspector UI can distinguish "explicitly empty" from
// "unknown".
func TestCCEvents_DetailOnlyHaveEmptyEmitsStatus(t *testing.T) {
	p := NewProvider(nil, nil, nil, nil)
	for _, e := range p.Events() {
		if e.Name != "SubagentStart" && e.Name != "SubagentStop" {
			continue
		}
		if e.EmitsStatus == nil {
			t.Errorf("cc %s EmitsStatus is nil; want non-nil empty slice", e.Name)
		}
		if len(e.EmitsStatus) != 0 {
			t.Errorf("cc %s EmitsStatus = %v, want empty", e.Name, e.EmitsStatus)
		}
	}
}

// TestCCEvents_DescriptionsNonEmpty enforces every event has a short English
// human-readable description without emoji (plan §1.1).
func TestCCEvents_DescriptionsNonEmpty(t *testing.T) {
	p := NewProvider(nil, nil, nil, nil)
	for _, e := range p.Events() {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("cc %s Description is empty", e.Name)
			continue
		}
		for _, r := range e.Description {
			if r >= 0x1F300 && r <= 0x1FAFF {
				t.Errorf("cc %s Description contains emoji rune %U: %q", e.Name, r, e.Description)
				break
			}
		}
	}
}

// TestCCEvents_FreshSliceDefensiveCopy guards the defensive-copy convention:
// each Events() call must return an independent backing array so consumers
// cannot mutate provider-internal state.
func TestCCEvents_FreshSliceDefensiveCopy(t *testing.T) {
	p := NewProvider(nil, nil, nil, nil)
	first := p.Events()
	if len(first) == 0 {
		t.Fatal("cc Events returned empty slice")
	}
	first[0].Name = "__mutated__"
	if len(first[0].EmitsStatus) > 0 {
		first[0].EmitsStatus[0] = agent.Status("__mutated__")
	}
	second := p.Events()
	if second[0].Name == "__mutated__" {
		t.Errorf("cc Events shares backing array; second call first Name mutated to %q", second[0].Name)
	}
	if len(second[0].EmitsStatus) > 0 && second[0].EmitsStatus[0] == "__mutated__" {
		t.Errorf("cc Events shares EmitsStatus backing array across calls")
	}
}
