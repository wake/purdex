// cmd/pdx/http_chain.go
package main

import (
	"net/http"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
	devicesmod "github.com/wake/purdex/internal/module/devices"
	peersmod "github.com/wake/purdex/internal/module/peers"
)

// registryDevices is the devices module as the token middleware reaches it: through the service registry, when the module
// is mounted and open (it publishes its authenticator at Init). Looked up on every use, so it needs no boot ordering.
type registryDevices struct{ c *core.Core }

func (r registryDevices) AuthenticateToken(token string) (devices.Principal, bool) {
	svc, ok := r.c.Registry.Get(devicesmod.RegistryKey)
	if !ok {
		return devices.Principal{}, false
	}
	a, ok := svc.(devices.Authenticator)
	if !ok {
		return devices.Principal{}, false
	}
	return a.AuthenticateToken(token)
}

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
		middleware.TokenAuthWith(tokenFn, c.Tickets, registryDevices{c})(mux))))

	outer := http.NewServeMux()
	outer.Handle("GET /api/health", middleware.CORS(http.HandlerFunc(c.HandleHealth)))
	outer.Handle("/api/peers", peerChain)
	outer.Handle("/api/peers/", peerChain)
	outer.Handle("/", general)
	return processInflight.Wrap(outer)
}
