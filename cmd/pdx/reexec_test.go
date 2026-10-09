package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/wake/purdex/internal/claudeenv"
)

func TestCaptureReexecPlan_CopiesBootState(t *testing.T) {
	args := []string{"/opt/pdx", "serve", "--config", "/c.toml"}
	env := []string{"PDX_DEV_MODE=1", "PATH=/usr/bin"}
	p, err := captureReexecPlan(func() (string, error) { return "/opt/pdx", nil }, args, env)
	if err != nil {
		t.Fatal(err)
	}
	// A mutation after capture (nex PATH policy, locale, tmuxenv) must not reach the plan.
	env[1] = "PATH=/nex/prepend:/usr/bin"
	args[3] = "/other.toml"
	if !reflect.DeepEqual(p.env, []string{"PDX_DEV_MODE=1", "PATH=/usr/bin"}) {
		t.Fatalf("env = %v, want the boot env", p.env)
	}
	if !reflect.DeepEqual(p.argv, []string{"/opt/pdx", "serve", "--config", "/c.toml"}) {
		t.Fatalf("argv = %v, want the boot argv", p.argv)
	}
	if p.path != "/opt/pdx" {
		t.Fatalf("path = %q", p.path)
	}
}

func TestCaptureReexecPlan_ExecutableError(t *testing.T) {
	if _, err := captureReexecPlan(func() (string, error) { return "", errors.New("nope") }, nil, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestReexec_ExecsSamePathArgvEnv(t *testing.T) {
	p := &reexecPlan{path: "/opt/pdx", argv: []string{"/opt/pdx", "serve"}, env: []string{"PDX_DEV_MODE=1"}}
	var gotPath string
	var gotArgv, gotEnv []string
	exitCode := -1
	reexec(p, func(path string, argv, env []string) error {
		gotPath, gotArgv, gotEnv = path, argv, env
		return errors.New("exec format error") // a real exec does not return
	}, func(string, ...any) {}, func(c int) { exitCode = c })
	if gotPath != p.path || !reflect.DeepEqual(gotArgv, p.argv) || !reflect.DeepEqual(gotEnv, p.env) {
		t.Fatalf("exec(%q, %v, %v), want the plan", gotPath, gotArgv, gotEnv)
	}
	if exitCode != 1 {
		t.Fatalf("exit(%d) after a failed exec, want 1", exitCode)
	}
}

func TestReexec_LogsFailure(t *testing.T) {
	var logs []string
	reexec(&reexecPlan{path: "/x"}, func(string, []string, []string) error { return errors.New("boom") },
		func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }, func(int) {})
	if len(logs) == 0 || !strings.Contains(logs[len(logs)-1], "boom") {
		t.Fatalf("logs = %v, want the exec error", logs)
	}
}

func lockedPlan(t *testing.T, clear func(*os.File) error) *reexecPlan {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return &reexecPlan{path: "/x", lock: f, clearCLOEXEC: clear}
}

func TestReexec_ClearsCloseOnExecRightBeforeExec(t *testing.T) {
	var order []string
	p := lockedPlan(t, func(*os.File) error { order = append(order, "clear"); return nil })
	reexec(p, func(string, []string, []string) error { order = append(order, "exec"); return errors.New("x") },
		func(string, ...any) {}, func(int) {})
	if !reflect.DeepEqual(order, []string{"clear", "exec"}) {
		t.Fatalf("order = %v, want clear then exec", order)
	}
}

func TestReexec_ClearFailureStillExecs(t *testing.T) {
	var logs []string
	execed := false
	p := lockedPlan(t, func(*os.File) error { return errors.New("fcntl boom") })
	reexec(p, func(string, []string, []string) error { execed = true; return errors.New("x") },
		func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }, func(int) {})
	if !execed {
		t.Fatal("exec skipped after a clear failure")
	}
	want := "pid lock: hand-off failed (fcntl boom); the new image re-acquires it"
	found := false
	for _, l := range logs {
		found = found || l == want
	}
	if !found {
		t.Fatalf("logs = %v, want %q", logs, want)
	}
}

func TestReexec_NoLockClearsNothing(t *testing.T) {
	called := false
	p := &reexecPlan{path: "/x", clearCLOEXEC: func(*os.File) error { called = true; return nil }}
	reexec(p, func(string, []string, []string) error { return errors.New("x") }, func(string, ...any) {}, func(int) {})
	if called {
		t.Fatal("cleared close-on-exec with no lock")
	}
}

