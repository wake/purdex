package watchmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/watch"
)

// Reconcile is tested at its two seams (the tmux lookup of pdx-watch and the agent module's owner resolver), faked; no test here
// touches a tmux server. One test registers the real agent module's types in the registry, to prove the wiring.

type fakePanes struct {
	mu        sync.Mutex
	sid, pane string
	found     bool
	err       error
	calls     int
}

func (f *fakePanes) Find(context.Context) (string, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.sid, f.pane, f.found, f.err
}

func (f *fakePanes) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeOwners struct {
	mu    sync.Mutex
	owner agent.PaneOwner
	ok    bool
	err   error
	codes []string
}

func (f *fakeOwners) ResolveSessionOwner(_ context.Context, code string) (agent.PaneOwner, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codes = append(f.codes, code)
	return f.owner, f.ok, f.err
}

func (f *fakeOwners) set(o agent.PaneOwner, ok bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owner, f.ok, f.err = o, ok, err
}

// watcherOwner is a Claude Code root of pane %7 with everything reconcile records.
func watcherOwner(frame string) agent.PaneOwner {
	return agent.PaneOwner{FrameID: frame, AgentType: "cc", SessionID: "sess-" + frame, TmuxPaneID: "%7",
		PID: 4242, ProcessStartTime: "Fri Oct 16 12:00:00 2026", TranscriptPath: "/t/" + frame + ".jsonl"}
}

type recRig struct {
	*testRig
	panes  *fakePanes
	owners *fakeOwners
	now    time.Time
}

func newRecRig(t *testing.T) *recRig {
	t.Helper()
	r := &recRig{testRig: newRig(t), panes: &fakePanes{sid: "$3", pane: "%7", found: true}, owners: &fakeOwners{}}
	r.now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	r.m.panes, r.m.ownerSrc = r.panes, r.owners
	r.m.now = func() time.Time { return r.now }
	return r
}

func (r *recRig) reconcile(t *testing.T) (watch.Holder, bool) {
	t.Helper()
	h, reg, err := r.m.Reconcile(context.Background())
	require.NoError(t, err)
	return h, reg
}

func TestReconcile_RegistersAClaudeCodeRootOfTheFirstPane(t *testing.T) {
	r := newRecRig(t)
	var announced []watch.Holder
	r.m.rec.registered = func(h watch.Holder) { announced = append(announced, h) }
	r.owners.set(watcherOwner("f1"), true, nil)

	h, reg := r.reconcile(t)
	require.True(t, reg)
	require.Equal(t, watch.HolderAwake, h.State)
	require.Equal(t, "sess-f1", h.SessionID)
	require.Equal(t, "f1", h.FrameID)
	require.Equal(t, "%7", h.PaneID)
	require.Equal(t, "$3", h.TmuxSessionID)
	require.Equal(t, 4242, h.PID)
	require.Equal(t, "Fri Oct 16 12:00:00 2026", h.ProcessStartTime)
	require.Equal(t, "/t/f1.jsonl", h.TranscriptPath)
	require.True(t, h.Since.Equal(r.now))
	wantCode, err := session.EncodeSessionID("$3")
	require.NoError(t, err)
	require.Equal(t, []string{wantCode}, r.owners.codes, "the resolver is asked about the session code of $3")

	// the same frame again: since is kept, nothing is announced
	r.now = r.now.Add(time.Hour)
	h2, reg := r.reconcile(t)
	require.False(t, reg)
	require.True(t, h2.Since.Equal(h.Since), "since is kept for the same frame")

	// a new frame (the watcher was restarted): since is now, announced again
	r.owners.set(watcherOwner("f2"), true, nil)
	h3, reg := r.reconcile(t)
	require.True(t, reg)
	require.True(t, h3.Since.Equal(r.now))
	require.Equal(t, "f2", h3.FrameID)
	require.Len(t, announced, 2, "announced once per new frame")
	require.Equal(t, "f2", announced[1].FrameID)
}

// Spec §12.3: only a live Claude Code root frame in the session's first pane whose session id is set is the watcher.
func TestReconcile_RegistersNothingForAnythingElse(t *testing.T) {
	for name, tc := range map[string]struct {
		owner agent.PaneOwner
		ok    bool
	}{
		"a codex agent":          {agent.PaneOwner{FrameID: "f", AgentType: "codex", SessionID: "s", TmuxPaneID: "%7"}, true},
		"another pane":           {agent.PaneOwner{FrameID: "f", AgentType: "cc", SessionID: "s", TmuxPaneID: "%8"}, true},
		"no session id":          {agent.PaneOwner{FrameID: "f", AgentType: "cc", TmuxPaneID: "%7"}, true},
		"no owner at all":        {agent.PaneOwner{}, false},
		"an owner that is empty": {agent.PaneOwner{AgentType: "cc", SessionID: "s", TmuxPaneID: "%7"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRecRig(t)
			called := false
			r.m.rec.registered = func(watch.Holder) { called = true }
			r.owners.set(tc.owner, tc.ok, nil)
			h, reg := r.reconcile(t)
			require.False(t, reg)
			require.False(t, called)
			require.Equal(t, watch.HolderNone, h.State)
		})
	}
}

