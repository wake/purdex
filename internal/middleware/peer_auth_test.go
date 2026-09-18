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

// matcherFor is the matcher PeerAuth takes, over a fixed PeersConfig: the
// production one (cmd/pdx/http_chain.go) reads the live config under
// CfgMu.RLock and notes the match there; here there is nothing to lock or
// note.
func matcherFor(p config.PeersConfig) func(string) (config.PeerHost, bool, bool) {
	return func(bearer string) (config.PeerHost, bool, bool) {
		return p.MatchInboundToken(bearer)
	}
}

func noHosts() func(string) (config.PeerHost, bool, bool) {
	return matcherFor(config.PeersConfig{})
}

func twoHosts() func(string) (config.PeerHost, bool, bool) {
	return matcherFor(config.PeersConfig{
		Hosts: []config.PeerHost{
			{Alias: "alpha", HostID: "host-alpha", InboundToken: "tok-alpha"},
			{Alias: "beta", HostID: "host-beta", InboundToken: "tok-beta"},
			{Alias: "no-token", HostID: "host-none", InboundToken: ""},
		},
	})
}

func TestPeerAuthAdminBearerCallsNextWithAdminPrincipal(t *testing.T) {
	next, called, seen := nextRecordingPrincipal()
	adminFn := func() string { return "admin-secret" }
	peersFn := noHosts()
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
	peersFn := noHosts()
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
	peersFn := noHosts()
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
	peersFn := twoHosts()
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
	peersFn := twoHosts()
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
	peersFn := twoHosts()
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
	peersFn := twoHosts()
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
	peersFn := twoHosts()
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
	peersFn := twoHosts()
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
	peersFn := noHosts()
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
	peersFn := twoHosts() // includes a host with InboundToken == ""
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
	peersFn := matcherFor(config.PeersConfig{Hosts: []config.PeerHost{
		{Alias: "beta", HostID: "host-beta", InboundToken: "tok-new", InboundTokenPrev: "tok-old"},
	}})
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

// TestPeerAuthMatcherObservesEverySuccessfulHostMatch: PeerAuth delegates
// the match to the matcher it is given and touches no config itself, so
// whatever the matcher records inside its own critical section
// (production: the peers module's rotation note, under CfgMu.RLock) is
// recorded exactly once per successful host match — including a match the
// route policy then refuses with 403 (the bearer DID authenticate; the
// refusal is policy, not authentication) — and never for an admin bearer
// or for a bearer that matches nothing (401).
func TestPeerAuthMatcherObservesEverySuccessfulHostMatch(t *testing.T) {
	type obs struct{ alias, fp string }
	var seen []obs
	inner := twoHosts()
	matcher := func(bearer string) (config.PeerHost, bool, bool) {
		h, usedPrev, ok := inner(bearer)
		if ok {
			seen = append(seen, obs{h.Alias, config.TokenFingerprint(bearer)})
		}
		return h, usedPrev, ok
	}
	adminFn := func() string { return "admin-secret" }

	serve := func(bearer string, allowed func(*http.Request) bool) int {
		next, _, _ := nextRecordingPrincipal()
		h := middleware.PeerAuth(adminFn, matcher, allowed)(next)
		req := httptest.NewRequest("GET", "/api/peers", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := serve("tok-alpha", allowAll); code != 200 {
		t.Fatalf("host alpha: want 200, got %d", code)
	}
	if len(seen) != 1 || seen[0] != (obs{"alpha", config.TokenFingerprint("tok-alpha")}) {
		t.Fatalf("after one host match: observations = %+v, want exactly [{alpha fp(tok-alpha)}]", seen)
	}
	if code := serve("admin-secret", allowAll); code != 200 {
		t.Fatalf("admin: want 200, got %d", code)
	}
	if len(seen) != 1 {
		t.Fatalf("admin bearer was observed as a host match: %+v", seen)
	}
	if code := serve("nope", allowAll); code != 401 {
		t.Fatalf("wrong bearer: want 401, got %d", code)
	}
	if code := serve("", allowAll); code != 401 {
		t.Fatalf("missing bearer: want 401, got %d", code)
	}
	if len(seen) != 1 {
		t.Fatalf("a 401 was observed: %+v", seen)
	}
	// 403: the match happened, so it is observed; the policy refusal comes
	// after authentication.
	if code := serve("tok-beta", denyAll); code != 403 {
		t.Fatalf("host beta on refused route: want 403, got %d", code)
	}
	if len(seen) != 2 || seen[1] != (obs{"beta", config.TokenFingerprint("tok-beta")}) {
		t.Fatalf("after a 403 host match: observations = %+v, want [{alpha …} {beta fp(tok-beta)}]", seen)
	}
}
