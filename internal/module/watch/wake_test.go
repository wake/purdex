package watchmod

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/watch"
)

func TestWakeWatcher_AnAwakeLocalWatcherIsToldTheReason(t *testing.T) {
	r := newSessionRig(t, "")
	r.owners.set(watcherOwner("f1"), true, nil)
	_, _, _ = r.m.Reconcile(context.Background())
	fn := &fakeNotifier{}
	r.m.dlv.notifier = fn
	require.NoError(t, r.m.WakeWatcher(context.Background(), "unattended_changed", "無人值守模式 → 開（by app）"))
	require.Equal(t, []string{"[負載守門] 叫醒：無人值守開關改變｜無人值守模式 → 開（by app）"}, fn.texts)
	require.Empty(t, r.typer.typed(), "an awake watcher is not started again")
}

func TestWakeWatcher_ARestingWatcherIsWokenFirstThenToldWhy(t *testing.T) {
	r := newSessionRig(t, "f1")
	fn := &fakeNotifier{}
	r.m.dlv.notifier = fn
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) {
		h.State, h.SessionID = watch.HolderResting, "11111111-2222-3333-4444-555555555555"
	}))
	require.NoError(t, r.m.WakeWatcher(context.Background(), "patrol", ""))
	require.Len(t, r.typer.typed(), 1, "the wake was typed")
	require.Equal(t, []string{"[負載守門] 叫醒：例行巡檢"}, fn.texts)
}

func TestWakeWatcher_BadArgumentsAndNoWatcherWithAutoWakeOffAreErrors(t *testing.T) {
	r := newSessionRig(t, "f1")
	require.Error(t, r.m.WakeWatcher(context.Background(), "Bad Reason", ""))
	require.Error(t, r.m.WakeWatcher(context.Background(), "patrol", "a\nb"))
	require.Empty(t, r.typer.typed())
}

func TestWakeWatcher_AWatcherOnAnotherHostIsToldThroughThePeerRoute(t *testing.T) {
	r, fc := askerRig(t)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26", Targets: []string{"air26", "mlab"}})
	fc.reply[pathNote] = peersmod.CallResult{Class: peersmod.ClassDone, Status: 200, Body: []byte(`{"told":true}`)}
	require.NoError(t, r.m.WakeWatcher(context.Background(), "disk_low", "剩 9 GB"))
	require.Contains(t, fc.calls, peerID+" "+pathNote)
}

func TestPeerNoteRoute_TheHostsWatcherIsToldAndTheRulesOfTheHolderRouteHold(t *testing.T) {
	r := newSessionRig(t, "")
	r.owners.set(watcherOwner("f1"), true, nil)
	_, _, _ = r.m.Reconcile(context.Background())
	fn := &fakeNotifier{}
	r.m.dlv.notifier = fn
	body := `{"to_host_id":"` + ourID + `","reason":"patrol","detail":"d"}`
	rr := r.serve(hostCtx(), "/api/peers/watch/note", body)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `"host_id":"`+ourID+`"`, "HostCaller wants this host's id in every 2xx")
	require.Equal(t, []string{"[負載守門] 叫醒：例行巡檢｜d"}, fn.texts)
	require.Equal(t, http.StatusForbidden, r.serve(adminCtx, "/api/peers/watch/note", body).Code, "not a host principal")
	require.Equal(t, http.StatusConflict, r.serve(hostCtx(), "/api/peers/watch/note", strings.Replace(body, ourID, "other", 1)).Code, "another host")
	require.Equal(t, http.StatusBadRequest, r.serve(hostCtx(), "/api/peers/watch/note", `{"to_host_id":"`+ourID+`","reason":"BAD"}`).Code)
	require.Len(t, fn.texts, 1)

	r.owners.set(watcherOwner("f1"), false, nil)
	r.now = r.now.Add(time.Minute) // the holder read is reconciled again after 30 s
	require.Equal(t, http.StatusConflict, r.serve(hostCtx(), "/api/peers/watch/note", body).Code, "no watcher awake here")
}

// The service the other modules look up is there once the module is initialised (a real module, not a stand-in).
func TestWakeWatcher_TheModuleRegistersItsWaker(t *testing.T) {
	r := newRig(t)
	svc, ok := r.m.core.Registry.Get(watch.WakerKey)
	require.True(t, ok)
	_, isWaker := svc.(watch.Waker)
	require.True(t, isWaker)
}

// A start that outlasts its grace is waited for: the note goes to the watcher that comes up, not nowhere.
func TestWakeWatcher_AStartThatOutlastsItsGraceStillGetsTheNote(t *testing.T) {
	r := newSessionRig(t, "")
	fn := &fakeNotifier{}
	r.m.dlv.notifier = fn
	r.m.startGrace = 20 * time.Millisecond
	r.typer.onType = func(string) {
		go func() { time.Sleep(150 * time.Millisecond); r.owners.set(watcherOwner("f1"), true, nil) }()
	}
	require.NoError(t, r.m.WakeWatcher(context.Background(), "patrol", ""))
	require.Equal(t, []string{"[負載守門] 叫醒：例行巡檢"}, fn.texts)
	require.Len(t, r.typer.typed(), 1, "one start, not one per poll")
}

// A host that says "no watcher here" makes the locator look again; a lost answer does not (the note may have arrived: not sent twice,
// the watcher not forgotten, no second one started).
func TestWakeWatcher_ARefusalMakesTheLocatorLookAgainAndALostAnswerDoesNot(t *testing.T) {
	count := func(fc *fakeCaller, path string) int {
		n := 0
		for _, c := range fc.calls {
			if c == peerID+" "+path {
				n++
			}
		}
		return n
	}
	r, fc := askerRig(t)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26", Targets: []string{"air26", "mlab"}})
	fc.reply[pathNote] = peersmod.CallResult{Class: peersmod.ClassRefused, Status: 409, Code: "no_watcher"}
	require.Error(t, r.m.WakeWatcher(context.Background(), "patrol", ""))
	require.Equal(t, 3, count(fc, pathNote), "three tries, each after the locator forgot the watcher")
	require.GreaterOrEqual(t, count(fc, pathHolder), 3, "and asked the host again each time")

	r2, fc2 := askerRig(t)
	fc2.reply[pathHolder] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26", Targets: []string{"air26", "mlab"}})
	fc2.reply[pathNote] = peersmod.CallResult{Class: peersmod.ClassTransient, Status: 502}
	require.Error(t, r2.m.WakeWatcher(context.Background(), "patrol", ""))
	require.Equal(t, 1, count(fc2, pathNote), "the answer was lost: the note is not sent again")
	cur, _ := r2.m.loc.Remembered()
	require.NotNil(t, cur, "and the watcher is still the remembered one")
}
