package resources

import "math"

// ReportMinute is one host_minutes row as the report reads it.
type ReportMinute struct {
	At           int64
	Load1, Mem   float64
	FullTicks    int
	FullStarts   int
	FullLongestS int
	// HeavyRunning and LightRunning are the minute's peaks of the gate's slot records (a row from before L2 has the lease's
	// heavy_held in HeavyRunning); GateWaiting the peak of commands waiting at the gate.
	HeavyRunning, LightRunning, GateWaiting int
	// Seconds sampled, and in each zone (0 for a row from before).
	SampledS, NormalS, WarningS, DangerS int
}

// BuildReport works the report out of the minute rows already read. It is pure: the same rows give the same Report.
func BuildReport(since, until int64, minutes []ReportMinute) Report {
	r := Report{Since: since, Until: until}
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
		if m.HeavyRunning >= 1 {
			r.Heavy.Minutes++
			r.Heavy.MaxRunning = max(r.Heavy.MaxRunning, m.HeavyRunning)
			r.Heavy.MaxLoad1 = math.Max(r.Heavy.MaxLoad1, m.Load1)
			r.Heavy.MaxMem = math.Max(r.Heavy.MaxMem, m.Mem)
		}
		r.Light.MaxRunning = max(r.Light.MaxRunning, m.LightRunning)
		if m.GateWaiting >= 1 {
			r.Gate.WaitingMinutes++
			r.Gate.MaxWaiting = max(r.Gate.MaxWaiting, m.GateWaiting)
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
