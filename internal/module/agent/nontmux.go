package agent

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// nonTmuxCodePrefix namespaces the agent code of a session that does not run
// in tmux. A tmux session code is a 6-char base36 token (internal/module/
// session/codec.go) and never contains '-', and exec workers use "exec-"
// (spa/src/lib/nex/worker-agent-status.ts), so "cc-<session id>" collides with
// neither. Like every agent code it is only unique per host: consumers key it
// with the host id (composite key), never on its own.
const nonTmuxCodePrefix = "cc-"

// NonTmuxAgentCode is the agent code of a non-tmux CC session.
func NonTmuxAgentCode(sessionID string) string { return nonTmuxCodePrefix + sessionID }

// handleNonTmuxEvent serves a verified hook event from a session that is not
// inside tmux. There is no pane, so no frame, projection or activity watcher
// exists for it: the status the provider derives is broadcast under the
// session-id-derived code, which is what the SPA's agent store consumes.
// It always finishes the trace and writes the response.
func (m *Module) handleNonTmuxEvent(w http.ResponseWriter, req EventRequest, trace *hookTraceCollector) {
	respond := func() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
	var result agentpkg.DeriveResult
	if m.registry != nil {
		if provider, ok := m.registry.Get(req.AgentType); ok {
			result = provider.DeriveStatus(req.PurdexName, req.RawEvent)
		}
	}
	if !result.Valid {
		reason := result.Reason
		if reason == "" {
			reason = "event_not_in_catalog"
		}
		trace.Verify(req, "skipped", reason, nil)
		trace.Finish("completed", reason)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "reason": reason})
		return
	}
	if m.core == nil || m.core.Events == nil {
		trace.Finish("completed", "emit_skipped")
		respond()
		return
	}
	code := NonTmuxAgentCode(req.SessionID)
	normalized := buildProjectionNormalized(nil, req.AgentType, req.PurdexName, time.Now().UnixNano(), result)
	m.emitNormalizedToCode(code, normalized)
	if isDevMode() {
		log.Printf("[broadcast] session=%s non_tmux=true raw_event_name=%s", code, normalized.RawEventName)
	}
	trace.Emit(normalized, normalized.AgentType, normalized.RawEventName, "broadcasted", "non_tmux")
	trace.Finish("completed", "emit_broadcasted")
	respond()
}
