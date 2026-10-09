// internal/middleware/middleware.go
package middleware

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/wake/purdex/internal/devices"
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

// CallerTicketValidator is a TicketValidator whose tickets remember who asked for them: a valid ticket then gives the
// caller back (validate-and-consume, one step), and the request carries that caller exactly as if it had come with the
// bearer. TokenAuth uses this when the validator has it.
type CallerTicketValidator interface {
	TicketValidator
	ValidateCaller(ticket string) (devices.Caller, bool)
}

// TokenAuth checks Bearer token or one-time ticket (?ticket=).
// tokenFn is called on each request to support runtime token changes.
// Bearer prefix is case-insensitive, token value is case-sensitive.
// If tickets is non-nil, ?ticket= is checked for WebSocket authentication —
// and ONLY on a real WebSocket handshake: a GET with Connection: Upgrade +
// Upgrade: websocket (websocket.IsWebSocketUpgrade) and a
// Sec-WebSocket-Version header (browsers and gorilla's Dialer always send
// "13"). A WebSocket handshake is a GET; anything else with upgrade
// headers is a REST call wearing a costume. A ticket is minted for a
// browser that cannot set an Authorization header on a handshake; on any
// other request shape (plain GET, POST, SSE GET, POST + upgrade headers,
// ...) it is neither consulted nor consumed, so a one-time ticket can
// never stand in for the bearer on a REST route (e.g. /api/nex/...
// mutations).
func TokenAuth(tokenFn func() string, tickets TicketValidator) func(http.Handler) http.Handler {
	return TokenAuthWith(tokenFn, tickets, nil)
}

// TokenAuthWith is TokenAuth that also accepts a live device token (a paired phone's, QR pairing spec §3.2): a bearer
// shaped like one (and only such a bearer) is offered to devs, and when it is live the request goes on carrying the
// device principal in its context. The admin token is checked first and carries no principal; an empty admin token leaves
// auth off exactly as before (device tokens add nothing there); a nil devs makes this TokenAuth.
func TokenAuthWith(tokenFn func() string, tickets TicketValidator, devs devices.Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := tokenFn()
			if token == "" {
				next.ServeHTTP(w, r.WithContext(devices.WithAdmin(r.Context()))) // auth is off: everyone is the admin
				return
			}
			// Check Authorization header first
			auth := r.Header.Get("Authorization")
			if len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") {
				bearer := auth[7:]
				if subtle.ConstantTimeCompare([]byte(bearer), []byte(token)) == 1 {
					next.ServeHTTP(w, r.WithContext(devices.WithAdmin(r.Context())))
					return
				}
				if devs != nil && devices.IsDeviceToken(bearer) {
					if p, ok := devs.AuthenticateToken(bearer); ok {
						next.ServeHTTP(w, r.WithContext(devices.WithPrincipal(r.Context(), p)))
						return
					}
				}
			}
			// Check one-time ticket — real WebSocket handshakes only: a
			// WebSocket handshake is a GET; anything else with upgrade
			// headers is a REST call wearing a costume.
			if tickets != nil && isWebSocketHandshake(r) {
				ticket := r.URL.Query().Get("ticket")
				if ct, ok := tickets.(CallerTicketValidator); ok {
					if caller, valid := ct.ValidateCaller(ticket); valid {
						// A device's ticket is a snapshot: the device is looked up again, so a revoked one is refused here
						// (before any handler work) and a live one carries its current bindings. Without a way to look it up
						// a device ticket is not honoured.
						if caller.Device != nil {
							ref, canRefresh := devs.(devices.Refresher)
							if !canRefresh {
								http.Error(w, "unauthorized", http.StatusUnauthorized)
								return
							}
							p, live := ref.RefreshPrincipal(caller.Device.ID)
							if !live {
								http.Error(w, "unauthorized", http.StatusUnauthorized)
								return
							}
							caller.Device = &p
						}
						next.ServeHTTP(w, r.WithContext(devices.WithCaller(r.Context(), caller)))
						return
					}
				} else if tickets.Validate(ticket) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}

// isWebSocketHandshake reports whether r has the shape of a WebSocket
// opening handshake (RFC 6455 §4.1): method GET, Connection: Upgrade,
// Upgrade: websocket, and a Sec-WebSocket-Version header.
func isWebSocketHandshake(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		websocket.IsWebSocketUpgrade(r) &&
		r.Header.Get("Sec-WebSocket-Version") != ""
}

// CORS adds permissive CORS headers. Safe because auth is handled by IP + token.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID, X-Pdx-Client")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
