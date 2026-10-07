// Package daemonclient is the one HTTP client every new pdx command uses to
// talk to the local daemon (spec §9.1). It hides a daemon restart: refused,
// reset and half-open connections and the daemon's own 503 shutting_down /
// not_ready answers are retried with backoff for a hard 30 s grace, with one
// stderr line, and the boot id from /api/health tells the caller when the
// daemon actually came back as a new process.
//
// Two things it does not hide. A daemon that accepts the connection and
// never answers is not restarting: each attempt is bounded by a per-attempt
// timeout and Do returns ErrNoAnswer once (the CLI counts those, spec §9.1).
// And a write the daemon may already have received is not replayed on the
// client's own initiative: a POST/PUT/DELETE whose connection dropped after
// the request went out returns ErrSentNoResponse unless the caller marked it
// Idempotent().
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
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/team"
)

// Grace is how long Do keeps retrying after the first failure (spec §9.1:
// three times the usual 5–10 s restart). It is a hard bound: every retry,
// probe and backoff sleep ends no later than first failure + Grace.
const Grace = time.Duration(team.BootGraceS) * time.Second

// DefaultAttemptTimeout bounds one attempt (request or health probe) when
// the caller's context has no deadline. It is longer than the daemon's
// longest long-poll (35 s), so a healthy daemon never trips it.
const DefaultAttemptTimeout = 60 * time.Second

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
	// ErrUnsupported means the route does not exist on this daemon: the 404
	// body is not a team.APIError (Go's plain text, a proxy's HTML, nothing)
	// (exit 21).
	ErrUnsupported = errors.New("unsupported")
	// ErrNoAnswer means the daemon accepted the connection but did not answer
	// within the attempt timeout. It is not a restart signal and is not
	// retried; the CLI decides after three in a row (spec §9.1).
	ErrNoAnswer = errors.New("no_answer")
	// ErrSentNoResponse means the connection dropped after a non-idempotent
	// write went out, so the daemon may have applied it. The transport error
	// stays in the chain. Mark the call Idempotent() to have it retried.
	ErrSentNoResponse = errors.New("sent_no_response")
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

// WithAfterFunc injects the timer that ends an attempt (default
// time.AfterFunc). after arms f to run once d has passed and returns a stop
// that reports whether it prevented f from running.
func WithAfterFunc(after func(d time.Duration, f func()) (stop func() bool)) Option {
	return func(c *Client) { c.after = after }
}

// WithAttemptTimeout replaces DefaultAttemptTimeout; 0 disables the
// per-attempt bound (the grace bound still applies once restarting).
func WithAttemptTimeout(d time.Duration) Option { return func(c *Client) { c.attemptTimeout = d } }

// WithGrace replaces Grace for this Client: how long Do keeps retrying
// after the first restart signal. `pdx hook` uses 5 s (spec §6.6: a
// session's tool call must not stall 30 s on a daemon restart); every
// other command keeps the default. d <= 0 is ignored.
func WithGrace(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.grace = d
		}
	}
}

// WithStderr sets where the restart lines go; the default discards them.
func WithStderr(w io.Writer) Option { return func(c *Client) { c.stderr = w } }

// RequestOption configures one Do call.
type RequestOption func(*requestOptions)

type requestOptions struct {
	idempotent bool
}

// Idempotent marks a POST/PUT/DELETE as safe to replay: the daemon
// deduplicates it (a client-generated id, spec §7.2 / §9.1 "writes carry
// their client id"), so Do may resend it after the connection dropped with
// no response. GET and HEAD are always treated as idempotent.
func Idempotent() RequestOption { return func(o *requestOptions) { o.idempotent = true } }

