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
}

func (o *ownHost) CollectCPU(context.Context) (HostCPUSample, error) {
	return HostCPUSample{Idle: 1, Total: 2}, nil
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
		Disk: []wire.Disk{{Mount: "/data", FreeGB: 1, UsedPct: 10}, {Mount: "/", FreeGB: 100, UsedPct: 50}},
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
	assert.InDelta(t, 200.0*(1<<30), float64(*m.Disk.TotalBytes), 1<<20, "free 100 GB at 50 % used = 200 GB")
	assert.InDelta(t, 100.0*(1<<30), float64(*m.Disk.UsedBytes), 1<<20)
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
	for name, ds := range map[string][]wire.Disk{
		"none":       nil,
		"zero used":  {{Mount: "/", FreeGB: 10, UsedPct: 0}},
		"full":       {{Mount: "/", FreeGB: 0, UsedPct: 100}},
		"negative":   {{Mount: "/", FreeGB: -1, UsedPct: 10}},
		"over 100 %": {{Mount: "/", FreeGB: 1, UsedPct: 120}},
	} {
		_, ok := diskSampleOf(ds)
		assert.False(t, ok, name)
	}
	s, ok := diskSampleOf([]wire.Disk{{Mount: "/a", FreeGB: 1, UsedPct: 50}})
	require.True(t, ok)
	assert.InDelta(t, 2.0*(1<<30), float64(s.TotalBytes), 1<<10, "no root mount: the first")
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
