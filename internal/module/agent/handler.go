package agent

import (
	"encoding/json"
	"log"
	"net/http"
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
		if err := m.events.Set(req.TmuxSession, req.EventName, req.RawEvent, req.AgentType); err != nil {
			log.Printf("[agent] store event: %v", err)
			http.Error(w, `{"error":"store failed"}`, http.StatusInternalServerError)
			return
		}

		// Broadcast to WS subscribers if we can resolve session code.
		if m.core != nil {
			code := m.resolveSessionCode(req.TmuxSession)
			if code != "" {
				ev := m.buildAgentEvent(req.TmuxSession, req.EventName, req.RawEvent, req.AgentType)
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

// buildAgentEvent builds a JSON-serializable map matching AgentEvent fields.
func (m *Module) buildAgentEvent(tmuxSession, eventName string, rawEvent json.RawMessage, agentType string) map[string]any {
	return map[string]any{
		"tmux_session": tmuxSession,
		"event_name":   eventName,
		"raw_event":    rawEvent,
		"agent_type":   agentType,
		"broadcast_ts": time.Now().UnixNano(),
	}
}
