package probe_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

// fastPoll shrinks the watch loop tick to keep tests well under the per-test
// deadline budget. 5ms is fast enough that a few hundred ticks fit in <2s.
const fastPoll = 5 * time.Millisecond

// scriptedExecutor is a probe-only fake tmux executor whose CapturePaneContent
// reads from a scripted call sequence. Lets us deterministically inject errors
// and content changes per call without relying on wall-clock timing.
type scriptedExecutor struct {
	*tmux.FakeExecutor

	mu     sync.Mutex
	target string
	script []scriptedCapture // consumed in order; last entry sticky
	calls  int32
}

type scriptedCapture struct {
	content string
	err     error
}

func newScripted(target string, script []scriptedCapture) *scriptedExecutor {
	return &scriptedExecutor{
		FakeExecutor: tmux.NewFakeExecutor(),
		target:       target,
		script:       script,
	}
}

func (s *scriptedExecutor) CapturePaneContent(target string, lastN int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	atomic.AddInt32(&s.calls, 1)
	if target != s.target {
		return "", errors.New("scripted: unexpected target")
	}
	if len(s.script) == 0 {
		return "", errors.New("scripted: empty script")
	}
	var step scriptedCapture
	if len(s.script) == 1 {
		step = s.script[0] // sticky
	} else {
		step = s.script[0]
		s.script = s.script[1:]
	}
	return step.content, step.err
}

func (s *scriptedExecutor) Calls() int {
	return int(atomic.LoadInt32(&s.calls))
}

// drainEvents waits up to timeout for the watcher to deliver `want` events.
// Returns the events received (possibly fewer than want on timeout).
func drainEvents(ch <-chan probe.ScreenChangeEvent, want int, timeout time.Duration) []probe.ScreenChangeEvent {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	out := make([]probe.ScreenChangeEvent, 0, want)
	for len(out) < want {
		select {
		case ev := <-ch:
			out = append(out, ev)
		case <-deadline.C:
			return out
		}
	}
	return out
}

// PR1 — every diff tick fires ScreenChanged; after threshold consecutive
// stable ticks ScreenStable fires (continuous-changing then settle).
func TestWatch_FiresChangedOnEveryDiff(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	// baseline + 5 distinct values + 4 identical (stable threshold = 3 by
	// default → fires on the 3rd identical tick after baseline=last-distinct).
	script := []scriptedCapture{
		{content: "v0"},
		{content: "v1"},
		{content: "v2"},
		{content: "v3"},
		{content: "v4"},
		{content: "v5"},
		// 4 stable ticks — threshold=3 → exactly one ScreenStable
		{content: "v5"},
		{content: "v5"},
		{content: "v5"},
		{content: "v5"},
	}
	exec := newScripted("sess:", script)
	p := probe.New(exec)

	ch := make(chan probe.ScreenChangeEvent, 16)
	p.Watch("sess:", probe.WatchOptions{}, func(ev probe.ScreenChangeEvent) { ch <- ev })
	t.Cleanup(func() { p.StopAllWatches() })

	events := drainEvents(ch, 6, 2*time.Second)
	p.StopWatch("sess:")

	var changed, stable int
	for _, ev := range events {
		switch ev.Kind {
		case probe.ScreenChanged:
			changed++
		case probe.ScreenStable:
			stable++
		}
	}
	if changed != 5 {
		t.Fatalf("ScreenChanged fired %d times, want 5 (one per diff tick)", changed)
	}
	if stable != 1 {
		t.Fatalf("ScreenStable fired %d times, want 1 (threshold reached once)", stable)
	}
}

// PR2 — continuous-stable: after baseline, ScreenStable fires every N ticks
// (counter resets after each fire).
func TestWatch_FiresStableEveryNTicksAfterThreshold(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	// 9 same captures → baseline + 8 stable ticks → 8/3 = 2 fires
	// (tick 3 reset, tick 6 reset, tick 7 not yet, tick 8 not yet).
	exec := newScripted("sess:", []scriptedCapture{{content: "same"}})
	p := probe.New(exec)

	ch := make(chan probe.ScreenChangeEvent, 16)
	p.Watch("sess:", probe.WatchOptions{IdleStableTicks: 3}, func(ev probe.ScreenChangeEvent) { ch <- ev })
	t.Cleanup(func() { p.StopAllWatches() })

	// Wait for at least 2 stable events. 8 ticks * 5ms = 40ms; pad to 1s.
	got := drainEvents(ch, 2, 2*time.Second)
	p.StopWatch("sess:")

	if len(got) != 2 {
		t.Fatalf("ScreenStable count = %d, want 2 (two threshold cycles)", len(got))
	}
	for i, ev := range got {
		if ev.Kind != probe.ScreenStable {
			t.Fatalf("event[%d].Kind = %q, want stable", i, ev.Kind)
		}
	}
}

