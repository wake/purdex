package agent

import (
	"errors"
	"testing"
	"time"
)

// Start answers from the snapshot entry alone: it must not read argv (which
// can fork ps for non-ASCII argv or another user's process).
func TestSnapshotStart_NoArgvRead(t *testing.T) {
	want := time.Date(2026, 10, 9, 8, 7, 6, 0, time.Local)
	parseErr := errors.New("lstart does not parse")
	snap := &ProcessSnapshot{procs: map[int]*snapshotEntry{
		// identity is zero, so a Read of this pid would fail its re-check.
		10: {ppid: 1, lstart: "x", start: want},
		20: {ppid: 1, lstart: "garbled", startErr: parseErr},
	}}

	got, err := snap.Start(10)
	if err != nil || !got.Equal(want) {
		t.Fatalf("Start(10) = %v, %v; want %v, nil", got, err, want)
	}
	if snap.procs[10].argsRead {
		t.Fatal("Start read the argv")
	}

	if _, err := snap.Start(20); !errors.Is(err, parseErr) {
		t.Fatalf("Start(20) error = %v, want the entry's startErr", err)
	}
	if snap.procs[20].argsRead {
		t.Fatal("Start read the argv of an entry with a bad start")
	}

	if _, err := snap.Start(30); !errors.Is(err, ErrNotInSnapshot) {
		t.Fatalf("Start(30) error = %v, want ErrNotInSnapshot", err)
	}
	if _, err := snap.Start(0); err == nil {
		t.Fatal("Start(0) must fail")
	}
}
