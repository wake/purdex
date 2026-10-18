package resourcesmod

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/hostmem"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/wwd/wire"
)

// procSampler counts the two reads a sampler has: the whole host (sysctl, vm_stat, ps) and the process table alone (ps).
type procSampler struct {
	host, procs atomic.Int64
	procsErr    atomic.Bool // SampleProcs fails
}

func (s *procSampler) Sample(context.Context) (resources.HostRaw, []resources.Proc, error) {
	s.host.Add(1)
	return goodRaw(), []resources.Proc{{PID: 1, Pcpu: 7}}, nil
}

func (s *procSampler) SampleProcs(context.Context) ([]resources.Proc, error) {
	s.procs.Add(1)
	if s.procsErr.Load() {
		return nil, assert.AnError
	}
	return []resources.Proc{{PID: 1, Pcpu: 7}}, nil
}

// poolSampling is a zone rig whose fleet row says pool and whose wwd answers with the facts the test sets (nil: no facts).
type poolSampling struct {
	*zoneRig
	s     *procSampler
	facts atomic.Pointer[wire.Facts]
	dir   string
}

func newPoolSampling(t *testing.T) *poolSampling {
	t.Helper()
	r := newZoneRig(t)
	p := &poolSampling{zoneRig: r, s: &procSampler{}, dir: t.TempDir()}
	r.m.sampler = p.s
	r.m.gateConfig = nil
	r.m.pool = &poolHost{now: r.m.now, pool: func(context.Context) (wire.Pool, error) {
		f := p.facts.Load()
		if f == nil {
			return wire.Pool{}, nil
		}
		return poolOf(f), nil
	}}
	row := hostconfig.FleetConfig{Source: hostconfig.FleetSourcePool}
	r.m.fleetSrc = &fleetSource{rev: func() (int64, error) { return 1, nil }, cfg: func() (hostconfig.FleetConfig, int64, error) { return row, 1, nil }}
	return p
}

func (p *poolSampling) setFacts() {
	f := factsAt(p.m.now())
	p.facts.Store(f)
}

// With the pool as the source and a fresh reading of this host in wwd, the sampler reads the process table and nothing of the
// host (no sysctl, no vm_stat), and the snapshot's host is wwd's: its zone, its figures.
func TestPoolSampling_ReadsOnlyProcessesWhenWWDHasTheHost(t *testing.T) {
	p := newPoolSampling(t)
	p.setFacts()
	p.step()
	require.False(t, p.m.tick(context.Background()))

	assert.Zero(t, p.s.host.Load(), "no host-level read")
	assert.EqualValues(t, 1, p.s.procs.Load(), "the process table is still read")
	s := p.m.latest.Load()
	require.True(t, s.Available)
	assert.Equal(t, "warning", s.Host.Zone, "wwd's zone")
	assert.Equal(t, 10, s.Host.NCPU)
	assert.Equal(t, 3.0, s.Host.PageRate)
	assert.Equal(t, uint64(16000)<<20, s.Host.MemBytes)
}

// Facts that wwd cannot give (none, or too old) leave the sampler as it was: the whole host is read.
func TestPoolSampling_FallsBackToTheOwnReadWithoutFacts(t *testing.T) {
	p := newPoolSampling(t)
	p.step()
	require.False(t, p.m.tick(context.Background()))
	assert.EqualValues(t, 1, p.s.host.Load(), "no facts: the own sample")
	assert.Zero(t, p.s.procs.Load())

	old := factsAt(p.m.now().Add(-time.Minute))
	p.facts.Store(old)
	p.step()
	require.False(t, p.m.tick(context.Background()))
	assert.EqualValues(t, 2, p.s.host.Load(), "stale facts are no reading")

	p.setFacts()
	p.step()
	require.False(t, p.m.tick(context.Background()))
	assert.EqualValues(t, 2, p.s.host.Load(), "fresh again: no further host read")
	assert.EqualValues(t, 1, p.s.procs.Load())
}

