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
	if r.Method == http.MethodGet && r.URL.Path == "/api/peers/team/roots" {
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/peers/team/commands" { // cross-host team commands (X2b)
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/peers/team/facts" { // cross-host team facts (X3b-2)
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/peers/team/roots-change" { // a lead host adds / removes / reads its roots (cross-host roots spec, XR-1)
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/peers/team/proxy" { // a remote member's report / task calls (X6)
		return true
	}
	if r.Method == http.MethodPost && (r.URL.Path == "/api/peers/watch/holder" || r.URL.Path == "/api/peers/watch/announce" || r.URL.Path == "/api/peers/watch/event" || r.URL.Path == "/api/peers/watch/ack" || r.URL.Path == "/api/peers/watch/log" || r.URL.Path == "/api/peers/watch/start" || r.URL.Path == "/api/peers/watch/wake" || r.URL.Path == "/api/peers/watch/note" || r.URL.Path == "/api/peers/watch/wake-settings/get" || r.URL.Path == "/api/peers/watch/wake-settings/put" || r.URL.Path == "/api/peers/watch/wake-settings/changed" || r.URL.Path == "/api/peers/watch/wake-suppressed") { // where is the load watcher; a host registered one (host-load-control W2a, W3a)
		return true
	}
	if r.Method == http.MethodPost && (r.URL.Path == "/api/peers/fleet/role" || r.URL.Path == "/api/peers/fleet/announce") { // the fleet's primary / standby calls (fleet spec §4)
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
