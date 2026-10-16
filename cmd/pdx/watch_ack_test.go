package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/watch"
)

// ackServer answers POST /api/watch/ack with answer and records the request body.
func ackServer(t *testing.T, answer ackWire, code int, got *map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/api/watch/ack" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if got != nil {
			_ = json.Unmarshal(raw, got)
		}
		if code != 0 {
			http.Error(w, `{"error":"nope"}`, code)
			return
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
}

func TestWatchAck_SendsTheIDsAndTheNoteAndSaysWhatBecameOfEach(t *testing.T) {
	var got map[string]any
	srv := ackServer(t, ackWire{Results: []watch.AckResult{{ID: "mlab-1", Outcome: watch.AckHandled}, {ID: "mlab-2", Outcome: watch.AckAlready}}, Handled: 1, Unhandled: 3}, 0, &got)
	defer srv.Close()
	cfg := watchCfgFile(t, srv, "tok")
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"ack", "mlab-1", "mlab-2", "--note", "已查，是 vitest", "--config", cfg}, &out, &errb), errb.String())
	require.Equal(t, []any{"mlab-1", "mlab-2"}, got["ids"])
	require.Equal(t, "已查，是 vitest", got["note"])
	require.Equal(t, false, got["all"])
	require.Contains(t, out.String(), "已處理  mlab-1")
	require.Contains(t, out.String(), "先前已處理（備註不變）  mlab-2")
	require.Contains(t, out.String(), "還有 3 筆待處理")
}

func TestWatchAck_AnyIDNotHandledIsAFailureAndIsNamed(t *testing.T) {
	srv := ackServer(t, ackWire{Results: []watch.AckResult{{ID: "mlab-1", Outcome: watch.AckHandled}, {ID: "a26-9", Outcome: watch.AckNotFound}}, Handled: 1}, 0, nil)
	defer srv.Close()
	var out, errb bytes.Buffer
	require.Equal(t, 1, watchMain([]string{"ack", "mlab-1", "a26-9", "--note", "n", "--config", watchCfgFile(t, srv, "tok")}, &out, &errb))
	require.Contains(t, errb.String(), "1 筆沒有處理到：a26-9")
	require.Contains(t, out.String(), "已處理  mlab-1", "the ones that went are still reported")
}

func TestWatchAck_AllAsksForAllAndRefusalsNeverReachTheDaemon(t *testing.T) {
	var got map[string]any
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_ = json.NewEncoder(w).Encode(ackWire{Handled: 2})
	}))
	defer srv.Close()
	cfg := watchCfgFile(t, srv, "tok")
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"ack", "--all", "--note", "all done", "--config", cfg}, &out, &errb), errb.String())
	require.Equal(t, true, got["all"])
	require.Equal(t, 1, calls)

	for name, args := range map[string][]string{
		"no note":       {"ack", "mlab-1", "--config", cfg},
		"a blank note":  {"ack", "mlab-1", "--note", " ", "--config", cfg},
		"two lines":     {"ack", "mlab-1", "--note", "a\nb", "--config", cfg},
		"too long":      {"ack", "mlab-1", "--note", strings.Repeat("a", 501), "--config", cfg},
		"no ids no all": {"ack", "--note", "n", "--config", cfg},
		"ids and --all": {"ack", "mlab-1", "--all", "--note", "n", "--config", cfg},
	} {
		out.Reset()
		errb.Reset()
		require.Equal(t, 2, watchMain(args, &out, &errb), name)
	}
	require.Equal(t, 1, calls, "none of the refused ones was sent")
}

func TestWatchAck_TheDaemonsRefusalIsExitOne(t *testing.T) {
	srv := ackServer(t, ackWire{}, http.StatusForbidden, nil)
	defer srv.Close()
	var out, errb bytes.Buffer
	require.Equal(t, 1, watchMain([]string{"ack", "mlab-1", "--note", "n", "--config", watchCfgFile(t, srv, "tok")}, &out, &errb))
	require.NotEmpty(t, errb.String())
}

func TestWatchLog_UnhandledLeavesOutAckedEventsAndTheListShowsTheNote(t *testing.T) {
	dir := t.TempDir()
	rc := watch.NewRecorder(dir, "mlab")
	now := time.Now()
	a, err := rc.RecordFull(watch.Event{Kind: watch.KindPressureHigh, At: now.Add(-2 * time.Minute), Pressure: 2, Detail: "high a"})
	require.NoError(t, err)
	b, err := rc.RecordFull(watch.Event{Kind: watch.KindPressureHigh, At: now.Add(-time.Minute), Pressure: 2, Detail: "high b"})
	require.NoError(t, err)
	acks, err := watch.OpenAcks(dir, time.Now)
	require.NoError(t, err)
	_, err = acks.Ack([]string{a.ID}, func(string) bool { return true }, watch.Ack{Note: "查過了"})
	require.NoError(t, err)

	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"log", "--dir", dir}, &out, &errb), errb.String())
	require.Contains(t, out.String(), "high a")
	require.Contains(t, out.String(), "✓ 查過了", "a handled event shows its note")
	require.Contains(t, out.String(), "high b")

	out.Reset()
	require.Equal(t, 0, watchMain([]string{"log", "--unhandled", "--dir", dir}, &out, &errb), errb.String())
	require.NotContains(t, out.String(), "high a")
	require.Contains(t, out.String(), "high b")
	_ = b
}

func TestWatchLog_JSONShowsTheHandledState(t *testing.T) {
	dir := t.TempDir()
	rc := watch.NewRecorder(dir, "mlab")
	a, err := rc.RecordFull(watch.Event{Kind: watch.KindPressureHigh, At: time.Now().Add(-time.Minute), Pressure: 2, Detail: "high a"})
	require.NoError(t, err)
	acks, err := watch.OpenAcks(dir, time.Now)
	require.NoError(t, err)
	_, err = acks.Ack([]string{a.ID}, func(string) bool { return true }, watch.Ack{Note: "查過了", By: "mlab/w"})
	require.NoError(t, err)

	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"log", "--json", "--dir", dir}, &out, &errb), errb.String())
	var line map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out.String())), &line))
	require.Equal(t, true, line["handled"])
	require.Equal(t, "查過了", line["note"])
	require.Equal(t, a.ID, line["id"])
}
