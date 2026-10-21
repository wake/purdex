package submit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An untracked symlink that points out of the repository is left out of the snapshot and the caller is told (#3841).
func TestPrepareWarnsOfUntrackedSymlinksLeftOut(t *testing.T) {
	dir := newRepo(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "node_modules")); err != nil {
		t.Fatal(err)
	}
	for _, local := range []bool{false, true} {
		p, err := Prepare(context.Background(), Request{Dir: dir, Task: "go-changed", Args: []string{"./a"}, Local: local})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(p.Note, "node_modules") {
			t.Fatalf("local=%v: note = %q, want it to name node_modules", local, p.Note)
		}
		if strings.Contains(gitT(t, dir, "ls-tree", p.Body.Snapshot.Tree), "node_modules") {
			t.Fatalf("local=%v: the symlink is in the tree", local)
		}
	}
}
