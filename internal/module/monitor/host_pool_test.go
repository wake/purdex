package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/wwd/wire"
)

// ownHost is the collector the monitor always had, with figures nobody could mistake for wwd's.
type ownHost struct {
	closed  bool
	cpuSeen int
	reads   uint64
}

// CollectCPU counts up, so that two reads always have a delta (a constant counter would read "pending" for ever).
func (o *ownHost) CollectCPU(context.Context) (HostCPUSample, error) {
	o.reads++
	return HostCPUSample{Idle: 10 * o.reads, Total: 20 * o.reads}, nil
}
func (o *ownHost) CollectMemory(context.Context) (HostMemorySample, error) {
	return HostMemorySample{TotalBytes: 1000, UsedBytes: 100}, nil
}
func (o *ownHost) CollectDisk(context.Context) (HostDiskSample, error) {
	return HostDiskSample{TotalBytes: 2000, UsedBytes: 200}, nil
}
func (o *ownHost) Close() { o.closed = true }

// ownPercentHost reports the CPU percentage itself, like the darwin collector.
type ownPercentHost struct{ ownHost }

func (o *ownPercentHost) CPUPercent(time.Duration) (float64, error) { o.cpuSeen++; return 7, nil }

func factsOK() (wire.Facts, bool) {
	return wire.Facts{
		CPU:  wire.CPU{NCPU: 10, IdlePct: 70},
		Mem:  wire.Mem{TotalMB: 16000, AvailableMB: 4000},
		Disk: []wire.Disk{{Mount: "/data", TotalBytes: 1 << 30, AvailBytes: 1 << 29}, {Mount: "/", TotalBytes: 200 << 30, AvailBytes: 100 << 30}},
	}, true
}

func factsNone() (wire.Facts, bool) { return wire.Facts{}, false }

// Pool source: CPU, memory and disk are wwd's. The disk's size is worked out from the free space and the used share, and the
// root mount is the one shown.
func TestPoolHostCollector_ReadsTheFacts(t *testing.T) {
	c := newPoolHostCollector(&ownHost{}, factsOK)
	m := collectHostMetrics(context.Background(), NewHostMetricsState(c))
	require.NotNil(t, m.CPU.Percent)
	assert.InDelta(t, 30.0, *m.CPU.Percent, 0.001, "100 - idle")
	assert.Equal(t, uint64(16000)<<20, *m.Memory.TotalBytes)
	assert.Equal(t, uint64(12000)<<20, *m.Memory.UsedBytes)
	require.NotNil(t, m.Disk.TotalBytes)
	assert.Equal(t, uint64(200)<<30, *m.Disk.TotalBytes, "statfs's total, exactly")
	assert.Equal(t, uint64(100)<<30, *m.Disk.UsedBytes, "total less what a user can still take")
	assert.InDelta(t, 50.0, *m.Disk.UsedPercent, 0.01)
}

// No fresh facts (or the source is not the pool): every figure is the collector's own.
func TestPoolHostCollector_FallsBackToTheOwnCollector(t *testing.T) {
	c := newPoolHostCollector(&ownHost{}, factsNone)
	state := NewHostMetricsState(c)
	m := collectHostMetrics(context.Background(), state)
	assert.Equal(t, uint64(1000), *m.Memory.TotalBytes)
	assert.Equal(t, uint64(2000), *m.Disk.TotalBytes)
	require.NotNil(t, m.CPU.UnavailableReason, "the delta collector's first CPU read is pending")
	assert.Equal(t, hostCPUPendingReason, *m.CPU.UnavailableReason)
}

// A fallback that reports the percentage itself keeps doing so through the wrapper, and wwd's CPU wins only while it is there.
func TestPoolHostCollector_KeepsAPercentSourceFallback(t *testing.T) {
	own := &ownPercentHost{}
	have := true
	c := newPoolHostCollector(own, func() (wire.Facts, bool) {
		if have {
			return factsOK()
		}
		return factsNone()
	})
	_, ok := c.(hostCPUPercentSource)
	require.True(t, ok, "the wrapper still reports the percentage")
	state := NewHostMetricsState(c)
	m := collectHostMetrics(context.Background(), state)
	assert.InDelta(t, 30.0, *m.CPU.Percent, 0.001)
	assert.Zero(t, own.cpuSeen, "wwd's CPU is used; the own sampler is not asked")
	have = false
	m = collectHostMetrics(context.Background(), state)
	assert.Equal(t, 7.0, *m.CPU.Percent)
	assert.Equal(t, 1, own.cpuSeen)
}

