# Daemon 背景模式 + Crash Log + Reconnect 修復 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓 tbox daemon 可背景啟動 + crash log 可追查 + reconnect 後自動清除 stale 錯誤訊息

**Architecture:** Phase 1 是 5 行 React fix（transition-aware useEffect 清 testResult），Phase 2 新增 Go `start/stop/status` 子命令（PID file + flock）、panic recover 寫 crash log、`internal/module/logs` 提供 `/api/logs/*` endpoints、SPA 新增 per-host Logs 子頁（2 個子元件）

**Tech Stack:** Go / React 19 / Vitest / Phosphor Icons / Tailwind 4

**Spec:** `docs/superpowers/specs/2026-04-12-daemon-background-mode-design.md`

---

## File Structure

### Phase 1 (修改)
- `spa/src/components/hosts/OverviewSection.tsx` — 加 transition-aware effect
- `spa/src/components/hosts/OverviewSection.test.tsx` — 新增 2 個 test case

### Phase 2 (新增)
- `cmd/tbox/daemon.go` — `start` / `stop` / `status` 子命令
- `cmd/tbox/daemon_test.go` — PID file + flock 測試
- `cmd/tbox/crashlog.go` — `writeCrashLog` + secret redaction
- `cmd/tbox/crashlog_test.go` — crash log 寫入 + redaction 測試
- `internal/module/logs/module.go` — logs module（`/api/logs/daemon` + `/api/logs/crash`）
- `internal/module/logs/module_test.go` — endpoint 測試
- `spa/src/components/hosts/LogsSection.tsx` — Logs 子頁 shell
- `spa/src/components/hosts/DaemonLogBlock.tsx` — daemon log viewer
- `spa/src/components/hosts/CrashLogsBlock.tsx` — crash log viewer
- `spa/src/components/hosts/LogsSection.test.tsx` — 子頁測試

### Phase 2 (修改)
- `cmd/tbox/main.go` — 註冊 `start/stop/status`、`runServe` 加 PID flock + panic recover
- `spa/src/components/HostPage.tsx` — `HostSubPage` union + switch case
- `spa/src/components/hosts/HostSidebar.tsx` — `SUB_PAGES` 加 logs
- `spa/src/locales/en.json` — 新增 `hosts.logs*` key
- `spa/src/locales/zh-TW.json` — 新增 `hosts.logs*` key

---

## Phase 1: 清 stale testResult

### Task 1: testResult transition-aware clear

**Files:**
- Modify: `spa/src/components/hosts/OverviewSection.tsx:1-30`
- Test: `spa/src/components/hosts/OverviewSection.test.tsx`

- [ ] **Step 1: Write the failing tests**

在 `spa/src/components/hosts/OverviewSection.test.tsx` 的 `describe('OverviewSection')` block 末尾追加：

```tsx
it('clears stale testResult when runtime transitions to connected', async () => {
  mockFetchHealth.mockResolvedValue({ ok: false, status: 503 } as Response)

  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test', ip: '1.2.3.4', port: 7860, order: 0, token: 'purdex_testtoken' } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'reconnecting' } },
  })

  const { rerender } = render(<OverviewSection hostId={HOST_ID} />)

  // Simulate a failed test connection
  fireEvent.click(screen.getByText('Test Connection'))
  await waitFor(() => {
    expect(screen.getByText(/Failed to fetch|HTTP 503/)).toBeInTheDocument()
  })

  // Transition runtime to connected
  useHostStore.setState({
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
  rerender(<OverviewSection hostId={HOST_ID} />)

  // Error should be cleared
  await waitFor(() => {
    expect(screen.queryByText(/Failed to fetch|HTTP 503/)).not.toBeInTheDocument()
  })
})

it('does not clear testResult when runtime stays connected', async () => {
  mockFetchHealth.mockResolvedValue({ ok: true } as Response)

  render(<OverviewSection hostId={HOST_ID} />)

  fireEvent.click(screen.getByText('Test Connection'))
  await waitFor(() => {
    expect(screen.getByText(/Connected/)).toBeInTheDocument()
  })

  // Runtime stays connected — rerender should NOT clear the success pill
  useHostStore.setState({
    runtime: { [HOST_ID]: { status: 'connected' } },
  })

  // Success pill should still be visible
  expect(screen.getByText(/Connected/)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/components/hosts/OverviewSection.test.tsx`
Expected: 2 new tests FAIL (no transition-aware effect exists yet)

- [ ] **Step 3: Implement the transition-aware effect**

在 `spa/src/components/hosts/OverviewSection.tsx` 的 import 行修改：

```tsx
import { useEffect, useRef, useState } from 'react'
```

在 `const [closeTabs, setCloseTabs] = useState(true)` 之後（約 line 31 後）加：

