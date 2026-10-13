package session

import "time"

// CachedSessionCodes is the set of session codes in the list this module already holds in memory (the one GET /api/sessions
// serves and refills), with no tmux read: ok is false when the list was never filled, has been invalidated since (a session
// was created, renamed or deleted, so it is no evidence of anything), or is older than maxAge. A caller that gets false must
// treat every code as unknown, not as absent.
func (m *SessionModule) CachedSessionCodes(maxAge time.Duration) (map[string]struct{}, bool) {
	m.listCacheMu.Lock()
	defer m.listCacheMu.Unlock()
	if m.listCacheData == nil || time.Since(m.listCacheAt) > maxAge {
		return nil, false
	}
	codes := make(map[string]struct{}, len(m.listCacheData))
	for _, s := range m.listCacheData {
		codes[s.Code] = struct{}{}
	}
	return codes, true
}
