package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
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

// runStatuslineProxy is the entry point for `pdx statusline-proxy [--inner "<cmd>"]`.
// Full implementation added in later tasks; stub for now.
func runStatuslineProxy(args []string) {
	inner := parseInnerFlag(args)
	raw := readStdinWithTimeout(os.Stdin, 5)

	if inner != "" {
		fmt.Print(execInner(inner, raw, 2))
	} else {
		fmt.Println(renderMinimal(raw))
	}
	os.Exit(0)
}
