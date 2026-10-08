package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/tmux"
)

// TestSnapshot_ReadSendSeedIsAtomicAgainstWorker: the snapshot has read the
// projection (state A, mod running) and is held before it sends; the mod then
// ends the turn (state B, idle) and the worker wants to broadcast B. If the
// worker could get in first, the new subscriber would see B and then the
// stale A, and the baseline (B) would hide the stale A forever. The
// snapshot's read, send and seed are one critical section against the
// worker, so the last frame the subscriber gets is the baseline.
func TestSnapshot_ReadSendSeedIsAtomicAgainstWorker(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	bt := &blockingTmux{FakeExecutor: tmux.NewFakeExecutor(), entered: make(chan struct{}), release: make(chan struct{})}
	bt.SetPaneSessionName("%5", "work")
	r.m.tmux = bt
	feedMod(r.m, modStrm, modStart, modTurnStart) // state A: running / mod

	bt.arm()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r.m.sendSnapshot(r.sub)
	}()
	<-bt.entered // the snapshot holds its running projection

	complete := modEv(modSID1, modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`)
	complete.Seq = 3
	feedMod(r.m, modStrm, complete) // state B: idle / mod
	workerDone := make(chan struct{})
	go func() {
		defer wg.Done()
		defer close(workerDone)
		r.m.emitSessionState("work", "mod", map[string]any{"mod_event": modevents.TypeTurnComplete})
	}()
	// Without the snapshot's critical section the worker finishes while the
	// snapshot is held; with it, the worker waits for the snapshot.
	select {
	case <-workerDone:
	case <-time.After(150 * time.Millisecond):
	}
	close(bt.release)
	wg.Wait()

	got := r.drain(t)
	last := lastEmit(t, "both done", got)
	d, ok := lastEmittedDigest(r.m, "work")
	if !ok || last.Ev.Status != d.status || last.Ev.Source != d.source {
		t.Fatalf("last frame to the subscriber is %s/%s but the baseline is %+v (%v); frames: %+v", last.Ev.Status, last.Ev.Source, d, ok, got)
	}
	if last.Ev.Status != "idle" {
		t.Fatalf("the subscriber ends on %s, want the current idle (frames: %+v)", last.Ev.Status, got)
	}
}
