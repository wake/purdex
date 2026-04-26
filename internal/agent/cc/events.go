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
var ccEventSpecs = []agent.HookEventSpec{
	{
		Name:        "SessionStart",
		EmitsStatus: []agent.Status{agent.StatusIdle},
		Description: "Claude Code session started (non-compact source)",
	},
	{
		Name:        "UserPromptSubmit",
		EmitsStatus: []agent.Status{agent.StatusRunning},
		Description: "User submitted a prompt to the agent",
	},
	{
		Name:        "SubagentStart",
		EmitsStatus: []agent.Status{},
		Description: "Nested sub-agent task dispatched",
	},
	{
		Name:        "SubagentStop",
		EmitsStatus: []agent.Status{},
		Description: "Nested sub-agent task completed",
	},
	{
		Name:        "Stop",
		EmitsStatus: []agent.Status{agent.StatusIdle},
		Description: "Agent finished responding and is idle",
	},
	{
		Name:        "StopFailure",
		EmitsStatus: []agent.Status{agent.StatusError},
		Description: "Agent stopped due to an error",
	},
	{
		Name:        "Notification",
		EmitsStatus: []agent.Status{agent.StatusWaiting, agent.StatusIdle},
		Description: "Permission/elicitation prompt, idle prompt, or auth success",
	},
	{
		Name:        "PermissionRequest",
		EmitsStatus: []agent.Status{agent.StatusWaiting},
		Description: "Tool permission request awaiting user approval",
	},
	{
		Name:        "SessionEnd",
		EmitsStatus: []agent.Status{agent.StatusClear},
		Description: "Claude Code session ended",
	},
	{
		Name:        "UserPromptExpansion",
		EmitsStatus: []agent.Status{},
		Description: "User command expanded before model processing",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "PreToolUse",
		EmitsStatus: []agent.Status{},
		Description: "Tool call about to execute",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "PermissionDenied",
		EmitsStatus: []agent.Status{},
		Description: "Tool permission denied by auto mode classifier",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "PostToolUse",
		EmitsStatus: []agent.Status{},
		Description: "Tool call completed successfully",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "PostToolUseFailure",
		EmitsStatus: []agent.Status{},
		Description: "Tool call failed",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "PostToolBatch",
		EmitsStatus: []agent.Status{},
		Description: "Parallel tool call batch resolved",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "TaskCreated",
		EmitsStatus: []agent.Status{},
		Description: "Task was created",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "TaskCompleted",
		EmitsStatus: []agent.Status{},
		Description: "Task was completed",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "TeammateIdle",
		EmitsStatus: []agent.Status{},
		Description: "Agent team teammate is about to go idle",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "InstructionsLoaded",
		EmitsStatus: []agent.Status{},
		Description: "Project instructions were loaded",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "ConfigChange",
		EmitsStatus: []agent.Status{},
		Description: "Claude Code configuration changed",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "CwdChanged",
		EmitsStatus: []agent.Status{},
		Description: "Working directory changed",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "FileChanged",
		EmitsStatus: []agent.Status{},
		Description: "Watched file changed on disk",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "WorktreeCreate",
		EmitsStatus: []agent.Status{},
		Description: "Worktree is being created",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "WorktreeRemove",
		EmitsStatus: []agent.Status{},
		Description: "Worktree is being removed",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "PreCompact",
		EmitsStatus: []agent.Status{},
		Description: "Context compaction is about to run",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "PostCompact",
		EmitsStatus: []agent.Status{},
		Description: "Context compaction completed",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "Elicitation",
		EmitsStatus: []agent.Status{},
		Description: "MCP server requested user input",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "ElicitationResult",
		EmitsStatus: []agent.Status{},
		Description: "MCP elicitation response was submitted",
		Handling:    agent.HookHandlingIgnored,
	},
}

// Events returns a fresh copy of the cc hook event catalog on every call so
// consumers cannot mutate the package-internal spec slice.
func (p *Provider) Events() []agent.HookEventSpec {
	out := make([]agent.HookEventSpec, len(ccEventSpecs))
	for i, spec := range ccEventSpecs {
		out[i] = agent.HookEventSpec{
			Name:        spec.Name,
			EmitsStatus: append([]agent.Status(nil), spec.EmitsStatus...),
			Description: spec.Description,
			FutureOnly:  spec.FutureOnly,
			Handling:    spec.Handling,
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
