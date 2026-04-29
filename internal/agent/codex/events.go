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
// every entry. Name is kept as a deprecated dev-time backfill so Phase 1's
// daemon and Phase 2/3 provider migrations still compile against legacy
// references; Phase 3 ship removes it.
var codexEventSpecs = []agent.HookEventSpec{
	{
		Name:         "SessionStart",
		PurdexName:   "PdxSessionStart",
		UpstreamKeys: []string{"SessionStart"},
		Lifecycle:    agent.LifecycleSessionStart,
		EmitsStatus:  []agent.Status{agent.StatusIdle},
		Description:  "Codex session started",
	},
	{
		Name:         "UserPromptSubmit",
		PurdexName:   "PdxUserPromptSubmit",
		UpstreamKeys: []string{"UserPromptSubmit"},
		Lifecycle:    agent.LifecycleUserPromptSubmit,
		EmitsStatus:  []agent.Status{agent.StatusRunning},
		Description:  "User submitted a prompt",
	},
	{
		Name:         "SubagentStart",
		PurdexName:   "PdxSubagentStart",
		UpstreamKeys: []string{"SubagentStart"},
		Lifecycle:    agent.LifecycleSubagentStart,
		EmitsStatus:  []agent.Status{},
		Description:  "Nested sub-agent task dispatched",
		FutureOnly:   true,
	},
	{
		Name:         "SubagentStop",
		PurdexName:   "PdxSubagentStop",
		UpstreamKeys: []string{"SubagentStop"},
		Lifecycle:    agent.LifecycleSubagentStop,
		EmitsStatus:  []agent.Status{},
		Description:  "Nested sub-agent task completed",
		FutureOnly:   true,
	},
	{
		Name:         "Stop",
		PurdexName:   "PdxStop",
		UpstreamKeys: []string{"Stop"},
		Lifecycle:    agent.LifecycleStop,
		EmitsStatus:  []agent.Status{agent.StatusIdle},
		Description:  "Agent finished responding and is idle",
	},
	{
		Name:         "StopFailure",
		PurdexName:   "PdxStopFailure",
		UpstreamKeys: []string{"StopFailure"},
		Lifecycle:    agent.LifecycleStopFailure,
		EmitsStatus:  []agent.Status{agent.StatusError},
		Description:  "Agent stopped due to an error",
		FutureOnly:   true,
	},
	{
		Name:         "Notification",
		PurdexName:   "PdxNotification",
		UpstreamKeys: []string{"Notification"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusWaiting, agent.StatusIdle},
		Description:  "Permission/elicitation/idle prompt notifications",
		FutureOnly:   true,
	},
	{
		Name:         "PermissionRequest",
		PurdexName:   "PdxPermissionRequest",
		UpstreamKeys: []string{"PermissionRequest"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusWaiting},
		Description:  "Tool permission request awaiting user approval",
	},
	{
		Name:         "SessionEnd",
		PurdexName:   "PdxSessionEnd",
		UpstreamKeys: []string{"SessionEnd"},
		Lifecycle:    agent.LifecycleSessionEnd,
		EmitsStatus:  []agent.Status{agent.StatusClear},
		Description:  "Codex session ended",
		FutureOnly:   true,
	},
	{
		Name:         "PreToolUse",
		PurdexName:   "PdxPreToolUse",
		UpstreamKeys: []string{"PreToolUse"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Tool call about to execute",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		Name:         "PostToolUse",
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
			Name:         spec.Name,
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

// eventNames returns the ordered installable event Name list for installer /
// check iteration, derived from codexEventSpecs so there is no parallel SSoT.
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
		out = append(out, spec.Name)
	}
	return out
}
