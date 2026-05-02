package codexbroker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestModule_Name verifies the module's stable name. Other modules look this
// up via Dependencies() so it must be deterministic.
func TestModule_Name(t *testing.T) {
	m := New()
	if got := m.Name(); got != "codexbroker" {
		t.Errorf("Name() = %q, want %q", got, "codexbroker")
	}
}

// TestModule_Dependencies should be empty: P1 inventory is read-only and
// stand-alone (no monitor/session dependency).
func TestModule_Dependencies(t *testing.T) {
	m := New()
	if deps := m.Dependencies(); len(deps) != 0 {
		t.Errorf("Dependencies() = %v, want []", deps)
	}
}

// TestModule_Init_NoError ensures Init runs without panic against a nil-fielded
// Core. The module should not require any registry lookup or shared service.
func TestModule_Init_NoError(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Errorf("Init(nil) error = %v, want nil", err)
	}
}

// TestModule_StartStop_NoOp verifies Start/Stop are no-ops in P1 (no
// goroutines, no background work).
func TestModule_StartStop_NoOp(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ctx := context.Background()
	if err := m.Start(ctx); err != nil {
		t.Errorf("Start error = %v", err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Errorf("Stop error = %v", err)
	}
}

// TestModule_RegistersRoute confirms RegisterRoutes installs the
// GET /api/codex/brokers handler on the supplied mux. Verified by issuing a
// real request through the mux.
func TestModule_RegistersRoute(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	// Probe with a GET request: even if the handler can't read real ps it must
	// produce *some* HTTP response (i.e. the route is registered, not 404).
	req := httptest.NewRequest(http.MethodGet, "/api/codex/brokers", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Errorf("GET /api/codex/brokers returned 404 — route not registered")
	}
}

// TestModule_RegistersOnlyGet ensures the registered route is method-scoped to
// GET. POSTing to the path should fall through to the mux's default 405 / 404
// behaviour rather than handing the request to HandleBrokers' own 405 path.
//
// (Net effect: clients see method-not-allowed semantics either way.)
func TestModule_RegistersOnlyGet(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/codex/brokers", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("POST /api/codex/brokers returned 200 — route should not accept POST")
	}
}

// TestModule_RegistersSweepRoute — task R: after Init + RegisterRoutes, the
// mux has both GET /api/codex/brokers and POST /api/codex/brokers/sweep.
func TestModule_RegistersSweepRoute(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/codex/brokers/sweep", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Errorf("POST /api/codex/brokers/sweep returned 404 — sweep route not registered")
	}
}

// TestModule_Init_QuarantineMissing_OK — no quarantine.json on disk → init
// still succeeds with an empty quarantine file.
func TestModule_Init_QuarantineMissing_OK(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.quarantine == nil {
		t.Errorf("expected non-nil quarantine after Init")
	}
	if len(m.quarantine.Entries) != 0 {
		t.Errorf("expected empty quarantine, got %d entries", len(m.quarantine.Entries))
	}
}

// TestModule_Init_E1TrackerEmpty — fresh Init produces an E1Tracker whose
// Snapshot() returns an empty map.
func TestModule_Init_E1TrackerEmpty(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.e1Tracker == nil {
		t.Fatalf("expected non-nil E1Tracker")
	}
	if got := m.e1Tracker.Snapshot(); len(got) != 0 {
		t.Errorf("expected empty E1Tracker snapshot, got %v", got)
	}
}

// TestModule_Init_LaunchRegistryMissing_Empty — no launch-registry.json on
// disk → Empty() reports true (P2 steady state on mlab).
func TestModule_Init_LaunchRegistryMissing_Empty(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.launchRegistry == nil {
		t.Fatalf("expected non-nil launchRegistry")
	}
	if !m.launchRegistry.Empty() {
		t.Errorf("expected empty launch registry on missing file")
	}
}
