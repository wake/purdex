package peers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
)

// errFakeProvider is the sentinel error fakeSessions.ListSessions returns
// when configured to fail, so tests can assert on a stable message.
var errFakeProvider = errors.New("fake session provider failure")

// fakeSessions is a test double implementing session.SessionProvider. Only
// ListSessions is exercised by the peers module; the rest exist to satisfy
// the interface.
type fakeSessions struct {
	// mu guards sessions, so a test can rename a live tmux session between
	// two requests to a running httptest server without racing the handler
	// goroutine that is reading the inventory. Tests that only set it at
	// construction never touch mu.
	mu       sync.Mutex
	sessions []session.SessionInfo
	err      error
	// instances, when non-nil, is a sequence consumed one entry per
	// TmuxInstance() call so tests can drive a "sampled before / sampled
	// after" generation mismatch; once exhausted it keeps returning the
	// final entry. When nil, TmuxInstance() returns "" (unknown), as before.
	instances    []string
	instanceCall int
	// listCalls counts ListSessions calls — the first thing localEnvelope
	// does — so a test can assert an inventory was (not) built.
	listCalls atomic.Int32
	// blockList makes ListSessionsContext hang until its context ends.
	blockList bool
	// blockInstance models a hung tmux-instance probe: TmuxInstanceContext
	// hangs until its context ends, and the context-free TmuxInstance hangs
	// for instanceHang (the real probe's own cap) — so a caller that probes
	// without the inventory's context pays that on every probe.
	blockInstance bool
	instanceHang  time.Duration
	// blockSecondInstance hangs only the second TmuxInstanceContext call
	// (the post-owner-resolution generation re-check) until its context
	// ends; the first probe answers normally.
	blockSecondInstance bool
	// instanceCtxCalls counts TmuxInstanceContext calls.
	instanceCtxCalls atomic.Int32
}

// TmuxInstanceContext is the context-aware probe localEnvelope uses (#1293).
func (f *fakeSessions) TmuxInstanceContext(ctx context.Context) string {
	n := f.instanceCtxCalls.Add(1)
	if f.blockInstance || (f.blockSecondInstance && n == 2) {
		<-ctx.Done()
		return ""
	}
	return f.TmuxInstance()
}

// setSessions replaces the live tmux inventory this fake reports — a tmux
// rename, from the daemon's point of view.
func (f *fakeSessions) setSessions(sessions []session.SessionInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = sessions
}

