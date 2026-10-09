package workbook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/team"
)

// fakeRoots is the team module's chain-root service: a map from session to root; absent = its own root.
type fakeRoots struct {
	mu   sync.Mutex
	root map[string]string
	err  error
}

func (f *fakeRoots) RootSessionOf(sid string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	if r, ok := f.root[sid]; ok {
		return r, nil
	}
	return sid, nil
}

type apiEnv struct {
	t      *testing.T
	m      *Module
	store  *Store
	roots  *fakeRoots
	mux    *http.ServeMux
	mu     sync.Mutex
	events []sentEvent
}

type sentEvent struct{ typ, value string }

func newAPI(t *testing.T) *apiEnv {
	t.Helper()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), HostID: "h1:abc"}})
	roots := &fakeRoots{root: map[string]string{}}
	c.Registry.Register(team.LineageRootKey, team.LineageRootResolver(roots))
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	e := &apiEnv{t: t, m: m, store: m.live(), roots: roots, mux: http.NewServeMux()}
	m.broadcast = func(typ, value string) {
		e.mu.Lock()
		e.events = append(e.events, sentEvent{typ, value})
		e.mu.Unlock()
	}
	m.RegisterRoutes(e.mux)
	return e
}

func (e *apiEnv) get(url string) *httptest.ResponseRecorder {
	e.t.Helper()
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func (e *apiEnv) sent() []sentEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.events
	e.events = nil
	return out
}

type convBody struct {
	ConvKey  string      `json:"conv_key"`
	Status   string      `json:"status"`
	StatusAt int64       `json:"status_at"`
	Entries  []entryBody `json:"entries"`
}