```tsx
  const prevStatusRef = useRef(runtime?.status)
  useEffect(() => {
    const prev = prevStatusRef.current
    prevStatusRef.current = runtime?.status
    if (prev !== 'connected' && runtime?.status === 'connected') {
      setTestResult(null)
    }
  }, [runtime?.status])
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/components/hosts/OverviewSection.test.tsx`
Expected: ALL tests PASS

- [ ] **Step 5: Run full lint + test suite**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: No errors

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/hosts/OverviewSection.tsx spa/src/components/hosts/OverviewSection.test.tsx
git commit -m "fix(spa): clear stale testResult on reconnect transition

Only clears when runtime.status transitions from non-connected to
connected, avoiding race where handleTestConnection's manualRetry()
would immediately clear the success pill."
```

---

## Phase 2: Daemon 背景模式 + Crash Log + Logs 子頁

### Task 2: Crash log writer + secret redaction

**Files:**
- Create: `cmd/tbox/crashlog.go`
- Create: `cmd/tbox/crashlog_test.go`

- [ ] **Step 1: Write the failing tests**

Create `cmd/tbox/crashlog_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCrashLog(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")

	writeCrashLog(logsDir, "test panic", []byte("goroutine 1 [running]:\nmain.main()\n"))

	entries, err := filepath.Glob(filepath.Join(logsDir, "crash-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 crash log, got %d", len(entries))
	}

	data, _ := os.ReadFile(entries[0])
	content := string(data)

	if !strings.Contains(content, "Panic: test panic") {
		t.Error("missing panic value")
	}
	if !strings.Contains(content, "goroutine 1") {
		t.Error("missing stack trace")
	}
}

func TestWriteCrashLogRedaction(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")

	setRedactTokens([]string{"supersecret123"})
	defer setRedactTokens(nil)

	panicVal := "Authorization: Bearer tok_abc123\ntoken=purdex_xyz789\nvalue=supersecret123"
	writeCrashLog(logsDir, panicVal, []byte("stack with supersecret123 inside"))

	entries, _ := filepath.Glob(filepath.Join(logsDir, "crash-*.log"))
	data, _ := os.ReadFile(entries[0])
	content := string(data)

	if strings.Contains(content, "tok_abc123") {
		t.Error("Authorization header value not redacted")
	}
	if strings.Contains(content, "purdex_xyz789") {
		t.Error("purdex_ token not redacted")
	}
	if strings.Contains(content, "supersecret123") {
		t.Error("cfg.Token value not redacted")
	}
	if !strings.Contains(content, "[REDACTED]") {
		t.Error("redaction marker missing")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./cmd/tbox/ -run TestWriteCrashLog -v`
Expected: FAIL (function not defined)

- [ ] **Step 3: Implement crashlog.go**

Create `cmd/tbox/crashlog.go`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

var (
	redactMu     sync.RWMutex
	redactTokens []string
)

func setRedactTokens(tokens []string) {
	redactMu.Lock()
	defer redactMu.Unlock()
	redactTokens = tokens
}

var (
	reAuthHeader = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)\S+`)
	rePurdexTok  = regexp.MustCompile(`(?i)(purdex_|tbox_)\S+`)
)

func redactSecrets(s string) string {
	s = reAuthHeader.ReplaceAllString(s, "${1}[REDACTED]")
	s = rePurdexTok.ReplaceAllString(s, "[REDACTED]")

	redactMu.RLock()
	tokens := redactTokens
	redactMu.RUnlock()

	for _, tok := range tokens {
		if tok != "" {
			s = strings.ReplaceAll(s, tok, "[REDACTED]")
		}
	}
	return s
}

