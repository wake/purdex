package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// deployedV4 writes a wwd.db exactly as the deployed v4 schema leaves it (the DDL of v1 to v4), with a row in the tables a
// migration must not lose.
func deployedV4(t *testing.T, p string) {
	t.Helper()
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE host_schema (version INTEGER NOT NULL)`,
		`INSERT INTO host_schema (version) VALUES (4)`,
		`CREATE TABLE worker_settings (id INTEGER PRIMARY KEY CHECK (id = 1), enabled INTEGER NOT NULL, heavy INTEGER NOT NULL, light INTEGER NOT NULL, owner_rule TEXT NOT NULL)`,
		`INSERT INTO worker_settings VALUES (1, 1, 1, 2, 'hid<5m')`,
		`CREATE TABLE exceptions (name TEXT PRIMARY KEY, until_ms INTEGER, pid INTEGER, pid_ident TEXT NOT NULL DEFAULT '', holds TEXT NOT NULL, since_ms INTEGER NOT NULL)`,
		`CREATE TABLE host_minutes (at INTEGER PRIMARY KEY, rec TEXT NOT NULL)`,
		`CREATE TABLE observations (id TEXT PRIMARY KEY, started_ms INTEGER NOT NULL, rec TEXT NOT NULL)`,
		`CREATE INDEX observations_started ON observations (started_ms)`,
		`CREATE TABLE job_usage (job_id TEXT PRIMARY KEY, ended_ms INTEGER NOT NULL, rec TEXT NOT NULL)`,
		`CREATE INDEX job_usage_ended ON job_usage (ended_ms)`,
		`INSERT INTO host_minutes VALUES (1000, '{"kept":true}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// The deployed v4 file gains wwd_starts and alerts on open, keeps every row it had, and opening it again changes nothing.
func TestOpenMigratesV4ToV5(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wwd.db")
	deployedV4(t, p)
	for round := 1; round <= 2; round++ { // the second open is the re-run
		s, err := Open(p)
		if err != nil {
			t.Fatalf("open %d: %v", round, err)
		}
		for _, name := range []string{"wwd_starts", "alerts"} {
			if !tableExists(t, s.DB(), name) {
				t.Fatalf("open %d: table %s missing", round, name)
			}
		}
		var rec string
		if err := s.DB().QueryRow(`SELECT rec FROM host_minutes WHERE at = 1000`).Scan(&rec); err != nil || rec != `{"kept":true}` {
			t.Fatalf("open %d: the old rows must be kept: %q %v", round, rec, err)
		}
		if ws, ok, err := s.LoadWorker(); err != nil || !ok || ws.OwnerRule != "hid<5m" {
			t.Fatalf("open %d: worker settings: %+v %v %v", round, ws, ok, err)
		}
		var v int
		if err := s.DB().QueryRow(`SELECT version FROM host_schema`).Scan(&v); err != nil || v != schemaVersion {
			t.Fatalf("open %d: version %d (const %d) err %v", round, v, schemaVersion, err)
		}
		s.Close()
	}
}

// A new file has them too, and a file a newer wwd wrote is refused (a rolled-back binary does not run on a newer schema).
func TestOpenFreshHasV5TablesAndRefusesNewer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wwd.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wwd_starts", "alerts"} {
		if !tableExists(t, s.DB(), name) {
			t.Fatalf("fresh: table %s missing", name)
		}
	}
	if _, err := s.DB().Exec(`UPDATE host_schema SET version = 99`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(p); err == nil {
		t.Fatal("a wwd.db of a newer schema was opened")
	}
}

func TestActiveAlertsReadsOnlyUnclearedRows(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.ActiveAlerts()
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("an empty table gives an empty list, not nil: %v %v", got, err)
	}
	since := time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC)
	rows := []struct {
		key, kind, level string
		cleared          any
	}{
		{"disk_low:/", "disk_low", "push", nil},
		{"thrash", "thrash", "digest", since.Add(20 * time.Minute).UnixMilli()},
		{"orphans", "orphans", "digest", nil},
	}
	for _, r := range rows {
		if _, err := s.DB().Exec(`INSERT INTO alerts (key, kind, level, since_ms, last_ms, cleared_ms) VALUES (?, ?, ?, ?, ?, ?)`,
			r.key, r.kind, r.level, since.UnixMilli(), since.Add(10*time.Minute).UnixMilli(), r.cleared); err != nil {
			t.Fatal(err)
		}
	}
	got, err = s.ActiveAlerts()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "disk_low:/" || got[1].Key != "orphans" {
		t.Fatalf("active alerts, oldest key first: %+v", got)
	}
	if got[0].ClearedAt != nil || !got[0].Since.Equal(since) || !got[0].Last.Equal(since.Add(10*time.Minute)) {
		t.Fatalf("%+v", got[0])
	}
	if err := got[0].Validate(); err != nil {
		t.Fatalf("a stored row is a valid wire.Alert: %v", err)
	}
}

