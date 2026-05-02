// internal/agent/codex/probe_intent_screen_change_test.go
package codex

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

// fakeWatcher is a deterministic screenWatcher test double. Watch
// captures the registered callback so tests can fire ScreenStable /
// ScreenChanged events synchronously via the fire helper. StopWatchOwned
// records the call so tests can verify teardown.
//
// Per spec §6.1 / plan §1 P1-T3: the test does NOT exercise the real
// probe.Prober internals (poll loop / hash diff). Instead the fake
// drives the detector's switch statement directly, isolating Phase A
// → Phase B transitions and emit-once invariants from the watcher's
// timing.
//
// W6-6 R2 F1: Watch returns a probe.WatchHandle whose .target is
// recorded; StopWatchOwned records the handle, lets the test verify
// the teardown was scoped to the correct identity (a stale handle
// passes through as a no-op).
type fakeWatcher struct {
	mu        sync.Mutex
	cb        probe.ScreenChangeCallback
	target    string
	opts      probe.WatchOptions
	handles   []probe.WatchHandle
	stopCalls []probe.WatchHandle
}

func newFakeWatcher() *fakeWatcher {
	return &fakeWatcher{}
}

func (f *fakeWatcher) Watch(target string, opts probe.WatchOptions, cb probe.ScreenChangeCallback) probe.WatchHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cb = cb
	f.target = target
	f.opts = opts
	// WatchHandle has unexported fields so the fake cannot fabricate
	// a non-zero token. Returning the zero value is fine: the codex
	// detector treats handles as opaque, only ever round-trips them
	// to StopWatchOwned, so identity is preserved across the round
	// trip.
	return probe.WatchHandle{}
}

func (f *fakeWatcher) StopWatchOwned(h probe.WatchHandle) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls = append(f.stopCalls, h)
	// Production *probe.Prober returns true on a successful match.
	// For the fake the handle is always the zero value so we cannot
	// distinguish ownership; report true to satisfy the spec
	// "owner-cancelled" expectation that detector tests rely on.
	return true
}

// fire delivers a ScreenChangeEvent to the registered callback
// synchronously (mirrors probe.Prober's inline-callback contract).
func (f *fakeWatcher) fire(ev probe.ScreenChangeEvent) {
	f.mu.Lock()
	cb := f.cb
	f.mu.Unlock()
	if cb == nil {
		return
	}
	cb(ev)
}

// captureOpts returns the WatchOptions the detector passed when
// registering (used to verify TopLines = screenChangeTopLines).
func (f *fakeWatcher) captureOpts() probe.WatchOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opts
}

// stopCallCount returns the number of StopWatchOwned invocations.
func (f *fakeWatcher) stopCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stopCalls)
}

// runDetector launches StartScreenChangeDetector on a goroutine and
// returns a done channel that closes when the detector returns. Used
// to keep the main test goroutine free for fire / cancel orchestration
// while still verifying the detector's exit path.
func runDetector(
	ctx context.Context,
	fw screenWatcher,
	isAlive func() bool,
	paneID string,
	senderPID int,
	out chan agent.Signal,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		StartScreenChangeDetector(ctx, fw, isAlive, paneID, senderPID, out)
		close(done)
	}()
	return done
}