// PR3 — watcher persists across changed → stable → changed cycles; both
// kinds fire per PR1/PR2 logic, watcher does not exit on callback.
func TestWatch_LoopContinuesAcrossTransitions(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	script := []scriptedCapture{
		{content: "a"}, // baseline
		{content: "b"}, // changed
		{content: "b"}, // stable 1
		{content: "b"}, // stable 2
		{content: "b"}, // stable 3 → fire ScreenStable (count reset)
		{content: "c"}, // changed
		{content: "c"}, // stable 1 (sticky after this — script length=1 sticks)
	}
	exec := newScripted("sess:", script)
	p := probe.New(exec)

	ch := make(chan probe.ScreenChangeEvent, 32)
	p.Watch("sess:", probe.WatchOptions{IdleStableTicks: 3}, func(ev probe.ScreenChangeEvent) { ch <- ev })
	t.Cleanup(func() { p.StopAllWatches() })

	events := drainEvents(ch, 3, 2*time.Second)
	if !p.HasWatcher("sess:") {
		t.Fatal("watcher exited prematurely; expected loop to persist after callback")
	}
	p.StopWatch("sess:")

	if len(events) < 3 {
		t.Fatalf("got %d events, want >= 3", len(events))
	}
	if events[0].Kind != probe.ScreenChanged {
		t.Fatalf("event[0].Kind = %q, want changed", events[0].Kind)
	}
	if events[1].Kind != probe.ScreenStable {
		t.Fatalf("event[1].Kind = %q, want stable", events[1].Kind)
	}
	if events[2].Kind != probe.ScreenChanged {
		t.Fatalf("event[2].Kind = %q, want changed", events[2].Kind)
	}
}

// PR4 — StopWatch cancels the loop; no further capture calls; HasWatcher false.
func TestWatch_StopWatch_CancelsLoop(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	exec := newScripted("sess:", []scriptedCapture{{content: "x"}})
	p := probe.New(exec)

	p.Watch("sess:", probe.WatchOptions{}, func(probe.ScreenChangeEvent) {})

	// Let a few ticks happen.
	time.Sleep(50 * time.Millisecond)
	if !p.HasWatcher("sess:") {
		t.Fatal("watcher should be registered before StopWatch")
	}
	p.StopWatch("sess:")
	if p.HasWatcher("sess:") {
		t.Fatal("HasWatcher should be false after StopWatch")
	}

	pre := exec.Calls()
	time.Sleep(50 * time.Millisecond)
	post := exec.Calls()
	if post > pre {
		t.Fatalf("capture calls grew after StopWatch: pre=%d post=%d", pre, post)
	}
}

