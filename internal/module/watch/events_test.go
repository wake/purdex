package watchmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/devices"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/watch"
)

// eventsRig is a rig with three recorded events on disk and the events sent as watch.events collected.
type eventsRig struct {
	*testRig
	ids []string
	ev  *events
}

func newEventsRig(t *testing.T) *eventsRig {
	t.Helper()
	r := newRig(t)
	ev := &events{}
	r.m.emit = ev.add
	rc := watch.NewRecorder(r.m.dlv.logDir, "mlab")
	var ids []string
	base := r.m.now()
	for i, k := range []watch.Kind{watch.KindUnmanagedHeavy, watch.KindPressureHigh, watch.KindSlotOrphaned} {
		rec, err := rc.RecordFull(watch.Event{Kind: k, At: base.Add(-time.Duration(3-i) * time.Minute), Label: watch.LabelGateGap, Argv: []string{"vitest", "run"}, Cwd: "/work", Pressure: 2, Detail: "d", Slot: "heavy-1", HolderPID: 7})
		require.NoError(t, err)
		ids = append(ids, rec.ID)
	}
	return &eventsRig{testRig: r, ids: ids, ev: ev}
}

func (r *eventsRig) do(ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, req)
	return rr
}

var adminCtx = devices.WithAdmin(context.Background())

func (r *eventsRig) ack(t *testing.T, ctx context.Context, body map[string]any) (*httptest.ResponseRecorder, ackResponse) {
	t.Helper()
	b, _ := json.Marshal(body)
	rr := r.do(ctx, http.MethodPost, "/api/watch/ack", string(b))
	var resp ackResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	return rr, resp
}

func (r *eventsRig) unhandled(t *testing.T) int {
	t.Helper()
	rr := r.do(adminCtx, http.MethodGet, "/api/watch/unhandled", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var out struct{ Count int }
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	return out.Count
}

func (r *eventsRig) list(t *testing.T, query string) []watch.EventView {
	t.Helper()
	rr := r.do(adminCtx, http.MethodGet, "/api/watch/events"+query, "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var out struct{ Events []watch.EventView }
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	return out.Events
}

func TestEvents_AreNewestFirstWithStatusAndTheCountCountsPending(t *testing.T) {
	r := newEventsRig(t)
	got := r.list(t, "")
	require.Len(t, got, 3)
	require.Equal(t, []string{r.ids[2], r.ids[1], r.ids[0]}, []string{got[0].ID, got[1].ID, got[2].ID})
	require.Equal(t, "pending", got[0].Status)
	require.Equal(t, "vitest run", got[2].Command)
	require.Equal(t, 3, r.unhandled(t))
	require.Len(t, r.list(t, "?limit=2"), 2)
	for _, bad := range []string{"?limit=0", "?limit=501", "?limit=x", "?since=yesterday"} {
		require.Equal(t, http.StatusBadRequest, r.do(adminCtx, http.MethodGet, "/api/watch/events"+bad, "").Code, bad)
	}
	since := r.m.now().Add(-90 * time.Second).UTC().Format(time.RFC3339)
	require.Len(t, r.list(t, "?since="+since), 1, "only the event of the last minute")
}

func TestAck_MarksHandledWithTheNoteAndWhoAndTheCountGoesDown(t *testing.T) {
	r := newEventsRig(t)
	r.m.core.Registry.Register(peersmod.OriginResolverKey, fakeResolver{})
	rr, resp := r.ack(t, adminCtx, map[string]any{"ids": []string{r.ids[0], r.ids[1]}, "note": "已查，是 vitest", "origin_inbox": "/tmp/x.sock"})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, 2, resp.Handled)
	require.Equal(t, 1, resp.Unhandled)
	require.Equal(t, 1, r.unhandled(t))
	got := r.list(t, "")
	var h watch.EventView
	for _, e := range got {
		if e.ID == r.ids[0] {
			h = e
		}
	}
	require.Equal(t, "handled", h.Status)
	require.Equal(t, "已查，是 vitest", h.Note)
	require.Equal(t, "mlab/the-watcher", h.HandledBy)
	require.NotNil(t, h.HandledAt)
}

type fakeResolver struct{}

func (fakeResolver) ResolveOrigin(string) (team.Origin, bool, error) {
	return team.Origin{SessionID: "s", Ref: "_abc123", Address: "mlab/the-watcher"}, true, nil
}

func TestAck_RefusesWhatItShouldAndChangesNothing(t *testing.T) {
	r := newEventsRig(t)
	device := devices.WithPrincipal(context.Background(), devices.Principal{ID: "phone"})
	for name, c := range map[string]struct {
		ctx    context.Context
		body   map[string]any
		status int
	}{
		"no token":            {context.Background(), map[string]any{"ids": []string{r.ids[0]}, "note": "n"}, http.StatusForbidden},
		"a device token":      {device, map[string]any{"ids": []string{r.ids[0]}, "note": "n"}, http.StatusForbidden},
		"no note":             {adminCtx, map[string]any{"ids": []string{r.ids[0]}}, http.StatusBadRequest},
		"a blank note":        {adminCtx, map[string]any{"ids": []string{r.ids[0]}, "note": "  "}, http.StatusBadRequest},
		"a note of two lines": {adminCtx, map[string]any{"ids": []string{r.ids[0]}, "note": "a\nb"}, http.StatusBadRequest},
		"a note over 500":     {adminCtx, map[string]any{"ids": []string{r.ids[0]}, "note": strings.Repeat("a", 501)}, http.StatusBadRequest},
		"neither ids nor all": {adminCtx, map[string]any{"note": "n"}, http.StatusBadRequest},
		"ids and all":         {adminCtx, map[string]any{"ids": []string{r.ids[0]}, "all": true, "note": "n"}, http.StatusBadRequest},
		"an id with a space":  {adminCtx, map[string]any{"ids": []string{"a b"}, "note": "n"}, http.StatusBadRequest},
	} {
		rr, _ := r.ack(t, c.ctx, c.body)
		require.Equal(t, c.status, rr.Code, name)
	}
	require.Equal(t, 3, r.unhandled(t))
	require.Equal(t, 0, r.ev.n(), "a refused ack sends no watch.events")
}

func TestAck_AnUnknownOrForeignIDIsNotFoundAndTheRestStillGo(t *testing.T) {
	r := newEventsRig(t)
	rr, resp := r.ack(t, adminCtx, map[string]any{"ids": []string{r.ids[0], "a26-0001", "mlab-nope"}, "note": "n"})
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, []watch.AckResult{{ID: r.ids[0], Outcome: watch.AckHandled}, {ID: "a26-0001", Outcome: watch.AckNotFound}, {ID: "mlab-nope", Outcome: watch.AckNotFound}}, resp.Results)
	require.Equal(t, 1, resp.Handled)
}