// Client talks to one daemon. It is safe for concurrent use.
type Client struct {
	base           string
	token          string
	http           *http.Client
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	after          func(time.Duration, func()) func() bool
	attemptTimeout time.Duration
	grace          time.Duration

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
		base:           strings.TrimRight(baseURL, "/"),
		token:          token,
		http:           &http.Client{},
		now:            time.Now,
		sleep:          sleepCtx,
		after:          afterFunc,
		attemptTimeout: DefaultAttemptTimeout,
		grace:          Grace,
		stderr:         io.Discard,
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

func afterFunc(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }

// outage is one Do call's view of a failure run: when it began and how many
// failures it has seen. The grace is measured from first.
type outage struct {
	first    time.Time
	failures int
}

// remaining is how much of grace is left; it is only meaningful once
// failures > 0.
func (o *outage) remaining(now time.Time, grace time.Duration) time.Duration {
	return grace - now.Sub(o.first)
}

// Do sends method path with body (JSON, nil for none) and decodes a 2xx
// body into out when out != nil. It returns the HTTP status with a nil
// error on 2xx, a *StatusError on any other settled answer, ErrUnsupported
// on a 404 whose body is not a team.APIError, ErrUnavailable when the daemon
// stayed unreachable through Grace, ErrNoAnswer when one attempt ran out its
// timeout with the connection open, ErrSentNoResponse when a non-idempotent
// write lost its connection after being sent, or ctx.Err() when ctx ended
// first.
//
// Before this Client's first request, and before every retry, Do reads
// /api/health for the daemon's boot_id; the first answer is the baseline
// and a different later answer is reported once on stderr.
//
// Only restart signals are retried (see classify): refused, reset, EOF and
// the daemon's 503 shutting_down / not_ready. Refused and 503 mean nothing
// was applied, so any method retries; reset and EOF after the request went
// out retry only GET, HEAD and calls marked Idempotent().
func (c *Client) Do(ctx context.Context, method, path string, body, out any, opts ...RequestOption) (int, error) {
	var ro requestOptions
	for _, o := range opts {
		o(&ro)
	}
	replayable := ro.idempotent || method == http.MethodGet || method == http.MethodHead
	var o outage
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if o.failures > 0 || !c.bootSeen() {
			_, err, bounded := c.attempt(ctx, &o, func(actx context.Context) (int, error) {
				return 0, c.probeBoot(actx)
			})
			if bounded {
				return 0, err
			}
			if err != nil {
				if classify(err) == settled {
					return 0, err
				}
				if werr := c.wait(ctx, &o); werr != nil {
					return 0, werr
				}
				continue
			}
		}
		status, err, bounded := c.attempt(ctx, &o, func(actx context.Context) (int, error) {
			return c.Once(actx, method, path, body, out)
		})
		if bounded {
			return 0, err
		}
		switch classify(err) {
		case settled:
			return status, err
		case afterSend:
			if !replayable {
				return 0, fmt.Errorf("%w: %w", ErrSentNoResponse, err)
			}
		}
		if werr := c.wait(ctx, &o); werr != nil {
			return 0, werr
		}
	}
}

// attempt runs fn under its own attemptCtx. bounded is true when the
// attempt ended on that context's timer: err is then ErrNoAnswer or
// ErrUnavailable and Do returns it without retrying.
func (c *Client) attempt(ctx context.Context, o *outage, fn func(context.Context) (int, error)) (status int, err error, bounded bool) {
	actx, cancel, ok := c.attemptCtx(ctx, o)
	if !ok {
		return 0, ErrUnavailable, true
	}
	defer cancel()
	status, err = fn(actx)
	err, bounded = c.boundedOut(ctx, actx, err)
	return status, err, bounded
}

// attemptCtx derives the context one attempt runs under. Without a caller
// deadline it is bounded by the attempt timeout (cause ErrNoAnswer); once
// restarting it is bounded by the grace deadline as well, whichever comes
// first (cause ErrUnavailable). ok is false when the grace is already over.
func (c *Client) attemptCtx(ctx context.Context, o *outage) (actx context.Context, cancel func(), ok bool) {
	var d time.Duration
	cause := ErrNoAnswer
	bounded := false
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.attemptTimeout > 0 {
		d, bounded = c.attemptTimeout, true
	}
	if o.failures > 0 {
		rem := o.remaining(c.now(), c.grace)
		if rem <= 0 {
			return nil, nil, false
		}
		if !bounded || rem <= d {
			d, cause, bounded = rem, ErrUnavailable, true
		}
	}
	if !bounded {
		return ctx, func() {}, true
	}
	cctx, ccancel := context.WithCancelCause(ctx)
	stop := c.after(d, func() { ccancel(cause) })
	return cctx, func() { stop(); ccancel(nil) }, true
}

