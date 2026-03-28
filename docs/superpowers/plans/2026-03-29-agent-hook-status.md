# Agent Hook Status Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace poller-based CC status detection with hook-driven agent event push, with daemon as pure relay and SPA interpreting all status logic.

**Architecture:** `tbox hook` CLI reads CC hook stdin + tmux session name → POSTs to daemon `/api/agent/event` → daemon stores raw event in new `agent_events` SQLite table → broadcasts via session-events WS as `"hook"` type → SPA agent store interprets events into running/waiting/idle status → drives Tab/SessionPanel/StatusBar UI.

**Tech Stack:** Go (daemon + CLI), SQLite (agent_events table), React 19 + Zustand 5 (SPA), Tailwind 4 (styling), Vitest (tests)

**Spec:** `docs/superpowers/specs/2026-03-29-agent-hook-status-design.md`

---

## File Structure

### Go (daemon + CLI)

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/store/agent_event.go` | Create | AgentEventStore: new SQLite table + CRUD |
| `internal/store/agent_event_test.go` | Create | AgentEventStore tests |
| `internal/module/agent/module.go` | Create | Agent module: init, routes, WS broadcast |
| `internal/module/agent/handler.go` | Create | POST /api/agent/event handler |
| `internal/module/agent/handler_test.go` | Create | Handler tests |
| `cmd/tbox/main.go` | Modify | Add `hook` + `setup` subcommands, wire agent module |
| `cmd/tbox/hook.go` | Create | `tbox hook` subcommand |
| `cmd/tbox/hook_test.go` | Create | Hook subcommand tests |
| `cmd/tbox/setup.go` | Create | `tbox setup` subcommand |
| `cmd/tbox/setup_test.go` | Create | Setup subcommand tests |
| `internal/module/cc/poller.go` | Modify | Remove CC status detection loop |
| `internal/module/cc/module.go` | Modify | Remove poller start + snapshot |

### SPA (React)

| File | Action | Responsibility |
|------|--------|----------------|
| `spa/src/stores/useAgentStore.ts` | Create | Agent state: hook events → status per session |
| `spa/src/stores/useAgentStore.test.ts` | Create | Agent store tests |
| `spa/src/components/TabStatusDot.tsx` | Create | Tab status indicator (3 styles A/B/C) |
| `spa/src/components/TabStatusDot.test.tsx` | Create | Tab status dot tests |
| `spa/src/hooks/useSessionEventWs.ts` | Modify | Add `"hook"` event handler |
| `spa/src/components/SortableTab.tsx` | Modify | Integrate TabStatusDot |
| `spa/src/components/SessionPanel.tsx` | Modify | Read from agent store, new dot position |
| `spa/src/components/SessionStatusBadge.tsx` | Modify | Update status types + colors |
| `spa/src/components/StatusBar.tsx` | Modify | Show agent name + version |
| `spa/src/stores/useStreamStore.ts` | Modify | Remove `sessionStatus` field |
| `spa/src/lib/settings-section-registry.ts` | — | Existing, used for new setting |

---

## Task 1: Agent Event Store (SQLite)

**Files:**
- Create: `internal/store/agent_event.go`
- Create: `internal/store/agent_event_test.go`

- [ ] **Step 1: Write failing tests for AgentEventStore**

```go
// internal/store/agent_event_test.go
package store

import (
	"encoding/json"
	"testing"
)

func openTestAgentEventStore(t *testing.T) *AgentEventStore {
	t.Helper()
	s, err := OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAgentEventStore_SetAndGet(t *testing.T) {
	s := openTestAgentEventStore(t)

	raw := json.RawMessage(`{"sessionId":"abc","hook_event_name":"Stop"}`)
	if err := s.Set("my-project", "Stop", raw); err != nil {
		t.Fatalf("set: %v", err)
	}

	ev, err := s.Get("my-project")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ev == nil {
		t.Fatal("expected event, got nil")
	}
	if ev.TmuxSession != "my-project" {
		t.Errorf("tmux_session: want my-project, got %s", ev.TmuxSession)
	}
	if ev.EventName != "Stop" {
		t.Errorf("event_name: want Stop, got %s", ev.EventName)
	}
}

func TestAgentEventStore_Overwrite(t *testing.T) {
	s := openTestAgentEventStore(t)

	raw1 := json.RawMessage(`{"hook_event_name":"SessionStart"}`)
	raw2 := json.RawMessage(`{"hook_event_name":"Stop"}`)
	s.Set("proj", "SessionStart", raw1)
	s.Set("proj", "Stop", raw2)

	ev, _ := s.Get("proj")
	if ev.EventName != "Stop" {
		t.Errorf("want Stop after overwrite, got %s", ev.EventName)
	}
}

func TestAgentEventStore_GetMissing(t *testing.T) {
	s := openTestAgentEventStore(t)
	ev, err := s.Get("nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ev != nil {
		t.Error("expected nil for missing key")
	}
}

func TestAgentEventStore_ListAll(t *testing.T) {
	s := openTestAgentEventStore(t)
	s.Set("proj-a", "Stop", json.RawMessage(`{}`))
	s.Set("proj-b", "SessionStart", json.RawMessage(`{}`))

	all, err := s.ListAll()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2, got %d", len(all))
	}
}

