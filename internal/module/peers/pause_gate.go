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
// bounced when the session has any pause row and says paused, with the detail to answer ("" = the fixed SessionPausedDetail); otherwise it writes nothing. from is the sender's address as
// the receiver shows it, at the time of the attempt (unix ms).
type PauseGate interface {
	PauseVerdict(sessionID, from, text string, at int64) (paused bool, detail string, err error)
}

// flowKey marks a context as a pause-flow send. It is an unexported type, so nothing outside this package can build the
// key, and a context built from an HTTP request never carries it: only moduleSender.SendFlow, an in-process Go call, sets it.
type flowKey struct{}

// selfNoticeKey marks a context as a daemon notice spoken in the session's OWN voice (SendSelfNotice): the origin is the target's own inbox.
// Like flowKey it is unexported, so only an in-process Go call can set it.
type selfNoticeKey struct{}

func isSelfNotice(ctx context.Context) bool {
	v, _ := ctx.Value(selfNoticeKey{}).(bool)
	return v
}

// isFlow reports whether ctx came from SendFlow.
func isFlow(ctx context.Context) bool {
	v, _ := ctx.Value(flowKey{}).(bool)
	return v
}

// paused asks the registered gate about sessionID; true means the message must not be delivered (its bounce is written).
// No gate registered is not paused (a daemon without the team module). A gate that cannot answer — team.db busy or full, or a
// registered service that is not a gate — is an error: a paused session takes no message, so a message that cannot be judged
// is held and the sender told to retry (503), never let through.
func (m *Module) paused(sessionID, from, text string) (bool, string, error) {
	svc, ok := m.core.Registry.Get(PauseGateKey)
	if !ok {
		return false, "", nil
	}
	gate, ok := svc.(PauseGate)
	if !ok {
		m.logf("peers: pause gate: the registered service is %T, not a PauseGate", svc)
		return false, "", errPauseUnavailable
	}
	paused, detail, err := gate.PauseVerdict(sessionID, from, text, time.Now().UnixMilli())
	if err != nil {
		m.logf("peers: pause gate: %v", err)
		return false, "", errPauseUnavailable
	}
	if detail == "" {
		detail = peers.SessionPausedDetail
	}
	return paused, detail, nil
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

// SelfNoticeSenderKey is the registry key of the team module's notice sender: a notice to a team lead about a member the lead's host does not
// run (a member on another host), which has no inbox here to speak from. The daemon has no identity of its own, so the notice is sent from
// the lead's own inbox to the lead - the one thing SendSelfNotice allows over Send is that self-addressed shape (self_target); the pause
// check and everything else apply as to any message.
const SelfNoticeSenderKey = "peers.self-notice-sender"

// SelfNoticeSender sends a notice from a session's own inbox to that session.
type SelfNoticeSender interface {
	SendSelfNotice(ctx context.Context, req peers.SendRequest) (peers.SendResponse, error)
}

// pausedRefusal is what a paused session answers: 409 session_paused with the gate's detail.
func pausedRefusal(detail string) *deliveryRefusal {
	return refusalOf(409, peers.ErrSessionPaused, detail)
}

// pauseHeldRefusal is what a message the gate could not judge answers: 503 not_ready.
func pauseHeldRefusal() *deliveryRefusal {
	return refusalOf(503, peers.ErrNotReady, pauseUnavailableDetail)
}
