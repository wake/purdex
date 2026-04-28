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
	"Setup":               agent.HookHandlingUnsupported,
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

// expectedCCLifecycle pins the W2 LifecycleEventKind value per cc Name. Any
// catalog edit that touches lifecycle classification must update this map and
// the §2.3.1 spec table together.
var expectedCCLifecycle = map[string]agent.LifecycleEventKind{
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

// expectedCCPreservedMetadata locks the EmitsStatus / Description / FutureOnly
// / Handling tuple per cc Name as of the pre-W2 catalog. Plain-struct-literal
// migration must not lose any of these fields (Round-2 G1 防漂移).
type ccLegacyMetadata struct {
	EmitsStatus []agent.Status
	Description string
	FutureOnly  bool
	Handling    agent.HookHandling
}

var expectedCCPreservedMetadata = map[string]ccLegacyMetadata{
	"Setup":               {[]agent.Status{}, "Claude Code setup hook initialization", false, agent.HookHandlingUnsupported},
	"SessionStart":        {[]agent.Status{agent.StatusIdle}, "Claude Code session started (non-compact source)", false, ""},
	"UserPromptSubmit":    {[]agent.Status{agent.StatusRunning}, "User submitted a prompt to the agent", false, ""},
	"SubagentStart":       {[]agent.Status{}, "Nested sub-agent task dispatched", false, ""},
	"SubagentStop":        {[]agent.Status{}, "Nested sub-agent task completed", false, ""},
	"Stop":                {[]agent.Status{agent.StatusIdle}, "Agent finished responding and is idle", false, ""},
	"StopFailure":         {[]agent.Status{agent.StatusError}, "Agent stopped due to an error", false, ""},
	"Notification":        {[]agent.Status{agent.StatusWaiting, agent.StatusIdle}, "Permission/elicitation prompt, idle prompt, or auth success", false, ""},
	"PermissionRequest":   {[]agent.Status{agent.StatusWaiting}, "Tool permission request awaiting user approval", false, ""},
	"SessionEnd":          {[]agent.Status{agent.StatusClear}, "Claude Code session ended", false, ""},
	"UserPromptExpansion": {[]agent.Status{}, "User command expanded before model processing", false, agent.HookHandlingUnsupported},
	"PreToolUse":          {[]agent.Status{}, "Tool call about to execute", false, agent.HookHandlingUnsupported},
	"PermissionDenied":    {[]agent.Status{}, "Tool permission denied by auto mode classifier", false, agent.HookHandlingIgnored},
	"PostToolUse":         {[]agent.Status{}, "Tool call completed successfully", false, agent.HookHandlingIgnored},
	"PostToolUseFailure":  {[]agent.Status{}, "Tool call failed", false, agent.HookHandlingIgnored},
	"PostToolBatch":       {[]agent.Status{}, "Parallel tool call batch resolved", false, agent.HookHandlingUnsupported},
	"TaskCreated":         {[]agent.Status{}, "Task was created", false, agent.HookHandlingIgnored},
	"TaskCompleted":       {[]agent.Status{}, "Task was completed", false, agent.HookHandlingIgnored},
	"TeammateIdle":        {[]agent.Status{}, "Agent team teammate is about to go idle", false, agent.HookHandlingIgnored},
	"InstructionsLoaded":  {[]agent.Status{}, "Project instructions were loaded", false, agent.HookHandlingIgnored},
	"ConfigChange":        {[]agent.Status{}, "Claude Code configuration changed", false, agent.HookHandlingIgnored},
	"CwdChanged":          {[]agent.Status{}, "Working directory changed", false, agent.HookHandlingIgnored},
	"FileChanged":         {[]agent.Status{}, "Watched file changed on disk", false, agent.HookHandlingUnsupported},
	"WorktreeCreate":      {[]agent.Status{}, "Worktree is being created", false, agent.HookHandlingUnsupported},
	"WorktreeRemove":      {[]agent.Status{}, "Worktree is being removed", false, agent.HookHandlingUnsupported},
	"PreCompact":          {[]agent.Status{}, "Context compaction is about to run", false, agent.HookHandlingUnsupported},
	"PostCompact":         {[]agent.Status{}, "Context compaction completed", false, agent.HookHandlingIgnored},
	"Elicitation":         {[]agent.Status{}, "MCP server requested user input", false, agent.HookHandlingIgnored},
	"ElicitationResult":   {[]agent.Status{}, "MCP elicitation response was submitted", false, agent.HookHandlingIgnored},
}

// TestCcEventSpecs_PurdexNamePdxPrefix verifies invariant 1: every cc entry
// has a non-empty PurdexName starting with "Pdx".
func TestCcEventSpecs_PurdexNamePdxPrefix(t *testing.T) {
	for _, e := range ccEventSpecs {
		if e.PurdexName == "" {
			t.Errorf("cc %q: PurdexName empty", e.Name)
			continue
		}
		if !strings.HasPrefix(e.PurdexName, "Pdx") {
			t.Errorf("cc %q: PurdexName %q lacks Pdx prefix", e.Name, e.PurdexName)
		}
	}
}

// TestCcEventSpecs_NameMatchesTrimPrefix verifies invariant 2: the legacy
// dev-time Name backfill equals strings.TrimPrefix(PurdexName, "Pdx") so
// fixtures and migration tooling can round-trip the rename mechanically.
// Phase 3 ship removes this invariant (and the Name field).
func TestCcEventSpecs_NameMatchesTrimPrefix(t *testing.T) {
	for _, e := range ccEventSpecs {
		want := strings.TrimPrefix(e.PurdexName, "Pdx")
		if e.Name != want {
			t.Errorf("cc %q: Name %q != TrimPrefix(PurdexName,%q)=%q", e.PurdexName, e.Name, "Pdx", want)
		}
	}
}

// TestCcEventSpecs_UpstreamKeysNotEmpty verifies invariant 1 sub-clause:
// every entry (installable / unsupported / ignored) has a non-empty
// UpstreamKeys slice so the upstream identifier is always traceable post
// Phase 3 Name removal.
func TestCcEventSpecs_UpstreamKeysNotEmpty(t *testing.T) {
	for _, e := range ccEventSpecs {
		if len(e.UpstreamKeys) == 0 {
			t.Errorf("cc %q: UpstreamKeys empty", e.PurdexName)
		}
	}
}

// TestCcEventSpecs_PurdexNameNotInUpstreamKeys verifies invariant 3: a
// PurdexName must not appear in its own UpstreamKeys slice (otherwise the
// schema degenerates to a single string).
func TestCcEventSpecs_PurdexNameNotInUpstreamKeys(t *testing.T) {
	for _, e := range ccEventSpecs {
		for _, k := range e.UpstreamKeys {
			if k == e.PurdexName {
				t.Errorf("cc %q: PurdexName %q present in UpstreamKeys", e.PurdexName, k)
			}
		}
	}
}

// TestCcEventSpecs_LifecycleAlignment verifies invariant 5: the Lifecycle
// field aligns with the §2.3.1 table. Entries not listed in
// expectedCCLifecycle must be LifecycleNone (unsupported / ignored).
func TestCcEventSpecs_LifecycleAlignment(t *testing.T) {
	for _, e := range ccEventSpecs {
		want, listed := expectedCCLifecycle[e.Name]
		if !listed {
			want = agent.LifecycleNone
		}
		if e.Lifecycle != want {
			t.Errorf("cc %q (Name=%q): Lifecycle=%v, want %v", e.PurdexName, e.Name, e.Lifecycle, want)
		}
	}
}

// TestCcEventSpecs_PreservedLegacyMetadata verifies invariant 4: the plain-
// struct-literal rewrite did not drop any pre-W2 metadata field
// (EmitsStatus / Description / FutureOnly / Handling).
func TestCcEventSpecs_PreservedLegacyMetadata(t *testing.T) {
	for _, e := range ccEventSpecs {
		want, ok := expectedCCPreservedMetadata[e.Name]
		if !ok {
			t.Errorf("cc %q: not present in expectedCCPreservedMetadata; update test fixture", e.Name)
			continue
		}
		if e.Description != want.Description {
			t.Errorf("cc %q: Description=%q want %q", e.Name, e.Description, want.Description)
		}
		if e.FutureOnly != want.FutureOnly {
			t.Errorf("cc %q: FutureOnly=%v want %v", e.Name, e.FutureOnly, want.FutureOnly)
		}
		if e.Handling != want.Handling {
			t.Errorf("cc %q: Handling=%q want %q", e.Name, e.Handling, want.Handling)
		}
		if len(e.EmitsStatus) != len(want.EmitsStatus) {
			t.Errorf("cc %q: EmitsStatus len=%d want %d", e.Name, len(e.EmitsStatus), len(want.EmitsStatus))
			continue
		}
		gotSet := make(map[agent.Status]bool, len(e.EmitsStatus))
		for _, s := range e.EmitsStatus {
			gotSet[s] = true
		}
		for _, s := range want.EmitsStatus {
			if !gotSet[s] {
				t.Errorf("cc %q: EmitsStatus missing %q (got %v)", e.Name, s, e.EmitsStatus)
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
