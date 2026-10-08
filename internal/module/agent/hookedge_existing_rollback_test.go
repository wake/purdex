package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// TestHookEdge_ExistingEdgeDiesOnClockRollback: an edge built at 10 s, then the
// wall clock goes back to 1 s. now - e.at is negative, which is "younger than
// the TTL" to a plain comparison; the edge would then outrank the mod for as
// long as the clock needs to catch up. An edge from the clock's future is
// dead: the pane goes back to the mod at once, the worker says so once, and
// the mod's next event is not overridden by it.
func TestHookEdge_ExistingEdgeDiesOnClockRollback(t *testing.T) {
	r := edgeRig(t)
	r.modIdle() // t = 0
	r.round()
	r.drain(t)
	r.setClock(sec(10))
	r.hook(t, "PdxUserPromptSubmit")
	wantLights(t, "prompt hook", r.drain(t), "running/hook")
	wantLight(t, "overlay before the rollback", r.light(t), agentpkg.StatusRunning, SourceHook)

	r.setClock(sec(1)) // the clock goes back 9 s
	wantLight(t, "overlay after the rollback", r.light(t), agentpkg.StatusIdle, SourceMod)
	r.round()
	e := wantOneEmit(t, "rollback", r.drain(t), "code-work", "idle", "mod")
	if e.Ev.Detail["mod_event"] != modEventEdgeExpired {
		t.Fatalf("detail = %v, want mod_event %s", e.Ev.Detail, modEventEdgeExpired)
	}
	r.round()
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("the expiry was sent again: %v", emitLights(got))
	}
	if n := r.edgeCount(); n != 0 {
		t.Fatalf("%d edges left after the rollback", n)
	}

	r.setClock(sec(1.6))
	r.modEventAt(sec(1.5), modevents.TypeTurnStart, `{"turn_id":"t2"}`)
	wantLight(t, "overlay after the first mod event", r.light(t), agentpkg.StatusRunning, SourceMod)
	r.round()
	wantLights(t, "turn.start", r.drain(t), "running/mod")
}
