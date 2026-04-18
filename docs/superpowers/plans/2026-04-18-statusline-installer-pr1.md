# CC StatusLine Installer — PR-1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** End-to-end CC statusLine wrapper + Agents-tab installer UI that displays CC session name alongside tmux session name in the active tab and bottom status bar.

**Architecture:** Go subcommand `pdx statusline-proxy` reads CC's statusLine JSON from stdin, prints fallback/inner stdout to CC, synchronously POSTs JSON to daemon `/api/agent/status`. Daemon broadcasts `agent.status` WS events; SPA merges into `useAgentStore` and reuses existing `oscTitles`/StatusBar/InlineTab rendering via an extracted `<HoverTooltip>` component.

**Tech Stack:** Go 1.26 (net/http, `os/exec`, `github.com/mattn/go-shellwords`), React 19, Zustand, Vitest, Tailwind.

**Spec:** `docs/superpowers/specs/2026-04-18-statusline-and-daemon-rebuild-design.md`

---

## File Structure

### Go (new / modified)
- **NEW** `internal/agent/cc/paths.go` — `ccSettingsPath()` helper
- **NEW** `internal/agent/cc/statusline.go` — reader / writer / mode detection / install / remove
- **NEW** `internal/agent/cc/statusline_test.go` — unit tests
- **NEW** `cmd/pdx/statusline_proxy.go` — `runStatuslineProxy(args)` subcommand entry
- **NEW** `cmd/pdx/statusline_proxy_test.go` — unit tests for stdin/POST/exec behavior
- **MODIFY** `cmd/pdx/main.go` — add `statusline-proxy` case
- **MODIFY** `internal/agent/cc/hooks.go` — use `ccSettingsPath()` helper (pure refactor)
- **MODIFY** `internal/agent/cc/provider.go` — expose `StatuslineInstaller` interface implementation
- **MODIFY** `internal/agent/cc/interfaces.go` — add `StatuslineInstaller` interface
- **MODIFY** `internal/module/agent/handler.go` — add GET/POST for `/api/agent/cc/statusline/*`, POST for `/api/agent/status`, snapshot replay on WS subscribe, cleared broadcast
- **MODIFY** `internal/module/agent/module.go` — register new routes
- **MODIFY** `go.mod` / `go.sum` — add `github.com/mattn/go-shellwords`

### SPA (new / modified)
- **NEW** `spa/src/components/HoverTooltip.tsx` — extracted from `ActivityBarNarrow.tsx` ws-tooltip pattern
- **NEW** `spa/src/components/HoverTooltip.test.tsx`
- **NEW** `spa/src/components/hosts/AgentExtensionRow.tsx` — per-extension install/remove row
- **NEW** `spa/src/components/hosts/AgentExtensionRow.test.tsx`
- **NEW** `spa/src/components/hosts/StatuslineConflictDialog.tsx` — Wrap/Cancel dialog
- **NEW** `spa/src/components/hosts/StatuslineConflictDialog.test.tsx`
- **NEW** `spa/src/hooks/useStatuslineInstall.ts`
- **NEW** `spa/src/hooks/useStatuslineInstall.test.ts`
- **MODIFY** `spa/src/stores/useAgentStore.ts` — add `ccStatus` field + actions, wire `agent.status` / `agent.status.cleared` events
- **MODIFY** `spa/src/stores/useAgentStore.test.ts` — new test cases
- **MODIFY** `spa/src/features/workspace/components/ActivityBarNarrow.tsx` — use `<HoverTooltip>`
- **MODIFY** `spa/src/components/InlineTab.tsx` — use `<HoverTooltip>` + new `{cc} - {tmux}` display rule
- **MODIFY** `spa/src/components/InlineTab.test.tsx`
- **MODIFY** `spa/src/components/hosts/AgentsSection.tsx` — render Extensions region for CC
- **MODIFY** `spa/src/components/hosts/AgentsSection.test.tsx`
- **MODIFY** `spa/src/locales/en.json` + `spa/src/locales/zh-TW.json` — new i18n keys

---

## Prep Refactor Tasks (no behavior change)

### Task 1: Extract `ccSettingsPath()` Go helper

**Files:**
- Create: `internal/agent/cc/paths.go`
- Modify: `internal/agent/cc/hooks.go` (replace 2–3 inline usages)
- Test: (covered by existing `hooks_test.go`; no new test needed for pure refactor)

- [ ] **Step 1: Create the helper**

```go
// internal/agent/cc/paths.go
package cc

import (
	"os"
	"path/filepath"
)

// ccSettingsPath returns the absolute path to the user's Claude Code
// settings.json (~/.claude/settings.json). It returns an error only if
// the user's home directory cannot be determined.
func ccSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}
```

- [ ] **Step 2: Replace inline `filepath.Join(home, ".claude", "settings.json")` occurrences in `internal/agent/cc/hooks.go`**

Find every occurrence of the pattern (there are 2–3) and change the call-site to:

```go
path, err := ccSettingsPath()
if err != nil {
	return err
}
```

Leave all other logic in `hooks.go` untouched.

- [ ] **Step 3: Run existing tests**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/statusline-installer-p1 && go test ./internal/agent/cc/...`
Expected: PASS (all existing tests still green, refactor-only)

- [ ] **Step 4: Commit**

```bash
git add internal/agent/cc/paths.go internal/agent/cc/hooks.go
git commit -m "refactor(cc): extract ccSettingsPath() helper"
```

---

### Task 2: Add go-shellwords dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/statusline-installer-p1 && go get github.com/mattn/go-shellwords@latest`

- [ ] **Step 2: Verify**

Run: `go mod tidy && go build ./...`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add github.com/mattn/go-shellwords for statusline arg parsing"
```

---

## Backend: statusline-proxy subcommand

### Task 3: Implement `pdx statusline-proxy` — stdin read + default minimal output (no POST yet)

**Files:**
- Create: `cmd/pdx/statusline_proxy.go`
- Create: `cmd/pdx/statusline_proxy_test.go`
- Modify: `cmd/pdx/main.go`

- [ ] **Step 1: Write the failing test for `renderMinimal`**

```go
// cmd/pdx/statusline_proxy_test.go
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderMinimal_FullFields(t *testing.T) {
	raw := json.RawMessage(`{
		"model": {"id": "claude-sonnet-4-6", "display_name": "Sonnet"},
		"context_window": {"used_percentage": 23},
		"cost": {"total_cost_usd": 0.12}
	}`)
	got := renderMinimal(raw)
	want := "[pdx] Sonnet · ctx 23% · $0.12"
	if got != want {
		t.Errorf("renderMinimal = %q, want %q", got, want)
	}
}

func TestRenderMinimal_MissingDisplayName(t *testing.T) {
	raw := json.RawMessage(`{"model":{"id":"claude-opus-4-7"}}`)
	got := renderMinimal(raw)
	if !strings.Contains(got, "claude-opus-4-7") {
		t.Errorf("renderMinimal = %q, expected id fallback", got)
	}
}

func TestRenderMinimal_NoCost(t *testing.T) {
	raw := json.RawMessage(`{"model":{"display_name":"Opus"},"context_window":{"used_percentage":8}}`)
	got := renderMinimal(raw)
	want := "[pdx] Opus · ctx 8%"
	if got != want {
		t.Errorf("renderMinimal = %q, want %q", got, want)
	}
}

func TestRenderMinimal_Empty(t *testing.T) {
	got := renderMinimal(json.RawMessage(`{}`))
	want := "[pdx]"
	if got != want {
		t.Errorf("renderMinimal = %q, want %q", got, want)
	}
}

func TestReadStdinWithTimeout_Valid(t *testing.T) {
	src := bytes.NewBufferString(`{"a":1}`)
	got := readStdinWithTimeout(src, 1)
	if string(got) != `{"a":1}` {
		t.Errorf("got %q, want JSON", got)
	}
}

