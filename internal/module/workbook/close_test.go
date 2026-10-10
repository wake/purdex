package workbook

import (
	"context"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// The store is released by Close (after the HTTP server drained), not by Stop (#1033): a request that took the store
// before Stop still finds it open. Mutation gate: Stop closes the store again → red.
func TestStopLeavesTheStoreOpenAndCloseReleasesIt(t *testing.T) {
	m := New()
	if err := m.Init(core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})); err != nil {
		t.Fatal(err)
	}
	var _ core.Closer = m
	m.mu.Lock()
	st := m.store // a request already in flight holds this
	m.mu.Unlock()
	if st == nil {
		t.Fatal("the store did not open")
	}

	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.db.Ping(); err != nil {
		t.Fatalf("Stop closed the store while HTTP can still be serving: %v", err)
	}
	if m.Status()["ready"] != false {
		t.Fatal("after Stop the module must report not ready")
	}

	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.db.Ping(); err == nil {
		t.Fatal("Close must release the store")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close is idempotent: %v", err)
	}
}
