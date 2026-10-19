package agent

import "testing"

// Two inventory passes each take their own snapshot; the process that only ps can read costs one comm+args pair, not one per pass.
func TestCachedReads_SecondPassOverANewSnapshotDoesNotForkPS(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	pid := startHelper(t, []string{"snap-helper", "中文"})
	forks := countPSForks(t)

	first, err := CachedReads(takeSnapshot(t)).Read(pid)
	if err != nil {
		t.Fatalf("first pass Read: %v", err)
	}
	if got := forks.Load(); got != 2 {
		t.Fatalf("first pass forked ps %d times, want 2 (comm, args)", got)
	}
	forks.Store(0)
	second, err := CachedReads(takeSnapshot(t)).Read(pid)
	if err != nil {
		t.Fatalf("second pass Read: %v", err)
	}
	if got := forks.Load(); got != 0 {
		t.Fatalf("second pass over a new snapshot forked ps %d times, want 0", got)
	}
	if len(first.Argv) != len(second.Argv) || first.ExePath != second.ExePath {
		t.Fatalf("second pass says %+v, first said %+v", second, first)
	}
}

// A different process (another pid and start time, so another key) is read again.
func TestCachedReads_AnotherProcessIsReadAgain(t *testing.T) {
	psArgsReset()
	t.Cleanup(psArgsReset)
	a := startHelper(t, []string{"snap-helper", "中文"})
	b := startHelper(t, []string{"snap-helper", "中文"})
	forks := countPSForks(t)
	v := CachedReads(takeSnapshot(t))
	if _, err := v.Read(a); err != nil {
		t.Fatal(err)
	}
	forks.Store(0)
	if _, err := v.Read(b); err != nil {
		t.Fatal(err)
	}
	if got := forks.Load(); got != 2 {
		t.Fatalf("a process not read before forked ps %d times, want 2", got)
	}
}

func TestCachedReads_NonSnapshotViewIsUnchanged(t *testing.T) {
	var v ProcessView = SharedProcessView{}
	if CachedReads(v) != v {
		t.Fatal("a view that is not a *ProcessSnapshot must be returned as it is")
	}
}
