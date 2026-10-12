package peers

import (
	"context"
	"errors"
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
// No gate registered is not paused (a daemon without the team module). A gate that cannot answer — team.db busy or full, or a
// registered service that is not a gate — is an error: a paused session takes no message, so a message that cannot be judged
// is held and the sender told to retry (503), never let through.
func (m *Module) paused(sessionID, from, text string) (bool, error) {
	svc, ok := m.core.Registry.Get(PauseGateKey)
	if !ok {
		return false, nil
	}
	gate, ok := svc.(PauseGate)
	if !ok {
		m.logf("peers: pause gate: the registered service is %T, not a PauseGate", svc)
		return false, errPauseUnavailable
	}
	paused, err := gate.PauseVerdict(sessionID, from, text, time.Now().UnixMilli())
	if err != nil {
		m.logf("peers: pause gate: %v", err)
		return false, errPauseUnavailable
	}
	return paused, nil
}

// errPauseUnavailable is why a message was held: the pause state could not be read.
var errPauseUnavailable = errors.New("pause state unavailable")

// pauseUnavailableDetail is the detail of the 503 a held message answers.
const pauseUnavailableDetail = "pause state unavailable; retry"

// FlowSenderKey is the registry key of the pause's flow sender (unattended quota pause spec §3.2): SendFlow, the one way past a
// pause, is on its own interface under its own key so the shared Sender every module holds cannot reach it. Only the pause
// outbox (UP-2a) takes it.
const FlowSenderKey = "peers.flow-sender"

// FlowSender sends the pause's own flow messages (resume notices, bounce lists, reask controls) to a paused session.
type FlowSender interface {
	SendFlow(ctx context.Context, req peers.SendRequest) (peers.SendResponse, error)
}

// pausedRefusal is what a paused session answers: 409 session_paused with the one fixed detail.
func pausedRefusal() *deliveryRefusal {
	return refusalOf(409, peers.ErrSessionPaused, peers.SessionPausedDetail)
}

// pauseHeldRefusal is what a message the gate could not judge answers: 503 not_ready.
func pauseHeldRefusal() *deliveryRefusal {
	return refusalOf(503, peers.ErrNotReady, pauseUnavailableDetail)
}
