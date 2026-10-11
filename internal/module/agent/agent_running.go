package agent

import "context"

// AgentRunningKey names the service the session module uses to tell whether a tmux session has an agent running (the
// no-agent launch panel's `agent_running`, spec 2026-10-12-no-agent-launch-panel §2.2). The interface lives here, not
// in the session package: agent already depends on session, never the other way round.
const AgentRunningKey = "agent.running"

// AgentRunning answers for one session code.
type AgentRunning interface {
	// AgentRunning reports whether an agent of ANY type (cc, codex, opencode) runs in the session: a live root frame
	// owns it, or the hook state says one is running. An unknown session or an empty code is false, not an error; an
	// error means the answer could not be established (the caller must not read it as "no agent").
	AgentRunning(ctx context.Context, code string) (bool, error)
}

// AgentRunning implements the AgentRunning service. The root frame is the primary answer (the same owner resolution
// the provenance route uses, so a frame whose process is gone does not count); the hook state — the session's stored
// latest event, deleted when its agent ends — covers a session the frame model has not caught up with.
func (m *Module) AgentRunning(ctx context.Context, code string) (bool, error) {
	if code == "" {
		return false, nil
	}
	if _, found, err := m.resolveSessionOwnerErr(ctx, code); err != nil {
		return false, err
	} else if found {
		return true, nil
	}
	if m.sessions == nil || m.events == nil {
		return false, nil
	}
	info, err := m.sessions.GetSession(code)
	if err != nil || info == nil {
		return false, err
	}
	ev, err := m.events.Get(info.Name)
	if err != nil {
		return false, err
	}
	return ev != nil, nil
}
