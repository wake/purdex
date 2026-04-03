# Phase 3: 連線偵測 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立完整的連線偵測機制，含 daemon 端 watcher 狀態機 + WS ping/pong + SPA 端 useHostConnection 閘控，取代現有各 WS 各自獨立重連的做法。

**Architecture:** 分兩個 PR。PR 1 (Daemon) 加入 watcher NORMAL/TMUX_DOWN 狀態機、`/api/health` tmux 欄位、host-events WS ping/pong、rename session-events → host-events。PR 2 (SPA) 加入 useHostConnection hook（L1/L2/L3 分類 + 重連狀態機）、WS 閘控、rename 對應。

**Tech Stack:** Go (gorilla/websocket) / React 19 / Zustand 5 / Vitest

**Spec:** `docs/superpowers/specs/2026-04-03-phase3-connection-detection-design.md`

---

## File Structure

### PR 1 — Daemon

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `internal/tmux/executor.go:20-39` | Add `TmuxAlive() bool` to Executor interface |
| Modify | `internal/tmux/executor.go` (new method) | RealExecutor.TmuxAlive implementation |
| Modify | `internal/tmux/fake_executor.go` | FakeExecutor.TmuxAlive + SetAlive |
| Modify | `internal/tmux/executor_test.go` | TmuxAlive tests |
| Modify | `internal/module/session/watcher.go` | NORMAL/TMUX_DOWN state machine + cached tmux status |
| Modify | `internal/module/session/module.go:50-86` | Initial TmuxAlive on Start, expose cached status |
| Create | `internal/module/session/watcher_test.go` | Watcher state machine tests |
| Modify | `internal/core/info_handler.go:21-24` | HandleHealth returns tmux field |
| Modify | `internal/core/info_handler_test.go` | Health endpoint test with tmux field |
| Modify | `internal/core/events.go:13-17,158-183` | Rename SessionEvent→HostEvent, add ping/pong |
| Modify | `internal/core/events_test.go` | Update struct name in tests |
| Modify | `internal/core/core.go:129-135` | Rename /ws/session-events → /ws/host-events |

### PR 2 — SPA

| Action | File | Responsibility |
|--------|------|---------------|
| Rename | `spa/src/lib/session-events.ts` → `spa/src/lib/host-events.ts` | Rename + update types |
| Modify | `spa/src/hooks/useMultiHostEventWs.ts` | Import rename + tmux event handler |
| Modify | `spa/src/stores/useHostStore.ts:17-21` | HostRuntime add daemonState + tmuxState |
| Modify | `spa/src/stores/useHostStore.test.ts` | Test new HostRuntime fields |
| Create | `spa/src/lib/host-connection.ts` | checkHealth with AbortController + classifyResult |
| Create | `spa/src/lib/host-connection.test.ts` | Health check classification tests |
| Create | `spa/src/lib/connection-state-machine.ts` | ConnectionStateMachine pure class (no React) |
| Create | `spa/src/lib/connection-state-machine.test.ts` | State machine tests |
| Modify | `spa/src/lib/host-events.ts` | Remove self-reconnect, add gateOpen param |
| Modify | `spa/src/lib/ws.ts` | Add gateOpen check to connectTerminal |
| Modify | `spa/src/lib/ws.test.ts` | Gate test for connectTerminal |

---

## PR 1: Daemon 端

### Task 1: TmuxAlive — Executor interface 擴充

**Files:**
- Modify: `internal/tmux/executor.go:20-39` (interface), append new method
- Modify: `internal/tmux/executor.go` (append RealExecutor method)
- Modify: `internal/tmux/fake_executor.go` (struct + method)
- Modify: `internal/tmux/executor_test.go` (append test)

- [ ] **Step 1: Write failing test for FakeExecutor.TmuxAlive**

In `internal/tmux/executor_test.go`, append:

```go
func TestTmuxAliveDefault(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	// Default should be true (tmux assumed alive)
	if !fake.TmuxAlive() {
		t.Error("TmuxAlive() default = false, want true")
	}
}

func TestTmuxAliveSetFalse(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetAlive(false)
	if fake.TmuxAlive() {
		t.Error("TmuxAlive() = true after SetAlive(false), want false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/tmux/ -run TestTmuxAlive -v`
Expected: compile error — `TmuxAlive` not defined

- [ ] **Step 3: Add TmuxAlive to Executor interface**

In `internal/tmux/executor.go`, add to the Executor interface (after `ShowHooksGlobal`):

```go
	TmuxAlive() bool
```

- [ ] **Step 4: Implement RealExecutor.TmuxAlive**

In `internal/tmux/executor.go`, append after `ShowHooksGlobal` method:

```go
func (r *RealExecutor) TmuxAlive() bool {
	return exec.Command("tmux", "info").Run() == nil
}
```

- [ ] **Step 5: Implement FakeExecutor.TmuxAlive + SetAlive**

In `internal/tmux/fake_executor.go`, add `alive` field to the struct (default true in constructor):

```go
// In FakeExecutor struct, add field:
alive bool

// In NewFakeExecutor, set:
alive: true,
```

Append methods:

```go
func (f *FakeExecutor) SetAlive(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive = v
}

func (f *FakeExecutor) TmuxAlive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/tmux/ -run TestTmuxAlive -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/tmux/executor.go internal/tmux/fake_executor.go internal/tmux/executor_test.go
git commit -m "feat(daemon): add TmuxAlive to Executor interface"
```

---

### Task 2: Watcher 狀態機

**Files:**
- Modify: `internal/module/session/watcher.go` (rewrite watchSessions)
- Modify: `internal/module/session/module.go` (expose tmux cached status, initial TmuxAlive)
- Create: `internal/module/session/watcher_test.go`

- [ ] **Step 1: Write failing tests for watcher state machine**

Create `internal/module/session/watcher_test.go`:

```go
package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

func newWatcherTestModule(t *testing.T) (*SessionModule, *tmux.FakeExecutor, *core.EventsBroadcaster) {
	t.Helper()
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { meta.Close() })

	fake := tmux.NewFakeExecutor()
	mod := NewSessionModule(meta)
	c := core.New(core.CoreDeps{
		Tmux:     fake,
		Registry: core.NewServiceRegistry(),
	})
	require.NoError(t, mod.Init(c))
	return mod, fake, c.Events
}

func TestWatcherTmuxAliveInitialState(t *testing.T) {
	mod, _, _ := newWatcherTestModule(t)
	// Before Start, tmuxAlive should be unset; after Start it should be initialized
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))
	assert.True(t, mod.TmuxAlive(), "tmux should be alive when FakeExecutor default alive=true")
}

func TestWatcherTransitionsToTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	// Kill tmux
	fake.SetAlive(false)

	// Trigger a tick by waiting for watcher to detect the change
	// (In tests, we'll call the internal method directly)
	mod.checkAndBroadcast()

	assert.False(t, mod.TmuxAlive())

	// Should have broadcast a tmux unavailable event
	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"tmux"`)
		assert.Contains(t, string(msg), `"value":"unavailable"`)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tmux unavailable broadcast")
	}
}

func TestWatcherRecoverFromTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	// Go down
	fake.SetAlive(false)
	mod.checkAndBroadcast()
	assert.False(t, mod.TmuxAlive())
	// Drain the unavailable event
	<-sub.SendCh()

	// Recover
	fake.SetAlive(true)
	fake.AddSession("recovered", "/tmp")
	mod.checkAndBroadcast()
	assert.True(t, mod.TmuxAlive())

	// Should have broadcast tmux ok event
	select {
	case msg := <-sub.SendCh():
		assert.Contains(t, string(msg), `"type":"tmux"`)
		assert.Contains(t, string(msg), `"value":"ok"`)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tmux ok broadcast")
	}
}

func TestWatcherNilSessionsWithTmuxAlive(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// tmux alive but 0 sessions — ListSessions returns nil, nil
	fake.SetAlive(true)
	// Don't add any sessions — FakeExecutor.ListSessions returns empty slice
	require.NoError(t, mod.Start(ctx))

	mod.checkAndBroadcast()
	// Should remain in NORMAL (not switch to TMUX_DOWN)
	assert.True(t, mod.TmuxAlive())
}

func TestWatcherNoRepeatBroadcastInTmuxDown(t *testing.T) {
	mod, fake, events := newWatcherTestModule(t)
	sub := events.AddTestSubscriber()
	defer events.RemoveTestSubscriber(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, mod.Start(ctx))

	fake.SetAlive(false)
	mod.checkAndBroadcast() // First: should broadcast unavailable
	<-sub.SendCh()

	mod.checkAndBroadcast() // Second: should NOT broadcast again

	select {
	case <-sub.SendCh():
		t.Fatal("should not broadcast tmux unavailable twice in a row")
	case <-time.After(50 * time.Millisecond):
		// OK — no duplicate broadcast
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/session/ -run TestWatcher -v`
Expected: compile error — `TmuxAlive()` and `checkAndBroadcast()` not defined on SessionModule

- [ ] **Step 3: Implement watcher state machine**

Rewrite `internal/module/session/watcher.go`:

```go
package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"
)

// watcherState tracks the NORMAL / TMUX_DOWN state machine.
type watcherState struct {
	mu        sync.RWMutex
	tmuxAlive bool // cached tmux status, read by /api/health
	lastHash  string
}

func (ws *watcherState) getTmuxAlive() bool {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.tmuxAlive
}

func (ws *watcherState) setTmuxAlive(v bool) (changed bool) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	changed = ws.tmuxAlive != v
	ws.tmuxAlive = v
	return
}

// TmuxAlive returns the cached tmux status (thread-safe).
func (m *SessionModule) TmuxAlive() bool {
	return m.wstate.getTmuxAlive()
}

// checkAndBroadcast performs one tick of the watcher state machine.
// Called by the 5s ticker goroutine and exposed for tests.
func (m *SessionModule) checkAndBroadcast() {
	if m.wstate.getTmuxAlive() {
		m.tickNormal()
	} else {
		m.tickTmuxDown()
	}
}

// tickNormal handles one tick in NORMAL state.
func (m *SessionModule) tickNormal() {
	sessions, err := m.ListSessions()
	if err != nil {
		// ListSessions returned an error (not nil,nil) — unexpected
		log.Printf("session: watcher list error: %v", err)
		return
	}

	if len(sessions) == 0 {
		// Empty — could be "no server" or "no sessions" (service layer never returns nil)
		if !m.tmux.TmuxAlive() {
			// tmux is down → transition to TMUX_DOWN
			if m.wstate.setTmuxAlive(false) {
				m.broadcastTmuxStatus("unavailable")
			}
			m.notifyWaitFor(false) // pause wait-for goroutine
			return
		}
		// tmux alive but 0 sessions → continue with empty slice
	}

	hash := hashSessions(sessions)
	m.wstate.mu.Lock()
	changed := hash != m.wstate.lastHash
	m.wstate.lastHash = hash
	m.wstate.mu.Unlock()

	if changed && m.core.Events.HasSubscribers() {
		data := mustMarshal(sessions)
		m.core.Events.Broadcast("", "sessions", data)
	}
}

// tickTmuxDown handles one tick in TMUX_DOWN state.
func (m *SessionModule) tickTmuxDown() {
	if m.tmux.TmuxAlive() {
		// tmux recovered → transition to NORMAL
		m.wstate.setTmuxAlive(true)
		m.broadcastTmuxStatus("ok")
		m.notifyWaitFor(true) // resume wait-for goroutine
		// Do an immediate session broadcast
		m.broadcastSessions()
	}
	// Still down — do nothing (no repeat broadcast)
}

// broadcastTmuxStatus sends a tmux status event to all subscribers.
func (m *SessionModule) broadcastTmuxStatus(value string) {
	if m.core.Events.HasSubscribers() {
		m.core.Events.Broadcast("", "tmux", value)
	}
}

// broadcastSessions fetches sessions and broadcasts to all WS subscribers.
func (m *SessionModule) broadcastSessions() {
	if !m.core.Events.HasSubscribers() {
		return
	}
	sessions, err := m.ListSessions()
	if err != nil {
		log.Printf("session: broadcast list error: %v", err)
		return
	}
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	data := mustMarshal(sessions)
	m.core.Events.Broadcast("", "sessions", data)
}

// watchSessions starts two goroutines:
//   - Goroutine A: listens for tmux wait-for signals (instant push), paused in TMUX_DOWN
//   - Goroutine B: polling fallback with 5s ticker (state machine)
func (m *SessionModule) watchSessions(ctx context.Context) {
	// Channel to pause/resume wait-for goroutine
	m.waitForGate = make(chan bool, 1)

	// Goroutine A: tmux wait-for loop (pauses in TMUX_DOWN)
	go func() {
		active := m.wstate.getTmuxAlive()
		for {
			if !active {
				// Wait for resume signal
				select {
				case <-ctx.Done():
					return
				case active = <-m.waitForGate:
					continue
				}
			}

			cmd := exec.CommandContext(ctx, "tmux", "wait-for", waitForChannel)
			err := cmd.Run()

			if ctx.Err() != nil {
				return
			}

			if err != nil {
				// Check if we should pause
				select {
				case v := <-m.waitForGate:
					active = v
					continue
				default:
				}
				log.Printf("session: wait-for error: %v, retrying in 1s", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
				case v := <-m.waitForGate:
					active = v
				}
				continue
			}

			// Signal received — broadcast updated session list.
			m.broadcastSessions()
		}
	}()

	// Goroutine B: polling fallback — drives the state machine
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.checkAndBroadcast()
			}
		}
	}()
}

