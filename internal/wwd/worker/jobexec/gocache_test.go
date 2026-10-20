package jobexec

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/worker/ident"
)

// goCacheJob prepares one go job (a workspace of its own each time) and returns its task env and workspace; the workspace is cleaned up
// with the test unless the test cleans it itself.
type goCacheJob struct {
	env []string
	ws  snapshot.Workspace
}

func (r *goRig) job(t *testing.T, e *Exec, id string) goCacheJob {
	t.Helper()
	j := job("golint")
	j.ID = id
	ref := ident.Ref{Source: "mlab", ID: id}
	ws, err := e.Materialize(context.Background(), ref, j, claimID)
	require.NoError(t, err)
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			_ = ws.Cleanup()
		}
	})
	plan, err := e.Prepare(context.Background(), ref, j, ws)
	require.NoError(t, err)
	// the test cleans some workspaces itself (to show what Cleanup lets go of): it replaces Cleanup with one that notes it ran
	inner := ws.Cleanup
	ws.Cleanup = func() error { cleaned = true; return inner() }
	return goCacheJob{env: plan.TaskEnv, ws: ws}
}

func timeAfter(n int) time.Time { return time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC) }

func envValue2(env []string, name string) string {
	v := ""
	for _, kv := range env { // the later value of a name is the one the command gets
		if k, val, ok := strings.Cut(kv, "="); ok && k == name {
			v = val
		}
	}
	return v
}

// A go task shares one build cache per Go version on the host: the same directory for every job, under the deps directory, so a job
// compiles what an earlier one did. The test results in it are not reused (-count=1 in GOFLAGS, after the task's own flags and -mod=readonly):
// a cached "ok" could come from a job that saw other state (tmux, a socket, an executable), and the reuse of whole jobs is the result cache's.
func TestAGoTaskSharesOneBuildCachePerGoVersion(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	e := r.exec(t, repo, sha)

	a := r.job(t, e, "j_one")
	b := r.job(t, e, "j_two")
	cache := envValue2(a.env, "GOCACHE")
	require.NotEmpty(t, cache)
	assert.Equal(t, cache, envValue2(b.env, "GOCACHE"), "one directory for the jobs of one Go")
	assert.Equal(t, filepath.Join(r.deps, "gocache"), filepath.Dir(cache), "under the deps directory")
	assert.DirExists(t, cache)
	assert.Equal(t, "-tags=x -mod=readonly -count=1", envValue2(a.env, "GOFLAGS"), "the task's flags, the module mode, and no test cache")
	assert.NotContains(t, cache, a.ws.Dir, "not in the job's workspace, which is removed with it")
}

// Another Go is another directory (the cache of one toolchain is of no use to another and would only grow), and only the newest two are kept.
func TestTheBuildCacheIsPerGoVersionAndTheOldOnesAreDropped(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	var dirs []string
	for i, v := range []string{"go1.26.0", "go1.26.1", "go1.26.2"} {
		b, err := os.ReadFile(filepath.Join(r.bin, "go"))
		require.NoError(t, err)
		s := string(b)
		for _, old := range []string{"go1.26.0", "go1.26.1", "go1.26.2"} {
			s = strings.ReplaceAll(s, old, v)
		}
		require.NoError(t, os.WriteFile(filepath.Join(r.bin, "go"), []byte(s), 0o755))
		e := r.exec(t, repo, sha) // a new worker: it asks the go again
		jb := r.job(t, e, "j_ver"+strconv.Itoa(i))
		dirs = append(dirs, envValue2(jb.env, "GOCACHE"))
		require.NoError(t, jb.ws.Cleanup()) // a cache a job still holds is not dropped
		// the directories are told apart by modification time
		require.NoError(t, os.Chtimes(dirs[len(dirs)-1], timeAfter(len(dirs)), timeAfter(len(dirs))))
	}
	assert.NotEqual(t, dirs[0], dirs[1])
	assert.NotEqual(t, dirs[1], dirs[2])
	assert.NoDirExists(t, dirs[0], "the oldest of three is dropped")
	assert.FileExists(t, dirs[0]+".lock", "but its lock file stays: a process waiting on it would otherwise lock an inode nobody else uses")
	assert.DirExists(t, dirs[1])
	assert.DirExists(t, dirs[2])
}

