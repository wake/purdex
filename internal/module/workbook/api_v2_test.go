package workbook

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// WB-2b-i (a): the v2 read surface — kind / usage / todo_changes on entries, the conversation's todos, GET …/todos and the
// workbook.todos event (spec §9).

type todoBody struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Detail        string `json:"detail"`
	State         string `json:"state"`
	ClosedBy      string `json:"closed_by"`
	CreatedAt     int64  `json:"created_at"`
	ClosedAt      int64  `json:"closed_at"`
	AddedEntryID  int64  `json:"added_entry_id"`
	ClosedEntryID int64  `json:"closed_entry_id"`
}

type idTitle struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type v2Entry struct {
	entryBody
	Kind  string `json:"kind"`
	Usage struct {
		In        int64 `json:"in"`
		Out       int64 `json:"out"`
		CacheRead int64 `json:"cache_read"`
	} `json:"usage"`
	TodoChanges struct {
		Added   []idTitle `json:"added"`
		Done    []idTitle `json:"done"`
		Dropped []idTitle `json:"dropped"`
	} `json:"todo_changes"`
}

type v2Conv struct {
	Entries []v2Entry `json:"entries"`
	Todos   struct {
		Open []todoBody `json:"open"`
		Done []todoBody `json:"done"`
	} `json:"todos"`
}

func decodeV2(t *testing.T, w *httptest.ResponseRecorder) v2Conv {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var b v2Conv
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("%v: %s", err, w.Body.String())
	}
	return b
}

func addTodos(t *testing.T, e *apiEnv, conv string, entry int64, ts ...string) {
	t.Helper()
	seedTodos(t, e.store, conv, entry, ts...)
}

// An entry carries its kind, its tokens and the todo changes it made; an entry that changed nothing has three empty lists,
// never null. Mutation gate: drop the todo_changes lookup → red.
func TestAPI_V2EntryShape(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c"
	a := mustInsert(t, e.store, pending("c", "s1", "a", 1))
	b := mustInsert(t, e.store, pending("c", "s1", "b", 2))
	addTodos(t, e, "c", a, "甲", "乙", "丙")
	open, _ := e.store.OpenTodos("c", 30)
	applyTx(t, e.store, "c", b, TodoChanges{Done: []int64{open[0].ID}, Dropped: []int64{open[1].ID}}, ClosedByModel)
	e.store.SetPushLineV2(a, PushLineV2{Thing: "x", Push: "y", Status: "z", Usage: Usage{In: 11, Out: 22, CacheRead: 33}})
	e.store.Finish(a, StateOK, "", Output{Thing: "x", Push: "y", Entry: "e", Usage: Usage{In: 11, Out: 22, CacheRead: 33}})
	e.store.Finish(b, StateSkipped, "model", Output{})

	got := decodeV2(t, e.get("/api/workbook/conversations/claude/s1")).Entries
	if len(got) != 2 {
		t.Fatalf("entries = %+v", got)
	}
	nb, na := got[0], got[1] // newest first
	if na.Kind != KindTurn || na.Usage.In != 11 || na.Usage.Out != 22 || na.Usage.CacheRead != 33 {
		t.Fatalf("a: %+v", na)
	}
	if len(na.TodoChanges.Added) != 3 || na.TodoChanges.Added[0].Title != "甲" || len(na.TodoChanges.Done) != 0 || len(na.TodoChanges.Dropped) != 0 {
		t.Fatalf("a changes: %+v", na.TodoChanges)
	}
	if len(nb.TodoChanges.Done) != 1 || nb.TodoChanges.Done[0].Title != "甲" || len(nb.TodoChanges.Dropped) != 1 || nb.TodoChanges.Dropped[0].Title != "乙" || len(nb.TodoChanges.Added) != 0 {
		t.Fatalf("b changes: %+v", nb.TodoChanges)
	}
	raw := e.get("/api/workbook/conversations/claude/s1").Body.String()
	if strings.Contains(raw, `"added":null`) || strings.Contains(raw, `"done":null`) || strings.Contains(raw, `"dropped":null`) {
		t.Fatalf("a null list: %s", raw)
	}
}

// The across-conversations list carries the same v2 fields.
func TestAPI_V2EntriesListCarriesTodoChanges(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c"
	a := mustInsert(t, e.store, pending("c", "s1", "a", 1))
	addTodos(t, e, "c", a, "甲")
	w := e.get("/api/workbook/entries")
	var body struct {
		Entries []v2Entry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Entries) != 1 || len(body.Entries[0].TodoChanges.Added) != 1 || body.Entries[0].Kind != KindTurn {
		t.Fatalf("%v %s", err, w.Body.String())
	}
}

