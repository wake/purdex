package workbook

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/team"
)

// seated is a pending turn entry of conversation conv in the given seat.
func seated(t *testing.T, s *Store, conv, turn, role, teamID string) int64 {
	t.Helper()
	e := pending(conv, conv+"-s", turn, 100)
	e.Role, e.TeamID = role, teamID
	return mustInsert(t, s, e)
}

type outboxRow struct {
	ID, EntryID int64
	Unit, Kind  string
	Payload     string
	Done        int64
}

func outboxRows(t *testing.T, s *Store) []outboxRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT id, entry_id, unit_key, kind, payload, done_at FROM wb_journal_outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.ID, &r.EntryID, &r.Unit, &r.Kind, &r.Payload, &r.Done); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func adds(titles ...string) TodoChanges {
	var c TodoChanges
	for _, t := range titles {
		c.Adds = append(c.Adds, TodoAdd{Title: t})
	}
	return c
}

// A lead's and a solo turn leave one `turn` row, in the transaction of SetPushLineV2 / FinishSkippedV2, carrying that
// transaction's todo changes; a member's turn leaves none; with the outbox off nothing is written at all.
// Mutation gate: no seat check -> the member case gets a row (red); drop the call in either transaction -> red; ignore
// journalOn -> the "off" case gets rows (red).
func TestTurnOutbox_LeadAndSoloGetARowMemberDoesNot(t *testing.T) {
	s := openTest(t)
	off := seated(t, s, "off", "t", team.SeatNone, "")
	if _, ok, err := s.SetPushLineV2(off, PushLineV2{Thing: "x", Status: "s"}); err != nil || !ok {
		t.Fatal(err, ok)
	}
	if n := len(outboxRows(t, s)); n != 0 {
		t.Fatalf("outbox off: %d rows", n)
	}
	s.EnableJournalOutbox()
	lead := seated(t, s, "c1", "t", team.SeatLead, "team-7")
	solo := seated(t, s, "c2", "t", team.SeatNone, "")
	mem := seated(t, s, "c3", "t", team.SeatMember, "team-7")
	if _, ok, err := s.SetPushLineV2(lead, PushLineV2{Thing: "x", Status: "s", Todos: adds("寫測試"), By: ClosedByModel}); err != nil || !ok {
		t.Fatal(err, ok)
	}
	if _, ok, err := s.FinishSkippedV2(solo, ReasonModel, Usage{}, 0, adds("甲", "乙"), ClosedByModel); err != nil || !ok {
		t.Fatal(err, ok)
	}
	if _, ok, err := s.SetPushLineV2(mem, PushLineV2{Thing: "x", Status: "s", Todos: adds("不該有"), By: ClosedByModel}); err != nil || !ok {
		t.Fatal(err, ok)
	}
	rows := outboxRows(t, s)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want the lead's and the solo's", rows)
	}
	if rows[0].EntryID != lead || rows[0].Unit != "team:team-7" || rows[0].Kind != OutboxTurn || rows[0].Done != 0 {
		t.Errorf("lead row = %+v", rows[0])
	}
	if rows[1].EntryID != solo || rows[1].Unit != "solo:c2" {
		t.Errorf("solo row = %+v", rows[1])
	}
	var p turnPayload
	if err := json.Unmarshal([]byte(rows[1].Payload), &p); err != nil || len(p.Todos) != 2 || p.Todos[0].Title != "甲" || p.Todos[0].Seq != 1 || p.Todos[1].Seq != 2 || p.Todos[1].State != TodoOpen {
		t.Errorf("solo payload = %s (%v)", rows[1].Payload, err)
	}
}

