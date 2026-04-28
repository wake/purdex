package cc

import "github.com/wake/purdex/internal/agent"

// ccEventSpecs is the declarative hook event catalog for Claude Code. It is
// the single source of truth for installer iteration (mergeClaudeHooks /
// CheckHooks), SupportedStatuses derivation, and future Inspector UI.
// Ordering matches the installer's emit order and plan §1.4 cc table.
//
// EmitsStatus semantics:
//   - Detail-only events (SubagentStart/Stop) declare an empty slice — they
//     produce Valid=true with Status="" at runtime.
//   - Polymorphic events (Notification) declare the union across sub-branches
//     (permission_prompt / elicitation_dialog → Waiting; idle_prompt /
//     auth_success → Idle). Drift test pins each sub-branch separately.
//
// W2 schema fields (PurdexName / UpstreamKeys / Lifecycle) are populated for
// every entry. Name is kept as a deprecated dev-time backfill so Phase 1's
// daemon and Phase 2/3 provider migrations still compile against legacy
// references; Phase 3 ship removes it.
var ccEventSpecs = []agent.HookEventSpec{
	{
		Name:         "Setup",
		PurdexName:   "PdxSetup",
		UpstreamKeys: []string{"Setup"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Claude Code setup hook initialization",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		Name:         "SessionStart",
		PurdexName:   "PdxSessionStart",
		UpstreamKeys: []string{"SessionStart"},
		Lifecycle:    agent.LifecycleSessionStart,
		EmitsStatus:  []agent.Status{agent.StatusIdle},
		Description:  "Claude Code session started (non-compact source)",
	},
	{
		Name:         "UserPromptSubmit",
		PurdexName:   "PdxUserPromptSubmit",
		UpstreamKeys: []string{"UserPromptSubmit"},
		Lifecycle:    agent.LifecycleUserPromptSubmit,
		EmitsStatus:  []agent.Status{agent.StatusRunning},
		Description:  "User submitted a prompt to the agent",
	},
	{
		Name:         "SubagentStart",
		PurdexName:   "PdxSubagentStart",
		UpstreamKeys: []string{"SubagentStart"},
		Lifecycle:    agent.LifecycleSubagentStart,
		EmitsStatus:  []agent.Status{},
		Description:  "Nested sub-agent task dispatched",
	},
	{
		Name:         "SubagentStop",
		PurdexName:   "PdxSubagentStop",
		UpstreamKeys: []string{"SubagentStop"},
		Lifecycle:    agent.LifecycleSubagentStop,
		EmitsStatus:  []agent.Status{},
		Description:  "Nested sub-agent task completed",
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
	},
	{
		Name:         "Notification",
		PurdexName:   "PdxNotification",
		UpstreamKeys: []string{"Notification"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusWaiting, agent.StatusIdle},
		Description:  "Permission/elicitation prompt, idle prompt, or auth success",
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
		Description:  "Claude Code session ended",
	},
	{
		Name:         "UserPromptExpansion",
		PurdexName:   "PdxUserPromptExpansion",
		UpstreamKeys: []string{"UserPromptExpansion"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "User command expanded before model processing",
		Handling:     agent.HookHandlingUnsupported,
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
		Name:         "PermissionDenied",
		PurdexName:   "PdxPermissionDenied",
		UpstreamKeys: []string{"PermissionDenied"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Tool permission denied by auto mode classifier",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "PostToolUse",
		PurdexName:   "PdxPostToolUse",
		UpstreamKeys: []string{"PostToolUse"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Tool call completed successfully",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "PostToolUseFailure",
		PurdexName:   "PdxPostToolUseFailure",
		UpstreamKeys: []string{"PostToolUseFailure"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Tool call failed",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "PostToolBatch",
		PurdexName:   "PdxPostToolBatch",
		UpstreamKeys: []string{"PostToolBatch"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Parallel tool call batch resolved",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		Name:         "TaskCreated",
		PurdexName:   "PdxTaskCreated",
		UpstreamKeys: []string{"TaskCreated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Task was created",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "TaskCompleted",
		PurdexName:   "PdxTaskCompleted",
		UpstreamKeys: []string{"TaskCompleted"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Task was completed",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "TeammateIdle",
		PurdexName:   "PdxTeammateIdle",
		UpstreamKeys: []string{"TeammateIdle"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Agent team teammate is about to go idle",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "InstructionsLoaded",
		PurdexName:   "PdxInstructionsLoaded",
		UpstreamKeys: []string{"InstructionsLoaded"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Project instructions were loaded",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "ConfigChange",
		PurdexName:   "PdxConfigChange",
		UpstreamKeys: []string{"ConfigChange"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Claude Code configuration changed",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "CwdChanged",
		PurdexName:   "PdxCwdChanged",
		UpstreamKeys: []string{"CwdChanged"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Working directory changed",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "FileChanged",
		PurdexName:   "PdxFileChanged",
		UpstreamKeys: []string{"FileChanged"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Watched file changed on disk",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		Name:         "WorktreeCreate",
		PurdexName:   "PdxWorktreeCreate",
		UpstreamKeys: []string{"WorktreeCreate"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Worktree is being created",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		Name:         "WorktreeRemove",
		PurdexName:   "PdxWorktreeRemove",
		UpstreamKeys: []string{"WorktreeRemove"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Worktree is being removed",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		Name:         "PreCompact",
		PurdexName:   "PdxPreCompact",
		UpstreamKeys: []string{"PreCompact"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Context compaction is about to run",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		Name:         "PostCompact",
		PurdexName:   "PdxPostCompact",
		UpstreamKeys: []string{"PostCompact"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Context compaction completed",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "Elicitation",
		PurdexName:   "PdxElicitation",
		UpstreamKeys: []string{"Elicitation"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "MCP server requested user input",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		Name:         "ElicitationResult",
		PurdexName:   "PdxElicitationResult",
		UpstreamKeys: []string{"ElicitationResult"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "MCP elicitation response was submitted",
		Handling:     agent.HookHandlingIgnored,
	},
}

// Events returns a fresh copy of the cc hook event catalog on every call so
// consumers cannot mutate the package-internal spec slice.
func (p *Provider) Events() []agent.HookEventSpec {
	out := make([]agent.HookEventSpec, len(ccEventSpecs))
	for i, spec := range ccEventSpecs {
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
		// Ensure a detail-only spec round-trips as an explicit empty slice,
		// not nil — callers distinguish "explicitly empty" from "unknown".
		if out[i].EmitsStatus == nil {
			out[i].EmitsStatus = []agent.Status{}
		}
	}
	return out
}

// eventNames returns the ordered installable event Name list for installer /
// check iteration, derived from ccEventSpecs so there is no parallel SSoT.
func (p *Provider) eventNames() []string {
	return ccEventNames()
}

// ccEventNames is the package-level helper used by mergeClaudeHooks (a free
// function, not a method). Keeping both entry points routes through the same
// ccEventSpecs slice so the Events SSoT cannot drift.
func ccEventNames() []string {
	out := make([]string, 0, len(ccEventSpecs))
	for _, spec := range ccEventSpecs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		out = append(out, spec.Name)
	}
	return out
}
