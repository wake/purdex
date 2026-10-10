package teammod

import (
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
	modeventsmod "github.com/wake/purdex/internal/module/modevents"
)

// RemoteResponders answers whether anyone remote could answer a 分流 row
// right now (spec §6.6 step 1, air26 point 1): a connected host-events
// subscriber — an App or a phone viewing this host (M23) — or, once the iOS
// line adds it, a device registered for push. P8a ships the WS half only.
type RemoteResponders interface {
	Any() bool
}

// wsResponders is the WS half: the /ws/host-events subscriber set.
type wsResponders struct{ events *core.EventsBroadcaster }

func (r wsResponders) Any() bool { return r.events != nil && r.events.HasSubscribers() }

// modPresent reports whether the session has a live mod event stream: one
// the modevents registry holds that is not ended, whose current sid is this
// session and whose last batch is within modevents.PresenceWindow (spec
// §6.6 "Mod present", #1832). The hello record (modSeen) is not this test:
// it is never withdrawn. A PreToolUse/AskUserQuestion from a present
// session opens no terminal_only row — the mod raises its own (U19 point 4).
//
// The registry is looked up per call, not at Init: the socket module has no
// ordering against this one and may be absent. Without it, or on an empty
// registry (a daemon that just restarted), nobody is present — the safe
// direction: the row opens and the mod's begin takes it over.
func (m *Module) modPresent(sessionID string) bool {
	if sessionID == "" || m.core == nil {
		return false
	}
	svc, ok := m.core.Registry.Get(modeventsmod.ServiceName)
	if !ok {
		return false
	}
	live, ok := svc.(interface {
		SessionLive(sid string, within time.Duration) bool
	})
	return ok && live.SessionLive(sessionID, modevents.PresenceWindow)
}
