package core

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"
)

// SetupSecretStore manages a single one-time setup secret for pairing.
// Only one active secret at a time; generating a new one clears the old.
type SetupSecretStore struct {
	mu     sync.Mutex
	secret string
	born   time.Time
	ttl    time.Duration
}

func NewSetupSecretStore(ttl time.Duration) *SetupSecretStore {
	return &SetupSecretStore{ttl: ttl}
}

// Generate creates a new setup secret, replacing any existing one.
func (ss *SetupSecretStore) Generate() (string, error) {
	raw := make([]byte, 16) // 128-bit
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(raw)

	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.secret = secret
	ss.born = time.Now()
	return secret, nil
}

// Validate checks and consumes the setup secret. Returns true if valid.
func (ss *SetupSecretStore) Validate(secret string) bool {
	if secret == "" {
		return false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.secret == "" || subtle.ConstantTimeCompare([]byte(ss.secret), []byte(secret)) != 1 {
		return false
	}
	if time.Since(ss.born) > ss.ttl {
		ss.secret = ""
		return false
	}
	ss.secret = "" // one-time use
	return true
}