// W6-6 R5 F7 (replaces R2/R4 F6 baseline-fail-self-cleanup contract):
// baseline capture failure is a probe-internal transient state, not a
// watcher lifecycle event. watchLoop must NOT exit, must NOT clear the
// map entry, and must NOT close WatchHandle.Done() when the immediate
// baseline capture fails — it should keep ticking and adopt the first
// successful capture as the baseline.
//
// Why the contract reframed: the earlier R2 self-cleanup + R4 F6
// `WatchHandle.Done()` lifecycle signal produced a tight rearm loop
// when capture-pane was persistently failing — the codex ScreenChange
// detector observed Done(), returned with zero emit, dispatcher !appliedAny
// teardown rearmed via applyStatus, the next Watch hit baseline-fail
// again, etc. R5 reverted both: baseline failure stays inside the probe
// (this test) and the detector lifecycle no longer observes Done().
func TestWatch_BaselineFailure_KeepsMapEntryAndHandle(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	// Repeated capture errors — never recovers within the test window.
	// Pins the worst-case path for the F7 contract: even with persistent
	// failure, the watcher does NOT exit, does NOT clear the entry, and
	// does NOT close Done().
	exec := newScripted("sess:", []scriptedCapture{
		{err: errors.New("boom")},
	})
	p := probe.New(exec)
	t.Cleanup(func() { p.StopAllWatches() })

	cbFired := atomic.Int32{}
	h := p.Watch("sess:", probe.WatchOptions{}, func(probe.ScreenChangeEvent) {
		cbFired.Add(1)
	})

	// Allow many poll ticks — the watcher must stay live the whole time
	// because every capture call is errorring.
	time.Sleep(80 * time.Millisecond)

	if !p.HasWatcher("sess:") {
		t.Error("HasWatcher = false after persistent baseline failure; expected map entry to persist")
	}

	select {
	case <-h.Done():
		t.Error("WatchHandle.Done() closed during persistent baseline failure; expected watcher to keep retrying")
	default:
		// expected
	}

	// No callback can fire because no successful capture ever happened.
	if got := cbFired.Load(); got != 0 {
		t.Errorf("callback fired %d times despite persistent capture failure, want 0", got)
	}

	// Tmux call count must have grown — proves the loop is actively
	// retrying, not parked.
	if got := exec.Calls(); got < 2 {
		t.Errorf("tmux capture call count = %d, want ≥ 2 (loop should keep retrying)", got)
	}

	// Stop explicitly — Done() must close on cancel teardown.
	p.StopWatch("sess:")
	select {
	case <-h.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("WatchHandle.Done() did not close after StopWatch")
	}
}