func TestAgentEventStore_Delete(t *testing.T) {
	s := openTestAgentEventStore(t)
	s.Set("proj", "Stop", json.RawMessage(`{}`))
	s.Delete("proj")

	ev, _ := s.Get("proj")
	if ev != nil {
		t.Error("expected nil after delete")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/store/ -run TestAgentEvent -v`
Expected: FAIL — `AgentEventStore` not defined

- [ ] **Step 3: Implement AgentEventStore**

```go
// internal/store/agent_event.go
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

// AgentEvent is a single hook event stored per tmux session.
type AgentEvent struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
}

// AgentEventStore persists the latest agent hook event per tmux session.
type AgentEventStore struct{ db *sql.DB }

func OpenAgentEvent(path string) (*AgentEventStore, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open agent event db: %w", err)
	}
	if err := migrateAgentEventDB(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate agent event db: %w", err)
	}
	return &AgentEventStore{db: db}, nil
}

func migrateAgentEventDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_events (
			tmux_session TEXT PRIMARY KEY,
			event_name   TEXT NOT NULL,
			raw_event    TEXT NOT NULL,
			updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (s *AgentEventStore) Close() error { return s.db.Close() }

func (s *AgentEventStore) Set(tmuxSession, eventName string, rawEvent json.RawMessage) error {
	_, err := s.db.Exec(`
		INSERT INTO agent_events (tmux_session, event_name, raw_event, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tmux_session) DO UPDATE SET
			event_name = excluded.event_name,
			raw_event  = excluded.raw_event,
			updated_at = CURRENT_TIMESTAMP
	`, tmuxSession, eventName, string(rawEvent))
	return err
}

func (s *AgentEventStore) Get(tmuxSession string) (*AgentEvent, error) {
	var ev AgentEvent
	var raw string
	err := s.db.QueryRow(`
		SELECT tmux_session, event_name, raw_event
		FROM agent_events WHERE tmux_session = ?
	`, tmuxSession).Scan(&ev.TmuxSession, &ev.EventName, &raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ev.RawEvent = json.RawMessage(raw)
	return &ev, nil
}

func (s *AgentEventStore) ListAll() ([]AgentEvent, error) {
	rows, err := s.db.Query(`
		SELECT tmux_session, event_name, raw_event
		FROM agent_events ORDER BY tmux_session
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentEvent
	for rows.Next() {
		var ev AgentEvent
		var raw string
		if err := rows.Scan(&ev.TmuxSession, &ev.EventName, &raw); err != nil {
			return nil, err
		}
		ev.RawEvent = json.RawMessage(raw)
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *AgentEventStore) Delete(tmuxSession string) error {
	_, err := s.db.Exec("DELETE FROM agent_events WHERE tmux_session = ?", tmuxSession)
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/store/ -run TestAgentEvent -v`
Expected: PASS (all 5 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/store/agent_event.go internal/store/agent_event_test.go
git commit -m "feat: add AgentEventStore for hook event persistence"
```

---

## Task 2: Agent Module — Skeleton + Handler

**Files:**
- Create: `internal/module/agent/module.go`
- Create: `internal/module/agent/handler.go`
- Create: `internal/module/agent/handler_test.go`

- [ ] **Step 1: Write failing handler test**

```go
// internal/module/agent/handler_test.go
package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/tmux-box/internal/store"
)

func TestHandleEvent_StoresAndReturns(t *testing.T) {
	es, _ := store.OpenAgentEvent(":memory:")
	defer es.Close()

	m := &Module{events: es}

	body := EventRequest{
		TmuxSession: "my-project",
		EventName:   "Stop",
		RawEvent:    json.RawMessage(`{"sessionId":"abc"}`),
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/agent/event", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}

	ev, _ := es.Get("my-project")
	if ev == nil {
		t.Fatal("event not stored")
	}
	if ev.EventName != "Stop" {
		t.Errorf("event_name: want Stop, got %s", ev.EventName)
	}
}

func TestHandleEvent_BadJSON(t *testing.T) {
	es, _ := store.OpenAgentEvent(":memory:")
	defer es.Close()
	m := &Module{events: es}

	req := httptest.NewRequest("POST", "/api/agent/event", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
}

func TestHandleEvent_MissingTmuxSession(t *testing.T) {
	es, _ := store.OpenAgentEvent(":memory:")
	defer es.Close()
	m := &Module{events: es}

	body := EventRequest{EventName: "Stop", RawEvent: json.RawMessage(`{}`)}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/agent/event", bytes.NewReader(b))
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	// tmux_session 為空仍接受（spec: 不在 tmux 內時送空字串）
	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/agent/ -run TestHandleEvent -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Implement module skeleton + handler**

```go
// internal/module/agent/module.go
package agent

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/module/session"
	"github.com/wake/tmux-box/internal/store"
)

type Module struct {
	core     *core.Core
	events   *store.AgentEventStore
	sessions session.SessionProvider
}

func New(events *store.AgentEventStore) *Module {
	return &Module{events: events}
}

func (m *Module) Name() string           { return "agent" }
func (m *Module) Dependencies() []string { return []string{"session"} }

func (m *Module) Init(c *core.Core) error {
	m.core = c
	m.sessions = c.Registry.MustGet(session.RegistryKey).(session.SessionProvider)
	return nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agent/event", m.handleEvent)
}

func (m *Module) Start(ctx context.Context) error {
	m.core.Events.OnSubscribe(func(sub *core.EventSubscriber) {
		m.sendSnapshot(sub)
	})
	return nil
}

func (m *Module) Stop(ctx context.Context) error { return nil }

func (m *Module) sendSnapshot(sub *core.EventSubscriber) {
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("agent: snapshot list error: %v", err)
		return
	}

	sessions, err := m.sessions.ListSessions()
	if err != nil {
		log.Printf("agent: snapshot sessions error: %v", err)
		return
	}

	nameToCode := make(map[string]string, len(sessions))
	for _, s := range sessions {
		nameToCode[s.Name] = s.Code
	}

	for _, ev := range all {
		code, ok := nameToCode[ev.TmuxSession]
		if !ok {
			continue
		}
		payload, _ := json.Marshal(ev)
		event := core.SessionEvent{
			Type:    "hook",
			Session: code,
			Value:   string(payload),
		}
		data, _ := json.Marshal(event)
		sub.Send(data)
	}
}
```

```go
// internal/module/agent/handler.go
package agent

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/wake/tmux-box/internal/core"
)

type EventRequest struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
}

func (m *Module) handleEvent(w http.ResponseWriter, r *http.Request) {
	var req EventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	// Store event
	if err := m.events.Set(req.TmuxSession, req.EventName, req.RawEvent); err != nil {
		log.Printf("agent: store event error: %v", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	// Broadcast via WS if we can resolve session code
	if m.core != nil && m.sessions != nil && req.TmuxSession != "" {
		if code := m.resolveSessionCode(req.TmuxSession); code != "" {
			payload, _ := json.Marshal(store.AgentEvent{
				TmuxSession: req.TmuxSession,
				EventName:   req.EventName,
				RawEvent:    req.RawEvent,
			})
			m.core.Events.Broadcast(code, "hook", string(payload))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (m *Module) resolveSessionCode(tmuxSession string) string {
	sessions, err := m.sessions.ListSessions()
	if err != nil {
		return ""
	}
	for _, s := range sessions {
		if s.Name == tmuxSession {
			return s.Code
		}
	}
	return ""
}
```

Note: `handler.go` imports `store` for `store.AgentEvent` — add the import:
```go
import (
	"github.com/wake/tmux-box/internal/store"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/agent/ -v`
Expected: PASS (all 3 tests)

- [ ] **Step 5: Wire agent module into main.go**

In `cmd/tbox/main.go`, add the import and module registration:

```go
// Add import:
"github.com/wake/tmux-box/internal/module/agent"

// In runServe(), after opening MetaStore, open AgentEventStore:
agentEvents, err := store.OpenAgentEvent(filepath.Join(cfg.DataDir, "agent_events.db"))
if err != nil {
    log.Fatalf("agent event store: %v", err)
}
defer agentEvents.Close()

// After c.AddModule(stream.New()):
c.AddModule(agent.New(agentEvents))
```

- [ ] **Step 6: Run full daemon tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./... -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/module/agent/ cmd/tbox/main.go
git commit -m "feat: add agent module with POST /api/agent/event endpoint"
```

---

## Task 3: tbox hook Subcommand

**Files:**
- Create: `cmd/tbox/hook.go`
- Create: `cmd/tbox/hook_test.go`
- Modify: `cmd/tbox/main.go`

- [ ] **Step 1: Write failing tests**

```go
// cmd/tbox/hook_test.go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildHookPayload(t *testing.T) {
	stdin := `{"sessionId":"abc123","hook_event_name":"Stop"}`
	payload := buildHookPayload("my-session", "Stop", strings.NewReader(stdin))

	if payload.TmuxSession != "my-session" {
		t.Errorf("tmux_session: want my-session, got %s", payload.TmuxSession)
	}
	if payload.EventName != "Stop" {
		t.Errorf("event_name: want Stop, got %s", payload.EventName)
	}
	if payload.RawEvent == nil {
		t.Fatal("raw_event is nil")
	}
}

func TestBuildHookPayload_EmptyStdin(t *testing.T) {
	payload := buildHookPayload("sess", "Stop", strings.NewReader(""))
	if payload.RawEvent == nil {
		t.Fatal("raw_event should be empty object for empty stdin")
	}
}

func TestPostHookEvent(t *testing.T) {
	var received hookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	payload := hookPayload{
		TmuxSession: "proj",
		EventName:   "Stop",
		RawEvent:    json.RawMessage(`{"sessionId":"x"}`),
	}

	err := postHookEvent(srv.URL+"/api/agent/event", payload)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if received.TmuxSession != "proj" {
		t.Errorf("want proj, got %s", received.TmuxSession)
	}
}

func TestPostHookEvent_ServerDown(t *testing.T) {
	payload := hookPayload{TmuxSession: "x", EventName: "Stop", RawEvent: json.RawMessage(`{}`)}
	err := postHookEvent("http://127.0.0.1:1/api/agent/event", payload)
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./cmd/tbox/ -run TestBuildHook -v`
Expected: FAIL — `buildHookPayload` not defined

- [ ] **Step 3: Implement tbox hook**

```go
// cmd/tbox/hook.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/wake/tmux-box/internal/config"
)

type hookPayload struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
}

func runHook(args []string) {
	if len(args) < 1 {
		os.Exit(0) // silent — don't break CC
	}
	eventName := args[0]

	tmuxSession := queryTmuxSession()
	payload := buildHookPayload(tmuxSession, eventName, os.Stdin)

	cfg, _ := config.Load("")
	url := fmt.Sprintf("http://%s:%d/api/agent/event", cfg.Bind, cfg.Port)

	_ = postHookEvent(url, payload) // ignore errors — don't break CC
}

func queryTmuxSession() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return ""
	}
	s := string(out)
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	return s
}

func buildHookPayload(tmuxSession, eventName string, stdin io.Reader) hookPayload {
	raw, err := io.ReadAll(stdin)
	if err != nil || len(raw) == 0 {
		raw = []byte("{}")
	}
	return hookPayload{
		TmuxSession: tmuxSession,
		EventName:   eventName,
		RawEvent:    json.RawMessage(raw),
	}
}

func postHookEvent(url string, payload hookPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
```

- [ ] **Step 4: Add `hook` case to main.go switch**

In `cmd/tbox/main.go`, update the `switch` and usage message:

```go
// Update usage:
fmt.Fprintf(os.Stderr, "Commands: serve, relay, hook, setup\n")

// Add case:
case "hook":
    runHook(os.Args[2:])
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./cmd/tbox/ -run "TestBuildHook|TestPostHook" -v`
Expected: PASS (all 4 tests)

- [ ] **Step 6: Commit**

```bash
git add cmd/tbox/hook.go cmd/tbox/hook_test.go cmd/tbox/main.go
git commit -m "feat: add tbox hook subcommand for CC hook relay"
```

---

## Task 4: tbox setup Subcommand

**Files:**
- Create: `cmd/tbox/setup.go`
- Create: `cmd/tbox/setup_test.go`
- Modify: `cmd/tbox/main.go`

- [ ] **Step 1: Write failing tests**

```go
// cmd/tbox/setup_test.go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeHooks_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	err := mergeHooks(path, "/usr/local/bin/tbox", false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	data, _ := os.ReadFile(path)
	var settings map[string]any
	json.Unmarshal(data, &settings)

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks not found")
	}
	if _, ok := hooks["Stop"]; !ok {
		t.Error("Stop hook not found")
	}
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("SessionStart hook not found")
	}
}

func TestMergeHooks_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	mergeHooks(path, "/usr/local/bin/tbox", false)
	mergeHooks(path, "/usr/local/bin/tbox", false)

	data, _ := os.ReadFile(path)
	var settings map[string]any
	json.Unmarshal(data, &settings)

	hooks := settings["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	// Should have exactly 1 entry, not 2
	if len(stop) != 1 {
		t.Errorf("Stop hook entries: want 1, got %d", len(stop))
	}
}

func TestMergeHooks_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	existing := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"other-tool"}]}]},"other":"value"}`
	os.WriteFile(path, []byte(existing), 0644)

	mergeHooks(path, "/usr/local/bin/tbox", false)

	data, _ := os.ReadFile(path)
	var settings map[string]any
	json.Unmarshal(data, &settings)

	if settings["other"] != "value" {
		t.Error("lost existing key")
	}

	hooks := settings["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	if len(stop) != 2 {
		t.Errorf("Stop should have 2 entries (existing + tbox), got %d", len(stop))
	}
}

func TestMergeHooks_Remove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	mergeHooks(path, "/usr/local/bin/tbox", false)
	mergeHooks(path, "/usr/local/bin/tbox", true)

	data, _ := os.ReadFile(path)
	var settings map[string]any
	json.Unmarshal(data, &settings)

	hooks := settings["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	if len(stop) != 0 {
		t.Errorf("Stop should be empty after remove, got %d", len(stop))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./cmd/tbox/ -run TestMergeHooks -v`
