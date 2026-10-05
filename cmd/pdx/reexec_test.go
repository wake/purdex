package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
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
