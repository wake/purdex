// cmd/pdx/device_scope.go
package main

import (
	"net/http"
	"regexp"

	"github.com/wake/purdex/internal/devices"
)

// deviceAllowed is the default-deny allow-list of a paired phone (QR pairing spec §3.3, R7): the exact patterns the daemon's
// mux registers that a device principal may reach. A route that is not here answers 403 device_forbidden, so a new route is
// denied to phones until someone adds it on purpose. A test pins this set against the real daemon's registered patterns.
var deviceAllowed = map[string]bool{
	// host
	"GET /api/info":       true,
	"POST /api/ws-ticket": true,
	"GET /api/hostconfig": true,
	// WebSockets: host events, terminal (and ?mirror=1), conversations
	"/ws/host-events":                               true,
	"/ws/terminal/{code}":                           true,
	"GET /ws/conversations/{provider}/{session_id}": true,
	// sessions
	"POST /api/sessions":                  true,
	"POST /api/sessions/{code}/send-keys": true,
	"GET /api/sessions/{code}/provenance": true,
	"GET /api/sessions/{code}/transcript": true,
	// conversations
	"GET /api/conversations/{provider}/{session_id}":                      true,
	"GET /api/conversations/{provider}/{session_id}/subagents/{agent_id}": true,
	// team
	"GET /api/team/approvals/{id}":         true,
	"GET /api/team/adoptions/{id}":         true, // read-only: the membership a remote adopt's approval led to (X3c)
	"POST /api/team/approvals/{id}/decide": true,
	"GET /api/team/unattended":             true,
	"PUT /api/team/unattended":             true,
	"PUT /api/team/relay-quota":            true, // the user's call (2026-10-09 20:2x): the phone may set the relay quota ...
	"PUT /api/team/max-members":            true, // ... and the team size cap
	"POST /api/relay/self":                 true,
	// nex: the executions list; the engine mount is narrowed by deviceNexAllowed
	"GET /api/nex/v1/executions": true,
	"/api/nex/":                  true,
	// push
	"POST /api/push/devices":               true,
	"GET /api/push/devices":                true,
	"DELETE /api/push/devices/{device_id}": true,
	// profiles (the list is narrowed to the device's own profile by the handler)
	"GET /api/profiles":                         true,
	"GET /api/profiles/{id}":                    true,
	"GET /api/profiles/{id}/sections/{section}": true,
	"PUT /api/profiles/{id}/sections/{section}": true,
	// devices
	"PUT /api/devices/self": true,
	// session workbook: read-only
	"GET /api/workbook/conversations/{provider}/{session_id}": true,
	"GET /api/workbook/entries":                               true,
}

// nexEnginePattern is the one pattern that mounts the whole embedded engine; for a device the scope looks past the pattern.
const nexEnginePattern = "/api/nex/"

// deviceNexAllowed: through the engine mount a phone may only GET an execution's prelude or event stream.
var deviceNexAllowed = regexp.MustCompile(`^/api/nex/v1/executions/[^/]+/(prelude|events)$`)

// deviceScope refuses a device principal every request the mux would not dispatch to a pattern in deviceAllowed. The mux
// itself decides the pattern (routes.Handler(r)), so wildcards, methods and path cleaning match the real dispatch. Without
// routes every device request is refused. Requests without a device principal (the admin's) pass untouched.
func deviceScope(routes *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, isDevice := devices.PrincipalFrom(r.Context()); isDevice && !deviceMayReach(routes, r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"device_forbidden","detail":"this route is not available to a paired phone"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func deviceMayReach(routes *http.ServeMux, r *http.Request) bool {
	if routes == nil {
		return false
	}
	_, pattern := routes.Handler(r)
	if !deviceAllowed[pattern] {
		return false
	}
	if pattern == nexEnginePattern {
		return r.Method == http.MethodGet && deviceNexAllowed.MatchString(r.URL.Path)
	}
	return true
}
