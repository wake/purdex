package stream

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/wake/purdex/internal/bridge"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
)

// StreamModule manages stream mode data pipelines:
// relay connection management, SPA subscriber fan-out, and mode switching.
type StreamModule struct {
	core     *core.Core
	bridge   *bridge.Bridge
	sessions session.SessionProvider
	ccOps    agentcc.CCOperator
	prober   *probe.Prober
	locks    *handoffLocks
}

// New creates a new StreamModule.
func New() *StreamModule {
	return &StreamModule{}
}

func (m *StreamModule) Name() string           { return "stream" }
func (m *StreamModule) Dependencies() []string { return []string{"session", "agent"} }

func (m *StreamModule) Init(c *core.Core) error {
	m.core = c
	m.bridge = bridge.New()
	m.sessions = c.Registry.MustGet(session.RegistryKey).(session.SessionProvider)
	m.ccOps = c.Registry.MustGet(agentcc.OperatorKey).(agentcc.CCOperator)
	m.prober = c.Registry.MustGet("agent.prober").(*probe.Prober)
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
