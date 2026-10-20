package jobexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/worker/ident"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

const goManifest = `
[tasks.golint]
run     = "true"
args    = "none"
summary = "plain"
deps    = ["go.sum"]
env     = ["HTTPS_PROXY=http://proxy:3128", "GIT_SSH_COMMAND=./evil", "GOFLAGS=-tags=x", "GOPROXY=https://evil.example"]

[tasks.gomulti]
run     = "true"
args    = "none"
summary = "plain"
deps    = ["go.sum", "sub/go.sum"]
`

// goRepo is a git repository with the manifest, a go.mod and a go.sum committed.
func goRepo(t *testing.T, gomod string) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	for rel, body := range map[string]string{".wwd/tasks.toml": goManifest, "go.mod": gomod, "go.sum": "example.com/m v1.0.0 h1:x=\n"} {
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

// goRig is a worker with a stub go: `go version` answers, and `go mod download` fills the module cache it was told to use and notes in the
// log what it saw (the cache, the private host list, the worker's own HOME and a secret of the worker's environment).
type goRig struct {
	e    *Exec
	bin  string
	log  string
	deps string
	// cfgGoCacheMB is the worker's bound on the shared build cache (0: the default)
	cfgGoCacheMB int
	// cfgGoCachePruneH is how long an entry may go unused before the worker removes it (0: the default, 24 hours)
	cfgGoCachePruneH int
}

func newGoRig(t *testing.T) *goRig {
	t.Helper()
	d := t.TempDir()
	rig := &goRig{bin: filepath.Join(d, "bin"), log: filepath.Join(d, "download.log"), deps: filepath.Join(d, "deps")}
	require.NoError(t, os.MkdirAll(rig.bin, 0o755))
	stub := "#!/bin/sh\ncase \"$1\" in\n  version) echo go version go1.26.0 darwin/arm64;;\n  mod)\n" +
		"    echo \"cache=$GOMODCACHE private=$GOPRIVATE flags=$GOFLAGS home=$HOME token=$GIT_TOKEN proxy=$HTTPS_PROXY ssh=$GIT_SSH_COMMAND toolchain=$GOTOOLCHAIN work=$GOWORK goproxy=$GOPROXY dir=$(pwd)\" >> " + rig.log + "\n" +
		"    mkdir -p \"$GOMODCACHE/lab.protype.tw/wake/nexen@v0.22.1\" && echo module > \"$GOMODCACHE/lab.protype.tw/wake/nexen@v0.22.1/go.mod\";;\nesac\n"
	require.NoError(t, os.WriteFile(filepath.Join(rig.bin, "go"), []byte(stub), 0o755))
	return rig
}

func (r *goRig) exec(t *testing.T, repo, sha string) *Exec {
	return New(Config{
		WorkRoot: t.TempDir(), Fetchers: func(string) Fetcher { return &localFetcher{repo: repo, commit: sha} },
		ParentEnv: []string{"PATH=" + r.bin + ":/usr/bin:/bin", "HOME=/home/worker", "GIT_TOKEN=s3cret"}, DepsDir: r.deps, DepsKeep: 2, GoCacheMaxMB: r.cfgGoCacheMB, GoCachePruneHours: r.cfgGoCachePruneH,
	})
}

func (r *goRig) downloads(t *testing.T) int {
	b, err := os.ReadFile(r.log)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(b), "\n")
}

func (r *goRig) prepare(t *testing.T, e *Exec, id string) []string {
	t.Helper()
	j := job("golint")
	j.ID = id
	ref := ident.Ref{Source: "mlab", ID: id}
	ws, err := e.Materialize(context.Background(), ref, j, claimID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.Cleanup() })
	plan, err := e.Prepare(context.Background(), ref, j, ws)
	require.NoError(t, err)
	mod, err := os.ReadFile(filepath.Join(ws.Dir, ".gomodcache", "lab.protype.tw", "wake", "nexen@v0.22.1", "go.mod"))
	require.NoError(t, err, "the module cache is in the job's workspace, downloaded or cloned")
	assert.Equal(t, "module\n", string(mod))
	assert.Contains(t, plan.TaskEnv, "GOMODCACHE="+filepath.Join(ws.Dir, ".gomodcache"))
	return plan.TaskEnv
}

