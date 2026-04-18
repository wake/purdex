// Package agent provides the agent hook event module.
// It receives hook events from `pdx hook`, stores them in AgentEventStore,
// and broadcasts to WS subscribers.
package agent

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
)

// Module is the agent hook event module.
type Module struct {
	core      *core.Core
	events    *store.AgentEventStore
	sessions  session.SessionProvider
	registry  *agentpkg.Registry
	uploadDir string

	prober *probe.Prober

	mu             sync.Mutex
	currentStatus  map[string]agentpkg.Status
	subagents      map[string][]string
	activeWatchers map[string]string // tmuxSession → agentType
}

// New creates a new agent Module backed by the given AgentEventStore.
func New(events *store.AgentEventStore) *Module {
	return &Module{
		events:         events,
		registry:       agentpkg.NewRegistry(),
		currentStatus:  make(map[string]agentpkg.Status),
		subagents:      make(map[string][]string),
		activeWatchers: make(map[string]string),
	}
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
	m.prober = probe.New(c.Tmux)
	m.prober.RegisterProcessNames("cc", c.Cfg.Detect.CCCommands)
	m.prober.RegisterContentMatcher("cc", agentcc.NewContentMatcher())
	m.prober.RegisterReadiness("cc", agentcc.NewReadinessChecker(c.Tmux))
	m.prober.RegisterProcessNames("codex", []string{"codex"})
	m.prober.RegisterReadiness("codex", codex.NewReadinessChecker(c.Tmux))
	c.Registry.Register("agent.prober", m.prober)

	// CC provider
	ccProvider := agentcc.NewProvider(m.prober, c.Tmux, c.Cfg, &c.CfgMu)
	ccProvider.RegisterServices(c.Registry)
	m.registry.Register(ccProvider)

	// Listen for config changes to update CC process names
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		cmds := c.Cfg.Detect.CCCommands
		newDir := c.Cfg.UploadDir
		c.CfgMu.RUnlock()
		m.prober.UpdateProcessNames("cc", cmds)
		if newDir != "" {
			m.mu.Lock()
			m.uploadDir = newDir
			m.mu.Unlock()
		}
	})

	// Codex provider
	m.registry.Register(codex.NewProvider())

	// Expose registry for other modules
	c.Registry.Register("agent.registry", m.registry)

	return nil
}

// RegisterRoutes registers the agent API endpoints.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agent/event", m.handleEvent)
	mux.HandleFunc("GET /api/hooks/{agent}/status", m.handleHookStatus)
	mux.HandleFunc("POST /api/hooks/{agent}/setup", m.handleHookSetup)
	mux.HandleFunc("GET /api/agent/{agent}/statusline/status", m.handleStatuslineStatus)
	mux.HandleFunc("POST /api/agent/{agent}/statusline/setup", m.handleStatuslineSetup)
	mux.HandleFunc("POST /api/agent/status", m.handleAgentStatus)
	mux.HandleFunc("POST /api/agent/check-alive/{session}", m.handleCheckAlive)
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
	m.replayFromDB()

	m.core.Events.OnSubscribe(func(sub *core.EventSubscriber) {
		m.sendSnapshot(sub)
		m.sendStatuslineSnapshot(sub)
		go m.checkAliveAll(sub)
	})

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
	if m.prober != nil {
		m.prober.StopAllWatches()
	}
	m.mu.Lock()
	m.activeWatchers = make(map[string]string)
	m.mu.Unlock()
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

