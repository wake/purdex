package daemon

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// flakyMinutes fails its saves while down is set and keeps what it saved.
type flakyMinutes struct {
	mu    sync.Mutex
	down  bool
	saved []wire.Facts
}

func (f *flakyMinutes) SaveMinute(r wire.Facts) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errors.New("database is locked")
	}
	f.saved = append(f.saved, r)
	return nil
}
func (f *flakyMinutes) LoadMinutes(time.Time) ([]wire.Facts, error) { return nil, nil }

// A minute whose write failed is written by a later tick, in order, once the
// store works again.
func TestFailedMinuteWritesAreRetriedInOrder(t *testing.T) {
	level := 1
	fm := &flakyMinutes{down: true}
	a := samplerWithMinutes(&level, fm)
	base := s0.Truncate(time.Minute)
	tick := func(i int) { a.Tick(base.Add(time.Duration(i) * SampleInterval)) }
	for i := 0; i < 2*12+1; i++ { // minutes 0 and 1 finish while the store is down
		tick(i)
	}
	fm.mu.Lock()
	fm.down = false
	fm.mu.Unlock()
	for i := 2*12 + 1; i < 3*12+1; i++ { // minute 2 finishes with the store back
		tick(i)
	}
	if len(fm.saved) != 3 {
		t.Fatalf("want 3 minutes saved after recovery, got %d", len(fm.saved))
	}
	for i, r := range fm.saved {
		if !r.At.Equal(base.Add(time.Duration(i) * time.Minute)) {
			t.Errorf("save %d is minute %v", i, r.At)
		}
	}
}

// Whatever order and repeats the sources come in, the history is one row per
// minute, oldest first, the stored one winning.
func TestMergeMinutesDedupesAllAndStoredWins(t *testing.T) {
	m := func(min int, load float64) wire.Facts {
		return wire.Facts{At: s0.Truncate(time.Minute).Add(time.Duration(min) * time.Minute), CPU: wire.CPU{Load1: load}}
	}
	got := mergeMinutes(
		[]wire.Facts{m(0, 1), m(1, 1), m(5, 1)},
		[]wire.Facts{m(1, 9), m(3, 9), m(2, 9)}, // a clock set back made 1 come again
	)
	wantMin := []int{0, 1, 2, 3, 5}
	if len(got) != len(wantMin) {
		t.Fatalf("%d rows: %+v", len(got), got)
	}
	for i, r := range got {
		if !r.At.Equal(m(wantMin[i], 0).At) {
			t.Errorf("row %d at %v", i, r.At)
		}
	}
	if got[1].CPU.Load1 != 1 {
		t.Errorf("the stored minute must win: %+v", got[1])
	}
}
