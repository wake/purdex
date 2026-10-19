package jobsapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

type stubReuser struct {
	res   wire.Result
	hit   bool
	asked int
}

func (s *stubReuser) Lookup(context.Context, wire.JobSubmission) (wire.Result, bool) {
	s.asked++
	return s.res, s.hit
}

func reuseRig(t *testing.T, hit bool) (http.Handler, *stubReuser, *rig) {
	t.Helper()
	r := newRig(t)
	zero := 0
	st := &stubReuser{res: wire.Result{Exit: &zero, Host: "mserv", Log: "j_old/log"}, hit: hit}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/jobs", New(r.q).WithReuse(st).Submit())
	return mux, st, r
}

func cacheable(s *wire.JobSubmission) { s.Cacheable, s.DefHash = true, "def1" }

// A cacheable submission a stored result answers comes back with {id, result} at once, as a succeeded job with cache_hit.
func TestSubmitAnsweredFromAStoredResult(t *testing.T) {
	h, st, r := reuseRig(t, true)
	w := call(h, "POST", "/v1/jobs", submission(cacheable))
	require.Equal(t, 200, w.Code, w.Body.String())
	resp := decode[wire.JobSubmissionResponse](t, w)
	require.NotNil(t, resp.Result)
	assert.True(t, resp.Result.CacheHit)
	assert.Equal(t, 1, st.asked)
	j, err := r.q.Get(context.Background(), resp.ID)
	require.NoError(t, err)
	assert.Equal(t, wire.StateSucceeded, j.State)
}

// Everything that must not be answered from a stored result is queued without even asking.
func TestSubmitNotAskedForWhatMustRun(t *testing.T) {
	for name, edit := range map[string]func(*wire.JobSubmission){
		"not cacheable":    func(s *wire.JobSubmission) { cacheable(s); s.Cacheable = false },
		"--no-cache":       func(s *wire.JobSubmission) { cacheable(s); s.NoCache = true },
		"undeclared":       func(s *wire.JobSubmission) { cacheable(s); s.AdHoc = true },
		"no definition":    func(s *wire.JobSubmission) { cacheable(s); s.DefHash = "" },
		"a miss is queued": func(s *wire.JobSubmission) { cacheable(s) },
	} {
		h, st, r := reuseRig(t, name != "a miss is queued") // the reuser would say yes to all but the last
		w := call(h, "POST", "/v1/jobs", submission(edit))
		require.Equal(t, 200, w.Code, name)
		resp := decode[wire.JobSubmissionResponse](t, w)
		assert.Nil(t, resp.Result, name)
		j, err := r.q.Get(context.Background(), resp.ID)
		require.NoError(t, err)
		assert.Equal(t, wire.StateQueued, j.State, name)
		if name != "a miss is queued" {
			assert.Zero(t, st.asked, name)
		}
	}
}
