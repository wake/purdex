package codexbroker

import (
	"testing"
	"time"
)

// fakeStaleLister returns the rows it was constructed with, ignoring ctx.
// Reused across staleness/decision tests; unique name distinguishes from
// FakeProcessLister already exported in process.go (which we still use too —
// this one is just shorter to construct inline).

func intPtr(v int) *int       { return &v }
func timePtr(t time.Time) *time.Time { return &t }

// TestIsStaleRunning_NilPidBeyondThreshold — pid is nil and updatedAt is
// older than the staleness threshold → stale.
func TestIsStaleRunning_NilPidBeyondThreshold(t *testing.T) {
	now := time.Now()
	job := StateJobLite{
		ID:        "j1",
		Status:    "running",
		UpdatedAt: now.Add(-2 * time.Hour),
		Pid:       nil,
	}
	stale, _ := IsStaleRunning(job, NewFakeProcessLister(nil), DefaultStaleRunningThreshold, now)
	if !stale {
		t.Errorf("expected stale=true for nil pid past threshold")
	}
}

// TestIsStaleRunning_NilPidWithinThreshold — pid is nil but updatedAt is
// fresh → not stale (might still be a momentary lag).
func TestIsStaleRunning_NilPidWithinThreshold(t *testing.T) {
	now := time.Now()
	job := StateJobLite{
		ID:        "j1",
		Status:    "running",
		UpdatedAt: now.Add(-5 * time.Minute),
		Pid:       nil,
	}
	stale, _ := IsStaleRunning(job, NewFakeProcessLister(nil), DefaultStaleRunningThreshold, now)
	if stale {
		t.Errorf("expected stale=false for nil pid within threshold")
	}
}

// TestIsStaleRunning_NonNilPidDead — pid is non-nil but lister returns no
// matching row → stale.
func TestIsStaleRunning_NonNilPidDead(t *testing.T) {
	now := time.Now()
	job := StateJobLite{
		ID:        "j1",
		Status:    "running",
		UpdatedAt: now.Add(-30 * time.Minute),
		Pid:       intPtr(4321),
	}
	stale, _ := IsStaleRunning(job, NewFakeProcessLister(nil), DefaultStaleRunningThreshold, now)
	if !stale {
		t.Errorf("expected stale=true for dead pid")
	}
}

// TestIsStaleRunning_NonNilPidAliveWrongCmdline — pid alive but cmdline does
// not look like a broker task-worker → treat as stale (PID reuse defence).
func TestIsStaleRunning_NonNilPidAliveWrongCmdline(t *testing.T) {
	now := time.Now()
	job := StateJobLite{
		ID:        "j1",
		Status:    "running",
		UpdatedAt: now.Add(-30 * time.Minute),
		Pid:       intPtr(4321),
	}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Cmdline: "/usr/bin/zsh -i",
	}})
	stale, _ := IsStaleRunning(job, lister, DefaultStaleRunningThreshold, now)
	if !stale {
		t.Errorf("expected stale=true when cmdline doesn't match broker shape")
	}
}

// TestIsStaleRunning_NonNilPidAliveCorrectCmdline — pid alive and cmdline
// matches broker shape → not stale.
func TestIsStaleRunning_NonNilPidAliveCorrectCmdline(t *testing.T) {
	now := time.Now()
	job := StateJobLite{
		ID:        "j1",
		Status:    "running",
		UpdatedAt: now.Add(-30 * time.Minute),
		Pid:       intPtr(4321),
	}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Cmdline: "node /opt/codex/dist/app-server-broker.mjs serve --cwd /tmp/x",
	}})
	stale, _ := IsStaleRunning(job, lister, DefaultStaleRunningThreshold, now)
	if stale {
		t.Errorf("expected stale=false for alive broker pid")
	}
}

// TestStaleRunningCount_MixedJobs — 3 jobs of which 2 are stale → 2.
func TestStaleRunningCount_MixedJobs(t *testing.T) {
	now := time.Now()
	jobs := []StateJobLite{
		{ID: "j1", Status: "running", UpdatedAt: now.Add(-2 * time.Hour), Pid: nil},                                    // stale (nil + past threshold)
		{ID: "j2", Status: "running", UpdatedAt: now.Add(-30 * time.Minute), Pid: intPtr(99)},                          // stale (pid dead)
		{ID: "j3", Status: "running", UpdatedAt: now.Add(-1 * time.Minute), Pid: nil},                                  // not stale
		{ID: "j4", Status: "completed", UpdatedAt: now.Add(-2 * time.Hour), CompletedAt: timePtr(now.Add(-1 * time.Hour))}, // not running, not counted
	}
	got := StaleRunningCount(jobs, NewFakeProcessLister(nil), DefaultStaleRunningThreshold, now)
	if got != 2 {
		t.Errorf("StaleRunningCount = %d, want 2", got)
	}
}