// notifyWaitFor sends a pause/resume signal to Goroutine A.
func (m *SessionModule) notifyWaitFor(active bool) {
	select {
	case m.waitForGate <- active:
	default:
	}
}

// hashSessions returns a short hex hash of the sessions list.
func hashSessions(sessions []SessionInfo) string {
	data, _ := json.Marshal(sessions)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

// mustMarshal marshals v to JSON string, returning "{}" on error.
func mustMarshal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
```

- [ ] **Step 4: Update SessionModule struct to include watcher state**

In `internal/module/session/module.go`, update the struct and Start method:

```go
// Add to SessionModule struct:
wstate       watcherState
waitForGate  chan bool

// In Start method, before watchSessions:
// Initialize cached tmux status
m.wstate.setTmuxAlive(m.tmux.TmuxAlive())
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/session/ -run TestWatcher -v`
Expected: PASS

- [ ] **Step 6: Run all session module tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/session/ -v`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/module/session/watcher.go internal/module/session/watcher_test.go internal/module/session/module.go
git commit -m "feat(daemon): watcher NORMAL/TMUX_DOWN state machine"
```

---

### Task 3: `/api/health` 擴充

**Files:**
- Modify: `internal/core/info_handler.go:21-24`
- Modify: `internal/core/info_handler_test.go`
- Modify: `internal/core/core.go` (add TmuxAliveFunc field)

- [ ] **Step 1: Write failing test**

In `internal/core/info_handler_test.go`, **delete** the existing `TestHealthEndpoint` and replace with three new tests:

```go
func TestHealthEndpointWithTmuxTrue(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	c.TmuxAliveFunc = func() bool { return true }

	req := httptest.NewRequest("GET", "/api/health", nil)
	rec := httptest.NewRecorder()
	c.HandleHealth(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, true, body["ok"])
	assert.Equal(t, true, body["tmux"])
}

func TestHealthEndpointWithTmuxFalse(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	c.TmuxAliveFunc = func() bool { return false }

	req := httptest.NewRequest("GET", "/api/health", nil)
	rec := httptest.NewRecorder()
	c.HandleHealth(rec, req)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, true, body["ok"])
	assert.Equal(t, false, body["tmux"])
}

func TestHealthEndpointWithoutTmuxFunc(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	// TmuxAliveFunc not set — should default to false

	req := httptest.NewRequest("GET", "/api/health", nil)
	rec := httptest.NewRecorder()
	c.HandleHealth(rec, req)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, true, body["ok"])
	assert.Equal(t, false, body["tmux"])
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestHealthEndpoint -v`
Expected: compile error — `TmuxAliveFunc` not defined

- [ ] **Step 3: Add TmuxAliveFunc to Core**

In `internal/core/core.go`, add field to Core struct:

```go
TmuxAliveFunc func() bool // set by session module after Start
```

- [ ] **Step 4: Update HandleHealth**

In `internal/core/info_handler.go`, replace HandleHealth:

```go
func (c *Core) HandleHealth(w http.ResponseWriter, r *http.Request) {
	tmuxAlive := false
	if c.TmuxAliveFunc != nil {
		tmuxAlive = c.TmuxAliveFunc()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "tmux": tmuxAlive})
}
```

- [ ] **Step 5: Wire TmuxAliveFunc in SessionModule.Start**

In `internal/module/session/module.go`, in Start method after `m.wstate.setTmuxAlive(...)`:

```go
// Expose cached tmux status to Core for /api/health
m.core.TmuxAliveFunc = m.TmuxAlive
```

- [ ] **Step 6: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestHealthEndpoint -v`
Expected: PASS

- [ ] **Step 7: Run all core tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -v`
Expected: all PASS

- [ ] **Step 8: Commit**

```bash
git add internal/core/core.go internal/core/info_handler.go internal/core/info_handler_test.go internal/module/session/module.go
git commit -m "feat(daemon): /api/health returns tmux status from watcher cache"
```

---

### Task 4: WS ping/pong

**Files:**
- Modify: `internal/core/events.go:158-183` (HandleSessionEvents → add ping goroutine)
- Modify: `internal/core/events_test.go` (add ping/pong test)

- [ ] **Step 1: Write failing test**

In `internal/core/events_test.go`, append:

```go
func TestPingPongClosesOnTimeout(t *testing.T) {
	eb := NewEventsBroadcaster()
	srv := httptest.NewServer(http.HandlerFunc(eb.HandleSessionEvents))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)

	// Set a very short pong deadline to trigger close quickly
	// The server sends pings; if we don't respond with pong, server closes us.
	// For testing: disable the default pong handler so pongs aren't sent.
	conn.SetPongHandler(func(string) error { return nil }) // keep default
	// Actually, for this test we want to verify pings are being sent.
	// Read a ping frame from the server.
	pingReceived := false
	conn.SetPingHandler(func(msg string) error {
		pingReceived = true
		// Respond with pong (normal behavior)
		return conn.WriteControl(websocket.PongMessage, []byte(msg), time.Now().Add(time.Second))
	})

	// Read loop to trigger ping handler
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	// Wait up to 35s for a ping (interval is 30s)
	time.Sleep(35 * time.Second)
	assert.True(t, pingReceived, "server should have sent a ping within 35s")

	conn.Close()
	<-done
}
```

Note: This test is slow (35s). For a faster unit test, we'll parametrize the ping interval. Let me revise — extract ping interval as a configurable field:

```go
func TestPingIsSent(t *testing.T) {
	eb := NewEventsBroadcaster()
	eb.PingInterval = 100 * time.Millisecond // fast for testing
	eb.PongTimeout = 50 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(eb.HandleSessionEvents))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	pingReceived := make(chan struct{}, 1)
	conn.SetPingHandler(func(msg string) error {
		select {
		case pingReceived <- struct{}{}:
		default:
		}
		return conn.WriteControl(websocket.PongMessage, []byte(msg), time.Now().Add(time.Second))
	})

	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	select {
	case <-pingReceived:
		// OK
	case <-time.After(time.Second):
		t.Fatal("expected ping within 1s")
	}

	conn.Close()
}

