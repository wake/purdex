package facts

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// What the worker was running is the minute's peak, not what it ran at the last instant (a job that starts and ends
// inside a minute still counts).
func TestRollupTakesTheRunningMax(t *testing.T) {
	var recs []wire.Facts
	for i, run := range []wire.Slots{{Heavy: 0, Light: 1}, {Heavy: 2, Light: 0}, {Heavy: 0, Light: 3}, {Heavy: 1, Light: 0}} {
		r := rec(t0.Add(time.Duration(i)*5*time.Second), 1, 1000, 1, wire.ZoneNormal)
		r.Worker.Running = run
		recs = append(recs, r)
	}
	got := Rollup(recs)
	if got.Worker.Running != (wire.Slots{Heavy: 2, Light: 3}) {
		t.Fatalf("running %+v", got.Worker.Running)
	}
	// the rest of the worker state stays the last record's
	recs[3].Worker.Enabled = true
	if got := Rollup(recs); !got.Worker.Enabled {
		t.Error("enabled follows the last record")
	}
}
