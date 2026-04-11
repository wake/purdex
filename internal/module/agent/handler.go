package agent

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentpkg "github.com/wake/tmux-box/internal/agent"
	"github.com/wake/tmux-box/internal/module/session"
)

// EventRequest is the JSON body expected by POST /api/agent/event.
type EventRequest struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
	AgentType   string          `json:"agent_type"`
}

// handleEvent handles POST /api/agent/event.
// It stores the hook event and broadcasts normalized events to WS subscribers.
func (m *Module) handleEvent(w http.ResponseWriter, r *http.Request) {
	var req EventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.TmuxSession == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	broadcastTs := time.Now().UnixNano()

	// Find provider
	var provider agentpkg.AgentProvider
	if m.registry != nil {
		provider, _ = m.registry.Get(req.AgentType)
	}

	// Derive status via provider
	var result agentpkg.DeriveResult
	if provider != nil {
		result = provider.DeriveStatus(req.EventName, req.RawEvent)
	}

	// Handle subagent events (transient — broadcast only, don't persist)
	if req.EventName == "SubagentStart" || req.EventName == "SubagentStop" {
		m.handleSubagentEvent(req.TmuxSession, req.EventName, result)
		normalized := m.buildNormalized(req.TmuxSession, req.EventName, req.AgentType, broadcastTs, result)
		m.broadcastToSession(req.TmuxSession, normalized)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// Error guard: when in error state, only whitelisted events can clear it
	if result.Valid && result.Status != "" && result.Status != agentpkg.StatusError {
		m.mu.Lock()
		current := m.currentStatus[req.TmuxSession]
		m.mu.Unlock()
		if current == agentpkg.StatusError {
			canClear := req.EventName == "UserPromptSubmit" ||
				req.EventName == "SessionStart" ||
				req.EventName == "Stop"
			if !canClear {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
				return
			}
		}
	}

	// Store raw event to DB
	if err := m.events.Set(req.TmuxSession, req.EventName, req.RawEvent, req.AgentType, broadcastTs); err != nil {
		log.Printf("[agent] store event: %v", err)
		http.Error(w, `{"error":"store failed"}`, http.StatusInternalServerError)
		return
	}

	// Update in-memory state
	if result.Valid && result.Status != "" {
		m.mu.Lock()
		if result.Status == agentpkg.StatusClear {
			delete(m.currentStatus, req.TmuxSession)
			delete(m.subagents, req.TmuxSession)
		} else {
			m.currentStatus[req.TmuxSession] = result.Status
		}
		m.mu.Unlock()
	}

	// Clear subagents on non-compact SessionStart
	if req.EventName == "SessionStart" && result.Valid {
		m.mu.Lock()
		delete(m.subagents, req.TmuxSession)
		m.mu.Unlock()
	}

	// Build and broadcast normalized event
	normalized := m.buildNormalized(req.TmuxSession, req.EventName, req.AgentType, broadcastTs, result)
	m.broadcastToSession(req.TmuxSession, normalized)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleSubagentEvent tracks subagent add/remove in memory.
func (m *Module) handleSubagentEvent(tmuxSession, eventName string, result agentpkg.DeriveResult) {
	agentID, _ := result.Detail["agent_id"].(string)
	if agentID == "" {
		return
	}
	if eventName == "SubagentStart" {
		// Guard: ignore SubagentStart for sessions whose latest persisted
		// event indicates they're not active.  Two cases this rejects:
		//   - No DB entry at all → session is unknown to the daemon
		//     (also covers daemon restart with no replay state).
		//   - Latest event is StatusClear (SessionEnd) → late hook arriving
		//     after the session ended; should not re-populate state.
		// Uses persistent DB state instead of in-memory currentStatus, so
		// the guard survives daemon restarts and edge cases like compact
		// SessionStart events that don't update currentStatus.
		ev, _ := m.events.Get(tmuxSession)
		if ev == nil {
			return
		}
		if provider, ok := m.registry.Get(ev.AgentType); ok {
			if r := provider.DeriveStatus(ev.EventName, ev.RawEvent); r.Status == agentpkg.StatusClear {
				return
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if eventName == "SubagentStart" {
		current := m.subagents[tmuxSession]
		for _, id := range current {
			if id == agentID {
				return
			}
		}
		m.subagents[tmuxSession] = append(current, agentID)
	} else {
		current := m.subagents[tmuxSession]
		filtered := make([]string, 0, len(current))
		for _, id := range current {
			if id != agentID {
				filtered = append(filtered, id)
			}
		}
		if len(filtered) == 0 {
			delete(m.subagents, tmuxSession)
		} else {
			m.subagents[tmuxSession] = filtered
		}
	}
}

// buildNormalized creates a NormalizedEvent from the derive result and current state.
func (m *Module) buildNormalized(tmuxSession, eventName, agentType string, broadcastTs int64, result agentpkg.DeriveResult) agentpkg.NormalizedEvent {
	m.mu.Lock()
	subs := make([]string, len(m.subagents[tmuxSession]))
	copy(subs, m.subagents[tmuxSession])
	m.mu.Unlock()

	normalized := agentpkg.NormalizedEvent{
		AgentType:    agentType,
		Status:       string(result.Status),
		Model:        result.Model,
		Subagents:    subs,
		RawEventName: eventName,
		BroadcastTs:  broadcastTs,
		Detail:       result.Detail,
	}
	return normalized
}

// broadcastToSession resolves the tmux session name to a session code and broadcasts.
func (m *Module) broadcastToSession(tmuxSession string, normalized agentpkg.NormalizedEvent) {
	if m.core == nil {
		return
	}
	code := m.resolveSessionCode(tmuxSession)
	if code == "" {
		return
	}
	payload, _ := json.Marshal(normalized)
	m.core.Events.Broadcast(code, "hook", string(payload))
}

// resolveSessionCode maps a tmux session name to the tbox session code.
func (m *Module) resolveSessionCode(tmuxName string) string {
	if m.sessions == nil {
		return ""
	}
	sessions, err := m.sessions.ListSessions()
	if err != nil {
		log.Printf("[agent] list sessions: %v", err)
		return ""
	}
	for _, s := range sessions {
		if s.Name == tmuxName {
			return s.Code
		}
	}
	return ""
}

// handleHookStatus handles GET /api/hooks/{agent}/status.
func (m *Module) handleHookStatus(w http.ResponseWriter, r *http.Request) {
	agentType := r.PathValue("agent")
	provider, ok := m.registry.Get(agentType)
	if !ok {
		http.Error(w, `{"error":"unknown agent type"}`, http.StatusNotFound)
		return
	}
	installer, ok := provider.(agentpkg.HookInstaller)
	if !ok {
		http.Error(w, `{"error":"agent does not support hooks"}`, http.StatusNotFound)
		return
	}
	status, err := installer.CheckHooks()
	if err != nil {
		http.Error(w, `{"error":"check failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleHookSetup handles POST /api/hooks/{agent}/setup.
func (m *Module) handleHookSetup(w http.ResponseWriter, r *http.Request) {
	agentType := r.PathValue("agent")
	provider, ok := m.registry.Get(agentType)
	if !ok {
		http.Error(w, `{"error":"unknown agent type"}`, http.StatusNotFound)
		return
	}
	installer, ok := provider.(agentpkg.HookInstaller)
	if !ok {
		http.Error(w, `{"error":"agent does not support hooks"}`, http.StatusNotFound)
		return
	}

	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	tboxPath, err := os.Executable()
	if err != nil {
		http.Error(w, `{"error":"cannot find tbox binary"}`, http.StatusInternalServerError)
		return
	}
	tboxPath, _ = filepath.EvalSymlinks(tboxPath)

	switch req.Action {
	case "install":
		if err := installer.InstallHooks(tboxPath); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "setup failed", "detail": err.Error()})
			return
		}
	case "remove":
		if err := installer.RemoveHooks(tboxPath); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "remove failed", "detail": err.Error()})
			return
		}
	default:
		http.Error(w, `{"error":"action must be install or remove"}`, http.StatusBadRequest)
		return
	}

	// Return updated status
	m.handleHookStatus(w, r)
}

// handleHistory handles GET /api/sessions/{code}/history.
func (m *Module) handleHistory(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if m.sessions == nil {
		http.Error(w, `{"error":"no session provider"}`, http.StatusInternalServerError)
		return
	}
	sessions, err := m.sessions.ListSessions()
	if err != nil {
		http.Error(w, `{"error":"list sessions"}`, http.StatusInternalServerError)
		return
	}
	var sess *session.SessionInfo
	for _, s := range sessions {
		if s.Code == code {
			sess = &s
			break
		}
	}
	if sess == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	ev, _ := m.events.Get(sess.Name)
	if ev == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	provider, ok := m.registry.Get(ev.AgentType)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	histProvider, ok := provider.(agentpkg.HistoryProvider)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	history, err := histProvider.GetHistory(sess.Cwd, sess.CCSessionID)
	if err != nil {
		log.Printf("[agent] history: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// handleCheckAlive handles POST /api/agent/check-alive/{session}.
func (m *Module) handleCheckAlive(w http.ResponseWriter, r *http.Request) {
	if m.sessions == nil {
		http.Error(w, `{"error":"no session provider"}`, http.StatusInternalServerError)
		return
	}
	sessionCode := r.PathValue("session")

	sessions, err := m.sessions.ListSessions()
	if err != nil {
		http.Error(w, `{"error":"list sessions"}`, http.StatusInternalServerError)
		return
	}
	var tmuxName string
	for _, s := range sessions {
		if s.Code == sessionCode {
			tmuxName = s.Name
			break
		}
	}
	if tmuxName == "" {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	ev, _ := m.events.Get(tmuxName)
	if ev == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"alive": false, "reason": "no event"})
		return
	}

	provider, ok := m.registry.Get(ev.AgentType)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"alive": false, "reason": "unknown agent"})
		return
	}

	alive := provider.IsAlive(tmuxName + ":")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"alive": alive})
}

// handleDetect handles GET /api/agents/detect.
// Checks if agent CLIs (claude, codex) are available on the host.
func (m *Module) handleDetect(w http.ResponseWriter, r *http.Request) {
	type agentInfo struct {
		Installed bool   `json:"installed"`
		Path      string `json:"path,omitempty"`
		Version   string `json:"version,omitempty"`
	}

	detect := func(cmd string, versionArgs ...string) agentInfo {
		path, err := exec.LookPath(cmd)
		if err != nil {
			return agentInfo{}
		}
		info := agentInfo{Installed: true, Path: path}
		if len(versionArgs) > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, path, versionArgs...).Output()
			if err == nil {
				info.Version = strings.TrimSpace(string(out))
			}
		}
		return info
	}

	result := map[string]agentInfo{
		"cc":    detect("claude", "--version"),
		"codex": detect("codex", "--version"),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
