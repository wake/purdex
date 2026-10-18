package jobexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

const manifest = `
[tasks.hello]
run     = "echo hello-from-the-job"
args    = "none"
summary = "plain"

[tasks.sub]
run     = "pwd"
cwd     = "sub"
args    = "none"
summary = "plain"
`

const claimID = "0123456789abcdef0123456789abcdef"

// repo is a real git repository with the manifest committed; it returns the repository and the commit.
func repo(t *testing.T) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".wwd/tasks.toml", manifest)
	write("sub/keep", "x")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "m"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

// localFetcher checks the job's commit out of a repository on this host, as the host's own origin does.
type localFetcher struct {
	repo, commit string
	got          struct{ clone, dir string }
}

func (f *localFetcher) Fetch(ctx context.Context, job wire.Job, claimID, clone, dir string) (snapshot.Workspace, error) {
	f.got.clone, f.got.dir = clone, dir
	return snapshot.CheckoutLocal(ctx, f.repo, f.commit, dir)
}

func newExec(t *testing.T, f Fetcher) *Exec {
	t.Helper()
	return New(Config{
		WorkRoot: t.TempDir(), Fetchers: func(string) Fetcher { return f },
		ClonePath: func(string) (string, bool) { return "", false }, ParentEnv: os.Environ(),
	})
}

func job(task string) wire.Job {
	return wire.Job{ID: "j_a1", Origin: "mlab", Task: task, Class: wire.ClassDev, Repo: wire.Repo{Name: "purdex"}, Snapshot: wire.Snapshot{Commit: "x"}, TimeoutS: 60}
}

func TestAJobFromSnapshotToCleanup(t *testing.T) {
	r, commit := repo(t)
	f := &localFetcher{repo: r, commit: commit}
	e := newExec(t, f)
	ctx := context.Background()

	ws, err := e.Materialize(ctx, job("hello"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	base := e.JobDir(job("hello"), claimID)
	if ws.Dir != filepath.Join(base, "ws") || f.got.dir != ws.Dir {
		t.Fatalf("workspace %q, fetched into %q, base %q", ws.Dir, f.got.dir, base)
	}
	if _, err := os.Stat(filepath.Join(ws.Dir, ".wwd", "tasks.toml")); err != nil {
		t.Fatalf("the snapshot's tree is not in the workspace: %v", err)
	}

	plan, err := e.Prepare(ctx, job("hello"), ws)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary != "plain" || plan.OutDir != filepath.Join(base, "out") || plan.Timeout <= 0 || len(plan.Argv) == 0 {
		t.Fatalf("plan %+v", plan)
	}

	res := e.Run(ctx, job("hello"), ws, plan)
	if res.Outcome != run.Exited || res.ExitCode != 0 || res.Err != nil {
		t.Fatalf("result %+v", res)
	}
	log, err := os.ReadFile(res.LogPath)
	if err != nil || !strings.Contains(string(log), "hello-from-the-job") {
		t.Fatalf("log %q err %v", log, err)
	}
	if !strings.HasPrefix(res.LogPath, plan.OutDir) {
		t.Fatalf("the run's files are in %s, not under %s", res.LogPath, plan.OutDir)
	}

	if err := ws.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatalf("the job directory stays after cleanup: %v", err)
	}
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = r
	out, _ := cmd.Output()
	if strings.Contains(string(out), "ws") {
		t.Fatalf("the worktree entry stays in the repository:\n%s", out)
	}
}

func TestTheTaskRunsInItsOwnDirectoryOfTheSnapshot(t *testing.T) {
	r, commit := repo(t)
	e := newExec(t, &localFetcher{repo: r, commit: commit})
	ctx := context.Background()
	ws, err := e.Materialize(ctx, job("sub"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	plan, err := e.Prepare(ctx, job("sub"), ws)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(ws.Dir, "sub")) // the plan's directory is the resolved one (/var is /private/var on macOS)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Dir != want {
		t.Fatalf("dir %q, want the task's cwd inside the snapshot, %q", plan.Dir, want)
	}
}

func TestAJobThatDoesNotFitIsRejectedNotAnInfraFailure(t *testing.T) {
	r, commit := repo(t)
	e := newExec(t, &localFetcher{repo: r, commit: commit})
	ctx := context.Background()
	ws, err := e.Materialize(ctx, job("nope"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	_, err = e.Prepare(ctx, job("nope"), ws)
	var rej *runner.Rejected
	if !errors.As(err, &rej) || !strings.Contains(rej.Reason, "task") {
		t.Fatalf("err = %v", err)
	}
}

func TestMaterializeWithoutAFetcherOrWhenItFailsLeavesNothing(t *testing.T) {
	e := New(Config{WorkRoot: t.TempDir(), Fetchers: func(string) Fetcher { return nil }})
	if _, err := e.Materialize(context.Background(), job("hello"), claimID); err == nil {
		t.Fatal("an origin nobody can fetch from must be an error")
	}
	bad := failFetcher{}
	e = newExec(t, bad)
	if _, err := e.Materialize(context.Background(), job("hello"), claimID); err == nil {
		t.Fatal("want the fetch error")
	}
	if _, err := os.Stat(e.JobDir(job("hello"), claimID)); !os.IsNotExist(err) {
		t.Fatalf("a failed fetch leaves its directory behind: %v", err)
	}
}

type failFetcher struct{}

func (failFetcher) Fetch(context.Context, wire.Job, string, string, string) (snapshot.Workspace, error) {
	return snapshot.Workspace{}, errors.New("origin unreachable")
}

func TestEachAttemptHasItsOwnDirectory(t *testing.T) {
	e := newExec(t, failFetcher{})
	a := e.JobDir(job("hello"), claimID)
	b := e.JobDir(job("hello"), "ffffffffffffffffffffffffffffffff")
	if a == b || filepath.Dir(a) != filepath.Dir(b) {
		t.Fatalf("%s and %s: a later attempt must never meet what an earlier one left", a, b)
	}
}

func TestTheClonePathReachesTheFetcher(t *testing.T) {
	r, commit := repo(t)
	f := &localFetcher{repo: r, commit: commit}
	e := New(Config{
		WorkRoot: t.TempDir(), Fetchers: func(string) Fetcher { return f },
		ClonePath: func(name string) (string, bool) { return "/clones/" + name, true },
	})
	ws, err := e.Materialize(context.Background(), job("hello"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	if f.got.clone != "/clones/purdex" {
		t.Fatalf("clone %q", f.got.clone)
	}
}

func TestADeadlineEndsTheCommand(t *testing.T) {
	r, commit := repo(t)
	e := newExec(t, &localFetcher{repo: r, commit: commit})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ws, err := e.Materialize(context.Background(), job("hello"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	plan, err := e.Prepare(context.Background(), job("hello"), ws)
	if err != nil {
		t.Fatal(err)
	}
	plan.Argv = []string{"sleep", "30"}
	start := time.Now()
	res := e.Run(ctx, job("hello"), ws, plan)
	if res.Outcome != run.Cancelled || time.Since(start) > 15*time.Second {
		t.Fatalf("outcome %s after %v", res.Outcome, time.Since(start))
	}
}
