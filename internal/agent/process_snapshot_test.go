//go:build darwin

package agent

import (
	"context"
	"encoding/binary"
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

	"golang.org/x/sys/unix"
)

const snapshotHelperEnv = "GO_WANT_PROCESS_SNAPSHOT_HELPER"

// A snapshot helper is this test binary re-executed with an argv the test
// chooses. That argv is what is under test, so it cannot carry -test.run (or
// anything else the testing package would parse, which an odd argv could
// trip). The environment selects the helper instead, here, before the testing
// package looks at argv at all.
func init() {
	if os.Getenv(snapshotHelperEnv) == "1" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}

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

// psColumn asks ps directly, not through runPS, so a test's own probing never
// shows up in a fork count.
func psColumn(pid int, column string) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", column+"=").Output()
	return strings.TrimSpace(string(out)), err
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
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
	waitFor(t, fmt.Sprintf("ps to see pid %d", pid), func() bool {
		_, err := psColumn(pid, "pid")
		return err == nil
	})
	return pid
}

// startHelper runs a sleeping copy of this test binary whose argv is exactly
// args. The environment holds only the helper switch, so the strings after
// argv in the argument area are known (an empty argv[0] makes ps read one of
// them, see parseProcArgs).
func startHelper(t *testing.T, args []string) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return startProcess(t, &exec.Cmd{Path: exe, Args: args, Env: []string{snapshotHelperEnv + "=1"}})
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
func requireSameAsPerPIDReader(t *testing.T, snap *ProcessSnapshot, pid int) ProcessInfo {
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
	return got
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

// The cases ps was measured on (plan §0). Printable ASCII takes the
// procargs2 fast path and must match ps verbatim; everything else must come
// out of the ps fallback unchanged. The fork count pins which path each case
// took, so a fast path that silently never runs cannot pass as parity.
func TestProcessSnapshot_Parity_ExoticArgv(t *testing.T) {
	const fast, viaPS = true, false
	cases := []struct {
		name string
		args []string
		fast bool
	}{
		{"empty argument", []string{"snap-helper", "", "x"}, fast},
		{"trailing empty argument", []string{"snap-helper", "x", ""}, fast},
		{"space inside", []string{"snap-helper", "a b"}, fast},
		{"quotes", []string{"snap-helper", `"dq"`, `'sq'`}, fast},
		{"leading spaces in argv0", []string{"   snap-helper", "x"}, fast},
		// The helper's environment is plain ASCII, so the env string ps
		// reads in place of the swallowed argv[0] keeps this on the fast path.
		{"empty argv0", []string{"", "x", "y"}, fast},
		{"backslash", []string{"snap-helper", `a\b`, `c\\d`}, fast},
		{"20000-byte argument", []string{"snap-helper", strings.Repeat("x", 20000)}, fast},
		{"tab", []string{"snap-helper", "a\tb"}, viaPS},
		{"tab in argv0", []string{"snap\thelper", "x"}, viaPS},
		{"newline", []string{"snap-helper", "a\nb"}, viaPS},
		{"DEL", []string{"snap-helper", "a\x7fb"}, viaPS},
		{"byte 0x80", []string{"snap-helper", "a\x80b"}, viaPS},
		{"byte 0xff", []string{"snap-helper", "a\xffb"}, viaPS},
		{"U+0085", []string{"snap-helper", "a\u0085b"}, viaPS},
		{"U+00A0", []string{"snap-helper", "a b"}, viaPS},
		{"U+3000", []string{"snap-helper", "a　b"}, viaPS},
		{"U+200B", []string{"snap-helper", "a​b"}, viaPS},
		{"U+202E", []string{"snap-helper", "a‮b"}, viaPS},
		{"CJK", []string{"snap-helper", "中文參數"}, viaPS},
		{"CJK argv0", []string{"中文", "x"}, viaPS},
		{"emoji", []string{"snap-helper", "a😀b"}, viaPS},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pid := startHelper(t, tc.args)
			snap := takeSnapshot(t)

			forks := countPSForks(t)
			if _, err := snap.Read(pid); err != nil {
				t.Fatalf("snap.Read(%d): %v", pid, err)
			}
			want := int64(2)
			if tc.fast {
				want = 0
			}
			if got := forks.Load(); got != want {
				t.Fatalf("Read forked ps %d times, want %d (fast path: %v)", got, want, tc.fast)
			}
			requireSameAsPerPIDReader(t, snap, pid)
		})
	}
}

