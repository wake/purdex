// internal/module/peers/bind.go
package peers

import (
	"net/http"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
)

// BindError is a refusal of BindHostPrincipal: the HTTP status and the wire
// code and detail the route answers with.
type BindError struct {
	Status int
	Code   string
	Detail string
}

// BindHostPrincipal is the binding every host-principal route starts with
// (cross-host team spec §6.1, /deliver's steps 1–2): an admin is refused
// admin_not_allowed; anything but a host principal with a host id is
// host_unverified; and the live entry for the principal's alias (entryOf,
// read from the one config snapshot the route uses) must still carry that
// host id — an alias deleted and re-created for another host between
// authentication and handling would otherwise inherit the request. what
// names the route in the admin refusal. The principal is returned even on a
// refusal (zero when there was none), for the route's log line.
func BindHostPrincipal(r *http.Request, what string, entryOf func(alias string) (config.PeerHost, bool)) (middleware.Principal, config.PeerHost, *BindError) {
	p, ok := middleware.PrincipalFrom(r.Context())
	refuse := func(code, detail string) (middleware.Principal, config.PeerHost, *BindError) {
		return p, config.PeerHost{}, &BindError{Status: http.StatusForbidden, Code: code, Detail: detail}
	}
	switch {
	case ok && p.Kind == middleware.PrincipalAdmin:
		return refuse(ipeers.ErrAdminNotAllowed, what+" is for peer hosts, not the admin token")
	case !ok || p.Kind != middleware.PrincipalHost:
		return refuse(ipeers.ErrHostUnverified, "no host principal")
	case p.HostID == "":
		return refuse(ipeers.ErrHostUnverified, "host entry is unverified")
	}
	entry, found := entryOf(p.Alias)
	if !found || entry.HostID == "" || entry.HostID != p.HostID {
		return refuse(ipeers.ErrHostUnverified, "host entry no longer matches the authenticated host")
	}
	return p, entry, nil
}

// HostLimiter is the per-authenticated-host sliding-window admission limiter
// (ipeers.HostRateLimit), exported for the routes of other modules.
type HostLimiter = hostLimiter

// NewHostLimiter returns a limiter allowing limit requests per host id within window.
func NewHostLimiter(limit int, window time.Duration, now func() time.Time) *HostLimiter {
	return newHostLimiter(limit, window, now)
}

// AdmitDecode spends the host's limit, then decodes the capped body into v;
// see admitDecode. 0 means admitted and decoded.
func AdmitDecode(w http.ResponseWriter, r *http.Request, lim *HostLimiter, hostID string, maxBytes int64, v any) int {
	return admitDecode(w, r, lim, hostID, maxBytes, v)
}
