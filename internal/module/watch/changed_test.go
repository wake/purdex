package watchmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	peersmod "github.com/wake/purdex/internal/module/peers"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/watch"
)

type events struct {
	mu  sync.Mutex
	evs []core.HostEvent
}

func (e *events) add(ev core.HostEvent) { e.mu.Lock(); e.evs = append(e.evs, ev); e.mu.Unlock() }
func (e *events) n() int                { e.mu.Lock(); defer e.mu.Unlock(); return len(e.evs) }
func (e *events) last() ChangedPayload {
	e.mu.Lock()
	defer e.mu.Unlock()
	var p ChangedPayload
	_ = json.Unmarshal([]byte(e.evs[len(e.evs)-1].Value), &p)
	return p
}

func statusOf(t *testing.T, r *testRig) Status {
	t.Helper()
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/watch/status", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	var st Status
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &st))
	return st
}

// Spec §12.6: the status names the watcher events go to — awake, resting, or none (different answers) — and this host's holder
// with its pane ids.
func TestStatus_NamesTheWatcherAndThePaneIDs(t *testing.T) {
	r := newRecRig(t)
	r.m.core.Cfg.Peers.Alias = "mlab"

	st := statusOf(t, r.testRig)
	require.Nil(t, st.Watcher, "none")

	r.owners.set(watcherOwner("f1"), true, nil)
	r.reconcile(t)
	st = statusOf(t, r.testRig)
	require.NotNil(t, st.Watcher)
	require.Equal(t, WatcherView{HostID: ourID, Alias: "mlab", SessionID: "sess-f1", State: "awake", TmuxSession: "pdx-watch"}, *st.Watcher)
	require.Equal(t, "$3", st.Self.TmuxSessionID)
	require.Equal(t, "%7", st.Self.PaneID)

	r.owners.set(agent.PaneOwner{}, false, nil)
	r.reconcile(t)
	st = statusOf(t, r.testRig)
	require.NotNil(t, st.Watcher, "resting is not none")
	require.Equal(t, "resting", st.Watcher.State)
	require.Equal(t, "sess-f1", st.Watcher.SessionID)
	require.Empty(t, st.Self.PaneID, "no pane while it rests")
}

// A watcher the locator remembers on another host is the one the status names.
func TestStatus_ARememberedWatcherOnAnotherHost(t *testing.T) {
	r, fc := askerRig(t)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26", Since: time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)})
	_, err := r.m.loc.Current(context.Background(), []string{"air26"}, false)
	require.NoError(t, err)
	st := statusOf(t, r)
	require.NotNil(t, st.Watcher)
	require.Equal(t, peerID, st.Watcher.HostID)
	require.Equal(t, "air26", st.Watcher.Alias)
	require.Equal(t, "s-26", st.Watcher.SessionID)
	require.Equal(t, "awake", st.Watcher.State)
	require.Equal(t, "pdx-watch", st.Watcher.TmuxSession)
}

// watch.changed goes out once per change of those fields, and not for a reconcile that changed nothing or only the last turn or
// the last delivery.
func TestChanged_OncePerChangeAndNeverForTurnsOrDeliveries(t *testing.T) {
	r := newRecRig(t)
	ev := &events{}
	r.m.emit = ev.add
	require.Equal(t, 0, ev.n(), "the state the module starts with is not a change")

	r.reconcile(t) // nothing there, nothing held
	require.Equal(t, 0, ev.n(), "a reconcile that changes nothing sends nothing")

	r.owners.set(watcherOwner("f1"), true, nil)
	r.reconcile(t)
	require.Equal(t, 1, ev.n())
	p := ev.last()
	require.NotNil(t, p.Watcher)
	require.Equal(t, "awake", p.Watcher.State)
	require.Equal(t, "%7", p.Holder.PaneID)

	r.reconcile(t)
	require.Equal(t, 1, ev.n(), "the same watcher again")

	// the last turn and the last delivery moved: not a change of the watcher
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.LastTurn, h.LastDelivery = r.now, r.now }))
	r.reconcile(t)
	r.m.notifyChanged()
	require.Equal(t, 1, ev.n())

	// a new frame: a new session id, one notice
	r.owners.set(watcherOwner("f2"), true, nil)
	r.reconcile(t)
	require.Equal(t, 2, ev.n())
	require.Equal(t, "sess-f2", ev.last().Watcher.SessionID)

	// resting
	r.owners.set(agent.PaneOwner{}, false, nil)
	r.reconcile(t)
	require.Equal(t, 3, ev.n())
	require.Equal(t, "resting", ev.last().Watcher.State)
	require.Empty(t, ev.last().Holder.PaneID)
}

