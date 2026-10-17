package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/wake/purdex/internal/wwd/wire"
)

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
	v, err := wire.Decode[T](raw)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrBadResponse, err)
	}
	return v, nil
}

// send does the request and returns the body of a 200/201 answer, at most limit bytes.
func (c *Client) send(ctx context.Context, method, path string, q url.Values, body any, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	u := "http://wwd" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		msg := string(raw)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, &StatusError{Code: resp.StatusCode, Body: msg}
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: the answer is over %d bytes", ErrBadResponse, limit)
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
	if err := checkID(id); err != nil {
		return wire.Job{}, err
	}
	return call[wire.Job](ctx, c, http.MethodGet, "/v1/jobs/"+url.PathEscape(id), nil, nil)
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

// Worker is GET /v1/worker: this host's worker switch and slots.
func (c *Client) Worker(ctx context.Context) (wire.WorkerSettings, error) {
	return call[wire.WorkerSettings](ctx, c, http.MethodGet, "/v1/worker", nil, nil)
}

// SetWorker is PUT /v1/worker and returns the settings now in force.
func (c *Client) SetWorker(ctx context.Context, s wire.WorkerSettings) (wire.WorkerSettings, error) {
	return call[wire.WorkerSettings](ctx, c, http.MethodPut, "/v1/worker", nil, s)
}

// StartException is POST /v1/exceptions (spec §9).
func (c *Client) StartException(ctx context.Context, e wire.Exception) error {
	if err := e.Validate(); err != nil {
		return err
	}
	_, err := c.send(ctx, http.MethodPost, "/v1/exceptions", nil, e, maxBody)
	return err
}

// StopException is DELETE /v1/exceptions/{name}.
func (c *Client) StopException(ctx context.Context, name string) error {
	_, err := c.send(ctx, http.MethodDelete, "/v1/exceptions/"+url.PathEscape(name), nil, nil, maxBody)
	return err
}
