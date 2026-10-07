package teammod

import (
	"database/sql"
	"path/filepath"
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
