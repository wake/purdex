package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// lastEmittedDigest reads the session's baseline.
func lastEmittedDigest(m *Module, name string) (lightsDigest, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.lastEmittedLights[name]
	return d, ok
}

// TestEmitBaseline_NotRecordedWhenBroadcastFailed: a hook whose session code
// cannot be resolved puts nothing on the wire, so it must not become the
// baseline; when the code comes back the worker, finding the same light,
// still has to send it.
func TestEmitBaseline_NotRecordedWhenBroadcastFailed(t *testing.T) {
	r := newWorkerRig(t)
	r.registerIdleHooks()
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	known := r.m.sessions
	r.m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{}} // "work" has no code

	r.hook(t, "PdxStop")
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("setup: the hook emitted without a code: %+v", got)
	}
	if d, ok := lastEmittedDigest(r.m, "work"); ok {
		t.Fatalf("a frame that never went out became the baseline: %+v", d)
	}

	r.m.sessions = known
	r.m.modMu.Lock()
	r.m.modDirty[modSID1] = "heartbeat"
	r.m.modMu.Unlock()
	r.round()
	wantOneEmit(t, "worker after the code came back", r.drain(t), "code-work", "idle", "hook")
}

// blockingTmux holds the first PaneSessionName call after arm(): that call
// happens inside the projection read, after the mod overlay was applied, so
// whoever is held there is holding a projection that is about to go stale.
type blockingTmux struct {
	*tmux.FakeExecutor
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (b *blockingTmux) arm() { b.armed.Store(true) }

func (b *blockingTmux) PaneSessionNameCtx(_ context.Context, pane string) (string, error) {
	return b.PaneSessionName(pane)
}

func (b *blockingTmux) PaneSessionName(pane string) (string, error) {
	if b.armed.CompareAndSwap(true, false) {
		close(b.entered)
		<-b.release
	}
	return b.FakeExecutor.PaneSessionName(pane)
}

// TestEmitBaseline_WorkerDoesNotOvertakeNewerHook: the worker has read the
// projection (mod running) and is held before it compares and sends; the mod
// then reports turn.complete and a Stop hook emits idle. Whatever the
// interleaving, the last frame on the wire is the newer one and the baseline
// is that frame's digest — the worker never sends a projection older than a
// hook emit that has already gone out.
func TestEmitBaseline_WorkerDoesNotOvertakeNewerHook(t *testing.T) {
	r := newWorkerRig(t)
	r.registerIdleHooks()
	seedIdentityFrame(t, r.m, "%5", "cc", 200, "Sun Apr 20 01:30:00 2026", 10, modSID1, "/w")
	bt := &blockingTmux{FakeExecutor: tmux.NewFakeExecutor(), entered: make(chan struct{}), release: make(chan struct{})}
	bt.SetPaneSessionName("%5", "work")
	// The batch snapshot answers the name without a PaneSessionName call; an
	// ambiguous pane is the one that still asks, and that call is the hold
	// point (after the overlay was applied) this test needs.
	bt.SetPaneAmbiguous("%5", true)
	r.m.tmux = bt

	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.m.emitSessionState("work", "mod", nil) // baseline: running / mod
	wantOneEmit(t, "baseline", r.drain(t), "code-work", "running", "mod")

	bt.arm()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.m.emitSessionState("work", "mod", map[string]any{"mod_event": "heartbeat"})
	}()
	<-bt.entered // the worker holds its (running) projection

	// Make the held projection stale: the mod's turn ends, and the Stop hook
	// goes out as idle / mod.
	complete := modEv(modSID1, modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`)
	complete.Seq = 3
	feedMod(r.m, modStrm, complete)
	hookDone := make(chan struct{})
	go func() {
		defer close(hookDone)
		r.hook(t, "PdxStop")
	}()
	// Without a lock around the worker's read-compare-send the hook finishes
	// while the worker is held; with one it waits for the worker.
	select {
	case <-hookDone:
	case <-time.After(150 * time.Millisecond):
	}
	close(bt.release)
	wg.Wait()
	<-hookDone

	got := r.drain(t)
	last := lastEmit(t, "after both", got)
	if last.Ev.Status != "idle" || last.Ev.Source != "mod" {
		t.Fatalf("last frame on the wire is %s/%s, want the newer idle/mod (all: %+v)", last.Ev.Status, last.Ev.Source, got)
	}
	d, ok := lastEmittedDigest(r.m, "work")
	if !ok || d.status != "idle" || d.source != "mod" {
		t.Fatalf("baseline = %+v (%v), want the digest of the last frame on the wire (idle/mod)", d, ok)
	}
}
