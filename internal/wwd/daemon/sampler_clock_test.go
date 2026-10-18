package daemon

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A clock set back must not reorder or duplicate the minute rollups (R2).
func TestClockSetBackKeepsRollupsOrderedAndUnique(t *testing.T) {
	level := 1
	s := newSampler(&level, nil)
	tick := func(sec int) { s.Tick(s0.Add(time.Duration(sec) * time.Second)) }
	for sec := 0; sec < 180; sec += 5 { // 05:00 .. 05:02:55
		tick(sec)
	}
	tick(70)  // the clock jumps back into 05:01
	tick(75)  // and runs on from there
	tick(180) // then real time is reached again: 05:03
	tick(185)

	got := s.History(s0, wire.Step1m)
	for i := 1; i < len(got); i++ {
		if !got[i].At.After(got[i-1].At) {
			t.Fatalf("rollups not strictly increasing at %d: %v then %v", i, got[i-1].At, got[i].At)
		}
	}
	if len(got) != 4 { // 05:00, 05:01, 05:02 and the open 05:03
		t.Fatalf("%d rollups: %v", len(got), got)
	}
}
