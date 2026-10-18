package resourcesmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/wwd/wire"
)

var poolDay0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// minuteFacts is one wwd minute rollup: 4 CPUs, 16000 MB of which availMB is free.
func minuteFacts(i int, load1 float64, availMB, pressure int, zone wire.Zone, heavy, light int) wire.Facts {
	return wire.Facts{
		V: wire.Proto, Alias: "mlab", At: poolDay0.Add(time.Duration(i) * time.Minute),
		CPU:     wire.CPU{NCPU: 4, Load1: load1, IdlePct: 80},
		Mem:     wire.Mem{TotalMB: 16000, AvailableMB: availMB, Pressure: pressure},
		Derived: wire.Derived{Zone: zone},
		Worker:  wire.FactsWorker{Running: wire.Slots{Heavy: heavy, Light: light}},
	}
}

// The mapping from wwd's minute rollups to the report's minute rows, with the figures worked out by hand.
func TestReportMinutesOf_MapsTheRollups(t *testing.T) {
	recs := []wire.Facts{
		minuteFacts(0, 1, 8000, 1, wire.ZoneNormal, 0, 0),  // calm
		minuteFacts(1, 5, 8000, 1, wire.ZoneNormal, 0, 0),  // load1 5 >= 4 cpus: full (start of a run)
		minuteFacts(2, 1, 1000, 1, wire.ZoneWarning, 2, 3), // memory 93.75 %: full; heavy 2, light 3
		minuteFacts(3, 1, 8000, 2, wire.ZoneDanger, 1, 0),  // pressure 2: full; the run is 3 minutes long so far
		minuteFacts(4, 1, 8000, 1, wire.ZoneUnknown, 0, 0), // calm again; zone unknown is not a sampled zone
		minuteFacts(5, 5, 8000, 1, wire.ZoneNormal, 0, 0),  // a second run
	}
	got := reportMinutesOf(recs)
	require.Len(t, got, 6)
	assert.Equal(t, poolDay0.UnixMilli(), got[0].At)
	assert.Equal(t, 50.0, got[0].Mem, "used share from the lowest available of the minute")
	assert.Equal(t, 1.0, got[0].Load1)
	assert.Zero(t, got[0].FullTicks)

	assert.Equal(t, 12, got[1].FullTicks, "a full minute is 12 ticks of 5 s")
	assert.Equal(t, 1, got[1].FullStarts)
	assert.Equal(t, 1, got[1].FullTicks/12)
	assert.Equal(t, 12, got[2].FullTicks)
	assert.Zero(t, got[2].FullStarts, "the run goes on")
	assert.Equal(t, 12, got[3].FullTicks)
	assert.Equal(t, 180, got[3].FullLongestS, "the run ended at minute 3 after 3 minutes: reported on its last row")
	assert.Equal(t, 1, got[5].FullStarts)
	assert.Equal(t, 60, got[5].FullLongestS)

	assert.Equal(t, 2, got[2].HeavyRunning)
	assert.Equal(t, 3, got[2].LightRunning)
	assert.Zero(t, got[2].GateWaiting, "the queue is not in the facts (it comes with the jobs API)")

	assert.Equal(t, 60, got[0].SampledS)
	assert.Equal(t, 60, got[0].NormalS)
	assert.Equal(t, 60, got[2].WarningS)
	assert.Equal(t, 60, got[3].DangerS)
	assert.Zero(t, got[4].SampledS, "an unknown zone is not sampled time")
	assert.Zero(t, got[4].NormalS+got[4].WarningS+got[4].DangerS)
}

// A run still going on at the end of the series is reported on its last row too.
func TestReportMinutesOf_ARunOpenAtTheEnd(t *testing.T) {
	got := reportMinutesOf([]wire.Facts{minuteFacts(0, 5, 8000, 1, wire.ZoneNormal, 0, 0), minuteFacts(1, 5, 8000, 1, wire.ZoneNormal, 0, 0)})
	assert.Equal(t, 120, got[1].FullLongestS)
	assert.Zero(t, got[0].FullLongestS)
}

// A gap of minutes breaks a run: the host was not seen, so two full minutes either side of it are not one run.
func TestReportMinutesOf_AGapBreaksARun(t *testing.T) {
	got := reportMinutesOf([]wire.Facts{minuteFacts(0, 5, 8000, 1, wire.ZoneNormal, 0, 0), minuteFacts(3, 5, 8000, 1, wire.ZoneNormal, 0, 0)})
	assert.Equal(t, 1, got[0].FullStarts)
	assert.Equal(t, 1, got[1].FullStarts)
	assert.Equal(t, 60, got[0].FullLongestS)
	assert.Equal(t, 60, got[1].FullLongestS)
}

// reportRig is a module with the resources.db rows of an old day, the pool reader, and a wwd that answers the 1 m history.
type reportRig struct {
	m       *Module
	now     time.Time
	history []wire.Facts
	histErr error
	asked   []wire.FactsHistoryQuery
	row     *fakeFleetRow
}

