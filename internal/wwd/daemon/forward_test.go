package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func newLocal(t *testing.T, peers ...PeerAddr) *Daemon {
	t.Helper()
	level := 1
	s := NewSampler(SamplerOptions{HostID: "id-mlab", Alias: "mlab", Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}})
	for i := 0; i < 2; i++ {
		s.Tick(s0.Add(time.Duration(i) * 5 * time.Second))
	}
	return New(Options{HostID: "id-mlab", Alias: "mlab", Version: "v0", Token: testToken, Sampler: s, Peers: peers})
}

func historyPath(host string) string {
	q := url.Values{"since": {s0.Format(time.RFC3339)}}
	if host != "" {
		q.Set("host", host)
	}
	return "/v1/facts/history?" + q.Encode()
}

func TestHistoryForwardsToAPeerThroughTheSocket(t *testing.T) {
	_, srv := peerDaemon(t, "air26", testToken)
	d := newLocal(t, PeerAddr{"air26", addr(srv)})

	w := do(d.SocketHandler(), "GET", historyPath("air26"), "")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	resp, err := wire.Decode[wire.FactsHistoryResponse](w.Body.Bytes())
	if err != nil || len(resp.Records) != 3 || resp.Records[0].Alias != "air26" {
		t.Fatalf("the peer's own records expected, got %+v (%v)", resp, err)
	}
	// the local alias, or no host, is answered here
	for _, host := range []string{"", "mlab"} {
		w := do(d.SocketHandler(), "GET", historyPath(host), "")
		r, err := wire.Decode[wire.FactsHistoryResponse](w.Body.Bytes())
		if w.Code != 200 || err != nil || len(r.Records) != 2 || r.Records[0].Alias != "mlab" {
			t.Errorf("host=%q: %d %v %+v", host, w.Code, err, r)
		}
	}
}

func TestHistoryForwardingFailureModes(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadAddr := addr(dead)
	dead.Close()
	_, wrongToken := peerDaemon(t, "mserv", "another-token")
	d := newLocal(t, PeerAddr{"air19", deadAddr}, PeerAddr{"mserv", addr(wrongToken)})

	// a peer that cannot be reached: 502 {host, reason}
	w := do(d.SocketHandler(), "GET", historyPath("air19"), "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("unreachable peer: %d %s", w.Code, w.Body)
	}
	var e struct{ Host, Reason string }
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Host != "air19" || e.Reason == "" {
		t.Fatalf("502 body %s", w.Body)
	}
	// the peer's own answer is returned as it is (here its 401)
	if w := do(d.SocketHandler(), "GET", historyPath("mserv"), ""); w.Code != http.StatusUnauthorized {
		t.Errorf("peer's 401 expected, got %d", w.Code)
	}
	// a host that is not in the pool
	if w := do(d.SocketHandler(), "GET", historyPath("nowhere"), ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown host: %d", w.Code)
	}
	// a bad query is refused before anything is sent
	if w := do(d.SocketHandler(), "GET", "/v1/facts/history?since=nope&host=air19", ""); w.Code != http.StatusBadRequest {
		t.Errorf("bad since: %d", w.Code)
	}
}

// An answer bigger than the cap is a 502, not a truncated 200 (R2).
func TestForwardingRefusesAnOversizedAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(make([]byte, maxPeerBytes+10))
	}))
	defer srv.Close()
	d := newLocal(t, PeerAddr{"air26", addr(srv)})
	w := do(d.SocketHandler(), "GET", historyPath("air26"), "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("oversized answer: %d", w.Code)
	}
	var e struct{ Host, Reason string }
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Host != "air26" || e.Reason == "" {
		t.Fatalf("body %s", w.Body)
	}
}

// Only the socket forwards. A TCP request carrying host is refused whatever it
// names, so a token holder cannot relay through this wwd (no multi-hop).
func TestTCPRefusesTheHostParameter(t *testing.T) {
	_, srv := peerDaemon(t, "air26", testToken)
	d := newLocal(t, PeerAddr{"air26", addr(srv)})
	auth := "Bearer " + testToken
	for _, host := range []string{"air26", "mlab", "nowhere", ""} {
		path := historyPath("")
		if host != "" {
			path = historyPath(host)
		} else {
			path += "&host="
		}
		if w := do(d.TCPHandler(), "GET", path, auth); w.Code != http.StatusBadRequest {
			t.Errorf("TCP history host=%q: %d, want 400", host, w.Code)
		}
	}
	// any route, not only history
	for _, p := range []string{"/v1/facts?host=air26", "/v1/info?host=mlab", "/v1/events?host=air26"} {
		if w := do(d.TCPHandler(), "GET", p, auth); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", p, w.Code)
		}
	}
	// the token check still comes first
	if w := do(d.TCPHandler(), "GET", historyPath("air26"), ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", w.Code)
	}
}

// What the socket forwards carries no host parameter, so the peer (which refuses
// one) is asked for its own history only.
func TestForwardedRequestHasNoHostAndCarriesTheToken(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"step":"5s","records":[]}`))
	}))
	defer srv.Close()
	d := newLocal(t, PeerAddr{"air26", addr(srv)})
	w := do(d.SocketHandler(), "GET", historyPath("air26")+"&step=1m", "")
	if w.Code != 200 || got == nil {
		t.Fatalf("%d", w.Code)
	}
	if got.URL.Query().Has("host") || got.URL.Query().Get("step") != "1m" || got.URL.Path != "/v1/facts/history" {
		t.Errorf("forwarded as %s", got.URL)
	}
	if got.Header.Get("Authorization") != "Bearer "+testToken {
		t.Errorf("authorization %q", got.Header.Get("Authorization"))
	}
}
