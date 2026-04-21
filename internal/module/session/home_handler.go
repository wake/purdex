package session

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
)

// handleSessionHome returns the pane shell HOME for the given session code.
// The SPA uses this to expand tilde-prefixed terminal links.
func (m *SessionModule) handleSessionHome(w http.ResponseWriter, r *http.Request) {
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

	home, err := m.sessionHome(info.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"home": home})
}

func (m *SessionModule) sessionHome(sessionName string) (string, error) {
	if m.shellHomeReader != nil {
		if pid, err := m.tmux.ActivePanePID(sessionName); err == nil && pid != "" {
			if home, err := m.shellHomeReader(pid); err == nil && home != "" {
				return home, nil
			}
		}
	}

	if home := os.Getenv("HOME"); home != "" {
		return home, nil
	}

	return "", errors.New("home not available")
}
