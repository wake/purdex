package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	watchmod "github.com/wake/purdex/internal/module/watch"
	"github.com/wake/purdex/internal/watch"
)

func statusServer(t *testing.T, st watchmod.Status, code int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/api/watch/status" {
			http.NotFound(w, r)
			return
		}
		if code != 0 {
			http.Error(w, "x", code)
			return
		}
		_ = json.NewEncoder(w).Encode(st)
	}))
}

func TestWatchStatus_ShowsWhatThisHostHoldsAndWhereEventsGo(t *testing.T) {
	at := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	srv := statusServer(t, watchmod.Status{
		Self:    watchmod.HolderView{State: "resting", SessionID: "s-held", LastTurn: at},
		Current: &watch.Where{Alias: "air26", HostID: "a:1", SessionID: "s-26", State: watch.HolderAwake, At: at},
		Last:    &watch.Where{Alias: "mlab", SessionID: "s-old", At: at},
		Enabled: true, AutoWake: true, Mode: "patrol", Targets: []string{"air26", "mlab"},
	}, 0)
	defer srv.Close()
	cfg := watchCfgFile(t, srv, "tok")
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"status", "--config", cfg}, &out, &errb), errb.String())
	for _, want := range []string{"偵測 開", "自動喚醒 開", "模式 patrol", "targets  air26, mlab", "本機守著  resting（session s-held）", "事件送往  air26（awake，session s-26）", "上一個    mlab（session s-old"} {
		require.Contains(t, out.String(), want)
	}
	out.Reset()
	require.Equal(t, 0, watchMain([]string{"status", "--json", "--config", cfg}, &out, &errb))
	var back watchmod.Status
	require.NoError(t, json.Unmarshal(out.Bytes(), &back))
	require.Equal(t, "s-26", back.Current.SessionID)
}

func TestWatchStatus_NoRememberedWatcherAndRefusals(t *testing.T) {
	srv := statusServer(t, watchmod.Status{Self: watchmod.HolderView{State: "none"}, Targets: []string{"air26"}}, 0)
	defer srv.Close()
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"status", "--config", watchCfgFile(t, srv, "tok")}, &out, &errb))
	require.Contains(t, out.String(), "還沒有記住的守門員")
	require.Contains(t, out.String(), "偵測 關")

	require.Equal(t, 2, watchMain([]string{"status", "extra", "--config", watchCfgFile(t, srv, "tok")}, &out, &errb))

	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	errb.Reset()
	require.Equal(t, 1, watchMain([]string{"status", "--config", watchCfgFile(t, old, "tok")}, &out, &errb))
	require.Contains(t, errb.String(), "no watch support")

	bad := statusServer(t, watchmod.Status{}, http.StatusInternalServerError)
	defer bad.Close()
	errb.Reset()
	require.Equal(t, 1, watchMain([]string{"status", "--config", watchCfgFile(t, bad, "tok")}, &out, &errb))
	require.Contains(t, errb.String(), "500")
}

func TestWatchStatus_SaysAStartIsInProgressAndWhenACooldownEnds(t *testing.T) {
	until := time.Now().Add(20 * time.Minute)
	out := renderWatchStatus(watchmod.Status{Self: watchmod.HolderView{State: "none", Starting: true, CooldownUntil: &until}})
	require.Contains(t, out, "啟動／喚醒進行中")
	require.Contains(t, out, "之前不再嘗試")
	past := renderWatchStatus(watchmod.Status{Self: watchmod.HolderView{State: "none", CooldownUntil: ptrTime(time.Now().Add(-time.Minute))}})
	require.NotContains(t, past, "不再嘗試", "an ended cooldown is not shown")
}

func ptrTime(t time.Time) *time.Time { return &t }
