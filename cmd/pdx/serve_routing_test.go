// cmd/pdx/serve_routing_test.go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func stubHandler(marker string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(marker))
	})
}

// buildTestHandler wires a minimal inner mux + SPA/health stubs through the
// real buildHTTPHandler with the given auth/whitelist/pairing knobs.
func buildTestHandler(tokenFn func() string, allow []string, isPairing func() bool) http.Handler {
	inner := http.NewServeMux()
	inner.Handle("GET /api/info", stubHandler("API_INFO"))
	inner.Handle("/ws/host-events", stubHandler("WS"))
	spa := stubHandler("SPA")
	health := stubHandler("HEALTH")
	return buildHTTPHandler(inner, spa, allow, isPairing, tokenFn, nil, health)
}

func req(t *testing.T, h http.Handler, method, target, auth, remote string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	if remote != "" {
		r.RemoteAddr = remote
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func tokenOn() string  { return "SEKRIT" }
func tokenOff() string { return "" }
func noPairing() bool  { return false }

func TestRouting_HealthBypassesAuth(t *testing.T) {
	h := buildTestHandler(tokenOn, nil, noPairing)
	rec := req(t, h, "GET", "/api/health", "", "")
	if rec.Body.String() != "HEALTH" {
		t.Fatalf("health: got %q", rec.Body.String())
	}
}

func TestRouting_StaticServedWithoutToken(t *testing.T) {
	h := buildTestHandler(tokenOn, nil, noPairing) // token ON
	rec := req(t, h, "GET", "/", "", "")
	if rec.Body.String() != "SPA" {
		t.Fatalf("static root: got %q (code %d)", rec.Body.String(), rec.Code)
	}
	rec2 := req(t, h, "GET", "/assets/app.js", "", "")
	if rec2.Body.String() != "SPA" {
		t.Fatalf("static asset: got %q", rec2.Body.String())
	}
}

func TestRouting_ApiRequiresAuthWhenTokenSet(t *testing.T) {
	h := buildTestHandler(tokenOn, nil, noPairing)
	rec := req(t, h, "GET", "/api/info", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("api no-auth: got %d, want 401", rec.Code)
	}
	rec2 := req(t, h, "GET", "/api/info", "Bearer SEKRIT", "")
	if rec2.Body.String() != "API_INFO" {
		t.Fatalf("api with-auth: got %q (code %d)", rec2.Body.String(), rec2.Code)
	}
}

func TestRouting_WsNotEatenByStaticFallback(t *testing.T) {
	h := buildTestHandler(tokenOff, nil, noPairing)
	rec := req(t, h, "GET", "/ws/host-events", "", "")
	if rec.Body.String() != "WS" {
		t.Fatalf("ws routing: got %q — static fallback ate the WS route", rec.Body.String())
	}
}

func TestRouting_StaticStillSubjectToIPWhitelist(t *testing.T) {
	h := buildTestHandler(tokenOff, []string{"127.0.0.1"}, noPairing)
	// Disallowed source (httptest default RemoteAddr 192.0.2.1) → static blocked.
	rec := req(t, h, "GET", "/", "", "192.0.2.1:1234")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("static from disallowed IP: got %d, want 403", rec.Code)
	}
	// Allowed source → static served.
	rec2 := req(t, h, "GET", "/", "", "127.0.0.1:1234")
	if rec2.Body.String() != "SPA" {
		t.Fatalf("static from allowed IP: got %q (code %d)", rec2.Body.String(), rec2.Code)
	}
}

func TestRouting_PairingModeBlocksApiButNotStatic(t *testing.T) {
	inPairing := func() bool { return true }
	h := buildTestHandler(tokenOff, nil, inPairing)
	// Static shell must load during pairing (bypasses PairingGuard).
	recStatic := req(t, h, "GET", "/", "", "")
	if recStatic.Body.String() != "SPA" {
		t.Fatalf("pairing static: got %q (code %d)", recStatic.Body.String(), recStatic.Code)
	}
	// A non-pairing API route is blocked with 503 while pairing.
	recApi := req(t, h, "GET", "/api/info", "", "")
	if recApi.Code != http.StatusServiceUnavailable {
		t.Fatalf("pairing /api/info: got %d, want 503", recApi.Code)
	}
}

func TestRouting_BareApiPrefixRedirects(t *testing.T) {
	h := buildTestHandler(tokenOff, nil, noPairing)
	// Go 1.22+ ServeMux redirects the bare prefix "/api" → "/api/". Lock this
	// documented behavior change (no client depends on bare /api or /ws).
	// Verified against net/http server.go (Go 1.26): pattern-based ServeMux
	// trailing-slash redirects use StatusTemporaryRedirect (307), not 301.
	rec := req(t, h, "GET", "/api", "", "")
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("/api redirect: got %d, want 307", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/" {
		t.Fatalf("/api redirect Location: got %q, want /api/", loc)
	}
}

func TestRouting_OptionsPreflightHandledByCORS(t *testing.T) {
	h := buildTestHandler(tokenOn, nil, noPairing)
	// CORS middleware answers OPTIONS with 204 before reaching the SPA handler.
	rec := req(t, h, "OPTIONS", "/", "", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS preflight: got %d, want 204", rec.Code)
	}
}
