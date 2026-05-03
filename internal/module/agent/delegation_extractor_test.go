package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// mkBashRaw builds a cc Bash tool hook payload. Pass empty string for any of
// agentID / toolUseID to exercise the omission paths in spec §6.1.
func mkBashRaw(toolName, command, agentID, toolUseID string) json.RawMessage {
	payload := map[string]any{
		"tool_name": toolName,
		"tool_input": map[string]any{
			"command": command,
		},
	}
	if agentID != "" {
		payload["agent_id"] = agentID
	}
	if toolUseID != "" {
		payload["tool_use_id"] = toolUseID
	}
	b, _ := json.Marshal(payload)
	return b
}

// mkRawWithControlAgentID hand-assembles a JSON payload whose agent_id
// contains a literal NUL byte, so the extractor's control-char guard is
// exercised on a real attacker shape (json.Marshal would escape NUL via map).
func mkRawWithControlAgentID() json.RawMessage {
	const prefix = `{"tool_name":"Bash","tool_input":{"command":"node codex-companion.mjs task"},"agent_id":"agent-`
	const suffix = `-X","tool_use_id":"toolUse-Y"}`
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteByte(0x00) // NUL
	b.WriteString(suffix)
	return json.RawMessage(b.String())
}

// TestExtractDelegationHint_Cases mirrors spec §6.1 — table-driven cc
// PreToolUse / PostToolUse / PostToolUseFailure cases plus defenses
// (size cap, malformed JSON, control-char rejection).
func TestExtractDelegationHint_Cases(t *testing.T) {
	const canonicalCmd = `node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" task --background "..."`
	const barePathCmd = `node /path/codex-companion.mjs review`
	const singleQuotedCmd = `node 'codex-companion.mjs' task`
	const continuationCmd = `node codex-companion.mjs.bak task`
	const nonCodexCmd = `pnpm build`

	// Build a >MaxRawEventBytes payload by stuffing command field.
	bigCmd := strings.Repeat("a", MaxRawEventBytes+1024)
	bigRaw := mkBashRaw("Bash", bigCmd, "agent-X", "toolUse-Y")

	cases := []struct {
		name        string
		raw         json.RawMessage
		eventName   string
		wantOK      bool
		wantAgent   string
		wantToolUse string
		wantMark    bool
		wantUnmark  bool
	}{
		{
			name:        "PreToolUse Bash canonical quoted command",
			raw:         mkBashRaw("Bash", canonicalCmd, "agent-X", "toolUse-Y"),
			eventName:   "PdxPreToolUse",
			wantOK:      true,
			wantAgent:   "agent-X",
			wantToolUse: "toolUse-Y",
			wantMark:    true,
			wantUnmark:  false,
		},
		{
			name:        "PreToolUse Bash bare path command",
			raw:         mkBashRaw("Bash", barePathCmd, "agent-X", "toolUse-Y"),
			eventName:   "PdxPreToolUse",
			wantOK:      true,
			wantAgent:   "agent-X",
			wantToolUse: "toolUse-Y",
			wantMark:    true,
			wantUnmark:  false,
		},
		{
			name:        "PreToolUse Bash single-quoted command",
			raw:         mkBashRaw("Bash", singleQuotedCmd, "agent-X", "toolUse-Y"),
			eventName:   "PdxPreToolUse",
			wantOK:      true,
			wantAgent:   "agent-X",
			wantToolUse: "toolUse-Y",
			wantMark:    true,
			wantUnmark:  false,
		},
		{
			name:        "PreToolUse Bash token-continuation .bak",
			raw:         mkBashRaw("Bash", continuationCmd, "agent-X", "toolUse-Y"),
			eventName:   "PdxPreToolUse",
			wantOK:      true,
			wantAgent:   "agent-X",
			wantToolUse: "toolUse-Y",
			wantMark:    false,
			wantUnmark:  false,
		},
		{
			name:        "PreToolUse Bash non-codex command",
			raw:         mkBashRaw("Bash", nonCodexCmd, "agent-X", "toolUse-Y"),
			eventName:   "PdxPreToolUse",
			wantOK:      true,
			wantAgent:   "agent-X",
			wantToolUse: "toolUse-Y",
			wantMark:    false,
			wantUnmark:  false,
		},
		{
			name:      "PreToolUse non-Bash tool (Read)",
			raw:       mkBashRaw("Read", canonicalCmd, "agent-X", "toolUse-Y"),
			eventName: "PdxPreToolUse",
			wantOK:    false,
		},
		{
			name:      "PreToolUse Bash without agent_id (top-level cc)",
			raw:       mkBashRaw("Bash", canonicalCmd, "", "toolUse-Y"),
			eventName: "PdxPreToolUse",
			wantOK:    false,
		},
		{
			name:      "PreToolUse Bash without tool_use_id",
			raw:       mkBashRaw("Bash", canonicalCmd, "agent-X", ""),
			eventName: "PdxPreToolUse",
			wantOK:    false,
		},
		{
			name:        "PostToolUse Bash canonical command",
			raw:         mkBashRaw("Bash", canonicalCmd, "agent-X", "toolUse-Y"),
			eventName:   "PdxPostToolUse",
			wantOK:      true,
			wantAgent:   "agent-X",
			wantToolUse: "toolUse-Y",
			wantMark:    false,
			wantUnmark:  true,
		},
		{
			name:        "PostToolUse Bash non-codex command",
			raw:         mkBashRaw("Bash", nonCodexCmd, "agent-X", "toolUse-Y"),
			eventName:   "PdxPostToolUse",
			wantOK:      true,
			wantAgent:   "agent-X",
			wantToolUse: "toolUse-Y",
			wantMark:    false,
			wantUnmark:  true,
		},
		{
			name:        "PostToolUseFailure Bash canonical command",
			raw:         mkBashRaw("Bash", canonicalCmd, "agent-X", "toolUse-Y"),
			eventName:   "PdxPostToolUseFailure",
			wantOK:      true,
			wantAgent:   "agent-X",
			wantToolUse: "toolUse-Y",
			wantMark:    false,
			wantUnmark:  true,
		},
		{
			name:      "raw event over MaxRawEventBytes cap",
			raw:       bigRaw,
			eventName: "PdxPreToolUse",
			wantOK:    false,
		},
		{
			name:      "malformed JSON",
			raw:       json.RawMessage(`{"tool_name":`),
			eventName: "PdxPreToolUse",
			wantOK:    false,
		},
		{
			name:      "control char in agent_id (NUL)",
			raw:       mkRawWithControlAgentID(),
			eventName: "PdxPreToolUse",
			wantOK:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint, ok := ExtractDelegationHint(tc.raw, tc.eventName)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (hint=%+v)", ok, tc.wantOK, hint)
			}
			if !ok {
				return
			}
			if hint.AgentID != tc.wantAgent {
				t.Errorf("AgentID = %q, want %q", hint.AgentID, tc.wantAgent)
			}
			if hint.ToolUseID != tc.wantToolUse {
				t.Errorf("ToolUseID = %q, want %q", hint.ToolUseID, tc.wantToolUse)
			}
			if hint.IsCodexMark != tc.wantMark {
				t.Errorf("IsCodexMark = %v, want %v", hint.IsCodexMark, tc.wantMark)
			}
			if hint.IsUnmark != tc.wantUnmark {
				t.Errorf("IsUnmark = %v, want %v", hint.IsUnmark, tc.wantUnmark)
			}
		})
	}
}

