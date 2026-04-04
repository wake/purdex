package core

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestTokenAuthConfirmSuccess(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePending)
	c.Cfg.Token = "purdex_test_token_that_is_long_enough_for_validation"
	c.CfgPath = t.TempDir() + "/config.toml"

	req := httptest.NewRequest("POST", "/api/token/auth", nil)
	rec := httptest.NewRecorder()
	c.handleTokenAuth(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if c.Pairing.Get() != StateNormal {
		t.Error("pairing state should be normal after confirm")
	}
}

func TestTokenAuthAlreadyConfirmed(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StateNormal)
	c.Cfg.Token = "purdex_existing_token_long_enough"

	req := httptest.NewRequest("POST", "/api/token/auth", nil)
	rec := httptest.NewRecorder()
	c.handleTokenAuth(rec, req)

	if rec.Code != 409 {
		t.Errorf("want 409, got %d", rec.Code)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["reason"] != "already_confirmed" {
		t.Errorf("want reason=already_confirmed, got %s", body["reason"])
	}
}