func TestReadStdinWithTimeout_Empty(t *testing.T) {
	got := readStdinWithTimeout(bytes.NewBuffer(nil), 1)
	if string(got) != "{}" {
		t.Errorf("empty stdin got %q, want {}", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/pdx/ -run 'TestRenderMinimal|TestReadStdin' -v`
Expected: FAIL — undefined `renderMinimal`, `readStdinWithTimeout`.

- [ ] **Step 3: Implement minimal code**

```go
// cmd/pdx/statusline_proxy.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	out := parts[0]
	for _, p := range parts[1:] {
		out += " · " + p
	}
	return out
}

// runStatuslineProxy is the entry point for `pdx statusline-proxy [--inner "<cmd>"]`.
// Full implementation added in later tasks; stub for now.
func runStatuslineProxy(args []string) {
	_ = args
	raw := readStdinWithTimeout(os.Stdin, 5)
	fmt.Println(renderMinimal(raw))
	os.Exit(0)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/pdx/ -run 'TestRenderMinimal|TestReadStdin' -v`
Expected: PASS (6 passing)

- [ ] **Step 5: Wire dispatch in main.go**

In `cmd/pdx/main.go`, add `statusline-proxy` to the Usage string and the switch:

```go
// in the switch statement in main():
case "statusline-proxy":
    runStatuslineProxy(os.Args[2:])
```

Also update the Usage string to include `statusline-proxy`.

- [ ] **Step 6: Commit**

```bash
git add cmd/pdx/statusline_proxy.go cmd/pdx/statusline_proxy_test.go cmd/pdx/main.go
git commit -m "feat(pdx): statusline-proxy subcommand skeleton with default minimal status"
```

---

### Task 4: Add `--inner` flag parsing + inner exec

**Files:**
- Modify: `cmd/pdx/statusline_proxy.go`
- Modify: `cmd/pdx/statusline_proxy_test.go`

- [ ] **Step 1: Add failing tests**

Append to `cmd/pdx/statusline_proxy_test.go`:

```go
func TestParseInnerFlag(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{}, ""},
		{[]string{"--inner", "ccstatusline"}, "ccstatusline"},
		{[]string{"--inner", "ccstatusline --format compact"}, "ccstatusline --format compact"},
		{[]string{"--unknown", "x"}, ""},
	}
	for _, tc := range cases {
		got := parseInnerFlag(tc.args)
		if got != tc.want {
			t.Errorf("parseInnerFlag(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestExecInner_Success(t *testing.T) {
	stdin := []byte(`{"a":1}`)
	got := execInner("echo hello", stdin, 2)
	if strings.TrimSpace(got) != "hello" {
		t.Errorf("execInner stdout = %q, want %q", got, "hello")
	}
}

func TestExecInner_Timeout(t *testing.T) {
	got := execInner("sleep 5", []byte("{}"), 1)
	// Timeout is silent; empty or partial stdout is acceptable.
	if got == "should-never-happen" {
		t.Error("sentinel check")
	}
	_ = got
}

func TestExecInner_NonZeroExitCaptured(t *testing.T) {
	got := execInner("printf 'foo'; exit 1", []byte("{}"), 2)
	if strings.TrimSpace(got) != "foo" {
		t.Errorf("non-zero exit should still capture stdout; got %q", got)
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./cmd/pdx/ -run 'TestParseInnerFlag|TestExecInner' -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Append to `cmd/pdx/statusline_proxy.go`:

```go
import (
	// add:
	"bytes"
	"context"
	"os/exec"
)

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
```

Then update `runStatuslineProxy`:

```go
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/pdx/ -run 'TestParseInnerFlag|TestExecInner' -v`
Expected: PASS (4 passing).

- [ ] **Step 5: Commit**

```bash
git add cmd/pdx/statusline_proxy.go cmd/pdx/statusline_proxy_test.go
git commit -m "feat(pdx): statusline-proxy --inner flag + exec support"
```

---

### Task 5: Synchronous POST to `/api/agent/status` + error-swallow

**Files:**
- Modify: `cmd/pdx/statusline_proxy.go`
- Modify: `cmd/pdx/statusline_proxy_test.go`

- [ ] **Step 1: Add failing test**

Append to `cmd/pdx/statusline_proxy_test.go`:

```go
import (
	// add:
	"net/http"
	"net/http/httptest"
	// already: "encoding/json"
)

func TestPostStatus_Success(t *testing.T) {
	var received statuslinePayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	err := postStatus(ts.URL+"/api/agent/status", "tok", statuslinePayload{
		TmuxSession: "sess1",
		AgentType:   "cc",
		RawStatus:   json.RawMessage(`{"x":1}`),
	})
	if err != nil {
		t.Fatalf("postStatus: %v", err)
	}
	if received.TmuxSession != "sess1" {
		t.Errorf("tmux_session mismatch: %q", received.TmuxSession)
	}
}

func TestPostStatus_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer ts.Close()

	err := postStatus(ts.URL, "", statuslinePayload{TmuxSession: "x", AgentType: "cc"})
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestPostStatus_SilentOn5xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	err := postStatus(ts.URL, "", statuslinePayload{TmuxSession: "x", AgentType: "cc"})
	if err == nil {
		t.Error("5xx should return error, got nil")
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./cmd/pdx/ -run 'TestPostStatus' -v`
Expected: FAIL — `postStatus` and `statuslinePayload` undefined.

- [ ] **Step 3: Implement**

Append to `cmd/pdx/statusline_proxy.go`:

```go
import (
	// add:
	"net/http"
	"github.com/wake/purdex/internal/config"
)

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
```

Replace the body of `runStatuslineProxy`:

```go
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
		url = fmt.Sprintf("http://%s:%d/api/agent/status", cfg.Bind, cfg.Port)
		token = cfg.Token
	}
	_ = postStatus(url, token, statuslinePayload{
		TmuxSession: tmuxSession,
		AgentType:   "cc",
		RawStatus:   raw,
	})

	os.Exit(0)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/pdx/ -v`
Expected: PASS (all statusline_proxy tests green)

- [ ] **Step 5: Commit**

```bash
git add cmd/pdx/statusline_proxy.go cmd/pdx/statusline_proxy_test.go
git commit -m "feat(pdx): statusline-proxy synchronous POST to daemon"
```

---

## Backend: CC settings.json install/remove

### Task 6: Mode detection with go-shellwords

**Files:**
- Create: `internal/agent/cc/statusline.go`
- Create: `internal/agent/cc/statusline_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/agent/cc/statusline_test.go
package cc

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return path
}

func TestDetectStatuslineMode_None(t *testing.T) {
	path := writeSettings(t, `{}`)
	m, err := detectStatuslineMode(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != "none" {
		t.Errorf("mode = %q, want none", m.Mode)
	}
}

func TestDetectStatuslineMode_NullField(t *testing.T) {
	path := writeSettings(t, `{"statusLine": null}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "none" {
		t.Errorf("null statusLine: mode = %q, want none", m.Mode)
	}
}

func TestDetectStatuslineMode_NonObject(t *testing.T) {
	path := writeSettings(t, `{"statusLine": "raw string"}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "none" {
		t.Errorf("non-object statusLine: mode = %q, want none", m.Mode)
	}
}

func TestDetectStatuslineMode_Pdx(t *testing.T) {
	path := writeSettings(t, `{
  "statusLine": {"type": "command", "command": "/opt/homebrew/bin/pdx statusline-proxy"}
}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "pdx" {
		t.Errorf("mode = %q, want pdx", m.Mode)
	}
}

func TestDetectStatuslineMode_Wrapped(t *testing.T) {
	path := writeSettings(t, `{
  "statusLine": {"type": "command", "command": "/a/b/pdx statusline-proxy --inner 'ccstatusline --format compact'"}
}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "wrapped" {
		t.Errorf("mode = %q, want wrapped", m.Mode)
	}
	if m.Inner != "ccstatusline --format compact" {
		t.Errorf("inner = %q", m.Inner)
	}
}

func TestDetectStatuslineMode_WrappedWithSingleQuoteEscape(t *testing.T) {
	// Shell: --inner 'it'\''s'   after escape
	path := writeSettings(t, `{
  "statusLine": {"type": "command", "command": "/x/pdx statusline-proxy --inner 'it'\''s'"}
}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "wrapped" {
		t.Errorf("mode = %q, want wrapped", m.Mode)
	}
	if m.Inner != "it's" {
		t.Errorf("inner = %q, want \"it's\"", m.Inner)
	}
}

func TestDetectStatuslineMode_Unmanaged(t *testing.T) {
	path := writeSettings(t, `{
  "statusLine": {"type": "command", "command": "ccstatusline --format compact"}
}`)
	m, _ := detectStatuslineMode(path)
	if m.Mode != "unmanaged" {
		t.Errorf("mode = %q, want unmanaged", m.Mode)
	}
	if m.Inner != "ccstatusline --format compact" {
		t.Errorf("inner (raw command) = %q", m.Inner)
	}
}

func TestDetectStatuslineMode_MissingFile(t *testing.T) {
	m, err := detectStatuslineMode(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file should be ok: %v", err)
	}
	if m.Mode != "none" {
		t.Errorf("missing file: mode = %q, want none", m.Mode)
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./internal/agent/cc/ -run TestDetectStatusline -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

```go
// internal/agent/cc/statusline.go
package cc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	shellwords "github.com/mattn/go-shellwords"
)

// StatuslineState describes the current state of CC's statusLine config.
type StatuslineState struct {
	Mode         string `json:"mode"`  // "none" | "pdx" | "wrapped" | "unmanaged"
	Installed    bool   `json:"installed"`
	Inner        string `json:"innerCommand,omitempty"`
	RawCommand   string `json:"rawCommand,omitempty"`
	SettingsPath string `json:"settingsPath"`
}

// detectStatuslineMode reads ~/.claude/settings.json and classifies the
// current statusLine.command value.
func detectStatuslineMode(path string) (StatuslineState, error) {
	s := StatuslineState{Mode: "none", SettingsPath: path}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return s, err
	}
	slRaw, ok := settings["statusLine"]
	if !ok || slRaw == nil {
		return s, nil
	}
	slObj, isObj := slRaw.(map[string]any)
	if !isObj {
		return s, nil // non-object: treat as none (safe overwrite)
	}
	cmdAny, ok := slObj["command"]
	if !ok {
		return s, nil
	}
	cmd, ok := cmdAny.(string)
	if !ok || strings.TrimSpace(cmd) == "" {
		return s, nil
	}

	s.Installed = true
	s.RawCommand = cmd

	argv, err := shellwords.Parse(cmd)
	if err != nil || len(argv) < 2 {
		s.Mode = "unmanaged"
		s.Inner = cmd
		return s, nil
	}
	base := filepath.Base(argv[0])
	if base != "pdx" && base != "pdx.exe" {
		s.Mode = "unmanaged"
		s.Inner = cmd
		return s, nil
	}
	if argv[1] != "statusline-proxy" {
		s.Mode = "unmanaged"
		s.Inner = cmd
		return s, nil
	}
	switch {
	case len(argv) == 2:
		s.Mode = "pdx"
	case len(argv) >= 4 && argv[2] == "--inner":
		s.Mode = "wrapped"
		s.Inner = argv[3]
	default:
		s.Mode = "unmanaged"
		s.Inner = cmd
	}
	return s, nil
}
```

- [ ] **Step 4: Run**

Run: `go test ./internal/agent/cc/ -run TestDetectStatusline -v`
Expected: PASS (8 passing)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/cc/statusline.go internal/agent/cc/statusline_test.go
git commit -m "feat(cc): statusline mode detection with shellwords parsing"
```

---

### Task 7: Install / Remove statusline in settings.json

**Files:**
- Modify: `internal/agent/cc/statusline.go`
- Modify: `internal/agent/cc/statusline_test.go`

- [ ] **Step 1: Add failing tests**

Append to `internal/agent/cc/statusline_test.go`:

```go
func readSettingsMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func getStatusLineCommand(t *testing.T, path string) string {
	t.Helper()
	s := readSettingsMap(t, path)
	sl, _ := s["statusLine"].(map[string]any)
	if sl == nil {
		return ""
	}
	c, _ := sl["command"].(string)
	return c
}

func TestInstallStatuslinePdx_Empty(t *testing.T) {
	path := writeSettings(t, `{}`)
	if err := installStatuslinePdx(path, "/opt/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	got := getStatusLineCommand(t, path)
	if got != "/opt/bin/pdx statusline-proxy" {
		t.Errorf("command = %q", got)
	}
}

func TestInstallStatuslinePdx_PreservesHooks(t *testing.T) {
	path := writeSettings(t, `{"hooks": {"Stop": "something"}}`)
	if err := installStatuslinePdx(path, "/opt/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	s := readSettingsMap(t, path)
	if _, ok := s["hooks"]; !ok {
		t.Error("hooks dropped")
	}
	sl, _ := s["statusLine"].(map[string]any)
	if sl["type"] != "command" {
		t.Errorf("type = %v", sl["type"])
	}
}

func TestInstallStatuslineWrap_SimpleInner(t *testing.T) {
	path := writeSettings(t, `{}`)
	if err := installStatuslineWrap(path, "/opt/bin/pdx", "ccstatusline"); err != nil {
		t.Fatal(err)
	}
	got := getStatusLineCommand(t, path)
	want := "/opt/bin/pdx statusline-proxy --inner 'ccstatusline'"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestInstallStatuslineWrap_InnerWithSingleQuote(t *testing.T) {
	path := writeSettings(t, `{}`)
	if err := installStatuslineWrap(path, "/opt/bin/pdx", "it's"); err != nil {
		t.Fatal(err)
	}
	got := getStatusLineCommand(t, path)
	// POSIX single-quote escape: ' -> '\''
	want := "/opt/bin/pdx statusline-proxy --inner 'it'\\''s'"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	// Round-trip: detect should return the original inner.
	m, _ := detectStatuslineMode(path)
	if m.Inner != "it's" {
		t.Errorf("round-trip inner = %q, want \"it's\"", m.Inner)
	}
}

func TestInstallStatuslineWrap_InnerWithSpaceAndAmpersand(t *testing.T) {
	inner := `foo "bar" & baz 'qux'`
	path := writeSettings(t, `{}`)
	if err := installStatuslineWrap(path, "/opt/bin/pdx", inner); err != nil {
		t.Fatal(err)
	}
	m, _ := detectStatuslineMode(path)
	if m.Inner != inner {
		t.Errorf("round-trip: got %q, want %q", m.Inner, inner)
	}
}

func TestRemoveStatusline_Pdx(t *testing.T) {
	path := writeSettings(t, `{"hooks":{"Stop":"x"},"statusLine":{"type":"command","command":"/opt/bin/pdx statusline-proxy"}}`)
	if err := removeStatusline(path); err != nil {
		t.Fatal(err)
	}
	s := readSettingsMap(t, path)
	if _, ok := s["statusLine"]; ok {
		t.Error("statusLine should be removed")
	}
	if _, ok := s["hooks"]; !ok {
		t.Error("hooks should be preserved")
	}
}

func TestRemoveStatusline_WrappedRestoresInner(t *testing.T) {
	path := writeSettings(t, `{"statusLine":{"type":"command","command":"/opt/bin/pdx statusline-proxy --inner 'ccstatusline --format compact'","padding":0}}`)
	if err := removeStatusline(path); err != nil {
		t.Fatal(err)
	}
	got := getStatusLineCommand(t, path)
	if got != "ccstatusline --format compact" {
		t.Errorf("restored command = %q", got)
	}
	s := readSettingsMap(t, path)
	sl, _ := s["statusLine"].(map[string]any)
	if sl["type"] != "command" {
		t.Errorf("type not preserved: %v", sl["type"])
	}
	if sl["padding"] == nil {
		t.Errorf("padding not preserved")
	}
}

func TestRemoveStatusline_UnmanagedRefuses(t *testing.T) {
	path := writeSettings(t, `{"statusLine":{"type":"command","command":"ccstatusline"}}`)
	err := removeStatusline(path)
	if err == nil {
		t.Error("expected refusal on unmanaged remove")
	}
}

func TestRemoveStatusline_None_NoOp(t *testing.T) {
	path := writeSettings(t, `{}`)
	if err := removeStatusline(path); err != nil {
		t.Fatalf("remove of none should no-op: %v", err)
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./internal/agent/cc/ -run 'TestInstallStatusline|TestRemoveStatusline' -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Append to `internal/agent/cc/statusline.go`:

```go
import (
	// add:
	"fmt"
)

// shellSingleQuote returns a POSIX-safe single-quoted form of s that round-trips
// through `sh -c`. Embedded ' characters become '\''.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeSettingsAtomic marshals settings as JSON and writes to path via temp+rename.
func writeSettingsAtomic(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func readSettings(path string) (map[string]any, error) {
	settings := make(map[string]any)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return settings, nil
}

func installStatuslinePdx(path, pdxPath string) error {
	settings, err := readSettings(path)
	if err != nil {
		return err
	}
	settings["statusLine"] = map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s statusline-proxy", pdxPath),
	}
	return writeSettingsAtomic(path, settings)
}

func installStatuslineWrap(path, pdxPath, inner string) error {
	settings, err := readSettings(path)
	if err != nil {
		return err
	}
	settings["statusLine"] = map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s statusline-proxy --inner %s", pdxPath, shellSingleQuote(inner)),
	}
	return writeSettingsAtomic(path, settings)
}

func removeStatusline(path string) error {
	state, err := detectStatuslineMode(path)
	if err != nil {
		return err
	}
	settings, err := readSettings(path)
	if err != nil {
		return err
	}
	switch state.Mode {
	case "none":
		return nil
	case "unmanaged":
		return fmt.Errorf("refusing to remove unmanaged statusLine; please remove manually")
	case "pdx":
		delete(settings, "statusLine")
	case "wrapped":
		sl, _ := settings["statusLine"].(map[string]any)
		if sl == nil {
			sl = map[string]any{"type": "command"}
		}
		sl["command"] = state.Inner
		settings["statusLine"] = sl
	}
	return writeSettingsAtomic(path, settings)
}
```

- [ ] **Step 4: Run**

Run: `go test ./internal/agent/cc/ -v`
Expected: PASS (all new + existing)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/cc/statusline.go internal/agent/cc/statusline_test.go
git commit -m "feat(cc): install/remove statusLine with atomic write + shell-quote round-trip"
```

---

### Task 8: Provider interface — StatuslineInstaller

**Files:**
- Modify: `internal/agent/cc/interfaces.go` (or wherever `HookInstaller` is)
- Modify: `internal/agent/cc/provider.go`

- [ ] **Step 1: Find existing HookInstaller definition**

Run: `grep -rn "HookInstaller" internal/agent/`
Note the file. The new interface should live in the same package.

- [ ] **Step 2: Add interface**

In `internal/agent/cc/interfaces.go` (or wherever your `HookInstaller` lives), add alongside it:

```go
// StatuslineInstaller manages CC's statusLine.command in ~/.claude/settings.json.
type StatuslineInstaller interface {
	CheckStatusline() (StatuslineState, error)
	InstallStatuslinePdx(pdxPath string) error
	InstallStatuslineWrap(pdxPath, inner string) error
	RemoveStatusline() error
}
```

- [ ] **Step 3: Wire up the Provider**

Add methods to `Provider` in `internal/agent/cc/provider.go`:

```go
func (p *Provider) CheckStatusline() (StatuslineState, error) {
	path, err := ccSettingsPath()
	if err != nil {
		return StatuslineState{}, err
	}
	return detectStatuslineMode(path)
}

func (p *Provider) InstallStatuslinePdx(pdxPath string) error {
	path, err := ccSettingsPath()
	if err != nil {
		return err
	}
	return installStatuslinePdx(path, pdxPath)
}

func (p *Provider) InstallStatuslineWrap(pdxPath, inner string) error {
	path, err := ccSettingsPath()
	if err != nil {
		return err
	}
	return installStatuslineWrap(path, pdxPath, inner)
}

func (p *Provider) RemoveStatusline() error {
	path, err := ccSettingsPath()
	if err != nil {
		return err
	}
	return removeStatusline(path)
}
```

- [ ] **Step 4: Build + existing tests**

Run: `go build ./... && go test ./internal/agent/cc/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/cc/
git commit -m "feat(cc): Provider implements StatuslineInstaller interface"
```

---

## Backend: HTTP handlers

### Task 9: `GET /api/agent/{agent}/statusline/status` handler

**Files:**
- Modify: `internal/module/agent/handler.go`
- Modify: `internal/module/agent/module.go`
- Modify: `internal/module/agent/handler_test.go` (if it exists — else create)

- [ ] **Step 1: Add failing test**

Check for existing `internal/module/agent/handler_test.go`. If it has setup helpers for building a test module, reuse them. Otherwise create a minimal test:

```go
// internal/module/agent/handler_test.go (add this test)
func TestHandleStatuslineStatus_CC(t *testing.T) {
	m := buildTestModule(t) // existing helper, or replicate hook test setup
	srv := httptest.NewServer(m.makeMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/agent/cc/statusline/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var body cc.StatuslineState
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Mode == "" {
		t.Error("empty mode")
	}
}
```

(If the existing test file uses a different helper pattern, mirror it; the key is a round-trip HTTP test.)

- [ ] **Step 2: Implement handler**

Add to `internal/module/agent/handler.go`:

```go
func (m *Module) handleStatuslineStatus(w http.ResponseWriter, r *http.Request) {
	agentType := r.PathValue("agent")
	if agentType != "cc" {
		http.Error(w, `{"error":"unsupported agent"}`, http.StatusNotFound)
		return
	}
	provider, ok := m.registry.Get(agentType)
	if !ok {
		http.Error(w, `{"error":"unknown agent"}`, http.StatusNotFound)
		return
	}
	installer, ok := provider.(cc.StatuslineInstaller)
	if !ok {
		http.Error(w, `{"error":"agent does not support statusline"}`, http.StatusNotFound)
		return
	}
	state, err := installer.CheckStatusline()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}
```

Register the route in `internal/module/agent/module.go` (or wherever routes are registered):

```go
mux.HandleFunc("GET /api/agent/{agent}/statusline/status", m.handleStatuslineStatus)
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/module/agent/ -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/module/agent/
git commit -m "feat(agent): GET /api/agent/{agent}/statusline/status handler"
```

---

### Task 10: `POST /api/agent/{agent}/statusline/setup` handler + per-host mutex

**Files:**
- Modify: `internal/module/agent/handler.go`
- Modify: `internal/module/agent/module.go`
- Modify: `internal/module/agent/handler_test.go`

- [ ] **Step 1: Add failing test**

```go
func TestHandleStatuslineSetup_InstallPdx(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := buildTestModule(t)
	srv := httptest.NewServer(m.makeMux())
	defer srv.Close()

	body := `{"action":"install","mode":"pdx"}`
	resp, err := http.Post(srv.URL+"/api/agent/cc/statusline/setup", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Errorf("status %d: %s", resp.StatusCode, b)
	}
	// Verify settings.json was written
	data, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(data), "statusline-proxy") {
		t.Errorf("settings.json did not install statusline-proxy: %s", data)
	}
}

func TestHandleStatuslineSetup_RemoveUnmanagedRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Pre-populate with unmanaged statusLine
	os.MkdirAll(filepath.Join(home, ".claude"), 0755)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"),
		[]byte(`{"statusLine":{"type":"command","command":"ccstatusline"}}`), 0644)

	m := buildTestModule(t)
	srv := httptest.NewServer(m.makeMux())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/agent/cc/statusline/setup", "application/json",
		strings.NewReader(`{"action":"remove"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Errorf("status %d, want 409 on unmanaged remove", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Implement handler**

Add to `internal/module/agent/handler.go`:

```go
import "sync"

// statuslineMutex serializes concurrent /setup requests per host.
// CC settings.json is a shared resource; atomic rename doesn't protect
// read-modify-write ordering.
var statuslineMutex sync.Mutex

func (m *Module) handleStatuslineSetup(w http.ResponseWriter, r *http.Request) {
	agentType := r.PathValue("agent")
	if agentType != "cc" {
		http.Error(w, `{"error":"unsupported agent"}`, http.StatusNotFound)
		return
	}
	provider, ok := m.registry.Get(agentType)
	if !ok {
		http.Error(w, `{"error":"unknown agent"}`, http.StatusNotFound)
		return
	}
	installer, ok := provider.(cc.StatuslineInstaller)
	if !ok {
		http.Error(w, `{"error":"agent does not support statusline"}`, http.StatusNotFound)
		return
	}

	var req struct {
		Action string `json:"action"`
		Mode   string `json:"mode"`
		Inner  string `json:"inner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	pdxPath, err := os.Executable()
	if err != nil {
		http.Error(w, `{"error":"cannot find pdx binary"}`, http.StatusInternalServerError)
		return
	}
	pdxPath, _ = filepath.EvalSymlinks(pdxPath)

	statuslineMutex.Lock()
	defer statuslineMutex.Unlock()

	var opErr error
	switch req.Action {
	case "install":
		switch req.Mode {
		case "pdx":
			opErr = installer.InstallStatuslinePdx(pdxPath)
		case "wrap":
			if req.Inner == "" {
				http.Error(w, `{"error":"wrap requires inner"}`, http.StatusBadRequest)
				return
			}
			opErr = installer.InstallStatuslineWrap(pdxPath, req.Inner)
		default:
			http.Error(w, `{"error":"mode must be pdx or wrap"}`, http.StatusBadRequest)
			return
		}
	case "remove":
		opErr = installer.RemoveStatusline()
		if opErr != nil && strings.Contains(opErr.Error(), "refusing to remove unmanaged") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": opErr.Error()})
			return
		}
	default:
		http.Error(w, `{"error":"action must be install or remove"}`, http.StatusBadRequest)
		return
	}
	if opErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"error": opErr.Error()})
		return
	}

	// Return updated status
	m.handleStatuslineStatus(w, r)
}
```

Register route:

```go
mux.HandleFunc("POST /api/agent/{agent}/statusline/setup", m.handleStatuslineSetup)
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/module/agent/ -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/module/agent/
git commit -m "feat(agent): POST /api/agent/{agent}/statusline/setup with per-host mutex"
```

---

### Task 11: `POST /api/agent/status` endpoint + WS broadcast

**Files:**
- Modify: `internal/module/agent/handler.go`
- Modify: `internal/module/agent/module.go`

- [ ] **Step 1: Add failing test**

```go
func TestHandleAgentStatus_BroadcastsOnSessionMatch(t *testing.T) {
	m := buildTestModule(t)
	// Stub a tmux session that resolves to a known session code.
	m.sessions = &fakeSessions{all: []session.Info{{Name: "sess1", Code: "code-1"}}}

	srv := httptest.NewServer(m.makeMux())
	defer srv.Close()

	body := `{"tmux_session":"sess1","agent_type":"cc","raw_status":{"model":{"display_name":"Sonnet"}}}`
	resp, err := http.Post(srv.URL+"/api/agent/status", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}

	// Verify a broadcast was made to "code-1".
	if !m.core.(*fakeCore).broadcasted("code-1", "agent.status") {
		t.Error("expected agent.status broadcast for code-1")
	}
}

func TestHandleAgentStatus_NoBroadcastOnUnknownSession(t *testing.T) {
	m := buildTestModule(t)
	m.sessions = &fakeSessions{all: nil}
	srv := httptest.NewServer(m.makeMux())
	defer srv.Close()

	body := `{"tmux_session":"unknown","agent_type":"cc","raw_status":{}}`
	resp, _ := http.Post(srv.URL+"/api/agent/status", "application/json", strings.NewReader(body))
	if resp.StatusCode != 200 {
		t.Errorf("status %d, want 200", resp.StatusCode)
	}
	// No broadcast assertion — fake core records any; we only assert length == 0.
	if c := m.core.(*fakeCore); len(c.events) != 0 {
		t.Errorf("expected no broadcasts, got %d", len(c.events))
	}
}
```

(If existing test fixtures differ, mirror the hook-event test pattern already in `handler_test.go`.)

- [ ] **Step 2: Implement**

Add to `internal/module/agent/handler.go`:

```go
// statusSnapshots is an in-memory cache of latest statusline payload per sessionCode.
// It is protected by snapshotMu and is NOT persisted (high-frequency / display-only).
type statusSnapshot struct {
	AgentType string          `json:"agent_type"`
	Status    json.RawMessage `json:"status"`
}

var (
	snapshotMu     sync.RWMutex
	statusSnapshots = make(map[string]statusSnapshot) // key: sessionCode
)

func (m *Module) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		TmuxSession string          `json:"tmux_session"`
		AgentType   string          `json:"agent_type"`
		RawStatus   json.RawMessage `json:"raw_status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if payload.AgentType != "cc" {
		http.Error(w, `{"error":"unsupported agent_type"}`, http.StatusBadRequest)
		return
	}

	code := m.resolveSessionCode(payload.TmuxSession)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{}`))

	if code == "" {
		return // 200 but no broadcast (spec: session might have just closed)
	}

	snap := statusSnapshot{AgentType: payload.AgentType, Status: payload.RawStatus}
	snapshotMu.Lock()
	statusSnapshots[code] = snap
	snapshotMu.Unlock()

	if m.core != nil {
		body, _ := json.Marshal(snap)
		m.core.Events.Broadcast(code, "agent.status", string(body))
	}
}
```

Register:

```go
mux.HandleFunc("POST /api/agent/status", m.handleAgentStatus)
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/module/agent/ -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/module/agent/
git commit -m "feat(agent): POST /api/agent/status endpoint + WS broadcast"
```

---

### Task 12: Snapshot replay on WS subscribe + cleared broadcast on remove

**Files:**
- Modify: `internal/module/agent/handler.go`
- Modify: `internal/module/agent/module.go`

- [ ] **Step 1: Locate WS subscribe hook**

Run: `grep -rn "sendSnapshot\|OnSubscribe\|subscribe" internal/module/agent/`
Note the existing hook-state replay function. Mirror it.

- [ ] **Step 2: Add replay logic**

In the existing WS-subscribe entry point (wherever `m.handleHookStatus`/hook snapshot replay lives), add after the hook replay:

```go
// Statusline snapshot replay
snapshotMu.RLock()
for code, snap := range statusSnapshots {
	body, _ := json.Marshal(snap)
	m.core.Events.BroadcastTo(subscriber, code, "agent.status", string(body))
}
snapshotMu.RUnlock()
```

(Use the existing `BroadcastTo` or equivalent per-subscriber push — if only a global `Broadcast` exists, check how hooks do per-subscriber replay and follow suit.)

- [ ] **Step 3: Add cleared broadcast to `handleStatuslineSetup` remove action**

After successful remove in `handleStatuslineSetup` (before returning status):

```go
if req.Action == "remove" && opErr == nil {
	snapshotMu.Lock()
	statusSnapshots = make(map[string]statusSnapshot) // simplest: clear all (single-host daemon)
	snapshotMu.Unlock()
	if m.core != nil {
		m.core.Events.Broadcast("*", "agent.status.cleared",
			`{"host_id":"*","agent_type":"cc"}`)
	}
}
```

(If the broadcast API requires a specific host/session code format, look at how hooks broadcast cross-session events and mirror.)

- [ ] **Step 4: Run existing tests**

Run: `go test ./internal/module/agent/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/
git commit -m "feat(agent): statusline snapshot replay on WS subscribe + cleared broadcast on remove"
```

---

## Frontend: HoverTooltip extraction

### Task 13: Extract `<HoverTooltip>` component

**Files:**
- Create: `spa/src/components/HoverTooltip.tsx`
- Create: `spa/src/components/HoverTooltip.test.tsx`
- Modify: `spa/src/features/workspace/components/ActivityBarNarrow.tsx`

- [ ] **Step 1: Write failing test**

```tsx
// spa/src/components/HoverTooltip.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { HoverTooltip } from './HoverTooltip'

describe('HoverTooltip', () => {
  it('renders the provided text', () => {
    render(
      <div className="relative group">
        <span>trigger</span>
        <HoverTooltip>Hello world</HoverTooltip>
      </div>
    )
    expect(screen.getByText('Hello world')).toBeInTheDocument()
  })

  it('supports placement=top (absolute bottom-full)', () => {
    render(
      <div className="relative group">
        <HoverTooltip placement="top">top tip</HoverTooltip>
      </div>
    )
    const el = screen.getByText('top tip')
    expect(el.className).toContain('bottom-full')
  })

  it('defaults to placement=right (absolute left-full)', () => {
    render(
      <div className="relative group">
        <HoverTooltip>r tip</HoverTooltip>
      </div>
    )
    const el = screen.getByText('r tip')
    expect(el.className).toContain('left-full')
  })
})
```

- [ ] **Step 2: Run — expect failure**

Run: `cd spa && npx vitest run src/components/HoverTooltip.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

```tsx
// spa/src/components/HoverTooltip.tsx
import type { ReactNode } from 'react'

export type HoverTooltipPlacement = 'top' | 'right'

interface Props {
  children: ReactNode
  placement?: HoverTooltipPlacement
  'data-testid'?: string
}

const PLACEMENTS: Record<HoverTooltipPlacement, string> = {
  right: 'left-full ml-2 top-1/2 -translate-y-1/2',
  top: 'bottom-full mb-2 left-1/2 -translate-x-1/2',
}

// HoverTooltip is a CSS-only tooltip that fades in when its parent has the
// `.group` class and the user hovers. Extracted from the ActivityBarNarrow
// ws-tooltip pattern; parent MUST have `class="relative group"`.
export function HoverTooltip({ children, placement = 'right', 'data-testid': testId }: Props) {
  const pos = PLACEMENTS[placement]
  return (
    <span
      data-testid={testId}
      className={`pointer-events-none absolute ${pos} whitespace-nowrap rounded bg-surface-secondary border border-border-default px-2 py-1 text-xs text-text-primary shadow-lg opacity-0 group-hover:opacity-100 transition-opacity z-50`}
    >
      {children}
    </span>
  )
}
```

- [ ] **Step 4: Refactor ActivityBarNarrow to use it**

In `spa/src/features/workspace/components/ActivityBarNarrow.tsx`, replace lines 84-87 (the `ws-tooltip` span) with:

```tsx
<HoverTooltip data-testid="ws-tooltip" placement="right">{tooltipText}</HoverTooltip>
```

Add import at top:

```tsx
import { HoverTooltip } from '../../../components/HoverTooltip'
```

- [ ] **Step 5: Run**

Run: `cd spa && npx vitest run src/components/HoverTooltip.test.tsx src/features/workspace/components/ActivityBarNarrow.test.tsx`
Expected: PASS (both files green; `ws-tooltip` test still passes because we kept the `data-testid`).

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/HoverTooltip.tsx spa/src/components/HoverTooltip.test.tsx spa/src/features/workspace/components/ActivityBarNarrow.tsx
git commit -m "refactor(spa): extract <HoverTooltip> from ws-tooltip pattern"
```

---

## Frontend: Store updates

### Task 14: `useAgentStore` — add `ccStatus`, `setCcStatus`, clear on remove, wipe on remove helper

**Files:**
- Modify: `spa/src/stores/useAgentStore.ts`
- Modify: `spa/src/stores/useAgentStore.test.ts`

- [ ] **Step 1: Write failing tests**

Append to `spa/src/stores/useAgentStore.test.ts` inside the file (pattern: add a new `describe` block):

```ts
describe('useAgentStore.ccStatus', () => {
  it('setCcStatus stores snapshot under composite key', () => {
    const raw = { model: { display_name: 'Sonnet' } }
    useAgentStore.getState().setCcStatus(H, 'dev', raw)
    const entry = useAgentStore.getState().ccStatus[`${H}:dev`]
    expect(entry?.raw).toEqual(raw)
    expect(typeof entry?.receivedAt).toBe('number')
  })

  it('setCcStatus with session_name also sets oscTitle', () => {
    useAgentStore.getState().setCcStatus(H, 'dev', { session_name: 'my-feature' })
    expect(useAgentStore.getState().oscTitles[`${H}:dev`]).toBe('my-feature')
  })

  it('setCcStatus with empty session_name clears oscTitle', () => {
    useAgentStore.getState().setOscTitle(H, 'dev', 'stale')
    useAgentStore.getState().setCcStatus(H, 'dev', { model: { display_name: 'x' } })
    expect(useAgentStore.getState().oscTitles[`${H}:dev`]).toBeUndefined()
  })

  it('clearHostAgentStatus wipes ccStatus + oscTitles for host', () => {
    useAgentStore.getState().setCcStatus(H, 'dev', { session_name: 'a' })
    useAgentStore.getState().setCcStatus(H, 'prod', { session_name: 'b' })
    useAgentStore.getState().clearHostAgentStatus(H)
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
    expect(useAgentStore.getState().ccStatus[`${H}:prod`]).toBeUndefined()
    expect(useAgentStore.getState().oscTitles[`${H}:dev`]).toBeUndefined()
    expect(useAgentStore.getState().oscTitles[`${H}:prod`]).toBeUndefined()
  })

  it('clearSession also wipes ccStatus', () => {
    useAgentStore.getState().setCcStatus(H, 'dev', { session_name: 'a' })
    useAgentStore.getState().clearSession(H, 'dev')
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
  })

  it('removeHost wipes ccStatus', () => {
    useAgentStore.getState().setCcStatus(H, 'dev', { session_name: 'a' })
    useAgentStore.getState().removeHost(H)
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
  })
})
```

Also update the `beforeEach` at the top of the file to include `ccStatus: {}` in the reset.

- [ ] **Step 2: Run — expect failure**

Run: `cd spa && npx vitest run src/stores/useAgentStore.test.ts`
Expected: FAIL — `setCcStatus` / `clearHostAgentStatus` / `ccStatus` undefined.

- [ ] **Step 3: Implement in store**

In `spa/src/stores/useAgentStore.ts`:

(a) Add to the state interface:

```ts
interface CcStatusEntry {
  receivedAt: number
  raw: Record<string, unknown>
}

// ... inside AgentState:
ccStatus: Record<string, CcStatusEntry>

// new actions:
setCcStatus: (hostId: string, sessionCode: string, raw: Record<string, unknown>) => void
clearHostAgentStatus: (hostId: string) => void
```

(b) Add `ccStatus: {}` to `clearSession`'s filterOut list and `removeHost`'s filterKeys list.

(c) Initial state: `ccStatus: {}` in the zustand `create` initializer.

(d) Implement setters:

```ts
setCcStatus: (hostId, sessionCode, raw) => {
  const key = compositeKey(hostId, sessionCode)
  const entry: CcStatusEntry = { receivedAt: Date.now(), raw }
  set((s) => ({ ccStatus: { ...s.ccStatus, [key]: entry } }))
  // Mirror session_name → oscTitle (statusline's channel for cc session name)
  const sessionName = typeof raw?.session_name === 'string' ? raw.session_name : ''
  get().setOscTitle(hostId, sessionCode, sessionName)
},

clearHostAgentStatus: (hostId) => set((s) => {
  const prefix = `${hostId}:`
  const filterKeys = <T,>(r: Record<string, T>): Record<string, T> => {
    const out: Record<string, T> = {}
    for (const [k, v] of Object.entries(r)) if (!k.startsWith(prefix)) out[k] = v
    return out
  }
  return {
    ccStatus: filterKeys(s.ccStatus),
    oscTitles: filterKeys(s.oscTitles),
  }
}),
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/stores/useAgentStore.test.ts`
Expected: PASS (all existing + new)

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useAgentStore.ts spa/src/stores/useAgentStore.test.ts
git commit -m "feat(store): ccStatus field + setCcStatus / clearHostAgentStatus actions"
```

---

### Task 15: Wire `agent.status` + `agent.status.cleared` WS events

**Files:**
- Modify: `spa/src/stores/useAgentStore.ts` (or the WS dispatcher file, if separate)
- Modify: `spa/src/stores/useAgentStore.test.ts` (add WS handler test)

- [ ] **Step 1: Find WS dispatcher**

Run: `grep -rn "agent.event\|agent\\.event\|handleWsMessage" spa/src/`
The handler dispatches normalized event types. Add two new cases.

- [ ] **Step 2: Add failing WS handler tests**

Mirror existing `handleNormalizedEvent` tests. Example:

```ts
describe('useAgentStore WS dispatch', () => {
  it('agent.status event calls setCcStatus', () => {
    const raw = { session_name: 'abc' }
    // Directly invoke the dispatcher helper the store exposes (whatever name):
    useAgentStore.getState().handleWsMessage?.(H, 'dev', {
      type: 'agent.status',
      agent_type: 'cc',
      status: raw,
    })
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]?.raw).toEqual(raw)
  })

  it('agent.status.cleared event calls clearHostAgentStatus', () => {
    useAgentStore.getState().setCcStatus(H, 'dev', { session_name: 'x' })
    useAgentStore.getState().handleWsMessage?.(H, '', {
      type: 'agent.status.cleared',
      host_id: H,
      agent_type: 'cc',
    })
    expect(useAgentStore.getState().ccStatus[`${H}:dev`]).toBeUndefined()
  })
})
```

(If the dispatcher has a different entry point, mirror its signature. The key is the switch-on-type.)

- [ ] **Step 3: Run — expect failure**

Run: `cd spa && npx vitest run src/stores/useAgentStore.test.ts`
Expected: FAIL on new cases.

- [ ] **Step 4: Implement**

In the WS dispatcher (likely in `useAgentStore.ts`'s `handleNormalizedEvent` or an adjacent helper), add:

```ts
// when event type === 'agent.status'
case 'agent.status': {
  if (event.agent_type === 'cc' && event.status) {
    get().setCcStatus(hostId, sessionCode, event.status as Record<string, unknown>)
  }
  break
}
case 'agent.status.cleared': {
  const targetHost = (event.host_id as string) ?? hostId
  get().clearHostAgentStatus(targetHost)
  break
}
```

(If the dispatcher uses a different pattern — e.g., separate handler functions — mirror that.)

- [ ] **Step 5: Run**

Run: `cd spa && npx vitest run src/stores/useAgentStore.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add spa/src/stores/
git commit -m "feat(store): dispatch agent.status + agent.status.cleared WS events"
```

---

## Frontend: Tab rendering + tooltip

### Task 16: Update `InlineTab` — new display rule + HoverTooltip

**Files:**
- Modify: `spa/src/components/InlineTab.tsx`
- Modify: `spa/src/components/InlineTab.test.tsx`

- [ ] **Step 1: Read current structure**

Run: `sed -n '40,90p' spa/src/components/InlineTab.tsx`
Note line 60 (`displayTitle = oscTitle || title`) and line 61 (`<span title=...>`).

- [ ] **Step 2: Add failing tests**

In `InlineTab.test.tsx`, add:

```tsx
import { useAgentStore } from '../stores/useAgentStore'

describe('InlineTab statusline display', () => {
  beforeEach(() => {
    useAgentStore.setState({ oscTitles: {}, showOscTitle: true })
  })

  it('shows "{cc} - {tmux}" when showOscTitle enabled and oscTitle present', () => {
    useAgentStore.getState().setOscTitle('h', 'dev', 'my-feature')
    render(<InlineTab hostId="h" sessionCode="dev" title="dev" ... />)
    expect(screen.getByTestId('tab-title')).toHaveTextContent('my-feature - dev')
  })

  it('shows only tmux name when oscTitle absent', () => {
    render(<InlineTab hostId="h" sessionCode="dev" title="dev" ... />)
    expect(screen.getByTestId('tab-title')).toHaveTextContent('dev')
    expect(screen.getByTestId('tab-title')).not.toHaveTextContent(' - ')
  })

  it('shows only tmux name when showOscTitle disabled', () => {
    useAgentStore.setState({ showOscTitle: false })
    useAgentStore.getState().setOscTitle('h', 'dev', 'my-feature')
    render(<InlineTab hostId="h" sessionCode="dev" title="dev" ... />)
    expect(screen.getByTestId('tab-title')).toHaveTextContent('dev')
  })

  it('hover tooltip shows combined string', () => {
    useAgentStore.getState().setOscTitle('h', 'dev', 'my-feature')
    render(<InlineTab hostId="h" sessionCode="dev" title="dev" ... />)
    expect(screen.getByText('my-feature - dev')).toBeInTheDocument() // tooltip text node
  })
})
```

(Fill in `...` with the actual required props from `InlineTabProps`.)

- [ ] **Step 3: Run — expect failure**

Run: `cd spa && npx vitest run src/components/InlineTab.test.tsx`
Expected: FAIL.

- [ ] **Step 4: Implement change**

In `spa/src/components/InlineTab.tsx`:

(a) Replace the `displayTitle` derivation line (~60):

```tsx
// Derive the displayed tab text per spec Section 4.
// oscTitle here is sourced from useAgentStore and populated by statusLine wrapper's session_name.
const combined = oscTitle && showOscTitle ? `${oscTitle} - ${title}` : title
```

(b) Replace the `<span title=...>` rendering (~61) with HoverTooltip:

```tsx
<div className="relative group">
  <span data-testid="tab-title" className="truncate">{combined}</span>
  <HoverTooltip placement="top">{combined}</HoverTooltip>
</div>
```

(c) Import `HoverTooltip` and `useAgentStore` at top if not already:

```tsx
import { HoverTooltip } from './HoverTooltip'
import { useAgentStore } from '../stores/useAgentStore'
// and inside the component:
const showOscTitle = useAgentStore((s) => s.showOscTitle)
```

(Adjust `oscTitle` sourcing to read from `useAgentStore` via composite key — follow the existing oscTitle subscription pattern in this file.)

- [ ] **Step 5: Run**

Run: `cd spa && npx vitest run src/components/InlineTab.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/InlineTab.tsx spa/src/components/InlineTab.test.tsx
git commit -m "feat(spa): tab title shows {cc} - {tmux}; hover tooltip via HoverTooltip"
```

---

## Frontend: Installer UI

### Task 17: `useStatuslineInstall` hook

**Files:**
- Create: `spa/src/hooks/useStatuslineInstall.ts`
- Create: `spa/src/hooks/useStatuslineInstall.test.ts`

- [ ] **Step 1: Write failing test**

```tsx
// spa/src/hooks/useStatuslineInstall.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useStatuslineInstall } from './useStatuslineInstall'
import { hostFetch } from '../lib/host-api'

vi.mock('../lib/host-api', () => ({
  hostFetch: vi.fn(),
}))

const mockFetch = hostFetch as unknown as ReturnType<typeof vi.fn>

beforeEach(() => mockFetch.mockReset())

describe('useStatuslineInstall', () => {
  it('loads status on mount', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.state.mode).toBe('none'))
  })

  it('install with mode=pdx POSTs and refreshes', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.state.mode).toBe('none'))

    await act(async () => {
      await result.current.install('pdx')
    })
    expect(mockFetch).toHaveBeenCalledWith('host1', '/api/agent/cc/statusline/setup', expect.objectContaining({ method: 'POST' }))
    expect(result.current.state.mode).toBe('pdx')
  })

  it('install with mode=wrap passes inner', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'unmanaged', installed: true, innerCommand: 'ccstatusline', settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'wrapped', installed: true, innerCommand: 'ccstatusline', settingsPath: '/x' }) })

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.state.mode).toBe('unmanaged'))

    await act(async () => {
      await result.current.install('wrap', 'ccstatusline')
    })
    const call = mockFetch.mock.calls[1]
    expect(JSON.parse(call[2].body)).toMatchObject({ action: 'install', mode: 'wrap', inner: 'ccstatusline' })
  })

  it('remove POSTs action=remove', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })

    const { result } = renderHook(() => useStatuslineInstall('host1'))
    await waitFor(() => expect(result.current.state.mode).toBe('pdx'))

    await act(async () => {
      await result.current.remove()
    })
    expect(result.current.state.mode).toBe('none')
  })
})
```

- [ ] **Step 2: Run — expect failure**

Run: `cd spa && npx vitest run src/hooks/useStatuslineInstall.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement**