Expected: FAIL — `mergeHooks` not defined

- [ ] **Step 3: Implement tbox setup**

```go
// cmd/tbox/setup.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var hookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"Stop",
	"Notification",
	"PermissionRequest",
	"SessionEnd",
}

func runSetup(args []string) {
	remove := len(args) > 0 && args[0] == "--remove"

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: cannot find home dir: %v\n", err)
		os.Exit(1)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: cannot find executable path: %v\n", err)
		os.Exit(1)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	if err := mergeHooks(settingsPath, exe, remove); err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(1)
	}

	if remove {
		fmt.Println("tbox hooks removed from", settingsPath)
	} else {
		fmt.Println("tbox hooks installed to", settingsPath)
	}
	fmt.Println("Please restart Claude Code for changes to take effect.")
}

func mergeHooks(path, tboxPath string, remove bool) error {
	var settings map[string]any

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", path, err)
		}
		settings = make(map[string]any)
	} else {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = make(map[string]any)
	}

	for _, event := range hookEvents {
		cmd := fmt.Sprintf("%s hook %s", tboxPath, event)
		entries := toEntrySlice(hooks[event])

		if remove {
			entries = filterOutTbox(entries, tboxPath)
		} else {
			if !hasTboxEntry(entries, tboxPath) {
				entry := map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": cmd,
						},
					},
				}
				entries = append(entries, entry)
			}
		}

		hooks[event] = entries
	}

	settings["hooks"] = hooks

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0644)
}

func toEntrySlice(v any) []any {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	return arr
}

func hasTboxEntry(entries []any, tboxPath string) bool {
	for _, e := range entries {
		if entryMatchesTbox(e, tboxPath) {
			return true
		}
	}
	return false
}

func filterOutTbox(entries []any, tboxPath string) []any {
	var out []any
	for _, e := range entries {
		if !entryMatchesTbox(e, tboxPath) {
			out = append(out, e)
		}
	}
	if out == nil {
		out = []any{}
	}
	return out
}

func entryMatchesTbox(entry any, tboxPath string) bool {
	m, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	innerHooks, ok := m["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range innerHooks {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hm["command"].(string)
		if strings.Contains(cmd, tboxPath) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Add `setup` case to main.go switch**

```go
case "setup":
    runSetup(os.Args[2:])
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./cmd/tbox/ -run TestMergeHooks -v`
Expected: PASS (all 4 tests)

- [ ] **Step 6: Commit**

```bash
git add cmd/tbox/setup.go cmd/tbox/setup_test.go cmd/tbox/main.go
git commit -m "feat: add tbox setup subcommand for hook installation"
```

---

## Task 5: Remove Poller CC Status Detection

**Files:**
- Modify: `internal/module/cc/poller.go`
- Modify: `internal/module/cc/module.go`

- [ ] **Step 1: Read current poller.go and module.go to understand what to remove**

Read both files fully before editing.

- [ ] **Step 2: Remove the poller polling loop**

In `poller.go`, remove the `startPoller` function body that polls CC status. Keep the file but gut the loop — the poller goroutine, `lastStatus` map, `sendStatusSnapshot`, ticker, and all `Detect()` calls.

Remove `resetPollerCh` field and `resetPoller` method if they only serve the status detection.

- [ ] **Step 3: Remove poller start from module.go**

In `module.go` `Start()`, remove the call to `startPoller(ctx)` and the `OnSubscribe` callback that sends status snapshots (the agent module now handles snapshots).

- [ ] **Step 4: Run existing CC module tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/cc/ -v`
Expected: PASS (some tests may need updating if they test poller behavior)

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./... -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/module/cc/poller.go internal/module/cc/module.go
git commit -m "refactor: remove CC status detection poller, replaced by agent hooks"
```

---

## Task 6: SPA Agent Store

**Files:**
- Create: `spa/src/stores/useAgentStore.ts`
- Create: `spa/src/stores/useAgentStore.test.ts`
- Modify: `spa/src/hooks/useSessionEventWs.ts`
- Modify: `spa/src/stores/useStreamStore.ts`

- [ ] **Step 1: Write failing agent store tests**

```typescript
// spa/src/stores/useAgentStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useAgentStore } from './useAgentStore'

