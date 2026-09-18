// internal/middleware/peer_auth_test.go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
)

// nextRecordingPrincipal returns a handler that records whether it was
// called and the Principal it observed in the request context (via
// middleware.PrincipalFrom), so tests can assert both.
func nextRecordingPrincipal() (http.Handler, *bool, *middleware.Principal) {
	called := false
	var seen middleware.Principal
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if p, ok := middleware.PrincipalFrom(r.Context()); ok {
			seen = p
		}
		w.WriteHeader(200)
	})
	return h, &called, &seen
}

func allowAll(*http.Request) bool { return true }
func denyAll(*http.Request) bool  { return false }

func twoHosts() config.PeersConfig {
	return config.PeersConfig{
		Hosts: []config.PeerHost{
			{Alias: "alpha", HostID: "host-alpha", InboundToken: "tok-alpha"},
			{Alias: "beta", HostID: "host-beta", InboundToken: "tok-beta"},
			{Alias: "no-token", HostID: "host-none", InboundToken: ""},
		},
	}
}

func TestPeerAuthAdminBearerCallsNextWithAdminPrincipal(t *testing.T) {
	next, called, seen := nextRecordingPrincipal()
	adminFn := func() string { return "admin-secret" }
	peersFn := func() config.PeersConfig { return config.PeersConfig{} }
	h := middleware.PeerAuth(adminFn, peersFn, allowAll)(next)

	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !*called {
		t.Fatal("want next called")
	}
	if seen.Kind != middleware.PrincipalAdmin {
		t.Fatalf("want admin principal, got %+v", *seen)
	}
}

func TestPeerAuthEmptyAdminTokenDisablesAdminAuth(t *testing.T) {
	next, called, _ := nextRecordingPrincipal()
	adminFn := func() string { return "" }
	peersFn := func() config.PeersConfig { return config.PeersConfig{} }
	h := middleware.PeerAuth(adminFn, peersFn, allowAll)(next)

	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Fatal("want next NOT called")
	}
}

func TestPeerAuthEmptyAdminTokenAndAdminBearerStill401(t *testing.T) {
	// Presenting the literal empty string as a bearer when the admin token
	// is also empty must not be treated as a match.
	next, called, _ := nextRecordingPrincipal()
	adminFn := func() string { return "" }
	peersFn := func() config.PeersConfig { return config.PeersConfig{} }
	h := middleware.PeerAuth(adminFn, peersFn, allowAll)(next)

	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Fatal("want next NOT called")
	}
}

func TestPeerAuthHostBearerCallsNextWithHostPrincipal(t *testing.T) {
	next, called, seen := nextRecordingPrincipal()
	adminFn := func() string { return "admin-secret" }
	peersFn := twoHosts
	h := middleware.PeerAuth(adminFn, peersFn, allowAll)(next)

	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer tok-beta")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !*called {
		t.Fatal("want next called")
	}
	if seen.Kind != middleware.PrincipalHost || seen.Alias != "beta" || seen.HostID != "host-beta" {
		t.Fatalf("want host principal beta/host-beta, got %+v", *seen)
	}
}

func TestPeerAuthTwoHostsRightOneMatched(t *testing.T) {
	next, called, seen := nextRecordingPrincipal()
	adminFn := func() string { return "admin-secret" }
	peersFn := twoHosts
	h := middleware.PeerAuth(adminFn, peersFn, allowAll)(next)

	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer tok-alpha")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !*called {
		t.Fatal("want next called")
	}
	if seen.Alias != "alpha" || seen.HostID != "host-alpha" {
		t.Fatalf("want alpha/host-alpha, got %+v", *seen)
	}
}

func TestPeerAuthHostBearerOnDisallowedRequest403(t *testing.T) {
	next, called, _ := nextRecordingPrincipal()
	adminFn := func() string { return "admin-secret" }
	peersFn := twoHosts
	h := middleware.PeerAuth(adminFn, peersFn, denyAll)(next)

	req := httptest.NewRequest("GET", "/api/peers/hosts", nil)
	req.Header.Set("Authorization", "Bearer tok-alpha")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	if *called {
		t.Fatal("want next NOT called")
	}
}

