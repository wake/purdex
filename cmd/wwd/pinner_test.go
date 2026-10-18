package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The two settings a real host cannot do without are in what main builds the queue from. (The e2e tests found them missing: no
// Pinner, so no peer worker could fetch a snapshot; no log directory, so no log could be uploaded.)
func TestQueueOptionsCarryThePinnerAndTheLogDirectory(t *testing.T) {
	o := queueOptions("mlab", "/data", nil)
	if o.Pinner == nil {
		t.Fatal("no Pinner: a submission would be stored unpinned, and its snapshot could not be served")
	}
	if o.LogDir != filepath.Join("/data", "joblogs") {
		t.Fatalf("LogDir = %q", o.LogDir)
	}
	if o.Origin != "mlab" {
		t.Fatalf("Origin = %q", o.Origin)
	}
}

// A queue built from those options pins a submission's snapshot in the submitter's repository, and unpins it, and makes its log
// directory.
func TestQueueFromOptionsPinsInARealRepositoryAndMakesItsLogDirectory(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(repo, ".wwd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".wwd", "tasks.toml"), []byte("[tasks.hello]\nrun = \"/bin/echo hi\"\nargs = \"none\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "x")

	data := t.TempDir()
	st, err := store.Open(filepath.Join(data, "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := queue.Migrate(ctx, st.DB()); err != nil {
		t.Fatal(err)
	}
	q, err := queue.New(st.DB(), queueOptions("mlab", data, func(wire.JobEvent) {}))
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(data, "joblogs")); err != nil || !fi.IsDir() {
		t.Fatalf("the log directory was not made: %v", err)
	}
	prep, err := submit.Prepare(ctx, submit.Request{Dir: repo, Task: "hello", Submitter: wire.Submitter{Session: "t", Host: "mlab"}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := q.Submit(ctx, prep.Body)
	if err != nil {
		t.Fatal(err)
	}
	pin := run("rev-parse", "--verify", "refs/wwd/"+resp.ID)
	if pin != prep.Body.Snapshot.Commit {
		t.Fatalf("refs/wwd/%s = %s, want the snapshot commit %s", resp.ID, pin, prep.Body.Snapshot.Commit)
	}
	if err := (gitPinner{}).Unpin(ctx, filepath.Join(repo, ".git"), resp.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "-q", "--verify", "refs/wwd/"+resp.ID).CombinedOutput(); err == nil {
		t.Fatalf("the pin is still there: %s", out)
	}
	ids, err := (gitPinner{}).ListPins(ctx, filepath.Join(repo, ".git"))
	if err != nil || len(ids) != 0 {
		t.Fatalf("ListPins = %v %v", ids, err)
	}
}
