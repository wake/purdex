package codexbroker

import (
	"context"
	"strings"
	"time"
)

// DefaultStaleRunningThreshold is the spec §5.2 default for when a
// running-state job has been silent long enough to count as
// terminal-abandoned absent positive process evidence.
const DefaultStaleRunningThreshold = 1 * time.Hour

// brokerTaskWorkerCmdlineMarker is the substring we expect to find in
// a live broker task-worker process's cmdline. Anything not containing
// this marker is treated as PID-reuse and stale per spec §5.2 line 389.
const brokerTaskWorkerCmdlineMarker = "app-server-broker.mjs"

// IsStaleRunning implements spec §5.2 stale-running detection for one job.
// A job that is not in "running" state is never stale (the rule does not
// apply). For running jobs:
//
//   - pid == nil and (now - updatedAt) > threshold → stale.
//   - pid != nil and lister.List does not return a row for that pid
//     → stale (process dead).
//   - pid != nil and lister returns a row whose cmdline does not contain
//     the broker task-worker marker → stale (PID reuse defence).
//   - otherwise → not stale.
//
// detail is a short human-readable hint for the audit trace. lister errors
// are treated as "process not found" (most permissive interpretation that
// defends correctness — better to call a busy process stale and have
// predicate A fall to false than to mistakenly keep a dead broker alive).
func IsStaleRunning(job StateJobLite, lister ProcessLister, threshold time.Duration, now time.Time) (bool, string) {
	if job.Status != "running" {
		return false, "not-running"
	}
	if job.Pid == nil {
		if now.Sub(job.UpdatedAt) > threshold {
			return true, "nil-pid-past-threshold"
		}
		return false, "nil-pid-within-threshold"
	}
	pid := *job.Pid
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	rows, err := lister.List(ctx)
	if err != nil {
		// Conservative: if we can't enumerate, treat as stale so a busy
		// broker has to prove itself via the RPC path instead.
		return true, "lister-error"
	}
	for _, row := range rows {
		if row.PID != pid {
			continue
		}
		if strings.Contains(row.Cmdline, brokerTaskWorkerCmdlineMarker) {
			return false, "alive-broker-cmdline"
		}
		return true, "alive-cmdline-mismatch"
	}
	return true, "pid-not-found"
}

// StaleRunningCount counts how many jobs are stale per IsStaleRunning. Used
// by the decision layer to roll up a per-broker stale-running diagnostic.
func StaleRunningCount(jobs []StateJobLite, lister ProcessLister, threshold time.Duration, now time.Time) int {
	n := 0
	for _, job := range jobs {
		if stale, _ := IsStaleRunning(job, lister, threshold, now); stale {
			n++
		}
	}
	return n
}
