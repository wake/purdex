package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// recordingMinutes is the real store that remembers how it was read.
type recordingMinutes struct {
	*store.Store
	all    int
	latest []int
}

func (r *recordingMinutes) LoadMinutes(since time.Time) ([]wire.Facts, error) {
	r.all++
	return r.Store.LoadMinutes(since)
}

func (r *recordingMinutes) LoadMinutesLatest(since time.Time, n int) ([]wire.Facts, error) {
	r.latest = append(r.latest, n)
	return r.Store.LoadMinutesLatest(since, n)
}

// A series reads as many stored minutes as it has points (and one more, to know it cut), not every minute since: the cost of a chart
// is its limit. The history still reads them all.
func TestHistoryLatestReadsOnlyTheNewestMinutes(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rec := &recordingMinutes{Store: db}
	s := statsSampler(&steer{load1: 1}, rec)
	tickN(s, 0, 12*6) // five finished minutes and the sixth open
	rec.all, rec.latest = 0, nil

	got := s.HistoryLatest(minuteStart(), wire.Step1m, 3)
	if len(rec.latest) != 1 || rec.latest[0] != 3 || rec.all != 0 {
		t.Fatalf("the store was read by LoadMinutes %d times and LoadMinutesLatest with %v", rec.all, rec.latest)
	}
	if len(got) < 3 || !got[len(got)-1].At.After(got[0].At) {
		t.Fatalf("records: %d, oldest first expected", len(got))
	}
	s.History(minuteStart(), wire.Step1m)
	if rec.all != 1 {
		t.Fatalf("the history reads them all: %d", rec.all)
	}
	// 5 s records are in memory: the limit does not matter
	if five := s.HistoryLatest(minuteStart(), wire.Step5s, 3); len(five) < 30 || len(rec.latest) != 1 {
		t.Fatalf("5 s: %d records, store reads %v", len(five), rec.latest)
	}
}

// Through the route: GET /v1/facts/series?limit=n reads n+1 stored minutes (so it knows it cut), answers the newest n, oldest first,
// and says truncated; with room to spare it is not truncated.
func TestSeriesRouteReadsLimitPlusOneMinutes(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rec := &recordingMinutes{Store: db}
	s := statsSampler(&steer{load1: 1}, rec)
	tickN(s, 0, 12*8) // seven finished minutes and the eighth open
	d := New(Options{HostID: "h1", Alias: "mlab", Version: "v0", Token: testToken, Sampler: s})
	rec.all, rec.latest = 0, nil

	path := "/v1/facts/series?step=1m&since=" + minuteStart().Format(time.RFC3339) + "&limit="
	w := do(d.SocketHandler(), "GET", path+"3", "")
	got, err := wire.Decode[wire.FactsSeriesResponse](w.Body.Bytes())
	if w.Code != 200 || err != nil || len(got.Points) != 3 || !got.Truncated {
		t.Fatalf("limit 3: %d %v %+v", w.Code, err, got)
	}
	if len(rec.latest) != 1 || rec.latest[0] != 4 || rec.all != 0 {
		t.Fatalf("the store was read by LoadMinutes %d times and LoadMinutesLatest with %v, want one read of 4", rec.all, rec.latest)
	}
	if !got.Points[0].At.Before(got.Points[2].At) {
		t.Fatalf("oldest first expected: %+v", got.Points)
	}
	w = do(d.SocketHandler(), "GET", path+"100", "")
	whole, err := wire.Decode[wire.FactsSeriesResponse](w.Body.Bytes())
	if err != nil || whole.Truncated || len(whole.Points) < 7 {
		t.Fatalf("limit 100: %v %+v", err, whole)
	}
}

// A store that cannot read the newest n (the plain MinuteStore) still answers: all of them.
func TestHistoryLatestWithAStoreThatCannotLimit(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := statsSampler(&steer{load1: 1}, struct{ MinuteStore }{db})
	tickN(s, 0, 12*4)
	if got := s.HistoryLatest(minuteStart(), wire.Step1m, 2); len(got) != len(s.History(minuteStart(), wire.Step1m)) {
		t.Fatalf("fell back to all of them: %d", len(got))
	}
}
