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
	"sync"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
)

// statuslineMutex serializes concurrent /statusline/setup requests.
// CC settings.json is a shared resource; atomic rename doesn't protect
// read-modify-write ordering across simultaneous install/remove calls.
var statuslineMutex sync.Mutex

// testNoncePrefix identifies statusline self-test POSTs to /api/agent/status.
// Real tmux session names cannot start with this prefix; the SPA self-test
// panel generates nonces like "__pdx_test_<random>" so we can route them down
// a dedicated path that signals the test observer and broadcasts keyed by the
// nonce, without touching the production snapshot map / session lookup.
const testNoncePrefix = "__pdx_test_"

// resolveStatuslineInstaller returns the StatuslineInstaller for the agent
// named by the request path variable "agent", or writes a 404 JSON error and
// returns (nil, false). Used by both /statusline/status and /statusline/setup.
func (m *Module) resolveStatuslineInstaller(w http.ResponseWriter, r *http.Request) (agentpkg.StatuslineInstaller, bool) {
	agentType := r.PathValue("agent")
	if agentType != "cc" {
		http.Error(w, `{"error":"unsupported agent"}`, http.StatusNotFound)
		return nil, false
	}
	provider, ok := m.registry.Get(agentType)
	if !ok {
		http.Error(w, `{"error":"unknown agent"}`, http.StatusNotFound)
		return nil, false
	}
	installer, ok := provider.(agentpkg.StatuslineInstaller)
	if !ok {
		http.Error(w, `{"error":"agent does not support statusline"}`, http.StatusNotFound)
		return nil, false
	}
	return installer, true
}

// EventRequest is the JSON body expected by POST /api/agent/event.
type EventRequest struct {
	TmuxSession     string          `json:"tmux_session"`
	TmuxPaneID      string          `json:"tmux_pane_id"`
	EventName       string          `json:"event_name"`
	RawEvent        json.RawMessage `json:"raw_event"`
	AgentType       string          `json:"agent_type"`
	SenderPID       int             `json:"sender_pid"`
	SenderStartTime string          `json:"sender_start_time"`
	SenderUncertain bool            `json:"sender_uncertain"`
}

