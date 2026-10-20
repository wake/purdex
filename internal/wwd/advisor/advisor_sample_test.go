package advisor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// sampleStub is a SampleRule whose verdict the test sets.
type sampleStub struct {
	stubRule
	calls atomic.Int32
}

func (r *sampleStub) EverySample() bool { return true }
func (r *sampleStub) Eval(in Input) (Verdict, error) {
	r.calls.Add(1)
	return r.stubRule.Eval(in)
}

// lockedBook is a Book that fails the test if two writes ever overlap: the evaluator's 30 s pass and the per-sample pass must never write
// alerts at the same time.
type lockedBook struct {
	t       *testing.T
	inside  atomic.Int32
	maxSeen atomic.Int32
	mu      sync.Mutex
	active  map[string]bool
	raised  atomic.Int32
}

func newLockedBook(t *testing.T) *lockedBook { return &lockedBook{t: t, active: map[string]bool{}} }

func (b *lockedBook) enter() func() {
	n := b.inside.Add(1)
	for {
		m := b.maxSeen.Load()
		if n <= m || b.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(200 * time.Microsecond) // long enough for an overlapping writer to show
	return func() { b.inside.Add(-1) }
}

func (b *lockedBook) Raise(a wire.Alert) error {
	defer b.enter()()
	b.mu.Lock()
	b.active[a.Key] = true
	b.mu.Unlock()
	b.raised.Add(1)
	return nil
}

func (b *lockedBook) Clear(key string) error {
	defer b.enter()()
	b.mu.Lock()
	delete(b.active, key)
	b.mu.Unlock()
	return nil
}

func (b *lockedBook) Active() (map[string]bool, error) {
	defer b.enter()()
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]bool{}
	for k := range b.active {
		out[k] = true
	}
	return out, nil
}

// PassSample judges only the rules that ask to be judged at every sample.
func TestEvaluator_PassSampleRunsOnlyTheSampleRules(t *testing.T) {
	b := newBook()
	each := &sampleStub{stubRule: stubRule{kind: wire.AlertPressureDanger, v: Verdict{Holds: true, Key: "pressure_danger", Level: wire.AlertPush}}}
	slow := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Holds: true, Key: "disk_low", Level: wire.AlertPush}}
	ev := NewEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, nil, slow, each)
	ev.PassSample()
	if _, ok := b.active["pressure_danger"]; !ok || len(b.active) != 1 {
		t.Fatalf("PassSample wrote %+v, want only pressure_danger", b.active)
	}
	ev.Pass() // the 30 s pass judges everything, the sample rule included
	if _, ok := b.active["disk_low"]; !ok || each.calls.Load() != 2 {
		t.Fatalf("Pass: active %+v, danger judged %d times", b.active, each.calls.Load())
	}
	// no sample rule at all: PassSample reads nothing
	var inputs atomic.Int32
	only := NewEvaluator(newBook(), func() (Input, bool) { inputs.Add(1); return Input{Now: t0}, true }, nil, slow)
	only.PassSample()
	if inputs.Load() != 0 {
		t.Error("PassSample with no sample rule still took an input")
	}
}

// A signal on the sample channel is judged at once, not at the next tick: the alert is raised well before the 30 s pass would have been.
func TestEvaluator_RunJudgesASampleSignalAtOnce(t *testing.T) {
	b := newLockedBook(t)
	each := &sampleStub{stubRule: stubRule{kind: wire.AlertPressureDanger, v: Verdict{Holds: true, Key: "pressure_danger", Level: wire.AlertPush}}}
	sig := make(chan struct{}, 1)
	ev := NewEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, nil, each).WithSampleSignal(sig)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ev.Run(ctx); close(done) }()
	start := time.Now()
	sig <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for b.raised.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if b.raised.Load() == 0 {
		t.Fatal("the sample signal did not raise the alert")
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("raised after %s: the signal waited for something", took)
	}
	cancel()
	<-done
}

