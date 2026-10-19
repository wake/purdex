package main

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
)

// measureOrphans is a diagnostic: with a staged snapshot and a Fresh that returns the same table, it runs the requested number of rounds
// without panicking (no division by zero on an empty run, no index out of range on the first-run line) and the cost helpers behave.
func TestMeasureOrphansRunsOnAStagedSnapshot(t *testing.T) {
	table := []facts.ProcRec{{PID: 1, Name: "launchd"}, {PID: 500, PPID: 1, UID: 0, Name: "sh"}}
	snap := facts.ProcSnapshot{Procs: table}
	ctx := facts.ProcContext{Fresh: func() ([]facts.ProcRec, error) { return table, nil }}
	measureOrphans(snap, ctx, 2)
	if got := median([]time.Duration{3, 1, 2}); got != 2 {
		t.Fatalf("median = %v", got)
	}
	if cpuTime() <= 0 {
		t.Fatal("no CPU time read")
	}
}
