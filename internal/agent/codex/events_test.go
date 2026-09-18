package codex_test

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

// expectedCodexInstallableEventNames lists the hook events Purdex installs
// for Codex 0.153.x (issue #1159), keyed on PurdexName.
var expectedCodexInstallableEventNames = []string{
	"PdxSessionStart",
	"PdxUserPromptSubmit",
	"PdxSubagentStart",
	"PdxSubagentStop",
	"PdxStop",
	"PdxPermissionRequest",
	"PdxSessionEnd",
	"PdxPreToolUse",
	"PdxPostToolUse",
	"PdxInterrupt",
}

// expectedCodexEventNames is the upstream-key view of the installable set.
var expectedCodexEventNames = []string{
	"SessionStart",
	"UserPromptSubmit",
	"SubagentStart",
	"SubagentStop",
	"Stop",
	"PermissionRequest",
	"SessionEnd",
	"PreToolUse",
	"PostToolUse",
	"Interrupt",
}

// expectedCodexCurrentUpstreamEventNames is pinned to the codex hooks docs
// (https://developers.openai.com/codex/hooks), fetched 2026-09-18 for
// codex-cli 0.153.4. Exactly the 12 upstream hook events.
var expectedCodexCurrentUpstreamEventNames = []string{
	"SessionStart",
	"SessionEnd",
	"SubagentStart",
	"SubagentStop",
	"PreToolUse",
	"PermissionRequest",
	"PostToolUse",
	"PreCompact",
	"PostCompact",
	"UserPromptSubmit",
	"Stop",
	"Interrupt",
}

// expectedCodexRetiredUpstreamEventNames are keys the pre-0.153 installer
// wrote that codex never fired. They stay in the catalog as ignored so
// in-flight payloads still resolve, but are not upstream events.
var expectedCodexRetiredUpstreamEventNames = []string{
	"Notification",
	"StopFailure",
}

var expectedCodexCatalogHandling = map[string]agent.HookHandling{
	"PdxSessionStart":      agent.HookHandlingStatus,
	"PdxUserPromptSubmit":  agent.HookHandlingStatus,
	"PdxSubagentStart":     agent.HookHandlingDetail,
	"PdxSubagentStop":      agent.HookHandlingDetail,
	"PdxStop":              agent.HookHandlingStatus,
	"PdxStopFailure":       agent.HookHandlingIgnored,
	"PdxNotification":      agent.HookHandlingIgnored,
	"PdxPermissionRequest": agent.HookHandlingStatus,
	"PdxSessionEnd":        agent.HookHandlingStatus,
	"PdxPreToolUse":        agent.HookHandlingDetail,
	"PdxPostToolUse":       agent.HookHandlingStatus,
	"PdxInterrupt":         agent.HookHandlingStatus,
	"PdxPreCompact":        agent.HookHandlingIgnored,
	"PdxPostCompact":       agent.HookHandlingIgnored,
}

// TestCodexEvents_ExpandedToCatalog asserts Events() exposes the classified
// upstream catalog while the installer still derives its installable subset.
func TestCodexEvents_MatchesCatalogHandling(t *testing.T) {
	p := codex.NewProvider()
	events := p.Events()
	if len(events) != len(expectedCodexCatalogHandling) {
		t.Fatalf("codex Events count = %d, want %d", len(events), len(expectedCodexCatalogHandling))
	}

	got := make(map[string]bool, len(events))
	for _, e := range events {
		if got[e.PurdexName] {
			t.Errorf("codex Events contains duplicate PurdexName %q", e.PurdexName)
		}
		got[e.PurdexName] = true
	}
	want := make(map[string]bool, len(expectedCodexCatalogHandling))
	for n := range expectedCodexCatalogHandling {
		want[n] = true
	}
	for n := range want {
		if !got[n] {
			t.Errorf("codex Events missing required PurdexName %q (post-expansion)", n)
		}
	}
	for n := range got {
		if !want[n] {
			t.Errorf("codex Events contains unexpected PurdexName %q", n)
		}
	}
}

