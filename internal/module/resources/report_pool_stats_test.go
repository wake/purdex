package resourcesmod

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/wwd/wire"
)

func statsOf(s wire.MinuteStats) *wire.MinuteStats { return &s }

// A rollup that carries wwd's per-sample stats is mapped straight: nothing is worked out again from the minute's one record.
func TestReportMinutesOf_StatsAreMappedStraight(t *testing.T) {
	gate := 4
	f := minuteFacts(0, 2, 8000, 1, wire.ZoneNormal, 0, 0) // as a record: calm, nothing running at the last instant
	f.Stats = statsOf(wire.MinuteStats{
		SampledS: 40, FullTicks: 3, FullStarts: 2, FullLongestS: 25, NormalS: 20, WarningS: 15, DangerS: 5,
		HeavyRunningMax: 2, LightRunningMax: 1, GateWaitingMax: &gate,
	})
	got := reportMinutesOf([]wire.Facts{f}, 0)
	require.Len(t, got, 1)
	m := got[0]
	assert.Equal(t, 40, m.SampledS, "a minute wwd started inside is not a whole 60 s")
	assert.Equal(t, 3, m.FullTicks, "not all 12 ticks")
	assert.Equal(t, 2, m.FullStarts)
	assert.Equal(t, 25, m.FullLongestS)
	assert.Equal(t, 20, m.NormalS)
	assert.Equal(t, 15, m.WarningS, "seconds in each zone, not the whole minute in the worst")
	assert.Equal(t, 5, m.DangerS)
	assert.Equal(t, 2, m.HeavyRunning, "the peak, not the last instant")
	assert.Equal(t, 1, m.LightRunning)
	assert.Equal(t, 4, m.GateWaiting)
	assert.Equal(t, f.CPU.Load1, m.Load1)
	assert.Equal(t, 50.0, m.Mem)
}

// A null gate figure is no waiting counted, not a failure.
func TestReportMinutesOf_NoGateFigureIsZero(t *testing.T) {
	f := minuteFacts(0, 1, 8000, 1, wire.ZoneNormal, 0, 0)
	f.Stats = statsOf(wire.MinuteStats{SampledS: 60, NormalS: 60})
	assert.Zero(t, reportMinutesOf([]wire.Facts{f}, 0)[0].GateWaiting)
}

// A minute with stats says its own run state: a full minute (by the rollup's figures) with stats showing no full ticks is not
// full, and the approximation's run bookkeeping does not run through it.
func TestReportMinutesOf_StatsDecideFullNotTheRecord(t *testing.T) {
	f := minuteFacts(0, 5, 1000, 2, wire.ZoneDanger, 0, 0) // load, memory and pressure all say full on the one record
	f.Stats = statsOf(wire.MinuteStats{SampledS: 60, NormalS: 60})
	m := reportMinutesOf([]wire.Facts{f}, 0)[0]
	assert.Zero(t, m.FullTicks)
	assert.Zero(t, m.FullStarts)
	assert.Equal(t, 60, m.NormalS)
	assert.Zero(t, m.DangerS)
}

// Minutes from before the stats existed and minutes with them sit side by side: each is mapped its own way, and an
// approximated run does not carry on into a stats minute (nor from one).
func TestReportMinutesOf_MixedMinutes(t *testing.T) {
	old1 := minuteFacts(0, 5, 8000, 1, wire.ZoneNormal, 0, 0) // no stats: full by the approximation
	old2 := minuteFacts(1, 5, 8000, 1, wire.ZoneNormal, 0, 0) // no stats: the run goes on
	st := minuteFacts(2, 5, 8000, 1, wire.ZoneNormal, 0, 0)   // stats: they decide
	st.Stats = statsOf(wire.MinuteStats{SampledS: 60, FullTicks: 12, FullStarts: 0, FullLongestS: 180, NormalS: 60})
	old3 := minuteFacts(3, 5, 8000, 1, wire.ZoneNormal, 0, 0) // no stats again: a run of its own
	got := reportMinutesOf([]wire.Facts{old1, old2, st, old3}, 0)
	require.Len(t, got, 4)
	assert.Equal(t, 1, got[0].FullStarts)
	assert.Equal(t, 120, got[1].FullLongestS, "the approximated run is closed on its last row before the stats minute")
	assert.Equal(t, 12, got[2].FullTicks)
	assert.Zero(t, got[2].FullStarts, "the stats say the run began earlier")
	assert.Equal(t, 180, got[2].FullLongestS)
	assert.Equal(t, 1, got[3].FullStarts, "after a stats minute the approximation starts again")
	assert.Equal(t, 60, got[3].FullLongestS)
}

