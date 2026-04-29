package monitor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
)

func TestSnapshotHandlerContract_ReturnsStableJSONFieldsAndUnits(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 123000000))
	hostCollector := newFakeHostCollector()
	hostCollector.cpuSamples = []HostCPUSample{{Idle: 80, Total: 100}, {Idle: 90, Total: 150}}
	hostCollector.memory = HostMemorySample{TotalBytes: 1024, UsedBytes: 256}
	hostCollector.disk = HostDiskSample{TotalBytes: 4096, UsedBytes: 1024}
	m := New(WithCollectors(Collectors{
		HostCollector: hostCollector,
		TmuxPaneLister: &fakeSnapshotTmuxPaneLister{panes: []TmuxPane{
			{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101},
		}},
		ProcessTableCollector: &fakeSnapshotProcessCollector{processes: []Process{
			{PID: 101, PPID: 1, Command: "shell", CPUPercent: 2.5, MemoryBytes: 2048},
		}},
	}), withClock(clock.Now), withSessionProvider(&fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "abc123", TmuxID: "$1", Name: "work"},
	}}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{
		Monitor: MonitorConfig{RefreshIntervalMS: 2000, TopProcessLimit: 5},
	}})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/snapshot", nil)
	m.handleSnapshot(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var first map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&first))
	assert.Equal(t, float64(clock.Now().UnixMilli()), first["sampled_at"])
	firstHost := requireMap(t, first["host"])
	firstCPU := requireMap(t, firstHost["cpu"])
	assert.Nil(t, firstCPU["percent"])
	assert.Equal(t, "pending", firstCPU["unavailable_reason"])

	clock.Advance(2 * time.Second)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/monitor/snapshot", nil)
	m.handleSnapshot(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var got map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, float64(clock.Now().UnixMilli()), got["sampled_at"])

	host := requireMap(t, got["host"])
	cpu := requireMap(t, host["cpu"])
	assert.Equal(t, float64(80), cpu["percent"])
	assertPresentNil(t, cpu, "unavailable_reason")
	memory := requireMap(t, host["memory"])
	assert.Equal(t, float64(1024), memory["total_bytes"])
	assert.Equal(t, float64(256), memory["used_bytes"])
	assert.Equal(t, float64(25), memory["used_percent"])
	assertPresentNil(t, memory, "unavailable_reason")
	disk := requireMap(t, host["disk"])
	assert.Equal(t, float64(4096), disk["total_bytes"])
	assert.Equal(t, float64(1024), disk["used_bytes"])
	assert.Equal(t, float64(25), disk["used_percent"])
	assertPresentNil(t, disk, "unavailable_reason")

	sessions, ok := got["sessions"].([]any)
	require.True(t, ok)
	require.Len(t, sessions, 1)
	sessionMetric := requireMap(t, sessions[0])
	assert.Equal(t, "abc123", sessionMetric["session_code"])
	tmuxSession := requireMap(t, sessionMetric["tmux_session"])
	assert.Equal(t, "$1", tmuxSession["id"])
	assert.Equal(t, "work", tmuxSession["name"])
	daemon := requireMap(t, sessionMetric["daemon"])
	assert.Equal(t, float64(2.5), daemon["cpu_percent"])
	assert.Equal(t, float64(2048), daemon["memory_bytes"])
	assert.Equal(t, float64(1), daemon["process_count"])
	assertPresentNil(t, daemon, "unavailable_reason")
	topProcesses, ok := daemon["top_processes"].([]any)
	require.True(t, ok)
	require.Len(t, topProcesses, 1)
	topProcess := requireMap(t, topProcesses[0])
	assert.Equal(t, float64(101), topProcess["pid"])
	assert.Equal(t, float64(1), topProcess["ppid"])
	assert.Equal(t, "shell", topProcess["command"])
	assert.Equal(t, float64(2.5), topProcess["cpu_percent"])
	assert.Equal(t, float64(2048), topProcess["memory_bytes"])

	config := requireMap(t, got["config"])
	assert.Equal(t, float64(2000), config["refresh_interval_ms"])
	assert.Equal(t, float64(5), config["top_process_limit"])
	bounds := requireMap(t, config["bounds"])
	refreshBounds := requireMap(t, bounds["refresh_interval_ms"])
	assert.Equal(t, float64(1000), refreshBounds["min"])
	assert.Equal(t, float64(60000), refreshBounds["max"])
	topProcessBounds := requireMap(t, bounds["top_process_limit"])
	assert.Equal(t, float64(1), topProcessBounds["min"])
	assert.Equal(t, float64(50), topProcessBounds["max"])
}

func TestConfigHandlerContract_PartialPutClampsAndPreservesExistingValues(t *testing.T) {
	cfg := &config.Config{Monitor: MonitorConfig{RefreshIntervalMS: 3000, TopProcessLimit: 7}}
	m := New()
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: cfg})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/monitor/config", bytes.NewBufferString(`{"top_process_limit":999}`))
	m.handleConfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got EffectiveConfig
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, 3000, got.RefreshIntervalMS)
	assert.Equal(t, 50, got.TopProcessLimit)
	assert.Equal(t, 3000, cfg.Monitor.RefreshIntervalMS)
	assert.Equal(t, 50, cfg.Monitor.TopProcessLimit)
}

func requireMap(t *testing.T, value any) map[string]any {
	t.Helper()
	got, ok := value.(map[string]any)
	require.True(t, ok)
	return got
}

func assertPresentNil(t *testing.T, values map[string]any, key string) {
	t.Helper()
	value, ok := values[key]
	require.True(t, ok, "%s should be present", key)
	assert.Nil(t, value)
}
