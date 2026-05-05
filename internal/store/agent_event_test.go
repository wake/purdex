// internal/store/agent_event_test.go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	const ts int64 = 1700000000000000000
	if err := s.Set("my-project", "Stop", raw, "cc", ts); err != nil {
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
	if ev.AgentType != "cc" {
		t.Errorf("agent_type: want cc, got %s", ev.AgentType)
	}
	if ev.BroadcastTs != ts {
		t.Errorf("broadcast_ts: want %d, got %d", ts, ev.BroadcastTs)
	}
}

func TestAgentEventStore_AgentTypeDefault(t *testing.T) {
	s := openTestAgentEventStore(t)

	raw := json.RawMessage(`{}`)
	if err := s.Set("proj", "Stop", raw, "", 0); err != nil {
		t.Fatalf("set: %v", err)
	}

	ev, err := s.Get("proj")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ev.AgentType != "" {
		t.Errorf("agent_type: want empty, got %q", ev.AgentType)
	}
}

func TestAgentEventStore_Overwrite(t *testing.T) {
	s := openTestAgentEventStore(t)

	raw1 := json.RawMessage(`{"hook_event_name":"SessionStart"}`)
	raw2 := json.RawMessage(`{"hook_event_name":"Stop"}`)
	const ts2 int64 = 1700000000000000002
	s.Set("proj", "SessionStart", raw1, "cc", 1700000000000000001)
	s.Set("proj", "Stop", raw2, "codex", ts2)

	ev, _ := s.Get("proj")
	if ev.EventName != "Stop" {
		t.Errorf("want Stop after overwrite, got %s", ev.EventName)
	}
	if ev.AgentType != "codex" {
		t.Errorf("want codex after overwrite, got %s", ev.AgentType)
	}
	if ev.BroadcastTs != ts2 {
		t.Errorf("broadcast_ts: want %d, got %d", ts2, ev.BroadcastTs)
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
	const tsA int64 = 1700000000000000010
	s.Set("proj-a", "Stop", json.RawMessage(`{}`), "cc", tsA)
	s.Set("proj-b", "SessionStart", json.RawMessage(`{}`), "", 0)

	all, err := s.ListAll()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2, got %d", len(all))
	}
	// Verify agent_type and broadcast_ts are persisted in list
	for _, ev := range all {
		if ev.TmuxSession == "proj-a" {
			if ev.AgentType != "cc" {
				t.Errorf("proj-a agent_type: want cc, got %s", ev.AgentType)
			}
			if ev.BroadcastTs != tsA {
				t.Errorf("proj-a broadcast_ts: want %d, got %d", tsA, ev.BroadcastTs)
			}
		}
	}
}

func TestAgentEventStore_Delete(t *testing.T) {
	s := openTestAgentEventStore(t)
	s.Set("proj", "Stop", json.RawMessage(`{}`), "cc", 0)
	s.Delete("proj")

	ev, _ := s.Get("proj")
	if ev != nil {
		t.Error("expected nil after delete")
	}
}

// TestOpenAgentEvent_BusyTimeoutApplied verifies that a file-backed
// AgentEventStore opens with PRAGMA busy_timeout=500 — making transient
// write contention WAIT instead of return SQLITE_BUSY immediately.
func TestOpenAgentEvent_BusyTimeoutApplied(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	s, err := OpenAgentEvent(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	var ms int
	if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&ms); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if ms != 500 {
		t.Errorf("busy_timeout: want 500, got %d", ms)
	}
}

// TestOpenAgentEvent_PoolCapApplied verifies the file-backed agent DB pool
// cap is set to 4. The agent_event DB is shared across FramesStore and
// TraceStore via OpenAgentEvent's *sql.DB, so cap=4 governs all three.
func TestOpenAgentEvent_PoolCapApplied(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	s, err := OpenAgentEvent(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	got := s.db.Stats().MaxOpenConnections
	if got != 4 {
		t.Errorf("MaxOpenConnections: want 4, got %d", got)
	}
}

// TestOpenAgentEvent_BusyTimeoutWaitsNotAborts proves that competing writers
// on a file-backed DB block-wait via busy_timeout instead of returning
// SQLITE_BUSY. Goroutine A holds a write tx for ~200ms; goroutine B
// concurrently issues a write that would otherwise abort. Without
// busy_timeout, B fails fast with SQLITE_BUSY; with it, B waits and succeeds.
func TestOpenAgentEvent_BusyTimeoutWaitsNotAborts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	s, err := OpenAgentEvent(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// Seed a row so goroutine A's UPDATE has something to lock.
	if err := s.Set("locked-key", "Stop", json.RawMessage(`{}`), "cc", 1); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	holdStarted := make(chan struct{})

	// Goroutine A — hold a write tx for 200ms.
	go func() {
		defer wg.Done()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Errorf("A: begin: %v", err)
			return
		}
		// BEGIN IMMEDIATE-equivalent: do an UPDATE to acquire RESERVED lock.
		if _, err := tx.Exec("UPDATE agent_events SET event_name = ? WHERE tmux_session = ?", "A-write", "locked-key"); err != nil {
			_ = tx.Rollback()
			t.Errorf("A: update: %v", err)
			return
		}
		close(holdStarted)
		time.Sleep(200 * time.Millisecond)
		if err := tx.Commit(); err != nil {
			t.Errorf("A: commit: %v", err)
		}
	}()

	// Goroutine B — race a competing write while A holds the lock.
	go func() {
		defer wg.Done()
		<-holdStarted
		// Tiny extra sleep so A is definitely mid-tx.
		time.Sleep(20 * time.Millisecond)
		if err := s.Set("locked-key", "B-write", json.RawMessage(`{}`), "cc", 2); err != nil {
			if strings.Contains(err.Error(), "SQLITE_BUSY") {
				t.Errorf("B: aborted with SQLITE_BUSY — busy_timeout not effective: %v", err)
			} else {
				t.Errorf("B: unexpected error: %v", err)
			}
		}
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("test timed out waiting for goroutines: %v", ctx.Err())
	}
}

func TestAgentEventStore_TracesDefaults(t *testing.T) {
	s := openTestAgentEventStore(t)
	traces, err := s.Traces()
	if err != nil {
		t.Fatalf("traces: %v", err)
	}
	if traces.maxChains != 10000 {
		t.Fatalf("maxChains = %d, want 10000", traces.maxChains)
	}
	if traces.maxSteps != 100000 {
		t.Fatalf("maxSteps = %d, want 100000", traces.maxSteps)
	}
}

// TestOpenAgentEvent_AllPoolConnsHaveForeignKeysEnabled forces acquisition of
// every connection in the pool (cap=4) and asserts each one reports
// foreign_keys=1.
//
// Why: PRAGMA foreign_keys is per-connection in SQLite. A post-Open db.Exec
// only enables it on whichever single connection is checked out at that
// moment. The remaining pool connections are dialled later without FK enabled,
// so ON DELETE CASCADE silently does not fire for ~75 % of transactions.
// Moving the pragma into the DSN _pragma parameter causes the driver to apply
// it on every new connection at dial time, making the pool fully consistent.
func TestOpenAgentEvent_AllPoolConnsHaveForeignKeysEnabled(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenAgentEvent(filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	const n = 4
	conns := make([]*sql.Conn, n)
	for i := range conns {
		c, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn %d: %v", i, err)
		}
		conns[i] = c
	}
	for i, c := range conns {
		var fk int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("conn %d: query foreign_keys: %v", i, err)
		}
		if fk != 1 {
			t.Errorf("conn %d: foreign_keys = %d, want 1", i, fk)
		}
		_ = c.Close()
	}
}
