// Package agent provides the agent hook event module.
// It receives hook events from `pdx hook`, projects live state into frames,
// and broadcasts to WS subscribers. AgentEventStore remains as a legacy
// fallback source for pre-frame sessions and session rename compatibility.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// Module is the agent hook event module.
type Module struct {
	core      *core.Core
	events    *store.AgentEventStore
	frames    *store.FramesStore
	traces    *store.TraceStore
	sessions  session.SessionProvider
	registry  *agentpkg.Registry
	uploadDir string
	traceSink *hookTraceSink

	prober *probe.Prober
	tmux   tmux.Executor

	mu             sync.Mutex
	currentStatus  map[string]agentpkg.Status
	subagents      map[string][]agentpkg.SubagentRef
	activeWatchers map[string]string // tmuxSession → agentType

	// statusSnapshots caches the latest statusline payload per sessionCode.
	// Display-only, not persisted; guarded by snapshotMu (separate from mu
	// because hot-path agent.status POSTs shouldn't contend with hook writes).
	snapshotMu      sync.RWMutex
	statusSnapshots map[string]statusSnapshot

	// testObservers: per-nonce channel for the statusline self-test endpoint.
	// Guarded by testMu (separate from snapshotMu and mu so test traffic
	// cannot block production hook / status writes).
	testMu        sync.Mutex
	testObservers map[string]*testObserver

	// testSpawnProxy is a test seam; production leaves this nil so the handler
	// falls back to defaultSpawnTestProxy which execs the real pdx binary.
	testSpawnProxy func(nonce string) error

	sweepCancel context.CancelFunc
	sweepWG     sync.WaitGroup
}

// New creates a new agent Module backed by the given AgentEventStore. Returns
// an error if the frames / traces stores fail to migrate — most notably if
// the on-disk agent_frames contains malformed subagents_json that cannot be
// classified as either the legacy []string shape or the Phase 2 []SubagentRef
// shape. Surfacing the error here lets the daemon refuse to start rather than
// continuing with m.frames == nil (which would degrade silently via the
// module's m.frames == nil fallbacks).
func New(events *store.AgentEventStore) (*Module, error) {
	var frames *store.FramesStore
	var traces *store.TraceStore
	if events != nil {
		var err error
		frames, err = events.Frames()
		if err != nil {
			return nil, fmt.Errorf("agent module: frames store: %w", err)
		}
		traces, err = events.Traces()
		if err != nil {
			return nil, fmt.Errorf("agent module: traces store: %w", err)
		}
	}
	m := &Module{
		events:          events,
		frames:          frames,
		traces:          traces,
		registry:        agentpkg.NewRegistry(),
		currentStatus:   make(map[string]agentpkg.Status),
		subagents:       make(map[string][]agentpkg.SubagentRef),
		activeWatchers:  make(map[string]string),
		statusSnapshots: make(map[string]statusSnapshot),
		testObservers:   make(map[string]*testObserver),
	}
	if traces != nil {
		m.traceSink = newHookTraceSink(traces)
	}
	return m, nil
}

func (m *Module) Name() string           { return "agent" }
func (m *Module) Dependencies() []string { return []string{"session"} }

// Init retrieves the SessionProvider, initializes the provider registry,
// and registers CC and Codex providers.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	svc, ok := c.Registry.Get(session.RegistryKey)
	if !ok {
		log.Printf("[agent] warning: session provider not found")
		return nil
	}
	m.sessions = svc.(session.SessionProvider)

	// Expose event store and module so other modules (e.g. session rename) can update it.
	c.Registry.Register("agent.events", m.events)
	c.Registry.Register("agent.module", m)

	if m.uploadDir == "" {
		c.CfgMu.RLock()
		m.uploadDir = c.Cfg.UploadDir
		c.CfgMu.RUnlock()
	}

	// Prober (shared across all providers)
	m.tmux = c.Tmux
	m.prober = probe.New(c.Tmux)
	ccProvider := agentcc.NewProvider(m.prober, c.Tmux, c.Cfg, &c.CfgMu)
	m.prober.RegisterIdentifier(ccProvider.Type(), ccProvider.Identify)
	m.prober.RegisterReadiness(ccProvider.Type(), agentcc.NewReadinessChecker(c.Tmux))
	ccProvider.RegisterServices(c.Registry)
	m.registry.Register(ccProvider)

	codexProvider := codex.NewProvider()
	m.prober.RegisterIdentifier(codexProvider.Type(), codexProvider.Identify)
	m.prober.RegisterReadiness(codexProvider.Type(), codex.NewReadinessChecker(c.Tmux))
	c.Registry.Register("agent.prober", m.prober)

	// Listen for config changes to update mutable module state.
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		newDir := c.Cfg.UploadDir
		c.CfgMu.RUnlock()
		if newDir != "" {
			m.mu.Lock()
			m.uploadDir = newDir
			m.mu.Unlock()
		}
	})

	// Codex provider
	m.registry.Register(codexProvider)

	opencodeProvider := opencode.NewProvider()
	m.prober.RegisterIdentifier(opencodeProvider.Type(), opencodeProvider.Identify)
	m.registry.Register(opencodeProvider)

	// Expose registry for other modules
	c.Registry.Register("agent.registry", m.registry)

	return nil
}

