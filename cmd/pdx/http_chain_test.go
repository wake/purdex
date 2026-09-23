// cmd/pdx/http_chain_test.go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	hosttransfermod "github.com/wake/purdex/internal/module/hosttransfer"
	"github.com/wake/purdex/internal/tmux"
)

// muxRecorder is the stub inner mux passed to newOuterHandler: it always
// answers 200 and records whether it was invoked and the Principal (if
// any) the auth chain attached to the request context, so tests can tell
// which chain a request travelled through without registering real
// module routes.
type muxRecorder struct {
	called       bool
	principal    middleware.Principal
	hasPrincipal bool
}

func (m *muxRecorder) reset() {
	m.called = false
	m.principal = middleware.Principal{}
	m.hasPrincipal = false
}

func (m *muxRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.called = true
		m.principal, m.hasPrincipal = middleware.PrincipalFrom(r.Context())
		w.WriteHeader(200)
	})
}

// newTestCore builds a core.Core the way
// internal/module/agent/owner_resolver_test.go does.
func newTestCore(cfg *config.Config) *core.Core {
	return core.New(core.CoreDeps{
		Config:   cfg,
		Tmux:     tmux.NewFakeExecutor(),
		Registry: core.NewServiceRegistry(),
	})
}

func doRequest(t *testing.T, h http.Handler, method, target, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// wsUpgrade gives req the WebSocket handshake headers (Connection:
// Upgrade + Upgrade: websocket + Sec-WebSocket-Version: 13) — since the
// codex R2 follow-up a GET with these is the only request shape TokenAuth
// accepts a one-time ?ticket= on.
func wsUpgrade(req *http.Request) *http.Request {
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	return req
}

// TestNewOuterHandler_EmptyAdminToken covers the empty-admin-token rows:
// the general chain stays wide open (TokenAuth semantics), the peer chain
// still requires a matching bearer, and a valid host bearer still works.
func TestNewOuterHandler_EmptyAdminToken(t *testing.T) {
	cfg := &config.Config{
		Token: "",
		Peers: config.PeersConfig{
			Hosts: []config.PeerHost{
				{Alias: "host-a", HostID: "hostid-a", InboundToken: "host-a-token"},
			},
		},
	}
	c := newTestCore(cfg)
	rec := &muxRecorder{}
	outer := newOuterHandler(c, rec.handler(), nil)

	t.Run("/api/sessions 200 via general chain", func(t *testing.T) {
		rec.reset()
		res := doRequest(t, outer, "GET", "/api/sessions", "")
		if res.Code != 200 {
			t.Fatalf("want 200, got %d", res.Code)
		}
		if !rec.called {
			t.Fatal("want inner mux called")
		}
		if rec.hasPrincipal {
			t.Fatalf("want no principal on general chain, got %+v", rec.principal)
		}
	})

	t.Run("/api/peers 401 without bearer", func(t *testing.T) {
		rec.reset()
		res := doRequest(t, outer, "GET", "/api/peers", "")
		if res.Code != 401 {
			t.Fatalf("want 401, got %d", res.Code)
		}
		if rec.called {
			t.Fatal("want inner mux NOT called")
		}
	})

	t.Run("/api/peers 200 with valid host bearer", func(t *testing.T) {
		rec.reset()
		res := doRequest(t, outer, "GET", "/api/peers", "host-a-token")
		if res.Code != 200 {
			t.Fatalf("want 200, got %d", res.Code)
		}
		if !rec.called {
			t.Fatal("want inner mux called")
		}
		if !rec.hasPrincipal || rec.principal.Kind != middleware.PrincipalHost || rec.principal.Alias != "host-a" || rec.principal.HostID != "hostid-a" {
			t.Fatalf("want host principal host-a/hostid-a, got hasPrincipal=%v %+v", rec.hasPrincipal, rec.principal)
		}
	})
}

// TestNewOuterHandler_TicketNeverConsultedOnPeerChain proves the peer chain
// never touches the ticket store: a valid ticket presented on /api/peers is
// rejected, and the SAME ticket still validates afterwards on the general
// chain — which would fail if the peer chain had already consumed it.
func TestNewOuterHandler_TicketNeverConsultedOnPeerChain(t *testing.T) {
	cfg := &config.Config{Token: "admin-secret"}
	c := newTestCore(cfg)
	rec := &muxRecorder{}
	outer := newOuterHandler(c, rec.handler(), nil)

	ticket, err := c.Tickets.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rec.reset()
	res := doRequest(t, outer, "GET", "/api/peers?ticket="+ticket, "")
	if res.Code != 401 {
		t.Fatalf("/api/peers?ticket=...: want 401, got %d", res.Code)
	}
	if rec.called {
		t.Fatal("/api/peers?ticket=...: want inner mux NOT called")
	}

	// If PeerAuth had consulted (and thus consumed) the ticket, this
	// second use on the general chain — as a WebSocket upgrade, the only
	// shape tickets authenticate — would now fail.
	rec.reset()
	req := wsUpgrade(httptest.NewRequest("GET", "/api/sessions?ticket="+ticket, nil))
	res = httptest.NewRecorder()
	outer.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("/api/sessions?ticket=... (WS upgrade): want 200 (ticket still valid), got %d", res.Code)
	}
	if !rec.called {
		t.Fatal("/api/sessions?ticket=... (WS upgrade): want inner mux called")
	}
}

