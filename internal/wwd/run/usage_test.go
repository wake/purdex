package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseStatusReadsTheCodeAndTheUnitsFigures(t *testing.T) {
	code, u, got := parseStatus([]byte("3\n2500000\n734003200\n"))
	if !got || code != 3 || u.From != UsageUnit || u.CPUSeconds == nil || *u.CPUSeconds != 2.5 || u.MemPeakBytes == nil || *u.MemPeakBytes != 734003200 {
		t.Fatalf("%d %+v %v", code, u, got)
	}
	// each figure is its own: a memory peak without CPU time and the other way round
	_, u, _ = parseStatus([]byte("0\n\n1048576\n"))
	if u.CPUSeconds != nil || u.MemPeakBytes == nil || u.From != UsageUnit {
		t.Fatalf("%+v", u)
	}
	_, u, _ = parseStatus([]byte("0\n1000000\n\n"))
	if u.CPUSeconds == nil || *u.CPUSeconds != 1 || u.MemPeakBytes != nil {
		t.Fatalf("%+v", u)
	}
}

// What cannot be read is nil, never 0.
func TestParseStatusLeavesWhatItCannotReadNil(t *testing.T) {
	for name, in := range map[string]string{
		"code only (the old wrapper)": "7",
		"empty figures":               "7\n\n\n",
		"garbage":                     "7\nabc\n-5\n",
		"max":                         "7\n\nmax\n",
	} {
		code, u, got := parseStatus([]byte(in))
		if !got || code != 7 || u.CPUSeconds != nil || u.MemPeakBytes != nil || u.From != UsageNone {
			t.Errorf("%s: %d %+v %v", name, code, u, got)
		}
	}
	for _, in := range []string{"", "\n1\n2\n", "x\n1\n2\n"} {
		if _, _, got := parseStatus([]byte(in)); got {
			t.Errorf("%q has no code and must not count as a status", in)
		}
	}
}

