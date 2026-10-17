package gate

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- X2a-2: snapshot, marks, the run on an assist host (a fake ssh runs the remote command in a temporary home; no network) ----

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), mode))
}

// workRepo is a repo with a commit, a staged change, an unstaged change, an untracked file and an ignored one.
func workRepo(t *testing.T) string {
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir+"/.gitignore", "ignored.txt\nnode_modules/\n", 0o644)
	writeFile(t, dir+"/a.txt", "a1\n", 0o644)
	writeFile(t, dir+"/sub/b.txt", "b1\n", 0o644)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	writeFile(t, dir+"/a.txt", "a2 staged\n", 0o644)
	gitIn(t, dir, "add", "a.txt")
	writeFile(t, dir+"/sub/b.txt", "b2 unstaged\n", 0o644)
	writeFile(t, dir+"/new.txt", "untracked\n", 0o644)
	writeFile(t, dir+"/ignored.txt", "ignored\n", 0o644)
	return dir
}

// The snapshot equals the working tree (staged, unstaged, untracked in; ignored out) and the caller's repo is untouched. Mutation gates:
// snapshot with the real index → the caller's index changes (red); no -A → the untracked file is missing (red).
func TestSnapshot_IsTheWorkingTreeAndTouchesNothing(t *testing.T) {
	dir := workRepo(t)
	headBefore := gitIn(t, dir, "rev-parse", "HEAD")
	statusBefore := gitIn(t, dir, "status", "--porcelain=v1")
	cachedBefore := gitIn(t, dir, "diff", "--cached")
	info, err := SnapshotTree(context.Background(), "", dir+"/sub")
	require.NoError(t, err)
	assert.Equal(t, dir, info.Root)
	assert.Equal(t, "sub", info.Rel)
	files := strings.Fields(gitIn(t, dir, "ls-tree", "-r", "--name-only", info.SHA))
	assert.ElementsMatch(t, []string{".gitignore", "a.txt", "new.txt", "sub/b.txt"}, files)
	assert.Equal(t, "a2 staged", gitIn(t, dir, "show", info.SHA+":a.txt"))
	assert.Equal(t, "b2 unstaged", gitIn(t, dir, "show", info.SHA+":sub/b.txt"))
	assert.Equal(t, headBefore, gitIn(t, dir, "rev-parse", info.SHA+"^"), "its parent is HEAD")
	assert.Equal(t, headBefore, gitIn(t, dir, "rev-parse", "HEAD"))
	assert.Equal(t, statusBefore, gitIn(t, dir, "status", "--porcelain=v1"), "the caller's index and tree are as they were")
	assert.Equal(t, cachedBefore, gitIn(t, dir, "diff", "--cached"))
}

// A repo with no commit yet is snapshotted too (no parent).
func TestSnapshot_NoCommitYet(t *testing.T) {
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir+"/x.txt", "x\n", 0o644)
	info, err := SnapshotTree(context.Background(), "", dir)
	require.NoError(t, err)
	assert.Equal(t, ".", info.Rel)
	assert.Equal(t, "x.txt", gitIn(t, dir, "ls-tree", "-r", "--name-only", info.SHA))
}

func TestSnapshot_NotARepo(t *testing.T) {
	_, err := SnapshotTree(context.Background(), "", t.TempDir())
	assert.Error(t, err)
}

// The filter takes out the gate's marks (with the sender's nonce) and nothing else, however the bytes are cut.
func TestMarkFilter(t *testing.T) {
	var out bytes.Buffer
	f := newMarkFilter(&out, "n1")
	in := "before\n" + AdmittedMark("n1") + "after\n" + AdmittedMark("other") + "\x1esome other line\n" + NotStartedMark("n1") + "tail"
	for i := 0; i < len(in); i += 3 { // three bytes at a time: a mark is cut anywhere
		end := i + 3
		if end > len(in) {
			end = len(in)
		}
		_, _ = f.Write([]byte(in[i:end]))
	}
	f.Flush()
	assert.True(t, f.sawAdmit)
	assert.True(t, f.sawNoStart)
	assert.Equal(t, "before\nafter\n"+AdmittedMark("other")+"\x1esome other line\ntail", out.String(), "a mark of another nonce is the command's own line")
}

func TestMarkFilter_AMarkInTheMiddleOfALineIsNotOne(t *testing.T) {
	var out bytes.Buffer
	f := newMarkFilter(&out, "n1")
	_, _ = f.Write([]byte("x" + AdmittedMark("n1")))
	f.Flush()
	assert.False(t, f.sawAdmit)
	assert.Equal(t, "x"+AdmittedMark("n1"), out.String())
}

// ---- the run ----

// fakeHost is a home directory standing in for the assist host, a fake ssh that runs the remote command there (as the login shell of a
// new session, like sshd), a fake `pdx gate` and a fake `pnpm`.
type fakeHost struct {
	home string
	ssh  string
	repo string
}

