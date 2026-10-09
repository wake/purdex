package workbook

import (
	"fmt"
	"strings"
	"testing"
)

// WB-1b′-a: the todo list (spec §5.4–§5.5, plan D13) and the push-line transaction.

// applyTx runs ApplyTodoChanges in its own transaction and returns what changed.
func applyTx(t *testing.T, s *Store, conv string, entry int64, ch TodoChanges, by string) TodoResult {
	t.Helper()
	var res TodoResult
	if err := s.inTx(func(tx execer) error {
		var err error
		res, err = applyTodoChanges(tx, conv, entry, ch, by, s.now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return res
}

func titles(ts []Todo) string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Title)
	}
	return strings.Join(out, "|")
}

func seedTodos(t *testing.T, s *Store, conv string, entry int64, ts ...string) []Todo {
	t.Helper()
	var adds []TodoAdd
	for _, x := range ts {
		adds = append(adds, TodoAdd{Title: x})
	}
	// the model path takes 2 adds; seed through the refresh path (10) in batches
	for len(adds) > 0 {
		n := len(adds)
		if n > 10 {
			n = 10
		}
		applyTx(t, s, conv, entry, TodoChanges{Adds: adds[:n]}, ClosedByRefresh)
		adds = adds[n:]
	}
	open, err := s.OpenTodos(conv, 100)
	if err != nil {
		t.Fatal(err)
	}
	return open
}

func TestTodos_OpenOldestFirstAndByState(t *testing.T) {
	s := openTest(t)
	e := mustInsert(t, s, pending("c", "s1", "t1", 1))
	open := seedTodos(t, s, "c", e, "甲", "乙", "丙")
	if titles(open) != "甲|乙|丙" {
		t.Fatalf("open = %s", titles(open))
	}
	applyTx(t, s, "c", e, TodoChanges{Done: []int64{open[0].ID}, Dropped: []int64{open[1].ID}}, ClosedByModel)
	if got, _ := s.OpenTodos("c", 30); titles(got) != "丙" {
		t.Fatalf("open after closing = %s", titles(got))
	}
	done, _ := s.Todos("c", TodoDone, 10, 0)
	dropped, _ := s.Todos("c", TodoDropped, 10, 0)
	if titles(done) != "甲" || titles(dropped) != "乙" {
		t.Fatalf("done=%s dropped=%s", titles(done), titles(dropped))
	}
	// newest first with an id cursor
	more := seedTodos(t, s, "c", e, "丁", "戊")
	applyTx(t, s, "c", e, TodoChanges{Done: []int64{more[1].ID, more[2].ID}}, ClosedByModel)
	all, _ := s.Todos("c", TodoDone, 10, 0)
	if len(all) != 3 || all[0].Title != "戊" {
		t.Fatalf("done newest first = %s", titles(all))
	}
	page, _ := s.Todos("c", TodoDone, 10, all[0].ID)
	if titles(page) != "丁|甲" {
		t.Fatalf("page = %s", titles(page))
	}
	if other, _ := s.OpenTodos("other", 30); len(other) != 0 {
		t.Fatalf("another conversation sees %s", titles(other))
	}
}

// The done record carries who closed it and with which entry.
func TestTodos_RecordsAddedAndClosingEntryAndBy(t *testing.T) {
	s := openTest(t)
	e1 := mustInsert(t, s, pending("c", "s1", "t1", 1))
	e2 := mustInsert(t, s, pending("c", "s1", "t2", 2))
	e3 := mustInsert(t, s, pending("c", "s1", "t3", 3))
	s.now = func() int64 { return 1000 }
	res := applyTx(t, s, "c", e1, TodoChanges{Adds: []TodoAdd{{Title: "寫測試", Detail: "先紅再綠"}}}, ClosedByModel)
	if len(res.Changed) != 1 || res.Changed[0].State != TodoOpen || res.Changed[0].AddedEntryID != e1 {
		t.Fatalf("add: %+v", res.Changed)
	}
	id := res.Changed[0].ID
	s.now = func() int64 { return 2000 }
	applyTx(t, s, "c", e2, TodoChanges{Done: []int64{id}}, ClosedByModel)
	got, _ := s.Todos("c", TodoDone, 10, 0)
	if len(got) != 1 || got[0].AddedEntryID != e1 || got[0].ClosedEntryID != e2 || got[0].ClosedBy != ClosedByModel ||
		got[0].CreatedAt != 1000 || got[0].ClosedAt != 2000 || got[0].Detail != "先紅再綠" {
		t.Fatalf("record = %+v", got)
	}
	// a todo closed by a refresh keeps closed_by: refresh
	res = applyTx(t, s, "c", e3, TodoChanges{Adds: []TodoAdd{{Title: "另一件"}}}, ClosedByModel)
	applyTx(t, s, "c", e3, TodoChanges{Dropped: []int64{res.Changed[0].ID}}, ClosedByRefresh)
	dropped, _ := s.Todos("c", TodoDropped, 10, 0)
	if len(dropped) != 1 || dropped[0].ClosedBy != ClosedByRefresh {
		t.Fatalf("dropped = %+v", dropped)
	}
}

