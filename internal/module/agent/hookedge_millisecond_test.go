package agent

import (
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// The mod's event time has millisecond precision and the hook's arrival (the
// daemon clock) has nanoseconds. The hook's time is compared in whole
// milliseconds, and a tie goes to the mod: an event that happened in the
// millisecond the hook arrived counts as the newer side.

// TestHookEdge_SameMillisecondModEventHandsBack: the Stop reaches the daemon
// 500 µs into a millisecond, and the mod's turn.complete, stamped with that
// same millisecond, arrives right after. The pane goes back to the mod.
func TestHookEdge_SameMillisecondModEventHandsBack(t *testing.T) {
	r := edgeRig(t)
	r.modIdle() // idle to idle: only the edge's takeover can dirty the pane
	r.round()
	r.drain(t)

	r.setClock(sec(10) + 500*time.Microsecond)
	r.hook(t, "PdxStop")
	wantLights(t, "stop hook", r.drain(t), "idle/hook")

	r.setClock(sec(10) + 600*time.Microsecond)
	r.modEventAt(sec(10), modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`)
	wantLight(t, "overlay", r.light(t), agentpkg.StatusIdle, SourceMod)
	r.round()
	wantLights(t, "turn.complete", r.drain(t), "idle/mod")
}

// TestHookEdge_SameMillisecondModEventBeforeNoteDoesNotCreateEdge: the same
// tie at the moment the edge is noted (setHookEdge): the mod already reported
// an event of the hook's millisecond, so there is nothing to show.
func TestHookEdge_SameMillisecondModEventBeforeNoteDoesNotCreateEdge(t *testing.T) {
	r := edgeRig(t)
	r.modRunning()
	r.setClock(sec(10) + 200*time.Microsecond)
	r.modEventAt(sec(10), modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`)

	f, err := r.m.frames.GetByIdentity(racePane, 200, raceFrameStart)
	if err != nil || f == nil {
		t.Fatalf("frame: %v %v", f, err)
	}
	r.m.setHookEdge(f.FrameID, hookEdge{status: agentpkg.StatusIdle, at: r.clock.Now().Add(300 * time.Microsecond), sid: modSID1})
	if n := r.edgeCount(); n != 0 {
		t.Fatalf("%d edges, want none: the mod's event is from the hook's millisecond", n)
	}
}
