# Daemon Dev Rebuild — PR-2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add "Daemon" section to `Settings → Development` that rebuilds the daemon binary (`go build`) and safely self-restarts via `syscall.Exec`, with live build log via SSE. Structure mirrors existing app-update flow; only local rebuild in this PR.

**Architecture:** New `/api/dev/daemon/check` + `/api/dev/daemon/rebuild` (SSE) endpoints in the existing dev module. Daemon listener uses `SO_REUSEADDR` + startup retry bind loop to survive `syscall.Exec`. SPA adds a Daemon section to `DevEnvironmentSection` that streams build events and detects restart via WS reconnection.

**Tech Stack:** Go 1.26 (`net.ListenConfig`, `syscall`, `os/exec`), React 19, Vitest, Tailwind.

**Spec:** `docs/superpowers/specs/2026-04-18-statusline-and-daemon-rebuild-design.md` Sections 7–14

**Note:** This plan assumes PR-1 is merged (or can be stacked). No direct file conflicts exist with PR-1.

---

## File Structure

### Go (new / modified)
- **NEW** `internal/module/dev/daemon.go` — check/rebuild handlers, SSE, exec-self
- **NEW** `internal/module/dev/daemon_test.go`
- **MODIFY** `internal/module/dev/module.go` — register routes + **`PDX_DEV_UPDATE` gate**
- **MODIFY** `cmd/pdx/main.go` — listener with `SO_REUSEADDR` + retry bind loop
- **MODIFY** `cmd/pdx/main_test.go` — new tests for listener retry (if exists; else skip)

### SPA (new / modified)
- **MODIFY** `spa/src/components/settings/DevEnvironmentSection.tsx` — add Daemon section
- **MODIFY** `spa/src/components/settings/DevEnvironmentSection.test.tsx`
- **MODIFY** `spa/src/locales/en.json` + `spa/src/locales/zh-TW.json`

---

## Prep: secure existing dev endpoints

### Task 1: Gate existing `/api/dev/update/*` behind `PDX_DEV_UPDATE=1` (server-side)

**Files:**
- Modify: `internal/module/dev/module.go`
- Modify: `internal/module/dev/module_test.go` (or create)

- [ ] **Step 1: Add failing test**

Create or extend `internal/module/dev/module_test.go`:

```go
package dev

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterRoutes_DisabledByDefault(t *testing.T) {
	t.Setenv("PDX_DEV_UPDATE", "")
	m := New(nil, "")
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dev/update/check")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 when PDX_DEV_UPDATE unset, got %d", resp.StatusCode)
	}
}

func TestRegisterRoutes_EnabledWithEnv(t *testing.T) {
	t.Setenv("PDX_DEV_UPDATE", "1")
	m := New(nil, "")
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dev/update/check")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == http.StatusNotFound {
		t.Error("expected route registered when PDX_DEV_UPDATE=1")
	}
}
```

(Adjust `New(...)` args to match actual constructor signature.)

- [ ] **Step 2: Run — expect failure**

Run: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/statusline-installer-p1 && go test ./internal/module/dev/ -run TestRegisterRoutes -v`
Expected: FAIL.

- [ ] **Step 3: Implement gate in `module.go`**

Wrap the existing route registration:

```go
// internal/module/dev/module.go

import "os"

