// Package daemonclient is the one HTTP client every new pdx command uses to
// talk to the local daemon (spec §9.1). It hides a daemon restart: refused,
// reset and half-open connections and the daemon's own 503 shutting_down /
// not_ready answers are retried with backoff for a 30 s grace, with one
// stderr line, and the boot id from /api/health tells the caller when the
// daemon actually came back as a new process.
//
// cmd/pdx must not import a daemon module package; the wire types come
// from the leaf package internal/team.
package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/team"
)

// Grace is how long Do keeps retrying after the first failure (spec §9.1:
// three times the usual 5–10 s restart).
const Grace = time.Duration(team.BootGraceS) * time.Second

// MsgRestarting is printed once per Client, on the first retryable failure.
const MsgRestarting = "daemon 重啟中，繼續等待…"

// msgRestartedFmt is printed once per Client when /api/health answers with a
// boot_id different from the first one seen.
const msgRestartedFmt = "daemon 已重新啟動（boot %s）\n"

// errShuttingDown is the daemon's 503 body while it is restarting
// (internal/core/restart.go). It has no wire constant of its own.
const errShuttingDown = "shutting_down"

// maxBodyBytes bounds every response body read; approvals are small.
const maxBodyBytes = 1 << 20

const healthPath = "/api/health"

// backoffs is the retry schedule; the last entry repeats.
var backoffs = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second}

var (
	// ErrUnavailable means the daemon did not answer within Grace (exit 20).
	ErrUnavailable = errors.New("daemon_unavailable")
	// ErrUnsupported means the route does not exist on this daemon: Go's
	// default mux answered a plain-text 404 (exit 21).
	ErrUnsupported = errors.New("unsupported")
)

// StatusError is a non-2xx answer that is not a retry signal. API is filled
// when the body decoded as a team.APIError with a non-empty error code;
// otherwise Body holds what the daemon sent (plain text, or a JSON object
// of another shape such as PairingGuard's {"reason":"pairing_mode"}).
type StatusError struct {
	Status int
	API    team.APIError
	Body   []byte
}

func (e *StatusError) Error() string {
	if e.API.Error != "" {
		if e.API.Detail != "" {
			return fmt.Sprintf("HTTP %d %s: %s", e.Status, e.API.Error, e.API.Detail)
		}
		return fmt.Sprintf("HTTP %d %s", e.Status, e.API.Error)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the underlying http.Client (tests; a custom transport).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithClock injects the clock Do measures the grace with and the sleep it
// backs off with. sleep must return ctx.Err() when ctx ends first.
func WithClock(now func() time.Time, sleep func(context.Context, time.Duration) error) Option {
	return func(c *Client) { c.now, c.sleep = now, sleep }
}

// WithStderr sets where the restart lines go; the default discards them.
func WithStderr(w io.Writer) Option { return func(c *Client) { c.stderr = w } }

// Client talks to one daemon. It is safe for concurrent use.
type Client struct {
	base  string
	token string
	http  *http.Client
	now   func() time.Time
	sleep func(context.Context, time.Duration) error

	mu              sync.Mutex
	stderr          io.Writer
	bootID          string
	bootKnown       bool
	restartingNoted bool
	restartedNoted  bool
}

// New returns a Client for baseURL (scheme://host:port, no trailing slash
// needed) presenting token as the bearer token.
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		base:   strings.TrimRight(baseURL, "/"),
		token:  token,
		http:   &http.Client{},
		now:    time.Now,
		sleep:  sleepCtx,
		stderr: io.Discard,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// outage is one Do call's view of a failure run: when it began and how many
// failures it has seen. The grace is measured from first.
type outage struct {
	first    time.Time
	failures int
}

// Do sends method path with body (JSON, nil for none) and decodes a 2xx
// body into out when out != nil. It returns the HTTP status with a nil
// error on 2xx, a *StatusError on any other settled answer, ErrUnsupported
// on a plain-text 404, ErrUnavailable when the daemon stayed unreachable
// through Grace, or ctx.Err() when ctx ended first.
//
// Before this Client's first request, and before every retry, Do reads
// /api/health for the daemon's boot_id; the first answer is the baseline
// and a different later answer is reported once on stderr.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) (int, error) {
	var o outage
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if o.failures > 0 || !c.bootSeen() {
			if err := c.probeBoot(ctx); err != nil {
				if !retryable(err) {
					return 0, err
				}
				if werr := c.wait(ctx, &o); werr != nil {
					return 0, werr
				}
				continue
			}
		}
		status, err := c.Once(ctx, method, path, body, out)
		if !retryable(err) {
			return status, err
		}
		if werr := c.wait(ctx, &o); werr != nil {
			return 0, werr
		}
	}
}