// W6-6 R5 F7: after a transient baseline-capture failure, the first
// successful capture is adopted as the rolling baseline (no
// ScreenChanged for that seeding tick). The next tick that diverges
// from that baseline produces ScreenChanged with the new content. This
// pins the symmetric mid-loop / baseline-establishment behavior.
func TestWatch_BaselineFailure_RetriesUntilFirstSuccessfulCapture(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	// err, err, baseline, changed (sticky).
	exec := newScripted("sess:", []scriptedCapture{
		{err: errors.New("boom1")},
		{err: errors.New("boom2")},
		{content: "baseline"},
		{content: "changed"},
	})
	p := probe.New(exec)
	t.Cleanup(func() { p.StopAllWatches() })

	ch := make(chan probe.ScreenChangeEvent, 8)
	p.Watch("sess:", probe.WatchOptions{}, func(ev probe.ScreenChangeEvent) {
		ch <- ev
	})

	// Expect the first event to be ScreenChanged with content "changed"
	// — proves the seeding tick did NOT emit (baseline established
	// silently) and the next tick produced the diff.
	select {
	case ev := <-ch:
		if ev.Kind != probe.ScreenChanged {
			t.Fatalf("first event kind = %q, want %q", ev.Kind, probe.ScreenChanged)
		}
		if ev.Content != "changed" {
			t.Fatalf("first event content = %q, want \"changed\" (seeding tick must not emit)", ev.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("no ScreenChanged event after baseline retry recovery")
	}

	// Watcher must still be live (no exit on transient capture errors).
	if !p.HasWatcher("sess:") {
		t.Error("HasWatcher = false; watcher should still be live after recovery")
	}
}

// W6-6 R5 F7: after a transient baseline failure, the watcher can still
// emit ScreenStable when the first successful capture is followed by
// IdleStableTicks identical captures. Pins that "false ScreenChanged
// from seeding" cannot happen.
func TestWatch_BaselineFailure_FirstSuccessCanEmitStable(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	// err then 5 identical "same" (sticky after that) → with
	// IdleStableTicks=3, after seeding (no fire) we get 3 identical
	// stable ticks → ScreenStable fires once.
	exec := newScripted("sess:", []scriptedCapture{
		{err: errors.New("boom")},
		{content: "same"},
	})
	p := probe.New(exec)
	t.Cleanup(func() { p.StopAllWatches() })

	ch := make(chan probe.ScreenChangeEvent, 8)
	p.Watch("sess:", probe.WatchOptions{IdleStableTicks: 3}, func(ev probe.ScreenChangeEvent) {
		ch <- ev
	})

	// First event must be ScreenStable (no false ScreenChanged from the
	// seeding tick).
	select {
	case ev := <-ch:
		if ev.Kind != probe.ScreenStable {
			t.Fatalf("first event kind = %q, want %q (no false ScreenChanged from seeding)", ev.Kind, probe.ScreenStable)
		}
		if ev.Content != "same" {
			t.Fatalf("first event content = %q, want \"same\"", ev.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("no ScreenStable event after baseline retry recovery")
	}
}

// W6-6 R5 F7: WatchHandle.Done() must NOT close while watchLoop is
// merely retrying the baseline. Done() only closes on real teardown:
// ctx cancel / StopWatch / StopWatchOwned / replacement / shutdown.
// Pin the contract by exercising a transient baseline error then an
// explicit StopWatchOwned — Done() closes only after the latter.
func TestWatch_BaselineFailure_DoesNotCloseHandleDoneUntilStopped(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	exec := newScripted("sess:", []scriptedCapture{
		{err: errors.New("boom")},
		{content: "baseline"},
	})
	p := probe.New(exec)
	t.Cleanup(func() { p.StopAllWatches() })

	h := p.Watch("sess:", probe.WatchOptions{}, func(probe.ScreenChangeEvent) {})

	// Allow several ticks — Done() must remain open across the baseline
	// retry window and into the post-recovery steady state.
	time.Sleep(80 * time.Millisecond)
	select {
	case <-h.Done():
		t.Fatal("Done() closed before any teardown happened")
	default:
		// expected
	}

	// Now request a real teardown via StopWatchOwned. Done() must close.
	if !p.StopWatchOwned(h) {
		t.Fatal("StopWatchOwned(h) = false, want true (h owns the live entry)")
	}
	select {
	case <-h.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("Done() did not close after StopWatchOwned cancel")
	}
}

// W6-6 R4 F6: zero-value WatchHandle.Done() returns nil — production
// callers that select on Done() must therefore tolerate a nil channel
// (which disables the select case rather than firing it). Test fakes
// across the codebase (probe_orchestrator_test.go fakeProber,
// codex/probe_intent_screen_change_test.go fakeWatcher,
// module/agent/probe_intent_dispatcher_codex_wire_test.go
// fakeScreenWatcher) all return WatchHandle{} from Watch; this pin
// ensures the contract stays compatible.
func TestWatchHandle_Zero_DoneIsNil(t *testing.T) {
	var zero probe.WatchHandle
	if ch := zero.Done(); ch != nil {
		t.Errorf("zero WatchHandle.Done() = %v, want nil (must disable select case in callers)", ch)
	}
}

// W6-6 R4 F6: StopWatchOwned-induced cleanup also closes Done().
// Pins the contract that ANY watchLoop exit (baseline fail / ctx
// cancel via StopWatchOwned / StopWatch / StopAllWatches / same-target
// replacement) yields a closed Done(). Detector teardown after a
// successful emit relies on this implicitly (watchLoop already ran
// through cleanup by the time the detector returns; selecting on
// Done() in that case observes a closed channel, not a leak).
func TestWatch_StopWatchOwned_ClosesHandleDone(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	exec := newScripted("sess:", []scriptedCapture{{content: "baseline"}})
	p := probe.New(exec)
	t.Cleanup(func() { p.StopAllWatches() })

	h := p.Watch("sess:", probe.WatchOptions{}, func(probe.ScreenChangeEvent) {})
	if !p.StopWatchOwned(h) {
		t.Fatal("StopWatchOwned(h) = false, want true (h owns the live entry)")
	}
	select {
	case <-h.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("WatchHandle.Done() did not close after StopWatchOwned cancel")
	}
}

// PR5 — TopLines path ignores changes outside top N; full-pane (opts={})
// path catches them.
func TestWatch_TopLinesIgnoresBottomChanges(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	fake := tmux.NewFakeExecutor()
	target := "sess:"

	// Top 3 lines stay constant; lines 3..4 will change.
	fake.SetPaneContentByRange(target, []string{"t1", "t2", "t3", "b1", "b2"})
	// Full-pane source (paneContents) will be flipped to drive the second
	// watcher's ScreenChanged.
	fake.SetPaneContent(target, "full-v1")

	p := probe.New(fake)

	// --- Phase A: TopLines=3, mutate only the bottom rows → no ScreenChanged.
	chTop := make(chan probe.ScreenChangeEvent, 4)
	p.Watch(target, probe.WatchOptions{TopLines: 3}, func(ev probe.ScreenChangeEvent) {
		if ev.Kind == probe.ScreenChanged {
			chTop <- ev
		}
	})
	time.Sleep(20 * time.Millisecond) // allow baseline
	fake.SetPaneContentByRange(target, []string{"t1", "t2", "t3", "B1-CHANGED", "B2-CHANGED"})
	select {
	case ev := <-chTop:
		t.Fatalf("TopLines watcher fired ScreenChanged for bottom-only change: %+v", ev)
	case <-time.After(120 * time.Millisecond):
		// expected — no fire
	}
	p.StopWatch(target)

	// --- Phase B: full-pane (opts={}) sees the same bottom change.
	// Set up a fresh full-pane baseline that we then flip.
	fake.SetPaneContent(target, "full-v1")
	chFull := make(chan probe.ScreenChangeEvent, 4)
	p.Watch(target, probe.WatchOptions{}, func(ev probe.ScreenChangeEvent) {
		if ev.Kind == probe.ScreenChanged {
			chFull <- ev
		}
	})
	t.Cleanup(func() { p.StopAllWatches() })
	time.Sleep(20 * time.Millisecond)
	fake.SetPaneContent(target, "full-v2") // change the full-pane source
	select {
	case <-chFull:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("full-pane watcher did not fire ScreenChanged")
	}
}

// PR5b — BottomLines path ignores top-only changes; TopLines path catches
// them. Confirms the two options route to distinct capture methods (R9 fix).
func TestWatch_BottomLinesIgnoresTopChanges(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	fake := tmux.NewFakeExecutor()
	target := "sess:"

	// BottomLines uses CapturePaneContent (paneContents map). Keep it stable.
	fake.SetPaneContent(target, "bottom-stable")
	// TopLines uses CapturePaneRange (paneContentByRange map). Will be flipped.
	fake.SetPaneContentByRange(target, []string{"top-v1", "row2", "row3"})

	p := probe.New(fake)

	// --- Phase A: BottomLines=3, mutate top-line source → no ScreenChanged.
	chBottom := make(chan probe.ScreenChangeEvent, 4)
	p.Watch(target, probe.WatchOptions{BottomLines: 3}, func(ev probe.ScreenChangeEvent) {
		if ev.Kind == probe.ScreenChanged {
			chBottom <- ev
		}
	})
	time.Sleep(20 * time.Millisecond) // baseline established
	fake.SetPaneContentByRange(target, []string{"TOP-CHANGED", "row2", "row3"})
	select {
	case ev := <-chBottom:
		t.Fatalf("BottomLines watcher fired ScreenChanged for top-only change: %+v", ev)
	case <-time.After(120 * time.Millisecond):
		// expected
	}
	p.StopWatch(target)

	// --- Phase B: TopLines=3 sees the change (paneContentByRange already mutated).
	chTop := make(chan probe.ScreenChangeEvent, 4)
	p.Watch(target, probe.WatchOptions{TopLines: 3}, func(ev probe.ScreenChangeEvent) {
		if ev.Kind == probe.ScreenChanged {
			chTop <- ev
		}
	})
	t.Cleanup(func() { p.StopAllWatches() })
	time.Sleep(20 * time.Millisecond) // baseline (top-changed already set)
	fake.SetPaneContentByRange(target, []string{"top-v3", "row2", "row3"})
	select {
	case <-chTop:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("TopLines watcher did not fire ScreenChanged on top-line change")
	}
}

// PR6 — mid-loop capture error skips the tick (no false ScreenChanged); the
// next successful capture restores the diff path.
func TestWatch_TmuxErrorTickSkipped(t *testing.T) {
	probe.SetWatchPollIntervalForTest(t, fastPoll)
	script := []scriptedCapture{
		{content: "baseline"},
		{err: errors.New("transient")},
		{content: "after-recovery"},
		{content: "after-recovery"}, // sticky
	}
	exec := newScripted("sess:", script)
	p := probe.New(exec)

	ch := make(chan probe.ScreenChangeEvent, 8)
	p.Watch("sess:", probe.WatchOptions{}, func(ev probe.ScreenChangeEvent) {
		if ev.Kind == probe.ScreenChanged {
			ch <- ev
		}
	})
	t.Cleanup(func() { p.StopAllWatches() })

	select {
	case ev := <-ch:
		if ev.Content != "after-recovery" {
			t.Fatalf("ScreenChanged fired with content %q, want after-recovery (no false event from error tick)", ev.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ScreenChanged did not fire after recovery tick")
	}
}

// W6-6 R2 F1: ownership-aware StopWatchOwned tests.
//
// The dispatcher detector teardown path can race a same-target Watch
// re-arm: detector1 main goroutine wakes from <-ctx.Done() and calls
// StopWatch(target) AFTER detector2 has already installed its own
// watcher for the same target via Watch (which internally cancels the
// previous entry and replaces it). The legacy StopWatch matches by
// target string only, so detector1's late teardown silently kills
// detector2's freshly-installed watcher.
//
// StopWatchOwned takes a WatchHandle (returned by Watch) that captures
// the watcher's identity token. The implementation only cancels +
// deletes the entry when entry.id == handle.id, so a stale handle
// cannot reach a replacement watcher.

// TestProber_StopWatchOwned_OnlyCancelsOwnEntry pins the canonical
// race: two consecutive Watch calls on the same target install two
// watchers (the second's existing.cancel() retires the first's
// watchLoop, then installs a fresh entry). StopWatchOwned with the
// FIRST handle must report false and leave the second watcher intact.
//
// Default poll interval (500ms) is intentional — the test never lets a
// tick fire, just exercises the watcher map state. Using fastPoll +
// SetWatchPollIntervalForTest leaves a t.Cleanup that races a still-
// running watchLoop goroutine reading watchPollInterval; the canonical
// pre-existing tests work around this by capturing pollInterval into
// a local before the goroutine starts, but the simpler fix here is to
// avoid the override entirely (the test doesn't depend on tick rate).
func TestProber_StopWatchOwned_OnlyCancelsOwnEntry(t *testing.T) {
	exec := tmux.NewFakeExecutor()
	exec.SetPaneContent("sess:", "baseline")
	p := probe.New(exec)
	t.Cleanup(func() { p.StopAllWatches() })

	h1 := p.Watch("sess:", probe.WatchOptions{}, func(probe.ScreenChangeEvent) {})
	h2 := p.Watch("sess:", probe.WatchOptions{}, func(probe.ScreenChangeEvent) {})

	if got := p.StopWatchOwned(h1); got {
		t.Errorf("StopWatchOwned(h1) = true, want false (h1 no longer owns the entry)")
	}
	if !p.HasWatcher("sess:") {
		t.Errorf("HasWatcher(sess:) = false, want true (h2 must still be active)")
	}
	if got := p.StopWatchOwned(h2); !got {
		t.Errorf("StopWatchOwned(h2) = false, want true (h2 owns the live entry)")
	}
	if p.HasWatcher("sess:") {
		t.Errorf("HasWatcher(sess:) = true, want false after StopWatchOwned(h2)")
	}
}

// TestProber_StopWatchOwned_NoEntry_ReturnsFalse pins the
// nothing-to-stop case: StopWatchOwned on a target that was never
// watched returns false (idempotent, no panic).
func TestProber_StopWatchOwned_NoEntry_ReturnsFalse(t *testing.T) {
	exec := tmux.NewFakeExecutor()
	p := probe.New(exec)

	// Construct a synthetic handle. Production code only obtains
	// handles from Watch; this test reaches for the zero value to
	// confirm no-watcher == false.
	var zero probe.WatchHandle
	if got := p.StopWatchOwned(zero); got {
		t.Errorf("StopWatchOwned(zero handle, no entry) = true, want false")
	}
}

// TestProber_StopWatchOwned_AfterStopWatch_ReturnsFalse pins the
// legacy-API interaction: StopWatch (target-only) clears the entry,
// so a later StopWatchOwned with a stale handle returns false even
// though it once owned the entry.
func TestProber_StopWatchOwned_AfterStopWatch_ReturnsFalse(t *testing.T) {
	exec := tmux.NewFakeExecutor()
	exec.SetPaneContent("sess:", "baseline")
	p := probe.New(exec)
	t.Cleanup(func() { p.StopAllWatches() })

	h := p.Watch("sess:", probe.WatchOptions{}, func(probe.ScreenChangeEvent) {})
	p.StopWatch("sess:")

	if got := p.StopWatchOwned(h); got {
		t.Errorf("StopWatchOwned(h) after StopWatch = true, want false")
	}
	if p.HasWatcher("sess:") {
		t.Errorf("HasWatcher(sess:) = true, want false after StopWatch")
	}
}
