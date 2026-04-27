package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

var getenvFn = os.Getenv

const (
	titleMarkerStart = "# >>> purdex agent-title >>>"
	titleMarkerLine  = "set -gw allow-set-title on"
	titleMarkerEnd   = "# <<< purdex agent-title <<<"
)

type titleStatusResponse struct {
	AllowSetTitle     bool   `json:"allow_set_title"`
	Installed         bool   `json:"installed"`
	RuntimeApplied    bool   `json:"runtime_applied"`
	ManagedConfigPath string `json:"managed_config_path"`
	Error             string `json:"error"`
}

type titleCapability struct {
	State string `json:"state"`
	Note  string `json:"note"`
}

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
	// recordHookAt opens the probeGraceWindow so any screen-change event
	// arriving in the next probeGraceWindow interval is suppressed — the
	// hook (this code path) is the authoritative status source. Recorded
	// once per accepted hook regardless of whether activity-watching is
	// (re)started below; the orchestrator owns watcher state.
	if req.TmuxSession != "" && m.probeOrch != nil && result.Valid {
		m.probeOrch.recordHookAt(req.TmuxSession)
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

func (m *Module) handleTitleStatus(w http.ResponseWriter, r *http.Request) {
	state := m.titleStatus()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func (m *Module) handleTitleSetup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	path, err := tmuxConfigPath()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(titleStatusResponse{Error: err.Error()})
		return
	}

	switch req.Action {
	case "install":
		if err := installTitleMarker(path); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(titleStatusResponse{ManagedConfigPath: path, Error: err.Error()})
			return
		}
		if m.tmux != nil {
			if err := m.tmux.SetWindowOptionGlobal("allow-set-title", "on"); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				state := m.titleStatus()
				state.Error = err.Error()
				_ = json.NewEncoder(w).Encode(state)
				return
			}
		}
	case "remove":
		if err := removeTitleMarker(path); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(titleStatusResponse{ManagedConfigPath: path, Error: err.Error()})
			return
		}
	default:
		http.Error(w, `{"error":"action must be install or remove"}`, http.StatusBadRequest)
		return
	}

	m.handleTitleStatus(w, r)
}

func (m *Module) titleStatus() titleStatusResponse {
	path, err := tmuxConfigPath()
	if err != nil {
		return titleStatusResponse{Error: err.Error()}
	}
	state := titleStatusResponse{ManagedConfigPath: path}
	data, err := os.ReadFile(path)
	if err == nil {
		if hasMalformedTitleMarker(data) {
			state.Error = "malformed purdex agent-title marker block"
		} else {
			state.Installed = bytes.Contains(data, []byte(titleMarkerStart)) && bytes.Contains(data, []byte(titleMarkerEnd))
			state.AllowSetTitle = state.Installed && bytes.Contains(data, []byte(titleMarkerLine))
		}
	} else if !os.IsNotExist(err) {
		state.Error = err.Error()
	}
	if m.tmux != nil {
		value, err := m.tmux.ShowWindowOption("allow-set-title")
		if err != nil {
			state.Error = err.Error()
		} else {
			state.RuntimeApplied = strings.TrimSpace(value) == "on"
		}
	}
	return state
}

func tmuxConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tmux.conf"), nil
}

func installTitleMarker(path string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if hasMalformedTitleMarker(data) {
		return errors.New("malformed purdex agent-title marker block")
	}
	clean := removeTitleMarkerBytes(data)
	block := []byte(titleMarkerStart + "\n" + titleMarkerLine + "\n" + titleMarkerEnd + "\n")
	if len(clean) > 0 && !bytes.HasSuffix(clean, []byte("\n")) {
		clean = append(clean, '\n')
	}
	clean = append(clean, block...)
	return os.WriteFile(path, clean, 0644)
}

func removeTitleMarker(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if hasMalformedTitleMarker(data) {
		return errors.New("malformed purdex agent-title marker block")
	}
	return os.WriteFile(path, removeTitleMarkerBytes(data), 0644)
}

