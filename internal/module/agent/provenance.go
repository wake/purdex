package agent

import (
	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// Provenance is the self-contained rebuild-record envelope. It is deliberately
// separate from NormalizedEvent.AgentType: on a proxy-collapsed event the
// outer type names the session projection winner, which may be a different
// agent in a different tmux pane (frame_ops.go:1129, :1170-1182). Consumers of
// the rebuild record read ONLY this struct. See spec §4.3.1.
type Provenance struct {
	OwnerSessionStart bool   `json:"owner_session_start"`
	AgentType         string `json:"agent_type"`
	SessionID         string `json:"session_id,omitempty"`
	Cwd               string `json:"cwd,omitempty"`
	TmuxPaneID        string `json:"tmux_pane_id"`
	TmuxInstance      string `json:"tmux_instance"`
	// FrameID names this one agent run. The exit envelope (pdx_exit) carries
	// the same id, and the SPA applies an exit only on an exact match — so a
	// late exit of an older run that shares the session id (/resume, a
	// restart) can never mark the new run exited.
	FrameID string `json:"frame_id"`
}

// buildProvenance assembles the envelope from the request that produced the
// frame mutation plus the derive result that carries session_id / cwd. The
// caller is responsible for the ownership gate; reaching here means the
// mutation outcome already confirmed the sender kept its own top-level frame.
func buildProvenance(req EventRequest, result agentpkg.DeriveResult, tmuxInstance, frameID string) Provenance {
	return Provenance{
		OwnerSessionStart: true,
		AgentType:         req.AgentType,
		SessionID:         strFromDetail(result.Detail, "session_id"),
		Cwd:               strFromDetail(result.Detail, "cwd"),
		TmuxPaneID:        req.TmuxPaneID,
		TmuxInstance:      tmuxInstance,
		FrameID:           frameID,
	}
}

// attachProvenance copies the envelope onto the outgoing normalized event's
// Detail map when — and only when — applyFrameEvent granted one. Nil is the
// fail-safe: every return path that did not confirm ownership leaves
// FrameTraceMeta.Provenance at its zero value, so no field is written.
func attachProvenance(normalized *agentpkg.NormalizedEvent, meta FrameTraceMeta) {
	if normalized == nil || meta.Provenance == nil {
		return
	}
	if normalized.Detail == nil {
		normalized.Detail = map[string]any{}
	}
	normalized.Detail["pdx_provenance"] = *meta.Provenance
}

// sessionTmuxInstance reports the tmux generation this daemon is currently
// serving, or "" when the session provider is not wired (test setups, a
// half-initialized module). "" is rejected by the SPA's envelope parser, so a
// half-wired daemon writes no rebuild record rather than a wrong one.
func (m *Module) sessionTmuxInstance() string {
	if m == nil || m.sessions == nil {
		return ""
	}
	return m.sessions.TmuxInstance()
}

// Exit reasons carried by the exit envelope.
const (
	// ExitReasonSessionEnd: the agent reported its own end (SessionEnd hook).
	ExitReasonSessionEnd = "session-end"
	// ExitReasonProcessDead: the sweep found the frame's process gone or its
	// PID reused — a quit with no hook (opencode), a crash or a kill.
	ExitReasonProcessDead = "process-dead"
)

// Exit is the rebuild record's "the agent is gone" envelope, broadcast as
// detail.pdx_exit when a ROOT frame ends (agent-last-state spec §1). Like
// Provenance it is self-contained: the outer event describes the session
// projection, which may name another pane's agent.
//
// FrameID is the match key — the SPA applies an exit only to a record whose
// agent carries the same frame id (review decision 2). At is Unix
// milliseconds on the daemon's clock, for display only (review decision 5).
type Exit struct {
	AgentType    string `json:"agent_type"`
	SessionID    string `json:"session_id"`
	TmuxPaneID   string `json:"tmux_pane_id"`
	TmuxInstance string `json:"tmux_instance"`
	FrameID      string `json:"frame_id"`
	Reason       string `json:"reason"`
	At           int64  `json:"at"`
}

// exitForFrame builds the envelope for a frame that is about to be deleted,
// or nil when the frame is not a root: a child frame (ParentFrameID set) is
// part of another agent's run and never ended the pane's agent. It reads ONLY
// the frame value it is handed, so callers take it BEFORE the delete.
func exitForFrame(frame store.Frame, tmuxInstance, reason string, atMs int64) *Exit {
	if frame.ParentFrameID != "" {
		return nil
	}
	return &Exit{
		AgentType:    frame.AgentType,
		SessionID:    frame.SessionID,
		TmuxPaneID:   frame.PaneID,
		TmuxInstance: tmuxInstance,
		FrameID:      frame.FrameID,
		Reason:       reason,
		At:           atMs,
	}
}

// attachExit copies the envelope onto the outgoing event's Detail map; nil
// writes nothing.
func attachExit(normalized *agentpkg.NormalizedEvent, exit *Exit) {
	if normalized == nil || exit == nil {
		return
	}
	if normalized.Detail == nil {
		normalized.Detail = map[string]any{}
	}
	normalized.Detail["pdx_exit"] = *exit
}