const fakeSSH = `#!/usr/bin/env python3
import os, re, sys
args = sys.argv[1:]
i = 0
while i < len(args) and args[i].startswith('-'):
    i += 2 if args[i] == '-o' else 1
host = args[i]
cmd = ' '.join(args[i + 1:])
if os.environ.get('FAKE_SSH_FAIL'):
    sys.exit(255)
home = os.environ['FAKE_HOME']
os.environ['HOME'] = home
os.environ['PATH'] = home + '/bin:' + os.environ['PATH']
cmd = re.sub(r'^git-(\w+)', r'git \1', cmd)
os.chdir(home)
os.setsid()
os.execvp('sh', ['sh', '-c', cmd])
`

const fakePdx = `#!/bin/sh
mode=$(cat "$HOME/fake-pdx-mode" 2>/dev/null)
[ "$1" = gate ] || exit 2
shift
while [ "$1" != -- ]; do shift; done
shift
case "$mode" in
  wait75) echo "pdx gate: not admitted within 60s: the command was not run" >&2; exit 75;;
  nostart) printf '\036pdx-gate-admitted:%s\n' "$PDX_GATE_ADMITTED_MARK" >&2; printf '\036pdx-gate-not-started:%s\n' "$PDX_GATE_ADMITTED_MARK" >&2; exit 126;;
esac
printf '\036pdx-gate-admitted:%s\n' "$PDX_GATE_ADMITTED_MARK" >&2
echo "$PDX_GATE_TAG" > "$HOME/last-tag"
exec "$@"
`

const fakePnpm = `#!/bin/sh
echo x >> "$HOME/pnpm-count"
mkdir -p node_modules && echo installed > node_modules/marker
`

func newFakeHost(t *testing.T, repo string) *fakeHost {
	t.Helper()
	home := t.TempDir()
	if r, err := filepath.EvalSymlinks(home); err == nil {
		home = r
	}
	writeFile(t, home+"/bin/ssh", fakeSSH, 0o755)
	writeFile(t, home+"/bin/pdx", fakePdx, 0o755)
	writeFile(t, home+"/bin/pnpm", fakePnpm, 0o755)
	writeFile(t, home+"/Assist/assist.env", "", 0o644)
	require.NoError(t, os.MkdirAll(home+"/Assist/repos.noindex/"+repo, 0o755))
	gitIn(t, home+"/Assist/repos.noindex/"+repo, "init", "-q", "-b", "main")
	t.Setenv("FAKE_HOME", home)
	t.Setenv("FAKE_SSH_FAIL", "")
	return &fakeHost{home: home, ssh: home + "/bin/ssh", repo: repo}
}

func (h *fakeHost) remote(rel string) string { return filepath.Join(h.home, rel) }

func (h *fakeHost) listDir(t *testing.T, rel string) []string {
	ents, _ := os.ReadDir(h.remote(rel))
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// runOn runs argv in dir on the fake host and returns the result with what was written.
func runOn(t *testing.T, g *Gate, h *fakeHost, dir string, argv ...string) (AssistResult, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	res := g.RunAssist(context.Background(), Options{Argv: argv, Dir: dir, Stdout: &out, Stderr: &errb},
		AssistRun{Host: "air26", Repo: h.repo, SSH: h.ssh, AdmitWait: 60 * time.Second})
	return res, out.String(), errb.String()
}

// The command runs on the host in the same relative directory, sees the uncommitted and untracked files, its output comes back as it is
// written without the gate's marks, and its exit code is the command's. Everything of the job is gone afterwards.
func TestRunAssist_RunsTheSnapshotThereAndCleansUp(t *testing.T) {
	g, errs := testGate(t)
	dir := workRepo(t)
	h := newFakeHost(t, filepath.Base(dir))
	res, out, errb := runOn(t, g, h, dir+"/sub", "sh", "-c", "cat b.txt ../new.txt ../a.txt; pwd; echo oops >&2; touch ranhere; exit 3")
	require.NoError(t, res.Err)
	assert.True(t, res.Started)
	assert.Equal(t, 3, res.Code)
	assert.Contains(t, out, "b2 unstaged\nuntracked\na2 staged\n")
	assert.Contains(t, out, h.home+"/Assist/tmp.noindex/"+res.Job+"/sub\n", "same relative directory, in the job's worktree")
	assert.Equal(t, "oops\n", errb, "stderr comes back, the marks do not")
	assert.NoFileExists(t, dir+"/sub/ranhere", "never run on this host")
	assert.Contains(t, errs.String(), "running on air26 (assist)")
	assert.Greater(t, res.TransferS, 0.0)
	assert.Equal(t, "assist:"+res.Job, strings.TrimSpace(mustRead(t, h.remote("last-tag"))), "the host's gate was told the job")
	// cleaned: no worktree, no ref, no pid file
	assert.Empty(t, h.listDir(t, "Assist/tmp.noindex"))
	assert.Empty(t, h.listDir(t, "Assist/jobs.noindex"))
	assert.Empty(t, gitIn(t, h.remote("Assist/repos.noindex/"+h.repo), "for-each-ref", "refs/assist"))
	assert.NotContains(t, gitIn(t, h.remote("Assist/repos.noindex/"+h.repo), "worktree", "list"), res.Job)
}

func mustRead(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(b)
}

// A host that does not admit the command in time (its gate exits 75) is a failure before the start; so is an admitted command that
// could not be started; the job is cleaned either way.
func TestRunAssist_FailuresBeforeTheStart(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{{"wait75", "did not admit"}, {"nostart", "could not start"}} {
		g, _ := testGate(t)
		dir := workRepo(t)
		h := newFakeHost(t, filepath.Base(dir))
		writeFile(t, h.remote("fake-pdx-mode"), tc.mode, 0o644)
		res, _, _ := runOn(t, g, h, dir, "sh", "-c", "touch ran")
		assert.False(t, res.Started, tc.mode)
		require.Error(t, res.Err, tc.mode)
		assert.Contains(t, res.Err.Error(), tc.want)
		assert.Empty(t, h.listDir(t, "Assist/tmp.noindex"), tc.mode)
		assert.Empty(t, gitIn(t, h.remote("Assist/repos.noindex/"+h.repo), "for-each-ref", "refs/assist"), tc.mode)
	}
}

// ssh failing (255) and a repo that is not on the host are failures before the start, with the cause.
func TestRunAssist_SSHAndPushFailures(t *testing.T) {
	g, _ := testGate(t)
	dir := workRepo(t)
	h := newFakeHost(t, filepath.Base(dir))
	t.Setenv("FAKE_SSH_FAIL", "1")
	res, _, _ := runOn(t, g, h, dir, "true")
	assert.False(t, res.Started)
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "push to air26")

	t.Setenv("FAKE_SSH_FAIL", "")
	h2 := newFakeHost(t, "no-such-repo")
	require.NoError(t, os.RemoveAll(h2.remote("Assist/repos.noindex/no-such-repo")))
	res, _, _ = runOn(t, g, h2, dir, "true")
	assert.False(t, res.Started)
	require.Error(t, res.Err)
}

