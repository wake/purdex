// internal/middleware/peer_route_auth.go
package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// PeerRouteAuth guards prefix (and prefix + "/…") — see spec §4.6.
// On a matched path, a non-empty admin token must be presented as a Bearer
// header (constant-time compare, prefix case-insensitive, mirroring
// TokenAuth); ?ticket= is never accepted and no TicketValidator is ever
// consulted; an empty configured admin token yields 401. Non-matching paths
// pass to next unchanged, with no check performed by this middleware.
func PeerRouteAuth(prefix string, tokenFn func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path != prefix && !strings.HasPrefix(path, prefix+"/") {
				next.ServeHTTP(w, r)
				return
			}
			token := tokenFn()
			if token != "" {
				if auth := r.Header.Get("Authorization"); len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") && subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(token)) == 1 {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}
