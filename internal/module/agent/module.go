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

	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/module/session"
	"github.com/wake/tmux-box/internal/store"
)

// Module is the agent hook event module.
type Module struct {
	core      *core.Core
	events    *store.AgentEventStore
	sessions  session.SessionProvider
	uploadDir string
}

// New creates a new agent Module backed by the given AgentEventStore.
func New(events *store.AgentEventStore) *Module {
	return &Module{events: events}
}

func (m *Module) Name() string           { return "agent" }
func (m *Module) Dependencies() []string { return []string{"session"} }

// Init retrieves the SessionProvider from the service registry.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	svc, ok := c.Registry.Get(session.RegistryKey)
	if !ok {
		log.Printf("[agent] warning: session provider not found in registry")
		return nil
	}
	m.sessions = svc.(session.SessionProvider)

	// Expose event store so other modules (e.g. session rename) can update it.
	c.Registry.Register("agent.events", m.events)

	if m.uploadDir == "" {
		home, _ := os.UserHomeDir()
		m.uploadDir = filepath.Join(home, "tmp", "tbox-upload")
	}
	return nil
}

// RegisterRoutes registers the agent API endpoints.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agent/event", m.handleEvent)
	mux.HandleFunc("GET /api/agent/hook-status", m.handleHookStatus)
	mux.HandleFunc("POST /api/agent/hook-setup", m.handleHookSetup)
	mux.HandleFunc("POST /api/agent/upload", m.handleUpload)

	// Upload management
	mux.HandleFunc("GET /api/upload/stats", m.handleUploadStats)
	mux.HandleFunc("GET /api/upload/files", m.handleUploadFiles)
	mux.HandleFunc("DELETE /api/upload/files/{session}/{filename}", m.handleDeleteUploadFile)
	mux.HandleFunc("DELETE /api/upload/files/{session}", m.handleDeleteUploadSession)
	mux.HandleFunc("DELETE /api/upload/files", m.handleDeleteAllUploads)
}

// Start registers an OnSubscribe callback to send snapshot data on WS connect.
func (m *Module) Start(_ context.Context) error {
	m.core.Events.OnSubscribe(m.sendSnapshot)
	log.Println("[agent] hook event endpoint registered")
	return nil
}

// Stop is a no-op.
func (m *Module) Stop(_ context.Context) error { return nil }

// sendSnapshot sends the latest hook event for each known session to a new WS subscriber.
func (m *Module) sendSnapshot(sub *core.EventSubscriber) {
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("[agent] snapshot list: %v", err)
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
		enriched := m.buildAgentEvent(ev.TmuxSession, ev.EventName, ev.RawEvent, ev.AgentType, ev.BroadcastTs)
		payload, _ := json.Marshal(enriched)
		event := core.HostEvent{Type: "hook", Session: code, Value: string(payload)}
		data, _ := json.Marshal(event)
		sub.Send(data)
	}
}