// The outbox insert is part of the result transaction: when it fails, the push line, the status and the todos are all
// rolled back, and a skipped entry stays pending.
// Mutation gate: insert after the commit (outside inTx) -> the entry is written although the insert failed (red).
func TestTurnOutbox_IsInTheResultTransaction(t *testing.T) {
	s := openTest(t)
	s.EnableJournalOutbox()
	a := seated(t, s, "c", "a", team.SeatNone, "")
	b := seated(t, s, "c", "b", team.SeatNone, "")
	if _, err := s.db.Exec(`DROP TABLE wb_journal_outbox`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.SetPushLineV2(a, PushLineV2{Thing: "x", Push: "p", Status: "s", Todos: adds("甲"), By: ClosedByModel}); err == nil || ok {
		t.Fatalf("push line: ok=%v err=%v, want a failure", ok, err)
	}
	if _, ok, err := s.FinishSkippedV2(b, ReasonModel, Usage{}, 0, adds("乙"), ClosedByModel); err == nil || ok {
		t.Fatalf("skipped: ok=%v err=%v, want a failure", ok, err)
	}
	for _, id := range []int64{a, b} {
		if e := mustEntry(t, s, id); e.State != StatePending || e.Push != "" || e.PushReadyAt != 0 {
			t.Errorf("entry %d after a failed transaction: %+v", id, e)
		}
	}
	if _, ok, _ := s.Status("c"); ok {
		t.Error("a status was kept")
	}
	if ts, _ := s.OpenTodos("c", 10); len(ts) != 0 {
		t.Errorf("todos kept: %v", ts)
	}
}

func mustEntry(t *testing.T, s *Store, id int64) Entry {
	t.Helper()
	e, err := s.Entry(id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// An entry that is not pending any more writes nothing, outbox row included.
// Mutation gate: insert the row before the pending check -> a row for a finished entry (red).
func TestTurnOutbox_NotPendingWritesNothing(t *testing.T) {
	s := openTest(t)
	s.EnableJournalOutbox()
	a := seated(t, s, "c", "a", team.SeatNone, "")
	if _, err := s.Finish(a, StateFailed, "api", Output{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.SetPushLineV2(a, PushLineV2{Thing: "x", Todos: adds("甲"), By: ClosedByModel}); err != nil || ok {
		t.Fatal(ok, err)
	}
	if _, ok, err := s.FinishSkippedV2(a, ReasonModel, Usage{}, 0, adds("乙"), ClosedByModel); err != nil || ok {
		t.Fatal(ok, err)
	}
	if n := len(outboxRows(t, s)); n != 0 {
		t.Fatalf("%d rows for a finished entry", n)
	}
}

func journalOutcome(entry int64, evs ...journal.Kind) JournalOutcome {
	r := JournalResult{}
	for _, k := range evs {
		r.Events = append(r.Events, JournalEvent{Kind: k, Text: "事件", Actor: "1f → 88"})
	}
	r.Ticks = []ConfirmTick{{EntryID: "ev:1:0", ItemIndex: 1}}
	r.Roadmap = []RoadmapChange{{Feature: "F", Stage: "S", Status: "完成"}}
	return JournalOutcome{EntryID: entry, PromptVer: 1, Result: r}
}

// A journal result becomes one `journal` row for its entry's unit; a second record of the same entry, an empty result,
// a member's entry and an unknown entry store nothing more.
// Mutation gate: no unique/ON CONFLICT -> a second row or an error (red); drop the empty check -> a row for nothing
// (red); drop the seat check -> a member gets a row (red).
func TestRecordJournalResult(t *testing.T) {
	s := openTest(t)
	s.EnableJournalOutbox()
	lead := seated(t, s, "c1", "t", team.SeatLead, "team-7")
	mem := seated(t, s, "c2", "t", team.SeatMember, "team-7")
	for i := 0; i < 2; i++ {
		if err := s.RecordJournalResult(journalOutcome(lead, journal.KindComm, journal.KindRequest)); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []JournalOutcome{journalOutcome(mem, journal.KindComm), {EntryID: lead + 100, Result: journalOutcome(1, journal.KindComm).Result}} {
		if err := s.RecordJournalResult(o); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordJournalResult(JournalOutcome{EntryID: seated(t, s, "c4", "t", team.SeatNone, "")}); err != nil { // a result with nothing in it
		t.Fatal(err)
	}
	rows := outboxRows(t, s)
	if len(rows) != 1 || rows[0].Kind != OutboxJournal || rows[0].Unit != "team:team-7" || rows[0].EntryID != lead {
		t.Fatalf("rows = %+v", rows)
	}
	var p journalPayload
	if err := json.Unmarshal([]byte(rows[0].Payload), &p); err != nil || p.Ver != 1 || len(p.Events) != 2 || p.Events[1].Kind != "request" ||
		len(p.Ticks) != 1 || p.Ticks[0].Entry != "ev:1:0" || p.Ticks[0].Idx != 1 || len(p.Roadmap) != 1 || p.Roadmap[0].Status != "完成" {
		t.Fatalf("payload = %s (%v)", rows[0].Payload, err)
	}
}

// With the outbox off, a journal result is not stored.
func TestRecordJournalResult_OffStoresNothing(t *testing.T) {
	s := openTest(t)
	lead := seated(t, s, "c1", "t", team.SeatLead, "team-7")
	if err := s.RecordJournalResult(journalOutcome(lead, journal.KindComm)); err != nil {
		t.Fatal(err)
	}
	if n := len(outboxRows(t, s)); n != 0 {
		t.Fatalf("%d rows with the outbox off", n)
	}
}

// NewEngine turns the outbox on exactly when a journal source is wired, and the engine's journal result lands in it.
// Mutation gate: drop the Enable call in NewEngine -> no row (red); enable it without a source -> the "no source" case
// gets rows (red).
func TestEngine_WiresTheOutboxOnlyWithAJournalSource(t *testing.T) {
	k := journalKit(t, team.SeatLead)
	if k.st.journalOn.Load() {
		t.Fatal("outbox on without a journal source")
	}
	st := openTest(t)
	NewEngine(Deps{Store: st, Logf: func(string, ...any) {}})
	if st.journalOn.Load() {
		t.Fatal("outbox on for an engine without a source")
	}
	NewEngine(Deps{Store: st, Journal: fakeJournal{}, Logf: func(string, ...any) {}})
	if !st.journalOn.Load() {
		t.Fatal("outbox off for an engine with a source")
	}
	k.st.EnableJournalOutbox()
	k.turns.set("s1", endedTurn("a", 100, "做了 a"))
	k.event("s1", 1000)
	k.finish("m", mustNext(t, k, "m", "s1"), answerJSON("事", "推", "紀錄", "狀態", ""))
	k.finish("m", mustNext(t, k, "m", "s1"), answer(journalAnswer))
	rows := outboxRows(t, k.st)
	if len(rows) != 2 || rows[0].Kind != OutboxTurn || rows[1].Kind != OutboxJournal || rows[0].Unit != "team:team-9" {
		t.Fatalf("rows = %+v", rows)
	}
}

// Todo numbers: 1, 2, 3 per conversation, never reused after a close, independent between conversations; and under
// concurrent writers (distinct entries of one conversation) every number is distinct and none is skipped.
// Mutation gate: seq = count of open todos + 1 -> a reused number after a close (a unique violation, red); seq from
// the global todo count -> the second conversation does not start at 1 (red).
func TestTodoSeq_PerConversationNeverReusedAndConcurrentSafe(t *testing.T) {
	s := openTest(t)
	a := seated(t, s, "c", "a", team.SeatNone, "")
	res, _, err := s.SetPushLineV2(a, PushLineV2{Thing: "x", Todos: adds("一", "二"), By: ClosedByModel})
	if err != nil || len(res.Changed) != 2 || res.Changed[0].Seq != 1 || res.Changed[1].Seq != 2 {
		t.Fatalf("first: %+v %v", res, err)
	}
	b := seated(t, s, "c", "b", team.SeatNone, "")
	res, _, err = s.SetPushLineV2(b, PushLineV2{Thing: "x", Todos: TodoChanges{Done: []int64{res.Changed[0].ID}, Adds: []TodoAdd{{Title: "三"}}}, By: ClosedByModel})
	if err != nil || len(res.Changed) != 2 || res.Changed[1].Seq != 3 {
		t.Fatalf("after a close: %+v %v", res, err)
	}
	other := seated(t, s, "d", "a", team.SeatNone, "")
	res, _, err = s.SetPushLineV2(other, PushLineV2{Thing: "x", Todos: adds("甲"), By: ClosedByModel})
	if err != nil || res.Changed[0].Seq != 1 {
		t.Fatalf("other conversation: %+v %v", res, err)
	}
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		id := seated(t, s, "e", fmt.Sprintf("t%d", i), team.SeatNone, "")
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := s.SetPushLineV2(id, PushLineV2{Thing: "x", Todos: adds(fmt.Sprintf("任務%d", id)), By: ClosedByModel}); err != nil || !ok {
				t.Errorf("entry %d: ok=%v err=%v", id, ok, err)
			}
		}()
	}
	wg.Wait()
	seen := map[int64]bool{}
	rows, err := s.db.Query(`SELECT seq FROM wb_todos WHERE conv_key = 'e'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var q int64
		rows.Scan(&q)
		seen[q] = true
	}
	for q := int64(1); q <= n; q++ {
		if !seen[q] {
			t.Errorf("number %d missing from %v", q, seen)
		}
	}
}
