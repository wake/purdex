// internal/team/wire_proxy.go
package team

import "encoding/json"

// The member-report proxy (cross-host team spec §7): a remote member's `pdx report` / `pdx task …` are forwarded by its
// member host to the lead host, synchronously, as POST /api/peers/team/proxy. Only an allow-list of owner-scoped routes is
// served, and the actor is the member bound to the calling host and member key — never a session the request names.

// ProxyRoute is the lead host's proxy route.
const ProxyRoute = "/api/peers/team/proxy"

// Proxy refusal codes (HTTP 4xx of the route itself; a business refusal travels inside ProxyAnswer).
const (
	ErrProxyBadRequest      = "bad_request"
	ErrProxyForbidden       = "route_not_allowed" // 403: a method / path outside the allow-list
	ErrProxyOriginInbox     = "origin_inbox_forbidden"
	ErrProxyLeadUnreachable = "lead_unreachable" // 503, answered by the member host's CLI path when the lead host cannot be reached
	ErrProxyLeadUnpaired    = "lead_unpaired"    // 409: this host no longer has a paired entry for the lead host (permanent)
	ErrProxyLeadRefused     = "lead_refused"     // 502: the lead host refused the proxy call itself (a version skew or a bug; permanent)
)

// ProxyRequest is the request body. ToHostID is the lead host's own host id (409 wrong_host otherwise); MK the member key
// of the caller's membership; Method and Path (with its query) the forwarded call; Body its JSON body, which must not
// carry an origin_inbox (nor may the query).
type ProxyRequest struct {
	ToHostID string          `json:"to_host_id"`
	MK       string          `json:"mk"`
	Method   string          `json:"method"`
	Path     string          `json:"path"`
	Body     json.RawMessage `json:"body,omitempty"`
}

// ProxyAnswer is the 200 body: the lead host's id (the member host checks it is who it addressed) and the forwarded call's
// answer, status and body unchanged.
type ProxyAnswer struct {
	HostID string          `json:"host_id"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}
