// cmd/pdx/http_chain.go
package main

import (
	"net/http"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
	devicesmod "github.com/wake/purdex/internal/module/devices"
	hosttransfermod "github.com/wake/purdex/internal/module/hosttransfer"
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

// RefreshPrincipal is what a redeemed device ticket is held to (devices.Refresher): the module's current answer for the
// device id. Without it the middleware would refuse every device ticket.
func (r registryDevices) RefreshPrincipal(deviceID string) (devices.Principal, bool) {
	svc, ok := r.c.Registry.Get(devicesmod.RegistryKey)
	if !ok {
		return devices.Principal{}, false
	}
	ref, ok := svc.(devices.Refresher)
	if !ok {
		return devices.Principal{}, false
	}
	return ref.RefreshPrincipal(deviceID)
}

// registryTracker is the devices module's WebSocket tracker as the chain reaches it: through the service registry, looked
// up on every request; with no module mounted the request passes through.
type registryTracker struct{ c *core.Core }

func (r registryTracker) Track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if svc, ok := r.c.Registry.Get(devicesmod.ConnsKey); ok {
			if t, ok := svc.(devices.Tracker); ok {
				t.Track(next).ServeHTTP(w, req)
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}

// newOuterHandler builds the daemon's outer http.Handler: /api/health
// (CORS only), the /api/peers prefix chain (PeerAuth, no TokenAuth) and the
// general chain (today's, minus PeerRouteAuth) for everything else.
// routes is the ServeMux the modules register on (the device scope asks it which pattern a request would reach); inner is
// what serves the request (normally the same mux). A nil routes refuses every device request.
func newOuterHandler(c *core.Core, routes *http.ServeMux, inner http.Handler, allow []string) http.Handler {
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
		middleware.PeerAuth(tokenFn, peersmod.HostMatcher(c), peersmod.HostRoutePolicy)(inner))))
	general := middleware.CORS(middleware.IPWhitelist(allow)(middleware.PairingGuard(isPairing)(
		middleware.TokenAuthWith(tokenFn, c.Tickets, registryDevices{c})(registryTracker{c}.Track(deviceScope(routes, inner))))))

	// The pairing claim (QR pairing spec §4.2) is the one route that takes no bearer: the phone has no credential yet and the
	// code is it. It skips TokenAuth and so never carries a principal (the device scope has nothing to scope); the route checks
	// its own source (tailnet / loopback) and keeps its own failure limiter. The exemption is this one exact path and method.
	claim := middleware.CORS(middleware.IPWhitelist(allow)(middleware.PairingGuard(isPairing)(inner)))

	outer := http.NewServeMux()
	outer.Handle("POST "+hosttransfermod.ClaimRoute, claim)
	outer.Handle("GET /api/health", middleware.CORS(http.HandlerFunc(c.HandleHealth)))
	outer.Handle("/api/peers", peerChain)
	outer.Handle("/api/peers/", peerChain)
	outer.Handle("/", general)
	return processInflight.Wrap(outer)
}
