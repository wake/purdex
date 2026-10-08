package resources

import (
	"math"
	"time"
)

// Lease is what admission needs to know about an active lease.
type Lease struct {
	ID        string
	Weight    int       // host percent, the estimate the holder asked for
	GrantedAt time.Time // the warmup runs from here
	Measured  float64   // EWMA of the lease's own measured use, host percent
	Samples   int       // how many samples fed Measured
}

// Charge is what a lease counts for in admission (spec D-2): its weight until
// it has run the warmup, then its measured average, but never less than
// floor x weight. Pure: now is passed in.
func Charge(l Lease, now time.Time, s Settings) float64 {
	weight := float64(l.Weight)
	if now.Sub(l.GrantedAt) < s.Warmup() {
		return weight
	}
	return math.Max(l.Measured, s.Floor()*weight)
}

// UpdateEWMA folds one sample taken dt after the previous one into the
// average, with the given half-life: after one half-life the average has
// moved halfway to the sample. The first sample is taken as it is, and so is
// every sample when the half-life is not positive; a step that is not
// positive leaves the average alone.
func UpdateEWMA(prev, sample float64, dt, halfLife time.Duration, first bool) float64 {
	if first || halfLife <= 0 {
		return sample
	}
	if dt <= 0 {
		return prev
	}
	alpha := 1 - math.Exp2(-float64(dt)/float64(halfLife))
	return prev + alpha*(sample-prev)
}