// A fallback without a percentage source does not gain one.
func TestPoolHostCollector_NoPercentSourceFromADeltaFallback(t *testing.T) {
	_, ok := newPoolHostCollector(&ownHost{}, factsNone).(hostCPUPercentSource)
	assert.False(t, ok)
}

func TestPoolHostCollector_CloseReachesTheFallback(t *testing.T) {
	own := &ownHost{}
	newPoolHostCollector(own, factsNone).(interface{ Close() }).Close()
	assert.True(t, own.closed)
}

func TestDiskSampleOf(t *testing.T) {
	// A facts record from a wwd that does not send the sizes yet (older than the sizes), no disk at all, and a size that does not
	// add up are left to the monitor's own collector.
	for name, ds := range map[string][]wire.Disk{
		"none":              nil,
		"no sizes yet":      {{Mount: "/", FreeGB: 10, UsedPct: 5}},
		"avail over total":  {{Mount: "/", TotalBytes: 100, AvailBytes: 101}},
		"zero total, avail": {{Mount: "/", TotalBytes: 0, AvailBytes: 5}},
		"no root mount":     {{Mount: "/home", TotalBytes: 1000, AvailBytes: 500}},
	} {
		_, ok := diskSampleOf(ds)
		assert.False(t, ok, name)
	}
	// statfs as it was: total is Blocks x Bsize, used is total less what a user can still take (Bavail), so reserved blocks count
	// as used exactly as the monitor's own collector counts them.
	s, ok := diskSampleOf([]wire.Disk{{Mount: "/a", TotalBytes: 1000, AvailBytes: 200}, {Mount: "/", TotalBytes: 500, AvailBytes: 100}})
	require.True(t, ok)
	assert.Equal(t, HostDiskSample{TotalBytes: 500, UsedBytes: 400}, s, "the root mount")
	s, ok = diskSampleOf([]wire.Disk{{Mount: "/", TotalBytes: 1000, AvailBytes: 1000}})
	require.True(t, ok, "an empty disk is a valid reading")
	assert.Equal(t, HostDiskSample{TotalBytes: 1000, UsedBytes: 0}, s)
}

type fakeLocalFacts struct{}

func (fakeLocalFacts) LatestLocalFacts() (wire.Facts, bool) { return factsOK() }

// Init wraps the monitor's own host collector (and only that one), and the facts come from the resources module's registered
// reader, looked up on each call.
func TestModule_WrapsTheDefaultHostCollectorAndLooksTheFactsUp(t *testing.T) {
	c := core.New(core.CoreDeps{Registry: core.NewServiceRegistry()})
	m := New()
	require.True(t, m.defaultHost)
	require.NoError(t, m.Init(c))
	_, wrapped := m.collectors.HostCollector.(*poolHostCollector)
	_, wrappedPercent := m.collectors.HostCollector.(*poolPercentCollector)
	assert.True(t, wrapped || wrappedPercent)
	_, ok := m.localFacts()
	assert.False(t, ok, "no resources module yet")
	c.Registry.Register(resources.LocalFactsKey, fakeLocalFacts{})
	f, ok := m.localFacts()
	require.True(t, ok)
	assert.Equal(t, 16000, f.Mem.TotalMB)

	own := &ownHost{}
	injected := New(WithCollectors(Collectors{HostCollector: own}))
	require.False(t, injected.defaultHost)
	require.NoError(t, injected.Init(c))
	assert.Same(t, own, injected.collectors.HostCollector, "a test's collector is left alone")
}

// Leaving the pool source starts the own delta collector afresh: its first reading is pending, not an average over the whole
// time wwd was the source.
func TestPoolHostCollector_TheDeltaBaselineRestartsAfterWWD(t *testing.T) {
	have := false
	c := newPoolHostCollector(&ownHost{}, func() (wire.Facts, bool) {
		if have {
			return factsOK()
		}
		return factsNone()
	})
	state := NewHostMetricsState(c)
	m := collectHostMetrics(context.Background(), state)
	require.Equal(t, hostCPUPendingReason, *m.CPU.UnavailableReason, "first own reading: pending")
	have = true
	m = collectHostMetrics(context.Background(), state)
	require.NotNil(t, m.CPU.Percent, "wwd's reading")
	have = false
	m = collectHostMetrics(context.Background(), state)
	require.NotNil(t, m.CPU.UnavailableReason)
	assert.Equal(t, hostCPUPendingReason, *m.CPU.UnavailableReason, "the own baseline starts over")
}

