// internal/middleware/middleware_test.go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/tmux-box/internal/middleware"
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

func TestTokenAuthQueryParam(t *testing.T) {
	h := middleware.TokenAuth(func() string { return "secret" }, nil)(ok)
	req := httptest.NewRequest("GET", "/?token=secret", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200 for valid query param token, got %d", rec.Code)
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

func TestTokenAuthTicketValid(t *testing.T) {
	tv := &fakeTickets{valid: map[string]bool{"abc123": true}}
	h := middleware.TokenAuth(func() string { return "secret" }, tv)(ok)
	req := httptest.NewRequest("GET", "/?ticket=abc123", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200 for valid ticket, got %d", rec.Code)
	}
}

func TestTokenAuthTicketInvalid(t *testing.T) {
	tv := &fakeTickets{valid: map[string]bool{}}
	h := middleware.TokenAuth(func() string { return "secret" }, tv)(ok)
	req := httptest.NewRequest("GET", "/?ticket=wrong", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401 for invalid ticket, got %d", rec.Code)
	}
}

func TestTokenAuthTicketConsumed(t *testing.T) {
	tv := &fakeTickets{valid: map[string]bool{"once": true}}
	h := middleware.TokenAuth(func() string { return "secret" }, tv)(ok)

	// First request should succeed
	req1 := httptest.NewRequest("GET", "/?ticket=once", nil)
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != 200 {
		t.Errorf("first request: want 200, got %d", rec1.Code)
	}

	// Second request with same ticket should fail
	req2 := httptest.NewRequest("GET", "/?ticket=once", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 401 {
		t.Errorf("second request: want 401 (consumed), got %d", rec2.Code)
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
}
