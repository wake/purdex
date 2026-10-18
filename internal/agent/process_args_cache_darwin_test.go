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

func readCached(t *testing.T, snap *ProcessSnapshot, pid int) ProcessInfo {
	t.Helper()
	info, err := snap.ReadCached(pid)
	if err != nil {
		t.Fatalf("ReadCached(%d): %v", pid, err)
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

	first := readCached(t, snap, pid)
	if got := forks.Load(); got != 2 {
		t.Fatalf("first ReadCached of a non-ASCII argv forked ps %d times, want 2 (comm, args)", got)
	}
	forks.Store(0)
	second := readCached(t, snap, pid)
	if got := forks.Load(); got != 0 {
		t.Fatalf("second ReadCached forked ps %d times, want 0 (kept)", got)
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

// ReadFresh is for a caller that classifies by Argv: it asks ps on every call and keeps nothing, however often it is asked.
func TestPSArgsCache_ReadFreshNeverUsesIt(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	pid := startHelper(t, []string{"snap-helper", "中文"})
	forks := countPSForks(t)
	snap := takeSnapshot(t)
	for i := 1; i <= 2; i++ {
		forks.Store(0)
		if _, err := snap.ReadFresh(pid); err != nil {
			t.Fatalf("ReadFresh #%d: %v", i, err)
		}
		if got := forks.Load(); got != 2 {
			t.Fatalf("ReadFresh #%d forked ps %d times, want 2 (comm, args) every time", i, got)
		}
	}
	psArgsMu.Lock()
	n := len(psArgsStore)
	psArgsMu.Unlock()
	if n != 0 {
		t.Fatalf("ReadFresh left %d entries in the cache, want 0", n)
	}
}

// The registry's liveness reads through the shared view: that is the caller the cache is for.
func TestPSArgsCache_SharedViewReadIsTheCachedRead(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	pid := startHelper(t, []string{"snap-helper", "中文"})
	forks := countPSForks(t)
	view := SharedProcessView{}
	waitFor(t, "the shared table to see the helper", func() bool { return view.Alive(pid) })
	if _, err := view.Read(pid); err != nil {
		t.Fatalf("view.Read: %v", err)
	}
	forks.Store(0)
	if _, err := view.Read(pid); err != nil {
		t.Fatalf("second view.Read: %v", err)
	}
	if got := forks.Load(); got != 0 {
		t.Fatalf("the second SharedProcessView.Read forked ps %d times, want 0", got)
	}
}

// Only what the fast path cannot answer comes to the cache.
func TestPSArgsCache_AnArgvTheFastPathTakesIsNotKept(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	pid := startProcess(t, exec.Command("sleep", "30"))
	forks := countPSForks(t)
	snap := takeSnapshot(t)
	readCached(t, snap, pid)
	readCached(t, snap, pid)
	if got := forks.Load(); got != 0 {
		t.Fatalf("ReadCached of an ASCII argv forked ps %d times, want 0", got)
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

	readCached(t, snap, pid)
	forks.Store(0)
	*now = now.Add(psArgsCacheTTL - time.Second)
	readCached(t, snap, pid)
	if got := forks.Load(); got != 0 {
		t.Fatalf("a read just inside the TTL forked ps %d times, want 0", got)
	}
	*now = now.Add(2 * time.Second)
	readCached(t, snap, pid)
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
	if _, err := snap.ReadCached(pid); err == nil {
		t.Fatal("ReadCached succeeded though ps failed")
	}
	runPS = orig
	psArgsMu.Lock()
	n := len(psArgsStore)
	psArgsMu.Unlock()
	if n != 0 {
		t.Fatalf("the cache holds %d entries after a ps failure, want 0", n)
	}
	forks := countPSForks(t)
	readCached(t, snap, pid)
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
	forks := countPSForks(t) // before the first read: it empties the cache
	snap := takeSnapshot(t)
	readCached(t, snap, pid) // kept for the process the snapshot saw
	forks.Store(0)

	orig := kernProcPid
	kernProcPid = func(p int) (*unix.KinfoProc, error) {
		kp, err := orig(p)
		if err == nil && p == pid {
			kp.Proc.P_starttime.Usec += 7 // another process, with the pid of this one
		}
		return kp, err
	}
	t.Cleanup(func() { kernProcPid = orig })

	if _, err := snap.ReadCached(pid); !errors.Is(err, ErrProcessChanged) {
		t.Fatalf("ReadCached of a reused pid = %v, want ErrProcessChanged", err)
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
	forks := countPSForks(t) // before the first read: it empties the cache
	snap := takeSnapshot(t)
	readCached(t, snap, pid)
	forks.Store(0)

	orig := kernProcPid
	kernProcPid = func(p int) (*unix.KinfoProc, error) {
		kp, err := orig(p)
		if err == nil && p == pid {
			kp.Proc.P_comm[0] ^= 0x20
		}
		return kp, err
	}
	t.Cleanup(func() { kernProcPid = orig })

	readCached(t, snap, pid)
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

// ps asks comm and then args. A process that execs in between, with the same pid and start time, would give one answer half of each: it
// is told as a changed process, and nothing is kept for the program that is there now.
func TestPSArgsCache_ExecDuringPSIsNeitherReturnedNorKept(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	pid := startHelper(t, []string{"snap-helper", "中文"})
	snap := takeSnapshot(t)

	origKern := kernProcPid
	execed := false
	kernProcPid = func(p int) (*unix.KinfoProc, error) {
		kp, err := origKern(p)
		if err == nil && p == pid && execed {
			kp.Proc.P_comm[0] ^= 0x20 // what the kernel says after the exec
		}
		return kp, err
	}
	t.Cleanup(func() { kernProcPid = origKern })
	origPS := runPS
	runPS = func(ctx context.Context, args ...string) ([]byte, error) {
		execed = true // the exec happens while ps runs
		return origPS(ctx, args...)
	}
	t.Cleanup(func() { runPS = origPS })

	if _, err := snap.ReadCached(pid); !errors.Is(err, ErrProcessChanged) {
		t.Fatalf("ReadCached across an exec = %v, want ErrProcessChanged", err)
	}
	psArgsMu.Lock()
	n := len(psArgsStore)
	psArgsMu.Unlock()
	if n != 0 {
		t.Fatalf("the cache holds %d entries after a read across an exec, want 0", n)
	}
}
