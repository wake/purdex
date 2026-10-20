package client

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestUsage_SendsTheQueryAndDecodes(t *testing.T) {
	var gotSince, gotLimit, gotAfter string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/usage", func(w http.ResponseWriter, r *http.Request) {
		gotSince, gotLimit, gotAfter = r.URL.Query().Get("since"), r.URL.Query().Get("limit"), r.URL.Query().Get("after")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"host":"air26","since":"2026-10-18T01:02:03Z","records":[],"truncated":false,"next_since":null,"next_after":null}`))
	})
	c := New(fakeWWD(t, mux))
	since := time.Date(2026, 10, 18, 1, 2, 3, 500_000_000, time.FixedZone("x", 8*3600))
	r, err := c.Usage(context.Background(), UsageQuery{Since: since, After: "j_17", Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	// the fraction of a second goes out as it is, in UTC: cutting it would skip rows of that second
	if gotSince != "2026-10-17T17:02:03.5Z" || gotLimit != "25" || gotAfter != "j_17" || r.Host != "air26" {
		t.Fatalf("since=%q limit=%q answer=%+v", gotSince, gotLimit, r)
	}
	// no query at all when nothing is asked: wwd's defaults apply
	if _, err := c.Usage(context.Background(), UsageQuery{}); err != nil || gotSince != "" || gotLimit != "" || gotAfter != "" {
		t.Fatalf("since=%q limit=%q err=%v", gotSince, gotLimit, err)
	}
}

func TestUsage_RefusesWhatTheContractRefuses(t *testing.T) {
	for name, body := range map[string]string{
		"truncated alone":  `{"host":"air26","since":"2026-10-18T01:02:03Z","records":[],"truncated":true,"next_since":null,"next_after":null}`,
		"no host":          `{"host":"","since":"2026-10-18T01:02:03Z","records":[],"truncated":false,"next_since":null,"next_after":null}`,
		"a record missing": `{"host":"air26","since":"2026-10-18T01:02:03Z","records":[{}],"truncated":false,"next_since":null,"next_after":null}`,
	} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/usage", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		_, err := New(fakeWWD(t, mux)).Usage(context.Background(), UsageQuery{})
		if !errors.Is(err, ErrBadResponse) {
			t.Errorf("%s: err = %v, want ErrBadResponse", name, err)
		}
	}
}
