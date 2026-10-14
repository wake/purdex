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

// The write that replaces a frame's session id records the id it displaced, in the same statement.
func TestUpdateSessionIdentity_RecordsTheDisplacedId(t *testing.T) {
	s := openTestFramesStore(t)
	f := seedRoot(t, s, "%1", 10, "", "/w")
	step := func(sid string, seq int64, wantCur, wantPrev string) {
		t.Helper()
		if err := s.UpdateSessionIdentity(f.FrameID, sid, "", seq); err != nil {
			t.Fatal(err)
		}
		cur, prev, found, err := s.SessionTransition(f.FrameID)
		if err != nil || !found || cur != wantCur || prev != wantPrev {
			t.Fatalf("after %s: cur=%q prev=%q found=%v err=%v, want %q/%q", sid, cur, prev, found, err, wantCur, wantPrev)
		}
	}
	step("A", 1, "A", "")  // the first id displaces nothing
	step("A", 2, "A", "")  // the same id again changes nothing
	step("B", 3, "B", "A") // a new id displaces A
	step("B", 4, "B", "A") // a retry of it leaves the record
	step("C", 5, "C", "B")
	if _, _, found, err := s.SessionTransition("nope"); err != nil || found {
		t.Fatalf("missing frame: found=%v err=%v", found, err)
	}
}

// A last_agent_run table from before the pane column gains it (rows keep an empty pane), and a re-run is a no-op.
func TestLastRunMigration_AddsThePaneColumnToAnOldTable(t *testing.T) {
	events := openTestAgentEventStore(t)
	if _, err := events.Frames(); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE last_agent_run`,
		`CREATE TABLE last_agent_run (code TEXT PRIMARY KEY, tmux_instance TEXT NOT NULL, agent_type TEXT NOT NULL, session_id TEXT NOT NULL, cwd TEXT NOT NULL, ended_at INTEGER NOT NULL)`,
		`INSERT INTO last_agent_run VALUES ('c', 'i', 'cc', 'S', '/w', 5)`,
	} {
		if _, err := events.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := migrateFramesDB(events.db); err != nil {
			t.Fatal(err)
		}
	}
	frames, _ := events.Frames()
	run, err := frames.LastRun("c")
	if err != nil || run == nil || run.SessionID != "S" || run.PaneID != "" {
		t.Fatalf("run = %+v err=%v", run, err)
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