func TestCodexEventsClassifyCurrentDocs(t *testing.T) {
	p := codex.NewProvider()
	for _, e := range p.Events() {
		want, ok := expectedCodexCatalogHandling[e.PurdexName]
		if !ok {
			continue
		}
		if got := agent.EffectiveHookHandling(e); got != want {
			t.Errorf("codex %s handling = %q, want %q", e.PurdexName, got, want)
		}
		if !agent.IsInstallableHookSpec(e) && len(e.EmitsStatus) != 0 {
			if _, retired := expectedCodexRetiredEmitsStatus[e.PurdexName]; !retired {
				t.Errorf("codex non-installable %s EmitsStatus = %v, want empty", e.PurdexName, e.EmitsStatus)
			}
		}
	}
}

func TestCodexEvents_CurrentUpstreamDocsSubset(t *testing.T) {
	p := codex.NewProvider()
	got := map[string]bool{}
	for _, e := range p.Events() {
		// Cover the catalog by upstream-key membership; expectedCodexCurrent
		// UpstreamEventNames pins the raw event names that codex docs declare.
		for _, k := range e.UpstreamKeys {
			got[k] = true
		}
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
			got[e.PurdexName] = true
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

// expectedCodexRetiredEmitsStatus pins the parser-retained EmitsStatus of
// the retired entries: they are not installable (explicit Ignored) but
// DeriveStatus still handles them (spec §2.1), so SupportedStatuses keeps
// error/waiting/idle from them (spec §2.5).
var expectedCodexRetiredEmitsStatus = map[string][]agent.Status{
	"PdxNotification": {agent.StatusWaiting, agent.StatusIdle},
	"PdxStopFailure":  {agent.StatusError},
}

// TestCodexEvents_RetiredEntriesIgnored asserts Notification / StopFailure
// remain resolvable, are explicitly ignored (never installed), and keep
// their parser-retained EmitsStatus.
func TestCodexEvents_RetiredEntriesIgnored(t *testing.T) {
	p := codex.NewProvider()
	for _, key := range expectedCodexRetiredUpstreamEventNames {
		spec, ok := agent.LookupByUpstreamKey(p.Events(), key)
		if !ok {
			t.Fatalf("codex catalog missing retired upstream key %q", key)
		}
		if spec.PurdexName != "Pdx"+key {
			t.Errorf("retired %q PurdexName = %q, want %q", key, spec.PurdexName, "Pdx"+key)
		}
		if spec.Handling != agent.HookHandlingIgnored {
			t.Errorf("retired %q Handling = %q, want explicit ignored", key, spec.Handling)
		}
		if agent.IsInstallableHookSpec(spec) {
			t.Errorf("retired %q is installable", key)
		}
		want := expectedCodexRetiredEmitsStatus[spec.PurdexName]
		if len(spec.EmitsStatus) != len(want) {
			t.Errorf("retired %q EmitsStatus = %v, want %v", key, spec.EmitsStatus, want)
		}
	}
}

// TestCodexEvents_UpstreamPinBidirectional asserts catalog upstream keys
// minus the retired set equal the pinned 0.153.4 docs list exactly, and
// that no retired key is claimed as current upstream.
func TestCodexEvents_UpstreamPinBidirectional(t *testing.T) {
	p := codex.NewProvider()
	retired := map[string]bool{}
	for _, k := range expectedCodexRetiredUpstreamEventNames {
		retired[k] = true
	}
	pinned := map[string]bool{}
	for _, k := range expectedCodexCurrentUpstreamEventNames {
		if retired[k] {
			t.Fatalf("pinned upstream list contains retired key %q", k)
		}
		pinned[k] = true
	}
	catalog := map[string]bool{}
	for _, e := range p.Events() {
		for _, k := range e.UpstreamKeys {
			if catalog[k] {
				t.Errorf("duplicate upstream key %q in codex catalog", k)
			}
			catalog[k] = true
		}
	}
	for k := range pinned {
		if !catalog[k] {
			t.Errorf("codex catalog missing pinned upstream event %q", k)
		}
	}
	for k := range catalog {
		if !pinned[k] && !retired[k] {
			t.Errorf("codex catalog upstream key %q is neither pinned nor retired", k)
		}
	}
	for k := range retired {
		if !catalog[k] {
			t.Errorf("codex catalog missing retired key %q", k)
		}
	}
}

// TestCodexEvents_DetailOnlyHaveEmptyEmitsStatus enforces empty-slice
// semantics for detail-only SubagentStart/Stop.
func TestCodexEvents_DetailOnlyHaveEmptyEmitsStatus(t *testing.T) {
	p := codex.NewProvider()
	for _, e := range p.Events() {
		if e.PurdexName != "PdxSubagentStart" && e.PurdexName != "PdxSubagentStop" {
			continue
		}
		if e.EmitsStatus == nil {
			t.Errorf("codex %s EmitsStatus is nil; want non-nil empty slice", e.PurdexName)
		}
		if len(e.EmitsStatus) != 0 {
			t.Errorf("codex %s EmitsStatus = %v, want empty", e.PurdexName, e.EmitsStatus)
		}
	}
}

// TestCodexEventsFutureOnlyFlags asserts Codex current required events are not
// FutureOnly while parser-capable events that are not guaranteed in current
// installs remain tolerated absent.
func TestCodexEventsFutureOnlyFlags(t *testing.T) {
	wantFutureOnly := map[string]bool{
		"PdxSessionStart":      false,
		"PdxUserPromptSubmit":  false,
		"PdxStop":              false,
		"PdxSubagentStart":     true,
		"PdxSubagentStop":      true,
		"PdxStopFailure":       false,
		"PdxNotification":      false,
		"PdxPermissionRequest": false,
		"PdxSessionEnd":        true,
		"PdxPreToolUse":        true,
		"PdxPostToolUse":       false,
		"PdxInterrupt":         false,
		"PdxPreCompact":        false,
		"PdxPostCompact":       false,
	}
	p := codex.NewProvider()
	events := p.Events()
	got := make(map[string]bool, len(events))
	for _, e := range events {
		got[e.PurdexName] = e.FutureOnly
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
	originalName := first[idx].PurdexName
	first[idx].FutureOnly = false

	second := p.Events()
	for _, e := range second {
		if e.PurdexName == originalName {
			if !e.FutureOnly {
				t.Fatalf("codex Events second call spec %q FutureOnly = false, want true (defensive copy failed)", e.PurdexName)
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
			t.Errorf("codex %s Description is empty", e.PurdexName)
			continue
		}
		for _, r := range e.Description {
			if r >= 0x1F300 && r <= 0x1FAFF {
				t.Errorf("codex %s Description contains emoji rune %U: %q", e.PurdexName, r, e.Description)
				break
			}
		}
	}
}

// expectedCodexLifecycle pins the W2 LifecycleEventKind value per codex
// PurdexName. Mirrors expectedCCLifecycle in cc/events_test.go — codex catalog
// has parity with cc on the 9 installable events plus PreToolUse/PostToolUse
// unsupported.
var expectedCodexLifecycle = map[string]agent.LifecycleEventKind{
	"PdxSessionStart":      agent.LifecycleSessionStart,
	"PdxUserPromptSubmit":  agent.LifecycleUserPromptSubmit,
	"PdxStop":              agent.LifecycleStop,
	"PdxStopFailure":       agent.LifecycleStopFailure,
	"PdxNotification":      agent.LifecycleNone,
	"PdxPermissionRequest": agent.LifecycleNone,
	"PdxSessionEnd":        agent.LifecycleSessionEnd,
	"PdxSubagentStart":     agent.LifecycleSubagentStart,
	"PdxSubagentStop":      agent.LifecycleSubagentStop,
	// L2: PdxPreToolUse shares the UserPromptSubmit lifecycle so the new
	// applyFrameEvent case attaches the codex broker proxy ref for
	// non-prompt turns (spec §3.3.C strategy a).
	"PdxPreToolUse": agent.LifecycleUserPromptSubmit,
	"PdxInterrupt":  agent.LifecycleStop,
}

// codexLegacyMetadata locks EmitsStatus / Description / FutureOnly / Handling
// per codex PurdexName as of the pre-W2 catalog. Plain-struct-literal
// migration must not lose any of these fields.
type codexLegacyMetadata struct {
	EmitsStatus []agent.Status
	Description string
	FutureOnly  bool
	Handling    agent.HookHandling
}

var expectedCodexPreservedMetadata = map[string]codexLegacyMetadata{
	"PdxSessionStart":      {[]agent.Status{agent.StatusIdle}, "Codex session started", false, ""},
	"PdxUserPromptSubmit":  {[]agent.Status{agent.StatusRunning}, "User submitted a prompt", false, ""},
	"PdxSubagentStart":     {[]agent.Status{}, "Nested sub-agent task dispatched", true, ""},
	"PdxSubagentStop":      {[]agent.Status{}, "Nested sub-agent task completed", true, ""},
	"PdxStop":              {[]agent.Status{agent.StatusIdle}, "Agent finished responding and is idle", false, ""},
	"PdxStopFailure":       {[]agent.Status{agent.StatusError}, "Retired: not a codex hook event since 0.153", false, agent.HookHandlingIgnored},
	"PdxNotification":      {[]agent.Status{agent.StatusWaiting, agent.StatusIdle}, "Retired: not a codex hook event since 0.153", false, agent.HookHandlingIgnored},
	"PdxPermissionRequest": {[]agent.Status{agent.StatusWaiting}, "Tool permission request awaiting user approval", false, ""},
	"PdxSessionEnd":        {[]agent.Status{agent.StatusClear}, "Codex session ended", true, ""},
	"PdxPreToolUse":        {[]agent.Status{}, "Tool call about to execute", true, ""},
	"PdxPostToolUse":       {[]agent.Status{agent.StatusRunning}, "Tool call completed (signals running after permission grant)", false, ""},
	"PdxInterrupt":         {[]agent.Status{agent.StatusIdle}, "Turn interrupted by the user", false, ""},
	"PdxPreCompact":        {[]agent.Status{}, "Context compaction about to start", false, agent.HookHandlingIgnored},
	"PdxPostCompact":       {[]agent.Status{}, "Context compaction completed", false, agent.HookHandlingIgnored},
}

// TestCodexEventSpecs_PurdexNamePdxPrefix verifies invariant 1: every codex
// entry has a non-empty PurdexName starting with "Pdx".
func TestCodexEventSpecs_PurdexNamePdxPrefix(t *testing.T) {
	for _, e := range codex.NewProvider().Events() {
		if e.PurdexName == "" {
			t.Errorf("codex %v: PurdexName empty", e.UpstreamKeys)
			continue
		}
		if !strings.HasPrefix(e.PurdexName, "Pdx") {
			t.Errorf("codex %q: PurdexName lacks Pdx prefix", e.PurdexName)
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
		want, listed := expectedCodexLifecycle[e.PurdexName]
		if !listed {
			want = agent.LifecycleNone
		}
		if e.Lifecycle != want {
			t.Errorf("codex %q: Lifecycle=%v, want %v", e.PurdexName, e.Lifecycle, want)
		}
	}
}

// TestCodexEventSpecs_PreservedLegacyMetadata verifies invariant 4: the
// plain-struct-literal rewrite did not drop any pre-W2 metadata field.
func TestCodexEventSpecs_PreservedLegacyMetadata(t *testing.T) {
	for _, e := range codex.NewProvider().Events() {
		want, ok := expectedCodexPreservedMetadata[e.PurdexName]
		if !ok {
			t.Errorf("codex %q: not present in expectedCodexPreservedMetadata; update test fixture", e.PurdexName)
			continue
		}
		if e.Description != want.Description {
			t.Errorf("codex %q: Description=%q want %q", e.PurdexName, e.Description, want.Description)
		}
		if e.FutureOnly != want.FutureOnly {
			t.Errorf("codex %q: FutureOnly=%v want %v", e.PurdexName, e.FutureOnly, want.FutureOnly)
		}
		if e.Handling != want.Handling {
			t.Errorf("codex %q: Handling=%q want %q", e.PurdexName, e.Handling, want.Handling)
		}
		if len(e.EmitsStatus) != len(want.EmitsStatus) {
			t.Errorf("codex %q: EmitsStatus len=%d want %d", e.PurdexName, len(e.EmitsStatus), len(want.EmitsStatus))
			continue
		}
		gotSet := make(map[agent.Status]bool, len(e.EmitsStatus))
		for _, s := range e.EmitsStatus {
			gotSet[s] = true
		}
		for _, s := range want.EmitsStatus {
			if !gotSet[s] {
				t.Errorf("codex %q: EmitsStatus missing %q (got %v)", e.PurdexName, s, e.EmitsStatus)
			}
		}
	}
}
