package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func spec(t *testing.T, argv ...string) Spec {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Spec{
		JobID: "j_test1", Argv: argv, Dir: ws, Workspace: ws,
		ParentEnv: []string{"PATH=/usr/bin:/bin", "SECRET=hunter2", "KEEP=yes"},
		OutDir:    filepath.Join(ws, ".out"), KillAfter: 300 * time.Millisecond,
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// alive says whether the process is still running. A zombie (killed, not yet collected by its parent) is gone for this purpose, but
// kill(pid, 0) still succeeds on it: on Linux /proc/<pid>/stat says which it is (#3383). macOS has no /proc and keeps the kill test.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	_, perr := os.Stat("/proc/self/stat")
	return statSaysAlive(b, err, perr == nil)
}

// statSaysAlive judges a /proc/<pid>/stat read after kill(pid, 0) succeeded. A process that is leaving can answer the read with
// ESRCH instead of ENOENT; both mean gone (#3560). hasProc says whether this host has /proc at all.
func statSaysAlive(b []byte, err error, hasProc bool) bool {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		if !hasProc {
			return true // no /proc here (macOS): the kill test stands
		}
		return false // kill(pid, 0) saw a zombie, and init collected it before this read: gone
	}
	if err != nil {
		return true
	}
	// "pid (comm) S ...": comm may hold spaces and parentheses, so the state is the field after the last ')'
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return true
	}
	return s[i+2] != 'Z'
}