// Once sends exactly one request: no retry, no health probe, no stderr.
// The DELETE a cancelled `pdx lead request` sends uses it (spec §6.1 step
// 5: best effort, 3 s, no retry). Status and error follow Do's contract
// minus ErrUnavailable; a transport error is returned as is.
func (c *Client) Once(ctx context.Context, method, path string, body, out any) (int, error) {
	var rd io.Reader
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode request: %w", err)
		}
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return resp.StatusCode, err
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out != nil && len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return resp.StatusCode, fmt.Errorf("decode response: %w", err)
			}
		}
		return resp.StatusCode, nil
	}
	if resp.StatusCode == http.StatusNotFound && isPlainNotFound(resp.Header.Get("Content-Type"), raw) {
		return resp.StatusCode, ErrUnsupported
	}
	se := &StatusError{Status: resp.StatusCode, Body: raw}
	_ = json.Unmarshal(raw, &se.API) // best effort: a non-JSON or other-shaped body leaves API empty
	return resp.StatusCode, se
}

// isPlainNotFound recognises http.NotFound's answer — what Go's ServeMux
// writes for a route this daemon never registered — as opposed to a JSON
// 404 a team handler wrote on purpose.
func isPlainNotFound(contentType string, body []byte) bool {
	return strings.HasPrefix(contentType, "text/plain") &&
		strings.TrimSpace(string(body)) == "404 page not found"
}

// retryable reports whether err is a restart signal (spec §9.1): a refused,
// reset or half-closed connection, any other net.OpError that is not a
// context error, or the daemon's own 503 shutting_down / not_ready. A
// PairingGuard 503 has no "error" field and is not retried.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status == http.StatusServiceUnavailable &&
			(se.API.Error == errShuttingDown || se.API.Error == team.ErrNotReady)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op)
}

// wait records one failure, prints MsgRestarting on the Client's first,
// gives up with ErrUnavailable once the grace has run out, and otherwise
// sleeps the next backoff step. It returns ctx.Err() when ctx ends first.
func (c *Client) wait(ctx context.Context, o *outage) error {
	now := c.now()
	if o.failures == 0 {
		o.first = now
		c.noteRestarting()
	}
	o.failures++
	if now.Sub(o.first) >= Grace {
		return ErrUnavailable
	}
	step := o.failures - 1
	if step >= len(backoffs) {
		step = len(backoffs) - 1
	}
	return c.sleep(ctx, backoffs[step])
}

func (c *Client) noteRestarting() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.restartingNoted {
		return
	}
	c.restartingNoted = true
	fmt.Fprintln(c.stderr, MsgRestarting)
}

func (c *Client) bootSeen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bootKnown
}

// probeBoot reads /api/health (outside TokenAuth and PairingGuard, so it
// answers whenever the process is up). A transport error is returned for
// retryable() to classify. A non-200 or unreadable answer is not an error:
// the daemon is up, its boot is just unknown.
func (c *Client) probeBoot(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+healthPath, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var h struct {
		BootID string `json:"boot_id"`
	}
	if json.Unmarshal(raw, &h) != nil || h.BootID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case !c.bootKnown:
		c.bootID, c.bootKnown = h.BootID, true
	case h.BootID != c.bootID:
		c.bootID = h.BootID
		if !c.restartedNoted {
			c.restartedNoted = true
			fmt.Fprintf(c.stderr, msgRestartedFmt, h.BootID)
		}
	}
	return nil
}
