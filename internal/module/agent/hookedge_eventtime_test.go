package agent

import (
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
)

// A hook edge is compared with the time the mod's event happened (the event's
// own at), not the time it reached the daemon: alpha.609 measured mod events
// arriving about 1.2 s late (3 s at most), so a turn.start that happened before
// a Stop was received after it, and read as "the mod caught up". The mod
// reaches the daemon only through a Unix socket on the same host, so the event
// at and the daemon's clock are one wall clock.

// modEventAt feeds one mod event that happened at `happened` (an offset from
// modT0) and is received at the clock's now.
func (r *workerRig) modEventAt(happened time.Duration, typ, data string) {
	ev := modEv(modSID1, typ, data)
	ev.At = modT0.Add(happened).UnixMilli()
	feedMod(r.m, modStrm, ev)
}

// setClock puts the daemon's clock at an offset from modT0.
func (r *workerRig) setClock(offset time.Duration) { r.clock.Set(modT0.Add(offset)) }

func sec(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// TestHookEdge_LateDeliveredOlderTurnStartDoesNotRevealRunning replays the
// alpha.609 smoke: the Stop hook is received at 13.93 s, the mod's turn.start
// (it happened at 12.75 s) at 13.95 s, and its turn.complete (14.64 s) at
// 14.70 s. The pane must stay idle (hook) until the turn.complete, with no
// running frame in between.
func TestHookEdge_LateDeliveredOlderTurnStartDoesNotRevealRunning(t *testing.T) {
	r := edgeRig(t)
	r.modIdle() // t = 0
	r.round()
	r.drain(t)

	var seen []emitted
	step := func() {
		t.Helper()
		r.round()
		seen = append(seen, r.drain(t)...)
	}

	r.setClock(sec(13.93))
	r.hook(t, "PdxStop")
	seen = append(seen, r.drain(t)...)
	wantLight(t, "overlay after the Stop", r.light(t), agentpkg.StatusIdle, SourceHook)

	r.setClock(sec(13.95))
	r.modEventAt(sec(12.75), modevents.TypeTurnStart, `{"turn_id":"t2"}`)
	step()
	wantLight(t, "overlay after the late turn.start", r.light(t), agentpkg.StatusIdle, SourceHook)

	r.setClock(sec(14.70))
	r.modEventAt(sec(14.64), modevents.TypeTurnComplete, `{"turn_id":"t2","reason":"answer"}`)
	wantLight(t, "overlay after turn.complete", r.light(t), agentpkg.StatusIdle, SourceMod)
	step()

	for _, e := range seen {
		if e.Ev.Status == "running" {
			t.Fatalf("a running frame went out: %v", emitLights(seen))
		}
	}
	if lights := emitLights(seen); len(lights) == 0 || lights[len(lights)-1] != "idle/mod" {
		t.Fatalf("emits %v, want the last one idle/mod", lights)
	}
}

// TestHookEdge_ModEventAfterHookHandsBack: the turn.start happened after the
// Stop (a new turn), so the mod has caught up and the pane goes back to it.
func TestHookEdge_ModEventAfterHookHandsBack(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.round()
	r.drain(t)

	r.setClock(sec(13.93))
	r.hook(t, "PdxStop")
	r.drain(t)

	r.setClock(sec(14.20))
	r.modEventAt(sec(14.10), modevents.TypeTurnStart, `{"turn_id":"t2"}`)
	wantLight(t, "overlay", r.light(t), agentpkg.StatusRunning, SourceMod)
	r.round()
	wantLights(t, "turn.start", r.drain(t), "running/mod")
}

// TestHookEdge_TTLIsFiveSeconds: mod events arrive up to about 3 s late, so an
// edge the mod never confirms lasts 5 s.
func TestHookEdge_TTLIsFiveSeconds(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.setClock(sec(10))
	r.hook(t, "PdxUserPromptSubmit")
	r.drain(t)

	r.setClock(sec(14.9))
	r.round()
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("emit at 4.9 s: %v", emitLights(got))
	}
	wantLight(t, "overlay at 4.9 s", r.light(t), agentpkg.StatusRunning, SourceHook)

	r.setClock(sec(15))
	wantLight(t, "overlay at 5.0 s", r.light(t), agentpkg.StatusIdle, SourceMod)
	r.round()
	e := wantOneEmit(t, "expiry", r.drain(t), "code-work", "idle", "mod")
	if e.Ev.Detail["mod_event"] != modEventEdgeExpired {
		t.Fatalf("detail = %v, want mod_event %s", e.Ev.Detail, modEventEdgeExpired)
	}
}

// TestHookEdge_OlderEventDoesNotMarkDirty: a light event that happened before
// the edge's hook does not take the edge over, so the pane is not re-emitted
// for it; one that happened after does.
func TestHookEdge_OlderEventDoesNotMarkDirty(t *testing.T) {
	r := edgeRig(t)
	r.modIdle()
	r.setClock(sec(10))
	r.hook(t, "PdxUserPromptSubmit") // edge at 10 s
	r.round()
	r.drain(t)
	if d := modDirtySIDs(r.m); len(d) != 0 {
		t.Fatalf("dirty before the test event: %v", d)
	}

	// Idle to idle: the status does not change, only the edge could dirty it.
	r.setClock(sec(10.5))
	r.modEventAt(sec(5), modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`)
	if d := modDirtySIDs(r.m); len(d) != 0 {
		t.Fatalf("an event from before the hook marked %v dirty", d)
	}
	wantLight(t, "overlay", r.light(t), agentpkg.StatusRunning, SourceHook)

	r.setClock(sec(10.8))
	r.modEventAt(sec(10.7), modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`)
	if d := modDirtySIDs(r.m); !d[modSID1] {
		t.Fatalf("an event from after the hook did not mark it dirty: %v", d)
	}
}
