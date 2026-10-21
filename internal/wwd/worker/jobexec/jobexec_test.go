package jobexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/ident"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

const testManifest = `
[tasks.hello]
run     = "echo hello-from-the-job"
args    = "none"
summary = "plain"

[tasks.envs]
run     = "env"
args    = "none"
summary = "plain"
env     = ["FOO=fixed-by-the-task"]
timeout = "3m"

[tasks.hostenv]
run     = "env"
args    = "none"
summary = "plain"
env     = ["PLAYWRIGHT_BROWSERS_PATH"]

[tasks.dbtask]
run     = "env"
args    = "none"
summary = "plain"
db      = "per-job"

[tasks.dbover]
run     = "env"
args    = "none"
summary = "plain"
db      = "per-job"
env     = ["DB_HOST=evil.example"]

[tasks.wptask]
run     = "env"
args    = "none"
summary = "plain"
db      = "per-job"
db_env  = "wptests"

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
	write(".wwd/tasks.toml", testManifest)
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

// testRef is the job as the worker names it; jobexec does not use it yet (#3373 PR1 only passes it down).
var testRef = ident.Ref{Source: "mlab", ID: "j_test"}

func job(task string) wire.Job {
	return wire.Job{ID: "j_a1", Origin: "mlab", Task: task, Class: wire.ClassDev, Repo: wire.Repo{Name: "purdex"}, Snapshot: wire.Snapshot{Commit: "x"}, TimeoutS: 60}
}

func TestAJobFromSnapshotToCleanup(t *testing.T) {
	r, commit := repo(t)
	f := &localFetcher{repo: r, commit: commit}
	e := newExec(t, f)
	ctx := context.Background()

	ws, err := e.Materialize(ctx, testRef, job("hello"), claimID)
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

	plan, err := e.Prepare(ctx, testRef, job("hello"), ws)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary != "plain" || plan.OutDir != filepath.Join(base, "out") || plan.Timeout <= 0 || len(plan.Argv) == 0 {
		t.Fatalf("plan %+v", plan)
	}

	res := e.Run(ctx, testRef, job("hello"), ws, plan)
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
	ws, err := e.Materialize(ctx, testRef, job("sub"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	plan, err := e.Prepare(ctx, testRef, job("sub"), ws)
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
	ws, err := e.Materialize(ctx, testRef, job("nope"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	_, err = e.Prepare(ctx, testRef, job("nope"), ws)
	var rej *runner.Rejected
	if !errors.As(err, &rej) || !strings.Contains(rej.Reason, "task") {
		t.Fatalf("err = %v", err)
	}
}

func TestMaterializeWithoutAFetcherOrWhenItFailsLeavesNothing(t *testing.T) {
	e := New(Config{WorkRoot: t.TempDir(), Fetchers: func(string) Fetcher { return nil }})
	if _, err := e.Materialize(context.Background(), testRef, job("hello"), claimID); err == nil {
		t.Fatal("an origin nobody can fetch from must be an error")
	}
	bad := failFetcher{}
	e = newExec(t, bad)
	if _, err := e.Materialize(context.Background(), testRef, job("hello"), claimID); err == nil {
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
	same := "01234567" + strings.Repeat("f", 24) // the same first eight characters
	other := job("hello")
	other.Origin = "air26"
	for name, b := range map[string]string{
		"another job id":               e.JobDir(func() wire.Job { j := job("hello"); j.ID = "j_other"; return j }(), claimID),
		"another claim":                e.JobDir(job("hello"), "ffffffffffffffffffffffffffffffff"),
		"a claim with the same start":  e.JobDir(job("hello"), same),
		"the same job id on an origin": e.JobDir(other, claimID),
	} {
		if a == b || filepath.Dir(a) != filepath.Dir(b) {
			t.Errorf("%s: %s and %s must be different children of the work root", name, a, b)
		}
	}
}

func TestAHostileJobIdCannotLeaveTheWorkRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside-marker")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	e := New(Config{WorkRoot: filepath.Join(root, "jobs"), Fetchers: func(string) Fetcher { return failFetcher{} }})
	for _, id := range []string{"../../outside-marker", "j_a/../../../outside-marker", "/etc", "..", "."} {
		j := job("hello")
		j.ID = id
		dir := e.JobDir(j, claimID)
		if filepath.Dir(dir) != filepath.Join(root, "jobs") {
			t.Errorf("id %q: directory %s is not a direct child of the work root", id, dir)
		}
		_, _ = e.Materialize(context.Background(), testRef, j, claimID) // fails at the fetch and removes only what it made
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("something outside the work root was removed: %v", err)
	}
}

func TestAnExistingJobDirectoryIsLeftAloneAndNotReused(t *testing.T) {
	e := newExec(t, failFetcher{})
	base := e.JobDir(job("hello"), claimID)
	if err := os.MkdirAll(filepath.Join(base, "ws"), 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(base, "ws", "still-written")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Materialize(context.Background(), testRef, job("hello"), claimID); err == nil {
		t.Fatal("a job directory that is already there must not be taken over")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the earlier attempt's files were removed: %v", err)
	}
}

type countFetcher struct{ calls int }

func (c *countFetcher) Fetch(context.Context, wire.Job, string, string, string) (snapshot.Workspace, error) {
	c.calls++
	return snapshot.Workspace{}, errors.New("fetched")
}

func TestAnInvalidClaimIdIsRefusedBeforeAnythingIsMade(t *testing.T) {
	f := &countFetcher{}
	e := newExec(t, f)
	if _, err := e.Materialize(context.Background(), testRef, job("hello"), "../x"); err == nil || f.calls != 0 {
		t.Fatalf("err %v, fetches %d: nothing may be fetched for a claim id that is not one", err, f.calls)
	}
}

func TestTheWorkRootIsMadeWhenItIsNotThereYet(t *testing.T) {
	r, commit := repo(t)
	e := New(Config{WorkRoot: filepath.Join(t.TempDir(), "var", "jobs"), Fetchers: func(string) Fetcher { return &localFetcher{repo: r, commit: commit} }})
	ws, err := e.Materialize(context.Background(), testRef, job("hello"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
}

func TestTheClonePathReachesTheFetcher(t *testing.T) {
	r, commit := repo(t)
	f := &localFetcher{repo: r, commit: commit}
	e := New(Config{
		WorkRoot: t.TempDir(), Fetchers: func(string) Fetcher { return f },
		ClonePath: func(name string) (string, bool) { return "/clones/" + name, true },
	})
	ws, err := e.Materialize(context.Background(), testRef, job("hello"), claimID)
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
	ws, err := e.Materialize(context.Background(), testRef, job("hello"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	plan, err := e.Prepare(context.Background(), testRef, job("hello"), ws)
	if err != nil {
		t.Fatal(err)
	}
	plan.Argv = []string{"sleep", "30"}
	start := time.Now()
	res := e.Run(ctx, testRef, job("hello"), ws, plan)
	if res.Outcome != run.Cancelled || time.Since(start) > 15*time.Second {
		t.Fatalf("outcome %s after %v", res.Outcome, time.Since(start))
	}
}

func TestTheCommandSeesTheTasksEnvAndAHomeInsideTheJobDirectory(t *testing.T) {
	r, commit := repo(t)
	e := newExec(t, &localFetcher{repo: r, commit: commit})
	ctx := context.Background()
	j := job("envs")
	j.TimeoutS = 0
	ws, err := e.Materialize(ctx, testRef, j, claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	plan, err := e.Prepare(ctx, testRef, j, ws)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Timeout != 3*time.Minute {
		t.Fatalf("timeout %v: the snapshot task's own timeout rules when the job sets none", plan.Timeout)
	}
	res := e.Run(ctx, testRef, j, ws, plan)
	log, _ := os.ReadFile(res.LogPath)
	base := e.JobDir(j, claimID)
	if !strings.Contains(string(log), "fixed-by-the-task") || !strings.Contains(string(log), filepath.Join(base, ".home")) {
		t.Fatalf("log %q: want the task's env value and HOME under %s", log, base)
	}
}

func TestTheJobsTimeoutCanOnlyShortenTheTasks(t *testing.T) {
	r, commit := repo(t)
	e := newExec(t, &localFetcher{repo: r, commit: commit})
	ctx := context.Background()
	j := job("envs")
	j.TimeoutS = 5
	ws, err := e.Materialize(ctx, testRef, j, claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	plan, err := e.Prepare(ctx, testRef, j, ws)
	if err != nil {
		t.Fatal(err)
	}
	plan.Argv = []string{"sleep", "30"}
	start := time.Now()
	res := e.Run(ctx, testRef, j, ws, plan)
	if res.Outcome != run.TimedOut || time.Since(start) > 25*time.Second {
		t.Fatalf("outcome %s after %v: the plan's timeout is the command's deadline", res.Outcome, time.Since(start))
	}
}

type wrongPlaceFetcher struct {
	dir     func(want string) string
	cleaned *int
}

func (f wrongPlaceFetcher) Fetch(_ context.Context, _ wire.Job, _, _, dir string) (snapshot.Workspace, error) {
	return snapshot.Workspace{Dir: f.dir(dir), Cleanup: func() error { *f.cleaned++; return nil }}, nil
}

func TestAWorkspaceInTheWrongPlaceIsRefusedAndCleanedUp(t *testing.T) {
	root := t.TempDir()
	for name, pick := range map[string]func(string) string{
		"the work root":     func(string) string { return root },
		"the job directory": func(want string) string { return filepath.Dir(want) },
		"another place":     func(string) string { return "/tmp" },
		"inside the tree":   func(want string) string { return filepath.Join(want, "sub") },
	} {
		cleaned := 0
		e := New(Config{WorkRoot: root, Fetchers: func(string) Fetcher { return wrongPlaceFetcher{dir: pick, cleaned: &cleaned} }})
		if _, err := e.Materialize(context.Background(), testRef, job("hello"), claimID); err == nil {
			t.Errorf("%s: want an error", name)
		}
		if cleaned != 1 {
			t.Errorf("%s: the fetcher's cleanup ran %d times, want 1", name, cleaned)
		}
		if _, err := os.Stat(e.JobDir(job("hello"), claimID)); !os.IsNotExist(err) {
			t.Errorf("%s: the job directory stays: %v", name, err)
		}
	}
}

type fakeDB struct {
	mu        sync.Mutex
	created   []string
	dropped   []string
	createErr error
	dropErr   error
	dropGate  chan struct{} // when set, Drop waits for it to be closed (a database manager stuck behind its own lock)
}

func (f *fakeDB) Create(_ context.Context, id string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, id)
	return map[string]string{
		"DB_HOST": "127.0.0.1", "DB_DATABASE": "wwd_x", "DB_PASSWORD": "pw",
		"WP_TESTS_DB_NAME": "wwd_x", "WP_PHPUNIT__TABLE_PREFIX": "wptests_",
	}, nil
}

func (f *fakeDB) Drop(_ context.Context, id string) error {
	if f.dropGate != nil {
		<-f.dropGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, id)
	return f.dropErr
}

func dbExec(t *testing.T, db PerJobDB) (*Exec, string, string) {
	t.Helper()
	r, commit := repo(t)
	return New(Config{
		WorkRoot: t.TempDir(), Fetchers: func(string) Fetcher { return &localFetcher{repo: r, commit: commit} },
		ParentEnv: os.Environ(), DB: db,
	}), r, commit
}

func TestATaskThatAsksForADatabaseGetsOneOfItsOwnAndOnlyTheNamesItReads(t *testing.T) {
	db := &fakeDB{}
	e, _, _ := dbExec(t, db)
	ctx := context.Background()
	for task, want := range map[string]struct{ has, hasNot string }{
		"dbtask": {"DB_HOST=127.0.0.1", "WP_TESTS_DB_NAME"}, "wptask": {"WP_TESTS_DB_NAME=wwd_x", "DB_HOST"},
	} {
		j := job(task)
		ref := ident.Ref{Source: "air26", ID: j.ID}
		ws, err := e.Materialize(ctx, ref, j, claimID)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := e.Prepare(ctx, ref, j, ws)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(plan.TaskEnv, "\n")
		if !strings.Contains(joined, want.has) || strings.Contains(joined, want.hasNot) {
			t.Errorf("%s: env %v, want %q and not %q", task, plan.TaskEnv, want.has, want.hasNot)
		}
		if err := ws.Cleanup(); err != nil {
			t.Fatal(err)
		}
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	// the database is named by the key, "source/id", not by the id alone
	if len(db.created) != 2 || len(db.dropped) != 2 || db.created[0] != "air26/"+job("dbtask").ID || db.dropped[0] != db.created[0] {
		t.Fatalf("created %v dropped %v: the database goes with the workspace", db.created, db.dropped)
	}
}

// Two origins can send the same id: each job gets a database of its own (a Create is by key), and the clean-up of one drops its own.
func TestTwoOriginsWithOneIDGetTwoDatabases(t *testing.T) {
	db := &fakeDB{}
	e, _, _ := dbExec(t, db)
	ctx := context.Background()
	j := job("dbtask")
	var cleanups []func() error
	for _, src := range []string{"air26", "mserv"} {
		ref := ident.Ref{Source: src, ID: j.ID}
		jj := j
		jj.Origin = src // the job is named after the connection it came in on (#3386); its directory is made from that
		ws, err := e.Materialize(ctx, ref, jj, claimID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Prepare(ctx, ref, jj, ws); err != nil {
			t.Fatal(err)
		}
		cleanups = append(cleanups, ws.Cleanup)
	}
	db.mu.Lock()
	created := append([]string(nil), db.created...)
	db.mu.Unlock()
	if len(created) != 2 || created[0] == created[1] || created[0] != "air26/"+j.ID || created[1] != "mserv/"+j.ID {
		t.Fatalf("created %v: one id from two connections must be two databases", created)
	}
	if err := cleanups[1](); err != nil {
		t.Fatal(err)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.dropped) != 1 || db.dropped[0] != "mserv/"+j.ID {
		t.Fatalf("dropped %v: the clean-up of one job drops its own database only", db.dropped)
	}
}

func TestATaskWithNoDatabaseCreatesNoneAndDropsNone(t *testing.T) {
	db := &fakeDB{}
	e, _, _ := dbExec(t, db)
	ctx := context.Background()
	ws, err := e.Materialize(ctx, testRef, job("hello"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Prepare(ctx, testRef, job("hello"), ws); err != nil {
		t.Fatal(err)
	}
	_ = ws.Cleanup()
	if len(db.created) != 0 || len(db.dropped) != 0 {
		t.Fatalf("created %v dropped %v", db.created, db.dropped)
	}
}

func TestAHostWithoutADatabaseRejectsTheTask(t *testing.T) {
	e, _, _ := dbExec(t, nil)
	ctx := context.Background()
	ws, err := e.Materialize(ctx, testRef, job("dbtask"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	_, err = e.Prepare(ctx, testRef, job("dbtask"), ws)
	var rej *runner.Rejected
	if !errors.As(err, &rej) || !strings.Contains(rej.Reason, "db_unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestADatabaseThatCannotBeMadeIsTheHostsTroubleNotTheJobs(t *testing.T) {
	db := &fakeDB{createErr: errors.New("mysql down")}
	e, _, _ := dbExec(t, db)
	ctx := context.Background()
	ws, err := e.Materialize(ctx, testRef, job("dbtask"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Prepare(ctx, testRef, job("dbtask"), ws)
	var rej *runner.Rejected
	if err == nil || errors.As(err, &rej) {
		t.Fatalf("err = %v: a database that could not be made is an infrastructure failure, not a rejection", err)
	}
	_ = ws.Cleanup()
	if len(db.dropped) != 0 {
		t.Fatalf("dropped %v: nothing was made", db.dropped)
	}
}

func TestAQuarantinedWorkspaceKeepsItsDatabase(t *testing.T) {
	// the runner does not call Cleanup for a run it could not prove stopped, so nothing drops the database; the loop's sweep keeps it
	// (the job is in Held()) until the quarantine is cleared
	db := &fakeDB{}
	e, _, _ := dbExec(t, db)
	ctx := context.Background()
	ws, err := e.Materialize(ctx, testRef, job("dbtask"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Prepare(ctx, testRef, job("dbtask"), ws); err != nil {
		t.Fatal(err)
	}
	// (no Cleanup)
	if len(db.dropped) != 0 {
		t.Fatalf("dropped %v", db.dropped)
	}
}

// BuildEnv lets a later entry win, so the database's values come after the task's own env list: a manifest that lists DB_HOST (or names
// it, to take the host's) never points the job at another database.
func TestThePerJobDatabaseValuesComeAfterTheTasksOwnEnvEntries(t *testing.T) {
	db := &fakeDB{}
	e, _, _ := dbExec(t, db)
	ctx := context.Background()
	ws, err := e.Materialize(ctx, testRef, job("dbover"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	plan, err := e.Prepare(ctx, testRef, job("dbover"), ws)
	if err != nil {
		t.Fatal(err)
	}
	last := ""
	for _, kv := range plan.TaskEnv {
		if strings.HasPrefix(kv, "DB_HOST=") {
			last = kv
		}
	}
	if last != "DB_HOST=127.0.0.1" {
		t.Fatalf("the last DB_HOST entry is %q in %v: the job's own database must win over the task's env list", last, plan.TaskEnv)
	}
}

// A database that could not be dropped is reported by Cleanup (the loop's Sweeper takes it later), and the workspace goes all the same.
func TestADropThatFailsIsReportedByCleanupAndTheWorkspaceStillGoes(t *testing.T) {
	db := &fakeDB{dropErr: errors.New("mysql down")}
	e, _, _ := dbExec(t, db)
	ctx := context.Background()
	ws, err := e.Materialize(ctx, testRef, job("dbtask"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Prepare(ctx, testRef, job("dbtask"), ws); err != nil {
		t.Fatal(err)
	}
	dir := ws.Dir
	err = ws.Cleanup()
	if err == nil || !strings.Contains(err.Error(), "mysql down") {
		t.Fatalf("Cleanup err = %v, want the drop's error", err)
	}
	if _, serr := os.Stat(filepath.Dir(dir)); !os.IsNotExist(serr) {
		t.Fatalf("the workspace stays although only the drop failed: %v", serr)
	}
}

// The runner waits for Cleanup, and the manager's Drop can wait behind its own lock: Cleanup stops waiting after the drop timeout, says
// so, and still removes the workspace.
func TestACleanupDoesNotWaitForeverForADropThatIsStuck(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	db := &fakeDB{dropGate: gate}
	r, commit := repo(t)
	e := New(Config{
		WorkRoot: t.TempDir(), Fetchers: func(string) Fetcher { return &localFetcher{repo: r, commit: commit} },
		ParentEnv: os.Environ(), DB: db, DropTimeout: 100 * time.Millisecond,
	})
	ctx := context.Background()
	ws, err := e.Materialize(ctx, testRef, job("dbtask"), claimID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Prepare(ctx, testRef, job("dbtask"), ws); err != nil {
		t.Fatal(err)
	}
	dir := ws.Dir
	done := make(chan error, 1)
	go func() { done <- ws.Cleanup() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not finished") {
			t.Fatalf("Cleanup err = %v, want the drop that did not finish", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Cleanup waited for a Drop that never returns")
	}
	if _, serr := os.Stat(filepath.Dir(dir)); !os.IsNotExist(serr) {
		t.Fatalf("the workspace stays although only the drop was slow: %v", serr)
	}
}

// The command's environment carries the whole mark: the job id and the tag of the connection the job came in on.
func TestTheCommandCarriesTheJobAndOriginMark(t *testing.T) {
	r, commit := repo(t)
	e := newExec(t, &localFetcher{repo: r, commit: commit})
	ctx := context.Background()
	j := job("envs")
	ref := ident.Ref{Source: "air26", ID: j.ID}
	ws, err := e.Materialize(ctx, ref, j, claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	plan, err := e.Prepare(ctx, ref, j, ws)
	if err != nil {
		t.Fatal(err)
	}
	res := e.Run(ctx, ref, j, ws, plan)
	log, _ := os.ReadFile(res.LogPath)
	if !strings.Contains(string(log), "WWD_JOB="+j.ID+"\n") || !strings.Contains(string(log), "WWD_ORIGIN="+ident.OriginTag("air26")+"\n") {
		t.Fatalf("log %q: want WWD_JOB=%s and WWD_ORIGIN=%s", log, j.ID, ident.OriginTag("air26"))
	}
}

// The fetcher is chosen by the connection the job was claimed on (ref.Source), not by the Origin the job's own JSON carries: a peer can
// write another peer's alias there (#3373).
func TestFetcherIsChosenBySourceNotByJobOrigin(t *testing.T) {
	r, commit := repo(t)
	mine := &localFetcher{repo: r, commit: commit}
	other := &localFetcher{repo: r, commit: commit}
	fetchers := map[string]Fetcher{"air26": mine, "mserv": other}
	e := New(Config{
		WorkRoot: t.TempDir(), ParentEnv: os.Environ(),
		Fetchers:  func(source string) Fetcher { return fetchers[source] },
		ClonePath: func(string) (string, bool) { return "", false },
	})
	j := job("hello")
	j.Origin = "mserv" // what a hostile air26 writes into its job
	ws, err := e.Materialize(context.Background(), ident.Ref{Source: "air26", ID: j.ID}, j, claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	if mine.got.dir == "" || other.got.dir != "" {
		t.Fatalf("fetched from the wrong host: source's fetcher dir=%q, the Origin-named one dir=%q", mine.got.dir, other.got.dir)
	}
	// a source nobody is configured for is refused, whatever the job says its Origin is
	if _, err := e.Materialize(context.Background(), ident.Ref{Source: "nobody", ID: j.ID}, j, claimID); err == nil {
		t.Fatal("a source without a fetcher was served")
	}
}