// D13: an unknown id, another conversation's todo and one that is no longer open are ignored; done wins over dropped.
// Mutation gate: let dropped win → red.
func TestTodos_ClosingRules(t *testing.T) {
	s := openTest(t)
	e := mustInsert(t, s, pending("c", "s1", "t1", 1))
	f := mustInsert(t, s, pending("x", "s2", "t1", 1))
	open := seedTodos(t, s, "c", e, "甲", "乙", "丙")
	foreign := seedTodos(t, s, "x", f, "別人的")
	res := applyTx(t, s, "c", e, TodoChanges{
		Done:    []int64{open[0].ID, 9999, foreign[0].ID},
		Dropped: []int64{open[0].ID, open[1].ID}, // 甲 is in both lists: it counts as done
	}, ClosedByModel)
	if len(res.Changed) != 2 {
		t.Fatalf("changed = %+v", res.Changed)
	}
	if d, _ := s.Todos("c", TodoDone, 10, 0); titles(d) != "甲" {
		t.Fatalf("done = %s", titles(d))
	}
	if d, _ := s.Todos("c", TodoDropped, 10, 0); titles(d) != "乙" {
		t.Fatalf("dropped = %s", titles(d))
	}
	if o, _ := s.OpenTodos("x", 30); titles(o) != "別人的" {
		t.Fatalf("a foreign todo was closed: %s", titles(o))
	}
	// closing what is already closed changes nothing, and does not rewrite who closed it
	res = applyTx(t, s, "c", e, TodoChanges{Dropped: []int64{open[0].ID}}, ClosedByRefresh)
	if len(res.Changed) != 0 {
		t.Fatalf("a closed todo changed again: %+v", res.Changed)
	}
	if d, _ := s.Todos("c", TodoDone, 10, 0); len(d) != 1 || d[0].ClosedBy != ClosedByModel {
		t.Fatalf("done = %+v", d)
	}
}

func TestTodos_AddRules(t *testing.T) {
	s := openTest(t)
	e := mustInsert(t, s, pending("c", "s1", "t1", 1))
	long := strings.Repeat("題", 40)
	res := applyTx(t, s, "c", e, TodoChanges{Adds: []TodoAdd{
		{Title: "  " + long + "  ", Detail: strings.Repeat("甲", 60) + "。" + strings.Repeat("乙", 80)},
		{Title: "第二件", Detail: strings.Repeat("字", 150)},
		{Title: "第三件被略過"}, // only the first 2 of a turn are kept
	}}, ClosedByModel)
	if len(res.Changed) != 2 {
		t.Fatalf("a turn keeps 2 adds, got %d", len(res.Changed))
	}
	if r := []rune(res.Changed[0].Title); len(r) != 30 {
		t.Fatalf("title = %d runes", len(r))
	}
	if res.Changed[0].Detail != strings.Repeat("甲", 60)+"。" {
		t.Fatalf("detail cut at the last sentence end ≤ 100 = %q", res.Changed[0].Detail)
	}
	if r := []rune(res.Changed[1].Detail); len(r) != 100 {
		t.Fatalf("a detail with no sentence end is cut at 100, got %d", len(r))
	}
}

// A refresh may add up to 10; an empty title is nothing.
func TestTodos_RefreshAddsTenAndEmptyTitleIsDropped(t *testing.T) {
	s := openTest(t)
	e := mustInsert(t, s, pending("c", "s1", "r1", 1))
	var adds []TodoAdd
	for i := 0; i < 12; i++ {
		adds = append(adds, TodoAdd{Title: fmt.Sprintf("項目%d", i)})
	}
	adds[3].Title = "   "
	res := applyTx(t, s, "c", e, TodoChanges{Adds: adds}, ClosedByRefresh)
	// the first 10 of the list are looked at; the empty one is not a todo
	if len(res.Changed) != 9 {
		t.Fatalf("changed = %d, want 9", len(res.Changed))
	}
}

