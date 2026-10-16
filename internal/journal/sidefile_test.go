package journal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A side file (tasks.md, roadmap.md) is read whole only if it is a regular file within the limit; a file that is not there
// is os.ErrNotExist, so a caller can tell "none yet" from "refused".
// Mutation gate: follow a symlink (red); no limit (red); a missing file reported as refused (red).
func TestReadFileBounded(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(good, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFileBounded(good, 100); err != nil || got != "hello" {
		t.Fatalf("a small file: %q, %v", got, err)
	}
	if _, err := ReadFileBounded(filepath.Join(dir, "missing.md"), 100); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing file: %v, want ErrNotExist", err)
	}
	big := filepath.Join(dir, "big.md")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 101)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFileBounded(big, 100); err == nil || errors.Is(err, os.ErrNotExist) || got != "" {
		t.Fatalf("a file over the limit: %q, %v", got, err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(good, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if got, err := ReadFileBounded(link, 100); err == nil || errors.Is(err, os.ErrNotExist) || got != "" {
		t.Fatalf("a symlink: %q, %v", got, err)
	}
	if got, err := ReadFileBounded(dir, 100); err == nil || got != "" {
		t.Fatalf("a directory: %q, %v", got, err)
	}
}
