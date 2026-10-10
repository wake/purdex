package agent

import (
	"context"
	"log"

	"github.com/wake/purdex/internal/statuspending"
)

// pendingSkewMs is how far ahead of the daemon's clock a pending timestamp may be (the same host: only a clock that stepped
// back since the proxy wrote it). Beyond that the file is not a reading to believe — it would beat every later live POST.
const pendingSkewMs = 60 * 1000

// applyPendingStatuslines catches the usage readings up with the payloads the statusline proxy kept while the daemon was down
// (#2545), at boot after the persisted readings are back. Only the usage reading is touched — the UI's status snapshot and its
// broadcasts are the live screen's, and an old payload is not replayed into them. A payload is applied only when it is NEWER than
// the reading the daemon has (a live POST that already arrived wins), only for a session that is live, and never when older than
// statuspending.MaxAge or dated in the future. The file is the only copy until the reading is on disk, so the applied readings
// are flushed first and a file is deleted only once its reading is persisted (kept for the next boot when the flush failed); a
// file that was not applied is deleted at once. A file is deleted only if it is still the version that was loaded. When the live
// sessions cannot be read nothing is applied or deleted.
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
	if m.pendingAfterLoad != nil {
		m.pendingAfterLoad()
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
	var applied []statuspending.Entry
	for _, e := range entries {
		keep := false
		if live[e.SessionID] && now-e.AtMs <= statuspending.MaxAge && e.AtMs <= now+pendingSkewMs {
			if sid, u, ok := parseStatusline(e.Raw); ok && sid == e.SessionID {
				u.At = e.AtMs
				if m.storeContextUsage(sid, u, true) {
					applied = append(applied, e)
					keep = true // its file waits for the flush below
				}
			}
		}
		if !keep {
			statuspending.RemoveIfNotNewer(m.pendingDir, e.SessionID, e.AtMs)
		}
	}
	if len(applied) == 0 {
		return
	}
	m.flushContextUsage() // persisted before any file goes: the file is the only copy until then
	caught := 0
	for _, e := range applied {
		if m.readingPersisted(e.SessionID, e.AtMs) {
			statuspending.RemoveIfNotNewer(m.pendingDir, e.SessionID, e.AtMs)
			caught++
		} else {
			log.Printf("[agent] pending statuslines: the reading of %s is not on disk yet; its file stays for the next boot", e.SessionID)
		}
	}
	log.Printf("[agent] boot: caught %d session reading(s) up with statusline payloads lost while the daemon was down", caught)
}

// readingPersisted says whether sid's stored reading, at least as new as at, is on disk. A module with no store has nowhere to
// persist, so nothing is waiting on a flush.
func (m *Module) readingPersisted(sid string, at int64) bool {
	if m.usage == nil {
		return true
	}
	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()
	return m.usagePersistedAt[sid] >= at
}
