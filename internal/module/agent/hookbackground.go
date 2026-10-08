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

// setHookBackground records frameID's symbol; the empty symbol removes the
// entry.
func (m *Module) setHookBackground(frameID string, b lights.Background) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	if b == "" {
		delete(m.hookBackground, frameID)
		return
	}
	if m.hookBackground == nil {
		m.hookBackground = make(map[string]lights.Background)
	}
	m.hookBackground[frameID] = b
}

// forgetHookBackground drops frameID's symbol (a no-op without one).
func (m *Module) forgetHookBackground(frameID string) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	delete(m.hookBackground, frameID)
	m.modMu.Unlock()
}

// noteHookBackground keeps the symbol in step with a cc hook that was just
// applied to the sender's frame: a root frame's Stop sets it from the payload,
// a SessionStart clears it. It reads the sender's own frame, never the one
// applyFrameEvent reported, because a collapsed proxy SessionStart reports
// its parent's id. Call it after applyFrameEvent and before the projection
// the event emits is built, so that projection carries the new symbol. Holds
// no lock; takes modMu only inside the setters.
func (m *Module) noteHookBackground(req EventRequest, lifecycle agentpkg.LifecycleEventKind) {
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
		m.forgetHookBackground(frame.FrameID)
		return
	}
	m.setHookBackground(frame.FrameID, hookBackgroundOf(req.RawEvent))
}
