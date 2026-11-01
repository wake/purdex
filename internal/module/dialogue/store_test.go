package dialogue

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dialogue.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	s.now = func() int64 { return 1000 }
	t.Cleanup(func() { s.Close() })
	return s, path
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestOpenStoreCreatesSchema(t *testing.T) {
	s, _ := openTestStore(t)
	if v := userVersion(t, s.db); v != 1 {
		t.Fatalf("user_version = %d, want 1", v)
	}
	for _, tb := range []string{"dlg_sessions", "dlg_turns"} {
		if !tableExists(t, s.db, tb) {
			t.Errorf("table %s missing", tb)
		}
	}
}

func TestReopenKeepsData(t *testing.T) {
	s, path := openTestStore(t)
	if err := s.Commit("s1", SessionState{Segment: 1, SegBytes: 10, NextTurnNo: 2}, []TurnRow{{TurnID: "t1", TurnNo: 1, Segment: 1, EndedAt: 5}}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	st, ok, err := s2.Session("s1")
	if err != nil || !ok || st.SegBytes != 10 {
		t.Fatalf("session after reopen = %+v %v %v", st, ok, err)
	}
	if n, ok, _ := s2.TurnNo("s1", "t1"); !ok || n != 1 {
		t.Fatalf("turn no after reopen = %d %v", n, ok)
	}
}

func TestOpenStoreRefusesNewerVersion(t *testing.T) {
	s, path := openTestStore(t)
	if _, err := s.db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := OpenStore(path); err == nil {
		t.Fatal("OpenStore accepted a newer schema version")
	}
}

func TestCommitIsTransactional(t *testing.T) {
	s, _ := openTestStore(t)
	if err := s.Commit("s1", SessionState{Segment: 1, SegBytes: 10, NextTurnNo: 1}, nil); err != nil {
		t.Fatal(err)
	}
	s.failAfterSession = func() error { return errors.New("boom") }
	err := s.Commit("s1", SessionState{Segment: 2, SegBytes: 99, NextTurnNo: 5}, []TurnRow{{TurnID: "t1", TurnNo: 1, Segment: 2}})
	if err == nil {
		t.Fatal("Commit should fail")
	}
	st, _, _ := s.Session("s1")
	if st.Segment != 1 || st.SegBytes != 10 || st.NextTurnNo != 1 {
		t.Fatalf("session changed despite rollback: %+v", st)
	}
	if _, ok, _ := s.TurnNo("s1", "t1"); ok {
		t.Fatal("turn row survived rollback")
	}
}

func TestCommitKeepsExistingTurnNo(t *testing.T) {
	s, _ := openTestStore(t)
	st := SessionState{Segment: 1, NextTurnNo: 2}
	if err := s.Commit("s1", st, []TurnRow{{TurnID: "t1", TurnNo: 1, Segment: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit("s1", st, []TurnRow{{TurnID: "t1", TurnNo: 7, Segment: 1}}); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := s.TurnNo("s1", "t1"); n != 1 {
		t.Fatalf("turn_no = %d, want 1", n)
	}
}

func TestResetForRebuildKeepsTurns(t *testing.T) {
	s, _ := openTestStore(t)
	if err := s.Commit("s1", SessionState{Segment: 1, NextTurnNo: 3}, []TurnRow{{TurnID: "a", TurnNo: 1}, {TurnID: "b", TurnNo: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetForRebuild("s1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Session("s1"); ok {
		t.Fatal("session still present")
	}
	if n, ok, _ := s.TurnNo("s1", "b"); !ok || n != 2 {
		t.Fatalf("TurnNo b = %d %v", n, ok)
	}
	m, err := s.TurnNos("s1")
	if err != nil || len(m) != 2 || m["a"] != 1 {
		t.Fatalf("TurnNos = %v %v", m, err)
	}
}

func TestStoreFilePermissions(t *testing.T) {
	s, path := openTestStore(t)
	if err := s.Commit("s1", SessionState{Segment: 1, NextTurnNo: 1}, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", p, fi.Mode().Perm())
		}
	}
}

// A file left with wider permissions (an older build, a copy) is restricted again when it is opened.
func TestOpenStoreRestrictsAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dialogue.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil { // WriteFile's mode is cut by the umask
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}