func TestAck_AgainIsAlreadyAndKeepsTheFirstNoteAndSendsNothing(t *testing.T) {
	r := newEventsRig(t)
	r.ack(t, adminCtx, map[string]any{"ids": []string{r.ids[0]}, "note": "first"})
	n := r.ev.n()
	rr, resp := r.ack(t, adminCtx, map[string]any{"ids": []string{r.ids[0]}, "note": "second"})
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, []watch.AckResult{{ID: r.ids[0], Outcome: watch.AckAlready}}, resp.Results)
	require.Equal(t, 0, resp.Handled)
	require.Equal(t, n, r.ev.n(), "nothing changed: no push")
	for _, e := range r.list(t, "") {
		if e.ID == r.ids[0] {
			require.Equal(t, "first", e.Note)
		}
	}
}

func TestAck_AllAcksEveryPendingEventOfThisHost(t *testing.T) {
	r := newEventsRig(t)
	r.ack(t, adminCtx, map[string]any{"ids": []string{r.ids[0]}, "note": "first"})
	rr, resp := r.ack(t, adminCtx, map[string]any{"all": true, "note": "all done"})
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 2, resp.Handled)
	require.Equal(t, 0, resp.Unhandled)
}

func payloadOf(t *testing.T, e *events) EventsPayload {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	var p EventsPayload
	require.NoError(t, json.Unmarshal([]byte(e.evs[len(e.evs)-1].Value), &p))
	require.Equal(t, EventsEventType, e.evs[len(e.evs)-1].Type)
	return p
}

// An ack of three ids is one watch.events, and one recorded event is one.
func TestWatchEvents_OncePerOperation(t *testing.T) {
	r := newEventsRig(t)
	r.ack(t, adminCtx, map[string]any{"ids": r.ids, "note": "n"})
	require.Equal(t, 1, r.ev.n(), "three ids, one push")
	require.Equal(t, EventsPayload{HostID: ourID, Unhandled: 0, Cause: CauseAcked}, payloadOf(t, r.ev))

	rc := watch.NewRecorder(r.m.dlv.logDir, "mlab")
	rec, err := rc.RecordFull(watch.Event{Kind: watch.KindPressureHigh, At: r.m.now(), Pressure: 2, Detail: "x"})
	require.NoError(t, err)
	r.m.Enqueue(rec)
	require.Equal(t, 2, r.ev.n())
	require.Equal(t, EventsPayload{HostID: ourID, Unhandled: 1, Cause: CauseRecorded}, payloadOf(t, r.ev))
}

// The real module registered in a real core, asked through GET /api/info: watch.v1 is announced (the lesson of fleet.v1, alpha.743).
func TestInfo_AnnouncesWatchV1ForTheRealModule(t *testing.T) {
	r := newRig(t)
	r.m.core.AddModule(r.m)
	mux := http.NewServeMux()
	r.m.core.RegisterCoreRoutes(mux)
	caps := func() []any {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		list, _ := body["capabilities"].([]any)
		return list
	}
	require.Contains(t, caps(), "watch.v1", "the module is initialised")
	require.NoError(t, r.m.Start(context.Background()))
	require.Contains(t, caps(), "watch.v1", "and running")
	require.NoError(t, r.m.Stop(context.Background()))
	require.NotContains(t, caps(), "watch.v1", "a stopped module announces nothing")
}
