package resources

import (
	"slices"
	"time"
)

// ringKeep is how much history the ring holds.
const ringKeep = 10 * time.Minute

// trendMinSamples is the fewest samples a window needs before a direction is
// claimed.
const trendMinSamples = 6

// TrendSample is one good sampler reading, as the trend reads it.
type TrendSample struct {
	At time.Time
	// Mem is the memory in use, a percentage.
	Mem float64
	// CPU is load1 / ncpu as a percentage.
	CPU float64
	// Pressure is the memory pressure level: 1, 2 or 4; 0 unknown.
	Pressure int
}

// Ring keeps the samples of the last ringKeep (lease v2 spec D-2′). It is
// not safe for concurrent use: the sampler goroutine owns it and everything
// else gets copies from whoever owns it.
type Ring struct{ s []TrendSample }

// Add appends a sample and drops those older than ringKeep before it.
func (r *Ring) Add(s TrendSample) {
	r.s = append(r.s, s)
	cutoff := s.At.Add(-ringKeep)
	if i := slices.IndexFunc(r.s, func(x TrendSample) bool { return !x.At.Before(cutoff) }); i > 0 {
		r.s = r.s[i:]
	}
}

// Since returns a copy of the samples at or after t, oldest first.
func (r *Ring) Since(t time.Time) []TrendSample {
	i := slices.IndexFunc(r.s, func(x TrendSample) bool { return !x.At.Before(t) })
	if i < 0 {
		return nil
	}
	return slices.Clone(r.s[i:])
}

// All returns a copy of every sample, oldest first.
func (r *Ring) All() []TrendSample { return slices.Clone(r.s) }

// Trend is where host usage is heading.
type Trend int

const (
	// TrendUnknown is too little data to say; admission treats it as rising.
	TrendUnknown Trend = iota
	TrendFalling
	TrendFlat
	TrendRising
)

func (t Trend) String() string {
	switch t {
	case TrendFalling:
		return "falling"
	case TrendFlat:
		return "flat"
	case TrendRising:
		return "rising"
	}
	return "unknown"
}

// ParseTrend is the inverse of Trend.String; anything else is unknown.
func ParseTrend(s string) Trend {
	for _, t := range []Trend{TrendFalling, TrendFlat, TrendRising} {
		if t.String() == s {
			return t
		}
	}
	return TrendUnknown
}

// TrendOf reads the direction of host usage from the samples within
// trend_window_s of now (lease v2 spec D-2′).
//
// Rising: the pressure level at the end of the window is higher than at its
// start, or the least-squares slope of memory exceeds trend_mem_dppm/10
// points per minute, or that of cpu exceeds trend_cpu_dppm/10. Otherwise
// falling on the mirror conditions, else flat. Fewer than 6 samples in the
// window, or a span shorter than half the window, is unknown.
func TrendOf(samples []TrendSample, now time.Time, s Settings) Trend {
	window := s.TrendWindow()
	from := now.Add(-window)
	i := slices.IndexFunc(samples, func(x TrendSample) bool { return !x.At.Before(from) })
	if i < 0 {
		return TrendUnknown
	}
	in := samples[i:]
	if len(in) > 0 && in[len(in)-1].At.After(now) {
		in = in[:slices.IndexFunc(in, func(x TrendSample) bool { return x.At.After(now) })]
	}
	if len(in) < trendMinSamples || in[len(in)-1].At.Sub(in[0].At) < window/2 {
		return TrendUnknown
	}
	memSlope := slope(in, func(x TrendSample) float64 { return x.Mem })
	cpuSlope := slope(in, func(x TrendSample) float64 { return x.CPU })
	memLimit, cpuLimit := s.TrendMemPPM(), s.TrendCPUPPM()
	first, last := in[0].Pressure, in[len(in)-1].Pressure
	// Pressure 0 is a failed read, not a level below normal: the direction
	// counts only when both ends are known.
	known := first > 0 && last > 0
	switch {
	case known && last > first || memSlope > memLimit || (cpuLimit > 0 && cpuSlope > cpuLimit):
		return TrendRising
	case known && last < first || memSlope < -memLimit || (cpuLimit > 0 && cpuSlope < -cpuLimit):
		return TrendFalling
	}
	return TrendFlat
}

// slope is the least-squares slope of value over time, in units per minute.
// The caller guarantees the samples span a positive time.
func slope(in []TrendSample, value func(TrendSample) float64) float64 {
	t0 := in[0].At
	var n, sx, sy, sxx, sxy float64
	for _, x := range in {
		m := x.At.Sub(t0).Minutes()
		y := value(x)
		n++
		sx += m
		sy += y
		sxx += m * m
		sxy += m * y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / den
}
