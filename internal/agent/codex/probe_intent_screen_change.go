// internal/agent/codex/probe_intent_screen_change.go
//
// Codex-side detector for ProbeIntentKindScreenChange. Watches the top
// 10 lines of the codex pane via probe.Prober.Watch and emits a single
// Signal once the screen-contract is satisfied. Used by the daemon
// ProbeIntent dispatcher to recover the missing permission-approval
// transition that codex 0.125.0 does not emit (TUI fires
// PdxPermissionRequest → status=waiting, then NO hook when the user
// approves the modal). See
// docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md (v6.1
// final, 7 round codex spec review).
package codex

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
)

// screenWatcher is the minimal contract this detector requires from the
// prober. Production wires *probe.Prober; tests inject a fake to drive
// callbacks deterministically. Same pattern as W6-3's tmuxPaneLister
// interface — keeps detector focused on the two methods it actually
// uses rather than the full *probe.Prober surface.
//
// Why StopWatchOwned (W6-6 R2 F1):
//
//	The dispatcher can re-arm a same-target ScreenChange intent (status
//	cycles waiting → running → waiting); each re-arm runs Watch on the
//	new detector goroutine, which Watch internally cancels the previous
//	entry and installs a fresh entry tagged with a new identity token.
//	The PREVIOUS detector's main goroutine then wakes from <-ctx.Done()
//	and runs its teardown — calling target-only StopWatch here would
//	silently kill the freshly-installed replacement watcher, leaving
//	the new detector's intent armed but its underlying watchLoop dead.
//	StopWatchOwned takes the WatchHandle returned by THIS detector's
//	Watch call and only cancels the entry when the live id still
//	matches; a stale teardown is a no-op.
type screenWatcher interface {
	Watch(target string, opts probe.WatchOptions, cb probe.ScreenChangeCallback) probe.WatchHandle
	StopWatchOwned(h probe.WatchHandle) bool
}

// screenChangeTopLines is the capture region. Codex 0.125.0 TUI puts
// conversation at lines 1-10, input at line 11, status at line 13.
// Capturing only the top 10 lines excludes input echo + status line
// tick noise so the detector observes only conversation flow (mlab
// live verify 2026-05-01).
const screenChangeTopLines = 10

