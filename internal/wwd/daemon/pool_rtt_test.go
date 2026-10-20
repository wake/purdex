package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stepClock is a clock the test moves by hand.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// slowPeer is another wwd whose routes each take the clock the time in cost to answer, and answer the status in fail instead when
// one is set (both can be changed between polls).
type slowPeer struct {
	*httptest.Server
	mu   sync.Mutex
	cost map[string]time.Duration
	fail map[string]int
}

func newSlowPeer(t *testing.T, alias string, clock *stepClock, cost map[string]time.Duration) *slowPeer {
	t.Helper()
	d, _ := peerDaemon(t, alias, testToken)
	inner := d.TCPHandler()
	s := &slowPeer{cost: cost, fail: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		cost, code := s.cost[r.URL.Path], s.fail[r.URL.Path]
		s.mu.Unlock()
		clock.advance(cost)
		if code != 0 {
			http.Error(w, "no", code)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *slowPeer) set(path string, cost time.Duration, fail int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cost[path], s.fail[path] = cost, fail
}

// rtt_ms is the time of the GET /v1/facts request alone: the slow /v1/info before it is not in it.
func TestPoolRTTCountsOnlyTheFactsRequest(t *testing.T) {
	clock := &stepClock{t: s0}
	srv := newSlowPeer(t, "air26", clock, map[string]time.Duration{"/v1/info": 800 * time.Millisecond, "/v1/facts": 42 * time.Millisecond})
	p := NewPoolPoller([]PeerAddr{{"air26", addr(srv.Server)}}, testToken, nil)
	p.SetClock(clock.now)
	p.PollOnce(context.Background())
	got := p.Peers()[0]
	if !got.Reachable || got.RttMS == nil {
		t.Fatalf("reachable %v rtt %v", got.Reachable, got.RttMS)
	}
	if *got.RttMS != 42 {
		t.Errorf("rtt_ms %v want 42 (the facts request only)", *got.RttMS)
	}
	// the next poll measures again: a faster answer replaces it, and a fraction of a millisecond is kept
	srv.set("/v1/facts", 7500*time.Microsecond, 0)
	p.PollOnce(context.Background())
	if got := p.Peers()[0]; got.RttMS == nil || *got.RttMS != 7.5 {
		t.Errorf("rtt_ms %v want 7.5", got.RttMS)
	}
}

// A peer whose facts did not come (503 before its first sample), or that stopped answering, has no rtt: a stale one is not kept.
func TestPoolRTTIsNullWhenTheFactsRequestFailsOrThePeerIsDown(t *testing.T) {
	clock := &stepClock{t: s0}
	srv := newSlowPeer(t, "mserv", clock, map[string]time.Duration{"/v1/info": 30 * time.Millisecond, "/v1/facts": 30 * time.Millisecond})
	p := NewPoolPoller([]PeerAddr{{"mserv", addr(srv.Server)}}, testToken, nil)
	p.SetClock(clock.now)
	p.PollOnce(context.Background())
	if got := p.Peers()[0]; got.RttMS == nil || *got.RttMS != 30 {
		t.Fatalf("first poll %v", got.RttMS)
	}
	srv.set("/v1/facts", 30*time.Millisecond, http.StatusServiceUnavailable)
	p.PollOnce(context.Background())
	if got := p.Peers()[0]; !got.Reachable || got.RttMS != nil {
		t.Errorf("facts failed: reachable %v rtt %v want reachable and null", got.Reachable, got.RttMS)
	}
	srv.set("/v1/facts", 30*time.Millisecond, 0)
	p.PollOnce(context.Background())
	if got := p.Peers()[0]; got.RttMS == nil {
		t.Fatal("it answers again: rtt back")
	}
	srv.Close()
	p.PollOnce(context.Background())
	if got := p.Peers()[0]; got.Reachable || got.RttMS != nil {
		t.Errorf("down: rtt %v want null", got.RttMS)
	}
}

// The local host is in the pool answer too and has no rtt to itself: the key is not there (an absent rtt_ms is a null one).
func TestPoolSelfHasNoRTT(t *testing.T) {
	d, _ := peerDaemon(t, "mlab", testToken)
	d.pool = NewPoolPoller(nil, testToken, nil)
	mux := http.NewServeMux()
	d.poolRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/pool", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"reachable":true`) || strings.Contains(rec.Body.String(), "rtt_ms") {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}
