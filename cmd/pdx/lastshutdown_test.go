// cmd/pdx/lastshutdown_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLastShutdown_RoundTripAndConsume(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if err := writeLastShutdown(dir, []string{"stop modules: nex: timeout"}, at); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "last-shutdown.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v; want 0600 file", st, err)
	}
	r, err := takeLastShutdown(dir)
	if err != nil || r == nil || !r.At.Equal(at) || len(r.Errors) != 1 || r.Errors[0] != "stop modules: nex: timeout" {
		t.Fatalf("take = %+v, %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "last-shutdown.json")); !os.IsNotExist(err) {
		t.Fatal("take must delete the file")
	}
	if r, err := takeLastShutdown(dir); r != nil || err != nil {
		t.Fatalf("second take = %+v, %v; want nil, nil", r, err)
	}
}

func TestLastShutdown_CorruptIsDeleted(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "last-shutdown.json"), []byte("{not json"), 0o600)
	if _, err := takeLastShutdown(dir); err == nil {
		t.Fatal("want error for a corrupt record")
	}
	if _, err := os.Stat(filepath.Join(dir, "last-shutdown.json")); !os.IsNotExist(err) {
		t.Fatal("a corrupt record must be deleted")
	}
}

// L3: an empty directory squatting on the record name is removed (never RemoveAll).
func TestLastShutdown_EmptyDirSlotRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lastShutdownFile)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if rep, err := takeLastShutdown(dir); err == nil || rep != nil {
		t.Fatalf("take = (%v, %v), want (nil, error)", rep, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("empty dir must be removed, lstat err = %v", err)
	}
}

// L3: a non-empty directory is left intact.
func TestLastShutdown_NonEmptyDirSlotLeftIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lastShutdownFile)
	keep := filepath.Join(path, "keep")
	if err := os.MkdirAll(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(keep, "data")
	if err := os.WriteFile(data, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rep, err := takeLastShutdown(dir); err == nil || rep != nil {
		t.Fatalf("take = (%v, %v), want (nil, error)", rep, err)
	}
	if b, err := os.ReadFile(data); err != nil || string(b) != "x" {
		t.Fatalf("tree must survive: %q, %v", b, err)
	}
}

// L3: a symlink at the record name is removed; its target is untouched.
func TestLastShutdown_SymlinkSlotRemovedTargetKept(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, lastShutdownFile)
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if rep, err := takeLastShutdown(dir); err == nil || rep != nil {
		t.Fatalf("take = (%v, %v), want (nil, error)", rep, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("symlink must be removed, lstat err = %v", err)
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "precious" {
		t.Fatalf("victim changed: %q, %v", b, err)
	}
}

// L2: a take that cannot consume (rename fails) publishes nothing and keeps the record.
func TestLastShutdown_UnwritableDirNotPublished(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	dir := t.TempDir()
	if err := writeLastShutdown(dir, []string{"boom"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if rep, err := takeLastShutdown(dir); err == nil || rep != nil {
		t.Fatalf("take = (%v, %v), want (nil, error)", rep, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, lastShutdownFile)); err != nil {
		t.Fatalf("record must remain: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rep, err := takeLastShutdown(dir)
	if err != nil || rep == nil || len(rep.Errors) != 1 {
		t.Fatalf("retry take = (%v, %v)", rep, err)
	}
	if rep, err := takeLastShutdown(dir); rep != nil || err != nil {
		t.Fatalf("third take = (%v, %v), want (nil, nil)", rep, err)
	}
}

// L2: a stale .consumed with no record is never reported; take leaves no .consumed behind.
func TestLastShutdown_StaleConsumedIgnored(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, lastShutdownFile+".consumed")
	body := `{"at":"2026-10-06T12:00:00Z","errors":["old"]}`
	if err := os.WriteFile(stale, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if rep, err := takeLastShutdown(dir); rep != nil || err != nil {
		t.Fatalf("take = (%v, %v), want (nil, nil)", rep, err)
	}
	// a real record overwrites the stale one and cleans up
	if err := writeLastShutdown(dir, []string{"new"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rep, err := takeLastShutdown(dir)
	if err != nil || rep == nil || rep.Errors[0] != "new" {
		t.Fatalf("take = (%v, %v)", rep, err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf(".consumed must be gone, err = %v", err)
	}
}

// L1: a symlink at the old fixed tmp name is not followed.
func TestLastShutdown_WriteIgnoresTmpSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, lastShutdownFile+".tmp")); err != nil {
		t.Fatal(err)
	}
	if err := writeLastShutdown(dir, []string{"e"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("victim overwritten: %q", b)
	}
}

// L1: a leftover 0644 tmp file does not leak its mode into the record.
func TestLastShutdown_WriteIs0600DespiteLeftoverTmp(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, lastShutdownFile+".tmp")
	if err := os.WriteFile(tmp, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(tmp, 0o644)
	if err := writeLastShutdown(dir, []string{"e"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, lastShutdownFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v; want 0600", st, err)
	}
}

// L1: a symlink at the destination is replaced, not followed.
func TestLastShutdown_WriteReplacesDestSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, lastShutdownFile)
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if err := writeLastShutdown(dir, []string{"e"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		t.Fatalf("lstat = %v, %v; want regular 0600", st, err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("victim changed: %q", b)
	}
}

// L1: success leaves no stray tmp files.
func TestLastShutdown_WriteLeavesNoTmp(t *testing.T) {
	dir := t.TempDir()
	if err := writeLastShutdown(dir, []string{"e"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	m, _ := filepath.Glob(filepath.Join(dir, "last-shutdown-*.tmp"))
	if len(m) != 0 {
		t.Fatalf("stray tmp files: %v", m)
	}
}

// K3: valid JSON with no errors is not a real record.
func TestLastShutdown_EmptyRecordIsCorrupt(t *testing.T) {
	for _, body := range []string{`{}`, `{"at":"2026-10-06T12:00:00Z","errors":[]}`, `{"errors":null}`} {
		dir := t.TempDir()
		path := filepath.Join(dir, lastShutdownFile)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if rep, err := takeLastShutdown(dir); err == nil || rep != nil {
			t.Fatalf("%s: take = (%v, %v), want (nil, error)", body, rep, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s: record must be gone, stat err = %v", body, err)
		}
	}
}
