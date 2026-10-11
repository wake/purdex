package probe

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/execstat"
)

// c933b0-128: the identity checks of a projection read (start time, ancestor walk) ask the kernel, not ps.
// Mutation: PidAncestorIncludes back to agentpkg.ReadProcessInfo / ProcessStartTime back to exec.Command("ps") → red.
func TestPidAncestorIncludes_ForksNoPS(t *testing.T) {
	grand := ppidViaPS(t, os.Getppid())
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	t.Setenv("PATH", t.TempDir()) // no ps to run: a fork would fail the walk
	if !PidAncestorIncludes(os.Getpid(), os.Getpid()) {
		t.Fatal("a pid is its own ancestor here (pane pid == the pid)")
	}
	if !PidAncestorIncludes(os.Getpid(), os.Getppid()) {
		t.Fatal("the parent is an ancestor")
	}
	if !PidAncestorIncludes(os.Getpid(), grand) {
		t.Fatalf("the grandparent %d is an ancestor", grand)
	}
	if PidAncestorIncludes(os.Getpid(), 2147483646) {
		t.Fatal("a pid outside the chain is not an ancestor")
	}
	if n, _ := execstat.PS.Snapshot(); n != 0 {
		t.Fatalf("PidAncestorIncludes forked ps %d times", n)
	}
}

func TestPidAncestorIncludes_MissingPidIsNotIncluded(t *testing.T) {
	if PidAncestorIncludes(2147483646, os.Getppid()) {
		t.Fatal("a pid that is gone has no ancestors")
	}
}

func TestProcessStartTime_EqualsPSWithoutForking(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("LANG", "en_US.UTF-8")
	if _, err := ProcessStartTime(os.Getpid()); err != nil { // the one-time calibration forks once, before the count
		t.Fatal(err)
	}
	want := map[int]string{}
	for _, pid := range []int{os.Getpid(), os.Getppid()} {
		out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
		if err != nil {
			t.Fatal(err)
		}
		want[pid] = strings.TrimSpace(string(out))
	}
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	t.Setenv("PATH", t.TempDir()) // no ps to run: a fork would fail, so success proves the read asked the kernel
	for pid, w := range want {
		got, err := ProcessStartTime(pid)
		if err != nil {
			t.Fatalf("pid %d: %v", pid, err)
		}
		if got != w {
			t.Fatalf("pid %d: %q != ps %q", pid, got, w)
		}
	}
	if n, _ := execstat.PS.Snapshot(); n != 0 {
		t.Fatalf("ProcessStartTime forked ps %d times", n)
	}
	if _, err := ProcessStartTime(2147483646); err == nil {
		t.Fatal("a missing pid must be an error")
	}
}

func ppidViaPS(t *testing.T, pid int) int {
	t.Helper()
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}
