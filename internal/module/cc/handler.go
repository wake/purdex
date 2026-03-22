package cc

import (
	"encoding/json"
	"net/http"
)

// handleHistory serves GET /api/sessions/{code}/history.
// Returns the CC conversation history for the given session as JSON.
func (m *CCModule) handleHistory(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	sess, err := m.sessions.GetSession(code)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	messages, _ := m.GetHistory(sess.Cwd, sess.CCSessionID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}
