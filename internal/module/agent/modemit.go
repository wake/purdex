package agent

import (
	agentpkg "github.com/wake/purdex/internal/agent"
)

// emitMu serialises "broadcast a light frame and remember it as the
// baseline", so lastEmittedLights is always the frame that really went out
// last and the mod worker (which reads a projection, compares it with the
// baseline and sends) can never send a projection older than a hook emit
// that went out in between. It is the b-2 emit slot's mutex brought forward
// (plan U1-2b-2); the hook paths still read their projection before taking
// it, which b-2 fixes.
//
// Lock order is total: emitMu → m.mu → modMu. Nothing may take emitMu or
// emit while holding m.mu or modMu. Whatever holds emitMu may read the
// frame store, tmux and the session provider (none of them calls back into
// this module) and takes m.mu / modMu briefly; it never takes a second
// emitMu.
//
// emitMu is taken in exactly these places: emitRecorded (hook, probe and
// sweep emits) and Module.emitSessionState (the mod worker), plus
// Module.seedBaselineFromSnapshot (sendSnapshot).

// seedBaselineLocked records the frame a snapshot just sent as session's
// baseline, but only when the session has none: the baseline stands for what
// every connection has seen, so a connection arriving later must not rewrite
// it. Without a seed the first mod worker round after a daemon restart would
// send the light the subscriber was just told. The caller holds emitMu (the
// snapshot's critical section) and m.mu; this takes neither.
func (m *Module) seedBaselineLocked(session string, p *SessionProjection, n agentpkg.NormalizedEvent) {
	if session == "" || p == nil || p.TopFrame == nil || n.Status == string(agentpkg.StatusClear) {
		return
	}
	if _, ok := m.lastEmittedLights[session]; !ok {
		m.lastEmittedLights[session] = lightsDigestOf(p, n)
	}
}

// emitRecorded puts n on the wire under session code and, only when it went
// out, records its digest as session's baseline. Callers resolve code first
// and hold no module lock.
func (m *Module) emitRecorded(code, session string, p *SessionProjection, n agentpkg.NormalizedEvent) bool {
	m.emitMu.Lock()
	defer m.emitMu.Unlock()
	if !m.emitNormalizedToCode(code, n) {
		return false
	}
	m.recordEmittedLights(session, p, n)
	return true
}

// broadcastRecorded resolves the tmux session name to a code (the probe has
// no hook payload, hence no tmux_session_id) and emits n with emitRecorded.
// False when nothing went out. A nil projection records no baseline: what
// went out is a frame the digest cannot describe, so the worker must not
// compare against an older one.
func (m *Module) broadcastRecorded(tmuxSession string, p *SessionProjection, n agentpkg.NormalizedEvent) bool {
	if m.core == nil {
		return false
	}
	code := m.resolveSessionCode(tmuxSession)
	if code == "" {
		return false
	}
	return m.emitRecorded(code, tmuxSession, p, n)
}

// emitHookRecorded routes a hook-derived frame to its WS code (preferring
// the immutable tmux_session_id) and records it as the baseline when it went
// out. Returns the (decision, reason) tuple the trace pipeline annotates.
func (m *Module) emitHookRecorded(req EventRequest, p *SessionProjection, n agentpkg.NormalizedEvent) (string, string) {
	if m.core == nil {
		return "skipped", "core_unavailable"
	}
	code, path := m.resolveSessionCodeFromHook(req)
	if code == "" {
		return "skipped", "session_code_missing"
	}
	if !m.emitRecorded(code, req.TmuxSession, p, n) {
		return "skipped", "core_unavailable"
	}
	return "broadcasted", string(path)
}
