package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func withSharedSeams(t *testing.T) (calls *int, now *time.Time, fail *bool) {
	t.Helper()
	oldNow, oldSnap := sharedNow, sharedSnapshot
	shared.snap = nil
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
	t.Cleanup(func() { sharedNow, sharedSnapshot = oldNow, oldSnap; shared.snap = nil })
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

func TestSharedProcessSnapshot_AFailedReadIsNotKept(t *testing.T) {
	calls, _, fail := withSharedSeams(t)
	*fail = true
	if _, err := SharedProcessSnapshot(context.Background()); err == nil {
		t.Fatal("want the read's error")
	}
	*fail = false
	if _, err := SharedProcessSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("%d reads, want 2 (the failure is retried at once)", *calls)
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
