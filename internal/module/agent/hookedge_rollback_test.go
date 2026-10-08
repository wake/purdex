package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// TestHookEdge_WorksAfterClockRollback: the wall clock goes back (NTP, a wake
// from sleep) after the mod reported a light event, so the stream's high-water
// StatusEventAt is ahead of the clock. The next mod event starts it over, and a
// Stop hook that arrives after that builds an edge and wins as before.
func TestHookEdge_WorksAfterClockRollback(t *testing.T) {
	r := edgeRig(t)
	r.setClock(sec(10))
	r.modEventAt(sec(9.5), modevents.TypeTurnStart, `{"turn_id":"t1"}`)
	r.round()
	r.drain(t)

	r.setClock(sec(1)) // the clock goes back 9 s
	r.modEvent(modevents.TypeHeartbeat, `{"turn_id":"t1"}`)

	r.setClock(sec(1.5))
	r.hook(t, "PdxStop")
	wantLight(t, "overlay after the Stop", r.light(t), agentpkg.StatusIdle, SourceHook)
	r.round()
	wantLights(t, "stop hook", r.drain(t), "idle/hook")
}