// An add equal (trimmed) to an open title is ignored — also one added earlier in the same batch.
// Mutation gate: skip the duplicate check → red.
func TestTodos_DuplicateOfAnOpenTitleIsIgnored(t *testing.T) {
	s := openTest(t)
	e := mustInsert(t, s, pending("c", "s1", "t1", 1))
	seedTodos(t, s, "c", e, "寫測試")
	res := applyTx(t, s, "c", e, TodoChanges{Adds: []TodoAdd{{Title: " 寫測試 "}, {Title: "新的"}}}, ClosedByModel)
	if titles(res.Changed) != "新的" {
		t.Fatalf("changed = %s", titles(res.Changed))
	}
	res = applyTx(t, s, "c", e, TodoChanges{Adds: []TodoAdd{{Title: "同批"}, {Title: "同批 "}}}, ClosedByModel)
	if titles(res.Changed) != "同批" {
		t.Fatalf("a duplicate inside one batch: %s", titles(res.Changed))
	}
	// a closed todo's title is free again
	open, _ := s.OpenTodos("c", 30)
	applyTx(t, s, "c", e, TodoChanges{Done: []int64{open[0].ID}}, ClosedByModel)
	res = applyTx(t, s, "c", e, TodoChanges{Adds: []TodoAdd{{Title: "寫測試"}}}, ClosedByModel)
	if titles(res.Changed) != "寫測試" {
		t.Fatalf("a closed title could not be added again: %s", titles(res.Changed))
	}
}

// 30 open is the cap, re-checked before each add: 29 open + 2 adds → 30, the rest ignored and counted.
// Mutation gate: check the cap once before the batch → red.
func TestTodos_CapIsRecheckedBeforeEachAdd(t *testing.T) {
	s := openTest(t)
	e := mustInsert(t, s, pending("c", "s1", "t1", 1))
	var names []string
	for i := 0; i < 29; i++ {
		names = append(names, fmt.Sprintf("舊%d", i))
	}
	seedTodos(t, s, "c", e, names...)
	res := applyTx(t, s, "c", e, TodoChanges{Adds: []TodoAdd{{Title: "甲"}, {Title: "乙"}}}, ClosedByModel)
	if titles(res.Changed) != "甲" || res.CapIgnored != 1 {
		t.Fatalf("changed = %s, cap ignored = %d", titles(res.Changed), res.CapIgnored)
	}
	if open, _ := s.OpenTodos("c", 100); len(open) != 30 {
		t.Fatalf("open = %d", len(open))
	}
	// at 30, a closing in the same batch frees a place first
	open, _ := s.OpenTodos("c", 100)
	res = applyTx(t, s, "c", e, TodoChanges{Done: []int64{open[0].ID}, Adds: []TodoAdd{{Title: "丙"}}}, ClosedByModel)
	if len(res.Changed) != 2 || res.Changed[0].State != TodoDone || res.Changed[1].Title != "丙" || res.CapIgnored != 0 {
		t.Fatalf("closings apply first, then the add: %+v (cap ignored %d)", res.Changed, res.CapIgnored)
	}
	if open, _ := s.OpenTodos("c", 100); len(open) != 30 {
		t.Fatalf("open = %d", len(open))
	}
}

func readyEntry(t *testing.T, s *Store, turn string) int64 {
	t.Helper()
	return mustInsert(t, s, pending("c", "s1", turn, 1))
}

