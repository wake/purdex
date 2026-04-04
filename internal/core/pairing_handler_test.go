package core

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestPairVerifySuccess(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)
	c.PairingSecret = "abcdef"

	body, _ := json.Marshal(map[string]string{"secret": "abcdef"})
	req := httptest.NewRequest("POST", "/api/pair/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairVerify(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp["setupSecret"]) != 32 {
		t.Errorf("want 32-char setupSecret, got %d", len(resp["setupSecret"]))
	}
}

func TestPairVerifyWrongSecret(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)
	c.PairingSecret = "abcdef"

	body, _ := json.Marshal(map[string]string{"secret": "wrong1"})
	req := httptest.NewRequest("POST", "/api/pair/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairVerify(rec, req)

	if rec.Code != 401 {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestPairVerifyNotPairingMode(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StateNormal)

	body, _ := json.Marshal(map[string]string{"secret": "abcdef"})
	req := httptest.NewRequest("POST", "/api/pair/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairVerify(rec, req)

	if rec.Code != 409 {
		t.Errorf("want 409, got %d", rec.Code)
	}
}

func TestPairSetupSuccess(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)
	c.CfgPath = t.TempDir() + "/config.toml"

	// Generate a valid setupSecret
	secret, _ := c.SetupSecrets.Generate()

	body, _ := json.Marshal(map[string]string{
		"setupSecret": secret,
		"token":       "purdex_abcdef1234567890abcdef1234567890abcdef12",
	})
	req := httptest.NewRequest("POST", "/api/pair/setup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairSetup(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if c.Pairing.Get() != StateNormal {
		t.Error("pairing state should be normal after setup")
	}
	if c.Cfg.Token == "" {
		t.Error("cfg.Token should be set after setup")
	}
}

func TestPairSetupShortToken(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)
	secret, _ := c.SetupSecrets.Generate()

	body, _ := json.Marshal(map[string]string{
		"setupSecret": secret,
		"token":       "short",
	})
	req := httptest.NewRequest("POST", "/api/pair/setup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairSetup(rec, req)

	if rec.Code != 422 {
		t.Errorf("want 422 for short token, got %d", rec.Code)
	}
}

func TestPairSetupInvalidSecret(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)

	body, _ := json.Marshal(map[string]string{
		"setupSecret": "invalid",
		"token":       "purdex_abcdef1234567890abcdef1234567890abcdef12",
	})
	req := httptest.NewRequest("POST", "/api/pair/setup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairSetup(rec, req)

	if rec.Code != 401 {
		t.Errorf("want 401 for invalid setupSecret, got %d", rec.Code)
	}
}