// A change of the remembered watcher (a failed delivery, a search that found another) is a change too.
func TestChanged_TheLocatorMovingTheWatcherIsAChange(t *testing.T) {
	r, fc := askerRig(t)
	ev := &events{}
	r.m.emit = ev.add
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26", Since: time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)})
	_, err := r.m.loc.Current(context.Background(), []string{"air26"}, false)
	require.NoError(t, err)
	require.Equal(t, 1, ev.n(), "the search found the watcher on air26")
	require.Equal(t, peerID, ev.last().Watcher.HostID)

	cur, _ := r.m.loc.Remembered()
	r.m.loc.Failed(*cur) // a delivery failed: the watcher is not known any more
	require.Equal(t, 2, ev.n())
	require.Nil(t, ev.last().Watcher, "none")
}

// A watcher that is already held when the module starts is the baseline; the first reconcile of Start that finds it awake is a change
// (the stored state said resting).
func TestChanged_StartPicksUpAWatcherAsAChange(t *testing.T) {
	r := newRecRig(t)
	ev := &events{}
	r.m.emit = ev.add
	r.owners.set(watcherOwner("manual"), true, nil)
	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	require.Equal(t, 1, ev.n())
	require.Equal(t, "awake", ev.last().Watcher.State)
}

// A notifier that is slow to get the notice lock does not send an older state after a newer one: the state is read inside the lock.
func TestChanged_ASlowNotifierDoesNotSendAnOlderStateAfterANewerOne(t *testing.T) {
	r := newRecRig(t)
	ev := &events{}
	r.m.emit = ev.add

	gate := make(chan struct{})
	reached := make(chan struct{})
	var once sync.Once
	r.m.beforeChgLock = func() {
		first := false
		once.Do(func() { first = true })
		if first {
			close(reached)
			<-gate // the first notifier stalls at the lock, as one that lost the race for it would
		}
	}

	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) {
		h.State, h.SessionID, h.FrameID, h.PaneID, h.TmuxSessionID = watch.HolderAwake, "sess-1", "f1", "%7", "$3"
	}))
	done := make(chan struct{})
	go func() { r.m.notifyChanged(); close(done) }()
	<-reached

	// meanwhile the holder moves on and a second notifier runs to the end
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.SessionID, h.FrameID = "sess-2", "f2" }))
	r.m.notifyChanged()
	require.Equal(t, "sess-2", ev.last().Watcher.SessionID)

	close(gate)
	<-done
	require.Equal(t, "sess-2", ev.last().Watcher.SessionID, "the stalled notifier did not send the older state after the newer one")
	r.m.chg.mu.Lock()
	var cur ChangedPayload
	_ = json.Unmarshal([]byte(r.m.chg.last), &cur)
	r.m.chg.mu.Unlock()
	require.Equal(t, "sess-2", cur.Watcher.SessionID, "and the comparison did not go back")
}

// Spec §12.4: a watcher on another host is known from the last search; its host does not say when it rests or goes. refreshRemote
// asks that host again, so the status and watch.changed follow it, and asks at most once per staleAfter.
func remoteRig(t *testing.T) (*recRig, *fakeCaller, *events) {
	t.Helper()
	r := newRecRig(t)
	r.m.core.Cfg.Peers.Alias = "mlab"
	r.m.core.Cfg.Peers.Hosts[0].Alias = "air26"
	fc := &fakeCaller{reply: map[string]peersmod.CallResult{}}
	r.m.caller = fc
	ev := &events{}
	r.m.emit = ev.add
	since := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26", Since: since})
	_, err := r.m.loc.Current(context.Background(), []string{"air26"}, false)
	require.NoError(t, err)
	require.Equal(t, 1, ev.n(), "the search found the watcher on air26")
	return r, fc, ev
}

