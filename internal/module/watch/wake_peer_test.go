package watchmod

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/watchwake"
)

// peerPost posts to a peer route of r as the host principal of the other side of the fleet.
func (f *fleet) peerPost(r *wakeRig, p *middleware.Principal, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, req)
	return rr
}

var (
	asAir26 = &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "air26", HostID: folHostID}
	allPeer = []string{pathWakeGet, pathWakePut, pathWakeChanged, pathWakeSuppressed}
)

// Each peer route is a host's: the principal bound to its live entry, the question addressed to this host.
func TestWakePeerRoutes_Refusals(t *testing.T) {
	f := newFleet(t)
	for _, path := range allPeer {
		body := map[string]any{"to_host_id": srcHostID, "settings": json.RawMessage(`{}`), "expect_etag": "x", "entries": []any{}, "revision": 1}
		assert.Equal(t, http.StatusForbidden, f.peerPost(f.src, nil, path, body).Code, path+": no principal")
		assert.Equal(t, http.StatusForbidden, f.peerPost(f.src, &middleware.Principal{Kind: middleware.PrincipalAdmin}, path, body).Code, path+": the admin is not a host")
		assert.Equal(t, http.StatusForbidden, f.peerPost(f.src, &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "stranger", HostID: "x:cccccc"}, path, body).Code, path+": not paired")
		assert.Equal(t, http.StatusForbidden, f.peerPost(f.src, &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "air26", HostID: "other:dddddd"}, path, body).Code, path+": the id is not the entry's")
		wrong := map[string]any{"to_host_id": "c5n:eeeeee"}
		rr := f.peerPost(f.src, asAir26, path, wrong)
		assert.Equal(t, http.StatusConflict, rr.Code, path+": addressed to another host")
		assert.Contains(t, rr.Body.String(), "wrong_host")
		assert.Equal(t, http.StatusBadRequest, f.peerPost(f.src, asAir26, path, json.RawMessage(`{nope`)).Code, path+": not JSON")
	}
	// a device token is not a host either
	req := httptest.NewRequest(http.MethodPost, pathWakeGet, strings.NewReader(`{"to_host_id":"`+srcHostID+`"}`)).WithContext(devices.WithPrincipal(context.Background(), devices.Principal{ID: "d_1"}))
	rr := httptest.NewRecorder()
	f.src.mux.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// The envelope carries the inner status and body whole, inside a 200 that names the host.
func TestWakePeerRoutes_TheEnvelopeCarriesTheInnerStatusAndBodyWhole(t *testing.T) {
	f := newFleet(t)
	cur := f.src.viewOf(t)

	var env wakeEnvelope
	rr := f.peerPost(f.src, asAir26, pathWakeGet, map[string]any{"to_host_id": srcHostID})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
	assert.Equal(t, srcHostID, env.HostID)
	assert.Equal(t, http.StatusOK, env.Status)
	var got wakeSettingsView
	require.NoError(t, json.Unmarshal(env.Body, &got))
	assert.Equal(t, cur.Etag, got.Etag)
	assert.True(t, got.IsSource)
	assert.Equal(t, srcHostID, got.SourceID)

	// a 400 with its fields
	rr = f.peerPost(f.src, asAir26, pathWakePut, map[string]any{"to_host_id": srcHostID, "settings": json.RawMessage(`{"version":1,"enabled":3}`), "expect_etag": cur.Etag})
	require.Equal(t, http.StatusOK, rr.Code, "the refusal is inside the envelope: the HTTP status is the route's own")
	env = wakeEnvelope{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
	assert.Equal(t, http.StatusBadRequest, env.Status)
	var inv struct {
		Error  string                 `json:"error"`
		Fields []watchwake.FieldError `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(env.Body, &inv))
	assert.Equal(t, "invalid", inv.Error)
	assert.NotEmpty(t, inv.Fields)

	// a 409 with the current document
	rr = f.peerPost(f.src, asAir26, pathWakePut, map[string]any{"to_host_id": srcHostID, "settings": json.RawMessage(watchwake.Marshal(watchwake.Default("Asia/Taipei"))), "expect_etag": "0000000000000000"})
	env = wakeEnvelope{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
	assert.Equal(t, http.StatusConflict, env.Status)
	assert.Contains(t, string(env.Body), cur.Etag)
}

func TestWakePeerRoutes_GetAndPutOnAHostThatIsNotTheSourceAreNotSource503(t *testing.T) {
	f := newFleet(t)
	for _, path := range []string{pathWakeGet, pathWakePut} {
		rr := f.peerPost(f.fol, &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: srcHostID}, path,
			map[string]any{"to_host_id": folHostID, "settings": json.RawMessage(`{}`), "expect_etag": "x"})
		require.Equal(t, http.StatusOK, rr.Code, path)
		var env wakeEnvelope
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
		assert.Equal(t, http.StatusServiceUnavailable, env.Status, path)
		assert.Contains(t, string(env.Body), "not_source", path)
	}
	_, _, ok, _ := f.fol.rows.Get("watch_wake")
	assert.False(t, ok, "nothing was written")
}

func TestWakePeerRoutes_ChangedMakesAFollowerReadAndTheSourceIgnoresIt(t *testing.T) {
	f := newFleet(t)
	f.src.viewOf(t)
	f.folCall.reset()
	rr := f.peerPost(f.fol, &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: srcHostID}, pathWakeChanged, map[string]any{"to_host_id": folHostID, "revision": 1})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"host_id":"`+folHostID+`"}`, rr.Body.String())
	assert.Equal(t, []string{pathWakeGet}, f.folCall.paths(), "the follower read the source")
	assert.Equal(t, int64(1), f.fol.viewOf(t).Revision)

	f.srcCall.reset()
	rr = f.peerPost(f.src, asAir26, pathWakeChanged, map[string]any{"to_host_id": srcHostID, "revision": 99})
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, f.srcCall.paths(), "the source reads nothing")
}

