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

func TestPairVerifyBruteForceRegenerate(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)
	c.PairingSecret = "abcdef"
	c.Cfg.Bind = "100.64.0.2"
	c.Cfg.Port = 7860
	originalSecret := c.PairingSecret

	// Send maxVerifyFailures (10) wrong attempts
	for i := 0; i < 10; i++ {
		body, _ := json.Marshal(map[string]string{"secret": "ffffff"})
		req := httptest.NewRequest("POST", "/api/pair/verify", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		c.handlePairVerify(rec, req)
		if rec.Code != 401 {
			t.Fatalf("attempt %d: want 401, got %d", i+1, rec.Code)
		}
	}

	// PairingSecret should have been regenerated
	c.CfgMu.RLock()
	newSecret := c.PairingSecret
	c.CfgMu.RUnlock()
	if newSecret == originalSecret {
		t.Error("PairingSecret should have been regenerated after 10 failures")
	}
	if newSecret == "" {
		t.Error("PairingSecret should not be empty after regeneration")
	}
}

func TestPairSetupWriteFileFailure(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)
	c.CfgPath = "/nonexistent/deep/path/config.toml"
	originalToken := c.Cfg.Token

	secret, _ := c.SetupSecrets.Generate()
	body, _ := json.Marshal(map[string]string{
		"setupSecret": secret,
		"token":       "purdex_abcdef1234567890abcdef1234567890abcdef12",
	})
	req := httptest.NewRequest("POST", "/api/pair/setup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairSetup(rec, req)

	if rec.Code != 500 {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if c.Cfg.Token != originalToken {
		t.Errorf("cfg.Token should be unchanged after WriteFile failure, got %s", c.Cfg.Token)
	}
	if c.Pairing.Get() != StatePairing {
		t.Errorf("pairing state should still be StatePairing, got %s", c.Pairing.Get())
	}
}

func TestPairSetupNotPairingMode(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StateNormal)

	body, _ := json.Marshal(map[string]string{
		"setupSecret": "anything",
		"token":       "purdex_abcdef1234567890abcdef1234567890abcdef12",
	})
	req := httptest.NewRequest("POST", "/api/pair/setup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairSetup(rec, req)

	if rec.Code != 409 {
		t.Errorf("want 409, got %d", rec.Code)
	}
}
