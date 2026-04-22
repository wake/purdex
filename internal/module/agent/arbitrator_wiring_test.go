package agent

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent/observation"
	"github.com/wake/purdex/internal/store"
)

// newWiringTestCore returns a Core wired with a minimal Config so Init's
// c.CfgMu read doesn't panic. The Core intentionally omits the session
// provider so Init stops after the degraded-path Arbitrator wiring — exactly
// what these tests want to cover.
func newWiringTestCore(t *testing.T) *core.Core {
	t.Helper()
	cfg := &config.Config{}
	return core.New(core.CoreDeps{Config: cfg})
}

// TestModule_Init_BuildsArbitrator_WhenTracesExist confirms that when the
// Module is constructed with a real (in-memory) AgentEventStore, Init wires
// up the Arbitrator + TraceWriter and the trace-id registry.
func TestModule_Init_BuildsArbitrator_WhenTracesExist(t *testing.T) {
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open agent event store: %v", err)
	}
	t.Cleanup(func() { events.Close() })

	m := New(events)
	c := newWiringTestCore(t)
	if err := m.Init(c); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.arbitrator == nil {
		t.Fatalf("expected arbitrator to be wired when traces store is present")
	}
	if m.traceWriter == nil {
		t.Fatalf("expected traceWriter to be wired when traces store is present")
	}
	if m.traceMinter == nil || m.traceLookup == nil {
		t.Fatalf("expected trace-id minter + lookup to be wired")
	}
}

// TestModule_Init_NoArbitrator_WhenTracesNil confirms the degraded path:
// New(nil) → traces store missing → Init skips Arbitrator construction.
func TestModule_Init_NoArbitrator_WhenTracesNil(t *testing.T) {
	m := New(nil)
	c := newWiringTestCore(t)
	if err := m.Init(c); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.arbitrator != nil {
		t.Fatalf("expected arbitrator to be nil when traces store is missing, got %+v", m.arbitrator)
	}
	if m.traceWriter != nil {
		t.Fatalf("expected traceWriter to be nil when traces store is missing")
	}
	// Minter/Lookup are still constructed even in the degraded path so future
	// dual-write consumers can tag outbound events (PR-1b-1c).
	if m.traceMinter == nil || m.traceLookup == nil {
		t.Fatalf("expected trace-id registry to be wired even in degraded path")
	}
	// SubmitObservation must be a no-op and must not panic.
	m.SubmitObservation(observation.Observation{
		SessionID: "sess", Phase: observation.PhaseCommitted,
	})
}

// TestModule_StartStop_StartsAndCancelsArbitratorGoroutine is the lifecycle
// smoke test. It uses the real Start/Stop codepath but bypasses replay/sweep
// (which require a real session provider) by invoking the arbitrator wiring
// subset directly — mirroring Start's implementation.
func TestModule_StartStop_StartsAndCancelsArbitratorGoroutine(t *testing.T) {
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open agent event store: %v", err)
	}
	t.Cleanup(func() { events.Close() })

	m := New(events)
	c := newWiringTestCore(t)
	if err := m.Init(c); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.arbitrator == nil {
		t.Fatalf("arbitrator not wired")
	}

	// Mirror Start's arbitrator-only wiring (avoid sweep/replay which need the
	// session provider).
	ctx, cancel := context.WithCancel(context.Background())
	m.arbCancel = cancel
	m.arbDone = make(chan struct{})
	m.traceWriter.Start(ctx)
	go func() {
		defer close(m.arbDone)
		m.arbitrator.Run(ctx)
	}()

	before := runtime.NumGoroutine()
	// Submit a committed SessionStart observation — must drain without blocking.
	obs := observation.Observation{
		SessionID:          "sess-wiring",
		ObservedGeneration: 1,
		SourceKind:         observation.SourceHook,
		Action:             "SessionStart",
		Phase:              observation.PhaseCommitted,
		ObservedAt: time.Now(),
	}
	m.SubmitObservation(obs)

	// Give the goroutine a moment to consume.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(m.arbitrator.InChForTesting()) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if l := len(m.arbitrator.InChForTesting()); l != 0 {
		t.Fatalf("arbitrator did not consume observation; channel len=%d", l)
	}

	// Stop the module — should cancel ctx, join the goroutine, shutdown trace
	// writer, and return cleanly.
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop, the goroutine should have exited. Allow goroutine-count
	// slack for test framework overhead; just ensure the number did not grow
	// (i.e. our goroutine leaked) by more than a handful.
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+2 {
		t.Fatalf("goroutine count grew after Stop: before=%d after=%d", before, after)
	}

	// arbCancel is reset so a second Stop is safe.
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestModule_SubmitObservation_SessionStartMintsTraceID verifies that
// SessionStart observations travel through the apply pipeline and mint a
// trace-id via the shared registry.
func TestModule_SubmitObservation_SessionStartMintsTraceID(t *testing.T) {
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open agent event store: %v", err)
	}
	t.Cleanup(func() { events.Close() })

	m := New(events)
	c := newWiringTestCore(t)
	if err := m.Init(c); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.arbCancel = cancel
	m.arbDone = make(chan struct{})
	m.traceWriter.Start(ctx)
	go func() {
		defer close(m.arbDone)
		m.arbitrator.Run(ctx)
	}()
	t.Cleanup(func() { _ = m.Stop(context.Background()) })

	sid := "sess-mint"
	const gen int64 = 7
	obs := observation.Observation{
		SessionID:          sid,
		ObservedGeneration: gen,
		SourceKind:         observation.SourceHook,
		Action:             "SessionStart",
		Phase:              observation.PhaseCommitted,
		ObservedAt: time.Now(),
	}
	m.SubmitObservation(obs)

	// Poll for mint — apply runs asynchronously.
	deadline := time.Now().Add(500 * time.Millisecond)
	var gotID string
	var ok bool
	for time.Now().Before(deadline) {
		gotID, ok = m.traceLookup.Get(sid, gen)
		if ok && gotID != "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("SessionStart did not mint trace_id for (%q, %d); got=%q ok=%v", sid, gen, gotID, ok)
}
