package teammod

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Compile-time: the store is the JournalFeed the module publishes.
var _ team.JournalFeed = (*Store)(nil)

// allTaskChanges pages TasksChangedAfter by limit from the zero cursor to the end, the way the journal's ingest does.
func allTaskChanges(t *testing.T, f team.JournalFeed, limit int) []team.TaskChange {
	t.Helper()
	var all []team.TaskChange
	c := team.TaskCursor{}
	for i := 0; i < 100; i++ {
		page, err := f.TasksChangedAfter(c, limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			return all
		}
		if len(page) > limit {
			t.Fatalf("page of %d rows, limit %d", len(page), limit)
		}
		all = append(all, page...)
		last := page[len(page)-1]
		c = team.TaskCursor{UpdatedAt: last.UpdatedAt, TeamID: last.TeamID, Seq: last.Seq}
	}
	t.Fatal("paging did not end")
	return nil
}

func journalTaskIDs(cs []team.TaskChange) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.TeamID[:8]+"/"+string(rune('0'+c.Seq)))
	}
	return out
}

// Tasks with one updated_at are paged by (team_id, seq) with no row lost and none repeated, across a page boundary that
// falls inside the tie. Mutation: order and cursor by updated_at only (drop team_id, seq) → a row is skipped or repeated.
func TestJournalFeed_TasksTieOnUpdatedAtPageWithoutLossOrRepeat(t *testing.T) {
	s := openTestStore(t)
	ma := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mb := seedTaskTeam(t, s, tTeamB, "lead-b", "op-b", "sess-b")
	for i := 0; i < 3; i++ {
		mustCreateTask(t, s, newTask(tTeamA, ma, "a task", 100))
	}
	for i := 0; i < 2; i++ {
		mustCreateTask(t, s, newTask(tTeamB, mb, "b task", 100))
	}
	want := []string{"3f2a9c01/1", "3f2a9c01/2", "3f2a9c01/3", "3f2a9c02/1", "3f2a9c02/2"}
	for _, limit := range []int{1, 2, 3, 10} {
		if got := journalTaskIDs(allTaskChanges(t, s, limit)); !reflect.DeepEqual(got, want) {
			t.Errorf("limit %d: tasks = %v, want %v", limit, got, want)
		}
	}
}

// The order is (updated_at, team_id, seq): a later update sorts after an earlier one whatever its team and seq.
func TestJournalFeed_TasksAreOrderedByUpdatedAtFirst(t *testing.T) {
	s := openTestStore(t)
	ma := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mb := seedTaskTeam(t, s, tTeamB, "lead-b", "op-b", "sess-b")
	mustCreateTask(t, s, newTask(tTeamB, mb, "b first", 100))
	mustCreateTask(t, s, newTask(tTeamA, ma, "a later", 200))
	got := journalTaskIDs(allTaskChanges(t, s, 10))
	if want := []string{"3f2a9c02/1", "3f2a9c01/1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tasks = %v, want %v", got, want)
	}
}

// A status change moves the task past a cursor that had already read it, and nothing else does.
// Mutation: leave updated_at out of the cursor comparison → the changed task is not seen again.
func TestJournalFeed_AStatusChangeMovesTheTaskPastTheCursor(t *testing.T) {
	s := openTestStore(t)
	ma := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mustCreateTask(t, s, newTask(tTeamA, ma, "one", 100))
	mustCreateTask(t, s, newTask(tTeamA, ma, "two", 101))
	all := allTaskChanges(t, s, 10)
	last := all[len(all)-1]
	cur := team.TaskCursor{UpdatedAt: last.UpdatedAt, TeamID: last.TeamID, Seq: last.Seq}
	if page, err := s.TasksChangedAfter(cur, 10); err != nil || len(page) != 0 {
		t.Fatalf("nothing changed yet: %+v err=%v", page, err)
	}
	if _, err := s.SetTaskStatus(tTeamA, 1, team.TaskInProgress, team.TaskByLead, 500); err != nil {
		t.Fatal(err)
	}
	page, err := s.TasksChangedAfter(cur, 10)
	if err != nil || len(page) != 1 || page[0].Seq != 1 || page[0].Status != "in_progress" || page[0].UpdatedAt != 500 {
		t.Fatalf("after the change = %+v err=%v, want task 1 in_progress at 500", page, err)
	}
}

