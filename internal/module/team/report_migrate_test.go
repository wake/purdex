package teammod

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// A team.db written by alpha.608 has a reports table whose primary key is
// (team_id, id); CREATE TABLE IF NOT EXISTS skips it, so OpenStore rebuilds it
// with the per-member key. These tests build the OLD table with raw SQL.

// oldReportsDDL is the reports table as an older build created it, with the
// given primary key clause and member_key declaration.
func oldReportsDDL(pk, memberKey string) string {
	return `CREATE TABLE reports (
		id          TEXT    NOT NULL,
		team_id     TEXT    NOT NULL,
		task_seq    INTEGER NOT NULL,
		member_key  ` + memberKey + `,
		kind        TEXT    NOT NULL,
		summary     TEXT    NOT NULL,
		fields_json TEXT    NOT NULL DEFAULT '{}',
		body        TEXT    NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL,
		PRIMARY KEY (` + pk + `)
	);
	CREATE INDEX reports_task ON reports (team_id, task_seq, created_at);`
}

// oldRows are three reports of two teams, kinds, fields and bodies.
var oldRows = [][]any{
	{reportID(1), tTeamA, 1, "op-a", "ready", "ready one", `{"pr":12,"reviews":["R1=job-1"]}`, "body\nwith two lines", 100},
	{reportID(2), tTeamB, 1, "op-b", "done", "done two", `{}`, "", 110},
	{reportID(3), tTeamA, 2, "op-a", "question", "ask three", `{"needs":"lead"}`, "ünïcode body", 120},
}

func writeOldReports(t *testing.T, path, pk, memberKey string, rows [][]any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(oldReportsDDL(pk, memberKey)); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO reports (id, team_id, task_seq, member_key, kind, summary, fields_json, body, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r...); err != nil {
			t.Fatal(err)
		}
	}
}

// dumpReports is every reports row, all columns, ordered by id.
func dumpReports(t *testing.T, db *sql.DB) [][]any {
	t.Helper()
	rows, err := db.Query(`SELECT id, team_id, task_seq, member_key, kind, summary, fields_json, body, created_at FROM reports ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		var id, tm, mk, kind, sum, fj, body string
		var seq, at int
		if err := rows.Scan(&id, &tm, &seq, &mk, &kind, &sum, &fj, &body, &at); err != nil {
			t.Fatal(err)
		}
		out = append(out, []any{id, tm, seq, mk, kind, sum, fj, body, at})
	}
	return out
}

func pkLine(t *testing.T, db *sql.DB) string {
	t.Helper()
	pk, err := reportsPK(db)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(pk, ",")
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func TestMigrateReportsPK_RebuildsAnOldTable(t *testing.T) {
	for name, pk := range map[string]string{"(team_id, id)": "team_id, id", "(id)": "id"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "team.db")
			writeOldReports(t, path, pk, "TEXT NOT NULL", oldRows)

			raw, _ := sql.Open("sqlite", path)
			t.Logf("before: pk = [%s]", pkLine(t, raw))
			raw.Close()

			s, err := OpenStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if got := pkLine(t, s.db); got != "team_id,member_key,id" {
				t.Fatalf("pk = [%s], want [team_id,member_key,id]", got)
			}
			t.Logf("after:  pk = [%s]", pkLine(t, s.db))
			if got := dumpReports(t, s.db); !reflect.DeepEqual(got, oldRows) {
				t.Fatalf("rows after the rebuild:\n%v\nwant\n%v", got, oldRows)
			}
			var idx string
			if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'reports_task'`).Scan(&idx); err != nil ||
				!strings.Contains(idx, "(team_id, task_seq, created_at)") {
				t.Fatalf("reports_task index = %q (%v)", idx, err)
			}
			if tableExists(t, s.db, "reports_new") {
				t.Fatal("reports_new was left behind")
			}

			// The rebuilt rows read through the store, and two members of team A
			// now use one id each, both succeed.
			seedTeam(t, s, tTeamA, "lead-a", 1)
			seedMember(t, s, "op-a", tTeamA, "sess-a", 1)
			seedMember(t, s, "op-c", tTeamA, "sess-c", 1)
			mustCreateTask(t, s, newTask(tTeamA, "op-a", "a", 10))
			mustCreateTask(t, s, newTask(tTeamA, "op-c", "c", 10))
			mustCreateTask(t, s, newTask(tTeamA, "op-a", "a2", 10))
			if got, ok, _ := s.GetReportOf(tTeamA, "op-a", reportID(1)); !ok || got.PR != 12 || got.Body != "body\nwith two lines" {
				t.Fatalf("migrated report through the store: %+v ok=%v", got, ok)
			}
			if _, _, replay, err := s.InsertReport(newReport(tTeamA, 1, "op-a", team.ReportProgress, 9, 200)); err != nil || replay {
				t.Fatalf("a: %v replay=%v", err, replay)
			}
			c := newReport(tTeamA, 2, "op-c", team.ReportDone, 9, 210)
			if _, _, replay, err := s.InsertReport(c); err != nil || replay {
				t.Fatalf("c with a's id: %v replay=%v, want an independent insert", err, replay)
			}
		})
	}
}

