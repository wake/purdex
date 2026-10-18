package facts

import (
	"errors"
	"testing"
	"time"
)

// The observation hook gets the whole process table of the sample, once, after the other builders; a failed process read gives it
// nothing (an empty or missing table must not look like "everything ended").
func TestCollectorHandsTheSnapshotToTheObserveHook(t *testing.T) {
	procsErr := error(nil)
	var calls int
	var gotAt time.Time
	var gotN int
	src := Sources{
		Now:   func() time.Time { return t0 },
		CPU:   func() (RawCPU, error) { return RawCPU{NCPU: 1}, nil },
		Mem:   func() (RawMem, error) { return fixtureMem(1, 0), nil },
		Disks: func() ([]RawDisk, error) { return nil, nil },
		Procs: func(time.Time) (ProcSnapshot, ProcContext, error) {
			if procsErr != nil {
				return ProcSnapshot{}, ProcContext{}, procsErr
			}
			return ProcSnapshot{Procs: []ProcRec{{PID: 1, Name: "launchd"}, {PID: 200, PPID: 1, Name: "make"}}}, ProcContext{}, nil
		},
		Observe: func(now time.Time, snap ProcSnapshot, c ProcContext) {
			calls++
			gotAt, gotN = now, len(snap.Procs)
			if c.Table == nil {
				t.Error("the table is built before the hook sees the context")
			}
		},
	}
	c := NewCollector(src)
	if _, err := c.Sample(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !gotAt.Equal(t0) || gotN != 2 {
		t.Fatalf("calls %d at %v with %d processes", calls, gotAt, gotN)
	}
	procsErr = errors.New("ps failed")
	if _, err := c.Sample(); err == nil {
		t.Fatal("the failure is reported")
	}
	if calls != 1 {
		t.Fatalf("the hook was called on a failed read: %d", calls)
	}
}

func TestCollectorWithoutAnObserveHookIsUnchanged(t *testing.T) {
	src := Sources{
		Now:   func() time.Time { return t0 },
		CPU:   func() (RawCPU, error) { return RawCPU{NCPU: 1}, nil },
		Mem:   func() (RawMem, error) { return fixtureMem(1, 0), nil },
		Disks: func() ([]RawDisk, error) { return nil, nil },
		Procs: func(time.Time) (ProcSnapshot, ProcContext, error) { return ProcSnapshot{}, ProcContext{}, nil },
	}
	if _, err := NewCollector(src).Sample(); err != nil {
		t.Fatal(err)
	}
}
