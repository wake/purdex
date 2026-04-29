package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestSnapshot_IncludesPurdexSessionProcessTotals(t *testing.T) {
	collector := newFakeHostCollector()
	tmuxLister := &fakeSnapshotTmuxPaneLister{panes: []TmuxPane{
		{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101},
		{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%2", PanePID: 201},
		{TmuxSessionID: "$2", TmuxSessionName: "other", PaneID: "%3", PanePID: 901},
	}}
	processCollector := &fakeSnapshotProcessCollector{processes: []Process{
		{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
		{PID: 201, PPID: 101, Command: "worker", CPUPercent: 2, MemoryBytes: 200},
		{PID: 301, PPID: 201, Command: "nested", CPUPercent: 3, MemoryBytes: 300},
		{PID: 901, PPID: 1, Command: "unrelated", CPUPercent: 90, MemoryBytes: 9000},
	}}
	m := New(WithCollectors(Collectors{
		HostCollector:         collector,
		TmuxPaneLister:        tmuxLister,
		ProcessTableCollector: processCollector,
	}), withSessionProvider(&fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "abc123", TmuxID: "$1", Name: "work"},
	}}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{}})))

	snapshot := requestSnapshot(t, m)

	var sessions []SessionMetrics
	require.NoError(t, json.Unmarshal(snapshot.Sessions, &sessions))
	require.Len(t, sessions, 1)
	assert.Equal(t, "abc123", sessions[0].SessionCode)
	assert.Equal(t, "$1", sessions[0].TmuxSession.ID)
	assert.Equal(t, "work", sessions[0].TmuxSession.Name)
	assert.Equal(t, 6.0, *sessions[0].Daemon.CPUPercent)
	assert.Equal(t, uint64(600), *sessions[0].Daemon.MemoryBytes)
	assert.Equal(t, 3, *sessions[0].Daemon.ProcessCount)
	assert.Empty(t, sessions[0].Daemon.UnavailableReason)
	assert.Equal(t, 1, tmuxLister.calls)
	assert.Equal(t, 1, processCollector.calls)
}

func TestSnapshot_MarksSessionUnavailableWhenTmuxPaneListingFails(t *testing.T) {
	m := New(WithCollectors(Collectors{
		HostCollector:         newFakeHostCollector(),
		TmuxPaneLister:        &fakeSnapshotTmuxPaneLister{err: errors.New("boom")},
		ProcessTableCollector: &fakeSnapshotProcessCollector{},
	}), withSessionProvider(&fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "abc123", TmuxID: "$1", Name: "work"},
	}}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{}})))

	snapshot := requestSnapshot(t, m)

	var sessions []SessionMetrics
	require.NoError(t, json.Unmarshal(snapshot.Sessions, &sessions))
	require.Len(t, sessions, 1)
	assertSessionDaemonUnavailable(t, sessions[0].Daemon, "tmux_panes_unavailable")
	assertRawSessionDaemonNullFields(t, snapshot.Sessions, 0)
}

func TestSnapshot_TmuxPaneFailurePreservesMappingReasonsPerSession(t *testing.T) {
	m := New(WithCollectors(Collectors{
		HostCollector:         newFakeHostCollector(),
		TmuxPaneLister:        &fakeSnapshotTmuxPaneLister{err: errors.New("boom")},
		ProcessTableCollector: &fakeSnapshotProcessCollector{},
	}), withSessionProvider(&fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "mapped", TmuxID: "$1", Name: "work"},
		{Code: "missing-session", Name: "unknown"},
	}}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{}})))

	snapshot := requestSnapshot(t, m)

	var sessions []SessionMetrics
	require.NoError(t, json.Unmarshal(snapshot.Sessions, &sessions))
	require.Len(t, sessions, 2)
	reasons := sessionUnavailableReasonsByCode(sessions)
	assert.Equal(t, "tmux_panes_unavailable", reasons["mapped"])
	assert.Equal(t, "session_mapping_unavailable", reasons["missing-session"])
}

