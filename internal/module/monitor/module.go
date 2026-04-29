package monitor

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
)

type TmuxPane struct {
	TmuxSessionID   string `json:"tmux_session_id"`
	TmuxSessionName string `json:"tmux_session_name"`
	PaneID          string `json:"pane_id"`
	PanePID         int    `json:"pane_pid"`
}

type TmuxPaneLister interface {
	ListPanes(context.Context) ([]TmuxPane, error)
}

type ProcessTableCollector interface {
	ListProcesses(context.Context) ([]Process, error)
}

type sessionProvider interface {
	ListSessions() ([]session.SessionInfo, error)
}

const (
	sessionMappingUnavailableReason = "session_mapping_unavailable"
	sessionPanesUnavailableReason   = "session_panes_unavailable"
	processTableUnavailableReason   = "process_table_unavailable"
	processDataUnavailableReason    = "process_data_unavailable"
	tmuxPanesUnavailableReason      = "tmux_panes_unavailable"
)

type Collectors struct {
	HostCollector         HostCollector
	TmuxPaneLister        TmuxPaneLister
	ProcessTableCollector ProcessTableCollector
}

type Option func(*Module)

type Module struct {
	collectors      Collectors
	core            *core.Core
	now             func() time.Time
	hostState       *HostMetricsState
	sessionProvider sessionProvider

	snapshotMu     sync.Mutex
	cachedSnapshot *snapshot
}

