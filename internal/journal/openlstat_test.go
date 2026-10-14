package journal

import (
	"os"
	"path/filepath"
	"testing"
)

// openLstatChecked is how a platform without O_NOFOLLOW opens a month file: refuse what Lstat says is not a regular file
// (a symlink is not followed), and refuse a file that is not the one Lstat looked at (swapped between the check and the open).
// Mutation gate: skip the Lstat check -> a symlink is followed (red); skip the SameFile check -> a file swapped in after the
// check is read (red).
func TestOpenLstatChecked(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "2026-11.md")
	if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openLstatChecked(regular)
	if err != nil {
		t.Fatalf("a regular file: %v", err)
	}
	f.Close()

	link := filepath.Join(dir, "2026-10.md")
	if err := os.Symlink(regular, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if f, err := openLstatChecked(link); err == nil {
		f.Close()
		t.Fatal("a symlink was opened")
	}
	if f, err := openLstatChecked(dir); err == nil {
		f.Close()
		t.Fatal("a directory was opened")
	}
	if f, err := openLstatChecked(filepath.Join(dir, "missing.md")); err == nil {
		f.Close()
		t.Fatal("a missing file was opened")
	}

	// a different file takes the path between the check and the open
	other := filepath.Join(dir, "other.md")
	if err := os.WriteFile(other, []byte("not the one that was checked"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := afterLstatHook
	afterLstatHook = func() {
		if err := os.Rename(other, regular); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterLstatHook = old })
	if f, err := openLstatChecked(regular); err == nil {
		f.Close()
		t.Fatal("a file swapped in after the check was opened")
	}
}