func writeCrashLog(logsDir string, panicVal interface{}, stack []byte) {
	os.MkdirAll(logsDir, 0700)

	ts := time.Now().Format("20060102-150405")
	path := filepath.Join(logsDir, fmt.Sprintf("crash-%s.log", ts))

	bi, _ := debug.ReadBuildInfo()
	goVersion := runtime.Version()
	version := "unknown"
	if bi != nil && bi.Main.Version != "" {
		version = bi.Main.Version
	}
	// Try reading VERSION file (one level up from logs dir)
	if vData, err := os.ReadFile(filepath.Join(filepath.Dir(logsDir), "..", "VERSION")); err == nil {
		version = strings.TrimSpace(string(vData))
	}

	content := fmt.Sprintf("Time:        %s\nVersion:     %s\nGo Runtime:  %s\nGoroutines:  %d\n\nPanic: %v\n\nStack:\n%s\n",
		time.Now().Format(time.RFC3339),
		version,
		goVersion,
		runtime.NumGoroutine(),
		panicVal,
		string(stack),
	)

	content = redactSecrets(content)
	os.WriteFile(path, []byte(content), 0600)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./cmd/tbox/ -run TestWriteCrashLog -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/tbox/crashlog.go cmd/tbox/crashlog_test.go
git commit -m "feat(daemon): add crash log writer with secret redaction"
```

### Task 3: PID file management with flock

**Files:**
- Create: `cmd/tbox/daemon.go`
- Create: `cmd/tbox/daemon_test.go`

- [ ] **Step 1: Write the failing tests**

Create `cmd/tbox/daemon_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPidFileLockAndUnlock(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tbox.pid")

	// Acquire lock
	f, err := acquirePidLock(pidPath, os.Getpid())
	if err != nil {
		t.Fatalf("acquirePidLock: %v", err)
	}

	// PID file should contain our PID
	data, _ := os.ReadFile(pidPath)
	pid, _ := strconv.Atoi(string(data))
	if pid != os.Getpid() {
		t.Errorf("pid file = %d, want %d", pid, os.Getpid())
	}

	// Second acquire should fail
	_, err = acquirePidLock(pidPath, os.Getpid()+1)
	if err == nil {
		t.Fatal("expected error for second lock, got nil")
	}

	// Release
	releasePidLock(f, pidPath)

	// After release, acquire should succeed again
	f2, err := acquirePidLock(pidPath, os.Getpid())
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	releasePidLock(f2, pidPath)
}

func TestIsDaemonRunning(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tbox.pid")

	// No PID file — not running
	running, pid := isDaemonRunning(pidPath)
	if running {
		t.Error("expected not running when no PID file")
	}
	if pid != 0 {
		t.Errorf("expected pid=0, got %d", pid)
	}

	// Lock held — running
	f, _ := acquirePidLock(pidPath, os.Getpid())
	running, pid = isDaemonRunning(pidPath)
	if !running {
		t.Error("expected running when lock held")
	}
	if pid != os.Getpid() {
		t.Errorf("expected pid=%d, got %d", os.Getpid(), pid)
	}
	releasePidLock(f, pidPath)

	// Stale PID file (no lock held) — not running
	os.WriteFile(pidPath, []byte("99999"), 0644)
	running, _ = isDaemonRunning(pidPath)
	if running {
		t.Error("expected not running with stale PID file")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./cmd/tbox/ -run "TestPidFile|TestIsDaemon" -v`
Expected: FAIL (functions not defined)

- [ ] **Step 3: Implement daemon.go**

Create `cmd/tbox/daemon.go`:

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wake/tmux-box/internal/config"
)

func acquirePidLock(pidPath string, pid int) (*os.File, error) {
	f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open pid file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		data, _ := os.ReadFile(pidPath)
		existingPid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return nil, fmt.Errorf("already running (pid %d)", existingPid)
	}

	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "%d", pid)
	f.Sync()

	return f, nil
}

func releasePidLock(f *os.File, pidPath string) {
	if f != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
	os.Remove(pidPath)
}

func isDaemonRunning(pidPath string) (bool, int) {
	f, err := os.OpenFile(pidPath, os.O_RDWR, 0644)
	if err != nil {
		return false, 0
	}
	defer f.Close()

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Lock held by another process — daemon is running
		data, _ := os.ReadFile(pidPath)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return true, pid
	}

	// We got the lock — no daemon running. Release it.
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	// Read stale PID for info
	data, _ := os.ReadFile(pidPath)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return false, pid
}

func runStart(args []string) {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	pidPath := filepath.Join(cfg.DataDir, "tbox.pid")

	// Pre-check: is daemon already running?
	if running, pid := isDaemonRunning(pidPath); running {
		fmt.Fprintf(os.Stderr, "tbox: already running (pid %d)\n", pid)
		os.Exit(1)
	}

	// Ensure logs directory
	logsDir := filepath.Join(cfg.DataDir, "logs")
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		log.Fatalf("create logs dir: %v", err)
	}

	logFile, err := os.OpenFile(filepath.Join(logsDir, "tbox.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("open log file: %v", err)
	}

	// Resolve --config to absolute path if present
	resolvedArgs := make([]string, len(args))
	copy(resolvedArgs, args)
	for i, a := range resolvedArgs {
		if (a == "--config" || a == "-config") && i+1 < len(resolvedArgs) {
			abs, err := filepath.Abs(resolvedArgs[i+1])
			if err == nil {
				resolvedArgs[i+1] = abs
			}
		}
	}

	self, err := os.Executable()
	if err != nil {
		log.Fatalf("os.Executable: %v", err)
	}

	cmd := exec.Command(self, append([]string{"serve"}, resolvedArgs...)...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		log.Fatalf("start daemon: %v", err)
	}
	logFile.Close()

	childPid := cmd.Process.Pid

	// Wait for health check
	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	healthURL := fmt.Sprintf("http://%s/api/health", addr)
	healthy := false

	time.Sleep(500 * time.Millisecond)
	for i := 0; i < 5; i++ {
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			healthy = true
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}

	if !healthy {
		fmt.Fprintf(os.Stderr, "tbox: daemon started but health check failed\n")
		fmt.Fprintf(os.Stderr, "tbox: last 20 lines of %s:\n\n", filepath.Join(logsDir, "tbox.log"))
		tailCmd := exec.Command("tail", "-n", "20", filepath.Join(logsDir, "tbox.log"))
		tailCmd.Stdout = os.Stderr
		tailCmd.Run()
		os.Exit(1)
	}

	logPath := filepath.Join(logsDir, "tbox.log")
	fmt.Printf("tbox daemon started (pid %d, bind %s, log %s)\n", childPid, addr, logPath)
}

func runStop(_ []string) {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	pidPath := filepath.Join(cfg.DataDir, "tbox.pid")

	running, pid := isDaemonRunning(pidPath)
	if !running {
		fmt.Println("tbox: not running")
		return
	}

	// Send SIGTERM
	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tbox: cannot find process %d: %v\n", pid, err)
		os.Exit(1)
	}

	proc.Signal(syscall.SIGTERM)

	// Poll with flock (up to 30 seconds)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if r, _ := isDaemonRunning(pidPath); !r {
			fmt.Printf("tbox: stopped (pid %d)\n", pid)
			os.Remove(pidPath)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Timeout — force kill
	fmt.Fprintf(os.Stderr, "tbox: daemon did not stop within 30s, sending SIGKILL\n")
	proc.Signal(syscall.SIGKILL)
	time.Sleep(1 * time.Second)
	os.Remove(pidPath)
}

func runStatus(_ []string) {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	pidPath := filepath.Join(cfg.DataDir, "tbox.pid")
	logsDir := filepath.Join(cfg.DataDir, "logs")
	logPath := filepath.Join(logsDir, "tbox.log")

	running, pid := isDaemonRunning(pidPath)
	if !running {
		fmt.Println("Status:  stopped")
		os.Exit(1)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	healthURL := fmt.Sprintf("http://%s/api/health", addr)

	health := "unreachable"
	resp, err := http.Get(healthURL)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			health = "ok"
		} else {
			health = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}

	fmt.Printf("Status:  running\n")
	fmt.Printf("PID:     %d\n", pid)
	fmt.Printf("Bind:    %s\n", addr)
	fmt.Printf("Health:  %s\n", health)
	fmt.Printf("Log:     %s\n", logPath)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./cmd/tbox/ -run "TestPidFile|TestIsDaemon" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/tbox/daemon.go cmd/tbox/daemon_test.go
git commit -m "feat(daemon): add start/stop/status subcommands with flock PID management"
```

### Task 4: Wire subcommands + panic recover in main.go

**Files:**
- Modify: `cmd/tbox/main.go:30-50` (switch), `cmd/tbox/main.go:53-60` (runServe top)

- [ ] **Step 1: Register new subcommands in main.go**

In `cmd/tbox/main.go`, add to the switch statement (after the `case "token":` line, before `default:`):

```go
	case "start":
		runStart(os.Args[2:])
	case "stop":
		runStop(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
```

Update the usage line to include the new commands:

```go
		fmt.Fprintf(os.Stderr, "Commands: serve, start, stop, status, relay, hook, setup, token\n")
```

- [ ] **Step 2: Add PID file flock + panic recover in runServe**

Add import `"runtime/debug"` to the import block in `cmd/tbox/main.go`.

At the very beginning of `runServe`, before `fs := flag.NewFlagSet(...)`, add:

```go
	defer func() {
		if r := recover(); r != nil {
			home, _ := os.UserHomeDir()
			logsDir := filepath.Join(home, ".config", "tbox", "logs")
			writeCrashLog(logsDir, r, debug.Stack())
			panic(r)
		}
	}()
```

After `log.Printf("host_id: %s", hostID)` (around line 87), add PID file flock + token redaction setup:

```go
	// Acquire PID file lock (for tbox start/stop/status)
	pidPath := filepath.Join(cfg.DataDir, "tbox.pid")
	pidFile, pidErr := acquirePidLock(pidPath, os.Getpid())
	if pidErr != nil {
		log.Printf("pid lock: %v (another instance may be running)", pidErr)
	} else {
		defer releasePidLock(pidFile, pidPath)
	}

	// Register token for crash log redaction
	if cfg.Token != "" {
		setRedactTokens([]string{cfg.Token})
	}
```

- [ ] **Step 3: Run Go tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./cmd/tbox/ -v`
Expected: ALL PASS

- [ ] **Step 4: Build and smoke test**

Run: `cd /Users/wake/Workspace/wake/tmux-box && make build && bin/tbox status`
Expected: `Status:  stopped` (exit code 1) or `Status:  running` if daemon is alive

- [ ] **Step 5: Commit**

```bash
git add cmd/tbox/main.go
git commit -m "feat(daemon): wire start/stop/status commands + panic recover in runServe"
```

### Task 5: Logs module (Go API endpoints)

**Files:**
- Create: `internal/module/logs/module.go`
- Create: `internal/module/logs/module_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/module/logs/module_test.go`:

```go
package logs

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleDaemonLog(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0700)
	os.WriteFile(filepath.Join(logsDir, "tbox.log"), []byte("line1\nline2\nline3\nline4\nline5\n"), 0644)

	m := &LogsModule{logsDir: logsDir}

	req := httptest.NewRequest("GET", "/api/logs/daemon?tail=3", nil)
	w := httptest.NewRecorder()
	m.handleDaemonLog(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 3 {
		t.Errorf("got %d lines, want 3: %q", len(lines), body)
	}
}

func TestHandleDaemonLogNoFile(t *testing.T) {
	m := &LogsModule{logsDir: t.TempDir()}

	req := httptest.NewRequest("GET", "/api/logs/daemon", nil)
	w := httptest.NewRecorder()
	m.handleDaemonLog(w, req)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestHandleCrashLog(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0700)
	os.WriteFile(filepath.Join(logsDir, "crash-20260412-041136.log"), []byte("crash content"), 0644)

	m := &LogsModule{logsDir: logsDir}

	req := httptest.NewRequest("GET", "/api/logs/crash", nil)
	w := httptest.NewRecorder()
	m.handleCrashLog(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "crash content") {
		t.Error("missing crash content")
	}
}

func TestHandleCrashLogNone(t *testing.T) {
	m := &LogsModule{logsDir: t.TempDir()}

	req := httptest.NewRequest("GET", "/api/logs/crash", nil)
	w := httptest.NewRecorder()
	m.handleCrashLog(w, req)

	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestHandleCrashLogPicksLatest(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0700)
	os.WriteFile(filepath.Join(logsDir, "crash-20260410-120000.log"), []byte("older"), 0644)
	os.WriteFile(filepath.Join(logsDir, "crash-20260412-120000.log"), []byte("newer"), 0644)

	m := &LogsModule{logsDir: logsDir}

	req := httptest.NewRequest("GET", "/api/logs/crash", nil)
	w := httptest.NewRecorder()
	m.handleCrashLog(w, req)

	if !strings.Contains(w.Body.String(), "newer") {
		t.Errorf("expected latest crash log, got: %s", w.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/logs/ -v`
Expected: FAIL (package not found)

- [ ] **Step 3: Implement logs module**

Create `internal/module/logs/module.go`:

```go
package logs

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/wake/tmux-box/internal/core"
)

type LogsModule struct {
	logsDir string
}

func New() *LogsModule {
	return &LogsModule{}
}

func (m *LogsModule) Name() string           { return "logs" }
func (m *LogsModule) Dependencies() []string { return nil }

func (m *LogsModule) Init(c *core.Core) error {
	m.logsDir = filepath.Join(c.Cfg.DataDir, "logs")
	return nil
}

func (m *LogsModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logs/daemon", m.handleDaemonLog)
	mux.HandleFunc("GET /api/logs/crash", m.handleCrashLog)
}

func (m *LogsModule) Start(_ context.Context) error {
	log.Println("[logs] endpoints enabled")
	return nil
}

func (m *LogsModule) Stop(_ context.Context) error { return nil }

var reCrashFile = regexp.MustCompile(`^crash-\d{8}-\d{6}\.log$`)

func (m *LogsModule) handleDaemonLog(w http.ResponseWriter, r *http.Request) {
	logPath := filepath.Join(m.logsDir, "tbox.log")
	if _, err := os.Stat(logPath); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	tailN := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 2000 {
				n = 2000
			}
			tailN = n
		}
	}

	cmd := exec.Command("tail", "-n", strconv.Itoa(tailN), logPath)
	out, err := cmd.Output()
	if err != nil {
		http.Error(w, "failed to read log", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(out)
}

func (m *LogsModule) handleCrashLog(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(m.logsDir)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var crashFiles []string
	for _, e := range entries {
		name := e.Name()
		cleaned := filepath.Base(filepath.Clean(name))
		if cleaned != name {
			continue
		}
		if reCrashFile.MatchString(name) {
			crashFiles = append(crashFiles, name)
		}
	}

	if len(crashFiles) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	sort.Strings(crashFiles)
	latest := crashFiles[len(crashFiles)-1]

	fullPath := filepath.Join(m.logsDir, latest)
	if !strings.HasPrefix(fullPath, m.logsDir) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "failed to read crash log", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/logs/ -v`
Expected: ALL PASS

- [ ] **Step 5: Register logs module in main.go**

In `cmd/tbox/main.go`, add import:

```go
	"github.com/wake/tmux-box/internal/module/logs"
```

After `c.AddModule(files.New())` (around line 122), add:

```go
	c.AddModule(logs.New())
```

- [ ] **Step 6: Run full Go tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./... -count=1`
Expected: ALL PASS

- [ ] **Step 7: Commit**

```bash
git add internal/module/logs/ cmd/tbox/main.go
git commit -m "feat(daemon): add logs module with /api/logs/daemon and /api/logs/crash endpoints"
```

### Task 6: SPA — i18n keys + routing

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`
- Modify: `spa/src/components/HostPage.tsx:13, 44-55`
- Modify: `spa/src/components/hosts/HostSidebar.tsx:7-13`

- [ ] **Step 1: Add i18n keys to en.json**

In `spa/src/locales/en.json`, after the `"hosts.uploads"` line, add:

```json
  "hosts.logs": "Logs",
  "hosts.logs_daemon": "Daemon Log",
  "hosts.logs_crash": "Crash Logs",
  "hosts.logs_no_crash": "No crashes recorded",
  "hosts.logs_refresh": "Refresh",
  "hosts.logs_offline": "Host is offline",
```

- [ ] **Step 2: Add i18n keys to zh-TW.json**

In `spa/src/locales/zh-TW.json`, after the `"hosts.uploads"` line, add:

```json
  "hosts.logs": "日誌",
  "hosts.logs_daemon": "Daemon 日誌",
  "hosts.logs_crash": "Crash 紀錄",
  "hosts.logs_no_crash": "尚無 Crash 紀錄",
  "hosts.logs_refresh": "重新整理",
  "hosts.logs_offline": "主機離線中",
```

- [ ] **Step 3: Add 'logs' to HostSubPage union**

In `spa/src/components/HostPage.tsx:13`, change:

```tsx
export type HostSubPage = 'overview' | 'sessions' | 'hooks' | 'agents' | 'uploads'
```

to:

```tsx
export type HostSubPage = 'overview' | 'sessions' | 'hooks' | 'agents' | 'uploads' | 'logs'
```

Add import at the top of `HostPage.tsx`:

```tsx
import { LogsSection } from './hosts/LogsSection'
```

In `renderContent()` switch, before the closing `}` of the switch (after the `case 'uploads':` block), add:

```tsx
      case 'logs':
        return <LogsSection hostId={effectiveSelection.hostId} />
```

- [ ] **Step 4: Add 'logs' to SUB_PAGES**

In `spa/src/components/hosts/HostSidebar.tsx:7-13`, add to the `SUB_PAGES` array:

```tsx
  { id: 'logs', labelKey: 'hosts.logs' },
```

- [ ] **Step 5: Run lint**

Run: `cd spa && pnpm run lint`
Expected: Error about missing `LogsSection` (we'll create it next). This is expected — we need a stub first.

- [ ] **Step 6: Create LogsSection stub**

Create `spa/src/components/hosts/LogsSection.tsx`:

```tsx
interface Props {
  hostId: string
}

export function LogsSection({ hostId }: Props) {
  return <div data-testid="logs-section">{hostId}</div>
}
```

- [ ] **Step 7: Run lint + locale completeness test**

Run: `cd spa && pnpm run lint && npx vitest run src/locales/locale-completeness.test.ts`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add spa/src/locales/en.json spa/src/locales/zh-TW.json spa/src/components/HostPage.tsx spa/src/components/hosts/HostSidebar.tsx spa/src/components/hosts/LogsSection.tsx
git commit -m "feat(spa): add logs sub-page routing + i18n keys"
```

### Task 7: SPA — DaemonLogBlock component

**Files:**
- Create: `spa/src/components/hosts/DaemonLogBlock.tsx`

- [ ] **Step 1: Implement DaemonLogBlock**

Create `spa/src/components/hosts/DaemonLogBlock.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { hostFetch } from '../../lib/host-api'

interface Props {
  hostId: string
}

export function DaemonLogBlock({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const runtime = useHostStore((s) => s.runtime[hostId])
  const [content, setContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const isOffline = runtime?.status !== 'connected'

  const fetchLog = useCallback(async () => {
    if (isOffline) return
    setLoading(true)
    try {
      const res = await hostFetch(hostId, '/api/logs/daemon?tail=200')
      if (res.status === 204) {
        setContent(null)
      } else if (res.ok) {
        const text = await res.text()
        setContent(text || null)
      }
    } catch {
      /* ignore — host may be offline */
    } finally {
      setLoading(false)
    }
  }, [hostId, isOffline])

  useEffect(() => {
    fetchLog()
  }, [fetchLog])

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold text-text-primary">{t('hosts.logs_daemon')}</h3>
        <button
          onClick={fetchLog}
          disabled={loading || isOffline}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary border border-border-default text-text-secondary cursor-pointer disabled:opacity-50"
        >
          <ArrowsClockwise size={12} className={loading ? 'animate-spin' : ''} />
          {t('hosts.logs_refresh')}
        </button>
      </div>
      {isOffline ? (
        <p className="text-xs text-text-muted">{t('hosts.logs_offline')}</p>
      ) : content ? (
        <pre className="text-xs font-mono bg-surface-primary border border-border-subtle rounded p-3 overflow-auto max-h-96 whitespace-pre-wrap text-text-secondary">
          {content}
        </pre>
      ) : (
        <p className="text-xs text-text-muted">{t('hosts.loading')}</p>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add spa/src/components/hosts/DaemonLogBlock.tsx
git commit -m "feat(spa): add DaemonLogBlock component"
```

### Task 8: SPA — CrashLogsBlock component

**Files:**
- Create: `spa/src/components/hosts/CrashLogsBlock.tsx`

- [ ] **Step 1: Implement CrashLogsBlock**

Create `spa/src/components/hosts/CrashLogsBlock.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { hostFetch } from '../../lib/host-api'

interface Props {
  hostId: string
}

export function CrashLogsBlock({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const runtime = useHostStore((s) => s.runtime[hostId])
  const [content, setContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const isOffline = runtime?.status !== 'connected'

  const fetchCrash = useCallback(async () => {
    if (isOffline) return
    setLoading(true)
    try {
      const res = await hostFetch(hostId, '/api/logs/crash')
      if (res.status === 204) {
        setContent(null)
      } else if (res.ok) {
        const text = await res.text()
        setContent(text || null)
      }
    } catch {
      /* ignore */
    } finally {
      setLoading(false)
    }
  }, [hostId, isOffline])

  useEffect(() => {
    fetchCrash()
  }, [fetchCrash])

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold text-text-primary">{t('hosts.logs_crash')}</h3>
        <button
          onClick={fetchCrash}
          disabled={loading || isOffline}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary border border-border-default text-text-secondary cursor-pointer disabled:opacity-50"
        >
          <ArrowsClockwise size={12} className={loading ? 'animate-spin' : ''} />
          {t('hosts.logs_refresh')}
        </button>
      </div>
      {isOffline ? (
        <p className="text-xs text-text-muted">{t('hosts.logs_offline')}</p>
      ) : content ? (
        <pre className="text-xs font-mono bg-red-500/5 border border-red-500/20 rounded p-3 overflow-auto max-h-96 whitespace-pre-wrap text-text-secondary">
          {content}
        </pre>
      ) : (
        <p className="text-xs text-text-muted">{t('hosts.logs_no_crash')}</p>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add spa/src/components/hosts/CrashLogsBlock.tsx
git commit -m "feat(spa): add CrashLogsBlock component"
```

### Task 9: SPA — Wire LogsSection with child components

**Files:**
- Modify: `spa/src/components/hosts/LogsSection.tsx`

- [ ] **Step 1: Replace LogsSection stub with full implementation**

Replace `spa/src/components/hosts/LogsSection.tsx` content:

```tsx
import { useHostStore } from '../../stores/useHostStore'
import { DaemonLogBlock } from './DaemonLogBlock'
import { CrashLogsBlock } from './CrashLogsBlock'

interface Props {
  hostId: string
}

export function LogsSection({ hostId }: Props) {
  const host = useHostStore((s) => s.hosts[hostId])

  if (!host) return null

  return (
    <div className="max-w-2xl space-y-6">
      <h2 className="text-lg font-semibold">{host.name}</h2>
      <DaemonLogBlock hostId={hostId} />
      <CrashLogsBlock hostId={hostId} />
    </div>
  )
}
```

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/hosts/LogsSection.tsx
git commit -m "feat(spa): wire LogsSection with DaemonLogBlock + CrashLogsBlock"
```

### Task 10: SPA — LogsSection tests

**Files:**
- Create: `spa/src/components/hosts/LogsSection.test.tsx`

- [ ] **Step 1: Write tests**

Create `spa/src/components/hosts/LogsSection.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { LogsSection } from './LogsSection'
import { useHostStore } from '../../stores/useHostStore'

vi.mock('../../lib/host-api', () => ({
  hostFetch: vi.fn(),
}))

import { hostFetch } from '../../lib/host-api'

const mockHostFetch = vi.mocked(hostFetch)
const HOST_ID = 'test-host'

beforeEach(() => {
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'TestHost', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
})

describe('LogsSection', () => {
  it('returns null when host does not exist', () => {
    const { container } = render(<LogsSection hostId="nonexistent" />)
    expect(container.innerHTML).toBe('')
  })

  it('renders host name and both blocks', async () => {
    mockHostFetch.mockResolvedValue({ ok: true, status: 204 } as Response)

    render(<LogsSection hostId={HOST_ID} />)

    expect(screen.getByRole('heading', { level: 2, name: 'TestHost' })).toBeInTheDocument()
    expect(screen.getByText('Daemon Log')).toBeInTheDocument()
    expect(screen.getByText('Crash Logs')).toBeInTheDocument()
  })
})

describe('DaemonLogBlock', () => {
  it('shows daemon log content', async () => {
    mockHostFetch.mockImplementation(async (_hostId, path) => {
      if (String(path).includes('/api/logs/daemon')) {
        return { ok: true, status: 200, text: () => Promise.resolve('log line 1\nlog line 2') } as Response
      }
      return { ok: false, status: 204 } as Response
    })

    render(<LogsSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText(/log line 1/)).toBeInTheDocument()
    })
  })

  it('shows offline message when host disconnected', () => {
    useHostStore.setState({
      runtime: { [HOST_ID]: { status: 'reconnecting' } },
    })

    render(<LogsSection hostId={HOST_ID} />)

    const offlineMessages = screen.getAllByText('Host is offline')
    expect(offlineMessages.length).toBeGreaterThanOrEqual(1)
  })

  it('refresh button fetches again', async () => {
    mockHostFetch.mockResolvedValue({
      ok: true, status: 200, text: () => Promise.resolve('initial log'),
    } as Response)

    render(<LogsSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText(/initial log/)).toBeInTheDocument()
    })

    mockHostFetch.mockResolvedValue({
      ok: true, status: 200, text: () => Promise.resolve('refreshed log'),
    } as Response)

    const refreshButtons = screen.getAllByText('Refresh')
    fireEvent.click(refreshButtons[0])

    await waitFor(() => {
      expect(screen.getByText(/refreshed log/)).toBeInTheDocument()
    })
  })
})

describe('CrashLogsBlock', () => {
  it('shows no crashes message when 204', async () => {
    mockHostFetch.mockResolvedValue({ ok: true, status: 204 } as Response)

    render(<LogsSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('No crashes recorded')).toBeInTheDocument()
    })
  })

  it('shows crash content when available', async () => {
    mockHostFetch.mockImplementation(async (_hostId, path) => {
      if (String(path).includes('/api/logs/crash')) {
        return { ok: true, status: 200, text: () => Promise.resolve('Panic: test panic\nStack: ...') } as Response
      }
      return { ok: false, status: 204 } as Response
    })

    render(<LogsSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText(/Panic: test panic/)).toBeInTheDocument()
    })
  })
})
```

- [ ] **Step 2: Run tests**

Run: `cd spa && npx vitest run src/components/hosts/LogsSection.test.tsx`
Expected: ALL PASS

- [ ] **Step 3: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/hosts/LogsSection.test.tsx
git commit -m "test(spa): add LogsSection, DaemonLogBlock, CrashLogsBlock tests"
```