func New(opts ...Option) *Module {
	collectors := Collectors{
		HostCollector:         NewSystemHostCollector(),
		TmuxPaneLister:        NewTmuxPaneLister(nil),
		ProcessTableCollector: NewProcessTableCollector(nil),
	}
	m := &Module{
		collectors: collectors,
		now:        time.Now,
		hostState:  NewHostMetricsState(collectors.HostCollector),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func WithCollectors(collectors Collectors) Option {
	return func(m *Module) {
		m.collectors = collectors
		m.hostState = NewHostMetricsState(collectors.HostCollector)
	}
}

func withClock(now func() time.Time) Option {
	return func(m *Module) {
		m.now = now
	}
}

func withSessionProvider(provider sessionProvider) Option {
	return func(m *Module) {
		m.sessionProvider = provider
	}
}

func (m *Module) Name() string { return "monitor" }

func (m *Module) Dependencies() []string { return []string{"session"} }

func (m *Module) Init(c *core.Core) error {
	m.core = c
	if m.sessionProvider == nil && c != nil && c.Registry != nil {
		if svc, ok := c.Registry.Get(session.RegistryKey); ok {
			if provider, ok := svc.(sessionProvider); ok {
				m.sessionProvider = provider
			}
		}
	}
	return nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/monitor/snapshot", m.handleSnapshot)
	mux.HandleFunc("GET /api/monitor/config", m.handleConfig)
	mux.HandleFunc("PUT /api/monitor/config", m.handleConfig)
}

func (m *Module) Start(_ context.Context) error {
	log.Println("[monitor] endpoints enabled")
	return nil
}

func (m *Module) Stop(_ context.Context) error { return nil }

func (m *Module) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := m.getSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}

type snapshot struct {
	SampledAt int64            `json:"sampled_at"`
	Host      HostMetrics      `json:"host"`
	Sessions  []SessionMetrics `json:"sessions"`
	Config    EffectiveConfig  `json:"config"`
	sampledAt time.Time
}

type TmuxSessionRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SessionMetrics struct {
	SessionCode string               `json:"session_code"`
	TmuxSession TmuxSessionRef       `json:"tmux_session"`
	Daemon      SessionDaemonMetrics `json:"daemon"`
}

type SessionDaemonMetrics struct {
	CPUPercent        *float64  `json:"cpu_percent"`
	MemoryBytes       *uint64   `json:"memory_bytes"`
	ProcessCount      *int      `json:"process_count"`
	TopProcesses      []Process `json:"top_processes"`
	UnavailableReason string    `json:"unavailable_reason,omitempty"`
}

func (m *Module) getSnapshot(ctx context.Context) (*snapshot, error) {
	now := m.now()
	cfg := m.effectiveConfig()
	refreshInterval := time.Duration(cfg.RefreshIntervalMS) * time.Millisecond

	m.snapshotMu.Lock()
	defer m.snapshotMu.Unlock()

	if m.cachedSnapshot != nil && now.Sub(m.cachedSnapshot.sampledAt) < refreshInterval {
		return m.cachedSnapshot, nil
	}

	sessions, err := m.collectSessionMetrics(ctx, cfg.TopProcessLimit)
	if err != nil {
		return nil, err
	}

	sampledAt := m.now()
	snapshot := &snapshot{
		SampledAt: sampledAt.UnixMilli(),
		Host:      collectHostMetrics(ctx, m.ensureHostMetricsState()),
		Sessions:  sessions,
		Config:    cfg,
		sampledAt: sampledAt,
	}
	m.cachedSnapshot = snapshot
	return snapshot, nil
}

func (m *Module) ensureHostMetricsState() *HostMetricsState {
	if m.hostState == nil {
		m.hostState = NewHostMetricsState(m.collectors.HostCollector)
	}
	return m.hostState
}

func (m *Module) collectSessionMetrics(ctx context.Context, topProcessLimit int) ([]SessionMetrics, error) {
	if m.sessionProvider == nil {
		return []SessionMetrics{}, nil
	}
	sessions, err := m.sessionProvider.ListSessions()
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return []SessionMetrics{}, nil
	}

	panes, err := m.collectors.TmuxPaneLister.ListPanes(ctx)
	if err != nil {
		return unavailableSessionMetricsForTmuxPanesFailure(sessions), nil
	}
	hasMatchingPane := make(map[string]bool, len(sessions))
	for _, pane := range panes {
		hasMatchingPane[pane.TmuxSessionID] = true
	}
	needsProcessTable := false
	for _, sess := range sessions {
		if sess.TmuxID != "" && hasMatchingPane[sess.TmuxID] {
			needsProcessTable = true
			break
		}
	}
	if !needsProcessTable {
		metrics := make([]SessionMetrics, 0, len(sessions))
		for _, sess := range sessions {
			reason := sessionPanesUnavailableReason
			if sess.TmuxID == "" {
				reason = sessionMappingUnavailableReason
			}
			metrics = append(metrics, unavailableSessionMetric(sess, reason))
		}
		return metrics, nil
	}

	processes, err := m.collectors.ProcessTableCollector.ListProcesses(ctx)
	if err != nil {
		return unavailableSessionMetricsForProcessTableFailure(sessions, hasMatchingPane), nil
	}

	metrics := make([]SessionMetrics, 0, len(sessions))
	for _, sess := range sessions {
		if sess.TmuxID == "" {
			metrics = append(metrics, unavailableSessionMetric(sess, sessionMappingUnavailableReason))
			continue
		}
		if !hasMatchingPane[sess.TmuxID] {
			metrics = append(metrics, unavailableSessionMetric(sess, sessionPanesUnavailableReason))
			continue
		}
		aggregate := AggregateSessionProcesses(sess.TmuxID, panes, processes, topProcessLimit)
		if aggregate.ProcessCount == 0 {
			metrics = append(metrics, unavailableSessionMetric(sess, processDataUnavailableReason))
			continue
		}
		metrics = append(metrics, SessionMetrics{
			SessionCode: sess.Code,
			TmuxSession: TmuxSessionRef{
				ID:   sess.TmuxID,
				Name: sess.Name,
			},
			Daemon: availableSessionDaemonMetrics(aggregate),
		})
	}
	return metrics, nil
}

func unavailableSessionMetrics(sessions []session.SessionInfo, reason string) []SessionMetrics {
	metrics := make([]SessionMetrics, 0, len(sessions))
	for _, sess := range sessions {
		metrics = append(metrics, unavailableSessionMetric(sess, reason))
	}
	return metrics
}

