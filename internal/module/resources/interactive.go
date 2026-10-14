package resourcesmod

import (
	"github.com/wake/purdex/internal/modevents"
)

// sessionInteractive says whether a notice can reach the session: a live mod
// stream of it announced the prompt capability within modevents.CapsFresh
// (only an interactive session's mod does; `claude -p`, launchd and cron have
// none). An auto request from such a session is queued instead of waiting
// (lease v2 amendment A §2, decided with the M-P2(a) probe: a command prefix
// that would tell the daemon changes the permission prompts, so the daemon
// reads it from the session itself). The registry is looked up at use time:
// the mod event channel may start after this module.
func (m *Module) sessionInteractive(sid string) bool {
	if m.interactive != nil {
		return m.interactive(sid)
	}
	if m.core == nil || m.core.Registry == nil {
		return false
	}
	svc, ok := m.core.Registry.Get("modevents")
	if !ok {
		return false
	}
	reg, ok := svc.(*modevents.Registry)
	return ok && reg.SessionCapable(sid, modevents.CapPromptV1, modevents.CapsFresh)
}
