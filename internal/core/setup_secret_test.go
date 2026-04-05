package core

import (
	"testing"
	"time"
)

func TestSetupSecretGenerate(t *testing.T) {
	ss := NewSetupSecretStore(5 * time.Minute)
	secret, err := ss.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 {
		t.Errorf("want 32-char hex, got %d chars", len(secret))
	}
}

func TestSetupSecretValidateSuccess(t *testing.T) {
	ss := NewSetupSecretStore(5 * time.Minute)
	secret, _ := ss.Generate()
	if !ss.Validate(secret) {
		t.Error("expected valid secret to pass")
	}
}

func TestSetupSecretOneTimeUse(t *testing.T) {
	ss := NewSetupSecretStore(5 * time.Minute)
	secret, _ := ss.Generate()
	ss.Validate(secret) // consume
	if ss.Validate(secret) {
		t.Error("expected consumed secret to fail")
	}
}

func TestSetupSecretExpired(t *testing.T) {
	ss := NewSetupSecretStore(1 * time.Millisecond)
	secret, _ := ss.Generate()
	time.Sleep(5 * time.Millisecond)
	if ss.Validate(secret) {
		t.Error("expected expired secret to fail")
	}
}

func TestSetupSecretNewGenerateClearsOld(t *testing.T) {
	ss := NewSetupSecretStore(5 * time.Minute)
	old, _ := ss.Generate()
	_, _ = ss.Generate() // should clear old
	if ss.Validate(old) {
		t.Error("expected old secret to be cleared after new generate")
	}
}

func TestSetupSecretEmpty(t *testing.T) {
	ss := NewSetupSecretStore(5 * time.Minute)
	if ss.Validate("") {
		t.Error("empty string should not validate")
	}
}
