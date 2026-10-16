package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

func withSharedSeams(t *testing.T) (calls *int, now *time.Time, fail *bool) {
	t.Helper()
	oldNow, oldSnap := sharedNow, sharedSnapshot
	resetShared()
	n := 0
	clock := time.Unix(1_000_000, 0)
	f := false
	sharedNow = func() time.Time { return clock }
	sharedSnapshot = func(context.Context) (*ProcessSnapshot, error) {
		n++
		if f {
			return nil, errors.New("sysctl failed")
		}
		return &ProcessSnapshot{procs: map[int]*snapshotEntry{1: {ppid: 0}}}, nil
	}
	t.Cleanup(func() { sharedNow, sharedSnapshot = oldNow, oldSnap; resetShared() })
	return &n, &clock, &f
}

// G3a: however many readers ask inside the TTL, the process table is read once; after it, once more.
func TestSharedProcessSnapshot_OneReadPerTTL(t *testing.T) {
	calls, now, _ := withSharedSeams(t)
	for i := 0; i < 50; i++ {
		if _, err := SharedProcessSnapshot(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if *calls != 1 {
		t.Fatalf("50 readers inside the TTL read the table %d times, want 1", *calls)
	}
	*now = now.Add(SharedSnapshotTTL - time.Millisecond)
	_, _ = SharedProcessSnapshot(context.Background())
	if *calls != 1 {
		t.Fatalf("just inside the TTL: %d reads, want 1", *calls)
	}
	*now = now.Add(2 * time.Millisecond)
	_, _ = SharedProcessSnapshot(context.Background())
	if *calls != 2 {
		t.Fatalf("after the TTL: %d reads, want 2", *calls)
	}
}

// releaseAndDrain lets a blocked read return and waits until its goroutine is done with the shared state, so a test's seams are
// not restored under it.
func releaseAndDrain(release chan struct{}) {
	close(release)
	for i := 0; i < 500; i++ {
		shared.mu.Lock()
		idle := shared.inflight == nil
		shared.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func resetShared() {
	shared.mu.Lock()
	shared.snap, shared.inflight, shared.failErr = nil, nil, nil
	shared.mu.Unlock()
}

// A failed read is answered to everyone for sharedFailBackoff (no retry storm), then tried again.
func TestSharedProcessSnapshot_AFailureIsSharedThenRetried(t *testing.T) {
	calls, now, fail := withSharedSeams(t)
	*fail = true
	for i := 0; i < 5; i++ {
		if _, err := SharedProcessSnapshot(context.Background()); err == nil {
			t.Fatal("want the read's error")
		}
	}
	if *calls != 1 {
		t.Fatalf("5 callers after a failure read the table %d times, want 1", *calls)
	}
	*fail = false
	*now = now.Add(sharedFailBackoff + time.Millisecond)
	if _, err := SharedProcessSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("%d reads, want 2 (retried after the backoff)", *calls)
	}
}

// A read that does not return holds no lock: a waiter gives up on its own ctx, and the callers that come after wait for the
// same read rather than starting one each.
func TestSharedProcessSnapshot_ABlockedReadBlocksNoLockAndIsNotStartedTwice(t *testing.T) {
	_, _, _ = withSharedSeams(t)
	release := make(chan struct{})
	var started atomic.Int32
	sharedSnapshot = func(context.Context) (*ProcessSnapshot, error) {
		started.Add(1)
		<-release
		return &ProcessSnapshot{procs: map[int]*snapshotEntry{}}, nil
	}
	t.Cleanup(func() { releaseAndDrain(release) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err := SharedProcessSnapshot(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the first waiter: %v, want its own deadline", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := SharedProcessSnapshot(context.Background()); !errors.Is(err, errSharedReadSlow) {
			t.Fatalf("later caller %d: %v, want errSharedReadSlow", i, err)
		}
	}
	if n := started.Load(); n != 1 {
		t.Fatalf("%d reads started while one was in flight, want 1", n)
	}
}

// Once a waiter has given up on a read that does not return, the callers after it do not wait at all (a registry pass asks
// once per entry: N entries must not cost N waits).
func TestSharedProcessSnapshot_AfterOneWaiterGivesUpTheOthersFailAtOnce(t *testing.T) {
	_, _, _ = withSharedSeams(t)
	release := make(chan struct{})
	sharedSnapshot = func(context.Context) (*ProcessSnapshot, error) { <-release; return &ProcessSnapshot{}, nil }
	t.Cleanup(func() { releaseAndDrain(release) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, _ = SharedProcessSnapshot(ctx)
	cancel()
	start := time.Now()
	for i := 0; i < 50; i++ {
		if _, err := SharedProcessSnapshot(context.Background()); !errors.Is(err, errSharedReadSlow) {
			t.Fatalf("call %d: %v, want errSharedReadSlow", i, err)
		}
	}
	if time.Since(start) > time.Second {
		t.Fatalf("50 calls took %v, want no waiting", time.Since(start))
	}
}

// ReadFresh holds no lock of the snapshot while it reads (a slow `ps` fallback for one PID must not stop the others).
func TestProcessSnapshot_ReadFreshTakesNoSnapshotLock(t *testing.T) {
	snap := takeSnapshot(t)
	snap.mu.Lock()
	defer snap.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := snap.ReadFresh(os.Getpid()); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ReadFresh(self): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadFresh waits for the snapshot's lock")
	}
}

func TestSharedProcessView_AnswersFromTheSharedTable(t *testing.T) {
	calls, _, _ := withSharedSeams(t)
	v := SharedProcessView{}
	if !v.Alive(1) || v.Alive(2) {
		t.Fatal("Alive disagrees with the table")
	}
	if p, err := v.PPID(1); err != nil || p != 0 {
		t.Fatalf("PPID = %d, %v", p, err)
	}
	if _, err := v.PPID(99); !errors.Is(err, ErrNotInSnapshot) {
		t.Fatalf("an unknown pid: %v, want ErrNotInSnapshot", err)
	}
	if *calls != 1 {
		t.Fatalf("%d reads for four questions, want 1", *calls)
	}
}

// A snapshot shared for a TTL must not answer for a process that left (and whose PID may be reused) after an earlier Read:
// Read keeps its first answer, ReadFresh checks the process again every time (the shared view uses it).
func TestProcessSnapshot_ReadFreshRechecksWhatReadKept(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	pid := startProcess(t, cmd)
	snap := takeSnapshot(t)
	if _, err := snap.Read(pid); err != nil {
		t.Fatalf("first Read: %v", err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := snap.Read(pid); err != nil {
		t.Fatalf("Read keeps its first answer, got %v", err)
	}
	if _, err := snap.ReadFresh(pid); !errors.Is(err, ErrProcessChanged) {
		t.Fatalf("ReadFresh of a process that left after an earlier Read: %v, want ErrProcessChanged", err)
	}
}
