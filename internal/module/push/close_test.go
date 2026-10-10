package push

import (
	"testing"

	"github.com/wake/purdex/internal/core"
)

// The store is released by Close (after the HTTP server drained), not by Stop (#1033): a device registration that
// arrives while the server drains still lands. Mutation gate: Stop closes the store again → red.
func TestStopLeavesTheStoreOpenAndCloseReleasesIt(t *testing.T) {
	e := newEnv(t)
	var _ core.Closer = e.mod
	st := e.mod.store

	must(t, e.mod.Stop(nil))
	if err := st.db.Ping(); err != nil {
		t.Fatalf("Stop closed the store while HTTP can still be serving: %v", err)
	}
	if rec := e.do("POST", "/api/push/devices", reqBody(tokA, nil)); rec.Code >= 500 {
		t.Fatalf("a registration after Stop = %d %s", rec.Code, rec.Body)
	}

	must(t, e.mod.Close())
	if err := st.db.Ping(); err == nil {
		t.Fatal("Close must release the store")
	}
	must(t, e.mod.Close()) // idempotent
}
