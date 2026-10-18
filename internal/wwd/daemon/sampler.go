package daemon

import (
	"context"
	"log"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	// SampleInterval is how often facts are taken (spec §3.1).
	SampleInterval = 5 * time.Second
	ringRecords    = 720  // an hour of 5 s records
	rollupMinutes  = 1440 // a day of one-minute rollups (persisted in wwd.db from WWD-1f)
)

// SamplerOptions configures a Sampler.
type SamplerOptions struct {
	HostID, Alias, OSVersion string
	Sources                  facts.Sources
	Slots                    wire.Slots
	WorkerEnabled            bool
	OwnerRule                string // free text shown in facts.worker.owner_rule ("" without an owner rule)
	// State, when set, is where the worker settings and the exceptions come from
	// (Slots, WorkerEnabled and OwnerRule are then only the fallback).
	State *HostState
	// OnFacts, when set, is called with every new record (outside the sampler's
	// lock). It must not block.
	OnFacts func(wire.Facts)
	// Minutes, when set, keeps the finished one-minute rollups (wwd.db) and
	// answers the 1m history from them; without it they live in memory only.
	Minutes MinuteStore
}

// MinuteStore persists the per-minute rollups (store.Store).
type MinuteStore interface {
	SaveMinute(wire.Facts) error
	LoadMinutes(since time.Time) ([]wire.Facts, error)
}

// Sampler takes one facts record per interval, judges it (derived), keeps the
// last hour and the per-minute rollups, and answers the facts routes.
type Sampler struct {
	opts      SamplerOptions
	collector *facts.Collector
	latch     derived.ZoneLatch

	mu      sync.RWMutex
	ring    *facts.Ring
	rollups *facts.Ring
	minute  []wire.Facts // records of the minute being filled
	last    string       // the last sampling error logged, to log a change only

	lastStore string // the last store error logged
}

func NewSampler(o SamplerOptions) *Sampler {
	return &Sampler{
		opts: o, collector: facts.NewCollector(o.Sources),
		ring: facts.NewRing(ringRecords), rollups: facts.NewRing(rollupMinutes),
	}
}

// Run takes a sample now and then every SampleInterval until ctx ends.
func (s *Sampler) Run(ctx context.Context) {
	t := time.NewTicker(SampleInterval)
	defer t.Stop()
	s.Tick(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.Tick(now)
		}
	}
}

// Tick takes one sample stamped now. A failing source is logged once per change
// and leaves its fields at their zero values; the record is still kept.
func (s *Sampler) Tick(now time.Time) {
	reading, err := s.collector.Sample()
	reading.At = now
	var f wire.Facts
	f.V, f.HostID, f.Alias, f.OSVersion = wire.Proto, s.opts.HostID, s.opts.Alias, s.opts.OSVersion
	f.Platform, f.Arch = runtime.GOOS, runtime.GOARCH
	reading.Apply(&f)
	f.Exception = []wire.FactExc{}
	f.Worker = wire.FactsWorker{Enabled: s.opts.WorkerEnabled, Slots: s.opts.Slots, OwnerRule: s.opts.OwnerRule}
	var holds []derived.Hold
	if st := s.opts.State; st != nil {
		// one view: an exception that ended is announced before this record shows it
		// gone, and the exceptions and the holds they cause come from the same moment
		snap := st.Snapshot(now)
		f.Worker = wire.FactsWorker{Enabled: snap.Worker.Enabled, Slots: snap.Worker.Slots, OwnerRule: snap.Worker.OwnerRule}
		f.Exception, holds = snap.Exceptions, snap.Holds
	}

	s.mu.Lock()
	in := derived.Input{
		Pressure: f.Mem.Pressure, PressureKnown: f.Mem.Pressure > 0,
		Slots: f.Worker.Slots, Running: f.Worker.Running,
		OwnerActive: f.Owner != nil && f.Owner.Active, AvailableMB: f.Mem.AvailableMB, Exceptions: holds,
	}
	if f.Mem.CompressorMB != nil {
		in.CompressorMB = *f.Mem.CompressorMB
	}
	if runtime.GOOS == "darwin" {
		in.RateLimit = derived.PageRateLimit
		if f.Mem.PageOutRate != nil {
			in.PageRate += *f.Mem.PageOutRate
		}
		if f.Mem.SwapOutRate != nil {
			in.PageRate += *f.Mem.SwapOutRate
		}
	}
	f.Derived = derived.Compute(&s.latch, in)
	s.ring.Add(f)
	done := s.addToMinute(f)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	changed := msg != s.last
	s.last = msg
	s.mu.Unlock()

	// the write is outside the lock: a slow disk must not hold up the routes
	if done != nil && s.opts.Minutes != nil {
		if err := s.opts.Minutes.SaveMinute(*done); err != nil {
			s.logMinutes("saving a minute", err)
		}
	}

	if changed && msg != "" {
		log.Printf("wwd: sampling: %s", msg)
	}
	s.mu.RLock()
	on := s.opts.OnFacts
	s.mu.RUnlock()
	if on != nil {
		on(facts.Clone(f))
	}
}

