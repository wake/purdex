package workbook

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/team"
)

// fakeProjects resolves every unit to one temp worktree and remembers what it was asked.
type fakeProjects struct {
	wt    string
	calls []UnitRef
	err   error
}

func (f *fakeProjects) Project(ref UnitRef) (UnitProject, error) {
	f.calls = append(f.calls, ref)
	if f.err != nil {
		return UnitProject{}, f.err
	}
	return UnitProject{ProjectID: "proj1", WtPath: f.wt, Label: "專案 A", LeadConv: ref.ConvKey}, nil
}

type drainKit struct {
	t    *testing.T
	st   *Store
	fp   *fakeProjects
	d    *Drainer
	logs []string
	now  time.Time
}

func newDrainKit(t *testing.T) *drainKit {
	t.Helper()
	k := &drainKit{t: t, st: openTest(t), fp: &fakeProjects{wt: t.TempDir()}, now: time.Unix(1_700_000_000, 0)}
	k.st.EnableJournalOutbox()
	k.d = NewDrainer(k.st, k.fp, func(f string, a ...any) { k.logs = append(k.logs, f) })
	k.d.now = func() time.Time { return k.now }
	return k
}

func (k *drainKit) drain() int {
	k.t.Helper()
	n, err := k.d.Drain()
	if err != nil {
		k.t.Fatal(err)
	}
	return n
}

// dir is the journal folder of the unit key (the label is the fake's "專案 A").
func (k *drainKit) dir(key string) string {
	return filepath.Join(k.fp.wt, ".purdex", "journal", unitSlug("專案 A", key))
}

// entries reads every entry of the unit's month files.
func (k *drainKit) entries(key string) []journal.Entry {
	k.t.Helper()
	files, _ := filepath.Glob(filepath.Join(k.dir(key), "*.md"))
	var out []journal.Entry
	for _, f := range files {
		es, err := journal.Parse(f)
		if err != nil {
			k.t.Fatal(err)
		}
		out = append(out, es...)
	}
	return out
}

func (k *drainKit) byID(key, id string) (journal.Entry, bool) {
	for _, e := range k.entries(key) {
		if e.ID == id {
			return e, true
		}
	}
	return journal.Entry{}, false
}

// okEntry finishes entry id as ok with the given text.
func (k *drainKit) okEntry(id int64, text string, thingDone bool) {
	k.t.Helper()
	if ok, err := k.st.Finish(id, StateOK, "", Output{Thing: "事", Push: "推", Entry: text, ThingDone: thingDone}); err != nil || !ok {
		k.t.Fatal(ok, err)
	}
}

func (k *drainKit) turn(conv, turn, role string, ch TodoChanges) int64 {
	k.t.Helper()
	e := pending(conv, conv+"-s", turn, 1_700_000_000_000)
	e.Role, e.TeamID = role, "team-7"
	if role == team.SeatNone {
		e.TeamID = ""
	}
	id := mustInsert(k.t, k.st, e)
	if _, ok, err := k.st.SetPushLineV2(id, PushLineV2{Thing: "事", Push: "推", Status: "狀況", Todos: ch, By: ClosedByModel}); err != nil || !ok {
		k.t.Fatal(ok, err)
	}
	return id
}