// handleEvent handles POST /api/agent/event.
// It stores the hook event and broadcasts normalized events to WS subscribers.
func (m *Module) handleEvent(w http.ResponseWriter, r *http.Request) {
	var req EventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.TmuxSession == "" || req.TmuxPaneID == "" || req.AgentType == "" || req.EventName == "" || req.SenderPID == 0 {
		http.Error(w, `{"error":"schema_invalid"}`, http.StatusBadRequest)
		return
	}

	if req.SenderStartTime == "" && !req.SenderUncertain {
		http.Error(w, `{"error":"schema_invalid"}`, http.StatusBadRequest)
		return
	}

	trace := beginHookTrace(m.traceSink, req)
	traceFinished := false
	defer func() {
		if !traceFinished {
			trace.Finish("aborted", "handler_return")
		}
	}()

	if decision := verifyEventFn(m, req); !decision.Accepted {
		trace.Verify(req, "rejected", decision.Reason, map[string]any{"decision": "rejected", "reason": decision.Reason})
		trace.Finish("completed", "verify_rejected")
		traceFinished = true
		writeVerifyRejected(w, req, decision.Reason)
		return
	}
	trace.Verify(req, "accepted", "verify_passed", map[string]any{"decision": "accepted"})

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

	// Invalid result: provider returned Valid=false. Two sub-classes:
	//   - Reason=="" → truly unknown event name → "event_not_in_catalog"
	//   - Reason!="" → known event but payload not mappable → use that reason
	//     (e.g. "compact_ignored", "notification_unknown_type")
	//
	// Both branches:
	//   - record a verify-kind trace step (decision=skipped) with the chosen reason
	//   - clear any legacy agent_events row so replay/snapshot don't surface
	//     stale state on top of an unprocessed event (matches the cleanup the
	//     valid path performs at line ~163)
	//   - return 200 OK with the reason in the body
	//   - skip frame / projection / broadcast / activity-watch
	//
	// 200 (vs verify_rejected's 202) signals "received and acknowledged, no retry".
	// Hook CLI only retries on non-2xx.
	if !result.Valid {
		reason := result.Reason
		if reason == "" {
			reason = "event_not_in_catalog"
		}
		trace.Verify(req, "skipped", reason, nil)
		if req.TmuxSession != "" && m.events != nil {
			if err := m.events.Delete(req.TmuxSession); err != nil {
				log.Printf("[agent] clear legacy event on invalid result: %v", err)
			}
		}
		trace.Finish("completed", reason)
		traceFinished = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"reason": reason,
		})
		return
	}

	// Error guard: when in error state, only whitelisted events can clear it
	if result.Valid && result.Status != "" && result.Status != agentpkg.StatusError {
		m.mu.Lock()
		current := m.currentStatus[req.TmuxSession]
		m.mu.Unlock()
		if current == agentpkg.StatusError {
			canClear := req.EventName == "UserPromptSubmit" || req.EventName == "SessionStart"
			// SessionEnd carries StatusClear and unconditionally tears down
			// session state — it must always pass the error guard or the
			// session would stay stuck red after a StopFailure followed by a
			// real session shutdown.
			canClear = canClear || req.EventName == "SessionEnd"
			if req.AgentType != "opencode" {
				canClear = canClear || req.EventName == "Stop"
			}
			if !canClear {
				normalized := buildProjectionNormalized(nil, req.AgentType, req.EventName, broadcastTs, result)
				trace.Emit(normalized, normalized.AgentType, normalized.RawEventName, "skipped", "error_guard_blocked")
				trace.Finish("completed", "emit_skipped")
				traceFinished = true
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
				return
			}
		}
	}

	paneProjection, frameMeta, err := m.applyFrameEvent(req, result, broadcastTs)
	if err != nil {
		log.Printf("[agent] frame event: %v", err)
		trace.Finish("aborted", "frame_apply_failed")
		traceFinished = true
		http.Error(w, `{"error":"frame update failed"}`, http.StatusInternalServerError)
		return
	}
	trace.Frame(req, frameMeta)
	projection := paneProjection
	if req.TmuxSession != "" {
		projection, err = m.projectionForSession(req.TmuxSession)
		if err != nil {
			log.Printf("[agent] session projection: %v", err)
			trace.Finish("aborted", "projection_failed")
			traceFinished = true
			http.Error(w, `{"error":"frame update failed"}`, http.StatusInternalServerError)
			return
		}
	}
	trace.Projection(req, summarizeProjectionChange(paneProjection, projection))

	if req.TmuxSession != "" && m.frames != nil && m.events != nil {
		if err := m.events.Delete(req.TmuxSession); err != nil {
			log.Printf("[agent] clear legacy event: %v", err)
			trace.Finish("aborted", "legacy_delete_failed")
			traceFinished = true
			http.Error(w, `{"error":"store failed"}`, http.StatusInternalServerError)
			return
		}
	}

	// Handle subagent events (transient — broadcast only, don't persist)
	if req.EventName == "SubagentStart" || req.EventName == "SubagentStop" {
		if frameMeta.Decision != "updated_frame" {
			trace.Finish("completed", "emit_skipped")
			traceFinished = true
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
		m.mu.Lock()
		syncProjectionState(m.currentStatus, m.subagents, req.TmuxSession, projection)
		m.mu.Unlock()
		normalized := buildProjectionNormalized(projection, req.AgentType, req.EventName, broadcastTs, result)
		emitDecision, emitReason := m.emitHookToSession(req.TmuxSession, normalized)
		trace.Emit(normalized, normalized.AgentType, normalized.RawEventName, emitDecision, emitReason)
		if emitDecision == "broadcasted" {
			trace.Finish("completed", "emit_broadcasted")
		} else {
			trace.Finish("completed", "emit_skipped")
		}
		traceFinished = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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

	// Activity watch management:
	// 1. Any hook event stops an active watcher for this session.
	// 2. waiting/running/idle transitions restart the watcher for the top frame.
	watchAgentType := req.AgentType
	watchStatus := result.Status
	if projection != nil && projection.TopFrame != nil {
		watchAgentType = projection.TopFrame.AgentType
		watchStatus = projection.TopFrame.Status
	}
	if req.TmuxSession != "" && m.prober != nil && result.Valid {
		m.manageActivityWatch(req.TmuxSession, watchAgentType, watchStatus)
	}

	// Clear subagents on non-compact SessionStart
	if req.EventName == "SessionStart" && result.Valid {
		m.mu.Lock()
		delete(m.subagents, req.TmuxSession)
		m.mu.Unlock()
	}

	// Build and broadcast normalized event
	normalized := buildProjectionNormalized(projection, req.AgentType, req.EventName, broadcastTs, result)
	m.mu.Lock()
	syncProjectionState(m.currentStatus, m.subagents, req.TmuxSession, projection)
	m.mu.Unlock()
	emitDecision, emitReason := m.emitHookToSession(req.TmuxSession, normalized)
	trace.Emit(normalized, normalized.AgentType, normalized.RawEventName, emitDecision, emitReason)
	if emitDecision == "broadcasted" {
		trace.Finish("completed", "emit_broadcasted")
	} else {
		trace.Finish("completed", "emit_skipped")
	}
	traceFinished = true

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// buildNormalized creates a NormalizedEvent from the derive result and current state.
func (m *Module) buildNormalized(tmuxSession, eventName, agentType string, broadcastTs int64, result agentpkg.DeriveResult) agentpkg.NormalizedEvent {
	m.mu.Lock()
	subs := make([]agentpkg.SubagentRef, len(m.subagents[tmuxSession]))
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
	_, _ = m.emitHookToSession(tmuxSession, normalized)
}

func (m *Module) emitHookToSession(tmuxSession string, normalized agentpkg.NormalizedEvent) (string, string) {
	if m.core == nil {
		return "skipped", "core_unavailable"
	}
	code := m.resolveSessionCode(tmuxSession)
	if code == "" {
		return "skipped", "session_code_missing"
	}
	payload, _ := json.Marshal(normalized)
	m.core.Events.Broadcast(code, "hook", string(payload))
	return "broadcasted", "session_code_resolved"
}

// resolveSessionCode maps a tmux session name to the pdx session code.
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

	pdxPath, err := os.Executable()
	if err != nil {
		http.Error(w, `{"error":"cannot find pdx binary"}`, http.StatusInternalServerError)
		return
	}
	pdxPath, _ = filepath.EvalSymlinks(pdxPath)

	switch req.Action {
	case "install":
		if err := installer.InstallHooks(pdxPath); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "setup failed", "detail": err.Error()})
			return
		}
	case "remove":
		if err := installer.RemoveHooks(pdxPath); err != nil {
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

// handleStatuslineStatus handles GET /api/agent/{agent}/statusline/status.
// Currently only "cc" is supported; other agent types return 404.
func (m *Module) handleStatuslineStatus(w http.ResponseWriter, r *http.Request) {
	installer, ok := m.resolveStatuslineInstaller(w, r)
	if !ok {
		return
	}
	state, err := installer.CheckStatusline()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// handleStatuslineSetup handles POST /api/agent/{agent}/statusline/setup.
// Action "install" with mode "pdx" installs the pdx-native statusLine;
// mode "wrap" installs pdx as a wrapper around the given inner command.
// Action "remove" removes a pdx-managed statusLine (unmanaged entries are
// refused with 409 Conflict).
func (m *Module) handleStatuslineSetup(w http.ResponseWriter, r *http.Request) {
	installer, ok := m.resolveStatuslineInstaller(w, r)
	if !ok {
		return
	}

	var req struct {
		Action string `json:"action"`
		Mode   string `json:"mode"`
		Inner  string `json:"inner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	pdxPath, err := os.Executable()
	if err != nil {
		http.Error(w, `{"error":"cannot find pdx binary"}`, http.StatusInternalServerError)
		return
	}
	pdxPath, _ = filepath.EvalSymlinks(pdxPath)

	// Acquire mutex only for the mutation phase. The status reply at the end
	// is a read-only CheckStatusline() call plus HTTP write; keeping it
	// outside the lock means install/remove don't block subsequent status
	// polls, and avoids holding the mutex across HTTP response writes.
	statuslineMutex.Lock()
	var (
		opErr       error
		badRequest  string
		conflictErr error
	)
	switch req.Action {
	case "install":
		switch req.Mode {
		case "pdx":
			opErr = installer.InstallStatuslinePdx(pdxPath)
		case "wrap":
			if req.Inner == "" {
				badRequest = `{"error":"wrap requires inner"}`
			} else {
				opErr = installer.InstallStatuslineWrap(pdxPath, req.Inner)
			}
		default:
			badRequest = `{"error":"mode must be pdx or wrap"}`
		}
	case "remove":
		opErr = installer.RemoveStatusline()
		if opErr != nil && strings.Contains(opErr.Error(), "refusing to remove unmanaged") {
			conflictErr = opErr
			opErr = nil
		} else if opErr == nil {
			// On successful remove: wipe cached snapshots and broadcast a
			// cleared event so the SPA can drop stale statusline state.
			// Global clear is intentional for single-host daemon (simplest-
			// possible approach); the empty session code is the existing
			// codebase convention for cross-session events (see watcher.go
			// sessions/tmux broadcasts).
			m.snapshotMu.Lock()
			m.statusSnapshots = make(map[string]statusSnapshot)
			m.snapshotMu.Unlock()
			if m.core != nil {
				m.core.Events.Broadcast("", "agent.status.cleared", `{"agent_type":"cc"}`)
			}
		}
	default:
		badRequest = `{"error":"action must be install or remove"}`
	}
	statuslineMutex.Unlock()

	switch {
	case badRequest != "":
		http.Error(w, badRequest, http.StatusBadRequest)
		return
	case conflictErr != nil:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": conflictErr.Error()})
		return
	case opErr != nil:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": opErr.Error()})
		return
	}

	// Return updated status (mutex released; CheckStatusline is a pure read).
	m.handleStatuslineStatus(w, r)
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

	agentType := ""
	if projection, err := m.projectionForSession(sess.Name); err == nil && projection != nil && projection.TopFrame != nil {
		agentType = projection.TopFrame.AgentType
	}
	if agentType == "" && m.events != nil {
		ev, _ := m.events.Get(sess.Name)
		if ev != nil {
			agentType = ev.AgentType
		}
	}
	if agentType == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	provider, ok := m.registry.Get(agentType)
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

// statusSnapshot is the in-memory shape cached per sessionCode and broadcast over WS.
// It is intentionally display-only and not persisted (high-frequency, agent-owned).
// Lives as a Module field (m.statusSnapshots) guarded by m.snapshotMu.
type statusSnapshot struct {
	AgentType string          `json:"agent_type"`
	Status    json.RawMessage `json:"status"`
}

// handleAgentStatus handles POST /api/agent/status.
// Receives statusline payloads from `pdx statusline-proxy` and broadcasts
// agent.status WS events to subscribers.
func (m *Module) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		TmuxSession string          `json:"tmux_session"`
		AgentType   string          `json:"agent_type"`
		RawStatus   json.RawMessage `json:"raw_status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if payload.AgentType != "cc" {
		http.Error(w, `{"error":"unsupported agent_type"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))

	// Test-nonce path: only treat the request as self-test traffic when there is
	// an active observer for that nonce. This prevents legitimate sessions whose
	// names happen to start with the prefix from silently losing production
	// status updates.
	if strings.HasPrefix(payload.TmuxSession, testNoncePrefix) && m.hasTestObserver(payload.TmuxSession) {
		m.signalTestStage(payload.TmuxSession, testStageReceived)
		if m.core != nil {
			snap := statusSnapshot{AgentType: payload.AgentType, Status: payload.RawStatus}
			body, _ := json.Marshal(snap)
			m.core.Events.Broadcast(payload.TmuxSession, "agent.status", string(body))
		}
		m.signalTestStage(payload.TmuxSession, testStageBroadcast)
		return
	}

	code := m.resolveSessionCode(payload.TmuxSession)
	if code == "" {
		return
	}

	snap := statusSnapshot{AgentType: payload.AgentType, Status: payload.RawStatus}
	m.snapshotMu.Lock()
	m.statusSnapshots[code] = snap
	m.snapshotMu.Unlock()

	if m.core != nil {
		body, _ := json.Marshal(snap)
		m.core.Events.Broadcast(code, "agent.status", string(body))
	}
}

// sendStatuslineSnapshot pushes the cached statusline snapshots to a new
// WebSocket subscriber. Marshals under RLock, then releases the lock
// before calling sub.Send — a slow subscriber (full channel) would
// otherwise block every concurrent agent.status writer through snapshotMu.
func (m *Module) sendStatuslineSnapshot(sub *core.EventSubscriber) {
	if m.core == nil {
		return
	}
	m.snapshotMu.RLock()
	pending := make([][]byte, 0, len(m.statusSnapshots))
	for code, snap := range m.statusSnapshots {
		body, err := json.Marshal(snap)
		if err != nil {
			continue
		}
		event := core.HostEvent{Type: "agent.status", Session: code, Value: string(body)}
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		pending = append(pending, data)
	}
	m.snapshotMu.RUnlock()
	for _, data := range pending {
		sub.Send(data)
	}
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
		"cc":       detect("claude", "--version"),
		"codex":    detect("codex", "--version"),
		"opencode": detect("opencode", "--version"),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