func TestPongTimeoutClosesConnection(t *testing.T) {
	eb := NewEventsBroadcaster()
	eb.PingInterval = 100 * time.Millisecond
	eb.PongTimeout = 50 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(eb.HandleSessionEvents))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	// Don't respond to pings — server should close us
	conn.SetPingHandler(func(string) error {
		return nil // swallow ping, don't send pong
	})

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	select {
	case <-closed:
		// OK — connection was closed by server
	case <-time.After(time.Second):
		t.Fatal("expected server to close connection after pong timeout")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestPing -v`
Expected: compile error — `PingInterval` field not defined

- [ ] **Step 3: Add PingInterval/PongTimeout fields to EventsBroadcaster**

In `internal/core/events.go`, add fields:

```go
type EventsBroadcaster struct {
	mu           sync.RWMutex
	subscribers  map[*EventSubscriber]struct{}
	onSubscribe  []func(*EventSubscriber)
	PingInterval time.Duration // default 30s
	PongTimeout  time.Duration // default 10s
}

func NewEventsBroadcaster() *EventsBroadcaster {
	return &EventsBroadcaster{
		subscribers:  make(map[*EventSubscriber]struct{}),
		PingInterval: 30 * time.Second,
		PongTimeout:  10 * time.Second,
	}
}
```

- [ ] **Step 4: Integrate ping into write pump (one-writer rule)**

**Critical:** gorilla/websocket requires at most one concurrent writer. The existing write pump goroutine (in `Add`) is the sole writer. Pings MUST go through the same goroutine — a separate ping goroutine would cause a data race.

In `internal/core/events.go`, update the `Add` method to include a ping ticker in the write pump:

```go
func (eb *EventsBroadcaster) Add(conn *websocket.Conn) *EventSubscriber {
	sub := &EventSubscriber{
		conn: conn,
		send: make(chan []byte, 64),
	}
	eb.mu.Lock()
	eb.subscribers[sub] = struct{}{}
	eb.mu.Unlock()

	// Write pump — the ONLY goroutine that calls WriteMessage on this conn.
	// Handles both data messages and periodic pings.
	go func() {
		ticker := time.NewTicker(eb.PingInterval)
		defer ticker.Stop()
		for {
			select {
			case msg, ok := <-sub.send:
				if !ok {
					return // channel closed by Remove
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					eb.Remove(sub)
					return
				}
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					eb.Remove(sub)
					return
				}
				// Pong timeout is handled by read-side deadline in HandleHostEvents
			}
		}
	}()

	return sub
}
```

- [ ] **Step 5: Add pong handler + read deadline in HandleSessionEvents**

In `internal/core/events.go`, update `HandleSessionEvents` to set up pong handling (read-side only — no writes here):

```go
func (eb *EventsBroadcaster) HandleSessionEvents(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	// Pong handling — reset read deadline on each pong received
	conn.SetReadDeadline(time.Now().Add(eb.PingInterval + eb.PongTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(eb.PingInterval + eb.PongTimeout))
		return nil
	})

	sub := eb.Add(conn)
	defer eb.Remove(sub)

	// Call all registered OnSubscribe callbacks.
	eb.mu.RLock()
	callbacks := make([]func(*EventSubscriber), len(eb.onSubscribe))
	copy(callbacks, eb.onSubscribe)
	eb.mu.RUnlock()
	for _, fn := range callbacks {
		fn(sub)
	}

	// Read loop — exits on disconnect or pong timeout (ReadDeadline exceeded)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
```

- [ ] **Step 6: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestPing -v -timeout 10s`
Expected: PASS

