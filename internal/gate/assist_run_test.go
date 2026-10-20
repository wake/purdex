package gate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/snapshot"
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
	info, err := snapshot.SnapshotTree(context.Background(), "", dir+"/sub")
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
	info, err := snapshot.SnapshotTree(context.Background(), "", dir)
	require.NoError(t, err)
	assert.Equal(t, ".", info.Rel)
	assert.Equal(t, "x.txt", gitIn(t, dir, "ls-tree", "-r", "--name-only", info.SHA))
}

func TestSnapshot_NotARepo(t *testing.T) {
	_, err := snapshot.SnapshotTree(context.Background(), "", t.TempDir())
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

// assistHost is a home directory standing in for the assist host, a fake ssh that runs the remote command there (as the login shell of a
// new session, like sshd), a fake `pdx gate` and a fake `pnpm`.
type assistHost struct {
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
open(os.environ['FAKE_HOME'] + '/ssh-hosts', 'a').write(host + '\n')
cmd = ' '.join(args[i + 1:])
if os.environ.get('FAKE_SSH_FAIL'):
    sys.exit(255)
import time
home = os.environ['FAKE_HOME']
os.environ['HOME'] = home
os.environ['PATH'] = home + '/bin:' + os.environ['PATH']
cmd = re.sub(r'^git-(\w+)', r'git \1', cmd)
if os.path.exists(home + '/cleanup-fails') and 'worktree remove' in cmd:
    sys.exit(1)
os.chdir(home)
pid = os.fork()
if pid == 0:
    os.setsid()  # as sshd gives each remote command a session of its own
    os.execvp('sh', ['sh', '-c', cmd])
if os.path.exists(home + '/ssh-drop') and 'pdx gate' in cmd:
    # the connection goes once the remote command has done what the test waits for (the file named in ssh-drop), not after a
    # fixed time: a loaded host starts the command late
    want = open(home + '/ssh-drop').read().strip()
    t0 = time.time()
    while want and not os.path.exists(want) and time.time() - t0 < 60:
        time.sleep(0.05)
    sys.exit(255)  # the connection goes while the remote command goes on
_, st = os.waitpid(pid, 0)
sys.exit(os.waitstatus_to_exitcode(st) % 256 if os.waitstatus_to_exitcode(st) >= 0 else 128 - os.waitstatus_to_exitcode(st))
`

const fakePdx = `#!/bin/sh
mode=$(cat "$HOME/fake-pdx-mode" 2>/dev/null)
[ "$1" = gate ] || exit 2
if [ "$2" = would-wait ]; then [ -e "$HOME/fake-wouldwait-sleep" ] && sleep 20; cat "$HOME/fake-wouldwait"; exit 0; fi
shift
while [ "$1" != -- ]; do shift; done
shift
case "$mode" in
  wait75) echo "pdx gate: not admitted within 60s: the command was not run" >&2; exit 75;;
  slowmark) sleep 3;;
  nostart) printf '\036pdx-gate-admitted:%s\n' "$PDX_GATE_ADMITTED_MARK" >&2; printf '\036pdx-gate-not-started:%s\n' "$PDX_GATE_ADMITTED_MARK" >&2; exit 126;;