type entryBody struct {
	ID          int64  `json:"id"`
	ConvKey     string `json:"conv_key"`
	HostID      string `json:"host_id"`
	Provider    string `json:"provider"`
	SessionID   string `json:"session_id"`
	TurnID      string `json:"turn_id"`
	TurnAt      int64  `json:"turn_at"`
	TurnSeq     int64  `json:"turn_seq"`
	State       string `json:"state"`
	Reason      string `json:"reason"`
	Thing       string `json:"thing"`
	Push        string `json:"push"`
	Entry       string `json:"entry"`
	ThingDone   bool   `json:"thing_done"`
	PushReadyAt int64  `json:"push_ready_at"`
	TeamID      string `json:"team_id"`
	Role        string `json:"role"`
	Ref         string `json:"ref"`
	PromptVer   int    `json:"prompt_ver"`
	LatencyMS   int64  `json:"latency_ms"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

func decodeConv(t *testing.T, w *httptest.ResponseRecorder) convBody {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var b convBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("%v: %s", err, w.Body.String())
	}
	return b
}

func errCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var b struct{ Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != status || b.Error != code {
		t.Fatalf("got %d %q, want %d %q (%s)", w.Code, b.Error, status, code, w.Body.String())
	}
}

// Any session of a relay chain finds the conversation; every entry field of spec §6 is on the wire, times in ms.
func TestAPI_ConversationResolvesThroughTheRelayChain(t *testing.T) {
	e := newAPI(t)
	e.roots.root["child"] = "root"
	e.roots.root["grandchild"] = "root"
	a := pending("root", "root", "t1", 1700000000000)
	id := mustInsert(t, e.store, a)
	e.store.SetPushLine(id, "接力指令", "審查已修")
	e.store.Finish(id, StateOK, "", Output{Thing: "接力指令", Push: "審查已修", Entry: "改了。", ThingDone: true, LatencyMS: 8400})
	e.store.SetStatus("root", "等放行", id, "root")

	for _, sid := range []string{"root", "child", "grandchild"} {
		b := decodeConv(t, e.get("/api/workbook/conversations/claude/"+sid))
		if b.ConvKey != "root" || b.Status != "等放行" || b.StatusAt == 0 || len(b.Entries) != 1 {
			t.Fatalf("%s: %+v", sid, b)
		}
		en := b.Entries[0]
		if en.ID != id || en.State != StateOK || en.Thing != "接力指令" || en.Push != "審查已修" || en.Entry != "改了。" || !en.ThingDone ||
			en.TurnAt != 1700000000000 || en.LatencyMS != 8400 || en.PushReadyAt == 0 || en.PromptVer != 1 || en.TeamID != "team-1" ||
			en.Role != "member" || en.Ref != "_abc123" || en.Provider != "claude" || en.HostID != "h1" || en.SessionID != "root" ||
			en.TurnID != "t1" || en.CreatedAt == 0 || en.UpdatedAt == 0 {
			t.Fatalf("%s: entry = %+v", sid, en)
		}
	}
}

func TestAPI_ConversationPagesByIDNewestFirst(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c" // the conversation key is the relay chain's root
	var ids []int64
	for i, turn := range []string{"a", "b", "c", "d", "e"} {
		ids = append(ids, mustInsert(t, e.store, pending("c", "s1", turn, int64(100+i))))
	}
	p1 := decodeConv(t, e.get("/api/workbook/conversations/claude/s1?limit=2"))
	if len(p1.Entries) != 2 || p1.Entries[0].ID != ids[4] || p1.Entries[1].ID != ids[3] {
		t.Fatalf("page 1: %+v", p1.Entries)
	}
	p2 := decodeConv(t, e.get("/api/workbook/conversations/claude/s1?limit=2&before="+itoa(p1.Entries[1].ID)))
	if len(p2.Entries) != 2 || p2.Entries[0].ID != ids[2] || p2.Entries[1].ID != ids[1] {
		t.Fatalf("page 2: %+v", p2.Entries)
	}
	p3 := decodeConv(t, e.get("/api/workbook/conversations/claude/s1?limit=2&before="+itoa(p2.Entries[1].ID)))
	if len(p3.Entries) != 1 || p3.Entries[0].ID != ids[0] {
		t.Fatalf("page 3: %+v", p3.Entries)
	}
}

// skipped entries are returned (the Apps hide them), failed ones carry the reason, pending ones are there too.
func TestAPI_ConversationReturnsEveryState(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c" // the conversation key is the relay chain's root
	a := mustInsert(t, e.store, pending("c", "s1", "a", 1))
	b := mustInsert(t, e.store, pending("c", "s1", "b", 2))
	mustInsert(t, e.store, pending("c", "s1", "c", 3))
	e.store.Finish(a, StateSkipped, "no_text", Output{})
	e.store.Finish(b, StateFailed, "timeout", Output{LatencyMS: 30000})
	got := decodeConv(t, e.get("/api/workbook/conversations/claude/s1")).Entries
	if len(got) != 3 || got[0].State != StatePending || got[1].State != StateFailed || got[1].Reason != "timeout" || got[2].State != StateSkipped || got[2].Reason != "no_text" {
		t.Fatalf("entries = %+v", got)
	}
}

func TestAPI_ConversationErrors(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c" // the conversation key is the relay chain's root
	mustInsert(t, e.store, pending("c", "s1", "a", 1))
	errCode(t, e.get("/api/workbook/conversations/claude/nobody"), 404, "not_found")
	errCode(t, e.get("/api/workbook/conversations/codex/s1"), 404, "not_found")
	for _, q := range []string{"limit=0", "limit=201", "limit=x", "limit=-1", "before=x", "before=-3", "before=0"} {
		errCode(t, e.get("/api/workbook/conversations/claude/s1?"+q), 400, "bad_request")
	}
	// a status with no entry still finds the conversation, and its entries are [] rather than null
	e.store.SetStatus("s9", "只有狀況", 0, "s9")
	w := e.get("/api/workbook/conversations/claude/s9")
	if b := decodeConv(t, w); b.Status != "只有狀況" || len(b.Entries) != 0 || !strings.Contains(w.Body.String(), `"entries":[]`) {
		t.Fatalf("status only: %s", w.Body.String())
	}
	e.roots.err = context.DeadlineExceeded
	errCode(t, e.get("/api/workbook/conversations/claude/s1"), 500, "internal")
}

// A cursor does not change whether a conversation exists: unknown stays 404 with any `before`, and the end of paging of a
// known conversation is an empty 200. Mutation gate: skip the HasEntries check → red.
func TestAPI_PagingNeverTurnsAnUnknownConversationIntoAnEmptyOne(t *testing.T) {
	e := newAPI(t)
	id := mustInsert(t, e.store, pending("c", "s1", "a", 1))
	e.roots.root["s1"] = "c"
	errCode(t, e.get("/api/workbook/conversations/claude/nobody?before=99"), 404, "not_found")
	end := decodeConv(t, e.get("/api/workbook/conversations/claude/s1?before="+itoa(id)))
	if len(end.Entries) != 0 || end.ConvKey != "c" {
		t.Fatalf("end of paging: %+v", end)
	}
}

func TestAPI_NoStatusYetIsEmptyNotMissing(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c" // the conversation key is the relay chain's root
	mustInsert(t, e.store, pending("c", "s1", "a", 1))
	b := decodeConv(t, e.get("/api/workbook/conversations/claude/s1"))
	if b.Status != "" || b.StatusAt != 0 || len(b.Entries) != 1 {
		t.Fatalf("%+v", b)
	}
}

func TestAPI_EntriesFiltersAreMilliseconds(t *testing.T) {
	e := newAPI(t)
	a := mustInsert(t, e.store, pending("c1", "s1", "a", 1700000000000))
	b := mustInsert(t, e.store, pending("c2", "s2", "b", 1700000100000))
	c := mustInsert(t, e.store, pending("c1", "s1", "c", 1700000200000))
	e.store.Finish(a, StateOK, "", Output{Entry: "x", ThingDone: true})
	e.store.Finish(b, StateOK, "", Output{Entry: "y"})
	e.store.Finish(c, StateOK, "", Output{Entry: "z", ThingDone: true})

	list := func(q string) []entryBody {
		w := e.get("/api/workbook/entries" + q)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
		}
		var out struct{ Entries []entryBody }
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Entries
	}
	if got := list(""); len(got) != 3 || got[0].ID != c {
		t.Fatalf("all: %+v", got)
	}
	if got := list("?since=1700000050000&until=1700000200000"); len(got) != 1 || got[0].ID != b {
		t.Fatalf("window: %+v", got)
	}
	if got := list("?since=1700000200000"); len(got) != 1 || got[0].ID != c {
		t.Fatalf("since inclusive: %+v", got)
	}
	if got := list("?thing_done=1"); len(got) != 2 || got[0].ID != c || got[1].ID != a {
		t.Fatalf("thing_done: %+v", got)
	}
	if got := list("?limit=1"); len(got) != 1 {
		t.Fatalf("limit: %+v", got)
	}
}

func TestAPI_EntriesBadRequests(t *testing.T) {
	e := newAPI(t)
	for _, q := range []string{"since=x", "since=-1", "until=x", "until=-5", "thing_done=0", "thing_done=true", "thing_done=", "limit=0", "limit=201", "limit=x"} {
		errCode(t, e.get("/api/workbook/entries?"+q), 400, "bad_request")
	}
	if w := e.get("/api/workbook/entries?limit=200"); w.Code != 200 {
		t.Fatalf("limit=200: %d", w.Code)
	}
}

// A module whose store could not be opened answers 503 and says nothing else.
func TestAPI_SoftFailedModuleAnswers503(t *testing.T) {
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: "/nonexistent/dir/for/workbook"}})
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	for _, url := range []string{"/api/workbook/entries", "/api/workbook/conversations/claude/s1"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		errCode(t, w, 503, "unavailable")
	}
}

// The events follow the store; the value is a JSON string like every host event.
func TestAPI_EventsAreJSONStringsAndFollowTheStore(t *testing.T) {
	e := newAPI(t)
	id := mustInsert(t, e.store, pending("root", "s1", "a", 1700000000000))
	ev := e.sent()
	if len(ev) != 1 || ev[0].typ != "workbook.entry" {
		t.Fatalf("after insert: %+v", ev)
	}
	var got struct {
		ConvKey   string    `json:"conv_key"`
		SessionID string    `json:"session_id"`
		Entry     entryBody `json:"entry"`
	}
	if err := json.Unmarshal([]byte(ev[0].value), &got); err != nil {
		t.Fatalf("value is not a JSON string of the object: %v: %s", err, ev[0].value)
	}
	if got.ConvKey != "root" || got.SessionID != "s1" || got.Entry.ID != id || got.Entry.State != StatePending || got.Entry.TurnAt != 1700000000000 {
		t.Fatalf("entry event: %+v", got)
	}

	e.store.SetPushLine(id, "事", "推播")
	if ev := e.sent(); len(ev) != 0 {
		t.Fatalf("a push line sent %+v", ev)
	}
	e.store.SetStatus("root", "進行中", id, "s1")
	ev = e.sent()
	if len(ev) != 1 || ev[0].typ != "workbook.status" {
		t.Fatalf("after status: %+v", ev)
	}
	var st struct {
		ConvKey   string `json:"conv_key"`
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
		UpdatedAt int64  `json:"updated_at"`
	}
	if err := json.Unmarshal([]byte(ev[0].value), &st); err != nil || st.ConvKey != "root" || st.SessionID != "s1" || st.Status != "進行中" || st.UpdatedAt == 0 {
		t.Fatalf("status event: %+v err=%v (%s)", st, err, ev[0].value)
	}

	e.store.Finish(id, StateOK, "", Output{Thing: "事", Push: "推播", Entry: "做了。"})
	e.store.Finish(id, StateOK, "", Output{Entry: "again"}) // a second finish: no second ok
	ev = e.sent()
	if len(ev) != 1 || ev[0].typ != "workbook.entry" || !strings.Contains(ev[0].value, `"state":"ok"`) {
		t.Fatalf("after finish: %+v", ev)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
