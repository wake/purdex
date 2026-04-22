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
	minter, lookup := observation.NewTraceIDRegistry()
	arb := arbmode.NewManager("", string(arbmode.ModePassthrough))
	return Options{
		Minter:          minter,
		Lookup:          lookup,
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

// fakeLookup is a minimal TraceIDLookup fake: tests can seed entries, and it
// records Get calls so reconcile tests can assert lookup was consulted.
type fakeLookup struct {
	mu      sync.Mutex
	entries map[observation.TraceIDKey]string
	calls   int
}

func newFakeLookup() *fakeLookup {
	return &fakeLookup{entries: make(map[observation.TraceIDKey]string)}
}

func (f *fakeLookup) seed(sessionID string, generation int64, traceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[observation.TraceIDKey{SessionID: sessionID, Generation: generation}] = traceID
}

func (f *fakeLookup) Get(sessionID string, generation int64) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	id, ok := f.entries[observation.TraceIDKey{SessionID: sessionID, Generation: generation}]
	return id, ok
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
	// Pre-mint a trace_id so the reconcile stale-emit path has a valid
	// trace_id to stamp onto its synthesized records (C2 fix requires
	// reconcile to skip emission on lookup miss).
	opts.Minter.Mint("sess-t", 1)
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

// ----- C2: Reconcile stale trace must carry a valid trace_id --------------

// TestReconcile_StaleActor_NoTraceID_SkipsEmit verifies that when the
// TraceIDLookup has no entry for an actor's (session, generation), the
// reconcile emitStale closure skips the submission entirely. Submitting a
// record with an empty TraceID would fail validateLightsRow and roll back
// the TraceWriter's whole flush batch.
func TestReconcile_StaleActor_NoTraceID_SkipsEmit(t *testing.T) {
	cap := &captureTraceSubmitter{}
	opts := newArbOptionsForTest(cap)
	opts.Lookup = newFakeLookup() // empty — every Get misses
	opts.ReconcileEvery = 5 * time.Millisecond
	opts.StaleThreshold = -1 * time.Nanosecond
	arb := NewArbitrator(opts)

	// Seed an active actor so reconcile's stale scan finds something.
	key := observation.ActorKey{SessionID: "sess-miss", Generation: 1, ActorID: "a1"}
	arb.deps.frames.getOrCreateSession("sess-miss").Generation = 1
	arb.deps.frames.upsertActor(key, func(a *actorSummary) {
		a.LastActivity = time.Now()
	})

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	go func() {
		arb.Run(ctx)
		close(doneCh)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	<-doneCh

	if got := cap.count(); got != 0 {
		t.Fatalf("stale trace submissions on miss = %d, want 0 (empty TraceID would poison batch)", got)
	}
}

// TestReconcile_StaleActor_WithTraceID_EmitsValidTrace verifies that when the
// lookup returns a valid trace_id, reconcile emits a stale trace record and
// the record carries the looked-up TraceID (plus a generated SpanID).
func TestReconcile_StaleActor_WithTraceID_EmitsValidTrace(t *testing.T) {
	cap := &captureTraceSubmitter{}
	opts := newArbOptionsForTest(cap)
	lookup := newFakeLookup()
	lookup.seed("sess-hit", 1, "TRACE_FROM_LOOKUP")
	opts.Lookup = lookup
	opts.ReconcileEvery = 5 * time.Millisecond
	opts.StaleThreshold = -1 * time.Nanosecond
	arb := NewArbitrator(opts)

	key := observation.ActorKey{SessionID: "sess-hit", Generation: 1, ActorID: "a1"}
	arb.deps.frames.getOrCreateSession("sess-hit").Generation = 1
	arb.deps.frames.upsertActor(key, func(a *actorSummary) {
		a.LastActivity = time.Now()
	})

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	go func() {
		arb.Run(ctx)
		close(doneCh)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	<-doneCh

	if cap.count() < 1 {
		t.Fatalf("expected at least 1 stale trace; got 0")
	}
	cap.mu.Lock()
	first := cap.records[0]
	cap.mu.Unlock()
	if first.TraceID != "TRACE_FROM_LOOKUP" {
		t.Errorf("stale trace TraceID = %q, want TRACE_FROM_LOOKUP", first.TraceID)
	}
	if first.SpanID == "" {
		t.Error("stale trace SpanID must be non-empty (generated UUID)")
	}
	if first.SessionID != "sess-hit" {
		t.Errorf("stale trace SessionID = %q, want sess-hit", first.SessionID)
	}
	if first.ObservedGeneration != 1 {
		t.Errorf("stale trace ObservedGeneration = %d, want 1", first.ObservedGeneration)
	}
}

// ----- C3: Shutdown drains queued observations ----------------------------

// drainingSubmitter captures submissions so TestArbitrator_Run_ContextCancel_DrainsInCh
// can count how many observations reached apply before Run exited.
type drainingSubmitter struct {
	mu      sync.Mutex
	records []TraceRecord
	block   chan struct{} // if non-nil, first Submit blocks on it
}

func (d *drainingSubmitter) Submit(r TraceRecord) {
	d.mu.Lock()
	d.records = append(d.records, r)
	d.mu.Unlock()
}

func (d *drainingSubmitter) Shutdown(context.Context) error { return nil }

func (d *drainingSubmitter) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.records)
}

// TestArbitrator_Run_ContextCancel_DrainsInCh verifies that cancelling the
// Run context drains any observations already enqueued in inCh before the
// goroutine exits. Without this, Module.Stop would lose queued observations
// between ctx cancel and goroutine exit.
//
// The test pre-cancels the ctx BEFORE starting Run so the goroutine enters
// the ctx.Done branch on its first iteration. If Run lacks the drain logic,
// the 3 queued observations are lost and the submitter sees 0 records.
func TestArbitrator_Run_ContextCancel_DrainsInCh(t *testing.T) {
	sub := &drainingSubmitter{}
	opts := newArbOptionsForTest(sub)
	// Very large ReconcileEvery so the ticker cannot consume cycles.
	opts.ReconcileEvery = time.Hour
	opts.InChCap = 32
	arb := NewArbitrator(opts)

	now := time.Now()
	// Pre-seed 3 obs BEFORE Run so the channel has queued work immediately.
	for i := 0; i < 3; i++ {
		arb.InCh() <- makeSessionStart("sess-drain", int64(i+1), now)
	}

	// Pre-cancel before Run so the goroutine MUST take the drain path to
	// process the queued observations.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	doneCh := make(chan struct{})
	go func() {
		arb.Run(ctx)
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not exit within 500ms after cancel")
	}

	// Each SessionStart produces at least one trace (the boundary). All 3
	// must have been applied via the drain loop.
	if got := sub.count(); got < 3 {
		t.Fatalf("submissions = %d, want >= 3 (all queued observations must drain)", got)
	}
}
