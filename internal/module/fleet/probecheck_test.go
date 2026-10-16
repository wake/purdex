package fleetmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	leaf "github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/middleware"
)

type checkRig struct {
	m   *Measure
	mux *http.ServeMux
}

func newCheckRig(t *testing.T, run func(context.Context, string) leaf.Result) *checkRig {
	t.Helper()
	m := NewMeasure(Deps{Home: t.TempDir()})
	m.probeValid = func(string, string) bool { return true }
	if run != nil {
		m.probeRun = run
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	return &checkRig{m: m, mux: mux}
}

func (r *checkRig) post(body string, p *middleware.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/probe-check", strings.NewReader(body))
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, req)
	return rr
}

type checkBody struct {
	SSH     string         `json:"ssh"`
	Measure string         `json:"measure"`
	Error   *string        `json:"error"`
	Reading map[string]any `json:"reading"`
}

func decodeCheck(t *testing.T, rr *httptest.ResponseRecorder) checkBody {
	t.Helper()
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var b checkBody
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &b))
	return b
}

var admin = &middleware.Principal{Kind: middleware.PrincipalAdmin}

func TestProbeCheck_EveryCodeMapsToItsPair(t *testing.T) {
	cases := []struct {
		res          leaf.Result
		ssh, measure string
		code         string
		wantReading  bool
	}{
		{leaf.Result{Status: leaf.StatusOK, Reading: leaf.Reading{CPU: 12.5, Mem: 40, Disk: 61}}, "ok", "ok", "", true},
		{leaf.Result{Status: leaf.StatusError, Code: leaf.CodeAuth}, "failed", "unknown", "auth", false},
		{leaf.Result{Status: leaf.StatusError, Code: leaf.CodeHostKey}, "failed", "unknown", "host_key", false},
		{leaf.Result{Status: leaf.StatusError, Code: leaf.CodeAliasGone}, "failed", "unknown", "alias_gone", false},
		{leaf.Result{Status: leaf.StatusOffline, Code: leaf.CodeTimeout}, "unknown", "unknown", "timeout", false},
		{leaf.Result{Status: leaf.StatusOffline, Code: leaf.CodeUnreachable}, "unknown", "unknown", "unreachable", false},
		{leaf.Result{Status: leaf.StatusError, Code: leaf.CodeRemoteExit}, "ok", "failed", "remote_exit", false},
		{leaf.Result{Status: leaf.StatusError, Code: leaf.CodeParse}, "ok", "failed", "parse", false},
	}
	for _, c := range cases {
		t.Run(string(c.res.Code)+"/"+string(c.res.Status), func(t *testing.T) {
			res := c.res
			res.Stderr = "SECRET-STDERR Permission denied (publickey)"
			r := newCheckRig(t, func(context.Context, string) leaf.Result { return res })
			rr := r.post(`{"ssh_alias":"box"}`, admin)
			b := decodeCheck(t, rr)
			require.Equal(t, c.ssh, b.SSH)
			require.Equal(t, c.measure, b.Measure)
			if c.code == "" {
				require.Nil(t, b.Error)
			} else {
				require.NotNil(t, b.Error)
				require.Equal(t, c.code, *b.Error)
			}
			require.Equal(t, c.wantReading, b.Reading != nil)
			require.NotContains(t, rr.Body.String(), "SECRET-STDERR")
			require.NotContains(t, strings.ToLower(rr.Body.String()), "stderr")
		})
	}
}

func TestProbeCheck_ReadingOnOK(t *testing.T) {
	r := newCheckRig(t, func(context.Context, string) leaf.Result {
		return leaf.Result{Status: leaf.StatusOK, Reading: leaf.Reading{CPU: 12.5, Mem: 40.25, Disk: 61}}
	})
	b := decodeCheck(t, r.post(`{"ssh_alias":"box"}`, admin))
	require.Equal(t, 12.5, b.Reading["cpu"])
	require.Equal(t, 40.25, b.Reading["mem"])
	require.Equal(t, 61.0, b.Reading["disk"])
	require.Contains(t, b.Reading, "level")
}

func TestProbeCheck_AliasNotInConfigIsAliasGoneWithoutRunning(t *testing.T) {
	var ran atomic.Int32
	r := newCheckRig(t, func(context.Context, string) leaf.Result { ran.Add(1); return leaf.Result{Status: leaf.StatusOK} })
	r.m.probeValid = func(string, string) bool { return false }
	b := decodeCheck(t, r.post(`{"ssh_alias":"nope"}`, admin))
	require.Equal(t, "failed", b.SSH)
	require.Equal(t, "alias_gone", *b.Error)
	require.Zero(t, ran.Load())
}

