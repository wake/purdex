//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The helpers below are the darwin test file's; the two files never build
// together, so each carries its own copy.

// countPSForks routes runPS through a counter for the rest of the test. The
// real ps still runs, so the reads under test return real answers.
func countPSForks(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	orig := runPS
	runPS = func(ctx context.Context, args ...string) ([]byte, error) {
		n.Add(1)
		return orig(ctx, args...)
	}
	t.Cleanup(func() { runPS = orig })
	return &n
}

// fakePS makes runPS print out (or fail with err) for the rest of the test.
func fakePS(t *testing.T, out string, err error) {
	t.Helper()
	orig := runPS
	runPS = func(context.Context, ...string) ([]byte, error) { return []byte(out), err }
	t.Cleanup(func() { runPS = orig })
}

// psColumn asks ps directly, not through runPS, so a test's own probing never
// shows up in a fork count.
func psColumn(pid int, column string) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", column+"=").Output()
	return strings.TrimSpace(string(out)), err
}

// startProcess starts cmd, kills and reaps it at cleanup, and returns once ps
// can see it.
func startProcess(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %q: %v", cmd.Args, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	pid := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := psColumn(pid, "pid"); err == nil {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for ps to see pid %d", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func takeSnapshot(t *testing.T) *ProcessSnapshot {
	t.Helper()
	snap, err := SnapshotProcesses(context.Background())
	if err != nil {
		t.Fatalf("SnapshotProcesses: %v", err)
	}
	return snap
}

// requireSameAsPerPIDReader pins the snapshot's Read of pid to what the
// per-PID reader returns for it, field by field.
func requireSameAsPerPIDReader(t *testing.T, snap *ProcessSnapshot, pid int) {
	t.Helper()
	want, err := ReadProcessInfo(pid)
	if err != nil {
		t.Fatalf("ReadProcessInfo(%d): %v", pid, err)
	}
	got, err := snap.Read(pid)
	if err != nil {
		t.Fatalf("snap.Read(%d): %v", pid, err)
	}
	if got.PID != want.PID {
		t.Errorf("PID = %d, want %d", got.PID, want.PID)
	}
	if got.PPID != want.PPID {
		t.Errorf("PPID = %d, want %d", got.PPID, want.PPID)
	}
	if got.ExePath != want.ExePath {
		t.Errorf("ExePath = %q, want %q", got.ExePath, want.ExePath)
	}
	if !slices.Equal(got.Argv, want.Argv) {
		t.Errorf("Argv = %q, want %q", got.Argv, want.Argv)
	}
	if !got.StartTime.Equal(want.StartTime) {
		t.Errorf("StartTime = %v, want %v", got.StartTime, want.StartTime)
	}
	for name, st := range map[string]time.Time{"snapshot": got.StartTime, "per-PID": want.StartTime} {
		if st.Location() != time.Local {
			t.Errorf("%s StartTime location = %v, want time.Local", name, st.Location())
		}
		if st.Nanosecond() != 0 {
			t.Errorf("%s StartTime has %d ns, want whole seconds", name, st.Nanosecond())
		}
	}
}

func TestProcessSnapshot_Parity_Plain(t *testing.T) {
	sleepPID := startProcess(t, exec.Command("sleep", "30"))
	snap := takeSnapshot(t)

	for name, pid := range map[string]int{"self": os.Getpid(), "parent": os.Getppid(), "sleep": sleepPID} {
		t.Run(name, func(t *testing.T) {
			if !snap.Alive(pid) {
				t.Fatalf("Alive(%d) = false", pid)
			}
			requireSameAsPerPIDReader(t, snap, pid)

			lstart, err := psColumn(pid, "lstart")
			if err != nil {
				t.Fatalf("ps lstart: %v", err)
			}
			got, err := snap.StartTime(pid)
			if err != nil {
				t.Fatalf("snap.StartTime(%d): %v", pid, err)
			}
			if got != lstart {
				t.Fatalf("StartTime = %q, want ps lstart %q", got, lstart)
			}
		})
	}
}

func TestProcessSnapshot_MissingPID(t *testing.T) {
	snap := takeSnapshot(t)

	// PID_MAX_LIMIT: pid_max cannot be set above it and PIDs stay below
	// pid_max, so no process ever has this PID.
	const never = 1 << 22
	if snap.Alive(never) {
		t.Fatalf("Alive(%d) = true", never)
	}
	if _, err := snap.Read(never); !errors.Is(err, ErrNotInSnapshot) {
		t.Fatalf("Read(%d): err = %v, want ErrNotInSnapshot", never, err)
	}
	if _, err := snap.StartTime(never); !errors.Is(err, ErrNotInSnapshot) {
		t.Fatalf("StartTime(%d): err = %v, want ErrNotInSnapshot", never, err)
	}

	for _, pid := range []int{0, -1} {
		if snap.Alive(pid) {
			t.Fatalf("Alive(%d) = true", pid)
		}
		if _, err := snap.Read(pid); err == nil || errors.Is(err, ErrNotInSnapshot) {
			t.Fatalf("Read(%d): err = %v, want a plain invalid pid error", pid, err)
		}
		if _, err := snap.StartTime(pid); err == nil || errors.Is(err, ErrNotInSnapshot) {
			t.Fatalf("StartTime(%d): err = %v, want a plain invalid pid error", pid, err)
		}
	}
}

func TestProcessSnapshot_ForkCount(t *testing.T) {
	sleepPID := startProcess(t, exec.Command("sleep", "30"))

	forks := countPSForks(t)
	snap := takeSnapshot(t)
	for _, pid := range []int{os.Getpid(), os.Getppid(), sleepPID} {
		if _, err := snap.Read(pid); err != nil {
			t.Fatalf("snap.Read(%d): %v", pid, err)
		}
		if _, err := snap.StartTime(pid); err != nil {
			t.Fatalf("snap.StartTime(%d): %v", pid, err)
		}
	}
	if got := forks.Load(); got != 1 {
		t.Fatalf("snapshot + Read + StartTime of self, parent and sleep forked ps %d times, want 1 (the table)", got)
	}
}

func TestProcessSnapshot_ForkCount_PerPIDReaderIsCounted(t *testing.T) {
	forks := countPSForks(t)
	if _, err := ReadProcessInfo(os.Getpid()); err != nil {
		t.Fatalf("ReadProcessInfo(self): %v", err)
	}
	if got := forks.Load(); got != 2 {
		t.Fatalf("ReadProcessInfo(self) forked ps %d times, want 2 (ppid, lstart)", got)
	}
}

func TestProcessSnapshot_UnparseableStart(t *testing.T) {
	const lstart = "Di  6 Okt 05:11:40 2026"
	fakePS(t, fmt.Sprintf("%d %d %s\n", os.Getpid(), os.Getppid(), lstart), nil)
	snap := takeSnapshot(t)

	if !snap.Alive(os.Getpid()) {
		t.Fatal("Alive(self) = false; an unparseable start time does not make a process absent")
	}
	if got, err := snap.StartTime(os.Getpid()); err != nil || got != lstart {
		t.Fatalf("StartTime(self) = %q, %v; want ps's text %q", got, err, lstart)
	}
	_, err := snap.Read(os.Getpid())
	if err == nil || !strings.Contains(err.Error(), "parse start time for pid") {
		t.Fatalf("Read(self): err = %v, want the per-PID reader's parse error", err)
	}
}

func TestProcessSnapshot_UnusableTable(t *testing.T) {
	t.Run("ps fails", func(t *testing.T) {
		fakePS(t, "", errors.New("exit status 1"))
		if _, err := SnapshotProcesses(context.Background()); err == nil {
			t.Fatal("SnapshotProcesses succeeded with ps failing")
		}
	})
	t.Run("no rows parse", func(t *testing.T) {
		fakePS(t, "ps: unknown option -- A\n", nil)
		if _, err := SnapshotProcesses(context.Background()); err == nil {
			t.Fatal("SnapshotProcesses succeeded with a table of no processes")
		}
	})
}
