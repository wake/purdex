package pick

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Source is one origin's queue as a worker reads it: the queued jobs it may claim.
type Source interface {
	Host() string
	Queued(ctx context.Context) ([]wire.Job, error)
}

// QueueReader is the part of this host's queue a worker reads (the daemon adapts *queue.Queue to it). forPeers false is the local view,
// which includes ad-hoc jobs.
type QueueReader interface {
	Queued(ctx context.Context, forPeers bool) ([]wire.Job, error)
}

type localSource struct {
	host string
	q    QueueReader
}

// NewLocalSource reads this host's own queue in process. A worker runs its own host's ad-hoc jobs (spec 5), so this is the local
// view, not the one peers get.
func NewLocalSource(host string, q QueueReader) Source { return localSource{host: host, q: q} }

func (s localSource) Host() string { return s.host }
func (s localSource) Queued(ctx context.Context) ([]wire.Job, error) {
	return s.q.Queued(ctx, false)
}

const maxAnswer = 32 << 20

type peerSource struct {
	host, base, token string
	hc                *http.Client
}

// NewPeerSource reads another host's queue over TCP with the pool token (spec 4.3). hc may be nil.
func NewPeerSource(host, base, token string, hc *http.Client) Source {
	if hc == nil {
		hc = &http.Client{}
	}
	return peerSource{host: host, base: strings.TrimRight(base, "/"), token: token, hc: hc}
}

func (s peerSource) Host() string { return s.host }

// peerTimeout bounds one read of a peer's queue: a peer that accepts the connection and then says nothing must not stop the worker
// from serving this host and the other peers.
var peerTimeout = 10 * time.Second

func (s peerSource) Queued(ctx context.Context) ([]wire.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, peerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/v1/jobs?state=queued", nil)
	if err != nil {
		return nil, fmt.Errorf("peer %s: %w", s.host, err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("peer %s: %w", s.host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("peer %s: GET /v1/jobs: HTTP %d", s.host, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return nil, fmt.Errorf("peer %s: %w", s.host, err)
	}
	if len(body) > maxAnswer {
		return nil, fmt.Errorf("peer %s: jobs answer over %d bytes", s.host, maxAnswer)
	}
	r, err := wire.DecodeLenient[wire.JobsListResponse](body)
	if err != nil {
		return nil, fmt.Errorf("peer %s: bad jobs answer: %w", s.host, err)
	}
	// A peer never lists ad-hoc or fast-lane jobs (spec 4.4); one that does is not ours to claim.
	out := r.Jobs[:0:0]
	for _, j := range r.Jobs {
		if j.State == wire.StateQueued && !j.AdHoc && !j.Local {
			out = append(out, j)
		}
	}
	return out, nil
}

// Collect reads every source at once and tags each job with its origin and that origin's zone. The sources are read in parallel so
// that one silent peer costs one timeout, not one per peer, and never delays the others; the result keeps the order of srcs. A source
// that fails is left out and reported by host: one unreachable peer must not stop the worker from serving the others.
func Collect(ctx context.Context, srcs []Source, zone func(host string) wire.Zone) ([]Candidate, map[string]error) {
	type answer struct {
		jobs []wire.Job
		err  error
	}
	answers := make([]answer, len(srcs))
	var wg sync.WaitGroup
	for i, s := range srcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i].jobs, answers[i].err = s.Queued(ctx)
		}()
	}
	wg.Wait()
	var cands []Candidate
	errs := map[string]error{}
	for i, s := range srcs {
		if answers[i].err != nil {
			errs[s.Host()] = answers[i].err
			continue
		}
		var z wire.Zone
		if zone != nil {
			z = zone(s.Host())
		}
		for _, j := range answers[i].jobs {
			cands = append(cands, Candidate{Source: s.Host(), Zone: z, Job: j})
		}
	}
	return cands, errs
}
