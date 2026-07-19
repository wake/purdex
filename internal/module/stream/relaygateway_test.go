package stream

import (
	"testing"

	"github.com/stretchr/testify/require"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// relayGatewayContract is the narrow port dispatch's launcher needs off the
// bridge. It mirrors execution.RelayGateway without importing execution here, so
// the test proves the registered value satisfies that exact method set.
type relayGatewayContract interface {
	HasRelay(name string) bool
	SubscriberToRelay(name string, data []byte)
}

// TestInit_RegistersRelayGateway proves stream.Init publishes the bridge under
// RelayGatewayKey (the P.7 seam) and that the published value satisfies the
// launcher's relay-gateway contract — additive wiring, no behaviour change.
func TestInit_RegistersRelayGateway(t *testing.T) {
	reg := core.NewServiceRegistry()
	reg.Register(session.RegistryKey, session.SessionProvider(&fakeSessionProvider{}))
	reg.Register(agentcc.OperatorKey, agentcc.CCOperator(&fakeCCOperator{}))
	reg.Register("agent.prober", probe.New(tmux.NewFakeExecutor()))

	c := core.New(core.CoreDeps{Registry: reg})
	m := New()
	require.NoError(t, m.Init(c))

	svc, ok := reg.Get(RelayGatewayKey)
	require.True(t, ok, "relay gateway must be registered under %q", RelayGatewayKey)

	gw, ok := svc.(relayGatewayContract)
	require.True(t, ok, "registered gateway must satisfy HasRelay/SubscriberToRelay")
	// Probing an unknown relay is a safe no-op false — exercises the live method
	// set rather than a nil interface.
	require.False(t, gw.HasRelay("no-such-session"))
}
