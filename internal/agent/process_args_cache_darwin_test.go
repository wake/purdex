package agent

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// cacheClock moves psArgsNow for the rest of the test.
func cacheClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Now()
	orig := psArgsNow
	psArgsNow = func() time.Time { return now }
	t.Cleanup(func() { psArgsNow = orig })
	return &now
}

func readFresh(t *testing.T, snap *ProcessSnapshot, pid int) ProcessInfo {
	t.Helper()
	info, err := snap.ReadFresh(pid)
	if err != nil {
		t.Fatalf("ReadFresh(%d): %v", pid, err)
	}
	return info
}

// The shared snapshot keeps no answer, so a process that only ps can read used to cost two forks on every read. Now the second read within
// the TTL costs none, and says what the first said and what the per-PID reader says.
func TestPSArgsCache_ExoticArgvIsReadByPSOnce(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	pid := startHelper(t, []string{"snap-helper", "中文"})
	forks := countPSForks(t)
	snap := takeSnapshot(t)

	first := readFresh(t, snap, pid)
	if got := forks.Load(); got != 2 {
		t.Fatalf("first ReadFresh of a non-ASCII argv forked ps %d times, want 2 (comm, args)", got)
	}
	forks.Store(0)
	second := readFresh(t, snap, pid)
	if got := forks.Load(); got != 0 {
		t.Fatalf("second ReadFresh forked ps %d times, want 0 (kept)", got)
	}
	if first.ExePath != second.ExePath || !slices.Equal(first.Argv, second.Argv) || !first.StartTime.Equal(second.StartTime) {
		t.Fatalf("a hit says %+v, the read said %+v", second, first)
	}
	want, err := ReadProcessInfo(pid)
	if err != nil {
		t.Fatalf("ReadProcessInfo: %v", err)
	}
	if second.ExePath != want.ExePath || !slices.Equal(second.Argv, want.Argv) {
		t.Fatalf("a hit says %q %q, the per-PID reader says %q %q", second.ExePath, second.Argv, want.ExePath, want.Argv)
	}
}

// Only what the fast path cannot answer comes to the cache.
func TestPSArgsCache_AnArgvTheFastPathTakesIsNotKept(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	pid := startProcess(t, exec.Command("sleep", "30"))
	forks := countPSForks(t)
	snap := takeSnapshot(t)
	readFresh(t, snap, pid)
	readFresh(t, snap, pid)
	if got := forks.Load(); got != 0 {
		t.Fatalf("ReadFresh of an ASCII argv forked ps %d times, want 0", got)
	}
	psArgsMu.Lock()
	n := len(psArgsStore)
	psArgsMu.Unlock()
	if n != 0 {
		t.Fatalf("the cache holds %d entries after reading an ASCII argv, want 0", n)
	}
}

// A hit that came back after the TTL would be a ps answer of any age.
func TestPSArgsCache_AnswerExpires(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	now := cacheClock(t)
	pid := startHelper(t, []string{"snap-helper", "中文"})
	forks := countPSForks(t)
	snap := takeSnapshot(t)

	readFresh(t, snap, pid)
	forks.Store(0)
	*now = now.Add(psArgsCacheTTL - time.Second)
	readFresh(t, snap, pid)
	if got := forks.Load(); got != 0 {
		t.Fatalf("a read just inside the TTL forked ps %d times, want 0", got)
	}
	*now = now.Add(2 * time.Second)
	readFresh(t, snap, pid)
	if got := forks.Load(); got != 2 {
		t.Fatalf("a read after the TTL forked ps %d times, want 2 (read again)", got)
	}
}

// ps failing says nothing about the process, so nothing is kept.
func TestPSArgsCache_FailureIsNotKept(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	pid := startHelper(t, []string{"snap-helper", "中文"})
	snap := takeSnapshot(t)

	orig := runPS
	runPS = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("ps is not available") }
	if _, err := snap.ReadFresh(pid); err == nil {
		t.Fatal("ReadFresh succeeded though ps failed")
	}
	runPS = orig
	psArgsMu.Lock()
	n := len(psArgsStore)
	psArgsMu.Unlock()
	if n != 0 {
		t.Fatalf("the cache holds %d entries after a ps failure, want 0", n)
	}
	forks := countPSForks(t)
	readFresh(t, snap, pid)
	if got := forks.Load(); got != 2 {
		t.Fatalf("the read after the failure forked ps %d times, want 2", got)
	}
}

// A pid taken by another process is not the process the answer was kept for: the snapshot's start time is part of the key, and the start
// time of the process that is there is compared before anything is answered.
func TestPSArgsCache_ReusedPidFindsNoEntry(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	pid := startHelper(t, []string{"snap-helper", "中文"})
	snap := takeSnapshot(t)
	readFresh(t, snap, pid) // kept for the process the snapshot saw

	forks := countPSForks(t)
	orig := kernProcPid
	kernProcPid = func(p int) (*unix.KinfoProc, error) {
		kp, err := orig(p)
		if err == nil && p == pid {
			kp.Proc.P_starttime.Usec += 7 // another process, with the pid of this one
		}
		return kp, err
	}
	t.Cleanup(func() { kernProcPid = orig })

	if _, err := snap.ReadFresh(pid); !errors.Is(err, ErrProcessChanged) {
		t.Fatalf("ReadFresh of a reused pid = %v, want ErrProcessChanged", err)
	}
	if got := forks.Load(); got != 0 {
		t.Fatalf("a reused pid forked ps %d times, want 0 (the answer cannot change)", got)
	}
}

// An exec keeps the pid and the start time; the kernel's command name is what tells the new program.
func TestPSArgsCache_ChangedCommandNameFindsNoEntry(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	pid := startHelper(t, []string{"snap-helper", "中文"})
	snap := takeSnapshot(t)
	readFresh(t, snap, pid)

	forks := countPSForks(t)
	orig := kernProcPid
	kernProcPid = func(p int) (*unix.KinfoProc, error) {
		kp, err := orig(p)
		if err == nil && p == pid {
			kp.Proc.P_comm[0] ^= 0x20
		}
		return kp, err
	}
	t.Cleanup(func() { kernProcPid = orig })

	readFresh(t, snap, pid)
	if got := forks.Load(); got != 2 {
		t.Fatalf("a process whose command name changed forked ps %d times, want 2 (read again)", got)
	}
}

// The cache is bounded; an entry that is still current is not what makes room for the 65th, the store starts over.
func TestPSArgsCache_IsBounded(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	for i := 0; i < psArgsCacheMax*2; i++ {
		psArgsPut(psArgsKey{pid: i + 1}, "/x", []string{"x"})
		psArgsMu.Lock()
		n := len(psArgsStore)
		psArgsMu.Unlock()
		if n > psArgsCacheMax {
			t.Fatalf("after %d puts the cache holds %d entries, max %d", i+1, n, psArgsCacheMax)
		}
	}
}

// A caller that edits its Argv does not change the next answer.
func TestPSArgsCache_AnswerIsACopy(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	k := psArgsKey{pid: 1}
	psArgsPut(k, "/x", []string{"a", "b"})
	_, got, _ := psArgsGet(k)
	got[0] = "changed"
	_, again, ok := psArgsGet(k)
	if !ok || !slices.Equal(again, []string{"a", "b"}) {
		t.Fatalf("second answer = %q, %v; want [a b]", again, ok)
	}
}