beforeEach(() => {
  useAgentStore.setState({
    events: {},
    statuses: {},
    unread: {},
  })
})

describe('useAgentStore', () => {
  it('processes hook event and derives running status', () => {
    useAgentStore.getState().handleHookEvent('abc', {
      tmux_session: 'proj',
      event_name: 'UserPromptSubmit',
      raw_event: { sessionId: 'x' },
    })
    expect(useAgentStore.getState().statuses['abc']).toBe('running')
  })

  it('derives waiting from Notification event', () => {
    useAgentStore.getState().handleHookEvent('abc', {
      tmux_session: 'proj',
      event_name: 'Notification',
      raw_event: {},
    })
    expect(useAgentStore.getState().statuses['abc']).toBe('waiting')
  })

  it('derives idle from Stop event', () => {
    useAgentStore.getState().handleHookEvent('abc', {
      tmux_session: 'proj',
      event_name: 'Stop',
      raw_event: {},
    })
    expect(useAgentStore.getState().statuses['abc']).toBe('idle')
  })

  it('marks unread on Stop when not focused', () => {
    useAgentStore.getState().handleHookEvent('abc', {
      tmux_session: 'proj',
      event_name: 'Stop',
      raw_event: {},
    })
    expect(useAgentStore.getState().unread['abc']).toBe(true)
  })

  it('clears unread on markRead', () => {
    useAgentStore.getState().handleHookEvent('abc', {
      tmux_session: 'proj',
      event_name: 'Stop',
      raw_event: {},
    })
    useAgentStore.getState().markRead('abc')
    expect(useAgentStore.getState().unread['abc']).toBeFalsy()
  })

  it('clears status on SessionEnd', () => {
    useAgentStore.getState().handleHookEvent('abc', {
      tmux_session: 'proj',
      event_name: 'UserPromptSubmit',
      raw_event: {},
    })
    useAgentStore.getState().handleHookEvent('abc', {
      tmux_session: 'proj',
      event_name: 'SessionEnd',
      raw_event: {},
    })
    expect(useAgentStore.getState().statuses['abc']).toBeUndefined()
  })

  it('extracts agent info from raw_event', () => {
    useAgentStore.getState().handleHookEvent('abc', {
      tmux_session: 'proj',
      event_name: 'SessionStart',
      raw_event: { sessionId: 'sess-123' },
    })
    expect(useAgentStore.getState().events['abc']?.raw_event.sessionId).toBe('sess-123')
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/stores/useAgentStore.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement agent store**

```typescript
// spa/src/stores/useAgentStore.ts
import { create } from 'zustand'

export type AgentStatus = 'running' | 'waiting' | 'idle'

export interface AgentHookEvent {
  tmux_session: string
  event_name: string
  raw_event: Record<string, unknown>
}

interface AgentState {
  events: Record<string, AgentHookEvent>
  statuses: Record<string, AgentStatus>
  unread: Record<string, boolean>
  focusedSession: string | null

  handleHookEvent: (session: string, event: AgentHookEvent) => void
  markRead: (session: string) => void
  setFocusedSession: (session: string | null) => void
}

function deriveStatus(eventName: string): AgentStatus | null {
  switch (eventName) {
    case 'SessionStart':
    case 'UserPromptSubmit':
      return 'running'
    case 'Notification':
    case 'PermissionRequest':
      return 'waiting'
    case 'Stop':
      return 'idle'
    default:
      return null
  }
}

export const useAgentStore = create<AgentState>()((set, get) => ({
  events: {},
  statuses: {},
  unread: {},
  focusedSession: null,

  handleHookEvent: (session, event) =>
    set((s) => {
      if (event.event_name === 'SessionEnd') {
        const { [session]: _ev, ...restEvents } = s.events
        const { [session]: _st, ...restStatuses } = s.statuses
        const { [session]: _ur, ...restUnread } = s.unread
        return { events: restEvents, statuses: restStatuses, unread: restUnread }
      }

      const status = deriveStatus(event.event_name)
      const newStatuses = status
        ? { ...s.statuses, [session]: status }
        : s.statuses

      const isIdle = status === 'idle' || status === 'waiting'
      const isFocused = get().focusedSession === session
      const newUnread = isIdle && !isFocused
        ? { ...s.unread, [session]: true }
        : s.unread

      return {
        events: { ...s.events, [session]: event },
        statuses: newStatuses,
        unread: newUnread,
      }
    }),

  markRead: (session) =>
    set((s) => {
      const { [session]: _, ...rest } = s.unread
      return { unread: rest }
    }),

  setFocusedSession: (session) => {
    set({ focusedSession: session })
    if (session) {
      get().markRead(session)
    }
  },
}))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/stores/useAgentStore.test.ts`
Expected: PASS (all 7 tests)

- [ ] **Step 5: Add hook event handler to useSessionEventWs.ts**

Add a new `if` block for `event.type === 'hook'`:

```typescript
if (event.type === 'hook') {
  try {
    const hookData = JSON.parse(event.value)
    useAgentStore.getState().handleHookEvent(event.session, hookData)
  } catch { /* ignore parse errors */ }
}
```

Add import: `import { useAgentStore } from '../stores/useAgentStore'`

- [ ] **Step 6: Remove `sessionStatus` from useStreamStore.ts**

Remove the `sessionStatus` field, `setSessionStatus` action, and the `status` event handling from `useSessionEventWs.ts`.

- [ ] **Step 7: Run SPA tests**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add spa/src/stores/useAgentStore.ts spa/src/stores/useAgentStore.test.ts \
  spa/src/hooks/useSessionEventWs.ts spa/src/stores/useStreamStore.ts
git commit -m "feat: add agent store with hook event → status state machine"
```

---

## Task 7: SPA UI — Tab Status Indicator

**Files:**
- Create: `spa/src/components/TabStatusDot.tsx`
- Create: `spa/src/components/TabStatusDot.test.tsx`
- Modify: `spa/src/components/SortableTab.tsx`
- Modify: `spa/src/lib/pane-labels.ts`

- [ ] **Step 1: Write failing TabStatusDot tests**

```typescript
// spa/src/components/TabStatusDot.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TabStatusDot } from './TabStatusDot'

describe('TabStatusDot', () => {
  it('renders nothing when status is undefined', () => {
    const { container } = render(<TabStatusDot status={undefined} style="overlay" isActive={false} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders dot with running class for overlay style', () => {
    render(<TabStatusDot status="running" style="overlay" isActive={false} />)
    const dot = screen.getByTestId('tab-status-dot')
    expect(dot).toBeTruthy()
  })

  it('renders dot for replace style', () => {
    render(<TabStatusDot status="running" style="replace" isActive={false} />)
    const dot = screen.getByTestId('tab-status-dot')
    expect(dot).toBeTruthy()
  })

  it('renders dot for inline style', () => {
    render(<TabStatusDot status="waiting" style="inline" isActive={false} />)
    const dot = screen.getByTestId('tab-status-dot')
    expect(dot).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/components/TabStatusDot.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: Implement TabStatusDot**

```typescript
// spa/src/components/TabStatusDot.tsx
import type { AgentStatus } from '../stores/useAgentStore'

export type TabIndicatorStyle = 'overlay' | 'replace' | 'inline'

interface Props {
  status: AgentStatus | undefined
  style: TabIndicatorStyle
  isActive: boolean
}

const STATUS_COLORS: Record<AgentStatus, string> = {
  running: 'bg-green-400',
  waiting: 'bg-yellow-400',
  idle: 'bg-gray-500',
}

export function TabStatusDot({ status, style, isActive }: Props) {
  if (!status) return null

  const color = STATUS_COLORS[status]
  const breathe = status === 'running' ? 'animate-breathe' : ''

  if (style === 'overlay') {
    return (
      <span
        data-testid="tab-status-dot"
        className={`absolute top-0 -right-px w-1.5 h-1.5 rounded-full ${color} ${breathe}`}
        style={{
          boxShadow: `0 0 0 1.5px var(${isActive ? '--color-surface-active' : '--color-surface-secondary'})`,
        }}
      />
    )
  }

  if (style === 'replace') {
    return (
      <span
        data-testid="tab-status-dot"
        className={`inline-block w-2 h-2 rounded-full flex-shrink-0 ${color} ${breathe}`}
      />
    )
  }

  // inline
  return (
    <span
      data-testid="tab-status-dot"
      className={`inline-block w-1.5 h-1.5 rounded-full flex-shrink-0 ${color} ${breathe}`}
    />
  )
}
```

- [ ] **Step 4: Add breathe animation CSS**

In `spa/src/index.css`, add the animation and utility. The breathe animation uses `background-color` transitioning to the tab background (not opacity), via CSS custom properties:

```css
@keyframes breathe {
  0%, 100% { background-color: var(--breathe-color); }
  50% { background-color: var(--breathe-bg); }
}

@utility animate-breathe {
  animation: breathe 2s ease-in-out infinite;
}
```

Update `TabStatusDot` overlay style to set the CSS variables inline:

```tsx
if (style === 'overlay') {
  const bgVar = isActive ? 'var(--color-surface-active)' : 'var(--color-surface-secondary)'
  return (
    <span
      data-testid="tab-status-dot"
      className={`absolute top-0 -right-px w-1.5 h-1.5 rounded-full ${color} ${breathe}`}
      style={{
        boxShadow: `0 0 0 1.5px ${bgVar}`,
        '--breathe-color': status === 'running' ? '#4ade80' : undefined,
        '--breathe-bg': status === 'running' ? bgVar : undefined,
      } as React.CSSProperties}
    />
  )
}
```

Same pattern for `replace` and `inline` styles — set `--breathe-bg` based on `isActive`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/components/TabStatusDot.test.tsx`
Expected: PASS

- [ ] **Step 6: Integrate into SortableTab.tsx**

Modify `SortableTab.tsx`:

1. Add imports:
```typescript
import { useAgentStore } from '../stores/useAgentStore'
import { TabStatusDot } from './TabStatusDot'
```

2. Inside the component, read agent state:
```typescript
const agentStatus = useAgentStore((s) => s.statuses[tab.id] ?? undefined)
const isUnread = useAgentStore((s) => !!s.unread[tab.id])
const tabIndicatorStyle = useAgentStore((s) => s.tabIndicatorStyle)
```

Note: `tab.id` 需對應到 session code。查看 `getPrimaryPane(tab.layout).content`，若 `content.kind === 'session'`，用 `content.sessionCode` 作為 key。

3. For **overlay style** (A): wrap icon in `icon-wrap` relative container, add `TabStatusDot` as absolute child:
```tsx
<span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
  {IconComponent && <IconComponent size={14} className="flex-shrink-0" />}
  <TabStatusDot status={agentStatus} style="overlay" isActive={isActive} />
</span>
```

4. For **replace style** (B): replace icon with dot when agent active:
```tsx
{agentStatus
  ? <TabStatusDot status={agentStatus} style="replace" isActive={isActive} />
  : IconComponent && <IconComponent size={14} className="flex-shrink-0" />
}
```

5. For **inline style** (C): render icon then dot:
```tsx
{IconComponent && <IconComponent size={14} className="flex-shrink-0" />}
<TabStatusDot status={agentStatus} style="inline" isActive={isActive} />
```

6. Add **unread red dot** (all styles, inactive tab only):
```tsx
{!isActive && isUnread && (
  <span className="absolute top-0.5 right-1 w-[5px] h-[5px] rounded-full"
    style={{ backgroundColor: '#b91c1c' }} />
)}
```

- [ ] **Step 7: Run SPA tests**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add spa/src/components/TabStatusDot.tsx spa/src/components/TabStatusDot.test.tsx \
  spa/src/components/SortableTab.tsx spa/src/index.css
git commit -m "feat: add tab status indicator with 3 style options"
```

---

## Task 8: SPA UI — Session Panel + StatusBar

**Files:**
- Modify: `spa/src/components/SessionPanel.tsx`
- Modify: `spa/src/components/SessionStatusBadge.tsx`
- Modify: `spa/src/components/StatusBar.tsx`

- [ ] **Step 1: Update SessionStatusBadge**

Replace the existing `SessionStatus` type and colors to use agent store status:

```typescript
// spa/src/components/SessionStatusBadge.tsx
import type { AgentStatus } from '../stores/useAgentStore'

const STATUS_COLORS: Record<AgentStatus, string> = {
  running: 'bg-green-400',
  waiting: 'bg-yellow-400',
  idle: 'bg-gray-500',
}

interface Props {
  status: AgentStatus | undefined
}

export default function SessionStatusBadge({ status }: Props) {
  if (!status) return null

  return (
    <span
      data-testid="status-badge"
      className={`inline-block w-2 h-2 rounded-full ${STATUS_COLORS[status] || 'bg-border-default'}`}
      title={status}
    />
  )
}
```

- [ ] **Step 2: Update SessionPanel.tsx**

Replace `sessionStatus` from stream store with `statuses` from agent store. Move badge to right side (before code):

```typescript
// Replace import:
import { useAgentStore } from '../stores/useAgentStore'

// In component:
const agentStatuses = useAgentStore((s) => s.statuses)

// In render, per session:
const status = agentStatuses[s.code]

// Move badge position: between name and code
<span className="flex-1 truncate">{s.name}</span>
{status && <SessionStatusBadge status={status} />}
<span className="text-xs text-text-muted">{s.mode}</span>
```

Remove `deriveStatus` and `mapStatus` helper functions (no longer needed).

- [ ] **Step 3: Update StatusBar.tsx**

Add agent name + version display when an agent is active:

```typescript
import { useAgentStore } from '../stores/useAgentStore'

// In component:
const agentEvent = useAgentStore((s) =>
  content.kind === 'session' ? s.events[content.sessionCode] : undefined
)

// Extract agent info from raw_event (CC-specific parsing in component)
const agentInfo = agentEvent?.raw_event
const agentName = agentInfo?.modelName as string | undefined

// In render, between session name and connection status:
{agentName && <span className="text-text-muted">{agentName}</span>}
```

- [ ] **Step 4: Run SPA tests**

Run: `cd spa && npx vitest run`
Expected: PASS (update any broken tests from SessionStatusBadge type change)

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/SessionPanel.tsx spa/src/components/SessionStatusBadge.tsx \
  spa/src/components/StatusBar.tsx
git commit -m "feat: update session panel + status bar with agent hook status"
```

---

## Task 9: Settings — Tab Indicator Style Option

**Files:**
- Modify: `spa/src/stores/useAgentStore.ts` (add style preference)
- Modify: Settings section component (add dropdown)

- [ ] **Step 1: Add `tabIndicatorStyle` to agent store**

```typescript
// Add to AgentState:
tabIndicatorStyle: TabIndicatorStyle

// Add action:
setTabIndicatorStyle: (style: TabIndicatorStyle) => void

// Default:
tabIndicatorStyle: 'overlay',

// With persist:
export const useAgentStore = create<AgentState>()(
  persist(
    (set, get) => ({
      // ... existing
      tabIndicatorStyle: 'overlay' as TabIndicatorStyle,
      setTabIndicatorStyle: (style) => set({ tabIndicatorStyle: style }),
    }),
    {
      name: 'tbox-agent',
      partialize: (state) => ({ tabIndicatorStyle: state.tabIndicatorStyle }),
    },
  ),
)
```

- [ ] **Step 2: Add setting to AppearanceSection**

Add a dropdown for tab indicator style in the Appearance settings section:

```typescript
// In AppearanceSection component:
const tabIndicatorStyle = useAgentStore((s) => s.tabIndicatorStyle)
const setTabIndicatorStyle = useAgentStore((s) => s.setTabIndicatorStyle)

<SettingItem
  label={t('settings.appearance.tab_indicator.label')}
  description={t('settings.appearance.tab_indicator.desc')}
>
  <select
    value={tabIndicatorStyle}
    onChange={(e) => setTabIndicatorStyle(e.target.value as TabIndicatorStyle)}
    className="..."
  >
    <option value="overlay">{t('settings.appearance.tab_indicator.overlay')}</option>
    <option value="replace">{t('settings.appearance.tab_indicator.replace')}</option>
    <option value="inline">{t('settings.appearance.tab_indicator.inline')}</option>
  </select>
</SettingItem>
```

- [ ] **Step 3: Add i18n keys**

Add to the default locale file the keys:
- `settings.appearance.tab_indicator.label`
- `settings.appearance.tab_indicator.desc`
- `settings.appearance.tab_indicator.overlay`
- `settings.appearance.tab_indicator.replace`
- `settings.appearance.tab_indicator.inline`

- [ ] **Step 4: Run SPA tests + lint**

Run: `cd spa && npx vitest run && pnpm run lint`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useAgentStore.ts spa/src/components/AppearanceSection.tsx \
  spa/src/locales/
git commit -m "feat: add tab indicator style setting (overlay/replace/inline)"
```

---

## Task 10: Integration Test — End-to-End Smoke

- [ ] **Step 1: Build daemon**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go build -o bin/tbox ./cmd/tbox/`
Expected: Build succeeds

- [ ] **Step 2: Test tbox hook manually**

```bash
echo '{"sessionId":"test-123","hook_event_name":"Stop"}' | bin/tbox hook Stop
```

Expected: Exits 0 (daemon may not be running — silent failure is correct)

- [ ] **Step 3: Test tbox setup**

```bash
bin/tbox setup
cat ~/.claude/settings.json | jq '.hooks.Stop'
```

Expected: Shows tbox hook entry with full path

- [ ] **Step 4: Build SPA**

Run: `cd spa && pnpm run build`
Expected: Build succeeds

- [ ] **Step 5: Run full test suites**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./... -count=1 && cd spa && npx vitest run && pnpm run lint`
Expected: All PASS

- [ ] **Step 6: Commit any remaining fixes**

```bash
git add -A
git commit -m "test: integration smoke test pass for agent hook status"
```