// The alert's one-line detail is a column of the v5 table (made with it, so 8a-1 needs no second step), empty by default, and a stored
// text over the limit is cut on the way out instead of failing the whole list.
func TestAlertsDetailColumn(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	since := time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC).UnixMilli()
	long := strings.Repeat("剩", 100) + "x" // 301 bytes
	for _, r := range []struct{ key, detail string }{{"a", "mlab 剩 11.2 GB"}, {"b", long}} {
		if _, err := s.DB().Exec(`INSERT INTO alerts (key, kind, level, since_ms, last_ms, detail) VALUES (?, 'disk_low', 'push', ?, ?, ?)`, r.key, since, since, r.detail); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`INSERT INTO alerts (key, kind, level, since_ms, last_ms) VALUES ('c', 'orphans', 'digest', ?, ?)`, since, since); err != nil {
		t.Fatalf("detail must default to empty: %v", err)
	}
	got, err := s.ActiveAlerts()
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Detail != "mlab 剩 11.2 GB" || got[2].Detail != "" {
		t.Fatalf("%+v", got)
	}
	if len(got[1].Detail) > wire.MaxAlertDetailBytes || len(got[1].Detail) == 0 {
		t.Fatalf("a long stored detail is cut, not dropped or refused: %d bytes", len(got[1].Detail))
	}
}

// A deployed v4 file gets the column with its table (the table is new in v5, so there is no older shape to alter).
func TestV4FileGetsAlertsWithDetail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wwd.db")
	deployedV4(t, p)
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB().Exec(`INSERT INTO alerts (key, kind, level, since_ms, last_ms, detail) VALUES ('k', 'thrash', 'digest', 1, 1, 'x')`); err != nil {
		t.Fatal(err)
	}
}

// wwd_starts is a table only in this PR (spec §13.1): the start record itself is written by 8a-0.
func TestStartsTableShape(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB().Exec(`INSERT INTO wwd_starts (started_ms, version) VALUES (1000, 'v1')`); err != nil {
		t.Fatal(err)
	}
	var prev string
	if err := s.DB().QueryRow(`SELECT prev_exit FROM wwd_starts WHERE started_ms = 1000`).Scan(&prev); err != nil || prev != "" {
		t.Fatalf("previous exit defaults to empty (unknown): %q %v", prev, err)
	}
	// one start is one row: a second record of the same instant is refused by the table (the writer uses INSERT OR IGNORE, so a
	// retried write is not counted twice)
	if _, err := s.DB().Exec(`INSERT INTO wwd_starts (started_ms, version) VALUES (1000, 'again')`); err == nil {
		t.Fatal("two starts at the same millisecond were both stored")
	}
	if _, err := s.DB().Exec(`INSERT OR IGNORE INTO wwd_starts (started_ms, version) VALUES (1000, 'again')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM wwd_starts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
