package daemon

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// statsSampler is a sampler whose load and memory source can be steered and made to fail.
type steer struct {
	load1   float64
	memFail bool
}

func statsSampler(st *steer, m MinuteStore) *Sampler {
	level := 1
	src := fakeSources(&level, nil)
	src.CPU = func() (facts.RawCPU, error) {
		return facts.RawCPU{NCPU: 10, Load1: st.load1, Busy: 100, Total: 1000}, nil
	}
	inner := src.Mem
	src.Mem = func() (facts.RawMem, error) {
		if st.memFail {
			return facts.RawMem{}, errors.New("host_statistics failed")
		}
		return inner()
	}
	return NewSampler(SamplerOptions{HostID: "h1", Alias: "mlab", Sources: src, Slots: wire.Slots{Heavy: 1, Light: 2}, Minutes: m})
}

func minuteStart() time.Time { return s0.Truncate(time.Minute) }

func tickN(s *Sampler, from, to int) {
	for i := from; i < to; i++ {
		s.Tick(minuteStart().Add(time.Duration(i) * SampleInterval))
	}
}

// A finished minute's rollup carries the minute's stats, the live minute carries its partial stats, and a 5 s record
// carries none.
func TestRollupsCarryStats(t *testing.T) {
	st := &steer{load1: 2}
	s := statsSampler(st, nil)
	tickN(s, 0, 12+3) // minute one whole, three samples of minute two
	h := s.History(minuteStart(), wire.Step1m)
	if len(h) != 2 {
		t.Fatalf("want a finished minute and the live one, got %d", len(h))
	}
	if h[0].Stats == nil || h[0].Stats.SampledS != 60 || h[0].Stats.NormalS != 60 {
		t.Fatalf("finished minute: %+v", h[0].Stats)
	}
	if h[1].Stats == nil || h[1].Stats.SampledS != 15 {
		t.Fatalf("live minute: %+v", h[1].Stats)
	}
	if last, _ := s.Latest(); last.Stats != nil {
		t.Errorf("a 5 s record has no stats: %+v", last.Stats)
	}
	for _, r := range s.History(minuteStart(), wire.Step5s) {
		if r.Stats != nil {
			t.Fatalf("5 s history has no stats: %+v", r.Stats)
		}
	}
}

// A sample that failed adds nothing to the minute's seconds; the run of full across the next good samples is not broken
// by it.
func TestFailedSamplesAreNotCounted(t *testing.T) {
	st := &steer{load1: 12} // full
	s := statsSampler(st, nil)
	tickN(s, 0, 4)
	st.memFail = true
	tickN(s, 4, 8)
	st.memFail = false
	tickN(s, 8, 13)
	h := s.History(minuteStart(), wire.Step1m)
	if len(h) != 2 || h[0].Stats == nil {
		t.Fatalf("%d minutes", len(h))
	}
	got := h[0].Stats
	if got.SampledS != 40 || got.FullTicks != 8 || got.FullStarts != 1 {
		t.Fatalf("8 good samples of 12: %+v", got)
	}
}

// The stats are part of the stored rollup and come back from wwd.db with it.
func TestStatsSurviveTheStore(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := &steer{load1: 12}
	s := statsSampler(st, db)
	tickN(s, 0, 13)
	got, err := db.LoadMinutes(minuteStart())
	if err != nil || len(got) != 1 || got[0].Stats == nil {
		t.Fatalf("stored: %+v err %v", got, err)
	}
	if got[0].Stats.SampledS != 60 || got[0].Stats.FullTicks != 12 || got[0].Stats.FullStarts != 1 || got[0].Stats.FullLongestS != 60 {
		t.Fatalf("%+v", got[0].Stats)
	}
}

// A clock that steps back is ignored for the minute and ends the run: the next full sample is a new start, and no
// negative length appears.
func TestClockStepBackBreaksTheRun(t *testing.T) {
	st := &steer{load1: 12}
	s := statsSampler(st, nil)
	tickN(s, 0, 3)
	s.Tick(minuteStart().Add(-2 * time.Minute)) // set back: belongs to no open minute
	tickN(s, 3, 6)
	tickN(s, 12, 13) // closes the minute
	h := s.History(minuteStart(), wire.Step1m)
	if len(h) == 0 || h[0].Stats == nil {
		t.Fatal("no stats")
	}
	if got := h[0].Stats; got.FullStarts != 2 || got.SampledS != 30 || got.FullLongestS != 15 {
		t.Fatalf("%+v", got)
	}
}
