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
	WorkbookNextPath   = "/mod/v1/workbook/next"
	WorkbookResultPath = "/mod/v1/workbook/result"
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
}

// WithWorkbook enables the workbook routes and the events answer's `workbook` hint; get returns nil while the workbook
// module is off.
func WithWorkbook(get func() WorkbookService) HandlerOption {
	return func(h *handler) { h.workbook = get }
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
	wait := time.Duration(*in.WaitMS) * time.Millisecond
	// The server's WriteTimeout is 10 s; a 15 s poll must be able to answer, so this request carries its own deadline.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(wait + waitWriteSlack))
	ctx := r.Context()
	release, ok := h.polls.acquire(ctx, in.Stream)
	if !ok {
		return // the client went away while queued behind its own earlier poll
	}
	defer release()
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
