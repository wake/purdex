// Package client is the Purdex daemon's way to its local wwd (spec §10): plain HTTP over wwd's unix socket, every answer decoded
// with the wire contract's strict Decode. The daemon reads only its own host's wwd through here; the socket is trusted (same
// user), so there is no token.
package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// DefaultTimeout bounds one call: wwd answers from memory, so a wwd slower than this is not answering.
const DefaultTimeout = 2 * time.Second

// maxBody caps an answer read (a pool of a few hosts and a day of 1 m rollups is far below it).
const maxBody = 8 << 20

var (
	// ErrUnavailable: wwd could not be reached (no socket, connection refused, no answer in time). The caller reports "wwd is
	// not running" and nothing else is affected.
	ErrUnavailable = errors.New("wwd unavailable")
	// ErrBadResponse: wwd answered, but not with something the contract allows (undecodable, unknown field, bad proto).
	ErrBadResponse = errors.New("wwd answered outside the contract")
)

// StatusError is a non-200 answer of wwd.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string { return fmt.Sprintf("wwd answered %d: %s", e.Code, e.Body) }

// DefaultSocket is where wwd listens: $PDX_WWD_SOCK, else ~/.local/share/wwd/wwd.sock (spec §2).
func DefaultSocket() string {
	if p := os.Getenv("PDX_WWD_SOCK"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "wwd", "wwd.sock")
}

// Client talks to one wwd socket.
type Client struct {
	// Timeout bounds one call (DefaultTimeout when zero).
	Timeout time.Duration
	// Logf takes the client's own log lines (an alert it left out, S3-3a); the standard log when nil.
	Logf       func(format string, args ...any)
	sock       string
	http       *http.Client
	alertSkips wire.AlertSkips
}

// New returns a client for the socket at path.
func New(path string) *Client {
	c := &Client{sock: path}
	c.http = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", c.sock)
		},
		DisableKeepAlives: true,
	}}
	return c
}

// Socket is the path the client dials.
func (c *Client) Socket() string { return c.sock }

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

// get fetches path (with query) and decodes the 200 body as T.
func get[T any](ctx context.Context, c *Client, path string, q url.Values) (T, error) {
	return getLimit[T](ctx, c, path, q, c.timeout(), maxBody)
}

// getLimit is get with its own timeout and answer cap.
func getLimit[T any](ctx context.Context, c *Client, path string, q url.Values, timeout time.Duration, limit int64) (T, error) {
	var zero T
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u := "http://wwd" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return zero, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return zero, fmt.Errorf("%w: reading the answer: %v", ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(body)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return zero, &StatusError{Code: resp.StatusCode, Body: msg}
	}
	if int64(len(body)) > limit {
		return zero, fmt.Errorf("%w: the answer is over %d bytes", ErrBadResponse, limit)
	}
	v, err := wire.DecodeLenient[T](body)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrBadResponse, err)
	}
	return v, nil
}

// MutateTimeout bounds a change: wwd may forward it to another host over the tailnet (its own forwarding limit is 10 s).
const MutateTimeout = 12 * time.Second

// maxMutateAnswer caps the answer of a change (a worker setting or an exception is tiny).
const maxMutateAnswer = 64 << 10

// Mutate sends a change to the local wwd and returns its answer as it is: the status and the body, whatever the status.
// host names the host the change is about; the local wwd applies it itself or forwards it to that peer (spec §10). Only a
// call that got no answer is an error (ErrUnavailable).
func (c *Client) Mutate(ctx context.Context, method, path, host string, body []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, MutateTimeout)
	defer cancel()
	u := "http://wwd" + path + "?" + url.Values{"host": {host}}.Encode()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxMutateAnswer+1))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: reading the answer: %v", ErrUnavailable, err)
	}
	if len(b) > maxMutateAnswer {
		return 0, nil, fmt.Errorf("%w: the answer is over %d bytes", ErrBadResponse, maxMutateAnswer)
	}
	return resp.StatusCode, b, nil
}

// Info is GET /v1/info.
func (c *Client) Info(ctx context.Context) (wire.Info, error) {
	return get[wire.Info](ctx, c, "/v1/info", nil)
}

// Pool is GET /v1/pool: this wwd's view of every peer.
func (c *Client) Pool(ctx context.Context) (wire.Pool, error) {
	return get[wire.Pool](ctx, c, "/v1/pool", nil)
}

// PoolWithLiveness is Pool with each peer's liveness (wire.PoolLivenessQuery): whether the host answers when its wwd does not. A wwd that
// predates the fields ignores the query and answers as Pool does, with no liveness.
func (c *Client) PoolWithLiveness(ctx context.Context) (wire.Pool, error) {
	return get[wire.Pool](ctx, c, "/v1/pool", url.Values{"liveness": {"1"}})
}

// FactsSeries is GET /v1/facts/series: the host that has the data cuts its history to the points a chart draws. Like the history, a
// call that names another host waits for wwd's forwarding. A wwd that does not have the route answers 404: see RouteMissing.
func (c *Client) FactsSeries(ctx context.Context, q wire.FactsSeriesQuery) (wire.FactsSeriesResponse, error) {
	timeout := c.timeout()
	if q.Host != "" && timeout < MutateTimeout {
		timeout = MutateTimeout
	}
	return getLimit[wire.FactsSeriesResponse](ctx, c, "/v1/facts/series", q.Values(), timeout, maxBody)
}

// notFoundBody is what net/http's mux answers for a path it has no handler for; methodNotAllowedBody is what it answers for a path that
// has handlers, but none for the method asked (an older wwd that has POST /v1/exceptions and not yet GET: a 405, not a 404).
const (
	notFoundBody         = "404 page not found"
	methodNotAllowedBody = "Method Not Allowed"
)

// RouteMissing says err is a wwd (or the peer behind it) that has no such route: exactly net/http's own 404 for an unknown path.
// Any other 404 (a JSON {"error":"unknown host"}, a proxy's page, another route's reason) is the answer it is and is not this, so that
// it is never mistaken for an old release and answered by asking again for ten times the data.
func RouteMissing(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusNotFound && strings.TrimSpace(se.Body) == notFoundBody
}

// RouteOrMethodMissing is RouteMissing, and also net/http's own 405 for a path an older release has only for another method (a wwd with
// POST /v1/exceptions and not yet GET). That text is also what any handler or layer in front of one can write with http.Error, so it is for
// a call that knows an older wwd answers so — GET /v1/exceptions — and for no other: a route that every wwd has (FactsSeries) keeps its 405
// as the refusal it is. Only for calls that GET: a 405 for a write is a refusal.
func RouteOrMethodMissing(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	return RouteMissing(err) || (se.Code == http.StatusMethodNotAllowed && strings.TrimSpace(se.Body) == methodNotAllowedBody)
}

// maxHistoryBody caps a history answer: a whole facts record is about 7 KB, so a day of 1 m points or two hours of 5 s ones is about
// 10 MB, which the general cap would refuse.
const maxHistoryBody = 16 << 20

// FactsHistory is GET /v1/facts/history. A history of another host (q.Host) may be forwarded by the local wwd, whose own forwarding limit
// is 10 s, so that call is bounded by MutateTimeout rather than the short default.
func (c *Client) FactsHistory(ctx context.Context, q wire.FactsHistoryQuery) (wire.FactsHistoryResponse, error) {
	timeout := c.timeout()
	if q.Host != "" && timeout < MutateTimeout {
		timeout = MutateTimeout
	}
	return getLimit[wire.FactsHistoryResponse](ctx, c, "/v1/facts/history", q.Values(), timeout, maxHistoryBody)
}