// waitFor polls pred until it returns true or the timeout elapses.
func waitForCondition(t *testing.T, deadline time.Duration, pred func() bool, msg string) {
	t.Helper()
	start := time.Now()
	for time.Since(start) < deadline {
		if pred() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waitForCondition timeout: %s", msg)
}

// Case 1 — Happy path: ScreenStable arms detector, ScreenChanged with
// alive=true emits exactly one Signal carrying the expected payload.
func TestStartScreenChangeDetector_Case1Happy_EmitsOnce(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := runDetector(ctx, fw, func() bool { return true }, "%5", 4242, out)

	// Wait until detector has registered the callback before firing.
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	// Spec §4.3: detector must register Watch with TopLines=10.
	if got := fw.captureOpts().TopLines; got != screenChangeTopLines {
		t.Errorf("WatchOptions.TopLines = %d, want %d", got, screenChangeTopLines)
	}

	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	select {
	case sig := <-out:
		if sig.Kind != agent.ProbeIntentKindScreenChange {
			t.Errorf("Kind = %q, want %q", sig.Kind, agent.ProbeIntentKindScreenChange)
		}
		if !sig.PaneAlive {
			t.Errorf("PaneAlive = false, want true")
		}
		if sig.PaneID != "%5" {
			t.Errorf("PaneID = %q, want %q", sig.PaneID, "%5")
		}
		if sig.SenderPID != 4242 {
			t.Errorf("SenderPID = %d, want 4242", sig.SenderPID)
		}
	case <-time.After(time.Second):
		t.Fatalf("detector did not emit a signal in time")
	}

	// Detector exits after emit (emittedCh closes → main select returns).
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("detector did not return after emit")
	}
	if fw.stopCallCount() != 1 {
		t.Errorf("StopWatch call count = %d, want 1 (post-emit teardown)", fw.stopCallCount())
	}
}

// Case 2 — armed=false: ScreenChanged events drop without emit until
// ctx cancel.
func TestStartScreenChangeDetector_Case2NoStable_AllDrop(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := runDetector(ctx, fw, func() bool { return true }, "%5", 4242, out)
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	for i := 0; i < 5; i++ {
		fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})
	}

	// No signal expected within a generous window.
	select {
	case sig := <-out:
		t.Fatalf("unexpected signal in armed=false phase: %+v", sig)
	case <-time.After(50 * time.Millisecond):
		// expected: nothing
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("detector did not return after ctx cancel")
	}
	if fw.stopCallCount() != 1 {
		t.Errorf("StopWatch call count = %d, want 1 (ctx-cancel teardown)", fw.stopCallCount())
	}
}

// Case 1 (identity false) — Phase B reached but isCodexAlive returns
// false on the outer check. Drop without emit; emitted stays false so
// a later recovery can still emit (per round 4 F1 retry-able).
func TestStartScreenChangeDetector_Case1IdentityFalse_Drop(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	aliveFalse := func() bool { return false }
	done := runDetector(ctx, fw, aliveFalse, "%5", 4242, out)
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	select {
	case sig := <-out:
		t.Fatalf("unexpected signal with isCodexAlive=false: %+v", sig)
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	cancel()
	<-done
}

// Case 1 (identity race) — outer isCodexAlive returns true, but the
// in-mutex second check returns false. The detector must NOT emit
// and emitted must stay false (retry-able).
func TestStartScreenChangeDetector_Case1IdentityRace_DropAtSecondCheck(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	// First call true (outer check), subsequent calls false (inner check).
	var calls atomic.Int32
	aliveFn := func() bool {
		return calls.Add(1) == 1
	}
	done := runDetector(ctx, fw, aliveFn, "%5", 4242, out)
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	select {
	case sig := <-out:
		t.Fatalf("unexpected signal on identity race: %+v", sig)
	case <-time.After(50 * time.Millisecond):
		// expected: in-mutex second check refused emit
	}

	if got := calls.Load(); got < 2 {
		t.Fatalf("isCodexAlive call count = %d, want ≥ 2 (outer + in-mutex second check)", got)
	}

	cancel()
	<-done
}

// Case 1 (multiple changes) — Phase B with multiple ScreenChanged
// events all alive=true. Mutex + emitted=true means only one Signal
// is sent; subsequent callbacks see emitted=true and return early.
func TestStartScreenChangeDetector_Case1MultipleChanges_EmitsOnlyOnce(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal, 4)
	ctx, cancel := context.WithCancel(context.Background())

	done := runDetector(ctx, fw, func() bool { return true }, "%5", 4242, out)
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	for i := 0; i < 4; i++ {
		fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})
	}

	// Drain expected single signal.
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatalf("detector did not emit a signal")
	}

	// Verify no second signal lands.
	select {
	case sig := <-out:
		t.Fatalf("got second signal — emit-once invariant broken: %+v", sig)
	case <-time.After(20 * time.Millisecond):
		// expected
	}

	cancel()
	<-done
}