func TestSnapshot_MarksSessionUnavailableWhenProcessTableFails(t *testing.T) {
	m := New(WithCollectors(Collectors{
		HostCollector:         newFakeHostCollector(),
		TmuxPaneLister:        &fakeSnapshotTmuxPaneLister{panes: []TmuxPane{{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101}}},
		ProcessTableCollector: &fakeSnapshotProcessCollector{err: errors.New("boom")},
	}), withSessionProvider(&fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "abc123", TmuxID: "$1", Name: "work"},
	}}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{}})))

	snapshot := requestSnapshot(t, m)

	var sessions []SessionMetrics
	require.NoError(t, json.Unmarshal(snapshot.Sessions, &sessions))
	require.Len(t, sessions, 1)
	assertSessionDaemonUnavailable(t, sessions[0].Daemon, "process_table_unavailable")
}

func TestSnapshot_ProcessTableFailurePreservesMappingReasonsPerSession(t *testing.T) {
	m := New(WithCollectors(Collectors{
		HostCollector: newFakeHostCollector(),
		TmuxPaneLister: &fakeSnapshotTmuxPaneLister{panes: []TmuxPane{
			{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101},
		}},
		ProcessTableCollector: &fakeSnapshotProcessCollector{err: errors.New("boom")},
	}), withSessionProvider(&fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "mapped", TmuxID: "$1", Name: "work"},
		{Code: "missing-pane", TmuxID: "$2", Name: "gone"},
		{Code: "missing-session", Name: "unknown"},
	}}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{}})))

	snapshot := requestSnapshot(t, m)

	var sessions []SessionMetrics
	require.NoError(t, json.Unmarshal(snapshot.Sessions, &sessions))
	require.Len(t, sessions, 3)
	reasons := sessionUnavailableReasonsByCode(sessions)
	assert.Equal(t, "process_table_unavailable", reasons["mapped"])
	assert.Equal(t, "session_panes_unavailable", reasons["missing-pane"])
	assert.Equal(t, "session_mapping_unavailable", reasons["missing-session"])
}

func TestSnapshot_MarksSessionUnavailableWhenPaneMappingIsMissing(t *testing.T) {
	m := New(WithCollectors(Collectors{
		HostCollector: newFakeHostCollector(),
		TmuxPaneLister: &fakeSnapshotTmuxPaneLister{panes: []TmuxPane{
			{TmuxSessionID: "$2", TmuxSessionName: "other", PaneID: "%2", PanePID: 201},
		}},
		ProcessTableCollector: &fakeSnapshotProcessCollector{processes: []Process{{PID: 201, PPID: 1, Command: "other", CPUPercent: 1, MemoryBytes: 100}}},
	}), withSessionProvider(&fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "abc123", TmuxID: "$1", Name: "work"},
	}}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{}})))

	snapshot := requestSnapshot(t, m)

	var sessions []SessionMetrics
	require.NoError(t, json.Unmarshal(snapshot.Sessions, &sessions))
	require.Len(t, sessions, 1)
	assertSessionDaemonUnavailable(t, sessions[0].Daemon, "session_panes_unavailable")
}

func TestSnapshot_MarksSessionUnavailableWhenProcessDataIsMissing(t *testing.T) {
	m := New(WithCollectors(Collectors{
		HostCollector: newFakeHostCollector(),
		TmuxPaneLister: &fakeSnapshotTmuxPaneLister{panes: []TmuxPane{
			{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101},
		}},
		ProcessTableCollector: &fakeSnapshotProcessCollector{processes: []Process{{PID: 201, PPID: 1, Command: "other", CPUPercent: 1, MemoryBytes: 100}}},
	}), withSessionProvider(&fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "abc123", TmuxID: "$1", Name: "work"},
	}}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{}})))

	snapshot := requestSnapshot(t, m)

	var sessions []SessionMetrics
	require.NoError(t, json.Unmarshal(snapshot.Sessions, &sessions))
	require.Len(t, sessions, 1)
	assertSessionDaemonUnavailable(t, sessions[0].Daemon, "process_data_unavailable")
}

