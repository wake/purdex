package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// After a wall clock rollback the stream's high-water StatusEventAt is ahead of
// the clock until the next mod event starts it over (lights.StreamState.Apply).
// The edge's readers treat a mark later than the clock as zero, so a hook that
// arrives in that gap is not held down by it.

// TestHookEdge_FutureHighWaterDoesNotBlockEdge: the mod's last light event
// happened at 9.5 s, then the clock goes back to 1 s. A Stop hook at 1.5 s,
// before any new mod event, builds an edge and wins.
func TestHookEdge_FutureHighWaterDoesNotBlockEdge(t *testing.T) {
	r := edgeRig(t)
	r.setClock(sec(10))
	r.modEventAt(sec(9.5), modevents.TypeTurnStart, `{"turn_id":"t1"}`)
	r.round()
	r.drain(t)

	r.setClock(sec(1.5)) // the clock went back; no mod event has arrived since
	r.hook(t, "PdxStop")

	if n := r.edgeCount(); n != 1 {
		t.Fatalf("%d edges, want 1", n)
	}
	wantLight(t, "overlay", r.light(t), agentpkg.StatusIdle, SourceHook)
	r.round()
	wantLights(t, "stop hook", r.drain(t), "idle/hook")
}

// TestHookEdge_ModEventTakesOverAfterRollback: the same gap seen from the mod's
// side. The first mod event after the rollback, an event that happened after the
// hook and does not change the status, still takes the edge over: the old mark
// is not the baseline it moved from.
func TestHookEdge_ModEventTakesOverAfterRollback(t *testing.T) {
	r := edgeRig(t)
	r.setClock(sec(10))
	r.modEventAt(sec(9.5), modevents.TypeTurnStart, `{"turn_id":"t1"}`)
	r.round()
	r.drain(t)

	r.setClock(sec(1.5))
	r.hook(t, "PdxStop")
	r.drain(t)

	r.setClock(sec(1.8))
	r.modEventAt(sec(1.7), modevents.TypeTurnStart, `{"turn_id":"t2"}`) // running to running
	wantLight(t, "overlay", r.light(t), agentpkg.StatusRunning, SourceMod)
	r.round()
	wantLights(t, "turn.start", r.drain(t), "running/mod")
}
