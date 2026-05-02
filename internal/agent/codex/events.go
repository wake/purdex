package codex

import "github.com/wake/purdex/internal/agent"

// codexEventSpecs is the declarative hook event catalog for Codex. It expands
// the previous 3-event installer (SessionStart, UserPromptSubmit, Stop) to the
// full 9-event set (issue #613 / plan §1.4), reaching parity with cc and with
// codex DeriveStatus, which has supported all 9 events since Phase 1.
//
// The 6 newly-added events (SubagentStart, SubagentStop, StopFailure,
// Notification, PermissionRequest, SessionEnd) may not be emitted by the
// current codex CLI in every path. Declaring them is intentional per plan §8
// risk table: it rigs the installer so proxy paths and future CLI versions
// land on ready infrastructure, and the drift test pins Events() ↔
// DeriveStatus parity either way.
//
// W2 schema fields (PurdexName / UpstreamKeys / Lifecycle) are populated for
// every entry. The pre-W2 Name field has been removed in Phase 3 (P3-T4);
// daemon-internal lookups read PurdexName, installer/plugin boundary writes
// read UpstreamKeys.
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
		PurdexName:   "PdxStopFailure",
		UpstreamKeys: []string{"StopFailure"},
		Lifecycle:    agent.LifecycleStopFailure,
		EmitsStatus:  []agent.Status{agent.StatusError},
		Description:  "Agent stopped due to an error",
		FutureOnly:   true,
	},
	{
		PurdexName:   "PdxNotification",
		UpstreamKeys: []string{"Notification"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusWaiting, agent.StatusIdle},
		Description:  "Permission/elicitation/idle prompt notifications",
		FutureOnly:   true,
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
		PurdexName:   "PdxPostToolUse",
		UpstreamKeys: []string{"PostToolUse"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Tool call completed",
		Handling:     agent.HookHandlingUnsupported,
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
