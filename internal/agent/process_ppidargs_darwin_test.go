package agent

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wake/purdex/internal/execstat"
)

// psPPIDArgs is the ps reader the hook CLI used before: `ps -p pid -o ppid= -o args=`, first field the parent, the rest split on
// white space.
func psPPIDArgs(t *testing.T, pid int) (int, []string) {
	t.Helper()
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=", "-o", "args=").Output()
	if err != nil {
		t.Fatalf("ps %d: %v", pid, err)
	}
	f := strings.Fields(strings.TrimSpace(string(out)))
	ppid, err := strconv.Atoi(f[0])
	if err != nil {
		t.Fatal(err)
	}
	return ppid, f[1:]
}

// descendants lists pid's children, breadth first, until none are left or depth runs out.
func descendants(t *testing.T, pid int) []int {
	t.Helper()
	var all []int
	frontier := []int{pid}
	for depth := 0; depth < 6 && len(frontier) > 0; depth++ {
		var next []int
		for _, p := range frontier {
			out, _ := exec.Command("pgrep", "-P", strconv.Itoa(p)).Output()
			for _, f := range strings.Fields(string(out)) {
				if c, err := strconv.Atoi(f); err == nil {
					all = append(all, c)
					next = append(next, c)
				}
			}
		}
		frontier = next
	}
	return all
}

// startTree starts argv and waits until its children exist (or gives up), returning every pid of the tree, root first.
func startTree(t *testing.T, want int, name string, args ...string) []int {
	t.Helper()
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	var pids []int
	for i := 0; i < 100; i++ {
		pids = append([]int{cmd.Process.Pid}, descendants(t, cmd.Process.Pid)...)
		if len(pids) >= want {
			return pids
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tree of %s has %d processes, want %d", name, len(pids), want)
	return nil
}

func TestProcessPPIDArgs_SameFieldsAsPS(t *testing.T) {
	cases := map[string][]int{
		"self":                  {os.Getpid()},
		"plain sleep":           startTree(t, 1, "/bin/sleep", "30"),
		"arguments with spaces": startTree(t, 2, "/bin/sh", "-c", "sleep 30; :", "name", "an  argument with   spaces", "tab-free"),
		// a shim chain: sh under sh under sleep, as a hook launched through npx/sh wrappers sees it
		"nested shims": startTree(t, 3, "/bin/sh", "-c", "sh -c 'sh -c \"sleep 30; :\"; :'; :"),
	}
	for name, pids := range cases {
		for _, pid := range pids {
			ppid, argv, err := ProcessPPIDArgs(pid)
			if err != nil {
				t.Errorf("%s pid %d: %v", name, pid, err)
				continue
			}
			wantPPID, wantArgv := psPPIDArgs(t, pid)
			if ppid != wantPPID || !reflect.DeepEqual(argv, wantArgv) {
				t.Errorf("%s pid %d: sysctl %d %q, ps %d %q", name, pid, ppid, argv, wantPPID, wantArgv)
			}
		}
	}
}

func TestProcessPPIDArgs_ForksNothing(t *testing.T) {
	execstat.PS.Reset()
	defer execstat.PS.Reset()
	if _, _, err := ProcessPPIDArgs(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if n, _ := execstat.PS.Snapshot(); n != 0 {
		t.Fatalf("forked ps %d times", n)
	}
}

func TestProcessPPIDArgs_AnswersByErrorWhenPSMustDecide(t *testing.T) {
	// an argument ps would print through its own escaping (a TAB) is left to ps
	pid := startTree(t, 1, "/bin/sleep", "30")[0]
	tabbed := startTree(t, 1, "/bin/sh", "-c", "sleep 30; :", "name", "a\tb")[0]
	if _, _, err := ProcessPPIDArgs(tabbed); err == nil {
		t.Fatal("an argv with a TAB was answered; ps escapes it, so ps must decide")
	}
	if _, _, err := ProcessPPIDArgs(pid); err != nil {
		t.Fatal(err)
	}
	// the argument area cannot be read
	orig := kernProcArgs2
	kernProcArgs2 = func(int) ([]byte, error) { return nil, errors.New("boom") }
	defer func() { kernProcArgs2 = orig }()
	if _, _, err := ProcessPPIDArgs(pid); err == nil {
		t.Fatal("an unreadable argument area was answered")
	}
	// and a parser that rejects the buffer
	kernProcArgs2 = func(int) ([]byte, error) { return []byte{1}, nil }
	if _, _, err := ProcessPPIDArgs(pid); err == nil {
		t.Fatal("a corrupt buffer was answered")
	}
}

// A pid reused within the same second has the same parent and the same start second; only the microseconds differ.
func TestProcessPPIDArgs_ReusedPidInTheSameSecondIsRefused(t *testing.T) {
	pid := startTree(t, 1, "/bin/sleep", "30")[0]
	orig := kernProcPid
	calls := 0
	kernProcPid = func(p int) (*unix.KinfoProc, error) {
		kp, err := orig(p)
		if err == nil && kp != nil {
			calls++
			if calls > 1 {
				kp.Proc.P_starttime.Usec++ // the replacement started a moment later, in the same second
			}
		}
		return kp, err
	}
	defer func() { kernProcPid = orig }()
	if _, _, err := ProcessPPIDArgs(pid); !errors.Is(err, ErrProcessChanged) {
		t.Fatalf("a process that started at a different microsecond was taken for the same one (err %v)", err)
	}
	if calls < 2 {
		t.Fatalf("the identity was read %d times, want a read before and after the arguments", calls)
	}
}

func TestProcessPPIDArgs_GonePidAndBadPid(t *testing.T) {
	if _, _, err := ProcessPPIDArgs(2147483646); err == nil {
		t.Fatal("a pid that does not exist was answered")
	}
	for _, bad := range []int{0, -1} {
		if _, _, err := ProcessPPIDArgs(bad); err == nil {
			t.Fatalf("pid %d was answered", bad)
		}
	}
}
