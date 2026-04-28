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

// expectedCodexLifecycle pins the W2 LifecycleEventKind value per codex Name.
// Mirrors expectedCCLifecycle in cc/events_test.go — codex catalog has parity
// with cc on the 9 installable events plus PreToolUse/PostToolUse unsupported.
var expectedCodexLifecycle = map[string]agent.LifecycleEventKind{
	"SessionStart":      agent.LifecycleSessionStart,
	"UserPromptSubmit":  agent.LifecycleUserPromptSubmit,
	"Stop":              agent.LifecycleStop,
	"StopFailure":       agent.LifecycleStopFailure,
	"Notification":      agent.LifecycleNone,
	"PermissionRequest": agent.LifecycleNone,
	"SessionEnd":        agent.LifecycleSessionEnd,
	"SubagentStart":     agent.LifecycleSubagentStart,
	"SubagentStop":      agent.LifecycleSubagentStop,
}

// codexLegacyMetadata locks EmitsStatus / Description / FutureOnly / Handling
// per codex Name as of the pre-W2 catalog. Plain-struct-literal migration must
// not lose any of these fields.
type codexLegacyMetadata struct {
	EmitsStatus []agent.Status
	Description string
	FutureOnly  bool
	Handling    agent.HookHandling
}

var expectedCodexPreservedMetadata = map[string]codexLegacyMetadata{
	"SessionStart":      {[]agent.Status{agent.StatusIdle}, "Codex session started", false, ""},
	"UserPromptSubmit":  {[]agent.Status{agent.StatusRunning}, "User submitted a prompt", false, ""},
	"SubagentStart":     {[]agent.Status{}, "Nested sub-agent task dispatched", true, ""},
	"SubagentStop":      {[]agent.Status{}, "Nested sub-agent task completed", true, ""},
	"Stop":              {[]agent.Status{agent.StatusIdle}, "Agent finished responding and is idle", false, ""},
	"StopFailure":       {[]agent.Status{agent.StatusError}, "Agent stopped due to an error", true, ""},
	"Notification":      {[]agent.Status{agent.StatusWaiting, agent.StatusIdle}, "Permission/elicitation/idle prompt notifications", true, ""},
	"PermissionRequest": {[]agent.Status{agent.StatusWaiting}, "Tool permission request awaiting user approval", false, ""},
	"SessionEnd":        {[]agent.Status{agent.StatusClear}, "Codex session ended", true, ""},
	"PreToolUse":        {[]agent.Status{}, "Tool call about to execute", false, agent.HookHandlingUnsupported},
	"PostToolUse":       {[]agent.Status{}, "Tool call completed", false, agent.HookHandlingUnsupported},
}

// TestCodexEventSpecs_PurdexNamePdxPrefix verifies invariant 1: every codex
// entry has a non-empty PurdexName starting with "Pdx".
func TestCodexEventSpecs_PurdexNamePdxPrefix(t *testing.T) {
	for _, e := range codex.NewProvider().Events() {
		if e.PurdexName == "" {
			t.Errorf("codex %q: PurdexName empty", e.Name)
			continue
		}
		if !strings.HasPrefix(e.PurdexName, "Pdx") {
			t.Errorf("codex %q: PurdexName %q lacks Pdx prefix", e.Name, e.PurdexName)
		}
	}
}

// TestCodexEventSpecs_NameMatchesTrimPrefix verifies invariant 2: legacy
// dev-time Name backfill equals strings.TrimPrefix(PurdexName, "Pdx").
// Phase 3 ship removes this invariant (and the Name field).
func TestCodexEventSpecs_NameMatchesTrimPrefix(t *testing.T) {
	for _, e := range codex.NewProvider().Events() {
		want := strings.TrimPrefix(e.PurdexName, "Pdx")
		if e.Name != want {
			t.Errorf("codex %q: Name %q != TrimPrefix(PurdexName,%q)=%q", e.PurdexName, e.Name, "Pdx", want)
		}
	}
}

// TestCodexEventSpecs_UpstreamKeysNotEmpty verifies invariant 1 sub-clause:
// every entry has a non-empty UpstreamKeys slice.
func TestCodexEventSpecs_UpstreamKeysNotEmpty(t *testing.T) {
	for _, e := range codex.NewProvider().Events() {
		if len(e.UpstreamKeys) == 0 {
			t.Errorf("codex %q: UpstreamKeys empty", e.PurdexName)
		}
	}
}

// TestCodexEventSpecs_PurdexNameNotInUpstreamKeys verifies invariant 3: a
// PurdexName must not appear in its own UpstreamKeys slice.
func TestCodexEventSpecs_PurdexNameNotInUpstreamKeys(t *testing.T) {
	for _, e := range codex.NewProvider().Events() {
		for _, k := range e.UpstreamKeys {
			if k == e.PurdexName {
				t.Errorf("codex %q: PurdexName %q present in UpstreamKeys", e.PurdexName, k)
			}
		}
	}
}

// TestCodexEventSpecs_LifecycleAlignment verifies invariant 5: the Lifecycle
// field aligns with the §2.3.1 table. Entries not listed in
// expectedCodexLifecycle must be LifecycleNone.
func TestCodexEventSpecs_LifecycleAlignment(t *testing.T) {
	for _, e := range codex.NewProvider().Events() {
		want, listed := expectedCodexLifecycle[e.Name]
		if !listed {
			want = agent.LifecycleNone
		}
		if e.Lifecycle != want {
			t.Errorf("codex %q (Name=%q): Lifecycle=%v, want %v", e.PurdexName, e.Name, e.Lifecycle, want)
		}
	}
}

// TestCodexEventSpecs_PreservedLegacyMetadata verifies invariant 4: the
// plain-struct-literal rewrite did not drop any pre-W2 metadata field.
func TestCodexEventSpecs_PreservedLegacyMetadata(t *testing.T) {
	for _, e := range codex.NewProvider().Events() {
		want, ok := expectedCodexPreservedMetadata[e.Name]
		if !ok {
			t.Errorf("codex %q: not present in expectedCodexPreservedMetadata; update test fixture", e.Name)
			continue
		}
		if e.Description != want.Description {
			t.Errorf("codex %q: Description=%q want %q", e.Name, e.Description, want.Description)
		}
		if e.FutureOnly != want.FutureOnly {
			t.Errorf("codex %q: FutureOnly=%v want %v", e.Name, e.FutureOnly, want.FutureOnly)
		}
		if e.Handling != want.Handling {
			t.Errorf("codex %q: Handling=%q want %q", e.Name, e.Handling, want.Handling)
		}
		if len(e.EmitsStatus) != len(want.EmitsStatus) {
			t.Errorf("codex %q: EmitsStatus len=%d want %d", e.Name, len(e.EmitsStatus), len(want.EmitsStatus))
			continue
		}
		gotSet := make(map[agent.Status]bool, len(e.EmitsStatus))
		for _, s := range e.EmitsStatus {
			gotSet[s] = true
		}
		for _, s := range want.EmitsStatus {
			if !gotSet[s] {
				t.Errorf("codex %q: EmitsStatus missing %q (got %v)", e.Name, s, e.EmitsStatus)
			}
		}
	}
}
