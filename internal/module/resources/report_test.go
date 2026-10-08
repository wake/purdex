package resourcesmod

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden report files")

// The fixture day: 24 hours of a host with known rows, so that every figure of
// the report can be worked out by hand (the comments do).
var reportDay0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

type fixtureLease struct {
	id, kind         string
	weight           int
	createdOffsetMin int // minutes after the day's start; negative: before it
	granted          bool
	recorded         bool
	path             string
	waitedMS         int64
	r2               bool
	peak, mean       float64
	samples          int
}

var fixtureLeases = []fixtureLease{
	{"l01", "test-full", 35, 10, true, true, resources.PathImmediate, 0, false, 40, 30, 10},
	{"l02", "test-full", 35, 20, true, true, resources.PathWaited, 12000, true, 55, 41, 20},
	{"l03", "test-full", 35, 30, true, true, resources.PathOverrun, 300000, true, 50, 36, 15},
	{"l04", "build", 35, 40, true, true, resources.PathImmediate, 0, false, 20, 12, 8},
	{"l05", "build", 35, 50, true, true, resources.PathWaited, 4000, true, 28, 18, 12},
	{"l06", "test-pkg", 15, 60, true, true, resources.PathImmediate, 0, false, 10, 6, 5},
	{"l07", "", 20, 70, true, true, resources.PathImmediate, 0, false, 0, 0, 0},             // an explicit weight, never measured
	{"l08", "test-full", 35, 80, true, false, "", 0, false, 33, 20, 7},                      // granted before the decision record existed
	{"l09", "build", 35, 90, false, false, "", 0, false, 0, 0, 0},                           // cancelled before it was granted
	{"l10", "test-full", 35, -30, true, true, resources.PathImmediate, 0, false, 99, 99, 9}, // before the period
}

// fixtureMinutes: 1440 minute rows, minutes 700 to 709 missing. Hand figures:
//
//	full: minutes 100-104 are full for 12 ticks each (one run of 5 minutes, 300 s,
//	      started at 100; longest-so-far 60..300), minute 500 for 3 ticks (a run of
//	      15 s): Σ full_ticks = 63 -> 315 s of 1430 × 60 = 85800 s covered.
//	heavy: minutes 200-202 hold two heavy leases (load1 9.5/11.2/10.0,
//	      mem 70/83.5/80); 203 holds only one (load1 20, not counted).
func fixtureMinutes() []minuteRow {
	var out []minuteRow
	for i := 0; i < 1440; i++ {
		if i >= 700 && i <= 709 {
			continue
		}
		r := minuteRow{At: reportDay0.Add(time.Duration(i) * time.Minute).UnixMilli(), Load1: 3, NCPU: 10, Mem: 50, Measured: 40}
		switch {
		case i >= 100 && i <= 104:
			r.Full, r.FullTicks, r.FullLongestS = true, 12, 60*(i-99)
			if i == 100 {
				r.FullStarts = 1
			}
		case i == 500:
			r.Full, r.FullTicks, r.FullStarts, r.FullLongestS = true, 3, 1, 15
		case i == 200:
			r.HeavyHeld, r.Load1, r.Mem = 2, 9.5, 70
		case i == 201:
			r.HeavyHeld, r.Load1, r.Mem = 2, 11.2, 83.5
		case i == 202:
			r.HeavyHeld, r.Load1, r.Mem = 2, 10, 80
		case i == 203:
			r.HeavyHeld, r.Load1, r.Mem = 1, 20, 99
		}
		out = append(out, r)
	}
	return out
}

