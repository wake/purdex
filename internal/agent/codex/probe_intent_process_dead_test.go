// internal/agent/codex/probe_intent_process_dead_test.go
package codex

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
)

// fakePaneLister is a deterministic tmuxPaneLister test double. Tests register
// the panes they want HasPane to recognise and then assert call counts via
// HasPaneCalls.
type fakePaneLister struct {
	mu       sync.Mutex
	panes    map[string]bool
	calls    int
	callLog  []string
	override func(string) bool
}

func newFakePaneLister(panes ...string) *fakePaneLister {
	m := make(map[string]bool, len(panes))
	for _, p := range panes {
		m[p] = true
	}
	return &fakePaneLister{panes: m}
}

func (f *fakePaneLister) HasPane(paneID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.callLog = append(f.callLog, paneID)
	if f.override != nil {
		return f.override(paneID)
	}
	return f.panes[paneID]
}

func (f *fakePaneLister) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakePaneLister) CallLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.callLog))
	copy(out, f.callLog)
	return out
}

// withFastPoll temporarily lowers processDeadPollInterval for the duration of
// a test. Restored via t.Cleanup.
func withFastPoll(t *testing.T, d time.Duration) {
	t.Helper()
	t.Cleanup(SetProcessDeadPollIntervalForTest(d))
}

// withPidAlive overrides isPidAliveFn for the duration of a test, restoring
// the production probe.IsPidAlive implementation in cleanup.
func withPidAlive(t *testing.T, fn func(int) bool) {
	t.Helper()
	t.Cleanup(SetIsPidAliveFnForTest(fn))
}

// TestStartProcessDeadDetector_BothAlive_KeepsPolling verifies that when both
// the pid is alive and the pane exists the detector keeps polling without
// emitting a Signal. After two ticks we cancel ctx and check the channel is
// empty (within a short grace window).
func TestStartProcessDeadDetector_BothAlive_KeepsPolling(t *testing.T) {
	withFastPoll(t, 1*time.Millisecond)
	withPidAlive(t, func(pid int) bool { return true })

	lister := newFakePaneLister("%5")
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		StartProcessDeadDetector(ctx, lister, "%5", 1234, out)
		close(done)
	}()

	// Let it tick a few times before cancelling.
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	select {
	case sig := <-out:
		t.Fatalf("expected no signal, got %+v", sig)
	default:
	}
	if lister.Calls() == 0 {
		t.Errorf("expected pane lister to be called at least once")
	}
}

