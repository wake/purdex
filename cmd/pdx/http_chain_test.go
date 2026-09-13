// cmd/pdx/http_chain_test.go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
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
	// second use on the general chain would now fail.
	rec.reset()
	res = doRequest(t, outer, "GET", "/api/sessions?ticket="+ticket, "")
	if res.Code != 200 {
		t.Fatalf("/api/sessions?ticket=...: want 200 (ticket still valid), got %d", res.Code)
	}
	if !rec.called {
		t.Fatal("/api/sessions?ticket=...: want inner mux called")
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
