package submit

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Request.Commit takes the snapshot at that commit: the working tree is not looked at (#3658; the qualification pair runs the commit a
// manifest_ref names, the same one its manifest was read from).
func TestPrepareAtACommitIgnoresTheWorkingTree(t *testing.T) {
	dir := newRepo(t)
	commit := gitT(t, dir, "rev-parse", "HEAD")
	write(t, filepath.Join(dir, ".wwd/tasks.toml"), strings.Replace(testManifest, `timeout = "10m"`, `timeout = "45m"`, 1)) // uncommitted
	write(t, filepath.Join(dir, "spa/src/untracked.test.ts"), "x\n")
	p, err := Prepare(context.Background(), Request{Dir: dir, Task: "go-changed", Args: []string{"./a"}, Commit: commit})
	if err != nil {
		t.Fatal(err)
	}
	if p.Body.Snapshot.Commit != commit {
		t.Fatalf("snapshot commit = %s, want the named %s", p.Body.Snapshot.Commit, commit)
	}
	if p.Body.TimeoutS != 600 {
		t.Fatalf("timeout_s = %d: the manifest must be the commit's (600), not the working tree's", p.Body.TimeoutS)
	}
	if p.Body.Snapshot.Tree != gitT(t, dir, "rev-parse", commit+"^{tree}") {
		t.Fatalf("tree = %s", p.Body.Snapshot.Tree)
	}
}

func TestPrepareAtACommitChecksFilesAgainstThatCommit(t *testing.T) {
	dir := newRepo(t)
	commit := gitT(t, dir, "rev-parse", "HEAD")
	write(t, filepath.Join(dir, "spa/src/new.test.ts"), "untracked\n")
	if _, err := Prepare(context.Background(), Request{Dir: dir, Task: "spa-file", Args: []string{"src/new.test.ts"}, Commit: commit}); err == nil {
		t.Fatal("a file that is only in the working tree must not pass at a commit")
	}
	if _, err := Prepare(context.Background(), Request{Dir: dir, Task: "spa-file", Args: []string{"src/a.test.ts"}, Commit: commit}); err != nil {
		t.Fatalf("a file of the commit passes: %v", err)
	}
}

func TestPrepareAtAnUnknownCommitFails(t *testing.T) {
	dir := newRepo(t)
	if _, err := Prepare(context.Background(), Request{Dir: dir, Task: "lint", Commit: strings.Repeat("0", 40)}); err == nil {
		t.Fatal("a commit that is not in the repository must be refused")
	}
}
