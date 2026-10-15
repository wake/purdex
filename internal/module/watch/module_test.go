package watchmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/watchsettings"
)

const ourID, peerID = "mlab:aaaaaa", "a26:bbbbbb"

type testRig struct {
	m   *Module
	mux *http.ServeMux
}

func newRig(t *testing.T) *testRig {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), HostID: ourID}
	cfg.Peers.Hosts = []config.PeerHost{{Alias: "a26", HostID: peerID, URL: "http://x", Token: "t", InboundToken: "i"}}
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	m := New()
	m.logf = func(string, ...any) {}
	require.NoError(t, m.Init(c))
	m.settings = func() (watchsettings.Settings, error) { return watchsettings.Default(), nil }
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	return &testRig{m: m, mux: mux}
}

func (r *testRig) post(p *middleware.Principal, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/peers/watch/holder", strings.NewReader(body))
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, req)
	return rr
}

var host = &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "a26", HostID: peerID}

func TestHolderRoute_AnswersNoneThenAwakeThenResting(t *testing.T) {
	r := newRig(t)
	q := `{"to_host_id":"` + ourID + `"}`
	var v HolderView
	rr := r.post(host, q)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &v))
	require.Equal(t, ourID, v.HostID, "HostCaller checks the answer names the host it asked")
	require.Equal(t, "none", v.State)
	require.Equal(t, []string{"mlab", "a26"}, v.Targets)

	at := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.State, h.SessionID, h.Since, h.LastTurn = watch.HolderAwake, "sess-9", at, at }))
	v = HolderView{}
	require.NoError(t, json.Unmarshal(r.post(host, q).Body.Bytes(), &v))
	require.Equal(t, "awake", v.State)
	require.Equal(t, "sess-9", v.SessionID)
	require.True(t, v.LastTurn.Equal(at))

	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.State = watch.HolderResting }))
	v = HolderView{}
	require.NoError(t, json.Unmarshal(r.post(host, q).Body.Bytes(), &v))
	require.Equal(t, "resting", v.State)
	require.Equal(t, "sess-9", v.SessionID, "resting names the session to wake")
}

func TestHolderRoute_Refusals(t *testing.T) {
	r := newRig(t)
	q := `{"to_host_id":"` + ourID + `"}`
	// no principal / admin / unverified / an entry that no longer carries the id
	require.Equal(t, http.StatusForbidden, r.post(nil, q).Code)
	require.Equal(t, http.StatusForbidden, r.post(&middleware.Principal{Kind: middleware.PrincipalAdmin}, q).Code)
	require.Equal(t, http.StatusForbidden, r.post(&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "a26"}, q).Code)
	require.Equal(t, http.StatusForbidden, r.post(&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "a26", HostID: "other:cccccc"}, q).Code)
	// addressed to another host: 409 wrong_host
	rr := r.post(host, `{"to_host_id":"c5n:dddddd"}`)
	require.Equal(t, http.StatusConflict, rr.Code)
	require.Contains(t, rr.Body.String(), "wrong_host")
	// bad body, oversized body
	require.Equal(t, http.StatusBadRequest, r.post(host, `{nope`).Code)
	require.Equal(t, http.StatusRequestEntityTooLarge, r.post(host, `{"to_host_id":"`+strings.Repeat("x", 8000)+`"}`).Code)
}

func TestHolderRoute_ARateLimitPerHost(t *testing.T) {
	r := newRig(t)
	q := `{"to_host_id":"` + ourID + `"}`
	var limited bool
	for i := 0; i < 400 && !limited; i++ {
		limited = r.post(host, q).Code == http.StatusTooManyRequests
	}
	require.True(t, limited, "a host cannot ask without bound")
}

func TestHolderRoute_TargetsAreLeftOutWhenTheSettingCannotBeRead(t *testing.T) {
	r := newRig(t)
	r.m.settings = func() (watchsettings.Settings, error) { return watchsettings.Settings{}, context.Canceled }
	var v HolderView
	require.NoError(t, json.Unmarshal(r.post(host, `{"to_host_id":"`+ourID+`"}`).Body.Bytes(), &v))
	require.Nil(t, v.Targets)
}

func TestLocalHolder_IsTheSameViewAndTheHolderSurvivesARestart(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.State, h.SessionID = watch.HolderResting, "s1" }))
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/watch/holder", nil))
	var v HolderView
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &v))
	require.Equal(t, "resting", v.State)
	require.Equal(t, ourID, v.HostID)

	// a second module over the same data dir reads the file
	c2 := core.New(core.CoreDeps{Config: &config.Config{DataDir: r.m.core.Cfg.DataDir, HostID: ourID}, Registry: core.NewServiceRegistry()})
	m2 := New()
	m2.logf = func(string, ...any) {}
	require.NoError(t, m2.Init(c2))
	require.Equal(t, watch.HolderResting, m2.holder.Get().State)
	require.FileExists(t, filepath.Join(r.m.core.Cfg.DataDir, "watch", "holder.json"))
}

func TestModule_NameAndDependencies(t *testing.T) {
	m := New()
	require.Equal(t, "watch", m.Name())
	require.Equal(t, []string{"peers", "hostconfig"}, m.Dependencies())
	var _ core.Module = m
}
