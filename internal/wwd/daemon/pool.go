package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// PoolInterval is how often peers are polled (spec §3.3).
const PoolInterval = 5 * time.Second

const (
	peerTimeout  = 3 * time.Second
	maxPeerBytes = 8 << 20
)

// noRedirect keeps the pool token from following a redirect anywhere.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// PeerAddr is another wwd of the pool: its alias and host:port (from config).
type PeerAddr struct{ Alias, HostPort string }

// PoolPoller keeps this wwd's view of the peers: their info, their latest facts
// and whether they answer. A peer that stops answering keeps its last info and
// facts, marked unreachable; one never reached has a stub info and null facts.
type PoolPoller struct {
	peers  []PeerAddr
	token  string
	client *http.Client

	now func() time.Time
	// dial connects to a peer's SSH port when its wwd does not answer (liveness.go); a test replaces it.
	dial func(ctx context.Context, network, address string) (net.Conn, error)

	mu    sync.RWMutex
	state []wire.Peer // in config order
	// downSince is when each peer last stopped answering (in config order): the start of the first failed poll after an answer, or this
	// poller's creation for a peer never reached. Zero: it answers now. The silence rules (spec §13.3) judge "unreachable for ≥ 3 min" on it.
	downSince []time.Time

	// lastFile and last are the last known info of each peer kept in a file (pool_last.go); "" : not kept
	lastFile  string
	last      map[string]lastKnownPeer
	lastGen   int  // the number of the newest snapshot of last (under mu)
	lastRetry bool // the last write failed: the next poll writes again (under mu)
	saveLast  func(path string, b []byte) error
	saveMu    sync.Mutex // one write of the file at a time, outside mu
	savedGen  int        // the newest snapshot written (under saveMu)
}

// NewPoolPoller polls peers with the pool token. client may be nil.
func NewPoolPoller(peers []PeerAddr, token string, client *http.Client) *PoolPoller {
	if client == nil {
		client = &http.Client{Timeout: peerTimeout, CheckRedirect: noRedirect}
	}
	p := &PoolPoller{peers: peers, token: token, client: client, now: time.Now, state: make([]wire.Peer, len(peers)), downSince: make([]time.Time, len(peers)),
		dial: (&net.Dialer{}).DialContext}
	born := p.now()
	for i, a := range peers {
		p.downSince[i] = born
		// a peer never reached claims the oldest proto: the writers gate on every host reporting proto >= 2 (spec §13.1)
		p.state[i] = wire.Peer{Info: wire.Info{Alias: a.Alias, Proto: wire.MinProto, Labels: []string{}, Fingerprint: map[string]string{}}, Liveness: wire.LivenessUnknown}
	}
	return p
}

// Run polls now and then every PoolInterval until ctx ends.
func (p *PoolPoller) Run(ctx context.Context) {
	t := time.NewTicker(PoolInterval)
	defer t.Stop()
	p.PollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.PollOnce(ctx)
		}
	}
}

// PollOnce asks every peer once, in parallel.
func (p *PoolPoller) PollOnce(ctx context.Context) {
	var wg sync.WaitGroup
	for i := range p.peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.pollPeer(ctx, i)
		}()
	}
	wg.Wait()
}

func (p *PoolPoller) pollPeer(ctx context.Context, i int) {
	started := p.now()
	info, err := get[wire.Info](ctx, p.client, p.peers[i], p.token, "/v1/info")
	if err != nil || info.Alias != p.peers[i].Alias {
		// The configured address and alias are the trust boundary: this catches a
		// wrong address or a wrong daemon (an answer under another name). It does not
		// defend against a compromised peer, which holds the shared pool token anyway
		// and is outside the threat model (spec §11).
		p.mu.Lock()
		p.markDown(i, started)
		answered := answeredOverHTTP(err)
		if answered { // an error status, a body that is not info, another name: something there did answer, so the host is up
			p.setLiveness(i, wire.LivenessWwdDown)
		}
		p.mu.Unlock()
		if !answered {
			p.look(ctx, i) // nothing answered over HTTP: does the host?
		}
		return
	}
	// facts may not exist yet (503 before the peer's first sample): keep what we had
	// rtt_ms is this request alone: timed from here, after the /v1/info above (which can be slow for its own reasons, and is not a look at
	// the facts the pool is for).
	factsStart := p.now()
	facts, ferr := get[wire.Facts](ctx, p.client, p.peers[i], p.token, "/v1/facts")
	factsTook := p.now().Sub(factsStart)
	var write func() // the file of last known infos, written after mu is released (deferred first, so it runs last)
	defer func() {
		if write != nil {
			write()
		}
	}()
	p.mu.Lock()
	defer p.mu.Unlock()
	if ferr == nil && (facts.HostID != info.HostID || facts.Alias != info.Alias) {
		p.markDown(i, started)                 // facts of another host: keep the last verified view
		p.setLiveness(i, wire.LivenessWwdDown) // something answered, so the host is up
		return
	}
	p.state[i].Info, p.state[i].Reachable = info, true
	p.state[i].RttMS = nil // only a facts answer that was taken has one
	if ferr == nil {
		ms := float64(factsTook) / float64(time.Millisecond)
		p.state[i].RttMS = &ms
	}
	p.setLiveness(i, wire.LivenessOK)
	p.downSince[i] = time.Time{}
	write = p.rememberInfo(info)
	switch {
	case ferr == nil:
		p.state[i].Facts = &facts
	case p.state[i].Facts != nil && p.state[i].Facts.HostID != info.HostID:
		p.state[i].Facts = nil // another host now answers under this alias: its predecessor's facts are not its
	}
}