// The report names how it was worked out: the approximation when any wwd minute lacks stats, the stats when all have them.
func TestReport_ApproxNamesTheSourceOfTheFigures(t *testing.T) {
	r := newReportRig(t)
	for i := 0; i < 3; i++ {
		f := minuteFacts(i, 1, 8000, 1, wire.ZoneNormal, 0, 0)
		f.Stats = statsOf(wire.MinuteStats{SampledS: 60, NormalS: 60})
		r.history = append(r.history, f)
	}
	assert.Equal(t, resources.ReportApproxWWDStats, r.get(t, "24h").Approx)

	r.history = append(r.history, minuteFacts(3, 1, 8000, 1, wire.ZoneNormal, 0, 0)) // one without
	assert.Equal(t, resources.ReportApproxWWD, r.get(t, "24h").Approx)
}

// End to end through the handler: a restart inside a minute and a short full run come out as they happened.
func TestReport_StatsReachTheFigures(t *testing.T) {
	r := newReportRig(t)
	a := minuteFacts(0, 1, 8000, 1, wire.ZoneNormal, 0, 0)
	a.Stats = statsOf(wire.MinuteStats{SampledS: 20, NormalS: 20}) // wwd came up 40 s into the minute
	b := minuteFacts(1, 1, 8000, 1, wire.ZoneNormal, 0, 0)
	b.Stats = statsOf(wire.MinuteStats{SampledS: 60, FullTicks: 2, FullStarts: 1, FullLongestS: 10, NormalS: 45, WarningS: 15})
	r.history = []wire.Facts{a, b}
	rep := r.get(t, "24h")
	assert.Equal(t, 80, rep.Zones.SampledS, "20 + 60 seconds, not two whole minutes")
	assert.InDelta(t, 15.0/80, rep.Zones.Warning, 1e-9)
	assert.Equal(t, 1, rep.Full.Runs)
	assert.Equal(t, 10, rep.Full.LongestS)
	assert.InDelta(t, 2.0*5/120, rep.Full.Share, 1e-9, "2 full ticks of 5 s over the 2 covered minutes")
}

// wwd's stats for a minute that is full all through (every sample), the way a run carried over the join looks.
func fullStatsMinute(i int, starts, longestS int) wire.Facts {
	f := minuteFacts(i, 5, 8000, 1, wire.ZoneNormal, 0, 0)
	f.Stats = statsOf(wire.MinuteStats{SampledS: 60, FullTicks: 12, FullStarts: starts, FullLongestS: longestS, NormalS: 60})
	return f
}

// A full run that goes on from the old timeline into wwd's stats minutes is one run: wwd's first minute does not start it again, and
// the length adds up across the join for as long as the minutes stay full all through.
func TestReportMinutesOf_ACarriedRunJoinsTheFirstStatsMinutes(t *testing.T) {
	recs := []wire.Facts{
		fullStatsMinute(5, 1, 60),  // wwd's aggregate started this run itself at its first sample
		fullStatsMinute(6, 0, 120), // and it goes on
		fullStatsMinute(7, 0, 180),
		minuteFacts(8, 1, 8000, 1, wire.ZoneNormal, 0, 0),
	}
	recs[3].Stats = statsOf(wire.MinuteStats{SampledS: 60, FullTicks: 4, FullLongestS: 200, NormalS: 60}) // the run ends inside this minute
	got := reportMinutesOf(recs, 180)
	assert.Zero(t, got[0].FullStarts, "the run started in the old rows")
	assert.Equal(t, 240, got[0].FullLongestS, "180 s before and the minute")
	assert.Zero(t, got[1].FullStarts)
	assert.Equal(t, 300, got[1].FullLongestS)
	assert.Equal(t, 360, got[2].FullLongestS)
	assert.Equal(t, 200, got[3].FullLongestS, "a minute that is not full all through is wwd's own figure: the carry stops")
}