func TestPeerAuthWrongBearer401(t *testing.T) {
	next, called, _ := nextRecordingPrincipal()
	adminFn := func() string { return "admin-secret" }
	peersFn := twoHosts
	h := middleware.PeerAuth(adminFn, peersFn, allowAll)(next)

	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer nope")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Fatal("want next NOT called")
	}
}

func TestPeerAuthMissingBearer401(t *testing.T) {
	next, called, _ := nextRecordingPrincipal()
	adminFn := func() string { return "admin-secret" }
	peersFn := twoHosts
	h := middleware.PeerAuth(adminFn, peersFn, allowAll)(next)

	req := httptest.NewRequest("GET", "/api/peers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Fatal("want next NOT called")
	}
}

func TestPeerAuthTicketNeverConsulted401(t *testing.T) {
	next, called, _ := nextRecordingPrincipal()
	adminFn := func() string { return "admin-secret" }
	peersFn := twoHosts
	h := middleware.PeerAuth(adminFn, peersFn, allowAll)(next)

	req := httptest.NewRequest("GET", "/api/peers?ticket=x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Fatal("want next NOT called")
	}
}

func TestPeerAuthBearerCaseInsensitivePrefix(t *testing.T) {
	next, called, seen := nextRecordingPrincipal()
	adminFn := func() string { return "admin-secret" }
	peersFn := func() config.PeersConfig { return config.PeersConfig{} }
	h := middleware.PeerAuth(adminFn, peersFn, allowAll)(next)

	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "bearer admin-secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !*called {
		t.Fatal("want next called")
	}
	if seen.Kind != middleware.PrincipalAdmin {
		t.Fatalf("want admin principal, got %+v", *seen)
	}
}

func TestPeerAuthHostWithEmptyInboundTokenNeverMatchesEmptyBearer(t *testing.T) {
	next, called, _ := nextRecordingPrincipal()
	adminFn := func() string { return "admin-secret" }
	peersFn := twoHosts // includes a host with InboundToken == ""
	h := middleware.PeerAuth(adminFn, peersFn, allowAll)(next)

	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if *called {
		t.Fatal("want next NOT called")
	}
}

// TestPeerAuthPrevTokenSetsUsedPrevToken: during a rotation the old token
// still authenticates as the same host, and the principal says so.
func TestPeerAuthPrevTokenSetsUsedPrevToken(t *testing.T) {
	peersFn := func() config.PeersConfig {
		return config.PeersConfig{Hosts: []config.PeerHost{
			{Alias: "beta", HostID: "host-beta", InboundToken: "tok-new", InboundTokenPrev: "tok-old"},
		}}
	}
	for _, tc := range []struct {
		bearer   string
		wantPrev bool
	}{{"tok-new", false}, {"tok-old", true}} {
		next, called, seen := nextRecordingPrincipal()
		h := middleware.PeerAuth(func() string { return "admin-secret" }, peersFn, allowAll)(next)
		req := httptest.NewRequest("GET", "/api/peers", nil)
		req.Header.Set("Authorization", "Bearer "+tc.bearer)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 || !*called {
			t.Fatalf("%s: want 200 and next called, got %d called=%v", tc.bearer, rec.Code, *called)
		}
		if seen.Kind != middleware.PrincipalHost || seen.Alias != "beta" || seen.UsedPrevToken != tc.wantPrev {
			t.Fatalf("%s: principal = %+v, want host beta UsedPrevToken=%v", tc.bearer, *seen, tc.wantPrev)
		}
		// The principal also carries WHICH token, by non-reversible
		// fingerprint, so the peers module can bind its rotation record
		// to the token rather than to the moment it was noted.
		if want := config.TokenFingerprint(tc.bearer); seen.TokenFingerprint != want {
			t.Fatalf("%s: TokenFingerprint = %q, want %q", tc.bearer, seen.TokenFingerprint, want)
		}
	}
	// An admin principal is not a peer dial and carries no fingerprint.
	next, called, seen := nextRecordingPrincipal()
	h := middleware.PeerAuth(func() string { return "admin-secret" }, peersFn, allowAll)(next)
	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !*called || seen.Kind != middleware.PrincipalAdmin || seen.TokenFingerprint != "" {
		t.Fatalf("admin: code=%d called=%v principal=%+v; want admin with empty TokenFingerprint", rec.Code, *called, *seen)
	}
}
