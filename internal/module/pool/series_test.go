package poolmod

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func f64(v float64) *float64 { return &v }

// baseFacts is a record wwd's decoder accepts (the fixture's); rec changes what a series reads.
func baseFacts(t *testing.T) wire.Facts {
	t.Helper()
	f, err := wire.Decode[wire.Facts](fixture(t, "facts_darwin.json"))
	require.NoError(t, err)
	return f
}

// rec is a facts record with just what a series reads set.
func rec(base wire.Facts, at time.Time, zone wire.Zone, idle float64, avail, pressure int, pageOut *float64, used []float64, h wire.Slots) wire.Facts {
	f := base
	f.At, f.Disk = at, nil
	f.Derived.Zone, f.Derived.Headroom = zone, h
	f.CPU.IdlePct = idle
	f.Mem.AvailableMB, f.Mem.Pressure, f.Mem.PageOutRate = avail, pressure, pageOut
	for _, u := range used {
		f.Disk = append(f.Disk, wire.Disk{Mount: "/", UsedPct: u})
	}
	return f
}

func historyOf(step string, recs ...wire.Facts) []byte {
	b, _ := json.Marshal(wire.FactsHistoryResponse{Step: step, Records: recs})
	return b
}

func TestSeriesOfProjectsEachRecordToAPoint(t *testing.T) {
	base := baseFacts(t)
	t0 := time.Date(2026, 10, 18, 11, 0, 0, 0, time.UTC)
	got := seriesOf(wire.FactsHistoryResponse{Step: "1m", Records: []wire.Facts{
		rec(base, t0, wire.ZoneNormal, 80.5, 4000, 1, f64(12), []float64{40, 91.5, 60}, wire.Slots{Heavy: 1, Light: 2}),
		rec(base, t0.Add(time.Minute), wire.ZoneWarning, 20, 900, 2, nil, nil, wire.Slots{}),
	}})
	require.Len(t, got.Points, 2)
	require.False(t, got.Truncated)
	p := got.Points[0]
	assert.Equal(t, t0, p.At)
	assert.Equal(t, wire.ZoneNormal, p.Zone)
	assert.Equal(t, 80.5, p.CPUIdlePct)
	assert.Equal(t, 4000, p.MemAvailableMB)
	assert.Equal(t, 1, p.Pressure)
	require.NotNil(t, p.PageOutRate)
	assert.Equal(t, 12.0, *p.PageOutRate)
	require.NotNil(t, p.DiskMaxUsedPct)
	assert.Equal(t, 91.5, *p.DiskMaxUsedPct, "the fullest disk")
	assert.Equal(t, wire.Slots{Heavy: 1, Light: 2}, p.Headroom)
	q := got.Points[1]
	assert.Nil(t, q.PageOutRate)
	assert.Nil(t, q.DiskMaxUsedPct, "no disk named: null, not 0")
	assert.Equal(t, wire.ZoneWarning, q.Zone)
}

func TestSeriesOfKeepsTheLatestPointsWithinTheCap(t *testing.T) {
	base := baseFacts(t)
	t0 := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	recs := make([]wire.Facts, seriesMaxPoints+7)
	for i := range recs {
		recs[i] = rec(base, t0.Add(time.Duration(i)*time.Minute), wire.ZoneNormal, 50, 1, 1, nil, nil, wire.Slots{})
	}
	got := seriesOf(wire.FactsHistoryResponse{Step: "1m", Records: recs})
	require.Len(t, got.Points, seriesMaxPoints)
	assert.True(t, got.Truncated)
	assert.Equal(t, t0.Add(7*time.Minute), got.Points[0].At, "the oldest 7 are the ones left out")
	assert.Equal(t, recs[len(recs)-1].At, got.Points[len(got.Points)-1].At)
	empty := seriesOf(wire.FactsHistoryResponse{Step: "5s"})
	assert.NotNil(t, empty.Points, "no records: an empty array, not null")
	b, _ := json.Marshal(empty)
	assert.Contains(t, string(b), `"points":[]`)
}

// The route: the host, since and step go to wwd; since may be a duration; the step defaults to 5 s for a short window and 1 m for a long one.
func TestSeriesRouteAsksWWDAndProjects(t *testing.T) {
	var q [][3]string
	base := baseFacts(t)
	t0 := time.Date(2026, 10, 18, 11, 30, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query()
		q = append(q, [3]string{v.Get("host"), v.Get("since"), v.Get("step")})
		serve(historyOf(v.Get("step"), rec(base, t0, wire.ZoneNormal, 70, 3000, 1, nil, []float64{50}, wire.Slots{Heavy: 1})))(w, r)
	})
	m := newModule(t, fakeWWD(t, mux)) // now is 2026-10-18T12:00:00Z

	rr := call(m, "/api/pool/facts/series?host=mserv&since=1h")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	var got SeriesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "5s", got.Step)
	require.Len(t, got.Points, 1)
	assert.Equal(t, 70.0, got.Points[0].CPUIdlePct)

	call(m, "/api/pool/facts/series?since=24h")
	call(m, "/api/pool/facts/series?since=2026-10-18T01:00:00Z&step=1m")
	call(m, "/api/pool/facts/series")
	assert.Equal(t, [][3]string{
		{"mserv", "2026-10-18T11:00:00Z", "5s"}, // 1h back from the clock, an hour is a fine window
		{"", "2026-10-17T12:00:00Z", "1m"},      // a day is a long one
		{"", "2026-10-18T01:00:00Z", "1m"},
		{"", "2026-10-18T11:00:00Z", "5s"}, // the default: the last hour
	}, q)
}

func TestSeriesRouteRefusesWhatItCannotAnswer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", serve(historyOf("1m")))
	m := newModule(t, fakeWWD(t, mux))
	for _, p := range []string{"?since=yesterday", "?since=-1h", "?since=0s", "?since=337h", "?since=2025-01-01T00:00:00Z", "?since=2026-10-19T00:00:00Z", "?step=2m", "?since=1h&step=1s"} {
		rr := call(m, "/api/pool/facts/series"+p)
		assert.Equal(t, 400, rr.Code, p)
		assert.Equal(t, ErrBadRequest, errCode(rr), p)
	}
	assert.Equal(t, 200, call(m, "/api/pool/facts/series?since=336h").Code, "14 days is the longest wwd keeps")
	assert.Equal(t, 200, call(m, "/api/pool/facts/series?since=2026-10-05T12:00:00Z").Code, "an RFC 3339 time is held to the same window")
}

func TestSeriesRouteErrorsAreHistorys(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("host") == "nowhere" {
			http.Error(w, `{"error":"unknown host"}`, http.StatusNotFound)
			return
		}
		http.Error(w, "x", http.StatusInternalServerError)
	})
	m := newModule(t, fakeWWD(t, mux))
	rr := call(m, "/api/pool/facts/series?host=nowhere")
	assert.Equal(t, 404, rr.Code)
	assert.Equal(t, ErrWWDRejected, errCode(rr))
	rr = call(m, "/api/pool/facts/series")
	assert.Equal(t, 502, rr.Code)
	assert.Equal(t, ErrWWDError, errCode(rr))
}
