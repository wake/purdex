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

// The answers are kept for a second per key: two hosts asked inside it are two keys (a host's chart is never another's), and the same
// question twice is one wwd call.
func TestSeriesCacheKeysTheHostAndTheWindow(t *testing.T) {
	base := baseFacts(t)
	t0 := time.Date(2026, 10, 18, 11, 30, 0, 0, time.UTC)
	calls := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query()
		calls[v.Get("host")+"|"+v.Get("since")+"|"+v.Get("step")]++
		idle := 10.0
		if v.Get("host") == "air19" {
			idle = 90
		}
		serve(historyOf(v.Get("step"), rec(base, t0, wire.ZoneNormal, idle, 1, 1, nil, nil, wire.Slots{})))(w, r)
	})
	m := New()
	m.cl = fakeWWD(t, mux)
	m.now = func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }
	srv := http.NewServeMux()
	m.RegisterRoutes(srv)

	idleOf := func(path string) float64 {
		rr := call(srv, path)
		require.Equal(t, 200, rr.Code, rr.Body.String())
		var got SeriesResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
		require.Len(t, got.Points, 1)
		return got.Points[0].CPUIdlePct
	}
	assert.Equal(t, 10.0, idleOf("/api/pool/facts/series?host=mserv"))
	assert.Equal(t, 90.0, idleOf("/api/pool/facts/series?host=air19"), "another host inside the TTL is another answer")
	assert.Equal(t, 10.0, idleOf("/api/pool/facts/series?host=mserv"))
	assert.Equal(t, 1, calls["mserv|2026-10-18T11:00:00Z|5s"], "the same question twice is one wwd call")
	assert.Equal(t, 2, len(calls))
}

// A big answer is not kept: a raw history of megabytes would sit in the cache until the next store, so asking twice is two wwd calls.
func TestBigAnswersAreNotCached(t *testing.T) {
	base := baseFacts(t)
	t0 := time.Date(2026, 10, 18, 11, 30, 0, 0, time.UTC)
	recs := []wire.Facts{rec(base, t0, wire.ZoneNormal, 10, 1, 1, nil, nil, wire.Slots{})}
	for n := maxCachedBody/len(historyOf("1m", recs...))*12/10 + 2; n > 0; n-- {
		recs = append(recs, recs[0])
	}
	require.Greater(t, len(historyOf("1m", recs...)), maxCachedBody)
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) {
		calls++
		serve(historyOf("1m", recs...))(w, r)
	})
	m := New() // New has the cache; a module built bare keeps nothing
	m.cl = fakeWWD(t, mux)
	m.now = func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }
	srv := http.NewServeMux()
	m.RegisterRoutes(srv)
	require.Equal(t, 200, call(srv, "/api/pool/facts/history?since=2026-10-18T11:00:00Z&step=1m").Code)
	require.Equal(t, 200, call(srv, "/api/pool/facts/history?since=2026-10-18T11:00:00Z&step=1m").Code)
	assert.Equal(t, 2, calls)
	// a small answer, same setup, is kept (so the test above is about size and not about the cache being off)
	small := 0
	mux.HandleFunc("GET /v1/pool", func(w http.ResponseWriter, r *http.Request) { small++; serve(fixture(t, "pool.json"))(w, r) })
	call(srv, "/api/pool/hosts")
	call(srv, "/api/pool/hosts")
	assert.Equal(t, 1, small)
}
