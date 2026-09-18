// internal/middleware/peer_auth.go
package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/config"
)

// PrincipalKind identifies who a request was authenticated as by PeerAuth.
type PrincipalKind string

const (
	PrincipalAdmin PrincipalKind = "admin"
	PrincipalHost  PrincipalKind = "host"
)

// Principal is the authenticated identity PeerAuth stores in the request
// context for downstream handlers.
type Principal struct {
	Kind   PrincipalKind
	Alias  string // host only
	HostID string // host only; "" when the entry is unverified
	// UsedPrevToken is true when a host principal authenticated with the
	// entry's InboundTokenPrev (a rotation is pending and the peer is still
	// on the old token). The peers module records it per alias (spec §6.2).
	UsedPrevToken bool
	// TokenFingerprint is config.TokenFingerprint of the bearer a host
	// principal presented — WHICH token, not merely whether it was the
	// prev one. The peers module's rotation record stores this and derives
	// "current"/"prev" against the entry's tokens at read time, so a note
	// that raced a rotate is judged by the token it carries, not by the
	// moment it landed (spec §6.2). Empty for admin principals.
	TokenFingerprint string
}

type principalCtxKey struct{}

// WithPrincipal returns a copy of ctx carrying p, so handler tests in other
// packages can build a request context without running PeerAuth itself.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFrom returns the principal PeerAuth stored in ctx, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}

// PeerAuth authenticates every request it sees (it is mounted only on the
// /api/peers prefix — it does no prefix matching itself):
//  1. a non-empty admin token presented as Bearer ⇒ PrincipalAdmin;
//  2. else a Bearer matching a configured host's InboundToken ⇒ PrincipalHost;
//  3. else 401.
//
// ?ticket= is never consulted. An empty admin token disables (1) only.
// hostAllowed decides whether a host principal may reach this request at
// all (admin may reach everything); a refused host gets 403.
func PeerAuth(adminToken func() string, peers func() config.PeersConfig, hostAllowed func(r *http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bearer, ok := bearerToken(r)
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			if admin := adminToken(); admin != "" && subtle.ConstantTimeCompare([]byte(bearer), []byte(admin)) == 1 {
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), Principal{Kind: PrincipalAdmin})))
				return
			}

			if host, usedPrev, matched := peers().MatchInboundToken(bearer); matched {
				if !hostAllowed(r) {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
				p := Principal{
					Kind:             PrincipalHost,
					Alias:            host.Alias,
					HostID:           host.HostID,
					UsedPrevToken:    usedPrev,
					TokenFingerprint: config.TokenFingerprint(bearer),
				}
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
				return
			}

			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header, mirroring TokenAuth's case-insensitive prefix match. ok is false
// when the header is absent or not a Bearer header.
func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if len(auth) < 7 || !strings.EqualFold(auth[:7], "bearer ") {
		return "", false
	}
	return auth[7:], true
}
