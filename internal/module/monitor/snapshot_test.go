package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
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

func TestSnapshot_StaleConcurrentRequestsShareInFlightSample(t *testing.T) {
	collector := newBlockingHostCollector()
	clock := newFakeClock(time.Unix(100, 0))
	m := New(WithCollectors(Collectors{HostCollector: collector}), withClock(clock.Now))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{
		Monitor: MonitorConfig{RefreshIntervalMS: 1000, TopProcessLimit: 10},
	}})))

	firstDone := make(chan snapshotResult, 1)
	go func() {
		firstDone <- requestSnapshotResult(m)
	}()
	collector.WaitForCPUCall(t, 1)
	collector.ReleaseOne()
	firstResult := <-firstDone
	require.NoError(t, firstResult.err)
	require.Equal(t, http.StatusOK, firstResult.statusCode)
	first := firstResult.response
	require.Equal(t, 1, collector.cpuCalls)

	clock.Advance(time.Second)
	const concurrentRequests = 5
	responses := make(chan snapshotResult, concurrentRequests)
	var wg sync.WaitGroup
	for range concurrentRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- requestSnapshotResult(m)
		}()
	}
	collector.WaitForCPUCall(t, 2)
	collector.ReleaseOne()
	wg.Wait()
	close(responses)

	var sampledAt int64
	for result := range responses {
		require.NoError(t, result.err)
		require.Equal(t, http.StatusOK, result.statusCode)
		response := result.response
		assert.NotEqual(t, first.SampledAt, response.SampledAt)
		if sampledAt == 0 {
			sampledAt = response.SampledAt
		}
		assert.Equal(t, sampledAt, response.SampledAt)
	}
	assert.Equal(t, 2, collector.cpuCalls)
	assert.Equal(t, 2, collector.memoryCalls)
	assert.Equal(t, 2, collector.diskCalls)
}

func TestPutMonitorConfigDoesNotWaitForInFlightSnapshotCollection(t *testing.T) {
	collector := newBlockingHostCollector()
	m := New(WithCollectors(Collectors{HostCollector: collector}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{
		Monitor: MonitorConfig{RefreshIntervalMS: 1000, TopProcessLimit: 10},
	}})))

	snapshotDone := make(chan struct{})
	go func() {
		_ = requestSnapshotResult(m)
		close(snapshotDone)
	}()
	collector.WaitForCPUCall(t, 1)

	putDone := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/monitor/config", bytes.NewBufferString(`{"refresh_interval_ms":2000,"top_process_limit":7}`))
		m.handleConfig(rec, req)
		putDone <- rec.Code
	}()

	select {
	case code := <-putDone:
		assert.Equal(t, http.StatusOK, code)
	case <-time.After(100 * time.Millisecond):
		collector.ReleaseOne()
		<-snapshotDone
		t.Fatal("PUT /api/monitor/config should not wait for in-flight snapshot collection")
	}

	collector.ReleaseOne()
	<-snapshotDone
}

func TestSnapshot_LeaderCancellationDoesNotCancelSharedInFlightSample(t *testing.T) {
	collector := newBlockingHostCollector()
	m := New(WithCollectors(Collectors{HostCollector: collector}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{
		Monitor: MonitorConfig{RefreshIntervalMS: 1000, TopProcessLimit: 10},
	}})))

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := m.getSnapshot(leaderCtx)
		leaderDone <- err
	}()
	collector.WaitForCPUCall(t, 1)
	cancelLeader()

	waiterDone := make(chan snapshotResult, 1)
	go func() {
		waiterDone <- requestSnapshotResult(m)
	}()

	collector.ReleaseOne()
	require.NoError(t, <-leaderDone)
	waiter := <-waiterDone
	require.NoError(t, waiter.err)
	require.Equal(t, http.StatusOK, waiter.statusCode)
	assert.NotZero(t, waiter.response.SampledAt)
	assert.Equal(t, 1, collector.cpuCalls)
}

