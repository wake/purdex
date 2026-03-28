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

	"github.com/wake/tmux-box/internal/config"
)

type hookPayload struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
}

// runHook is the entry point for `tbox hook <event_name>`.
// It reads stdin, queries tmux for the session name, and POSTs to the daemon.
// Any error exits 0 silently — this MUST NOT break CC.
func runHook(args []string) {
	if len(args) < 1 {
		os.Exit(0)
	}
	eventName := args[0]

	tmuxSession := queryTmuxSession()
	payload := buildHookPayload(tmuxSession, eventName, os.Stdin)

	cfg, err := config.Load("")
	var url, token string
	if err != nil {
		url = "http://127.0.0.1:7860/api/agent/event"
	} else {
		url = fmt.Sprintf("http://%s:%d/api/agent/event", cfg.Bind, cfg.Port)
		token = cfg.Token
	}

	_ = postHookEvent(url, token, payload)
}

// queryTmuxSession runs `tmux display-message -p '#{session_name}'` and returns
// the session name, or "" on any error.
func queryTmuxSession() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// buildHookPayload constructs a hookPayload from the given parameters.
// If stdin is empty or cannot be read, raw_event defaults to {}.
func buildHookPayload(tmuxSession, eventName string, stdin io.Reader) hookPayload {
	raw, err := io.ReadAll(stdin)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	return hookPayload{
		TmuxSession: tmuxSession,
		EventName:   eventName,
		RawEvent:    json.RawMessage(raw),
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
