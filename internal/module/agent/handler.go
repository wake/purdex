package agent

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// EventRequest is the JSON body expected by POST /api/agent/event.
type EventRequest struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
	AgentType   string          `json:"agent_type"`
}

// handleEvent handles POST /api/agent/event.
// It stores the hook event and broadcasts it to WS subscribers.
func (m *Module) handleEvent(w http.ResponseWriter, r *http.Request) {
	var req EventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Store event (skip if no tmux session — can't map to anything useful).
	if req.TmuxSession != "" {
		broadcastTs := time.Now().UnixNano()
		if err := m.events.Set(req.TmuxSession, req.EventName, req.RawEvent, req.AgentType, broadcastTs); err != nil {
			log.Printf("[agent] store event: %v", err)
			http.Error(w, `{"error":"store failed"}`, http.StatusInternalServerError)
			return
		}

		// Broadcast to WS subscribers if we can resolve session code.
		if m.core != nil {
			code := m.resolveSessionCode(req.TmuxSession)
			if code != "" {
				ev := m.buildAgentEvent(req.TmuxSession, req.EventName, req.RawEvent, req.AgentType, broadcastTs)
				payload, _ := json.Marshal(ev)
				m.core.Events.Broadcast(code, "hook", string(payload))
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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

// handleHookStatus handles GET /api/agent/hook-status.
// It reads ~/.claude/settings.json and checks if tbox hooks are installed for each event.
func (m *Module) handleHookStatus(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, `{"error":"cannot find home dir"}`, http.StatusInternalServerError)
		return
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"agent_type": "cc",
			"installed":  false,
			"events":     map[string]any{},
			"issues":     []string{"settings.json not found"},
		})
		return
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		http.Error(w, `{"error":"invalid settings.json"}`, http.StatusInternalServerError)
		return
	}

	hooks, _ := settings["hooks"].(map[string]any)
	events := map[string]any{}
	var issues []string
	allInstalled := true

	hookEvents := []string{"SessionStart", "UserPromptSubmit", "Stop", "StopFailure", "Notification", "PermissionRequest", "SessionEnd"}
	for _, eventName := range hookEvents {
		entries, ok := hooks[eventName]
		if !ok {
			events[eventName] = map[string]any{"installed": false, "command": nil}
			issues = append(issues, eventName+" hook not installed")
			allInstalled = false
			continue
		}
		command := findTboxCommand(entries)
		events[eventName] = map[string]any{"installed": command != "", "command": command}
		if command == "" {
			issues = append(issues, eventName+" hook: tbox command not found")
			allInstalled = false
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"agent_type": "cc",
		"installed":  allInstalled,
		"events":     events,
		"issues":     issues,
	})
}

// findTboxCommand searches hook entries for a command containing "tbox hook".
func findTboxCommand(entries any) string {
	arr, ok := entries.([]any)
	if !ok {
		return ""
	}
	for _, entry := range arr {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hooksList, ok := entryMap["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hooksList {
			hookMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hookMap["command"].(string)
			if strings.Contains(strings.ReplaceAll(cmd, `"`, ""), "tbox hook") {
				return cmd
			}
		}
	}
	return ""
}

// hookSetupRequest is the JSON body expected by POST /api/agent/hook-setup.
type hookSetupRequest struct {
	AgentType string `json:"agent_type"`
	Action    string `json:"action"`
}

// handleHookSetup handles POST /api/agent/hook-setup.
// It runs `tbox setup` or `tbox setup --remove` and returns the updated hook status.
func (m *Module) handleHookSetup(w http.ResponseWriter, r *http.Request) {
	var req hookSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	tboxPath, err := os.Executable()
	if err != nil {
		http.Error(w, `{"error":"cannot find tbox binary"}`, http.StatusInternalServerError)
		return
	}

	var args []string
	switch req.Action {
	case "install":
		args = []string{"setup"}
	case "remove":
		args = []string{"setup", "--remove"}
	default:
		http.Error(w, `{"error":"action must be install or remove"}`, http.StatusBadRequest)
		return
	}

	cmd := exec.Command(tboxPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error":  "setup failed",
			"detail": string(output),
		})
		return
	}

	// Return updated status
	m.handleHookStatus(w, r)
}

// buildAgentEvent builds a JSON-serializable map matching AgentEvent fields.
func (m *Module) buildAgentEvent(tmuxSession, eventName string, rawEvent json.RawMessage, agentType string, broadcastTs int64) map[string]any {
	return map[string]any{
		"tmux_session": tmuxSession,
		"event_name":   eventName,
		"raw_event":    rawEvent,
		"agent_type":   agentType,
		"broadcast_ts": broadcastTs,
	}
}
