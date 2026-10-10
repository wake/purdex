package modevents

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// The prompt routes (interface U3 plan D7). The Apps' send goes through the session's own mod: a request waits in the
// daemon's prompt queue, the mod fetches it with `next`, runs `$.prompt.submit` / `$.turn.abort` and posts what happened to
// `result`. The daemon keeps every decision (the owner, the at-most-once ledger); this file only carries the wire.
const (
	PromptNextPath   = "/mod/v1/prompt/next"
	PromptResultPath = "/mod/v1/prompt/result"
	CapPromptV1      = "prompt.v1" // announced by a mod that can run a prompt job
)

// PromptResult is the body of POST /mod/v1/prompt/result.
type PromptResult struct {
	Stream string `json:"stream"`
	JobID  string `json:"job_id"`
	Status string `json:"status"` // accepted | dropped | busy
	Reason string `json:"reason"`
}

// Errors of a PromptService.
var (
	ErrPromptNotLeased = errors.New("modevents: the prompt job is not leased to this stream")
	ErrPromptNotOwner  = errors.New("modevents: the stream is no longer the owner of the session's prompts")
)

// PromptService is the prompt queue as the routes see it, looked up per request (a nil service is a 503).
type PromptService interface {
	// NextPrompt waits up to wait for the session's next request, handing it to the stream only if the stream is the
	// session's owner; job is JSON-encodable.
	NextPrompt(ctx context.Context, stream, sessionID string, wait time.Duration) (job any, ok bool)
	// PromptResult reports a job. ErrPromptNotLeased and ErrPromptNotOwner are 409s.
	PromptResult(stream string, r PromptResult) error
}

// WithPrompt enables the prompt routes; get returns nil while the queue is not there.
func WithPrompt(get func() PromptService) HandlerOption {
	return func(h *handler) { h.prompt = get }
}

func (h *handler) promptService() PromptService {
	if h.prompt == nil {
		return nil
	}
	return h.prompt()
}

// promptNext serves POST /mod/v1/prompt/next {stream, session_id, wait_ms}: 200 {"job":…} or 204, 400, 503. Only the stream
// that is the session's own, live and prompt-capable may poll, and one poll per stream runs at a time.
func (h *handler) promptNext(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Stream    string `json:"stream"`
		SessionID string `json:"session_id"`
		WaitMS    *int64 `json:"wait_ms"`
	}
	if !h.readJSON(w, r, &in) {
		return
	}
	if !ValidStream(in.Stream) || !sidRe.MatchString(in.SessionID) || in.WaitMS == nil || *in.WaitMS < 0 || *in.WaitMS > MaxWaitMS {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	svc := h.promptService()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	may := func() bool {
		return h.reg != nil && h.reg.StreamCapable(in.Stream, in.SessionID, CapPromptV1, CapsFresh)
	}
	if !may() {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if h.activePolls.Add(1) > int32(h.maxPolls) {
		h.activePolls.Add(-1)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "busy"})
		return
	}
	defer h.activePolls.Add(-1)
	wait := time.Duration(*in.WaitMS) * time.Millisecond
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(wait + waitWriteSlack))
	ctx, cancel := h.pollContext(r)
	defer cancel()
	release, ok := h.promptPolls.acquire(ctx, in.Stream+"/"+in.SessionID) // per session: after a /clear the old session's poll must not hold up the new one
	if !ok {
		return
	}
	defer release()
	if !may() { // the stream may have switched session or ended while this poll was queued behind its earlier one
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(wait + waitWriteSlack))
	job, ok := svc.NextPrompt(ctx, in.Stream, in.SessionID, wait)
	if !ok || ctx.Err() != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

// promptResult serves POST /mod/v1/prompt/result: 200 {}, 409 not_leased | not_owner, 400, 503.
func (h *handler) promptResult(w http.ResponseWriter, r *http.Request) {
	var in PromptResult
	if !h.readJSON(w, r, &in) {
		return
	}
	if !ValidStream(in.Stream) || in.JobID == "" || len(in.JobID) > maxJobID || len(in.Reason) > 128 ||
		(in.Status != "accepted" && in.Status != "dropped" && in.Status != "busy") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	svc := h.promptService()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	err := svc.PromptResult(in.Stream, in)
	switch {
	case errors.Is(err, ErrPromptNotLeased):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "not_leased"})
	case errors.Is(err, ErrPromptNotOwner):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "not_owner"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
