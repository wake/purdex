package watchmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/agent"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/watch"
)

// None of these tests sends to a live watcher: every daemon's WatcherNotifier is this fake, set before anything is delivered.
type fakeNotifier struct {
	mu    sync.Mutex
	texts []string
	err   error
}

func (f *fakeNotifier) Notify(_ context.Context, _ watch.Holder, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.texts = append(f.texts, text)
	return nil
}
func (f *fakeNotifier) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.texts) }
func (f *fakeNotifier) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.texts[len(f.texts)-1]
}

// withNotifiers are the two daemons' fake notifiers (newPair installs them before the module starts).
func withNotifiers(p *pair) (a, b *fakeNotifier) {
	return p.a.m.dlv.notifier.(*fakeNotifier), p.b.m.dlv.notifier.(*fakeNotifier)
}

var evSeq int

func evRecord(kind watch.Kind, host string) watch.Record {
	evSeq++
	return watch.Record{ID: host + "-" + strings.Repeat("0", 4-len(itoa(evSeq))) + itoa(evSeq), Kind: kind, Host: host, At: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
		Label: watch.LabelGateGap, Argv: []string{"vitest", "run"}, PeakCores: 4, PeakMB: 2048}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (d *daemon) pass(t *testing.T) { t.Helper(); d.m.deliverPass(context.Background()) }

func TestDelivery_AnEventGoesToTheWatcherOnThisHostOnce(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	na, _ := withNotifiers(p)
	p.a.register(t, "wa")
	rec := evRecord(watch.KindUnmanagedHeavy, "mlab")
	p.a.m.Enqueue(rec)
	p.a.pass(t)
	require.Equal(t, 1, na.count())
	require.Contains(t, na.last(), "[負載守門]")
	require.Contains(t, na.last(), rec.ID)
	require.Equal(t, 0, p.a.m.dlv.outbox.Pending())
	require.False(t, p.a.m.hs().Get().LastDelivery.IsZero(), "the rest countdown counts from a delivery")
	p.a.pass(t)
	require.Equal(t, 1, na.count(), "nothing more to send")
}

func TestDelivery_AnEventGoesToTheWatcherOnAnotherHostThroughThePeerRoute(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	na, nb := withNotifiers(p)
	p.b.register(t, "wb")
	rec := evRecord(watch.KindPressureHigh, "mlab")
	p.a.m.Enqueue(rec)
	p.a.pass(t)
	require.Equal(t, 0, na.count(), "not to a's own session")
	require.Equal(t, 1, nb.count())
	require.Contains(t, nb.last(), rec.ID)
	require.Equal(t, 0, p.a.m.dlv.outbox.Pending())
	require.Contains(t, p.a.loop().pathsNow(), idB+" /api/peers/watch/event")
}

func TestDelivery_AReplyThatWasLostDoesNotGiveTheWatcherTheEventTwice(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	_, nb := withNotifiers(p)
	p.b.register(t, "wb")
	p.a.m.Enqueue(evRecord(watch.KindPressureHigh, "mlab"))
	msg, _ := p.a.m.dlv.outbox.Next()
	// b got it, a never heard: a's message is still queued
	require.NoError(t, p.b.m.receiveEvent(context.Background(), eventMsg{ID: msg.ID, EventIDs: msg.EventIDs, Text: msg.Text}))
	require.Equal(t, 1, nb.count())
	p.a.pass(t)
	require.Equal(t, 1, nb.count(), "the second arrival is recognised by its event ids")
	require.Equal(t, 0, p.a.m.dlv.outbox.Pending(), "and a may drop it: b said it is done")
}

func TestDelivery_AFailedSendWaitsAndIsRetriedAsTheSameMessage(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	_, nb := withNotifiers(p)
	p.b.register(t, "wb")
	_, err := p.a.m.loc.Current(context.Background(), bothOrder, false) // a knows where the watcher is
	require.NoError(t, err)
	p.a.loop().setDown(idB, true)
	p.a.m.Enqueue(evRecord(watch.KindPressureHigh, "mlab"))
	first, _ := p.a.m.dlv.outbox.Next()

	p.a.pass(t)
	require.Equal(t, 0, nb.count())
	m, ok := p.a.m.dlv.outbox.Next()
	require.True(t, ok)
	require.Equal(t, first.ID, m.ID)
	require.Equal(t, first.Text, m.Text)
	require.Equal(t, 1, m.Attempts)

	p.a.loop().setDown(idB, false)
	p.a.pass(t)
	require.Equal(t, 0, nb.count(), "not before the retry time")
	p.a.setNow(p.a.getNow().Add(time.Minute))
	p.a.pass(t)
	require.Equal(t, 1, nb.count())
	require.Equal(t, 0, p.a.m.dlv.outbox.Pending())
}

func TestDelivery_WithNobodyToSendToTheQueueWaits(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	na, nb := withNotifiers(p)
	p.a.noAutoWake.Store(true)
	p.b.noAutoWake.Store(true)
	p.a.m.Enqueue(evRecord(watch.KindPressureHigh, "mlab"))
	p.a.pass(t)
	require.Equal(t, 1, p.a.m.dlv.outbox.Pending())
	require.Equal(t, 0, na.count()+nb.count())
}

func TestDelivery_ANewWatcherFirstGetsOneSummaryOfTheEventsNobodyDealtWith(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	_, nb := withNotifiers(p)
	rc := watch.NewRecorder(p.a.m.dlv.logDir, "mlab")
	var ids []string
	for i := 0; i < 3; i++ {
		rec, err := rc.RecordFull(watch.Event{Kind: watch.KindUnmanagedHeavy, At: p.a.getNow().Add(time.Duration(i) * time.Second), Label: watch.LabelGateGap, Argv: []string{"vitest"}})
		require.NoError(t, err)
		ids = append(ids, rec.ID) // recorded while nobody watched, never queued
	}
	p.b.register(t, "wb")
	_, err := p.a.m.loc.Current(context.Background(), bothOrder, false) // a learns where the watcher is
	require.NoError(t, err)
	p.a.pass(t)
	require.Equal(t, 1, nb.count())
	require.Contains(t, nb.last(), "pdx watch log --all")
	for _, id := range ids {
		require.Contains(t, nb.last(), id)
	}
	p.a.pass(t)
	require.Equal(t, 1, nb.count(), "once per watcher")

	// the same watcher's next session, with nothing new: no second summary
	p.b.owners.set(watcherOwner("wb2"), true, nil)
	_, _, err = p.b.m.Reconcile(context.Background())
	require.NoError(t, err)
	p.a.pass(t)
	require.Equal(t, 1, nb.count(), "everything was delivered already")
}

func TestDelivery_TheBacklogLeavesOutWhatTheWatcherAlreadyHandled(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	_, nb := withNotifiers(p)
	rc := watch.NewRecorder(p.a.m.dlv.logDir, "mlab")
	r1, _ := rc.RecordFull(watch.Event{Kind: watch.KindUnmanagedHeavy, At: p.a.getNow(), Argv: []string{"a"}})
	r2, _ := rc.RecordFull(watch.Event{Kind: watch.KindUnmanagedHeavy, At: p.a.getNow().Add(time.Second), Argv: []string{"b"}})
	p.a.m.dlv.handled = func(id string) bool { return id == r1.ID }
	p.b.register(t, "wb")
	_, err := p.a.m.loc.Current(context.Background(), bothOrder, false)
	require.NoError(t, err)
	p.a.pass(t)
	require.Equal(t, 1, nb.count())
	require.NotContains(t, nb.last(), r1.ID)
	require.Contains(t, nb.last(), r2.ID)
}

func TestDelivery_AFailedNotifyReconcilesTheHolder(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	_, nb := withNotifiers(p)
	p.b.register(t, "wb")
	nb.err = errors.New("no inbox")
	before := len(p.b.owners.codes)
	err := p.b.m.receiveEvent(context.Background(), eventMsg{ID: "m1", EventIDs: []string{"e1"}, Text: "[負載守門] x"})
	require.Error(t, err)
	require.Greater(t, len(p.b.owners.codes), before, "the holder was looked at again")
	nb.err = nil
	require.NoError(t, p.b.m.receiveEvent(context.Background(), eventMsg{ID: "m1", EventIDs: []string{"e1"}, Text: "[負載守門] x"}))
	require.Equal(t, 1, nb.count(), "a failed notify is not remembered as delivered")
}

func TestDelivery_NoWatcherHereIsRefused(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	withNotifiers(p)
	err := p.b.m.receiveEvent(context.Background(), eventMsg{ID: "m1", EventIDs: []string{"e1"}, Text: "[負載守門] x"})
	require.ErrorIs(t, err, errNoWatcherHere)
}

func postEvent(from, to *daemon, p *middleware.Principal, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/peers/watch/event", strings.NewReader(body))
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rr := httptest.NewRecorder()
	to.mux.ServeHTTP(rr, req)
	return rr
}

func TestEventRoute_Refusals(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	_, nb := withNotifiers(p)
	p.b.register(t, "wb")
	hostA := &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: idA}
	ok := func(mut func(m map[string]any)) string {
		m := map[string]any{"to_host_id": idB, "message": map[string]any{"id": "m1", "event_ids": []string{"e1"}, "text": "[負載守門] hi"}}
		if mut != nil {
			mut(m)
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	msg := func(m map[string]any) map[string]any { return m["message"].(map[string]any) }
	require.Equal(t, http.StatusForbidden, postEvent(p.a, p.b, nil, ok(nil)).Code)
	require.Equal(t, http.StatusForbidden, postEvent(p.a, p.b, &middleware.Principal{Kind: middleware.PrincipalAdmin}, ok(nil)).Code)
	require.Equal(t, http.StatusConflict, postEvent(p.a, p.b, hostA, ok(func(m map[string]any) { m["to_host_id"] = "c5n:dddddd" })).Code)
	require.Equal(t, http.StatusBadRequest, postEvent(p.a, p.b, hostA, ok(func(m map[string]any) { msg(m)["text"] = "hi" })).Code, "not a [負載守門] text")
	require.Equal(t, http.StatusBadRequest, postEvent(p.a, p.b, hostA, ok(func(m map[string]any) { msg(m)["event_ids"] = []string{} })).Code)
	require.Equal(t, http.StatusBadRequest, postEvent(p.a, p.b, hostA, ok(func(m map[string]any) { msg(m)["event_ids"] = []string{"a b"} })).Code)
	require.Equal(t, http.StatusBadRequest, postEvent(p.a, p.b, hostA, `{nope`).Code)
	require.Equal(t, http.StatusRequestEntityTooLarge, postEvent(p.a, p.b, hostA, `{"to_host_id":"`+strings.Repeat("x", 40000)+`"}`).Code)
	require.Equal(t, 0, nb.count(), "none of the refused ones reached the watcher")
	rr := postEvent(p.a, p.b, hostA, ok(nil))
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, nb.count())
	// no watcher awake here: 409
	p.b.owners.set(agent.PaneOwner{}, false, nil)
	_, _, err := p.b.m.Reconcile(context.Background())
	require.NoError(t, err)
	rr = postEvent(p.a, p.b, hostA, ok(func(m map[string]any) { msg(m)["event_ids"] = []string{"e2"} }))
	require.Equal(t, http.StatusConflict, rr.Code)
}

func TestEventRoute_IsInTheHostRoutePolicy(t *testing.T) {
	require.True(t, peersmod.HostRoutePolicy(httptest.NewRequest(http.MethodPost, "/api/peers/watch/event", nil)))
	require.False(t, peersmod.HostRoutePolicy(httptest.NewRequest(http.MethodGet, "/api/peers/watch/event", nil)))
}

// The module registers its sink with the daemon's registry as a real module does; the resources module finds it there.
func TestInit_RegistersTheEventSinkAndTheQueueIsAFileBesideTheHolder(t *testing.T) {
	r := newRig(t)
	svc, ok := r.m.core.Registry.Get(watch.EventSinkKey)
	require.True(t, ok, "the sink is registered by Init")
	sink, ok := svc.(watch.EventSink)
	require.True(t, ok)
	sink.Enqueue(evRecord(watch.KindPressureHigh, "mlab"))
	o, err := watch.OpenOutbox(filepath.Join(r.m.core.Cfg.DataDir, "watch", "outbox.json"), time.Now)
	require.NoError(t, err)
	require.Equal(t, 1, o.Pending(), "a restart keeps the queue")
}

// A message the receiving host refuses for good (400, 413) is dropped and the ones behind it still go; one it refuses for now
// (409: no watcher awake there; 429) stays and is tried again.
func deliveryRig(t *testing.T) (*recRig, *fakeCaller) {
	r, fc, _ := remoteRig(t)
	require.NoError(t, r.m.Start(context.Background())) // a pass runs only while the module runs; the loop itself is off in tests
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	return r, fc
}

func TestDelivery_APermanentRefusalDoesNotBlockTheQueue(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge} {
		r, fc := deliveryRig(t)
		fc.reply[pathEvent] = peersmod.CallResult{Class: peersmod.ClassRefused, Status: status, Code: "bad_request", Detail: "nope"}
		r.m.Enqueue(evRecord(watch.KindPressureHigh, "mlab"))
		r.m.Enqueue(evRecord(watch.KindSlotOrphaned, "mlab"))
		before := len(fc.calls)
		r.m.deliverPass(context.Background())
		require.Equal(t, 0, r.m.dlv.outbox.Pending(), "both were refused for good: neither waits at the head")
		require.Equal(t, before+2, len(fc.calls), "the second was tried in the same pass")
		require.Equal(t, "", r.m.dlv.outbox.LastDelivered(), "a dropped message is not a delivered one")
	}
}

func TestDelivery_ARefusalForNowKeepsTheMessage(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusTooManyRequests, http.StatusBadGateway} {
		r, fc := deliveryRig(t)
		fc.reply[pathEvent] = peersmod.CallResult{Class: peersmod.ClassRefused, Status: status, Code: "no_watcher", Detail: "later"}
		r.m.Enqueue(evRecord(watch.KindPressureHigh, "mlab"))
		before := len(fc.calls)
		r.m.deliverPass(context.Background())
		require.Greater(t, len(fc.calls), before, "status %d: it was tried", status)
		require.Equal(t, 1, r.m.dlv.outbox.Pending(), "status %d: kept", status)
	}
}
