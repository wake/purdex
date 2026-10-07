package modeventsmod

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// apiModule inits and starts a module on dataDir whose registry reads
// time from clk, and returns it with the daemon mux its routes are on.
func apiModule(t *testing.T, dataDir string, clk *fakeClock) (*Module, *http.ServeMux) {
	t.Helper()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dataDir}})
	m := New()
	m.logf = (&logs{}).logf
	m.now = clk.now
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	return m, mux
}

// get serves GET target on mux and decodes the JSON body into a map.
func get(t *testing.T, mux *http.ServeMux, target string) (int, map[string]any, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET %s: Content-Type = %q", target, ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET %s: body %q: %v", target, rec.Body.String(), err)
	}
	return rec.Code, body, rec.Body.String()
}

func apply(t *testing.T, reg *modevents.Registry, b modevents.Batch) {
	t.Helper()
	if _, err := reg.Apply(b); err != nil {
		t.Fatal(err)
	}
}

func ev(seq int64, sid, typ, data string) modevents.Event {
	return modevents.Event{Seq: seq, At: 1700000000000 + seq, SID: sid, Type: typ, Data: json.RawMessage(data)}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const (
	sidA1 = "11111111-1111-4111-8111-111111111111"
	sidA2 = "22222222-2222-4222-8222-222222222222"
	sidB  = "33333333-3333-4333-8333-333333333333"
)

func TestAPI_StreamsListsSocketAndStreams(t *testing.T) {
	dir := shortDir(t)
	// A zone other than UTC: the API must convert.
	t0 := time.Date(2026, 10, 8, 9, 2, 3, 0, time.FixedZone("TPE", 8*3600))
	clk := &fakeClock{t: t0}
	m, mux := apiModule(t, dir, clk)

	apply(t, m.reg, modevents.Batch{V: 1, Stream: "streamAAA", Agent: "cc", CCVersion: "2.1.293", ModVersion: "m1", DroppedTotal: 3,
		Events: []modevents.Event{ev(1, sidA1, modevents.TypeSessionStart, `{"cwd":"/w/a"}`), ev(2, sidA1, modevents.TypeTurnStart, `{"turn_id":"t1"}`)}})
	clk.advance(5 * time.Second)
	apply(t, m.reg, modevents.Batch{V: 1, Stream: "streamBBB", Agent: "cc", CCVersion: "2.1.290", ModVersion: "m0",
		Events: []modevents.Event{ev(1, sidB, modevents.TypeHeartbeat, `{}`)}})
	m.reg.Reject("streamBBB")
	clk.advance(5 * time.Second)
	// Seq 3 and 4 never arrive: one gap. A lower dropped_total keeps the max.
	apply(t, m.reg, modevents.Batch{V: 1, Stream: "streamAAA", Agent: "cc", CCVersion: "2.1.293", ModVersion: "m1", DroppedTotal: 2,
		Events: []modevents.Event{ev(5, sidA2, modevents.TypeTurnComplete, `{}`), ev(6, sidA2, "future.thing", `{}`)}})

	code, body, raw := get(t, mux, "/api/mod/streams")
	if code != http.StatusOK {
		t.Fatalf("code = %d, body %s", code, raw)
	}
	if got := keys(body); !reflect.DeepEqual(got, []string{"socket", "streams"}) {
		t.Fatalf("top-level keys = %v", got)
	}
	wantSocket := map[string]any{"path": resolvedSock(t, dir), "enabled": true}
	if !reflect.DeepEqual(body["socket"], wantSocket) {
		t.Fatalf("socket = %v, want %v (no reason while enabled)", body["socket"], wantSocket)
	}

	streams, ok := body["streams"].([]any)
	if !ok || len(streams) != 2 {
		t.Fatalf("streams = %v", body["streams"])
	}
	utc := func(d time.Duration) string { return t0.Add(d).UTC().Format(time.RFC3339) }
	wantA := map[string]any{
		"stream": "streamAAA", "agent": "cc", "sid": sidA2, "cwd": "/w/a", "interactive": true,
		"cc_version": "2.1.293", "mod_version": "m1",
		"first_seen": utc(0), "last_seen": utc(10 * time.Second),
		"last_seq": 6.0, "gaps": 1.0, "dropped_total": 3.0, "rejected": 0.0, "ended": false,
		"counts": map[string]any{"session.start": 1.0, "turn.start": 1.0, "turn.complete": 1.0, modevents.CountUnknown: 1.0},
	}
	wantB := map[string]any{
		"stream": "streamBBB", "agent": "cc", "sid": sidB, "cwd": "", "interactive": false,
		"cc_version": "2.1.290", "mod_version": "m0",
		"first_seen": utc(5 * time.Second), "last_seen": utc(5 * time.Second),
		"last_seq": 1.0, "gaps": 0.0, "dropped_total": 0.0, "rejected": 1.0, "ended": false,
		"counts": map[string]any{"heartbeat": 1.0},
	}
	// Newest last_seen first.
	for i, want := range []map[string]any{wantA, wantB} {
		if !reflect.DeepEqual(streams[i], any(want)) {
			t.Errorf("streams[%d] =\n %v\nwant\n %v", i, streams[i], want)
		}
	}
	if !strings.Contains(raw, `"first_seen":"2026-10-08T01:02:03Z"`) {
		t.Fatalf("times must be RFC 3339 UTC: %s", raw)
	}
}

func TestAPI_StreamsDisabledSocket(t *testing.T) {
	// A data dir whose socket path is too long: the channel is disabled.
	dir := filepath.Join(shortDir(t), strings.Repeat("d", modevents.MaxSocketPath))
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m, mux := apiModule(t, dir, &fakeClock{t: time.Unix(1, 0)})
	if m.Status().Enabled {
		t.Fatal("the channel must be disabled")
	}

	code, body, raw := get(t, mux, "/api/mod/streams")
	if code != http.StatusOK {
		t.Fatalf("code = %d, body %s", code, raw)
	}
	wantSocket := map[string]any{"path": resolvedSock(t, dir), "enabled": false, "reason": modevents.ReasonPathTooLong}
	if !reflect.DeepEqual(body["socket"], wantSocket) {
		t.Fatalf("socket = %v, want %v", body["socket"], wantSocket)
	}
	if !strings.Contains(raw, `"streams":[]`) {
		t.Fatalf("no streams must be an empty array: %s", raw)
	}
}

func TestAPI_EventsAfter(t *testing.T) {
	m, mux := apiModule(t, shortDir(t), &fakeClock{t: time.Unix(1, 0)})
	apply(t, m.reg, modevents.Batch{V: 1, Stream: "streamAAA", Agent: "cc", Events: []modevents.Event{
		ev(1, sidA1, modevents.TypeSessionStart, `{"cwd":"/w"}`),
		ev(2, sidA1, modevents.TypeTurnStart, `{}`),
		ev(3, sidA1, modevents.TypeToolStart, `{"tool":"Bash","n":2}`),
		ev(4, sidA2, modevents.TypeTurnComplete, `{}`),
	}})

	type event struct {
		Seq  int64           `json:"seq"`
		At   int64           `json:"at"`
		SID  string          `json:"sid"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	events := func(target string) []event {
		t.Helper()
		code, body, raw := get(t, mux, target)
		if code != http.StatusOK {
			t.Fatalf("GET %s: code = %d, body %s", target, code, raw)
		}
		if got := keys(body); !reflect.DeepEqual(got, []string{"events"}) {
			t.Fatalf("GET %s: keys = %v", target, got)
		}
		for _, e := range body["events"].([]any) {
			if got := keys(e.(map[string]any)); !reflect.DeepEqual(got, []string{"at", "data", "seq", "sid", "type"}) {
				t.Fatalf("GET %s: event keys = %v", target, got)
			}
		}
		var out struct {
			Events []event `json:"events"`
		}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out.Events
	}
	seqs := func(evs []event) []int64 {
		out := []int64{}
		for _, e := range evs {
			out = append(out, e.Seq)
		}
		return out
	}

	got := events("/api/mod/streams/streamAAA/events?after=2")
	if !reflect.DeepEqual(seqs(got), []int64{3, 4}) {
		t.Fatalf("after=2: seqs = %v", seqs(got))
	}
	want := event{Seq: 3, At: 1700000000003, SID: sidA1, Type: modevents.TypeToolStart, Data: json.RawMessage(`{"tool":"Bash","n":2}`)}
	if g := got[0]; g.Seq != want.Seq || g.At != want.At || g.SID != want.SID || g.Type != want.Type || !bytes.Equal(g.Data, want.Data) {
		t.Fatalf("event = %+v (data %s), want %+v (data embedded as raw JSON)", g, g.Data, want)
	}
	if got := seqs(events("/api/mod/streams/streamAAA/events")); !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("no after: seqs = %v, want the whole ring", got)
	}
	if got := seqs(events("/api/mod/streams/streamAAA/events?after=0")); !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("after=0: seqs = %v", got)
	}
	_, _, raw := get(t, mux, "/api/mod/streams/streamAAA/events?after=4")
	if raw != `{"events":[]}` {
		t.Fatalf("after the last seq: %s, want an empty array", raw)
	}
}

func TestAPI_UnknownStream404(t *testing.T) {
	_, mux := apiModule(t, shortDir(t), &fakeClock{t: time.Unix(1, 0)})
	code, _, raw := get(t, mux, "/api/mod/streams/streamZZZ/events?after=0")
	if code != http.StatusNotFound || raw != `{"error":"no_stream"}` {
		t.Fatalf("unknown stream: %d %s", code, raw)
	}
}

func TestAPI_BadAfter400(t *testing.T) {
	m, mux := apiModule(t, shortDir(t), &fakeClock{t: time.Unix(1, 0)})
	apply(t, m.reg, modevents.Batch{V: 1, Stream: "streamAAA", Agent: "cc", Events: []modevents.Event{ev(1, sidA1, modevents.TypeHeartbeat, `{}`)}})
	for _, after := range []string{"x", "-1", "1.5", "9223372036854775808", "0x10"} {
		code, _, raw := get(t, mux, "/api/mod/streams/streamAAA/events?after="+after)
		if code != http.StatusBadRequest || raw != `{"error":"bad_after"}` {
			t.Errorf("after=%s: %d %s", after, code, raw)
		}
	}
}

func TestAPI_RoutesMountedOnDaemonMux(t *testing.T) {
	// Through core, as the daemon mounts every module on its one mux
	// (the one TokenAuth wraps).
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: shortDir(t)}})
	m := New()
	m.logf = (&logs{}).logf
	c.AddModule(m)
	if err := c.InitModules(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	c.RegisterRoutes(mux)
	for target, want := range map[string]string{
		"/api/mod/streams":                     "GET /api/mod/streams",
		"/api/mod/streams/streamAAA/events":    "GET /api/mod/streams/{stream}/events",
		"/api/mod/streams/streamAAA/events?x=": "GET /api/mod/streams/{stream}/events",
	} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, target, nil)); pattern != want {
			t.Errorf("GET %s: pattern %q, want %q", target, pattern, want)
		}
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mod/streams", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/mod/streams through core's mux: %d %s", rec.Code, rec.Body)
	}
}
