package workbook

import (
	"testing"
)

func TestJournalCursor_ReadsWhatApplyTaskPageWrote(t *testing.T) {
	s := openTest(t)
	if _, ok, err := s.JournalCursor("tasks"); ok || err != nil {
		t.Fatalf("an unset cursor: %v, %v", ok, err)
	}
	if err := s.ApplyTaskPage(TaskPage{Cursors: map[string]string{"tasks": "1", "reports": "7"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyTaskPage(TaskPage{Cursors: map[string]string{"tasks": "2"}}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"tasks": "2", "reports": "7"} {
		if got, ok, err := s.JournalCursor(name); err != nil || !ok || got != want {
			t.Errorf("cursor %s = %q, %v, %v; want %q", name, got, ok, err, want)
		}
	}
}

// The rows and the cursors of a page are one transaction: a page that cannot be stored leaves the cursor where it was,
// so the page is read again, not lost.
// Mutation gate: write the cursors in a transaction of their own, first -> the cursor moves although no row was stored (red).
func TestApplyTaskPage_IsAllOrNothing(t *testing.T) {
	s := openTest(t)
	if err := s.ApplyTaskPage(TaskPage{Cursors: map[string]string{"tasks": "1"}}); err != nil {
		t.Fatal(err)
	}
	err := s.ApplyTaskPage(TaskPage{
		Rows:    []TaskOutboxRow{{UnitKey: "team:t1", Payload: map[string]any{"x": make(chan int)}}}, // cannot be encoded
		Cursors: map[string]string{"tasks": "99"},
	})
	if err == nil {
		t.Fatal("a page with an unstorable row was accepted")
	}
	if got, _, _ := s.JournalCursor("tasks"); got != "1" {
		t.Fatalf("the cursor moved to %q although the page failed", got)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM wb_journal_outbox`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d outbox rows after a failed page, %v", n, err)
	}
}

func TestReadyOutbox_ListsTaskRowsThatHaveNoEntryAndStillHoldsBackPendingEntries(t *testing.T) {
	s := openTest(t)
	s.EnableJournalOutbox()
	if err := s.ApplyTaskPage(TaskPage{Rows: []TaskOutboxRow{{UnitKey: "team:t1", Payload: map[string]any{"team": "t1"}}, {UnitKey: "team:t1", Payload: map[string]any{"team": "t1", "n": 2}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO wb_entries (id, conv_key, host_id, provider, session_id, turn_id, turn_at, turn_seq, state, prompt_ver, created_at, updated_at)
		VALUES (5, 'c', 'h', 'claude', 's', 't', 1, 1, 'pending', 2, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO wb_journal_outbox (entry_id, unit_key, kind, payload, created_at) VALUES (5, 'solo:c', 'turn', '{}', 1)`); err != nil {
		t.Fatal(err)
	}
	items, err := s.ReadyOutbox(10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Kind != OutboxTask || items[0].EntryID != 0 || items[1].Kind != OutboxTask {
		t.Fatalf("ready = %+v, want the two task rows and not the pending entry's", items)
	}
}
