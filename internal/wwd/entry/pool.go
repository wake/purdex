package entry

import (
	"slices"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// GateOnPool keeps a rewrite that goes through the queue only when some host can take the job now. Without that, `pdx test` would end
// at once with exit 5 (no host holds the task's labels, e.g. aigora-v2's repo:aigora-v2 while no host has a clone of it) or the call would
// wait in the background up to the queue timeout for a host that is down: a command that fails, or waits for hours, where it would have run
// as typed. So the answer becomes none, and the hook lets the command run (the same fail-open the entry has everywhere).
//
// A host can take it when its labels hold all of the task's, it answers now, and its worker is on; an owner at the keyboard making the host
// yield is waiting, not a no. A host that answers but has not sent its first facts record is still announcing its labels: while one is, the
// answer is not "no" and the rewrite stays. A host that holds the labels but is down or has its worker off does not count: its last known labels would
// only say the setting exists. A pool that cannot be read is none too (wwd is not answering, and `pdx test` would exit 3).
//
// Only a rewrite that is queued is judged: the local fast lane and the ad-hoc job need no other host (a queued task with no labels needs any
// worker that is on and reachable).
//
// Known gap: the pool does not show a host's `explicit_labels_only` (a worker setting: mserv takes only jobs whose labels name its os: label
// or a task: label it earned). Labels that such a host satisfies, while it will not take the job, still pass here, and the call then waits
// in the background until the queue timeout. Purdex does not meet it: purdex_manifest_test makes every queued task carry os:darwin or its own
// task: label. When another repo does, the setting has to become a field of the host's info.
func GateOnPool(r Result, pool wire.Pool, poolErr error) Result {
	if !NeedsPool(r) {
		return r
	}
	if poolErr != nil {
		return Result{Action: ActionNone, Reason: "wwd pool cannot be read (" + firstLine(poolErr.Error()) + "), so the command runs as typed"}
	}
	settling := false
	for _, p := range pool.Peers {
		if canTake(p, r.Labels) {
			return r
		}
		// answers, but its first facts record has not come: only its os and arch labels are announced so far, the repo:, tool: and earned
		// task: labels follow. It may hold them, so this is not a "no" (pdx test reads the pool the same way and queues as usual). Its worker
		// switch is in its info, no facts needed: one that is off is not a host to wait for
		if p.Reachable && p.Info.Worker.Enabled && p.Facts == nil {
			settling = true
		}
	}
	if settling {
		return r
	}
	why := "no reachable host with a worker on"
	if len(r.Labels) > 0 {
		why += " holds " + strings.Join(r.Labels, ", ")
	}
	return Result{Action: ActionNone, Reason: why + ", so the command runs as typed"}
}

// NeedsPool says whether the answer has to be judged against the pool: a rewrite through the queue (with labels or without: a task with none
// is taken by any worker, which still has to be on and reachable). The caller reads the pool only then.
func NeedsPool(r Result) bool {
	return r.Action == ActionRewrite && r.Queued
}

func canTake(p wire.Peer, labels []string) bool {
	if !p.Reachable || !p.Info.Worker.Enabled {
		return false
	}
	for _, l := range labels {
		if !slices.Contains(p.Info.Labels, l) {
			return false
		}
	}
	return true
}

// firstLine is the first line of s, at most 120 bytes: the reason is one line.
func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
