package codexbroker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
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

// TestModule_Init_WiresDialerAndPanes — PR review finding B: production
// SweepHandler must have non-nil Dialer + Panes in BaseDecisionOpts so
// EvalPredicateA + EvalPredicateC degrade ONLY on real RPC down / pane
// missing, not on a wiring miss. The previous code left both nil, which
// silently pushed every broker toward baseline Kill=true.
func TestModule_Init_WiresDialerAndPanes(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.sweepHandler == nil {
		t.Fatalf("expected non-nil sweepHandler")
	}
	if m.sweepHandler.BaseDecisionOpts.Dialer == nil {
		t.Errorf("BaseDecisionOpts.Dialer is nil — predicate A would always degrade to false")
	}
	if m.sweepHandler.BaseDecisionOpts.Panes == nil {
		t.Errorf("BaseDecisionOpts.Panes is nil — predicate C would always degrade to false")
	}
}

// TestModule_BuildKillSequence_HasDialer — finding B: the per-broker
// KillSequence factory must populate Dialer so Step 2 (graceful RPC
// shutdown) actually runs. Previously left nil → Step 2 silently skipped
// → operator-issued kills always reached SIGTERM/SIGKILL, missing the
// graceful path the spec mandates.
func TestModule_BuildKillSequence_HasDialer(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	runner := m.buildKillSequence(BrokerRecord{Key: "k1"})
	ks, ok := runner.(*KillSequence)
	if !ok {
		t.Fatalf("expected *KillSequence, got %T", runner)
	}
	if ks.Dialer == nil {
		t.Errorf("KillSequence.Dialer is nil — Step 2 graceful would be silently skipped")
	}
}

// shortTempDir mirrors t.TempDir but uses /tmp/<prefix><random> to keep the
// total sockaddr_un length under macOS's 104-char limit. Caller is
// responsible for removeAllSilent on cleanup.
func shortTempDir(prefix string) (string, error) {
	return os.MkdirTemp("/tmp", prefix)
}

func removeAllSilent(p string) error { return os.RemoveAll(p) }

// startFakeBrokerSocket spins up a local unix listener that responds to a
// single POST /thread/list request with the supplied JSON body. Used to
// exercise the production realDialer wiring without any real broker.
func startFakeBrokerSocket(t *testing.T, sockPath, jsonBody string) net.Listener {
	t.Helper()
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				// Drain the request line + headers.
				buf := make([]byte, 1024)
				_, _ = c.Read(buf)
				resp := fmt.Sprintf(
					"HTTP/1.1 200 OK\r\nContent-Length: %d\r\nContent-Type: application/json\r\n\r\n%s",
					len(jsonBody), jsonBody,
				)
				_, _ = c.Write([]byte(resp))
			}(conn)
		}
	}()
	return listener
}

// TestEvalDecision_Production_ActiveRPCThread_NoKill — finding B end-to-end:
// using the production realDialer + realPaneChecker via Module.Init, an
// active RPC thread observed on the broker socket → predicate A=true →
// baseline Kill=false. Spawns a real unix listener that returns the broker
// /jobs JSON shape so EvalPredicateA classifies as active.
//
// NOTE: This test does NOT use the full HandleSweep path; it pulls
// BaseDecisionOpts straight off the Module-wired SweepHandler and feeds
// EvalDecision directly. That isolates the wiring-correctness invariant
// from sweep concurrency / quarantine machinery (which is covered
// separately by sweep_test.go and the kill tests above).
func TestEvalDecision_Production_ActiveRPCThread_NoKill(t *testing.T) {
	m := New()
	if err := m.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	opts := m.sweepHandler.BaseDecisionOpts
	if opts.Dialer == nil {
		t.Fatalf("BaseDecisionOpts.Dialer nil — wiring regression")
	}
	// Spin up a fake unix listener that returns the broker /jobs response
	// shape with one running thread; EvalPredicateA should classify A=true.
	// macOS has a ~104-char unix sockaddr_un.sun_path limit; t.TempDir()
	// often exceeds it under TestEvalDecision_*. Use /tmp directly.
	sockDir, err := shortTempDir("modtest-")
	if err != nil {
		t.Fatalf("shortTempDir: %v", err)
	}
	t.Cleanup(func() { _ = removeAllSilent(sockDir) })
	sockPath := sockDir + "/b.sock"
	listener := startFakeBrokerSocket(t, sockPath, `{"threads":[{"status":"running"}]}`)
	t.Cleanup(func() { listener.Close() })

	rec := BrokerRecord{
		Key:      "active",
		PID:      999999, // arbitrary; A is RPC-driven, not lister-driven
		Lstart:   time.Now(),
		Endpoint: "unix:" + sockPath,
		Cwd:      sockDir,
	}
	dec := EvalDecision(context.Background(), rec, opts)
	if dec.Kill {
		t.Errorf("expected Kill=false on active RPC thread; got Kill=true (A=%v ADetail=%q)",
			dec.Predicates.A, dec.Predicates.ADetail)
	}
	if !dec.Predicates.A {
		t.Errorf("expected predicate A=true with active thread on production dialer; got A=false ADetail=%q", dec.Predicates.ADetail)
	}
}