func TestProcessSnapshot_Parity_Unreadable(t *testing.T) {
	t.Run("pid 1, another user's process", func(t *testing.T) {
		if _, err := kernProcArgs2(1); err == nil {
			t.Skip("procargs2 of pid 1 is readable here (running as root?); nothing to fall back from")
		}
		requireSameAsPerPIDReader(t, takeSnapshot(t), 1)
	})

	t.Run("zombie", func(t *testing.T) {
		cmd := exec.Command("/bin/sh", "-c", "exit 0")
		if err := cmd.Start(); err != nil {
			t.Fatalf("start sh: %v", err)
		}
		t.Cleanup(func() { _ = cmd.Wait() })
		pid := cmd.Process.Pid
		waitFor(t, "sh to become a zombie", func() bool {
			stat, err := psColumn(pid, "stat")
			return err == nil && strings.HasPrefix(stat, "Z")
		})

		snap := takeSnapshot(t)
		if !snap.Alive(pid) {
			t.Fatalf("Alive(zombie %d) = false; a zombie is still in the process table", pid)
		}
		got := requireSameAsPerPIDReader(t, snap, pid)
		if !slices.Equal(got.Argv, []string{"<defunct>"}) {
			t.Fatalf("zombie Argv = %q, want [<defunct>]", got.Argv)
		}
	})
}

func TestProcessSnapshot_ForkCount(t *testing.T) {
	sleepPID := startProcess(t, exec.Command("sleep", "30"))
	nonASCIIPID := startHelper(t, []string{"snap-helper", "中文"})

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
	if got := forks.Load(); got != 0 {
		t.Fatalf("snapshot + Read + StartTime of self, parent and sleep forked ps %d times, want 0", got)
	}

	forks.Store(0)
	if _, err := snap.Read(nonASCIIPID); err != nil {
		t.Fatalf("snap.Read(non-ASCII helper): %v", err)
	}
	if got := forks.Load(); got != 2 {
		t.Fatalf("Read of a non-ASCII argv forked ps %d times, want 2 (comm, args)", got)
	}
	forks.Store(0)
	if _, err := snap.Read(nonASCIIPID); err != nil {
		t.Fatalf("second snap.Read(non-ASCII helper): %v", err)
	}
	if got := forks.Load(); got != 0 {
		t.Fatalf("second Read of the same pid forked ps %d times, want 0 (remembered)", got)
	}

	if _, err := kernProcArgs2(1); err != nil {
		forks.Store(0)
		if _, err := snap.Read(1); err != nil {
			t.Fatalf("snap.Read(1): %v", err)
		}
		if got := forks.Load(); got != 2 {
			t.Fatalf("Read of pid 1 forked ps %d times, want 2 (comm, args)", got)
		}
	}
}

func TestProcessSnapshot_IdentityRecheck_ProcessGone(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	pid := startProcess(t, cmd)
	snap := takeSnapshot(t)
	if !snap.Alive(pid) {
		t.Fatalf("Alive(%d) = false before the kill", pid)
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	_, err := snap.Read(pid)
	if !errors.Is(err, ErrProcessChanged) {
		t.Fatalf("Read of a pid that exited after the snapshot: err = %v, want ErrProcessChanged", err)
	}
	if !snap.Alive(pid) {
		t.Fatal("Alive must keep describing the snapshot's moment")
	}
}

func TestProcessSnapshot_IdentityRecheck_Seam(t *testing.T) {
	sleepPID := startProcess(t, exec.Command("sleep", "30"))
	orig := kernProcPid

	cases := []struct {
		name string
		fake func(pid int) (*unix.KinfoProc, error)
	}{
		{"start time differs", func(pid int) (*unix.KinfoProc, error) {
			kp, err := orig(pid)
			if err != nil {
				return nil, err
			}
			kp.Proc.P_starttime.Sec++
			return kp, nil
		}},
		{"pid gone", func(pid int) (*unix.KinfoProc, error) { return nil, unix.EIO }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := takeSnapshot(t)
			var calls atomic.Int64
			kernProcPid = func(pid int) (*unix.KinfoProc, error) {
				calls.Add(1)
				return tc.fake(pid)
			}
			t.Cleanup(func() { kernProcPid = orig })

			for i := range 2 {
				if _, err := snap.Read(sleepPID); !errors.Is(err, ErrProcessChanged) {
					t.Fatalf("Read #%d: err = %v, want ErrProcessChanged", i+1, err)
				}
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("identity re-checked %d times over two Reads, want 1 (the failure is remembered)", got)
			}
		})
	}
}

