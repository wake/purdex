package core

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/wake/tmux-box/internal/config"
)

const (
	maxVerifyFailures = 10
	minTokenLength    = 20
)

func (c *Core) handlePairVerify(w http.ResponseWriter, r *http.Request) {
	if c.Pairing.Get() != StatePairing {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"reason": "already_paired"})
		return
	}

	var req struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Validate hex format
	expected, err := hex.DecodeString(c.PairingSecret)
	if err != nil || len(expected) == 0 {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	given, decErr := hex.DecodeString(req.Secret)

	// Constant-time compare; treat invalid hex as wrong secret (401, not 400)
	// to avoid leaking format hints to potential attackers.
	if decErr != nil || subtle.ConstantTimeCompare(expected, given) != 1 {
		count := atomic.AddInt32(&c.failedVerify, 1)
		if count >= maxVerifyFailures {
			c.regeneratePairingSecret()
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Generate setup secret
	setupSecret, err := c.SetupSecrets.Generate()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"setupSecret": setupSecret})
}

func (c *Core) handlePairSetup(w http.ResponseWriter, r *http.Request) {
	if c.Pairing.Get() != StatePairing {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"reason": "already_paired"})
		return
	}

	var req struct {
		SetupSecret string `json:"setupSecret"`
		Token       string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Validate setupSecret
	if !c.SetupSecrets.Validate(req.SetupSecret) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Validate token format
	if len(req.Token) < minTokenLength {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": "token too short (min 20 chars)"})
		return
	}

	// Persist token
	c.CfgMu.Lock()
	c.Cfg.Token = req.Token
	cfgCopy := *c.Cfg
	c.CfgMu.Unlock()

	if c.CfgPath != "" {
		if err := config.WriteFile(c.CfgPath, cfgCopy); err != nil {
			log.Printf("pair setup: write config: %v", err)
			http.Error(w, "failed to persist config", http.StatusInternalServerError)
			return
		}
	}

	c.Pairing.Set(StateNormal)
	log.Println("pairing complete — token set, switching to normal mode")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// regeneratePairingSecret creates a new 3-byte secret and logs a warning.
func (c *Core) regeneratePairingSecret() {
	secret := make([]byte, 3)
	if _, err := rand.Read(secret); err != nil {
		log.Printf("regenerate pairing secret: %v", err)
		return
	}
	c.PairingSecret = hex.EncodeToString(secret)
	atomic.StoreInt32(&c.failedVerify, 0)

	// Reconstruct full pairing code from cfg bind IP/Port + new secret
	c.CfgMu.RLock()
	ip := net.ParseIP(c.Cfg.Bind).To4()
	port := uint16(c.Cfg.Port)
	c.CfgMu.RUnlock()
	code := EncodePairingCode(ip, port, secret)
	log.Printf("⚠ 配對碼已失效（過多失敗嘗試），新配對碼：%s", code)
	fmt.Printf("\n新配對碼: %s\n\n", code)
}
