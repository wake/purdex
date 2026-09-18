// internal/module/peers/policy.go
package peers

import (
	"net/http"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// HostMatcher is the matcher cmd/pdx/http_chain.go hands to
// middleware.PeerAuth: the host-token match over the LIVE config and its
// observation (c.HostAuthObserver, installed by Init as noteInboundFP) in
// ONE critical section under CfgMu.RLock. UpdateConfig's CfgMu.Lock() —
// the rotation gates run inside it — therefore waits for every in-flight
// authentication and sees its note (codex F2). The observer must be called
// before RUnlock; moving it after reopens the window in which a commit
// drops the token a peer has just authenticated with.
func HostMatcher(c *core.Core) func(bearer string) (config.PeerHost, bool, bool) {
	return func(bearer string) (config.PeerHost, bool, bool) {
		c.CfgMu.RLock()
		defer c.CfgMu.RUnlock()
		h, usedPrev, ok := c.Cfg.Peers.MatchInboundToken(bearer)
		if ok && c.HostAuthObserver != nil {
			c.HostAuthObserver(h.Alias, config.TokenFingerprint(bearer))
		}
		return h, usedPrev, ok
	}
}

// HostRoutePolicy says which requests a host principal may make: GET
// /api/peers (exact path) with no scope query parameter, or scope=local;
// and POST /api/peers/deliver (a peer host delivering an inbound message,
// Task 8). Everything else — hosts routes, settings, scope=all, any other
// method — is admin-only (false here means "refuse the host principal";
// admin principals bypass this policy entirely).
func HostRoutePolicy(r *http.Request) bool {
	if r.Method == http.MethodPost && r.URL.Path == "/api/peers/deliver" {
		return true
	}
	if r.Method != http.MethodGet || r.URL.Path != "/api/peers" {
		return false
	}
	switch r.URL.Query().Get("scope") {
	case "", "local":
		return true
	default:
		return false
	}
}
