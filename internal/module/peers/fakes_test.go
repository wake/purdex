package peers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
)

// errFakeProvider is the sentinel error fakeSessions.ListSessions returns
// when configured to fail, so tests can assert on a stable message.
var errFakeProvider = errors.New("fake session provider failure")

// fakeSessions is a test double implementing session.SessionProvider. Only
// ListSessions is exercised by the peers module; the rest exist to satisfy
// the interface.
type fakeSessions struct {
	sessions []session.SessionInfo
	err      error
}

func (f *fakeSessions) ListSessions() ([]session.SessionInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

func (f *fakeSessions) GetSession(code string) (*session.SessionInfo, error) {
	for _, s := range f.sessions {
		if s.Code == code {
			cp := s
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeSessions) UpdateMeta(code string, update session.MetaUpdate) error { return nil }

func (f *fakeSessions) HandleTerminalWS(w http.ResponseWriter, r *http.Request, code string) {}

func (f *fakeSessions) TmuxInstance() string { return "" }

// fakeOwners is a test double implementing agent.OwnerResolver, backed by a
// map keyed by session code. calls records every code passed to
// ResolveSessionOwner, in order, so tests can assert the resolver was (or
// was not) invoked for a given session.
type fakeOwners struct {
	owners map[string]agent.PaneOwner
	calls  []string
}

func (f *fakeOwners) ResolveSessionOwner(ctx context.Context, code string) (agent.PaneOwner, bool) {
	f.calls = append(f.calls, code)
	owner, ok := f.owners[code]
	return owner, ok
}

// fakeClock is a settable sequence of times. Each call to Now returns the
// next entry in the sequence; once exhausted, it keeps returning the final
// entry, so tests can drive the handler's "elapsed budget" checks
// deterministically without needing one entry per call.
type fakeClock struct {
	times []time.Time
	i     int
}

func (c *fakeClock) Now() time.Time {
	if c.i >= len(c.times) {
		return c.times[len(c.times)-1]
	}
	t := c.times[c.i]
	c.i++
	return t
}