func removeTitleMarkerBytes(data []byte) []byte {
	text := string(data)
	for {
		start := strings.Index(text, titleMarkerStart)
		if start == -1 {
			return []byte(text)
		}
		end := strings.Index(text[start:], titleMarkerEnd)
		if end == -1 {
			return []byte(text)
		}
		end += start + len(titleMarkerEnd)
		if end < len(text) && text[end] == '\r' {
			end++
		}
		if end < len(text) && text[end] == '\n' {
			end++
		}
		text = text[:start] + text[end:]
	}
}

func hasMalformedTitleMarker(data []byte) bool {
	text := string(data)
	start := strings.Index(text, titleMarkerStart)
	for start != -1 {
		end := strings.Index(text[start:], titleMarkerEnd)
		if end == -1 {
			return true
		}
		between := text[start+len(titleMarkerStart) : start+end]
		if strings.Contains(between, titleMarkerStart) {
			return true
		}
		nextOffset := start + end + len(titleMarkerEnd)
		remaining := text[nextOffset:]
		next := strings.Index(remaining, titleMarkerStart)
		if next == -1 {
			return false
		}
		start = nextOffset + next
	}
	return false
}

func titleCapabilities() map[string]titleCapability {
	return map[string]titleCapability{
		"cc":       claudeTitleCapability(),
		"codex":    codexTitleCapability(),
		"opencode": {State: "unknown", Note: "OpenCode has no documented persistent title toggle."},
	}
}

func claudeTitleCapability() titleCapability {
	if getenvFn("CLAUDE_CODE_DISABLE_TERMINAL_TITLE") == "1" {
		return titleCapability{State: "disabled", Note: "CLAUDE_CODE_DISABLE_TERMINAL_TITLE=1 disables Claude terminal titles for daemon-launched sessions."}
	}
	return titleCapability{State: "enabled", Note: "Claude terminal titles are likely enabled; session-local environment overrides may differ."}
}

func codexTitleCapability() titleCapability {
	home, err := os.UserHomeDir()
	if err != nil {
		return titleCapability{State: "unknown", Note: "Codex terminal title config could not be checked."}
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if os.IsNotExist(err) {
		return titleCapability{State: "missing", Note: "Codex terminal title uses its default behavior; no config file was found."}
	}
	if err != nil {
		return titleCapability{State: "unknown", Note: "Codex terminal title config could not be read."}
	}
	return parseCodexTitleCapability(string(data))
}

func parseCodexTitleCapability(config string) titleCapability {
	idx := strings.Index(config, "terminal_title")
	if idx == -1 {
		return titleCapability{State: "missing", Note: "Codex terminal title uses its default behavior; terminal_title is not configured."}
	}
	line := strings.TrimSpace(strings.SplitN(config[idx:], "\n", 2)[0])
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return titleCapability{State: "unknown", Note: "Codex terminal title config could not be parsed."}
	}
	value := strings.TrimSpace(parts[1])
	if value == "[]" {
		return titleCapability{State: "disabled", Note: "Codex terminal title is disabled with terminal_title = []."}
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return titleCapability{State: "configured", Note: "Codex terminal title is configured in ~/.codex/config.toml."}
	}
	return titleCapability{State: "unknown", Note: "Codex terminal title config could not be parsed."}
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
		Installed    bool            `json:"installed"`
		Path         string          `json:"path,omitempty"`
		Version      string          `json:"version,omitempty"`
		DynamicTitle titleCapability `json:"dynamic_title"`
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

	capabilities := titleCapabilities()
	result := map[string]agentInfo{
		"cc":       detect("claude", "--version"),
		"codex":    detect("codex", "--version"),
		"opencode": detect("opencode", "--version"),
	}
	for agentType, info := range result {
		info.DynamicTitle = capabilities[agentType]
		result[agentType] = info
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