func TestProcessSnapshot_MissingPID(t *testing.T) {
	snap := takeSnapshot(t)

	// macOS PIDs stop at 99999, so 999999 is never in the table.
	if snap.Alive(999999) {
		t.Fatal("Alive(999999) = true")
	}
	if _, err := snap.Read(999999); !errors.Is(err, ErrNotInSnapshot) {
		t.Fatalf("Read(999999): err = %v, want ErrNotInSnapshot", err)
	}
	if _, err := snap.StartTime(999999); !errors.Is(err, ErrNotInSnapshot) {
		t.Fatalf("StartTime(999999): err = %v, want ErrNotInSnapshot", err)
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

// procArgs2Buf builds a kern.procargs2 buffer: argc, then the raw bytes of
// the argument area (exec path, padding, strings), as the kernel lays it out.
func procArgs2Buf(argc int32, area string) []byte {
	buf := binary.NativeEndian.AppendUint32(nil, uint32(argc))
	return append(buf, area...)
}

func TestParseProcArgs(t *testing.T) {
	cases := []struct {
		name   string
		buf    []byte
		want   []string
		wantOK bool
	}{
		{"nil", nil, nil, false},
		{"shorter than argc", []byte{1, 0, 0}, nil, false},
		{"argc zero", procArgs2Buf(0, "/bin/x\x00\x00x\x00"), nil, false},
		{"argc negative", procArgs2Buf(-1, "/bin/x\x00\x00x\x00"), nil, false},
		{"exec path without NUL", procArgs2Buf(1, "/bin/x"), nil, false},
		{"only padding after exec path", procArgs2Buf(1, "/bin/x\x00\x00\x00\x00"), nil, false},
		{"argc larger than the strings present", procArgs2Buf(3, "/bin/x\x00\x00a\x00b\x00"), nil, false},
		{"last string unterminated", procArgs2Buf(2, "/bin/x\x00\x00a\x00b"), nil, false},
		{"huge argc", procArgs2Buf(1<<31-1, "/bin/x\x00\x00a\x00"), nil, false},
		{"plain", procArgs2Buf(2, "/bin/x\x00\x00\x00\x00x\x00-v\x00HOME=/h\x00"), []string{"x", "-v"}, true},
		{"no padding", procArgs2Buf(1, "/bin/x\x00x\x00"), []string{"x"}, true},
		{"empty exec path", procArgs2Buf(1, "\x00\x00x\x00"), []string{"x"}, true},
		{"empty middle argument", procArgs2Buf(3, "/bin/x\x00\x00a\x00\x00b\x00"), []string{"a", "", "b"}, true},
		// ps skips every NUL after the exec path, so an empty argv[0] is
		// swallowed as padding and counting starts at argv[1], running one
		// string into the environment. Matching that keeps comm/args equal
		// to ps's.
		{"empty argv0", procArgs2Buf(3, "/bin/x\x00\x00\x00x\x00y\x00HOME=/h\x00"), []string{"x", "y", "HOME=/h"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseProcArgs(tc.buf)
			if ok != tc.wantOK || !slices.Equal(got, tc.want) {
				t.Fatalf("parseProcArgs = %q, %v; want %q, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// A procargs2 buffer the parser rejects, or one the kernel refuses, is read
// through ps instead: same fields, two forks.
func TestProcessSnapshot_UnreadableProcArgs_FallsBackToPS(t *testing.T) {
	orig := kernProcArgs2
	cases := []struct {
		name string
		fake func(pid int) ([]byte, error)
	}{
		{"kernel refuses", func(int) ([]byte, error) { return nil, unix.EINVAL }},
		{"malformed buffer", func(int) ([]byte, error) { return procArgs2Buf(5, "/bin/x\x00\x00a\x00"), nil }},
		{"empty buffer", func(int) ([]byte, error) { return nil, nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kernProcArgs2 = tc.fake
			t.Cleanup(func() { kernProcArgs2 = orig })
			snap := takeSnapshot(t)

			forks := countPSForks(t)
			if _, err := snap.Read(os.Getpid()); err != nil {
				t.Fatalf("snap.Read(self): %v", err)
			}
			if got := forks.Load(); got != 2 {
				t.Fatalf("fallback Read forked ps %d times, want 2 (comm, args)", got)
			}
			requireSameAsPerPIDReader(t, snap, os.Getpid())
		})
	}
}

func TestProcessSnapshot_ForkCount_PerPIDReaderIsCounted(t *testing.T) {
	forks := countPSForks(t)
	if _, err := ReadProcessInfo(os.Getpid()); err != nil {
		t.Fatalf("ReadProcessInfo(self): %v", err)
	}
	if got := forks.Load(); got != 4 {
		t.Fatalf("ReadProcessInfo(self) forked ps %d times, want 4 (comm, args, lstart, ppid)", got)
	}
}
