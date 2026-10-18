package submit

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// An undeclared command needs no manifest: the repo has none, the record is local, marked adhoc and declares nothing.
func TestPrepareAdHocRecordsAnUndeclaredCommandWithoutAManifest(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "a.txt"), "x\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")
	argv := []string{"go", "vet", "./..."}
	p, err := Prepare(context.Background(), Request{Dir: dir, AdHoc: argv, Submitter: wire.Submitter{Session: "s1"}})
	if err != nil {
		t.Fatal(err)
	}
	b := p.Body
	if !b.AdHoc || b.Task != "adhoc" || len(b.Args) != 3 || b.Args[1] != "vet" || b.Cacheable || b.SupersedeKey != "" || len(b.Locks) != 0 || len(b.Labels) != 0 {
		t.Fatalf("body = %+v", b)
	}
	if b.Class != wire.ClassDev || b.TimeoutS <= 0 || b.Snapshot.Commit == "" || b.Snapshot.RelCwd != "." {
		t.Fatalf("body = %+v", b)
	}
	if err := (wire.LocalRunRequest{JobSubmission: b}).Validate(); err != nil {
		t.Fatalf("not a valid local run: %v", err)
	}
	if p.Manifest != nil {
		t.Fatal("an undeclared command has no manifest task")
	}
}

func TestPrepareAdHocOnARepositoryWithoutACommitIsNoCommit(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	if _, err := Prepare(context.Background(), Request{Dir: dir, AdHoc: []string{"true"}}); !IsUsage(err) {
		t.Fatalf("err = %v", err)
	}
}