func TestSnapshot_WaiterIgnoresStaleGenerationInFlightError(t *testing.T) {
	provider := newBlockingFirstSessionProvider([]session.SessionInfo{{Code: "abc123", TmuxID: "$1", Name: "work"}})
	m := New(WithCollectors(Collectors{
		HostCollector:         newFakeHostCollector(),
		TmuxPaneLister:        &fakeSnapshotTmuxPaneLister{panes: []TmuxPane{{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101}}},
		ProcessTableCollector: &fakeSnapshotProcessCollector{processes: []Process{{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100}}},
	}), withSessionProvider(provider))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{
		Monitor: MonitorConfig{RefreshIntervalMS: 1000, TopProcessLimit: 10},
	}})))

	leaderDone := make(chan error, 1)
	go func() {
		_, err := m.getSnapshot(context.Background())
		leaderDone <- err
	}()
	provider.WaitForFirstCall(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/monitor/config", bytes.NewBufferString(`{"refresh_interval_ms":2000,"top_process_limit":7}`))
	m.handleConfig(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	waiterDone := make(chan snapshotResult, 1)
	go func() {
		waiterDone <- requestSnapshotResult(m)
	}()

	provider.ReleaseFirst(errors.New("stale boom"))
	require.Error(t, <-leaderDone)
	waiter := <-waiterDone
	require.NoError(t, waiter.err)
	require.Equal(t, http.StatusOK, waiter.statusCode)
	assert.Equal(t, 2, provider.Calls())
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
	assert.Equal(t, []Process{
		{PID: 301, PPID: 201, Command: "nested", CPUPercent: 3, MemoryBytes: 300},
		{PID: 201, PPID: 101, Command: "worker", CPUPercent: 2, MemoryBytes: 200},
		{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
	}, sessions[0].Daemon.TopProcesses)
	assert.Empty(t, sessions[0].Daemon.UnavailableReason)
	assert.Equal(t, 1, tmuxLister.calls)
	assert.Equal(t, 1, processCollector.calls)
}

func TestSnapshot_UsesConfiguredTopProcessLimitWhileKeepingTotalsInclusive(t *testing.T) {
	m := New(WithCollectors(Collectors{
		HostCollector: newFakeHostCollector(),
		TmuxPaneLister: &fakeSnapshotTmuxPaneLister{panes: []TmuxPane{
			{TmuxSessionID: "$1", TmuxSessionName: "work", PaneID: "%1", PanePID: 101},
		}},
		ProcessTableCollector: &fakeSnapshotProcessCollector{processes: []Process{
			{PID: 101, PPID: 1, Command: "shell", CPUPercent: 1, MemoryBytes: 100},
			{PID: 201, PPID: 101, Command: "node", CPUPercent: 8, MemoryBytes: 800},
			{PID: 202, PPID: 101, Command: "go", CPUPercent: 4, MemoryBytes: 400},
		}},
	}), withSessionProvider(&fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "abc123", TmuxID: "$1", Name: "work"},
	}}))
	require.NoError(t, m.Init(core.New(core.CoreDeps{Config: &config.Config{
		Monitor: MonitorConfig{RefreshIntervalMS: 5000, TopProcessLimit: 1},
	}})))

	snapshot := requestSnapshot(t, m)

	var sessions []SessionMetrics
	require.NoError(t, json.Unmarshal(snapshot.Sessions, &sessions))
	require.Len(t, sessions, 1)
	assert.Equal(t, 13.0, *sessions[0].Daemon.CPUPercent)
	assert.Equal(t, uint64(1300), *sessions[0].Daemon.MemoryBytes)
	assert.Equal(t, 3, *sessions[0].Daemon.ProcessCount)
	assert.Equal(t, []Process{{PID: 201, PPID: 101, Command: "node", CPUPercent: 8, MemoryBytes: 800}}, sessions[0].Daemon.TopProcesses)
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

type snapshotResult struct {
	response    snapshotResponse
	statusCode  int
	contentType string
	err         error
}

func requestSnapshotResult(m *Module) snapshotResult {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor/snapshot", nil).WithContext(context.Background())
	m.handleSnapshot(rec, req)

	var got snapshotResponse
	err := json.NewDecoder(rec.Body).Decode(&got)
	return snapshotResult{
		response:    got,
		statusCode:  rec.Code,
		contentType: rec.Header().Get("Content-Type"),
		err:         err,
	}
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
	mu       sync.Mutex
}

func (p *fakeSessionProvider) ListSessions() ([]session.SessionInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.sessions, p.err
}

func (p *fakeSessionProvider) setError(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

type blockingFirstSessionProvider struct {
	sessions     []session.SessionInfo
	mu           sync.Mutex
	calls        int
	firstStarted chan struct{}
	releaseFirst chan error
}

func newBlockingFirstSessionProvider(sessions []session.SessionInfo) *blockingFirstSessionProvider {
	return &blockingFirstSessionProvider{
		sessions:     sessions,
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan error, 1),
	}
}

func (p *blockingFirstSessionProvider) ListSessions() ([]session.SessionInfo, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		close(p.firstStarted)
		if err := <-p.releaseFirst; err != nil {
			return nil, err
		}
	}
	return p.sessions, nil
}

func (p *blockingFirstSessionProvider) WaitForFirstCall(t *testing.T) {
	t.Helper()
	select {
	case <-p.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first ListSessions call did not start")
	}
}

func (p *blockingFirstSessionProvider) ReleaseFirst(err error) {
	p.releaseFirst <- err
}

func (p *blockingFirstSessionProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
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
	assert.Empty(t, daemon.TopProcesses)
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
	topProcesses, exists := daemon["top_processes"]
	assert.True(t, exists, "top_processes should be present")
	assert.Empty(t, topProcesses, "top_processes should be an empty array")
}

type blockingHostCollector struct {
	mu          sync.Mutex
	cpuCalls    int
	memoryCalls int
	diskCalls   int
	cpuCalled   chan int
	releaseCPU  chan struct{}
}

func newBlockingHostCollector() *blockingHostCollector {
	return &blockingHostCollector{
		cpuCalled:  make(chan int, 10),
		releaseCPU: make(chan struct{}, 10),
	}
}

func (c *blockingHostCollector) CollectCPU(context.Context) (HostCPUSample, error) {
	c.mu.Lock()
	c.cpuCalls++
	call := c.cpuCalls
	c.mu.Unlock()
	c.cpuCalled <- call
	<-c.releaseCPU
	return HostCPUSample{Idle: uint64(100 - call), Total: uint64(100 + call)}, nil
}

func (c *blockingHostCollector) CollectMemory(context.Context) (HostMemorySample, error) {
	c.mu.Lock()
	c.memoryCalls++
	c.mu.Unlock()
	return HostMemorySample{TotalBytes: 1, UsedBytes: 0}, nil
}

func (c *blockingHostCollector) CollectDisk(context.Context) (HostDiskSample, error) {
	c.mu.Lock()
	c.diskCalls++
	c.mu.Unlock()
	return HostDiskSample{TotalBytes: 1, UsedBytes: 0}, nil
}

func (c *blockingHostCollector) WaitForCPUCall(t *testing.T, calls int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case call := <-c.cpuCalled:
			if call >= calls {
				return
			}
		case <-deadline:
			c.mu.Lock()
			actual := c.cpuCalls
			c.mu.Unlock()
			require.GreaterOrEqual(t, actual, calls)
		}
	}
}

func (c *blockingHostCollector) ReleaseOne() {
	c.releaseCPU <- struct{}{}
}