// The 30 s pass and the per-sample pass share one goroutine: whatever the signals and ticks, the book never sees two writers at once.
// Run with -race.
func TestEvaluator_TickAndSampleSignalsNeverOverlap(t *testing.T) {
	b := newLockedBook(t)
	each := &sampleStub{stubRule: stubRule{kind: wire.AlertPressureDanger, v: Verdict{Holds: true, Key: "pressure_danger", Level: wire.AlertPush}}}
	slow := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Holds: true, Key: "disk_low", Level: wire.AlertPush}}
	sig := make(chan struct{}, 1)
	ev := NewEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, nil, slow, each).WithSampleSignal(sig).WithInterval(time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ev.Run(ctx); close(done) }()
	stop := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(stop) {
		select {
		case sig <- struct{}{}:
		default:
		}
		time.Sleep(100 * time.Microsecond)
	}
	cancel()
	<-done
	if max := b.maxSeen.Load(); max != 1 {
		t.Fatalf("the book saw %d writers at once", max)
	}
	if each.calls.Load() < 5 {
		t.Fatalf("the sample rule was judged only %d times", each.calls.Load())
	}
}

// Pass and PassSample called from two goroutines (a test, a future caller) are serialized too, not only the ones Run makes.
func TestEvaluator_PassAndPassSampleCalledTogetherDoNotOverlap(t *testing.T) {
	b := newLockedBook(t)
	each := &sampleStub{stubRule: stubRule{kind: wire.AlertPressureDanger, v: Verdict{Holds: true, Key: "pressure_danger", Level: wire.AlertPush}}}
	ev := NewEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, nil, each)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				ev.Pass()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				ev.PassSample()
			}
		}()
	}
	wg.Wait()
	if max := b.maxSeen.Load(); max != 1 {
		t.Fatalf("the book saw %d writers at once", max)
	}
}

// Clearing a retired alert needs no facts: a host whose sampler has nothing to hand out (no first sample yet, a source that keeps failing)
// still lets go of the alerts of rules it no longer judges.
func TestEvaluator_RetiredKeysAreClearedEvenWithNoInput(t *testing.T) {
	b := newBook()
	b.active["pressure_watch"] = wire.Alert{Key: "pressure_watch"}
	slow := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Holds: true, Key: "disk_low", Level: wire.AlertPush}}
	ev := NewEvaluator(b, func() (Input, bool) { return Input{}, false }, nil, slow).WithRetired("pressure_watch")
	ev.Pass()
	if _, ok := b.active["pressure_watch"]; ok {
		t.Fatal("a retired alert was left standing because there was no input")
	}
	if _, ok := b.active["disk_low"]; ok {
		t.Error("a rule was judged with no input")
	}
}

// A rule this host does not judge (watch_rules off, or in `off`) must not leave its alert standing: a standing alert of a new kind is what
// stops a rollback to a build that does not know it.
func TestEvaluator_RetiredKeysAreClearedAtEveryPass(t *testing.T) {
	b := newBook()
	b.active["pressure_watch"] = wire.Alert{Key: "pressure_watch"}
	b.active["swap_growth"] = wire.Alert{Key: "swap_growth"}
	b.active["disk_low"] = wire.Alert{Key: "disk_low"}
	slow := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Holds: true, Key: "disk_low", Level: wire.AlertPush}}
	ev := NewEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, nil, slow).WithRetired("pressure_watch", "swap_growth", "pressure_danger")
	ev.Pass()
	if _, ok := b.active["pressure_watch"]; ok {
		t.Error("a retired alert was left standing")
	}
	if _, ok := b.active["swap_growth"]; ok {
		t.Error("a retired alert was left standing")
	}
	if _, ok := b.active["disk_low"]; !ok {
		t.Error("an alert that is not retired was cleared")
	}
	if b.cleared != 2 {
		t.Errorf("cleared %d, want 2 (pressure_danger was not active: nothing to clear)", b.cleared)
	}
	// when the table cannot be read nothing is cleared either
	b.active["pressure_watch"] = wire.Alert{Key: "pressure_watch"}
	b.activeErr = errors.New("table unreadable")
	ev.Pass()
	if _, ok := b.active["pressure_watch"]; !ok {
		t.Error("a retired alert was cleared without reading the table")
	}
}