// TestNewOuterHandler_PeersHostsSubpathForbiddenForHost covers the P2 host
// policy: a host principal may reach GET /api/peers but not sub-paths.
func TestNewOuterHandler_PeersHostsSubpathForbiddenForHost(t *testing.T) {
	cfg := &config.Config{
		Token: "admin-secret",
		Peers: config.PeersConfig{
			Hosts: []config.PeerHost{
				{Alias: "host-a", HostID: "hostid-a", InboundToken: "host-a-token"},
			},
		},
	}
	c := newTestCore(cfg)
	rec := &muxRecorder{}
	outer := newOuterHandler(c, rec.handler(), nil)

	res := doRequest(t, outer, "GET", "/api/peers/hosts", "host-a-token")
	if res.Code != 403 {
		t.Fatalf("want 403, got %d", res.Code)
	}
	if rec.called {
		t.Fatal("want inner mux NOT called")
	}
}

// TestNewOuterHandler_PeersXFallsThroughToGeneralChain proves ServeMux
// routing: "/api/peersx" is not a sub-path of "/api/peers/" and reaches the
// general chain, which uses plain TokenAuth semantics (no Principal set).
func TestNewOuterHandler_PeersXFallsThroughToGeneralChain(t *testing.T) {
	cfg := &config.Config{Token: ""}
	c := newTestCore(cfg)
	rec := &muxRecorder{}
	outer := newOuterHandler(c, rec.handler(), nil)

	res := doRequest(t, outer, "GET", "/api/peersx", "")
	if res.Code != 200 {
		t.Fatalf("want 200, got %d", res.Code)
	}
	if !rec.called {
		t.Fatal("want inner mux called")
	}
	if rec.hasPrincipal {
		t.Fatalf("want no principal on general chain, got %+v", rec.principal)
	}
}

// TestNewOuterHandler_AdminBearerOnPeersSeenAsAdminPrincipal covers the
// admin path through the peer chain.
func TestNewOuterHandler_AdminBearerOnPeersSeenAsAdminPrincipal(t *testing.T) {
	cfg := &config.Config{Token: "admin-secret"}
	c := newTestCore(cfg)
	rec := &muxRecorder{}
	outer := newOuterHandler(c, rec.handler(), nil)

	res := doRequest(t, outer, "GET", "/api/peers/hosts", "admin-secret")
	if res.Code != 200 {
		t.Fatalf("want 200, got %d", res.Code)
	}
	if !rec.called {
		t.Fatal("want inner mux called")
	}
	if !rec.hasPrincipal || rec.principal.Kind != middleware.PrincipalAdmin {
		t.Fatalf("want admin principal, got hasPrincipal=%v %+v", rec.hasPrincipal, rec.principal)
	}
}