// The conversation answer carries the open todos (oldest first) and the newest 20 done ones.
func TestAPI_V2ConversationTodos(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c"
	a := mustInsert(t, e.store, pending("c", "s1", "a", 1))
	var names []string
	for i := 0; i < 25; i++ {
		names = append(names, "t"+itoa(int64(i)))
	}
	addTodos(t, e, "c", a, names...) // 25 open
	open, _ := e.store.OpenTodos("c", 30)
	var ids []int64
	for _, o := range open[:23] {
		ids = append(ids, o.ID)
	}
	applyTx(t, e.store, "c", a, TodoChanges{Done: ids}, ClosedByModel) // 23 done, 2 open
	b := decodeV2(t, e.get("/api/workbook/conversations/claude/s1"))
	if len(b.Todos.Open) != 2 || b.Todos.Open[0].Title != "t23" || b.Todos.Open[1].Title != "t24" {
		t.Fatalf("open = %+v", b.Todos.Open)
	}
	if len(b.Todos.Done) != 20 || b.Todos.Done[0].Title != "t22" || b.Todos.Done[19].Title != "t3" {
		t.Fatalf("done = %d, first %q last %q", len(b.Todos.Done), b.Todos.Done[0].Title, b.Todos.Done[len(b.Todos.Done)-1].Title)
	}
	d := b.Todos.Done[0]
	if d.State != TodoDone || d.ClosedBy != ClosedByModel || d.AddedEntryID != a || d.ClosedEntryID != a || d.CreatedAt == 0 || d.ClosedAt == 0 {
		t.Fatalf("a done todo: %+v", d)
	}
	if o := b.Todos.Open[0]; o.State != TodoOpen || o.ClosedEntryID != 0 || o.ClosedAt != 0 {
		t.Fatalf("an open todo: %+v", o)
	}
}

// An empty list is [] rather than null.
func TestAPI_V2EmptyTodosAreLists(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c"
	mustInsert(t, e.store, pending("c", "s1", "a", 1))
	raw := e.get("/api/workbook/conversations/claude/s1").Body.String()
	if !strings.Contains(raw, `"todos":{"open":[],"done":[]}`) {
		t.Fatalf("todos: %s", raw)
	}
}

