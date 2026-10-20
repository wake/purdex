// Package origin gives a worker its two ways to reach a job's origin: this host's own queue in process (Local) and another host's wwd over
// TCP with the pool token (Peer). Both are a runner.Origin (claim, start, heartbeat) and a report.Sink (result, log, release); Local is
// also the pick.QueueReader of the host's own queue. A refused transition is a *runner.Conflict and an unknown job runner.ErrNotFound,
// whichever way the origin is reached, so the runner and the reporter never see which one they talk to.
package origin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// Local is this host's own queue. Its claim is the host's own view: the host's worker may take an ad-hoc job of its own (spec 5, 8.3).
type Local struct{ Q *queue.Queue }

func mapQueueErr(err error) error {
	var ce *queue.ConflictError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ce):
		return &runner.Conflict{State: ce.State}
	case errors.Is(err, queue.ErrNotFound):
		return runner.ErrNotFound
	}
	return err
}

func (l Local) Claim(ctx context.Context, id string, req wire.ClaimRequest) (wire.ClaimResponse, error) {
	r, err := l.Q.Claim(ctx, id, req)
	return r, mapQueueErr(err)
}

func (l Local) Start(ctx context.Context, id, claimID string) (wire.Lease, error) {
	r, err := l.Q.Start(ctx, id, claimID)
	return r, mapQueueErr(err)
}

func (l Local) Heartbeat(ctx context.Context, id, claimID string) (wire.HeartbeatResponse, error) {
	r, err := l.Q.Heartbeat(ctx, id, claimID)
	return r, mapQueueErr(err)
}

func (l Local) Result(ctx context.Context, id string, req wire.ResultRequest) error {
	_, err := l.Q.Result(ctx, id, req)
	return mapQueueErr(err)
}

func (l Local) PutLog(ctx context.Context, id, claimID string, body io.Reader) error {
	return mapQueueErr(l.Q.PutLog(ctx, id, claimID, body))
}

func (l Local) Release(ctx context.Context, id string, req wire.ReleaseRequest) error {
	return mapQueueErr(l.Q.Release(ctx, id, req))
}

// Queued is pick.QueueReader: the queued jobs of this host's queue. forPeers is the peers' view (no ad-hoc, no fast-lane jobs).
func (l Local) Queued(ctx context.Context, forPeers bool) ([]wire.Job, error) {
	return l.Q.List(ctx, queue.Filter{States: []wire.JobState{wire.StateQueued}, ForPeers: forPeers})
}

const (
	callTimeout   = 10 * time.Second
	logTimeout    = 60 * time.Second
	maxAnswerSize = 4 << 20
)

// Peer is another host's wwd, reached over TCP with the pool token.
type Peer struct {
	base, token string
	hc          *http.Client
}

// NewPeer is a Peer for the wwd at base ("http://host:port"). hc may be nil. The token goes with every request, so a redirect is never
// followed.
func NewPeer(base, token string, hc *http.Client) *Peer {
	if hc == nil {
		hc = &http.Client{}
	}
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Peer{base: strings.TrimRight(base, "/"), token: token, hc: &c}
}

func (p *Peer) do(ctx context.Context, method, path string, q url.Values, body io.Reader, ctype string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u := p.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerSize+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxAnswerSize {
		return nil, fmt.Errorf("origin: %s %s: answer over %d bytes", method, path, maxAnswerSize)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return raw, nil
	case http.StatusNotFound:
		// only wwd's own "no such job" is the origin's decision; an empty or HTML 404 is a proxy, a router or a version mismatch
		var nf struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &nf) == nil && nf.Error == "no such job" {
			return nil, runner.ErrNotFound
		}
		return nil, fmt.Errorf("origin: %s %s: HTTP 404 that is not wwd's", method, path)
	case http.StatusConflict:
		c, derr := wire.DecodeLenient[wire.Conflict409](raw)
		if derr != nil {
			return nil, fmt.Errorf("origin: %s %s: 409 with an unreadable body: %w", method, path, derr)
		}
		return nil, &runner.Conflict{State: c.State}
	}
	return nil, fmt.Errorf("origin: %s %s: HTTP %d", method, path, resp.StatusCode)
}

func (p *Peer) postJSON(ctx context.Context, id, verb string, in any) ([]byte, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	return p.do(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/"+verb, nil, bytes.NewReader(raw), "application/json", callTimeout)
}

func decodeAnswer[T any](raw []byte, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	v, derr := wire.DecodeLenient[T](raw)
	if derr != nil {
		return zero, fmt.Errorf("origin: unreadable answer: %w", derr)
	}
	return v, nil
}

func (p *Peer) Claim(ctx context.Context, id string, req wire.ClaimRequest) (wire.ClaimResponse, error) {
	r, err := decodeAnswer[wire.ClaimResponse](p.postJSON(ctx, id, "claim", req))
	if errors.Is(err, runner.ErrNotFound) {
		// a peer answers a claim of an unknown, ad-hoc or fast-lane job with 409 {claimed} (spec 4.4), never 404
		return r, fmt.Errorf("origin: POST claim of %s: unexpected 404 from a peer", id)
	}
	if err == nil && r.Job.ID != id {
		// the job that comes back is the one whose locks were taken and whose snapshot is fetched: it must be the one asked for
		return wire.ClaimResponse{}, fmt.Errorf("origin: claim of %s answered with job %q", id, r.Job.ID)
	}
	return r, err
}

func (p *Peer) Start(ctx context.Context, id, claimID string) (wire.Lease, error) {
	return decodeAnswer[wire.Lease](p.postJSON(ctx, id, "start", wire.StartRequest{ClaimID: claimID}))
}

func (p *Peer) Heartbeat(ctx context.Context, id, claimID string) (wire.HeartbeatResponse, error) {
	return decodeAnswer[wire.HeartbeatResponse](p.postJSON(ctx, id, "heartbeat", wire.HeartbeatRequest{ClaimID: claimID}))
}

func (p *Peer) Result(ctx context.Context, id string, req wire.ResultRequest) error {
	_, err := p.postJSON(ctx, id, "result", req)
	return err
}

func (p *Peer) Release(ctx context.Context, id string, req wire.ReleaseRequest) error {
	_, err := p.postJSON(ctx, id, "release", req)
	return err
}

func (p *Peer) PutLog(ctx context.Context, id, claimID string, body io.Reader) error {
	_, err := p.do(ctx, http.MethodPut, "/v1/jobs/"+url.PathEscape(id)+"/log", url.Values{"claim_id": {claimID}}, body, "text/plain", logTimeout)
	return err
}
