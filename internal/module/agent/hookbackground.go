package agent

import (
	"encoding/json"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/lights"
)

// Hook-sourced background symbol (U1-2a-4, spec §7 "No mod, fixes"). CC's
// Stop hook lists the work still in flight (background_tasks) and the session's
// scheduled wake-ups (session_crons); a root frame's Stop turns that into the
// same symbol a live mod stream reports, by the same rule
// (lights.BackgroundKind). It is a fallback: applyModOverlay shows it only on
// a projection no live stream drives.
//
// The map lives under modMu (a leaf lock, see modLights) and is keyed by frame
// id. An entry is set by that frame's own Stop, replaced by the next one,
// dropped by a SessionStart (any source: a new conversation has no background
// work yet) and when the frame is deleted.

// HookBackground is the background work a Stop hook listed, by kind: shell commands, subagents, and everything else (monitor, workflow
// and the session's cron wake-ups). The conversation header carries it so the deck can say what the terminal says ("1 shell still running").
type HookBackground struct {
	Shell    int
	Subagent int
	Other    int
}

// Zero reports whether no background work is listed.
func (b HookBackground) Zero() bool { return b == HookBackground{} }

// stopBackgroundPayload is the part of CC's Stop hook input that carries the
// background work (plugin-authoring d.ts StopHookInput). Every field is
// optional; an older CC sends neither.
type stopBackgroundPayload struct {
	Tasks []lights.Task     `json:"background_tasks"`
	Crons []json.RawMessage `json:"session_crons"`
}

// hookBackgroundOf is the symbol a Stop payload reports: "" when it lists no
// monitor / workflow task and no cron, or is not parseable. Task status is
// not filtered — the payload lists only work in flight.
func hookBackgroundOf(raw json.RawMessage) lights.Background {
	var p stopBackgroundPayload
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return ""
	}
	return lights.BackgroundKind(p.Tasks, len(p.Crons))
}

// hookBackgroundCountOf counts what a Stop payload lists: shell tasks, subagent tasks, and every other kind of task plus each cron as other. The
// payload lists only work in flight, so status is not filtered. An unparseable payload counts nothing.
func hookBackgroundCountOf(raw json.RawMessage) HookBackground {
	var p stopBackgroundPayload
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return HookBackground{}
	}
	var c HookBackground
	for _, t := range p.Tasks {
		switch t.Type {
		case "shell":
			c.Shell++
		case "subagent":
			c.Subagent++
		default:
			c.Other++
		}
	}
	c.Other += len(p.Crons)
	return c
}

// setHookBackgroundCount records frameID's counts from the Stop stamped ts, under the same stamp guard as setHookBackground (a Stop at or before
// the frame's last SessionStart is refused); zero counts remove the entry.
func (m *Module) setHookBackgroundCount(frameID string, c HookBackground, ts int64) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	if cleared, ok := m.hookBgClearedAt[frameID]; ok && ts <= cleared {
		return
	}
	if c.Zero() {
		delete(m.hookBgCount, frameID)
		return
	}
	if m.hookBgCount == nil {
		m.hookBgCount = make(map[string]HookBackground)
	}
	m.hookBgCount[frameID] = c
}

// BackgroundWork is the background work the last Stop of sessionID's live Claude Code root frame listed; ok is false when no live frame
// reports the session or it listed none. It reads the frames store (no tmux listing), so the conversation stream may ask every few hundred
// milliseconds. The caller says whether the conversation is live (the header carries it only then).
func (m *Module) BackgroundWork(sessionID string) (HookBackground, bool) {
	if m == nil || m.frames == nil || sessionID == "" {
		return HookBackground{}, false
	}
	frames, err := m.frames.ListRootsBySessionIDCached(sessionID)
	if err != nil {
		return HookBackground{}, false
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	// frames are oldest first; the newest live cc frame is the conversation (an old process that outlived --resume or /clear must not
	// speak for it), so its answer is final even when it listed nothing.
	for i := len(frames) - 1; i >= 0; i-- {
		f := frames[i]
		if f.AgentType != "cc" || !isPidAliveFn(f.PID) {
			continue
		}
		c, ok := m.hookBgCount[f.FrameID]
		return c, ok
	}
	return HookBackground{}, false
}

// setHookBackground records frameID's symbol from the Stop stamped ts; the
// empty symbol removes the entry. A Stop stamped at or before the frame's last
// SessionStart (hookBgClearedAt) is refused: hooks of one process are applied
// out of order now and then, and a Stop from the old conversation must not
// write its symbol back into the new one.
func (m *Module) setHookBackground(frameID string, b lights.Background, ts int64) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	if cleared, ok := m.hookBgClearedAt[frameID]; ok && ts <= cleared {
		return
	}
	if b == "" {
		delete(m.hookBackground, frameID)
		return
	}
	if m.hookBackground == nil {
		m.hookBackground = make(map[string]lights.Background)
	}
	m.hookBackground[frameID] = b
}

// clearHookBackground is a SessionStart stamped ts: drop frameID's symbol and
// remember the stamp so an older Stop cannot set it again. The stamp only
// moves forward.
func (m *Module) clearHookBackground(frameID string, ts int64) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	delete(m.hookBackground, frameID)
	delete(m.hookBgCount, frameID)
	if m.hookBgClearedAt == nil {
		m.hookBgClearedAt = make(map[string]int64)
	}
	if ts > m.hookBgClearedAt[frameID] {
		m.hookBgClearedAt[frameID] = ts
	}
}

// forgetHookBackground drops everything kept for frameID — the symbol and the
// SessionStart stamp — because the frame is gone (a no-op without any).
func (m *Module) forgetHookBackground(frameID string) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	delete(m.hookBackground, frameID)
	delete(m.hookBgCount, frameID)
	delete(m.hookBgClearedAt, frameID)
	m.modMu.Unlock()
}

// noteHookBackground keeps the symbol in step with a cc hook that was just
// applied to the sender's frame: a root frame's Stop sets it from the payload,
// a SessionStart clears it. It reads the sender's own frame, never the one
// applyFrameEvent reported, because a collapsed proxy SessionStart reports
// its parent's id. Call it after applyFrameEvent and before the projection
// the event emits is built, so that projection carries the new symbol. Holds
// no lock; takes modMu only inside the setters.
func (m *Module) noteHookBackground(req EventRequest, lifecycle agentpkg.LifecycleEventKind, ts int64) {
	if req.AgentType != "cc" || m.frames == nil {
		return
	}
	if lifecycle != agentpkg.LifecycleStop && lifecycle != agentpkg.LifecycleSessionStart {
		return
	}
	frame, err := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
	if err != nil || frame == nil || frame.ParentFrameID != "" {
		return
	}
	if lifecycle == agentpkg.LifecycleSessionStart {
		m.clearHookBackground(frame.FrameID, ts)
		return
	}
	m.setHookBackground(frame.FrameID, hookBackgroundOf(req.RawEvent), ts)
	m.setHookBackgroundCount(frame.FrameID, hookBackgroundCountOf(req.RawEvent), ts)
}
