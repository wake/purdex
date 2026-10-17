package resourcesmod

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func newTestStore(t *testing.T) *resourcesStore {
	t.Helper()
	s, err := openResourcesStore(filepath.Join(t.TempDir(), "resources.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// Codex attack (medium): the lease rows name sessions, pids and tool use ids,
// and DataDir is 0755, so resources.db and its WAL sidecars must be private
// whatever the umask or the mode of a file left by an earlier run.
func TestStore_FilesArePrivate(t *testing.T) {
	check := func(t *testing.T, path string) {
		t.Helper()
		s, err := openResourcesStore(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		if err := s.InsertMinute(minuteRow{At: 60000, NCPU: 1}); err != nil { // a write, so the WAL exists
			t.Fatal(err)
		}
		for _, p := range []string{path, path + "-wal", path + "-shm"} {
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			if got := fi.Mode().Perm(); got != 0o600 {
				t.Errorf("%s mode = %o, want 600", filepath.Base(p), got)
			}
		}
	}
	t.Run("fresh file under umask 022", func(t *testing.T) {
		old := syscall.Umask(0o022)
		t.Cleanup(func() { syscall.Umask(old) })
		check(t, filepath.Join(t.TempDir(), "resources.db"))
	})
	t.Run("a loose file from an earlier run", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "resources.db")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+"-wal", nil, 0o644); err != nil {
			t.Fatal(err)
		}
		check(t, path)
	})
}

// Re-review (P1): a loose file is tightened before SQLite touches it, not
// only after a successful migration. A file that is not a database makes the
// migration fail, and it still must not stay readable.
func TestStore_LooseFileIsTightenedEvenWhenTheMigrationFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	if err := os.WriteFile(path, []byte("this is not a sqlite database, padded to be longer than a header......"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := openResourcesStore(path); err == nil {
		_ = s.Close()
		t.Fatal("a garbage file must fail to open")
	}
	for _, p := range []string{path, path + "-wal"} {
		fi, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue // SQLite may delete a stale sidecar; gone is private enough
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o after a failed open, want 600", filepath.Base(p), got)
		}
	}
}