// One snapshot has one source: a source that flips between the three reads of a snapshot does not mix them.
func TestPoolHostCollector_ASnapshotHasOneSource(t *testing.T) {
	var asked int
	c := newPoolHostCollector(&ownHost{}, func() (wire.Facts, bool) {
		asked++
		if asked == 1 { // the first look says pool, every later one says not
			return factsOK()
		}
		return factsNone()
	})
	m := collectHostMetrics(context.Background(), NewHostMetricsState(c))
	require.NotNil(t, m.CPU.Percent, "wwd's CPU")
	assert.Equal(t, uint64(16000)<<20, *m.Memory.TotalBytes, "and wwd's memory, not the own collector's")
	assert.Equal(t, uint64(200)<<30, *m.Disk.TotalBytes, "and wwd's disk")
	assert.Equal(t, 1, asked, "decided once")
}

// Facts that cannot give every figure (a wwd older than the disk sizes) give none: the whole snapshot is the own collector's, not
// wwd's CPU and memory with a local disk, and the facts are looked up once.
func TestPoolHostCollector_UnusableFactsMeanOneFallbackSource(t *testing.T) {
	asked := 0
	c := newPoolHostCollector(&ownHost{}, func() (wire.Facts, bool) {
		asked++
		f, _ := factsOK()
		f.Disk = []wire.Disk{{Mount: "/", FreeGB: 100, UsedPct: 50}} // no sizes yet
		return f, true
	})
	m := collectHostMetrics(context.Background(), NewHostMetricsState(c))
	assert.Equal(t, uint64(1000), *m.Memory.TotalBytes, "the own collector's memory, not wwd's")
	assert.Equal(t, uint64(2000), *m.Disk.TotalBytes)
	require.NotNil(t, m.CPU.UnavailableReason, "and the own CPU (pending on its first read), not wwd's")
	assert.Equal(t, 1, asked)

	asked = 0
	c = newPoolHostCollector(&ownHost{}, func() (wire.Facts, bool) {
		asked++
		f, _ := factsOK()
		f.Mem.TotalMB = 0
		return f, true
	})
	m = collectHostMetrics(context.Background(), NewHostMetricsState(c))
	assert.Equal(t, uint64(2000), *m.Disk.TotalBytes, "no memory total either: all of it is the own collector's")
	assert.Equal(t, 1, asked)
}

// A CPU that could not be read reaches the facts as zeros: that is not an idle of 0 % (a saturated host), it is no CPU, and the
// snapshot is the own collector's as a whole.
func TestPoolHostCollector_ACPUThatWasNotReadMeansTheOwnCollector(t *testing.T) {
	for name, cpu := range map[string]wire.CPU{
		"zeros":        {},
		"no cpu count": {IdlePct: 40},
		"idle over":    {NCPU: 4, IdlePct: 101},
		"idle under":   {NCPU: 4, IdlePct: -1},
	} {
		c := newPoolHostCollector(&ownHost{}, func() (wire.Facts, bool) {
			f, _ := factsOK()
			f.CPU = cpu
			return f, true
		})
		m := collectHostMetrics(context.Background(), NewHostMetricsState(c))
		assert.Equal(t, uint64(1000), *m.Memory.TotalBytes, name)
		assert.Equal(t, uint64(2000), *m.Disk.TotalBytes, name)
		assert.Nil(t, m.CPU.Percent, name)
	}
	c := newPoolHostCollector(&ownHost{}, func() (wire.Facts, bool) { // a saturated host with its CPU read: 0 % idle is a reading
		f, _ := factsOK()
		f.CPU = wire.CPU{NCPU: 4, IdlePct: 0}
		return f, true
	})
	m := collectHostMetrics(context.Background(), NewHostMetricsState(c))
	require.NotNil(t, m.CPU.Percent)
	assert.Equal(t, 100.0, *m.CPU.Percent)
}
