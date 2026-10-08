package resources

import (
	"time"
)

// Admission (spec D-2, R9): pure functions. The caller reads the store, the
// host figures and the clock; nothing here does I/O.

// Decision paths (D-8.1).
const (
	PathImmediate = "immediate" // granted by the pass that first saw the request
	PathWaited    = "waited"    // granted by a later pass, once it fitted
	PathOverrun   = "overrun"   // granted at its deadline although it did not fit
)

// Lease is the admission view of a held lease.
type Lease struct {
	ID        string
	Weight    int
	GrantedAt time.Time
	// Measured is the EWMA of the lease's own measured use, host percent;
	// Samples is how many measurements went into it.
	Measured float64
	Samples  int
}

// Waiter is a queued request.
type Waiter struct {
	ID         string
	Weight     int
	EnqueuedAt time.Time
	Deadline   time.Time // zero: none
	// Fresh marks the request that the pass was started for (its own POST):
	// a grant of it is "immediate", any other waiter's is "waited".
	Fresh bool
}

// Decision is what one grant saw (D-8.1): the host state at that moment, so
// that the rule can be judged afterwards. SumCharge, Unleased and the active
// count are taken before the grant is added.
type Decision struct {
	Path     string
	WaitedMS int64

	Load1    float64
	NCPU     int
	Mem      float64
	Measured int
	Full     bool

	Weight    int
	SumCharge float64
	Unleased  float64
	// WouldWaitR2 says whether the pre-R9 formula (Σ charge + unleased + w >
	// 100, with the same oversize exception) would have held the request back.
	WouldWaitR2 bool
}

// Grant is one request admitted by a pass.
type Grant struct {
	ID      string
	Overrun bool
	Decision
}

// Charge is what a lease counts for against the pool: its weight until it has
// run Warmup (and while nothing of it has been measured), then the average of
// its own measured use, never below Floor × weight.
func Charge(l Lease, now time.Time, s Settings) float64 {
	w := float64(l.Weight)
	if l.Samples == 0 || now.Sub(l.GrantedAt) < s.Warmup() {
		return w
	}
	return max(l.Measured, s.Floor()*w)
}

// Admit walks the waiters in FIFO order and returns the grants of this pass
// (D-2). A waiter of weight w is granted when Σ charge + w ≤ Capacity and the
// host is not full; a waiter heavier than Capacity when no lease is active
// (and the host is not full); a waiter whose Deadline has passed is granted
// whatever the state, as an overrun (R6). A waiter that does not fit is
// skipped and a later, lighter one may still fit (D-3). Each grant is added to
// the sum for the rest of the walk, and counts as an active lease for the
// oversize rule, so two oversize requests are never granted together.
//
// leaseUse is each held lease's latest raw measured use (host percent);
// unleased = host.Measured − Σ leaseUse[leases], never below 0, is only
// recorded. A host sample that is unavailable arrives as an all-zero HostUse
// (not full, nothing measured): the leases then queue against each other and
// deadlines still overrun.
func Admit(host HostUse, leases []Lease, leaseUse map[string]float64, waiters []Waiter, now time.Time, s Settings) []Grant {
	sum := 0.0
	var leased float64
	for _, l := range leases {
		sum += Charge(l, now, s)
		leased += leaseUse[l.ID]
	}
	unleased := max(0, float64(host.Measured)-leased)
	active := len(leases)

	var out []Grant
	for _, w := range waiters {
		weight := float64(w.Weight)
		oversize := w.Weight > Capacity
		fits := !host.Full && ((!oversize && sum+weight <= Capacity) || (oversize && active == 0))
		overrun := !w.Deadline.IsZero() && !now.Before(w.Deadline)
		if !fits && !overrun {
			continue
		}
		fitsR2 := (!oversize && sum+unleased+weight <= Capacity) || (oversize && active == 0)
		path := PathWaited
		switch {
		case !fits:
			path = PathOverrun
		case w.Fresh:
			path = PathImmediate
		}
		out = append(out, Grant{ID: w.ID, Overrun: !fits, Decision: Decision{
			Path: path, WaitedMS: max(0, now.Sub(w.EnqueuedAt).Milliseconds()),
			Load1: host.Load1, NCPU: host.NCPU, Mem: host.Mem, Measured: host.Measured, Full: host.Full,
			Weight: w.Weight, SumCharge: sum, Unleased: unleased, WouldWaitR2: !fitsR2,
		}})
		sum += weight
		active++
	}
	return out
}
