package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

func TestSnapshot_ReusesCachedSnapshotWithinRefreshInterval(t *testing.T) {
	hostCollector := newFakeHostCollector()
	clock := newFakeClock(time.Unix(100, 0))
	m := New(WithCollectors(Collectors{
		HostCollector: hostCollector,
	}), withClock(clock.Now))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{
		Monitor: MonitorConfig{RefreshIntervalMS: 5000, TopProcessLimit: 10},
	}})))

	first := requestSnapshot(t, m)
	second := requestSnapshot(t, m)

	assert.Equal(t, first.SampledAt, second.SampledAt, "fresh repeated requests should return the cached snapshot")
	assert.Equal(t, 1, hostCollector.cpuCalls, "host cpu should be sampled once for repeated fresh requests")
	assert.Equal(t, 1, hostCollector.memoryCalls, "host memory should be sampled once for repeated fresh requests")
	assert.Equal(t, 1, hostCollector.diskCalls, "host disk should be sampled once for repeated fresh requests")
}

func TestSnapshot_RecollectsWhenCacheExpires(t *testing.T) {
	hostCollector := newFakeHostCollector()
	clock := newFakeClock(time.Unix(100, 0))
	m := New(WithCollectors(Collectors{
		HostCollector: hostCollector,
	}), withClock(clock.Now))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{
		Monitor: MonitorConfig{RefreshIntervalMS: 5000, TopProcessLimit: 10},
	}})))

	first := requestSnapshot(t, m)
	clock.Advance(5 * time.Second)
	second := requestSnapshot(t, m)

	assert.NotEqual(t, first.SampledAt, second.SampledAt, "stale requests should collect a new snapshot")
	assert.Equal(t, 2, hostCollector.cpuCalls)
	assert.Equal(t, 2, hostCollector.memoryCalls)
	assert.Equal(t, 2, hostCollector.diskCalls)
}

func TestSnapshot_UsesUpdatedRefreshIntervalForCacheFreshness(t *testing.T) {
	hostCollector := newFakeHostCollector()
	clock := newFakeClock(time.Unix(100, 0))
	cfg := &config.Config{Monitor: MonitorConfig{RefreshIntervalMS: 5000, TopProcessLimit: 10}}
	m := New(WithCollectors(Collectors{
		HostCollector: hostCollector,
	}), withClock(clock.Now))
	c := core.New(core.CoreDeps{Config: cfg})
	require.NoError(t, m.Init(c))

	first := requestSnapshot(t, m)
	c.CfgMu.Lock()
	c.Cfg.Monitor.RefreshIntervalMS = 1000
	c.CfgMu.Unlock()
	clock.Advance(1500 * time.Millisecond)
	second := requestSnapshot(t, m)

	assert.NotEqual(t, first.SampledAt, second.SampledAt, "cache freshness should use the latest effective refresh interval")
	assert.Equal(t, 2, hostCollector.cpuCalls)
	assert.Equal(t, 2, hostCollector.memoryCalls)
	assert.Equal(t, 2, hostCollector.diskCalls)
}

func TestPutMonitorConfigInvalidatesCachedSnapshot(t *testing.T) {
	hostCollector := newFakeHostCollector()
	clock := newFakeClock(time.Unix(100, 0))
	m := New(WithCollectors(Collectors{HostCollector: hostCollector}), withClock(clock.Now))
	c := core.New(core.CoreDeps{Config: &config.Config{
		Monitor: MonitorConfig{RefreshIntervalMS: 5000, TopProcessLimit: 10},
	}})
	require.NoError(t, m.Init(c))

	first := requestSnapshot(t, m)
	require.Equal(t, 1, hostCollector.cpuCalls)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/monitor/config", bytes.NewBufferString(`{"refresh_interval_ms":60000,"top_process_limit":7}`))
	m.handleConfig(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	second := requestSnapshot(t, m)
	assert.NotEqual(t, first.Config.TopProcessLimit, second.Config.TopProcessLimit)
	assert.Equal(t, 7, second.Config.TopProcessLimit)
	assert.Equal(t, 2, hostCollector.cpuCalls, "config updates should invalidate cached snapshots")
}

func TestSnapshot_IncludesHostMetrics(t *testing.T) {
	collector := newFakeHostCollector()
	collector.cpuSamples = []HostCPUSample{{Idle: 80, Total: 100}}
	collector.memory = HostMemorySample{TotalBytes: 1000, UsedBytes: 250}
	collector.disk = HostDiskSample{TotalBytes: 2000, UsedBytes: 500}
	m := New(WithCollectors(Collectors{HostCollector: collector}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{}})))

	snapshot := requestSnapshot(t, m)

	var host HostMetrics
	require.NoError(t, json.Unmarshal(snapshot.Host, &host))
	require.NotNil(t, host.CPU)
	assert.Nil(t, host.CPU.Percent)
	assert.Equal(t, "pending", host.CPU.UnavailableReason)
	require.NotNil(t, host.Memory)
	assert.Equal(t, uint64(1000), *host.Memory.TotalBytes)
	require.NotNil(t, host.Disk)
	assert.Equal(t, uint64(2000), *host.Disk.TotalBytes)
}

type snapshotResponse struct {
	SampledAt int64           `json:"sampled_at"`
	Host      json.RawMessage `json:"host"`
	Sessions  json.RawMessage `json:"sessions"`
	Config    EffectiveConfig `json:"config"`
}

func requestSnapshot(t *testing.T, m *Module) snapshotResponse {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/snapshot", nil).WithContext(context.Background())
	m.handleSnapshot(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got snapshotResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	require.NotZero(t, got.SampledAt)
	require.NotNil(t, got.Host)
	require.NotNil(t, got.Sessions)
	assert.Positive(t, got.Config.RefreshIntervalMS)
	assert.Positive(t, got.Config.TopProcessLimit)
	return got
}

type fakeClock struct {
	now time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}
