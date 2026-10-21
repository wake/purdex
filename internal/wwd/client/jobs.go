package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ErrTooLarge is inside the error of an answer over the limit the caller set (it is also an ErrBadResponse).
var ErrTooLarge = errors.New("over the limit")

// maxLogBody caps a job log read through Log (the runner caps what it stores at 64 MiB).
const maxLogBody = 64 << 20

// call sends a JSON request (body may be nil) and decodes a 200 or 201 answer as T with the strict decoder. A non-2xx answer is a
// *StatusError. Like get, the call is bounded by the client's timeout.
func call[T any](ctx context.Context, c *Client, method, path string, q url.Values, body any) (T, error) {
	var zero T
	raw, err := c.send(ctx, method, path, q, body, maxBody)
	if err != nil {
		return zero, err
	}
	v, err := wire.DecodeLenient[T](raw)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrBadResponse, err)
	}
	return v, nil
}

// send does a JSON request (body may be nil) and returns the body of a 2xx answer, at most limit bytes.
func (c *Client) send(ctx context.Context, method, path string, q url.Values, body any, limit int64) ([]byte, error) {
	var rd io.Reader
	ctype := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd, ctype = bytes.NewReader(b), "application/json"
	}
	return c.sendRaw(ctx, method, path, q, rd, ctype, c.timeout(), limit)
}

// sendRaw does the request with the body as given, bounded by timeout, and returns the body of a 2xx answer, at most limit bytes.
// A non-2xx answer is a *StatusError.
func (c *Client) sendRaw(ctx context.Context, method, path string, q url.Values, rd io.Reader, ctype string, timeout time.Duration, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u := "http://wwd" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading the answer: %v", ErrUnavailable, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 { // 200 and 201 carry a body; 204 is a successful delete or cancel
		msg := string(raw)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, &StatusError{Code: resp.StatusCode, Body: msg}
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: the answer is over %d bytes: %w", ErrBadResponse, limit, ErrTooLarge)
	}
	return raw, nil
}

// Submit is POST /v1/jobs (local socket only): body is the submission with the git directory (submit.Body). The answer has the
// job's id, and its stored result at once when the result was reused.
func (c *Client) Submit(ctx context.Context, body any) (wire.JobSubmissionResponse, error) {
	return call[wire.JobSubmissionResponse](ctx, c, http.MethodPost, "/v1/jobs", nil, body)
}

// JobsQuery filters Jobs; an empty field does not filter.
type JobsQuery struct {
	State   wire.JobState
	Session string // the submitter's session
}

// Jobs is GET /v1/jobs.
func (c *Client) Jobs(ctx context.Context, f JobsQuery) ([]wire.Job, error) {
	q := url.Values{}
	if f.State != "" {
		q.Set("state", string(f.State))
	}
	if f.Session != "" {
		q.Set("submitter.session", f.Session)
	}
	r, err := call[wire.JobsListResponse](ctx, c, http.MethodGet, "/v1/jobs", q, nil)
	return r.Jobs, err
}

// JobsAfter is GET /v1/jobs?after=<cursor>: the jobs that changed after the cursor (oldest change first, each once, in the state it is
// in now) and the cursor to ask with next, with the Epoch of the database the cursor belongs to (empty from a wwd that has none). A
// cursor above wwd's head is a *CursorAheadError. The first cursor is "0". limit 0 leaves the server's default. An answer with no cursor is an
// error: a server that does not know ?after= would list everything, and that must not be taken for "nothing new".
func (c *Client) JobsAfter(ctx context.Context, after string, limit int) (wire.JobsListResponse, error) {
	if !wire.ValidCursor(after) {
		return wire.JobsListResponse{}, fmt.Errorf("invalid cursor %q", after)
	}
	q := url.Values{"after": {after}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	r, err := call[wire.JobsListResponse](ctx, c, http.MethodGet, "/v1/jobs", q, nil)
	if err != nil {
		return wire.JobsListResponse{}, cursorAhead(err)
	}
	if r.Cursor == "" {
		return wire.JobsListResponse{}, errors.New("wwd answered without a cursor: it does not support ?after=")
	}
	return r, nil
}

// CursorAheadError is wwd's 409 to an ?after= above its head: the cursor was made by another database (this one was removed and made
// again), so reading on from it would find nothing, forever. The caller starts over.
type CursorAheadError struct{ wire.CursorAhead }

func (e *CursorAheadError) Error() string {
	return fmt.Sprintf("wwd: the cursor is ahead of its head %s (database %s)", e.Head, e.Epoch)
}

var jobIDRe = regexp.MustCompile(`^j_[0-9A-Za-z_-]{1,64}$`)

// checkID refuses anything that is not a job id before it can become part of a path.
func checkID(id string) error {
	if !jobIDRe.MatchString(id) {
		return fmt.Errorf("invalid job id %q", id)
	}
	return nil
}

// Job is GET /v1/jobs/{id}.
func (c *Client) Job(ctx context.Context, id string) (wire.Job, error) {
	return c.JobAt(ctx, "", id)
}

// hostQuery is the query that names the host a read is about (empty: the local wwd's own).
func hostQuery(host string) url.Values {
	if host == "" {
		return nil
	}
	return url.Values{"host": {host}}
}

// JobAt is Job about the named host: the local wwd answers for itself or forwards to that peer (spec §10).
func (c *Client) JobAt(ctx context.Context, host, id string) (wire.Job, error) {
	if err := checkID(id); err != nil {
		return wire.Job{}, err
	}
	return call[wire.Job](ctx, c, http.MethodGet, "/v1/jobs/"+url.PathEscape(id), hostQuery(host), nil)
}

// LogAt is Log about the named host.
func (c *Client) LogAt(ctx context.Context, host, id string) ([]byte, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	return c.send(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id)+"/log", hostQuery(host), nil, maxLogBody)
}

