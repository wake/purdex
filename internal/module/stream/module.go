package stream

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/bridge"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
)

// RelayGatewayKey is the service-locator key under which the stream bridge is
// published as the relay gateway. In-process launch consumers (dispatch's M0
// launch durable cut, P.7) resolve it and use it as an execution.RelayGateway
// (HasRelay / SubscriberToRelay) — the bridge is the daemon's single relay
// registry, so a from-zero launch must probe/push through the same instance the
// WS relay handlers register into. Registration is additive: it does not change
// any existing bridge/relay/stream behaviour.
const RelayGatewayKey = "stream.relaygateway"

type livenessProber interface {
	IsAliveFor(agentType, target string) bool
	CheckReadiness(agentType, target string) (probe.ReadinessResult, bool)
}

// StreamModule manages stream mode data pipelines:
// relay connection management, SPA subscriber fan-out, and mode switching.
type StreamModule struct {
	core     *core.Core
	bridge   *bridge.Bridge
	sessions session.SessionProvider
	ccOps    agentcc.CCOperator
	prober   livenessProber
	locks    *handoffLocks

	// termMu guards termHandler, the single upper-layer seam that consumes the
	// authoritative process-exit terminal signal (spec §5.3). The execution
	// layer (P.8) registers a handler via SetTerminalHandler.
	termMu      sync.RWMutex
	termHandler TerminalHandler
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
	// Publish the bridge as the relay gateway so in-process launch consumers
	// (dispatch P.7) probe/push relays through this same instance. Registered
	// before any dependent module Init runs — dispatch depends on "stream", so
	// topological Init ordering guarantees the key exists when dispatch reads it.
	c.Registry.Register(RelayGatewayKey, m.bridge)
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
