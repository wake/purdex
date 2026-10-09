package workbook

import (
	"os"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "workbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func pending(conv, sid, turn string, at int64) Entry {
	return Entry{ConvKey: conv, HostID: "h1", Provider: "claude", SessionID: sid, TurnID: turn, TurnAt: at, TurnSeq: at,
		TeamID: "team-1", Role: "member", Ref: "_abc123", PromptVer: 1}
}

func mustInsert(t *testing.T, s *Store, e Entry) int64 {
	t.Helper()
	id, inserted, err := s.InsertPending(e)
	if err != nil || !inserted {
		t.Fatalf("insert %s/%s: id=%d inserted=%v err=%v", e.SessionID, e.TurnID, id, inserted, err)
	}
	return id
}

// The same turn twice (a retried hook) is one row. Mutation gate: drop UNIQUE(session_id, turn_id) → red.
func TestInsertPending_IdempotentOnSessionAndTurn(t *testing.T) {
	s := openTest(t)
	id1 := mustInsert(t, s, pending("c", "s1", "turn-a", 100))
	id2, inserted, err := s.InsertPending(pending("c", "s1", "turn-a", 200))
	if err != nil || inserted || id2 != id1 {
		t.Fatalf("again: id=%d (want %d) inserted=%v err=%v", id2, id1, inserted, err)
	}
	// another session with the same turn id is another row
	mustInsert(t, s, pending("c", "s2", "turn-a", 100))
	got, err := s.Conversation("c", 10, 0)
	if err != nil || len(got) != 2 {
		t.Fatalf("rows = %d err=%v", len(got), err)
	}
	if got[1].State != StatePending || got[1].TeamID != "team-1" || got[1].Role != "member" || got[1].TurnAt != 100 {
		t.Fatalf("row = %+v", got[1])
	}
}

func TestSetPushLine_KeepsPendingAndStampsReady(t *testing.T) {
	s := openTest(t)
	s.now = func() int64 { return 5000 }
	id := mustInsert(t, s, pending("c", "s1", "t1", 100))
	if ok, err := s.SetPushLine(id, "改 bug", "修好了登入"); err != nil || !ok {
		t.Fatalf("set: %v %v", ok, err)
	}
	e, _ := s.Entry(id)
	if e.State != StatePending || e.Thing != "改 bug" || e.Push != "修好了登入" || e.PushReadyAt != 5000 {
		t.Fatalf("entry = %+v", e)
	}
	// an empty push (validation dropped it) still stamps the line as final
	id2 := mustInsert(t, s, pending("c", "s1", "t2", 200))
	if ok, _ := s.SetPushLine(id2, "x", ""); !ok {
		t.Fatal("empty push refused")
	}
	if e2, _ := s.Entry(id2); e2.PushReadyAt != 5000 || e2.Push != "" {
		t.Fatalf("entry = %+v", e2)
	}
}

func TestFinish_Transitions(t *testing.T) {
	s := openTest(t)
	s.now = func() int64 { return 9000 }
	ok := mustInsert(t, s, pending("c", "s1", "t1", 100))
	if changed, err := s.Finish(ok, StateOK, "", Output{Thing: "t", Push: "p", Entry: "做了一件事。", ThingDone: true, LatencyMS: 1234}); err != nil || !changed {
		t.Fatalf("finish ok: %v %v", changed, err)
	}
	e, _ := s.Entry(ok)
	if e.State != StateOK || e.Entry != "做了一件事。" || !e.ThingDone || e.LatencyMS != 1234 || e.UpdatedAt != 9000 {
		t.Fatalf("entry = %+v", e)
	}
	// a final entry does not move again
	if changed, _ := s.Finish(ok, StateFailed, "exit", Output{}); changed {
		t.Fatal("an ok entry was finished twice")
	}
	if e, _ := s.Entry(ok); e.State != StateOK {
		t.Fatalf("state = %s", e.State)
	}
	failed := mustInsert(t, s, pending("c", "s1", "t2", 200))
	if changed, _ := s.Finish(failed, StateFailed, "timeout", Output{LatencyMS: 90000}); !changed {
		t.Fatal("failed not applied")
	}
	if e, _ := s.Entry(failed); e.State != StateFailed || e.Reason != "timeout" {
		t.Fatalf("entry = %+v", e)
	}
	skipped := mustInsert(t, s, pending("c", "s1", "t3", 300))
	if changed, _ := s.Finish(skipped, StateSkipped, "no_text", Output{}); !changed {
		t.Fatal("skipped not applied")
	}
	if _, err := s.Finish(skipped, "bogus", "", Output{}); err == nil {
		t.Fatal("an unknown state must be an error")
	}
}

func TestConversation_NewestFirstAndPagedByID(t *testing.T) {
	s := openTest(t)
	var ids []int64
	for i, turn := range []string{"a", "b", "c", "d", "e"} {
		ids = append(ids, mustInsert(t, s, pending("c", "s1", turn, int64(100+i))))
	}
	mustInsert(t, s, pending("other", "s9", "x", 1))
	page1, err := s.Conversation("c", 2, 0)
	if err != nil || len(page1) != 2 || page1[0].ID != ids[4] || page1[1].ID != ids[3] {
		t.Fatalf("page1 = %+v err=%v", page1, err)
	}
	page2, _ := s.Conversation("c", 2, page1[1].ID)
	if len(page2) != 2 || page2[0].ID != ids[2] || page2[1].ID != ids[1] {
		t.Fatalf("page2 = %+v", page2)
	}
	page3, _ := s.Conversation("c", 2, page2[1].ID)
	if len(page3) != 1 || page3[0].ID != ids[0] {
		t.Fatalf("page3 = %+v", page3)
	}
}