// Where the join cannot be proved to be one run, wwd's start stands: a first minute that was full only part of the time, or one that
// does not follow the old rows directly.
func TestReportMinutesOf_ACarryIsOnlyTakenWhereItIsCertain(t *testing.T) {
	part := minuteFacts(5, 5, 8000, 1, wire.ZoneNormal, 0, 0)
	part.Stats = statsOf(wire.MinuteStats{SampledS: 60, FullTicks: 6, FullStarts: 1, FullLongestS: 30, NormalS: 60})
	got := reportMinutesOf([]wire.Facts{part}, 180)
	assert.Equal(t, 1, got[0].FullStarts)
	assert.Equal(t, 30, got[0].FullLongestS)

	gap := fullStatsMinute(9, 1, 60) // the old rows ended at minute 4 (carry given for minute 5 only): not the minute right after
	got = reportMinutesOf([]wire.Facts{minuteFacts(5, 1, 8000, 1, wire.ZoneNormal, 0, 0), gap}, 180)
	assert.Equal(t, 1, got[1].FullStarts)
	assert.Equal(t, 60, got[1].FullLongestS)
}

// A minute wwd had no good sample in is no data (the old timeline wrote no row for it): it is not a calm minute that dilutes the
// share, and it breaks nothing.
func TestReport_AMinuteWithNoGoodSampleIsLeftOut(t *testing.T) {
	r := newReportRig(t)
	a := fullStatsMinute(0, 1, 60)
	empty := minuteFacts(1, 1, 8000, 1, wire.ZoneNormal, 0, 0)
	empty.Stats = statsOf(wire.MinuteStats{}) // every sample of the minute failed
	b := fullStatsMinute(2, 1, 60)
	r.history = []wire.Facts{a, empty, b}
	rep := r.get(t, "24h")
	assert.Equal(t, 2, rep.Coverage.Minutes, "the empty minute is not a minute of coverage")
	assert.InDelta(t, 1.0, rep.Full.Share, 1e-9, "both minutes with data were full all through")
	assert.Equal(t, 2, rep.Full.Runs)
}

// The same, as the join sees it: an empty first minute cannot be the one that continues the old run, and the minutes before it stay
// the old timeline's.
func TestReport_AnEmptyFirstMinuteDoesNotMoveTheJoin(t *testing.T) {
	r := newReportRig(t)
	seedOld(t, r, poolDay0, 4, true) // old minutes 0..3
	empty := minuteFacts(4, 1, 8000, 1, wire.ZoneNormal, 0, 0)
	empty.Stats = statsOf(wire.MinuteStats{})
	r.history = []wire.Facts{empty, fullStatsMinute(5, 1, 60)}
	rep := r.get(t, "24h")
	assert.Equal(t, 5, rep.Coverage.Minutes, "4 old minutes and wwd's minute 5; minute 4 has no data")
}

// The join through the handler: an old full run into wwd's stats minutes that are full all through is one run, as long as the whole.
func TestReport_ARunAcrossTheJoinIsOneRunWithStats(t *testing.T) {
	r := newReportRig(t)
	for i := 0; i < 3; i++ { // old minutes 0..2, full, the length so far on each row
		row := minuteRow{At: poolDay0.Add(time.Duration(i) * time.Minute).UnixMilli(), Load1: 1, NCPU: 4, Mem: 40, Full: true, FullTicks: 12, SampledS: 60, FullLongestS: 60 * (i + 1)}
		if i == 0 {
			row.FullStarts = 1
		}
		require.NoError(t, r.m.store.InsertMinute(row))
	}
	r.history = []wire.Facts{fullStatsMinute(3, 1, 60), fullStatsMinute(4, 0, 120)}
	rep := r.get(t, "24h")
	assert.Equal(t, 5, rep.Coverage.Minutes)
	assert.Equal(t, 1, rep.Full.Runs, "one run, not two")
	assert.Equal(t, 300, rep.Full.LongestS, "3 old minutes and 2 of wwd's")
}