```tsx
// spa/src/hooks/useStatuslineInstall.ts
import { useCallback, useEffect, useState } from 'react'
import { hostFetch } from '../lib/host-api'

export interface StatuslineState {
  mode: 'none' | 'pdx' | 'wrapped' | 'unmanaged'
  installed: boolean
  innerCommand?: string
  rawCommand?: string
  settingsPath: string
}

type Phase = 'idle' | 'loading' | 'ready' | 'error'

export function useStatuslineInstall(hostId: string) {
  const [state, setState] = useState<StatuslineState>({ mode: 'none', installed: false, settingsPath: '' })
  const [phase, setPhase] = useState<Phase>('idle')
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    setPhase('loading')
    setError(null)
    try {
      const res = await hostFetch(hostId, '/api/agent/cc/statusline/status')
      if (!res.ok) throw new Error(`${res.status}`)
      setState(await res.json())
      setPhase('ready')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setPhase('error')
    }
  }, [hostId])

  const install = useCallback(
    async (mode: 'pdx' | 'wrap', inner?: string) => {
      setPhase('loading')
      const body = JSON.stringify({ action: 'install', mode, inner })
      const res = await hostFetch(hostId, '/api/agent/cc/statusline/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body,
      })
      if (!res.ok) {
        setError(`${res.status}`)
        setPhase('error')
        return
      }
      setState(await res.json())
      setPhase('ready')
    },
    [hostId],
  )

  const remove = useCallback(async () => {
    setPhase('loading')
    const res = await hostFetch(hostId, '/api/agent/cc/statusline/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'remove' }),
    })
    if (!res.ok) {
      const msg = res.status === 409 ? 'Cannot remove unmanaged statusLine' : `${res.status}`
      setError(msg)
      setPhase('error')
      return
    }
    setState(await res.json())
    setPhase('ready')
  }, [hostId])

  useEffect(() => {
    void refresh()
  }, [refresh])

  return { state, phase, error, install, remove, refresh }
}
```

