package nex

import "strconv"

// The worker status classifier (#2142 PW-1). It is the Go twin of the SPA's projectWorkerStatus
// (spa/src/lib/nex/worker-agent-status.ts) read the way a list row feeds it (useWorkerAgentProjection.ts
// deriveSource): turnLive = state running, lastOutcome failed only when state is failed, awaitingApproval =
// isAwaitingApproval (worker-summary.ts). Both sides run testdata/worker-status-cases.json; change one, change
// the other and the fixture.

type workerStatus string

const (
	workerClear   workerStatus = "clear"
	workerRunning workerStatus = "running"
	workerWaiting workerStatus = "waiting" // needs-you: a permission request is pending
	workerIdle    workerStatus = "idle"    // done
	workerError   workerStatus = "error"   // failed or rejected
)

// pending reports whether the row carries a pending_permission (even one with a blank request id).
func (d rowDigest) pending() bool { return d.PermissionRequest != "" || d.PermissionBlank }

// projectWorker maps a row digest to the status the Mac shows. First match wins, as in the SPA.
func projectWorker(d rowDigest) workerStatus {
	switch {
	case d.Archived || d.State == "terminated":
		return workerClear
	case d.State == "rejected":
		return workerError
	case d.pending() && d.State != "failed": // isAwaitingApproval; archived/terminated/rejected are handled above
		return workerWaiting
	case d.State == "running" || d.State == "queued":
		return workerRunning
	case d.State == "failed":
		return workerError
	default:
		return workerIdle
	}
}

// classifyWorker decides whether the step from prev to cur is a notification. prev nil (the execution was never
// seen: the first read is a baseline, as an epoch seed is) never notifies. Only a change of projected status into
// waiting/idle/error notifies, plus two steps inside one status:
//   - waiting to waiting with another pending request id (a new needs-you);
//   - idle to idle with a larger turn_count (a whole turn ran and ended between two reads: a quick turn inside one
//     coalescing window, or the first turn after a daemon restart). A smaller turn_count (a reset) or an equal one
//     is not a turn; the caller still records cur, so the next comparison uses the new baseline. error to error is
//     not loosened.
//
// Done and failed otherwise follow a turn that was running or waiting; from clear (unarchive) nothing new happened.
// The key is what a consumer dedupes on: (exec, request_id) for waiting, (exec, "idle"|"error", turn_count) otherwise.
func classifyWorker(execID string, prev *rowDigest, cur rowDigest) (status workerStatus, key string, notify bool) {
	status = projectWorker(cur)
	if prev == nil {
		return status, "", false
	}
	before := projectWorker(*prev)
	switch status {
	case workerWaiting:
		// The same request the earlier row already carried is not new, whatever status that row projected to
		// (archive then unarchive goes through clear and back).
		if prev.pending() && prev.PermissionRequest == cur.PermissionRequest {
			return status, "", false
		}
		key = execID + "|waiting|" + cur.PermissionRequest
		if cur.PermissionRequest == "" { // blank request id: turn_count is all that tells two apart
			key += "|turn" + strconv.FormatInt(cur.TurnCount, 10)
		}
		return status, key, true
	case workerIdle, workerError:
		// Done and failed follow a turn that was running or waiting. From clear (unarchive), or from idle/error
		// (failed to idle without a running between), nothing new happened.
		turnRan := status == workerIdle && before == workerIdle && cur.TurnCount > prev.TurnCount
		if before != workerRunning && before != workerWaiting && !turnRan {
			return status, "", false
		}
		return status, execID + "|" + string(status) + "|" + strconv.FormatInt(cur.TurnCount, 10), true
	}
	return status, "", false
}
