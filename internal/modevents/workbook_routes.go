package modevents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// The workbook job routes (session workbook spec §5.1). The mod asks `next` for the summariser job of its session's
// conversation and posts the call's outcome to `result`; the daemon keeps every decision.
const (
	WorkbookNextPath    = "/mod/v1/workbook/next"
	WorkbookResultPath  = "/mod/v1/workbook/result"
	WorkbookRefreshPath = "/mod/v1/workbook/refresh"
)

// Limits of the routes.
const (
	MaxWaitMS      = 15_000
	waitWriteSlack = 5 * time.Second // the write deadline is wait + this: the server's own 10 s would cut a long poll
	maxJobID       = 64
	maxResultText  = 64 << 10
)

// WorkbookUsage is the tokens of a call.
type WorkbookUsage struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	CacheRead int64 `json:"cache_read"`
}

// WorkbookResult is the body of POST result.
type WorkbookResult struct {
	Stream    string        `json:"stream"`
	JobID     string        `json:"job_id"`
	Answered  bool          `json:"answered"`
	Text      string        `json:"text"`
	Reason    string        `json:"reason"`
	Status    int           `json:"status"`
	Error     string        `json:"error"`
	Usage     WorkbookUsage `json:"usage"`
	LatencyMS int64         `json:"latency_ms"`
}

// ErrNotLeased is a WorkbookService's answer to a result whose job the stream does not hold.
var ErrNotLeased = errors.New("modevents: the job is not leased to this stream")

// WorkbookService is the workbook module as the routes see it. It is looked up per request (the module may start after
// the socket), so a nil service is a 503.
type WorkbookService interface {
	// NextJob waits up to wait for the next job of the session's conversation; job is JSON-encodable.
	NextJob(ctx context.Context, stream, sessionID string, wait time.Duration) (job any, ok bool)
	// JobResult reports a call; more says another job is ready. ErrNotLeased is a 409.
	JobResult(stream string, r WorkbookResult) (more bool, err error)
	// JobWaiting says a job of the session's conversation is queued and nobody holds it (the events answer's hint).
	JobWaiting(sessionID string) bool
	// RequestRefresh queues a refresh of the session's conversation, preferring the caller's session to run it; entryID is
	// the refresh entry. ErrNotLive and ErrRefreshPending are 409s.
	RequestRefresh(stream, sessionID string) (entryID int64, err error)
}

// Errors of RequestRefresh.
var (
	ErrNotLive        = errors.New("modevents: no live session of the conversation can run a refresh")
	ErrRefreshPending = errors.New("modevents: a refresh of the conversation is already pending")
)

// workbookRefresh serves POST /mod/v1/workbook/refresh {stream, session_id} (the mod's /workbook refresh): 202
// {"entry_id"}, 409 not_live | refresh_pending, 400 bad_request, 503 unavailable. Only the stream that is the session's own,
// live and refresh-capable may ask.
func (h *handler) workbookRefresh(w http.ResponseWriter, r *http.Request) {
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
	svc := h.service()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	if h.reg == nil || !h.reg.StreamCapable(in.Stream, in.SessionID, CapWorkbookRefresh, CapsFresh) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "not_live"})
		return
	}
	id, err := svc.RequestRefresh(in.Stream, in.SessionID)
	switch {
	case errors.Is(err, ErrNotLive):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "not_live"})
	case errors.Is(err, ErrRefreshPending):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "refresh_pending"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	default:
		writeJSON(w, http.StatusAccepted, map[string]int64{"entry_id": id})
	}
}

// WithWorkbook enables the workbook routes and the events answer's `workbook` hint; get returns nil while the workbook
// module is off.
func WithWorkbook(get func() WorkbookService) HandlerOption {
	return func(h *handler) { h.workbook = get }
}

// Capabilities of the workbook, and how many long polls the socket serves at once.
const (
	CapWorkbookV2      = "workbook.v2"
	CapWorkbookRefresh = "workbook.refresh"
	defaultMaxPolls    = 64
)

// streamMayPoll: the stream is live, its current session is sid, and it announced a workbook capability within CapsFresh.
func (h *handler) streamMayPoll(stream, sid string) bool {
	return h.reg != nil && (h.reg.StreamCapable(stream, sid, CapWorkbookV2, CapsFresh) ||
		h.reg.StreamCapable(stream, sid, CapWorkbookRefresh, CapsFresh))
}

func (h *handler) service() WorkbookService {
	if h.workbook == nil {
		return nil
	}
	return h.workbook()
}

// pollGate lets one long poll of a stream run at a time: a second waits for the first to return (plan D10).
type pollGate struct {
	mu sync.Mutex
	m  map[string]*gate
}

type gate struct {
	ch   chan struct{}
	refs int
}

func (p *pollGate) acquire(ctx context.Context, stream string) (release func(), ok bool) {
	p.mu.Lock()
	if p.m == nil {
		p.m = map[string]*gate{}
	}
	g := p.m[stream]
	if g == nil {
		g = &gate{ch: make(chan struct{}, 1)}
		p.m[stream] = g
	}
	g.refs++
	p.mu.Unlock()
	drop := func() {
		p.mu.Lock()
		if g.refs--; g.refs == 0 {
			delete(p.m, stream)
		}
		p.mu.Unlock()
	}
	select {
	case g.ch <- struct{}{}:
		return func() { <-g.ch; drop() }, true
	case <-ctx.Done():
		drop()
		return nil, false
	}
}

// workbookNext serves POST /mod/v1/workbook/next {stream, session_id, wait_ms}: 200 {"job":…} or 204, 400 bad_request,
// 503 unavailable.
func (h *handler) workbookNext(w http.ResponseWriter, r *http.Request) {
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
	svc := h.service()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	// Only the stream that is the session's own, live and capable, may take its work: a made-up or someone else's stream
	// gets "no job" at once and never holds a poll.
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
	wait := time.Duration(*in.WaitMS) * time.Millisecond
	// The server's WriteTimeout is 10 s; a 15 s poll must be able to answer, so this request carries its own deadline.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(wait + waitWriteSlack))
	ctx := r.Context()
	release, ok := h.polls.acquire(ctx, in.Stream)
	if !ok {
		return // the client went away while queued behind its own earlier poll
	}
	defer release()
	// The stream may have switched session or ended while this poll was queued behind its earlier one (codex critic).
	if !h.streamMayPoll(in.Stream, in.SessionID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Queued behind its own earlier poll, this one's clock starts now: the deadline above covered the queue time only.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(wait + waitWriteSlack))
	job, ok := svc.NextJob(ctx, in.Stream, in.SessionID, wait)
	if !ok || ctx.Err() != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

// workbookResult serves POST /mod/v1/workbook/result: 200 {"more":bool}, 409 not_leased, 400 bad_request, 503.
func (h *handler) workbookResult(w http.ResponseWriter, r *http.Request) {
	var in WorkbookResult
	if !h.readJSON(w, r, &in) {
		return
	}
	if !ValidStream(in.Stream) || in.JobID == "" || len(in.JobID) > maxJobID || len(in.Text) > maxResultText ||
		len(in.Reason) > 64 || len(in.Error) > 128 || in.LatencyMS < 0 ||
		in.Usage.Input < 0 || in.Usage.Output < 0 || in.Usage.CacheRead < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	svc := h.service()
	if svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	more, err := svc.JobResult(in.Stream, in)
	switch {
	case errors.Is(err, ErrNotLeased):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "not_leased"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"more": more})
	}
}

// readJSON decodes a POST body into v; it answers 405 / 413 / 400 itself and reports false.
func (h *handler) readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBody))
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		}
		return false
	}
	return true
}
