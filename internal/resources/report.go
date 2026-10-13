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

	// Lease v2: how often the row was parked, the zone and trend at the grant,
	// the wait by reason, and the advise shadow's verdict (V2WaitMS -1: none).
	Parks    int
	Zone     string
	Trend    string
	Reasons  map[string]int64
	V2WaitMS int64
	V2Open   bool
}

// ReportMinute is one host_minutes row as the report reads it.
type ReportMinute struct {
	At           int64
	Load1, Mem   float64
	FullTicks    int
	FullStarts   int
	FullLongestS int
	HeavyHeld    int
	// Lease v2: seconds sampled, and in each zone (0 for a row from before).
	SampledS, NormalS, WarningS, DangerS int
}

// BuildReport works the report out of rows already read. It is pure: the same
// rows give the same Report.
func BuildReport(since, until int64, leases []ReportLease, minutes []ReportMinute) Report {
	r := Report{Since: since, Until: until}
	r.Requests.ByKind = []ReportKindCount{}
	r.Kinds = []ReportKind{}
	r.WaitByReason = []ReportReasonWait{}
	r.Longest = []ReportWaitRow{}
	r.Shadow.Longest = []ReportShadowRow{}

	counts := map[string]int{}
	var waits []int64
	type acc struct {
		n       int
		weight  int
		peak    float64
		meanSum float64
	}
	kinds := map[string]*acc{}
	var recordedLeases, shadowLeases []ReportLease
	for _, l := range leases {
		r.Requests.Total++
		counts[l.Kind]++
		r.Parks += l.Parks
		if l.V2WaitMS >= 0 {
			shadowLeases = append(shadowLeases, l)
		}
		if l.GrantedAt == 0 {
			r.Requests.NotGranted++
			continue
		}
		if !l.Recorded {
			// Granted before the decision record existed: in no figure, the
			// per-kind ones included (spec D-8: the report counts dec_recorded = 1).
			r.NotRecorded++
			continue
		} else {
			switch l.Path {
			case PathImmediate:
				r.Paths.Immediate++
			case PathWaited:
				r.Paths.Waited++
			case PathOverrun:
				r.Paths.Overrun++
			case PathOffer:
				r.Paths.Offer++
			case PathContinued:
				r.Paths.Continued++
			}
			waits = append(waits, l.WaitedMS)
			recordedLeases = append(recordedLeases, l)
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

	r.WaitByReason = waitByReason(recordedLeases)
	r.Longest = longestWaits(recordedLeases)
	r.Shadow = shadowOf(shadowLeases)

	var fullTicks int
	var zoneS [3]int
	for i, m := range minutes {
		if i == 0 || m.At < r.Coverage.From {
			r.Coverage.From = m.At
		}
		r.Coverage.To = max(r.Coverage.To, m.At)
		if m.SampledS > 0 {
			r.Zones.Minutes++
			r.Zones.SampledS += m.SampledS
			zoneS[0] += m.NormalS
			zoneS[1] += m.WarningS
			zoneS[2] += m.DangerS
		}
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
	if r.Zones.SampledS > 0 {
		total := float64(r.Zones.SampledS)
		r.Zones.Normal, r.Zones.Warning, r.Zones.Danger = float64(zoneS[0])/total, float64(zoneS[1])/total, float64(zoneS[2])/total
	}
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

// dominant is the reason with the most milliseconds in a wait_reasons map ("" for
// none); a tie goes to the name first in order.
func dominant(reasons map[string]int64) string {
	best, bestMS := "", int64(-1)
	for name, ms := range reasons {
		if ms > bestMS || (ms == bestMS && name < best) {
			best, bestMS = name, ms
		}
	}
	return best
}

// waitByReason groups the waits of the recorded grants by dominant reason, sorted
// by reason; rows without reasons are in no group.
func waitByReason(leases []ReportLease) []ReportReasonWait {
	groups := map[string][]int64{}
	for _, l := range leases {
		if d := dominant(l.Reasons); d != "" {
			groups[d] = append(groups[d], l.WaitedMS)
		}
	}
	out := make([]ReportReasonWait, 0, len(groups))
	for name, ws := range groups {
		sort.Slice(ws, func(i, j int) bool { return ws[i] < ws[j] })
		out = append(out, ReportReasonWait{Reason: name, N: len(ws), P50: nearestRank(ws, 50), P90: nearestRank(ws, 90), Max: ws[len(ws)-1]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reason < out[j].Reason })
	return out
}

// reportLongest is how many rows the "longest" lists show.
const reportLongest = 5

// longestWaits are the five longest waits among the recorded grants (waits of 0
// are not listed), longest first, older request first on a tie.
func longestWaits(leases []ReportLease) []ReportWaitRow {
	rows := make([]ReportLease, 0, len(leases))
	for _, l := range leases {
		if l.WaitedMS > 0 {
			rows = append(rows, l)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].WaitedMS != rows[j].WaitedMS {
			return rows[i].WaitedMS > rows[j].WaitedMS
		}
		return rows[i].CreatedAt < rows[j].CreatedAt
	})
	out := []ReportWaitRow{}
	for _, l := range rows[:min(len(rows), reportLongest)] {
		out = append(out, ReportWaitRow{Kind: l.Kind, CreatedAt: l.CreatedAt, WaitedMS: l.WaitedMS, Dominant: dominant(l.Reasons),
			Reasons: l.Reasons, Zone: l.Zone, Trend: l.Trend})
	}
	return out
}

// shadowOf summarises the advise shadow's verdicts (rows with V2WaitMS >= 0).
func shadowOf(leases []ReportLease) ReportShadow {
	sh := ReportShadow{Longest: []ReportShadowRow{}}
	if len(leases) == 0 {
		return sh
	}
	waits := make([]int64, 0, len(leases))
	var rows []ReportLease
	for _, l := range leases {
		sh.N++
		if l.V2Open {
			sh.Open++
		}
		waits = append(waits, l.V2WaitMS)
		if l.V2WaitMS > 0 {
			rows = append(rows, l)
		}
	}
	sort.Slice(waits, func(i, j int) bool { return waits[i] < waits[j] })
	sh.WaitMS = ReportWait{P50: nearestRank(waits, 50), P90: nearestRank(waits, 90), Max: waits[len(waits)-1]}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].V2WaitMS != rows[j].V2WaitMS {
			return rows[i].V2WaitMS > rows[j].V2WaitMS
		}
		return rows[i].CreatedAt < rows[j].CreatedAt
	})
	for _, l := range rows[:min(len(rows), reportLongest)] {
		sh.Longest = append(sh.Longest, ReportShadowRow{Kind: l.Kind, CreatedAt: l.CreatedAt, WaitMS: l.V2WaitMS, Open: l.V2Open,
			Dominant: dominant(l.Reasons), Reasons: l.Reasons})
	}
	return sh
}

// ReportPosition is one position row as the report reads it.
type ReportPosition struct {
	EndReason       string
	CommandsStarted int
	RequeuedFrom    string
	Extensions      int
	CreatedAt       int64
	OfferedAt       int64
	DeliveredAt     int64
	OfferPasses     int
	OfferWaitMS     int64
	IdleWaitMS      int64
	NoticeResult    string
}

// BuildPositionReport works the position figures out of rows already read. Pure.
func BuildPositionReport(rows []ReportPosition) ReportPositions {
	out := ReportPositions{ByEndReason: []ReportReasonCount{}}
	out.Offers.NoticeResults = []ReportReasonCount{}
	ends, notices := map[string]int{}, map[string]int{}
	var offerWait, idleWait, queuedWait, delay []int64
	for _, p := range rows {
		out.Total++
		if p.EndReason != "" {
			ends[p.EndReason]++
		}
		switch {
		case p.CommandsStarted == 0:
			out.Commands.None++
		case p.CommandsStarted == 1:
			out.Commands.One++
		case p.CommandsStarted == 2:
			out.Commands.Two++
		case p.CommandsStarted == 3:
			out.Commands.Three++
		default:
			out.Commands.FourPlus++
		}
		if p.RequeuedFrom != "" {
			out.Requeued++
		}
		out.Extensions += p.Extensions
		if p.OfferWaitMS > 0 {
			offerWait = append(offerWait, p.OfferWaitMS)
		}
		if p.IdleWaitMS > 0 {
			idleWait = append(idleWait, p.IdleWaitMS)
		}
		out.Offers.Passed += p.OfferPasses
		if p.OfferedAt > 0 {
			out.Offers.Offered++
			queuedWait = append(queuedWait, max(0, p.OfferedAt-p.CreatedAt))
			if p.DeliveredAt > 0 {
				out.Offers.Delivered++
				delay = append(delay, max(0, p.DeliveredAt-p.OfferedAt))
			}
		}
		if p.EndReason == EndNoAnswer {
			out.Offers.LapsedAfterDelivery++
		}
		if p.NoticeResult != "" {
			notices[p.NoticeResult]++
		}
	}
	out.ByEndReason = countsByName(ends)
	out.Offers.NoticeResults = countsByName(notices)
	out.OfferWaitMS, out.IdleWaitMS = distOf(offerWait), distOf(idleWait)
	out.Offers.DeliveryDelayMS = distOf(delay)
	sort.Slice(queuedWait, func(i, j int) bool { return queuedWait[i] < queuedWait[j] })
	if n := len(queuedWait); n > 0 {
		out.QueuedWaitMS = ReportWait{P50: nearestRank(queuedWait, 50), P90: nearestRank(queuedWait, 90), Max: queuedWait[n-1]}
	}
	return out
}

func countsByName(m map[string]int) []ReportReasonCount {
	out := make([]ReportReasonCount, 0, len(m))
	for k, n := range m {
		out = append(out, ReportReasonCount{Reason: k, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reason < out[j].Reason })
	return out
}

func distOf(values []int64) ReportDist {
	if len(values) == 0 {
		return ReportDist{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var total int64
	for _, v := range sorted {
		total += v
	}
	return ReportDist{N: len(sorted), Total: total, P50: nearestRank(sorted, 50), P90: nearestRank(sorted, 90), Max: sorted[len(sorted)-1]}
}
