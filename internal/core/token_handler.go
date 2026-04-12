package core

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/wake/purdex/internal/config"
)

// handleTokenAuth confirms the runtime token and persists it to config.
// NOTE: Token validation is performed by TokenAuth middleware in the chain.
// This handler MUST be behind TokenAuth — moving it to an unprotected route
// would allow unauthenticated callers to persist arbitrary tokens.
func (c *Core) handleTokenAuth(w http.ResponseWriter, r *http.Request) {
	// Already confirmed — idempotent success
	if c.Pairing.Get() == StateNormal {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"reason": "already_confirmed"})
		return
	}

	// Persist token (already validated by TokenAuth middleware)
	c.CfgMu.RLock()
	cfgCopy := *c.Cfg
	c.CfgMu.RUnlock()

	if c.CfgPath != "" {
		if err := config.WriteFile(c.CfgPath, cfgCopy); err != nil {
			log.Printf("token auth: write config: %v", err)
			http.Error(w, "failed to persist config", http.StatusInternalServerError)
			return
		}
	}

	c.Pairing.Set(StateNormal)
	log.Println("token confirmed — switching to normal mode")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
