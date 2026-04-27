package opencode

import "github.com/wake/purdex/internal/agent"

// opencodeEventSpecs is the declarative hook event catalog for OpenCode. It is
// the single source of truth for CheckHooks iteration, SupportedStatuses
// derivation, and Inspector UI. The managed plugin template wires installable
// events on the JS side; ordering follows: (1) installable Purdex names per
// plan §1.4, then (2) unsupported strong hooks + Bus events, then
// (3) ignored Bus events. Total = 65 entries (8 + 20 + 37).
//
// Bipartite naming (plan v1.3 §2.2):
//   - Installable entries (8): Purdex normalized PascalCase Names (SessionStart,
//     UserPromptSubmit, SubagentStart, SubagentStop, PermissionRequest, Stop,
//     StopFailure, SessionEnd). Plugin template's emit('Name', ...) calls bind
//     to these.
//   - Non-installable entries (57): upstream key (dotted lowercase) as Name —
//     installer + plugin template don't consume these; Name field exists for
//     catalog completeness + upstream-version drift tracking. Mapping from
//     upstream key to event metadata lives in
//     testdata/opencode-1.14.23-events.json.
//
// Decision 3 switch (audit §3.3): Stop's upstream source key changes from
// session.idle to session.status (filtered to type==='idle') after Commit 5.
// session.idle moves from installable to ignored in this commit; session.status
// stays unsupported in events.go (Bus event Purdex doesn't currently subscribe
// at upstream-key level — Purdex's plugin will use a runtime filter inside the
// session.status handler in Commit 5; events.go non-installable classification
// is by-upstream-key, so session.status is unsupported here pending Commit 5
// template refresh).
//
// Decision 4 defer (audit §3.4): busy/retry variants of session.status are
// receive-but-no-op in plugin handler (no separate catalog entry; trigger
// conditions tracked in follow-up issue #661).
var opencodeEventSpecs = []agent.HookEventSpec{
	// === Installable Purdex Names (8) ===
	{
		Name:        "SessionStart",
		EmitsStatus: []agent.Status{agent.StatusIdle},
		Description: "OpenCode session started",
	},
	{
		Name:        "UserPromptSubmit",
		EmitsStatus: []agent.Status{agent.StatusRunning},
		Description: "User submitted a prompt",
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
		Name:        "PermissionRequest",
		EmitsStatus: []agent.Status{agent.StatusWaiting},
		Description: "Tool permission request awaiting user approval",
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
		Name:        "SessionEnd",
		EmitsStatus: []agent.Status{agent.StatusClear},
		Description: "OpenCode session ended",
	},

	// === Unsupported (20): upstream exists, Purdex hasn't safely handled. ===
	// Strong hooks: 9 entries (1 DEAD + 4 mutator + 4 lifecycle/declarative).
	// Bus events: 6 entries (response/cleanup signals + session.status).
	// Experimental strong hooks: 5 entries.
	// Naming uses upstream key per plan §2.2 bipartite policy.

	// --- Strong hooks: lifecycle / declarative / DEAD ---
	{
		Name:        "permission.ask",
		EmitsStatus: []agent.Status{},
		Description: "DEAD strong hook in v1.14.23 (no Plugin.trigger callsite)",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "config",
		EmitsStatus: []agent.Status{},
		Description: "Plugin one-shot lifecycle notification at load time",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "tool",
		EmitsStatus: []agent.Status{},
		Description: "Declarative tool registration descriptor",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "auth",
		EmitsStatus: []agent.Status{},
		Description: "Plugin auth descriptor read by providers CLI",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "provider",
		EmitsStatus: []agent.Status{},
		Description: "Plugin provider descriptor read by model resolver",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "command.execute.before",
		EmitsStatus: []agent.Status{},
		Description: "Slash-command execution about to start",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "tool.definition",
		EmitsStatus: []agent.Status{},
		Description: "Mutator allowing override of tool description and parameters",
		Handling:    agent.HookHandlingUnsupported,
	},

	// --- Strong hooks: mutator (chat.* / shell.env) ---
	{
		Name:        "chat.params",
		EmitsStatus: []agent.Status{},
		Description: "Mutator for LLM call parameters (temperature, topP, options)",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "chat.headers",
		EmitsStatus: []agent.Status{},
		Description: "Mutator for outbound LLM HTTP headers",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "shell.env",
		EmitsStatus: []agent.Status{},
		Description: "Mutator for shell environment variables in tool execution",
		Handling:    agent.HookHandlingUnsupported,
	},

	// --- Strong hooks: experimental.* (5) ---
	{
		Name:        "experimental.chat.messages.transform",
		EmitsStatus: []agent.Status{},
		Description: "Experimental mutator for transforming the message list",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "experimental.chat.system.transform",
		EmitsStatus: []agent.Status{},
		Description: "Experimental mutator for system prompt transformation",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "experimental.session.compacting",
		EmitsStatus: []agent.Status{},
		Description: "Experimental hook fired before session compaction",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "experimental.compaction.autocontinue",
		EmitsStatus: []agent.Status{},
		Description: "Experimental hook controlling auto-continue after compaction",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "experimental.text.complete",
		EmitsStatus: []agent.Status{},
		Description: "Experimental mutator for completed text part",
		Handling:    agent.HookHandlingUnsupported,
	},

	// --- Bus events: response/cleanup signals (5) ---
	// Note: session.status itself is NOT a separate events.go entry — Stop
	// (installable) is its Purdex-side mapping after the Decision 3 switch.
	// events.json busEvents has session.status → Stop (kind: installable).
	{
		Name:        "session.updated",
		EmitsStatus: []agent.Status{},
		Description: "Session metadata updated (title, share, permission)",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "permission.replied",
		EmitsStatus: []agent.Status{},
		Description: "User reply to a permission request",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "question.replied",
		EmitsStatus: []agent.Status{},
		Description: "User answered a multiple-choice question prompt",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "question.rejected",
		EmitsStatus: []agent.Status{},
		Description: "User dismissed a multiple-choice question prompt",
		Handling:    agent.HookHandlingUnsupported,
	},
	{
		Name:        "server.instance.disposed",
		EmitsStatus: []agent.Status{},
		Description: "Server instance disposed (cleanup signal)",
		Handling:    agent.HookHandlingUnsupported,
	},

	// === Ignored (37): upstream GA but Purdex deliberately does not consume. ===
	// Naming uses upstream key per plan §2.2 bipartite policy.

	{
		Name:        "session.idle",
		EmitsStatus: []agent.Status{},
		Description: "Deprecated upstream; superseded by session.status filter",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "session.diff",
		EmitsStatus: []agent.Status{},
		Description: "File-diff metadata after summary or revert operations",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "session.compacted",
		EmitsStatus: []agent.Status{},
		Description: "Session compaction completed",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "message.updated",
		EmitsStatus: []agent.Status{},
		Description: "Message info updated (sync event, high-volume)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "message.removed",
		EmitsStatus: []agent.Status{},
		Description: "Message removed (sync event)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "message.part.updated",
		EmitsStatus: []agent.Status{},
		Description: "Message part updated (very high-volume sync stream)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "message.part.delta",
		EmitsStatus: []agent.Status{},
		Description: "Message text delta (highest-frequency streaming event)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "message.part.removed",
		EmitsStatus: []agent.Status{},
		Description: "Message part removed (sync event)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "todo.updated",
		EmitsStatus: []agent.Status{},
		Description: "Todo list updated by TodoWrite tool",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "command.executed",
		EmitsStatus: []agent.Status{},
		Description: "Slash command executed (informational)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "file.edited",
		EmitsStatus: []agent.Status{},
		Description: "File edited by edit/write/apply_patch tools",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "file.watcher.updated",
		EmitsStatus: []agent.Status{},
		Description: "Filesystem watcher reported a file change",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "lsp.updated",
		EmitsStatus: []agent.Status{},
		Description: "LSP configuration updated",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "lsp.client.diagnostics",
		EmitsStatus: []agent.Status{},
		Description: "LSP client diagnostics published",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "pty.created",
		EmitsStatus: []agent.Status{},
		Description: "OpenCode internal pty created (agent bash tool)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "pty.updated",
		EmitsStatus: []agent.Status{},
		Description: "OpenCode internal pty updated",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "pty.exited",
		EmitsStatus: []agent.Status{},
		Description: "OpenCode internal pty exited",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "pty.deleted",
		EmitsStatus: []agent.Status{},
		Description: "OpenCode internal pty deleted",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "mcp.tools.changed",
		EmitsStatus: []agent.Status{},
		Description: "MCP server tools list reloaded",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "mcp.browser.open.failed",
		EmitsStatus: []agent.Status{},
		Description: "MCP OAuth browser open failed (fail-open)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "installation.updated",
		EmitsStatus: []agent.Status{},
		Description: "OpenCode CLI installation updated to a new version",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "installation.update-available",
		EmitsStatus: []agent.Status{},
		Description: "OpenCode CLI update is available",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "ide.installed",
		EmitsStatus: []agent.Status{},
		Description: "DEAD in v1.14.23 (defined but no publish callsite)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "vcs.branch.updated",
		EmitsStatus: []agent.Status{},
		Description: "VCS branch updated for the project",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "project.updated",
		EmitsStatus: []agent.Status{},
		Description: "Project metadata updated (global-bus-only emit)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "worktree.ready",
		EmitsStatus: []agent.Status{},
		Description: "Worktree ready (global-bus-only emit)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "worktree.failed",
		EmitsStatus: []agent.Status{},
		Description: "Worktree creation failed (global-bus-only emit)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "workspace.ready",
		EmitsStatus: []agent.Status{},
		Description: "DEAD in v1.14.23 (defined but no publish callsite)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "workspace.failed",
		EmitsStatus: []agent.Status{},
		Description: "DEAD in v1.14.23 (defined but no publish callsite)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "workspace.restore",
		EmitsStatus: []agent.Status{},
		Description: "Cross-host workspace replay progress (global-bus-only)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "workspace.status",
		EmitsStatus: []agent.Status{},
		Description: "Workspace connectivity status (global-bus-only)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "server.connected",
		EmitsStatus: []agent.Status{},
		Description: "SSE handshake artifact (not via Bus.publish)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "global.disposed",
		EmitsStatus: []agent.Status{},
		Description: "Global daemon shutdown signal (global-bus-only)",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "tui.prompt.append",
		EmitsStatus: []agent.Status{},
		Description: "TUI command channel; Purdex is not a TUI",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "tui.command.execute",
		EmitsStatus: []agent.Status{},
		Description: "TUI command execution channel",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "tui.toast.show",
		EmitsStatus: []agent.Status{},
		Description: "TUI toast notification channel",
		Handling:    agent.HookHandlingIgnored,
	},
	{
		Name:        "tui.session.select",
		EmitsStatus: []agent.Status{},
		Description: "TUI session select channel",
		Handling:    agent.HookHandlingIgnored,
	},
}

// Events returns a fresh defensive copy of the opencode hook event catalog
// on every call.
func (p *Provider) Events() []agent.HookEventSpec {
	out := make([]agent.HookEventSpec, len(opencodeEventSpecs))
	for i, spec := range opencodeEventSpecs {
		out[i] = agent.HookEventSpec{
			Name:        spec.Name,
			EmitsStatus: append([]agent.Status(nil), spec.EmitsStatus...),
			Description: spec.Description,
			FutureOnly:  spec.FutureOnly,
			Handling:    spec.Handling,
		}
		if out[i].EmitsStatus == nil {
			out[i].EmitsStatus = []agent.Status{}
		}
	}
	return out
}

// eventNames returns the ordered installable event Name list for CheckHooks iteration.
func (p *Provider) eventNames() []string {
	return opencodeEventNames()
}

// opencodeEventNames is the package-level helper. Kept in parallel with the
// provider-method form for parity with cc/codex; any future free function in
// this package routes through the same spec slice.
func opencodeEventNames() []string {
	out := make([]string, 0, len(opencodeEventSpecs))
	for _, spec := range opencodeEventSpecs {
		if !agent.IsInstallableHookSpec(spec) {
			continue
		}
		out = append(out, spec.Name)
	}
	return out
}
