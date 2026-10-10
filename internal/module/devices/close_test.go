package devices

import (
	"context"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// The store is released by Close (after the HTTP server drained), not by Stop (#1033): a request that took the store
// before Stop still finds it open, while a request after Stop gets the module's not-ready answer. Mutation gate: Stop
// closes the store again → red.
func TestStopLeavesTheStoreOpenAndCloseReleasesIt(t *testing.T) {
	m := New()
	if err := m.Init(core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "adm_x"}})); err != nil {
		t.Fatal(err)
	}
	var _ core.Closer = m
	st := m.live() // a request already in flight holds this

	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.db.Ping(); err != nil {
		t.Fatalf("Stop closed the store while HTTP can still be serving: %v", err)
	}
	if m.live() != nil {
		t.Fatal("after Stop a new request must see the module as not ready")
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
