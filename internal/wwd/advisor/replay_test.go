package advisor

import (
	"sort"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A disk that sinks below 12 GB for 10 minutes, comes back to 12.2 (still raised: the margin), then recovers to 20, then sinks again: two
// episodes, the first 12 minutes long.
func TestReplay_CountsEpisodesNotMinutes(t *testing.T) {
	free := []float64{20, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 12.2, 12.2, 20, 20, 11, 20}
	var recs []wire.Facts
	for i, g := range free {
		recs = append(recs, diskFacts(t0.Add(time.Duration(i)*time.Minute), "/Users", g))
	}
	res := Replay(recs, nil, "/Users/wake/work", DiskLow{})
	got := res[wire.AlertDiskLow]
	if got.Episodes != 2 || got.HeldMinutes != 12+1 {
		t.Fatalf("%+v", got)
	}
	if got.FirstAt != t0.Add(time.Minute) {
		t.Fatalf("first at %v", got.FirstAt)
	}
}

// The slope rule sees only the records before the moment it judges, never the ones after.
func TestReplay_ARuleSeesOnlyThePast(t *testing.T) {
	recs := history(90, 40, 0) // flat 40 GB, then the last minute collapses
	recs[len(recs)-1] = diskFacts(recs[len(recs)-1].At, "/Users", 5)
	res := Replay(recs, nil, "/Users/wake/work", DiskFalling{})
	if got := res[wire.AlertDiskFalling]; got.Episodes != 1 || got.HeldMinutes != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestReplay_CrashLoopCountsStartsInTheHourBefore(t *testing.T) {
	var recs []wire.Facts
	for i := 0; i < 120; i++ {
		recs = append(recs, wire.Facts{At: t0.Add(time.Duration(i) * time.Minute)})
	}
	starts := []time.Time{t0.Add(10 * time.Minute), t0.Add(20 * time.Minute), t0.Add(30 * time.Minute), t0.Add(100 * time.Minute)}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	got := Replay(recs, starts, "/x", WorkerCrashLoop{})[wire.AlertWorkerCrash]
	// the third start is at minute 30; the hour window empties when the first (minute 10) is more than an hour old: held minutes 30..70
	if got.Episodes != 1 || got.HeldMinutes != 41 {
		t.Fatalf("%+v", got)
	}
}

func TestReplay_NothingToReplay(t *testing.T) {
	if res := Replay(nil, nil, "/x", DiskLow{}); res[wire.AlertDiskLow].Episodes != 0 {
		t.Fatalf("%+v", res)
	}
}