func TestSnapshot_UsesSessionProviderFromRegistry(t *testing.T) {
	collector := newFakeHostCollector()
	tmuxLister := &fakeSnapshotTmuxPaneLister{panes: []TmuxPane{
		{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101},
	}}
	processCollector := &fakeSnapshotProcessCollector{processes: []Process{
		{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
	}}
	provider := &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "abc123", TmuxID: "$1", Name: "work"}}}
	c := core.New(core.CoreDeps{Config: &config.Config{}})
	c.Registry.Register(session.RegistryKey, sessionProvider(provider))
	m := New(WithCollectors(Collectors{
		HostCollector:         collector,
		TmuxPaneLister:        tmuxLister,
		ProcessTableCollector: processCollector,
	}))
	require.NoError(t, m.Init(c))

	snapshot := requestSnapshot(t, m)

	var sessions []SessionMetrics
	require.NoError(t, json.Unmarshal(snapshot.Sessions, &sessions))
	require.Len(t, sessions, 1)
	assert.Equal(t, "abc123", sessions[0].SessionCode)
	assert.Equal(t, 1, provider.calls)
}

func TestSnapshot_SkipsTmuxAndProcessCollectorsWhenNoLiveSessions(t *testing.T) {
	tmuxLister := &fakeSnapshotTmuxPaneLister{}
	processCollector := &fakeSnapshotProcessCollector{}
	m := New(WithCollectors(Collectors{
		HostCollector:         newFakeHostCollector(),
		TmuxPaneLister:        tmuxLister,
		ProcessTableCollector: processCollector,
	}), withSessionProvider(&fakeSessionProvider{}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{}})))

	snapshot := requestSnapshot(t, m)

	assert.JSONEq(t, `[]`, string(snapshot.Sessions))
	assert.Zero(t, tmuxLister.calls)
	assert.Zero(t, processCollector.calls)
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

type fakeSessionProvider struct {
	sessions []session.SessionInfo
	err      error
	calls    int
}

func (p *fakeSessionProvider) ListSessions() ([]session.SessionInfo, error) {
	p.calls++
	return p.sessions, p.err
}

type fakeSnapshotTmuxPaneLister struct {
	panes []TmuxPane
	err   error
	calls int
}

func (l *fakeSnapshotTmuxPaneLister) ListPanes(context.Context) ([]TmuxPane, error) {
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	return l.panes, nil
}

type fakeSnapshotProcessCollector struct {
	processes []Process
	err       error
	calls     int
}

func (c *fakeSnapshotProcessCollector) ListProcesses(context.Context) ([]Process, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.processes, nil
}

func assertSessionDaemonUnavailable(t *testing.T, daemon SessionDaemonMetrics, reason string) {
	t.Helper()
	assert.Nil(t, daemon.CPUPercent)
	assert.Nil(t, daemon.MemoryBytes)
	assert.Nil(t, daemon.ProcessCount)
	assert.Equal(t, reason, daemon.UnavailableReason)
}

func sessionUnavailableReasonsByCode(sessions []SessionMetrics) map[string]string {
	reasons := make(map[string]string, len(sessions))
	for _, sess := range sessions {
		reasons[sess.SessionCode] = sess.Daemon.UnavailableReason
	}
	return reasons
}

func assertRawSessionDaemonNullFields(t *testing.T, raw json.RawMessage, index int) {
	t.Helper()
	var sessions []map[string]any
	require.NoError(t, json.Unmarshal(raw, &sessions))
	require.Greater(t, len(sessions), index)
	daemon, ok := sessions[index]["daemon"].(map[string]any)
	require.True(t, ok)
	for _, field := range []string{"cpu_percent", "memory_bytes", "process_count"} {
		value, exists := daemon[field]
		assert.True(t, exists, "%s should be present", field)
		assert.Nil(t, value, "%s should be null", field)
	}
}