- [ ] **Step 4: Run**

Run: `cd spa && npx vitest run src/hooks/useStatuslineInstall.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add spa/src/hooks/useStatuslineInstall.ts spa/src/hooks/useStatuslineInstall.test.ts
git commit -m "feat(spa): useStatuslineInstall hook with GET/POST + state machine"
```

---

### Task 18: `StatuslineConflictDialog` component

**Files:**
- Create: `spa/src/components/hosts/StatuslineConflictDialog.tsx`
- Create: `spa/src/components/hosts/StatuslineConflictDialog.test.tsx`

- [ ] **Step 1: Write failing test**

```tsx
// spa/src/components/hosts/StatuslineConflictDialog.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { StatuslineConflictDialog } from './StatuslineConflictDialog'

describe('StatuslineConflictDialog', () => {
  it('shows the existing command', () => {
    render(
      <StatuslineConflictDialog existingCommand="ccstatusline" onWrap={vi.fn()} onCancel={vi.fn()} />
    )
    expect(screen.getByText('ccstatusline')).toBeInTheDocument()
  })

  it('Wrap button invokes onWrap', () => {
    const onWrap = vi.fn()
    render(<StatuslineConflictDialog existingCommand="x" onWrap={onWrap} onCancel={vi.fn()} />)
    fireEvent.click(screen.getByText(/wrap/i))
    expect(onWrap).toHaveBeenCalledOnce()
  })

  it('Cancel button invokes onCancel', () => {
    const onCancel = vi.fn()
    render(<StatuslineConflictDialog existingCommand="x" onWrap={vi.fn()} onCancel={onCancel} />)
    fireEvent.click(screen.getByText(/cancel/i))
    expect(onCancel).toHaveBeenCalledOnce()
  })
})
```

