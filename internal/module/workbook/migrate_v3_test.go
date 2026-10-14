package workbook

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// writeV2 builds the deployed v2 file: v1's schema, the v2 step, and todos in two conversations (ids interleaved).
func writeV2(t *testing.T, path string) {
	t.Helper()
	writeV1(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateV2(db); err != nil {
		t.Fatal(err)
	}
	for i, conv := range []string{"a", "b", "a", "a", "b"} {
		if _, err := db.Exec(`INSERT INTO wb_todos (id, conv_key, title, state, added_entry_id, created_at, updated_at) VALUES (?, ?, 't', 'open', 1, 1, 1)`,
			10+i, conv); err != nil {
			t.Fatal(err)
		}
	}
}

// A v2 file keeps its todos, numbers them 1..n per conversation in id order, gets the journal tables and the unique
// index, and is at version 3.
// Mutation gate: backfill by a global counter (not per conversation) -> red; drop the unique index -> red.
func TestMigrateV3_BackfillsSeqPerConversation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbook.db")
	writeV2(t, path)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v := version(t, s); v != 3 {
		t.Fatalf("version = %d, want 3", v)
	}
	want := map[int]int{10: 1, 11: 1, 12: 2, 13: 3, 14: 2}
	rows, err := s.db.Query(`SELECT id, seq FROM wb_todos`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, seq int
		if err := rows.Scan(&id, &seq); err != nil {
			t.Fatal(err)
		}
		if want[id] != seq {
			t.Fatalf("todo %d: seq = %d, want %d", id, seq, want[id])
		}
		n++
	}
	if n != 5 {
		t.Fatalf("%d todos, want 5", n)
	}
	var tables int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN
		('wb_journal_outbox', 'wb_journal_units', 'wb_journal_cursors', 'wb_todos_conv_seq', 'wb_journal_outbox_entry_kind')`).Scan(&tables); err != nil || tables != 5 {
		t.Fatalf("journal tables and indexes = %d, want 5 (err %v)", tables, err)
	}
	// The unique index refuses a second todo with the same number in a conversation, but allows seq 0 repeats.
	ins := `INSERT INTO wb_todos (conv_key, title, state, added_entry_id, created_at, updated_at, seq) VALUES ('a', 't', 'open', 1, 1, 1, ?)`
	if _, err := s.db.Exec(ins, 3); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("duplicate seq: err = %v, want a UNIQUE violation", err)
	}
	if _, err := s.db.Exec(ins, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ins, 0); err != nil {
		t.Fatalf("seq 0 may repeat: %v", err)
	}
}

// The outbox holds one row per (entry, kind): a second insert of the same pair is refused.
func TestMigrateV3_OutboxIsUniquePerEntryAndKind(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "workbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ins := `INSERT INTO wb_journal_outbox (entry_id, unit_key, kind, payload, created_at) VALUES (1, 'u', ?, '{}', 1)`
	if _, err := s.db.Exec(ins, "turn"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ins, "journal"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ins, "turn"); err == nil {
		t.Fatal("a second turn row for one entry was accepted")
	}
}
