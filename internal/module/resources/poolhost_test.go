package resourcesmod

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/wwd/wire"
)

func factsAt(at time.Time) *wire.Facts {
	po, so := 2.0, 1.0
	return &wire.Facts{
		V: wire.Proto, Alias: "mlab", At: at, Platform: "darwin",
		CPU:     wire.CPU{NCPU: 10, Load1: 5, Load5: 4},
		Mem:     wire.Mem{TotalMB: 16000, AvailableMB: 4000, Pressure: 2, PageOutRate: &po, SwapOutRate: &so},
		Derived: wire.Derived{Zone: wire.ZoneWarning, SessionsRoom: 3},
	}
}

func newPoolHostT(now time.Time, get func(context.Context) (wire.Pool, error)) (*poolHost, *time.Time) {
	clock := now
	return &poolHost{pool: get, now: func() time.Time { return clock }}, &clock
}

func poolOf(f *wire.Facts) wire.Pool {
	return wire.Pool{Peers: []wire.Peer{{Info: wire.Info{Alias: "mlab"}, Facts: f, Reachable: true}}}
}

// The mapping: wwd's judgement (the zone) is passed through, the figures are converted, and what facts do not carry (trend,
// zone start, 15-minute load) stays unknown/zero instead of being worked out here.
func TestPoolHost_MapsFactsAndLeavesWhatWWDDoesNotCarry(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	p, _ := newPoolHostT(now, func(context.Context) (wire.Pool, error) { return poolOf(factsAt(now)), nil })
	d, ok := p.detail()
	require.True(t, ok)
	h := d.Host
	assert.Equal(t, "warning", h.Zone, "wwd's zone")
	assert.Equal(t, "unknown", h.Trend, "no trend is worked out here")
	assert.Zero(t, h.ZoneSince)
	assert.Zero(t, h.Load15)
	assert.Equal(t, 5.0, h.Load1)
	assert.Equal(t, 4.0, h.Load5)
	assert.Equal(t, 10, h.NCPU)
	assert.Equal(t, 50.0, h.CPU, "load1 / ncpu as a percentage")
	assert.Equal(t, 75.0, h.Mem)
	assert.Equal(t, 2, h.Pressure)
	assert.Equal(t, 3.0, h.PageRate, "pageouts + swapouts")
	assert.Equal(t, uint64(16000)<<20, h.MemBytes)
	assert.Equal(t, uint64(12000)<<20, h.MemUsedBytes)
	assert.True(t, h.Full, "pressure 2")
	assert.Equal(t, now.UnixMilli(), d.SampledAt)
	require.NotNil(t, d.SessionsRoom)
	assert.Equal(t, 3, *d.SessionsRoom, "the derived room rides along")
}

// No facts, an unreachable wwd, an empty pool and facts that are too old all give no reading (the caller falls back).
func TestPoolHost_NoFreshReading(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for name, get := range map[string]func(context.Context) (wire.Pool, error){
		"wwd down":   func(context.Context) (wire.Pool, error) { return wire.Pool{}, errors.New("down") },
		"empty pool": func(context.Context) (wire.Pool, error) { return wire.Pool{}, nil },
		"no facts": func(context.Context) (wire.Pool, error) {
			return wire.Pool{Peers: []wire.Peer{{Info: wire.Info{Alias: "mlab"}}}}, nil
		},
		"stale": func(context.Context) (wire.Pool, error) { return poolOf(factsAt(now.Add(-poolHostStale))), nil },
	} {
		p, _ := newPoolHostT(now, get)
		_, ok := p.detail()
		assert.False(t, ok, name)
	}
}

// One pool read serves the readers within the TTL, and a new one is made after it.
func TestPoolHost_CachesForTheTTL(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	p, clock := newPoolHostT(now, func(context.Context) (wire.Pool, error) { calls.Add(1); return poolOf(factsAt(now)), nil })
	for i := 0; i < 5; i++ {
		_, _ = p.detail()
	}
	assert.EqualValues(t, 1, calls.Load())
	*clock = now.Add(poolHostTTL)
	_, _ = p.detail()
	assert.EqualValues(t, 2, calls.Load())
}

// A slow wwd costs the readers ONE wait, not one each: the read that ends later than the TTL after it began still answers the
// readers that queued behind it.
func TestPoolHost_ASlowReadIsSharedByTheWaiters(t *testing.T) {
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	var clock atomic.Int64 // ns after start
	var calls atomic.Int32
	p := &poolHost{
		now: func() time.Time { return start.Add(time.Duration(clock.Load())) },
		pool: func(context.Context) (wire.Pool, error) {
			calls.Add(1)
			clock.Add(int64(1500 * time.Millisecond)) // the read takes longer than the TTL
			return poolOf(factsAt(start.Add(1500 * time.Millisecond))), nil
		},
	}
	done := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		go func() { _, ok := p.detail(); done <- ok }()
	}
	for i := 0; i < 8; i++ {
		assert.True(t, <-done)
	}
	assert.EqualValues(t, 1, calls.Load(), "one read for all eight readers")
}

