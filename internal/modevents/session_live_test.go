package modevents

import (
	"encoding/json"
	"testing"
	"time"
)

// A stream that only sends rejected (400) batches is not a working mod: Reject refreshes LastSeen (diagnostics, eviction)
// but must not keep the session live (#2555 attack). Mutation gate: SessionLive reads LastSeen → red.
func TestRegistry_SessionLive_RejectedBatchesDoNotKeepItAlive(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	if _, err := reg.Apply(mkBatch("stream-aaaa", 0, mkEvent(1, "heartbeat"))); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		clk.Advance(10 * time.Second)
		reg.Reject("stream-aaaa")
	}
	if reg.SessionLive(sidA, PresenceWindow) {
		t.Fatal("40 s of rejected batches after the last applied one: not live")
	}
	reg.Reject("stream-bbbb") // a stream only ever rejected has no session at all
	if reg.SessionLive("", PresenceWindow) {
		t.Fatal("an empty sid is never live")
	}
}

// SessionLive: a stream that is not ended, whose current session is sid and whose last batch is within the window.
// Mutation gate: ignore the age, the sid, or Ended → red.
func TestRegistry_SessionLive(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	apply := func(b Batch) {
		t.Helper()
		if _, err := reg.Apply(b); err != nil {
			t.Fatal(err)
		}
	}
	if reg.SessionLive(sidA, PresenceWindow) {
		t.Fatal("an empty registry has no live session")
	}
	apply(mkBatch("stream-aaaa", 0, mkEvent(1, "heartbeat")))
	if !reg.SessionLive(sidA, PresenceWindow) || reg.SessionLive(sidB, PresenceWindow) || reg.SessionLive("", PresenceWindow) {
		t.Fatal("live by current session id only")
	}
	clk.Advance(29 * time.Second)
	if !reg.SessionLive(sidA, PresenceWindow) {
		t.Fatal("still live at 29 s")
	}
	clk.Advance(2 * time.Second)
	if reg.SessionLive(sidA, PresenceWindow) {
		t.Fatal("stale at 31 s")
	}
	apply(mkBatch("stream-aaaa", 0, Event{Seq: 2, SID: sidB, Type: "heartbeat", Data: json.RawMessage(`{}`)}))
	if reg.SessionLive(sidA, PresenceWindow) || !reg.SessionLive(sidB, PresenceWindow) {
		t.Fatal("liveness follows the stream's current session (/clear, resume)")
	}
	apply(mkBatch("stream-aaaa", 0, Event{Seq: 3, SID: sidB, Type: TypeSessionEnd, Data: json.RawMessage(`{"reason":"exit"}`)}))
	if reg.SessionLive(sidB, PresenceWindow) {
		t.Fatal("an ended stream is not live")
	}
}
