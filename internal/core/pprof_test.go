package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// limitSeconds lets 0..max and no seconds through to the profiler, refuses anything else before it starts.
// Mutation gates: off-by-one at max → red; no check → red.
func TestLimitSeconds(t *testing.T) {
	for _, c := range []struct {
		query string
		want  int
	}{{"", 200}, {"?seconds=0", 200}, {"?seconds=1", 200}, {"?seconds=60", 200}, {"?seconds=61", 400}, {"?seconds=-1", 400}, {"?seconds=abc", 400}, {"?seconds=1.5", 400}, {"?seconds=99999999999999999999", 400}} {
		ran := false
		h := limitSeconds(60, func(w http.ResponseWriter, _ *http.Request) { ran = true; w.WriteHeader(200) })
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("GET", "/debug/pprof/profile"+c.query, nil))
		if rec.Code != c.want || ran != (c.want == 200) {
			t.Errorf("%q: %d (ran %v), want %d", c.query, rec.Code, ran, c.want)
		}
	}
}
