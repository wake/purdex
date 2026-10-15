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

// The fixture day: 24 hours of a host with known minute rows, so that every figure of the report can be worked out by hand (the
// comments do).
var reportDay0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// fixtureMinutes: 1440 minute rows, minutes 700 to 709 missing. Hand figures:
//
//	full:  minutes 100-104 are full for 12 ticks each (one run of 5 minutes, 300 s, started at 100), minute 500 for 3 ticks (a run
//	       of 15 s): Σ full_ticks = 63 -> 315 s of 1430 × 60 = 85800 s covered.
//	heavy: minutes 200-202 have a heavy command running in the gate's columns (load1 9.5/11.2/10.0, mem 70/83.5/80), minute 203 one
//	       in the lease's column from before L2 (load1 20, mem 99), minute 204 two at once (load1 4, mem 55): 5 minutes, most 2.
//	light: at most 3 (minute 300).
//	gate:  minutes 400-401 have a command waiting (1, then 4): 2 minutes, longest queue 4.
//	zones: minutes 600-602 carry zone seconds, 60 s each: normal 60+30+0, warning 0+30+45, danger 0+0+15 of 180 s
//	       (the rows with no zone seconds are left out).
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
			r.HeavyRunning, r.Load1, r.Mem = 1, 9.5, 70
		case i == 201:
			r.HeavyRunning, r.Load1, r.Mem = 1, 11.2, 83.5
		case i == 202:
			r.HeavyRunning, r.Load1, r.Mem = 1, 10, 80
		case i == 203:
			r.HeavyHeld, r.Load1, r.Mem = 1, 20, 99
		case i == 204:
			r.HeavyRunning, r.Load1, r.Mem = 2, 4, 55
		case i == 300:
			r.LightRunning = 3
		case i == 301:
			r.LightRunning = 2
		case i == 400:
			r.GateWaiting = 1
		case i == 401:
			r.GateWaiting = 4
		case i == 600:
			r.SampledS, r.NormalS = 60, 60
		case i == 601:
			r.SampledS, r.NormalS, r.WarningS = 60, 30, 30
		case i == 602:
			r.SampledS, r.WarningS, r.DangerS = 60, 45, 15
		}
		out = append(out, r)
	}
	return out
}

func seedReportDay(t *testing.T, s *leaseStore) {
	t.Helper()
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
	minutes, err := s.ReportMinutes(since, until)
	if err != nil {
		t.Fatal(err)
	}
	rep := resources.BuildReport(since, until, minutes)
	got, _ := json.MarshalIndent(rep, "", "  ")
	goldenCheck(t, "report-day.json", append(got, '\n'))

	// The figures worked out by hand above, so the golden file is not just whatever the code printed.
	want := resources.Report{
		Since: since, Until: until,
		Coverage: resources.ReportCoverage{From: since, To: reportDay0.Add(1439 * time.Minute).UnixMilli(), Minutes: 1430},
		Full:     resources.ReportFull{Share: 315.0 / 85800.0, Runs: 2, LongestS: 300},
		Heavy:    resources.ReportHeavy{Minutes: 5, MaxRunning: 2, MaxLoad1: 20, MaxMem: 99},
		Light:    resources.ReportLight{MaxRunning: 3},
		Gate:     resources.ReportGate{WaitingMinutes: 2, MaxWaiting: 4},
		Zones:    resources.ReportZones{Minutes: 3, SampledS: 180, Normal: 90.0 / 180, Warning: 75.0 / 180, Danger: 15.0 / 180},
	}
	wb, _ := json.MarshalIndent(want, "", "  ")
	if !bytes.Equal(got, wb) {
		t.Errorf("the report is not the hand-worked one:\n--- got\n%s\n--- hand\n%s", got, wb)
	}
}

// A period the timeline does not reach shows in coverage, adds nothing to the full figures, and a missing minute is no data (not a
// zero).
func TestReport_CoverageAndMissingMinutes(t *testing.T) {
	rep := resources.BuildReport(0, 1, nil)
	if rep.Coverage.Minutes != 0 || rep.Full.Share != 0 || rep.Heavy.Minutes != 0 {
		t.Errorf("empty report = %+v", rep)
	}
	// 2 minute rows, 30 full ticks (150 s) over 2 × 60 s: capped at 1.
	rep = resources.BuildReport(0, 1, []resources.ReportMinute{{At: 60000, FullTicks: 12}, {At: 0, FullTicks: 18}})
	if rep.Coverage.From != 0 || rep.Coverage.To != 60000 || rep.Full.Share != 1 {
		t.Errorf("report = %+v", rep)
	}
}

// The route: since is validated, the answer is the report of the minute rows in the period, and rows outside it are not in it. The
// lease tables are not read: a lease row in the period changes nothing.
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
	// A minute 2 hours ago is in a 3h report and not in a 1h one.
	if err := f.m.store.InsertMinute(minuteRow{At: f.nowMS() - 2*3600*1000, NCPU: 10, HeavyRunning: 1}); err != nil {
		t.Fatal(err)
	}
	r := baseRow("old", "c-old")
	r.CreatedAt = f.nowMS() - 30*60*1000
	mustCreate(t, f.m.store, r)
	minutes := func(q string) int {
		rec := get(q)
		var rep resources.Report
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &rep) != nil {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
		return rep.Heavy.Minutes
	}
	if minutes("?since=3h") != 1 || minutes("?since=1h") != 0 || minutes("") != 1 || minutes("?since=14d") != 1 {
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

// The gate's columns and the lease's column both make a heavy minute; the figures are the larger of the two.
func TestReport_ReadsTheGatesColumns(t *testing.T) {
	s := newTestStore(t)
	at := reportDay0.UnixMilli()
	for i, row := range []minuteRow{
		{At: at, NCPU: 10, HeavyHeld: 2},
		{At: at + 60000, NCPU: 10, HeavyRunning: 1, LightRunning: 3, GateWaiting: 2},
	} {
		if err := s.InsertMinute(row); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
	}
	minutes, err := s.ReportMinutes(at, at+3*60000)
	if err != nil {
		t.Fatal(err)
	}
	if len(minutes) != 2 || minutes[0].HeavyRunning != 2 || minutes[1].HeavyRunning != 1 || minutes[1].LightRunning != 3 || minutes[1].GateWaiting != 2 {
		t.Fatalf("minutes = %+v", minutes)
	}
}
