package dialogue

import (
	"errors"
	"testing"
)

func pendingRow(sid, turnID string, no int, at int64) SummaryRow {
	return SummaryRow{SessionID: sid, ConvKey: "root-" + sid, TurnID: turnID, TurnNo: no, TurnAt: at, TurnSeq: 7}
}

// storeWithSession: a store whose session s1 exists (a Commit made it), so since_recap has a row to count on.
func storeWithSession(t *testing.T) *Store {
	t.Helper()
	s, _ := openTestStore(t)
	if err := s.Commit("s1", SessionState{Segment: 1, NextTurnNo: 1}, nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func mustInsertSummary(t *testing.T, s *Store, r SummaryRow) int64 {
	t.Helper()
	id, inserted, err := s.InsertPendingSummary(r)
	if err != nil || !inserted {
		t.Fatalf("InsertPendingSummary: id=%d inserted=%v err=%v", id, inserted, err)
	}
	return id
}

func TestMigration2_SummariesAndSinceRecap(t *testing.T) {
	s, _ := openTestStore(t)
	if v := userVersion(t, s.db); v != len(migrations) || v < 2 {
		t.Fatalf("user_version = %d, want %d (at least 2)", v, len(migrations))
	}
	if !tableExists(t, s.db, "dlg_summaries") {
		t.Fatal("dlg_summaries is missing")
	}
	for _, idx := range []string{"dlg_summaries_session_at", "dlg_summaries_session_no"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index %s: n=%d err=%v", idx, n, err)
		}
	}
	if err := s.Commit("s1", SessionState{Segment: 1, NextTurnNo: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SinceRecap("s1"); err != nil || n != 0 {
		t.Fatalf("since_recap = %d err=%v", n, err)
	}
	if n, err := s.SinceRecap("nobody"); err != nil || n != 0 {
		t.Fatalf("since_recap of an unknown session = %d err=%v", n, err)
	}
}

func TestInsertPendingSummary_OncePerTurn(t *testing.T) {
	s := storeWithSession(t)
	id := mustInsertSummary(t, s, pendingRow("s1", "t1", 1, 5000))
	id2, inserted, err := s.InsertPendingSummary(pendingRow("s1", "t1", 1, 9999))
	if err != nil || inserted || id2 != id {
		t.Fatalf("second insert: id=%d inserted=%v err=%v (first id %d)", id2, inserted, err, id)
	}
	row, ok, err := s.SummaryByID(id)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if row.State != statePending || row.TurnAt != 5000 || row.TurnSeq != 7 || row.ConvKey != "root-s1" || row.TurnNo != 1 || row.CreatedAt != 1000 {
		t.Fatalf("row = %+v", row)
	}
	if _, ok, _ := s.SummaryByID(999); ok {
		t.Fatal("a row that does not exist")
	}
	// the same turn id in another session is another row
	if _, inserted, _ := s.InsertPendingSummary(pendingRow("s2", "t1", 1, 5000)); !inserted {
		t.Fatal("another session's turn was refused")
	}
}

func TestSetSummaryDone_WritesEverythingAndCountsOnce(t *testing.T) {
	s := storeWithSession(t)
	s.now = func() int64 { return 4242 }
	id := mustInsertSummary(t, s, pendingRow("s1", "t1", 1, 5000))
	applied, err := s.SetSummaryDone(id, "標題", "推播", "內文。", false, usage{In: 10, Out: 20, CacheRead: 30, LatencyMS: 40})
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	row, _, _ := s.SummaryByID(id)
	if row.State != stateDone || row.Title != "標題" || row.Push != "推播" || row.Summary != "內文。" || row.PushReadyAt != 4242 ||
		row.Usage != (usage{In: 10, Out: 20, CacheRead: 30, LatencyMS: 40}) || row.UpdatedAt != 4242 {
		t.Fatalf("row = %+v", row)
	}
	if n, _ := s.SinceRecap("s1"); n != 1 {
		t.Fatalf("since_recap = %d", n)
	}
	// the gate: a second answer for the same row writes nothing, and does not count again
	applied, err = s.SetSummaryDone(id, "別的", "別的", "別的", false, usage{In: 1})
	if err != nil || applied {
		t.Fatalf("second apply: applied=%v err=%v", applied, err)
	}
	row, _, _ = s.SummaryByID(id)
	if row.Title != "標題" || row.Usage.In != 10 {
		t.Fatalf("the gate let a second write through: %+v", row)
	}
	if n, _ := s.SinceRecap("s1"); n != 1 {
		t.Fatalf("since_recap counted twice: %d", n)
	}
}

func TestSetSummaryDone_SkipHasNoPushAndNoCount(t *testing.T) {
	s := storeWithSession(t)
	id := mustInsertSummary(t, s, pendingRow("s1", "t1", 1, 5000))
	applied, err := s.SetSummaryDone(id, "", "", "", true, usage{In: 5})
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	row, _, _ := s.SummaryByID(id)
	if row.State != stateSkipped || row.Reason != "model" || row.PushReadyAt != 0 || row.Usage.In != 5 {
		t.Fatalf("row = %+v", row)
	}
	if n, _ := s.SinceRecap("s1"); n != 0 {
		t.Fatalf("a skip counted toward the recap: %d", n)
	}
}

func TestSetSummaryDone_NotPendingWritesNothing(t *testing.T) {
	s := storeWithSession(t)
	id := mustInsertSummary(t, s, pendingRow("s1", "t1", 1, 5000))
	if ok, err := s.FinishSummary(id, stateFailed, reasonFormat); err != nil || !ok {
		t.Fatal(err)
	}
	applied, err := s.SetSummaryDone(id, "t", "p", "s", false, usage{})
	if err != nil || applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	row, _, _ := s.SummaryByID(id)
	if row.State != stateFailed || row.Reason != reasonFormat || row.Title != "" {
		t.Fatalf("row = %+v", row)
	}
	if n, _ := s.SinceRecap("s1"); n != 0 {
		t.Fatalf("since_recap = %d", n)
	}
}

// The row change and the since_recap count are one transaction: a failure between them leaves the row pending.
func TestSetSummaryDone_SameTransaction(t *testing.T) {
	s := storeWithSession(t)
	id := mustInsertSummary(t, s, pendingRow("s1", "t1", 1, 5000))
	boom := errors.New("boom")
	s.failInSummaryDone = func() error { return boom }
	if applied, err := s.SetSummaryDone(id, "t", "p", "s", false, usage{}); !errors.Is(err, boom) || applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	s.failInSummaryDone = nil
	row, _, _ := s.SummaryByID(id)
	if row.State != statePending || row.Title != "" {
		t.Fatalf("the row changed although the transaction failed: %+v", row)
	}
	if n, _ := s.SinceRecap("s1"); n != 0 {
		t.Fatalf("since_recap = %d", n)
	}
	if applied, err := s.SetSummaryDone(id, "t", "p", "s", false, usage{}); err != nil || !applied {
		t.Fatalf("retry: applied=%v err=%v", applied, err)
	}
}

func TestCommitDoesNotResetSinceRecap(t *testing.T) {
	s := storeWithSession(t)
	id := mustInsertSummary(t, s, pendingRow("s1", "t1", 1, 5000))
	if _, err := s.SetSummaryDone(id, "t", "p", "s", false, usage{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit("s1", SessionState{Segment: 2, NextTurnNo: 5}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetForRebuild("s1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.SinceRecap("s1"); n != 1 {
		t.Fatalf("since_recap = %d after Commit/ResetForRebuild", n)
	}
}

func TestFinishSummary_OnlyPending(t *testing.T) {
	s := storeWithSession(t)
	a := mustInsertSummary(t, s, pendingRow("s1", "a", 1, 1))
	b := mustInsertSummary(t, s, pendingRow("s1", "b", 2, 2))
	if _, err := s.SetSummaryDone(b, "t", "p", "s", false, usage{}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.FinishSummary(a, stateSkipped, reasonNoMod); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ok, err := s.FinishSummary(a, stateFailed, reasonAPI); err != nil || ok {
		t.Fatalf("an ended row was moved again: ok=%v err=%v", ok, err)
	}
	if ok, err := s.FinishSummary(b, stateFailed, reasonAPI); err != nil || ok {
		t.Fatalf("a done row was moved: ok=%v err=%v", ok, err)
	}
	ra, _, _ := s.SummaryByID(a)
	rb, _, _ := s.SummaryByID(b)
	if ra.State != stateSkipped || ra.Reason != reasonNoMod || rb.State != stateDone || rb.Title != "t" {
		t.Fatalf("a=%+v b=%+v", ra, rb)
	}
}

func TestFailPendingSummaries(t *testing.T) {
	s := storeWithSession(t)
	a := mustInsertSummary(t, s, pendingRow("s1", "a", 1, 1))
	b := mustInsertSummary(t, s, pendingRow("s1", "b", 2, 2))
	c := mustInsertSummary(t, s, pendingRow("s1", "c", 3, 3))
	if _, err := s.SetSummaryDone(c, "t", "p", "s", false, usage{}); err != nil {
		t.Fatal(err)
	}
	n, err := s.FailPendingSummaries()
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, id := range []int64{a, b} {
		r, _, _ := s.SummaryByID(id)
		if r.State != stateFailed || r.Reason != reasonStopped {
			t.Fatalf("row = %+v", r)
		}
	}
	if r, _, _ := s.SummaryByID(c); r.State != stateDone {
		t.Fatalf("a done row was touched: %+v", r)
	}
	if n, _ := s.FailPendingSummaries(); n != 0 {
		t.Fatalf("second run n=%d", n)
	}
}

func TestClosestSummary(t *testing.T) {
	s := storeWithSession(t)
	mustInsertSummary(t, s, pendingRow("s1", "a", 1, 10_000))
	second := mustInsertSummary(t, s, pendingRow("s1", "b", 2, 10_300))
	mustInsertSummary(t, s, pendingRow("s2", "x", 1, 10_100))
	cases := []struct {
		name   string
		at     int64
		wantID int64 // 0 = none
		wantNo int
	}{
		{"exact first", 10_000, 0, 1},
		{"nearest is the second", 10_250, second, 2},
		{"2000 before is inside", 12_300, 0, 2}, // 10_300 is exactly at - before; 10_000 is outside
		{"just outside the window before", 12_301, 0, 0},
		{"500 after is inside", 9_800, 0, 1},
		{"outside the window after", 9_499, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row, ok, err := s.ClosestSummary("s1", c.at, 2000, 500)
			if err != nil {
				t.Fatal(err)
			}
			if c.wantNo == 0 {
				if ok {
					t.Fatalf("found %+v", row)
				}
				return
			}
			if !ok || row.TurnNo != c.wantNo || (c.wantID != 0 && row.ID != c.wantID) || row.SessionID != "s1" {
				t.Fatalf("row=%+v ok=%v", row, ok)
			}
		})
	}
}

func TestRecentTitles_DoneWithATitleOldestFirst(t *testing.T) {
	s := storeWithSession(t)
	ids := map[string]int64{}
	for i, n := range []string{"a", "b", "c", "d", "e", "f"} {
		ids[n] = mustInsertSummary(t, s, pendingRow("s1", n, i+1, int64(i+1)))
	}
	done := func(n, title string, skip bool) {
		if _, err := s.SetSummaryDone(ids[n], title, "p", "s", skip, usage{}); err != nil {
			t.Fatal(err)
		}
	}
	done("a", "甲", false)
	done("b", "乙", false)
	done("c", "", true) // skipped: not a title
	done("d", "丁", false)
	// e stays pending, f failed
	if _, err := s.FinishSummary(ids["f"], stateFailed, reasonAPI); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecentTitles("s1", 2)
	if err != nil || len(got) != 2 || got[0] != "乙" || got[1] != "丁" {
		t.Fatalf("titles = %v err=%v", got, err)
	}
	got, _ = s.RecentTitles("s1", 10)
	if len(got) != 3 || got[0] != "甲" {
		t.Fatalf("titles = %v", got)
	}
	if got, err := s.RecentTitles("s2", 3); err != nil || len(got) != 0 {
		t.Fatalf("other session: %v err=%v", got, err)
	}
}

func TestSummariesByTurnIDAndRange(t *testing.T) {
	s := storeWithSession(t)
	for i, n := range []string{"a", "b", "c", "d"} {
		id := mustInsertSummary(t, s, pendingRow("s1", n, i+1, int64(i+1)))
		if n == "c" {
			continue // pending
		}
		if _, err := s.SetSummaryDone(id, "標題"+n, "p", "內文"+n, false, usage{}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.SummariesByTurnID("s1", "b")
	if err != nil || len(rows) != 1 || rows[0].Title != "標題b" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if rows, _ := s.SummariesByTurnID("s1", "c"); len(rows) != 0 {
		t.Fatalf("a pending row was returned: %+v", rows)
	}
	if rows, _ := s.SummariesByTurnID("s1", "zzz"); len(rows) != 0 {
		t.Fatal("an unknown turn")
	}
	rng, err := s.SummariesRange("s1", 1, 4, 0)
	if err != nil || len(rng) != 3 || rng[0].TurnID != "a" || rng[2].TurnID != "d" {
		t.Fatalf("range = %+v err=%v", rng, err)
	}
	rng, _ = s.SummariesRange("s1", 2, 3, 0)
	if len(rng) != 1 || rng[0].TurnID != "b" {
		t.Fatalf("range 2..3 = %+v", rng)
	}
	// a limit keeps the newest rows of the range, still oldest first
	rng, _ = s.SummariesRange("s1", 1, 4, 2)
	if len(rng) != 2 || rng[0].TurnID != "b" || rng[1].TurnID != "d" {
		t.Fatalf("limited range = %+v", rng)
	}
}