func TestStatSaysAliveJudgesReadErrors(t *testing.T) {
	run := []byte("1 (sleep) S 0 0")
	for _, c := range []struct {
		name    string
		b       []byte
		err     error
		hasProc bool
		want    bool
	}{
		{"running", run, nil, true, true},
		{"zombie", []byte("1 (a b) Z 0"), nil, true, false},
		{"ENOENT with /proc", nil, &os.PathError{Op: "open", Err: syscall.ENOENT}, true, false},
		{"ENOENT without /proc", nil, &os.PathError{Op: "open", Err: syscall.ENOENT}, false, true},
		{"ESRCH is gone", nil, &os.PathError{Op: "open", Err: syscall.ESRCH}, true, false},
		{"other error is alive", nil, &os.PathError{Op: "open", Err: syscall.EACCES}, true, true},
	} {
		if got := statSaysAlive(c.b, c.err, c.hasProc); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// On every platform: a running process is alive, a waited-for one is not.
func TestAliveSeesARunningProcessEverywhere(t *testing.T) {
	if !alive(os.Getpid()) {
		t.Fatal("this process is alive")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if !alive(pid) {
		t.Fatal("a running child must count as alive")
	}
	cmd.Process.Kill()
	cmd.Wait()
	if alive(pid) {
		t.Fatal("a killed and waited-for child must not count as alive")
	}
}

func TestAliveCountsAZombieAsGone(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc: the zombie check is Linux only")
	}
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer cmd.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && alive(pid) { // it exits at once and stays a zombie: nobody has waited for it
		time.Sleep(10 * time.Millisecond)
	}
	if alive(pid) {
		t.Fatal("an exited child that was not yet waited for must not count as alive")
	}
	if syscall.Kill(pid, 0) != nil {
		t.Skip("the zombie was already collected")
	}
	if !alive(os.Getpid()) {
		t.Fatal("this process is alive")
	}
}

func TestRunExitCodeAndOutput(t *testing.T) {
	s := spec(t, "sh", "-c", "echo out; echo err >&2; exit 3")
	r := Run(context.Background(), s)
	if r.Outcome != Exited || r.ExitCode != 3 {
		t.Fatalf("result = %+v", r)
	}
	if got := readFile(t, r.StdoutPath); got != "out\n" {
		t.Fatalf("stdout = %q", got)
	}
	log := readFile(t, r.LogPath)
	if !strings.Contains(log, "out\n") || !strings.Contains(log, "err\n") {
		t.Fatalf("log = %q", log)
	}
	if string(r.Tail) != log {
		t.Fatalf("tail = %q", r.Tail)
	}
}

func TestRunSuccess(t *testing.T) {
	r := Run(context.Background(), spec(t, "true"))
	if r.Outcome != Exited || r.ExitCode != 0 || r.Signal != "" {
		t.Fatalf("result = %+v", r)
	}
}

func TestEnvironmentIsClean(t *testing.T) {
	s := spec(t, "env")
	s.TaskEnv = []string{"KEEP", "FIXED=1", "ABSENT"}
	r := Run(context.Background(), s)
	out := readFile(t, r.StdoutPath)
	for _, want := range []string{"WWD_JOB=j_test1", "KEEP=yes", "FIXED=1", "PATH=/usr/bin:/bin", "TERM=dumb",
		"HOME=" + s.Workspace + "/.home", "TMPDIR=" + s.Workspace + "/.tmp", "XDG_CACHE_HOME=" + s.Workspace + "/.cache"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("env lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"SECRET", "hunter2", "ABSENT"} {
		if strings.Contains(out, bad) {
			t.Errorf("env leaks %q:\n%s", bad, out)
		}
	}
	for _, d := range []string{".home", ".tmp", ".config", ".cache"} {
		if st, err := os.Stat(filepath.Join(s.Workspace, d)); err != nil || !st.IsDir() {
			t.Errorf("%s was not created", d)
		}
	}
}

func TestTaskEnvCannotOverrideTheFixedNames(t *testing.T) {
	for _, e := range []string{"HOME", "HOME=/root", "TMPDIR=/x", "WWD_JOB=other", "WWD_X=1", "XDG_CONFIG_HOME"} {
		s := spec(t, "true")
		s.TaskEnv = []string{e}
		if r := Run(context.Background(), s); r.Outcome != StartFailed {
			t.Errorf("%s: outcome = %s, want start_failed", e, r.Outcome)
		}
	}
}

func TestEveryProcessOfTheTreeCarriesTheJobMark(t *testing.T) {
	s := spec(t, "sh", "-c", "sh -c 'env' | grep WWD_JOB")
	// a pipeline is shell syntax, which is fine here: the test's own command, not a task's run
	r := Run(context.Background(), s)
	if got := readFile(t, r.StdoutPath); got != "WWD_JOB=j_test1\n" {
		t.Fatalf("grandchild env = %q (result %+v)", got, r)
	}
}

func TestProgramIsFoundInTheCommandsPathNotTheRunnersPath(t *testing.T) {
	bin := t.TempDir()
	tool := filepath.Join(bin, "mytool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho found\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := spec(t, "mytool")
	r := Run(context.Background(), s)
	if r.Outcome != StartFailed {
		t.Fatalf("found a tool that is not on the command's PATH: %+v", r)
	}
	s.ParentEnv = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	r = Run(context.Background(), s)
	if r.Outcome != Exited || readFile(t, r.StdoutPath) != "found\n" {
		t.Fatalf("result = %+v", r)
	}
}

func TestStartFailures(t *testing.T) {
	for name, s := range map[string]Spec{
		"not found": spec(t, "no-such-program-xyz"),
		"empty":     spec(t),
	} {
		if r := Run(context.Background(), s); r.Outcome != StartFailed || r.Err == nil {
			t.Errorf("%s: %+v", name, r)
		}
	}
	rel := spec(t, "true")
	rel.Dir = "relative/dir"
	if r := Run(context.Background(), rel); r.Outcome != StartFailed {
		t.Errorf("relative dir: %+v", r)
	}
}

// A program that is not on the PATH says so with exec.ErrNotFound (the worker holds off that one task on it, #3908); no other way not to start does.
func TestOnlyAMissingProgramIsErrNotFound(t *testing.T) {
	if r := Run(context.Background(), spec(t, "no-such-program-xyz")); !errors.Is(r.Err, exec.ErrNotFound) {
		t.Errorf("a program not on the PATH: %+v", r.Err)
	}
	rel := spec(t, "true")
	rel.Dir = "relative/dir"
	if r := Run(context.Background(), rel); r.Outcome != StartFailed || errors.Is(r.Err, exec.ErrNotFound) {
		t.Errorf("a relative working directory is not a missing program: %+v", r)
	}
}

func TestTimeoutEndsTheWholeGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	s := spec(t, "sh", "-c", "sleep 30 & echo $! > "+pidFile+"; wait")
	s.Timeout = 300 * time.Millisecond
	start := time.Now()
	r := Run(context.Background(), s)
	if r.Outcome != TimedOut {
		t.Fatalf("result = %+v", r)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(readFile(t, pidFile)))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if alive(pid) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("the grandchild survived the timeout")
	}
}

func TestTermIgnoredThenKilled(t *testing.T) {
	s := spec(t, "sh", "-c", "trap '' TERM; sleep 30; sleep 30")
	s.Timeout = 200 * time.Millisecond
	s.KillAfter = 300 * time.Millisecond
	start := time.Now()
	r := Run(context.Background(), s)
	if r.Outcome != TimedOut {
		t.Fatalf("result = %+v", r)
	}
	if d := time.Since(start); d < 400*time.Millisecond || d > 5*time.Second {
		t.Fatalf("TERM-ignoring command ended after %v, want just over timeout+killAfter", d)
	}
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	r := Run(ctx, spec(t, "sleep", "30"))
	if r.Outcome != Cancelled {
		t.Fatalf("result = %+v", r)
	}
}

func TestSignalDeathIsReported(t *testing.T) {
	r := Run(context.Background(), spec(t, "sh", "-c", "kill -KILL $$"))
	if r.Outcome != Exited || r.Signal != "killed" || r.ExitCode != -1 {
		t.Fatalf("result = %+v", r)
	}
}

func TestStrayHoldingThePipeDoesNotHoldTheJob(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	s := spec(t, "sh", "-c", "sleep 30 & echo $! > "+pidFile+"; exit 0")
	start := time.Now()
	r := Run(context.Background(), s)
	if r.Outcome != Exited || r.ExitCode != 0 {
		t.Fatalf("result = %+v", r)
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Fatalf("took %v", d)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(readFile(t, pidFile)))
	time.Sleep(100 * time.Millisecond)
	if pid > 0 && alive(pid) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("the stray child survived the job")
	}
}

func TestLogIsCappedAndTheTailKept(t *testing.T) {
	s := spec(t, "sh", "-c", "i=0; while [ $i -lt 2000 ]; do echo line-$i; i=$((i+1)); done")
	s.MaxLog = 1000
	r := Run(context.Background(), s)
	if r.Outcome != Exited || r.ExitCode != 0 || !r.LogCut {
		t.Fatalf("result = %+v", r)
	}
	log := readFile(t, r.LogPath)
	if len(log) > 1000+len(truncatedNote) || !strings.HasSuffix(log, truncatedNote) {
		t.Fatalf("log is %d bytes, tail %q", len(log), log[max(0, len(log)-80):])
	}
	if !strings.HasSuffix(strings.TrimSpace(string(r.Tail)), "line-1999") {
		t.Fatalf("the in-memory tail lost the end: %q", string(r.Tail[max(0, len(r.Tail)-40):]))
	}
}

func TestPurdexStateDirectoriesAreInsideTheWorkspace(t *testing.T) {
	s := spec(t, "env")
	r := Run(context.Background(), s)
	out := readFile(t, r.StdoutPath)
	for _, want := range []string{"PDX_GATE_DIR=" + s.Workspace + "/.pdx/gate", "PDX_CODEX_STATE_ROOT=" + s.Workspace + "/.pdx/codex"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("env lacks %q:\n%s", want, out)
		}
	}
	for _, e := range []string{"PDX_GATE_DIR=/shared", "PDX_GATE_DIR", "PDX_DEV_MODE=1", "PDX_TEST_CFG"} {
		bad := spec(t, "true")
		bad.TaskEnv = []string{e}
		if r := Run(context.Background(), bad); r.Outcome != StartFailed {
			t.Errorf("%s: outcome = %s, want start_failed", e, r.Outcome)
		}
	}
}

