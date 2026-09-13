// internal/middleware/peer_route_auth_test.go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// nextOK is a minimal handler used to verify next was actually invoked.
func nextOK() (http.Handler, *bool) {
	called := false
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})
	return h, &called
}

func TestPeerRouteAuthCorrectBearerCallsNext(t *testing.T) {
	next, called := nextOK()
	h := PeerRouteAuth("/api/peers", func() string { return "secret" })(next)
	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
	if !*called {
		t.Error("want next called")
	}
}

func TestPeerRouteAuthWrongBearer401(t *testing.T) {
	next, called := nextOK()
	h := PeerRouteAuth("/api/peers", func() string { return "secret" })(next)
	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Error("want next NOT called")
	}
}

func TestPeerRouteAuthMissingBearer401(t *testing.T) {
	next, called := nextOK()
	h := PeerRouteAuth("/api/peers", func() string { return "secret" })(next)
	req := httptest.NewRequest("GET", "/api/peers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Error("want next NOT called")
	}
}

func TestPeerRouteAuthTicketRejected401(t *testing.T) {
	next, called := nextOK()
	h := PeerRouteAuth("/api/peers", func() string { return "secret" })(next)
	req := httptest.NewRequest("GET", "/api/peers?ticket=x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Error("want next NOT called")
	}
}

func TestPeerRouteAuthEmptyAdminToken401(t *testing.T) {
	next, called := nextOK()
	h := PeerRouteAuth("/api/peers", func() string { return "" })(next)
	req := httptest.NewRequest("GET", "/api/peers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Error("want next NOT called")
	}
}

func TestPeerRouteAuthSubPathGuarded(t *testing.T) {
	next, called := nextOK()
	h := PeerRouteAuth("/api/peers", func() string { return "secret" })(next)
	req := httptest.NewRequest("GET", "/api/peers/hosts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Error("want next NOT called")
	}

	// with correct bearer, sub-path passes through to next
	next2, called2 := nextOK()
	h2 := PeerRouteAuth("/api/peers", func() string { return "secret" })(next2)
	req2 := httptest.NewRequest("GET", "/api/peers/hosts", nil)
	req2.Header.Set("Authorization", "Bearer secret")
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Errorf("want 200, got %d", rec2.Code)
	}
	if !*called2 {
		t.Error("want next called")
	}
}

func TestPeerRouteAuthNonPeerPrefixPassesThroughUnchecked(t *testing.T) {
	for _, path := range []string{"/api/peersx", "/api/sessions"} {
		next, called := nextOK()
		// No token function invocation should matter here — panic-on-call
		// would be ideal, but a token that would fail TokenAuth is enough
		// to prove PeerRouteAuth performs no check of its own.
		h := PeerRouteAuth("/api/peers", func() string { return "secret" })(next)
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("path %s: want 200, got %d", path, rec.Code)
		}
		if !*called {
			t.Errorf("path %s: want next called", path)
		}
	}
}

// fakeTicketValidator records whether Validate was called.
type fakeTicketValidator struct {
	called bool
}

func (f *fakeTicketValidator) Validate(ticket string) bool {
	f.called = true
	return true
}

// TestPeerRouteAuthComposition builds the exact chain used in main.go:
// PeerRouteAuth(...)(TokenAuth(tokenFn, fakeTickets)(next))
func TestPeerRouteAuthComposition(t *testing.T) {
	t.Run("peers with ticket is rejected and validator not consulted", func(t *testing.T) {
		tokenFn := func() string { return "secret" }
		tickets := &fakeTicketValidator{}
		next, called := nextOK()
		h := PeerRouteAuth("/api/peers", tokenFn)(TokenAuth(tokenFn, tickets)(next))

		req := httptest.NewRequest("GET", "/api/peers?ticket=ok", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != 401 {
			t.Errorf("want 401, got %d", rec.Code)
		}
		if tickets.called {
			t.Error("want ticket validator NOT called")
		}
		if *called {
			t.Error("want next NOT called")
		}
	})

	t.Run("sessions with ticket still works via TokenAuth", func(t *testing.T) {
		tokenFn := func() string { return "secret" }
		tickets := &fakeTicketValidator{}
		next, called := nextOK()
		h := PeerRouteAuth("/api/peers", tokenFn)(TokenAuth(tokenFn, tickets)(next))

		req := httptest.NewRequest("GET", "/api/sessions?ticket=ok", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != 200 {
			t.Errorf("want 200, got %d", rec.Code)
		}
		if !*called {
			t.Error("want next called")
		}
	})

	t.Run("empty admin token: sessions open, peers still guarded", func(t *testing.T) {
		tokenFn := func() string { return "" }
		tickets := &fakeTicketValidator{}

		sessionsNext, sessionsCalled := nextOK()
		hSessions := PeerRouteAuth("/api/peers", tokenFn)(TokenAuth(tokenFn, tickets)(sessionsNext))
		reqSessions := httptest.NewRequest("GET", "/api/sessions", nil)
		recSessions := httptest.NewRecorder()
		hSessions.ServeHTTP(recSessions, reqSessions)
		if recSessions.Code != 200 {
			t.Errorf("/api/sessions: want 200, got %d", recSessions.Code)
		}
		if !*sessionsCalled {
			t.Error("/api/sessions: want next called")
		}

		peersNext, peersCalled := nextOK()
		hPeers := PeerRouteAuth("/api/peers", tokenFn)(TokenAuth(tokenFn, tickets)(peersNext))
		reqPeers := httptest.NewRequest("GET", "/api/peers", nil)
		recPeers := httptest.NewRecorder()
		hPeers.ServeHTTP(recPeers, reqPeers)
		if recPeers.Code != 401 {
			t.Errorf("/api/peers: want 401, got %d", recPeers.Code)
		}
		if *peersCalled {
			t.Error("/api/peers: want next NOT called")
		}
	})
}
