package modevents

import (
	"context"
	"net/http"
	"time"
)

// CompactContextPath is the route the mod posts to when a compaction starts; the answer is the text the compaction hands
// the agent (the session's recap).
const CompactContextPath = "/mod/v1/dialogue/compact-context"

// compactContextDeadline: 20 s waiting for the recap + 8 s reading the transcript + slack. The server's WriteTimeout is 10 s.
const compactContextDeadline = 35 * time.Second

// CompactContextService is what a WorkbookService may also implement; one that does not makes the route a 503.
type CompactContextService interface {
	CompactContext(ctx context.Context, sessionID string) (string, bool)
}

// compactContext serves POST /mod/v1/dialogue/compact-context {stream, session_id}: 200 {"text"}, 204 (nothing to hand
// over, not the session's own stream, or the wait ended), 400, 429, 503. It does not take h.polls: the same stream's
// `next` long poll must run meanwhile, or the job it would deliver never starts.
func (h *handler) compactContext(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Stream    string `json:"stream"`
		SessionID string `json:"session_id"`
	}
	if !h.readJSON(w, r, &in) {
		return
	}
	if !ValidStream(in.Stream) || !sidRe.MatchString(in.SessionID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	var svc CompactContextService
	if s := h.service(); s != nil {
		svc, _ = s.(CompactContextService)
	}
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	if !h.streamMayPoll(in.Stream, in.SessionID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if h.activePolls.Add(1) > int32(h.maxPolls) {
		h.activePolls.Add(-1)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "busy"})
		return
	}
	defer h.activePolls.Add(-1)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(compactContextDeadline))
	ctx, cancel := h.pollContext(r)
	defer cancel()
	text, ok := svc.CompactContext(ctx, in.SessionID)
	if !ok || text == "" || ctx.Err() != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}
