package agent

import (
	"testing"
	"time"
)

// TestModOverlay_EventsBeforeOverlayOnAreReEmitted: a mod event that lands
// between the subscription and the overlay going on is consumed by a worker
// round that still sees the overlay off (it emits the hook light), and the
// dirty mark is gone. Turning the overlay on must therefore re-mark every
// known sid, or the pane stays on the hook light until the stream changes.
func TestModOverlay_EventsBeforeOverlayOnAreReEmitted(t *testing.T) {
	r := newWorkerRig(t)
	r.m.modOverlayOn.Store(false)
	r.m.modTick = time.Hour
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	c, _ := modCoreWithRegistry(t)
	r.m.initModLights(c)

	// The event arrives and a round consumes it with the overlay still off.
	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.round()
	wantOneEmit(t, "round with the overlay off", r.drain(t), "code-work", "idle", "hook")
	if len(modDirtySIDs(r.m)) != 0 {
		t.Fatal("setup: the round left the sid dirty")
	}

	r.m.startModLights()
	t.Cleanup(r.m.stopModLights)

	deadline := time.Now().Add(time.Second)
	for {
		got := r.drain(t)
		if len(got) > 0 {
			wantOneEmit(t, "after the overlay came on", got, "code-work", "running", "mod")
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the stream known before the overlay went on was never re-emitted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
