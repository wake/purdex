package daemon

import (
	"context"
	"errors"
	"log"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/fault"
	"github.com/wake/purdex/internal/wwd/store"
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
	Fault                    *fault.Hooks // test hooks (nil: none): the owner file and the zone override
	OwnerRule                string       // free text shown in facts.worker.owner_rule ("" without an owner rule)
	// GateHeavyFull, when set, says whether this host's pdx gate has all its heavy slots held (or cannot be read): headroom.heavy is then
	// 0, because until WWD-6 a queue job runs outside the gate's slots (#3447). nil: this host has no gate to count. It is asked on every
	// sample, outside the sampler's lock, and does file reads only.
	GateHeavyFull func() bool
	// Running, when set, is the runs on this host now by slot (queue jobs and fast-lane runs): facts.worker.running (spec §5.2). It
	// is asked on every sample, outside the sampler's lock, and reads memory only. nil: nothing is counted.
	Running func() wire.Slots
	// State, when set, is where the worker settings and the exceptions come from
	// (Slots, WorkerEnabled and OwnerRule are then only the fallback).
	State *HostState
	// OnFacts, when set, is called with every new record (outside the sampler's
	// lock). It must not block.
	OnFacts func(wire.Facts)
	// Minutes, when set, keeps the finished one-minute rollups (wwd.db) and
	// answers the 1m history from them; without it they live in memory only.
	Minutes MinuteStore
	// SignalsOpen says whether the proto 2 fields may be written (spec §13.1, phase 2): wire.PoolAtLeast over the pool's peers.
	// nil is a shut gate. While shut a record is the v1 shape; while open it is v 2 and carries decompress_rate and queue.
	SignalsOpen func() bool
	// Queued is this host's queued jobs, for facts.queue (spec §13.2); nil: the record has no queue. An error leaves queue out of the
	// record (unknown), never 0, and is logged once per change.
	Queued func() (int, error)
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
	mstats    derived.MinuteAgg // the open minute's per-sample totals (guarded by mu)

	gateFull atomic.Bool // the gate's reading at the last sample (GateFull)

	mu      sync.RWMutex
	ring    *facts.Ring
	rollups *facts.Ring
	minute  []wire.Facts // records of the minute being filled
	last    string       // the last sampling error logged, to log a change only

	lastQueue string       // the last queue-count error logged
	lastStore string       // the last store error logged
	unsaved   []wire.Facts // finished minutes not yet in the store, oldest first

	// signals is the last hour of the memory readings this host's own alert rules judge thrash by, kept as read: the proto 2 gate drops
	// decompress_rate from every record that leaves the sampler (ring, rollups, store, events), but a local rule must not go blind when a
	// peer is unreachable (spec §13.1: alerts are not behind the gate). It is never served or stored (guarded by mu).
	signals []localSignal
}

// localSignal is one sample's compressor size and decompress rate, as read (nil: not read).
type localSignal struct {
	at           time.Time
	compressorMB *int
	rate         *float64
}