esac
printf '\036pdx-gate-admitted:%s\n' "$PDX_GATE_ADMITTED_MARK" >&2
echo "$PDX_GATE_TAG" > "$HOME/last-tag"
exec "$@"
`

// fakeGit passes everything to the real git, except update-ref while the test has asked for it to fail.
const fakeGit = `#!/bin/sh
if [ -e "$HOME/git-add-slow" ]; then for a in "$@"; do [ "$a" = add ] && { touch "$HOME/prepare-started"; sleep 6; }; done; fi
if [ -e "$HOME/git-update-ref-fails" ]; then for a in "$@"; do [ "$a" = update-ref ] && exit 1; done; fi
exec "$REAL_GIT" "$@"
`

const fakePnpm = `#!/bin/sh
echo x >> "$HOME/pnpm-count"
sleep 1
mkdir -p node_modules && echo installed > node_modules/marker
`

func newAssistHost(t *testing.T, repo string) *assistHost {
	t.Helper()
	home := t.TempDir()
	if r, err := filepath.EvalSymlinks(home); err == nil {
		home = r
	}
	writeFile(t, home+"/bin/ssh", fakeSSH, 0o755)
	writeFile(t, home+"/bin/pdx", fakePdx, 0o755)
	writeFile(t, home+"/bin/pnpm", fakePnpm, 0o755)
	writeFile(t, home+"/bin/git", fakeGit, 0o755)
	// what the probe reads (X2b): a macOS host that is idle, its gate free
	writeFile(t, home+"/bin/uname", "#!/bin/sh\necho Darwin\n", 0o755)
	writeFile(t, home+"/bin/sysctl", "#!/bin/sh\ncase \"$2\" in vm.loadavg) cat \"$HOME/fake-loadavg\";; hw.ncpu) echo 10;; esac\n", 0o755)
	writeFile(t, home+"/bin/top", "#!/bin/sh\necho \"CPU usage: 3.0% user, 4.0% sys, $(cat \"$HOME/fake-idle\")% idle\"\n", 0o755)
	writeFile(t, home+"/fake-wouldwait", `{"would_wait":false,"class":"heavy","ahead":0}`, 0o644)
	writeFile(t, home+"/fake-loadavg", "{ 0.50 0.50 0.50 }", 0o644)
	writeFile(t, home+"/fake-idle", "90.0", 0o644)
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	t.Setenv("REAL_GIT", realGit)
	writeFile(t, home+"/Assist/assist.env", "", 0o644)
	require.NoError(t, os.MkdirAll(home+"/Assist/repos.noindex/"+repo, 0o755))
	gitIn(t, home+"/Assist/repos.noindex/"+repo, "init", "-q", "-b", "main")
	t.Setenv("FAKE_HOME", home)
	t.Setenv("FAKE_SSH_FAIL", "")
	return &assistHost{home: home, ssh: home + "/bin/ssh", repo: repo}
}

func (h *assistHost) remote(rel string) string { return filepath.Join(h.home, rel) }

func (h *assistHost) listDir(t *testing.T, rel string) []string {
	ents, _ := os.ReadDir(h.remote(rel))
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// runOn runs argv in dir on the fake host and returns the result with what was written.
func runOn(t *testing.T, g *Gate, h *assistHost, dir string, argv ...string) (AssistResult, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	res := g.RunAssist(context.Background(), Options{Argv: argv, Dir: dir, Stdout: &out, Stderr: &errb},
		AssistRun{Host: "air26", Repo: h.repo, SSH: h.ssh, AdmitWait: 60 * time.Second})
	if res.Err != nil {
		t.Logf("run ended with %v; stdout %q stderr %q", res.Err, out.String(), errb.String())
	}
	return res, out.String(), errb.String()
}

// The command runs on the host in the same relative directory, sees the uncommitted and untracked files, its output comes back as it is
// written without the gate's marks, and its exit code is the command's. Everything of the job is gone afterwards.
func TestRunAssist_RunsTheSnapshotThereAndCleansUp(t *testing.T) {
	g, errs := testGate(t)
	dir := workRepo(t)
	h := newAssistHost(t, filepath.Base(dir))
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
		h := newAssistHost(t, filepath.Base(dir))
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
	h := newAssistHost(t, filepath.Base(dir))
	t.Setenv("FAKE_SSH_FAIL", "1")
	res, _, _ := runOn(t, g, h, dir, "true")
	assert.False(t, res.Started)
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "push to air26")

	t.Setenv("FAKE_SSH_FAIL", "")
	h2 := newAssistHost(t, "no-such-repo")
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
	h := newAssistHost(t, filepath.Base(dir))
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
	h := newAssistHost(t, filepath.Base(dir))
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
	h := newAssistHost(t, filepath.Base(dir))
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

// A run cancelled before the host's script got as far as its pid file does not start there at all (the tombstone).
func TestRunAssist_ACancelMarkBeforeTheStartStopsTheRun(t *testing.T) {
	g, _ := testGate(t)
	dir := workRepo(t)
	h := newAssistHost(t, filepath.Base(dir))
	writeFile(t, h.remote("Assist/jobs.noindex/jobx.cancel"), "", 0o644)
	var out, errb bytes.Buffer
	res := g.RunAssist(context.Background(), Options{Argv: []string{"sh", "-c", "touch $HOME/ran"}, Dir: dir, Stdout: &out, Stderr: &errb},
		AssistRun{Host: "air26", Repo: h.repo, SSH: h.ssh, Job: "jobx"})
	assert.False(t, res.Started)
	assert.NoFileExists(t, h.remote("ran"))
	assert.Empty(t, h.listDir(t, "Assist/jobs.noindex"), "the tombstone goes with the job")
}

// The connection going while the command runs is not the command's exit: it is ended on the host before its directory is removed.
// Mutation gate: no endRemote on 255 → the sleep goes on after the directory is gone (red).
func TestRunAssist_ALostConnectionEndsTheRemoteRun(t *testing.T) {
	g, _ := testGate(t)
	dir := workRepo(t)
	h := newAssistHost(t, filepath.Base(dir))
	p1, p2, pgidFile := h.remote("sleeper1.pid"), h.remote("sleeper2.pid"), h.remote("group.id")
	writeFile(t, h.remote("ssh-drop"), pgidFile, 0o644) // dropped once the sleepers and the group are written down (the last file)
	res, _, _ := runOn(t, g, h, dir, "sh", "-c",
		`sleep 300 & echo $! > `+p1+`; sleep 301 & echo $! > `+p2+`; ps -o pgid= -p $$ > `+pgidFile+`.tmp; mv `+pgidFile+`.tmp `+pgidFile+`; wait`)
	assert.True(t, res.Started)
	assert.Equal(t, 255, res.Code)
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "connection")
	pgid := strings.TrimSpace(mustReadOr(pgidFile))
	require.NotEmpty(t, pgid, "the group was written down before the connection went")
	for _, f := range []string{p1, p2} {
		pid := strings.TrimSpace(mustReadOr(f))
		require.NotEmpty(t, pid, "both sleepers were started before the connection went")
		eventuallyFor(t, 60*time.Second, "a remote sleeper to be gone", func() bool { return exec.Command("kill", "-0", pid).Run() != nil })
	}
	eventuallyFor(t, 60*time.Second, "the remote group to be gone", func() bool { return exec.Command("kill", "-0", "--", "-"+pgid).Run() != nil })
	assert.Empty(t, h.listDir(t, "Assist/tmp.noindex"))
}

// Two jobs with the same new lockfile install it once (the second waits for the first); three different lockfiles at once do not
// remove a set that a running job links. Mutation gates: no lock → two installs (red); no spare of linked sets → the running job loses
// its modules (red).
func TestRunAssist_DependenciesAreSharedAndKeptWhileInUse(t *testing.T) {
	g, _ := testGate(t)
	dir := workRepo(t)
	writeFile(t, dir+"/spa/pnpm-lock.yaml", "lock: same\n", 0o644)
	h := newAssistHost(t, filepath.Base(dir))
	count := func() int { return strings.Count(mustReadOr(h.remote("pnpm-count")), "x") }
	results := make(chan AssistResult, 2)
	for i := 0; i < 2; i++ {
		go func() { res, _, _ := runOn(t, g, h, dir, "sh", "-c", "cat spa/node_modules/marker"); results <- res }()
	}
	for i := 0; i < 2; i++ {
		res := <-results
		require.NoError(t, res.Err)
		assert.Equal(t, 0, res.Code)
	}
	assert.Equal(t, 1, count(), "installed once for two jobs")

	// a job that runs long on this set while two newer sets come and go
	hold := h.remote("hold-a")
	done := make(chan AssistResult, 1)
	go func() {
		res, _, _ := runOn(t, g, h, dir, "sh", "-c", "touch "+h.remote("a-started")+`; while [ ! -e `+hold+` ]; do sleep 0.1; done; cat spa/node_modules/marker`)
		done <- res
	}()
	eventuallyFor(t, 60*time.Second, "job A", func() bool { return exists(h.remote("a-started")) })
	for _, lock := range []string{"lock: b\n", "lock: c\n"} {
		writeFile(t, dir+"/spa/pnpm-lock.yaml", lock, 0o644)
		time.Sleep(1100 * time.Millisecond)
		res, _, _ := runOn(t, g, h, dir, "true")
		require.NoError(t, res.Err)
	}
	writeFile(t, hold, "", 0o644)
	res := <-done
	require.NoError(t, res.Err)
	assert.Equal(t, 0, res.Code, "job A still had its modules when it read them")
}

// A cleanup that cannot run is reported, not only logged.
func TestRunAssist_ACleanupThatFailsIsReported(t *testing.T) {
	g, _ := testGate(t)
	dir := workRepo(t)
	h := newAssistHost(t, filepath.Base(dir))
	writeFile(t, h.remote("cleanup-fails"), "", 0o644) // the fake ssh refuses the cleanup script
	res, _, _ := runOn(t, g, h, dir, "true")
	assert.True(t, res.Started)
	assert.Equal(t, 0, res.Code)
	require.Error(t, res.CleanupErr)
	assert.Contains(t, res.CleanupErr.Error(), res.Job)
}

// A cleanup step that fails inside the script (the ref cannot be removed) is reported although the ssh itself worked.
func TestRunAssist_ACleanupStepThatFailsInsideTheScriptIsReported(t *testing.T) {
	g, _ := testGate(t)
	dir := workRepo(t)
	h := newAssistHost(t, filepath.Base(dir))
	writeFile(t, h.remote("git-update-ref-fails"), "", 0o644)
	res, _, _ := runOn(t, g, h, dir, "true")
	assert.True(t, res.Started)
	require.Error(t, res.CleanupErr)
}

func depsSetDir(h *assistHost, lock string) string {
	sum := sha256.Sum256([]byte(lock))
	return h.remote("Assist/deps.noindex/" + h.repo + "/spa-" + hex.EncodeToString(sum[:])[:16])
}

// An install lock whose installer is gone is taken over; one whose installer is alive is waited for (never taken over because of its
// age). Mutation gate: the liveness check removed → the live installer's lock is removed and a second install starts (red).
func TestRunAssist_AnInstallLockIsTakenOverOnlyWhenItsInstallerIsGone(t *testing.T) {
	g, _ := testGate(t)
	dir := workRepo(t)
	lock := "lock: locked\n"
	writeFile(t, dir+"/spa/pnpm-lock.yaml", lock, 0o644)
	h := newAssistHost(t, filepath.Base(dir))
	set := depsSetDir(h, lock)

	// a dead installer: its pid is of a process that has ended
	dead := exec.Command("true")
	require.NoError(t, dead.Run())
	writeFile(t, set+".lock/pid", strconv.Itoa(dead.Process.Pid), 0o644)
	res, out, _ := runOn(t, g, h, dir, "sh", "-c", "cat spa/node_modules/marker")
	require.NoError(t, res.Err)
	assert.Contains(t, out, "installed")
	assert.Equal(t, 1, strings.Count(mustReadOr(h.remote("pnpm-count")), "x"), "the job installed it itself")

	// a live installer (this test process): the job waits until the set appears, and does not install
	set2lock := "lock: live\n"
	writeFile(t, dir+"/spa/pnpm-lock.yaml", set2lock, 0o644)
	set2 := depsSetDir(h, set2lock)
	writeFile(t, set2+".lock/pid", strconv.Itoa(os.Getpid()), 0o644)
	go func() {
		time.Sleep(2500 * time.Millisecond)
		writeFile(t, set2+"/node_modules/marker", "installed by the other\n", 0o644)
		_ = os.RemoveAll(set2 + ".lock")
	}()
	res, out, _ = runOn(t, g, h, dir, "sh", "-c", "cat spa/node_modules/marker")
	require.NoError(t, res.Err)
	assert.Contains(t, out, "installed by the other")
	assert.Equal(t, 1, strings.Count(mustReadOr(h.remote("pnpm-count")), "x"), "no second install for the live installer's set")
}