func TestEntries_FiltersOnTurnAtAndThingDone(t *testing.T) {
	s := openTest(t)
	a := mustInsert(t, s, pending("c1", "s1", "a", 100))
	b := mustInsert(t, s, pending("c2", "s2", "b", 200))
	c := mustInsert(t, s, pending("c1", "s1", "c", 300))
	s.Finish(a, StateOK, "", Output{Entry: "x", ThingDone: true})
	s.Finish(b, StateOK, "", Output{Entry: "y"})
	s.Finish(c, StateOK, "", Output{Entry: "z", ThingDone: true})
	all, _ := s.Entries(0, 0, false, 50)
	if len(all) != 3 || all[0].ID != c {
		t.Fatalf("all = %+v", all)
	}
	win, _ := s.Entries(150, 300, false, 50) // since inclusive, until exclusive
	if len(win) != 1 || win[0].ID != b {
		t.Fatalf("window = %+v", win)
	}
	done, _ := s.Entries(0, 0, true, 50)
	if len(done) != 2 || done[0].ID != c || done[1].ID != a {
		t.Fatalf("done = %+v", done)
	}
	if lim, _ := s.Entries(0, 0, false, 1); len(lim) != 1 {
		t.Fatalf("limit = %d", len(lim))
	}
}

func TestNewestTurn(t *testing.T) {
	s := openTest(t)
	if _, _, ok, err := s.NewestTurn("s1"); err != nil || ok {
		t.Fatalf("empty: ok=%v err=%v", ok, err)
	}
	mustInsert(t, s, pending("c", "s1", "a", 100))
	mustInsert(t, s, pending("c", "s1", "b", 200))
	mustInsert(t, s, pending("c", "s2", "z", 999))
	turn, at, ok, err := s.NewestTurn("s1")
	if err != nil || !ok || turn != "b" || at != 200 {
		t.Fatalf("newest = %q %d %v %v", turn, at, ok, err)
	}
}

// Only ok entries feed the prompt, the last n, oldest first.
func TestRecentForPrompt_OnlyOKOldestFirst(t *testing.T) {
	s := openTest(t)
	for i, turn := range []string{"a", "b", "c", "d"} {
		id := mustInsert(t, s, pending("c", "s1", turn, int64(100+i)))
		switch turn {
		case "c":
			s.Finish(id, StateFailed, "format", Output{})
		default:
			s.Finish(id, StateOK, "", Output{Entry: turn})
		}
	}
	mustInsert(t, s, pending("c", "s1", "e", 500)) // still pending
	got, err := s.RecentForPrompt("c", 2)
	if err != nil || len(got) != 2 || got[0].Entry != "b" || got[1].Entry != "d" {
		t.Fatalf("recent = %+v err=%v", got, err)
	}
}

func TestStatus_UpsertAndRead(t *testing.T) {
	s := openTest(t)
	s.now = func() int64 { return 777 }
	if _, ok, _ := s.Status("c"); ok {
		t.Fatal("status before any write")
	}
	if err := s.SetStatus("c", "修登入", 4, "s1"); err != nil {
		t.Fatal(err)
	}
	s.now = func() int64 { return 888 }
	if err := s.SetStatus("c", "改測試", 5, "s2"); err != nil {
		t.Fatal(err)
	}
	st, ok, err := s.Status("c")
	if err != nil || !ok || st.Status != "改測試" || st.EntryID != 5 || st.SessionID != "s2" || st.UpdatedAt != 888 {
		t.Fatalf("status = %+v ok=%v err=%v", st, ok, err)
	}
}

// A crash leaves rows pending; the next start turns them into failed:stopped (plan D9).
func TestFailPending_ByRestart(t *testing.T) {
	s := openTest(t)
	a := mustInsert(t, s, pending("c", "s1", "a", 100))
	b := mustInsert(t, s, pending("c", "s1", "b", 200))
	s.Finish(b, StateOK, "", Output{Entry: "x"})
	n, err := s.FailPending()
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if e, _ := s.Entry(a); e.State != StateFailed || e.Reason != ReasonStopped {
		t.Fatalf("a = %+v", e)
	}
	if e, _ := s.Entry(b); e.State != StateOK {
		t.Fatalf("b = %+v", e)
	}
}

func TestOpenStore_FileModesAndSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workbook.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	mustInsert(t, s, pending("c", "s1", "a", 1)) // makes the WAL sidecars appear
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(p)
		if err != nil {
			continue // a sidecar may be gone after a checkpoint
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %o, must be owner-only", filepath.Base(p), fi.Mode().Perm())
		}
	}
	var v int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("schema_version = %d err=%v", v, err)
	}
	s.Close()
	again, err := OpenStore(path) // reopening keeps the data and the version
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if rows, _ := again.Conversation("c", 10, 0); len(rows) != 1 {
		t.Fatalf("rows after reopen = %d", len(rows))
	}
}

func TestOpenStore_BrokenPathFails(t *testing.T) {
	if _, err := OpenStore(filepath.Join(t.TempDir(), "no", "such", "dir", "workbook.db")); err == nil {
		t.Fatal("a path that cannot be created must fail")
	}
}