func TestRefreshRemote_ARemoteWatcherThatRestsIsRestingInTheStatusWithOneChange(t *testing.T) {
	r, fc, ev := remoteRig(t)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "resting", SessionID: "s-26"})
	r.now = r.now.Add(staleAfter + time.Second)
	r.m.refreshRemote(context.Background())

	st := statusOf(t, r.testRig)
	require.NotNil(t, st.Watcher)
	require.Equal(t, "resting", st.Watcher.State)
	require.Equal(t, peerID, st.Watcher.HostID)
	require.Equal(t, 2, ev.n())
	require.Equal(t, "resting", ev.last().Watcher.State)
}

func TestRefreshRemote_ARemoteWatcherThatIsGoneIsNone(t *testing.T) {
	r, fc, ev := remoteRig(t)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "none"})
	r.now = r.now.Add(staleAfter + time.Second)
	r.m.refreshRemote(context.Background())

	require.Nil(t, statusOf(t, r.testRig).Watcher)
	require.Equal(t, 2, ev.n())
	require.Nil(t, ev.last().Watcher)
}

func TestRefreshRemote_ARemoteWatcherThatIsGoneWhileThisHostHoldsOneShowsThisHosts(t *testing.T) {
	r, fc, _ := remoteRig(t)
	r.owners.set(watcherOwner("mine"), true, nil)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "none"})
	r.now = r.now.Add(staleAfter + time.Second)
	r.m.refreshRemote(context.Background())
	r.reconcile(t)

	st := statusOf(t, r.testRig)
	require.NotNil(t, st.Watcher)
	require.Equal(t, ourID, st.Watcher.HostID)
}

func TestRefreshRemote_ARestingRemoteWatcherThatIsWokenIsAwakeAgain(t *testing.T) {
	r, fc, ev := remoteRig(t)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "resting", SessionID: "s-26"})
	r.now = r.now.Add(staleAfter + time.Second)
	r.m.refreshRemote(context.Background())
	require.Equal(t, 2, ev.n())

	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26", Since: r.now})
	r.now = r.now.Add(staleAfter + time.Second)
	r.m.refreshRemote(context.Background())
	require.Equal(t, "awake", statusOf(t, r.testRig).Watcher.State)
	require.Equal(t, 3, ev.n())
}

func TestRefreshRemote_AsksAtMostOncePerStaleAfter(t *testing.T) {
	r, fc, _ := remoteRig(t)
	n0 := len(fc.calls)
	r.now = r.now.Add(staleAfter + time.Second)
	for i := 0; i < 5; i++ {
		r.m.refreshRemote(context.Background())
	}
	require.Equal(t, n0+1, len(fc.calls), "one question inside the bound")
	r.now = r.now.Add(staleAfter + time.Second)
	r.m.refreshRemote(context.Background())
	require.Equal(t, n0+2, len(fc.calls))
}

func TestRefreshRemote_AHostThatCannotBeAskedLeavesWhatIsKnown(t *testing.T) {
	r, fc, ev := remoteRig(t)
	delete(fc.reply, pathHolder) // 404: an old daemon
	r.now = r.now.Add(staleAfter + time.Second)
	r.m.refreshRemote(context.Background())
	require.Equal(t, "awake", statusOf(t, r.testRig).Watcher.State)
	require.Equal(t, 1, ev.n())
}

// A status read of a running module refreshes the remote watcher itself (it is one of the holder reads that call fresh).
func TestRefreshRemote_AStatusReadRefreshesTheRemoteWatcher(t *testing.T) {
	r, fc, _ := remoteRig(t)
	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "resting", SessionID: "s-26"})
	r.now = r.now.Add(staleAfter + time.Second)

	st := statusOf(t, r.testRig)
	require.NotNil(t, st.Watcher)
	require.Equal(t, "resting", st.Watcher.State)
}
