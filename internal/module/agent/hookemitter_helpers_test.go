package agent

import agentpkg "github.com/wake/purdex/internal/agent"

// emitHookToSession sends a prebuilt frame through the slot the way a hook
// does. Production code builds its frame inside the slot (emitHookSession);
// the helper stays for the routing tests, which only care where the frame
// goes.
func (m *Module) emitHookToSession(req EventRequest, normalized agentpkg.NormalizedEvent) (string, string) {
	return m.emitHookSession(req, func(*SessionProjection) (agentpkg.NormalizedEvent, bool) {
		return normalized, true
	})
}
