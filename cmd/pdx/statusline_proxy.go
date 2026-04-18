package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/wake/purdex/internal/config"
)

// readStdinWithTimeout reads the entire stdin, returning []byte("{}") if empty
// or on any read error. The timeoutSec parameter bounds total read time.
func readStdinWithTimeout(r io.Reader, timeoutSec int) []byte {
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		ch <- result{data, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil || len(res.data) == 0 {
			return []byte("{}")
		}
		return res.data
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		return []byte("{}")
	}
}

// renderMinimal builds the default single-line status for CC to display when
// no --inner command is configured. Fields absent from raw are silently
// omitted; all format errors fall back to "[pdx]".
func renderMinimal(raw json.RawMessage) string {
	var s struct {
		Model struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"model"`
		Context struct {
			UsedPct *float64 `json:"used_percentage"`
		} `json:"context_window"`
		Cost struct {
			TotalUSD *float64 `json:"total_cost_usd"`
		} `json:"cost"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return "[pdx]"
	}
	parts := []string{"[pdx]"}
	model := s.Model.DisplayName
	if model == "" {
		model = s.Model.ID
	}
	if model != "" {
		parts = append(parts, model)
	}
	if s.Context.UsedPct != nil {
		parts = append(parts, fmt.Sprintf("ctx %.0f%%", *s.Context.UsedPct))
	}
	if s.Cost.TotalUSD != nil {
		parts = append(parts, fmt.Sprintf("$%.2f", *s.Cost.TotalUSD))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	out := parts[0] + " " + parts[1]
	for _, p := range parts[2:] {
		out += " · " + p
	}
	return out
}

// parseInnerFlag extracts the value following "--inner" from args.
// Returns "" when absent.
func parseInnerFlag(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--inner" {
			return args[i+1]
		}
	}
	return ""
}

// execInner runs the user-supplied inner command via `sh -c`, feeding stdinJSON
// to its stdin. The inner command's stdout is captured and returned; stderr
// and non-zero exit codes are ignored. timeoutSec caps total execution.
func execInner(inner string, stdinJSON []byte, timeoutSec int) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", inner)
	cmd.Stdin = bytes.NewReader(stdinJSON)
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	return out.String()
}

type statuslinePayload struct {
	TmuxSession string          `json:"tmux_session"`
	AgentType   string          `json:"agent_type"`
	RawStatus   json.RawMessage `json:"raw_status"`
}

// postStatus synchronously POSTs the payload to the daemon with a 2s timeout.
// Returns error on any failure; caller swallows errors silently.
func postStatus(url, token string, payload statuslinePayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("daemon returned %d", resp.StatusCode)
	}
	return nil
}

// resolveDaemonHost rewrites wildcard bind addresses (0.0.0.0, ::, empty) to
// 127.0.0.1 so the statusline-proxy can POST to the daemon on the loopback.
// A daemon bound to 0.0.0.0 is listening on all interfaces including loopback,
// but 0.0.0.0 itself is not a valid connection target on macOS/Linux.
func resolveDaemonHost(bind string) string {
	switch bind {
	case "", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	}
	return bind
}

// runStatuslineProxy is the entry point for `pdx statusline-proxy [--inner "<cmd>"]`.
func runStatuslineProxy(args []string) {
	inner := parseInnerFlag(args)
	raw := readStdinWithTimeout(os.Stdin, 5)

	// 1) Print to CC (never blocks on POST)
	if inner != "" {
		fmt.Print(execInner(inner, raw, 2))
	} else {
		fmt.Println(renderMinimal(raw))
	}

	// 2) Synchronously POST to daemon; silent fail.
	tmuxSession := queryTmuxSession() // defined in cmd/pdx/hook.go
	cfg, err := config.Load("")
	url := "http://127.0.0.1:7860/api/agent/status"
	var token string
	if err == nil {
		url = fmt.Sprintf("http://%s:%d/api/agent/status", resolveDaemonHost(cfg.Bind), cfg.Port)
		token = cfg.Token
	}
	_ = postStatus(url, token, statuslinePayload{
		TmuxSession: tmuxSession,
		AgentType:   "cc",
		RawStatus:   raw,
	})
}
