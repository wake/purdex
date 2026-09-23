package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// residueLog collects the log lines removeLegacyDataFiles emits.
type residueLog struct{ lines []string }

func (r *residueLog) logf(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func writeResidueFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func residueExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestLegacyDataFileNames(t *testing.T) {
	want := []string{
		"sync.db", "sync.db-wal", "sync.db-shm",
		"device_state.db", "device_state.db-wal", "device_state.db-shm",
	}
	if !slices.Equal(legacyDataFiles, want) {
		t.Fatalf("legacyDataFiles = %v, want %v", legacyDataFiles, want)
	}
}

func TestRemoveLegacyDataFiles_RemovesExactlyTheLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range legacyDataFiles {
		writeResidueFile(t, filepath.Join(dir, name))
	}
	// Live databases and look-alikes that are NOT on the list must survive. (No case variants: on a
	// case-insensitive volume such as the default macOS one, SYNC.DB *is* sync.db.)
	keep := []string{
		"meta.db", "agent_events.db", "backup.db", "profiles.db",
		"sync.db.bak", "sync.db-journal", "xsync.db", "device_state.db2",
	}
	for _, name := range keep {
		writeResidueFile(t, filepath.Join(dir, name))
	}

	rec := &residueLog{}
	removeLegacyDataFiles(dir, rec.logf)

	for _, name := range legacyDataFiles {
		if residueExists(filepath.Join(dir, name)) {
			t.Errorf("%s still exists", name)
		}
	}
	for _, name := range keep {
		if !residueExists(filepath.Join(dir, name)) {
			t.Errorf("%s was removed, want kept", name)
		}
	}
	if len(rec.lines) != len(legacyDataFiles) {
		t.Fatalf("got %d log lines, want one per removed file (%d): %q", len(rec.lines), len(legacyDataFiles), rec.lines)
	}
	for i, name := range legacyDataFiles {
		if !strings.Contains(rec.lines[i], name) {
			t.Errorf("log line %d = %q, want it to name %s", i, rec.lines[i], name)
		}
	}
}

func TestRemoveLegacyDataFiles_MissingFilesAreNotErrors(t *testing.T) {
	dir := t.TempDir()
	writeResidueFile(t, filepath.Join(dir, "sync.db")) // only one of the six

	rec := &residueLog{}
	removeLegacyDataFiles(dir, rec.logf)
	if residueExists(filepath.Join(dir, "sync.db")) {
		t.Error("sync.db still exists")
	}
	if len(rec.lines) != 1 {
		t.Fatalf("log lines = %q, want exactly the one removal", rec.lines)
	}

	// Idempotent: a second run over a clean dir says nothing.
	rec2 := &residueLog{}
	removeLegacyDataFiles(dir, rec2.logf)
	if len(rec2.lines) != 0 {
		t.Fatalf("second run logged %q, want nothing", rec2.lines)
	}
}

func TestRemoveLegacyDataFiles_MissingDataDirIsQuiet(t *testing.T) {
	rec := &residueLog{}
	removeLegacyDataFiles(filepath.Join(t.TempDir(), "absent"), rec.logf)
	if len(rec.lines) != 0 {
		t.Fatalf("logged %q, want nothing", rec.lines)
	}
}

func TestRemoveLegacyDataFiles_OnlyRegularFiles(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "precious.db")
	writeResidueFile(t, target)

	// sync.db is a symlink pointing outside DataDir; device_state.db is a directory.
	if err := os.Symlink(target, filepath.Join(dir, "sync.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "device_state.db"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeResidueFile(t, filepath.Join(dir, "device_state.db", "inner"))

	rec := &residueLog{}
	removeLegacyDataFiles(dir, rec.logf)

	if !residueExists(filepath.Join(dir, "sync.db")) {
		t.Error("the sync.db symlink was removed, want it left alone")
	}
	if !residueExists(target) {
		t.Error("the symlink target outside DataDir was removed")
	}
	if !residueExists(filepath.Join(dir, "device_state.db", "inner")) {
		t.Error("the device_state.db directory (or its content) was removed")
	}
	for _, line := range rec.lines {
		if strings.HasPrefix(line, "removed") {
			t.Errorf("unexpected removal log %q", line)
		}
	}
}