// A source that is not the pool never uses wwd's facts.
func TestPoolSampling_SshSourceKeepsTheOwnRead(t *testing.T) {
	p := newPoolSampling(t)
	p.setFacts()
	row := hostconfig.FleetConfig{Source: "ssh"}
	p.m.fleetSrc = &fleetSource{rev: func() (int64, error) { return 1, nil }, cfg: func() (hostconfig.FleetConfig, int64, error) { return row, 1, nil }}
	p.step()
	require.False(t, p.m.tick(context.Background()))
	assert.EqualValues(t, 1, p.s.host.Load())
	assert.Zero(t, p.s.procs.Load())
}

// A sampler that cannot read the process table alone (no SampleProcs) keeps the whole read, pool or not.
func TestPoolSampling_SamplerWithoutProcsReadKeepsTheOwnRead(t *testing.T) {
	p := newPoolSampling(t)
	fs := &fakeSampler{}
	p.m.sampler = fs
	p.setFacts()
	p.step()
	require.False(t, p.m.tick(context.Background()))
	assert.EqualValues(t, 1, fs.calls.Load())
}

// The pool tick writes no host_minutes (the report reads wwd's from its first minute on) and ends the minute that was open.
func TestPoolSampling_WritesNoHostMinutes(t *testing.T) {
	p := newPoolSampling(t)
	st, err := openResourcesStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	p.m.store = st

	// Own reading first: a minute is open.
	p.step()
	require.False(t, p.m.tick(context.Background()))
	require.True(t, p.m.minute.open, "the own tick opened a minute")

	p.setFacts()
	for i := 0; i < 30; i++ { // 150 s: two minute borders
		p.step()
		p.setFacts()
		require.False(t, p.m.tick(context.Background()))
	}
	assert.False(t, p.m.minute.open, "entering the pool ended the open minute")
	rows, err := st.ReportMinutes(0, p.m.now().Add(time.Hour).UnixMilli())
	require.NoError(t, err)
	assert.LessOrEqual(t, len(rows), 1, "only the minute of the own reading was written")
}

// The gate's pressure cache is fed from wwd's facts: the level, and counters that add up to wwd's page-out rate.
func TestPoolSampling_FeedsTheGateCacheFromFacts(t *testing.T) {
	p := newPoolSampling(t)
	cfg := gate.Defaults()
	cfg.Dir = p.dir
	p.m.gateConfig = func() gate.Config { return cfg }
	for i := 0; i < 3; i++ {
		p.step()
		p.setFacts()
		require.False(t, p.m.tick(context.Background()))
	}
	got := hostmem.Cache{Dir: p.dir}.Load().Samples
	require.Len(t, got, 3)
	assert.Equal(t, 2, got[2].Level, "wwd's pressure level")
	dt := got[2].At - got[0].At
	total := func(s hostmem.Sample) uint64 { return s.Pageouts + s.Swapouts }
	rate := float64(total(got[2])-total(got[0])) / (float64(dt) / 1000)
	assert.InDelta(t, 3.0, rate, 0.01, "the counters give wwd's rate (2 + 1 per second)")
}

// The counter goes on from the cache's own last one, whoever wrote it: another daemon's or a gate's high counter is not stepped
// back to a private one (codex attack).
func TestPoolSampling_CounterGoesOnFromTheSharedCache(t *testing.T) {
	p := newPoolSampling(t)
	cfg := gate.Defaults()
	cfg.Dir = p.dir
	p.m.gateConfig = func() gate.Config { return cfg }
	p.step()
	require.True(t, hostmem.Cache{Dir: p.dir}.Record(p.m.now(), hostmem.Sample{At: p.m.now().UnixMilli(), Level: 2, Pageouts: 900000, Swapouts: 100}))
	p.step()
	p.setFacts()
	require.False(t, p.m.tick(context.Background()))
	got := hostmem.Cache{Dir: p.dir}.Load().Samples
	require.Len(t, got, 2)
	assert.EqualValues(t, 900100+15, got[1].Pageouts+got[1].Swapouts, "the other writer's counter plus 5 s of 3 a second")
}

