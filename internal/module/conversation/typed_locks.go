package conversation

import (
	"context"
	"sync"
	"time"
)

// sidLocks is a keyed lock with a bounded wait: never a hang. The module holds two sets: one keyed by the Claude Code session id
// (shared by the typed route and the older conversation submit route, M1) and one keyed by the tmux pane id (the typed route
// only: two sessions' requests must not interleave their keystrokes in one pane). Order: the pane first, then the session.
type sidLocks struct {
	mu sync.Mutex
	m  map[string]*sidLock
}

type sidLock struct {
	ch   chan struct{}
	refs int
}

func (l *sidLocks) acquire(ctx context.Context, key string, wait time.Duration) (release func(), ok bool) {
	l.mu.Lock()
	if l.m == nil {
		l.m = map[string]*sidLock{}
	}
	e := l.m[key]
	if e == nil {
		e = &sidLock{ch: make(chan struct{}, 1)}
		l.m[key] = e
	}
	e.refs++
	l.mu.Unlock()
	drop := func() {
		l.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(l.m, key)
		}
		l.mu.Unlock()
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case e.ch <- struct{}{}:
		return func() { <-e.ch; drop() }, true
	case <-t.C:
	case <-ctx.Done():
	}
	drop()
	return nil, false
}

// lockPaneAndSession takes the pane lock, then the session lock, each with the same bounded wait.
func (m *Module) lockPaneAndSession(ctx context.Context, paneID, sessionID string, wait time.Duration) (release func(), ok bool) {
	rp, ok := m.paneLocks.acquire(ctx, paneID, wait)
	if !ok {
		return nil, false
	}
	rs, ok := m.locks.acquire(ctx, sessionID, wait)
	if !ok {
		rp()
		return nil, false
	}
	return func() { rs(); rp() }, true
}