// The dependencies are installed once per lockfile and linked in; a changed lockfile installs again; only the two newest are kept.
func TestRunAssist_DependenciesAreKeyedByTheLockfile(t *testing.T) {
	g, _ := testGate(t)
	dir := workRepo(t)
	writeFile(t, dir+"/spa/pnpm-lock.yaml", "lock: 1\n", 0o644)
	h := newFakeHost(t, filepath.Base(dir))
	count := func() int { return strings.Count(mustReadOr(h.remote("pnpm-count")), "x") }
	for i := 0; i < 2; i++ {
		res, out, _ := runOn(t, g, h, dir, "sh", "-c", "cat spa/node_modules/marker")
		require.NoError(t, res.Err)
		assert.Contains(t, out, "installed")
	}
	assert.Equal(t, 1, count(), "the same lockfile is installed once")
	for _, lock := range []string{"lock: 2\n", "lock: 3\n"} {
		writeFile(t, dir+"/spa/pnpm-lock.yaml", lock, 0o644)
		time.Sleep(1100 * time.Millisecond) // mtimes of the sets differ
		res, _, _ := runOn(t, g, h, dir, "true")
		require.NoError(t, res.Err)
	}
	assert.Equal(t, 3, count())
	assert.Len(t, h.listDir(t, "Assist/deps.noindex/"+h.repo), 2, "the two newest sets stay")
}

func mustReadOr(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

// The host takes the run back (assist-off puts .reclaimed beside the pid file): the result says so, and the cleanup still ran.
func TestRunAssist_ReclaimedIsReported(t *testing.T) {
	g, _ := testGate(t)
	dir := workRepo(t)
	h := newFakeHost(t, filepath.Base(dir))
	res, _, _ := runOn(t, g, h, dir, "sh", "-c", `touch "$HOME/Assist/jobs.noindex/${PDX_GATE_TAG#assist:}.reclaimed"; exit 143`)
	assert.True(t, res.Started)
	assert.True(t, res.Reclaimed)
	assert.Empty(t, h.listDir(t, "Assist/jobs.noindex"))
}

// An interrupt ends the remote process group (not just the ssh): the command's children are gone, and the job is cleaned.
// Mutation gate: the signal path does not call the host → the sleep is still running (red).
func TestRunAssist_AnInterruptEndsTheRemoteGroup(t *testing.T) {
	g, _ := testGate(t)
	sigs := make(chan os.Signal, 1)
	g.Signals = sigs
	dir := workRepo(t)
	h := newFakeHost(t, filepath.Base(dir))
	pidFile := h.remote("sleeper.pid")
	done := make(chan AssistResult, 1)
	go func() {
		res, _, _ := runOn(t, g, h, dir, "sh", "-c", `sleep 300 & echo $! > `+pidFile+`; wait`)
		done <- res
	}()
	eventually(t, "the remote command to run", func() bool { return exists(pidFile) && len(mustReadOr(pidFile)) > 1 })
	sigs <- syscall.SIGTERM
	var res AssistResult
	select {
	case res = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not end after the interrupt")
	}
	_ = res
	pid := strings.TrimSpace(mustReadOr(pidFile))
	eventually(t, "the remote sleep to be gone", func() bool {
		return exec.Command("kill", "-0", pid).Run() != nil
	})
	assert.Empty(t, h.listDir(t, "Assist/tmp.noindex"))
}