// RegisterRoutes registers the agent API endpoints.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agent/event", m.handleEvent)
	mux.HandleFunc("GET /api/agent/monitor/chains", m.handleMonitorChains)
	mux.HandleFunc("GET /api/agent/monitor/chains/{id}", m.handleMonitorChain)
	mux.HandleFunc("GET /api/agent/monitor/projection", m.handleMonitorProjection)
	mux.HandleFunc("GET /api/hooks/{agent}/status", m.handleHookStatus)
	mux.HandleFunc("POST /api/hooks/{agent}/setup", m.handleHookSetup)
	mux.HandleFunc("GET /api/agent/{agent}/statusline/status", m.handleStatuslineStatus)
	mux.HandleFunc("POST /api/agent/{agent}/statusline/setup", m.handleStatuslineSetup)
	mux.HandleFunc("POST /api/agent/cc/statusline/test", m.handleStatuslineTest)
	mux.HandleFunc("POST /api/agent/cc/statusline/test/ready", m.handleStatuslineTestReady)
	mux.HandleFunc("POST /api/agent/status", m.handleAgentStatus)
	mux.HandleFunc("GET /api/agents/detect", m.handleDetect)

	// History (delegates to provider)
	mux.HandleFunc("GET /api/sessions/{code}/history", m.handleHistory)

	// Upload (unchanged)
	mux.HandleFunc("POST /api/agent/upload", m.handleUpload)
	mux.HandleFunc("GET /api/upload/stats", m.handleUploadStats)
	mux.HandleFunc("GET /api/upload/files", m.handleUploadFiles)
	mux.HandleFunc("DELETE /api/upload/files/{session}/{filename}", m.handleDeleteUploadFile)
	mux.HandleFunc("DELETE /api/upload/files/{session}", m.handleDeleteUploadSession)
	mux.HandleFunc("DELETE /api/upload/files", m.handleDeleteAllUploads)
}

// Start replays DB state and registers OnSubscribe callback.
func (m *Module) Start(_ context.Context) error {
	if err := m.sweepOnce(); err != nil {
		log.Printf("[agent] startup sweep: %v", err)
	}
	m.replayFromDB()
	m.startSweep()

	if m.core != nil {
		m.core.Events.OnSubscribe(func(sub *core.EventSubscriber) {
			m.sendSnapshot(sub)
			m.sendStatuslineSnapshot(sub)
		})
	}

	log.Println("[agent] hook event endpoint registered")
	return nil
}

// getUploadDir returns the current upload directory under lock.
func (m *Module) getUploadDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.uploadDir
}

// Stop cancels all active Activity watchers and resets transient state.
func (m *Module) Stop(_ context.Context) error {
	if m.sweepCancel != nil {
		m.sweepCancel()
		m.sweepWG.Wait()
		m.sweepCancel = nil
	}
	if m.prober != nil {
		m.prober.StopAllWatches()
	}
	m.mu.Lock()
	m.activeWatchers = make(map[string]string)
	m.mu.Unlock()
	if m.traceSink != nil {
		m.traceSink.Close()
	}
	return nil
}

