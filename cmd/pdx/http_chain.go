// cmd/pdx/http_chain.go
package main

import (
	"net/http"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	peersmod "github.com/wake/purdex/internal/module/peers"
)

// newOuterHandler builds the daemon's outer http.Handler: /api/health
// (CORS only), the /api/peers prefix chain (PeerAuth, no TokenAuth) and the
// general chain (today's, minus PeerRouteAuth) for everything else.
func newOuterHandler(c *core.Core, mux http.Handler, allow []string) http.Handler {
	tokenFn := func() string {
		c.CfgMu.RLock()
		defer c.CfgMu.RUnlock()
		return c.Cfg.Token
	}
	isPairing := func() bool { return c.Pairing.Get() == core.StatePairing }

	// peersmod.HostMatcher: the host-token match and its observation (the
	// peers module's rotation note) are one critical section under
	// CfgMu.RLock, so a config writer cannot interleave between them.
	peerChain := middleware.CORS(middleware.IPWhitelist(allow)(middleware.PairingGuard(isPairing)(
		middleware.PeerAuth(tokenFn, peersmod.HostMatcher(c), peersmod.HostRoutePolicy)(mux))))
	general := middleware.CORS(middleware.IPWhitelist(allow)(middleware.PairingGuard(isPairing)(
		middleware.TokenAuth(tokenFn, c.Tickets)(mux))))

	outer := http.NewServeMux()
	outer.Handle("GET /api/health", middleware.CORS(http.HandlerFunc(c.HandleHealth)))
	outer.Handle("/api/peers", peerChain)
	outer.Handle("/api/peers/", peerChain)
	outer.Handle("/", general)
	return outer
}
