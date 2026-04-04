// internal/middleware/pairing_guard.go
package middleware

import (
	"encoding/json"
	"net/http"
	"strings"
)

// PairingGuard blocks non-pairing requests when isPairing returns true.
// Only /api/pair/* paths and OPTIONS requests are allowed through.
func PairingGuard(isPairing func() bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isPairing() {
				next.ServeHTTP(w, r)
				return
			}
			// Always allow OPTIONS (CORS preflight)
			if r.Method == "OPTIONS" {
				next.ServeHTTP(w, r)
				return
			}
			// Allow /api/pair/* paths
			if strings.HasPrefix(r.URL.Path, "/api/pair/") {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"reason": "pairing_mode"})
		})
	}
}