// Another worker of the host that makes a newer cache and drops the oldest ones must not take the one that is just being made: it is made and
// held under one lock, so the other waits until the new one is held (R2: a job given a directory that was gone fails to start in the Linux sandbox).
func TestACacheJustMadeIsNotDroppedByAnotherWorkerBeforeItIsHeld(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	root := filepath.Join(r.deps, "gocache")
	// two caches of other Go versions, newer than anything made now, so that the one made by a is the oldest of three
	for _, k := range []string{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, k), 0o755))
		require.NoError(t, os.Chtimes(filepath.Join(root, k), timeAfter(1).AddDate(5, 0, 0), timeAfter(1).AddDate(5, 0, 0)))
	}
	a := r.exec(t, repo, sha)
	b := r.exec(t, repo, sha)
	b.goCache.versionOverride = "go version go1.99.0 darwin/arm64" // b has another go: it makes another directory and drops the oldest
	released := make(chan struct{})
	bDone := make(chan string, 1)
	a.goCache.hook = func(stage string) {
		if stage != "made" {
			return
		}
		go func() { // another worker starts here: its directory is made and the oldest dropped, only after a holds its own
			bDone <- b.prepareGoCache(context.Background(), t.TempDir())
			close(released)
		}()
		time.Sleep(300 * time.Millisecond) // long enough for b to have dropped a's directory, if nothing stopped it
	}
	dir := a.prepareGoCache(context.Background(), t.TempDir())
	require.NotEmpty(t, dir)
	<-released
	assert.DirExists(t, dir, "the directory a holds is still there")
	assert.NotEmpty(t, <-bDone)
}

// An old cache that is in use when it is first looked at is dropped by a later job once it is free: "the newest two are kept" is not lost for
// as long as the process lives (R2).
func TestAnOldCacheInUseIsDroppedByALaterJob(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	root := filepath.Join(r.deps, "gocache")
	old := filepath.Join(root, "1111111111111111")
	mid := filepath.Join(root, "2222222222222222")
	for i, d := range []string{old, mid} {
		require.NoError(t, os.MkdirAll(d, 0o755))
		require.NoError(t, os.Chtimes(d, timeAfter(i+1), timeAfter(i+1)))
	}
	held, err := os.OpenFile(old+".lock", os.O_CREATE|os.O_RDWR, 0o600) // a job of another worker is using it
	require.NoError(t, err)
	defer held.Close()
	require.NoError(t, syscall.Flock(int(held.Fd()), syscall.LOCK_SH))

	e := r.exec(t, repo, sha)
	r.job(t, e, "j_first")
	assert.DirExists(t, old, "in use: left alone")

	require.NoError(t, syscall.Flock(int(held.Fd()), syscall.LOCK_UN))
	r.job(t, e, "j_second")
	assert.NoDirExists(t, old, "free by the next job: dropped")
	assert.DirExists(t, mid)
	assert.FileExists(t, old+".lock")
}

// A task that does not declare go.sum gets no shared cache (and a host with no deps directory cannot give one).
func TestATaskWithoutGoSumGetsNoSharedBuildCache(t *testing.T) {
	r := newDepsRig(t) // withdeps: pnpm
	plan, err := r.prepare(t, r.e, "j_pnpm")
	require.NoError(t, err)
	assert.Empty(t, envValue2(plan.TaskEnv, "GOCACHE"))
	assert.NoDirExists(t, filepath.Join(r.dir, "gocache"))
}

// A job holds its cache from Prepare to Cleanup: while it runs, the cache is not emptied under it; once it is cleaned up the hold is gone.
func TestAJobHoldsTheBuildCacheUntilItIsCleanedUp(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	e := r.exec(t, repo, sha)
	a := r.job(t, e, "j_hold")
	lockPath := envValue2(a.env, "GOCACHE") + ".lock"

	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	require.NoError(t, err)
	defer f.Close()
	assert.ErrorIs(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), syscall.EWOULDBLOCK, "held while the job lives")

	require.NoError(t, a.ws.Cleanup())
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "let go at Cleanup")
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_UN))
}

