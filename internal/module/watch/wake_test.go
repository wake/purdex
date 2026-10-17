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
	require.Equal(t, http.StatusOK, r.serve(hostCtx(), "/api/peers/watch/note", body).Code)
	require.Equal(t, []string{"[負載守門] 叫醒：例行巡檢｜d"}, fn.texts)
	require.Equal(t, http.StatusForbidden, r.serve(adminCtx, "/api/peers/watch/note", body).Code, "not a host principal")
	require.Equal(t, http.StatusConflict, r.serve(hostCtx(), "/api/peers/watch/note", strings.Replace(body, ourID, "other", 1)).Code, "another host")
	require.Equal(t, http.StatusBadRequest, r.serve(hostCtx(), "/api/peers/watch/note", `{"to_host_id":"`+ourID+`","reason":"BAD"}`).Code)
	require.Len(t, fn.texts, 1)

	r.owners.set(watcherOwner("f1"), false, nil)
	r.now = r.now.Add(time.Minute) // the holder read is reconciled again after 30 s
	require.Equal(t, http.StatusConflict, r.serve(hostCtx(), "/api/peers/watch/note", body).Code, "no watcher awake here")
}