// TestOuterChain_NexAuthMatrix (characterization) pins that the outer HTTP
// chain never lets an unauthenticated request reach a handler mounted at
// /api/nex/. It exercises the general chain (TokenAuth + IPWhitelist +
// PairingGuard, per newOuterHandler) with a probe mux.Handle("/api/nex/",
// ...) standing in for the mounted Nexen module, plus the SSE CORS
// preflight. These rows pin behaviour that T3b-T9 already produce and are
// expected GREEN on first run.
func TestOuterChain_NexAuthMatrix(t *testing.T) {
	// newHarness builds a fresh core.Core + probe + outer handler per
	// sub-test so state (tickets, pairing) never leaks between rows.
	newHarness := func() (*core.Core, *muxRecorder, http.Handler) {
		cfg := &config.Config{
			Token: "t",
			Peers: config.PeersConfig{
				Hosts: []config.PeerHost{
					{Alias: "peer-a", HostID: "hostid-a", InboundToken: "p"},
				},
			},
		}
		c := newTestCore(cfg)
		probe := &muxRecorder{}
		inner := http.NewServeMux()
		inner.Handle("/api/nex/", probe.handler())
		outer := newOuterHandler(c, inner, []string{"127.0.0.1"})
		return c, probe, outer
	}

	// nexReq builds a request against /api/nex/v1/x (or an overridden
	// target) from an allow-listed RemoteAddr unless the caller sets a
	// different one.
	nexReq := func(method, target string) *http.Request {
		if target == "" {
			target = "/api/nex/v1/x"
		}
		req := httptest.NewRequest(method, target, nil)
		req.RemoteAddr = "127.0.0.1:1"
		return req
	}

	// variants is the "same three" request shapes shared by the
	// credential rows: plain GET, POST, and GET with an SSE Accept
	// header (the shape a client opening an EventSource would send).
	type variant struct {
		name   string
		method string
		accept string
	}
	variants := []variant{
		{"GET", http.MethodGet, ""},
		{"POST", http.MethodPost, ""},
		{"GET_Accept_text_event-stream", http.MethodGet, "text/event-stream"},
	}

	runVariant := func(t *testing.T, v variant, target, bearer string, wantStatus int, wantReached bool) {
		t.Helper()
		_, probe, outer := newHarness()
		req := nexReq(v.method, target)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if v.accept != "" {
			req.Header.Set("Accept", v.accept)
		}
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("want %d, got %d", wantStatus, rec.Code)
		}
		if probe.called != wantReached {
			t.Fatalf("want probe.called=%v, got %v", wantReached, probe.called)
		}
	}

	t.Run("no credential", func(t *testing.T) {
		for _, v := range variants {
			t.Run(v.name, func(t *testing.T) {
				runVariant(t, v, "/api/nex/v1/x", "", 401, false)
			})
		}
	})

	t.Run("Bearer wrong", func(t *testing.T) {
		for _, v := range variants {
			t.Run(v.name, func(t *testing.T) {
				runVariant(t, v, "/api/nex/v1/x", "wrong", 401, false)
			})
		}
	})

	t.Run("Bearer t", func(t *testing.T) {
		for _, v := range variants {
			t.Run(v.name, func(t *testing.T) {
				runVariant(t, v, "/api/nex/v1/x", "t", 200, true)
			})
		}
	})

	t.Run("fresh ticket", func(t *testing.T) {
		// A one-time WS ticket authenticates WebSocket upgrades only
		// (codex R2 follow-up): on the plain GET / POST / SSE-GET shapes
		// it is not even consulted, so every /api/nex/... REST request
		// needs the bearer. The probe must not be reached.
		for _, v := range variants {
			t.Run(v.name, func(t *testing.T) {
				// Each variant needs its own core (and thus its own
				// TicketStore) so it can mint a ticket that hasn't been
				// touched by another sub-test; build the harness here
				// instead of going through runVariant's fresh-per-call
				// harness so the ticket is generated against the same
				// core the request is served by.
				c, probe, outer := newHarness()
				ticket, err := c.Tickets.Generate()
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				req := nexReq(v.method, "/api/nex/v1/x?ticket="+ticket)
				if v.accept != "" {
					req.Header.Set("Accept", v.accept)
				}
				rec := httptest.NewRecorder()
				outer.ServeHTTP(rec, req)
				if rec.Code != 401 {
					t.Fatalf("want 401, got %d", rec.Code)
				}
				if probe.called {
					t.Fatal("want probe NOT reached")
				}
			})
		}
	})

	t.Run("fresh ticket, WebSocket upgrade headers", func(t *testing.T) {
		// Documents that a WebSocket upgrade carrying a fresh ticket does
		// pass the chain even on a nex path: the chain cannot know Nexen
		// has no WS routes (the real handler answers 404 there). Only
		// reachability is asserted.
		c, probe, outer := newHarness()
		ticket, err := c.Tickets.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		req := wsUpgrade(nexReq(http.MethodGet, "/api/nex/v1/x?ticket="+ticket))
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("want 200, got %d", rec.Code)
		}
		if !probe.called {
			t.Fatal("want probe reached")
		}
	})

	t.Run("fresh ticket, POST with WebSocket upgrade headers", func(t *testing.T) {
		// A WebSocket handshake is a GET; a POST wearing the upgrade
		// headers is a REST call in a costume and must not get the
		// ticket path (the ticket stays unconsumed, too).
		c, probe, outer := newHarness()
		ticket, err := c.Tickets.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		req := wsUpgrade(nexReq(http.MethodPost, "/api/nex/v1/x?ticket="+ticket))
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("want 401, got %d", rec.Code)
		}
		if probe.called {
			t.Fatal("want probe NOT reached")
		}
		// Not consumed: the same ticket still opens a real handshake.
		req2 := wsUpgrade(nexReq(http.MethodGet, "/api/nex/v1/x?ticket="+ticket))
		rec2 := httptest.NewRecorder()
		outer.ServeHTTP(rec2, req2)
		if rec2.Code != 200 || !probe.called {
			t.Fatalf("ticket was consumed by the POST: GET handshake got %d, probe.called=%v", rec2.Code, probe.called)
		}
	})

	t.Run("same ticket reused", func(t *testing.T) {
		c, probe, outer := newHarness()
		ticket, err := c.Tickets.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		// First use (as a WebSocket upgrade, the only shape tickets
		// authenticate): consumes the one-time ticket via the real chain.
		req := wsUpgrade(nexReq(http.MethodGet, "/api/nex/v1/x?ticket="+ticket))
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("first use: want 200, got %d", rec.Code)
		}
		if !probe.called {
			t.Fatal("first use: want probe called")
		}

		// Second use of the same ticket: not reached, one-time only.
		probe.reset()
		req2 := wsUpgrade(nexReq(http.MethodGet, "/api/nex/v1/x?ticket="+ticket))
		rec2 := httptest.NewRecorder()
		outer.ServeHTTP(rec2, req2)
		if rec2.Code != 401 {
			t.Fatalf("second use: want 401, got %d", rec2.Code)
		}
		if probe.called {
			t.Fatal("second use: want probe NOT reached")
		}
	})

	t.Run("Bearer p at /api/nex/v1/x", func(t *testing.T) {
		// The peer inbound token "p" only authenticates on the
		// /api/peers chain; /api/nex/ routes through the general chain,
		// which only accepts the admin token "t" (or a valid ticket on a
		// WebSocket upgrade).
		_, probe, outer := newHarness()
		req := nexReq(http.MethodGet, "/api/nex/v1/x")
		req.Header.Set("Authorization", "Bearer p")
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("want 401, got %d", rec.Code)
		}
		if probe.called {
			t.Fatal("want probe NOT reached")
		}
	})

	t.Run("pairing active, Bearer t", func(t *testing.T) {
		c, probe, outer := newHarness()
		c.Pairing.Set(core.StatePairing)
		req := nexReq(http.MethodGet, "/api/nex/v1/x")
		req.Header.Set("Authorization", "Bearer t")
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		if rec.Code != 503 {
			t.Fatalf("want 503, got %d", rec.Code)
		}
		if probe.called {
			t.Fatal("want probe NOT reached")
		}
	})

	t.Run("RemoteAddr off-list, Bearer t", func(t *testing.T) {
		_, probe, outer := newHarness()
		req := nexReq(http.MethodGet, "/api/nex/v1/x")
		req.RemoteAddr = "10.9.8.7:1"
		req.Header.Set("Authorization", "Bearer t")
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("want 403, got %d", rec.Code)
		}
		if probe.called {
			t.Fatal("want probe NOT reached")
		}
	})

	t.Run("Bearer t, dotdot path /api/nex/../api/health", func(t *testing.T) {
		// http.ServeMux cleans dot-segments before dispatch and, when
		// cleaning changes the path, answers with a redirect to the
		// cleaned path directly -- the request never reaches the
		// general chain (or the probe) at all. The brief's table
		// predicted a 301; as of the Go 1.22+ ServeMux rewrite (this
		// repo builds with go1.26) mux.findHandler answers this case
		// with StatusTemporaryRedirect (307), not StatusMovedPermanently
		// (301) -- see net/http server.go's findHandler/matchOrRedirect.
		// That is a stdlib-version detail, not project behaviour, so
		// only "probe not reached" is asserted; the observed status is
		// recorded below and in the task report.
		_, probe, outer := newHarness()
		req := nexReq(http.MethodGet, "/api/nex/../api/health")
		req.Header.Set("Authorization", "Bearer t")
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		t.Logf("observed status (301 predicted by brief; 307 on go1.22+ ServeMux): %d", rec.Code)
		if probe.called {
			t.Fatal("want probe NOT reached")
		}
	})

	t.Run("Bearer t, double-slash path //api/nex/v1/x", func(t *testing.T) {
		// http.ServeMux's handling of a leading double slash varies by
		// Go version (301 redirect after cleaning, or 404 if it isn't
		// treated as needing a clean); the brief explicitly says not to
		// pin the status here. Only "probe not reached" is asserted;
		// the actual status is recorded in the report.
		_, probe, outer := newHarness()
		req := nexReq(http.MethodGet, "//api/nex/v1/x")
		req.Header.Set("Authorization", "Bearer t")
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		t.Logf("observed status (not pinned): %d", rec.Code)
		if probe.called {
			t.Fatalf("want probe NOT reached (observed status %d)", rec.Code)
		}
	})

	t.Run("OPTIONS /api/nex/v1/events SSE preflight", func(t *testing.T) {
		// CORS is the outermost middleware and answers OPTIONS directly
		// (204, static Access-Control-Allow-Headers) before IPWhitelist,
		// PairingGuard or TokenAuth ever run -- no credential is needed
		// and the probe is never reached. This is the outer half of I14;
		// the inner (module-level) half is out of scope for this task.
		_, probe, outer := newHarness()
		req := nexReq(http.MethodOptions, "/api/nex/v1/events")
		req.Header.Set("Access-Control-Request-Headers", "last-event-id, authorization")
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("want 204, got %d", rec.Code)
		}
		allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
		if !strings.Contains(allowHeaders, "Last-Event-ID") {
			t.Fatalf("want Access-Control-Allow-Headers to contain Last-Event-ID, got %q", allowHeaders)
		}
		if probe.called {
			t.Fatal("want probe NOT reached")
		}
	})
}