// A row is not written while its entry is pending (a re-write has not finished: the final text is not known); once the
// entry is ok the exec entry carries the FINAL text. thing_done chooses exec_done.
// Mutation gate: drop `e.state <> 'pending'` from ReadyOutbox -> the pre-rewrite pass writes a file (red); write the
// push line instead of the final entry -> wrong text (red); ignore ThingDone -> wrong kind (red).
func TestDrain_WaitsForTheEntryAndWritesItsFinalText(t *testing.T) {
	k := newDrainKit(t)
	id := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	if n := k.drain(); n != 0 || len(k.fp.calls) != 0 {
		t.Fatalf("drained %d rows (%d lookups) while the entry is pending", n, len(k.fp.calls))
	}
	k.okEntry(id, "改寫後的最終紀錄。", true)
	if n := k.drain(); n != 1 {
		t.Fatalf("drained %d rows", n)
	}
	e, ok := k.byID("solo:c", "turn:"+itoa(id))
	if !ok || e.Text != "改寫後的最終紀錄。" || e.Kind != journal.KindExecDone || e.At.UnixMilli() != 1_700_000_000_000 {
		t.Fatalf("exec entry = %+v ok=%v", e, ok)
	}
	if u, ok, err := k.st.Unit("solo:c"); err != nil || !ok || u.Kind != UnitSolo || u.ProjectID != "proj1" || u.WtPath != k.fp.wt {
		t.Fatalf("unit = %+v %v %v", u, ok, err)
	}
	if rows := outboxRows(t, k.st); len(rows) != 1 || rows[0].Done == 0 {
		t.Fatalf("outbox = %+v", rows)
	}
}

// A row left undone by a crash (the file was written, the done mark was not) is written again by a NEW drainer, and the
// files hold the entry once.
// Mutation gate: a fresh entry id per write (time in the id) -> a duplicate after the replay (red).
func TestDrain_ReplayAfterACrashWritesOnce(t *testing.T) {
	k := newDrainKit(t)
	id := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	k.okEntry(id, "紀錄。", false)
	k.d.markDone = func(int64) error { return errors.New("crash before the mark") }
	if n := k.drain(); n != 0 {
		t.Fatalf("drained %d with a failing mark", n)
	}
	if rows := outboxRows(t, k.st); rows[0].Done != 0 {
		t.Fatal("the row was marked done")
	}
	k2 := &drainKit{t: t, st: k.st, fp: k.fp, now: k.now}
	k2.d = NewDrainer(k.st, k.fp, nil) // the daemon restarted
	if n, err := k2.d.Drain(); err != nil || n != 1 {
		t.Fatalf("replay: %d %v", n, err)
	}
	if es := k.entries("solo:c"); len(es) != 1 || es[0].Kind != journal.KindExec {
		t.Fatalf("entries after the replay = %+v", es)
	}
	if len(k.fp.calls) != 1 {
		t.Fatalf("the project was resolved %d times, want once (the unit is registered)", len(k.fp.calls))
	}
}