func newReportRig(t *testing.T) *reportRig {
	t.Helper()
	r := &reportRig{now: poolDay0.Add(10 * time.Hour), row: &fakeFleetRow{cfg: hostconfig.FleetConfig{Source: hostconfig.FleetSourcePool}, rev: 1}}
	m, _ := initedModule(t, t.TempDir(), &fakeSettings{}, &fakeSampler{})
	m.now = func() time.Time { return r.now }
	m.pool = &poolHost{now: func() time.Time { return r.now }, pool: func(context.Context) (wire.Pool, error) { return wire.Pool{}, nil },
		history: func(_ context.Context, q wire.FactsHistoryQuery) (wire.FactsHistoryResponse, error) {
			r.asked = append(r.asked, q)
			return wire.FactsHistoryResponse{Step: q.Step, Records: r.history}, r.histErr
		}}
	m.fleetSrc = &fleetSource{cfg: r.row.FleetConfig, rev: r.row.FleetRevision}
	r.m = m
	return r
}

func (r *reportRig) get(t *testing.T, since string) resources.Report {
	t.Helper()
	rr := httptest.NewRecorder()
	r.m.handleReport(rr, httptest.NewRequest(http.MethodGet, resources.ReportPath+"?since="+since, nil))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var rep resources.Report
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rep))
	return rep
}

// seedOld writes n minute rows of the retired timeline, one a minute from start (a full minute each when full).
func seedOld(t *testing.T, r *reportRig, start time.Time, n int, full bool) {
	t.Helper()
	require.NotNil(t, r.m.store)
	for i := 0; i < n; i++ {
		row := minuteRow{At: start.Add(time.Duration(i) * time.Minute).UnixMilli(), Load1: 1, NCPU: 4, Mem: 40}
		if full {
			row.Full, row.FullTicks = true, 12
		}
		require.NoError(t, r.m.store.InsertMinute(row))
	}
}

// With the pool as the source the report is the old timeline up to where wwd's begins and wwd's minutes from there on: no minute
// is counted twice and the coverage shows the whole reach.
func TestReport_PoolSourceJoinsTheOldTimelineAndWWDs(t *testing.T) {
	r := newReportRig(t)
	seedOld(t, r, poolDay0, 10, true) // minutes 0..9 of the old timeline, all full
	// wwd has minutes 5..14: it overlaps the old rows from 5, and wwd's rows win from there
	for i := 5; i < 15; i++ {
		r.history = append(r.history, minuteFacts(i, 1, 8000, 1, wire.ZoneNormal, 1, 0))
	}
	rep := r.get(t, "24h")
	assert.Equal(t, 15, rep.Coverage.Minutes, "5 old + 10 from wwd")
	assert.Equal(t, poolDay0.UnixMilli(), rep.Coverage.From)
	assert.Equal(t, poolDay0.Add(14*time.Minute).UnixMilli(), rep.Coverage.To)
	assert.Equal(t, 10, rep.Heavy.Minutes, "wwd's minutes carry the running heavy command")
	assert.InDelta(t, 5.0/15, rep.Full.Share, 1e-9, "only the 5 old minutes were full")
	require.Len(t, r.asked, 1)
	assert.Equal(t, wire.Step1m, r.asked[0].Step)
	assert.Equal(t, r.now.Add(-24*time.Hour).Truncate(time.Second), r.asked[0].Since.Truncate(time.Second))
}

// The ssh source, or a wwd that cannot be asked, or one with no minutes yet: the report is the old timeline's alone.
func TestReport_OldTimelineAloneWhenWWDIsNotTheSource(t *testing.T) {
	r := newReportRig(t)
	seedOld(t, r, poolDay0, 10, false)
	r.history = []wire.Facts{minuteFacts(5, 1, 8000, 1, wire.ZoneNormal, 1, 0)}

	r.row.cfg.Source, r.row.rev = hostconfig.FleetSourceSSH, 2
	assert.Equal(t, 10, r.get(t, "24h").Coverage.Minutes, "ssh source")
	assert.Empty(t, r.asked, "and wwd is not asked")

	r.row.cfg.Source, r.row.rev = hostconfig.FleetSourcePool, 3
	r.histErr = errors.New("wwd down")
	assert.Equal(t, 10, r.get(t, "24h").Coverage.Minutes, "wwd cannot be asked")

	r.histErr, r.history = nil, nil
	assert.Equal(t, 10, r.get(t, "24h").Coverage.Minutes, "wwd has no minutes yet")
}

// Without resources.db (it did not open) the pool's minutes alone still make a report.
func TestReport_PoolSourceWithoutTheOldDatabase(t *testing.T) {
	r := newReportRig(t)
	r.m.store = nil
	for i := 0; i < 3; i++ {
		r.history = append(r.history, minuteFacts(i, 1, 8000, 1, wire.ZoneNormal, 0, 2))
	}
	rep := r.get(t, "24h")
	assert.Equal(t, 3, rep.Coverage.Minutes)
	assert.Equal(t, 2, rep.Light.MaxRunning)
}
