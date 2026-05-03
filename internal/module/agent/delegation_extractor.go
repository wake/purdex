package agent

import (
	"encoding/json"
	"strings"
)

// MaxDelegationIDBytes caps both agent_id and tool_use_id to a generous
// PATH_MAX-class bound. Real cc payloads stay well under this; the cap
// limits per-event memory cost and matches the defense pattern in
// path_hint_extractor.go.
const MaxDelegationIDBytes = 256

// codexCompanionToken is the literal script filename whose presence in a
// cc Bash invocation marks the subagent as currently delegating to codex
// via codex-companion.mjs. See spec §3.3 for boundary semantics.
const codexCompanionToken = "codex-companion.mjs"

// DelegationHint describes the delegation signal extracted from a cc
// PreToolUse / PostToolUse / PostToolUseFailure raw event. Callers pair
// it with the cc SubagentRef whose ID matches AgentID.
//
// IsCodexMark is set only when PreToolUse carries a Bash command whose
// codex-companion token passes the §3.3 boundary check.
//
// IsUnmark is set on every PostToolUse / PostToolUseFailure (regardless
// of command content). Callers remove ToolUseID from the SubagentRef's
// DelegatingToolUseIDs membership list — non-codex Bash invocations that
// were never marked simply produce a no-op removal.
type DelegationHint struct {
	AgentID     string
	ToolUseID   string
	IsCodexMark bool
	IsUnmark    bool
}

// rawDelegationEvent decodes the four fields ExtractDelegationHint needs
// from a cc Bash hook payload. Field tags align with the upstream cc hook
// schema (`tool_name` / `tool_input.command` / `agent_id` / `tool_use_id`).
type rawDelegationEvent struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	AgentID   string `json:"agent_id"`
	ToolUseID string `json:"tool_use_id"`
}

// ExtractDelegationHint is a pure function. Returns (hint, true) when the
// raw cc PreToolUse / PostToolUse / PostToolUseFailure event represents a
// Bash invocation inside a Task subagent.
//
// Defenses (mirror path_hint_extractor.go):
//   - rawEvent must be < MaxRawEventBytes
//   - agent_id / tool_use_id length bounded by MaxDelegationIDBytes
//   - control / NUL chars in agent_id / tool_use_id reject the event
//     (WS-injection guard — same shape as hasControlOrNul there)
//
// Pure function: no I/O, no goroutine state, no daemon dependency.
// All behaviour fully determined by inputs.
func ExtractDelegationHint(rawEvent json.RawMessage, eventName string) (DelegationHint, bool) {
	switch eventName {
	case "PdxPreToolUse", "PdxPostToolUse", "PdxPostToolUseFailure":
	default:
		return DelegationHint{}, false
	}
	if len(rawEvent) > MaxRawEventBytes {
		return DelegationHint{}, false
	}
	var ev rawDelegationEvent
	if err := json.Unmarshal(rawEvent, &ev); err != nil {
		return DelegationHint{}, false
	}
	if ev.ToolName != "Bash" {
		return DelegationHint{}, false
	}
	if ev.AgentID == "" || ev.ToolUseID == "" {
		return DelegationHint{}, false
	}
	if len(ev.AgentID) > MaxDelegationIDBytes || len(ev.ToolUseID) > MaxDelegationIDBytes {
		return DelegationHint{}, false
	}
	if hasControlOrNul(ev.AgentID) || hasControlOrNul(ev.ToolUseID) {
		return DelegationHint{}, false
	}

	hint := DelegationHint{
		AgentID:   ev.AgentID,
		ToolUseID: ev.ToolUseID,
	}
	if eventName == "PdxPreToolUse" {
		hint.IsCodexMark = containsCodexCompanionToken(ev.ToolInput.Command)
	} else {
		// PdxPostToolUse / PdxPostToolUseFailure: caller does membership
		// removal by ToolUseID. Command is intentionally not inspected —
		// non-codex Bash unmarks are no-ops at the helper layer.
		hint.IsUnmark = true
	}
	return hint, true
}

// containsCodexCompanionToken returns true when command contains the
// literal "codex-companion.mjs" followed by a token boundary: a quote
// (`"` / `'`), whitespace (space / tab / newline), or EOL. Continuation
// bytes (alphanumeric / `.` / `/` / `_` / `-`) reject the match so
// "codex-companion.mjs.bak" and "codex-companion.mjsx" don't fire.
//
// On a rejected first occurrence the search continues — multiple
// occurrences are inspected independently. See spec §3.3.
func containsCodexCompanionToken(command string) bool {
	for i := 0; i+len(codexCompanionToken) <= len(command); {
		idx := strings.Index(command[i:], codexCompanionToken)
		if idx < 0 {
			return false
		}
		end := i + idx + len(codexCompanionToken)
		if end == len(command) {
			return true
		}
		c := command[end]
		if c == '"' || c == '\'' || c == ' ' || c == '\t' || c == '\n' {
			return true
		}
		// Continues a different token — keep searching past this rejection.
		i = i + idx + len(codexCompanionToken)
	}
	return false
}