// StartScreenChangeDetector watches a codex pane and emits a single
// Signal once the screen contract is satisfied. The detector is a
// 2-phase passive observer with a 2-case truth table:
//
//	Phase A (passive observation, armed=false): wait for ScreenStable.
//	ScreenChanged events in this phase are dropped (dialog render
//	bursts, user typing into the prompt before the pane settles, etc.).
//
//	Phase B (active monitoring, armed=true): once ScreenStable arms the
//	detector, the next ScreenChanged with isCodexAlive=true emits the
//	Signal exactly once. Subsequent ScreenChanged events are no-ops
//	because emitted=true.
//
//	Case 1: Phase A succeeds — dialog renders, hash settles for
//	IdleStableTicks consecutive ticks (default 3 = 1.5s). User approval
//	triggers ScreenChanged → emit → status: waiting → running.
//
//	Case 2: Phase A never succeeds — user reacts inside IdleStableTicks,
//	tool completes silently before the pane settles, dialog redraws
//	indefinitely, etc. armed stays false; all ScreenChanged drop;
//	detector waits for ctx cancel. Lights stay waiting until the
//	PdxStop hook transitions to idle (skipping the running phase by
//	contract).
//
// "Quick approval" and "fast with output" are case-2 by contract, not
// bugs. The PdxStop hook is the secondary-signal cover. This is the
// accepted tradeoff against introducing long-dialog false positives
// (see spec §0.4 v1-v4 retrospective).
//
// Why atomic.Bool + sync.Mutex instead of sync.Once (v4 retired —
// round 4 F1):
//
//	sync.Once.Do permanently fires after its first invocation
//	regardless of whether the inner emit succeeded. A transient
//	isCodexAlive=false (tmux query failure, brief pane reuse) inside
//	Once.Do would permanently disable the detector. v5 uses atomic.Bool
//	for emitted plus a sync.Mutex so a failed emit attempt leaves
//	emitted=false and the next ScreenChanged can retry.
//
// Why two isCodexAlive checks (v3 round 3 F2, carried into v5):
//
//	The first check (outside the mutex) short-circuits dialog noise
//	from a pane that has already been reused by another process before
//	the detector has a chance to acquire the mutex. The second check
//	(inside the mutex, before emit) closes the race between the first
//	check and the actual send.
//
// Why FirstAliveAgentInTree (production wiring, v3 round 3 F3, carried
// into v5):
//
//	prober.IsAliveFor internally uses tmux PanePID(target), which
//	returns the first pane of the target's window — wrong for paneID
//	%N in multi-pane windows. prober.FirstAliveAgentInTree uses
//	ActivePanePID, which honors pane id targets exactly. The
//	IsAliveFor inconsistency is pre-existing infrastructure tracked in
//	a follow-up issue (out of scope for W6-6 PR). Production binds:
//
//	    isCodexAlive := func() bool {
//	        t, _, err := prober.FirstAliveAgentInTree(paneID)
//	        return err == nil && t == "codex"
//	    }
//
// Detector lifecycle vs WatchHandle.Done() (W6-6 R5 F7 contract):
//
//	The Watch() call returns a WatchHandle whose Done() channel closes
//	when the underlying watchLoop goroutine exits. That signal is used
//	for ownership-aware teardown (StopWatchOwned via F1) and as a
//	teardown-hygiene observation point in tests, but it is NOT a
//	detector-level lifecycle signal. The detector's state machine is
//	driven exclusively by ScreenStable / ScreenChanged callbacks and
//	ctx.Done(); baseline capture failure is a probe-internal transient
//	condition that the watchLoop now retries (see
//	internal/agent/probe/activity.go's watchLoop GoDoc), so the
//	detector must not observe Done() as "watcher gone, give up".
//
//	An earlier R4 F6 attempt added `case <-wh.Done()` to the main
//	select arm so the detector could re-arm via the dispatcher's
//	!appliedAny teardown path on persistent capture-pane failure.
//	R5 codex review found that pattern produced a tight rearm loop
//	under sustained tmux failures (zero-emit return → teardown →
//	applyStatus rearm → next Watch fails baseline → repeat). F7
//	reverts the observation here and shifts the retry into the probe
//	layer; baseline failure no longer surfaces as a lifecycle event.
//
// Why mutex-protected closed bool (v6.1 round 6 P1 fix):
//
//	The dispatcher wrap goroutine calls close(out) immediately after
//	this detector returns. probe.Prober.Watch is fire-and-forget:
//	callbacks run on the watchLoop goroutine, and prober.StopWatch
//	only cancels the watcher's ctx — it does NOT wait for an
//	in-flight callback to finish. That means a callback can be inside
//	the `select case out <- sig:` below at exactly the moment the
//	main goroutine returns and the wrap goroutine closes out. Go's
//	select picks a ready case at random, so even when ctx.Done is also
//	ready, the send case can be chosen and panic on the freshly
//	closed channel.
//
//	Fix: the main goroutine sets a mutex-protected closed flag before
//	returning. The callback's first action inside the mutex is
//	`if closed { return }`. Lock ordering then guarantees:
//	  - if main has already set closed=true, the callback returns
//	    without entering the send select;
//	  - if the callback is already inside the send select, main is
//	    blocked on mu.Lock so out has not yet been closed (the wrap
//	    goroutine only closes out AFTER the main goroutine returns,
//	    which only happens AFTER mu.Unlock).
//	Either branch makes close(out) and the send strictly disjoint.
//
// Why atomic.Bool insufficient for closed (v6.1 round 6 P1):
//
//	atomic.Bool Load+Store around the send admits a window where the
//	callback observes closed=false → enters the send select → main
//	stores closed=true → wrap goroutine closes out → callback's send
//	case can still fire on a freshly closed channel. Mutex gives
//	callback and main a serialized critical section, which is the
//	simplest construct that makes close(out) and the send strictly
//	disjoint.
func StartScreenChangeDetector(
	ctx context.Context,
	prober screenWatcher,
	isCodexAlive func() bool,
	paneID string,
	senderPID int,
	out chan<- agent.Signal,
) {
	var (
		armed     atomic.Bool
		emitted   atomic.Bool
		mu        sync.Mutex
		closed    bool // protected by mu; set true by main goroutine before return
		emittedCh = make(chan struct{})
	)
	sig := agent.Signal{
		Kind:      agent.ProbeIntentKindScreenChange,
		PaneAlive: true,
		PaneID:    paneID,
		SenderPID: senderPID,
	}
	cb := func(ev probe.ScreenChangeEvent) {
		switch ev.Kind {
		case probe.ScreenStable:
			// Phase A → Phase B. Idempotent across multiple ScreenStable
			// (e.g. the watcher's stable counter resets after fire and
			// can fire again if the hash stays put for another window).
			armed.Store(true)
		case probe.ScreenChanged:
			if !armed.Load() {
				// Phase A still in progress (or failed) — drop. Case 2
				// path stays here permanently.
				return
			}
			if !isCodexAlive() {
				// Pane no longer hosts a codex process (reused after
				// tmux server restart, etc.) — drop. emitted stays
				// false so a later ScreenChanged with a recovered
				// identity can still emit.
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if closed {
				// v6.1 round 6 P1 fix: main goroutine has begun
				// teardown and the wrap goroutine is about to
				// close(out). Drop without entering the send select to
				// avoid a panic on a closed channel.
				return
			}
			if emitted.Load() {
				// Idempotent: another callback already emitted.
				return
			}
			// Re-verify identity inside the critical section to close
			// the race between the outer isCodexAlive check and the
			// emit. If the second check fails, emitted stays false and
			// the next ScreenChanged can retry — this is the v5 fix
			// for round 4 F1 (sync.Once permanent fuse).
			if !isCodexAlive() {
				return
			}
			select {
			case out <- sig:
				emitted.Store(true)
				close(emittedCh)
			case <-ctx.Done():
				// ctx wins: emitted stays false, emittedCh stays open.
				// The main goroutine's <-ctx.Done() arm handles
				// teardown. The mutex + closed handshake below ensures
				// out is not yet closed at this point: the main
				// goroutine is blocked on mu.Lock while we hold mu.
			}
		}
	}
	wh := prober.Watch(paneID, probe.WatchOptions{TopLines: screenChangeTopLines}, cb)
	// W6-6 R5 F7: detector lifecycle is driven only by emit (via cb) or
	// ctx.Done(). wh.Done() is intentionally NOT observed here — see
	// the GoDoc above for the reasoning. Baseline capture failure is a
	// probe-internal transient state handled by the watchLoop's retry
	// behavior, not a detector lifecycle event.
	select {
	case <-emittedCh:
	case <-ctx.Done():
	}
	// W6-6 R2 F1 fix: ownership-aware teardown. StopWatchOwned only
	// cancels the entry when the live id still matches `wh.id`, so a
	// same-target re-arm that already installed a replacement watcher
	// is left intact. A target-only StopWatch here would silently kill
	// the replacement and leave the new detector's intent armed
	// without a watchLoop.
	prober.StopWatchOwned(wh)
	// v6.1 round 6 P1 fix: serialize with any in-flight callback
	// before returning. The wrap goroutine closes out immediately
	// after this function returns; setting closed=true under mu means
	// the next cb entry observes it and refuses to send, while a cb
	// already inside mu finishes its send select with out still open
	// (the wrap goroutine cannot close out until we unlock and
	// return).
	mu.Lock()
	closed = true
	mu.Unlock()
}