// TestNewOuterHandler_PeerChainObservesHostMatchUnderCfgRLock: the peer
// chain's matcher is peersmod.HostMatcher, so a host bearer's match is
// handed to c.HostAuthObserver (alias + token fingerprint) INSIDE the
// matcher's CfgMu.RLock hold — TryLock on CfgMu fails from inside the
// observer — and admin/unmatched bearers never reach it (codex F2).
func TestNewOuterHandler_PeerChainObservesHostMatchUnderCfgRLock(t *testing.T) {
	cfg := &config.Config{
		Token: "admin-token",
		Peers: config.PeersConfig{
			Hosts: []config.PeerHost{{Alias: "host-a", HostID: "hostid-a", InboundToken: "host-a-token"}},
		},
	}
	c := newTestCore(cfg)
	type obs struct {
		alias, fp     string
		writeLockFree bool
	}
	var seen []obs
	c.HostAuthObserver = func(alias, fp string) {
		free := c.CfgMu.TryLock()
		if free {
			c.CfgMu.Unlock()
		}
		seen = append(seen, obs{alias, fp, free})
	}
	rec := &muxRecorder{}
	outer := newOuterHandler(c, rec.handler(), nil)

	if res := doRequest(t, outer, "GET", "/api/peers", "host-a-token"); res.Code != 200 {
		t.Fatalf("host bearer: want 200, got %d", res.Code)
	}
	if len(seen) != 1 || seen[0].alias != "host-a" || seen[0].fp != config.TokenFingerprint("host-a-token") {
		t.Fatalf("observer calls = %+v; want exactly one for host-a with the bearer's fingerprint", seen)
	}
	if seen[0].writeLockFree {
		t.Fatal("observer ran with CfgMu free for writing: the match and its observation are not one critical section")
	}
	doRequest(t, outer, "GET", "/api/peers", "admin-token")
	doRequest(t, outer, "GET", "/api/peers", "nope")
	doRequest(t, outer, "GET", "/api/sessions", "host-a-token") // general chain, not PeerAuth
	if len(seen) != 1 {
		t.Fatalf("observer called for a non-host-match: %+v", seen)
	}
}

