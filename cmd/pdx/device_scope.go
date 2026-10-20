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
	"GET /api/fleet":      true, // the fleet status (fleet spec §6): the primary / standby answer; the alias list next to it is the admin's
	// the resource pool (wwd spec §10), read-only: the hosts with their facts, and a host's facts history
	"GET /api/pool/hosts":                    true,
	"GET /api/pool/hosts/{alias}/exceptions": true, // a host's exceptions with their holds (G4), read-only like the hosts
	"GET /api/pool/hosts/{alias}/alerts":     true, // a host's alerts with the kind of each silence (capability pool.alerts.v1), read-only like the hosts
	"GET /api/pool/facts/history":            true,
	"GET /api/pool/facts/series":             true, // the same history cut down to what a chart draws (capability pool.series.v1)
	// the pool's jobs (wwd spec §10), read-only like the hosts: the list, one job, its log. A phone sees the same job fields (task, args,
	// summary, log) as the Mac App; both are the user's own devices (spec §11).
	"GET /api/pool/jobs":          true,
	"GET /api/pool/jobs/{id}":     true,
	"GET /api/pool/jobs/{id}/log": true,
	// WebSockets: host events, terminal (and ?mirror=1), conversations
	"/ws/host-events":                               true,
	"/ws/terminal/{code}":                           true,
	"GET /ws/conversations/{provider}/{session_id}": true,
	// sessions
	"POST /api/sessions":                   true,
	"POST /api/sessions/{code}/send-keys":  true,
	"GET /api/sessions/{code}/launch-plan": true, // the no-agent launch panel: what to offer ...
	"POST /api/sessions/{code}/launch":     true, // ... and the choice (resume / start fresh)
	"POST /api/sessions/{code}/submit":     true, // typing into Claude Code through the daemon's guard (K-1a-2)
	"POST /api/sessions/{code}/recall":     true, // take the queued messages back out of Claude Code's input (K-1b)
	"POST /api/sessions/{code}/send-now":   true, // send Claude Code's queued messages at once (MS2-2)
	"POST /api/sessions/{code}/read":       true, // read sync (badge spec §7): the phone opened a session; the cursor it read through
	"GET /api/sessions/{code}/provenance":  true,
	"GET /api/sessions/{code}/transcript":  true,
	// conversations
	"GET /api/conversations/{provider}/{session_id}":                      true,
	"GET /api/conversations/{provider}/{session_id}/subagents/{agent_id}": true,
	// team
	"GET /api/team/approvals/{id}":            true,
	"GET /api/team/adoptions/{id}":            true, // read-only: the membership a remote adopt's approval led to (X3c)
	"POST /api/team/approvals/{id}/decide":    true,
	"GET /api/team/approvals/{id}/lead-names": true, // the names a lead request could take now (name pool v2 §5.3): the phone's card too
	"GET /api/team/unattended":                true,
	"PUT /api/team/unattended":                true,
	"PUT /api/team/relay-quota":               true, // the user's call (2026-10-09 20:2x): the phone may set the relay quota ...
	"PUT /api/team/max-members":               true, // ... and the team size cap
	"POST /api/relay/self":                    true,
	// the unattended quota pause (UP-5b): the phone lists the paused sessions and releases one, as the Mac App does; it may
	// already turn unattended off (which releases them all)
	"GET /api/team/paused":                       true,
	"POST /api/team/paused/{session_id}/release": true,
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
	// agent upload: the iOS deck / chat attachments (the phone sends inject=0 and carries the returned path in its own message)
	"POST /api/agent/upload": true,
	// session workbook: read-only
	"GET /api/workbook/conversations/{provider}/{session_id}":       true,
	"GET /api/workbook/entries":                                     true,
	"GET /api/workbook/conversations/{provider}/{session_id}/todos": true,
	// the workbook journal: a unit's entries, tasks and roadmap, and the hand check of a 待確認 item (iOS later; the same trust as the workbook reads)
	"GET /api/journal":         true,
	"GET /api/journal/tasks":   true,
	"GET /api/journal/roadmap": true,
	"PUT /api/journal/confirm": true,
	// the team members' status lines (journal.members.v1): read-only
	"GET /api/journal/members": true,
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