// ScreenStable idempotent — multiple ScreenStable events keep armed=true
// without side effects; subsequent ScreenChanged still emits normally.
func TestStartScreenChangeDetector_ScreenStableIdempotent(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := runDetector(ctx, fw, func() bool { return true }, "%5", 4242, out)
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	for i := 0; i < 3; i++ {
		fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	}
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	select {
	case sig := <-out:
		if sig.Kind != agent.ProbeIntentKindScreenChange {
			t.Errorf("Kind = %q, want %q", sig.Kind, agent.ProbeIntentKindScreenChange)
		}
	case <-time.After(time.Second):
		t.Fatalf("detector did not emit after multiple ScreenStable")
	}

	cancel()
	<-done
}

// Retry after transient false — first ScreenChanged sees alive=false
// and drops; isCodexAlive recovers; second ScreenChanged emits. Pins
// the v5 round 4 F1 fix (sync.Once permanent fuse retired).
func TestStartScreenChangeDetector_RetryAfterTransientFalse_EmitsOnRecovery(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	var alive atomic.Bool
	aliveFn := func() bool { return alive.Load() }
	done := runDetector(ctx, fw, aliveFn, "%5", 4242, out)
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	// First ScreenChanged: alive=false → drop.
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	select {
	case sig := <-out:
		t.Fatalf("unexpected signal during transient false: %+v", sig)
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	// Recover identity; second ScreenChanged should emit.
	alive.Store(true)
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	select {
	case sig := <-out:
		if sig.Kind != agent.ProbeIntentKindScreenChange {
			t.Errorf("Kind = %q, want %q", sig.Kind, agent.ProbeIntentKindScreenChange)
		}
	case <-time.After(time.Second):
		t.Fatalf("detector did not emit after identity recovery")
	}

	cancel()
	<-done
}

// Ctx cancel before any ScreenStable — main goroutine takes the
// <-ctx.Done() arm, calls StopWatch, sets closed=true, returns.
func TestStartScreenChangeDetector_CtxCancelBeforeStable_TeardownClean(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := runDetector(ctx, fw, func() bool { return true }, "%5", 4242, out)
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("detector did not return after ctx cancel")
	}
	if fw.stopCallCount() != 1 {
		t.Errorf("StopWatchOwned call count = %d, want 1", fw.stopCallCount())
	}
	// W6-6 R2 F1: WatchHandle has unexported fields so the fake
	// cannot fabricate a non-zero token. The detector's contract is
	// to forward whatever handle Watch returned to StopWatchOwned —
	// identity preservation is verified by the production
	// TestProber_StopWatchOwned_OnlyCancelsOwnEntry test in the probe
	// package. Here we only assert the teardown happened (count==1).
}

// Ctx cancel during emit select — when ctx wins the in-mutex select
// race, emitted stays false (so a future restart can retry) and
// emittedCh stays open (so detector exits via the <-ctx.Done() arm,
// not <-emittedCh).
//
// The test forces ctx to win by:
//  1. Using an unbuffered out channel so `case out<-sig` blocks
//     forever until a reader pulls.
//  2. Cancelling ctx before firing ScreenChanged so the detector's
//     in-mutex select sees ctx.Done immediately ready while out has
//     no reader → ctx.Done() must win.
func TestStartScreenChangeDetector_CtxCancelDuringEmit_NoStore(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal) // unbuffered; nobody reads
	ctx, cancel := context.WithCancel(context.Background())

	done := runDetector(ctx, fw, func() bool { return true }, "%5", 4242, out)
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})

	// Cancel ctx first, then fire ScreenChanged. Detector's in-mutex
	// select now has ctx.Done ready and out blocked → ctx wins.
	cancel()
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	// Detector main goroutine takes <-ctx.Done() arm and exits.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("detector did not return after ctx cancel during emit")
	}

	// out must still be empty (emit was abandoned).
	select {
	case sig := <-out:
		t.Fatalf("unexpected signal — emit should have been abandoned: %+v", sig)
	default:
		// expected
	}
}