// LogAtMost is LogAt for a caller that wants the log only when it is small: at most max bytes are read, and a longer log is
// (nil, true, nil) — it is not held in memory.
func (c *Client) LogAtMost(ctx context.Context, host, id string, max int64) (log []byte, tooLarge bool, err error) {
	if err := checkID(id); err != nil {
		return nil, false, err
	}
	b, err := c.send(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id)+"/log", hostQuery(host), nil, max)
	if errors.Is(err, ErrTooLarge) {
		return nil, true, nil
	}
	return b, false, err
}

// ValidJobID reports whether id has the form of a job id (what the job routes take).
func ValidJobID(id string) bool { return jobIDRe.MatchString(id) }

// JobsPageQuery is a list read for the App: the filters of JobsQuery, the host the list is about, a cursor to read after, a limit.
type JobsPageQuery struct {
	JobsQuery
	Host  string // "": the local wwd's own jobs
	After string // "": the list as it is now; a cursor: what changed after it
	Limit int    // 0: wwd's default
}

// JobsPage is GET /v1/jobs with the host, the cursor and the limit: the whole answer (jobs, cursor, epoch). A cursor past wwd's head
// is a *CursorAheadError.
func (c *Client) JobsPage(ctx context.Context, f JobsPageQuery) (wire.JobsListResponse, error) {
	q := url.Values{}
	if f.State != "" {
		q.Set("state", string(f.State))
	}
	if f.Session != "" {
		q.Set("submitter.session", f.Session)
	}
	if f.Host != "" {
		q.Set("host", f.Host)
	}
	if f.After != "" {
		if !wire.ValidCursor(f.After) {
			return wire.JobsListResponse{}, fmt.Errorf("invalid cursor %q", f.After)
		}
		q.Set("after", f.After)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	r, err := call[wire.JobsListResponse](ctx, c, http.MethodGet, "/v1/jobs", q, nil)
	return r, cursorAhead(err)
}

// cursorAhead turns wwd's 409 cursor_ahead into a *CursorAheadError; any other error stays as it is.
func cursorAhead(err error) error {
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusConflict {
		if ca, derr := wire.DecodeLenient[wire.CursorAhead]([]byte(se.Body)); derr == nil {
			return &CursorAheadError{CursorAhead: ca}
		}
	}
	return err
}

// Log is GET /v1/jobs/{id}/log: the stored log text. A job whose log is not stored yet is a *StatusError (404).
func (c *Client) Log(ctx context.Context, id string) ([]byte, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	return c.send(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id)+"/log", nil, nil, maxLogBody)
}

