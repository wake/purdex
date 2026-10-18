package resourcesmod

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The report from wwd's minutes (WWD-2d): while the fleet row's source is "pool", GET /api/resources/report joins the retired
// timeline (resources.db host_minutes, up to where wwd's begins) with the per-minute rollups wwd keeps in wwd.db, read through its
// 1 m facts history. wwd's rows are whole facts records, so the old rows cannot be copied into wwd.db without inventing facts;
// they are read from where they are and the two meet at the first minute wwd has. A wwd minute that carries wwd's per-sample stats
// (wire.MinuteStats, #3267) is mapped straight from them; one from before that is worked out from its one record, below.

// fullTicksPerMinute is the sampler ticks in a minute: a minute the host was full counts as all of them (the rollup keeps one
// record per minute, so a part of a minute cannot be told).
const fullTicksPerMinute = int(time.Minute / resources.SampleInterval)

// reportMinutesOf maps wwd's minute rollups (oldest first) onto the report's minute rows.
//
// A rollup that carries wwd's per-sample stats is taken from them as they are (statsMinute), and ends any approximated run before it. For the
// minutes without stats (from before wwd kept them):
//
//   - load1 and the memory share are the rollup's (the average load, the share at the lowest available memory of the minute);
//   - a minute is full on the daemon's own rule (load1 >= ncpu, memory >= resources.MemFullPercent, or pressure >= 2), and a run
//     is a row of consecutive full minutes; its length is reported on its last row, a missing minute ends it;
//   - the heavy and light running counts are the facts' worker.running of the minute; the queue (gate_waiting) is not in the facts;
//   - the zone seconds are the whole minute in the minute's worst zone; an unknown zone is not sampled time.
//
// carryRunS is the length, in seconds, of a full run that ends in the minute just before the first record (the old timeline's last
// minute), 0 when there is none: a first record that is full goes on with it instead of starting a run of its own.
func reportMinutesOf(recs []wire.Facts, carryRunS int) []resources.ReportMinute {
	out := make([]resources.ReportMinute, 0, len(recs))
	runLen := 0 // the open run: its length in minutes
	var prevAt int64
	if len(recs) > 0 && carryRunS > 0 {
		runLen = (carryRunS + 59) / 60
		prevAt = recs[0].At.UnixMilli() - time.Minute.Milliseconds()
	}
	// statsCarry is the old run still to be added to wwd's own count: wwd's aggregate began at its first sample and so counts a run of its own
	// even when the host was already full. It is taken only where the join is certain (see the stats branch); carryOpen says wwd's first
	// start of it has not been taken away yet.
	statsCarry, carryOpen := carryRunS, true
	tickS := int(resources.SampleInterval / time.Second)
	closeRun := func() {
		if runLen > 0 && len(out) > 0 {
			out[len(out)-1].FullLongestS = runLen * 60
		}
		runLen = 0
	}
	for _, f := range recs {
		at := f.At.UnixMilli()
		m := resources.ReportMinute{At: at, Load1: f.CPU.Load1, HeavyRunning: f.Worker.Running.Heavy, LightRunning: f.Worker.Running.Light}
		if f.Mem.TotalMB > 0 {
			m.Mem = 100 * float64(max(f.Mem.TotalMB-f.Mem.AvailableMB, 0)) / float64(f.Mem.TotalMB)
		}
		if st := f.Stats; st != nil {
			// wwd counted this minute per 5 s sample: take it as it is. An approximated run ends on its last row before it, and the
			// stats say themselves whether one goes on through it.
			contiguous := prevAt != 0 && at-prevAt == time.Minute.Milliseconds()
			closeRun()
			row := statsMinute(m, st)
			// A run carried over from the old rows goes on through minutes that were full at every sample, and only those are certain: a
			// minute full part of the time may have begun its run anywhere in it. Then wwd's first start is the old run's, and the lengths add.
			if statsCarry > 0 && contiguous && st.SampledS > 0 && st.FullTicks*tickS >= st.SampledS {
				if carryOpen {
					row.FullStarts = max(st.FullStarts-1, 0)
					carryOpen = false
				}
				row.FullLongestS = st.FullLongestS + statsCarry
			} else {
				statsCarry = 0
			}
			out = append(out, row)
			prevAt = at
			continue
		}
		statsCarry = 0 // an approximated minute handles the carry itself
		full := f.CPU.NCPU > 0 && f.CPU.Load1 >= float64(f.CPU.NCPU) || m.Mem >= resources.MemFullPercent || f.Mem.Pressure >= 2
		contiguous := prevAt != 0 && at-prevAt == time.Minute.Milliseconds()
		switch {
		case full && runLen > 0 && contiguous:
			runLen++
		case full:
			closeRun() // a run before a gap ends on the row before
			runLen = 1
			m.FullStarts = 1
		default:
			closeRun()
		}
		if full {
			m.FullTicks = fullTicksPerMinute
		}
		switch f.Derived.Zone {
		case wire.ZoneNormal:
			m.SampledS, m.NormalS = 60, 60
		case wire.ZoneWarning:
			m.SampledS, m.WarningS = 60, 60
		case wire.ZoneDanger:
			m.SampledS, m.DangerS = 60, 60
		}
		out = append(out, m)
		prevAt = at
	}
	if runLen > 0 {
		out[len(out)-1].FullLongestS = runLen * 60
	}
	return out
}