// After a long gap (the cache's last reading is minutes old) the rate is not taken to have held over it: the counter goes on
// from where it was.
func TestPoolSampling_NoCounterGrowthOverALongGap(t *testing.T) {
	p := newPoolSampling(t)
	cfg := gate.Defaults()
	cfg.Dir = p.dir
	p.m.gateConfig = func() gate.Config { return cfg }
	old := p.m.now().Add(-5 * time.Minute)
	require.True(t, hostmem.Cache{Dir: p.dir}.Record(old, hostmem.Sample{At: old.UnixMilli(), Level: 2, Pageouts: 1000}))
	p.step()
	p.setFacts()
	require.False(t, p.m.tick(context.Background()))
	got := hostmem.Cache{Dir: p.dir}.Load().Samples
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.EqualValues(t, 1000, last.Pageouts+last.Swapouts, "3 pages a second over 5 minutes is not added")
}

// The processes failing does not take the host reading with them: wwd's figures stay published and available, without sessions.
func TestPoolSampling_ProcessTableFailureKeepsTheHostReading(t *testing.T) {
	p := newPoolSampling(t)
	p.s.procsErr.Store(true)
	for i := 0; i < 5; i++ {
		p.step()
		p.setFacts()
		require.False(t, p.m.tick(context.Background()))
	}
	s := p.m.latest.Load()
	require.True(t, s.Available, "five failed ps reads, host still available")
	assert.Equal(t, "warning", s.Host.Zone)
	assert.Empty(t, s.Sessions)
}

// Back from wwd's reading, the zone and its start are the own sampler's again, not what the latch held before the pool.
func TestPoolSampling_LeavingThePoolRestartsTheZone(t *testing.T) {
	p := newPoolSampling(t)
	own := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return zoneRaw(40, 1, 4), nil, nil // pressure 4: danger by the own reading
	}}
	p.m.sampler = own
	p.step()
	require.False(t, p.m.tick(context.Background()))
	first := p.m.latest.Load().Host
	require.Equal(t, "danger", first.Zone)

	p.m.sampler = p.s
	for i := 0; i < 3; i++ {
		p.step()
		p.setFacts()
		require.False(t, p.m.tick(context.Background()))
	}
	p.m.sampler = own
	p.facts.Store(nil)
	p.step()
	require.False(t, p.m.tick(context.Background()))
	back := p.m.latest.Load().Host
	assert.Equal(t, "danger", back.Zone)
	assert.Greater(t, back.ZoneSince, first.ZoneSince, "the zone began again when the own reading came back")
}

// The trend window does not run across the time wwd's reading stood in: the samples from before the pool are dropped.
func TestPoolSampling_LeavingThePoolEmptiesTheTrendRing(t *testing.T) {
	p := newPoolSampling(t)
	own := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return zoneRaw(40, 1, 1), nil, nil
	}}
	p.m.sampler = own
	for i := 0; i < 4; i++ {
		p.step()
		require.False(t, p.m.tick(context.Background()))
	}
	require.Len(t, p.m.ringCopy(), 4)

	p.m.sampler = p.s
	p.step()
	p.setFacts()
	require.False(t, p.m.tick(context.Background()))
	assert.Len(t, p.m.ringCopy(), 4, "wwd's reading adds nothing, and the ring is not touched while it stands in")

	p.m.sampler = own
	p.facts.Store(nil)
	p.step()
	require.False(t, p.m.tick(context.Background()))
	assert.Len(t, p.m.ringCopy(), 1, "only the first own reading after the pool")
}

