package session

import "time"

// nameCacheTTL bounds how long a stale name→code mapping can be observed
// after an external mutation (tmux rename / kill / new outside the daemon's
// HTTP handlers) before the next refresh. The watcher path invalidates
// proactively via wait-for, but a hook arriving in the gap between the
// mutation and the watcher catching up still relies on this TTL as the
// upper bound. 250ms keeps user-perceived staleness invisible while leaving
// the cache hot for the 1+7×S avoidance that motivated this fast path.
const nameCacheTTL = 250 * time.Millisecond

// LookupCodeByName returns the session code for a tmux session name using a
// short-TTL cache built from a single `tmux list-sessions` call. It
// deliberately avoids the meta merge / pane metadata fan-out that
// ListSessions performs, because the hook hot path only needs name→code
// resolution and was paying 1+7×S tmux subprocesses per event.
func (m *SessionModule) LookupCodeByName(name string) (string, bool) {
	m.nameCacheMu.Lock()
	defer m.nameCacheMu.Unlock()

	if time.Since(m.nameCacheAt) < nameCacheTTL && m.nameCacheData != nil {
		code, ok := m.nameCacheData[name]
		return code, ok
	}

	sessions, err := m.tmux.ListSessions()
	if err != nil {
		return "", false
	}

	next := make(map[string]string, len(sessions))
	for _, s := range sessions {
		code, err := EncodeSessionID(s.ID)
		if err != nil {
			continue
		}
		next[s.Name] = code
	}
	m.nameCacheData = next
	m.nameCacheAt = time.Now()

	code, ok := next[name]
	return code, ok
}

// invalidateNameCache forces the next LookupCodeByName call to refresh from
// tmux. Call sites: handleCreate / handleRename / handleDelete success
// branches — the only mutation paths that change the name→code mapping.
func (m *SessionModule) invalidateNameCache() {
	m.nameCacheMu.Lock()
	m.nameCacheAt = time.Time{}
	m.nameCacheMu.Unlock()
}