// markDown records that peer i did not answer; the caller holds mu. The time it has been silent counts from the first failure, not the latest.
func (p *PoolPoller) markDown(i int, at time.Time) {
	p.state[i].Reachable, p.state[i].RttMS = false, nil
	if p.downSince[i].IsZero() {
		p.downSince[i] = at
	}
}

// PeerDown is one peer's reachability for the silence rules: Since is when it stopped answering (zero: it answers).
type PeerDown struct {
	Alias string
	Since time.Time
	// Liveness is what the last look at the peer's SSH port found (liveness.go): wwd_down, host_down, ok when it answers, or unknown.
	Liveness wire.Liveness
}

// SetClock replaces the clock the unreachable-since times are read from (a test drives time by hand); a peer not reached yet counts from
// the moment of the call. Call it before the first poll.
func (p *PoolPoller) SetClock(now func() time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.now = now
	born := now()
	for i := range p.downSince {
		if !p.downSince[i].IsZero() {
			p.downSince[i] = born
		}
	}
}

// Downs lists every configured peer with the time it has been unreachable since, in config order.
func (p *PoolPoller) Downs() []PeerDown {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]PeerDown, len(p.peers))
	for i, a := range p.peers {
		out[i] = PeerDown{Alias: a.Alias, Since: p.downSince[i], Liveness: p.state[i].Liveness}
	}
	return out
}

var errTooLarge = fmt.Errorf("peer answer is larger than %d bytes", maxPeerBytes)

// readCapped reads a whole body up to maxPeerBytes; a longer one is an error, not
// a silently shortened answer.
func readCapped(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxPeerBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxPeerBytes {
		return nil, errTooLarge
	}
	return b, nil
}

// StatusError is a peer answering something other than 200: its Code is what a reader tells apart (404 is an older build).
type StatusError struct {
	Path string
	Code int
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s: status %d", e.Path, e.Code) }

func get[T any](ctx context.Context, c *http.Client, a PeerAddr, token, path string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+a.HostPort+path, nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	body, err := readCapped(resp.Body)
	if err != nil {
		if errors.Is(err, errTooLarge) {
			return zero, err // it answered, with too much
		}
		// the connection went away in the middle of the answer: nothing answered (a url.Error, like a failure to connect)
		return zero, &url.Error{Op: "Get", URL: path, Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return zero, &StatusError{Path: path, Code: resp.StatusCode}
	}
	return wire.DecodeLenient[T](body)
}

// Peers is a copy of the current view of the peers.
func (p *PoolPoller) Peers() []wire.Peer {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]wire.Peer, len(p.state))
	for i, s := range p.state {
		out[i] = s
		if s.RttMS != nil {
			v := *s.RttMS // the stored figure is replaced, never edited, but the copy is not to alias it
			out[i].RttMS = &v
		}
		if s.Facts != nil {
			f := *s.Facts // the stored record is replaced, never edited, so a shallow copy is enough
			out[i].Facts = &f
		}
	}
	return out
}

// Pool is this daemon's poller.
func (d *Daemon) Pool() *PoolPoller { return d.pool }

// poolRoutes registers GET /v1/pool: this host first, then the peers. It is a
// socket route (the local Purdex daemon reads the pool here); a peer has no
// business asking another wwd for its view.
func (d *Daemon) poolRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/pool", func(w http.ResponseWriter, r *http.Request) {
		self := wire.Peer{Info: d.info(), Reachable: true}
		if d.opts.Sampler != nil {
			if f, ok := d.opts.Sampler.Latest(); ok {
				self.Facts = &f
			}
		}
		peers := d.pool.Peers()
		// The liveness of the peers goes out only to a reader that asks (wire.PoolLivenessQuery): this route is read by the Purdex daemon
		// of this host, which is not a wwd of the pool, so a reader that does not know the fields must never be shown them. Any other
		// query is ignored, as it always was.
		if r.URL.Query().Get("liveness") != "1" {
			for i := range peers {
				peers[i].Liveness, peers[i].LivenessAt, peers[i].LivenessSince = "", nil, nil
			}
		}
		writeJSON(w, http.StatusOK, wire.Pool{Peers: append([]wire.Peer{self}, peers...)})
	})
}