func TestProbeCheck_NonAdminPrincipalIs403(t *testing.T) {
	var ran atomic.Int32
	r := newCheckRig(t, func(context.Context, string) leaf.Result { ran.Add(1); return leaf.Result{} })
	host := &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab"}
	require.Equal(t, http.StatusForbidden, r.post(`{"ssh_alias":"box"}`, host).Code)
	require.Equal(t, http.StatusForbidden, r.post(`{"ssh_alias":"box"}`, &middleware.Principal{Kind: "device"}).Code)
	require.Zero(t, ran.Load())
}

func TestProbeCheck_BadBodyIs400(t *testing.T) {
	r := newCheckRig(t, nil)
	for _, body := range []string{``, `not json`, `{}`, `{"ssh_alias":""}`, `{"ssh_alias":5}`, `{"ssh_alias":"a","x":1}`,
		`{"ssh_alias":"` + strings.Repeat("a", 5000) + `"}`} {
		require.Equal(t, http.StatusBadRequest, r.post(body, admin).Code, body)
	}
}

func TestProbeCheck_SingleFlightPerAlias(t *testing.T) {
	var ran atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	r := newCheckRig(t, func(context.Context, string) leaf.Result {
		if ran.Add(1) == 1 {
			close(started)
		}
		<-release
		return leaf.Result{Status: leaf.StatusOK, Reading: leaf.Reading{CPU: 7}}
	})
	var wg sync.WaitGroup
	out := make([]*httptest.ResponseRecorder, 2)
	wg.Add(1)
	go func() { defer wg.Done(); out[0] = r.post(`{"ssh_alias":"box"}`, admin) }()
	<-started
	wg.Add(1)
	go func() { defer wg.Done(); out[1] = r.post(`{"ssh_alias":"box"}`, admin) }()
	require.Eventually(t, func() bool { return r.m.probeWaiters("box") == 1 }, 2*time.Second, time.Millisecond)
	close(release)
	wg.Wait()
	require.EqualValues(t, 1, ran.Load())
	require.Equal(t, out[0].Body.String(), out[1].Body.String())
	// a later request runs again
	r2 := r.post(`{"ssh_alias":"box"}`, admin)
	require.Equal(t, http.StatusOK, r2.Code)
	require.EqualValues(t, 2, ran.Load())
}

func TestProbeCheck_DifferentAliasesDoNotShare(t *testing.T) {
	var ran atomic.Int32
	r := newCheckRig(t, func(context.Context, string) leaf.Result { ran.Add(1); return leaf.Result{Status: leaf.StatusOK} })
	r.post(`{"ssh_alias":"a"}`, admin)
	r.post(`{"ssh_alias":"b"}`, admin)
	require.EqualValues(t, 2, ran.Load())
}

func TestProbeCheck_HangingRunnerEndsAsTimeout(t *testing.T) {
	r := newCheckRig(t, func(ctx context.Context, _ string) leaf.Result {
		<-ctx.Done()
		return leaf.Result{Status: leaf.StatusOffline, Code: leaf.CodeUnreachable} // what Runner says for a cancelled ctx
	})
	r.m.probeTimeout = 50 * time.Millisecond
	b := decodeCheck(t, r.post(`{"ssh_alias":"box"}`, admin))
	require.Equal(t, "unknown", b.SSH)
	require.Equal(t, "unknown", b.Measure)
	require.Equal(t, "timeout", *b.Error)
}

func defaultRunnerCheck(t *testing.T, look func(string) (string, error)) checkBody {
	t.Helper()
	m := NewMeasure(Deps{Home: t.TempDir(), Session: sessionCfg{
		LookPath: look,
		Valid:    func(string, string) bool { return true },
	}})
	mux := http.NewServeMux()
	m.Routes(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/fleet/probe-check", strings.NewReader(`{"ssh_alias":"box"}`)))
	return decodeCheck(t, rr)
}

// The route reaches ssh only through leaf.Runner (-> execstat "fleet.ssh-run"): the default runner honours the injected LookPath
// and really executes the binary it names.
func TestProbeCheck_DefaultRunnerGoesThroughRunner(t *testing.T) {
	b := defaultRunnerCheck(t, func(string) (string, error) { return "", context.DeadlineExceeded })
	require.Equal(t, "unreachable", *b.Error) // Runner's own answer for no ssh binary

	b = defaultRunnerCheck(t, func(string) (string, error) { return "/usr/bin/false", nil }) // exits 1: the remote command failed
	require.Equal(t, "remote_exit", *b.Error)
	require.Equal(t, "ok", b.SSH)
	require.Equal(t, "failed", b.Measure)
}
