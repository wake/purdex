package cc

import "github.com/wake/purdex/internal/agent"

// ccEventSpecs is the declarative hook event catalog for Claude Code. It is
// the single source of truth for installer iteration (mergeClaudeHooks /
// CheckHooks), SupportedStatuses derivation, and future Inspector UI.
// Ordering matches the installer's emit order and plan §1.4 cc table.
//
// EmitsStatus semantics:
//   - Detail-only events (SubagentStart/Stop, PreToolUse, PostToolUseFailure)
//     declare an empty slice — they produce Valid=true with Status="" at
//     runtime. PreToolUse + PostToolUseFailure are routed via handler.go's
//     delegation extractor (PR #829, issue #821) to mark/unmark the
//     Delegating flag for codex-companion Bash invocations; they must stay
//     installable so cc actually fires them on fresh installs.
//   - Polymorphic events (Notification) declare the union across sub-branches
//     (permission_prompt / elicitation_dialog → Waiting; idle_prompt /
//     auth_success → Idle). Drift test pins each sub-branch separately.
//
// W2 schema fields (PurdexName / UpstreamKeys / Lifecycle) are populated for
// every entry. The pre-W2 Name field has been removed in Phase 3 (P3-T4);
// daemon-internal lookups read PurdexName, installer/plugin boundary writes
// read UpstreamKeys.
var ccEventSpecs = []agent.HookEventSpec{
	{
		PurdexName:   "PdxSetup",
		UpstreamKeys: []string{"Setup"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Claude Code setup hook initialization",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxSessionStart",
		UpstreamKeys: []string{"SessionStart"},
		Lifecycle:    agent.LifecycleSessionStart,
		EmitsStatus:  []agent.Status{agent.StatusIdle},
		Description:  "Claude Code session started (non-compact source)",
	},
	{
		PurdexName:   "PdxUserPromptSubmit",
		UpstreamKeys: []string{"UserPromptSubmit"},
		Lifecycle:    agent.LifecycleUserPromptSubmit,
		EmitsStatus:  []agent.Status{agent.StatusRunning},
		Description:  "User submitted a prompt to the agent",
	},
	{
		PurdexName:   "PdxSubagentStart",
		UpstreamKeys: []string{"SubagentStart"},
		Lifecycle:    agent.LifecycleSubagentStart,
		EmitsStatus:  []agent.Status{},
		Description:  "Nested sub-agent task dispatched",
	},
	{
		PurdexName:   "PdxSubagentStop",
		UpstreamKeys: []string{"SubagentStop"},
		Lifecycle:    agent.LifecycleSubagentStop,
		EmitsStatus:  []agent.Status{},
		Description:  "Nested sub-agent task completed",
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
	},
	{
		PurdexName:   "PdxNotification",
		UpstreamKeys: []string{"Notification"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusWaiting, agent.StatusIdle},
		Description:  "Permission/elicitation prompt, idle prompt, or auth success",
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
		Description:  "Claude Code session ended",
	},
	{
		PurdexName:   "PdxUserPromptExpansion",
		UpstreamKeys: []string{"UserPromptExpansion"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "User command expanded before model processing",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxPreToolUse",
		UpstreamKeys: []string{"PreToolUse"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Tool call about to execute",
	},
	{
		PurdexName:   "PdxPermissionDenied",
		UpstreamKeys: []string{"PermissionDenied"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Tool permission denied by auto mode classifier",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxPostToolUse",
		UpstreamKeys: []string{"PostToolUse"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusRunning},
		Description:  "Tool call completed successfully (signals running after permission grant)",
	},
	{
		PurdexName:   "PdxPostToolUseFailure",
		UpstreamKeys: []string{"PostToolUseFailure"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Tool call failed",
	},
	{
		PurdexName:   "PdxPostToolBatch",
		UpstreamKeys: []string{"PostToolBatch"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Parallel tool call batch resolved",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxTaskCreated",
		UpstreamKeys: []string{"TaskCreated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Task was created",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxTaskCompleted",
		UpstreamKeys: []string{"TaskCompleted"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Task was completed",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxTeammateIdle",
		UpstreamKeys: []string{"TeammateIdle"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Agent team teammate is about to go idle",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxInstructionsLoaded",
		UpstreamKeys: []string{"InstructionsLoaded"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Project instructions were loaded",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxConfigChange",
		UpstreamKeys: []string{"ConfigChange"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Claude Code configuration changed",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxCwdChanged",
		UpstreamKeys: []string{"CwdChanged"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Working directory changed",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxFileChanged",
		UpstreamKeys: []string{"FileChanged"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Watched file changed on disk",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxWorktreeCreate",
		UpstreamKeys: []string{"WorktreeCreate"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Worktree is being created",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxWorktreeRemove",
		UpstreamKeys: []string{"WorktreeRemove"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Worktree is being removed",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxPreCompact",
		UpstreamKeys: []string{"PreCompact"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Context compaction is about to run",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxPostCompact",
		UpstreamKeys: []string{"PostCompact"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Context compaction completed",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxElicitation",
		UpstreamKeys: []string{"Elicitation"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "MCP server requested user input",
		Handling:     agent.HookHandlingIgnored,
	},
	{
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

// eventNames returns the ordered installable event PurdexName list for
// installer / check iteration, derived from ccEventSpecs so there is no
// parallel SSoT.
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
		out = append(out, spec.PurdexName)
	}
	return out
}
