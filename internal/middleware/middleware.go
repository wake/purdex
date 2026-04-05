// internal/middleware/middleware.go
package middleware

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// IPWhitelist restricts access by IP. Empty list = allow all.
func IPWhitelist(allowed []string) func(http.Handler) http.Handler {
	if len(allowed) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	var nets []*net.IPNet
	var ips []net.IP
	for _, a := range allowed {
		if _, cidr, err := net.ParseCIDR(a); err == nil {
			nets = append(nets, cidr)
		} else if ip := net.ParseIP(a); ip != nil {
			ips = append(ips, ip)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(host)
			if ip == nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			for _, cidr := range nets {
				if cidr.Contains(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
			for _, a := range ips {
				if a.Equal(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}
}

// TicketValidator validates one-time WS authentication tickets.
type TicketValidator interface {
	Validate(ticket string) bool
}

// TokenAuth checks Bearer token or one-time ticket (?ticket=).
// tokenFn is called on each request to support runtime token changes.
// Bearer prefix is case-insensitive, token value is case-sensitive.
// If tickets is non-nil, ?ticket= is checked for WebSocket authentication.
func TokenAuth(tokenFn func() string, tickets TicketValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := tokenFn()
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			// Check Authorization header first
			if auth := r.Header.Get("Authorization"); len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") && subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			// Check one-time ticket (for WebSocket)
			if tickets != nil {
				if ticket := r.URL.Query().Get("ticket"); tickets.Validate(ticket) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}

// CORS adds permissive CORS headers. Safe because auth is handled by IP + token.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
