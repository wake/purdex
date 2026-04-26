package codex_test

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

// expectedCodexEventNames is the post-expansion 9-event list (plan §1.4,
// issue #613). Pre-expansion codex only installed {SessionStart,
// UserPromptSubmit, Stop}; this PR brings the installer up to the full set
// DeriveStatus has already supported since Phase 1.
var expectedCodexEventNames = []string{
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

// TestCodexEvents_ExpandedTo9 is the core issue #613 assertion — codex
// Events() now declares 9 events, matching cc.
func TestCodexEvents_ExpandedTo9(t *testing.T) {
	p := codex.NewProvider()
	events := p.Events()
	if len(events) != 9 {
		t.Fatalf("codex Events count = %d, want 9 (issue #613 installer expansion)", len(events))
	}

	got := make(map[string]bool, len(events))
	for _, e := range events {
		if got[e.Name] {
			t.Errorf("codex Events contains duplicate Name %q", e.Name)
		}
		got[e.Name] = true
	}
	want := make(map[string]bool, len(expectedCodexEventNames))
	for _, n := range expectedCodexEventNames {
		want[n] = true
	}
	for n := range want {
		if !got[n] {
			t.Errorf("codex Events missing required Name %q (post-expansion)", n)
		}
	}
	for n := range got {
		if !want[n] {
			t.Errorf("codex Events contains unexpected Name %q", n)
		}
	}
}

// TestCodexEvents_EmitsStatusForNotification asserts codex Notification is
// declared to emit {Waiting, Idle} matching cc.
func TestCodexEvents_EmitsStatusForNotification(t *testing.T) {
	p := codex.NewProvider()
	var spec *agent.HookEventSpec
	for _, e := range p.Events() {
		if e.Name == "Notification" {
			ec := e
			spec = &ec
			break
		}
	}
	if spec == nil {
		t.Fatal("codex Events missing Notification entry")
	}
	got := make(map[agent.Status]bool, len(spec.EmitsStatus))
	for _, s := range spec.EmitsStatus {
		got[s] = true
	}
	for _, want := range []agent.Status{agent.StatusWaiting, agent.StatusIdle} {
		if !got[want] {
			t.Errorf("codex Notification EmitsStatus missing %q (got %v)", want, spec.EmitsStatus)
		}
	}
	if len(got) != 2 {
		t.Errorf("codex Notification EmitsStatus = %v, want exactly {Waiting, Idle}", spec.EmitsStatus)
	}
}

// TestCodexEvents_DetailOnlyHaveEmptyEmitsStatus enforces empty-slice
// semantics for detail-only SubagentStart/Stop.
func TestCodexEvents_DetailOnlyHaveEmptyEmitsStatus(t *testing.T) {
	p := codex.NewProvider()
	for _, e := range p.Events() {
		if e.Name != "SubagentStart" && e.Name != "SubagentStop" {
			continue
		}
		if e.EmitsStatus == nil {
			t.Errorf("codex %s EmitsStatus is nil; want non-nil empty slice", e.Name)
		}
		if len(e.EmitsStatus) != 0 {
			t.Errorf("codex %s EmitsStatus = %v, want empty", e.Name, e.EmitsStatus)
		}
	}
}

// TestCodexEventsFutureOnlyFlags asserts Codex current required events are not
// FutureOnly while parser-capable events that are not guaranteed in current
// installs remain tolerated absent.
func TestCodexEventsFutureOnlyFlags(t *testing.T) {
	wantFutureOnly := map[string]bool{
		"SessionStart":      false,
		"UserPromptSubmit":  false,
		"Stop":              false,
		"SubagentStart":     true,
		"SubagentStop":      true,
		"StopFailure":       true,
		"Notification":      true,
		"PermissionRequest": false,
		"SessionEnd":        true,
	}
	p := codex.NewProvider()
	events := p.Events()
	got := make(map[string]bool, len(events))
	for _, e := range events {
		got[e.Name] = e.FutureOnly
	}
	for name, want := range wantFutureOnly {
		if g, ok := got[name]; !ok {
			t.Errorf("codex Events missing %q", name)
		} else if g != want {
			t.Errorf("codex Events %q FutureOnly = %v, want %v", name, g, want)
		}
	}
	if len(events) != len(wantFutureOnly) {
		t.Fatalf("codex Events count = %d, want %d", len(events), len(wantFutureOnly))
	}
}

// TestCodexEventsDefensiveCopyPreservesFutureOnly is the fix-plan §2.2 CE2
// assertion: Events() returns a fresh copy on every call including the
// FutureOnly bit. Mutating the returned slice's FutureOnly field must not
// leak into the next Events() call — the installer-facing flag must survive
// the defensive copy just like EmitsStatus does.
func TestCodexEventsDefensiveCopyPreservesFutureOnly(t *testing.T) {
	p := codex.NewProvider()
	first := p.Events()
	if len(first) == 0 {
		t.Fatal("codex Events returned empty slice")
	}
	idx := -1
	for i, e := range first {
		if e.FutureOnly {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("codex Events has no FutureOnly=true spec; cannot exercise defensive copy")
	}
	originalName := first[idx].Name
	first[idx].FutureOnly = false

	second := p.Events()
	for _, e := range second {
		if e.Name == originalName {
			if !e.FutureOnly {
				t.Fatalf("codex Events second call spec %q FutureOnly = false, want true (defensive copy failed)", e.Name)
			}
			return
		}
	}
	t.Fatalf("codex Events second call missing %q", originalName)
}

// TestCodexEvents_DescriptionsNonEmpty asserts descriptions are non-empty
// and emoji-free.
func TestCodexEvents_DescriptionsNonEmpty(t *testing.T) {
	p := codex.NewProvider()
	for _, e := range p.Events() {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("codex %s Description is empty", e.Name)
			continue
		}
		for _, r := range e.Description {
			if r >= 0x1F300 && r <= 0x1FAFF {
				t.Errorf("codex %s Description contains emoji rune %U: %q", e.Name, r, e.Description)
				break
			}
		}
	}
}