// A failed / store entry (an orphan finished late) drains its events but writes no exec entry; a skipped turn writes its
// events too.
// Mutation gate: write the exec entry for any non-pending state -> an exec entry for the failed one (red).
func TestDrain_FailedAndSkippedEntriesWriteEventsWithoutExec(t *testing.T) {
	k := newDrainKit(t)
	failed := k.turn("c", "t1", team.SeatLead, TodoChanges{})
	if ok, err := k.st.Finish(failed, StateFailed, ReasonStore, Output{}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := k.st.db.Exec(`UPDATE wb_entries SET entry = '不該出現' WHERE id = ?`, failed); err != nil { // only the state may keep it out
		t.Fatal(err)
	}
	if err := k.st.RecordJournalResult(journalOutcomeEvents(failed)); err != nil {
		t.Fatal(err)
	}
	skipped := mustInsert(t, k.st, func() Entry {
		e := pending("c", "c-s", "t2", 1_700_000_001_000)
		e.Role, e.TeamID = team.SeatLead, "team-7"
		return e
	}())
	if _, ok, err := k.st.FinishSkippedV2(skipped, ReasonModel, Usage{}, 0, TodoChanges{}, ClosedByModel); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := k.st.RecordJournalResult(journalOutcomeEvents(skipped)); err != nil {
		t.Fatal(err)
	}
	if n := k.drain(); n != 4 {
		t.Fatalf("drained %d rows, want 4", n)
	}
	es := k.entries("team:team-7")
	var kinds []journal.Kind
	for _, e := range es {
		kinds = append(kinds, e.Kind)
	}
	if len(es) != 2 || es[0].Kind != journal.KindComm || es[1].Kind != journal.KindComm {
		t.Fatalf("entries = %v", kinds)
	}
	if ref := k.fp.calls[0]; ref.Kind != UnitTeam || ref.TeamID != "team-7" || ref.SessionID != "c-s" {
		t.Fatalf("project ref = %+v (a team unit resolves from the lead's session)", ref)
	}
}

func journalOutcomeEvents(entry int64) JournalOutcome {
	return JournalOutcome{EntryID: entry, PromptVer: 1, Result: JournalResult{Events: []JournalEvent{{Kind: journal.KindComm, Text: "通知部署", Actor: "1f → 88"}}}}
}

// A journal row waits for its turn's row: when the turn row cannot be written (the files are blocked), the journal row
// is not written either; once the files are fine, both are, the turn's first. A file error never touches the entry, and
// the retry waits for its delay.
// Mutation gate: drop the NOT EXISTS clause of ReadyOutbox -> the journal row is written before the turn row (red);
// drop the retry delay -> the second pass retries at once (red); mark done on error -> the row is lost (red).
func TestDrain_FileErrorRetriesWithBackoffInOrderAndLeavesTheEntry(t *testing.T) {
	k := newDrainKit(t)
	id := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	k.okEntry(id, "紀錄。", false)
	if err := k.st.RecordJournalResult(journalOutcomeEvents(id)); err != nil {
		t.Fatal(err)
	}
	before := mustEntry(t, k.st, id)
	blocked := filepath.Join(k.fp.wt, ".purdex")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil { // a file where the folder must be
		t.Fatal(err)
	}
	if n := k.drain(); n != 0 {
		t.Fatalf("drained %d through a blocked folder", n)
	}
	if len(k.logs) != 1 {
		t.Fatalf("logs = %v, want one", k.logs)
	}
	calls := len(k.fp.calls)
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if n := k.drain(); n != 0 { // inside the delay
		t.Fatalf("retried before its delay: %d", n)
	}
	k.now = k.now.Add(retryBase + time.Second)
	if n := k.drain(); n != 2 {
		t.Fatalf("after the delay: %d rows, want 2", n)
	}
	es := k.entries("solo:c")
	if len(es) != 2 || es[0].Kind != journal.KindExec || es[1].Kind != journal.KindComm {
		t.Fatalf("entries = %+v", es)
	}
	if after := mustEntry(t, k.st, id); after != before {
		t.Fatalf("the entry changed: %+v -> %+v", before, after)
	}
	_ = calls
	if len(k.logs) != 1 {
		t.Fatalf("logs = %v", k.logs)
	}
}

// The error log of one row is throttled to once an hour while it keeps failing.
// Mutation gate: log on every failure -> several lines (red).
func TestDrain_RepeatedFailureLogsOncePerHour(t *testing.T) {
	k := newDrainKit(t)
	k.fp.err = errors.New("no cwd")
	id := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	k.okEntry(id, "紀錄。", false)
	for i := 0; i < 5; i++ {
		k.drain()
		k.now = k.now.Add(retryMax + time.Second)
	}
	if len(k.logs) != 1 {
		t.Fatalf("%d log lines in 25 minutes, want 1", len(k.logs))
	}
	k.now = k.now.Add(2 * time.Hour)
	k.drain()
	if len(k.logs) != 2 {
		t.Fatalf("%d log lines after an hour, want 2", len(k.logs))
	}
}

// Solo todos become task entries (#seq title), done todos a task_done entry, a dropped one none; tasks.md is rewritten
// from the conversation's todos. A team unit mirrors none of this.
// Mutation gate: write task entries for a team unit -> red; write one for a dropped todo -> red; skip tasks.md -> red.
func TestDrain_SoloTodosBecomeTaskEntriesAndTheTasksFile(t *testing.T) {
	k := newDrainKit(t)
	a := k.turn("c", "t1", team.SeatNone, adds("寫測試", "丟掉的"))
	k.okEntry(a, "第一輪。", false)
	todos, _ := k.st.OpenTodos("c", 10)
	b := mustInsert(t, k.st, func() Entry {
		e := pending("c", "c-s", "t2", 1_700_000_002_000)
		e.Role, e.TeamID = team.SeatNone, ""
		return e
	}())
	if _, ok, err := k.st.SetPushLineV2(b, PushLineV2{Thing: "x", Todos: TodoChanges{Done: []int64{todos[0].ID}, Dropped: []int64{todos[1].ID}, Adds: []TodoAdd{{Title: "改實作"}}}, By: ClosedByModel}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	k.okEntry(b, "第二輪。", false)
	lead := k.turn("l", "t1", team.SeatLead, adds("領導的任務"))
	k.okEntry(lead, "領導。", false)
	if n := k.drain(); n != 3 {
		t.Fatalf("drained %d", n)
	}
	got := map[string]string{}
	for _, e := range k.entries("solo:c") {
		got[e.ID] = string(e.Kind) + "|" + e.Text
	}
	want := map[string]string{
		"todo:" + itoa(todos[0].ID) + ":new":   "task_new|#1 寫測試",
		"todo:" + itoa(todos[1].ID) + ":new":   "task_new|#2 丟掉的",
		"todo:" + itoa(todos[1].ID+1) + ":new": "task_new|#3 改實作",
		"todo:" + itoa(todos[0].ID) + ":done":  "task_done|#1 寫測試",
		"turn:" + itoa(a):                      "exec|第一輪。",
		"turn:" + itoa(b):                      "exec|第二輪。",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("entry %s = %q, want %q", id, got[id], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("entries = %v", got)
	}
	tasks, err := journal.ParseTasks(filepath.Join(k.fp.wt, ".purdex", "tasks", unitSlug("專案 A", "solo:c")+".md"))
	if err != nil || len(tasks) != 3 || states(tasks) != "#1:done #2:dropped #3:todo" {
		t.Fatalf("tasks = %+v %v", tasks, err)
	}
	for _, e := range k.entries("team:team-7") {
		if e.Kind == journal.KindTaskNew {
			t.Errorf("a team unit mirrored a lead todo: %+v", e)
		}
	}
}

// states lists a task list as "#n:state" in number order.
func states(ts []journal.Task) string {
	by := map[string]journal.TaskState{}
	for _, t := range ts {
		by[t.Key] = t.State
	}
	var out []string
	for _, k := range []string{"#1", "#2", "#3"} {
		out = append(out, k+":"+string(by[k]))
	}
	return strings.Join(out, " ")
}

// A confirm is written pinned; a journal result that ticks its items marks them, and the last tick unpins it. A tick
// for an entry or item that is not there is skipped without blocking the row. Roadmap statuses are applied; a missing
// roadmap file is not an error.
// Mutation gate: pin never cleared -> red; unpin on the first tick -> red; fail on a missing entry -> the row stays
// undone (red); fail on a missing roadmap file -> red.
func TestDrain_ConfirmTicksAndRoadmap(t *testing.T) {
	k := newDrainKit(t)
	a := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	k.okEntry(a, "。", false)
	conf := JournalOutcome{EntryID: a, PromptVer: 1, Result: JournalResult{Events: []JournalEvent{{Kind: journal.KindConfirm, Text: "請確認", Items: []string{"甲?", "乙?"}, Detail: "細節"}}}}
	if err := k.st.RecordJournalResult(conf); err != nil {
		t.Fatal(err)
	}
	k.drain()
	cid := "ev:" + itoa(a) + ":0"
	if e, ok := k.byID("solo:c", cid); !ok || !e.Pinned || len(e.Items) != 2 || e.Items[0].Done || e.Detail != "細節" {
		t.Fatalf("confirm = %+v %v", e, ok)
	}
	b := mustInsert(t, k.st, func() Entry {
		e := pending("c", "c-s", "t2", 1_700_000_002_000)
		e.Role = team.SeatNone
		e.TeamID = ""
		return e
	}())
	if _, ok, err := k.st.FinishSkippedV2(b, ReasonModel, Usage{}, 0, TodoChanges{}, ClosedByModel); !ok || err != nil {
		t.Fatal(ok, err)
	}
	tick := func(entry string, idx int) JournalOutcome {
		return JournalOutcome{EntryID: b, PromptVer: 1, Result: JournalResult{Ticks: []ConfirmTick{{EntryID: entry, ItemIndex: idx}, {EntryID: "ev:nope:0", ItemIndex: 0}, {EntryID: entry, ItemIndex: 9}},
			Roadmap: []RoadmapChange{{Feature: "F", Stage: "S", Status: "完成"}}}}
	}
	if err := k.st.RecordJournalResult(tick(cid, 0)); err != nil {
		t.Fatal(err)
	}
	if n := k.drain(); n != 2 { // the skipped turn's turn row and its journal row
		t.Fatalf("drained %d", n)
	}
	if e, _ := k.byID("solo:c", cid); !e.Items[0].Done || e.Items[1].Done || !e.Pinned {
		t.Fatalf("after one tick: %+v", e)
	}
	// second tick on a third entry
	c := mustInsert(t, k.st, func() Entry {
		e := pending("c", "c-s", "t3", 1_700_000_003_000)
		e.Role = team.SeatNone
		e.TeamID = ""
		return e
	}())
	if _, ok, err := k.st.FinishSkippedV2(c, ReasonModel, Usage{}, 0, TodoChanges{}, ClosedByModel); !ok || err != nil {
		t.Fatal(ok, err)
	}
	o := tick(cid, 1)
	o.EntryID = c
	if err := k.st.RecordJournalResult(o); err != nil {
		t.Fatal(err)
	}
	k.drain()
	if e, _ := k.byID("solo:c", cid); !e.Items[1].Done || e.Pinned {
		t.Fatalf("after the last tick: %+v", e)
	}
	// a roadmap file that exists is changed in the one cell
	road := filepath.Join(k.fp.wt, ".purdex", "roadmap.md")
	if err := os.WriteFile(road, []byte("## F\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| S | x | 進行中 |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := mustInsert(t, k.st, func() Entry {
		e := pending("c", "c-s", "t4", 1_700_000_004_000)
		e.Role = team.SeatNone
		e.TeamID = ""
		return e
	}())
	if _, ok, err := k.st.FinishSkippedV2(d, ReasonModel, Usage{}, 0, TodoChanges{}, ClosedByModel); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := k.st.RecordJournalResult(JournalOutcome{EntryID: d, PromptVer: 1, Result: JournalResult{Roadmap: []RoadmapChange{{Feature: "F", Stage: "S", Status: "完成"}}}}); err != nil {
		t.Fatal(err)
	}
	k.drain()
	b2, _ := os.ReadFile(road)
	if !strings.Contains(string(b2), "| S | x | 完成 |") {
		t.Fatalf("roadmap = %q", b2)
	}
	for _, r := range outboxRows(t, k.st) {
		if r.Done == 0 {
			t.Errorf("row %+v left undone", r)
		}
	}
}

// The unit's project is asked once; the registration decides afterwards, even if the resolver would now answer
// differently (a lead that moved, a relay).
// Mutation gate: resolve on every row -> the second call lands in the other worktree (red).
func TestDrain_RegistrationFixesTheProject(t *testing.T) {
	k := newDrainKit(t)
	a := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	k.okEntry(a, "一。", false)
	k.drain()
	k.fp.wt = t.TempDir()
	b := mustInsert(t, k.st, func() Entry {
		e := pending("c", "c-s", "t2", 1_700_000_002_000)
		e.Role = team.SeatNone
		e.TeamID = ""
		return e
	}())
	if _, ok, err := k.st.SetPushLineV2(b, PushLineV2{Thing: "x"}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	k.okEntry(b, "二。", false)
	k.drain()
	if len(k.fp.calls) != 1 {
		t.Fatalf("resolver asked %d times", len(k.fp.calls))
	}
	u, _, _ := k.st.Unit("solo:c")
	files, _ := filepath.Glob(filepath.Join(u.WtPath, ".purdex", "journal", "*", "*.md"))
	if len(files) != 1 {
		t.Fatalf("files in the registered worktree = %v", files)
	}
	if es, _ := journal.Parse(files[0]); len(es) != 2 {
		t.Fatalf("entries = %+v", es)
	}
}

// A refresh entry's `turn` row (its todo changes) is drained, but its 「重整：…」 line is not written as an exec entry.
// Mutation gate: drop the Kind check in writeTurn -> a turn:<id> entry appears (red).
func TestDrain_RefreshRowWritesNoExecEntry(t *testing.T) {
	k := newDrainKit(t)
	e := pending("c", "c-s", "r:1", 1_700_000_000_000)
	e.Kind, e.Role, e.TeamID = KindRefresh, team.SeatNone, ""
	id := mustInsert(t, k.st, e)
	if _, ok, err := k.st.FinishRefresh(id, RefreshDone{Status: "s", Todos: adds("新")}); err != nil || !ok {
		t.Fatal(err, ok)
	}
	if n := k.drain(); n != 1 {
		t.Fatalf("drained %d", n)
	}
	for _, x := range k.entries("solo:c") { // the todo it added is mirrored (see TestDrain_RefreshTodosAreMirrored), the line is not
		if x.Kind == journal.KindExec || x.Kind == journal.KindExecDone {
			t.Fatalf("an exec entry for a refresh: %+v", x)
		}
	}
}

// Rows that keep failing do not starve the rows behind them: with more failing rows than a batch, a healthy row after
// them is still written; and a row of a kind this binary does not know stays undone.
// Mutation gate: pass no skip list to ReadyOutbox -> the healthy row is never reached (red); mark an unknown kind done
// -> the row is gone (red).
func TestDrain_FailingRowsDoNotStarveOthersAndUnknownKindStays(t *testing.T) {
	k := newDrainKit(t)
	k.fp.err = errors.New("resolver down")
	for i := 0; i < drainBatch+3; i++ {
		id := k.turn("bad"+itoa(int64(i)), "t", team.SeatNone, TodoChanges{})
		k.okEntry(id, "x。", false)
	}
	k.drain() // every row fails and enters its delay
	k.fp.err = nil
	good := k.turn("good", "t", team.SeatNone, TodoChanges{})
	k.okEntry(good, "好。", false)
	if n := k.drain(); n != 1 {
		t.Fatalf("drained %d, want only the healthy row", n)
	}
	if _, ok := k.byID("solo:good", "turn:"+itoa(good)); !ok {
		t.Fatal("the healthy row behind the failing batch was not written")
	}
	if _, err := k.st.db.Exec(`INSERT INTO wb_journal_outbox (entry_id, unit_key, kind, payload, created_at) VALUES (?, 'solo:good', 'future', '{}', 1)`, good); err != nil {
		t.Fatal(err)
	}
	if _, err := k.st.db.Exec(`INSERT INTO wb_journal_outbox (entry_id, unit_key, kind, payload, created_at) VALUES (?, 'solo:good', 'journal', 'not json', 1)`, good); err != nil {
		t.Fatal(err)
	}
	k.drain()
	var bad int
	if err := k.st.db.QueryRow(`SELECT done_at FROM wb_journal_outbox WHERE payload = 'not json'`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("undecodable payload: done_at = %d err = %v, want undone", bad, err)
	}
	var done int
	if err := k.st.db.QueryRow(`SELECT done_at FROM wb_journal_outbox WHERE kind = 'future'`).Scan(&done); err != nil || done != 0 {
		t.Fatalf("unknown kind: done_at = %d err = %v, want undone", done, err)
	}
}

// A solo refresh that adds a todo mirrors it (task entry and tasks file) like a turn does, with no exec entry.
// Mutation gate: skip the todos of a refresh row -> no task entry (red).
func TestDrain_RefreshTodosAreMirrored(t *testing.T) {
	k := newDrainKit(t)
	e := pending("c", "c-s", "r:1", 1_700_000_000_000)
	e.Kind, e.Role, e.TeamID = KindRefresh, team.SeatNone, ""
	id := mustInsert(t, k.st, e)
	if _, ok, err := k.st.FinishRefresh(id, RefreshDone{Status: "s", Todos: adds("重整新增")}); err != nil || !ok {
		t.Fatal(err, ok)
	}
	k.drain()
	es := k.entries("solo:c")
	if len(es) != 1 || es[0].Kind != journal.KindTaskNew || es[0].Text != "#1 重整新增" {
		t.Fatalf("entries = %+v", es)
	}
	tasks, err := journal.ParseTasks(filepath.Join(k.fp.wt, ".purdex", "tasks", unitSlug("專案 A", "solo:c")+".md"))
	if err != nil || len(tasks) != 1 || tasks[0].Title != "重整新增" {
		t.Fatalf("tasks = %+v %v", tasks, err)
	}
}

// A replayed journal row does not undo what was done to its entries since: a confirm unpinned by hand and an item
// ticked stay so when the row is written again (it was not marked done).
// Mutation gate: write events with Upsert instead of putNew -> the confirm is pinned again (red).
func TestDrain_ReplayKeepsWhatHappenedToAnEvent(t *testing.T) {
	k := newDrainKit(t)
	a := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	k.okEntry(a, "。", false)
	conf := JournalOutcome{EntryID: a, PromptVer: 1, Result: JournalResult{Events: []JournalEvent{{Kind: journal.KindConfirm, Text: "請確認", Items: []string{"甲?"}}}}}
	if err := k.st.RecordJournalResult(conf); err != nil {
		t.Fatal(err)
	}
	k.drain()
	cid := "ev:" + itoa(a) + ":0"
	file := journal.MonthFile(k.dir("solo:c"), at(1_700_000_000_000))
	if _, err := journal.SetPinned(file, cid, false); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Tick(file, cid, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := k.st.db.Exec(`UPDATE wb_journal_outbox SET done_at = 0 WHERE kind = 'journal'`); err != nil { // the crash before MarkOutboxDone
		t.Fatal(err)
	}
	k.drain()
	if e, _ := k.byID("solo:c", cid); e.Pinned || !e.Items[0].Done {
		t.Fatalf("after the replay: %+v", e)
	}
}

// A replayed turn row keeps a pin the user set on its exec entry.
// Mutation gate: write turn entries with Upsert instead of putNew -> the pin is cleared (red).
func TestDrain_TurnReplayKeepsAPinOnItsExecEntry(t *testing.T) {
	k := newDrainKit(t)
	a := k.turn("c", "t1", team.SeatNone, TodoChanges{})
	k.okEntry(a, "做完了。", false)
	k.drain()
	file := journal.MonthFile(k.dir("solo:c"), at(1_700_000_000_000))
	if _, err := journal.SetPinned(file, "turn:"+itoa(a), true); err != nil {
		t.Fatal(err)
	}
	if _, err := k.st.db.Exec(`UPDATE wb_journal_outbox SET done_at = 0`); err != nil {
		t.Fatal(err)
	}
	k.drain()
	if e, _ := k.byID("solo:c", "turn:"+itoa(a)); !e.Pinned {
		t.Fatalf("after the replay: %+v", e)
	}
}
