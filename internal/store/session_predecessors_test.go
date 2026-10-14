package store

import "testing"

func TestSessionPredecessors_RecordAndRead(t *testing.T) {
	s := openTestFramesStore(t)
	if _, _, ok, err := s.PredecessorOf("B"); err != nil || ok {
		t.Fatalf("empty: ok=%v err=%v", ok, err)
	}
	if err := s.RecordPredecessor("B", "A", 1000, "c1"); err != nil {
		t.Fatal(err)
	}
	// the first record wins
	if err := s.RecordPredecessor("B", "Z", 2000, "c2"); err != nil {
		t.Fatal(err)
	}
	prev, at, ok, err := s.PredecessorOf("B")
	if err != nil || !ok || prev != "A" || at != 1000 {
		t.Fatalf("got %q %d %v %v", prev, at, ok, err)
	}
}

func TestSessionPredecessors_MigrationAddsTableToExistingDBAndIsIdempotent(t *testing.T) {
	events := openTestAgentEventStore(t)
	if _, err := events.Frames(); err != nil {
		t.Fatal(err)
	}
	if _, err := events.db.Exec(`DROP TABLE session_predecessors`); err != nil {
		t.Fatal(err)
	}
	// a DB from before this table: the next open gains it, and a second run changes nothing
	if err := migrateFramesDB(events.db); err != nil {
		t.Fatal(err)
	}
	if err := migrateFramesDB(events.db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := events.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'session_predecessors'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("table count %d %v", n, err)
	}
}
