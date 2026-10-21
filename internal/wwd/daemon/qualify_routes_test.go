package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/qualify"
	"github.com/wake/purdex/internal/wwd/wire"
)

type fakeQualifier struct {
	asked    []string
	err      error
	inFlight []qualify.Started
	recent   []qualify.Record
	view     wire.HostQualification
	viewErr  error
}

func (f *fakeQualifier) Request(_ context.Context, task string) (qualify.Started, error) {
	f.asked = append(f.asked, task)
	if f.err != nil {
		return qualify.Started{}, f.err
	}
	return qualify.Started{Label: "task:" + task, Candidate: "j_cand", Reference: "j_ref"}, nil
}

func (f *fakeQualifier) Status() ([]qualify.Started, []qualify.Record) { return f.inFlight, f.recent }

func (f *fakeQualifier) Qualification() (wire.HostQualification, error) {
	return f.view, f.viewErr
}

func qualifyDaemon(t *testing.T, q Qualifier) *Daemon {
	t.Helper()
	return New(Options{HostID: "h1", Alias: "mserv", Version: "v0", Token: testToken, Qualifier: q})
}

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// A person's request is answered with the pair that was queued; the body is strict, and the route is the socket's alone.
func TestPostQualifyStartsAPairOnTheSocketOnly(t *testing.T) {
	q := &fakeQualifier{}
	d := qualifyDaemon(t, q)
	w := call(d.SocketHandler(), http.MethodPost, "/v1/qualify", `{"task":"purdex/tsc"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got wire.QualifyStarted
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, wire.QualifyStarted{Label: "task:purdex/tsc", Candidate: "j_cand", Reference: "j_ref"}, got)
	assert.Equal(t, []string{"purdex/tsc"}, q.asked)

	for name, tc := range map[string]struct{ path, body string }{
		"another host asked for": {"/v1/qualify?host=air26", `{"task":"purdex/tsc"}`},
		"an unknown field":       {"/v1/qualify", `{"task":"purdex/tsc","x":1}`},
		"no body":                {"/v1/qualify", ``},
		"a task label":           {"/v1/qualify", `{"task":"task:purdex/tsc"}`},
		"no repo":                {"/v1/qualify", `{"task":"tsc"}`},
		"a path":                 {"/v1/qualify", `{"task":"../x/tsc"}`},
	} {
		w := call(d.SocketHandler(), http.MethodPost, tc.path, tc.body)
		assert.Equal(t, http.StatusBadRequest, w.Code, name)
	}
	assert.Len(t, q.asked, 1, "nothing refused reached the coordinator")

	assert.Equal(t, http.StatusNotFound, call(d.TCPHandler(), http.MethodPost, "/v1/qualify", `{"task":"purdex/tsc"}`).Code, "no such route over TCP")
	assert.Equal(t, http.StatusNotFound, call(d.TCPHandler(), http.MethodGet, "/v1/qualify", ``).Code)
}

// Why a request was refused is told in the coordinator's own words, with a status that says which kind: 404 for a task this host has no
// qualify for, 409 for one it may not be asked about now, 500 for a failure.
func TestARefusedQualifyRequestSaysWhy(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		code int
	}{
		"unknown task":     {&qualify.UnknownTaskError{Name: "purdex/nope"}, http.StatusNotFound},
		"the reference":    {qualify.ErrReference, http.StatusConflict},
		"worker off":       {qualify.ErrWorkerOff, http.StatusConflict},
		"already has":      {qualify.ErrAlreadyHas, http.StatusConflict},
		"in flight":        {qualify.ErrInFlight, http.StatusConflict},
		"no definition":    {qualify.ErrNoDefinition, http.StatusConflict},
		"no qualify":       {qualify.ErrNoQualify, http.StatusConflict},
		"missing labels":   {&qualify.MissingError{Labels: []string{"tool:node24"}}, http.StatusConflict},
		"wrapped refusal":  {fmt.Errorf("checking: %w", qualify.ErrWorkerOff), http.StatusConflict},
		"snapshot failure": {errors.New("preparing the snapshot: no commit"), http.StatusInternalServerError},
	} {
		d := qualifyDaemon(t, &fakeQualifier{err: tc.err})
		w := call(d.SocketHandler(), http.MethodPost, "/v1/qualify", `{"task":"purdex/tsc"}`)
		assert.Equal(t, tc.code, w.Code, name)
		var body map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), name)
		assert.Equal(t, tc.err.Error(), body["error"], name)
	}
}

// The status lists the pairs in flight and the last ones, as a body a reader can check.
func TestGetQualifyListsThePairsAndTheLastOutcomes(t *testing.T) {
	q := &fakeQualifier{
		inFlight: []qualify.Started{{Label: "task:purdex/tsc", Candidate: "j_1", Reference: "j_2"}},
		recent:   []qualify.Record{{Label: "task:purdex/spa-related", Outcome: qualify.OutcomeMismatch, Why: "exit 1 on the candidate, 0 on the reference", At: time.Unix(100, 0).UTC(), Candidate: "j_3", Reference: "j_4"}},
	}
	d := qualifyDaemon(t, q)
	w := call(d.SocketHandler(), http.MethodGet, "/v1/qualify", ``)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	st, err := wire.Decode[wire.QualifyStatus](w.Body.Bytes())
	require.NoError(t, err)
	require.NoError(t, st.Validate())
	assert.Equal(t, "task:purdex/tsc", st.InFlight[0].Label)
	assert.Equal(t, "mismatch", st.Recent[0].Outcome)
	assert.Equal(t, http.StatusBadRequest, call(d.SocketHandler(), http.MethodGet, "/v1/qualify?host=air26", ``).Code)

	empty := call(qualifyDaemon(t, &fakeQualifier{}).SocketHandler(), http.MethodGet, "/v1/qualify", ``)
	assert.JSONEq(t, `{"in_flight":[],"recent":[]}`, empty.Body.String(), "empty lists, not null")
}

// Without a coordinator there are no routes.
func TestNoQualifierNoQualifyRoutes(t *testing.T) {
	d := New(Options{HostID: "h1", Alias: "mserv", Version: "v0", Token: testToken})
	assert.Equal(t, http.StatusNotFound, call(d.SocketHandler(), http.MethodPost, "/v1/qualify", `{"task":"purdex/tsc"}`).Code)
}