// maxUnsaved bounds the minutes kept for retry: a day, the in-memory depth.
const maxUnsaved = rollupMinutes

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
	f.V, f.HostID, f.Alias, f.OSVersion = wire.WriteProto, s.opts.HostID, s.opts.Alias, s.opts.OSVersion
	f.Platform, f.Arch = runtime.GOOS, runtime.GOARCH
	reading.Apply(&f)
	local := localSignal{at: now, compressorMB: f.Mem.CompressorMB, rate: f.Mem.DecompressRate} // before the gate below drops the rate
	// The proto 2 fields: written only while every pool host reads them (the gate), else the record is the v1 shape and the
	// readings are dropped here, before the record reaches the ring, the minute rollups, the store or the event stream.
	if s.opts.SignalsOpen != nil && s.opts.SignalsOpen() {
		f.V = wire.FactsProto
		if s.opts.Queued != nil {
			if n, err := s.opts.Queued(); err != nil {
				s.logQueue(err)
			} else {
				s.logQueue(nil)
				f.Queue = &wire.Queue{Queued: max(n, 0)}
			}
		}
	} else {
		f.Mem.DecompressRate, f.Queue = nil, nil
	}
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

	if on := s.opts.Fault.OwnerActive(f.Owner != nil && f.Owner.Active); on && (f.Owner == nil || !f.Owner.Active) {
		// test hook H2: the owner block says active (with a reason that names the hook), which is what the matrix waits for
		o := wire.OwnerState{}
		if f.Owner != nil {
			o = *f.Owner
		}
		o.Active, o.Reason = true, "fault: owner_active_file"
		f.Owner = &o
	} else if f.Owner == nil && f.Worker.OwnerRule == "" && s.opts.Fault.OwnerFileAbsent() {
		// no owner rule in force on this host (f.Worker.OwnerRule: what a PUT /v1/worker last set, the same thing the worker loop reads as
		// "has an owner rule"; with a rule a null block is a failed reading, and a yield must stay) and the file is
		// confirmed absent: a null block would not end a yield the file started (yield.Observe), so the block says the owner is away, with a
		// reason that names the hook (jobusage ignores it, like the active one)
		f.Owner = &wire.OwnerState{Active: false, Reason: "fault: owner_active_file absent"}
	}
	gateFull := s.opts.GateHeavyFull != nil && s.opts.GateHeavyFull() // file reads: before the lock
	s.gateFull.Store(gateFull)
	if s.opts.Running != nil {
		f.Worker.Running = s.opts.Running()
	}
	s.mu.Lock()
	// the effective owner rule is the only basis for "the owner is active" (#3457): with none, f.Owner still shows the reading but it does not count
	in := derived.Input{
		Pressure: f.Mem.Pressure, PressureKnown: f.Mem.Pressure > 0,
		Slots: f.Worker.Slots, Running: f.Worker.Running,
		OwnerActive: f.Worker.OwnerRule != "" && f.Owner != nil && f.Owner.Active, AvailableMB: f.Mem.AvailableMB, Exceptions: holds,
		GateHeavyFull: gateFull,
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
	f.Derived.ZoneOverride = s.opts.Fault.ZoneOverride()
	s.ring.Add(f)
	s.signals = append(s.signals, local)
	if over := len(s.signals) - ringRecords; over > 0 {
		s.signals = s.signals[over:]
	}
	if done := s.addToMinute(f, err == nil); done != nil && s.opts.Minutes != nil {
		s.unsaved = append(s.unsaved, *done)
		if over := len(s.unsaved) - maxUnsaved; over > 0 {
			s.unsaved = s.unsaved[over:]
		}
	}
	todo := append([]wire.Facts(nil), s.unsaved...)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	changed := msg != s.last
	s.last = msg
	s.mu.Unlock()

	// the write is outside the lock: a slow disk must not hold up the routes
	// and in order: a minute that failed is tried again on the next tick, before
	// the ones after it
	saved := 0
	for _, r := range todo {
		if err := s.opts.Minutes.SaveMinute(r); err != nil {
			s.logMinutes("saving a minute", err)
			break
		}
		saved++
	}
	if saved > 0 {
		s.mu.Lock()
		s.unsaved = s.unsaved[min(saved, len(s.unsaved)):]
		s.mu.Unlock()
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

// LocalRecent is the samples at or after since (oldest first) as this host's own alert rules judge them: the compressor size and the
// decompress rate as read, whether or not the proto 2 gate lets the rate into the records others see. A record here carries only At,
// Platform and those two memory fields; it is for in-process rules and must not be served, stored or sent.
func (s *Sampler) LocalRecent(since time.Time) []wire.Facts {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []wire.Facts
	for _, g := range s.signals {
		if g.at.Before(since) {
			continue
		}
		f := wire.Facts{At: g.at, Platform: runtime.GOOS}
		if g.compressorMB != nil {
			v := *g.compressorMB
			f.Mem.CompressorMB = &v
		}
		if g.rate != nil {
			v := *g.rate
			f.Mem.DecompressRate = &v
		}
		out = append(out, f)
	}
	return out
}

// SetOnFacts sets the function called with every new record (the daemon's event
// broker). It must not block.
func (s *Sampler) SetOnFacts(fn func(wire.Facts)) {
	s.mu.Lock()
	s.opts.OnFacts = fn
	s.mu.Unlock()
}

// logQueue logs a failure to count the queue once per change (nil clears it).
func (s *Sampler) logQueue(err error) {
	msg := ""
	if err != nil {
		msg = "counting the queued jobs for facts: " + err.Error()
	}
	s.mu.Lock()
	changed := msg != s.lastQueue
	s.lastQueue = msg
	s.mu.Unlock()
	if changed && msg != "" {
		log.Printf("wwd: %s", msg)
	}
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
func (s *Sampler) addToMinute(f wire.Facts, ok bool) *wire.Facts {
	m := f.At.Truncate(time.Minute)
	var done *wire.Facts
	if len(s.minute) > 0 {
		open := s.minute[0].At.Truncate(time.Minute)
		if m.Before(open) {
			s.mstats.Break() // a run measured across the step would have a negative length
			return nil       // the clock was set back: a minute already summed is not summed twice
		}
		if !m.Equal(open) {
			r := facts.Rollup(s.minute)
			st := s.mstats.Close()
			r.Stats = &st
			s.rollups.Add(r)
			done = &r
			s.minute = s.minute[:0]
		}
	}
	s.minute = append(s.minute, facts.Clone(f))
	if ok { // a sample that failed adds nothing to the minute's seconds
		s.mstats.Add(f, SampleInterval)
	}
	return done
}

// mergeMinutes is one row per minute, oldest first, from several sources in any
// order (a clock set back can repeat a minute): the first source that has a
// minute wins it.
func mergeMinutes(sources ...[]wire.Facts) []wire.Facts {
	have := map[int64]bool{}
	var out []wire.Facts
	for _, src := range sources {
		for _, r := range src {
			k := r.At.Truncate(time.Minute).UnixMilli()
			if !have[k] {
				have[k] = true
				out = append(out, r)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Latest is the newest record, in the shape the proto 2 gate allows now (see Shaped): the gate may have shut since the last tick.
func (s *Sampler) Latest() (wire.Facts, bool) {
	s.mu.RLock()
	f, ok := s.ring.Last()
	s.mu.RUnlock()
	if !ok {
		return f, false
	}
	return s.Shaped(f), true
}

// GateFull is whether the gate had all its heavy slots held at the last sample (false before the first, or without a gate to read).
func (s *Sampler) GateFull() bool { return s.gateFull.Load() }

// Shaped is f in the shape the gate allows now: while the gate is shut a record written while it was open (v 2, with the new fields)
// is handed out as v1, on a copy, so the ring and the store keep what they were written as. Records already in v1 shape come back
// unchanged without a copy.
func (s *Sampler) Shaped(f wire.Facts) wire.Facts {
	if s.opts.SignalsOpen != nil && s.opts.SignalsOpen() {
		return f
	}
	if f.V == wire.WriteProto && f.Mem.DecompressRate == nil && f.Queue == nil {
		return f
	}
	f = facts.Clone(f)
	f.V, f.Mem.DecompressRate, f.Queue = wire.WriteProto, nil, nil
	return f
}

// History returns the records at or after since: the 5 s ring (the last hour)
// or the one-minute rollups, the minute being filled included as a partial one.
// While the proto 2 gate is shut (spec §13.1) every record is handed out in the v1 shape, also those written while it was open and
// those read back from wwd.db after a restart: a proto 1 reader refuses the whole answer on one field it does not know.
func (s *Sampler) History(since time.Time, step string) []wire.Facts {
	return s.shaped(s.history(since, step, 0))
}

// HistoryLatest is History for a reader that wants the newest limit records (it asks for one more than it will show, to know it cut).
// The stored minutes are read newest-first up to limit when the store can, so a chart of 14 days reads what it draws and not the
// 20160 minutes behind it; the minutes of this run that are in memory (at most a day) are merged in as usual, so the answer may hold
// a few more than limit, and the reader cuts.
func (s *Sampler) HistoryLatest(since time.Time, step string, limit int) []wire.Facts {
	return s.shaped(s.history(since, step, limit))
}

func (s *Sampler) shaped(recs []wire.Facts) []wire.Facts {
	for i := range recs {
		recs[i] = s.Shaped(recs[i])
	}
	return recs
}

// latestMinuteStore is a MinuteStore that can read the newest n minutes only (store.Store).
type latestMinuteStore interface {
	LoadMinutesLatest(since time.Time, n int) ([]wire.Facts, error)
}

// history: limit > 0 reads at most that many stored minutes (the newest) where the store can.
func (s *Sampler) history(since time.Time, step string, limit int) []wire.Facts {
	s.mu.RLock()
	if step != wire.Step1m {
		defer s.mu.RUnlock()
		return s.ring.Since(since)
	}
	mem := s.rollups.Since(since)
	var live []wire.Facts // the minute being filled, as a partial one
	if len(s.minute) > 0 {
		if r := facts.Rollup(s.minute); !r.At.Before(since) {
			st := s.mstats.Snapshot()
			r.Stats = &st
			live = []wire.Facts{r}
		}
	}
	s.mu.RUnlock()
	// the store holds the minutes from before this run too and wins for a minute
	// it has; this run's in-memory minutes fill in what it lacks (a write that
	// failed or has not committed), or are all there is when it cannot be read
	var stored []wire.Facts
	if st := s.opts.Minutes; st != nil {
		var err error
		if l, ok := st.(latestMinuteStore); ok && limit > 0 {
			stored, err = l.LoadMinutesLatest(since, limit)
		} else {
			stored, err = st.LoadMinutes(since)
		}
		if err != nil {
			s.logMinutes("reading the minutes", err)
			var skipped *store.SkippedRowsError
			if !errors.As(err, &skipped) {
				stored = nil
			}
		}
	}
	return mergeMinutes(stored, mem, live)
}
