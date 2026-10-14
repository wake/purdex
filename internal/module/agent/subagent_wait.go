package agent

import (
	"encoding/json"
	"sync"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// subagentWait remembers which agent caused a pane's current waiting light, and
// the status the light had before it. In memory only: a daemon restart loses it,
// and the light then clears on the next Stop or main-conversation event, as before.
type subagentWait struct {
	agentID string
	prior   agentpkg.Status
}

type subagentWaits struct {
	mu sync.Mutex
	by map[string]subagentWait
}

func rawAgentID(req EventRequest) string {
	var raw struct {
		AgentID string `json:"agent_id"`
	}
	_ = json.Unmarshal(req.RawEvent, &raw)
	return raw.AgentID
}

// waitKey is the pane for a tmux event, the session id for a non-tmux one.
func waitKey(req EventRequest) string {
	if req.TmuxPaneID != "" {
		return "pane:" + req.TmuxPaneID
	}
	return "sid:" + req.SessionID
}

// noteWaiting records who caused a cc waiting event: the subagent named by
// agent_id, or nobody (a main-conversation waiting drops any record). prior is
// the light before this waiting; a waiting on top of a recorded waiting keeps
// the older prior.
func (m *Module) noteWaiting(req EventRequest, prior agentpkg.Status) {
	id := rawAgentID(req)
	key := waitKey(req)
	m.subWaits.mu.Lock()
	defer m.subWaits.mu.Unlock()
	if id == "" {
		delete(m.subWaits.by, key)
		return
	}
	if m.subWaits.by == nil {
		m.subWaits.by = map[string]subagentWait{}
	}
	if old, ok := m.subWaits.by[key]; ok && prior == agentpkg.StatusWaiting {
		prior = old.prior
	}
	if prior == "" || prior == agentpkg.StatusWaiting {
		prior = agentpkg.StatusRunning
	}
	m.subWaits.by[key] = subagentWait{agentID: id, prior: prior}
}

func (m *Module) dropWait(req EventRequest) {
	m.subWaits.mu.Lock()
	delete(m.subWaits.by, waitKey(req))
	m.subWaits.mu.Unlock()
}

// takeSubagentWait reports the status a subagent's PostToolUse restores: only
// the agent that caused the recorded waiting clears it, once.
func (m *Module) takeSubagentWait(req EventRequest) (agentpkg.Status, bool) {
	id := rawAgentID(req)
	key := waitKey(req)
	m.subWaits.mu.Lock()
	defer m.subWaits.mu.Unlock()
	w, ok := m.subWaits.by[key]
	if !ok || id == "" || w.agentID != id {
		return "", false
	}
	delete(m.subWaits.by, key)
	return w.prior, true
}

// framePriorStatus is the sender's stored frame status, "" when it has none.
func (m *Module) framePriorStatus(req EventRequest) agentpkg.Status {
	if m.frames == nil || req.TmuxPaneID == "" {
		return ""
	}
	f, err := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
	if err != nil || f == nil {
		return ""
	}
	return f.Status
}
