package poolmod

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exceptionsWWD answers GET /v1/exceptions with the given status and body and records what it was asked.
func exceptionsWWD(t *testing.T, s *seen, status int, body string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/exceptions", func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return mux
}

const holdsJSON = `[{"name":"pkg","until":"2026-10-19T09:00:00Z","pid":null,"holds":["heavy","light"]}]`

// The holds of a host's exceptions, asked of the local wwd with that host (G4); a device may read it.
func TestExceptions_ReadsAHostsHoldsThroughTheLocalWWD(t *testing.T) {
	var s seen
	mux := newModule(t, fakeWWD(t, exceptionsWWD(t, &s, 200, holdsJSON)))
	for name, ctx := range map[string]func(context.Context) context.Context{"admin": asAdmin, "device": asDevice} {
		rr := send(mux, "GET", "/api/pool/hosts/air26/exceptions", "", ctx)
		require.Equal(t, 200, rr.Code, "%s: %s", name, rr.Body.String())
		assert.JSONEq(t, `{"host":"air26","known":true,"exceptions":`+holdsJSON+`}`, rr.Body.String(), name)
	}
	assert.Contains(t, s.all()[0], "GET /v1/exceptions host=air26")
}

// A host whose wwd predates the route answers 404 through the forward: the reader is told "not known", never a 500.
func TestExceptions_AnOldHostIsNotKnownNot500(t *testing.T) {
	var s seen
	mux := newModule(t, fakeWWD(t, exceptionsWWD(t, &s, 404, `404 page not found`)))
	rr := send(mux, "GET", "/api/pool/hosts/mserv/exceptions", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"host":"mserv","known":false,"exceptions":[]}`, rr.Body.String())
}

func TestExceptions_TheErrorsAreTheOnesTheOtherReadsHave(t *testing.T) {
	var s seen
	// an alias the local wwd does not know: its own 404 is passed on, as wwd_rejected
	mux := newModule(t, fakeWWD(t, exceptionsWWD(t, &s, 404, `{"error":"unknown host","host":"nowhere"}`)))
	rr := send(mux, "GET", "/api/pool/hosts/nowhere/exceptions", "", asAdmin)
	assert.Equal(t, 404, rr.Code)
	assert.Equal(t, "wwd_rejected", errCode(rr))
	// a peer that could not be reached: wwd's 502 stays 502
	mux = newModule(t, fakeWWD(t, exceptionsWWD(t, &s, 502, `{"host":"air26","reason":"x"}`)))
	rr = send(mux, "GET", "/api/pool/hosts/air26/exceptions", "", asAdmin)
	assert.Equal(t, 502, rr.Code)
	// a bad alias never reaches wwd
	n := len(s.all())
	rr = send(mux, "GET", "/api/pool/hosts/a%00b/exceptions", "", asAdmin)
	assert.Equal(t, 400, rr.Code)
	assert.Equal(t, n, len(s.all()))
}

// The answer means what the golden the App copies says (internal/wwd/wire/testdata): what wwd lists, wrapped as {host, known, exceptions}.
// The comparison is of the JSON value (keys, types, values), not of the bytes: the golden is indented for reading, the daemon writes
// compact JSON. What the App's fixtures-sync compares is the copied file with this file, not the daemon's bytes.
func TestExceptions_AnswerMeansTheGoldenTheAppCopies(t *testing.T) {
	golden, err := os.ReadFile("../../wwd/wire/testdata/pool_host_exceptions.json")
	require.NoError(t, err)
	var g struct {
		Exceptions json.RawMessage `json:"exceptions"`
	}
	require.NoError(t, json.Unmarshal(golden, &g))
	var s seen
	mux := newModule(t, fakeWWD(t, exceptionsWWD(t, &s, 200, string(g.Exceptions))))
	rr := send(mux, "GET", "/api/pool/hosts/air26/exceptions", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, string(golden), rr.Body.String())

	unknown, err := os.ReadFile("../../wwd/wire/testdata/pool_host_exceptions_unknown.json")
	require.NoError(t, err)
	mux = newModule(t, fakeWWD(t, exceptionsWWD(t, &s, 404, `404 page not found`)))
	rr = send(mux, "GET", "/api/pool/hosts/mserv/exceptions", "", asAdmin)
	assert.JSONEq(t, string(unknown), rr.Body.String())
}
