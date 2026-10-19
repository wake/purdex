package wire

import (
	"net/url"
	"testing"
	"time"
)

func TestParseFactsSeriesQuery(t *testing.T) {
	since := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	base := url.Values{"since": {since.Format(time.RFC3339)}}
	with := func(k, v string) url.Values {
		c := url.Values{"since": base["since"]}
		c.Set(k, v)
		return c
	}
	q, err := ParseFactsSeriesQuery(base)
	if err != nil || !q.Since.Equal(since) || q.Step != Step5s || q.Limit != SeriesMaxPoints {
		t.Fatalf("defaults: %+v %v", q, err)
	}
	if q, err = ParseFactsSeriesQuery(with("limit", "2000")); err != nil || q.Limit != 2000 {
		t.Fatalf("limit 2000: %+v %v", q, err)
	}
	if q, err = ParseFactsSeriesQuery(with("step", "1m")); err != nil || q.Step != Step1m {
		t.Fatalf("step: %+v %v", q, err)
	}
	for _, bad := range []url.Values{
		{}, with("since", "yesterday"), with("step", "2m"),
		with("limit", "0"), with("limit", "-3"), with("limit", "abc"), with("limit", "21601"), with("limit", ""),
	} {
		if bad.Has("limit") && bad.Get("limit") == "" { // an empty limit is the default, not an error
			if _, err := ParseFactsSeriesQuery(bad); err != nil {
				t.Errorf("empty limit: %v", err)
			}
			continue
		}
		if _, err := ParseFactsSeriesQuery(bad); err == nil {
			t.Errorf("%v: expected an error", bad)
		}
	}
}

// Values round-trips with the parser, and names the host only when there is one.
func TestFactsSeriesQueryValues(t *testing.T) {
	q := FactsSeriesQuery{FactsHistoryQuery: FactsHistoryQuery{Since: time.Date(2026, 10, 18, 1, 2, 3, 0, time.UTC), Step: Step1m, Host: "mserv"}, Limit: 2000}
	v := q.Values()
	if v.Get("host") != "mserv" || v.Get("limit") != "2000" || v.Get("step") != "1m" {
		t.Fatalf("%v", v)
	}
	v.Del("host")
	got, err := ParseFactsSeriesQuery(v)
	if err != nil || got.Limit != 2000 || got.Step != Step1m || !got.Since.Equal(q.Since) {
		t.Fatalf("%+v %v", got, err)
	}
	q.Host = ""
	if q.Values().Has("host") {
		t.Fatal("no host: no host parameter")
	}
}

func TestSeriesResponseValidate(t *testing.T) {
	if err := (FactsSeriesResponse{Step: Step1m, Points: []SeriesPoint{}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (FactsSeriesResponse{Step: "2m"}).Validate(); err == nil {
		t.Error("a bad step must be refused")
	}
	if err := (FactsSeriesResponse{Step: Step1m, Points: make([]SeriesPoint, SeriesMaxPoints+1)}).Validate(); err == nil {
		t.Error("more points than the cap must be refused")
	}
}