// seedReportDay writes the fixture into a store.
func seedReportDay(t *testing.T, s *leaseStore) {
	t.Helper()
	for _, l := range fixtureLeases {
		created := reportDay0.Add(time.Duration(l.createdOffsetMin) * time.Minute).UnixMilli()
		var granted any
		state := "ended"
		if l.granted {
			granted = created + l.waitedMS
		}
		rec := 0
		if l.recorded {
			rec = 1
		}
		_, err := s.db.Exec(`INSERT INTO resource_leases
			(id, client_id, state, kind, weight, holder_pid, scope, created_at, deadline_at, lease_until, granted_at, ended_at, end_reason,
			 waited_ms, peak_use, mean_use, samples, dec_recorded, dec_path, would_wait_r2)
			VALUES (?, ?, ?, ?, ?, 1, 'process', ?, ?, ?, ?, ?, 'released', ?, ?, ?, ?, ?, ?, ?)`,
			l.id, "c-"+l.id, state, l.kind, l.weight, created, created+300000, created+30000, granted, created+600000,
			l.waitedMS, l.peak, l.mean, l.samples, rec, l.path, b2i(l.r2))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range fixtureMinutes() {
		if err := s.InsertMinute(m); err != nil {
			t.Fatal(err)
		}
	}
}

func goldenCheck(t *testing.T, name string, got []byte) {
	t.Helper()
	path := "testdata/" + name
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// The whole day, through the real store and the pure function, byte for byte.
func TestReport_FixtureDayExactly(t *testing.T) {
	s := newTestStore(t)
	seedReportDay(t, s)
	since, until := reportDay0.UnixMilli(), reportDay0.Add(24*time.Hour).UnixMilli()
	leases, minutes, err := s.ReportRows(since, until)
	if err != nil {
		t.Fatal(err)
	}
	rep := resources.BuildReport(since, until, leases, minutes)
	got, _ := json.MarshalIndent(rep, "", "  ")
	goldenCheck(t, "report-day.json", append(got, '\n'))

	// The figures worked out by hand above, so the golden file is not just
	// whatever the code printed.
	want := resources.Report{
		Since: since, Until: until,
		Coverage:    resources.ReportCoverage{From: since, To: reportDay0.Add(1439 * time.Minute).UnixMilli(), Minutes: 1430},
		NotRecorded: 1,
		Requests: resources.ReportRequests{Total: 9, NotGranted: 1, ByKind: []resources.ReportKindCount{
			{Kind: "test-full", Count: 4}, {Kind: "build", Count: 3}, {Kind: "", Count: 1}, {Kind: "test-pkg", Count: 1}}},
		Paths:       resources.ReportPaths{Immediate: 4, Waited: 2, Overrun: 1},
		WaitMS:      resources.ReportWait{P50: 0, P90: 300000, Max: 300000},
		WouldWaitR2: 3,
		Full:        resources.ReportFull{Share: 315.0 / 85800.0, Runs: 2, LongestS: 300},
		Heavy:       resources.ReportHeavy{Minutes: 3, MaxLoad1: 11.2, MaxMem: 83.5},
		Kinds: []resources.ReportKind{
			{Kind: "build", N: 2, Weight: 35, PeakMax: 28, MeanAvg: 15},
			{Kind: "test-full", N: 3, Weight: 35, PeakMax: 55, MeanAvg: 107.0 / 3.0}, // l01, l02, l03: l08 was granted before the record existed
			{Kind: "test-pkg", N: 1, Weight: 15, PeakMax: 10, MeanAvg: 6},
		},
	}
	wb, _ := json.MarshalIndent(want, "", "  ")
	if !bytes.Equal(got, wb) {
		t.Errorf("the report is not the hand-worked one:\n--- got\n%s\n--- hand\n%s", got, wb)
	}
}

// Nearest-rank percentiles (rank = ceil(p/100 × n)), on small known lists.
func TestReport_NearestRank(t *testing.T) {
	mk := func(ws ...int64) []resources.ReportLease {
		var out []resources.ReportLease
		for _, w := range ws {
			out = append(out, resources.ReportLease{GrantedAt: 1, Recorded: true, Path: resources.PathWaited, WaitedMS: w})
		}
		return out
	}
	for _, c := range []struct {
		waits         []int64
		p50, p90, max int64
	}{
		{[]int64{10}, 10, 10, 10},
		{[]int64{10, 20}, 10, 20, 20},                      // rank 1, 2
		{[]int64{50, 10, 40, 20, 30}, 30, 50, 50},          // rank 3, 5
		{[]int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 5, 9, 10}, // rank 5, 9
		{nil, 0, 0, 0},
	} {
		got := resources.BuildReport(0, 1, mk(c.waits...), nil).WaitMS
		if got.P50 != c.p50 || got.P90 != c.p90 || got.Max != c.max {
			t.Errorf("%v: %+v, want %d/%d/%d", c.waits, got, c.p50, c.p90, c.max)
		}
	}
}

// A period the timeline does not reach shows in coverage, adds nothing to the
// full figures, and a missing minute is no data (not a zero).
func TestReport_CoverageAndMissingMinutes(t *testing.T) {
	rep := resources.BuildReport(0, 1, nil, nil)
	if rep.Coverage.Minutes != 0 || rep.Full.Share != 0 || rep.Requests.ByKind == nil || rep.Kinds == nil {
		t.Errorf("empty report = %+v", rep)
	}
	// 2 minute rows, 30 full ticks (150 s) over 2 × 60 s: capped at 1.
	rep = resources.BuildReport(0, 1, nil, []resources.ReportMinute{{At: 60000, FullTicks: 12}, {At: 0, FullTicks: 18}})
	if rep.Coverage.From != 0 || rep.Coverage.To != 60000 || rep.Full.Share != 1 {
		t.Errorf("report = %+v", rep)
	}
}

// The route: since is validated, the answer is the report of the rows in the
// period, and rows outside it are not in it.
func TestReport_Route(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	mux := http.NewServeMux()
	f.m.RegisterRoutes(mux)
	get := func(q string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/resources/report"+q, nil))
		return rec
	}
	for _, bad := range []string{"?since=soon", "?since=-1h", "?since=0s", "?since=15d", "?since=337h", "?since=0d", "?since=213504d", "?since=106752d", "?since=9223372036854775807d", "?since=1.5d", "?since=24H", "?since=%2024h", "?since=7%20d"} {
		if rec := get(bad); rec.Code != 400 || apiCode(rec) != resources.ErrBadRequest {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body.String())
		}
	}
	// A lease created 2 hours ago is in a 3h report and not in a 1h one.
	r := baseRow("old", "c-old")
	r.CreatedAt = f.nowMS() - 2*3600*1000
	mustCreate(t, f.m.store, r)
	count := func(q string) int {
		rec := get(q)
		var rep resources.Report
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &rep) != nil {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
		return rep.Requests.Total
	}
	if count("?since=3h") != 1 || count("?since=1h") != 0 || count("") != 1 || count("?since=14d") != 1 {
		t.Error("the period does not select the rows")
	}
	// Reading the report samples nothing and writes nothing.
	s := f.m.sampler.(*fakeSampler)
	before := s.calls.Load()
	get("?since=24h")
	if s.calls.Load() != before {
		t.Error("the report sampled")
	}
}

// A lease granted before the decision record existed is in no per-kind figure
// either, whatever it measured.
func TestReport_UnrecordedLeaseIsInNoKindFigure(t *testing.T) {
	rep := resources.BuildReport(0, 1, []resources.ReportLease{
		{Kind: "build", Weight: 35, GrantedAt: 1, Recorded: true, Path: resources.PathImmediate, Samples: 4, PeakUse: 20, MeanUse: 10},
		{Kind: "build", Weight: 35, GrantedAt: 1, Recorded: false, Samples: 9, PeakUse: 90, MeanUse: 80},
	}, nil)
	if len(rep.Kinds) != 1 || rep.Kinds[0].N != 1 || rep.Kinds[0].PeakMax != 20 || rep.Kinds[0].MeanAvg != 10 || rep.NotRecorded != 1 {
		t.Errorf("report = %+v", rep)
	}
}
