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
	// instances, when non-nil, is a sequence consumed one entry per
	// TmuxInstance() call so tests can drive a "sampled before / sampled
	// after" generation mismatch; once exhausted it keeps returning the
	// final entry. When nil, TmuxInstance() returns "" (unknown), as before.
	instances    []string
	instanceCall int
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

func (f *fakeSessions) TmuxInstance() string {
	if f.instances == nil {
		return ""
	}
	if f.instanceCall >= len(f.instances) {
		return f.instances[len(f.instances)-1]
	}
	v := f.instances[f.instanceCall]
	f.instanceCall++
	return v
}

// fakeOwners is a test double implementing agent.OwnerResolver, backed by a
// map keyed by session code. calls records every code passed to
// ResolveSessionOwner, in order, so tests can assert the resolver was (or
// was not) invoked for a given session. errs, when set for a code, makes
// ResolveSessionOwner report that code's lookup as failed (found=false,
// err=non-nil) instead of consulting owners — so tests can drive Item 1's
// "resolver failure" path without an owner entry masking it.
type fakeOwners struct {
	owners map[string]agent.PaneOwner
	errs   map[string]error
	calls  []string
}

func (f *fakeOwners) ResolveSessionOwner(ctx context.Context, code string) (agent.PaneOwner, bool, error) {
	f.calls = append(f.calls, code)
	if err, ok := f.errs[code]; ok {
		return agent.PaneOwner{}, false, err
	}
	owner, ok := f.owners[code]
	return owner, ok, nil
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
