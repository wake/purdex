package cc

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
)

// expectedCCInstallableEventNames lists the hook events cc's installer wires.
// Keep this stable while the upstream catalog grows around it.
var expectedCCInstallableEventNames = []string{
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

var expectedCCEventNames = expectedCCInstallableEventNames

// expectedCCCatalogHandling is pinned to the Claude Code hooks reference,
// fetched 2026-04-26 from https://docs.anthropic.com/en/docs/claude-code/hooks.
var expectedCCCatalogHandling = map[string]agent.HookHandling{
	"SessionStart":        agent.HookHandlingStatus,
	"UserPromptSubmit":    agent.HookHandlingStatus,
	"UserPromptExpansion": agent.HookHandlingUnsupported,
	"PreToolUse":          agent.HookHandlingUnsupported,
	"PermissionRequest":   agent.HookHandlingStatus,
	"PermissionDenied":    agent.HookHandlingIgnored,
	"PostToolUse":         agent.HookHandlingIgnored,
	"PostToolUseFailure":  agent.HookHandlingIgnored,
	"PostToolBatch":       agent.HookHandlingUnsupported,
	"Notification":        agent.HookHandlingStatus,
	"SubagentStart":       agent.HookHandlingDetail,
	"SubagentStop":        agent.HookHandlingDetail,
	"TaskCreated":         agent.HookHandlingIgnored,
	"TaskCompleted":       agent.HookHandlingIgnored,
	"Stop":                agent.HookHandlingStatus,
	"StopFailure":         agent.HookHandlingStatus,
	"TeammateIdle":        agent.HookHandlingIgnored,
	"InstructionsLoaded":  agent.HookHandlingIgnored,
	"ConfigChange":        agent.HookHandlingIgnored,
	"CwdChanged":          agent.HookHandlingIgnored,
	"FileChanged":         agent.HookHandlingUnsupported,
	"WorktreeCreate":      agent.HookHandlingUnsupported,
	"WorktreeRemove":      agent.HookHandlingUnsupported,
	"PreCompact":          agent.HookHandlingUnsupported,
	"PostCompact":         agent.HookHandlingIgnored,
	"Elicitation":         agent.HookHandlingIgnored,
	"ElicitationResult":   agent.HookHandlingIgnored,
	"SessionEnd":          agent.HookHandlingStatus,
}

// TestCCEvents_Count locks the classified upstream event count.
func TestCCEvents_Count(t *testing.T) {
	p := NewProvider(nil, nil, nil, nil)
	events := p.Events()
	if len(events) != len(expectedCCCatalogHandling) {
		t.Fatalf("cc Events count = %d, want %d", len(events), len(expectedCCCatalogHandling))
	}
}

// TestCCEvents_NamesMatchExpected asserts the Event Name set matches the
// version-pinned upstream catalog.
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
	want := make(map[string]bool, len(expectedCCCatalogHandling))
	for n := range expectedCCCatalogHandling {
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

func TestCCEvents_ClassifyKnownUpstreamHooks(t *testing.T) {
	p := NewProvider(nil, nil, nil, nil)
	for _, e := range p.Events() {
		want, ok := expectedCCCatalogHandling[e.Name]
		if !ok {
			continue
		}
		if got := agent.EffectiveHookHandling(e); got != want {
			t.Errorf("cc %s handling = %q, want %q", e.Name, got, want)
		}
		if !agent.IsInstallableHookSpec(e) && len(e.EmitsStatus) != 0 {
			t.Errorf("cc non-installable %s EmitsStatus = %v, want empty", e.Name, e.EmitsStatus)
		}
	}
}

func TestCCEvents_InstallableSetStaysStable(t *testing.T) {
	p := NewProvider(nil, nil, nil, nil)
	got := map[string]bool{}
	for _, e := range p.Events() {
		if agent.IsInstallableHookSpec(e) {
			got[e.Name] = true
		}
	}
	want := map[string]bool{}
	for _, name := range expectedCCInstallableEventNames {
		want[name] = true
	}
	if len(got) != len(want) {
		t.Fatalf("cc installable count = %d, want %d: %#v", len(got), len(want), got)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("cc installable set missing %q", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("cc installable set contains unexpected %q", name)
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
