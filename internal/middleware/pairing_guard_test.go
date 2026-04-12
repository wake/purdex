// internal/middleware/pairing_guard_test.go
package middleware_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/wake/purdex/internal/middleware"
)

func TestPairingGuardBlocksInPairingMode(t *testing.T) {
	isPairing := func() bool { return true }
	h := middleware.PairingGuard(isPairing)(ok)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Errorf("want 503, got %d", rec.Code)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["reason"] != "pairing_mode" {
		t.Errorf("want reason=pairing_mode, got %s", body["reason"])
	}
}

func TestPairingGuardAllowsPairEndpoints(t *testing.T) {
	isPairing := func() bool { return true }
	h := middleware.PairingGuard(isPairing)(ok)

	for _, path := range []string{"/api/pair/verify", "/api/pair/setup"} {
		req := httptest.NewRequest("POST", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("%s: want 200, got %d", path, rec.Code)
		}
	}
}

func TestPairingGuardPassThroughWhenNotPairing(t *testing.T) {
	isPairing := func() bool { return false }
	h := middleware.PairingGuard(isPairing)(ok)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200 pass-through, got %d", rec.Code)
	}
}
