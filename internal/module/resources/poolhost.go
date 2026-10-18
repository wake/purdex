package resourcesmod

import (
	"context"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The pool-backed host reader (WWD-2c-1): while the fleet row's source is "pool", the host figures the readers of
// resources.HostReaderKey ask for (the spawn resource card, the watch patrol's busy test, the spawn note) come from wwd's facts
// of this host, not from this module's own sampler. Judgements are wwd's alone (spec D2): the zone is facts.derived.zone, and
// the trend, the zone start and the 15-minute load, which facts do not carry, stay unknown/zero here rather than being worked
// out a second time. Whenever wwd cannot give a fresh reading the reader falls back to the module's own sample.

const (
	poolHostTTL   = time.Second      // one pool read serves the readers within this
	poolHostStale = 20 * time.Second // facts older than this are not a reading
)

// poolHost reads this host's facts from the pool. The local host is the first peer of wwd's pool.
type poolHost struct {
	pool func(ctx context.Context) (wire.Pool, error)
	// history is wwd's facts history (the report's minutes); nil in a test that does not read it.
	history func(ctx context.Context, q wire.FactsHistoryQuery) (wire.FactsHistoryResponse, error)
	now     func() time.Time

	mu    sync.Mutex
	at    time.Time // when the cached answer was read
	have  bool
	cache resources.HostDetail
	facts *wire.Facts // the facts the cached reading was made from
	ok    bool
}

func newPoolHost() *poolHost {
	cl := client.New(client.DefaultSocket())
	return &poolHost{pool: cl.Pool, history: cl.FactsHistory, now: time.Now}
}

// detail is this host's reading, ok false when wwd gives none that is fresh.
func (p *poolHost) detail() (resources.HostDetail, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if p.have && now.Sub(p.at) < poolHostTTL {
		// The answer is served for a second, but a reading never outlives its 20 s: judge its age now, not when it was read.
		if p.ok && now.Sub(time.UnixMilli(p.cache.SampledAt)) >= poolHostStale {
			return resources.HostDetail{}, false
		}
		return p.cache, p.ok
	}
	// The read is made under the lock on purpose: readers that arrive meanwhile wait for this one read and then find its answer
	// (or its failure) cached, instead of each making a read of their own. The cache is stamped when the read ends, so a read
	// that was slow does not make the next waiter's look stale.
	pool, err := p.pool(context.Background())
	now = p.now()
	p.at, p.have = now, true
	p.cache, p.facts, p.ok = resources.HostDetail{}, nil, false
	if err != nil || len(pool.Peers) == 0 || pool.Peers[0].Facts == nil {
		return p.cache, false
	}
	f := pool.Peers[0].Facts
	if now.Sub(f.At) >= poolHostStale {
		return p.cache, false
	}
	p.cache, p.facts, p.ok = hostDetailOf(f), f, true
	return p.cache, true
}

// factsNow is this host's fresh facts, ok false when wwd gives none.
func (p *poolHost) factsNow() (wire.Facts, bool) {
	if _, ok := p.detail(); !ok {
		return wire.Facts{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ok || p.facts == nil {
		return wire.Facts{}, false
	}
	return *p.facts, true
}

// hostDetailOf maps facts onto the host reading. trend, zone_since and load15 are not in facts and stay unknown/zero; the
// sessions room wwd derived rides along for whoever decides whether another member can be opened.
func hostDetailOf(f *wire.Facts) resources.HostDetail {
	memPct := 0.0
	var used uint64
	if f.Mem.TotalMB > 0 {
		usedMB := max(f.Mem.TotalMB-f.Mem.AvailableMB, 0)
		memPct = min(100*float64(usedMB)/float64(f.Mem.TotalMB), 100)
		used = uint64(usedMB) << 20
	}
	cpuPct := 0.0
	if f.CPU.NCPU > 0 {
		cpuPct = 100 * f.CPU.Load1 / float64(f.CPU.NCPU)
	}
	pageRate := 0.0
	if f.Mem.PageOutRate != nil {
		pageRate += *f.Mem.PageOutRate
	}
	if f.Mem.SwapOutRate != nil {
		pageRate += *f.Mem.SwapOutRate
	}
	room := f.Derived.SessionsRoom
	return resources.HostDetail{
		Host: resources.HostUse{
			Measured: int(max(min(cpuPct, 100), memPct)), CPU: cpuPct, Mem: memPct,
			Load1: f.CPU.Load1, Load5: f.CPU.Load5, NCPU: f.CPU.NCPU,
			MemBytes: uint64(f.Mem.TotalMB) << 20, MemUsedBytes: used,
			Pressure: f.Mem.Pressure, MemorystatusLevel: -1, PageRate: pageRate,
			Full: f.CPU.Load1 >= float64(f.CPU.NCPU) || memPct >= resources.MemFullPercent || f.Mem.Pressure >= 2,
			Zone: string(f.Derived.Zone), Trend: resources.TrendUnknown.String(),
		},
		SampledAt:    f.At.UnixMilli(),
		SessionsRoom: &room,
	}
}

// fleetSource tells whether the fleet row says source = "pool". The row is read through the hostconfig module only when its
// revision moved (the config reader re-reads ~/.ssh/config), so a reader that asks often costs one point read.
type fleetSource struct {
	rev func() (int64, error)
	cfg func() (hostconfig.FleetConfig, int64, error)

	mu   sync.Mutex
	have bool
	last int64
	pool bool
}

func (s *fleetSource) isPool() bool {
	if s == nil || s.rev == nil || s.cfg == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rev, err := s.rev(); err == nil && s.have && rev == s.last {
		return s.pool
	}
	c, rev, err := s.cfg()
	if err != nil {
		return s.pool // keep what was last read
	}
	s.have, s.last, s.pool = true, rev, c.Source == hostconfig.FleetSourcePool
	return s.pool
}

// poolDetail is this host's reading from wwd when the fleet row says so and wwd has a fresh one.
func (m *Module) poolDetail() (resources.HostDetail, bool) {
	if m.pool == nil || !m.fleetSrc.isPool() {
		return resources.HostDetail{}, false
	}
	return m.pool.detail()
}

// LatestLocalFacts is resources.LocalFactsReader: wwd's facts of this host while the fleet row says pool.
func (m *Module) LatestLocalFacts() (wire.Facts, bool) {
	if m.pool == nil || !m.fleetSrc.isPool() {
		return wire.Facts{}, false
	}
	return m.pool.factsNow()
}

// watchHostSignals is wwd's reading of this host's memory pressure and paging for the load watcher's E2, nil when the source is
// not the pool or wwd has none (the detector then reads the sampler's own, as before). The paging rate is wwd's pageouts and
// swapouts per second; a platform that gives neither leaves it unknown.
func (m *Module) watchHostSignals() *watch.HostSignals {
	f, ok := m.LatestLocalFacts()
	if !ok {
		return nil
	}
	h := &watch.HostSignals{Pressure: f.Mem.Pressure, At: f.At}
	if f.Mem.PageOutRate != nil || f.Mem.SwapOutRate != nil {
		h.PageRateKnown = true
		if f.Mem.PageOutRate != nil {
			h.PageRate += *f.Mem.PageOutRate
		}
		if f.Mem.SwapOutRate != nil {
			h.PageRate += *f.Mem.SwapOutRate
		}
	}
	return h
}

// findPoolHost wires the pool reader and the fleet row's source. A hostconfig module that is not there leaves the own sample.
func (m *Module) findPoolHost(c *core.Core) {
	if m.pool != nil {
		return // a test's
	}
	svc, ok := c.Registry.Get(hostconfig.FleetConfigKey)
	if !ok {
		return
	}
	rd, ok := svc.(hostconfig.FleetConfigReader)
	if !ok {
		return
	}
	src := &fleetSource{cfg: rd.FleetConfig}
	if r, ok := svc.(interface{ FleetRevision() (int64, error) }); ok {
		src.rev = r.FleetRevision
	}
	m.pool, m.fleetSrc = newPoolHost(), src
}
