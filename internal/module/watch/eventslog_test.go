package watchmod

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/watch"
)

type logAnswer struct {
	Events []watch.EventView `json:"events"`
	Hosts  []hostLog         `json:"hosts"`
}

func (ap *ackPair) logOn(t *testing.T, d *daemon, query string) (int, logAnswer) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/watch/log"+query, nil).WithContext(adminCtx)
	rr := httptest.NewRecorder()
	d.mux.ServeHTTP(rr, req)
	var out logAnswer
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func ids(evs []watch.EventView) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.ID)
	}
	return out
}

func TestLog_ThisHostByDefaultAnotherByAliasAndAllMergesByTime(t *testing.T) {
	ap := newAckPair(t)
	code, out := ap.logOn(t, ap.a, "")
	require.Equal(t, http.StatusOK, code)
	require.ElementsMatch(t, ap.aIDs, ids(out.Events), "this host's")

	_, out = ap.logOn(t, ap.a, "?host=a26")
	require.ElementsMatch(t, ap.bIDs, ids(out.Events), "a26's, asked through the peer route")
	require.Equal(t, []hostLog{{Host: "a26", OK: true}}, out.Hosts)

	_, out = ap.logOn(t, ap.a, "?host=all")
	require.Len(t, out.Events, 4)
	for i := 1; i < len(out.Events); i++ {
		require.False(t, out.Events[i].At.After(out.Events[i-1].At), "newest first across hosts")
	}
	require.ElementsMatch(t, []hostLog{{Host: "mlab", OK: true}, {Host: "a26", OK: true}}, out.Hosts)
}

func TestLog_AHostThatCannotBeReachedIsNamedAndTheRestStillShows(t *testing.T) {
	ap := newAckPair(t)
	ap.a.loop().setDown(idB, true)
	code, out := ap.logOn(t, ap.a, "?host=all")
	require.Equal(t, http.StatusOK, code)
	require.ElementsMatch(t, ap.aIDs, ids(out.Events))
	var bad hostLog
	for _, h := range out.Hosts {
		if !h.OK {
			bad = h
		}
	}
	require.Equal(t, "a26", bad.Host)
	require.NotEmpty(t, bad.Error)

	ap.a.loop().setDown(idB, false)
	ap.a.loop().setOld(idB, true)
	_, out = ap.logOn(t, ap.a, "?host=a26")
	require.Contains(t, out.Hosts[0].Error, "older", "an older daemon is told apart from an unreachable one")
}

func TestLog_FiltersGoToTheHostThatHoldsTheEvents(t *testing.T) {
	ap := newAckPair(t)
	ap.ackOn(t, ap.a, map[string]any{"ids": []string{ap.bIDs[0]}, "note": "done"})
	_, out := ap.logOn(t, ap.a, "?host=a26&unhandled=1")
	require.Equal(t, []string{ap.bIDs[1]}, ids(out.Events))
	_, out = ap.logOn(t, ap.a, "?host=a26&kind=unmanaged_heavy")
	require.Equal(t, []string{ap.bIDs[0]}, ids(out.Events))
	require.Equal(t, "handled", out.Events[0].Status, "and the note travels with it")
	require.Equal(t, "done", out.Events[0].Note)
	for _, bad := range []string{"?host=nowhere", "?kind=free_text", "?since=yesterday", "?limit=0"} {
		code, _ := ap.logOn(t, ap.a, bad)
		require.Equal(t, http.StatusBadRequest, code, bad)
	}
}

func postPeerLog(to *daemon, p *middleware.Principal, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/peers/watch/log", strings.NewReader(body))
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rr := httptest.NewRecorder()
	to.mux.ServeHTTP(rr, req)
	return rr
}

func TestPeerLog_AnswersWithThisHostsEventsAndRefusesWhatItShould(t *testing.T) {
	ap := newAckPair(t)
	hostA := &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: idA}
	rr := postPeerLog(ap.b, hostA, `{"to_host_id":"`+idB+`"}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var out struct {
		Events []watch.EventView `json:"events"`
		HostID string            `json:"host_id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	require.Equal(t, idB, out.HostID)
	require.ElementsMatch(t, ap.bIDs, ids(out.Events))

	require.Equal(t, http.StatusForbidden, postPeerLog(ap.b, nil, `{"to_host_id":"`+idB+`"}`).Code)
	require.Equal(t, http.StatusConflict, postPeerLog(ap.b, hostA, `{"to_host_id":"c5n:dddddd"}`).Code)
	require.Equal(t, http.StatusBadRequest, postPeerLog(ap.b, hostA, `{"to_host_id":"`+idB+`","kind":"x"}`).Code)
	require.Equal(t, http.StatusBadRequest, postPeerLog(ap.b, hostA, `{nope`).Code)
	require.True(t, policyAllows("/api/peers/watch/log"))
}
