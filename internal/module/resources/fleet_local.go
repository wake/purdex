package resourcesmod

import (
	"context"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/hostmem"
	"github.com/wake/purdex/internal/resources"
)

// The fleet's source for the host this daemon runs on (fleet status spec §7): a one-second reading of the host that forks
// nothing, and a five-second one (the top processes the sampler's ps already took, the local gate). The fleet module reads it
// instead of probing its own host over SSH. The one-second tick runs only while Start has been called, i.e. while this daemon is
// the fleet primary.

const (
	localTick = time.Second
	topN      = 5
)

// localSource implements fleet.LocalSource.
type localSource struct {
	m      *Module
	reader func() *hostmem.FastReader // made on Start, so a daemon that is never primary never loads libSystem
	logf   func(format string, args ...any)

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	fast    fleet.LocalFast
	haveF   bool
	slow    fleet.LocalSlow
	slowKey time.Time // the sampler snapshot slow was built from
	haveS   bool
	warned  bool
	changed chan struct{}
}

func newLocalSource(m *Module) *localSource {
	return &localSource{
		m: m, logf: m.logf, changed: make(chan struct{}, 1),
		reader: func() *hostmem.FastReader { return hostmem.NewFastReader(hostmem.SystemSources()) },
	}
}

// Start begins the one-second tick; a second Start while it runs does nothing.
func (s *localSource) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(s.m.runCtx)
	s.cancel, s.done = cancel, make(chan struct{})
	go s.run(ctx, s.reader(), s.done)
}

// Stop ends the tick and waits for it; the last readings stay.
func (s *localSource) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (s *localSource) run(ctx context.Context, r *hostmem.FastReader, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(localTick)
	defer t.Stop()
	for {
		s.tick(r)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// tick takes one reading and publishes it. A reading that lacks something is published with Missing set, and said once in the
// log: the figure is absent, never a quiet 0.
func (s *localSource) tick(r *hostmem.FastReader) {
	f, err := r.Read(s.m.now())
	if err != nil {
		s.warnOnce("fleet local source: no reading: %v", err)
		return
	}
	if len(f.Missing) > 0 {
		s.warnOnce("fleet local source: degraded, missing %v", f.Missing)
	}
	s.mu.Lock()
	s.fast = fleet.LocalFast{At: f.At, CPU: f.CPU, Mem: f.Mem, Level: f.Level, PageRate: f.PageRate, Load1: f.Load1, NCPU: f.NCPU, Missing: f.Missing}
	s.haveF = true
	s.mu.Unlock()
	select { // coalesced: a full buffer already says "something new"
	case s.changed <- struct{}{}:
	default:
	}
}

func (s *localSource) warnOnce(format string, args ...any) {
	s.mu.Lock()
	first := !s.warned
	s.warned = true
	s.mu.Unlock()
	if first {
		s.logf("[resources] "+format, args...)
	}
}

// Fast is the newest one-second reading.
func (s *localSource) Fast() (fleet.LocalFast, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fast, s.haveF
}

// Changed is signalled after each new fast reading.
func (s *localSource) Changed() <-chan struct{} { return s.changed }

// Slow is the five-second reading: the top processes of the sampler's last ps and the local gate, rebuilt once per sampler tick.
func (s *localSource) Slow() (fleet.LocalSlow, bool) {
	snap := s.m.latest.Load()
	procs := s.m.lastProcs.Load()
	if snap == nil || !snap.Available || procs == nil {
		return fleet.LocalSlow{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.haveS && s.slowKey.Equal(snap.SampledAt) {
		return s.slow, true
	}
	s.slow = fleet.LocalSlow{At: snap.SampledAt, TopCPU: topProcs(*procs, byCPU), TopMem: topProcs(*procs, byRSS), Gate: s.m.gateSummary()}
	s.slowKey, s.haveS = snap.SampledAt, true
	return s.slow, true
}

// gateSummary is the local gate as `pdx gate status --json` shows it, from the gate directory (no daemon of the gate's, no lsof
// except for a slot whose gate is gone); nil when no gate has run on this host.
func (m *Module) gateSummary() *gate.Status {
	if m.gateConfig == nil {
		return nil
	}
	cfg := m.gateConfig()
	if cfg.Dir == "" {
		return nil
	}
	if _, err := os.Stat(cfg.Dir); err != nil {
		return nil
	}
	st := gate.New(cfg).StatusWith(gate.HoldersOrphaned)
	return &st
}

type procOrder int

const (
	byCPU procOrder = iota
	byRSS
)

// topProcs are the topN processes by the order, biggest first, a lower pid first on a tie.
func topProcs(procs []resources.Proc, by procOrder) []fleet.Proc {
	sorted := append([]resources.Proc(nil), procs...)
	key := func(p resources.Proc) float64 {
		if by == byCPU {
			return p.Pcpu
		}
		return float64(p.RSSBytes)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if key(sorted[i]) != key(sorted[j]) {
			return key(sorted[i]) > key(sorted[j])
		}
		return sorted[i].PID < sorted[j].PID
	})
	out := make([]fleet.Proc, 0, topN)
	for _, p := range sorted[:min(len(sorted), topN)] {
		out = append(out, fleet.Proc{PID: p.PID, Name: p.Comm, CPU: p.Pcpu, RSSMB: float64(p.RSSBytes) / (1 << 20)})
	}
	return out
}
