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

func TestWatchAck_HostIsForAllAndIsSentWithTheNote(t *testing.T) {
	var got map[string]any
	srv := ackServer(t, ackWire{Results: []watch.AckResult{{ID: "*@a26", Outcome: watch.AckHandled}}, Handled: 2}, 0, &got)
	defer srv.Close()
	cfg := watchCfgFile(t, srv, "tok")
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"ack", "--all", "--host", "a26", "--note", "all of a26", "--config", cfg}, &out, &errb), errb.String())
	require.Equal(t, "a26", got["host"])
	require.Equal(t, true, got["all"])
	require.Equal(t, "all of a26", got["note"])
	out.Reset()
	errb.Reset()
	require.Equal(t, 2, watchMain([]string{"ack", "a26-1", "--host", "a26", "--note", "n", "--config", cfg}, &out, &errb), "--host is for --all")
}

func TestWatchAck_EveryFailureKindIsNamedAndIsNonzero(t *testing.T) {
	for _, outcome := range []watch.AckOutcome{watch.AckNotFound, watch.AckUnreachable, watch.AckUnsupported, watch.AckRefused, watch.AckAmbiguous} {
		srv := ackServer(t, ackWire{Results: []watch.AckResult{{ID: "a26-1", Outcome: outcome}, {ID: "mlab-1", Outcome: watch.AckHandled}}, Handled: 1}, 0, nil)
		var out, errb bytes.Buffer
		require.Equal(t, 1, watchMain([]string{"ack", "a26-1", "mlab-1", "--note", "n", "--config", watchCfgFile(t, srv, "tok")}, &out, &errb), string(outcome))
		require.Contains(t, errb.String(), "a26-1", "the id that was not handled is listed")
		require.NotContains(t, errb.String(), "mlab-1 ", "and the one that was is not")
		require.Contains(t, out.String(), "已處理  mlab-1")
		srv.Close()
	}
}

func TestWatchLog_HostAndAllGoThroughTheDaemonAndAHostThatFailedIsNamedWithExitOne(t *testing.T) {
	at := time.Now().Add(-time.Minute)
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/watch/log" || r.Header.Get("Authorization") != "Bearer tok" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"events":[{"id":"mlab-1","host":"mlab","kind":"pressure_high","at":"` + at.UTC().Format(time.RFC3339) + `","detail":"high","status":"handled","note":"查過了","handled_from_host":"a26"}],
			"hosts":[{"host":"mlab","ok":true},{"host":"a26","ok":false,"error":"unreachable"}]}`))
	}))
	defer srv.Close()
	cfg := watchCfgFile(t, srv, "tok")
	var out, errb bytes.Buffer
	require.Equal(t, 1, watchMain([]string{"log", "--all", "--unhandled", "--kind", "pressure_high", "--config", cfg}, &out, &errb), "a host could not be read: exit 1")
	require.Contains(t, gotQuery, "host=all")
	require.Contains(t, gotQuery, "unhandled=1")
	require.Contains(t, gotQuery, "kind=pressure_high")
	require.Contains(t, out.String(), "high", "the events of the others are printed")
	require.Contains(t, out.String(), "✓ 查過了（a26）", "with the note and from which host it was handled")
	require.Contains(t, errb.String(), "a26 的事件讀不到：unreachable")

	out.Reset()
	errb.Reset()
	require.Equal(t, 1, watchMain([]string{"log", "--host", "a26", "--config", cfg}, &out, &errb))
	require.Contains(t, gotQuery, "host=a26")
	require.Equal(t, 2, watchMain([]string{"log", "--host", "a26", "--all", "--config", cfg}, &out, &errb), "one or the other")
}

// Per host, so a count of one host is never read as "everything is done" (#3143).
func TestRenderAck_ListsTheRemainingCountOfEveryHost(t *testing.T) {
	out := renderAck(ackWire{Results: []watch.AckResult{{ID: "mlab-1", Outcome: watch.AckHandled}}, Handled: 1, Unhandled: 0, Remaining: intp(2),
		Hosts: []ackHostLeft{{Host: "mini-lab", Unhandled: 0}, {Host: "air26", Unhandled: 2}}})
	require.Contains(t, out, "各主機還有 2 筆待處理（mini-lab 0、air26 2）")

	out = renderAck(ackWire{Results: []watch.AckResult{{ID: "mlab-1", Outcome: watch.AckHandled}}, Handled: 1, Unhandled: 0, Remaining: intp(-1),
		Hosts: []ackHostLeft{{Host: "mini-lab", Unhandled: 0}, {Host: "air26", Unhandled: -1}}})
	require.Contains(t, out, "待處理數不完整（mini-lab 0、air26 ?）")
	require.NotContains(t, out, "還有 0 筆", "an unknown host is not zero")
}

func intp(n int) *int { return &n }

// A daemon that predates the per-host list sends only unhandled: shown as before.
func TestRenderAck_AnOlderDaemonsAnswerIsShownAsBefore(t *testing.T) {
	out := renderAck(ackWire{Results: []watch.AckResult{{ID: "mlab-1", Outcome: watch.AckHandled}}, Handled: 1, Unhandled: 3})
	require.Contains(t, out, "還有 3 筆待處理")
}
