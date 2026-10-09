package workbook

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// v1Schema is the deployed schema of workbook.db as WB-1a-ii shipped it, plus one row (what a host that has run the v1
// module holds). The v2 migration must carry it forward without touching a byte of it.
const v1Schema = `
	CREATE TABLE schema_version (version INTEGER NOT NULL);
	INSERT INTO schema_version (version) VALUES (1);
	CREATE TABLE wb_entries (
		id INTEGER PRIMARY KEY, conv_key TEXT NOT NULL, host_id TEXT NOT NULL, provider TEXT NOT NULL,
		session_id TEXT NOT NULL, turn_id TEXT NOT NULL, turn_at INTEGER NOT NULL, turn_seq INTEGER NOT NULL,
		state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', thing TEXT, push TEXT, entry TEXT,
		thing_done INTEGER NOT NULL DEFAULT 0, push_ready_at INTEGER NOT NULL DEFAULT 0,
		team_id TEXT, role TEXT, ref TEXT, prompt_ver INTEGER NOT NULL, latency_ms INTEGER,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, UNIQUE (session_id, turn_id));
	CREATE INDEX wb_entries_conv ON wb_entries (conv_key, id);
	CREATE INDEX wb_entries_turn_at ON wb_entries (turn_at);
	CREATE TABLE wb_status (conv_key TEXT PRIMARY KEY, status TEXT NOT NULL, entry_id INTEGER NOT NULL,
		session_id TEXT NOT NULL, updated_at INTEGER NOT NULL);
	INSERT INTO wb_entries (conv_key, host_id, provider, session_id, turn_id, turn_at, turn_seq, state, thing, push, entry,
		prompt_ver, created_at, updated_at) VALUES ('c', 'h1', 'claude', 's1', 't1', 1700000000000, 1, 'ok', '舊事', '舊推播', '舊紀錄。', 1, 5, 6);
	INSERT INTO wb_status VALUES ('c', '舊狀況', 1, 's1', 7);`

func writeV1(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(v1Schema); err != nil {
		t.Fatal(err)
	}
}

func version(t *testing.T, s *Store) int {
	t.Helper()
	var v int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// A host that ran v1 keeps every row, gets the new columns at their defaults, the todo table and its index, and version 2.
// Mutation gate: skip the ALTERs or the version bump → red.
func TestMigrateV2_CarriesAV1FileForward(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbook.db")
	writeV1(t, path)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v := version(t, s); v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	e, err := s.Entry(1)
	if err != nil || e.Thing != "舊事" || e.Push != "舊推播" || e.Entry != "舊紀錄。" || e.State != StateOK || e.TurnAt != 1700000000000 {
		t.Fatalf("the v1 row changed: %+v err=%v", e, err)
	}
	if e.Kind != KindTurn {
		t.Fatalf("kind = %q, want %q for an old row", e.Kind, KindTurn)
	}
	var in, out, cache sql.NullInt64
	if err := s.db.QueryRow(`SELECT usage_in, usage_out, usage_cache_read FROM wb_entries WHERE id = 1`).Scan(&in, &out, &cache); err != nil || in.Valid || out.Valid || cache.Valid {
		t.Fatalf("usage = %v %v %v err=%v, want NULLs", in, out, cache, err)
	}
	if st, ok, _ := s.Status("c"); !ok || st.Status != "舊狀況" {
		t.Fatalf("status = %+v", st)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('wb_todos', 'wb_todos_conv')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("todo table and index = %d, want 2 (err %v)", n, err)
	}
}

// A fresh file goes straight to v2; a v2 file opens untouched (re-runnable).
func TestMigrateV2_FreshAndAlreadyV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbook.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	id := mustInsert(t, s, pending("c", "s1", "t1", 1))
	if v := version(t, s); v != 2 {
		t.Fatalf("fresh version = %d", v)
	}
	s.Close()
	again, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen a v2 file: %v", err)
	}
	defer again.Close()
	if e, err := again.Entry(id); err != nil || e.ConvKey != "c" {
		t.Fatalf("row after reopen: %+v %v", e, err)
	}
	if v := version(t, again); v != 2 {
		t.Fatalf("version = %d", v)
	}
}

