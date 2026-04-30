package session

import "time"

const nameCacheTTL = time.Second

// LookupCodeByName returns the session code for a tmux session name using a
// 1s TTL cache built from a single `tmux list-sessions` call. It deliberately
// avoids the meta merge / pane metadata fan-out that ListSessions performs,
// because the hook hot path only needs name→code resolution and was paying
// 1+7×S tmux subprocesses per event.
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
