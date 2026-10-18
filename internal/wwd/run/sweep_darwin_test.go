//go:build darwin

package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSweepHelper is not a test: the job commands below re-run this binary to get a process that is not a system binary (the
// kernel does not show the environment of those, so the sweep could never find them) and that leaves the job's process group
// the way tmux -L servers, vite and playwright do.
//
//	SWEEP_HELPER=parent        starts a detached child (own session) and exits
//	SWEEP_HELPER=parent-nomark the same, but the child's environment loses WWD_JOB
//	SWEEP_HELPER=child         writes its pid to SWEEP_PIDFILE and sleeps
func TestSweepHelper(t *testing.T) {
	switch os.Getenv("SWEEP_HELPER") {
	case "":
		t.Skip("helper for the sweep tests")
	case "parent", "parent-nomark":
		env := os.Environ()
		if os.Getenv("SWEEP_HELPER") == "parent-nomark" {
			kept := env[:0:0]
			for _, kv := range env {
				if !strings.HasPrefix(kv, "WWD_JOB=") {
					kept = append(kept, kv)
				}
			}
			env = kept
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestSweepHelper$")
		cmd.Env = append(env, "SWEEP_HELPER=child")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	case "child":
		if err := os.WriteFile(os.Getenv("SWEEP_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(40 * time.Second)
	}
}

func waitPid(t *testing.T, pidFile string) int {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
	}
	t.Fatal("the escaped process never wrote its pid")
	return 0
}

func dead(pid int) bool {
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
	}
	return false
}

// runWithEscapee runs `<this binary> as helper; <wrapper>` as the job's command and returns the result and the escapee's pid.
func runWithEscapee(t *testing.T, helper, wrapper string, mutate func(*Spec), ctx context.Context) (Result, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pid")
	s := spec(t, "sh", "-c", `"$0" -test.run='^TestSweepHelper$' >/dev/null 2>&1; `+wrapper, os.Args[0])
	s.TaskEnv = []string{"SWEEP_HELPER=" + helper, "SWEEP_PIDFILE=" + pidFile}
	s.KillAfter = 500 * time.Millisecond
	if mutate != nil {
		mutate(&s)
	}
	r := Run(ctx, s)
	pid := waitPid(t, pidFile)
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	return r, pid
}

func TestRealSweepAfterNormalExit(t *testing.T) {
	r, pid := runWithEscapee(t, "parent", "sleep 1", nil, context.Background())
	if r.Outcome != Exited || r.ExitCode != 0 {
		t.Fatalf("result = %+v", r)
	}
	if !dead(pid) {
		t.Fatalf("the detached process %d survived a normal exit", pid)
	}
	if len(r.Escaped) != 1 || r.Escaped[0] != pid {
		t.Fatalf("escaped = %v, want [%d] (sweep error: %v)", r.Escaped, pid, r.SweepErr)
	}
	if !strings.Contains(string(r.Tail), "leaks processes") {
		t.Fatalf("the log does not say the task leaks: %q", r.Tail)
	}
}

func TestRealSweepAfterTimeout(t *testing.T) {
	r, pid := runWithEscapee(t, "parent", "sleep 30", func(s *Spec) { s.Timeout = 1500 * time.Millisecond }, context.Background())
	if r.Outcome != TimedOut {
		t.Fatalf("result = %+v", r)
	}
	if !dead(pid) {
		t.Fatalf("the detached process %d survived a timeout", pid)
	}
	if len(r.Escaped) != 1 {
		t.Fatalf("escaped = %v (sweep error: %v)", r.Escaped, r.SweepErr)
	}
}

func TestRealSweepAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(1500 * time.Millisecond); cancel() }()
	r, pid := runWithEscapee(t, "parent", "sleep 30", nil, ctx)
	if r.Outcome != Cancelled {
		t.Fatalf("result = %+v", r)
	}
	if !dead(pid) {
		t.Fatalf("the detached process %d survived a cancel", pid)
	}
	if len(r.Escaped) != 1 {
		t.Fatalf("escaped = %v (sweep error: %v)", r.Escaped, r.SweepErr)
	}
}

// KNOWN LIMIT, recorded as a test of what happens today: a process that drops WWD_JOB from its environment is invisible to the
// sweep and survives. The sweep is cleanup for well-behaved leaks (tmux -L servers, vite, playwright keep the mark), not a
// security boundary; the threat model does not include a repo's own declared task hiding from it (lead, 10-18). Likewise a
// system binary under SIP (/bin/sleep and the like) shows no environment to the kernel and cannot be found by its mark.
func TestKnownLimitAProcessThatDropsTheMarkIsNotSwept(t *testing.T) {
	r, pid := runWithEscapee(t, "parent-nomark", "sleep 1", nil, context.Background())
	if len(r.Escaped) != 0 {
		t.Fatalf("a process without the mark was found: %v", r.Escaped)
	}
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("the process without the mark is gone: the known limit no longer holds, update this test and spec §5")
	}
}