// replayFromDB rebuilds in-memory currentStatus from persisted events.
func (m *Module) replayFromDB() {
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("[agent] replay: %v", err)
		return
	}
	for _, ev := range all {
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

// checkAliveAll checks all tracked sessions for liveness and broadcasts clear events for dead ones.
func (m *Module) checkAliveAll(sub *core.EventSubscriber) {
	if m.sessions == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	all, err := m.events.ListAll()
	if err != nil {
		return
	}

	sessions, err := m.sessions.ListSessions()
	if err != nil {
		return
	}
	nameToCode := make(map[string]string, len(sessions))
	for _, s := range sessions {
		nameToCode[s.Name] = s.Code
	}

	for _, ev := range all {
		select {
		case <-ctx.Done():
			return
		default:
		}

		code, ok := nameToCode[ev.TmuxSession]
		if !ok {
			// Not in current sessions list — could be a real orphan or
			// a transient ListSessions inconsistency.  Use tmux.HasSession
			// as the authoritative tiebreaker before deleting persistent
			// state: this directly checks tmux session existence, unlike
			// provider.IsAlive which only checks whether the CC/Codex
			// process is the pane's current command (a user exiting CC
			// in a live tmux session would wrongly trigger deletion).
			//
			// No broadcast is sent here: orphaned sessions have no code
			// (resolveSessionCode would return ""), so the frontend cannot
			// be notified by session code.  The frontend's session-closed
			// detection (useMultiHostEventWs) handles this case via the
			// sessions WS event, which fires whenever ListSessions output
			// changes.
			if m.core != nil && m.core.Tmux != nil && m.core.Tmux.HasSession(ev.TmuxSession) {
				continue // transient — leave it alone
			}
			m.mu.Lock()
			delete(m.currentStatus, ev.TmuxSession)
			delete(m.subagents, ev.TmuxSession)
			m.mu.Unlock()
			_ = m.events.Delete(ev.TmuxSession)
			continue
		}
		_, ok = m.registry.Get(ev.AgentType)
		if !ok {
			continue
		}

		tmuxTarget := ev.TmuxSession + ":"
		if !m.prober.IsAliveFor(ev.AgentType, tmuxTarget) {
			m.mu.Lock()
			delete(m.currentStatus, ev.TmuxSession)
			delete(m.subagents, ev.TmuxSession)
			delete(m.activeWatchers, ev.TmuxSession)
			m.mu.Unlock()

			m.prober.StopWatch(tmuxTarget)
			_ = m.events.Delete(ev.TmuxSession)

			normalized := agentpkg.NormalizedEvent{
				AgentType:    ev.AgentType,
				Status:       string(agentpkg.StatusClear),
				RawEventName: "isAlive:dead",
				BroadcastTs:  time.Now().UnixNano(),
			}
			payload, _ := json.Marshal(normalized)
			m.core.Events.Broadcast(code, "hook", string(payload))
		}
	}
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

	if newStatus == agentpkg.StatusWaiting {
		m.mu.Lock()
		m.activeWatchers[session] = agentType
		m.mu.Unlock()
		m.prober.StartWatch(session+":", m.onActivityDetected(session, agentType))
	}
}

// onActivityDetected returns a callback for when screen activity is detected
// during a waiting state. The callback checks if the watcher is still active
// (a hook event may have already superseded it), then runs Readiness to
// determine the new status.
func (m *Module) onActivityDetected(session, agentType string) func(string) {
	return func(target string) {
		m.mu.Lock()
		if _, active := m.activeWatchers[session]; !active {
			m.mu.Unlock()
			return
		}
		delete(m.activeWatchers, session)
		m.mu.Unlock()

		if !m.prober.IsAliveFor(agentType, target) {
			return
		}

		result, ok := m.prober.CheckReadiness(agentType, target)
		if !ok {
			return
		}

		// Issue #2: StatusWaiting — restart watcher to keep monitoring
		if result.Status == agentpkg.StatusWaiting {
			m.mu.Lock()
			m.activeWatchers[session] = agentType
			m.mu.Unlock()
			m.prober.StartWatch(target, m.onActivityDetected(session, agentType))
			return
		}

		// Issue #1: Error Guard — don't overwrite StatusError
		m.mu.Lock()
		if m.currentStatus[session] == agentpkg.StatusError {
			m.mu.Unlock()
			return // respect Error Guard
		}
		m.currentStatus[session] = result.Status
		m.mu.Unlock()

		// Note: probe:activity status changes are not persisted to DB.
		// After daemon restart, replayFromDB may show stale "waiting" status,
		// but the next hook event or checkAliveAll will correct it.

		normalized := agentpkg.NormalizedEvent{
			AgentType:    agentType,
			Status:       string(result.Status),
			RawEventName: "probe:activity",
			BroadcastTs:  time.Now().UnixNano(),
		}
		m.broadcastToSession(session, normalized)
	}
}
