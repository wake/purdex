package agent

import (
	agentpkg "github.com/wake/purdex/internal/agent"
)

// The emit slot itself (hookEmitter, emitSession) is in hookemitter.go; this
// file keeps the light-baseline helper the snapshot uses.

// seedBaselineLocked records the frame a snapshot just sent as session's
// baseline, but only when the session has none: the baseline stands for what
// every connection has seen, so a connection arriving later must not rewrite
// it. Without a seed the first mod worker round after a daemon restart would
// send the light the subscriber was just told. The caller holds emit.mu (the
// snapshot's critical section) and m.mu; this takes neither.
func (m *Module) seedBaselineLocked(session string, p *SessionProjection, n agentpkg.NormalizedEvent) {
	if session == "" || p == nil || p.TopFrame == nil || n.Status == string(agentpkg.StatusClear) {
		return
	}
	if _, ok := m.lastEmittedLights[session]; !ok {
		m.lastEmittedLights[session] = lightsDigestOf(p, n)
	}
}
