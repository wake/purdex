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

	prober    *probe.Prober
	probeOrch *probeOrchestrator
	tmux      tmux.Executor

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

	pathHintDedup  *PathHintDedupCache
	pathHintBuffer *PathHintRingBuffer
}

// Test seams for Module.New. framesInitFn failure is fatal (hook processing
// depends on frames); tracesInitFn failure is best-effort (trace
// observability degrades to nil, daemon continues).
var (
	framesInitFn = func(e *store.AgentEventStore) (*store.FramesStore, error) { return e.Frames() }
	tracesInitFn = func(e *store.AgentEventStore) (*store.TraceStore, error) { return e.Traces() }
)

// New creates a new agent Module backed by the given AgentEventStore.
//
// Returns an error ONLY when the frames store cannot be initialized —
// typically when on-disk agent_frames contains malformed subagents_json
// that migrateFramesDB refuses to classify. That failure is fatal because
// hook processing depends on m.frames; continuing with m.frames == nil
// would degrade silently via the module's m.frames == nil fallbacks.
//
// Trace store initialization is best-effort: the module already tolerates
// m.traces == nil / m.traceSink == nil in normal operation (monitor
// endpoints degrade, hook processing still runs). A trace-table migration
// or corruption error is logged and the module continues without trace
// observability, not treated as a daemon-fatal condition.
func New(events *store.AgentEventStore) (*Module, error) {
	var frames *store.FramesStore
	var traces *store.TraceStore
	if events != nil {
		var err error
		frames, err = framesInitFn(events)
		if err != nil {
			return nil, fmt.Errorf("agent module: frames store: %w", err)
		}
		traces, err = tracesInitFn(events)
		if err != nil {
			log.Printf("[agent] traces store unavailable, continuing without trace observability: %v", err)
			traces = nil
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
		pathHintDedup:   NewPathHintDedupCache(5 * time.Second),
		pathHintBuffer:  NewPathHintRingBuffer(200),
	}
	if traces != nil {
		m.traceSink = newHookTraceSink(traces)
	}
	// probeOrchestrator owns probe-watcher lifecycle. Created here (not in
	// Init) so it is always available for tests that bypass Init and assign
	// m.prober directly. The orchestrator resolves m.prober lazily, so
	// "AFTER prober is set" semantics hold at startWatch call time.
	m.probeOrch = newProbeOrchestrator(m)
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

	// Surface whether the hook hot path will use the LookupCodeByName fast
	// path or fall back to the 1+7×S ListSessions fan-out. Anything that
	// wraps SessionProvider with a decorator that drops LookupCodeByName
	// will be visible at startup instead of as a silent latency regression.
	if _, ok := m.sessions.(sessionCodeLookuper); ok {
		log.Print("[agent] hook fast-path active (LookupCodeByName available)")
	} else {
		log.Print("[agent] hook fast-path NOT active — sessions does not implement LookupCodeByName")
	}

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
	mux.HandleFunc("GET /api/agent/title/status", m.handleTitleStatus)
	mux.HandleFunc("POST /api/agent/title/setup", m.handleTitleSetup)
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
	if _, ok := m.activeWatchers[oldName]; ok {
		// W3 撤回: rename is now stop-only. Phase 4a-1 wired a stopWatch +
		// startWatch(newName) sequence to keep the screen-watcher alive across
		// rename, but with manageActivityWatch reduced to a default no-op
		// there is no production caller starting watchers in the first place.
		// W6 will reintroduce starts via ProbeIntent; until then, evict the
		// renamed entry and tear down the orchestrator-side watcher (if any).
		// activeWatchers eviction is m.mu-protected; caller holds m.mu.
		delete(m.activeWatchers, oldName)
		// orchestrator stop is lock-free wrt m.mu (it touches prober.watcherMu,
		// a different mutex) so calling it while we hold m.mu is safe.
		// nil-prober is handled inside stopWatch (R14 fix).
		m.probeOrch.stopWatch(oldName)
		// Codex finding #2 regression (kept under W3): migrate the active
		// graceWindow so a hook-set status that was just recorded under
		// oldName is not overwritten by a probe event arriving for newName
		// within probeGraceWindow once W6 wires starts again.
		m.probeOrch.migrateLastHookAt(oldName, newName)
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
		if !result.Valid {
			// Pre-W2 stored EventName (e.g. opencode legacy literal "Stop")
			// no longer matches a Pdx-prefixed catalog entry; mirror the
			// hot-path invalid-result cleanup at handler.go:230 so a
			// subsequent sendSnapshot doesn't broadcast a stale row.
			if m.events != nil {
				if err := m.events.Delete(ev.TmuxSession); err != nil {
					log.Printf("[agent] replay cleanup of legacy event: %v", err)
				}
			}
			continue
		}
		if result.Status != "" {
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
		if !result.Valid {
			// Pre-W2 stored EventName no longer matches a Pdx-prefixed
			// catalog entry; mirror the hot-path invalid-result cleanup at
			// handler.go:230 so the SPA doesn't see a resurrected
			// raw_event_name on cold reconnect (which would re-key
			// hook-module lastTrigger and surface stale legacy events
			// despite replayFromDB intentionally skipping them).
			if m.events != nil {
				if err := m.events.Delete(ev.TmuxSession); err != nil {
					log.Printf("[agent] snapshot cleanup of legacy event: %v", err)
				}
			}
			continue
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

// manageActivityWatch is invoked by hook handlers as a status changes; W3
// reduces it to a default no-op (stop-only) path. When a watcher is already
// registered for `session`, it is stopped + evicted from activeWatchers; no
// new watcher is ever started here.
//
// W3 撤回 rationale: Phase 4a-1 wired this function as the always-on probe
// start site, which violated the "probe is recovery-only" v2.0 contract by
// covering every Waiting/Running/Idle transition with a screen watcher. W6
// will reintroduce starts via an explicit ProbeIntent caller that calls
// `m.probeOrch.startWatch(session, agentType, opts)` directly with chosen
// WatchOptions. The newStatus parameter is intentionally retained (reserved
// for that future caller) but currently unused.
//
// R3 fix: m.activeWatchers is owned by this function (and renameSessionLocked);
// the orchestrator deliberately does not touch it.
func (m *Module) manageActivityWatch(session, agentType string, newStatus agentpkg.Status) {
	_ = newStatus // reserved for W6 ProbeIntent wiring
	_ = agentType // reserved for W6 ProbeIntent wiring (e.g. profile lookup)

	m.mu.Lock()
	_, wasWatching := m.activeWatchers[session]
	delete(m.activeWatchers, session)
	m.mu.Unlock()
	if wasWatching {
		m.probeOrch.stopWatch(session)
	}
}
