package transcriptpath

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSlug(t *testing.T) {
	// Fixtures are real ~/.claude/projects directory names and the cwd they
	// came from (dot, hyphen runs, worktree dirs).
	cases := map[string]string{
		"/Users/wake/Workspace/.system":                                               "-Users-wake-Workspace--system",
		"/Users/wake/Workspace/tangency/csp-plugin/.claude/worktrees/hear3-migration": "-Users-wake-Workspace-tangency-csp-plugin--claude-worktrees-hear3-migration",
		"/Users/wake/Workspace/wake/purdex":                                           "-Users-wake-Workspace-wake-purdex",
		"/tmp/a_b c":                                                                  "-tmp-a-b-c",
		"/tmp/中文":                                                                     "-tmp---",
	}
	for cwd, want := range cases {
		if got := Slug(cwd); got != want {
			t.Errorf("slug(%q) = %q, want %q", cwd, got, want)
		}
	}
}

// A path that was a safe regular file when resolved but is a symlink by the
// time it is opened must be refused, not followed.
func TestOpenRefusesSymlink(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "projects")
	outsideDir := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(root, "safe"), outsideDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	good := filepath.Join(root, "safe", "t.jsonl")
	secret := filepath.Join(outsideDir, "t.jsonl")
	for _, p := range []string{good, secret} {
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	open := func(p string) (error, bool) {
		f, err := Open(root, p)
		if f != nil {
			f.Close()
		}
		return err, f != nil
	}

	if err, ok := open(good); err != nil || !ok {
		t.Fatalf("regular file: %v", err)
	}
	if err, _ := open(filepath.Join(root, "safe", "nope.jsonl")); !errors.Is(err, ErrFileMissing) {
		t.Fatalf("missing: %v", err)
	}
	// Final component swapped for a symlink to an outside file.
	link := filepath.Join(root, "safe", "l.jsonl")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if err, ok := open(link); !errors.Is(err, ErrNoTranscript) || ok {
		t.Fatalf("final symlink: err=%v opened=%v", err, ok)
	}
	// Intermediate directory swapped for a symlink to an outside directory
	// after the path was validated: same name exists outside.
	if err := os.RemoveAll(filepath.Join(root, "safe")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, "safe")); err != nil {
		t.Fatal(err)
	}
	if err, ok := open(good); !errors.Is(err, ErrNoTranscript) || ok {
		t.Fatalf("directory symlink: err=%v opened=%v", err, ok)
	}
}

// A FIFO where a transcript should be is refused at once: the open must not wait for a writer.
func TestOpenRefusesAFIFOWithoutBlocking(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "projects")
	if err := os.MkdirAll(filepath.Join(root, "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(root, "s", "t.jsonl")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		f, err := Open(root, fifo)
		if f != nil {
			f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNoTranscript) {
			t.Fatalf("err = %v, want ErrNoTranscript", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Open blocked on a FIFO")
	}
}