// The wrapper run by the real shell. On a host with a cgroup v2 of its own for the shell (Linux) it reads real figures; elsewhere (macOS)
// they are empty. Either way the code is the command's and is the first line.
func TestExitWrapperWritesCodeThenUsageFigures(t *testing.T) {
	file := filepath.Join(t.TempDir(), "status")
	cmd := exec.Command("sh", "-c", exitWrapper, file, "sh", "-c", "exit 4")
	if err := cmd.Run(); err == nil || err.(*exec.ExitError).ExitCode() != 4 {
		t.Fatalf("exit: %v", err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	code, u, got := parseStatus(b)
	if !got || code != 4 {
		t.Fatalf("status file %q", b)
	}
	if runtime.GOOS != "linux" && (u.CPUSeconds != nil || u.MemPeakBytes != nil) {
		t.Fatalf("figures where there is no cgroup: %q %+v", b, u)
	}
	// on Linux a figure is either missing or a real number, and never the root cgroup's (a shell outside a unit may be in the root
	// or in a user slice: we only insist that what is read parses)
	if u.CPUSeconds != nil && *u.CPUSeconds < 0 {
		t.Fatalf("%+v", u)
	}
}

// The wrapper does not touch the root cgroup: a shell whose /proc/self/cgroup says "/" reads nothing. (Only checked where it can be
// made to happen: the guard is in the script text.)
func TestExitWrapperRefusesTheRootCgroup(t *testing.T) {
	if !strings.Contains(exitWrapper, `"$cg" != /`) || !strings.Contains(exitWrapper, `-n "$cg"`) {
		t.Fatalf("the wrapper does not guard against an empty or root cgroup: %s", exitWrapper)
	}
}

// An unsandboxed run reports its CPU time from the process's rusage, the children it waited for included.
func TestRunReportsRusageCPUTime(t *testing.T) {
	// a shell that burns a little CPU in itself and in a child
	r := Run(context.Background(), spec(t, "sh", "-c", "i=0; while [ $i -lt 20000 ]; do i=$((i+1)); done; sh -c 'i=0; while [ $i -lt 20000 ]; do i=$((i+1)); done'"))
	if r.Outcome != Exited || r.ExitCode != 0 {
		t.Fatalf("%+v", r)
	}
	if r.Usage.From != UsageRusage || r.Usage.CPUSeconds == nil || *r.Usage.CPUSeconds <= 0 {
		t.Fatalf("%+v", r.Usage)
	}
	if r.Usage.MemPeakBytes != nil {
		t.Fatalf("a plain run has no tree memory figure: %+v", r.Usage)
	}
}

// A run that was ended (deadline, cancel) still has its CPU time; one that never started has none.
func TestRunUsageForEndedAndNotStartedRuns(t *testing.T) {
	s := spec(t, "sh", "-c", "sleep 30")
	s.Timeout = 200 * time.Millisecond
	r := Run(context.Background(), s)
	if r.Outcome != TimedOut || r.Usage.CPUSeconds == nil {
		t.Fatalf("%+v %+v", r, r.Usage)
	}
	r = Run(context.Background(), spec(t, "/no/such/program"))
	if r.Outcome != StartFailed || r.Usage.CPUSeconds != nil || r.Usage.From != UsageNone {
		t.Fatalf("%+v", r)
	}
}

// A sandboxed run takes the unit's figures from the status file the wrapper left.
func TestSandboxedRunReportsTheUnitsUsage(t *testing.T) {
	w := newWorld(t)
	w.set("gone", "")
	w.set("wrapper-exit", "0\n2500000\n734003200\n")
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != Exited || r.ExitCode != 0 || r.Err != nil {
		t.Fatalf("%+v", r)
	}
	u := r.Usage
	if u.From != UsageUnit || u.CPUSeconds == nil || *u.CPUSeconds != 2.5 || u.MemPeakBytes == nil || *u.MemPeakBytes != 734003200 {
		t.Fatalf("%+v", u)
	}
}

// A status file with the code alone, or with figures the wrapper could not read, gives an unknown usage and the same result.
func TestSandboxedRunWithoutFiguresHasNoUsage(t *testing.T) {
	for name, content := range map[string]string{"code only": "0", "empty figures": "0\n\n\n"} {
		w := newWorld(t)
		w.set("gone", "")
		w.set("wrapper-exit", content)
		r := Run(context.Background(), sandboxSpec(t, w, "true"))
		if r.Outcome != Exited || r.ExitCode != 0 || r.Err != nil || r.Usage.CPUSeconds != nil || r.Usage.MemPeakBytes != nil || r.Usage.From != UsageNone {
			t.Errorf("%s: %+v %+v", name, r, r.Usage)
		}
	}
}

// A task that failed has its figures too (the unit stays for systemd to show; the file is still what carries them).
func TestSandboxedFailedRunKeepsItsUsage(t *testing.T) {
	w := newWorld(t)
	w.set("loaded", "")
	w.set("exit", "3")
	w.set("wrapper-exit", "3\n1000000\n1048576\n")
	w.set("show", "ActiveState=failed\nResult=exit-code\nExecMainStatus=3\nExecMainCode=1\n")
	r := Run(context.Background(), sandboxSpec(t, w, "false"))
	if r.Outcome != Exited || r.ExitCode != 3 || r.Usage.CPUSeconds == nil || *r.Usage.CPUSeconds != 1 {
		t.Fatalf("%+v %+v", r, r.Usage)
	}
}

// The fourth line is how many processes the command left in the unit besides the wrapper.
func TestParseStatusReadsTheLeftoverProcesses(t *testing.T) {
	for in, want := range map[string]bool{"0\n1\n2\n1\n": true, "0\n1\n2\n5": true, "0\n1\n2\n0\n": false, "0\n1\n2\n": false, "0\n1\n2\n-1\n": false, "0\n1\n2\nx\n": false} {
		_, u, got := parseStatus([]byte(in))
		if !got || u.Leaked != want {
			t.Errorf("%q: leaked %v, want %v", in, u.Leaked, want)
		}
	}
	// the leftover count stands on its own: figures that could not be read do not hide it
	if _, u, _ := parseStatus([]byte("0\n\n\n2\n")); !u.Leaked || u.CPUSeconds != nil {
		t.Errorf("%+v", u)
	}
}

func TestExitWrapperCountsTheProcessesLeftBesideItself(t *testing.T) {
	// the count is made before the helpers that read the figures are forked, with the shell's own read
	i, j := strings.Index(exitWrapper, "cgroup.procs"), strings.Index(exitWrapper, "$(sed -n 's/^usage_usec")
	if i < 0 || j < 0 || i > j || !strings.Contains(exitWrapper, "while read -r _") {
		t.Fatalf("the count must come first and use the shell's own read: %s", exitWrapper)
	}
}

// An unsandboxed run that left a process in its group behind is marked; a clean one is not.
func TestRunMarksALeakedProcessAsLowerBound(t *testing.T) {
	r := Run(context.Background(), spec(t, "sh", "-c", "sleep 30 >/dev/null 2>&1 & exit 0"))
	if r.Outcome != Exited || r.ExitCode != 0 || r.Usage.CPUSeconds == nil || !r.Usage.Leaked {
		t.Fatalf("%+v %+v", r, r.Usage)
	}
	r = Run(context.Background(), spec(t, "true"))
	if r.Usage.Leaked {
		t.Fatalf("a clean run is not a leak: %+v", r.Usage)
	}
}

// A sandboxed run takes the leftover count from the status file.
func TestSandboxedRunMarksLeftoverProcessesAsLowerBound(t *testing.T) {
	w := newWorld(t)
	w.set("gone", "")
	w.set("wrapper-exit", "0\n2500000\n734003200\n2\n")
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != Exited || r.ExitCode != 0 || !r.Usage.Leaked || r.Usage.CPUSeconds == nil {
		t.Fatalf("%+v %+v", r, r.Usage)
	}
}

// User and system time both count.
func TestRusageSecondsIsUserPlusSystem(t *testing.T) {
	ru := &syscall.Rusage{Utime: syscall.Timeval{Sec: 1, Usec: 250000}, Stime: syscall.Timeval{Sec: 2, Usec: 250000}}
	if got := rusageSeconds(ru); got != 3.5 {
		t.Fatalf("%v", got)
	}
}