// A late SessionStart of an earlier run does not replace the current frame: reconcile asks the agent module for the owner NOW, so
// what an event says does not matter.
func TestReconcile_TheEventIsOnlyATrigger(t *testing.T) {
	r := newRecRig(t)
	r.owners.set(watcherOwner("current"), true, nil)
	r.reconcile(t)
	h, reg := r.reconcile(t) // a late event of an earlier run triggers another reconcile; the answer is still the current frame
	require.False(t, reg)
	require.Equal(t, "current", h.FrameID)
}

func TestReconcile_ALookupThatFailsChangesNothing(t *testing.T) {
	r := newRecRig(t)
	r.owners.set(watcherOwner("f1"), true, nil)
	r.reconcile(t)
	before := r.m.holder.Get()

	r.owners.set(agent.PaneOwner{}, false, errors.New("resolver timeout"))
	h, reg, err := r.m.Reconcile(context.Background())
	require.Error(t, err)
	require.False(t, reg)
	require.Equal(t, before, h, "an owner lookup that failed is not 'no agent'")
	require.Equal(t, before, r.m.holder.Get())

	r.panes.mu.Lock()
	r.panes.err = errors.New("tmux unreadable")
	r.panes.mu.Unlock()
	_, _, err = r.m.Reconcile(context.Background())
	require.Error(t, err)
	require.Equal(t, before, r.m.holder.Get(), "nor is a tmux lookup that failed")
}

func TestReconcile_AnAgentThatEndedRestsAndKeepsItsSession(t *testing.T) {
	for name, gone := range map[string]func(r *recRig){
		"the agent is gone from the pane": func(r *recRig) { r.owners.set(agent.PaneOwner{}, false, nil) },
		"the tmux session is gone": func(r *recRig) {
			r.panes.mu.Lock()
			r.panes.found = false
			r.panes.mu.Unlock()
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRecRig(t)
			r.owners.set(watcherOwner("f1"), true, nil)
			r.reconcile(t)
			r.now = r.now.Add(10 * time.Minute)
			gone(r)
			h, reg := r.reconcile(t)
			require.False(t, reg)
			require.Equal(t, watch.HolderResting, h.State)
			require.Equal(t, "sess-f1", h.SessionID, "the session id is kept for the next wake")
			require.Equal(t, "/t/f1.jsonl", h.TranscriptPath, "so is its transcript")
			require.Empty(t, h.FrameID+h.PaneID+h.TmuxSessionID+h.ProcessStartTime)
			require.Zero(t, h.PID)
			require.True(t, h.Since.Equal(r.now), "since is when it entered resting")

			// resting stays resting, with its since
			r.now = r.now.Add(time.Hour)
			h2, _ := r.reconcile(t)
			require.Equal(t, h, h2)
		})
	}
}

func TestReconcile_NoWatcherAndNoHolderStaysNone(t *testing.T) {
	r := newRecRig(t)
	r.panes.found = false
	h, reg := r.reconcile(t)
	require.False(t, reg)
	require.Equal(t, watch.HolderNone, h.State)
}

// The 10-16 transitional watcher, started by hand before this daemon, is registered by Start without a restart of the watcher; one
// that ended while the daemon was down reads as resting with its id.
func TestStart_RegistersAWatcherThatWasAlreadyRunning(t *testing.T) {
	r := newRecRig(t)
	r.owners.set(watcherOwner("manual"), true, nil)
	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	h := r.m.holder.Get()
	require.Equal(t, watch.HolderAwake, h.State)
	require.Equal(t, "manual", h.FrameID)
}

func TestStart_AWatcherThatEndedWhileTheDaemonWasDownIsResting(t *testing.T) {
	r := newRecRig(t)
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) {
		h.State, h.SessionID, h.FrameID, h.PaneID = watch.HolderAwake, "sess-old", "old", "%7"
	}))
	r.owners.set(agent.PaneOwner{}, false, nil)
	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	h := r.m.holder.Get()
	require.Equal(t, watch.HolderResting, h.State)
	require.Equal(t, "sess-old", h.SessionID)
}