// poolReportRecords is wwd's 1 m rollups from since on, without the minute still filling (its rollup is partial and would count as a
// whole one). ok is false when the source is not the pool, wwd cannot be asked, or it has no finished minute yet (the report is then
// the old timeline's alone).
func (m *Module) poolReportRecords(ctx context.Context, since, now time.Time) ([]wire.Facts, bool) {
	if m.pool == nil || m.pool.history == nil || !m.fleetSrc.isPool() {
		return nil, false
	}
	resp, err := m.pool.history(ctx, wire.FactsHistoryQuery{Since: since, Step: wire.Step1m})
	if err != nil {
		m.logf("[resources] report: wwd history: %v: the report is the old timeline's alone", err)
		return nil, false
	}
	var done []wire.Facts
	for _, f := range resp.Records {
		if f.Stats != nil && f.Stats.SampledS == 0 {
			continue // wwd had no good sample in that minute: no data, as a minute the old timeline wrote no row for
		}
		if !f.At.Add(time.Minute).After(now) {
			done = append(done, f)
		}
	}
	return done, len(done) > 0
}

// wholeMinuteFull says whether an old minute row was full at every sampled second, so that a run can be taken to go on into the
// next minute: all the ticks of a whole minute, and no more sampled seconds than full ones (a row merged from two runs of the
// sampler, a restart inside the minute, has calm ticks added to the full ones and proves nothing about its end).
func wholeMinuteFull(m resources.ReportMinute) bool {
	tick := int(resources.SampleInterval / time.Second)
	return m.FullTicks >= fullTicksPerMinute && m.SampledS > 0 && m.FullTicks*tick >= m.SampledS
}

// statsMinute is the minute row made from wwd's per-sample stats: the seconds sampled, the full ticks, starts and longest run, the
// seconds in each zone and the running peaks are wwd's own counts. A null gate figure (wwd has no gate to count yet) is no waiting
// counted. m carries what the stats do not have: the minute's load1 (its average) and memory share.
func statsMinute(m resources.ReportMinute, st *wire.MinuteStats) resources.ReportMinute {
	m.SampledS, m.NormalS, m.WarningS, m.DangerS = st.SampledS, st.NormalS, st.WarningS, st.DangerS
	m.FullTicks, m.FullStarts, m.FullLongestS = st.FullTicks, st.FullStarts, st.FullLongestS
	m.HeavyRunning, m.LightRunning = st.HeavyRunningMax, st.LightRunningMax
	m.GateWaiting = 0
	if st.GateWaitingMax != nil {
		m.GateWaiting = *st.GateWaitingMax
	}
	return m
}