// TestContainsCodexCompanionToken_Cases mirrors spec §3.3 — token-boundary
// rule: next byte after `codex-companion.mjs` must be quote, whitespace, or
// EOL. Continuation bytes (alnum / `.` / `/` / `_` / `-`) reject.
func TestContainsCodexCompanionToken_Cases(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{
			name: "canonical quoted plugin path",
			cmd:  `node "${CLAUDE_PLUGIN_ROOT}/scripts/codex-companion.mjs" task --background "..."`,
			want: true,
		},
		{
			name: "bare path with space boundary",
			cmd:  `node ~/.claude/plugins/.../codex-companion.mjs review --base main`,
			want: true,
		},
		{
			name: "double-quoted full subcommand",
			cmd:  `bash -c "node /custom/codex-companion.mjs adversarial-review focus"`,
			want: true,
		},
		{
			name: "single-quoted script name",
			cmd:  `node 'codex-companion.mjs' task`,
			want: true,
		},
		{
			name: "shell substitution hides the literal token",
			cmd:  `node $(./find-script).mjs task`,
			want: false,
		},
		{
			name: "token continuation .bak",
			cmd:  `node codex-companion.mjs.bak task`,
			want: false,
		},
		{
			name: "echo false positive (next byte: quote)",
			cmd:  `echo "codex-companion.mjs"`,
			want: true,
		},
		{
			name: "grep pipe — EOF boundary",
			cmd:  `cat README | grep codex-companion.mjs`,
			want: true,
		},
		{
			name: "git log mjsx continuation",
			cmd:  `git log --grep=codex-companion.mjsx`,
			want: false,
		},
		{
			name: "newline boundary",
			cmd:  "node codex-companion.mjs\nnext-line",
			want: true,
		},
		{
			name: "tab boundary",
			cmd:  "node codex-companion.mjs\ttask",
			want: true,
		},
		{
			name: "second occurrence after rejected first",
			cmd:  `node codex-companion.mjs.bak; node "codex-companion.mjs" task`,
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := containsCodexCompanionToken(tc.cmd)
			if got != tc.want {
				t.Fatalf("containsCodexCompanionToken(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}
