package opencode

import "github.com/wake/purdex/internal/agent"

// opencodeEventSpecs is the declarative hook event catalog for OpenCode. It is
// the single source of truth for CheckHooks iteration, SupportedStatuses
// derivation, and Inspector UI. The managed plugin template wires installable
// events on the JS side; ordering follows: (1) installable Purdex names per
// plan §1.4, then (2) unsupported strong hooks + Bus events, then
// (3) ignored Bus events. Total = 65 entries (8 + 20 + 37).
//
// W2 schema fields (PurdexName / UpstreamKeys / Lifecycle):
//   - Installable entries (8): PurdexName = canonical "Pdx" + PascalCase, with
//     UpstreamKeys mapping to the plugin Bus event sources that fire the entry
//     (e.g. PdxPermissionRequest is multi-source: permission.asked +
//     question.asked).
//   - Non-installable entries (57): PurdexName = "Pdx" + PascalCase(legacy
//     Name) for catalog drift tracking; UpstreamKeys = [legacy Name] (single
//     element, equal to the dotted-lowercase upstream identifier).
//   - filter conditions (type==='idle' / input.tool==='task') are NOT in
//     UpstreamKeys; those live in plugin dispatch logic per spec §2.3.
//
// The pre-W2 Name field has been removed in Phase 3 (P3-T4); daemon-internal
// lookups read PurdexName, installer/plugin boundary writes read UpstreamKeys.
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
		PurdexName:   "PdxSessionStart",
		UpstreamKeys: []string{"session.created"},
		Lifecycle:    agent.LifecycleSessionStart,
		EmitsStatus:  []agent.Status{agent.StatusIdle},
		Description:  "OpenCode session started",
	},
	{
		PurdexName:   "PdxUserPromptSubmit",
		UpstreamKeys: []string{"chat.message"},
		Lifecycle:    agent.LifecycleUserPromptSubmit,
		EmitsStatus:  []agent.Status{agent.StatusRunning},
		Description:  "User submitted a prompt",
	},
	{
		PurdexName:   "PdxSubagentStart",
		UpstreamKeys: []string{"tool.execute.before"},
		Lifecycle:    agent.LifecycleSubagentStart,
		EmitsStatus:  []agent.Status{},
		Description:  "Nested sub-agent task dispatched",
	},
	{
		PurdexName:   "PdxSubagentStop",
		UpstreamKeys: []string{"tool.execute.after"},
		Lifecycle:    agent.LifecycleSubagentStop,
		EmitsStatus:  []agent.Status{},
		Description:  "Nested sub-agent task completed",
	},
	{
		PurdexName:   "PdxPermissionRequest",
		UpstreamKeys: []string{"permission.asked", "question.asked"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{agent.StatusWaiting},
		Description:  "Tool permission request awaiting user approval",
	},
	{
		PurdexName:   "PdxStop",
		UpstreamKeys: []string{"session.status"},
		Lifecycle:    agent.LifecycleStop,
		EmitsStatus:  []agent.Status{agent.StatusIdle},
		Description:  "Agent finished responding and is idle",
	},
	{
		PurdexName:   "PdxStopFailure",
		UpstreamKeys: []string{"session.error"},
		Lifecycle:    agent.LifecycleStopFailure,
		EmitsStatus:  []agent.Status{agent.StatusError},
		Description:  "Agent stopped due to an error",
	},
	{
		PurdexName:   "PdxSessionEnd",
		UpstreamKeys: []string{"session.deleted"},
		Lifecycle:    agent.LifecycleSessionEnd,
		EmitsStatus:  []agent.Status{agent.StatusClear},
		Description:  "OpenCode session ended",
	},

	// === Unsupported (20): upstream exists, Purdex hasn't safely handled. ===
	// Strong hooks: 9 entries (1 DEAD + 4 mutator + 4 lifecycle/declarative).
	// Bus events: 6 entries (response/cleanup signals + session.status).
	// Experimental strong hooks: 5 entries.
	// Naming uses upstream key per plan §2.2 bipartite policy.

	// --- Strong hooks: lifecycle / declarative / DEAD ---
	{
		PurdexName:   "PdxPermissionAsk",
		UpstreamKeys: []string{"permission.ask"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "DEAD strong hook in v1.14.23 (no Plugin.trigger callsite)",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxConfig",
		UpstreamKeys: []string{"config"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Plugin one-shot lifecycle notification at load time",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxTool",
		UpstreamKeys: []string{"tool"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Declarative tool registration descriptor",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxAuth",
		UpstreamKeys: []string{"auth"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Plugin auth descriptor read by providers CLI",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxProvider",
		UpstreamKeys: []string{"provider"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Plugin provider descriptor read by model resolver",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxCommandExecuteBefore",
		UpstreamKeys: []string{"command.execute.before"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Slash-command execution about to start",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxToolDefinition",
		UpstreamKeys: []string{"tool.definition"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Mutator allowing override of tool description and parameters",
		Handling:     agent.HookHandlingUnsupported,
	},

	// --- Strong hooks: mutator (chat.* / shell.env) ---
	{
		PurdexName:   "PdxChatParams",
		UpstreamKeys: []string{"chat.params"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Mutator for LLM call parameters (temperature, topP, options)",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxChatHeaders",
		UpstreamKeys: []string{"chat.headers"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Mutator for outbound LLM HTTP headers",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxShellEnv",
		UpstreamKeys: []string{"shell.env"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Mutator for shell environment variables in tool execution",
		Handling:     agent.HookHandlingUnsupported,
	},

	// --- Strong hooks: experimental.* (5) ---
	{
		PurdexName:   "PdxExperimentalChatMessagesTransform",
		UpstreamKeys: []string{"experimental.chat.messages.transform"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Experimental mutator for transforming the message list",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxExperimentalChatSystemTransform",
		UpstreamKeys: []string{"experimental.chat.system.transform"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Experimental mutator for system prompt transformation",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxExperimentalSessionCompacting",
		UpstreamKeys: []string{"experimental.session.compacting"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Experimental hook fired before session compaction",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxExperimentalCompactionAutocontinue",
		UpstreamKeys: []string{"experimental.compaction.autocontinue"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Experimental hook controlling auto-continue after compaction",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxExperimentalTextComplete",
		UpstreamKeys: []string{"experimental.text.complete"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Experimental mutator for completed text part",
		Handling:     agent.HookHandlingUnsupported,
	},

	// --- Bus events: response/cleanup signals (5) ---
	// Note: session.status itself is NOT a separate events.go entry — Stop
	// (installable) is its Purdex-side mapping after the Decision 3 switch.
	// events.json busEvents has session.status → Stop (kind: installable).
	{
		PurdexName:   "PdxSessionUpdated",
		UpstreamKeys: []string{"session.updated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Session metadata updated (title, share, permission)",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxPermissionReplied",
		UpstreamKeys: []string{"permission.replied"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "User reply to a permission request",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxQuestionReplied",
		UpstreamKeys: []string{"question.replied"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "User answered a multiple-choice question prompt",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxQuestionRejected",
		UpstreamKeys: []string{"question.rejected"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "User dismissed a multiple-choice question prompt",
		Handling:     agent.HookHandlingUnsupported,
	},
	{
		PurdexName:   "PdxServerInstanceDisposed",
		UpstreamKeys: []string{"server.instance.disposed"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Server instance disposed (cleanup signal)",
		Handling:     agent.HookHandlingUnsupported,
	},

	// === Ignored (37): upstream GA but Purdex deliberately does not consume. ===
	// Naming uses upstream key per plan §2.2 bipartite policy.

	{
		PurdexName:   "PdxSessionIdle",
		UpstreamKeys: []string{"session.idle"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Deprecated upstream; superseded by session.status filter",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxSessionDiff",
		UpstreamKeys: []string{"session.diff"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "File-diff metadata after summary or revert operations",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxSessionCompacted",
		UpstreamKeys: []string{"session.compacted"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Session compaction completed",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxMessageUpdated",
		UpstreamKeys: []string{"message.updated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Message info updated (sync event, high-volume)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxMessageRemoved",
		UpstreamKeys: []string{"message.removed"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Message removed (sync event)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxMessagePartUpdated",
		UpstreamKeys: []string{"message.part.updated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Message part updated (very high-volume sync stream)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxMessagePartDelta",
		UpstreamKeys: []string{"message.part.delta"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Message text delta (highest-frequency streaming event)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxMessagePartRemoved",
		UpstreamKeys: []string{"message.part.removed"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Message part removed (sync event)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxTodoUpdated",
		UpstreamKeys: []string{"todo.updated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Todo list updated by TodoWrite tool",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxCommandExecuted",
		UpstreamKeys: []string{"command.executed"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Slash command executed (informational)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxFileEdited",
		UpstreamKeys: []string{"file.edited"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "File edited by edit/write/apply_patch tools",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxFileWatcherUpdated",
		UpstreamKeys: []string{"file.watcher.updated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Filesystem watcher reported a file change",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxLspUpdated",
		UpstreamKeys: []string{"lsp.updated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "LSP configuration updated",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxLspClientDiagnostics",
		UpstreamKeys: []string{"lsp.client.diagnostics"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "LSP client diagnostics published",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxPtyCreated",
		UpstreamKeys: []string{"pty.created"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "OpenCode internal pty created (agent bash tool)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxPtyUpdated",
		UpstreamKeys: []string{"pty.updated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "OpenCode internal pty updated",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxPtyExited",
		UpstreamKeys: []string{"pty.exited"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "OpenCode internal pty exited",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxPtyDeleted",
		UpstreamKeys: []string{"pty.deleted"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "OpenCode internal pty deleted",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxMcpToolsChanged",
		UpstreamKeys: []string{"mcp.tools.changed"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "MCP server tools list reloaded",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxMcpBrowserOpenFailed",
		UpstreamKeys: []string{"mcp.browser.open.failed"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "MCP OAuth browser open failed (fail-open)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxInstallationUpdated",
		UpstreamKeys: []string{"installation.updated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "OpenCode CLI installation updated to a new version",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxInstallationUpdateAvailable",
		UpstreamKeys: []string{"installation.update-available"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "OpenCode CLI update is available",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxIdeInstalled",
		UpstreamKeys: []string{"ide.installed"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "DEAD in v1.14.23 (defined but no publish callsite)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxVcsBranchUpdated",
		UpstreamKeys: []string{"vcs.branch.updated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "VCS branch updated for the project",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxProjectUpdated",
		UpstreamKeys: []string{"project.updated"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Project metadata updated (global-bus-only emit)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxWorktreeReady",
		UpstreamKeys: []string{"worktree.ready"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Worktree ready (global-bus-only emit)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxWorktreeFailed",
		UpstreamKeys: []string{"worktree.failed"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Worktree creation failed (global-bus-only emit)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxWorkspaceReady",
		UpstreamKeys: []string{"workspace.ready"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "DEAD in v1.14.23 (defined but no publish callsite)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxWorkspaceFailed",
		UpstreamKeys: []string{"workspace.failed"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "DEAD in v1.14.23 (defined but no publish callsite)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxWorkspaceRestore",
		UpstreamKeys: []string{"workspace.restore"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Cross-host workspace replay progress (global-bus-only)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxWorkspaceStatus",
		UpstreamKeys: []string{"workspace.status"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Workspace connectivity status (global-bus-only)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxServerConnected",
		UpstreamKeys: []string{"server.connected"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "SSE handshake artifact (not via Bus.publish)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxGlobalDisposed",
		UpstreamKeys: []string{"global.disposed"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "Global daemon shutdown signal (global-bus-only)",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxTuiPromptAppend",
		UpstreamKeys: []string{"tui.prompt.append"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "TUI command channel; Purdex is not a TUI",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxTuiCommandExecute",
		UpstreamKeys: []string{"tui.command.execute"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "TUI command execution channel",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxTuiToastShow",
		UpstreamKeys: []string{"tui.toast.show"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "TUI toast notification channel",
		Handling:     agent.HookHandlingIgnored,
	},
	{
		PurdexName:   "PdxTuiSessionSelect",
		UpstreamKeys: []string{"tui.session.select"},
		Lifecycle:    agent.LifecycleNone,
		EmitsStatus:  []agent.Status{},
		Description:  "TUI session select channel",
		Handling:     agent.HookHandlingIgnored,
	},
}

// Events returns a fresh defensive copy of the opencode hook event catalog
// on every call.
func (p *Provider) Events() []agent.HookEventSpec {
	out := make([]agent.HookEventSpec, len(opencodeEventSpecs))
	for i, spec := range opencodeEventSpecs {
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
// CheckHooks iteration.
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
		out = append(out, spec.PurdexName)
	}
	return out
}
