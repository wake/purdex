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

func TestReapInheritedChildren_SurvivorIsCollectedLaterWithoutTouchingNewChildren(t *testing.T) {
	reapScenario(t, "survivor")
}

func TestReapInheritedChildren_ListingFailureIsReportedAndTheExitedAreStillCollected(t *testing.T) {
	reapScenario(t, "pslost")
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
	case "survivor":
		reapSurvivor(t)
	case "pslost":
		reapPSLost(t)
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
	if n, _, _ := reapInheritedChildren(); n < 3 {
		t.Fatalf("reaped %d, want at least the 3 zombies", n)
	}
	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Errorf("pid %d: kill(0) = %v, want ESRCH (reaped)", pid, err)
		}
	}
	if n, _, _ := reapInheritedChildren(); n != 0 {
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
	var survivors []int
	go func() { n, s, _ := reapInheritedChildren(); survivors = s; done <- n }()
	select {
	case n := <-done:
		if n != 0 {
			t.Errorf("reaped %d while the only child runs", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reapInheritedChildren blocked on a running child")
	}
	if len(survivors) != 1 || survivors[0] != cmd.Process.Pid {
		t.Errorf("survivors = %v, want the running child %d", survivors, cmd.Process.Pid)
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Errorf("the running child was disturbed: %v", err)
	}
}

// A child still running at boot that exits later is collected by the targeted watcher — and a child of THIS image
// started afterwards keeps its exit status for its own Wait (the watcher waits for the given pids only).
// Mutation gate: wait4(-1) in the watcher → the new child's Wait gets ECHILD → red.
func reapSurvivor(t *testing.T) {
	old := exec.Command("/bin/sleep", "1")
	if err := old.Start(); err != nil { // the previous image's child, unwaited
		t.Fatal(err)
	}
	reaped, survivors, _ := reapInheritedChildren()
	if reaped != 0 || len(survivors) != 1 || survivors[0] != old.Process.Pid {
		t.Fatalf("reaped=%d survivors=%v, want the one running child %d", reaped, survivors, old.Process.Pid)
	}
	mine := exec.Command("/bin/sh", "-c", "exit 7") // this image's own child, started after the boot step
	if err := mine.Start(); err != nil {
		t.Fatal(err)
	}
	if n := reapSurvivors(survivors, 50*time.Millisecond, 10*time.Second, t.Logf); n != 1 {
		t.Fatalf("watcher collected %d, want 1", n)
	}
	if err := syscall.Kill(old.Process.Pid, 0); err != syscall.ESRCH {
		t.Errorf("the survivor is still there after it ended: %v", err)
	}
	if err := mine.Wait(); err == nil || err.(*exec.ExitError).ExitCode() != 7 {
		t.Errorf("this image's own child lost its exit status: %v", err)
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

func reapPSLost(t *testing.T) {
	psPath = "/nonexistent/ps"
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return zombieCount([]int{cmd.Process.Pid}) == 1 })
	reaped, survivors, err := reapInheritedChildren()
	if err == nil {
		t.Fatal("a failing lister was not reported")
	}
	if reaped < 1 || len(survivors) != 0 {
		t.Fatalf("reaped=%d survivors=%v, want the exited child collected and no survivors known", reaped, survivors)
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != syscall.ESRCH {
		t.Errorf("the exited child was not collected: %v", err)
	}
}
