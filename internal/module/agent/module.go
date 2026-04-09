// Package agent provides the agent hook event module.
// It receives hook events from `tbox hook`, stores them in AgentEventStore,
// and broadcasts to WS subscribers.
package agent

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	agentpkg "github.com/wake/tmux-box/internal/agent"
	agentcc "github.com/wake/tmux-box/internal/agent/cc"
	"github.com/wake/tmux-box/internal/agent/codex"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/module/session"
	"github.com/wake/tmux-box/internal/store"
)

// Module is the agent hook event module.
type Module struct {
	core      *core.Core
	events    *store.AgentEventStore
	sessions  session.SessionProvider
	registry  *agentpkg.Registry
	uploadDir string

	mu            sync.Mutex
	currentStatus map[string]agentpkg.Status
	subagents     map[string][]string
}

// New creates a new agent Module backed by the given AgentEventStore.
func New(events *store.AgentEventStore) *Module {
	return &Module{
		events:        events,
		currentStatus: make(map[string]agentpkg.Status),
		subagents:     make(map[string][]string),
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

	// Expose event store so other modules (e.g. session rename) can update it.
	c.Registry.Register("agent.events", m.events)

	if m.uploadDir == "" {
		home, _ := os.UserHomeDir()
		m.uploadDir = filepath.Join(home, "tmp", "tbox-upload")
	}

	// Initialize provider registry
	m.registry = agentpkg.NewRegistry()

	// CC provider
	ccDetector := agentcc.NewDetector(c.Tmux, c.Cfg.Detect.CCCommands)
	ccProvider := agentcc.NewProvider(ccDetector, c.Tmux, c.Cfg, &c.CfgMu)
	ccProvider.RegisterServices(c.Registry)
	m.registry.Register(ccProvider)

	// Listen for config changes to update CC detector
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		cmds := c.Cfg.Detect.CCCommands
		c.CfgMu.RUnlock()
		ccDetector.UpdateCommands(cmds)
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
	mux.HandleFunc("POST /api/agent/check-alive/{session}", m.handleCheckAlive)

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
		go m.checkAliveAll(sub)
	})

	log.Println("[agent] hook event endpoint registered")
	return nil
}

// Stop is a no-op.
func (m *Module) Stop(_ context.Context) error { return nil }

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
			continue
		}
		provider, ok := m.registry.Get(ev.AgentType)
		if !ok {
			continue
		}

		tmuxTarget := ev.TmuxSession + ":"
		if !provider.IsAlive(tmuxTarget) {
			m.mu.Lock()
			delete(m.currentStatus, ev.TmuxSession)
			delete(m.subagents, ev.TmuxSession)
			m.mu.Unlock()

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