// codex plan review #1: a signal that lands after serveAndWait's watcher
// has exited sits in sigCh. The last gate before exec stops delivery first
// (from then on SIGTERM takes its default action and ends the process),
// then takes anything already buffered: one pending → stop, not restart.
func TestRestartStillWanted_StopsDeliveryThenDrains(t *testing.T) {
	sig := make(chan os.Signal, 1)
	var order []string
	stop := func() { order = append(order, "stop") }
	if !restartStillWanted(sig, stop, func(string, ...any) {}) {
		t.Fatal("no pending signal → restart still wanted")
	}
	if len(order) != 1 {
		t.Fatalf("stop not called: %v", order)
	}

	sig <- syscall.SIGTERM
	var logs []string
	if restartStillWanted(sig, func() {}, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }) {
		t.Fatal("a pending signal must turn the restart into a stop")
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "exiting instead of restarting") {
		t.Fatalf("logs = %v", logs)
	}
}

// A signal delivered just before signal.Stop returns lands in sig during
// stop(). Stopping first and draining second catches it; the reverse
// order (drain, then stop) would miss it and re-exec under `pdx stop`.
func TestRestartStillWanted_SignalLandingDuringStopIsSeen(t *testing.T) {
	sig := make(chan os.Signal, 1)
	stop := func() { sig <- syscall.SIGTERM }
	var logs []string
	if restartStillWanted(sig, stop, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }) {
		t.Fatal("a signal landing during stop must turn the restart into a stop")
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "exiting instead of restarting") {
		t.Fatalf("logs = %v", logs)
	}
}

// J1: a fork between the CLOEXEC clear and the exec would hand the lock to a
// child. syscall.ForkLock is held exclusively across both.
func TestReexec_HoldsForkLockWhileExecRuns(t *testing.T) {
	p := lockedPlan(t, func(*os.File) error { return nil })
	var heldInExec bool
	reexec(p, func(string, []string, []string) error {
		if syscall.ForkLock.TryRLock() {
			syscall.ForkLock.RUnlock()
		} else {
			heldInExec = true
		}
		return errors.New("x")
	}, func(string, ...any) {}, func(int) {})
	if !heldInExec {
		t.Fatal("ForkLock was not held exclusively while execFn ran")
	}
	if !syscall.ForkLock.TryRLock() {
		t.Fatal("ForkLock still held after reexec returned")
	}
	syscall.ForkLock.RUnlock()
}

func TestReexec_NoLockDoesNotTakeForkLock(t *testing.T) {
	var free bool
	reexec(&reexecPlan{path: "/x"}, func(string, []string, []string) error {
		if free = syscall.ForkLock.TryRLock(); free {
			syscall.ForkLock.RUnlock()
		}
		return errors.New("x")
	}, func(string, ...any) {}, func(int) {})
	if !free {
		t.Fatal("ForkLock taken although there is no pid lock")
	}
}

func TestReexec_FailedExecRestoresCloseOnExec(t *testing.T) {
	p := lockedPlan(t, nil) // real clearCloseOnExec
	var inExec int
	reexec(p, func(string, []string, []string) error {
		fl, _, errno := syscall.Syscall(syscall.SYS_FCNTL, p.lock.Fd(), syscall.F_GETFD, 0)
		if errno != 0 {
			t.Fatal(errno)
		}
		inExec = int(fl)
		return errors.New("x")
	}, func(string, ...any) {}, func(int) {})
	if inExec&syscall.FD_CLOEXEC != 0 {
		t.Fatal("fd still close-on-exec during exec; hand-off is broken")
	}
	fl, _, errno := syscall.Syscall(syscall.SYS_FCNTL, p.lock.Fd(), syscall.F_GETFD, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	if int(fl)&syscall.FD_CLOEXEC == 0 {
		t.Fatal("fd not close-on-exec again after a failed exec")
	}
}

// #2122: a daemon started from inside a Claude Code session drops that session's identity before it
// captures the plan a restart re-execs, so neither the daemon's children nor its next image carry it.
// Mutation gate: capture before the scrub (or skip it) → red.
func TestCaptureCleanReexecPlan_DropsTheSessionIdentityFromTheProcessAndThePlan(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "tok")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid")
	t.Setenv("CLAUDE_CONFIG_DIR", "/keep")
	plan, err := captureCleanReexecPlan(func() (string, error) { return "/bin/pdx", nil }, []string{"pdx", "serve"})
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range plan.env {
		if name, _, _ := strings.Cut(kv, "="); claudeenv.IsSessionVar(name) {
			t.Errorf("the re-exec plan carries %s", name)
		}
	}
	if !slices.Contains(plan.env, "CLAUDE_CONFIG_DIR=/keep") {
		t.Error("configuration was dropped from the plan")
	}
	for _, name := range []string{"CLAUDECODE", "CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_SESSION_ID"} {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("%s is still in the daemon's own environment", name)
		}
	}
}
