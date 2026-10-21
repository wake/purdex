package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/wire"
)

var roomNow = time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)

func roomFacts(age time.Duration) wire.Facts {
	f := wire.Facts{}
	f.Derived.Zone = wire.ZoneNormal
	f.Worker = wire.FactsWorker{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}}
	if age > 0 {
		f.Worker.Waiting = &wire.Waiting{Count: 1, OldestID: "j_9", OldestOrigin: "mlab", OldestSince: roomNow.Add(-age), ObservedAt: roomNow}
	}
	return f
}

// The wiring of #3941 as the fast lane meets it: a heavy adhoc run is refused while a queue job waits, a declared (light) task is not, and
// past the cap the heavy run goes ahead and the daemon says so once.
func TestFastLaneRoom_HeavyGivesWayLightDoesNot(t *testing.T) {
	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	policy := derived.YieldPolicy{After: 5 * time.Minute, Max: 30 * time.Minute}
	age := 7 * time.Minute
	room := fastLaneRoom(func() (wire.Facts, bool) { return roomFacts(age), true }, policy, func() time.Time { return roomNow }, logf)

	if why := room(true, wire.Slots{}); len(why) != 1 || !strings.HasPrefix(why[0], derived.ReasonYieldToQueue+"j_9") {
		t.Errorf("heavy: %v", why)
	}
	if why := room(false, wire.Slots{}); len(why) != 0 {
		t.Errorf("light: %v", why)
	}
	if len(logs) != 0 {
		t.Errorf("logged while giving way: %v", logs)
	}

	age = 31 * time.Minute
	for range 3 {
		if why := room(true, wire.Slots{}); len(why) != 0 {
			t.Fatalf("past the cap: %v", why)
		}
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "j_9") || !strings.Contains(logs[0], "fastlane_yield_max_seconds") {
		t.Errorf("the cap is told once: %v", logs)
	}
}

func TestFastLaneRoom_NoRecordYetAndAFullSlot(t *testing.T) {
	policy := derived.YieldPolicy{After: 5 * time.Minute}
	none := fastLaneRoom(func() (wire.Facts, bool) { return wire.Facts{}, false }, policy, func() time.Time { return roomNow }, func(string, ...any) {})
	if why := none(true, wire.Slots{}); len(why) != 1 || why[0] != "no_facts_yet" {
		t.Errorf("%v", why)
	}
	busy := fastLaneRoom(func() (wire.Facts, bool) { return roomFacts(7 * time.Minute), true }, policy, func() time.Time { return roomNow }, func(string, ...any) {})
	if why := busy(true, wire.Slots{Heavy: 1}); len(why) != 1 || why[0] != derived.ReasonSlotsFull {
		t.Errorf("a full slot is told as it was: %v", why)
	}
}