- [ ] **Step 7: Run all core tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -v`
Expected: all PASS

- [ ] **Step 8: Commit**

```bash
git add internal/core/events.go internal/core/events_test.go
git commit -m "feat(daemon): WS ping/pong on host-events endpoint"
```

---

### Task 5: Rename session-events → host-events

**Files:**
- Modify: `internal/core/events.go:13-17` (SessionEvent → HostEvent, HandleSessionEvents → HandleHostEvents)
- Modify: `internal/core/core.go:130` (route path)
- Modify: `internal/core/events_test.go` (struct refs, `dialWS` helper URL, `HandleSessionEvents` refs)
- Modify: `internal/module/session/module.go:75-78` (OnSubscribe callback)
- Modify: `internal/module/stream/module.go:61` (relay snapshot)
- Modify: `internal/module/agent/module.go:109` (hook snapshot)
- Modify: `internal/module/stream/handler_test.go:221,235,379` (test assertions)
- Modify: `internal/module/stream/orchestrator_test.go:181-204` (test helper + assertions)

- [ ] **Step 1: Rename SessionEvent to HostEvent in events.go**

In `internal/core/events.go`:

```go
// Replace:
type SessionEvent struct {
// With:
type HostEvent struct {
```

Update all references in the same file (Broadcast method).

- [ ] **Step 2: Rename HandleSessionEvents to HandleHostEvents**

In `internal/core/events.go`, rename the method:

```go
// Replace:
func (eb *EventsBroadcaster) HandleSessionEvents(
// With:
func (eb *EventsBroadcaster) HandleHostEvents(
```

- [ ] **Step 3: Update route path in core.go**

In `internal/core/core.go:130`:

```go
// Replace:
mux.HandleFunc("/ws/session-events", c.Events.HandleSessionEvents)
// With:
mux.HandleFunc("/ws/host-events", c.Events.HandleHostEvents)
```

- [ ] **Step 4: Update events_test.go — all references**

In `internal/core/events_test.go`, update:
- `dialWS` helper (line 20): `/ws/session-events` → `/ws/host-events`
- All `HandleSessionEvents` → `HandleHostEvents` (lines 29,58,85,106,139,184)
- All `SessionEvent` → `HostEvent` (lines 48,134,149,171,179,197)
- `TestRegisterCoreRoutes` (line 245): `/ws/session-events` → `/ws/host-events`

- [ ] **Step 5: Update session module OnSubscribe**

In `internal/module/session/module.go:75-78`:

```go
// Replace:
data, err := json.Marshal(core.SessionEvent{
// With:
data, err := json.Marshal(core.HostEvent{
```

- [ ] **Step 6: Update stream and agent modules**

In `internal/module/stream/module.go:61`:
```go
// Replace: core.SessionEvent{Type: "relay", ...}
// With:    core.HostEvent{Type: "relay", ...}
```

In `internal/module/agent/module.go:109`:
```go
// Replace: core.SessionEvent{Type: "hook", ...}
// With:    core.HostEvent{Type: "hook", ...}
```

In `internal/module/stream/handler_test.go` (lines 221, 235, 379):
```go
// Replace: var evt core.SessionEvent
// With:    var evt core.HostEvent
```

In `internal/module/stream/orchestrator_test.go` (lines 181-204):
```go
// Replace all: core.SessionEvent
// With:        core.HostEvent
```

- [ ] **Step 7: Run all tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/... -v`
Expected: all PASS

- [ ] **Step 8: Commit**

```bash
git add internal/core/events.go internal/core/events_test.go internal/core/core.go \
  internal/module/session/module.go internal/module/stream/module.go \
  internal/module/agent/module.go internal/module/stream/handler_test.go \
  internal/module/stream/orchestrator_test.go
git commit -m "refactor(daemon): rename session-events to host-events"
```

---

## PR 2: SPA 端

### Task 6: Rename SPA — session-events → host-events

**Files:**
- Rename: `spa/src/lib/session-events.ts` → `spa/src/lib/host-events.ts`
- Modify: `spa/src/hooks/useMultiHostEventWs.ts` (imports + type references)

- [ ] **Step 1: Rename file and update types**

Rename `spa/src/lib/session-events.ts` to `spa/src/lib/host-events.ts` and update content:

```typescript
// spa/src/lib/host-events.ts

export interface HostEvent {
  type: 'handoff' | 'relay' | 'hook' | 'sessions' | 'tmux'
  session: string
  value: string
}

export interface EventConnection {
  close: () => void
}

export function connectHostEvents(
  url: string,
  onEvent: (event: HostEvent) => void,
  onClose?: () => void,
  onOpen?: () => void,
  getTicket?: () => Promise<string>,
): EventConnection {
  let ws: WebSocket
  let retryMs = 1000
  let closed = false

  async function connect() {
    let wsUrl = url
    if (getTicket) {
      try {
        const ticket = await getTicket()
        const u = new URL(wsUrl)
        u.searchParams.set('ticket', ticket)
        wsUrl = u.toString()
      } catch {
        if (!closed) setTimeout(connect, retryMs)
        retryMs = Math.min(retryMs * 2, 30000)
        return
      }
    }
    ws = new WebSocket(wsUrl)
    ws.onopen = () => { retryMs = 1000; onOpen?.() }
    ws.onmessage = (e) => {
      try {
        const event = JSON.parse(e.data) as HostEvent
        onEvent(event)
      } catch { /* ignore parse errors */ }
    }
    ws.onerror = () => { /* handled by onclose */ }
    ws.onclose = () => {
      if (closed) return
      onClose?.()
      setTimeout(() => {
        if (!closed) connect()
      }, retryMs)
      retryMs = Math.min(retryMs * 2, 30000)
    }
  }

  connect()
  return { close: () => { closed = true; ws?.close() } }
}
```

- [ ] **Step 2: Update useMultiHostEventWs imports and WS path**

In `spa/src/hooks/useMultiHostEventWs.ts`:

```typescript
// Replace:
import { connectSessionEvents } from '../lib/session-events'
// With:
import { connectHostEvents } from '../lib/host-events'
```

Replace `connectSessionEvents` call and WS path:

```typescript
// Replace:
const wsUrl = hostWsUrl(hostId, '/ws/session-events')
const conn = connectSessionEvents(
// With:
const wsUrl = hostWsUrl(hostId, '/ws/host-events')
const conn = connectHostEvents(
```

- [ ] **Step 3: Delete old file**

```bash
rm spa/src/lib/session-events.ts
```

- [ ] **Step 4: Run lint and build**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run lint && pnpm run build`
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/host-events.ts spa/src/hooks/useMultiHostEventWs.ts
git rm spa/src/lib/session-events.ts
git commit -m "refactor(spa): rename session-events to host-events"
```

---

### Task 7: HostRuntime 擴充 + tmux 事件處理

**Files:**
- Modify: `spa/src/stores/useHostStore.ts:17-21`
- Modify: `spa/src/stores/useHostStore.test.ts`
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`

- [ ] **Step 1: Write failing test for new HostRuntime fields**

In `spa/src/stores/useHostStore.test.ts`, append:

```typescript
  it('setRuntime updates daemonState and tmuxState', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    state.setRuntime(defaultId, {
      status: 'disconnected',
      daemonState: 'refused',
      tmuxState: 'unavailable',
    })

    const updated = useHostStore.getState()
    expect(updated.runtime[defaultId].daemonState).toBe('refused')
    expect(updated.runtime[defaultId].tmuxState).toBe('unavailable')
  })

  it('setRuntime partial update preserves existing fields', () => {
    const state = useHostStore.getState()
    const defaultId = state.activeHostId!
    state.setRuntime(defaultId, { status: 'connected', daemonState: 'connected', tmuxState: 'ok' })
    state.setRuntime(defaultId, { tmuxState: 'unavailable' })

    const updated = useHostStore.getState()
    expect(updated.runtime[defaultId].status).toBe('connected')
    expect(updated.runtime[defaultId].daemonState).toBe('connected')
    expect(updated.runtime[defaultId].tmuxState).toBe('unavailable')
  })
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/stores/useHostStore.test.ts`
Expected: FAIL — `daemonState` property doesn't exist on type

- [ ] **Step 3: Extend HostRuntime interface**

In `spa/src/stores/useHostStore.ts`, update HostRuntime:

```typescript
export interface HostRuntime {
  status: 'connected' | 'disconnected' | 'reconnecting'
  latency?: number
  info?: HostInfo
  daemonState?: 'connected' | 'refused' | 'unreachable'
  tmuxState?: 'ok' | 'unavailable'
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/stores/useHostStore.test.ts`
Expected: PASS

- [ ] **Step 5: Add tmux event handler in useMultiHostEventWs**

In `spa/src/hooks/useMultiHostEventWs.ts`, add inside the onEvent callback (after the handoff handler):

```typescript
          // Handle 'tmux' event
          if (event.type === 'tmux') {
            useHostStore.getState().setRuntime(hostId, {
              tmuxState: event.value === 'ok' ? 'ok' : 'unavailable',
            })
          }
```

- [ ] **Step 6: Run lint**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run lint`
Expected: no errors

- [ ] **Step 7: Commit**

```bash
git add spa/src/stores/useHostStore.ts spa/src/stores/useHostStore.test.ts spa/src/hooks/useMultiHostEventWs.ts
git commit -m "feat(spa): extend HostRuntime with daemonState/tmuxState + tmux event"
```

---

### Task 8: host-connection — Health check 分類

**Files:**
- Create: `spa/src/lib/host-connection.ts`
- Create: `spa/src/lib/host-connection.test.ts`

- [ ] **Step 1: Write failing tests**

Create `spa/src/lib/host-connection.test.ts`:

```typescript
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { checkHealth, type HealthResult } from './host-connection'

describe('checkHealth', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('returns connected with latency on HTTP 200', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true, tmux: true }), { status: 200 })
    )

    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('connected')
    expect(result.tmux).toBe('ok')
    expect(result.latency).toBeGreaterThanOrEqual(0)
  })

  it('returns connected with tmux unavailable', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true, tmux: false }), { status: 200 })
    )

    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('connected')
    expect(result.tmux).toBe('unavailable')
  })

  it('returns refused on TypeError (connection refused)', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'))

    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('refused')
    expect(result.tmux).toBe('unavailable')
    expect(result.latency).toBeNull()
  })

  it('returns unreachable on AbortError (timeout)', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(
      Object.assign(new Error('signal is aborted'), { name: 'AbortError' })
    )

    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('unreachable')
    expect(result.tmux).toBe('unavailable')
    expect(result.latency).toBeNull()
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/lib/host-connection.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement checkHealth**

Create `spa/src/lib/host-connection.ts`:

```typescript
// spa/src/lib/host-connection.ts — Health check with L1/L2/L3 classification

export interface HealthResult {
  daemon: 'connected' | 'refused' | 'unreachable'
  tmux: 'ok' | 'unavailable'
  latency: number | null
}

const HEALTH_TIMEOUT_MS = 3000

export async function checkHealth(baseUrl: string): Promise<HealthResult> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), HEALTH_TIMEOUT_MS)

  try {
    const start = performance.now()
    const res = await fetch(`${baseUrl}/api/health`, { signal: controller.signal })
    const latency = Math.round(performance.now() - start)
    const data = await res.json()
    return {
      daemon: 'connected',
      tmux: data.tmux ? 'ok' : 'unavailable',
      latency,
    }
  } catch (err: unknown) {
    if (err instanceof Error && err.name === 'AbortError') {
      return { daemon: 'unreachable', tmux: 'unavailable', latency: null }
    }
    return { daemon: 'refused', tmux: 'unavailable', latency: null }
  } finally {
    clearTimeout(timer)
  }
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/lib/host-connection.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/host-connection.ts spa/src/lib/host-connection.test.ts
git commit -m "feat(spa): checkHealth with L1/L2/L3 classification"
```

---

### Task 9: ConnectionStateMachine — 重連狀態機

**Note:** `ConnectionStateMachine` 是純 class，不是 React hook。它在 `useMultiHostEventWs` 的 `useEffect` 中被直接實例化和管理（Task 10），避免���反 Rules of Hooks（hook 不能在 for loop 中呼叫）。

**Files:**
- Create: `spa/src/lib/connection-state-machine.ts`
- Create: `spa/src/lib/connection-state-machine.test.ts`

- [ ] **Step 1: Write failing tests**

Create `spa/src/lib/connection-state-machine.test.ts`:

```typescript
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ConnectionStateMachine } from './connection-state-machine'
import type { HealthResult } from './host-connection'

describe('ConnectionStateMachine', () => {
  let checkFn: ReturnType<typeof vi.fn<() => Promise<HealthResult>>>
  let onStateChange: ReturnType<typeof vi.fn>
  let sm: ConnectionStateMachine

  beforeEach(() => {
    vi.useFakeTimers()
    checkFn = vi.fn()
    onStateChange = vi.fn()
  })

  afterEach(() => {
    sm?.stop()
    vi.useRealTimers()
  })

  it('transitions to connected on first successful check', async () => {
    checkFn.mockResolvedValue({ daemon: 'connected', tmux: 'ok', latency: 10 })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()
    expect(onStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ daemon: 'connected', tmux: 'ok', latency: 10 })
    )
  })

  it('enters FAST_RETRY then L1 on 3 timeouts', async () => {
    checkFn.mockResolvedValue({ daemon: 'unreachable', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    // After 3 failed attempts, should report unreachable
    expect(checkFn).toHaveBeenCalledTimes(3)
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('unreachable')
  })

  it('enters FAST_RETRY then L2 on 3 refused', async () => {
    checkFn.mockResolvedValue({ daemon: 'refused', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    expect(checkFn).toHaveBeenCalledTimes(3)
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('refused')
  })

  it('recovers during FAST_RETRY if second attempt succeeds', async () => {
    checkFn
      .mockResolvedValueOnce({ daemon: 'unreachable', tmux: 'unavailable', latency: null })
      .mockResolvedValueOnce({ daemon: 'connected', tmux: 'ok', latency: 5 })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    expect(checkFn).toHaveBeenCalledTimes(2) // stopped early
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('connected')
  })

  it('L1 continues retrying in background', async () => {
    checkFn.mockResolvedValue({ daemon: 'unreachable', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    // Should be in background retry mode
    const countAfterTrigger = checkFn.mock.calls.length

    // Background retries happen automatically (each attempt ~3s timeout)
    // Advance time to allow one background attempt
    checkFn.mockResolvedValueOnce({ daemon: 'connected', tmux: 'ok', latency: 15 })
    await vi.advanceTimersByTimeAsync(3100)

    expect(checkFn.mock.calls.length).toBeGreaterThan(countAfterTrigger)
  })

  it('L2 does NOT continue retrying', async () => {
    checkFn.mockResolvedValue({ daemon: 'refused', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    const countAfterTrigger = checkFn.mock.calls.length
    await vi.advanceTimersByTimeAsync(10000)
    expect(checkFn.mock.calls.length).toBe(countAfterTrigger)
  })

  it('manual retry restarts FAST_RETRY for L2', async () => {
    checkFn.mockResolvedValue({ daemon: 'refused', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    const countBefore = checkFn.mock.calls.length
    checkFn.mockResolvedValue({ daemon: 'connected', tmux: 'ok', latency: 8 })
    await sm.trigger() // manual retry

    expect(checkFn.mock.calls.length).toBeGreaterThan(countBefore)
  })

  it('uses last attempt result for classification', async () => {
    checkFn
      .mockResolvedValueOnce({ daemon: 'unreachable', tmux: 'unavailable', latency: null })
      .mockResolvedValueOnce({ daemon: 'unreachable', tmux: 'unavailable', latency: null })
      .mockResolvedValueOnce({ daemon: 'refused', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    // Last attempt was refused → classified as L2
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('refused')
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/lib/connection-state-machine.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement ConnectionStateMachine**

Create `spa/src/lib/connection-state-machine.ts`:

```typescript
// spa/src/lib/connection-state-machine.ts — Pure class, no React dependency
import type { HealthResult } from './host-connection'

const FAST_RETRY_COUNT = 3

export class ConnectionStateMachine {
  private checkFn: () => Promise<HealthResult>
  private onStateChange: (result: HealthResult) => void
  private stopped = false
  private backgroundTimer: ReturnType<typeof setTimeout> | null = null

  constructor(
    checkFn: () => Promise<HealthResult>,
    onStateChange: (result: HealthResult) => void,
  ) {
    this.checkFn = checkFn
    this.onStateChange = onStateChange
  }

  /** Trigger a FAST_RETRY cycle (called on WS close or manual retry). */
  async trigger(): Promise<void> {
    this.stopBackground()
    this.stopped = false

    let lastResult: HealthResult | null = null

    // FAST_RETRY: up to 3 immediate attempts
    for (let i = 0; i < FAST_RETRY_COUNT; i++) {
      if (this.stopped) return
      lastResult = await this.checkFn()
      this.onStateChange(lastResult)

      if (lastResult.daemon === 'connected') {
        return // recovered
      }
    }

    if (!lastResult || this.stopped) return

    // Classify by last result
    if (lastResult.daemon === 'unreachable') {
      // L1: background continuous retry
      this.startBackground()
    }
    // L2 (refused): stop — no background retry
  }

  /** Start background continuous retry for L1. */
  private startBackground() {
    if (this.stopped) return
    // Schedule next attempt — the 3s fetch timeout IS the natural pace
    this.backgroundTimer = setTimeout(async () => {
      if (this.stopped) return
      const result = await this.checkFn()
      this.onStateChange(result)

      if (result.daemon === 'connected') {
        return // recovered
      }
      // Continue retrying
      this.startBackground()
    }, 100) // Small delay to prevent tight loop; actual pace is dominated by 3s timeout
  }

  private stopBackground() {
    if (this.backgroundTimer) {
      clearTimeout(this.backgroundTimer)
      this.backgroundTimer = null
    }
  }

  stop() {
    this.stopped = true
    this.stopBackground()
  }
}

```

- [ ] **Step 4: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/lib/connection-state-machine.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/connection-state-machine.ts spa/src/lib/connection-state-machine.test.ts
git commit -m "feat(spa): ConnectionStateMachine for L1/L2 reconnection"
```

---

### Task 10: WS 閘控 — host-events 停止自身 reconnect + SM 整合

**Key design:** `ConnectionStateMachine` 是純 class（Task 9），直接在 `useMultiHostEventWs` 的 `useEffect` 中 per-host 實例化，避免 Rules of Hooks 問題（hook 不能在 for loop 中呼叫）。

**Files:**
- Modify: `spa/src/lib/host-events.ts` (disable autoReconnect, add `reconnect()`)
- Modify: `spa/src/hooks/useMultiHostEventWs.ts` (integrate ConnectionStateMachine)

- [ ] **Step 1: Modify connectHostEvents — disable auto-reconnect + add reconnect()**

In `spa/src/lib/host-events.ts`, replace the entire file content:

```typescript
// spa/src/lib/host-events.ts

export interface HostEvent {
  type: 'handoff' | 'relay' | 'hook' | 'sessions' | 'tmux'
  session: string
  value: string
}

export interface EventConnection {
  close: () => void
  reconnect: () => void
}

export function connectHostEvents(
  url: string,
  onEvent: (event: HostEvent) => void,
  onClose?: () => void,
  onOpen?: () => void,
  getTicket?: () => Promise<string>,
  autoReconnect = true,
): EventConnection {
  let ws: WebSocket
  let retryMs = 1000
  let closed = false

  async function connect() {
    let wsUrl = url
    if (getTicket) {
      try {
        const ticket = await getTicket()
        const u = new URL(wsUrl)
        u.searchParams.set('ticket', ticket)
        wsUrl = u.toString()
      } catch {
        if (!closed && autoReconnect) setTimeout(connect, retryMs)
        retryMs = Math.min(retryMs * 2, 30000)
        return
      }
    }
    ws = new WebSocket(wsUrl)
    ws.onopen = () => { retryMs = 1000; onOpen?.() }
    ws.onmessage = (e) => {
      try {
        const event = JSON.parse(e.data) as HostEvent
        onEvent(event)
      } catch { /* ignore parse errors */ }
    }
    ws.onerror = () => { /* handled by onclose */ }
    ws.onclose = () => {
      if (closed) return
      onClose?.()
      if (autoReconnect) {
        setTimeout(() => {
          if (!closed) connect()
        }, retryMs)
        retryMs = Math.min(retryMs * 2, 30000)
      }
    }
  }

  connect()
  return {
    close: () => { closed = true; ws?.close() },
    reconnect: () => {
      if (!closed) {
        retryMs = 1000
        connect()
      }
    },
  }
}
```

- [ ] **Step 2: Rewrite useMultiHostEventWs with SM integration**

In `spa/src/hooks/useMultiHostEventWs.ts`, replace the entire file:

```typescript
// spa/src/hooks/useMultiHostEventWs.ts — Multi-host event WS + connection state machine
import { useEffect } from 'react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useStreamStore } from '../stores/useStreamStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { connectHostEvents, type EventConnection } from '../lib/host-events'
import { hostWsUrl, fetchWsTicket } from '../lib/host-api'
import { fetchHistory } from '../lib/api'
import { checkHealth } from '../lib/host-connection'
import { ConnectionStateMachine } from '../lib/connection-state-machine'
import type { Session } from '../lib/api'

export function useMultiHostEventWs() {
  const hostOrderKey = useHostStore((s) => s.hostOrder.join(','))

  useEffect(() => {
    const { hosts, hostOrder } = useHostStore.getState()
    const connections = new Map<string, EventConnection>()
    const stateMachines = new Map<string, ConnectionStateMachine>()

    for (const hostId of hostOrder) {
      if (!hosts[hostId]) continue
      const wsUrl = hostWsUrl(hostId, '/ws/host-events')
      const baseUrl = useHostStore.getState().getDaemonBase(hostId)

      // --- Connection state machine (per host) ---
      let conn: EventConnection | undefined

      const sm = new ConnectionStateMachine(
        () => checkHealth(baseUrl),
        (result) => {
          useHostStore.getState().setRuntime(hostId, {
            status: result.daemon === 'connected' ? 'connected' : 'disconnected',
            latency: result.latency ?? undefined,
            daemonState: result.daemon,
            tmuxState: result.tmux,
          })
          // On recovery → reconnect WS
          if (result.daemon === 'connected' && conn) {
            conn.reconnect()
          }
        },
      )
      stateMachines.set(hostId, sm)

      // --- WS connection (per host) ---
      conn = connectHostEvents(
        wsUrl,
        (event) => {
          if (event.type === 'sessions') {
            try {
              const data: Session[] = JSON.parse(event.value)
              useSessionStore.getState().replaceHost(hostId, data)
              for (const s of data) {
                useTabStore.getState().updateSessionCache(hostId, s.code, s.name)
              }
            } catch { /* ignore */ }
            return
          }
          if (event.type === 'hook') {
            try {
              const hookData = JSON.parse(event.value)
              useAgentStore.getState().handleHookEvent(hostId, event.session, hookData)
            } catch { /* ignore */ }
          }
          if (event.type === 'relay') {
            useStreamStore.getState().setRelayStatus(hostId, event.session, event.value === 'connected')
          }
          if (event.type === 'handoff') {
            const store = useStreamStore.getState()
            const daemonBase = useHostStore.getState().getDaemonBase(hostId)
            if (event.value === 'connected') {
              store.setHandoffProgress(hostId, event.session, '')
              useSessionStore.getState().fetchHost(hostId, daemonBase).then(() => {
                const sess = (useSessionStore.getState().sessions[hostId] ?? [])
                  .find((s) => s.code === event.session)
                if (sess && sess.mode !== 'terminal') {
                  fetchHistory(daemonBase, sess.code).then((msgs) => {
                    useStreamStore.getState().loadHistory(hostId, event.session, msgs)
                  }).catch(() => {})
                } else {
                  useStreamStore.getState().clearSession(hostId, event.session)
                }
              }).catch(() => {})
            } else if (event.value.startsWith('failed')) {
              store.setHandoffProgress(hostId, event.session, '')
              useSessionStore.getState().fetchHost(hostId, daemonBase).catch(() => {})
            } else {
              store.setHandoffProgress(hostId, event.session, event.value)
            }
          }
          if (event.type === 'tmux') {
            useHostStore.getState().setRuntime(hostId, {
              tmuxState: event.value === 'ok' ? 'ok' : 'unavailable',
            })
          }
        },
        // onClose — trigger SM health check (no auto-reconnect)
        () => {
          useHostStore.getState().setRuntime(hostId, { status: 'reconnecting' })
          sm.trigger()
        },
        // onOpen
        () => {
          useHostStore.getState().setRuntime(hostId, {
            status: 'connected',
            daemonState: 'connected',
          })
          useAgentStore.getState().clearSubagentsForHost(hostId)
          const daemonBase = useHostStore.getState().getDaemonBase(hostId)
          useSessionStore.getState().fetchHost(hostId, daemonBase).catch(() => {})
        },
        () => fetchWsTicket(hostId),
        false, // autoReconnect disabled — SM manages reconnection
      )
      connections.set(hostId, conn)
    }

    return () => {
      connections.forEach((c) => c.close())
      stateMachines.forEach((sm) => sm.stop())
    }
  }, [hostOrderKey])
}
```

**Key design points:**
- `ConnectionStateMachine` 在 `useEffect` 內 per-host 實例化（純 class，不是 hook，不違反 Rules of Hooks）
- WS `onClose` → 直接呼叫 `sm.trigger()`（同一 closure 內，不需跨 hook）
- SM `onStateChange` 回 `connected` → 直接呼叫 `conn.reconnect()`（同一 closure 內）
- Cleanup: `connections.forEach(close)` + `stateMachines.forEach(stop)`
- 不需要第二個 `useEffect` 監聽 `runtimeStatus`（避免水平觸發重複連線循環）

- [ ] **Step 3: Run lint and tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run lint && npx vitest run`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/lib/host-events.ts spa/src/hooks/useMultiHostEventWs.ts
git commit -m "feat(spa): WS gating — SM manages host-events reconnection"
```

---

### Task 11: Terminal WS 閘控

**Files:**
- Modify: `spa/src/lib/ws.ts` (add gate check)
- Modify: `spa/src/lib/ws.test.ts` (gate test)
- Modify: `spa/src/hooks/useTerminalWs.ts` (pass gate function)

- [ ] **Step 1: Write failing test for gate**

In `spa/src/lib/ws.test.ts`, append:

```typescript
describe('connectTerminal gate', () => {
  it('does not reconnect when gate returns false', () => {
    const gate = vi.fn().mockReturnValue(false)
    connectTerminal('ws://test', vi.fn(), vi.fn(), undefined, gate)
    wsInstances[0].simulateOpen()
    wsInstances[0].simulateClose()

    vi.advanceTimersByTime(1000)
    expect(wsInstances).toHaveLength(1) // no reconnect — gate blocked
  })

  it('reconnects when gate returns true', () => {
    const gate = vi.fn().mockReturnValue(true)
    connectTerminal('ws://test', vi.fn(), vi.fn(), undefined, gate)
    wsInstances[0].simulateOpen()
    wsInstances[0].simulateClose()

    vi.advanceTimersByTime(1000)
    expect(wsInstances).toHaveLength(2) // reconnected — gate allowed
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/lib/ws.test.ts`
Expected: FAIL — `connectTerminal` doesn't accept 5th argument

- [ ] **Step 3: Add gate parameter to connectTerminal**

In `spa/src/lib/ws.ts`:

```typescript
export function connectTerminal(
  url: string,
  onData: (data: ArrayBuffer) => void,
  onClose: () => void,
  onOpen?: () => void,
  canReconnect?: () => boolean,
): TerminalConnection {
  let closed = false
  let retryMs = 1000
  let ws: WebSocket

  function connect() {
    ws = new WebSocket(url)
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      retryMs = 1000
      onOpen?.()
    }
    ws.onmessage = (e) => {
      if (e.data instanceof ArrayBuffer) onData(e.data)
    }
    ws.onerror = () => {}
    ws.onclose = () => {
      if (closed) return
      onClose()
      setTimeout(() => {
        if (closed) return
        if (canReconnect && !canReconnect()) return // gate check
        connect()
      }, retryMs)
      retryMs = Math.min(retryMs * 2, 30000)
    }
  }

  connect()

  return {
    send: (data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(data)
    },
    resize: (cols, rows) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    },
    close: () => {
      closed = true
      ws.close()
    },
  }
}
```

- [ ] **Step 4: Add hostId to UseTerminalWsOpts and pass gate function**

In `spa/src/hooks/useTerminalWs.ts`:

1. Add `hostId` to the opts interface:
```typescript
// In UseTerminalWsOpts, add:
hostId?: string
```

2. Destructure `hostId` from opts and build the gate function:
```typescript
const { hostId, wsUrl, termRef, fitAddonRef, containerRef } = opts
// ...
const canReconnect = hostId
  ? () => {
      const runtime = useHostStore.getState().runtime[hostId]
      return !runtime || runtime.status === 'connected'
    }
  : undefined

const conn = connectTerminal(wsUrl, onDataCb, onCloseCb, onOpenCb, canReconnect)
```

3. Add `useHostStore` import:
```typescript
import { useHostStore } from '../stores/useHostStore'
```

Note: `hostId` is optional for backward compatibility. Callers that pass `hostId` get gate protection; callers that don't keep existing behavior (always reconnect). The calling component (e.g., `SessionPaneContent`) already knows the `hostId` from `PaneContent` and passes it through.

- [ ] **Step 5: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/lib/ws.test.ts`
Expected: PASS

- [ ] **Step 6: Run all SPA tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add spa/src/lib/ws.ts spa/src/lib/ws.test.ts spa/src/hooks/useTerminalWs.ts
git commit -m "feat(spa): terminal WS gate — pauses reconnect when host disconnected"
```
