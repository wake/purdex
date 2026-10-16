package hostmem

import "time"

// RateWindow is how far back the page rate looks.
const RateWindow = 10 * time.Second

// PageSample is a reading of the paging counters: pages written out (pageouts + swapouts) since boot, at a time.
type PageSample struct {
	At    time.Time
	Total uint64
}

// RateOver is the page rate in pages per second at cur: against the oldest of the samples that are at least a second and at
// most RateWindow older (a short baseline beats none), 0 when there is none or the counter went back. It is the rate of the
// gate's admission rule, and the daemon's zone reads the same signal with the same arithmetic.
func RateOver(samples []PageSample, cur PageSample) float64 {
	var base *PageSample
	for i := range samples {
		s := &samples[i]
		age := cur.At.Sub(s.At)
		if age < time.Second || age > RateWindow {
			continue
		}
		if base == nil || s.At.Before(base.At) {
			base = s // the oldest inside the window: the longest baseline
		}
	}
	if base == nil {
		return 0 // no baseline yet: the rate is unknown, taken as 0 (the level still holds)
	}
	dt := cur.At.Sub(base.At).Seconds()
	if dt <= 0 {
		return 0
	}
	d := float64(cur.Total) - float64(base.Total)
	if d < 0 {
		d = 0
	}
	return d / dt
}
