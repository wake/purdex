package session

import (
	"encoding/json"
	"net/http"
)

// handleSessionCwd returns the current working directory of the tmux pane
// attached to the given session code. Used by the SPA terminal-link opener
// to resolve relative file paths at click time.
func (m *SessionModule) handleSessionCwd(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	cwd, err := m.tmux.PaneCurrentPath(info.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"cwd": cwd})
}
