package daemon

import (
	"context"
	"log"
	"runtime"
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
	// OnFacts, when set, is called with every new record (outside the sampler's
	// lock). It must not block.
	OnFacts func(wire.Facts)
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
	f.Exception = []wire.FactExc{} // exceptions arrive with WWD-1f
	f.Worker = wire.FactsWorker{Enabled: s.opts.WorkerEnabled, Slots: s.opts.Slots}

	s.mu.Lock()
	in := derived.Input{
		Pressure: f.Mem.Pressure, PressureKnown: f.Mem.Pressure > 0,
		Slots: f.Worker.Slots, Running: f.Worker.Running,
		OwnerActive: f.Owner != nil && f.Owner.Active, AvailableMB: f.Mem.AvailableMB,
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
	s.addToMinute(f)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	changed := msg != s.last
	s.last = msg
	s.mu.Unlock()

	if changed && msg != "" {
		log.Printf("wwd: sampling: %s", msg)
	}
	if s.opts.OnFacts != nil {
		s.opts.OnFacts(facts.Clone(f))
	}
}

// addToMinute collects records of one wall-clock minute and turns a finished
// minute into a rollup. The caller holds the lock.
func (s *Sampler) addToMinute(f wire.Facts) {
	m := f.At.Truncate(time.Minute)
	if len(s.minute) > 0 {
		open := s.minute[0].At.Truncate(time.Minute)
		if m.Before(open) {
			return // the clock was set back: a minute already summed is not summed twice
		}
		if !m.Equal(open) {
			s.rollups.Add(facts.Rollup(s.minute))
			s.minute = s.minute[:0]
		}
	}
	s.minute = append(s.minute, facts.Clone(f))
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
	defer s.mu.RUnlock()
	if step != wire.Step1m {
		return s.ring.Since(since)
	}
	out := s.rollups.Since(since)
	if len(s.minute) > 0 {
		if r := facts.Rollup(s.minute); !r.At.Before(since) {
			out = append(out, r)
		}
	}
	return out
}
