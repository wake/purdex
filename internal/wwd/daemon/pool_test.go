package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// peerDaemon is another wwd (alias, token, a sampler with a few records) behind a
// real TCP listener.
func peerDaemon(t *testing.T, alias, token string) (*Daemon, *httptest.Server) {
	t.Helper()
	level := 1
	s := NewSampler(SamplerOptions{HostID: "id-" + alias, Alias: alias, OSVersion: "1", Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}})
	for i := 0; i < 3; i++ {
		s.Tick(s0.Add(time.Duration(i) * 5 * time.Second))
	}
	d := New(Options{HostID: "id-" + alias, Alias: alias, Version: "v0", Token: token, Sampler: s})
	srv := httptest.NewServer(d.TCPHandler())
	t.Cleanup(srv.Close)
	return d, srv
}

func addr(srv *httptest.Server) string { return srv.Listener.Addr().String() }

func TestPoolPollsPeersAndKeepsLastKnownWhenOneGoesAway(t *testing.T) {
	_, up := peerDaemon(t, "air26", testToken)
	_, flaky := peerDaemon(t, "mserv", testToken)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadAddr := addr(dead)
	dead.Close() // nobody listens here

	p := NewPoolPoller([]PeerAddr{{"air26", addr(up)}, {"mserv", addr(flaky)}, {"air19", deadAddr}}, testToken, nil)
	p.PollOnce(context.Background())
	got := p.Peers()
	if len(got) != 3 {
		t.Fatalf("%d peers", len(got))
	}
	byAlias := map[string]wire.Peer{}
	for _, x := range got {
		byAlias[x.Info.Alias] = x
	}
	if a := byAlias["air26"]; !a.Reachable || a.Facts == nil || a.Facts.Alias != "air26" {
		t.Errorf("air26: %+v", a)
	}
	if a := byAlias["air19"]; a.Reachable || a.Facts != nil || a.Info.Proto != wire.MinProto || a.Info.Labels == nil {
		t.Errorf("a peer never reached: reachable=false, facts null, a valid info stub: %+v", a)
	}
	if err := (wire.Pool{Peers: got}).Validate(); err != nil {
		t.Fatal(err)
	}

	// a reachable peer that goes away keeps its last info and facts, marked unreachable
	flaky.Close()
	p.PollOnce(context.Background())
	m := p.Peers()[1]
	if m.Reachable || m.Facts == nil || m.Info.HostID != "id-mserv" {
		t.Errorf("after it went away: %+v", m)
	}
}

// The configured alias is the identity the pool is keyed by: a peer that answers
// as somebody else (wrong address, wrong daemon, a hijacked peer) is unreachable,
// never merged into the view (R2).
func TestPoolRefusesAPeerThatAnswersAsSomeoneElse(t *testing.T) {
	_, other := peerDaemon(t, "air26", testToken) // really air26
	p := NewPoolPoller([]PeerAddr{{"mserv", addr(other)}}, testToken, nil)
	p.PollOnce(context.Background())
	got := p.Peers()[0]
	if got.Reachable || got.Facts != nil || got.Info.Alias != "mserv" || got.Info.HostID != "" {
		t.Errorf("an air26 answering for mserv was taken: %+v", got)
	}

	// facts that disagree with the info of the same answer are not stored either
	var factsAlias atomic.Value
	factsAlias.Store("mserv")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/info":
			writeJSON(w, 200, wire.Info{HostID: "id-mserv", Alias: "mserv", Version: "v", Proto: wire.Proto, Platform: "linux", Arch: "amd64", Labels: []string{}})
		case "/v1/facts":
			f := wire.Facts{V: wire.Proto, HostID: "id-mserv", Alias: factsAlias.Load().(string), Derived: wire.Derived{Zone: wire.ZoneNormal}, At: s0}
			writeJSON(w, 200, f)
		}
	}))
	defer srv.Close()
	p = NewPoolPoller([]PeerAddr{{"mserv", addr(srv)}}, testToken, nil)
	p.PollOnce(context.Background())
	if g := p.Peers()[0]; !g.Reachable || g.Facts == nil {
		t.Fatalf("consistent answer refused: %+v", g)
	}
	factsAlias.Store("air26")
	p.PollOnce(context.Background())
	if g := p.Peers()[0]; g.Reachable || g.Facts == nil || g.Facts.Alias != "mserv" {
		t.Errorf("inconsistent facts must mark unreachable and keep the last verified ones: %+v", g)
	}
}

