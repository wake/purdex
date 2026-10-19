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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/ident"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

const depsManifest = `
[tasks.withdeps]
run     = "cat node_modules/pkg/index.js"
args    = "none"
summary = "plain"
deps    = ["pnpm-lock.yaml"]
`

// depsLock is a pnpm workspace's lockfile as pnpm writes it: the root, a member with dependencies (spa) and one with none (electron).
const depsLock = `lockfileVersion: '9.0'

settings:
  autoInstallPeers: true

importers:

  .:
    dependencies:
      tar:
        specifier: ^7.5.13
        version: 7.5.13

  electron: {}

  spa:
    devDependencies:
      vitest:
        specifier: ^4.1.1
        version: 4.1.1

packages:

  tar@7.5.13:
    resolution: {integrity: sha512-x}
`

// The members whose node_modules a one-lockfile install makes are read from the lockfile: the ones with dependencies of any kind, not the
// root, not an empty one, nothing that is not a plain relative path.
func TestPnpmMembersAreTheImportersWithDependencies(t *testing.T) {
	assert.Equal(t, []string{"spa"}, pnpmMembers([]byte(depsLock)))
	many := "importers:\n  .:\n    dependencies:\n      a:\n  'packages/a':\n    optionalDependencies:\n      b:\n  packages/b: {}\n  ../out:\n    dependencies:\n      c:\n  /abs:\n    dependencies:\n      d:\npackages:\n  x@1: {}\n"
	assert.Equal(t, []string{"packages/a"}, pnpmMembers([]byte(many)))
	assert.Empty(t, pnpmMembers([]byte("lockfileVersion: '9.0'\n")))
	assert.Empty(t, pnpmMembers([]byte("packages:\n  importers:\n    spa:\n      dependencies:\n        a:\n")), "importers is only the top-level key")
}

// stubTools is a directory with a pnpm and a node that behave like the real ones as far as the install goes: --version, and
// `pnpm install` makes node_modules/pkg/index.js, notes in the log that it ran (and where it was told to keep its download cache), and
// fails while the fail file exists.
type stubTools struct{ bin, log, failFile, slowFile, pnpmVersion string }

func newStubTools(t *testing.T) *stubTools {
	t.Helper()
	d := t.TempDir()
	s := &stubTools{bin: filepath.Join(d, "bin"), log: filepath.Join(d, "install.log"), failFile: filepath.Join(d, "fail"), slowFile: filepath.Join(d, "slow"), pnpmVersion: filepath.Join(d, "pnpm-version")}
	require.NoError(t, os.MkdirAll(s.bin, 0o755))
	require.NoError(t, os.WriteFile(s.pnpmVersion, []byte("9.0.0"), 0o644))
	script := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(s.bin, name), []byte("#!/bin/sh\n"+body), 0o755))
	}
	script("node", `echo v24.0.0`)
	script("pnpm", `case "$1" in
  --version) cat `+s.pnpmVersion+`;;
  install)
    echo "install store=$npm_config_store_dir dir=$(pwd)" >> `+s.log+`
    if [ -e `+s.failFile+` ]; then echo "ERR_PNPM_FETCH registry unreachable" >&2; exit 1; fi
    if [ -e `+s.slowFile+` ]; then sleep 30; fi
    mkdir -p node_modules/pkg && echo installed > node_modules/pkg/index.js
    mkdir -p spa/node_modules/dep && echo workspace > spa/node_modules/dep/index.js;;
esac
`)
	return s
}

func (s *stubTools) installs(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(s.log)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(b), "\n")
}

func (s *stubTools) env() []string { return []string{"PATH=" + s.bin + ":/usr/bin:/bin"} }

// depsRepo is a git repository with the manifest and a lockfile committed.
func depsRepo(t *testing.T, lock string) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	for rel, body := range map[string]string{".wwd/tasks.toml": depsManifest, "pnpm-lock.yaml": lock} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
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
	require.NoError(t, err)
	return dir, strings.TrimSpace(string(out))
}

type depsRig struct {
	e     *Exec
	tools *stubTools
	dir   string // the deps directory
	repo  string
	sha   string
}

func newDepsRig(t *testing.T) *depsRig {
	t.Helper()
	tools := newStubTools(t)
	r, sha := depsRepo(t, depsLock)
	rig := &depsRig{tools: tools, dir: filepath.Join(t.TempDir(), "deps"), repo: r, sha: sha}
	rig.e = rig.exec(t)
	return rig
}

// exec is a new Exec over the same directories, as after a restart.
func (r *depsRig) exec(t *testing.T) *Exec {
	return New(Config{
		WorkRoot: t.TempDir(), Fetchers: func(string) Fetcher { return &localFetcher{repo: r.repo, commit: r.sha} },
		ParentEnv: r.tools.env(), DepsDir: r.dir, DepsKeep: 2,
	})
}

