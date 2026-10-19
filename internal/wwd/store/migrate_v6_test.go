package store

import (
	"path/filepath"
	"testing"
)

func tableCols(t *testing.T, s *Store, table string) map[string]bool {
	t.Helper()
	rows, err := s.DB().Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		cols[n] = true
	}
	return cols
}

func checkV6Tables(t *testing.T, s *Store) {
	t.Helper()
	for table, want := range map[string][]string{
		"earned_labels": {"label", "fingerprint", "snapshot", "granted_ms", "state"},
		"result_cache":  {"key", "rec", "host", "host_fingerprint", "label", "created_ms"},
	} {
		cols := tableCols(t, s, table)
		for _, c := range want {
			if !cols[c] {
				t.Errorf("%s has no column %s", table, c)
			}
		}
	}
	if _, err := s.DB().Exec(`INSERT INTO earned_labels (label, fingerprint, snapshot, granted_ms, state) VALUES ('x', '{}', '', 0, 'bogus')`); err == nil {
		t.Error("earned_labels accepted a state outside earned|quarantined")
	}
	var v int
	if err := s.DB().QueryRow(`SELECT version FROM host_schema`).Scan(&v); err != nil || v != 6 {
		t.Errorf("version %d err %v", v, err)
	}
}

// A new wwd.db is created with both v6 tables.
func TestOpenCreatesV6Tables(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkV6Tables(t, s)
}

// The v5 file (what 755 deploys) gains both tables on open, keeps every row it had, and opening it again changes nothing.
func TestOpenMigratesV5ToV6(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wwd.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO host_minutes VALUES (1000, '{"kept":true}')`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE earned_labels`, `DROP TABLE result_cache`, `UPDATE host_schema SET version = 5`} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	for round := 1; round <= 2; round++ {
		s, err := Open(p)
		if err != nil {
			t.Fatalf("open %d: %v", round, err)
		}
		checkV6Tables(t, s)
		var rec string
		if err := s.DB().QueryRow(`SELECT rec FROM host_minutes WHERE at = 1000`).Scan(&rec); err != nil || rec != `{"kept":true}` {
			t.Fatalf("open %d: the old rows must be kept: %q %v", round, rec, err)
		}
		for _, name := range []string{"wwd_starts", "alerts"} {
			if !tableExists(t, s.DB(), name) {
				t.Fatalf("open %d: v5 table %s missing", round, name)
			}
		}
		s.Close()
	}
}
