package resources

import (
	"math"
	"sort"
)

// ReportLease is one lease row as the report reads it.
type ReportLease struct {
	Kind        string
	Weight      int
	CreatedAt   int64
	GrantedAt   int64 // 0: never granted
	Recorded    bool  // dec_recorded
	Path        string
	WaitedMS    int64
	WouldWaitR2 bool
	PeakUse     float64
	MeanUse     float64
	Samples     int
}

// ReportMinute is one host_minutes row as the report reads it.
type ReportMinute struct {
	At           int64
	Load1, Mem   float64
	FullTicks    int
	FullStarts   int
	FullLongestS int
	HeavyHeld    int
}

// BuildReport works the report out of rows already read. It is pure: the same
// rows give the same Report.
func BuildReport(since, until int64, leases []ReportLease, minutes []ReportMinute) Report {
	r := Report{Since: since, Until: until}
	r.Requests.ByKind = []ReportKindCount{}
	r.Kinds = []ReportKind{}

	counts := map[string]int{}
	var waits []int64
	type acc struct {
		n       int
		weight  int
		peak    float64
		meanSum float64
	}
	kinds := map[string]*acc{}
	for _, l := range leases {
		r.Requests.Total++
		counts[l.Kind]++
		if l.GrantedAt == 0 {
			r.Requests.NotGranted++
			continue
		}
		if !l.Recorded {
			r.NotRecorded++
		} else {
			switch l.Path {
			case PathImmediate:
				r.Paths.Immediate++
			case PathWaited:
				r.Paths.Waited++
			case PathOverrun:
				r.Paths.Overrun++
			}
			waits = append(waits, l.WaitedMS)
			if l.WouldWaitR2 {
				r.WouldWaitR2++
			}
		}
		if l.Samples > 0 {
			a := kinds[l.Kind]
			if a == nil {
				a = &acc{}
				kinds[l.Kind] = a
			}
			a.n++
			a.weight = max(a.weight, l.Weight)
			a.peak = math.Max(a.peak, l.PeakUse)
			a.meanSum += l.MeanUse
		}
	}
	for k, n := range counts {
		r.Requests.ByKind = append(r.Requests.ByKind, ReportKindCount{Kind: k, Count: n})
	}
	sort.Slice(r.Requests.ByKind, func(i, j int) bool {
		a, b := r.Requests.ByKind[i], r.Requests.ByKind[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Kind < b.Kind
	})
	for k, a := range kinds {
		r.Kinds = append(r.Kinds, ReportKind{Kind: k, N: a.n, Weight: a.weight, PeakMax: a.peak, MeanAvg: a.meanSum / float64(a.n)})
	}
	sort.Slice(r.Kinds, func(i, j int) bool { return r.Kinds[i].Kind < r.Kinds[j].Kind })

	sort.Slice(waits, func(i, j int) bool { return waits[i] < waits[j] })
	if n := len(waits); n > 0 {
		r.WaitMS = ReportWait{P50: nearestRank(waits, 50), P90: nearestRank(waits, 90), Max: waits[n-1]}
	}

	var fullTicks int
	for i, m := range minutes {
		if i == 0 || m.At < r.Coverage.From {
			r.Coverage.From = m.At
		}
		r.Coverage.To = max(r.Coverage.To, m.At)
		fullTicks += m.FullTicks
		r.Full.Runs += m.FullStarts
		r.Full.LongestS = max(r.Full.LongestS, m.FullLongestS)
		if m.HeavyHeld >= 2 {
			r.Heavy.Minutes++
			r.Heavy.MaxLoad1 = math.Max(r.Heavy.MaxLoad1, m.Load1)
			r.Heavy.MaxMem = math.Max(r.Heavy.MaxMem, m.Mem)
		}
	}
	r.Coverage.Minutes = len(minutes)
	if len(minutes) > 0 {
		seconds := float64(fullTicks) * SampleInterval.Seconds()
		r.Full.Share = math.Min(1, seconds/(float64(len(minutes))*60))
	}
	return r
}

// nearestRank is the p-th percentile of sorted by the nearest-rank method: the
// value at rank ceil(p/100 × n).
func nearestRank(sorted []int64, p int) int64 {
	rank := int(math.Ceil(float64(p) / 100 * float64(len(sorted))))
	return sorted[max(rank, 1)-1]
}
