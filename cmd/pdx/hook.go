package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wake/purdex/internal/config"
)

type hookPayload struct {
	TmuxSession     string          `json:"tmux_session"`
	TmuxSessionID   string          `json:"tmux_session_id,omitempty"`
	TmuxPaneID      string          `json:"tmux_pane_id"`
	PurdexName      string          `json:"purdex_name"`
	RawEvent        json.RawMessage `json:"raw_event"`
	AgentType       string          `json:"agent_type"`
	SenderPID       int             `json:"sender_pid"`
	SenderStartTime string          `json:"sender_start_time"`
	SenderUncertain bool            `json:"sender_uncertain,omitempty"`
}

type hookProvenance struct {
	TmuxPaneID      string
	SenderPID       int
	SenderStartTime string
	SenderUncertain bool
}

var queryTmuxSessionFn = queryTmuxSession
var queryTmuxSessionInfoFn = queryTmuxSessionInfo
var queryTmuxPaneIDFn = queryTmuxPaneID
var resolveHookProvenanceFn = resolveHookProvenance
var postHookEventFn = postHookEvent
var loadConfigFn = config.Load

// runHook is the entry point for `pdx hook <PurdexName>`.
// It reads stdin, queries tmux for the session name, and POSTs to the daemon.
// Daemon/tmux/runtime failures are swallowed to avoid breaking the agent hook path.
// CLI misuse still exits non-zero so local invocation mistakes are visible.
func runHook(args []string) {
	if len(args) < 1 {
		os.Exit(0)
	}

	// Parse --agent flag manually; remaining positional arg is the PurdexName.
	var agentType string
	var positional []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--agent" && i+1 < len(args) {
			agentType = args[i+1]
			i++ // skip value
		} else {
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 1 {
		os.Exit(0)
	}
	purdexName := positional[0]

	if agentType == "" {
		fmt.Fprintf(os.Stderr, "pdx hook: --agent flag is required\n")
		os.Exit(1)
	}

	tmuxSessionID, tmuxSession := queryTmuxSessionInfoFn()
	provenance := resolveHookProvenanceFn()
	payload := buildHookPayload(tmuxSessionID, tmuxSession, purdexName, os.Stdin, agentType, provenance)

	cfg, err := loadConfigFn("")
	var url, token string
	if err != nil {
		url = "http://127.0.0.1:7860/api/agent/event"
	} else {
		url = fmt.Sprintf("http://%s:%d/api/agent/event", cfg.Bind, cfg.Port)
		token = cfg.Token
	}

	_ = postHookEventFn(url, token, payload)
}

// queryTmuxSession runs `tmux display-message -p '#{session_name}'` and returns
// the session name, or "" on any error. Retained for non-hook callers
// (cmd/pdx/statusline_proxy.go) that only need the name.
func queryTmuxSession() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// queryTmuxSessionInfo returns (sessionID, sessionName) from a single
// `tmux display-message -p '#{session_id}|#{session_name}'` call. Both empty
// on any error. The sessionID ($N format) is immutable across rename and is
// the primary key the daemon uses to bypass the name cache rename-race
// window when resolving session codes for hook events.
func queryTmuxSessionInfo() (string, string) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}|#{session_name}").Output()
	if err != nil {
		return "", ""
	}
	return parseTmuxSessionInfo(string(out))
}

// parseTmuxSessionInfo splits "<sessionID>|<sessionName>" output on the FIRST
// '|'. Names may contain '|' characters (real tmux permits them); the parser
// preserves them in the name half. When no delimiter is present we treat the
// whole string as the name and return an empty ID — the daemon falls back to
// the name path safely.
func parseTmuxSessionInfo(out string) (string, string) {
	trimmed := strings.TrimRight(out, "\n")
	idx := strings.Index(trimmed, "|")
	if idx < 0 {
		return "", trimmed
	}
	return trimmed[:idx], trimmed[idx+1:]
}

func queryTmuxPaneID() string {
	return os.Getenv("TMUX_PANE")
}

// buildHookPayload constructs a hookPayload from the given parameters.
// If stdin is empty or cannot be read, raw_event defaults to {}.
// tmuxSessionID is optional ($N format); when non-empty the daemon prefers
// it over tmuxSession for code resolution to dodge the rename-race window.
func buildHookPayload(tmuxSessionID, tmuxSession, purdexName string, stdin io.Reader, agentType string, provenance hookProvenance) hookPayload {
	raw, err := io.ReadAll(stdin)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	return hookPayload{
		TmuxSession:     tmuxSession,
		TmuxSessionID:   tmuxSessionID,
		TmuxPaneID:      provenance.TmuxPaneID,
		PurdexName:      purdexName,
		RawEvent:        json.RawMessage(raw),
		AgentType:       agentType,
		SenderPID:       provenance.SenderPID,
		SenderStartTime: provenance.SenderStartTime,
		SenderUncertain: provenance.SenderUncertain,
	}
}

// postHookEvent POSTs the payload as JSON to the given URL with a 2-second timeout.
// If token is non-empty, it is sent as a Bearer Authorization header.
func postHookEvent(url, token string, payload hookPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("daemon returned %d", resp.StatusCode)
	}
	return nil
}