// Other ScreenChangeKind ignored — fire a fabricated kind value not
// matching ScreenStable / ScreenChanged. Detector switch falls through
// without state change; subsequent ScreenStable + ScreenChanged still
// works.
func TestStartScreenChangeDetector_OtherKindIgnored(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := runDetector(ctx, fw, func() bool { return true }, "%5", 4242, out)
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	// Unknown ScreenChangeKind: detector switch must ignore.
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChangeKind("bogus-kind")})

	select {
	case sig := <-out:
		t.Fatalf("unexpected signal on unknown kind: %+v", sig)
	case <-time.After(20 * time.Millisecond):
		// expected
	}

	// Sanity: subsequent normal flow still works.
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatalf("detector did not emit after unknown kind ignored")
	}

	cancel()
	<-done
}

// NoCloseRace (v6.1 round 6 P1 fix) — exercise the close-race path:
//
//	(a) cb enters mutex, passes closed/emitted/identity checks, takes
//	    the `out<-sig` send case (out has buffered capacity).
//	(b) Concurrently main goroutine sees emittedCh closed, runs
//	    StopWatch, blocks on mu.Lock waiting for cb to release.
//	(c) cb writes to out, sets emitted, closes emittedCh, releases mu.
//	(d) main wakes, sets closed=true, releases mu, returns.
//	(e) After detector returns, dispatcher wrap goroutine close(out).
//	    To prove no panic, we emulate the wrap by close(out) post-return.
//	(f) A late watcher tick fires cb again — cb enters mutex, sees
//	    closed=true, returns without entering the send select. No
//	    panic on the closed channel.
//
// The test runs under -race and is the explicit acceptance for
// spec §2.3 R15 + plan §1 P1-T3 #11.
func TestStartScreenChangeDetector_NoCloseRace(t *testing.T) {
	fw := newFakeWatcher()
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := runDetector(ctx, fw, func() bool { return true }, "%5", 4242, out)
	waitForCondition(t, time.Second, func() bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.cb != nil
	}, "callback registered")

	// Step 1: arm Phase B and emit normally so emittedCh closes →
	// detector main goroutine wakes and proceeds to StopWatch +
	// closed=true.
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	// Drain the emitted signal.
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatalf("detector did not emit")
	}

	// Detector should now return.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("detector did not return after emit")
	}

	// Emulate the dispatcher wrap goroutine: close(out) after detector
	// return. Any future cb invocation must NOT send on this channel.
	close(out)

	// Step 2: late watcher tick — fire one more ScreenChanged. The cb
	// should enter the mutex, observe closed=true, and return without
	// entering the send select. No panic on the closed channel.
	//
	// Note: we still fire even though prober StopWatch was called;
	// real watchLoop callbacks can race this. Detector cb is robust.
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	cancel()
}

// W6-6 R4 F6 — baseline-fail watchLoop exit path. The codex detector
// must observe `wh.Done()` so a transient capture-pane failure
// (tmux unresponsive, pane just closed) does NOT leave the detector
// parked forever. Without the Done() select arm the dispatcher would
// keep the ScreenChange intent armed with no live watcher and
// subsequent Waiting hooks would hit the case-4 lifecycle short-
// circuit, missing the approval ScreenChange.
//
// This test uses the REAL *probe.Prober wired to a tmux executor that
// errors CapturePaneTopLines on every call → watchLoop fails baseline
// → self-cleanup → close(done) → detector main goroutine wakes from
// `<-wh.Done()` → returns. The wrap goroutine then closes `out`.
//
// Why real prober (not fakeWatcher): the contract is the lifecycle
// signaling between probe.Prober and the codex detector. fakeWatcher
// returns WatchHandle{} whose Done() is nil (disabled select case)
// — that's correct production-API hygiene but it can't drive this
// path. Real prober is the cleanest fixture (cf. spec instruction
// "Test 2 option b").
type erroringTopLinesExecutor struct {
	*tmux.FakeExecutor
}