// renameSessionLocked transfers in-memory agent state (subagents, currentStatus,
// activeWatchers) from oldName to newName.  CALLER MUST hold m.mu.
func (m *Module) renameSessionLocked(oldName, newName string) {
	if subs, ok := m.subagents[oldName]; ok {
		m.subagents[newName] = subs
		delete(m.subagents, oldName)
	}
	if status, ok := m.currentStatus[oldName]; ok {
		m.currentStatus[newName] = status
		delete(m.currentStatus, oldName)
	}
	if agentType, ok := m.activeWatchers[oldName]; ok {
		delete(m.activeWatchers, oldName)
		// Stop old watcher — callback closure captured oldName, can't reuse
		if m.prober != nil {
			m.prober.StopWatch(oldName + ":")
		}
		// Restart watcher with new name
		m.activeWatchers[newName] = agentType
		if m.prober != nil {
			m.prober.StartWatch(newName+":", m.onActivityDetected(newName, agentType))
		}
	}
}

// RenameSession transfers in-memory agent state from oldName to newName
// under the module's lock.  Used by callers that don't need to coordinate
// with other rename steps (e.g. tests).  Production callers should prefer
// RenameSessionAtomic to make the rename atomic with tmux + DB updates.
func (m *Module) RenameSession(oldName, newName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renameSessionLocked(oldName, newName)
}

// RenameSessionAtomic runs doRename under the module's lock and then
// transfers in-memory state from oldName to newName.  This makes the
// entire rename (tmux + DB + in-memory) atomic from the perspective of
// concurrent hook events: any handler that acquires m.mu while a rename
// is in progress observes either the pre-rename or post-rename state,
// never partial state.  If doRename returns an error, the in-memory
// transfer is skipped and the error is propagated.
//
// Tradeoff: doRename is expected to include the tmux rename exec.Command,
// which runs under the lock.  This can delay all concurrent hook handlers
// by the duration of the tmux call (~50ms normally).  This is an intentional
// choice: hook handlers are brief and renames are low-frequency (user-
// triggered), so sacrificing a small amount of throughput during rename
// in exchange for full atomicity is the right tradeoff.  doRename MUST NOT
// call any method that acquires m.mu (would deadlock).
func (m *Module) RenameSessionAtomic(oldName, newName string, doRename func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := doRename(); err != nil {
		return err
	}
	m.renameSessionLocked(oldName, newName)
	return nil
}

// replayFromDB rebuilds in-memory state from persisted frame projections and
// falls back to legacy agent_events for sessions that have not migrated yet.
func (m *Module) replayFromDB() {
	projectedSessions := make(map[string]struct{})
	if projections, err := m.liveSessionProjections(); err == nil {
		for _, item := range projections {
			projectedSessions[item.SessionName] = struct{}{}
			m.mu.Lock()
			syncProjectionState(m.currentStatus, m.subagents, item.SessionName, &item.Projection)
			m.mu.Unlock()
		}
	} else {
		log.Printf("[agent] replay frames: %v", err)
	}
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("[agent] replay: %v", err)
		return
	}
	for _, ev := range all {
		if _, ok := projectedSessions[ev.TmuxSession]; ok {
			continue
		}
		provider, ok := m.registry.Get(ev.AgentType)
		if !ok {
			continue
		}
		result := provider.DeriveStatus(ev.EventName, ev.RawEvent)
		if result.Valid && result.Status != "" {
			m.mu.Lock()
			m.currentStatus[ev.TmuxSession] = result.Status
			m.mu.Unlock()
		}
	}
}

// sendSnapshot sends the latest hook event for each known session to a new WS subscriber.
func (m *Module) sendSnapshot(sub *core.EventSubscriber) {
	if m.sessions == nil {
		return
	}
	projectedSessions := make(map[string]struct{})
	if projections, err := m.liveSessionProjections(); err == nil {
		for _, item := range projections {
			projectedSessions[item.SessionName] = struct{}{}
			if item.SessionCode == "" {
				continue
			}
			normalized := buildProjectionNormalized(&item.Projection, item.Projection.TopFrame.AgentType, "replay", time.Now().UnixNano(), agentpkg.DeriveResult{})
			payload, _ := json.Marshal(normalized)
			event := core.HostEvent{Type: "hook", Session: item.SessionCode, Value: string(payload)}
			data, _ := json.Marshal(event)
			sub.Send(data)
			m.mu.Lock()
			syncProjectionState(m.currentStatus, m.subagents, item.SessionName, &item.Projection)
			m.mu.Unlock()
		}
	} else {
		log.Printf("[agent] snapshot frames: %v", err)
	}
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("[agent] snapshot: %v", err)
		return
	}
	if len(all) == 0 {
		return
	}

	sessions, err := m.sessions.ListSessions()
	if err != nil {
		log.Printf("[agent] snapshot sessions: %v", err)
		return
	}
	nameToCode := make(map[string]string, len(sessions))
	for _, s := range sessions {
		nameToCode[s.Name] = s.Code
	}

	for _, ev := range all {
		if _, ok := projectedSessions[ev.TmuxSession]; ok {
			continue
		}
		code, ok := nameToCode[ev.TmuxSession]
		if !ok {
			continue
		}
		var result agentpkg.DeriveResult
		if provider, ok := m.registry.Get(ev.AgentType); ok {
			result = provider.DeriveStatus(ev.EventName, ev.RawEvent)
		}
		normalized := m.buildNormalized(ev.TmuxSession, ev.EventName, ev.AgentType, ev.BroadcastTs, result)
		payload, _ := json.Marshal(normalized)
		event := core.HostEvent{Type: "hook", Session: code, Value: string(payload)}
		data, _ := json.Marshal(event)
		sub.Send(data)
	}
}