// Every field of a task reaches the feed, including the PRs its reports recorded.
func TestJournalFeed_ATaskCarriesItsFields(t *testing.T) {
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	in := newTask(tTeamA, m, "the subject", 100)
	in.Description = "line one\nline two"
	mustCreateTask(t, s, in)
	mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportReady, 1, 150)) // ready carries PR 12
	got := allTaskChanges(t, s, 10)
	if len(got) != 1 {
		t.Fatalf("tasks = %+v", got)
	}
	want := team.TaskChange{TeamID: tTeamA, Seq: 1, Subject: "the subject", Description: "line one\nline two", Status: "pending",
		OwnerKey: "op-a", OwnerSessionID: "sess-a", OwnerRef: "_memop-a", OwnerTitle: "worker", CreatedByRef: "_lead01",
		MetadataPRs: []int{12}, CreatedAt: 100, UpdatedAt: 150}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("task = %+v\nwant   %+v", got[0], want)
	}
}

// An unowned task, and one whose owner has no team_members row, answer empty owner fields; the key stays what the task says.
func TestJournalFeed_AnUnownedOrUnknownOwnerHasEmptyOwnerFields(t *testing.T) {
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mustCreateTask(t, s, newTask(tTeamA, m, "unowned", 100))
	mustCreateTask(t, s, newTask(tTeamA, m, "orphan", 101))
	if _, err := s.db.Exec(`UPDATE tasks SET owner_key = '' WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tasks SET owner_key = 'op-gone' WHERE seq = 2`); err != nil {
		t.Fatal(err)
	}
	got := allTaskChanges(t, s, 10)
	if len(got) != 2 {
		t.Fatalf("tasks = %+v", got)
	}
	if o := got[0]; o.OwnerKey != "" || o.OwnerSessionID != "" || o.OwnerRef != "" || o.OwnerTitle != "" {
		t.Errorf("unowned task owner fields = %+v, want all empty", o)
	}
	if o := got[1]; o.OwnerKey != "op-gone" || o.OwnerSessionID != "" || o.OwnerRef != "" || o.OwnerTitle != "" {
		t.Errorf("unknown-owner task = %+v, want key op-gone and nothing else", o)
	}
}

// A member that relayed answers its new session id and ref with the same OwnerKey / MemberKey: the key, not the
// session, is the stable identity. Mutation: join team_members on session_id instead of spawn_op → the fields go empty.
func TestJournalFeed_ARelayedMemberKeepsItsKeyAndShowsItsNewSession(t *testing.T) {
	s := openTestStore(t)
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mustCreateTask(t, s, newTask(tTeamA, m, "work", 100))
	mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportProgress, 1, 110))
	if _, err := s.db.Exec(`UPDATE team_members SET session_id = 'sess-new', ref = '_new', title = 'renamed' WHERE spawn_op = 'op-a'`); err != nil {
		t.Fatal(err)
	}
	tasks := allTaskChanges(t, s, 10)
	if len(tasks) != 1 || tasks[0].OwnerKey != "op-a" || tasks[0].OwnerSessionID != "sess-new" || tasks[0].OwnerRef != "_new" || tasks[0].OwnerTitle != "renamed" {
		t.Fatalf("task after the relay = %+v", tasks)
	}
	reports, err := s.ReportsAfter(0, 0, 10)
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports = %+v err=%v", reports, err)
	}
	if r := reports[0]; r.MemberKey != "op-a" || r.MemberSessionID != "sess-new" || r.MemberRef != "_new" || r.MemberTitle != "renamed" {
		t.Fatalf("report after the relay = %+v", r)
	}
}

func allReports(t *testing.T, f team.JournalFeed, limit int) []team.ReportRow {
	t.Helper()
	var all []team.ReportRow
	var after int64
	for i := 0; i < 100; i++ {
		page, err := f.ReportsAfter(after, 0, limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			return all
		}
		all = append(all, page...)
		after = page[len(page)-1].RowID
	}
	t.Fatal("paging did not end")
	return nil
}