// SetOnFacts sets the function called with every new record (the daemon's event
// broker). It must not block.
func (s *Sampler) SetOnFacts(fn func(wire.Facts)) {
	s.mu.Lock()
	s.opts.OnFacts = fn
	s.mu.Unlock()
}

// logMinutes logs a store failure once per change, like a sampling error.
func (s *Sampler) logMinutes(what string, err error) {
	msg := what + ": " + err.Error()
	s.mu.Lock()
	changed := msg != s.lastStore
	s.lastStore = msg
	s.mu.Unlock()
	if changed {
		log.Printf("wwd: %s", msg)
	}
}

// addToMinute collects records of one wall-clock minute and turns a finished
// minute into a rollup, which it returns (nil when no minute finished). The
// caller holds the lock.
func (s *Sampler) addToMinute(f wire.Facts) *wire.Facts {
	m := f.At.Truncate(time.Minute)
	var done *wire.Facts
	if len(s.minute) > 0 {
		open := s.minute[0].At.Truncate(time.Minute)
		if m.Before(open) {
			return nil // the clock was set back: a minute already summed is not summed twice
		}
		if !m.Equal(open) {
			r := facts.Rollup(s.minute)
			s.rollups.Add(r)
			done = &r
			s.minute = s.minute[:0]
		}
	}
	s.minute = append(s.minute, facts.Clone(f))
	return done
}

// mergeMinutes is the stored minutes plus the in-memory ones the store does not
// have (a minute finished a moment ago whose write has not committed, or whose
// write failed), oldest first. Both inputs are oldest first.
func mergeMinutes(stored, mem []wire.Facts) []wire.Facts {
	have := make(map[int64]bool, len(stored))
	for _, r := range stored {
		have[r.At.UnixMilli()] = true
	}
	out := stored
	for _, r := range mem {
		if !have[r.At.UnixMilli()] {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Latest is the newest record.
func (s *Sampler) Latest() (wire.Facts, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ring.Last()
}

// History returns the records at or after since: the 5 s ring (the last hour)
// or the one-minute rollups, the minute being filled included as a partial one.
func (s *Sampler) History(since time.Time, step string) []wire.Facts {
	s.mu.RLock()
	if step != wire.Step1m {
		defer s.mu.RUnlock()
		return s.ring.Since(since)
	}
	out := s.rollups.Since(since)
	var partial *wire.Facts
	if len(s.minute) > 0 {
		if r := facts.Rollup(s.minute); !r.At.Before(since) {
			partial = &r
		}
	}
	s.mu.RUnlock()
	// the store holds the minutes from before this run too; if it cannot be
	// read, this run's in-memory minutes are what there is
	if st := s.opts.Minutes; st != nil {
		if stored, err := st.LoadMinutes(since); err == nil {
			out = mergeMinutes(stored, out)
		} else {
			s.logMinutes("reading the minutes", err)
		}
	}
	if partial != nil {
		// a restart inside this minute left an earlier half of it stored: the live one replaces it
		for len(out) > 0 && out[len(out)-1].At.Equal(partial.At) {
			out = out[:len(out)-1]
		}
		out = append(out, *partial)
	}
	return out
}
