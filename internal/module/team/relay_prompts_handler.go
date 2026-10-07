package teammod

import (
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// handleRelayPrompts answers GET /api/relay/prompts (lead-team-relay spec
// §8.8, U21): each body as this host stores it, or the built-in default
// when unset, plus the defaults, the fixed parts and the variables. It
// reads host config on every call, so the mod's next relay uses an edit
// (U21 (b)). Per host (U21 (e)): a session's mod asks the daemon it
// reports to. TokenAuth like the other /api/relay/* routes; no
// HostRoutePolicy entry, so a peer token is refused.
func (m *Module) handleRelayPrompts(w http.ResponseWriter, _ *http.Request) {
	stored, err := m.prompts.RelayPrompts()
	if err != nil {
		m.logf("[team] relay prompts: %v", err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "host config relay prompts unreadable; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, team.NewRelayPrompts(stored))
}
