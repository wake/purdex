package resources

import (
	"math"
	"time"
)

// UpdateEWMA folds one sample into a running average whose weight halves
// every halfLife: after one half-life of dt, a step from prev to sample has
// covered half the way. The first sample is the sample itself. No time
// passed (dt <= 0, a clock that went back) leaves prev; a halfLife of zero or
// less has no memory and returns the sample.
func UpdateEWMA(prev, sample float64, dt, halfLife time.Duration, first bool) float64 {
	if first || halfLife <= 0 {
		return sample
	}
	if dt <= 0 {
		return prev
	}
	alpha := 1 - math.Pow(0.5, float64(dt)/float64(halfLife))
	return prev + alpha*(sample-prev)
}
