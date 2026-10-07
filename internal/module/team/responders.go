package teammod

import "github.com/wake/purdex/internal/core"

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

// modPresent reports whether the session's mod said hello: it reads the
// one presence record, P5a-2a's modSeen (written by handleRelayHello under
// m.mu). A PreToolUse/AskUserQuestion from a present session opens no
// terminal_only row — the mod raises its own (spec §6.6, U19 point 4).
func (m *Module) modPresent(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.modSeen[sessionID]
	return ok
}