func (m *Module) liveFrameProjections() ([]SessionProjection, error) {
	if m.frames == nil {
		return nil, nil
	}
	frames, err := m.frames.ListAll()
	if err != nil {
		return nil, err
	}
	if len(frames) == 0 {
		return nil, nil
	}
	return BuildSessionProjections(frames), nil
}

func (m *Module) resolvePaneSession(paneID string) (string, string) {
	if m.tmux == nil {
		return "", ""
	}
	sessionName, err := m.tmux.PaneSessionName(paneID)
	if err != nil {
		return "", ""
	}
	return sessionName, m.resolveSessionCode(sessionName)
}

// manageActivityWatch handles starting/stopping Activity watchers in response to hook events.
func (m *Module) manageActivityWatch(session, agentType string, newStatus agentpkg.Status) {
	m.mu.Lock()
	_, wasWatching := m.activeWatchers[session]
	delete(m.activeWatchers, session)
	m.mu.Unlock()
	if wasWatching {
		m.prober.StopWatch(session + ":")
	}

	if shouldWatchActivity(newStatus) {
		m.mu.Lock()
		m.activeWatchers[session] = agentType
		m.mu.Unlock()
		m.prober.StartWatch(session+":", m.onActivityDetected(session, agentType))
	}
}

func shouldWatchActivity(status agentpkg.Status) bool {
	switch status {
	case agentpkg.StatusWaiting, agentpkg.StatusRunning, agentpkg.StatusIdle:
		return true
	default:
		return false
	}
}

// onActivityDetected returns a callback for screen-activity transitions while a
// waiting/running/idle session is being watched. The callback checks if the
// watcher is still active and then maps the activity signal to a new status or
// triggers a sweep hint for shell-prompt + dead-PID cases.
func (m *Module) onActivityDetected(session, agentType string) func(string, probe.ActivitySignal) {
	return func(target string, signal probe.ActivitySignal) {
		m.mu.Lock()
		if _, active := m.activeWatchers[session]; !active {
			m.mu.Unlock()
			return
		}
		delete(m.activeWatchers, session)
		m.mu.Unlock()

		status := agentpkg.StatusIdle
		switch signal {
		case probe.ActivitySignalRunning:
			status = agentpkg.StatusRunning
		case probe.ActivitySignalIdle:
			status = agentpkg.StatusIdle
		case probe.ActivitySignalShellPrompt:
			projection, err := m.projectionForSession(session)
			if err == nil && projection != nil && projection.TopFrame != nil && !isPidAliveFn(projection.TopFrame.PID) {
				_ = m.sweepOnce()
				return
			}
			status = agentpkg.StatusIdle
		default:
			return
		}

		// Issue #1: Error Guard — don't overwrite StatusError
		m.mu.Lock()
		if m.currentStatus[session] == agentpkg.StatusError {
			m.mu.Unlock()
			return // respect Error Guard
		}
		m.currentStatus[session] = status
		m.mu.Unlock()

		if projection, err := m.setProjectionTopStatus(session, status); err == nil && projection != nil {
			normalized := buildProjectionNormalized(projection, agentType, "probe:activity", time.Now().UnixNano(), agentpkg.DeriveResult{})
			m.broadcastToSession(session, normalized)
			return
		}

		normalized := agentpkg.NormalizedEvent{
			AgentType:    agentType,
			Status:       string(status),
			RawEventName: "probe:activity",
			BroadcastTs:  time.Now().UnixNano(),
		}
		m.broadcastToSession(session, normalized)
	}
}