// prepare runs the job's materialize and prepare (a job id of its own each time) and returns the plan or the error.
func (r *depsRig) prepare(t *testing.T, e *Exec, id string) (runner.Plan, error) {
	t.Helper()
	j := job("withdeps")
	j.ID = id
	ref := ident.Ref{Source: "mlab", ID: id}
	ws, err := e.Materialize(context.Background(), ref, j, claimID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.Cleanup() })
	plan, err := e.Prepare(context.Background(), ref, j, ws)
	if err == nil {
		res := e.Run(context.Background(), ref, j, ws, plan)
		require.Equal(t, run.Exited, res.Outcome)
		require.Equal(t, 0, res.ExitCode, "the command sees the installed dependencies: %s", res.Tail)
		assert.Contains(t, string(res.Tail), "installed")
		member, merr := os.ReadFile(filepath.Join(ws.Dir, "spa", "node_modules", "dep", "index.js"))
		require.NoError(t, merr, "the workspace member's node_modules is there too, installed or cloned")
		assert.Equal(t, "workspace\n", string(member))
	}
	return plan, err
}

// The first job of a lockfile installs, under the package manager's download cache in the deps directory; the next one is cloned.
func TestTheFirstJobInstallsAndTheNextOneIsCloned(t *testing.T) {
	r := newDepsRig(t)
	_, err := r.prepare(t, r.e, "j_one")
	require.NoError(t, err)
	assert.Equal(t, 1, r.tools.installs(t))
	b, _ := os.ReadFile(r.tools.log)
	assert.Contains(t, string(b), "store="+filepath.Join(r.dir, "pnpm-store"), "the download cache is the host's, not the job's HOME")

	_, err = r.prepare(t, r.e, "j_two")
	require.NoError(t, err)
	assert.Equal(t, 1, r.tools.installs(t), "the second job had its dependencies cloned")
}

// The same lockfile installed by other tools is another entry: a new pnpm version is not served what the old one made.
func TestAnotherToolVersionIsAnotherEntry(t *testing.T) {
	r := newDepsRig(t)
	_, err := r.prepare(t, r.e, "j_one")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(r.tools.pnpmVersion, []byte("9.1.0"), 0o644))
	_, err = r.prepare(t, r.e, "j_two")
	require.NoError(t, err)
	assert.Equal(t, 1, r.tools.installs(t), "the versions are asked once per process: the same Exec still has the old key")
	_, err = r.prepare(t, r.exec(t), "j_three") // wwd restarted, the tool was upgraded
	require.NoError(t, err)
	assert.Equal(t, 2, r.tools.installs(t))
}

// worker.deps_keep reaches the cache: with 1, the newer set replaces the older, and with the default two sets are kept.
func TestDepsKeepSaysHowManySetsStay(t *testing.T) {
	entries := func(r *depsRig) int {
		list, err := os.ReadDir(filepath.Join(r.dir, "entries"))
		require.NoError(t, err)
		n := 0
		for _, f := range list {
			if f.IsDir() && !strings.Contains(f.Name(), ".") {
				n++
			}
		}
		return n
	}
	for keep, want := range map[int]int{0: 2, 1: 1} {
		r := newDepsRig(t)
		e := r.exec(t)
		e.cfg.DepsKeep = keep
		_, err := r.prepare(t, e, "j_one")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(r.tools.pnpmVersion, []byte("9.2.0"), 0o644))
		e2 := r.exec(t) // restarted with an upgraded pnpm: another key
		e2.cfg.DepsKeep = keep
		_, err = r.prepare(t, e2, "j_two")
		require.NoError(t, err)
		assert.Equal(t, want, entries(r), "deps_keep = %d", keep)
	}
}