// Over its bound, with nobody using it, the cache is emptied before the next job (a whole reset: a cold build is what every job costs
// today); the entries that were there are gone, the directory is there again.
func TestAnOverBoundBuildCacheIsEmptiedWhenNobodyUsesIt(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	r.cfgGoCacheMB = 1
	e := r.exec(t, repo, sha)
	first := r.job(t, e, "j_a")
	cache := envValue2(first.env, "GOCACHE")
	require.NoError(t, first.ws.Cleanup())
	big := filepath.Join(cache, "ab", "old-entry")
	require.NoError(t, os.MkdirAll(filepath.Dir(big), 0o755))
	require.NoError(t, os.WriteFile(big, make([]byte, 3<<20), 0o644)) // 3 MiB over a 1 MiB bound

	e2 := r.exec(t, repo, sha)
	second := r.job(t, e2, "j_b")
	assert.Equal(t, cache, envValue2(second.env, "GOCACHE"))
	assert.NoFileExists(t, big, "emptied")
	assert.DirExists(t, cache)
}

// While another job is using the cache it cannot be emptied, and when it cannot be trimmed either (a file that will not go) and it is over
// twice its bound, it is not given to a new job (it builds cold in its own workspace), so a busy host cannot grow it without end. (A cache
// that can be trimmed is trimmed while it is held: gocache_prune_test.go.)
func TestABusyBuildCacheThatCannotBeTrimmedOverTwiceItsBoundIsNotGivenToANewJob(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	r.cfgGoCacheMB = 1
	e := r.exec(t, repo, sha)
	holder := r.job(t, e, "j_busy")
	cache := envValue2(holder.env, "GOCACHE")
	big := filepath.Join(cache, "cd", "entry")
	require.NoError(t, os.MkdirAll(filepath.Dir(big), 0o755))
	require.NoError(t, os.WriteFile(big, make([]byte, 3<<20), 0o644)) // over 2 MiB
	old := goCacheRemoveFile
	goCacheRemoveFile = func(string) error { return os.ErrPermission }
	t.Cleanup(func() { goCacheRemoveFile = old })

	e2 := r.exec(t, repo, sha) // another worker process of the same host, so the throttle of the first does not decide
	cold := r.job(t, e2, "j_cold")
	assert.Empty(t, envValue2(cold.env, "GOCACHE"), "no shared cache: the job builds in its own workspace")
	assert.NotContains(t, strings.Join(cold.env, " "), "-count=1", "and its test results are its own")
	assert.FileExists(t, big, "and what the holder uses is left alone")

	// between one and two times the bound the cache is still given: it is emptied when it is free
	require.NoError(t, os.WriteFile(big, make([]byte, 3<<19), 0o644)) // 1.5 MiB
	e3 := r.exec(t, repo, sha)
	warm := r.job(t, e3, "j_warm")
	assert.Equal(t, cache, envValue2(warm.env, "GOCACHE"))
}

// The Linux sandbox of a go job may write its build cache (the sandbox is otherwise read-only but the workspace), and only that one.
func TestTheSandboxOfAGoJobMayWriteItsBuildCache(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	e := r.exec(t, repo, sha)
	e.cfg.Sandbox = &run.Sandbox{User: "wwd"}
	a := r.job(t, e, "j_sb")
	cache := envValue2(a.env, "GOCACHE")
	sb := e.sandboxOf(filepath.Dir(a.ws.Dir))
	require.NotNil(t, sb)
	assert.Contains(t, sb.ReadWritePaths, cache)
	assert.NotContains(t, e.cfg.Sandbox.ReadWritePaths, cache, "the worker's own sandbox is not changed")

	other := e.sandboxOf(filepath.Join(t.TempDir(), "no-go-job"))
	assert.Equal(t, e.cfg.Sandbox, other, "a job with no shared cache has the sandbox as it is")
}