func (f *fleet) report(t *testing.T, r *wakeRig, p *middleware.Principal, toHostID string, entries []wakeSuppressed) *httptest.ResponseRecorder {
	t.Helper()
	return f.peerPost(r, p, pathWakeSuppressed, map[string]any{"to_host_id": toHostID, "entries": entries})
}

func TestWakePeerRoutes_TheSourceKeepsTheLast50ReportsNewestFirstAndDropsWhatIsNotAnEntry(t *testing.T) {
	f := newFleet(t)
	at := func(min int) time.Time { return f.src.m.now().Add(-time.Duration(min) * time.Minute) }
	mk := func(min int, host string) wakeSuppressed {
		return wakeSuppressed{At: at(min), Host: host, Reason: "wwd_alert", Why: "level_off", Detail: "d" + strconv.Itoa(min)}
	}
	rr := f.report(t, f.src, asAir26, srcHostID, []wakeSuppressed{mk(5, "air26"), mk(1, "air26"), mk(3, "air26")})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	got := f.src.viewOf(t).Suppressed
	require.Len(t, got, 3)
	assert.Equal(t, []string{"d1", "d3", "d5"}, []string{got[0].Detail, got[1].Detail, got[2].Detail}, "newest first")

	// what is not an entry is left out, the rest is kept
	bad := []wakeSuppressed{
		{Host: "air26", Reason: "wwd_alert", Why: "kind_off"},                                              // no time
		{At: at(2), Reason: "wwd_alert", Why: "kind_off"},                                                  // no host
		{At: at(2), Host: "air26", Why: "kind_off"},                                                        // no reason
		{At: at(2), Host: "air26", Reason: "wwd_alert"},                                                    // no why
		{At: at(2), Host: strings.Repeat("h", 65), Reason: "r", Why: "w"},                                  // a host name too long
		{At: at(2), Host: "air26", Reason: "wwd_alert", Why: "kind_off", Detail: strings.Repeat("d", 501)}, // a detail too long
		{At: f.src.m.now().Add(48 * time.Hour), Host: "air26", Reason: "r", Why: "w"},                      // from the future
		mk(2, "air26"),
	}
	require.Equal(t, http.StatusOK, f.report(t, f.src, asAir26, srcHostID, bad).Code)
	assert.Len(t, f.src.viewOf(t).Suppressed, 4)

	// 60 more, in two reports: only the newest 50 of all stay
	var more []wakeSuppressed
	for i := 0; i < 30; i++ {
		more = append(more, mk(100+i, "air26"))
	}
	require.Equal(t, http.StatusOK, f.report(t, f.src, asAir26, srcHostID, more).Code)
	require.Equal(t, http.StatusOK, f.report(t, f.src, asAir26, srcHostID, more).Code)
	all := f.src.viewOf(t).Suppressed
	assert.Len(t, all, 50)
	assert.Equal(t, "d1", all[0].Detail, "the newest is still first")

	// a report over the cap is refused whole
	var tooMany []wakeSuppressed
	for i := 0; i < 51; i++ {
		tooMany = append(tooMany, mk(1, "air26"))
	}
	assert.Equal(t, http.StatusBadRequest, f.report(t, f.src, asAir26, srcHostID, tooMany).Code)
}

// A report sent to a host that is not the source is accepted and dropped: the source is where they are kept.
func TestWakePeerRoutes_AReportToAHostThatIsNotTheSourceIsDropped(t *testing.T) {
	f := newFleet(t)
	e := wakeSuppressed{At: f.fol.m.now(), Host: "mlab", Reason: "wwd_alert", Why: "rule_off"}
	rr := f.report(t, f.fol, &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: srcHostID}, folHostID, []wakeSuppressed{e})
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, f.fol.viewOf(t).Suppressed)
}

// A follower's own GET shows what it suppressed itself (WK-2 records them; here the log is filled the way WK-2 will).
func TestWakeFollow_AFollowerShowsItsOwnSuppressed(t *testing.T) {
	f := newFleet(t)
	f.fol.m.suppressed.add(wakeSuppressed{At: f.fol.m.now(), Host: "air26", Reason: "wwd_alert", Why: "kind_off", Detail: "x"})
	assert.Len(t, f.fol.viewOf(t).Suppressed, 1)
	assert.Empty(t, f.src.viewOf(t).Suppressed, "the source's log is its own and what is reported to it")
}
