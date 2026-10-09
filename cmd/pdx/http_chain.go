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

// interimDeviceScope keeps a device principal to two routes until QP-1b's default-deny allow-list replaces it: reading
// /api/info (how a phone checks a host) and renaming itself. Everything else answers 403 device_forbidden, so a token
// minted before the real scope exists reaches no config, file, restart, session or team route and cannot fetch a WebSocket
// ticket (a ticket carries no principal yet). Admin requests carry no principal and are untouched.
func interimDeviceScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, isDevice := devices.PrincipalFrom(r.Context()); isDevice {
			ok := (r.Method == http.MethodGet && r.URL.Path == "/api/info") || (r.Method == http.MethodPut && r.URL.Path == "/api/devices/self")
			if !ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"device_forbidden","detail":"this route is not available to a paired phone"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
		middleware.TokenAuthWith(tokenFn, c.Tickets, registryDevices{c})(registryTracker{c}.Track(interimDeviceScope(mux))))))

	outer := http.NewServeMux()
	outer.Handle("GET /api/health", middleware.CORS(http.HandlerFunc(c.HandleHealth)))
	outer.Handle("/api/peers", peerChain)
	outer.Handle("/api/peers/", peerChain)
	outer.Handle("/", general)
	return processInflight.Wrap(outer)
}