func TestChildThatIgnoresTermKeepsItsGraceAfterTheLeaderLeft(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	// the leader ends on TERM at once; its child ignores TERM and must live until KillAfter, then be killed
	s := spec(t, "sh", "-c", "(trap '' TERM; sleep 30) & echo $! > "+pidFile+"; wait")
	s.Timeout = 200 * time.Millisecond
	s.KillAfter = 1500 * time.Millisecond
	start := time.Now()
	r := Run(context.Background(), s)
	d := time.Since(start)
	if r.Outcome != TimedOut {
		t.Fatalf("result = %+v", r)
	}
	if d < 1500*time.Millisecond {
		t.Fatalf("the group was killed after %v, before KillAfter (%v)", d, s.KillAfter)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(readFile(t, pidFile)))
	// KILL is delivered at once but the process leaves a moment later (and on Linux sits as a zombie until init collects it): wait
	// for that before judging, or a slow host reads a child that is dying as one that survived (#3383)
	for end := time.Now().Add(3 * time.Second); pid > 0 && alive(pid) && time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond)
	}
	// judge twice, a beat apart: a process caught mid-exit may read differently from one moment to the next (#3560)
	if pid > 0 && alive(pid) && func() bool { time.Sleep(50 * time.Millisecond); return alive(pid) }() {
		st, rerr := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		kerr := syscall.Kill(pid, 0)
		t.Fatalf("the child survived KILL: pid=%d stat=%q readerr=%v kill0=%v", pid, st, rerr, kerr)
	}
}

func TestCaptureFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	c, err := newCapture(filepath.Join(dir, "log"), filepath.Join(dir, "stdout"), 0)
	if err != nil {
		t.Fatal(err)
	}
	c.log.Close() // a write to it now fails, as it would on a full disk
	n, err := c.Stdout().Write([]byte("data"))
	if n != 4 || err != nil {
		t.Fatalf("a failing disk must not stall the command: n=%d err=%v", n, err)
	}
	if _, _, cerr := c.close(); cerr == nil {
		t.Fatal("the write failure was not reported")
	}
}

func TestRunsInTheGivenDirectory(t *testing.T) {
	s := spec(t, "pwd")
	sub := filepath.Join(s.Workspace, "spa")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	s.Dir = sub
	r := Run(context.Background(), s)
	if got := strings.TrimSpace(readFile(t, r.StdoutPath)); got != sub {
		t.Fatalf("pwd = %q, want %q", got, sub)
	}
}