// Cancel is POST /v1/jobs/{id}/cancel. The job ends on the worker's next heartbeat; wait for it with WaitJob.
func (c *Client) Cancel(ctx context.Context, id string) error {
	if err := checkID(id); err != nil {
		return err
	}
	_, err := c.send(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/cancel", nil, nil, maxBody)
	return err
}

// CancelAt is POST /v1/jobs/{id}/cancel about the named host (the local wwd forwards to that peer, spec §10) and returns the job as it is
// then. A job that has ended is a StatusError 409.
func (c *Client) CancelAt(ctx context.Context, host, id string) (wire.Job, error) {
	if err := checkID(id); err != nil {
		return wire.Job{}, err
	}
	return call[wire.Job](ctx, c, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/cancel", hostQuery(host), nil)
}

// SetPosition is POST /v1/jobs/{id}/position (spec 4.6): the manual place of a queued job on this host. It returns the job as it is
// then; a job that is not queued is a StatusError 409.
func (c *Client) SetPosition(ctx context.Context, id string, p wire.Position) (wire.Job, error) {
	if err := checkID(id); err != nil {
		return wire.Job{}, err
	}
	req := wire.PositionRequest{Position: p}
	if err := req.Validate(); err != nil {
		return wire.Job{}, err
	}
	return call[wire.Job](ctx, c, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/position", nil, req)
}

// Worker is GET /v1/worker: this host's worker switch and slots.
func (c *Client) Worker(ctx context.Context) (wire.WorkerSettings, error) {
	return call[wire.WorkerSettings](ctx, c, http.MethodGet, "/v1/worker", nil, nil)
}

// SetWorker is PUT /v1/worker and returns the settings now in force.
func (c *Client) SetWorker(ctx context.Context, s wire.WorkerSettings) (wire.WorkerSettings, error) {
	return call[wire.WorkerSettings](ctx, c, http.MethodPut, "/v1/worker", nil, s)
}

// WorkerOn is GET /v1/worker for the named host (the local wwd forwards, spec §10; "" is this host): the settings in force with their
// etag. The etag is empty when that wwd predates it.
func (c *Client) WorkerOn(ctx context.Context, host string) (wire.WorkerView, error) {
	if host == "" {
		return call[wire.WorkerView](ctx, c, http.MethodGet, "/v1/worker", nil, nil)
	}
	timeout := c.timeout()
	if timeout < MutateTimeout {
		timeout = MutateTimeout // the local wwd may wait for the peer
	}
	return getLimit[wire.WorkerView](ctx, c, "/v1/worker", hostQuery(host), timeout, maxBody)
}

// SetWorkerOn is PUT /v1/worker for the named host and returns the settings then in force. expect, when not empty, is the etag the
// change is based on: a setting that has changed since is a *StatusError 409 whose body is a wire.WorkerEtagMismatch and nothing is
// changed. A wwd that predates the etag answers 400 (it does not know expect_etag); the call is not repeated without it.
func (c *Client) SetWorkerOn(ctx context.Context, host string, s wire.WorkerSettings, expect string) (wire.WorkerView, error) {
	// this host too goes through Mutate (an empty host is the local wwd itself): the refusal's body must come back whole, and the
	// ordinary call path cuts the body of a non-2xx answer to 200 bytes
	raw, err := json.Marshal(wire.WorkerPut{WorkerSettings: s, ExpectEtag: expect})
	if err != nil {
		return wire.WorkerView{}, err
	}
	status, answer, err := c.Mutate(ctx, http.MethodPut, "/v1/worker", host, raw)
	if err != nil {
		return wire.WorkerView{}, err
	}
	if status < 200 || status >= 300 {
		return wire.WorkerView{}, &StatusError{Code: status, Body: strings.TrimSpace(string(answer))}
	}
	v, err := wire.DecodeLenient[wire.WorkerView](answer)
	if err != nil {
		return wire.WorkerView{}, fmt.Errorf("%w: %v", ErrBadResponse, err)
	}
	return v, nil
}

// StartException is POST /v1/exceptions (spec §9).
func (c *Client) StartException(ctx context.Context, e wire.Exception) error {
	if err := e.Validate(); err != nil {
		return err
	}
	_, err := c.send(ctx, http.MethodPost, "/v1/exceptions", nil, e, maxBody)
	return err
}

// StartExceptionOn is StartException for the named host: the local wwd forwards it (spec §10). host "" is this host.
func (c *Client) StartExceptionOn(ctx context.Context, host string, e wire.Exception) error {
	if host == "" {
		return c.StartException(ctx, e)
	}
	if err := e.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return c.mutateStatus(ctx, http.MethodPost, "/v1/exceptions", host, body)
}

// StopExceptionOn is StopException for the named host.
func (c *Client) StopExceptionOn(ctx context.Context, host, name string) error {
	if host == "" {
		return c.StopException(ctx, name)
	}
	return c.mutateStatus(ctx, http.MethodDelete, "/v1/exceptions/"+url.PathEscape(name), host, nil)
}

// mutateStatus is Mutate with a non-2xx answer turned into a *StatusError, like the calls that read.
func (c *Client) mutateStatus(ctx context.Context, method, path, host string, body []byte) error {
	status, answer, err := c.Mutate(ctx, method, path, host, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return &StatusError{Code: status, Body: strings.TrimSpace(string(answer))}
	}
	return nil
}

// StopException is DELETE /v1/exceptions/{name}.
func (c *Client) StopException(ctx context.Context, name string) error {
	_, err := c.send(ctx, http.MethodDelete, "/v1/exceptions/"+url.PathEscape(name), nil, nil, maxBody)
	return err
}
