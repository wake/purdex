package arbitrator

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/arbmode"
	"github.com/wake/purdex/internal/module/agent/observation"
)

// captureTraceSubmitter is a minimal TraceSubmitter fake for Arbitrator tests
// (the apply_test.go fakeTraceSubmitter is package-local already, but to keep
// arbitrator_test.go self-contained and to exercise race-detector cleanliness
// for concurrent Submit calls, we define a mutex-guarded capture here).
type captureTraceSubmitter struct {
	mu      sync.Mutex
	records []TraceRecord
}

func (c *captureTraceSubmitter) Submit(r TraceRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r)
}

func (c *captureTraceSubmitter) Shutdown(ctx context.Context) error { return nil }

func (c *captureTraceSubmitter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}

func newArbOptionsForTest(submitter TraceSubmitter) Options {
	minter, _ := observation.NewTraceIDRegistry()
	arb := arbmode.NewManager("", string(arbmode.ModePassthrough))
	return Options{
		Minter:          minter,
		Arbmode:         arb,
		TraceWriter:     submitter,
		InChCap:         16,
		PendingDeadline: 200 * time.Millisecond,
		PerSessionCap:   8,
		PerEntryObsCap:  16,
		ReconcileEvery:  10 * time.Millisecond,
		HookStormWindow: 10 * time.Millisecond,
		HookStormCap:    50,
		StaleThreshold:  30 * time.Second,
		Now:             time.Now,
	}
}

// makeSessionStart is a small helper producing a hook SessionStart obs that
// passes the generation gate.
func makeSessionStart(sid string, gen int64, now time.Time) observation.Observation {
	return observation.Observation{
		TraceID:            "trace-" + sid,
		SessionID:          sid,
		ObservedGeneration: gen,
		SourceKind:         observation.SourceHook,
		Action:             "SessionStart",
		Phase:              observation.PhaseProposed,
		Proposal: observation.StateProposal{
			ActorKey: observation.ActorKey{SessionID: sid, Generation: gen, ActorID: "root"},
		},
		ObservedAt: now,
		Seq:        1,
	}
}

func TestArbitrator_NewWithDefaults_FieldsWiredCorrectly(t *testing.T) {
	cap := &captureTraceSubmitter{}
	arb := NewArbitrator(newArbOptionsForTest(cap))
	if arb == nil {
		t.Fatal("NewArbitrator returned nil")
	}
	if arb.deps == nil {
		t.Fatal("deps not wired")
	}
	if arb.deps.frames == nil || arb.deps.pending == nil || arb.deps.idem == nil ||
		arb.deps.stormGuard == nil || arb.deps.minter == nil || arb.deps.arbmode == nil ||
		arb.deps.traceSubmit == nil {
		t.Fatalf("deps missing: %+v", arb.deps)
	}
	if arb.reconciler == nil {
		t.Fatal("reconciler not wired")
	}
	if arb.inCh == nil {
		t.Fatal("inCh not wired")
	}
	if cap := len(arb.InCh()); cap != 0 {
		t.Fatalf("inCh length = %d, want 0 (empty)", cap)
	}
}

func TestArbitrator_Run_ConsumesInCh(t *testing.T) {
	cap := &captureTraceSubmitter{}
	opts := newArbOptionsForTest(cap)
	// Large ReconcileEvery so the ticker doesn't race the hook trace.
	opts.ReconcileEvery = 10 * time.Second
	arb := NewArbitrator(opts)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneCh := make(chan struct{})
	go func() {
		arb.Run(ctx)
		close(doneCh)
	}()

	now := time.Now()
	arb.InCh() <- makeSessionStart("sess-a", 1, now)

	// Poll for the boundary trace (emitted by applySessionStart).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if cap.count() >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := cap.count(); got < 1 {
		t.Fatalf("records = %d, want >= 1", got)
	}

	cancel()
	select {
	case <-doneCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not exit within 200ms of cancel")
	}
}

func TestArbitrator_Run_ReconcileTickerFires(t *testing.T) {
	cap := &captureTraceSubmitter{}
	opts := newArbOptionsForTest(cap)
	opts.ReconcileEvery = 5 * time.Millisecond
	// StaleThreshold=0 means every actor becomes stale immediately. Combined
	// with an actor present in frameState, reconcile will emit stale traces
	// on every tick.
	opts.StaleThreshold = -1 * time.Nanosecond
	arb := NewArbitrator(opts)

	// Seed one active actor so reconcile has something to iterate over.
	key := observation.ActorKey{SessionID: "sess-t", Generation: 1, ActorID: "a1"}
	arb.deps.frames.getOrCreateSession("sess-t").Generation = 1
	arb.deps.frames.upsertActor(key, func(a *actorSummary) {
		a.LastActivity = time.Now()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneCh := make(chan struct{})
	go func() {
		arb.Run(ctx)
		close(doneCh)
	}()

	time.Sleep(40 * time.Millisecond)
	cancel()
	<-doneCh

	got := cap.count()
	if got < 2 {
		t.Fatalf("reconcile stale traces = %d, want >= 2", got)
	}
}

func TestArbitrator_Run_ContextCancel_StopsGoroutine(t *testing.T) {
	cap := &captureTraceSubmitter{}
	arb := NewArbitrator(newArbOptionsForTest(cap))

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	go func() {
		arb.Run(ctx)
		close(doneCh)
	}()
	// Give Run a moment to spin up.
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-doneCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Run did not exit within 100ms of cancel")
	}

	// Goroutine count should return to baseline (±1 for the runtime's
	// asynchronous finalizer churn).
	time.Sleep(20 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+2 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestArbitrator_InCh_ReturnsSendOnlyChannel(t *testing.T) {
	cap := &captureTraceSubmitter{}
	arb := NewArbitrator(newArbOptionsForTest(cap))
	var _ chan<- observation.Observation = arb.InCh()
}

func TestArbitrator_SingleOwner_NoDataRace(t *testing.T) {
	cap := &captureTraceSubmitter{}
	opts := newArbOptionsForTest(cap)
	opts.InChCap = 1024
	opts.ReconcileEvery = 20 * time.Millisecond
	arb := NewArbitrator(opts)

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	go func() {
		arb.Run(ctx)
		close(doneCh)
	}()

	const senders = 8
	const perSender = 50
	var wg sync.WaitGroup
	wg.Add(senders)
	for s := 0; s < senders; s++ {
		go func(s int) {
			defer wg.Done()
			now := time.Now()
			for i := 0; i < perSender; i++ {
				arb.InCh() <- makeSessionStart("sess-race", int64(s*perSender+i+1), now)
			}
		}(s)
	}
	wg.Wait()

	// Give the run loop time to drain.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-doneCh
}
