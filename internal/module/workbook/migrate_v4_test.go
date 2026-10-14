package workbook

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A v3 file (the outbox rows of entries exist) gets the outbox rows of the team task mirror, which belong to no entry
// (entry_id 0): the one-per-(entry, kind) index covers entries only. Existing rows keep their place, and an entry still
// cannot have two rows of one kind.
// Mutation gate: keep the index unconditional -> the second task row is refused (red); drop the index -> an entry's
// duplicate row is accepted (red).
func TestMigrateV4_OutboxRowsWithoutAnEntryMayRepeatButAnEntrysMayNot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbook.db")
	writeV2(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateV3(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO wb_journal_outbox (entry_id, unit_key, kind, payload, created_at) VALUES (7, 'solo:a', 'turn', '{}', 1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v := version(t, s); v != 4 {
		t.Fatalf("version = %d, want 4", v)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM wb_journal_outbox WHERE entry_id = 7`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the existing row: %d, %v", n, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.db.Exec(`INSERT INTO wb_journal_outbox (entry_id, unit_key, kind, payload, created_at) VALUES (0, 'team:t1', 'task', '{}', 1)`); err != nil {
			t.Fatalf("task row %d: %v", i, err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO wb_journal_outbox (entry_id, unit_key, kind, payload, created_at) VALUES (7, 'solo:a', 'turn', '{}', 2)`); err == nil {
		t.Fatal("an entry got a second row of one kind")
	}
}