// TestStartProcessDeadDetector_PidDead_PaneAlive_EmitsErrorPath verifies the
// W6-3 path: process dead but pane still listed → Signal{PaneAlive: true}.
func TestStartProcessDeadDetector_PidDead_PaneAlive_EmitsErrorPath(t *testing.T) {
	withFastPoll(t, 1*time.Millisecond)
	withPidAlive(t, func(pid int) bool { return false })

	lister := newFakePaneLister("%5")
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		StartProcessDeadDetector(ctx, lister, "%5", 4321, out)
		close(done)
	}()

	select {
	case sig := <-out:
		if sig.Kind != agent.ProbeIntentKindProcessDead {
			t.Errorf("Kind = %q, want %q", sig.Kind, agent.ProbeIntentKindProcessDead)
		}
		if !sig.PaneAlive {
			t.Errorf("PaneAlive = false, want true (W6-3 error path)")
		}
		if sig.PaneID != "%5" {
			t.Errorf("PaneID = %q, want %q", sig.PaneID, "%5")
		}
		if sig.SenderPID != 4321 {
			t.Errorf("SenderPID = %d, want 4321", sig.SenderPID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("detector did not emit a signal in time")
	}
	<-done
}

// TestStartProcessDeadDetector_PidDead_PaneGone_EmitsClearPath verifies the
// W6-4 path: process dead and pane gone → Signal{PaneAlive: false}.
func TestStartProcessDeadDetector_PidDead_PaneGone_EmitsClearPath(t *testing.T) {
	withFastPoll(t, 1*time.Millisecond)
	withPidAlive(t, func(pid int) bool { return false })

	// pane list does NOT include %5 → HasPane returns false
	lister := newFakePaneLister("%9")
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		StartProcessDeadDetector(ctx, lister, "%5", 4321, out)
		close(done)
	}()

	select {
	case sig := <-out:
		if sig.Kind != agent.ProbeIntentKindProcessDead {
			t.Errorf("Kind = %q, want %q", sig.Kind, agent.ProbeIntentKindProcessDead)
		}
		if sig.PaneAlive {
			t.Errorf("PaneAlive = true, want false (W6-4 clear path)")
		}
		if sig.PaneID != "%5" {
			t.Errorf("PaneID = %q, want %q", sig.PaneID, "%5")
		}
		if sig.SenderPID != 4321 {
			t.Errorf("SenderPID = %d, want 4321", sig.SenderPID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("detector did not emit a signal in time")
	}
	<-done
}

// TestStartProcessDeadDetector_PidAlive_PaneGone_TreatedAsPaneGone verifies
// the rare re-parented edge case: pid is still alive (perhaps re-parented to
// init) but the pane is gone. Spec §4.2 maps this to the pane-gone path
// (PaneAlive=false) so caller routes it to clear, not error.
func TestStartProcessDeadDetector_PidAlive_PaneGone_TreatedAsPaneGone(t *testing.T) {
	withFastPoll(t, 1*time.Millisecond)
	withPidAlive(t, func(pid int) bool { return true })

	// pane list omits %5
	lister := newFakePaneLister("%9")
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		StartProcessDeadDetector(ctx, lister, "%5", 4321, out)
		close(done)
	}()

	select {
	case sig := <-out:
		if sig.PaneAlive {
			t.Errorf("PaneAlive = true, want false (re-parented edge case treated as pane-gone)")
		}
		if sig.PaneID != "%5" {
			t.Errorf("PaneID = %q, want %q", sig.PaneID, "%5")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("detector did not emit a signal in time")
	}
	<-done
}

// TestStartProcessDeadDetector_CtxCancel_ExitsImmediately verifies that when
// ctx is cancelled the detector returns without emitting a Signal.
func TestStartProcessDeadDetector_CtxCancel_ExitsImmediately(t *testing.T) {
	withFastPoll(t, 50*time.Millisecond) // slow enough that ctx wins the select
	withPidAlive(t, func(pid int) bool { return true })

	lister := newFakePaneLister("%5")
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		StartProcessDeadDetector(ctx, lister, "%5", 1234, out)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("detector did not return after ctx cancel")
	}

	select {
	case sig := <-out:
		t.Fatalf("expected no signal, got %+v", sig)
	default:
	}
}

// TestStartProcessDeadDetector_EmitsOnceThenReturns verifies that after
// emitting one Signal the detector returns (does not keep polling). Caller
// asserts only one Signal lands on the channel.
func TestStartProcessDeadDetector_EmitsOnceThenReturns(t *testing.T) {
	withFastPoll(t, 1*time.Millisecond)
	withPidAlive(t, func(pid int) bool { return false })

	lister := newFakePaneLister("%5")
	out := make(chan agent.Signal, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		StartProcessDeadDetector(ctx, lister, "%5", 1234, out)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("detector did not return after first emission")
	}

	if got := len(out); got != 1 {
		t.Errorf("len(out) = %d after detector return, want 1", got)
	}
}

// TestStartProcessDeadDetector_PidAliveFn_OverrideAndCleanup verifies the
// test override + cleanup pattern: after t.Cleanup runs, isPidAliveFn must
// be restored to probe.IsPidAlive (so subsequent tests get production
// behaviour by default).
func TestStartProcessDeadDetector_PidAliveFn_OverrideAndCleanup(t *testing.T) {
	// snapshot the production fn pointer for comparison
	productionFn := probe.IsPidAlive

	t.Run("override", func(t *testing.T) {
		withPidAlive(t, func(pid int) bool { return false })
		// detector logic is exercised elsewhere — here we just confirm the
		// package var IS overridden during the test
		if loadIsPidAliveFn()(99999) != false {
			t.Errorf("override did not take effect")
		}
	})

	// after t.Cleanup, the package var must point at production again.
	// We can't compare function values directly in Go, but we can check
	// behaviour (production probe.IsPidAlive on pid 1 — process 0/1 always
	// either exists or returns EPERM, both → true).
	if loadIsPidAliveFn()(1) != productionFn(1) {
		t.Errorf("isPidAliveFn was not restored to production probe.IsPidAlive after cleanup")
	}
}

// TestStartProcessDeadDetector_MultiPane_UsesCorrectPaneID verifies that
// when tmux lists multiple panes (e.g. %5 + %7), the detector queries with
// its own paneID and is unaffected by sibling panes.
func TestStartProcessDeadDetector_MultiPane_UsesCorrectPaneID(t *testing.T) {
	withFastPoll(t, 1*time.Millisecond)
	withPidAlive(t, func(pid int) bool { return false })

	// Pane list contains %5 and %7. We watch for %5; %5 is alive, %7 should
	// not interfere.
	lister := newFakePaneLister("%5", "%7")
	out := make(chan agent.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		StartProcessDeadDetector(ctx, lister, "%5", 1234, out)
		close(done)
	}()

	select {
	case sig := <-out:
		if sig.PaneID != "%5" {
			t.Errorf("PaneID = %q, want %q (must report watched pane, not sibling)", sig.PaneID, "%5")
		}
		if !sig.PaneAlive {
			t.Errorf("PaneAlive = false, want true (%%5 is in the listed panes)")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("detector did not emit a signal")
	}
	<-done

	// Confirm the lister was queried with %5 (not %7).
	for _, q := range lister.CallLog() {
		if q != "%5" {
			t.Errorf("lister queried with %q, want only %q", q, "%5")
		}
	}
}

// --- P2-T3: onProcessDead mapper tests (internal — tests unexported fn) ---

// TestOnProcessDead_PaneAliveTrue_MapsToError asserts the W6-3 path:
// PaneAlive=true (process dead, pane still listed) → StatusError.
func TestOnProcessDead_PaneAliveTrue_MapsToError(t *testing.T) {
	got := onProcessDead(agent.Signal{
		Kind:      agent.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 1234,
	})
	if got != agent.StatusError {
		t.Errorf("onProcessDead(PaneAlive=true) = %q, want %q", got, agent.StatusError)
	}
}

// TestOnProcessDead_PaneAliveFalse_MapsToClear asserts the W6-4 path:
// PaneAlive=false (process dead, pane gone) → StatusClear.
func TestOnProcessDead_PaneAliveFalse_MapsToClear(t *testing.T) {
	got := onProcessDead(agent.Signal{
		Kind:      agent.ProbeIntentKindProcessDead,
		PaneAlive: false,
		PaneID:    "%5",
		SenderPID: 1234,
	})
	if got != agent.StatusClear {
		t.Errorf("onProcessDead(PaneAlive=false) = %q, want %q", got, agent.StatusClear)
	}
}

// TestOnProcessDead_WrongKind_ReturnsEmpty asserts that a Signal with a
// non-ProcessDead Kind returns "" — defends against dispatcher misuse if a
// future Kind ever shares the OnSignal slot.
func TestOnProcessDead_WrongKind_ReturnsEmpty(t *testing.T) {
	got := onProcessDead(agent.Signal{
		Kind:      "screen_change",
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 1234,
	})
	if got != "" {
		t.Errorf("onProcessDead(Kind=screen_change) = %q, want empty Status", got)
	}
}
