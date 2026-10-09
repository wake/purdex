package agent

import (
	"encoding/json"
	"log"
	"strconv"
	"sync"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// hookEmitter is the hook emit slot (plan U1-2b-2): the one critical section
// every `hook` frame on /ws/host-events goes through. Inside it a frame's
// state is read, the frame is built from that read, broadcast, and remembered
// as the light baseline, so two emits for one session can never be read in
// one order and broadcast in the other, and the mod worker (which reads a
// projection, compares it with the baseline and sends) can never send a
// projection older than a hook emit that went out in between.
//
// Lock order is total: hookEmitter.mu → Module.mu → modMu (modMu is a leaf).
// Nothing may call emitSession (or take hookEmitter.mu) while holding m.mu or
// modMu, and nothing inside the slot takes hookEmitter.mu again: it is not
// recursive. Whatever runs inside the slot (a build closure included) may
// read the frame store, tmux and the session provider (none of them calls
// back into this module) and may take m.mu / modMu briefly.
//
// hookEmitter.mu is taken in exactly these places: emitSessionWith (every
// hook, probe, sweep, non-tmux and mod worker frame) and
// Module.sendSnapshot (the subscribe-time replay). Both time their hold
// (hookemitter_hold.go).
type hookEmitter struct {
	mu sync.Mutex
	// seq counts the frames broadcast under epoch; epoch is core.BootID, or
	// BootID-n after n rotations. All protected by mu.
	seq       uint64
	epoch     string
	rotations int
	// seqMax is the largest seq before the epoch rotates; 0 means
	// hookSeqMax. A test seam: no run gets near it.
	seqMax uint64
	// hold is the mutex's hold-time distribution (hookemitter_hold.go).
	hold holdStats
}

// hookSeqMax is the largest integer a JS client represents exactly (2^53-1):
// the seq after it starts a new epoch at 1.
const hookSeqMax uint64 = 1<<53 - 1

// stamp takes the next (epoch, seq) for n. It returns an undo for a frame
// that could not be sent, so a hole never reaches a subscriber. Under mu.
func (e *hookEmitter) stamp(boot string, n *agentpkg.NormalizedEvent) (undo func()) {
	prevSeq, prevEpoch, prevRotations := e.seq, e.epoch, e.rotations
	limit := e.seqMax
	if limit == 0 {
		limit = hookSeqMax
	}
	if e.epoch == "" {
		e.epoch = boot
	}
	if e.seq >= limit {
		e.rotations++
		e.epoch = boot + "-" + strconv.Itoa(e.rotations)
		e.seq = 0
	}
	e.seq++
	n.Epoch, n.Seq = e.epoch, e.seq
	return func() { e.seq, e.epoch, e.rotations = prevSeq, prevEpoch, prevRotations }
}

// buildFn builds the frame to send from p, the session's projection as read
// inside the slot (nil for a session that is not in tmux, or one with no
// pane). False declines: nothing is sent and the counter is not spent.
type buildFn func(p *SessionProjection) (agentpkg.NormalizedEvent, bool)

// buildTolerantFn is buildFn for callers that must still emit when the
// projection cannot be read: readErr is that failure and p is nil.
type buildTolerantFn func(p *SessionProjection, readErr error) (agentpkg.NormalizedEvent, bool)

// emitSession reads sessionName's projection, builds a frame from it and
// broadcasts it under code, all inside the emit slot. sessionName == ""
// means a session that is not in tmux: build gets a nil projection and no
// session state is touched. A failed projection read sends nothing. True
// when a frame went out.
//
// The frame must be built from the p it is given, never from a projection
// read before the call: that read is not ordered against the frames that
// went out while the caller waited for the slot.
func (m *Module) emitSession(kind slotKind, code, sessionName string, build buildFn) bool {
	return m.emitSessionWith(kind, code, sessionName, func(p *SessionProjection, readErr error) (agentpkg.NormalizedEvent, bool) {
		if readErr != nil {
			log.Printf("[agent] emit slot: projection of %q: %v", sessionName, readErr)
			return agentpkg.NormalizedEvent{}, false
		}
		return build(p)
	})
}

// emitSessionWith is emitSession for callers that handle a failed read
// themselves (a claimed exit must still go out, degraded; a probe falls back
// to a minimal frame). Under mu:
//
//  1. read sessionName's projection (projectionForSessionFn);
//  2. build; false declines;
//  3. sync the in-memory view from the projection (skipped when the read
//     failed: an unknown projection must not overwrite it with a guess);
//  4. take the next seq and broadcast; a frame that could not be sent (no
//     bus) gives the seq back;
//  5. record the frame as the session's light baseline (a non-tmux frame, kind
//     kindNonTmux, as the code's last frame instead: nontmux_last.go).
//
// An empty code sends nothing: the in-memory view is still synced (steps
// 1-3), as the callers did before the slot existed.
func (m *Module) emitSessionWith(kind slotKind, code, sessionName string, build buildTolerantFn) bool {
	return m.emitSlot(kind, code, sessionName, sessionName, build)
}

// emitSlot is emitSessionWith for a caller whose frame belongs to a tmux session it does not read a projection
// of (the minimal probe frame passes sessionName "" so nothing is read or synced): notifyName is the tmux session
// the notify hub says the frame is about. "" with kindNonTmux means a session outside tmux.
func (m *Module) emitSlot(kind slotKind, code, sessionName, notifyName string, build buildTolerantFn) bool {
	e := &m.emit
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.end(e.begin(), sessionName, kind)

	var (
		p       *SessionProjection
		readErr error
	)
	if sessionName != "" {
		p, readErr = projectionForSessionFn(m, sessionName)
		if readErr != nil {
			p = nil
		}
	}
	n, ok := build(p, readErr)
	if !ok {
		return false
	}
	if sessionName != "" && readErr == nil {
		m.mu.Lock()
		syncProjectionState(m.currentStatus, m.subagents, sessionName, p)
		m.mu.Unlock()
	}
	if code == "" {
		return false
	}
	boot := ""
	if m.core != nil {
		boot = m.core.BootID
	}
	undo := e.stamp(boot, &n)
	if !m.emitNormalizedToCode(code, n) {
		undo()
		return false
	}
	m.recordEmittedLights(sessionName, p, n)
	if kind == kindNonTmux {
		m.noteNonTmuxLocked(code, n)
	}
	m.publishNotify(kind, code, notifyName, p, n)
	return true
}

// emitNormalizedToCode is the only place a `hook` frame is put on the events
// bus: it marshals the normalized event and broadcasts it. Callers MUST be
// inside the emit slot and MUST have resolved the session code; this helper
// makes no assumptions about how it was obtained. False when there is no bus
// to put it on.
func (m *Module) emitNormalizedToCode(code string, normalized agentpkg.NormalizedEvent) bool {
	if m.core == nil || m.core.Events == nil {
		return false
	}
	payload, _ := json.Marshal(normalized)
	m.core.Events.Broadcast(code, "hook", string(payload))
	return true
}

// emitSessionByName is emitSessionWith for a caller that has only the tmux
// session name (the probe has no hook payload, hence no tmux_session_id).
// The code is resolved before the slot is entered.
func (m *Module) emitSessionByName(kind slotKind, tmuxSession string, build buildTolerantFn) bool {
	code := ""
	if m.core != nil {
		code = m.resolveSessionCode(tmuxSession)
	}
	return m.emitSessionWith(kind, code, tmuxSession, build)
}

// emitHookSession routes a hook-derived frame to its WS code (preferring the
// immutable tmux_session_id) and sends it through the slot. Returns the
// (decision, reason) tuple the trace pipeline annotates; for a sent frame the
// reason is the resolution path label, so operators can grep daemon logs and
// confirm hook clients have migrated to the ID payload.
func (m *Module) emitHookSession(req EventRequest, build buildFn) (string, string) {
	return m.emitHookSessionWith(req, func(p *SessionProjection, readErr error) (agentpkg.NormalizedEvent, bool) {
		if readErr != nil {
			return agentpkg.NormalizedEvent{}, false
		}
		return build(p)
	})
}

// emitHookSessionWith is emitHookSession for a build that handles a failed
// read itself (a SessionEnd that claimed its frame).
func (m *Module) emitHookSessionWith(req EventRequest, build buildTolerantFn) (string, string) {
	var (
		code string
		path hookSessionCodePath
	)
	if m.core != nil {
		code, path = m.resolveSessionCodeFromHook(req)
	}
	if m.emitSessionWith(kindHook, code, req.TmuxSession, build) {
		return "broadcasted", string(path)
	}
	switch {
	case m.core == nil || m.core.Events == nil:
		return "skipped", "core_unavailable"
	case code == "":
		return "skipped", "session_code_missing"
	}
	return "skipped", "emit_declined"
}