// The daemon behind an alias was replaced (new host_id) and has no sample yet:
// the old host's facts must not stay on the new host's entry.
func TestPoolDropsFactsOfAReplacedHost(t *testing.T) {
	var id atomic.Value
	id.Store("old")
	var noFacts atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/info":
			writeJSON(w, 200, wire.Info{HostID: id.Load().(string), Alias: "mserv", Version: "v", Proto: wire.Proto, Platform: "linux", Arch: "amd64", Labels: []string{}})
		case "/v1/facts":
			if noFacts.Load() {
				http.Error(w, "no sample yet", http.StatusServiceUnavailable)
				return
			}
			writeJSON(w, 200, wire.Facts{V: wire.Proto, HostID: id.Load().(string), Alias: "mserv", Derived: wire.Derived{Zone: wire.ZoneNormal}, At: s0})
		}
	}))
	defer srv.Close()
	p := NewPoolPoller([]PeerAddr{{"mserv", addr(srv)}}, testToken, nil)
	p.PollOnce(context.Background())
	if g := p.Peers()[0]; g.Facts == nil || g.Facts.HostID != "old" {
		t.Fatalf("setup: %+v", g)
	}
	id.Store("new")
	noFacts.Store(true)
	p.PollOnce(context.Background())
	g := p.Peers()[0]
	if !g.Reachable || g.Info.HostID != "new" || g.Facts != nil {
		t.Errorf("the old host's facts stayed on the new host: %+v", g)
	}
}

func TestPoolRefusesAnOversizedAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(append([]byte(`{"host_id":"x","pad":"`), make([]byte, maxPeerBytes+10)...))
	}))
	defer srv.Close()
	p := NewPoolPoller([]PeerAddr{{"big", addr(srv)}}, testToken, nil)
	p.PollOnce(context.Background())
	if p.Peers()[0].Reachable {
		t.Error("an oversized answer must not make a peer reachable")
	}
}

func TestPoolSendsTheTokenAndRejectedTokenIsUnreachable(t *testing.T) {
	var seen atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		http.Error(w, "unauthorized", 401)
	}))
	defer srv.Close()
	p := NewPoolPoller([]PeerAddr{{"x", addr(srv)}}, "pool-token", nil)
	p.PollOnce(context.Background())
	if seen.Load() != "Bearer pool-token" {
		t.Errorf("authorization %v", seen.Load())
	}
	if got := p.Peers()[0]; got.Reachable {
		t.Errorf("a peer that answers 401 is not usable: %+v", got)
	}
}

func TestPoolRouteIsSocketOnlyAndListsSelfFirst(t *testing.T) {
	_, srv := peerDaemon(t, "air26", testToken)
	level := 1
	self := NewSampler(SamplerOptions{HostID: "id-mlab", Alias: "mlab", Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true})
	self.Tick(s0)
	d := New(Options{HostID: "id-mlab", Alias: "mlab", Version: "v0", Token: testToken, Sampler: self, Peers: []PeerAddr{{"air26", addr(srv)}}})
	d.Pool().PollOnce(context.Background())

	w := do(d.SocketHandler(), "GET", "/v1/pool", "")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	pool, err := wire.Decode[wire.Pool](w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Peers) != 2 || pool.Peers[0].Info.Alias != "mlab" || !pool.Peers[0].Reachable || pool.Peers[0].Facts == nil || pool.Peers[1].Info.Alias != "air26" {
		t.Fatalf("%+v", pool.Peers)
	}
	// the pool view is for the local Purdex daemon: a peer cannot ask for it over TCP
	if w := do(d.TCPHandler(), "GET", "/v1/pool", "Bearer "+testToken); w.Code != http.StatusNotFound {
		t.Fatalf("TCP /v1/pool: %d, want 404", w.Code)
	}
}