// A table that already has the right key is left alone, and so is one the
// next open meets again: no rebuild (a column added afterwards survives).
func TestMigrateReportsPK_SecondOpenIsANoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	writeOldReports(t, path, "team_id, id", "TEXT NOT NULL", oldRows)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE reports ADD COLUMN marker TEXT`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !columnsOf(t, s.db, "reports")["marker"] {
		t.Fatal("the second open rebuilt a table that already had the right key")
	}
	if got := dumpReports(t, s.db); !reflect.DeepEqual(got, oldRows) {
		t.Fatalf("rows changed on the second open: %v", got)
	}
	// A brand-new database is created right and never rebuilt.
	fresh := openTestStore(t)
	if got := pkLine(t, fresh.db); got != "team_id,member_key,id" {
		t.Fatalf("a new database has pk [%s]", got)
	}
}

// A migration that fails in the middle rolls back whole: the old table, its
// rows and its index are exactly as they were, and nothing is left behind.
// The failure is a NULL member_key an older build allowed, which the new
// NOT NULL column refuses in the copy.
func TestMigrateReportsPK_FailureLeavesTheOldTableUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	rows := append([][]any{}, oldRows...)
	rows = append(rows, []any{reportID(4), tTeamA, 1, nil, "progress", "no owner", `{}`, "", 130})
	writeOldReports(t, path, "team_id, id", "TEXT", rows)

	if s, err := OpenStore(path); err == nil {
		s.Close()
		t.Fatal("OpenStore accepted a table that cannot be copied")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := pkLine(t, db); got != "team_id,id" {
		t.Fatalf("pk after the failed migration = [%s], want the old [team_id,id]", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reports`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("%d rows after the failed migration (%v), want 4", n, err)
	}
	if tableExists(t, db, "reports_new") {
		t.Fatal("reports_new was left behind")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'reports_task'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reports_task index n=%d err=%v", n, err)
	}
}

// Two daemons opening one team.db: the one that arrives second finds the
// table already rebuilt (it reads the key again under the write lock) and
// does not rebuild it.
func TestMigrateReportsPK_ASecondOpenerDoesNotRebuildTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	writeOldReports(t, path, "team_id, id", "TEXT NOT NULL", oldRows)
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	first, second := open(), open()

	var calls atomic.Int32
	afterReportsPKRead = func() {
		afterReportsPKRead = nil
		calls.Add(1)
		// The other daemon gets there between this one's check and its lock,
		// migrates, and adds a column a rebuild would drop.
		if err := migrateReportsPK(second); err != nil {
			t.Errorf("second: %v", err)
		}
		if _, err := second.Exec(`ALTER TABLE reports ADD COLUMN marker TEXT`); err != nil {
			t.Errorf("marker: %v", err)
		}
	}
	t.Cleanup(func() { afterReportsPKRead = nil })
	if err := migrateReportsPK(first); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("the seam ran %d times", calls.Load())
	}
	if !columnsOf(t, first, "reports")["marker"] {
		t.Fatal("the late opener rebuilt a table that was already rebuilt")
	}
	if got := dumpReports(t, first); len(got) != len(oldRows) {
		t.Fatalf("%d rows, want %d", len(got), len(oldRows))
	}
	if got := pkLine(t, first); got != "team_id,member_key,id" {
		t.Fatalf("pk = [%s]", got)
	}
}

// reportsPK is covered through the cases above; a table of another shape is
// rebuilt too, and a missing table is created.
func TestMigrateReportsPK_MissingTableIsCreated(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateReportsPK(db); err != nil {
		t.Fatal(err)
	}
	if got := pkLine(t, db); got != "team_id,member_key,id" {
		t.Fatalf("pk = [%s]", got)
	}
}