// An install that fails is the worker's trouble, not the job's: the job goes back. Failing twice in a row on this host ends the job (failed
// with no exit and a reason, which no outcome and no qualification counts), and the host does not try the key again until the window passes.
func TestAnInstallThatKeepsFailingEndsTheJobWithAReason(t *testing.T) {
	r := newDepsRig(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	r.e.deps.now = func() time.Time { return now }
	require.NoError(t, os.WriteFile(r.tools.failFile, nil, 0o644))

	_, err := r.prepare(t, r.e, "j_one")
	require.Error(t, err)
	var rej *runner.Rejected
	assert.False(t, errors.As(err, &rej), "the first failure: the job goes back to the queue, as the worker's trouble")
	assert.Contains(t, err.Error(), "registry unreachable", "the install's own words are in the error")

	_, err = r.prepare(t, r.e, "j_two")
	require.ErrorAs(t, err, &rej, "the second in a row ends the job")
	assert.Contains(t, rej.Reason, "deps_install_failed")
	assert.Contains(t, rej.Reason, "2 times in a row")
	assert.Equal(t, 2, r.tools.installs(t))

	require.NoError(t, os.Remove(r.tools.failFile)) // the registry is back, but the host has stopped trying for now
	_, err = r.prepare(t, r.e, "j_three")
	require.ErrorAs(t, err, &rej)
	assert.Contains(t, rej.Reason, "not tried again before")
	assert.Equal(t, 2, r.tools.installs(t), "not installed again inside the window")

	now = now.Add(depsFailWindow + time.Minute)
	_, err = r.prepare(t, r.e, "j_four")
	require.NoError(t, err, "after the window it is tried again, and works")
	assert.Equal(t, 3, r.tools.installs(t))
	_, err = r.prepare(t, r.e, "j_five")
	require.NoError(t, err, "and the failures are forgotten")
}

// A failure and then a success is not "in a row": the count starts again.
func TestAFailureFollowedByASuccessIsNotInARow(t *testing.T) {
	r := newDepsRig(t)
	require.NoError(t, os.WriteFile(r.tools.failFile, nil, 0o644))
	_, err := r.prepare(t, r.e, "j_one")
	require.Error(t, err)
	require.NoError(t, os.Remove(r.tools.failFile))
	_, err = r.prepare(t, r.e, "j_two")
	require.NoError(t, err)
	// a different lockfile is another key: it fails once without being held off by the first
	assert.Equal(t, 0, len(r.e.deps.fails), "the success cleared the key")
}

// A job that is cancelled, or a worker that is shutting down, while the install runs says nothing of the install: no failure is counted.
func TestACancelledInstallIsNotAFailure(t *testing.T) {
	r := newDepsRig(t)
	require.NoError(t, os.WriteFile(r.tools.slowFile, nil, 0o644)) // the install takes long enough to be cancelled in the middle of
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := job("withdeps")
	ref := ident.Ref{Source: "mlab", ID: j.ID}
	ws, err := r.e.Materialize(context.Background(), ref, j, claimID)
	require.NoError(t, err)
	defer ws.Cleanup()
	done := make(chan error, 1)
	go func() { _, err := r.e.Prepare(ctx, ref, j, ws); done <- err }()
	deadline := time.Now().Add(20 * time.Second)
	for r.tools.installs(t) == 0 {
		require.True(t, time.Now().Before(deadline), "the install never started")
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the cancelled install did not return")
	}
	require.Error(t, err)
	var rej *runner.Rejected
	assert.False(t, errors.As(err, &rej))
	assert.Empty(t, r.e.deps.fails, "a cancel in the middle of an install is no failure of the install")
}

// A worker with no place for the cache cannot prepare a task that declares deps: its own trouble, not a rejection of the job.
func TestAWorkerWithoutADepsDirectoryCannotPrepareDeps(t *testing.T) {
	r := newDepsRig(t)
	e := r.exec(t)
	e.cfg.DepsDir = ""
	_, err := r.prepare(t, e, "j_one")
	require.Error(t, err)
	var rej *runner.Rejected
	assert.False(t, errors.As(err, &rej))
}

// The install of a sandboxed job may write the download caches; the sandbox the job itself runs under is not changed.
func TestTheInstallsSandboxMayWriteTheDownloadCachesAndTheJobsDoesNot(t *testing.T) {
	sb := &run.Sandbox{User: "wake", ReadWritePaths: []string{"/srv/a"}}
	got := depsSandbox(sb, "/data/deps")
	assert.Equal(t, []string{"/srv/a", "/data/deps"}, got.ReadWritePaths)
	assert.Equal(t, []string{"/srv/a"}, sb.ReadWritePaths, "the job's own sandbox is untouched")
	assert.Nil(t, depsSandbox(nil, "/data/deps"))
}

// A task with no deps does not touch the cache: the directory is not even made.
func TestATaskWithoutDepsMakesNoCacheDirectory(t *testing.T) {
	r := newDepsRig(t)
	rr, commit := repo(t)
	e := New(Config{WorkRoot: t.TempDir(), Fetchers: func(string) Fetcher { return &localFetcher{repo: rr, commit: commit} }, ParentEnv: os.Environ(), DepsDir: r.dir})
	j := job("hello")
	ref := ident.Ref{Source: "mlab", ID: j.ID}
	ws, err := e.Materialize(context.Background(), ref, j, claimID)
	require.NoError(t, err)
	defer ws.Cleanup()
	_, err = e.Prepare(context.Background(), ref, j, ws)
	require.NoError(t, err)
	_, err = os.Stat(r.dir)
	assert.True(t, errors.Is(err, os.ErrNotExist), "no cache directory")
}

var _ = wire.ClassDev
