package codex_test

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

// expectedCodexInstallableEventNames lists the hook events Purdex currently
// installs for Codex. Keep this stable while upstream catalog entries grow
// around it.
var expectedCodexInstallableEventNames = []string{
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

var expectedCodexEventNames = expectedCodexInstallableEventNames

// expectedCodexCurrentUpstreamEventNames is pinned to Codex hooks docs,
// fetched 2026-04-26 from https://developers.openai.com/codex/hooks.
var expectedCodexCurrentUpstreamEventNames = []string{
	"SessionStart",
	"PreToolUse",
	"PermissionRequest",
	"PostToolUse",
	"UserPromptSubmit",
	"Stop",
}

// expectedCodexCatalogHandling covers the current upstream Codex hook surface
// plus Purdex compatibility entries that were already installable before this
// catalog-only PR. The compatibility entries are intentionally kept installable
// to avoid changing runtime hook behavior here.
var expectedCodexCatalogHandling = map[string]agent.HookHandling{
	"SessionStart":      agent.HookHandlingStatus,
	"UserPromptSubmit":  agent.HookHandlingStatus,
	"SubagentStart":     agent.HookHandlingDetail,
	"SubagentStop":      agent.HookHandlingDetail,
	"Stop":              agent.HookHandlingStatus,
	"StopFailure":       agent.HookHandlingStatus,
	"Notification":      agent.HookHandlingStatus,
	"PermissionRequest": agent.HookHandlingStatus,
	"SessionEnd":        agent.HookHandlingStatus,
	"PreToolUse":        agent.HookHandlingUnsupported,
	"PostToolUse":       agent.HookHandlingUnsupported,
}

// TestCodexEvents_ExpandedToCatalog asserts Events() exposes the classified
// upstream catalog while the installer still derives its installable subset.
func TestCodexEvents_ExpandedTo9(t *testing.T) {
	p := codex.NewProvider()
	events := p.Events()
	if len(events) != len(expectedCodexCatalogHandling) {
		t.Fatalf("codex Events count = %d, want %d", len(events), len(expectedCodexCatalogHandling))
	}

	got := make(map[string]bool, len(events))
	for _, e := range events {
		if got[e.Name] {
			t.Errorf("codex Events contains duplicate Name %q", e.Name)
		}
		got[e.Name] = true
	}
	want := make(map[string]bool, len(expectedCodexCatalogHandling))
	for n := range expectedCodexCatalogHandling {
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

func TestCodexEventsClassifyCurrentDocs(t *testing.T) {
	p := codex.NewProvider()
	for _, e := range p.Events() {
		want, ok := expectedCodexCatalogHandling[e.Name]
		if !ok {
			continue
		}
		if got := agent.EffectiveHookHandling(e); got != want {
			t.Errorf("codex %s handling = %q, want %q", e.Name, got, want)
		}
		if !agent.IsInstallableHookSpec(e) && len(e.EmitsStatus) != 0 {
			t.Errorf("codex non-installable %s EmitsStatus = %v, want empty", e.Name, e.EmitsStatus)
		}
	}
}

func TestCodexEvents_CurrentUpstreamDocsSubset(t *testing.T) {
	p := codex.NewProvider()
	got := map[string]bool{}
	for _, e := range p.Events() {
		got[e.Name] = true
	}
	for _, name := range expectedCodexCurrentUpstreamEventNames {
		if !got[name] {
			t.Errorf("codex catalog missing current upstream event %q", name)
		}
	}
}

func TestCodexEvents_InstallableSetStaysStable(t *testing.T) {
	p := codex.NewProvider()
	got := map[string]bool{}
	for _, e := range p.Events() {
		if agent.IsInstallableHookSpec(e) {
			got[e.Name] = true
		}
	}
	want := map[string]bool{}
	for _, name := range expectedCodexInstallableEventNames {
		want[name] = true
	}
	if len(got) != len(want) {
		t.Fatalf("codex installable count = %d, want %d: %#v", len(got), len(want), got)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("codex installable set missing %q", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("codex installable set contains unexpected %q", name)
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
		"PreToolUse":        false,
		"PostToolUse":       false,
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
