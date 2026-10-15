package resourcesmod

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// A minute recorded after L2 carries the gate's heavy_running and a 0 in the lease's heavy_held: the report, which has not
// moved to the gate's columns yet (L2c), still counts it as a minute with heavy work.
func TestReport_AMinuteOfTheGatesCountsAsHeavy(t *testing.T) {
	s := newTestStore(t)
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC).UnixMilli()
	for i, row := range []minuteRow{
		{At: at, NCPU: 10, HeavyHeld: 2},                       // before L2: the lease's figure
		{At: at + 60000, NCPU: 10, HeavyRunning: 2},            // after: the gate's
		{At: at + 120000, NCPU: 10, HeavyRunning: 1, Load1: 3}, // one heavy command alone is not "concurrent"
	} {
		if err := s.InsertMinute(row); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
	}
	_, minutes, err := s.ReportRows(at, at+4*60000)
	if err != nil {
		t.Fatal(err)
	}
	if len(minutes) != 3 || minutes[0].HeavyHeld != 2 || minutes[1].HeavyHeld != 2 || minutes[2].HeavyHeld != 1 {
		t.Fatalf("minutes = %+v", minutes)
	}
	rep := resources.BuildReport(at, at+4*60000, nil, minutes)
	if rep.Heavy.Minutes != 2 {
		t.Errorf("heavy minutes = %d, want 2 (the lease's and the gate's)", rep.Heavy.Minutes)
	}
}
