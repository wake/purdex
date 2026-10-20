package poolmod

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// observationsWWD answers the two observation reads with the given status and body and records what it was asked.
func observationsWWD(t *testing.T, s *seen, status int, body string) http.Handler {
	t.Helper()
	h := func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		s.mu.Lock()
		s.calls[len(s.calls)-1] += " q=" + r.URL.RawQuery // what the App's since and until became on the way
		s.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/observations", h)
	mux.HandleFunc("GET /v1/observations/undeclared", h)
	return mux
}

func obsRecord(id, started string) string {
	return `{"id":"` + id + `","host":"air26","session":"s1","via":"session","command":"go test","signature":"go test","started_at":"` + started +
		`","ended_at":null,"cpu_seconds":61.5,"cpu_peak":100,"mem_peak_mb":300,"procs_peak":3}`
}

const undeclaredBody = `{"host":"air26","since":"2026-10-13T00:00:00Z","groups":[{"signature":"go test","count":2,"cpu_seconds":130.5,"max_mem_mb":900,"last_at":"2026-10-20T01:00:00Z","last_session":"s1","last_via":"codex","last_command":"go test ./..."}],"truncated":false}`

// The two reads go through the local wwd with the host, and the query as the App gave it; the answer is wwd's, not rewritten. That a
// device token may not read them (the command lines are not for a phone until someone decides so) is the allow-list's, in cmd/pdx.
func TestObservations_ForwardAndReturnWhatWwdWrote(t *testing.T) {
	const next = "2026-10-20T01:00:00.123456789+08:00" // a zone and a fraction of a second: a cursor that must come back exactly
	body := `{"host":"air26","since":"2026-10-19T00:00:00Z","observations":[` + obsRecord("o_1_2", next) + `],"truncated":true,"next_until":"` + next + `"}`
	var s seen
	mux := newModule(t, fakeWWD(t, observationsWWD(t, &s, 200, body)))

	rr := send(mux, "GET", "/api/pool/hosts/air26/observations?since="+url.QueryEscape("2026-10-19T00:00:00Z")+"&until="+url.QueryEscape(next), "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, body, rr.Body.String())
	var got struct {
		NextUntil string `json:"next_until"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, next, got.NextUntil, "the cursor is not re-formatted")
	assert.Contains(t, rr.Body.String(), `"started_at":"`+next+`"`)
	assert.Contains(t, s.all()[0], "GET /v1/observations host=air26")
	assert.Contains(t, s.all()[0], "since=2026-10-19T00%3A00%3A00Z")
	assert.Contains(t, s.all()[0], "until=2026-10-20T01%3A00%3A00.123456789%2B08%3A00")

	var u seen
	mux = newModule(t, fakeWWD(t, observationsWWD(t, &u, 200, undeclaredBody)))
	rr = send(mux, "GET", "/api/pool/hosts/air26/undeclared?since="+url.QueryEscape("2026-10-13T00:00:00Z"), "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, undeclaredBody, rr.Body.String())
	assert.Contains(t, u.all()[0], "GET /v1/observations/undeclared host=air26")
}

// A bad since or until is refused here, with 400, and wwd is not asked.
func TestObservations_BadTimesAreRefusedBeforeWwdIsAsked(t *testing.T) {
	var s seen
	mux := newModule(t, fakeWWD(t, observationsWWD(t, &s, 200, undeclaredBody)))
	for _, path := range []string{
		"/api/pool/hosts/air26/observations?since=yesterday",
		"/api/pool/hosts/air26/observations?until=2026-10-20",
		"/api/pool/hosts/air26/observations?since=2026-10-19T00:00:00Z&until=nope",
		"/api/pool/hosts/air26/undeclared?since=1h",
		"/api/pool/hosts/a%00b/observations",
		"/api/pool/hosts/a%00b/undeclared",
	} {
		rr := send(mux, "GET", path, "", asAdmin)
		assert.Equal(t, 400, rr.Code, path)
		assert.Equal(t, "bad_request", errCode(rr), path)
	}
	assert.Empty(t, s.all(), "wwd was never asked")
}

// A host whose wwd predates the routes is "not known" with an empty answer, never a 500; the other errors are the other reads'.
func TestObservations_TheErrorsAreTheOnesTheOtherReadsHave(t *testing.T) {
	var s seen
	mux := newModule(t, fakeWWD(t, observationsWWD(t, &s, 404, `404 page not found`)))
	rr := send(mux, "GET", "/api/pool/hosts/mserv/observations", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"host":"mserv","known":false,"observations":[]}`, rr.Body.String())
	rr = send(mux, "GET", "/api/pool/hosts/mserv/undeclared", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"host":"mserv","known":false,"groups":[]}`, rr.Body.String())

	mux = newModule(t, fakeWWD(t, observationsWWD(t, &s, 404, `{"error":"unknown host","host":"nowhere"}`)))
	rr = send(mux, "GET", "/api/pool/hosts/nowhere/observations", "", asAdmin)
	assert.Equal(t, 404, rr.Code)
	assert.Equal(t, "wwd_rejected", errCode(rr))

	mux = newModule(t, fakeWWD(t, observationsWWD(t, &s, 502, `{"host":"air26","reason":"x"}`)))
	assert.Equal(t, 502, send(mux, "GET", "/api/pool/hosts/air26/undeclared", "", asAdmin).Code)

	mux = newModule(t, fakeWWD(t, observationsWWD(t, &s, 200, `{"host":"","since":"2026-10-20T01:00:00Z","observations":[],"truncated":false,"next_until":null}`)))
	rr = send(mux, "GET", "/api/pool/hosts/air26/observations", "", asAdmin)
	assert.Equal(t, 502, rr.Code, "an answer outside the contract")
	assert.Equal(t, "wwd_bad_response", errCode(rr))
}

// pagedWWD is a wwd that holds a few observations and answers like the real one: the newest `page` of those that began at or before `until`
// (inclusive), oldest first, truncated with next_until = the start of the oldest of them when more were left.
type pagedWWD struct {
	mu   sync.Mutex
	recs []struct{ id, at string } // oldest first
	page int
}

func (p *pagedWWD) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/observations", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		until := r.URL.Query().Get("until")
		var kept []struct{ id, at string }
		for _, rec := range p.recs {
			if until == "" || rec.at <= until { // the fixtures are all Z, so text order is time order
				kept = append(kept, rec)
			}
		}
		truncated := len(kept) > p.page
		if truncated {
			kept = kept[len(kept)-p.page:]
		}
		out := `{"host":"air26","since":"2026-10-19T00:00:00Z","observations":[`
		for i, rec := range kept {
			if i > 0 {
				out += ","
			}
			out += obsRecord(rec.id, rec.at)
		}
		out += `],"truncated":` + map[bool]string{true: "true", false: "false"}[truncated] + `,"next_until":`
		if truncated {
			out += `"` + kept[0].at + `"`
		} else {
			out += `null`
		}
		_, _ = w.Write([]byte(out + `}`))
	})
	return mux
}

// Paging back with next_until is inclusive (spec 8.4): the record that began at the boundary comes on both pages, and the reader drops the
// repeat by id. This is the expected behaviour, not a bug to be filtered here.
func TestObservations_NextUntilPagesBackAndTheBoundaryRecordComesTwice(t *testing.T) {
	p := &pagedWWD{page: 2, recs: []struct{ id, at string }{
		{"o_a", "2026-10-20T01:00:00Z"}, {"o_b", "2026-10-20T02:00:00Z"}, {"o_c", "2026-10-20T03:00:00Z"}, {"o_d", "2026-10-20T04:00:00Z"},
	}}
	mux := newModule(t, fakeWWD(t, p.handler()))
	type page struct {
		Observations []struct{ ID string }
		Truncated    bool
		NextUntil    *string `json:"next_until"`
	}
	read := func(path string) page {
		rr := send(mux, "GET", path, "", asAdmin)
		require.Equal(t, 200, rr.Code, rr.Body.String())
		var pg page
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &pg))
		return pg
	}
	ids := map[string]int{}
	path := "/api/pool/hosts/air26/observations"
	for i := 0; i < 5; i++ {
		pg := read(path)
		for _, o := range pg.Observations {
			ids[o.ID]++
		}
		if !pg.Truncated {
			break
		}
		require.NotNil(t, pg.NextUntil)
		path = "/api/pool/hosts/air26/observations?until=" + url.QueryEscape(*pg.NextUntil)
	}
	var all []string
	for id := range ids {
		all = append(all, id)
	}
	sort.Strings(all)
	assert.Equal(t, []string{"o_a", "o_b", "o_c", "o_d"}, all, "every record is reached by paging back")
	assert.Equal(t, 2, ids["o_c"], "the record at the boundary is on both pages")
	assert.Equal(t, 2, ids["o_b"], "and the next boundary too")
	assert.Equal(t, 1, ids["o_d"])
}

// The same read twice inside the cache window asks wwd once; a different until is a different read.
func TestObservations_AnswersAreKeptPerQuery(t *testing.T) {
	var s seen
	m := New()
	m.cl = fakeWWD(t, observationsWWD(t, &s, 200, undeclaredBody))
	m.now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	for i := 0; i < 2; i++ {
		require.Equal(t, 200, send(mux, "GET", "/api/pool/hosts/air26/observations?until=2026-10-20T01%3A00%3A00Z", "", asAdmin).Code)
	}
	assert.Len(t, s.all(), 1, "the second read is the first's answer")
	require.Equal(t, 200, send(mux, "GET", "/api/pool/hosts/air26/observations?until=2026-10-20T02%3A00%3A00Z", "", asAdmin).Code)
	assert.Len(t, s.all(), 2, "another until is another read")
}