// GET …/todos pages the list by todo id, newest first, one state at a time. Mutation gate: ignore `before` → red.
func TestAPI_V2TodosRoute(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c"
	e.roots.root["s2"] = "c"
	a := mustInsert(t, e.store, pending("c", "s1", "a", 1))
	addTodos(t, e, "c", a, "一", "二", "三", "四", "五")
	open, _ := e.store.OpenTodos("c", 30)
	applyTx(t, e.store, "c", a, TodoChanges{Done: []int64{open[0].ID, open[1].ID, open[2].ID}, Dropped: []int64{open[3].ID}}, ClosedByModel)

	get := func(q string) []todoBody {
		w := e.get("/api/workbook/conversations/claude/s2/todos?" + q)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
		}
		var b struct {
			Todos []todoBody `json:"todos"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b.Todos
	}
	if got := get("state=open"); len(got) != 1 || got[0].Title != "五" {
		t.Fatalf("open = %+v", got)
	}
	if got := get("state=dropped"); len(got) != 1 || got[0].Title != "四" {
		t.Fatalf("dropped = %+v", got)
	}
	p1 := get("state=done&limit=2")
	if len(p1) != 2 || p1[0].Title != "三" || p1[1].Title != "二" {
		t.Fatalf("page 1 = %+v", p1)
	}
	p2 := get("state=done&limit=2&before=" + itoa(p1[1].ID))
	if len(p2) != 1 || p2[0].Title != "一" {
		t.Fatalf("page 2 = %+v", p2)
	}
	if got := get("state=done&before=" + itoa(p2[0].ID)); got == nil || len(got) != 0 {
		t.Fatalf("exhausted = %#v", got)
	}
	if got := get(""); len(got) != 1 || got[0].Title != "五" { // state defaults to open
		t.Fatalf("default state = %+v", got)
	}
}

func TestAPI_V2TodosRouteErrors(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c"
	mustInsert(t, e.store, pending("c", "s1", "a", 1))
	for _, q := range []string{"state=all", "state=", "limit=0", "limit=201", "before=0", "before=x"} {
		errCode(t, e.get("/api/workbook/conversations/claude/s1/todos?"+q), 400, "bad_request")
	}
	errCode(t, e.get("/api/workbook/conversations/codex/s1/todos"), 404, "not_found")
	errCode(t, e.get("/api/workbook/conversations/claude/nobody/todos"), 404, "not_found")
}

// A conversation that has todos but no entries any more still answers (the entries are what proves it exists).
// The todos route and the conversation answer agree on which conversations exist.
func TestAPI_V2TodosRouteSameExistenceAsConversation(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c"
	e.store.SetStatus("s9", "只有狀況", 0, "s9")
	w := e.get("/api/workbook/conversations/claude/s9/todos")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"todos":[]`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// workbook.todos: one event per change, carrying the changed todos in the order they changed; a turn that changed none
// sends none. Mutation gate: drop the emit in SetPushLineV2 → red.
func TestAPI_V2TodosEventPerChange(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c"
	a := mustInsert(t, e.store, pending("c", "s1", "a", 1))
	e.sent()
	e.store.SetPushLineV2(a, PushLineV2{Thing: "x", Push: "y", Status: "z", Todos: TodoChanges{Adds: []TodoAdd{{Title: "甲"}, {Title: "乙"}}}, By: ClosedByModel})
	var todoEvents []sentEvent
	for _, ev := range e.sent() {
		if ev.typ == "workbook.todos" {
			todoEvents = append(todoEvents, ev)
		}
	}
	if len(todoEvents) != 1 {
		t.Fatalf("events = %+v", todoEvents)
	}
	var v struct {
		ConvKey   string     `json:"conv_key"`
		SessionID string     `json:"session_id"`
		Todos     []todoBody `json:"todos"`
	}
	if err := json.Unmarshal([]byte(todoEvents[0].value), &v); err != nil || v.ConvKey != "c" || v.SessionID != "s1" || len(v.Todos) != 2 || v.Todos[0].Title != "甲" || v.Todos[1].State != TodoOpen {
		t.Fatalf("%v %s", err, todoEvents[0].value)
	}
	// a turn with no todo change sends no todos event
	b := mustInsert(t, e.store, pending("c", "s1", "b", 2))
	e.sent()
	e.store.SetPushLineV2(b, PushLineV2{Thing: "x", Push: "y", Status: "z"})
	e.store.FinishSkippedV2(mustInsert(t, e.store, pending("c", "s1", "c", 3)), ReasonModel, Usage{}, 0, TodoChanges{}, ClosedByModel)
	for _, ev := range e.sent() {
		if ev.typ == "workbook.todos" {
			t.Fatalf("an event with no change: %+v", ev)
		}
	}
	// a skipped turn that still closed a todo does
	open, _ := e.store.OpenTodos("c", 30)
	d := mustInsert(t, e.store, pending("c", "s1", "d", 4))
	e.sent()
	e.store.FinishSkippedV2(d, ReasonModel, Usage{}, 0, TodoChanges{Done: []int64{open[0].ID}}, ClosedByModel)
	got := 0
	for _, ev := range e.sent() {
		if ev.typ == "workbook.todos" {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("skipped-with-changes events = %d", got)
	}
}

// A workbook.entry event carries the same v2 fields as the route (so an App merging events sees the todo lines too).
func TestAPI_V2EntryEventCarriesTodoChanges(t *testing.T) {
	e := newAPI(t)
	e.roots.root["s1"] = "c"
	a := mustInsert(t, e.store, pending("c", "s1", "a", 1))
	e.store.SetPushLineV2(a, PushLineV2{Thing: "x", Push: "y", Status: "z", Todos: TodoChanges{Adds: []TodoAdd{{Title: "甲"}}}, By: ClosedByModel})
	e.sent()
	e.store.Finish(a, StateOK, "", Output{Thing: "x", Push: "y", Entry: "e"})
	for _, ev := range e.sent() {
		if ev.typ != "workbook.entry" {
			continue
		}
		var v struct {
			Entry v2Entry `json:"entry"`
		}
		if err := json.Unmarshal([]byte(ev.value), &v); err != nil || v.Entry.Kind != KindTurn || len(v.Entry.TodoChanges.Added) != 1 {
			t.Fatalf("%v %s", err, ev.value)
		}
		return
	}
	t.Fatal("no entry event")
}