// boundedOut tells whether err came from attemptCtx's own timer (the caller's
// ctx is still live and actx ended): then the attempt's cause — ErrNoAnswer
// or ErrUnavailable — is the error to return, with no retry. It must run
// before the attempt's cancel.
func (c *Client) boundedOut(ctx, actx context.Context, err error) (error, bool) {
	if err == nil || ctx.Err() != nil || actx.Err() == nil {
		return err, false
	}
	cause := context.Cause(actx)
	if cause == nil || errors.Is(cause, context.Canceled) {
		return err, false
	}
	return cause, true
}

// Once sends exactly one request: no retry, no health probe, no stderr, no
// attempt timeout (the caller's ctx is the only bound). The DELETE a
// cancelled `pdx lead request` sends uses it (spec §6.1 step 5: best
// effort, 3 s, no retry). Status and error follow Do's contract minus
// ErrUnavailable / ErrNoAnswer / ErrSentNoResponse; a transport error is
// returned as is.
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
	se := &StatusError{Status: resp.StatusCode, Body: raw}
	_ = json.Unmarshal(raw, &se.API) // best effort: a non-JSON or other-shaped body leaves API empty
	if resp.StatusCode == http.StatusNotFound && se.API.Error == "" {
		// Not a team handler's answer: Go's ServeMux plain text, a proxy's
		// HTML, an empty body. The route does not exist on this daemon.
		return resp.StatusCode, ErrUnsupported
	}
	return resp.StatusCode, se
}

// failure is what one failed attempt tells Do about retrying it.
type failure int

const (
	// settled: nil, or an answer / error that is not a restart signal.
	// Return it as is.
	settled failure = iota
	// beforeSend: the daemon applied nothing (connection refused, or its
	// own 503 shutting_down / not_ready). Safe to retry for any method.
	beforeSend
	// afterSend: the connection dropped after the request went out (reset,
	// EOF); the daemon may have received it. Retry only when replayable.
	afterSend
)

func (f failure) String() string {
	switch f {
	case beforeSend:
		return "beforeSend"
	case afterSend:
		return "afterSend"
	}
	return "settled"
}

// classify sorts an attempt's error by the restart signals of spec §9.1 —
// an allowlist, not "any network error": ECONNREFUSED; ECONNRESET, io.EOF,
// io.ErrUnexpectedEOF; a 503 whose code is shutting_down or not_ready.
// Everything else is settled: DNS failures, EHOSTUNREACH / ENETUNREACH,
// TLS and certificate errors, context errors, any other net.OpError, a
// PairingGuard 503 with no "error" field, every other status.
func classify(err error) failure {
	if err == nil {
		return settled
	}
	var se *StatusError
	if errors.As(err, &se) {
		if se.Status == http.StatusServiceUnavailable &&
			(se.API.Error == errShuttingDown || se.API.Error == team.ErrNotReady) {
			return beforeSend
		}
		return settled
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return settled
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return beforeSend
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return afterSend
	}
	return settled
}

// wait records one failure, prints MsgRestarting on the Client's first,
// gives up with ErrUnavailable once the grace has run out, and otherwise
// sleeps the next backoff step, cut to what is left of the grace. It
// returns ctx.Err() when ctx ends first.
func (c *Client) wait(ctx context.Context, o *outage) error {
	now := c.now()
	if o.failures == 0 {
		o.first = now
		c.noteRestarting()
	}
	o.failures++
	rem := o.remaining(now, c.grace)
	if rem <= 0 {
		return ErrUnavailable
	}
	step := o.failures - 1
	if step >= len(backoffs) {
		step = len(backoffs) - 1
	}
	d := backoffs[step]
	if rem < d {
		d = rem
	}
	return c.sleep(ctx, d)
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
// classify to sort. A non-200 or unreadable answer is not an error: the
// daemon is up, its boot is just unknown.
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