// A migration that fails half way leaves version 1 and the old shape, and the retry works.
// Mutation gate: run the v2 statements outside a transaction → red.
func TestMigrateV2_FailedStepLeavesVersion1AndRetries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbook.db")
	writeV1(t, path)
	pre, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer pre.Close()
	// an object already named wb_todos_conv: the v2 step's last CREATE fails, after its ALTERs and its CREATE TABLE
	if _, err := pre.Exec(`CREATE VIEW wb_todos_conv AS SELECT 1 AS x`); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("the migration should have failed")
	}
	var cols, todos int
	if err := pre.QueryRow(`SELECT count(*) FROM pragma_table_info('wb_entries') WHERE name IN ('kind', 'usage_in')`).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("columns survived a failed step: %d (err %v)", cols, err)
	}
	if err := pre.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'wb_todos'`).Scan(&todos); err != nil || todos != 0 {
		t.Fatalf("wb_todos survived a failed step: %d (err %v)", todos, err)
	}
	var v int
	if err := pre.QueryRow(`SELECT version FROM schema_version`).Scan(&v); err != nil || v != 1 {
		t.Fatalf("version = %d (err %v), want 1", v, err)
	}
	if _, err := pre.Exec(`DROP VIEW wb_todos_conv`); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	defer s.Close()
	if version(t, s) != 2 {
		t.Fatal("the retry did not reach version 2")
	}
}

func TestMigrateV2_FileModesStayOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbook.db")
	writeV1(t, path)
	if err := os.Chmod(path, 0o644); err != nil { // a v1 file made with the usual umask
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustInsert(t, s, pending("c", "s2", "t2", 2))
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %o", filepath.Base(p), fi.Mode().Perm())
		}
	}
}

// A file from a newer daemon is refused, not guessed at.
func TestMigrate_RefusesANewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbook.db")
	writeV1(t, path)
	db, _ := sql.Open("sqlite", path)
	db.Exec(`UPDATE schema_version SET version = 99`)
	db.Close()
	if _, err := OpenStore(path); err == nil {
		t.Fatal("a version 99 file was opened")
	}
}

// Two daemons (or a restart that overlaps its predecessor) opening the same file at once: both succeed and the file ends at
// version 2 with its single version row. Before the fix the second saw an old version outside the transaction and ran the
// step again (a duplicate column). Mutation gate: read the version outside the lock → red (run with -count).
func TestMigrate_ConcurrentOpenersBothSucceed(t *testing.T) {
	for _, fromV1 := range []bool{false, true} {
		for round := 0; round < 5; round++ {
			path := filepath.Join(t.TempDir(), "workbook.db")
			if fromV1 {
				writeV1(t, path)
			}
			const n = 6
			errs := make(chan error, n)
			stores := make(chan *Store, n)
			start := make(chan struct{})
			for i := 0; i < n; i++ {
				go func() {
					<-start
					s, err := OpenStore(path)
					errs <- err
					stores <- s
				}()
			}
			close(start)
			for i := 0; i < n; i++ {
				if err := <-errs; err != nil {
					t.Fatalf("fromV1=%v round %d: an opener failed: %v", fromV1, round, err)
				}
			}
			var last *Store
			for i := 0; i < n; i++ {
				if s := <-stores; s != nil {
					if last != nil {
						last.Close()
					}
					last = s
				}
			}
			var rows, v int
			if err := last.db.QueryRow(`SELECT count(*), max(version) FROM schema_version`).Scan(&rows, &v); err != nil || rows != 1 || v != 2 {
				t.Fatalf("fromV1=%v: version rows=%d max=%d err=%v", fromV1, rows, v, err)
			}
			last.Close()
		}
	}
}