// A reading served from the cache is judged by its age now: facts that turn 20 s old inside the TTL stop being a reading.
func TestPoolHost_ACachedReadingStillExpires(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	p, clock := newPoolHostT(now, func(context.Context) (wire.Pool, error) {
		return poolOf(factsAt(now.Add(-poolHostStale + 200*time.Millisecond))), nil // 19.8 s old when read
	})
	_, ok := p.detail()
	require.True(t, ok)
	*clock = now.Add(500 * time.Millisecond) // inside the TTL, but the facts are 20.3 s old
	_, ok = p.detail()
	assert.False(t, ok)
}

type fakeFleetRow struct {
	cfg hostconfig.FleetConfig
	rev int64
	n   atomic.Int32
}

func (f *fakeFleetRow) FleetConfig() (hostconfig.FleetConfig, int64, error) {
	f.n.Add(1)
	return f.cfg, f.rev, nil
}
func (f *fakeFleetRow) FleetRevision() (int64, error) { return f.rev, nil }

// The source is read through the revision: the row is read again only when it moved, and a flip is seen without a restart.
func TestFleetSource_FollowsTheRowByRevision(t *testing.T) {
	row := &fakeFleetRow{cfg: hostconfig.FleetConfig{Source: hostconfig.FleetSourcePool}, rev: 1}
	s := &fleetSource{cfg: row.FleetConfig, rev: row.FleetRevision}
	assert.True(t, s.isPool())
	assert.True(t, s.isPool())
	assert.EqualValues(t, 1, row.n.Load(), "read once while the revision stays")
	row.cfg.Source, row.rev = hostconfig.FleetSourceSSH, 2
	assert.False(t, s.isPool(), "flipped without a restart")
	row.cfg.Source = ""
	row.rev = 3
	assert.False(t, s.isPool(), "left out = ssh")
	var none *fleetSource
	assert.False(t, none.isPool(), "no hostconfig = the own sample")
}

// LatestLocalFacts (the monitor's input) follows the same source and gives the facts as wwd sent them.
func TestModule_LatestLocalFactsFollowsTheSource(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	row := &fakeFleetRow{cfg: hostconfig.FleetConfig{Source: hostconfig.FleetSourcePool}, rev: 1}
	m := &Module{
		pool: &poolHost{now: func() time.Time { return now }, pool: func(context.Context) (wire.Pool, error) {
			return poolOf(factsAt(now)), nil
		}},
		fleetSrc: &fleetSource{cfg: row.FleetConfig, rev: row.FleetRevision},
	}
	f, ok := m.LatestLocalFacts()
	require.True(t, ok)
	assert.Equal(t, 16000, f.Mem.TotalMB)
	assert.Equal(t, "mlab", f.Alias)

	row.cfg.Source, row.rev = hostconfig.FleetSourceSSH, 2
	_, ok = m.LatestLocalFacts()
	assert.False(t, ok, "ssh source: nothing")
	_, ok = (&Module{}).LatestLocalFacts()
	assert.False(t, ok, "a module without the pool reader gives nothing")
}

// The module answers from wwd only while the row says pool and wwd has a reading; otherwise from its own sample.
func TestModule_LatestHostFollowsTheSource(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	row := &fakeFleetRow{cfg: hostconfig.FleetConfig{Source: hostconfig.FleetSourcePool}, rev: 1}
	down := false
	m := &Module{
		pool: &poolHost{now: func() time.Time { return now }, pool: func(context.Context) (wire.Pool, error) {
			if down {
				return wire.Pool{}, errors.New("down")
			}
			return poolOf(factsAt(now)), nil
		}},
		fleetSrc: &fleetSource{cfg: row.FleetConfig, rev: row.FleetRevision},
	}
	own := &resources.Snapshot{Available: true, SampledAt: now, Host: resources.HostUse{Zone: "normal", Trend: "flat", Load15: 9}}
	m.latest.Store(own)

	h, ok := m.LatestHost()
	require.True(t, ok)
	assert.Equal(t, "warning", h.Zone, "pool source: wwd's reading")
	d, ok := m.LatestHostDetail()
	require.True(t, ok)
	assert.NotNil(t, d.SessionsRoom)

	m.pool.have = false // let the next call read again
	down = true
	h, ok = m.LatestHost()
	require.True(t, ok)
	assert.Equal(t, "normal", h.Zone, "wwd gives nothing: the own sample")
	d, _ = m.LatestHostDetail()
	assert.Nil(t, d.SessionsRoom)

	m.pool.have = false
	down = false
	row.cfg.Source, row.rev = hostconfig.FleetSourceSSH, 2
	h, _ = m.LatestHost()
	assert.Equal(t, "normal", h.Zone, "ssh source: the own sample even though wwd answers")
}

// Init wires the pool reader when the hostconfig module is there, and leaves the own sample alone when it is not.
func TestFindPoolHost_NeedsTheFleetRow(t *testing.T) {
	c := core.New(core.CoreDeps{Registry: core.NewServiceRegistry()})
	m := New()
	m.findPoolHost(c)
	assert.Nil(t, m.pool, "no hostconfig: nothing wired")

	c.Registry.Register(hostconfig.FleetConfigKey, &fakeFleetRow{})
	m.findPoolHost(c)
	require.NotNil(t, m.pool)
	require.NotNil(t, m.fleetSrc)
	assert.NotNil(t, m.fleetSrc.rev, "the revision is the cheap look")
}
