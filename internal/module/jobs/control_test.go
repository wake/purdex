package jobsmod

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

var (
	asAdmin  = devices.WithAdmin
	asDevice = func(c context.Context) context.Context {
		return devices.WithPrincipal(c, devices.Principal{ID: "d_abc", PairingID: "p_1"})
	}
)

// seen is what the fake wwd was asked: "METHOD path query body".
type seen struct {
	mu    sync.Mutex
	calls []string
}

func (s *seen) add(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path+" "+r.URL.RawQuery+" "+string(b))
}

func (s *seen) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// controlWWD answers cancel and position with the given status (a 2xx carries the job), and /v1/info with the alias "mlab".
func controlWWD(t *testing.T, s *seen, status int, body string) *client.Client {
	mux := http.NewServeMux()
	answer := func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		if status/100 == 2 {
			writeJSON(w, status, jobOf(t, "j_a", wire.StateCancelled))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	mux.HandleFunc("POST /v1/jobs/{id}/cancel", answer)
	mux.HandleFunc("POST /v1/jobs/{id}/position", answer)
	mux.HandleFunc("GET /v1/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, wire.Info{Alias: "mlab", Proto: wire.Proto, Labels: []string{}, Fingerprint: map[string]string{}})
	})
	return fakeWWD(t, mux)
}

