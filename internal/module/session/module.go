package session

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

// SessionModule manages tmux sessions, meta cache, and HTTP API.
type SessionModule struct {
	meta        *store.MetaStore
	tmux        tmux.Executor
	core        *core.Core
	cancelWatch context.CancelFunc
	wstate      watcherState
	waitForGate chan bool
}

// NewSessionModule creates a SessionModule with the given MetaStore.
func NewSessionModule(meta *store.MetaStore) *SessionModule {
	return &SessionModule{meta: meta}
}

func (m *SessionModule) Name() string         { return "session" }
func (m *SessionModule) Dependencies() []string { return nil }

func (m *SessionModule) Init(c *core.Core) error {
	m.core = c
	m.tmux = c.Tmux
	c.Registry.Register(RegistryKey, SessionProvider(m))
	return nil
}

func (m *SessionModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions", m.handleList)
	mux.HandleFunc("GET /api/sessions/{code}", m.handleGet)
	mux.HandleFunc("POST /api/sessions", m.handleCreate)
	mux.HandleFunc("PATCH /api/sessions/{code}", m.handleRename)
	mux.HandleFunc("DELETE /api/sessions/{code}", m.handleDelete)
	mux.HandleFunc("POST /api/sessions/{code}/mode", m.handleSwitchMode)
	mux.HandleFunc("/ws/terminal/{code}", m.handleTerminalWS)
	mux.HandleFunc("GET /api/hooks/tmux/status", m.handleTmuxHookStatus)
	mux.HandleFunc("POST /api/hooks/tmux/setup", m.handleTmuxHookSetup)
}

func (m *SessionModule) Start(ctx context.Context) error {
	if err := m.meta.ResetStaleModes(); err != nil {
		return err
	}

	// Install tmux hooks (log warning on error, don't fail startup).
	if err := m.installTmuxHooks(); err != nil {
		log.Printf("session: failed to install tmux hooks: %v (continuing without push)", err)
	}

	// Start session watcher with a child context.
	watchCtx, cancel := context.WithCancel(ctx)
	m.cancelWatch = cancel
	m.wstate.setTmuxAlive(m.tmux.TmuxAlive())
	m.core.TmuxAliveFunc = m.TmuxAlive
	m.watchSessions(watchCtx)

	// Register OnSubscribe callback to send initial sessions snapshot.
	m.core.Events.OnSubscribe(func(sub *core.EventSubscriber) {
		sessions, err := m.ListSessions()
		if err != nil {
			log.Printf("session: OnSubscribe list error: %v", err)
			return
		}
		if sessions == nil {
			sessions = []SessionInfo{}
		}
		data, err := json.Marshal(core.HostEvent{
			Type:  "sessions",
			Value: mustMarshal(sessions),
		})
		if err != nil {
			return
		}
		sub.Send(data)
	})

	return nil
}

func (m *SessionModule) Stop(_ context.Context) error {
	// Cancel watcher goroutines.
	if m.cancelWatch != nil {
		m.cancelWatch()
	}
	// Remove tmux hooks (best-effort).
	m.removeTmuxHooks()
	return nil
}
