package stream

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/wake/tmux-box/internal/bridge"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/module/cc"
	"github.com/wake/tmux-box/internal/module/session"
)

// StreamModule manages stream mode data pipelines:
// relay connection management, SPA subscriber fan-out, and mode switching.
type StreamModule struct {
	core     *core.Core
	bridge   *bridge.Bridge
	sessions session.SessionProvider
	ccOps    cc.CCOperator
	ccDetect cc.CCDetector
	locks    *handoffLocks
}

// New creates a new StreamModule.
func New() *StreamModule {
	return &StreamModule{}
}

func (m *StreamModule) Name() string           { return "stream" }
func (m *StreamModule) Dependencies() []string { return []string{"session", "cc"} }

func (m *StreamModule) Init(c *core.Core) error {
	m.core = c
	m.bridge = bridge.New()
	m.sessions = c.Registry.MustGet(session.RegistryKey).(session.SessionProvider)
	m.ccOps = c.Registry.MustGet(cc.OperatorKey).(cc.CCOperator)
	m.ccDetect = c.Registry.MustGet(cc.DetectorKey).(cc.CCDetector)
	m.locks = newHandoffLocks()
	return nil
}

func (m *StreamModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/{code}/handoff", m.handleHandoff)
	mux.HandleFunc("/ws/cli-bridge/{code}", m.handleCliBridge)
	mux.HandleFunc("/ws/cli-bridge-sub/{code}", m.handleCliBridgeSubscribe)
}

func (m *StreamModule) Start(_ context.Context) error {
	m.core.Events.OnSubscribe(m.sendRelaySnapshot)
	return nil
}

func (m *StreamModule) Stop(_ context.Context) error {
	return nil
}

// sendRelaySnapshot pushes current relay state to a newly connected event subscriber.
func (m *StreamModule) sendRelaySnapshot(sub *core.EventSubscriber) {
	for _, key := range m.bridge.RelaySessionNames() {
		msg, err := json.Marshal(core.HostEvent{Type: "relay", Session: key, Value: "connected"})
		if err != nil {
			continue
		}
		sub.Send(msg)
	}
}