func post(mux *http.ServeMux, path, body string, ctx func(context.Context) context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if ctx != nil {
		req = req.WithContext(ctx(req.Context()))
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestCancelGoesToTheNamedHostThroughTheLocalWWD(t *testing.T) {
	var s seen
	mux := newMux(t, controlWWD(t, &s, 200, ""))

	rr := post(mux, "/api/pool/jobs/j_a/cancel?host=air26", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"id":"j_a"`, "wwd's job as it is then")

	rr = post(mux, "/api/pool/jobs/j_a/cancel", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())

	got := s.all()
	require.Len(t, got, 2)
	assert.Equal(t, "POST /v1/jobs/j_a/cancel host=air26 ", got[0])
	assert.Equal(t, "POST /v1/jobs/j_a/cancel  ", got[1], "no host: the local wwd's own job")
}

func TestPositionIsForTheLocalHostOnly(t *testing.T) {
	var s seen
	mux := newMux(t, controlWWD(t, &s, 200, ""))

	rr := post(mux, "/api/pool/jobs/j_a/position", `{"position":"front"}`, asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	rr = post(mux, "/api/pool/jobs/j_a/position?host=mlab", `{"position":"hold"}`, asAdmin) // naming the local host is fine
	require.Equal(t, 200, rr.Code, rr.Body.String())

	rr = post(mux, "/api/pool/jobs/j_a/position?host=air26", `{"position":"front"}`, asAdmin)
	assert.Equal(t, 400, rr.Code)
	assert.Equal(t, "host_not_local", errCode(rr))

	got := s.all()
	require.Len(t, got, 2, "the refused one never reached wwd")
	assert.Equal(t, `POST /v1/jobs/j_a/position  {"position":"front"}`, got[0], "wwd's position route takes no host at all")
	assert.Equal(t, `POST /v1/jobs/j_a/position  {"position":"hold"}`, got[1])
}

// A device (and anyone who is not the admin) is refused before wwd is asked.
func TestControlIsForTheAdminTokenOnly(t *testing.T) {
	var s seen
	mux := newMux(t, controlWWD(t, &s, 200, ""))
	for name, ctx := range map[string]func(context.Context) context.Context{
		"device":  asDevice,
		"nobody":  nil,
		"ticket":  func(c context.Context) context.Context { return devices.WithCaller(c, devices.Caller{}) },
		"dev+adm": func(c context.Context) context.Context { return asAdmin(asDevice(c)) }, // a principal wins over the mark
	} {
		for _, rt := range [][2]string{
			{"/api/pool/jobs/j_a/cancel", ""},
			{"/api/pool/jobs/j_a/position", `{"position":"front"}`},
		} {
			rr := post(mux, rt[0], rt[1], ctx)
			assert.Equal(t, 403, rr.Code, "%s %s", name, rt[0])
		}
	}
	assert.Empty(t, s.all(), "wwd is never asked for a refused caller")
}

func TestControlRefusesWhatWWDShouldNeverSee(t *testing.T) {
	var s seen
	mux := newMux(t, controlWWD(t, &s, 200, ""))
	for _, c := range []struct{ path, body string }{
		{"/api/pool/jobs/not%20an%20id/cancel", ""},
		{"/api/pool/jobs/j_a/cancel?host=a%00b", ""},
		{"/api/pool/jobs/j_a/position", `not json`},
		{"/api/pool/jobs/j_a/position", `{"position":"sideways"}`},
		{"/api/pool/jobs/j_a/position", `{"position":"front","surprise":1}`},
		{"/api/pool/jobs/j_a/position", strings.Repeat("x", 70000)},
		{"/api/pool/jobs/j_a/position?host=a%00b", `{"position":"front"}`},
	} {
		rr := post(mux, c.path, c.body, asAdmin)
		assert.Equal(t, 400, rr.Code, "%s -> %s", c.path, rr.Body.String())
		assert.Equal(t, "bad_request", errCode(rr), c.path)
	}
	assert.Empty(t, s.all())
}

// A position is set on one host, so the host it names must be one clear value: an empty or repeated one is not "the local host".
// The same for a cancel: wwd takes an empty host (or the first of two) as the local one, and a cancel is not undone.
func TestControlRefusesAnEmptyOrRepeatedHost(t *testing.T) {
	var s seen
	mux := newMux(t, controlWWD(t, &s, 200, ""))
	for _, q := range []string{"?host=", "?host=&host=mlab", "?host=&host=air26", "?host=mlab&host=air26", "?host=air26&host=air26",
		"?host=air26;x=y", "?host=air26&x=%zz"} { // a query that does not parse is not a request without a host
		for _, rt := range [][2]string{
			{"/api/pool/jobs/j_a/position", `{"position":"front"}`},
			{"/api/pool/jobs/j_a/cancel", ""},
		} {
			rr := post(mux, rt[0]+q, rt[1], asAdmin)
			assert.Equal(t, 400, rr.Code, rt[0]+q)
			assert.Equal(t, "bad_request", errCode(rr), rt[0]+q)
		}
	}
	assert.Empty(t, s.all(), "wwd is never asked")
}

// The reads a paired phone may make cannot use up the places a cancel needs.
func TestControlHasItsOwnInFlightPlaces(t *testing.T) {
	var s seen
	m := New()
	m.cl = controlWWD(t, &s, 200, "")
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	for i := 0; i < cap(m.inflight); i++ { // every read place taken
		m.inflight <- struct{}{}
	}
	assert.Equal(t, 429, get(mux, "/api/pool/jobs").Code, "the reads are turned away")
	rr := post(mux, "/api/pool/jobs/j_a/cancel", "", asAdmin)
	assert.Equal(t, 200, rr.Code, rr.Body.String())
	rr = post(mux, "/api/pool/jobs/j_a/position", `{"position":"front"}`, asAdmin)
	assert.Equal(t, 200, rr.Code, rr.Body.String())

	for i := 0; i < cap(m.control); i++ { // and the control places are their own bound
		m.control <- struct{}{}
	}
	rr = post(mux, "/api/pool/jobs/j_a/cancel", "", asAdmin)
	assert.Equal(t, 429, rr.Code)
	assert.Equal(t, ErrBusy, errCode(rr))
}

func TestControlRelaysWWDsOwnRefusalWithItsStatus(t *testing.T) {
	var s seen
	// a job that has ended is wwd's 409
	mux := newMux(t, controlWWD(t, &s, 409, `{"error":"job has ended","state":"succeeded"}`))
	for _, rt := range [][2]string{
		{"/api/pool/jobs/j_a/cancel", ""},
		{"/api/pool/jobs/j_a/position", `{"position":"back"}`},
	} {
		rr := post(mux, rt[0], rt[1], asAdmin)
		assert.Equal(t, 409, rr.Code, rt[0])
		assert.Equal(t, "wwd_rejected", errCode(rr), rt[0])
		assert.Contains(t, rr.Body.String(), "job has ended", rt[0])
	}
}

func TestControlWWDErrorsKeepTheirMeaning(t *testing.T) {
	var s seen
	// the peer cannot be reached: wwd's 502 with {host, reason}
	mux := newMux(t, controlWWD(t, &s, 502, `{"host":"air26","reason":"connection refused"}`))
	rr := post(mux, "/api/pool/jobs/j_a/cancel?host=air26", "", asAdmin)
	assert.Equal(t, 502, rr.Code)
	assert.Equal(t, ErrHostUnreachable, errCode(rr))

	// wwd not running
	mux = newMux(t, client.New(filepath.Join(t.TempDir(), "none.sock")))
	for _, rt := range [][2]string{
		{"/api/pool/jobs/j_a/cancel", ""},
		{"/api/pool/jobs/j_a/position?host=mlab", `{"position":"front"}`},
	} {
		rr := post(mux, rt[0], rt[1], asAdmin)
		assert.Equal(t, 503, rr.Code, rt[0])
		assert.Equal(t, ErrWWDUnavailable, errCode(rr), rt[0])
	}
}