// A paging rate under one page per tick still adds up: the fractions are carried, not cut off each tick (codex R1).
func TestPoolSampling_SlowRateIsNotTruncated(t *testing.T) {
	p := newPoolSampling(t)
	cfg := gate.Defaults()
	cfg.Dir = p.dir
	p.m.gateConfig = func() gate.Config { return cfg }
	for i := 0; i < 11; i++ {
		p.step()
		f := factsAt(p.m.now())
		slow, none := 0.1, 0.0
		f.Mem.PageOutRate, f.Mem.SwapOutRate = &slow, &none
		p.facts.Store(f)
		require.False(t, p.m.tick(context.Background()))
	}
	got := hostmem.Cache{Dir: p.dir}.Load().Samples
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.EqualValues(t, 5, last.Pageouts+last.Swapouts, "ten intervals of 5 s at 0.1 a second")
}

// The summed pcpu of the process table is still published in pool mode (the P3 calibration reads pcpu_total).
func TestPoolSampling_KeepsPcpuTotal(t *testing.T) {
	p := newPoolSampling(t)
	p.setFacts()
	p.step()
	require.False(t, p.m.tick(context.Background()))
	assert.InDelta(t, 0.7, p.m.latest.Load().Host.PcpuTotal, 0.001, "7 % of one core on 10 cores")
}

// The reading the session attribution and the watcher take from the facts is usable and carries wwd's figures.
func TestRawOfFacts(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	raw := rawOfFacts(*factsAt(now), []resources.Proc{{PID: 1, Pcpu: 7}, {PID: 2, Pcpu: 3}})
	assert.True(t, raw.Usable())
	assert.Equal(t, 10, raw.NCPU)
	assert.Equal(t, uint64(16000)<<20, raw.MemBytes)
	assert.Equal(t, 2, raw.Pressure)
	assert.Equal(t, 5.0, raw.Load1)
	assert.Equal(t, 10.0, raw.PcpuSum)
	assert.False(t, raw.PageCounters, "no counters are made up")
}

// The facts' counter starts where the own sampler's last one was, so going back to the own reading is not a jump.
func TestPoolSampling_CounterContinuesFromTheOwnReading(t *testing.T) {
	p := newPoolSampling(t)
	cfg := gate.Defaults()
	cfg.Dir = p.dir
	p.m.gateConfig = func() gate.Config { return cfg }
	p.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return withPages(zoneRaw(40, 1, 2), 5000), nil, nil
	}}
	p.step()
	require.False(t, p.m.tick(context.Background())) // no facts: the own reading, counter 5000
	p.m.sampler = p.s
	p.step()
	p.setFacts()
	require.False(t, p.m.tick(context.Background()))
	got := hostmem.Cache{Dir: p.dir}.Load().Samples
	require.Len(t, got, 2)
	assert.EqualValues(t, 5000+15, got[1].Pageouts+got[1].Swapouts, "5000 plus 5 s of 3 pages a second")
}

// Facts without a pressure level, or without any paging rate, write nothing to the gate's cache.
func TestPoolSampling_NothingCachedWithoutLevelOrRate(t *testing.T) {
	for name, mut := range map[string]func(f *wire.Facts){
		"no level": func(f *wire.Facts) { f.Mem.Pressure = 0 },
		"no rate":  func(f *wire.Facts) { f.Mem.PageOutRate, f.Mem.SwapOutRate = nil, nil },
	} {
		p := newPoolSampling(t)
		cfg := gate.Defaults()
		cfg.Dir = p.dir
		p.m.gateConfig = func() gate.Config { return cfg }
		p.step()
		f := factsAt(p.m.now())
		mut(f)
		p.facts.Store(f)
		require.False(t, p.m.tick(context.Background()), name)
		assert.EqualValues(t, 1, p.s.procs.Load(), name+": the facts were used")
		assert.Empty(t, hostmem.Cache{Dir: p.dir}.Load().Samples, name)
	}
}