// A task that lists go.sum gets the modules of the snapshot in its workspace and a Go that uses only them: the download is the worker's, outside
// the job's sandbox and with the worker's own HOME (its git and ssh configuration reach the private host); the job gets no network and no
// credentials. The next job of the same go.mod and go.sum is cloned.
func TestAGoTaskGetsTheModulesAndNoNetwork(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	e := r.exec(t, repo, sha)

	env := r.prepare(t, e, "j_one")
	assert.Equal(t, 1, r.downloads(t))
	assert.Contains(t, env, "GOFLAGS=-tags=x -mod=readonly -count=1", "the task's own flags stay, readonly is added, and with the shared build cache no test result is reused")
	assert.Contains(t, env, "GOPROXY=off")
	assert.Contains(t, env, "GOTOOLCHAIN=local")
	assert.Contains(t, env, "GOWORK=off")
	assert.NotContains(t, strings.Join(env, "\n"), "s3cret", "no credential of the worker reaches the job")

	b, _ := os.ReadFile(r.log)
	line := string(b)
	assert.Contains(t, line, "private=lab.protype.tw", "the private host is named by the worker, not left to its go env")
	assert.Contains(t, line, "home=/home/worker", "the download runs as the worker, with its own HOME")
	assert.Contains(t, line, "token=s3cret")
	assert.Contains(t, line, "flags=-modcacherw", "the cache is writable, so the workspace can be removed")
	assert.Contains(t, line, "proxy=http://proxy:3128", "a proxy setting of the task reaches the download")
	assert.Contains(t, line, "toolchain=local", "a snapshot's go.mod cannot make the unsandboxed go fetch and run another toolchain")
	assert.Contains(t, line, "work=off", "no go.work of the snapshot is followed: it is not in the key and may point out of the workspace")
	assert.Contains(t, line, "goproxy= ", "the task does not choose where the modules come from")
	assert.Contains(t, line, "ssh= ", "nothing else of the task's environment does: the download runs with the worker's credentials, outside the sandbox")

	r.prepare(t, e, "j_two")
	assert.Equal(t, 1, r.downloads(t), "the second job had its modules cloned")
}

// go.mod is part of the key: the same go.sum with another requirement is another entry.
func TestAnotherGoModIsAnotherEntry(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	r.prepare(t, r.exec(t, repo, sha), "j_one")
	repo2, sha2 := goRepo(t, "module x\n\ngo 1.26\n\nrequire example.com/m v1.0.0\n")
	r.prepare(t, r.exec(t, repo2, sha2), "j_two")
	assert.Equal(t, 2, r.downloads(t))
}

// Another Go is another entry: the version of go is part of the key.
func TestAnotherGoVersionIsAnotherEntry(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	r.prepare(t, r.exec(t, repo, sha), "j_one")
	b, err := os.ReadFile(filepath.Join(r.bin, "go"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(r.bin, "go"), []byte(strings.Replace(string(b), "go1.26.0", "go1.26.1", 1)), 0o755))
	r.prepare(t, r.exec(t, repo, sha), "j_two")
	assert.Equal(t, 2, r.downloads(t))
}

// One module cache serves a job, so a task with two go.sum files is turned away rather than given a cache for only one of them.
func TestATaskWithTwoGoSumFilesIsRejected(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	e := r.exec(t, repo, sha)
	j := job("gomulti")
	ref := ident.Ref{Source: "mlab", ID: j.ID}
	ws, err := e.Materialize(context.Background(), ref, j, claimID)
	require.NoError(t, err)
	defer ws.Cleanup()
	_, err = e.Prepare(context.Background(), ref, j, ws)
	var rej *runner.Rejected
	require.ErrorAs(t, err, &rej)
	assert.Contains(t, rej.Reason, "deps_one_go_sum")
	assert.Equal(t, 0, r.downloads(t))
}
