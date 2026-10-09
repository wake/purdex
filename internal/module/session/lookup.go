package session

import (
	"context"
	"time"
)

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
//
// A refresh runs under a fresh listReadTimeout budget; callers that hold a
// context use LookupCodeByNameContext.
func (m *SessionModule) LookupCodeByName(name string) (string, bool) {
	return m.LookupCodeByNameContext(context.Background(), name)
}

// LookupCodeByNameContext is LookupCodeByName whose cache refresh is bounded
// by ctx, capped at listReadTimeout (#1293). The refresh runs under
// nameCacheMu, so ending it with the caller's context also frees the lock.
// A refresh ended that way is a miss ("", false) and leaves the cache as it
// was.
func (m *SessionModule) LookupCodeByNameContext(ctx context.Context, name string) (string, bool) {
	m.nameCacheMu.Lock()
	defer m.nameCacheMu.Unlock()

	if time.Since(m.nameCacheAt) < nameCacheTTL && m.nameCacheData != nil {
		code, ok := m.nameCacheData[name]
		return code, ok
	}

	// Bounded like every session-list read (#1293): this runs under
	// nameCacheMu on the hook hot path.
	ctx, cancel := context.WithTimeout(ctx, m.readTimeout())
	defer cancel()
	sessions, err := m.tmux.ListSessions(ctx)
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

// SessionRef is one tmux session as SessionsByName reports it.
type SessionRef struct {
	Code    string
	Created int64 // tmux #{session_created}, unix seconds; 0 = unknown
}

// SessionsByName is every tmux session's code and creation time, keyed by name, from ONE `tmux list-sessions` call bounded
// by ctx (and by listReadTimeout). It is not cached and takes no lock, so a caller with a short budget (the push sender)
// can never wait on someone else's refresh. A name is the key, so a session killed and recreated under the same name shows
// the new session's code and creation time: callers that must tell the two apart compare Created.
func (m *SessionModule) SessionsByName(ctx context.Context) (map[string]SessionRef, error) {
	ctx, cancel := context.WithTimeout(ctx, m.readTimeout())
	defer cancel()
	sessions, err := m.tmux.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]SessionRef, len(sessions))
	for _, s := range sessions {
		code, err := EncodeSessionID(s.ID)
		if err != nil {
			continue
		}
		out[s.Name] = SessionRef{Code: code, Created: s.Created}
	}
	return out, nil
}