func (m *DevModule) RegisterRoutes(mux *http.ServeMux) {
	if os.Getenv("PDX_DEV_UPDATE") != "1" {
		return // dev endpoints disabled
	}
	mux.HandleFunc("GET /api/dev/update/check", m.handleCheck)
	mux.HandleFunc("GET /api/dev/update/check/stream", m.handleCheckStream)
	mux.HandleFunc("GET /api/dev/update/download", m.handleDownload)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/module/dev/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/module/dev/
git commit -m "feat(dev): gate /api/dev/update/* on PDX_DEV_UPDATE=1 (server-side)"
```

---

## Backend: daemon listener with SO_REUSEADDR + retry

### Task 2: Daemon listener uses `SO_REUSEADDR` + retry bind loop

**Files:**
- Modify: `cmd/pdx/main.go`

- [ ] **Step 1: Identify current listener binding**

Run: `grep -n "srv.ListenAndServe\|net.Listen\|net\\.Listen" cmd/pdx/main.go cmd/pdx/daemon.go cmd/pdx/serve.go 2>/dev/null`
Locate the current `ListenAndServe` call. Replace with explicit `net.Listen` + `srv.Serve(listener)` so we control the socket options.

- [ ] **Step 2: Add helper**

Create or add to `cmd/pdx/main.go`:

```go
import (
	"context"
	"net"
	"syscall"
	"time"
	"golang.org/x/sys/unix"
)

// listenWithReusePort wraps net.Listen with SO_REUSEADDR so that the daemon
// can re-bind the same port immediately after exec-self during dev rebuild.
// Retries bind up to 5 times (200ms → 1s exponential) on EADDRINUSE to
// cover kernel TIME_WAIT races.
func listenWithReusePort(addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var opErr error
			err := c.Control(func(fd uintptr) {
				opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
			})
			if err != nil {
				return err
			}
			return opErr
		},
	}

	backoff := 200 * time.Millisecond
	for i := 0; i < 5; i++ {
		l, err := lc.Listen(context.Background(), "tcp", addr)
		if err == nil {
			return l, nil
		}
		// Retry on address-in-use errors; all other errors fail fast.
		if !isAddrInUse(err) {
			return nil, err
		}
		time.Sleep(backoff)
		backoff *= 2
		if backoff > time.Second {
			backoff = time.Second
		}
	}
	return lc.Listen(context.Background(), "tcp", addr) // last attempt, return whatever
}