### Task 11: Create GitHub issues for deferred work

**Files:** none (GitHub CLI only)

- [ ] **Step 1: Create safeGo issue**

```bash
gh issue create --title "feat(daemon): add safeGo helper for cross-goroutine panic recovery" \
  --body "$(cat <<'EOF'
## Context

`runServe`'s panic recover defer only covers the main goroutine (startup phase).
Runtime panics in HTTP handlers and module background goroutines bypass it entirely.

## Proposal

Add a `safeGo(fn func())` helper that wraps goroutine launches with:
- `recover()` + crash log write
- Signal main context to shut down cleanly (cancel ctx, call srv.Shutdown)
- Ensure DB closes happen before process exits

## References

- Spec: `docs/superpowers/specs/2026-04-12-daemon-background-mode-design.md` (review finding P10)
EOF
)" --label "feature" --label "daemon"
```

- [ ] **Step 2: Create HTTP recover middleware issue**

```bash
gh issue create --title "feat(daemon): add HTTP recover middleware to prevent single-handler panics from crashing daemon" \
  --body "$(cat <<'EOF'
## Context

A panic in a single HTTP handler currently crashes the entire daemon.
Adding a recover middleware at the outerMux level would catch per-request panics,
log them, return 500, and keep the daemon alive.

## References

- Spec: `docs/superpowers/specs/2026-04-12-daemon-background-mode-design.md` (review finding P18)
EOF
)" --label "feature" --label "daemon"
```