// A backfill larger than the limit is paged to the end, each report once, in rowid order.
// Mutation: drop `rowid > afterRowID` → the first page repeats forever (the helper's page cap trips).
func TestJournalFeed_ReportsPageToTheEndByRowID(t *testing.T) {
	s, m := reportFixture(t)
	for n := 1; n <= 5; n++ {
		mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportProgress, n, int64(20+n)))
	}
	for _, limit := range []int{1, 2, 5, 9} {
		got := allReports(t, s, limit)
		if len(got) != 5 {
			t.Fatalf("limit %d: %d reports, want 5", limit, len(got))
		}
		for i, r := range got {
			if r.ID != reportID(i+1) {
				t.Errorf("limit %d: report %d = %s, want %s", limit, i, r.ID, reportID(i+1))
			}
			if i > 0 && r.RowID <= got[i-1].RowID {
				t.Errorf("limit %d: rowids not ascending: %d after %d", limit, r.RowID, got[i-1].RowID)
			}
		}
	}
}

// A report inserted after the cursor is seen once, and the cursor then sees nothing.
// Mutation: `rowid >= afterRowID` → the last row repeats.
func TestJournalFeed_AReportAfterTheCursorIsSeenOnce(t *testing.T) {
	s, m := reportFixture(t)
	mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportProgress, 1, 20))
	first := allReports(t, s, 10)
	cur := first[len(first)-1].RowID
	if page, err := s.ReportsAfter(cur, 0, 10); err != nil || len(page) != 0 {
		t.Fatalf("nothing new: %+v err=%v", page, err)
	}
	mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportProgress, 2, 21))
	page, err := s.ReportsAfter(cur, 0, 10)
	if err != nil || len(page) != 1 || page[0].ID != reportID(2) {
		t.Fatalf("after the insert = %+v err=%v, want only report 2", page, err)
	}
	if again, err := s.ReportsAfter(page[0].RowID, 0, 10); err != nil || len(again) != 0 {
		t.Fatalf("report 2 seen again: %+v err=%v", again, err)
	}
}

// sinceMS keeps only the reports created at or after it (the first-start backfill window); 0 keeps all.
// Mutation: `>` for `>=` → a report created exactly at sinceMS is lost.
func TestJournalFeed_ReportsHonourTheSinceWindow(t *testing.T) {
	s, m := reportFixture(t)
	for n, at := range []int64{10, 20, 30} {
		mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportProgress, n+1, at))
	}
	got, err := s.ReportsAfter(0, 20, 10)
	if err != nil || len(got) != 2 || got[0].CreatedAt != 20 || got[1].CreatedAt != 30 {
		t.Fatalf("since 20 = %+v err=%v, want the reports at 20 and 30", got, err)
	}
	if all, err := s.ReportsAfter(0, 0, 10); err != nil || len(all) != 3 {
		t.Fatalf("since 0 = %d reports err=%v, want 3", len(all), err)
	}
}

// fields_json decodes into Needs and PR; a report without them has "" and 0, and the plain columns come through.
func TestJournalFeed_ReportFieldsAndMissingFields(t *testing.T) {
	s, m := reportFixture(t)
	prog := newReport(tTeamA, 1, m, team.ReportProgress, 1, 20)
	prog.Body = "the body"
	mustInsertReport(t, s, prog)
	mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportQuestion, 2, 21))
	mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportReady, 3, 22))
	if _, err := s.db.Exec(`UPDATE reports SET fields_json = '{}' WHERE id = ?`, reportID(1)); err != nil {
		t.Fatal(err)
	}
	got := allReports(t, s, 10)
	if len(got) != 3 {
		t.Fatalf("reports = %+v", got)
	}
	if r := got[0]; r.Kind != "progress" || r.Summary != "summary progress" || r.Body != "the body" || r.TaskSeq != 1 || r.Needs != "" || r.PR != 0 ||
		r.TeamID != tTeamA || r.MemberKey != m || r.MemberSessionID != "sess-a" || r.CreatedAt != 20 {
		t.Errorf("progress report (fields_json {}) = %+v", r)
	}
	if r := got[1]; r.Kind != "question" || r.Needs != "lead" || r.PR != 0 {
		t.Errorf("question report = %+v, want needs lead", r)
	}
	if r := got[2]; r.Kind != "ready" || r.PR != 12 {
		t.Errorf("ready report = %+v, want PR 12", r)
	}
}

