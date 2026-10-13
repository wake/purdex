package resources

import (
	"time"
)

// What a grant records about the capacity rule that v2 no longer applies. The
// decision itself is AdmitV2 (admit_v2.go); the figures here are bookkeeping,
// kept so that the report can still compare the two rules. Pure functions: the
// caller reads the store, the host figures and the clock.

// capEpsilon absorbs float noise in the sums (EWMA charges are fractions): a
// total that is mathematically exactly Capacity must fit.
const capEpsilon = 1e-9

// Decision paths (D-8.1).
const (
	PathImmediate = "immediate" // granted by the pass started for its own request
	PathWaited    = "waited"    // granted by a later pass
	// PathUnknownZone is a grant made because the zone was unknown (the sampler
	// could not read the host): admission fails open.
	PathUnknownZone = "unknown_zone"
	// PathOverrun was a v1 grant at the deadline although the rule said wait. v2
	// never writes it; rows from before keep it.
	PathOverrun = "overrun"
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

// Decision is what one grant saw (D-8.1): the host state at that moment, so
// that the rule can be judged afterwards. SumCharge and Unleased are taken
// before the grant is added.
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

// Charge is what a lease counts for against the pool: its weight until it has
// run Warmup (and while nothing of it has been measured), then the average of
// its own measured use, never below Floor × weight. It no longer decides
// admission; it is recorded and shown.
func Charge(l Lease, now time.Time, s Settings) float64 {
	w := float64(l.Weight)
	if l.Samples == 0 || now.Sub(l.GrantedAt) < s.Warmup() {
		return w
	}
	return max(l.Measured, s.Floor()*w)
}

// Record is the capacity bookkeeping of one grant.
type Record struct {
	SumCharge float64
	Unleased  float64
	// WouldWaitR2 is the pre-R9 formula's verdict (see Decision).
	WouldWaitR2 bool
}

// RecordOf works the capacity bookkeeping out for a grant of the given weight
// against the held leases. leaseUse is each held lease's latest raw
// measurement (CPU and Mem, host percent; a lease's Use is a maximum and does
// not add up). Unleased = host.Measured − max(Σ CPU, Σ Mem), never below 0.
// An unavailable host sample arrives as an all-zero HostUse.
func RecordOf(host HostUse, leases []Lease, leaseUse map[string]LeaseUsage, weight int, now time.Time, s Settings) Record {
	sum := 0.0
	var cpu, mem float64
	for _, l := range leases {
		sum += Charge(l, now, s)
		cpu += leaseUse[l.ID].CPU
		mem += leaseUse[l.ID].Mem
	}
	unleased := max(0, float64(host.Measured)-max(cpu, mem))
	oversize := weight > Capacity
	fitsR2 := (!oversize && sum+unleased+float64(weight) <= Capacity+capEpsilon) || (oversize && len(leases) == 0)
	return Record{SumCharge: sum, Unleased: unleased, WouldWaitR2: !fitsR2}
}
