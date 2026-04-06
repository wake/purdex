package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// hookSetupRequest is the JSON body expected by POST /api/hooks/cc/setup.
type hookSetupRequest struct {
	Action string `json:"action"`
}

// handleHookStatus handles GET /api/hooks/cc/status.
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
			"installed": false,
			"events":    map[string]any{},
			"issues":    []string{"settings.json not found"},
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

	hookEvents := []string{"SessionStart", "UserPromptSubmit", "SubagentStart", "SubagentStop", "Stop", "StopFailure", "Notification", "PermissionRequest", "SessionEnd"}
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
		"installed": allInstalled,
		"events":    events,
		"issues":    issues,
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

// handleHookSetup handles POST /api/hooks/cc/setup.
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

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tboxPath, args...)
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
