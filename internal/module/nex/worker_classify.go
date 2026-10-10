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
// waiting/idle/error notifies, plus a waiting worker whose pending request id changed (a new needs-you). The key
// is what a consumer dedupes on: (exec, request_id) for waiting, (exec, "idle"|"error", turn_count) otherwise.
func classifyWorker(execID string, prev *rowDigest, cur rowDigest) (status workerStatus, key string, notify bool) {
	status = projectWorker(cur)
	if prev == nil {
		return status, "", false
	}
	before := projectWorker(*prev)
	switch status {
	case workerWaiting:
		if before == workerWaiting && prev.PermissionRequest == cur.PermissionRequest {
			return status, "", false
		}
		return status, execID + "|waiting|" + cur.PermissionRequest, true
	case workerIdle, workerError:
		if before == status {
			return status, "", false
		}
		return status, execID + "|" + string(status) + "|" + strconv.FormatInt(cur.TurnCount, 10), true
	}
	return status, "", false
}
