package daemon

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

func samplerWithMinutes(level *int, m MinuteStore) *Sampler {
	return NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", OSVersion: "26.6.2",
		Sources: fakeSources(level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}, Minutes: m,
	})
}

// Finished minutes reach wwd.db, and a new run (a restart) still answers the
// 1m history with them, its own partial minute last.
func TestFinishedMinutesSurviveARestart(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	level := 1
	a := samplerWithMinutes(&level, st)
	base := s0.Truncate(time.Minute)
	for i := 0; i < 3*12+1; i++ { // three full minutes and the first tick of a fourth
		a.Tick(base.Add(time.Duration(i) * SampleInterval))
	}
	b := samplerWithMinutes(&level, st) // the restarted daemon: empty memory, same db
	b.Tick(base.Add(4*time.Minute + time.Second))
	got := b.History(base, wire.Step1m)
	if len(got) != 4 {
		t.Fatalf("want 3 stored minutes and the live one, got %d", len(got))
	}
	for i, r := range got[:3] {
		if !r.At.Equal(base.Add(time.Duration(i) * time.Minute)) {
			t.Errorf("minute %d at %v", i, r.At)
		}
	}
	if !got[3].At.Equal(base.Add(4 * time.Minute)) {
		t.Errorf("live minute at %v", got[3].At)
	}
}

type brokenMinutes struct{}

func (brokenMinutes) SaveMinute(wire.Facts) error { return errors.New("disk full") }
func (brokenMinutes) LoadMinutes(time.Time) ([]wire.Facts, error) {
	return nil, errors.New("unreadable")
}

// A failing store never stops sampling; the 1m history falls back to what this
// run holds in memory.
func TestBrokenMinuteStoreFallsBackToMemory(t *testing.T) {
	level := 1
	a := samplerWithMinutes(&level, brokenMinutes{})
	base := s0.Truncate(time.Minute)
	for i := 0; i < 2*12+1; i++ {
		a.Tick(base.Add(time.Duration(i) * SampleInterval))
	}
	if got := a.History(base, wire.Step1m); len(got) != 3 {
		t.Fatalf("want 2 finished minutes and the live one from memory, got %d", len(got))
	}
}