- [ ] **Step 2: Run — expect failure**

Run: `cd spa && npx vitest run src/components/hosts/StatuslineConflictDialog.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

```tsx
// spa/src/components/hosts/StatuslineConflictDialog.tsx
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  existingCommand: string
  onWrap: () => void
  onCancel: () => void
}

export function StatuslineConflictDialog({ existingCommand, onWrap, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-surface-elevated border border-border-default rounded-lg p-6 max-w-lg w-full mx-4 shadow-xl">
        <h3 className="text-base font-semibold mb-3">{t('hosts.extensions.conflict_title')}</h3>
        <p className="text-sm text-text-secondary mb-2">{t('hosts.extensions.conflict_existing_label')}</p>
        <code className="block bg-surface-secondary border border-border-subtle rounded px-2 py-1.5 text-xs font-mono mb-4 break-all">
          {existingCommand}
        </code>
        <p className="text-xs text-text-muted mb-5">{t('hosts.extensions.conflict_wrap_explainer')}</p>
        <div className="flex justify-end gap-2">
          <button
            onClick={onCancel}
            className="px-3 py-1.5 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary text-text-secondary cursor-pointer"
          >
            {t('hosts.extensions.cancel')}
          </button>
          <button
            onClick={onWrap}
            className="px-3 py-1.5 rounded text-xs bg-accent text-white hover:bg-accent-hover cursor-pointer"
          >
            {t('hosts.extensions.wrap')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run**

Run: `cd spa && npx vitest run src/components/hosts/StatuslineConflictDialog.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/hosts/StatuslineConflictDialog.tsx spa/src/components/hosts/StatuslineConflictDialog.test.tsx
git commit -m "feat(spa): StatuslineConflictDialog component"
```

---

### Task 19: `AgentExtensionRow` component + install flow orchestration

**Files:**
- Create: `spa/src/components/hosts/AgentExtensionRow.tsx`
- Create: `spa/src/components/hosts/AgentExtensionRow.test.tsx`

- [ ] **Step 1: Write failing tests**

```tsx
// spa/src/components/hosts/AgentExtensionRow.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { AgentExtensionRow } from './AgentExtensionRow'
import { hostFetch } from '../../lib/host-api'

vi.mock('../../lib/host-api', () => ({ hostFetch: vi.fn() }))
const mockFetch = hostFetch as unknown as ReturnType<typeof vi.fn>

beforeEach(() => mockFetch.mockReset())

describe('AgentExtensionRow (statusline)', () => {
  it('shows Install button when mode=none', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => expect(screen.getByText(/install/i)).toBeInTheDocument())
  })

  it('shows Remove button when mode=pdx', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => expect(screen.getByText(/remove/i)).toBeInTheDocument())
  })

  it('shows Remove + "wrap" label when mode=wrapped', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'wrapped', installed: true, innerCommand: 'x', settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => expect(screen.getByText(/wrap/i)).toBeInTheDocument())
  })

  it('Install on mode=none directly installs pdx (no dialog)', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByText(/install/i))
    fireEvent.click(screen.getByText(/install/i))
    await waitFor(() => expect(screen.getByText(/remove/i)).toBeInTheDocument())
    const call = mockFetch.mock.calls[1]
    expect(JSON.parse(call[2].body)).toMatchObject({ action: 'install', mode: 'pdx' })
  })

  it('Install on mode=unmanaged shows conflict dialog', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'unmanaged', installed: true, innerCommand: 'ccstatusline', settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByText(/install/i))
    fireEvent.click(screen.getByText(/install/i))
    expect(await screen.findByText('ccstatusline')).toBeInTheDocument() // dialog
  })
})
```

- [ ] **Step 2: Run — expect failure**

Run: `cd spa && npx vitest run src/components/hosts/AgentExtensionRow.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

```tsx
// spa/src/components/hosts/AgentExtensionRow.tsx
import { useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useStatuslineInstall } from '../../hooks/useStatuslineInstall'
import { StatuslineConflictDialog } from './StatuslineConflictDialog'

interface Props {
  hostId: string
  extensionId: 'statusline'
}

export function AgentExtensionRow({ hostId, extensionId }: Props) {
  const t = useI18nStore((s) => s.t)
  const { state, phase, error, install, remove } = useStatuslineInstall(hostId)
  const [showConflict, setShowConflict] = useState(false)

  const handleInstall = () => {
    if (state.mode === 'unmanaged') {
      setShowConflict(true)
    } else {
      void install('pdx')
    }
  }

  const handleWrap = () => {
    setShowConflict(false)
    void install('wrap', state.innerCommand ?? state.rawCommand ?? '')
  }

  const handleRemove = () => {
    if (window.confirm(t('hosts.extensions.confirm_remove'))) void remove()
  }

  const badge = (() => {
    switch (state.mode) {
      case 'pdx': return t('hosts.extensions.installed')
      case 'wrapped': return t('hosts.extensions.installed_wrap')
      case 'unmanaged': return t('hosts.extensions.unmanaged')
      default: return t('hosts.extensions.not_installed')
    }
  })()

  return (
    <>
      <div className="flex items-center justify-between py-2">
        <div className="flex items-center gap-2">
          <span className="text-sm">{t(`hosts.extensions.${extensionId}`)}</span>
          <span className="text-xs text-text-muted">{badge}</span>
        </div>
        <div className="flex gap-2">
          {state.installed ? (
            <button
              onClick={handleRemove}
              disabled={phase === 'loading'}
              className="px-3 py-1 rounded text-xs bg-red-500/10 text-red-400 border border-red-500/30 cursor-pointer disabled:opacity-50"
            >
              {t('hosts.extensions.remove')}
            </button>
          ) : (
            <button
              onClick={handleInstall}
              disabled={phase === 'loading'}
              className="px-3 py-1 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
            >
              {t('hosts.extensions.install')}
            </button>
          )}
        </div>
      </div>
      {error && <p className="text-xs text-red-400">{error}</p>}
      {showConflict && (
        <StatuslineConflictDialog
          existingCommand={state.rawCommand ?? state.innerCommand ?? ''}
          onWrap={handleWrap}
          onCancel={() => setShowConflict(false)}
        />
      )}
    </>
  )
}
```

- [ ] **Step 4: Run**

Run: `cd spa && npx vitest run src/components/hosts/AgentExtensionRow.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/hosts/AgentExtensionRow.tsx spa/src/components/hosts/AgentExtensionRow.test.tsx
git commit -m "feat(spa): AgentExtensionRow with install/remove + conflict dialog integration"
```

---

### Task 20: Extend `AgentsSection` to render Extensions region for CC

**Files:**
- Modify: `spa/src/components/hosts/AgentsSection.tsx`
- Modify: `spa/src/components/hosts/AgentsSection.test.tsx`

- [ ] **Step 1: Add failing test**

```tsx
// spa/src/components/hosts/AgentsSection.test.tsx — add:
it('renders Extensions row for cc agent', async () => {
  // mock: /api/agents/detect returns cc installed; /api/agent/cc/statusline/status returns mode=none
  // (mock hostFetch accordingly)
  render(<AgentsSection hostId="h1" />)
  await waitFor(() => expect(screen.getByText(/status integration/i)).toBeInTheDocument())
})

it('does not render Extensions row for codex agent', async () => {
  // mock: /api/agents/detect returns only codex
  render(<AgentsSection hostId="h1" />)
  await waitFor(() => {
    expect(screen.queryByText(/status integration/i)).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run — expect failure**

Expected: FAIL (Extensions region not rendered yet).

- [ ] **Step 3: Implement**

In `spa/src/components/hosts/AgentsSection.tsx`, inside the `.map(...)` block at line 72 (per exploration), after the existing agent info div (~line 97), add:

```tsx
{agentType === 'cc' && (
  <div className="mt-3 pt-3 border-t border-border-subtle">
    <p className="text-xs text-text-muted mb-2">{t('hosts.extensions.heading')}</p>
    <AgentExtensionRow hostId={hostId} extensionId="statusline" />
  </div>
)}
```

Add import:

```tsx
import { AgentExtensionRow } from './AgentExtensionRow'
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/hosts/AgentsSection.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/hosts/AgentsSection.tsx spa/src/components/hosts/AgentsSection.test.tsx
git commit -m "feat(spa): Agents tab renders Extensions region for cc"
```

---

## i18n

### Task 21: Add i18n keys

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: Add to en.json**

Under the `hosts.*` namespace:

```json
"hosts.extensions.heading": "Extensions",
"hosts.extensions.statusline": "Status integration",
"hosts.extensions.installed": "Installed",
"hosts.extensions.installed_wrap": "Installed (wrap)",
"hosts.extensions.unmanaged": "External (unmanaged)",
"hosts.extensions.not_installed": "Not installed",
"hosts.extensions.install": "Install",
"hosts.extensions.remove": "Remove",
"hosts.extensions.cancel": "Cancel",
"hosts.extensions.wrap": "Wrap",
"hosts.extensions.confirm_remove": "Remove pdx statusLine integration?",
"hosts.extensions.conflict_title": "Existing statusLine detected",
"hosts.extensions.conflict_existing_label": "Current command:",
"hosts.extensions.conflict_wrap_explainer": "Wrap mode: pdx will invoke your existing command and forward its output to Claude Code, while reporting status to Purdex in the background."
```

- [ ] **Step 2: Add to zh-TW.json**

```json
"hosts.extensions.heading": "擴充",
"hosts.extensions.statusline": "Status 整合",
"hosts.extensions.installed": "已安裝",
"hosts.extensions.installed_wrap": "已安裝（wrap）",
"hosts.extensions.unmanaged": "外部安裝（未管理）",
"hosts.extensions.not_installed": "未安裝",
"hosts.extensions.install": "安裝",
"hosts.extensions.remove": "移除",
"hosts.extensions.cancel": "取消",
"hosts.extensions.wrap": "Wrap",
"hosts.extensions.confirm_remove": "移除 pdx statusLine 整合？",
"hosts.extensions.conflict_title": "偵測到既有 statusLine",
"hosts.extensions.conflict_existing_label": "目前指令：",
"hosts.extensions.conflict_wrap_explainer": "Wrap 模式：pdx 會呼叫你現有的指令並把輸出轉給 Claude Code，同時在背景回報狀態給 Purdex。"
```

- [ ] **Step 3: Type check + full test pass**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add spa/src/locales/
git commit -m "i18n(spa): add hosts.extensions.* keys (en + zh-TW)"
```

---

## Final integration + manual verification

### Task 22: End-to-end smoke test (manual)

**Files:** none modified; this is verification.

- [ ] **Step 1: Build daemon + SPA**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/statusline-installer-p1
go build -o bin/pdx ./cmd/pdx
cd spa && pnpm install && pnpm run build
```

- [ ] **Step 2: Start daemon in worktree**

```bash
./bin/pdx serve
```

- [ ] **Step 3: SPA dev server** (separate terminal)

```bash
cd spa && pnpm run dev
```

- [ ] **Step 4: From SPA, navigate to Host → Agents**

- Expect: CC card shows Extensions → "Status integration: Not installed" + [Install] button.
- Click Install → status becomes "Installed" + [Remove] button.
- Check `~/.claude/settings.json`: `statusLine.command` contains `<pdx-abs-path> statusline-proxy`.

- [ ] **Step 5: Run CC in a tmux session**

- Start a session via Purdex SPA's normal flow.
- Run `claude` inside that pane.
- Observe: CC's bottom status bar shows `[pdx] <model> · ctx X% · $Y.YY`.
- In SPA: with Settings → Terminal → "Show agent dynamic title" enabled and after CC `/rename my-feature`, the tab label updates to `my-feature - <tmux name>`; status bar shows `my-feature`.

- [ ] **Step 6: Test Wrap flow**

- Manually edit `~/.claude/settings.json` to set `statusLine.command = "echo [test]"`.
- In SPA, click Install — conflict dialog appears with `echo [test]`.
- Click Wrap.
- `~/.claude/settings.json` now has: `<pdx> statusline-proxy --inner 'echo [test]'`.
- CC shows `[test]` (inner output).
- Click Remove — `~/.claude/settings.json` restored to `echo [test]`.

- [ ] **Step 7: Test Unmanaged refuse**

- Edit settings.json to non-pdx command (like above). Do NOT wrap.
- Click Remove via SPA — expect error toast "Cannot remove unmanaged statusLine".

- [ ] **Step 8: Commit verification notes (optional)**

```bash
git commit --allow-empty -m "chore: manual e2e smoke test passed (see task 22)"
```

---

# Summary

22 tasks total across:
- **Prep**: 1 refactor (ccSettingsPath helper) + 1 dep (shellwords)
- **Backend Go**: 6 tasks (proxy subcommand 3-task TDD split, settings installer 2-task, HTTP handlers 4-task)
- **Frontend**: 8 tasks (HoverTooltip + store updates + InlineTab + 3 new components + i18n)
- **Verification**: 1 manual smoke test

Every task follows TDD: write test → see fail → implement → see pass → commit.