// The push line, the status, the usage and the todo changes are one transaction; the entry stays pending (D3).
// Mutation gate: write the status outside the transaction → the rollback test below goes red.
func TestSetPushLineV2_OneTransactionEntryStaysPending(t *testing.T) {
	s, r := observed(t)
	s.now = func() int64 { return 5000 }
	prev := readyEntry(t, s, "t0")
	seed := seedTodos(t, s, "c", prev, "舊待辦")
	id := readyEntry(t, s, "t1")
	r.take()
	changed, ok, err := s.SetPushLineV2(id, PushLineV2{
		Thing: "事", Push: "推播", Status: "進行中",
		Usage: Usage{In: 100, Out: 20, CacheRead: 80},
		Todos: TodoChanges{Done: []int64{seed[0].ID}, Adds: []TodoAdd{{Title: "新待辦"}}},
		By:    ClosedByModel,
	})
	if err != nil || !ok || len(changed) != 2 {
		t.Fatalf("ok=%v err=%v changed=%+v", ok, err, changed)
	}
	e, _ := s.Entry(id)
	if e.State != StatePending || e.Thing != "事" || e.Push != "推播" || e.PushReadyAt != 5000 ||
		e.UsageIn != 100 || e.UsageOut != 20 || e.UsageCacheRead != 80 {
		t.Fatalf("entry = %+v", e)
	}
	if st, _, _ := s.Status("c"); st.Status != "進行中" || st.EntryID != id || st.SessionID != "s1" {
		t.Fatalf("status = %+v", st)
	}
	if o, _ := s.OpenTodos("c", 30); titles(o) != "新待辦" {
		t.Fatalf("open = %s", titles(o))
	}
	// a status event, and no entry event (the entry is still pending)
	ev := r.take()
	if len(ev) != 1 || ev[0].Kind != EventStatus || ev[0].Status.Status != "進行中" {
		t.Fatalf("events = %+v", ev)
	}
}

// An entry that is no longer pending takes nothing: no status, no todos.
func TestSetPushLineV2_NotPendingChangesNothing(t *testing.T) {
	s, r := observed(t)
	id := readyEntry(t, s, "t1")
	s.Finish(id, StateFailed, ReasonStopped, Output{})
	r.take()
	changed, ok, err := s.SetPushLineV2(id, PushLineV2{Thing: "x", Status: "不該寫入", Todos: TodoChanges{Adds: []TodoAdd{{Title: "不該有"}}}, By: ClosedByModel})
	if err != nil || ok || len(changed) != 0 {
		t.Fatalf("ok=%v changed=%v err=%v", ok, changed, err)
	}
	if _, has, _ := s.Status("c"); has {
		t.Fatal("a status was written for a final entry")
	}
	if o, _ := s.OpenTodos("c", 30); len(o) != 0 {
		t.Fatalf("todos were added for a final entry: %s", titles(o))
	}
	if ev := r.take(); len(ev) != 0 {
		t.Fatalf("events = %+v", ev)
	}
}

// If any part fails the whole is undone: no push line, no status, no todos.
func TestSetPushLineV2_FailureRollsEverythingBack(t *testing.T) {
	s := openTest(t)
	id := readyEntry(t, s, "t1")
	// the todo table is gone: the last part of the transaction fails
	if _, err := s.db.Exec(`DROP TABLE wb_todos`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.SetPushLineV2(id, PushLineV2{Thing: "事", Push: "推播", Status: "狀況", Todos: TodoChanges{Adds: []TodoAdd{{Title: "x"}}}, By: ClosedByModel}); err == nil || ok {
		t.Fatalf("ok=%v err=%v, want an error", ok, err)
	}
	e, _ := s.Entry(id)
	if e.Thing != "" || e.Push != "" || e.PushReadyAt != 0 {
		t.Fatalf("the push line survived a failed transaction: %+v", e)
	}
	if _, has, _ := s.Status("c"); has {
		t.Fatal("the status survived a failed transaction")
	}
}

// skip: true → the entry is skipped: model and its todos are still applied, in one transaction.
func TestFinishSkippedV2_AppliesTheTodosToo(t *testing.T) {
	s, r := observed(t)
	prev := readyEntry(t, s, "t0")
	seed := seedTodos(t, s, "c", prev, "等回覆")
	id := readyEntry(t, s, "t1")
	r.take()
	changed, ok, err := s.FinishSkippedV2(id, "model", Usage{In: 7}, TodoChanges{Done: []int64{seed[0].ID}}, ClosedByModel)
	if err != nil || !ok || len(changed) != 1 {
		t.Fatalf("ok=%v err=%v changed=%v", ok, err, changed)
	}
	e, _ := s.Entry(id)
	if e.State != StateSkipped || e.Reason != "model" || e.UsageIn != 7 {
		t.Fatalf("entry = %+v", e)
	}
	if d, _ := s.Todos("c", TodoDone, 10, 0); titles(d) != "等回覆" {
		t.Fatalf("done = %s", titles(d))
	}
	if ev := r.take(); len(ev) != 1 || ev[0].Kind != EventEntry || ev[0].Entry.State != StateSkipped {
		t.Fatalf("events = %+v", ev)
	}
}
