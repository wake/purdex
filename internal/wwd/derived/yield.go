package derived

import (
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ReasonYieldToQueue is why a heavy fast-lane run waits although a slot is free: a queued job this host's worker would take has waited long
// enough (YieldPolicy). It is followed by the job and how long it has waited, "yield_to_queue:j_01ABC(7m)", which is what pdx test prints.
const ReasonYieldToQueue = "yield_to_queue:"

const (
	// DefaultYieldAfter is how long a queued job waits before the fast lane gives way to it.
	DefaultYieldAfter = 5 * time.Minute
	// DefaultYieldMax is the age past which the fast lane no longer gives way to a job: one that old is not kept waiting by the fast lane
	// alone (a label, a lock or a hold is more likely), and the fast lane must not be stopped for good by it.
	DefaultYieldMax = 30 * time.Minute
	// waitingTrust is how old the worker's reading of the queues (Waiting.ObservedAt) may be: a worker that has stopped reading must not
	// leave the fast lane giving way to a job that has been taken long ago.
	waitingTrust = time.Minute
)

// YieldPolicy is when a heavy fast-lane run (an undeclared command, pdx test --adhoc) gives way to the queue (#3941). The worker of a host with
// one heavy slot polls every few seconds, and a fast-lane run that ends is followed by the next before the worker has looked, so without this
// a queued job never gets the slot. After 0 is off; Max 0 is DefaultYieldMax.
type YieldPolicy struct {
	After, Max time.Duration
}

// Judge says whether the fast lane gives way now to the oldest job of w, and whether it would but for that job being older than Max (over).
// Both are false when nothing waits, when the reading is not recent (waitingTrust) or when the policy is off.
func (p YieldPolicy) Judge(w *wire.Waiting, now time.Time) (yield, over bool) {
	if p.After <= 0 || w == nil || w.Count <= 0 || w.OldestSince.IsZero() {
		return false, false
	}
	if age := now.Sub(w.ObservedAt); age < 0 || age > waitingTrust { // a reading "from the future" (the clock was set back) is as little to be trusted as an old one
		return false, false
	}
	waited := now.Sub(w.OldestSince)
	if waited < p.After {
		return false, false // also a submission time ahead of this clock: its origin's clock runs ahead, and it has waited no time yet
	}
	max := p.Max
	if max <= 0 {
		max = DefaultYieldMax
	}
	if waited > max {
		return false, true
	}
	return true, false
}

// YieldReason is the reason ReasonYieldToQueue followed by the oldest job of w and how many whole minutes it has waited.
func YieldReason(w *wire.Waiting, now time.Time) string {
	return fmt.Sprintf("%s%s(%dm)", ReasonYieldToQueue, w.OldestID, int(now.Sub(w.OldestSince).Minutes()))
}
