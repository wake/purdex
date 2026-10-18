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
// they are read from where they are and the two meet at the first minute wwd has. Judgements stay wwd's: the zone of a minute is
// the worst zone wwd saw in it.

// fullTicksPerMinute is the sampler ticks in a minute: a minute the host was full counts as all of them (the rollup keeps one
// record per minute, so a part of a minute cannot be told).
const fullTicksPerMinute = int(time.Minute / resources.SampleInterval)

// reportMinutesOf maps wwd's minute rollups (oldest first) onto the report's minute rows.
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
