package run

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type fakeSource func(jobID string) ([]int, error)

func (f fakeSource) JobProcesses(_, jobID string) ([]int, error) { return f(jobID) }

// victim starts a process of the test's own that ignores TERM, in a group of its own, and reaps it in the background.
func victim(t *testing.T) (pid int, gone func() bool) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "trap '' TERM; sleep 30; sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var exited atomic.Bool
	go func() { cmd.Wait(); exited.Store(true) }()
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	time.Sleep(100 * time.Millisecond) // let the trap be installed
	return cmd.Process.Pid, exited.Load
}

func TestSweepTermsThenKillsWhatCarriesTheMark(t *testing.T) {
	pid, gone := victim(t)
	s := spec(t, "true")
	s.KillAfter = 400 * time.Millisecond
	s.Procs = fakeSource(func(job string) ([]int, error) {
		if job != "j_test1" {
			t.Errorf("scanned for %q", job)
		}
		if gone() {
			return nil, nil
		}
		return []int{pid}, nil
	})
	start := time.Now()
	r := Run(context.Background(), s)
	if r.Outcome != Exited || r.ExitCode != 0 {
		t.Fatalf("result = %+v", r)
	}
	if len(r.Escaped) != 1 || r.Escaped[0] != pid {
		t.Fatalf("escaped = %v, want [%d]", r.Escaped, pid)
	}
	if d := time.Since(start); d < 350*time.Millisecond {
		t.Fatalf("a TERM-ignoring process was gone after %v, before KillAfter: it was not given the grace", d)
	}
	if !gone() {
		t.Fatal("the escaped process is still running after Run returned")
	}
	if !strings.Contains(string(r.Tail), "leaks processes") || !strings.Contains(readFile(t, r.LogPath), "leaks processes") {
		t.Fatalf("the log does not say the task leaks: %q", r.Tail)
	}
}

func TestSweepDoesNotKillAPidThatNoLongerCarriesTheMark(t *testing.T) {
	pid, gone := victim(t)
	var calls int32
	s := spec(t, "true")
	s.KillAfter = 300 * time.Millisecond
	s.Procs = fakeSource(func(string) ([]int, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return []int{pid}, nil
		}
		return nil, nil // the second look: that pid is somebody else's now
	})
	r := Run(context.Background(), s)
	if len(r.Escaped) != 1 {
		t.Fatalf("escaped = %v", r.Escaped)
	}
	time.Sleep(100 * time.Millisecond)
	if gone() {
		t.Fatal("KILL was sent to a pid that did not carry the mark on the second look")
	}
}

func TestSweepReportsAnUnreadableTableOnTheSecondLookAndDoesNotKill(t *testing.T) {
	pid, gone := victim(t)
	var calls int32
	s := spec(t, "true")
	s.KillAfter = 300 * time.Millisecond
	s.Procs = fakeSource(func(string) ([]int, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return []int{pid}, nil
		}
		return nil, errors.New("sysctl failed")
	})
	r := Run(context.Background(), s)
	if r.SweepErr == nil || len(r.Escaped) != 1 {
		t.Fatalf("a table that cannot be read must be reported, not taken for clean: %+v", r)
	}
	time.Sleep(100 * time.Millisecond)
	if gone() {
		t.Fatal("KILL was sent without a confirming look at the table")
	}
}

func TestEscapedNoteIsWrittenEvenWhenTheLogHitsItsCap(t *testing.T) {
	pid, gone := victim(t)
	s := spec(t, "sh", "-c", "i=0; while [ $i -lt 500 ]; do echo line-$i; i=$((i+1)); done")
	s.MaxLog = 200
	s.KillAfter = 300 * time.Millisecond
	s.Procs = fakeSource(func(string) ([]int, error) {
		if gone() {
			return nil, nil
		}
		return []int{pid}, nil
	})
	r := Run(context.Background(), s)
	if !r.LogCut {
		t.Fatalf("the log should have hit its cap: %+v", r)
	}
	if log := readFile(t, r.LogPath); !strings.Contains(log, "leaks processes") {
		t.Fatalf("the leak line is missing from the log on disk: %q", log[max(0, len(log)-200):])
	}
}

func TestSweepIsBoundedInTime(t *testing.T) {
	old := sweepSlack
	sweepSlack = 200 * time.Millisecond
	defer func() { sweepSlack = old }()
	block := make(chan struct{})
	defer close(block)
	start := time.Now()
	pids, err := sweepBounded(fakeSource(func(string) ([]int, error) { <-block; return nil, nil }), "", "j_test1", 100*time.Millisecond)
	if err == nil || pids != nil {
		t.Fatalf("a table that never answers must give an error: %v %v", pids, err)
	}
	if d := time.Since(start); d > 100*time.Millisecond+sweepSlack+time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestAbandonedSweepSignalsNobodyWhenTheScanReturnsLate(t *testing.T) {
	pid, gone := victim(t)
	old := sweepSlack
	sweepSlack = 100 * time.Millisecond
	defer func() { sweepSlack = old }()
	release := make(chan struct{})
	_, err := sweepBounded(fakeSource(func(string) ([]int, error) { <-release; return []int{pid}, nil }), "", "j_test1", 100*time.Millisecond)
	if err == nil {
		t.Fatal("want a time budget error")
	}
	close(release) // the scan answers after Run gave up
	time.Sleep(500 * time.Millisecond)
	if gone() {
		t.Fatal("the abandoned sweep still ended a process")
	}
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("the process is gone")
	}
}

func TestSweepNothingFound(t *testing.T) {
	s := spec(t, "true")
	s.Procs = fakeSource(func(string) ([]int, error) { return nil, nil })
	r := Run(context.Background(), s)
	if len(r.Escaped) != 0 || r.SweepErr != nil || strings.Contains(string(r.Tail), "leaks") {
		t.Fatalf("result = %+v", r)
	}
}

func TestSweepErrorDoesNotChangeTheJobsResult(t *testing.T) {
	s := spec(t, "sh", "-c", "exit 4")
	s.Procs = fakeSource(func(string) ([]int, error) { return nil, errors.New("table unreadable") })
	r := Run(context.Background(), s)
	if r.Outcome != Exited || r.ExitCode != 4 || r.SweepErr == nil {
		t.Fatalf("result = %+v", r)
	}
}

func TestSweepNeverTouchesTheRunnersOwnPid(t *testing.T) {
	var once sync.Once
	s := spec(t, "true")
	s.Procs = fakeSource(func(string) ([]int, error) {
		var out []int
		once.Do(func() { out = []int{syscall.Getpid()} })
		return out, nil
	})
	r := Run(context.Background(), s)
	if len(r.Escaped) != 0 {
		t.Fatalf("escaped = %v", r.Escaped)
	}
}

func TestJobIdMustBeOneTheFactsRecognise(t *testing.T) {
	for _, id := range []string{"", "notajob", "j_", "j_has space", "j_x\nWWD_X=1"} {
		s := spec(t, "true")
		s.JobID = id
		if r := Run(context.Background(), s); r.Outcome != StartFailed {
			t.Errorf("job id %q: outcome = %s, want start_failed", id, r.Outcome)
		}
	}
}