// An agent that ends while the daemon runs is seen at the next holder read past 30 s (no SessionEnd), not before.
func TestReads_ReconcileOnlyWhenTheLastOneIsOlderThan30s(t *testing.T) {
	r := newRecRig(t)
	r.owners.set(watcherOwner("f1"), true, nil)
	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	require.Equal(t, watch.HolderAwake, r.m.holder.Get().State)
	started := r.panes.count()

	get := func() HolderView {
		rr := httptest.NewRecorder()
		r.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/watch/holder", nil))
		require.Equal(t, http.StatusOK, rr.Code)
		var v HolderView
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &v))
		return v
	}

	r.owners.set(agent.PaneOwner{}, false, nil) // the agent ended
	r.now = r.now.Add(10 * time.Second)
	require.Equal(t, "awake", get().State, "within 30 s the read does not look again")
	require.Equal(t, started, r.panes.count())

	r.now = r.now.Add(25 * time.Second) // 35 s after the last reconcile
	require.Equal(t, "resting", get().State, "past 30 s the read reconciles first")
	require.Equal(t, started+1, r.panes.count())

	require.Equal(t, "resting", get().State)
	require.Equal(t, started+1, r.panes.count(), "and the next read within 30 s does not reconcile again")
}

// Before Start (and after Stop) a read never reconciles: tests and tools that build the module without starting it touch no tmux.
func TestReads_DoNotReconcileBeforeStart(t *testing.T) {
	r := newRecRig(t)
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/watch/holder", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	require.Zero(t, r.panes.count())
}

type fakeSessions struct {
	mu           sync.Mutex
	fn           func(agent.SessionStartEvent)
	unsubscribed bool
}

func (f *fakeSessions) LiveBySessionID(context.Context, string, string) ([]agent.TerminalSession, error) {
	return nil, nil
}
func (f *fakeSessions) LiveSessions(context.Context, string) ([]agent.TerminalSession, error) {
	return nil, nil
}
func (f *fakeSessions) SubscribeSessionStart(fn func(agent.SessionStartEvent)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fn = fn
	return func() {
		f.mu.Lock()
		f.unsubscribed = true
		f.mu.Unlock()
	}
}
func (f *fakeSessions) SubscribeTurnEnd(func(agent.TurnEndEvent)) func() { return func() {} }

// A verified SessionStart from pdx-watch reconciles, and so does an overflow of the subscription; one from any other tmux session
// does not.
func TestSessionStart_OnlyPdxWatchAndOverflowReconcile(t *testing.T) {
	r := newRecRig(t)
	ts := &fakeSessions{}
	r.m.core.Registry.Register(agent.TerminalSessionsKey, agent.TerminalSessions(ts))
	r.owners.set(watcherOwner("f1"), true, nil)
	require.NoError(t, r.m.Start(context.Background()))
	require.NotNil(t, ts.fn, "Start subscribes to the agent module's SessionStart")
	base := r.panes.count()

	ts.fn(agent.SessionStartEvent{AgentType: "cc", SessionID: "x", TmuxSession: "some-other-session"})
	require.Equal(t, base, r.panes.count(), "another tmux session is none of its business")

	ts.fn(agent.SessionStartEvent{AgentType: "cc", SessionID: "sess-f1", TmuxSession: watch.PaneSession})
	require.Equal(t, base+1, r.panes.count())

	ts.fn(agent.SessionStartEvent{Overflow: true})
	require.Equal(t, base+2, r.panes.count(), "an overflow means: look again")

	require.NoError(t, r.m.Stop(context.Background()))
	require.True(t, ts.unsubscribed)
}

func TestStart_TheTickerReconcilesWithoutAnyRead(t *testing.T) {
	r := newRecRig(t)
	r.m.tickEvery = 10 * time.Millisecond
	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	require.Eventually(t, func() bool { return r.panes.count() >= 4 }, 3*time.Second, 5*time.Millisecond)
}

// The wiring with the agent module's real types: what agent.Init registers under OwnerResolverKey and TerminalSessionsKey satisfies
// what this module asks of them, so Start subscribes and Reconcile reaches the resolver through the registry (not through a test
// seam). The real module here has no session provider, so it answers "no owner" and nothing is registered.
func TestWiring_TheRealAgentModulesServicesAreWhatReconcileUses(t *testing.T) {
	r := newRecRig(t)
	r.m.ownerSrc = nil // the registry, as in production
	am, err := agent.New(nil)
	require.NoError(t, err)
	r.m.core.Registry.Register(agent.OwnerResolverKey, agent.OwnerResolver(am))
	r.m.core.Registry.Register(agent.TerminalSessionsKey, agent.TerminalSessions(am))

	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	o, err := r.m.owners()
	require.NoError(t, err)
	require.Same(t, am, o.(*agent.Module), "the module asks the agent module itself")

	_, reg, err := r.m.Reconcile(context.Background())
	require.NoError(t, err, "a bare agent module answers 'no owner'")
	require.False(t, reg)
	require.Equal(t, watch.HolderNone, r.m.holder.Get().State)

	// and with nothing registered the reconcile says why instead of guessing
	r2 := newRecRig(t)
	r2.m.ownerSrc = nil
	_, _, err = r2.m.Reconcile(context.Background())
	require.ErrorIs(t, err, errNoOwnerLookup)
}
