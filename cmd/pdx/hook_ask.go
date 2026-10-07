package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// 分流's settings-hook backstop (spec §6.6 table row 3; daemon side in
// internal/module/team/hook_observe.go). P2c's decision path calls the
// daemon only behind the hooklocks flag; this adds the few events the
// terminal-only degradation needs, bounded at hookAskTimeout and never
// printing anything (the daemon answers {} on this path):
//
//   - ungated: PreToolUse of AskUserQuestion, PermissionRequest — the
//     daemon may open a read-only card for the dialog the terminal shows;
//   - behind <data_dir>/hookasks/<agent>/<session_id> (written by the
//     daemon while such a card is open): PostToolUse, PostToolUseFailure,
//     Stop, UserPromptSubmit, SessionEnd — they close the card.
//
// So a session that never shows a dialog to a connected client pays one
// stat per frequent event and no round trip (U17's cost rule).

// hookAskTimeout bounds the forward: one attempt, no restart grace. A
// daemon that is restarting simply gets no card for this dialog.
const hookAskTimeout = 2 * time.Second

// hookAskEvent is the subset of a hook's stdin the predicate reads.
type hookAskEvent struct {
	HookEventName string          `json:"hook_event_name"`
	SessionID     string          `json:"session_id"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolUseID     string          `json:"tool_use_id"`
}

// parseHookAskEvent reads the fields; a malformed stdin is the zero value.
func parseHookAskEvent(raw json.RawMessage) hookAskEvent {
	var e hookAskEvent
	_ = json.Unmarshal(raw, &e)
	return e
}

// askFlagExists reports whether the daemon says this session has an open
// terminal-only card.
func askFlagExists(dataDir, agent, sessionID string) bool {
	if dataDir == "" || sessionID == "" {
		return false
	}
	if agent == "" {
		agent = "cc"
	}
	// Reject anything that is not one plain path element: Base would alias
	// "../victim" and "/tmp/victim" onto the real victim's flag.
	if !plainElem(agent) || !plainElem(sessionID) {
		return false
	}
	fi, err := os.Stat(filepath.Join(dataDir, "hookasks", agent, sessionID))
	return err == nil && fi.Mode().IsRegular()
}

// plainElem reports whether s is a single, non-special path element.
func plainElem(s string) bool {
	return s != "" && s != "." && s != ".." && filepath.Base(s) == s && !strings.ContainsAny(s, `/\`)
}

// askForward decides whether this event is sent to POST /api/hooks/decide
// for the terminal-only degradation (independently of P2c's lock gate).
func askForward(e hookAskEvent, flag bool) bool {
	switch e.HookEventName {
	case "PreToolUse":
		return e.ToolName == "AskUserQuestion"
	case "PermissionRequest":
		return true
	case "PostToolUse", "PostToolUseFailure", "Stop", "UserPromptSubmit", "SessionEnd":
		return flag
	}
	return false
}

// forwardHookAsk sends the event to POST /api/hooks/decide when askForward
// says so: one attempt within hookAskTimeout, the answer discarded (this
// path never carries a decision). It returns whether a request went out,
// for the tests; the hook ignores it. P2c's lock path, when it ran for the
// same event, already sent the same request — the caller skips this then.
// ctx is the hook's budget: the forward ends with it, so it never holds the
// agent past the hook's 5 s.
func forwardHookAsk(ctx context.Context, base, token, dataDir, agent string, raw json.RawMessage) bool {
	e := parseHookAskEvent(raw)
	if !askForward(e, askFlagExists(dataDir, agent, e.SessionID)) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, hookAskTimeout)
	defer cancel()
	client := daemonclient.New(base, token, daemonclient.WithStderr(io.Discard), daemonclient.WithAttemptTimeout(hookAskTimeout))
	_, _ = client.Once(ctx, http.MethodPost, "/api/hooks/decide", team.HookDecideRequest{
		Agent: agent, Event: e.HookEventName, SessionID: e.SessionID,
		ToolName: e.ToolName, ToolInput: e.ToolInput, ToolUseID: e.ToolUseID, Raw: raw,
	}, nil)
	return true
}
