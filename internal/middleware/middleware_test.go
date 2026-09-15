// internal/middleware/middleware_test.go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/middleware"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

func TestIPWhitelistAllowed(t *testing.T) {
	h := middleware.IPWhitelist([]string{"192.168.1.0/24"})(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.50:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestIPWhitelistDenied(t *testing.T) {
	h := middleware.IPWhitelist([]string{"192.168.1.0/24"})(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("want 403, got %d", rec.Code)
	}
}

func TestIPWhitelistEmptyAllowsAll(t *testing.T) {
	h := middleware.IPWhitelist(nil)(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestTokenAuthValid(t *testing.T) {
	h := middleware.TokenAuth(func() string { return "secret" }, nil)(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestTokenAuthInvalid(t *testing.T) {
	h := middleware.TokenAuth(func() string { return "secret" }, nil)(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestTokenAuthCaseSensitive(t *testing.T) {
	h := middleware.TokenAuth(func() string { return "Secret" }, nil)(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401 (case mismatch), got %d", rec.Code)
	}
}

func TestTokenAuthQueryParamRemoved(t *testing.T) {
	h := middleware.TokenAuth(func() string { return "secret" }, nil)(ok)
	req := httptest.NewRequest("GET", "/?token=secret", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401 for removed query param token, got %d", rec.Code)
	}
}

func TestTokenAuthEmptyAllowsAll(t *testing.T) {
	h := middleware.TokenAuth(func() string { return "" }, nil)(ok)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

// fakeTickets implements TicketValidator for testing.
type fakeTickets struct {
	valid map[string]bool
}

func (f *fakeTickets) Validate(ticket string) bool {
	if v, ok := f.valid[ticket]; ok {
		delete(f.valid, ticket) // one-time
		return v
	}
	return false
}

// wsUpgradeRequest builds a real WebSocket handshake: a GET carrying
// Connection: Upgrade, Upgrade: websocket and Sec-WebSocket-Version: 13 —
// the only shape TokenAuth accepts a one-time ?ticket= on.
func wsUpgradeRequest(target string) *http.Request {
	req := httptest.NewRequest("GET", target, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	return req
}

func TestTokenAuthTicketValidOnWebSocketUpgrade(t *testing.T) {
	tv := &fakeTickets{valid: map[string]bool{"abc123": true}}
	h := middleware.TokenAuth(func() string { return "secret" }, tv)(ok)
	req := wsUpgradeRequest("/?ticket=abc123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200 for valid ticket on a WebSocket upgrade, got %d", rec.Code)
	}
}

func TestTokenAuthTicketInvalid(t *testing.T) {
	tv := &fakeTickets{valid: map[string]bool{}}
	h := middleware.TokenAuth(func() string { return "secret" }, tv)(ok)
	req := wsUpgradeRequest("/?ticket=wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401 for invalid ticket, got %d", rec.Code)
	}
}

func TestTokenAuthTicketConsumedOnWebSocketUpgrade(t *testing.T) {
	tv := &fakeTickets{valid: map[string]bool{"once": true}}
	h := middleware.TokenAuth(func() string { return "secret" }, tv)(ok)

	// First upgrade should succeed
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, wsUpgradeRequest("/?ticket=once"))
	if rec1.Code != 200 {
		t.Errorf("first upgrade: want 200, got %d", rec1.Code)
	}

	// Second upgrade with same ticket should fail
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, wsUpgradeRequest("/?ticket=once"))
	if rec2.Code != 401 {
		t.Errorf("second upgrade: want 401 (consumed), got %d", rec2.Code)
	}
}

// TestTokenAuthTicketRejectedOnPlainRequests: a one-time WS ticket is a
// WebSocket credential only. On a plain (non-upgrade) request it must be
// ignored — and not consumed — so it cannot drive REST mutations (e.g.
// under /api/nex/) that are meant to require the bearer token.
func TestTokenAuthTicketRejectedOnPlainRequests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		header map[string]string
	}{
		{name: "plain GET", method: "GET"},
		{name: "POST", method: "POST"},
		{name: "GET with Upgrade header but no Connection: Upgrade", method: "GET", header: map[string]string{"Upgrade": "websocket"}},
		{name: "GET with Connection: Upgrade but no Upgrade: websocket", method: "GET", header: map[string]string{"Connection": "Upgrade"}},
		{name: "GET with SSE Accept", method: "GET", header: map[string]string{"Accept": "text/event-stream"}},
		// A REST call wearing a costume: upgrade headers on a non-GET.
		{name: "POST with full handshake headers", method: "POST", header: map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13"}},
		{name: "DELETE with full handshake headers", method: "DELETE", header: map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13"}},
		// Upgrade headers but no Sec-WebSocket-Version: not a handshake.
		{name: "GET with upgrade headers but no Sec-WebSocket-Version", method: "GET", header: map[string]string{"Connection": "Upgrade", "Upgrade": "websocket"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tv := &fakeTickets{valid: map[string]bool{"fresh": true}}
			h := middleware.TokenAuth(func() string { return "secret" }, tv)(ok)
			req := httptest.NewRequest(tc.method, "/x?ticket=fresh", nil)
			for k, v := range tc.header {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 401 {
				t.Fatalf("want 401 for a ticket on a non-upgrade request, got %d", rec.Code)
			}
			if !tv.valid["fresh"] {
				t.Fatal("ticket was consumed by a non-upgrade request; it must not even be consulted")
			}
		})
	}
}

// TestTokenAuthBearerStillWorksOnWebSocketUpgrade: the bearer path is
// unchanged by the ticket rule — a bearer on an upgrade request passes.
func TestTokenAuthBearerStillWorksOnWebSocketUpgrade(t *testing.T) {
	h := middleware.TokenAuth(func() string { return "secret" }, &fakeTickets{valid: map[string]bool{}})(ok)
	req := wsUpgradeRequest("/ws")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200 for bearer on a WebSocket upgrade, got %d", rec.Code)
	}
}

func TestCORSHeaders(t *testing.T) {
	h := middleware.CORS(ok)
	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("want CORS Allow-Origin *")
	}
	if rec.Code != 204 {
		t.Errorf("want 204 for OPTIONS, got %d", rec.Code)
	}
	methods := rec.Header().Get("Access-Control-Allow-Methods")
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		if !strings.Contains(methods, m) {
			t.Errorf("want %s in Allow-Methods, got %s", m, methods)
		}
	}
}

func TestCORSAllowsLastEventID(t *testing.T) {
	h := middleware.CORS(ok)
	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Headers", "last-event-id, authorization")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Errorf("want 204 for OPTIONS, got %d", rec.Code)
	}
	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(allowHeaders, "Last-Event-ID") {
		t.Errorf("want Last-Event-ID in Access-Control-Allow-Headers, got %s", allowHeaders)
	}
}

// TestCORSAllowsXPdxClient: the optional per-client principal suffix
// header (spec §4.3) must survive a browser preflight, so it is in the
// Allow-Headers list alongside Authorization and Last-Event-ID.
func TestCORSAllowsXPdxClient(t *testing.T) {
	h := middleware.CORS(ok)
	req := httptest.NewRequest("OPTIONS", "/api/nex/v1/executions", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization, last-event-id, x-pdx-client")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Errorf("want 204 for OPTIONS, got %d", rec.Code)
	}
	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	for _, want := range []string{"Authorization", "Content-Type", "Last-Event-ID", "X-Pdx-Client"} {
		if !strings.Contains(allowHeaders, want) {
			t.Errorf("want %s in Access-Control-Allow-Headers, got %s", want, allowHeaders)
		}
	}
}

func TestCORSPassesThroughGET(t *testing.T) {
	h := middleware.CORS(ok)
	req := httptest.NewRequest("GET", "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200 for GET to pass through, got %d", rec.Code)
	}
}
