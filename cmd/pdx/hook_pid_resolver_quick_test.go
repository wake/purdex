package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"testing"

	"github.com/wake/purdex/internal/agent"
)

func TestReadProcessInfo_UsesTheSysctlReadWithoutForking(t *testing.T) {
	orig := quickProcessInfo
	t.Cleanup(func() { quickProcessInfo = orig })
	quickProcessInfo = func(pid int) (int, []string, error) { return 7, []string{"/opt/bin/claude", "--flag", "a"}, nil }
	hookForks.Store(0)
	got, err := readProcessInfo(4242)
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 4242 || got.PPID != 7 || got.ExePath != "/opt/bin/claude" || !reflect.DeepEqual(got.Argv, []string{"/opt/bin/claude", "--flag", "a"}) {
		t.Fatalf("procInfo = %+v", got)
	}
	if n := hookForks.Load(); n != 0 {
		t.Fatalf("forks = %d, want 0", n)
	}
}

func TestReadProcessInfo_FallsBackToPSWhenTheSysctlReadFails(t *testing.T) {
	orig := quickProcessInfo
	t.Cleanup(func() { quickProcessInfo = orig })
	quickProcessInfo = func(pid int) (int, []string, error) { return 0, nil, errors.New("boom") }
	hookForks.Store(0)
	got, err := readProcessInfo(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != os.Getpid() || got.PPID != os.Getppid() || len(got.Argv) == 0 || got.ExePath != got.Argv[0] {
		t.Fatalf("procInfo = %+v", got)
	}
	if n := hookForks.Load(); n != 1 {
		t.Fatalf("forks = %d, want the one ps", n)
	}
	// an answer with no argument fields is not an answer either
	quickProcessInfo = func(pid int) (int, []string, error) { return 7, nil, nil }
	hookForks.Store(0)
	if _, err := readProcessInfo(os.Getpid()); err != nil || hookForks.Load() != 1 {
		t.Fatalf("empty argv: err=%v forks=%d", err, hookForks.Load())
	}
}

// A pid reused while its arguments were read must not be answered by ps for the replacement: no answer, no fork, and the sender
// becomes uncertain.
func TestReadProcessInfo_ChangedPidIsNotAskedOfPS(t *testing.T) {
	orig := quickProcessInfo
	t.Cleanup(func() { quickProcessInfo = orig })
	quickProcessInfo = func(pid int) (int, []string, error) {
		return 0, nil, fmt.Errorf("pid %d changed: %w", pid, agent.ErrProcessChanged)
	}
	hookForks.Store(0)
	if _, err := readProcessInfo(os.Getpid()); err == nil {
		t.Fatal("a changed pid was answered")
	}
	if n := hookForks.Load(); n != 0 {
		t.Fatalf("forks = %d, want 0: ps must not be asked", n)
	}
	pid, uncertain := resolveAgentPID(os.Getpid())
	if pid != os.Getpid() || !uncertain {
		t.Fatalf("resolveAgentPID = %d, uncertain %v; want the pid, uncertain", pid, uncertain)
	}
}

// The numbers of #3323: the forks one hook makes to name its sender when the agent is the hook's direct parent (not a shell).
// Before: ps ppid+args, ps lstart (2; the third fork is tmux display-message, counted elsewhere). After, on darwin: ps lstart (1).
func TestResolveHookProvenance_ForkCount(t *testing.T) {
	child := exec.Command("/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	origParent, origQuick := getParentPID, quickProcessInfo
	t.Cleanup(func() { getParentPID, quickProcessInfo = origParent, origQuick })
	getParentPID = func() int { return child.Process.Pid }

	measure := func() (int64, hookProvenance) {
		hookForks.Store(0)
		p := resolveHookProvenance()
		return hookForks.Load(), p
	}
	quickProcessInfo = func(pid int) (int, []string, error) { return 0, nil, errors.New("off") }
	before, pBefore := measure()
	quickProcessInfo = origQuick
	after, pAfter := measure()
	t.Logf("hookForks for the sender lookup: before %d, after %d", before, after)
	if before != 2 {
		t.Fatalf("before = %d, want 2 (ps args, ps lstart)", before)
	}
	want := int64(2)
	if runtime.GOOS == "darwin" {
		want = 1
	}
	if after != want {
		t.Fatalf("after = %d, want %d", after, want)
	}
	if pBefore.SenderPID != pAfter.SenderPID || pBefore.SenderStartTime != pAfter.SenderStartTime || pBefore.SenderUncertain != pAfter.SenderUncertain {
		t.Fatalf("provenance changed: before %+v, after %+v", pBefore, pAfter)
	}
}
