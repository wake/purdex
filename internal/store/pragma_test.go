// internal/store/pragma_test.go
// Internal-package tests covering sqlite tuning pragmas (busy_timeout) and
// connection pool caps for the meta DB. The agent_event DB has equivalent
// coverage in agent_event_test.go.
package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestOpenMeta_BusyTimeoutApplied verifies that a file-backed MetaStore
// opens with PRAGMA busy_timeout=5000 — making transient write contention
// WAIT instead of return SQLITE_BUSY immediately.
func TestOpenMeta_BusyTimeoutApplied(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "meta.db")
	m, err := OpenMeta(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { m.Close() })

	var ms int
	if err := m.db.QueryRow("PRAGMA busy_timeout").Scan(&ms); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if ms != 5000 {
		t.Errorf("busy_timeout: want 5000, got %d", ms)
	}
}

// TestOpenMeta_PoolCapApplied verifies the file-backed meta DB pool cap is 2.
// Meta DB has lower write volume than agent_event so a smaller cap is fine.
func TestOpenMeta_PoolCapApplied(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "meta.db")
	m, err := OpenMeta(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { m.Close() })

	got := m.db.Stats().MaxOpenConnections
	if got != 2 {
		t.Errorf("MaxOpenConnections: want 2, got %d", got)
	}
}

// TestOpenMeta_BusyTimeoutWaitsNotAborts proves competing writers block-wait
// instead of returning SQLITE_BUSY on the meta DB.
func TestOpenMeta_BusyTimeoutWaitsNotAborts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "meta.db")
	m, err := OpenMeta(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { m.Close() })

	if err := m.SetMeta("$0", SessionMeta{Mode: "terminal"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	holdStarted := make(chan struct{})

	go func() {
		defer wg.Done()
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			t.Errorf("A: begin: %v", err)
			return
		}
		if _, err := tx.Exec("UPDATE session_meta SET mode = ? WHERE tmux_id = ?", "stream", "$0"); err != nil {
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

	go func() {
		defer wg.Done()
		<-holdStarted
		time.Sleep(20 * time.Millisecond)
		if err := m.SetMeta("$0", SessionMeta{Mode: "jsonl"}); err != nil {
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
