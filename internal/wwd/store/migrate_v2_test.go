package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A wwd.db written by the v1 schema (no host_minutes) gains the table on open,
// keeping what it had.
func TestOpenMigratesV1ToV2(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wwd.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE host_schema (version INTEGER NOT NULL)`,
		`INSERT INTO host_schema (version) VALUES (1)`,
		`CREATE TABLE worker_settings (id INTEGER PRIMARY KEY CHECK (id = 1), enabled INTEGER NOT NULL, heavy INTEGER NOT NULL, light INTEGER NOT NULL, owner_rule TEXT NOT NULL)`,
		`INSERT INTO worker_settings VALUES (1, 1, 1, 2, 'r')`,
		`CREATE TABLE exceptions (name TEXT PRIMARY KEY, until_ms INTEGER, pid INTEGER, pid_ident TEXT NOT NULL DEFAULT '', holds TEXT NOT NULL, since_ms INTEGER NOT NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveMinute(minuteRec(t0, 1)); err != nil {
		t.Fatalf("host_minutes after migration: %v", err)
	}
	if ws, ok, err := s.LoadWorker(); err != nil || !ok || ws.OwnerRule != "r" {
		t.Fatalf("old rows kept: %+v %v %v", ws, ok, err)
	}
	var v int
	if err := s.DB().QueryRow(`SELECT version FROM host_schema`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("version %d err %v", v, err)
	}
}