// TeamInfo answers the label and the lead's CURRENT session: a lead that relayed shows the new one.
// Mutation: read the lead from team_members / a cached value → the old session answers.
func TestJournalFeed_TeamInfoFollowsALeadRelay(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, tTeamA, "lead-a", 1)
	if _, err := s.db.Exec(`UPDATE teams SET team_label = 'frontend' WHERE id = ?`, tTeamA); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.TeamInfo(tTeamA)
	if err != nil || !ok || !reflect.DeepEqual(got, team.TeamInfo{TeamID: tTeamA, Label: "frontend", LeadSessionID: "lead-a"}) {
		t.Fatalf("TeamInfo = %+v ok=%v err=%v", got, ok, err)
	}
	if _, err := s.db.Exec(`UPDATE teams SET lead_session_id = 'lead-new', ended_at = 777 WHERE id = ?`, tTeamA); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.TeamInfo(tTeamA)
	if err != nil || !ok || got.LeadSessionID != "lead-new" || got.EndedAt != 777 {
		t.Fatalf("after the relay and the end = %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := s.TeamInfo("no-such-team"); err != nil || ok {
		t.Fatalf("an unknown team: ok=%v err=%v, want false and no error", ok, err)
	}
}

// A limit below 1 answers none; a store error is an error, never an empty answer.
func TestJournalFeed_LimitAndStoreError(t *testing.T) {
	s, m := reportFixture(t)
	mustInsertReport(t, s, newReport(tTeamA, 1, m, team.ReportProgress, 1, 20))
	if got, err := s.TasksChangedAfter(team.TaskCursor{}, 0); err != nil || len(got) != 0 {
		t.Errorf("tasks with limit 0 = %+v err=%v", got, err)
	}
	if got, err := s.ReportsAfter(0, 0, -1); err != nil || len(got) != 0 {
		t.Errorf("reports with limit -1 = %+v err=%v", got, err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TasksChangedAfter(team.TaskCursor{}, 10); err == nil {
		t.Error("TasksChangedAfter on a closed store answered without an error")
	}
	if _, err := s.ReportsAfter(0, 0, 10); err == nil {
		t.Error("ReportsAfter on a closed store answered without an error")
	}
	if _, _, err := s.TeamInfo(tTeamA); err == nil {
		t.Error("TeamInfo on a closed store answered without an error")
	}
}

func hasIndex(t *testing.T, s *Store, table, name string) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_index_list(?) WHERE name = ?`, table, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// A team.db written before the index existed gets tasks_updated when it is opened, with no data change; and the task
// query is served by it, not by a scan and a sort. Mutation: drop the CREATE INDEX → both fail.
func TestJournalFeed_TheTasksUpdatedIndexExistsAfterMigratingAnOldDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	m := seedTaskTeam(t, s, tTeamA, "lead-a", "op-a", "sess-a")
	mustCreateTask(t, s, newTask(tTeamA, m, "work", 100))
	if _, err := s.db.Exec(`DROP INDEX tasks_updated`); err != nil { // the schema as an older build left it
		t.Fatal(err)
	}
	if hasIndex(t, s, "tasks", "tasks_updated") {
		t.Fatal("setup: the index is still there")
	}
	s.Close()

	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !hasIndex(t, s, "tasks", "tasks_updated") {
		t.Fatal("tasks_updated was not created when the old DB was opened")
	}
	if got := allTaskChanges(t, s, 10); len(got) != 1 {
		t.Fatalf("data after the migration = %+v", got)
	}
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+tasksChangedAfterSQL, 0, "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, " | ")
	if !strings.Contains(joined, "tasks_updated") || strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("the tasks query is not served by the index in order: %s", joined)
	}
}

// The module publishes the feed under its key, answering from its own store.
// Mutation: drop the Register line → red.
func TestJournalFeed_RegisteredForTheJournal(t *testing.T) {
	f := newFixture(t)
	svc, ok := f.core.Registry.Get(team.JournalFeedKey)
	feed, isFeed := svc.(team.JournalFeed)
	if !ok || !isFeed {
		t.Fatalf("registry has no JournalFeed under %q (%T)", team.JournalFeedKey, svc)
	}
	f.makeMember("sid-m")
	if info, found, err := feed.TeamInfo(uid(9)); err != nil || !found || info.LeadSessionID != "sid-2" {
		t.Fatalf("TeamInfo(%s) = %+v found=%v err=%v; want the lead sid-2", uid(9), info, found, err)
	}
}
