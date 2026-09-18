package codex

import "github.com/wake/purdex/internal/agent"

// codexEventSpecs is the declarative hook event catalog for Codex, aligned
// with codex-cli 0.153.4 (issue #1159, spec
// docs/specs/2026-09-18-codex-hooks-catalog-spec.md). The 12 upstream hook
// events are all declared; 10 are installable, PreCompact/PostCompact are
// ignored. Notification and StopFailure were written by the pre-0.153
// installer but codex never fired them; they stay as explicitly ignored
// entries (never installed, stripped on install/remove) that keep their
// EmitsStatus and DeriveStatus cases so in-flight payloads still resolve
// and SupportedStatuses is unchanged (spec §2.1 / §2.5).
var codexEventSpecs = []agent.HookEventSpec{
	{
		PurdexName:   "PdxSessionStart",
		UpstreamKeys: []string{"SessionStart"},
		Lifecycle:    agent.LifecycleSessionStart,
		EmitsStatus:  []agent.Status{agent.StatusIdle},
		Description:  "Codex session started",
	},
	{
		PurdexName:   "PdxUserPromptSubmit",
		UpstreamKeys: []string{"UserPromptSubmit"},
		Lifecycle:    agent.LifecycleUserPromptSubmit,
		EmitsStatus:  []agent.Status{agent.StatusRunning},
		Description:  "User submitted a prompt",
	},
	{
		PurdexName:   "PdxSubagentStart",
		UpstreamKeys: []string{"SubagentStart"},
		Lifecycle:    agent.LifecycleSubagentStart,
		EmitsStatus:  []agent.Status{},
		Description:  "Nested sub-agent task dispatched",
		FutureOnly:   true,
	},
	{
		PurdexName:   "PdxSubagentStop",
		UpstreamKeys: []string{"SubagentStop"},
		Lifecycle:    agent.LifecycleSubagentStop,
		EmitsStatus:  []agent.Status{},
		Description:  "Nested sub-agent task completed",
		FutureOnly:   true,
	},
	{
		PurdexName:   "PdxStop",
		UpstreamKeys: []string{"Stop"},
		Lifecycle:    agent.LifecycleStop,
		EmitsStatus:  []agent.Status{agent.StatusIdle},
		Description:  "Agent finished responding and is idle",
	},
	{
		// Retired (#1159): codex never fires StopFailure. Handling is
		// explicit so the installer skips it; EmitsStatus + the
		// DeriveStatus case are retained (spec §2.1 / §2.5).
		PurdexName:   "PdxStopFailure",
		UpstreamKeys: []string{"StopFailure"},
		Lifecycle:    agent.LifecycleStopFailure,
		EmitsStatus:  []agent.Status{agent.StatusError},
		Description:  "Retired: not a codex hook event since 0.153",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		// Retired (#1159): codex never fires Notification. Same policy
		// as PdxStopFailure above.
		PurdexName:   "PdxNotification",
		UpstreamKeys: []string{"Notification"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusWaiting, agent.StatusIdle},
		Description:  "Retired: not a codex hook event since 0.153",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxPermissionRequest",
		UpstreamKeys: []string{"PermissionRequest"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusWaiting},
		Description:  "Tool permission request awaiting user approval",
	},
	{
		PurdexName:   "PdxSessionEnd",
		UpstreamKeys: []string{"SessionEnd"},
		Lifecycle:    agent.LifecycleSessionEnd,
		EmitsStatus:  []agent.Status{agent.StatusClear},
		Description:  "Codex session ended",
		FutureOnly:   true,
	},
	{
		// L2: PreToolUse is the codex non-prompt turn attach trigger
		// (per spec §3.3.C strategy a). Lifecycle moves from None to
		// UserPromptSubmit so applyFrameEvent's new lifecycle case picks
		// it up; Handling is omitted so EffectiveHookHandling resolves to
		// HookHandlingDetail (empty EmitsStatus → detail). FutureOnly=true
		// keeps CheckHooks tolerant of legacy users who installed before
		// the L2 catalog expansion (mirrors the SubagentStart/Stop /
		// SessionEnd / StopFailure / Notification expansion gating).
		PurdexName:   "PdxPreToolUse",
		UpstreamKeys: []string{"PreToolUse"},
		Lifecycle:    agent.LifecycleUserPromptSubmit,
		EmitsStatus:  []agent.Status{},
		Description:  "Tool call about to execute",
		FutureOnly:   true,
	},
	{
		// 0.153: PostToolUse fires after every tool call, including the
		// first one after a granted PermissionRequest, so it is the hook
		// that moves the light waiting → running (mirrors cc W6-1a).
		PurdexName:   "PdxPostToolUse",
		UpstreamKeys: []string{"PostToolUse"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusRunning},
		Description:  "Tool call completed (signals running after permission grant)",
	},
	{
		// Interrupt = the user cancelled the turn (Ctrl-C). The turn is
		// over, so it shares LifecycleStop: frame_ops detaches the codex
		// broker proxy ref by turn_id exactly like PdxStop.
		PurdexName:   "PdxInterrupt",
		UpstreamKeys: []string{"Interrupt"},
		Lifecycle:    agent.LifecycleStop,
		EmitsStatus:  []agent.Status{agent.StatusIdle},
		Description:  "Turn interrupted by the user",
	},
	{
		PurdexName:   "PdxPreCompact",
		UpstreamKeys: []string{"PreCompact"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Context compaction about to start",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxPostCompact",
		UpstreamKeys: []string{"PostCompact"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Context compaction completed",
		Handling:     agent.HookHandlingIgnored,
	},
}

// Events returns a fresh defensive copy of the codex hook event catalog on
// every call.
func (p *Provider) Events() []agent.HookEventSpec {
	out := make([]agent.HookEventSpec, len(codexEventSpecs))
	for i, spec := range codexEventSpecs {
		out[i] = agent.HookEventSpec{
			PurdexName:   spec.PurdexName,
			UpstreamKeys: append([]string(nil), spec.UpstreamKeys...),
			Lifecycle:    spec.Lifecycle,
			EmitsStatus:  append([]agent.Status(nil), spec.EmitsStatus...),
			Description:  spec.Description,
			FutureOnly:   spec.FutureOnly,
			Handling:     spec.Handling,
		}
		if out[i].EmitsStatus == nil {
			out[i].EmitsStatus = []agent.Status{}
		}
	}
	return out
}

// eventNames returns the ordered installable event PurdexName list for
// installer / check iteration, derived from codexEventSpecs so there is no
// parallel SSoT.
func (p *Provider) eventNames() []string {
	return codexEventNames()
}

// codexEventNames is the package-level helper used by mergeCodexHooks (a
// free function, not a method).
func codexEventNames() []string {
	out := make([]string, 0, len(codexEventSpecs))
	for _, spec := range codexEventSpecs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		out = append(out, spec.PurdexName)
	}
	return out
}
