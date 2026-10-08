package agent

import (
	"testing"

	"github.com/wake/purdex/internal/modevents"
)

// TestSnapshot_SeedsBaselineSoRestartIsQuiet: after a daemon restart nothing
// has been emitted yet, so the worker has no baseline and would send the
// first light it finds — an idle one the SPA marks unread although the
// subscriber was just told the same thing by the snapshot. The snapshot
// seeds the baseline for a session that has none.
func TestSnapshot_SeedsBaselineSoRestartIsQuiet(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(r.m, modStrm, modStart) // a live, idle stream

	r.m.sendSnapshot(r.sub)
	snap := r.drain(t)
	if len(snap) != 1 || snap[0].Session != "code-work" || snap[0].Ev.RawEventName != "replay" || snap[0].Ev.Status != "idle" || snap[0].Ev.Source != "mod" {
		t.Fatalf("snapshot = %+v, want one replay frame idle/mod for code-work", snap)
	}

	// The mod's heartbeat brings back the same state.
	hb := modEv(modSID1, modevents.TypeHeartbeat, `{"turn_id":""}`)
	hb.Seq = 5
	feedMod(r.m, modStrm, hb)
	r.m.modMu.Lock()
	r.m.modDirty[modSID1] = modevents.TypeHeartbeat
	r.m.modMu.Unlock()
	r.round()

	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("the worker re-sent what the snapshot had already said: %+v", got)
	}
}

// TestSnapshot_DoesNotOverwriteExistingBaseline: the baseline stands for
// what every connection has seen; a connection that arrives later gets the
// current state without rewriting it.
func TestSnapshot_DoesNotOverwriteExistingBaseline(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.round()
	wantOneEmit(t, "baseline", r.drain(t), "code-work", "running", "mod")

	complete := modEv(modSID1, modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`)
	complete.Seq = 3
	feedMod(r.m, modStrm, complete) // not yet emitted

	r.m.sendSnapshot(r.sub)
	snap := r.drain(t)
	if len(snap) != 1 || snap[0].Ev.Status != "idle" {
		t.Fatalf("snapshot = %+v, want the current idle", snap)
	}
	if d, ok := lastEmittedDigest(r.m, "work"); !ok || d.status != "running" {
		t.Fatalf("baseline = %+v (%v), want the emitted running left alone", d, ok)
	}
	r.round()
	wantOneEmit(t, "worker still sends the change", r.drain(t), "code-work", "idle", "mod")
}