func unavailableSessionMetricsForTmuxPanesFailure(sessions []session.SessionInfo) []SessionMetrics {
	metrics := make([]SessionMetrics, 0, len(sessions))
	for _, sess := range sessions {
		reason := tmuxPanesUnavailableReason
		if sess.TmuxID == "" {
			reason = sessionMappingUnavailableReason
		}
		metrics = append(metrics, unavailableSessionMetric(sess, reason))
	}
	return metrics
}

func unavailableSessionMetricsForProcessTableFailure(sessions []session.SessionInfo, hasMatchingPane map[string]bool) []SessionMetrics {
	metrics := make([]SessionMetrics, 0, len(sessions))
	for _, sess := range sessions {
		reason := processTableUnavailableReason
		if sess.TmuxID == "" {
			reason = sessionMappingUnavailableReason
		} else if !hasMatchingPane[sess.TmuxID] {
			reason = sessionPanesUnavailableReason
		}
		metrics = append(metrics, unavailableSessionMetric(sess, reason))
	}
	return metrics
}

func unavailableSessionMetric(sess session.SessionInfo, reason string) SessionMetrics {
	return SessionMetrics{
		SessionCode: sess.Code,
		TmuxSession: TmuxSessionRef{
			ID:   sess.TmuxID,
			Name: sess.Name,
		},
		Daemon: SessionDaemonMetrics{TopProcesses: []Process{}, UnavailableReason: reason},
	}
}

func availableSessionDaemonMetrics(aggregate PaneProcessAggregate) SessionDaemonMetrics {
	return SessionDaemonMetrics{
		CPUPercent:   &aggregate.CPUPercent,
		MemoryBytes:  &aggregate.MemoryBytes,
		ProcessCount: &aggregate.ProcessCount,
		TopProcesses: aggregate.TopProcesses,
	}
}

func (m *Module) effectiveConfig() EffectiveConfig {
	if m.core == nil || m.core.Cfg == nil {
		return EffectiveMonitorConfig(DefaultMonitorConfig())
	}

	m.core.CfgMu.RLock()
	cfg := m.core.Cfg.Monitor
	m.core.CfgMu.RUnlock()
	return EffectiveMonitorConfig(cfg)
}

func (m *Module) handleConfig(w http.ResponseWriter, r *http.Request) {
	if m.core == nil || m.core.Cfg == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "monitor config unavailable"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		m.core.CfgMu.RLock()
		cfg := m.core.Cfg.Monitor
		m.core.CfgMu.RUnlock()
		writeJSON(w, http.StatusOK, EffectiveMonitorConfig(cfg))
	case http.MethodPut:
		m.handlePutConfig(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type configUpdateRequest struct {
	RefreshIntervalMS *int `json:"refresh_interval_ms,omitempty"`
	TopProcessLimit   *int `json:"top_process_limit,omitempty"`
}

func (m *Module) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var req configUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	m.core.CfgMu.Lock()
	snapshot := *m.core.Cfg

	current := EffectiveMonitorConfig(m.core.Cfg.Monitor).persistedConfig()
	if req.RefreshIntervalMS != nil {
		current.RefreshIntervalMS = *req.RefreshIntervalMS
	}
	if req.TopProcessLimit != nil {
		current.TopProcessLimit = *req.TopProcessLimit
	}

	effective := ClampSubmittedMonitorConfig(current)
	m.core.Cfg.Monitor = effective.persistedConfig()

	if m.core.CfgPath != "" {
		if err := config.WriteFile(m.core.CfgPath, *m.core.Cfg); err != nil {
			*m.core.Cfg = snapshot
			m.core.CfgMu.Unlock()
			http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	m.core.CfgMu.Unlock()

	m.invalidateSnapshot()
	m.core.NotifyConfigChange()
	writeJSON(w, http.StatusOK, effective)
}

func (m *Module) invalidateSnapshot() {
	m.snapshotMu.Lock()
	m.cachedSnapshot = nil
	m.snapshotMu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
