package poolmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// qualificationWWD answers GET /v1/qualification with the given status and body and records what it was asked.
func qualificationWWD(t *testing.T, s *seen, status int, body string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/qualification", func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return mux
}

const qualJSON = `{"host":"air26","tasks":[{"label":"task:purdex/tsc","reference":"air26","hold":"reference","progress":"idle"}],"recent":[]}`

// A host's tasks on two axes, asked of the local wwd with that host (WWD-7b2-0a-2); a device may read it.
func TestQualification_ReadsAHostThroughTheLocalWWD(t *testing.T) {
	var s seen
	mux := newModule(t, fakeWWD(t, qualificationWWD(t, &s, 200, qualJSON)))
	for name, ctx := range map[string]func(context.Context) context.Context{"admin": asAdmin, "device": asDevice} {
		rr := send(mux, "GET", "/api/pool/hosts/air26/qualification", "", ctx)
		require.Equal(t, 200, rr.Code, "%s: %s", name, rr.Body.String())
		assert.JSONEq(t, `{"host":"air26","known":true,"tasks":[{"label":"task:purdex/tsc","reference":"air26","hold":"reference","progress":"idle"}],"recent":[]}`, rr.Body.String(), name)
	}
	assert.Contains(t, s.all()[0], "GET /v1/qualification host=air26")
}

// A host whose wwd predates the route answers 404 through the forward: known false with empty lists, never a 500.
func TestQualification_AnOldHostIsNotKnownNot500(t *testing.T) {
	var s seen
	mux := newModule(t, fakeWWD(t, qualificationWWD(t, &s, 404, `404 page not found`)))
	rr := send(mux, "GET", "/api/pool/hosts/mserv/qualification", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"host":"mserv","known":false,"tasks":[],"recent":[]}`, rr.Body.String())
}

func TestQualification_TheErrorsAreTheOnesTheOtherReadsHave(t *testing.T) {
	var s seen
	mux := newModule(t, fakeWWD(t, qualificationWWD(t, &s, 404, `{"error":"unknown host","host":"nowhere"}`)))
	rr := send(mux, "GET", "/api/pool/hosts/nowhere/qualification", "", asAdmin)
	assert.Equal(t, 404, rr.Code)
	assert.Equal(t, "wwd_rejected", errCode(rr))
	mux = newModule(t, fakeWWD(t, qualificationWWD(t, &s, 502, `{"host":"air26","reason":"x"}`)))
	rr = send(mux, "GET", "/api/pool/hosts/air26/qualification", "", asAdmin)
	assert.Equal(t, 502, rr.Code)
	n := len(s.all())
	rr = send(mux, "GET", "/api/pool/hosts/a%00b/qualification", "", asAdmin)
	assert.Equal(t, 400, rr.Code)
	assert.Equal(t, n, len(s.all()))
}

// The answer is cached for a second, per host: two reads in a row reach wwd once.
func TestQualification_IsCachedForASecondPerHost(t *testing.T) {
	var s seen
	m := New() // New has the cache; newModule turns it off
	m.cl = fakeWWD(t, qualificationWWD(t, &s, 200, qualJSON))
	m.now = func() time.Time { return time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	for i := 0; i < 3; i++ {
		require.Equal(t, 200, send(mux, "GET", "/api/pool/hosts/air26/qualification", "", asAdmin).Code)
	}
	assert.Len(t, s.all(), 1)
	require.Equal(t, 200, send(mux, "GET", "/api/pool/hosts/mserv/qualification", "", asAdmin).Code)
	assert.Len(t, s.all(), 2, "another host is another key")
}

// pool.qualify.v1 is announced for the real module registered in a real core (same condition as pool.v1), and goes with it.
func TestInfo_AnnouncesPoolQualifyV1ForTheRealModule(t *testing.T) {
	t.Setenv("PDX_WWD_SOCK", filepath.Join(t.TempDir(), "none.sock"))
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	c.AddModule(m)
	require.NoError(t, m.Init(c))
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	caps := func() []any {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		list, _ := body["capabilities"].([]any)
		return list
	}
	require.Contains(t, caps(), "pool.qualify.v1")
	require.NoError(t, m.Stop(context.Background()))
	require.NotContains(t, caps(), "pool.qualify.v1")
}

// The answer means what the golden the App copies says (internal/wwd/wire/testdata): what wwd lists (no known), wrapped with known.
func TestQualification_AnswerMeansTheGoldenTheAppCopies(t *testing.T) {
	golden, err := os.ReadFile("../../wwd/wire/testdata/pool_host_qualification.json")
	require.NoError(t, err)
	var fromWWD map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(golden, &fromWWD))
	delete(fromWWD, "known")
	body, err := json.Marshal(fromWWD)
	require.NoError(t, err)
	var s seen
	mux := newModule(t, fakeWWD(t, qualificationWWD(t, &s, 200, string(body))))
	rr := send(mux, "GET", "/api/pool/hosts/mserv/qualification", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, string(golden), rr.Body.String())

	unknown, err := os.ReadFile("../../wwd/wire/testdata/pool_host_qualification_unknown.json")
	require.NoError(t, err)
	mux = newModule(t, fakeWWD(t, qualificationWWD(t, &s, 404, `404 page not found`)))
	rr = send(mux, "GET", "/api/pool/hosts/mserv/qualification", "", asAdmin)
	assert.JSONEq(t, string(unknown), rr.Body.String())
}