// TestOuterChain_HostTransferBehindTokenAuth (H4a test 15): the two host
// transfer routes are on the general chain, so with an admin token
// configured a request without the bearer — or with a peer's host token —
// is 401 and never reaches the module; with the bearer it does.
func TestOuterChain_HostTransferBehindTokenAuth(t *testing.T) {
	cfg := &config.Config{
		Token: "admin-token",
		Peers: config.PeersConfig{
			Hosts: []config.PeerHost{{Alias: "host-a", HostID: "hostid-a", InboundToken: "host-a-token"}},
		},
	}
	c := newTestCore(cfg)
	mod := hosttransfermod.New()
	if err := mod.Init(c); err != nil {
		t.Fatalf("init: %v", err)
	}
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	outer := newOuterHandler(c, mux, nil)

	post := func(path, body, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		return rec
	}
	const hosts = `{"hosts":[{"ip":"10.0.0.1","token":"t"}]}`

	for _, bearer := range []string{"", "host-a-token", "nope"} {
		if res := post("/api/host-transfer", hosts, bearer); res.Code != http.StatusUnauthorized {
			t.Fatalf("create with bearer %q: want 401, got %d", bearer, res.Code)
		}
		if res := post("/api/host-transfer/redeem", `{"code":"ZZZZZZZZ"}`, bearer); res.Code != http.StatusUnauthorized {
			t.Fatalf("redeem with bearer %q: want 401, got %d", bearer, res.Code)
		}
	}

	res := post("/api/host-transfer", hosts, "admin-token")
	if res.Code != http.StatusOK {
		t.Fatalf("create with bearer: want 200, got %d %s", res.Code, res.Body.String())
	}
	var created struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil || len(created.Code) != 8 {
		t.Fatalf("create body %s: %v", res.Body.String(), err)
	}
	if res := post("/api/host-transfer/redeem", `{"code":"ZZZZZZZZ"}`, "admin-token"); res.Code != http.StatusNotFound {
		t.Fatalf("redeem of a wrong code with bearer: want 404 from the module, got %d", res.Code)
	}
	res = post("/api/host-transfer/redeem", `{"code":"`+created.Code+`"}`, "admin-token")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"10.0.0.1"`) {
		t.Fatalf("redeem with bearer: want 200 with the rows, got %d %s", res.Code, res.Body.String())
	}
}

// TestOuterChain_HostTransferFailsClosedWithoutAdminToken (H4a attacker
// finding): with no admin token configured TokenAuth lets every request
// through, so the module itself refuses — 403 no_token, not 200 — rather
// than park or hand out host credentials unauthenticated.
func TestOuterChain_HostTransferFailsClosedWithoutAdminToken(t *testing.T) {
	c := newTestCore(&config.Config{Token: ""})
	mod := hosttransfermod.New()
	if err := mod.Init(c); err != nil {
		t.Fatalf("init: %v", err)
	}
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	outer := newOuterHandler(c, mux, nil)

	for path, body := range map[string]string{
		"/api/host-transfer":        `{"hosts":[{"ip":"10.0.0.1","token":"t"}]}`,
		"/api/host-transfer/redeem": `{"code":"ZZZZZZZZ"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"no_token"`) {
			t.Fatalf("%s without admin token: want 403 no_token, got %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

// TestOuterChain_HostTransferTokenSetAfterOuterAuth (PR #1394 critic,
// TOCTOU): the outer TokenAuth reads an empty admin token and lets the
// request through; the token is set before the module handler reads it. The
// handler re-authenticates against its own snapshot, so a request with no
// bearer is 401 — not 200 (credentials parked or handed out) nor 403.
//
// The outer chain reads c.Cfg.Token directly, so the window is reproduced by
// an inner handler that sets the token between the outer check and the
// module's mux (equivalent to a tokenFn that returns "" then "T").
func TestOuterChain_HostTransferTokenSetAfterOuterAuth(t *testing.T) {
	for path, body := range map[string]string{
		"/api/host-transfer":        `{"hosts":[{"ip":"10.0.0.1","token":"t"}]}`,
		"/api/host-transfer/redeem": `{"code":"ZZZZZZZZ"}`,
	} {
		t.Run(path, func(t *testing.T) {
			c := newTestCore(&config.Config{Token: ""})
			mod := hosttransfermod.New()
			if err := mod.Init(c); err != nil {
				t.Fatalf("init: %v", err)
			}
			mux := http.NewServeMux()
			mod.RegisterRoutes(mux)
			setTokenThenServe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c.CfgMu.Lock()
				c.Cfg.Token = "T"
				c.CfgMu.Unlock()
				mux.ServeHTTP(w, r)
			})
			outer := newOuterHandler(c, setTokenThenServe, nil)

			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			rec := httptest.NewRecorder()
			outer.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"unauthorized"`) {
				t.Fatalf("%s with the token set after outer auth: want 401 unauthorized, got %d %s", path, rec.Code, rec.Body.String())
			}
		})
	}
}
