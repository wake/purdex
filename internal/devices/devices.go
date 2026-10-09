// Package devices holds what the daemon's pieces share about a paired phone's token (QR pairing spec §3): the token's
// format and hash, the device id, and the principal an authenticated request carries. It has no state and no
// dependencies; the module that stores and serves the tokens is internal/module/devices.
package devices

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// TokenPrefix marks a device token among the bearers the daemon accepts (the admin token has none; a peer's is pdxp_).
const TokenPrefix = "pdxd_"

var (
	tokenRe = regexp.MustCompile(`^pdxd_[0-9a-f]{32}$`)
	idRe    = regexp.MustCompile(`^d_[0-9a-f]{12}$`)
)

// NewToken is a fresh device token: "pdxd_" + 32 hex (16 random bytes). It is shown once, at creation; only Hash(token) is
// ever stored.
func NewToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return TokenPrefix + hex.EncodeToString(b[:]), nil
}

// IsDeviceToken: exactly the shape NewToken makes. A bearer that is not this is not looked up.
func IsDeviceToken(s string) bool { return tokenRe.MatchString(s) }

// Hash is the SHA-256 of the token, lowercase hex: what the store keeps and looks up by. A 128-bit random token gives an
// attacker nothing to time, so no constant-time comparison is claimed (spec §3.1).
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewID is a device id: "d_" + 12 hex.
func NewID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "d_" + hex.EncodeToString(b[:]), nil
}

// ValidID reports whether s has the shape of a device id.
func ValidID(s string) bool { return idRe.MatchString(s) }

// ClientID is the profile client id a device's writes name it by (spec §5.2): "c_" + the 12 hex of its device id.
func ClientID(deviceID string) string { return "c_" + strings.TrimPrefix(deviceID, "d_") }

// Principal is who a device-authenticated request is: the device, the pairing it belongs to, and the one profile it may
// read (empty = none).
type Principal struct {
	ID        string
	PairingID string
	ProfileID string
}

type ctxKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

type adminKey struct{}

// WithAdmin marks ctx as authenticated by the admin token (or by auth being off). A request that got in some other way, a
// one-time WebSocket ticket for one, carries neither this nor a principal and is NOT an admin: the management routes ask
// for this mark instead of treating "no device principal" as "admin".
func WithAdmin(ctx context.Context) context.Context { return context.WithValue(ctx, adminKey{}, true) }

// IsAdmin reports whether ctx carries the admin mark.
func IsAdmin(ctx context.Context) bool { v, _ := ctx.Value(adminKey{}).(bool); return v }

// PrincipalFrom is the device principal of a request's context; absent for the admin token (and with auth off).
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// Caller is who a request, or a one-time ticket recorded at its creation, speaks for: the admin, one device, or nobody in
// particular (the zero value: a ticket made without a context, an anonymous authenticated request).
type Caller struct {
	Admin  bool
	Device *Principal
}

// CallerFrom reads the caller off a request's context. The device principal is a copy.
func CallerFrom(ctx context.Context) Caller {
	c := Caller{Admin: IsAdmin(ctx)}
	if p, ok := PrincipalFrom(ctx); ok {
		c.Device = &p
	}
	return c
}

// WithCaller returns ctx carrying exactly c: the admin mark when it is the admin, the principal when it is a device (and
// then never the admin mark, whatever ctx held), neither when it is nobody.
func WithCaller(ctx context.Context, c Caller) context.Context {
	switch {
	case c.Device != nil:
		return context.WithValue(context.WithValue(ctx, adminKey{}, false), ctxKey{}, *c.Device)
	case c.Admin:
		return WithAdmin(ctx)
	}
	return ctx
}

// Refresher gives the CURRENT principal of a device id, or false when the device is revoked or unknown. A one-time ticket
// minted by a device is redeemed through it: the ticket is a snapshot taken up to 30 s earlier, and a device revoked in the
// meantime (or whose bindings changed) must not be let in on that snapshot.
type Refresher interface {
	RefreshPrincipal(deviceID string) (Principal, bool)
}

// RevokeFeed lets another module hear that devices were revoked (by id or by pairing), with their ids, after the module has
// closed their connections. Push uses it to drop a revoked phone's registrations. A subscriber runs on the revoking
// request's goroutine, so it must be quick.
type RevokeFeed interface {
	SubscribeRevoked(fn func(ids []string))
}

// Authenticator turns a bearer into a principal. The middleware holds one; the devices module implements it.
type Authenticator interface {
	// AuthenticateToken: ok only for a live device token (not revoked, used before or still before its use_by).
	AuthenticateToken(token string) (Principal, bool)
}
