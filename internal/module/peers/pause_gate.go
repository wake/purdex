package peers

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/peers"
)

// PauseGateKey is the service-registry key of the pause gate (unattended quota pause spec §3.2): the team module registers it,
// and peers asks it before a message reaches a session — through this interface, never by importing team.
const PauseGateKey = "peers.pause-gate"

// PauseGate answers whether a session is paused. PauseVerdict, in one write transaction of its own, records the message as
// bounced when the session has any pause row and says paused; otherwise it writes nothing. from is the sender's address as
// the receiver shows it, at the time of the attempt (unix ms).
type PauseGate interface {
	PauseVerdict(sessionID, from, text string, at int64) (paused bool, err error)
}

// flowKey marks a context as a pause-flow send. It is an unexported type, so nothing outside this package can build the
// key, and a context built from an HTTP request never carries it: only moduleSender.SendFlow, an in-process Go call, sets it.
type flowKey struct{}

// isFlow reports whether ctx came from SendFlow.
func isFlow(ctx context.Context) bool {
	v, _ := ctx.Value(flowKey{}).(bool)
	return v
}

// paused asks the registered gate about sessionID; true means the message must not be delivered (its bounce is written).
// No gate registered is not paused. A gate that cannot answer (team.db trouble) does not stop the daemon's messages: the
// failure is logged and the message goes through, as it did before the gate existed.
func (m *Module) paused(sessionID, from, text string) bool {
	svc, ok := m.core.Registry.Get(PauseGateKey)
	if !ok {
		return false
	}
	gate, ok := svc.(PauseGate)
	if !ok {
		return false
	}
	paused, err := gate.PauseVerdict(sessionID, from, text, time.Now().UnixMilli())
	if err != nil {
		m.logf("peers: pause gate: %v", err)
		return false
	}
	return paused
}

// pausedRefusal is what a paused session answers: 409 session_paused with the one fixed detail.
func pausedRefusal() *deliveryRefusal {
	return refusalOf(409, peers.ErrSessionPaused, peers.SessionPausedDetail)
}