// ListSessionsContext is what localEnvelope calls (#1293): with blockList set
// it models a hung tmux read that only the context ends.
func (f *fakeSessions) ListSessionsContext(ctx context.Context) ([]session.SessionInfo, error) {
	if f.blockList {
		f.listCalls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.ListSessions()
}

func (f *fakeSessions) ListSessions() ([]session.SessionInfo, error) {
	f.listCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

func (f *fakeSessions) GetSession(code string) (*session.SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeSessions) SessionExists(string) bool { return false }
func (f *fakeSessions) ValidateCwd(string) error  { return nil }
func (f *fakeSessions) CreateSession(string, string) (*session.SessionInfo, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeSessions) TmuxInstance() string {
	if f.blockInstance {
		time.Sleep(f.instanceHang)
		return ""
	}
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
	// delay, when non-zero, is slept at the start of every
	// ResolveSessionOwner call — for Item 4's overlap test, simulating a
	// slow local resolver alongside a slow remote fetch.
	delay time.Duration
	// onResolveStart, when set, is invoked at the very start of every
	// ResolveSessionOwner call, before the delay sleep — so a test can
	// record exactly when the (possibly slow) local resolution began,
	// to assert ordering against a concurrent remote fetch's own start.
	onResolveStart func()
}

func (f *fakeOwners) ResolveSessionOwner(ctx context.Context, code string) (agent.PaneOwner, bool, error) {
	if f.onResolveStart != nil {
		f.onResolveStart()
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
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

// ctxRecordingOwners is an agent.OwnerResolver that hands every call's
// context to record and reports no owner.
type ctxRecordingOwners struct {
	record func(ctx context.Context)
}

func (o *ctxRecordingOwners) ResolveSessionOwner(ctx context.Context, _ string) (agent.PaneOwner, bool, error) {
	o.record(ctx)
	return agent.PaneOwner{}, false, nil
}

// livenessView is an agent.ProcessView that answers from a fake Liveness, the
// way the test module's pass view does: Read is the fake's Info, or, for a
// fake without one, a ProcessInfo carrying its StartTime. Every PID is in the
// view, so the inventory's registry read gets exactly the fake's answers.
type livenessView struct {
	l ipeers.Liveness
}

func (v livenessView) Alive(pid int) bool { return v.l.PidAlive(pid) }

func (v livenessView) Read(pid int) (iagent.ProcessInfo, error) {
	if v.l.Info != nil {
		return v.l.Info(pid)
	}
	start, err := v.l.StartTime(pid)
	return iagent.ProcessInfo{PID: pid, StartTime: start}, err
}

func (v livenessView) StartTime(pid int) (string, error) {
	info, err := v.Read(pid)
	if err != nil {
		return "", err
	}
	return info.StartTime.Format("Mon Jan _2 15:04:05 2006"), nil
}

func (v livenessView) PPID(pid int) (int, error) {
	info, err := v.Read(pid)
	if err != nil {
		return 0, err
	}
	return info.PPID, nil
}

// mapView is an agent.ProcessView over fixed answers: Read returns errs[pid]
// when set, else infos[pid], and any other PID is not in the view
// (ErrNotInSnapshot), as with a real snapshot. reads counts Read calls.
type mapView struct {
	infos map[int]iagent.ProcessInfo
	errs  map[int]error
	reads int
}

func (v *mapView) Alive(pid int) bool {
	_, ok := v.infos[pid]
	return ok
}

func (v *mapView) Read(pid int) (iagent.ProcessInfo, error) {
	v.reads++
	if err, ok := v.errs[pid]; ok {
		return iagent.ProcessInfo{}, err
	}
	if info, ok := v.infos[pid]; ok {
		return info, nil
	}
	return iagent.ProcessInfo{}, fmt.Errorf("pid %d: %w", pid, iagent.ErrNotInSnapshot)
}

func (v *mapView) StartTime(pid int) (string, error) {
	info, err := v.Read(pid)
	if err != nil {
		return "", err
	}
	return info.StartTime.Format("Mon Jan _2 15:04:05 2006"), nil
}

func (v *mapView) PPID(pid int) (int, error) {
	info, err := v.Read(pid)
	if err != nil {
		return 0, err
	}
	return info.PPID, nil
}

// passOwners is an agent.OwnerPassResolver whose passes answer from results,
// by code (absent ⇒ "no owner"). NewOwnerPass records the ProcessSource it is
// handed and calls it once, as a real pass does on its first walk, keeping
// what it returned; a source that failed is every session's answer, as in a
// real pass. A pass's answers exist only in Confirm's map, so a caller that
// read them from anywhere else would find nothing. Confirm leaves the codes in
// omit out of its map. direct records ResolveSessionOwner calls, which an
// inventory with a pass must not make.
type passOwners struct {
	results map[string]agent.OwnerResult
	omit    map[string]bool

	srcs     []agent.ProcessSource
	views    []iagent.ProcessView
	viewErrs []error
	resolved []string
	confirms int
	direct   []string
}

func (o *passOwners) ResolveSessionOwner(_ context.Context, code string) (agent.PaneOwner, bool, error) {
	o.direct = append(o.direct, code)
	return agent.PaneOwner{}, false, errors.New("passOwners: per-session lookup")
}

func (o *passOwners) NewOwnerPass(src agent.ProcessSource) agent.OwnerPass {
	o.srcs = append(o.srcs, src)
	p := &fakePass{o: o}
	if src != nil {
		p.view, p.viewErr = src()
	}
	o.views = append(o.views, p.view)
	o.viewErrs = append(o.viewErrs, p.viewErr)
	return p
}

type fakePass struct {
	o       *passOwners
	view    iagent.ProcessView
	viewErr error
	codes   []string
}

func (p *fakePass) Resolve(_ context.Context, code string) {
	p.o.resolved = append(p.o.resolved, code)
	p.codes = append(p.codes, code)
}

func (p *fakePass) Confirm(context.Context) map[string]agent.OwnerResult {
	p.o.confirms++
	out := make(map[string]agent.OwnerResult, len(p.codes))
	for _, code := range p.codes {
		if p.o.omit[code] {
			continue
		}
		if p.viewErr != nil {
			out[code] = agent.OwnerResult{Err: p.viewErr}
			continue
		}
		out[code] = p.o.results[code]
	}
	return out
}
