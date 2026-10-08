package resources

import (
	"math"
	"sort"
	"time"
)

// Waiter is a request that has not been granted yet.
type Waiter struct {
	ID         string
	Weight     int       // host percent
	EnqueuedAt time.Time // FIFO order
	Deadline   time.Time // granted anyway from here on (spec R6)
}

// fitSlack absorbs float noise in the sum of charges; a sum this close to
// Capacity counts as exactly Capacity, anything beyond it does not fit.
const fitSlack = 1e-9

// Admit decides which waiters are granted now (spec D-2, D-3). Pure: no I/O,
// no clock, the arguments are not changed.
//
//   - committed is the sum of Charge over the active leases;
//   - unleased is the host's measured use that no lease owns: Measured less
//     the sum of leaseUse over the active leases (each lease's latest raw
//     measured use, 0 when missing), never negative. It is 0 whenever the
//     host sample is not available (available false: warming up, failed
//     samples, unsupported platform): leases still queue against each
//     other's charges, unknown work just cannot block (review #10);
//   - waiters are walked in FIFO order (EnqueuedAt, ties in the order given).
//     A waiter fits when committed + unleased + weight stays within Capacity,
//     or, for a weight above Capacity, when nothing at all is active (no
//     lease, and nothing granted earlier in this pass). A fitting waiter is
//     granted and counts against the ones behind it; one that does not fit is
//     skipped, so a later, lighter waiter may still pass it;
//   - a waiter whose Deadline is not after now is granted regardless of fit:
//     it is returned in overrun, and counts against the ones behind it too.
//
// grant and overrun are disjoint, each in walk order.
func Admit(host HostUse, available bool, leases []Lease, leaseUse map[string]float64, waiters []Waiter, now time.Time, s Settings) (grant, overrun []string) {
	var committed, leaseSum float64
	for _, l := range leases {
		committed += Charge(l, now, s)
		leaseSum += leaseUse[l.ID]
	}
	unleased := 0.0
	if available {
		unleased = math.Max(0, float64(host.Measured)-leaseSum)
	}

	queue := append([]Waiter(nil), waiters...)
	sort.SliceStable(queue, func(i, j int) bool { return queue[i].EnqueuedAt.Before(queue[j].EnqueuedAt) })

	idle := len(leases) == 0 // nothing active yet, for a request above Capacity
	for _, w := range queue {
		weight := float64(w.Weight)
		switch {
		case !w.Deadline.After(now):
			overrun = append(overrun, w.ID)
		case weight > Capacity && idle,
			weight <= Capacity && committed+unleased+weight <= Capacity+fitSlack:
			grant = append(grant, w.ID)
		default:
			continue
		}
		committed += weight
		idle = false
	}
	return grant, overrun
}
