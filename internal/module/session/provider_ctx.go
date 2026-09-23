package session

import "context"

// ContextSessionGetter is the context-aware single-session read the
// production provider (*SessionModule) offers alongside SessionProvider. It is
// an optional interface — asserted, as the peers module does for
// ListSessionsContext — so the SessionProvider contract (and its many fakes)
// stays unchanged (#1293).
type ContextSessionGetter interface {
	GetSessionContext(ctx context.Context, code string) (*SessionInfo, error)
}

// The production provider offers it; a rename there must not silently drop
// every HTTP caller back to reads its request cannot end.
var _ ContextSessionGetter = (*SessionModule)(nil)

// GetSessionWithin reads one session under ctx — an HTTP handler passes
// r.Context(), so a client that gives up ends a stuck tmux read at once
// instead of leaving it to run out listReadTimeout. A provider without
// GetSessionContext is read with GetSession (which still caps its own read).
func GetSessionWithin(ctx context.Context, p SessionProvider, code string) (*SessionInfo, error) {
	if g, ok := p.(ContextSessionGetter); ok {
		return g.GetSessionContext(ctx, code)
	}
	return p.GetSession(code)
}
