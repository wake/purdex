package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The worker's side of GET /v1/jobs/{id}/snapshot (WWD spec §4.3): a Client keeps its connections to the origin alive (a new one cost
// 0.2 s per call on the ssh path) and turns the answers of serve.go into errors a caller can act on.

var (
	// ErrHaveUnknown is a 422 have_unknown: the origin does not hold the `have` commit; ask again with an older one, or for a tar.
	ErrHaveUnknown = errors.New("snapshot: the origin does not know the have commit")
	// ErrNothingToSend is a 422 nothing_to_send: `have` already holds the snapshot commit.
	ErrNothingToSend = errors.New("snapshot: the have commit already holds the snapshot")
	// ErrBundleTooLarge is a 413: the bundle would be over the origin's limit; ask for a tar.
	ErrBundleTooLarge = errors.New("snapshot: the bundle is too large")
	// ErrBusy is a 503 that outlived the retries: the origin is making as many snapshots as it will.
	ErrBusy = errors.New("snapshot: the origin is busy")
)

// Client fetches snapshots from one origin with the pool token.
type Client struct {
	base, token string
	hc          *http.Client
	retries     int
	// Delay, when set, runs before every fetch and ends it with its error (the fault hook H4 of the C2 matrix; nil in production).
	Delay func(context.Context) error
	sleep func(context.Context, time.Duration) error // a variable so that a test need not wait
}

// NewClient is a Client for the origin at base ("http://host:port"). There is no overall timeout: a tar is as long as it is; the
// caller's context ends a fetch.
func NewClient(base, token string) *Client {
	tr := &http.Transport{MaxIdleConns: 8, MaxIdleConnsPerHost: 4, IdleConnTimeout: 90 * time.Second, ForceAttemptHTTP2: false}
	return &Client{base: strings.TrimRight(base, "/"), token: token, hc: &http.Client{Transport: tr}, retries: 3, sleep: sleepCtx}
}

// Close drops the idle connections.
func (c *Client) Close() { c.hc.CloseIdleConnections() }

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Bundle is the bundle of have..commit of the job. The caller closes the body.
func (c *Client) Bundle(ctx context.Context, jobID, claimID, have string) (io.ReadCloser, error) {
	return c.get(ctx, jobID, url.Values{"claim_id": {claimID}, "format": {"bundle"}, "have": {have}})
}

// Tar is the tar of the job's snapshot commit. The caller closes the body.
func (c *Client) Tar(ctx context.Context, jobID, claimID string) (io.ReadCloser, error) {
	return c.get(ctx, jobID, url.Values{"claim_id": {claimID}, "format": {"tar"}})
}

func (c *Client) get(ctx context.Context, jobID string, q url.Values) (io.ReadCloser, error) {
	if err := validJobID(jobID); err != nil {
		return nil, err
	}
	if c.Delay != nil {
		if err := c.Delay(ctx); err != nil {
			return nil, err
		}
	}
	u := c.base + "/v1/jobs/" + url.PathEscape(jobID) + "/snapshot?" + q.Encode()
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp.Body, nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10)) // drained, so that the connection is reused
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusConflict:
			return nil, ErrStaleClaim
		case resp.StatusCode == http.StatusRequestEntityTooLarge:
			return nil, ErrBundleTooLarge
		case resp.StatusCode == http.StatusUnprocessableEntity:
			var e struct{ Error string }
			_ = json.Unmarshal(body, &e)
			switch e.Error {
			case "have_unknown":
				return nil, ErrHaveUnknown
			case "nothing_to_send":
				return nil, ErrNothingToSend
			}
		case resp.StatusCode == http.StatusServiceUnavailable:
			if attempt >= c.retries {
				return nil, ErrBusy
			}
			wait := 2 * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 && s <= 30 {
				wait = time.Duration(s) * time.Second
			}
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("snapshot: the origin answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}
