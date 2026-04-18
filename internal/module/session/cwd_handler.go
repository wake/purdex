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
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	cwd, err := m.tmux.PaneCurrentPath(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"cwd": cwd})
}