- [ ] **Step 3: Commit — N/A (no code changes)**

### Task 12: Manual smoke test + build verification

- [ ] **Step 1: Build daemon**

Run: `cd /Users/wake/Workspace/wake/tmux-box && make build`
Expected: `bin/tbox` built successfully

- [ ] **Step 2: Test start/status/stop cycle**

```bash
bin/tbox start
bin/tbox status
bin/tbox stop
```
Expected: start prints pid/bind/log, status shows running/ok, stop shows stopped

- [ ] **Step 3: Test crash log generation**

Temporarily add a deliberate panic in `runServe` (after module init), run `bin/tbox serve`, verify `~/.config/tbox/logs/crash-*.log` is created with proper content and redacted secrets.

- [ ] **Step 4: Start SPA dev server and verify Logs sub-page**

Run: `cd spa && pnpm run dev`

In browser at `http://100.64.0.2:5174`:
1. Navigate to Hosts → mlab → Logs
2. Verify Daemon Log block shows content (or loading state)
3. Verify Crash Logs block shows "No crashes recorded" or crash content
4. Click Refresh buttons — content updates
5. Verify other sub-pages still work

- [ ] **Step 5: Run full test suites**

```bash
cd /Users/wake/Workspace/wake/tmux-box && go test ./... -count=1
cd spa && pnpm run lint && npx vitest run
```
Expected: ALL PASS
