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
	"github.com/wake/purdex/internal/devices"
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
	} else {
		req = req.WithContext(devices.WithAdmin(req.Context())) // what TokenAuthWith does for the admin token
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

var admin *middleware.Principal // nil: the admin token carries the devices admin mark, no middleware principal

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
	require.Nil(t, b.Reading["level"], "level is JSON null on the one-shot probe")
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

// Through the real TokenAuthWith: the admin token reaches the route, a device (principal in the context), a host principal and
// a request that got in without the admin mark (a ticket) do not; no credential is a 401.
func TestProbeCheck_AdminOnlyThroughRealMiddleware(t *testing.T) {
	var ran atomic.Int32
	r := newCheckRig(t, func(context.Context, string) leaf.Result { ran.Add(1); return leaf.Result{Status: leaf.StatusOK} })
	h := middleware.TokenAuthWith(func() string { return "adm" }, nil, nil)(r.mux)
	do := func(token string, mutate func(*http.Request) *http.Request) int {
		req := httptest.NewRequest(http.MethodPost, "/api/fleet/probe-check", strings.NewReader(`{"ssh_alias":"box"}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if mutate != nil {
			req = mutate(req)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	require.Equal(t, http.StatusOK, do("adm", nil))
	require.EqualValues(t, 1, ran.Load())
	require.Equal(t, http.StatusUnauthorized, do("", nil))
	require.Equal(t, http.StatusUnauthorized, do("wrong", nil))
	// the handler itself, behind a chain that let something else in
	bare := func(ctx func(context.Context) context.Context) int {
		req := httptest.NewRequest(http.MethodPost, "/api/fleet/probe-check", strings.NewReader(`{"ssh_alias":"box"}`))
		rr := httptest.NewRecorder()
		r.mux.ServeHTTP(rr, req.WithContext(ctx(req.Context())))
		return rr.Code
	}
	require.Equal(t, http.StatusForbidden, bare(func(c context.Context) context.Context {
		return devices.WithPrincipal(c, devices.Principal{ID: "d_aaaaaaaaaaaa"})
	}))
	require.Equal(t, http.StatusForbidden, bare(func(c context.Context) context.Context {
		return middleware.WithPrincipal(c, middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab"})
	}))
	require.Equal(t, http.StatusForbidden, bare(func(c context.Context) context.Context { return c }), "no admin mark")
	require.EqualValues(t, 1, ran.Load())
}

func TestProbeCheck_BadBodyIs400(t *testing.T) {
	r := newCheckRig(t, nil)
	for _, body := range []string{``, `not json`, `{}`, `{"ssh_alias":""}`, `{"ssh_alias":5}`, `{"ssh_alias":"a","x":1}`, `{"ssh_alias":"a"}x`,
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

func TestProbeCheck_PanickingRunnerDoesNotWedgeTheAlias(t *testing.T) {
	var ran atomic.Int32
	r := newCheckRig(t, func(context.Context, string) leaf.Result {
		if ran.Add(1) == 1 {
			panic("boom")
		}
		return leaf.Result{Status: leaf.StatusOK}
	})
	func() {
		defer func() { _ = recover() }()
		r.post(`{"ssh_alias":"box"}`, admin)
	}()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- r.post(`{"ssh_alias":"box"}`, admin) }()
	select {
	case rr := <-done:
		require.Equal(t, "ok", decodeCheck(t, rr).SSH)
	case <-time.After(3 * time.Second):
		t.Fatal("the next request joined a dead call")
	}
	require.EqualValues(t, 2, ran.Load())
}

func TestProbeCheck_JoinerPanicOfLeaderGetsAnAnswer(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	r := newCheckRig(t, func(context.Context, string) leaf.Result {
		close(started)
		<-release
		panic("boom")
	})
	go func() {
		defer func() { _ = recover() }()
		r.post(`{"ssh_alias":"box"}`, admin)
	}()
	<-started
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- r.post(`{"ssh_alias":"box"}`, admin) }()
	require.Eventually(t, func() bool { return r.m.probeWaiters("box") == 1 }, 2*time.Second, time.Millisecond)
	close(release)
	select {
	case rr := <-done:
		b := decodeCheck(t, rr)
		require.Equal(t, "unknown", b.SSH)
		require.Equal(t, "unreachable", *b.Error)
	case <-time.After(3 * time.Second):
		t.Fatal("the joiner hung behind a panicked leader")
	}
}

func TestProbeCheck_CancelledJoinerLeavesTheLeaderAlone(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	r := newCheckRig(t, func(context.Context, string) leaf.Result {
		close(started)
		<-release
		return leaf.Result{Status: leaf.StatusOK}
	})
	leader := make(chan *httptest.ResponseRecorder, 1)
	go func() { leader <- r.post(`{"ssh_alias":"box"}`, admin) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/fleet/probe-check", strings.NewReader(`{"ssh_alias":"box"}`)).WithContext(devices.WithAdmin(ctx))
	joined := make(chan int, 1)
	go func() { rr := httptest.NewRecorder(); r.mux.ServeHTTP(rr, req); joined <- rr.Code }()
	require.Eventually(t, func() bool { return r.m.probeWaiters("box") == 1 }, 2*time.Second, time.Millisecond)
	cancel()
	select {
	case code := <-joined:
		require.Equal(t, http.StatusRequestTimeout, code)
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled joiner did not return")
	}
	close(release)
	require.Equal(t, "ok", decodeCheck(t, <-leader).SSH)
}

func TestProbeCheck_JoinerHasItsOwnBound(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	r := newCheckRig(t, func(context.Context, string) leaf.Result {
		close(started)
		<-release
		return leaf.Result{Status: leaf.StatusOK}
	})
	defer close(release)
	r.m.probeTimeout = 20 * time.Millisecond
	r.m.probeJoinSlack = 20 * time.Millisecond
	go r.post(`{"ssh_alias":"box"}`, admin)
	<-started
	b := decodeCheck(t, r.post(`{"ssh_alias":"box"}`, admin))
	require.Equal(t, "timeout", *b.Error)
}

func TestProbeCheck_StopEndsAHangingRun(t *testing.T) {
	started, ended := make(chan struct{}), make(chan struct{})
	r := newCheckRig(t, func(ctx context.Context, _ string) leaf.Result {
		close(started)
		<-ctx.Done()
		close(ended)
		return leaf.Result{Status: leaf.StatusOffline, Code: leaf.CodeUnreachable}
	})
	go r.post(`{"ssh_alias":"box"}`, admin)
	<-started
	r.m.Stop()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop left the run going")
	}
}

func TestProbeCheck_DeadlineOnlyRewritesUnreachable(t *testing.T) {
	for _, c := range []struct{ code, want leaf.Code }{
		{leaf.CodeUnreachable, leaf.CodeTimeout}, {leaf.CodeAuth, leaf.CodeAuth}, {leaf.CodeRemoteExit, leaf.CodeRemoteExit},
	} {
		r := newCheckRig(t, func(ctx context.Context, _ string) leaf.Result {
			<-ctx.Done()
			return leaf.Result{Status: leaf.StatusError, Code: c.code}
		})
		r.m.probeTimeout = 20 * time.Millisecond
		b := decodeCheck(t, r.post(`{"ssh_alias":"box"}`, admin))
		require.Equal(t, string(c.want), *b.Error, c.code)
	}
}

func TestProbeCheck_EmptyCodeOnNonOKIsUnreachable(t *testing.T) {
	r := newCheckRig(t, func(context.Context, string) leaf.Result { return leaf.Result{Status: leaf.StatusError} })
	rr := r.post(`{"ssh_alias":"box"}`, admin)
	b := decodeCheck(t, rr)
	require.Equal(t, "unreachable", *b.Error)
	require.Equal(t, "unknown", b.SSH)
	require.Equal(t, "unknown", b.Measure)
}

func TestProbeCheck_DifferentAliasesRunConcurrently(t *testing.T) {
	var started atomic.Int32
	both := make(chan struct{})
	r := newCheckRig(t, func(context.Context, string) leaf.Result {
		if started.Add(1) == 2 {
			close(both)
		}
		select {
		case <-both:
		case <-time.After(2 * time.Second):
		}
		return leaf.Result{Status: leaf.StatusOK}
	})
	var wg sync.WaitGroup
	for _, a := range []string{"a", "b"} {
		wg.Add(1)
		go func() { defer wg.Done(); r.post(`{"ssh_alias":"`+a+`"}`, admin) }()
	}
	wg.Wait()
	select {
	case <-both:
	default:
		t.Fatal("the second alias waited for the first (a global lock)")
	}
}