func (e *erroringTopLinesExecutor) CapturePaneTopLines(target string, n int) (string, error) {
	return "", errors.New("simulated tmux capture-pane failure")
}

func TestStartScreenChangeDetector_WatchDoneBeforeEmit_ReturnsAndStopsOwned(t *testing.T) {
	// Real prober wired to an executor whose CapturePaneTopLines errors
	// → baseline fail → watchLoop self-cleans + closes done.
	exec := &erroringTopLinesExecutor{FakeExecutor: tmux.NewFakeExecutor()}
	prober := probe.New(exec)

	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := runDetector(ctx, prober, func() bool { return true }, "%5", 4242, out)

	// Detector must return WITHOUT emit and WITHOUT external ctx cancel —
	// the only signal that wakes it is `<-wh.Done()`.
	select {
	case <-done:
		// expected: detector observed wh.Done() close and returned.
	case <-time.After(2 * time.Second):
		t.Fatal("detector did not return after baseline-fail watchLoop exit (Done() not observed)")
	}

	// out must remain empty — no signal was ever produced.
	select {
	case sig := <-out:
		t.Fatalf("unexpected signal from baseline-failed watcher: %+v", sig)
	default:
		// expected
	}

	// Watcher map must be cleared (watchLoop self-cleanup ran).
	if prober.HasWatcher("%5") {
		t.Error("HasWatcher still true after baseline-fail self-cleanup")
	}

	// External ctx was never cancelled — proves Done() (not ctx) was the
	// signal that woke the detector.
	select {
	case <-ctx.Done():
		t.Fatal("ctx unexpectedly cancelled — test fixture compromised")
	default:
		// expected: ctx still live
	}
}

// --- P1-T5: onScreenChange mapper tests (internal — exercises the
// unexported mapper directly, mirroring the onProcessDead pattern in
// probe_intent_process_dead_test.go).

// TestOnScreenChange_MatchKind_ReturnsRunning asserts the canonical
// W6-6 mapping: a Signal with Kind=ScreenChange returns StatusRunning.
func TestOnScreenChange_MatchKind_ReturnsRunning(t *testing.T) {
	got := onScreenChange(agent.Signal{
		Kind:      agent.ProbeIntentKindScreenChange,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 4242,
	})
	if got != agent.StatusRunning {
		t.Errorf("onScreenChange(Kind=ScreenChange) = %q, want %q", got, agent.StatusRunning)
	}
}

// TestOnScreenChange_NonScreenChangeKind_ReturnsEmpty asserts the
// defensive shape: a Signal carrying a different ProbeIntentKind
// (e.g. ProcessDead) returns "" — defends against dispatcher misuse
// if a future Kind ever shares the OnSignal slot.
func TestOnScreenChange_NonScreenChangeKind_ReturnsEmpty(t *testing.T) {
	got := onScreenChange(agent.Signal{
		Kind:      agent.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 4242,
	})
	if got != "" {
		t.Errorf("onScreenChange(Kind=ProcessDead) = %q, want \"\"", got)
	}
}

// TestOnScreenChange_NilOrZeroKind_ReturnsEmpty asserts the
// zero-value Kind (e.g. an uninitialized Signal struct) returns ""
// rather than spuriously mapping to Running.
func TestOnScreenChange_NilOrZeroKind_ReturnsEmpty(t *testing.T) {
	got := onScreenChange(agent.Signal{}) // Kind = "" (zero value)
	if got != "" {
		t.Errorf("onScreenChange(Kind=\"\") = %q, want \"\"", got)
	}
}