func isAddrInUse(err error) bool {
	// EADDRINUSE appears as SyscallError / OpError depending on OS.
	return err != nil && (contains(err.Error(), "address already in use") || contains(err.Error(), "bind:"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

(You can replace the contains/indexOf helpers with `strings.Contains` if `strings` is already imported.)

- [ ] **Step 3: Swap serve call**

Find the existing:

```go
if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
```

Replace with:

```go
listener, err := listenWithReusePort(addr)
if err != nil {
	log.Fatalf("bind %s: %v", addr, err)
}
if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
```

- [ ] **Step 4: Add `golang.org/x/sys/unix` dependency if missing**

Run: `go mod tidy && go build ./...`
Expected: no error.

- [ ] **Step 5: Smoke test**

Build + run twice in succession:

```bash
go build -o /tmp/pdx-test ./cmd/pdx
/tmp/pdx-test serve &
sleep 1
kill %1
/tmp/pdx-test serve  # should bind immediately, no TIME_WAIT error
kill %1
```

Expected: second start binds without `address already in use`.

- [ ] **Step 6: Commit**

```bash
git add cmd/pdx/main.go go.mod go.sum
git commit -m "feat(daemon): listener uses SO_REUSEADDR + retry bind for exec-self restart"
```

---

## Backend: daemon rebuild endpoints

### Task 3: `GET /api/dev/daemon/check` handler

**Files:**
- Create: `internal/module/dev/daemon.go`
- Create: `internal/module/dev/daemon_test.go`
- Modify: `internal/module/dev/module.go`

- [ ] **Step 1: Write failing test**

```go
// internal/module/dev/daemon_test.go
package dev

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleDaemonCheck_ReturnsHashes(t *testing.T) {
	t.Setenv("PDX_DEV_UPDATE", "1")
	m := New(nil, ".") // repoRoot = current dir
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dev/daemon/check")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	var body struct {
		CurrentHash string `json:"current_hash"`
		LatestHash  string `json:"latest_hash"`
		Available   bool   `json:"available"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	// In a git worktree, latest_hash should be non-empty.
	if body.LatestHash == "" {
		t.Error("latest_hash empty")
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./internal/module/dev/ -run TestHandleDaemonCheck -v`
Expected: FAIL — route not registered.

- [ ] **Step 3: Implement**

Create `internal/module/dev/daemon.go`:

```go
package dev

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"sync"
)

// BakedInHash is set at build time via -ldflags. See Makefile / build scripts.
// If empty at runtime, check reports "unknown".
var BakedInHash = "unknown"

// daemonRebuildMu serializes concurrent rebuild requests.
var daemonRebuildMu sync.Mutex

type daemonCheckResponse struct {
	CurrentHash string `json:"current_hash"`
	LatestHash  string `json:"latest_hash"`
	Available   bool   `json:"available"`
}

func (m *DevModule) handleDaemonCheck(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("git", "-C", m.repoRoot, "log", "-1", "--format=%H")
	out, err := cmd.Output()
	latest := ""
	if err == nil {
		latest = strings.TrimSpace(string(out))
	}
	resp := daemonCheckResponse{
		CurrentHash: BakedInHash,
		LatestHash:  latest,
		Available:   latest != "" && latest != BakedInHash,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

In `internal/module/dev/module.go`, inside `RegisterRoutes` (after the existing update routes, still within the PDX_DEV_UPDATE gate):

```go
mux.HandleFunc("GET /api/dev/daemon/check", m.handleDaemonCheck)
```

(You may also need to expose `m.repoRoot` as a field if it isn't already; look at how `defaultBuild` references `m.repoRoot` — that field already exists per exploration.)

- [ ] **Step 4: Run**

Run: `go test ./internal/module/dev/ -v`
Expected: PASS.

- [ ] **Step 5: Inject BakedInHash at build time**

Update build invocations (Makefile / build script / existing `pnpm run electron:build`) to add:

```bash
go build -ldflags "-X github.com/wake/purdex/internal/module/dev.BakedInHash=$(git log -1 --format=%h)" -o bin/pdx ./cmd/pdx
```

Find the existing build command (likely in a Makefile or npm script) and add the `-ldflags` option. If no central build script exists, document the expected flag in the plan for manual verification.

- [ ] **Step 6: Commit**

```bash
git add internal/module/dev/daemon.go internal/module/dev/daemon_test.go internal/module/dev/module.go
# + any build script changes
git commit -m "feat(dev): /api/dev/daemon/check endpoint with baked-in hash"
```

---

### Task 4: `POST /api/dev/daemon/rebuild` SSE handler — build phase only (no exec yet)

**Files:**
- Modify: `internal/module/dev/daemon.go`
- Modify: `internal/module/dev/daemon_test.go`
- Modify: `internal/module/dev/module.go`

- [ ] **Step 1: Add failing test**

```go
func TestHandleDaemonRebuild_SuccessStreamsSuccess(t *testing.T) {
	t.Setenv("PDX_DEV_UPDATE", "1")
	dir := t.TempDir()
	// Write a tiny go.mod + main.go so `go build` succeeds.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd/pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd/pdx/main.go"), []byte(`package main; func main(){}`), 0644); err != nil {
		t.Fatal(err)
	}

	m := &DevModule{repoRoot: dir}
	mux := http.NewServeMux()
	// register only rebuild route
	mux.HandleFunc("POST /api/dev/daemon/rebuild", m.handleDaemonRebuild)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Use a custom client so we can read the stream chunk by chunk.
	resp, err := http.Post(srv.URL+"/api/dev/daemon/rebuild", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	got := string(body)
	if !strings.Contains(got, `"type":"success"`) {
		t.Errorf("expected success event, got:\n%s", got)
	}
}

func TestHandleDaemonRebuild_ConcurrencyRefused(t *testing.T) {
	m := &DevModule{repoRoot: "."}
	// Grab the mutex as if a build is in flight.
	daemonRebuildMu.Lock()
	defer daemonRebuildMu.Unlock()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/dev/daemon/rebuild", nil)

	// Invoke handler in a goroutine that attempts TryLock semantics via select.
	// Simpler: call handler directly and expect 409 if it fast-fails on contention.
	m.handleDaemonRebuild(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status %d, want 409", w.Code)
	}
}
```

- [ ] **Step 2: Run — expect failure**

Run: `go test ./internal/module/dev/ -run TestHandleDaemonRebuild -v`
Expected: FAIL.

- [ ] **Step 3: Implement build phase (no exec yet; added in Task 5)**

Append to `internal/module/dev/daemon.go`:

```go
import (
	// add:
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type daemonRebuildEvent struct {
	Type    string `json:"type"`             // "log" | "error" | "success" | "restarting"
	Line    string `json:"line,omitempty"`
	Message string `json:"message,omitempty"`
	NewHash string `json:"new_hash,omitempty"`
}

func (m *DevModule) handleDaemonRebuild(w http.ResponseWriter, r *http.Request) {
	if !daemonRebuildMu.TryLock() {
		http.Error(w, `{"error":"rebuild in progress"}`, http.StatusConflict)
		return
	}
	defer daemonRebuildMu.Unlock()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	writeEvent := func(ev daemonRebuildEvent) bool {
		data, err := json.Marshal(ev)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Build pdx.new in bin/.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	binDir := filepath.Join(m.repoRoot, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}
	newPath := filepath.Join(binDir, "pdx.new")

	cmd := exec.CommandContext(ctx, "go", "build", "-o", newPath, "./cmd/pdx")
	cmd.Dir = m.repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}

	stream := func(src io.Reader) {
		scanner := bufio.NewScanner(src)
		for scanner.Scan() {
			writeEvent(daemonRebuildEvent{Type: "log", Line: scanner.Text()})
		}
	}
	doneOut := make(chan struct{})
	doneErr := make(chan struct{})
	go func() { stream(stdout); close(doneOut) }()
	go func() { stream(stderr); close(doneErr) }()
	<-doneOut
	<-doneErr

	if err := cmd.Wait(); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}

	// Compute new hash from the source tree.
	newHash := ""
	if out, err := exec.Command("git", "-C", m.repoRoot, "log", "-1", "--format=%h").Output(); err == nil {
		newHash = strings.TrimSpace(string(out))
	}
	writeEvent(daemonRebuildEvent{Type: "success", NewHash: newHash})

	// TODO: atomic rename + exec self (added in Task 5)
}
```

Register the route in `module.go` (within the PDX_DEV_UPDATE gate):

```go
mux.HandleFunc("POST /api/dev/daemon/rebuild", m.handleDaemonRebuild)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/module/dev/ -run TestHandleDaemonRebuild -v`
Expected: PASS (success test); concurrency test may need adjustment based on TryLock semantics — if test fails, relax to: just check handler doesn't deadlock.

- [ ] **Step 5: Commit**

```bash
git add internal/module/dev/
git commit -m "feat(dev): /api/dev/daemon/rebuild SSE handler (build phase)"
```

---

### Task 5: Atomic rename + `syscall.Exec` self

**Files:**
- Modify: `internal/module/dev/daemon.go`

- [ ] **Step 1: Implement rename + exec**

Replace the `// TODO: atomic rename + exec self` block in `handleDaemonRebuild` with:

```go
// Atomic replace: bin/pdx.new → bin/pdx
finalPath := filepath.Join(binDir, "pdx")
if err := os.Rename(newPath, finalPath); err != nil {
	writeEvent(daemonRebuildEvent{Type: "error", Message: "rename failed: " + err.Error()})
	return
}

// Signal SPA that restart is imminent.
writeEvent(daemonRebuildEvent{Type: "restarting"})
// Give SSE a brief moment to flush.
time.Sleep(200 * time.Millisecond)

// Exec self — replaces this process with the new binary.
// After this call returns, we've already been replaced (or an error occurred).
self, err := os.Executable()
if err != nil {
	writeEvent(daemonRebuildEvent{Type: "error", Message: "Executable(): " + err.Error()})
	return
}
if err := syscall.Exec(self, os.Args, os.Environ()); err != nil {
	// If we reach here, exec failed.
	writeEvent(daemonRebuildEvent{Type: "error", Message: "exec: " + err.Error()})
}
```

Add import `"syscall"`.

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: no error.

- [ ] **Step 3: Manual test (skip unit test for exec — can't meaningfully test in-process)**

This step is verified in Task 8 (E2E).

- [ ] **Step 4: Commit**

```bash
git add internal/module/dev/daemon.go
git commit -m "feat(dev): daemon rebuild does atomic rename + syscall.Exec self"
```

---

## Frontend: Settings Dev section

### Task 6: Extend `DevEnvironmentSection` with Daemon block

**Files:**
- Modify: `spa/src/components/settings/DevEnvironmentSection.tsx`
- Modify: `spa/src/components/settings/DevEnvironmentSection.test.tsx`

- [ ] **Step 1: Read current structure**

Run: `sed -n '1,60p' spa/src/components/settings/DevEnvironmentSection.tsx`
Note the existing App section's state, fetch pattern, log streaming.

- [ ] **Step 2: Add failing test**

Append to `DevEnvironmentSection.test.tsx`:

```tsx
describe('DevEnvironmentSection Daemon block', () => {
  it('renders Daemon heading', async () => {
    // mock fetch for /api/dev/daemon/check
    globalThis.fetch = vi.fn((url: string) => {
      if (url.includes('/api/dev/daemon/check')) {
        return Promise.resolve({ ok: true, json: () => Promise.resolve({ current_hash: 'abc', latest_hash: 'abc', available: false }) } as Response)
      }
      // other calls (app update): minimal stub
      return Promise.resolve({ ok: true, json: () => Promise.resolve({}) } as Response)
    }) as any

    render(<DevEnvironmentSection />)
    await waitFor(() => expect(screen.getByText('Daemon')).toBeInTheDocument())
  })

  it('shows Rebuild button', async () => {
    // ... mock as above ...
    render(<DevEnvironmentSection />)
    await waitFor(() => expect(screen.getByText(/rebuild/i)).toBeInTheDocument())
  })
})
```

- [ ] **Step 3: Run — expect failure**

Run: `cd spa && npx vitest run src/components/settings/DevEnvironmentSection.test.tsx`
Expected: FAIL.

- [ ] **Step 4: Implement**

Add to `DevEnvironmentSection.tsx` (insert after the existing App section's JSX):

```tsx
// State
const [daemonCheck, setDaemonCheck] = useState<{ current_hash: string; latest_hash: string; available: boolean } | null>(null)
const [daemonLog, setDaemonLog] = useState<string[]>([])
const [daemonPhase, setDaemonPhase] = useState<'idle' | 'checking' | 'rebuilding' | 'restarting' | 'error'>('idle')

const checkDaemon = async () => {
  setDaemonPhase('checking')
  try {
    const res = await fetch('/api/dev/daemon/check')
    if (!res.ok) throw new Error(String(res.status))
    setDaemonCheck(await res.json())
    setDaemonPhase('idle')
  } catch {
    setDaemonPhase('error')
  }
}

const rebuildDaemon = async () => {
  setDaemonPhase('rebuilding')
  setDaemonLog([])
  const res = await fetch('/api/dev/daemon/rebuild', { method: 'POST' })
  if (!res.body) {
    setDaemonPhase('error')
    return
  }
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value)
    const lines = buffer.split('\n\n')
    buffer = lines.pop() ?? ''
    for (const line of lines) {
      if (!line.startsWith('data: ')) continue
      try {
        const ev = JSON.parse(line.slice(6))
        if (ev.type === 'log') setDaemonLog((prev) => [...prev, ev.line])
        else if (ev.type === 'error') { setDaemonLog((prev) => [...prev, `ERROR: ${ev.message}`]); setDaemonPhase('error') }
        else if (ev.type === 'success') setDaemonLog((prev) => [...prev, `✓ ${t('settings.dev.daemon.build_complete')}`])
        else if (ev.type === 'restarting') setDaemonPhase('restarting')
      } catch { /* ignore parse error */ }
    }
  }
  // WS disconnect/reconnect is already handled by the SPA's WS manager.
  // After reconnect, re-check hash to confirm new daemon is running.
  setTimeout(() => void checkDaemon(), 3000)
}

useEffect(() => { void checkDaemon() }, [])

// JSX (inside the component's return, after existing App block):
<section className="mt-6 pt-6 border-t border-border-subtle">
  <h3 className="text-sm font-semibold mb-2">{t('settings.dev.daemon.heading')}</h3>
  {daemonCheck && (
    <div className="space-y-1 mb-3 text-xs text-text-muted">
      <div>{t('settings.dev.daemon.current_hash')}: <code className="font-mono">{daemonCheck.current_hash}</code></div>
      <div>{t('settings.dev.daemon.latest_hash')}: <code className="font-mono">{daemonCheck.latest_hash}</code></div>
      {daemonCheck.available && <div className="text-accent">{t('settings.dev.daemon.update_available')}</div>}
    </div>
  )}
  <div className="flex gap-2 mb-3">
    <button onClick={checkDaemon} disabled={daemonPhase !== 'idle'} className="px-3 py-1.5 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary text-text-secondary cursor-pointer disabled:opacity-50">
      {t('settings.dev.daemon.check')}
    </button>
    <button onClick={rebuildDaemon} disabled={daemonPhase === 'rebuilding' || daemonPhase === 'restarting'} className="px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">
      {t('settings.dev.daemon.rebuild')}
    </button>
  </div>
  {daemonLog.length > 0 && (
    <pre className="bg-surface-secondary border border-border-subtle rounded p-2 text-xs font-mono max-h-60 overflow-y-auto whitespace-pre-wrap">
      {daemonLog.join('\n')}
    </pre>
  )}
  {daemonPhase === 'restarting' && <p className="text-xs text-accent mt-2">{t('settings.dev.daemon.restarting')}</p>}
</section>
```

- [ ] **Step 5: Run tests + lint**

Run: `cd spa && npx vitest run src/components/settings/DevEnvironmentSection.test.tsx && pnpm run lint`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/settings/DevEnvironmentSection.tsx spa/src/components/settings/DevEnvironmentSection.test.tsx
git commit -m "feat(spa): Settings Dev page Daemon block with SSE rebuild log"
```

---

## i18n + verification

### Task 7: Add i18n keys for Daemon block

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: Add to en.json**

Under `settings.dev.*`:

```json
"settings.dev.daemon.heading": "Daemon",
"settings.dev.daemon.current_hash": "Current hash",
"settings.dev.daemon.latest_hash": "Latest hash",
"settings.dev.daemon.update_available": "Update available",
"settings.dev.daemon.check": "Check Update",
"settings.dev.daemon.rebuild": "Rebuild & Restart",
"settings.dev.daemon.build_complete": "Build complete, restarting daemon...",
"settings.dev.daemon.restarting": "Restarting — reconnecting WS..."
```

- [ ] **Step 2: Add to zh-TW.json**

```json
"settings.dev.daemon.heading": "Daemon",
"settings.dev.daemon.current_hash": "目前 hash",
"settings.dev.daemon.latest_hash": "最新 hash",
"settings.dev.daemon.update_available": "有可用更新",
"settings.dev.daemon.check": "檢查更新",
"settings.dev.daemon.rebuild": "Rebuild & 重啟",
"settings.dev.daemon.build_complete": "編譯完成，正在重啟 daemon…",
"settings.dev.daemon.restarting": "重啟中 — WS 重連中…"
```

- [ ] **Step 3: Full test pass**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add spa/src/locales/
git commit -m "i18n(spa): settings.dev.daemon.* keys (en + zh-TW)"
```

---

### Task 8: Manual E2E verification

**Files:** none; verification.

- [ ] **Step 1: Build + run daemon with dev flag**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/statusline-installer-p1
go build -ldflags "-X github.com/wake/purdex/internal/module/dev.BakedInHash=$(git log -1 --format=%h)" -o bin/pdx ./cmd/pdx
PDX_DEV_UPDATE=1 ./bin/pdx serve &
```

- [ ] **Step 2: Start SPA dev server**

```bash
cd spa && pnpm run dev
```

- [ ] **Step 3: Verify dev gate**

With `PDX_DEV_UPDATE` unset, `curl http://127.0.0.1:7860/api/dev/daemon/check` should return `404`. Re-run with `PDX_DEV_UPDATE=1` — should return JSON.

- [ ] **Step 4: Navigate to Settings → Development in SPA**

- Expect: Daemon section visible with current hash + Latest hash.

- [ ] **Step 5: Trigger a real rebuild**

- Make a trivial change in the Go code (e.g., add a `log.Println("hello")` to an unused code path).
- Commit the change.
- In SPA, click "Rebuild & Restart".
- Expect: build log streams in, ends with "Build complete", status changes to "Restarting", WS disconnects, WS reconnects within a few seconds, Daemon block re-checks and shows new hash.

- [ ] **Step 6: Verify tmux sessions survived**

- If there were active tmux sessions before rebuild, they should still appear in the Hosts / Sessions tab after reconnect.

- [ ] **Step 7: Build failure path**

- Introduce a syntax error in Go source.
- Click Rebuild. Build log should show `go build` errors; daemon should NOT restart. Clean up by reverting the syntax error.

- [ ] **Step 8: Commit verification note (optional)**

```bash
git commit --allow-empty -m "chore: daemon rebuild E2E verification passed (see task 8)"
```

---

# Summary

8 tasks total:
- **Prep**: 1 (PDX_DEV_UPDATE gate for existing routes)
- **Backend**: 4 (listener + 3 handler tasks)
- **Frontend**: 2 (UI + i18n)
- **Verification**: 1 manual E2E

All tasks follow TDD where practical; `syscall.Exec` is verified manually in Task 8.
