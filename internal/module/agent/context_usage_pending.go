package agent

import (
	"context"
	"log"

	"github.com/wake/purdex/internal/statuspending"
)

// applyPendingStatuslines catches the usage readings up with the payloads the statusline proxy kept while the daemon was down
// (#2545), at boot after the persisted readings are back. Only the usage reading is touched — the UI's status snapshot and its
// broadcasts are the live screen's, and an old payload is not replayed into them. A payload is applied only when it is NEWER than
// the reading the daemon has (a live POST that already arrived wins), only for a session that is live, and never when older than
// statuspending.MaxAge; every file is deleted once looked at. When the live sessions cannot be read nothing is applied or deleted:
// the files wait for the next boot.
func (m *Module) applyPendingStatuslines(ctx context.Context) {
	if m.pendingDir == "" {
		return
	}
	entries, err := statuspending.Load(m.pendingDir)
	if err != nil {
		log.Printf("[agent] pending statuslines: %v", err)
		return
	}
	if len(entries) == 0 {
		return
	}
	sessions, err := m.LiveSessions(ctx, "cc")
	if err != nil {
		log.Printf("[agent] pending statuslines: live sessions: %v", err)
		return
	}
	live := map[string]bool{}
	for _, s := range sessions {
		live[s.SessionID] = true
	}
	now := usageNow()
	applied := 0
	for _, e := range entries {
		if live[e.SessionID] && now-e.AtMs <= statuspending.MaxAge {
			if sid, u, ok := parseStatusline(e.Raw); ok && sid == e.SessionID {
				u.At = e.AtMs
				if m.storeContextUsage(sid, u, true) {
					applied++
				}
			}
		}
		statuspending.Remove(m.pendingDir, e.SessionID)
	}
	if applied > 0 {
		log.Printf("[agent] boot: caught %d session reading(s) up with statusline payloads lost while the daemon was down", applied)
	}
}
