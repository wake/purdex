package main

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// #2137: children the previous image started and nobody waits for become zombies of this pid; the boot reaper
// collects them. A child that is still running is left alone.
func TestReapInheritedChildren_CollectsExitedUnwaitedChildren(t *testing.T) {
	reapScenario(t, "collect")
}

// Mutation gate: a blocking wait (no WNOHANG) hangs on the sleeper → the helper times out → red.
func TestReapInheritedChildren_NeverBlocksOnARunningChild(t *testing.T) {
	reapScenario(t, "running")
}

// reapScenario runs the scenario in a fresh copy of the test binary: wait4(-1) takes the exit status of ANY
// child of the process, so in this package's own test process it could steal one from a test that is still
// waiting for its command. The copy has no children but its own.
func reapScenario(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestReapHelperProcess$")
	cmd.Env = append(os.Environ(), "PDX_REAP_HELPER="+name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scenario %s: %v\n%s", name, err, out)
	}
}

// TestReapHelperProcess is the child of reapScenario, not a test of its own.
func TestReapHelperProcess(t *testing.T) {
	switch os.Getenv("PDX_REAP_HELPER") {
	case "collect":
		reapCollect(t)
	case "running":
		reapRunning(t)
	}
}

func reapCollect(t *testing.T) {
	var pids []int
	for range 3 {
		cmd := exec.Command("/bin/sh", "-c", "exit 0")
		if err := cmd.Start(); err != nil { // never waited: what an exec in place leaves behind
			t.Fatal(err)
		}
		pids = append(pids, cmd.Process.Pid)
	}
	waitUntil(t, func() bool { return zombieCount(pids) == len(pids) })
	if n := reapInheritedChildren(); n < 3 {
		t.Fatalf("reaped %d, want at least the 3 zombies", n)
	}
	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Errorf("pid %d: kill(0) = %v, want ESRCH (reaped)", pid, err)
		}
	}
	if n := reapInheritedChildren(); n != 0 {
		t.Errorf("a second pass reaped %d, want 0", n)
	}
}

func reapRunning(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	done := make(chan int, 1)
	go func() { done <- reapInheritedChildren() }()
	select {
	case n := <-done:
		if n != 0 {
			t.Errorf("reaped %d while the only child runs", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reapInheritedChildren blocked on a running child")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Errorf("the running child was disturbed: %v", err)
	}
}

// zombieCount is how many of pids have exited but not been waited for: kill(0) still succeeds on a zombie, so
// ask wait4 with WNOWAIT-less probing is not possible; ps says it.
func zombieCount(pids []int) int {
	n := 0
	for _, pid := range pids {
		out, err := exec.Command("/bin/ps", "-o", "stat=", "-p", itoa(pid)).Output()
		if err == nil && len(out) > 0 && out[0] == 'Z' {
			n++
		}
	}
	return n
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
