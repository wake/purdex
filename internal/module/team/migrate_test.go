package teammod

import (
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// columnsOf lists a table's columns (tests only).
func columnsOf(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[n] = true
	}
	return out
}

// ensureColumn adds a column once (a second call, or a reopen, is a no-op)
// and keeps the rows it finds; a name that is not a plain identifier is
// refused before any SQL runs. OpenStore gives team_members and teams their
// persisted reading (P4-6, spec §8.5) on a team.db written before them.
func TestEnsureColumn_AddsOnceKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (id TEXT PRIMARY KEY); INSERT INTO t (id) VALUES ('a')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := ensureColumn(db, "t", "n", "INTEGER NOT NULL DEFAULT 7"); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	var id string
	var n int
	if err := db.QueryRow(`SELECT id, n FROM t`).Scan(&id, &n); err != nil || id != "a" || n != 7 {
		t.Fatalf("row after the add = %q %d (%v), want a 7", id, n, err)
	}
	for _, bad := range [][2]string{{"t; DROP TABLE t", "n"}, {"t", "n INTEGER; --"}, {"", "n"}} {
		if err := ensureColumn(db, bad[0], bad[1], "TEXT"); err == nil {
			t.Fatalf("ensureColumn(%q, %q) accepted", bad[0], bad[1])
		}
	}

	s := openTestStore(t)
	for table, cols := range map[string][]string{
		"team_members": {"usage_pct", "usage_window", "usage_model", "usage_effort", "usage_at"},
		"teams":        {"lead_usage_pct", "lead_usage_window", "lead_usage_model", "lead_usage_effort", "lead_usage_at"},
	} {
		have := columnsOf(t, s.db, table)
		for _, c := range cols {
			if !have[c] {
				t.Errorf("%s has no column %s", table, c)
			}
		}
	}
}

// Two daemons opening one old team.db at once (P4-6 review, attacker
// medium): both connections find a column absent, both ALTER, and the one
// that loses the race meets "duplicate column". It re-reads the schema, finds
// the column the other added, with the declared type, and goes on: both
// migrations succeed. A column the other added with another type is still an
// error. Mutation gate: drop the re-check after a failed ALTER → red.
func TestEnsureColumn_TwoConnectionsMigrateOneOldDBAtOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	if _, err := open().Exec(teamSchema + `; CREATE TABLE t (id TEXT)`); err != nil { // teams and team_members as P4-3 shipped them
		t.Fatal(err)
	}
	var calls atomic.Int32
	var both sync.WaitGroup
	both.Add(2)
	afterColumnCheck = func() { // both read "absent" before either ALTERs
		if calls.Add(1) <= 2 {
			both.Done()
			both.Wait()
		}
	}
	t.Cleanup(func() { afterColumnCheck = nil })
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		db := open()
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = migrateUsage(db)
		}()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("concurrent migrations: %v / %v, want both to succeed", errs[0], errs[1])
	}
	db := open()
	if have := columnsOf(t, db, "team_members"); !have["usage_pct"] || !have["usage_at"] {
		t.Fatalf("team_members columns after the race = %v", have)
	}

	other := open()
	afterColumnCheck = func() { _, _ = other.Exec(`ALTER TABLE t ADD COLUMN n TEXT`) }
	if err := ensureColumn(db, "t", "n", "INTEGER NOT NULL DEFAULT 0"); err == nil {
		t.Fatal("a column the other connection added with another type was accepted")
	}
}
