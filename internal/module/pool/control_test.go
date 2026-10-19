package poolmod

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/wwd/client"
)

// seen is what the fake wwd was asked.
type seen struct {
	mu    sync.Mutex
	calls []string // "METHOD path host=<alias> body"
}

func (s *seen) add(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path+" host="+r.URL.Query().Get("host")+" "+string(b))
}

func (s *seen) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

const workerJSON = `{"enabled":true,"slots":{"heavy":1,"light":2},"owner_rule":""}`

// controlWWD is a wwd answering the three mutations with the given status codes and bodies.
func controlWWD(t *testing.T, s *seen, put, post, del int, putBody string) *client.Client {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/worker", func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		w.WriteHeader(put)
		_, _ = w.Write([]byte(putBody))
	})
	mux.HandleFunc("POST /v1/exceptions", func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		w.WriteHeader(post)
		_, _ = w.Write([]byte(`{"name":"pkg","until":"2026-10-19T00:00:00Z","holds":["heavy"]}`))
	})
	mux.HandleFunc("DELETE /v1/exceptions/{name}", func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		w.WriteHeader(del)
	})
	return fakeWWD(t, mux)
}

func send(mux *http.ServeMux, method, path, body string, ctx func(context.Context) context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if ctx != nil {
		req = req.WithContext(ctx(req.Context()))
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

var (
	asAdmin  = devices.WithAdmin
	asDevice = func(c context.Context) context.Context {
		return devices.WithPrincipal(c, devices.Principal{ID: "d_abc", PairingID: "p_1"})
	}
)

func TestControl_ForwardsToTheNamedHostThroughTheLocalWWD(t *testing.T) {
	var s seen
	mux := newModule(t, controlWWD(t, &s, 200, 200, 204, workerJSON))

	rr := send(mux, "PUT", "/api/pool/hosts/air26/worker", workerJSON, asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, workerJSON, rr.Body.String())

	rr = send(mux, "POST", "/api/pool/hosts/mserv/exceptions", `{"name":"pkg","until":"2026-10-19T00:00:00Z","holds":["heavy"]}`, asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"name":"pkg","until":"2026-10-19T00:00:00Z","holds":["heavy"]}`, rr.Body.String())

	rr = send(mux, "DELETE", "/api/pool/hosts/mserv/exceptions/pkg", "", asAdmin)
	require.Equal(t, 204, rr.Code, rr.Body.String())
	assert.Empty(t, rr.Body.String())

	got := s.all()
	require.Len(t, got, 3)
	assert.Equal(t, "PUT /v1/worker host=air26 "+workerJSON, got[0])
	assert.Equal(t, `POST /v1/exceptions host=mserv {"name":"pkg","until":"2026-10-19T00:00:00Z","holds":["heavy"]}`, got[1])
	assert.Equal(t, "DELETE /v1/exceptions/pkg host=mserv ", got[2])
}

// A device (and anyone who is not the admin) is refused before wwd is asked.
func TestControl_OnlyTheAdminMayChangeAHost(t *testing.T) {
	var s seen
	mux := newModule(t, controlWWD(t, &s, 200, 200, 204, workerJSON))
	for name, ctx := range map[string]func(context.Context) context.Context{
		"device":  asDevice,
		"nobody":  nil,
		"ticket":  func(c context.Context) context.Context { return devices.WithCaller(c, devices.Caller{}) },
		"dev+adm": func(c context.Context) context.Context { return asAdmin(asDevice(c)) }, // a principal wins over the mark
	} {
		for _, rt := range [][3]string{
			{"PUT", "/api/pool/hosts/air26/worker", workerJSON},
			{"POST", "/api/pool/hosts/air26/exceptions", `{"name":"pkg","until":"2026-10-19T00:00:00Z","holds":["heavy"]}`},
			{"DELETE", "/api/pool/hosts/air26/exceptions/pkg", ""},
		} {
			rr := send(mux, rt[0], rt[1], rt[2], ctx)
			assert.Equal(t, 403, rr.Code, "%s %s %s", name, rt[0], rt[1])
		}
	}
	assert.Empty(t, s.all(), "wwd is never asked for a refused caller")
}

func TestControl_RefusesWhatWWDShouldNeverSee(t *testing.T) {
	var s seen
	mux := newModule(t, controlWWD(t, &s, 200, 200, 204, workerJSON))
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/api/pool/hosts/air26/worker", `not json`},
		{"PUT", "/api/pool/hosts/air26/worker", `{"enabled":true,"surprise":1}`},
		{"PUT", "/api/pool/hosts/air26/worker", strings.Repeat("x", 70000)},
		{"POST", "/api/pool/hosts/air26/exceptions", `{}`},
		{"PUT", "/api/pool/hosts/a%00b/worker", workerJSON},                            // a control character in the alias
		{"PUT", "/api/pool/hosts/" + strings.Repeat("a", 129) + "/worker", workerJSON}, // over the shared limit, which wwd's config refuses too
		{"DELETE", "/api/pool/hosts/air26/exceptions/%0a", ""},
	} {
		rr := send(mux, c.method, c.path, c.body, asAdmin)
		assert.Equal(t, 400, rr.Code, "%s %s -> %s", c.method, c.path, rr.Body.String())
		assert.Equal(t, "bad_request", errCode(rr))
	}
	assert.Empty(t, s.all())
}

func TestExceptionNameAndAliasRules(t *testing.T) {
	for _, bad := range []string{"", "a\nb", "a\x00b", "\xff\xfe", strings.Repeat("x", 129)} {
		assert.False(t, exceptionName(bad), "%q", bad)
		assert.False(t, validAlias(bad), "%q", bad)
	}
	for _, ok := range []string{"pkg", "a/b c", "-x", "électron", "a..b", strings.Repeat("x", 128)} {
		assert.True(t, exceptionName(ok), "%q", ok)
		assert.True(t, validAlias(ok), "%q", ok)
	}
	assert.False(t, exceptionName("."))
	assert.False(t, exceptionName(".."))
	assert.False(t, validAlias("."), "a dot name is not one path segment (#3615)")
	assert.False(t, validAlias(".."), "a dot name is not one path segment (#3615)")
}

// wwd accepts any non-empty alias in its peer config, so any alias the pool lists can be changed; the host reaches wwd as
// one query value however odd it is, and an exception name as one path segment.
func TestControl_AnyAliasWWDAcceptsIsPassedAsOneValue(t *testing.T) {
	var s seen
	mux := newModule(t, controlWWD(t, &s, 200, 200, 204, workerJSON))
	rr := send(mux, "PUT", "/api/pool/hosts/-air%2026%26host=x%2Fy%C3%A9/worker", workerJSON, asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	rr = send(mux, "DELETE", "/api/pool/hosts/mserv/exceptions/a%2Fb%20c", "", asAdmin)
	require.Equal(t, 204, rr.Code, rr.Body.String())
	got := s.all()
	require.Len(t, got, 2)
	assert.Equal(t, "PUT /v1/worker host=-air 26&host=x/yé "+workerJSON, got[0], "one host value, nothing added")
	assert.Equal(t, "DELETE /v1/exceptions/a/b c host=mserv ", got[1])
}

func TestControl_WWDAnswersAreRelayedWithTheirMeaning(t *testing.T) {
	var s seen
	// wwd says 400 (the settings were refused), then 404 for a missing exception, then 502 for a host it cannot reach
	mux := newModule(t, controlWWD(t, &s, 400, 502, 404, `{"error":"slots cannot be negative"}`))

	rr := send(mux, "PUT", "/api/pool/hosts/air26/worker", workerJSON, asAdmin)
	assert.Equal(t, 400, rr.Code)
	assert.Equal(t, "wwd_rejected", errCode(rr))
	assert.Contains(t, rr.Body.String(), "slots cannot be negative")

	rr = send(mux, "DELETE", "/api/pool/hosts/air26/exceptions/pkg", "", asAdmin)
	assert.Equal(t, 404, rr.Code)
	assert.Equal(t, "wwd_rejected", errCode(rr))

	rr = send(mux, "POST", "/api/pool/hosts/air26/exceptions", `{"name":"pkg","until":"2026-10-19T00:00:00Z","holds":["heavy"]}`, asAdmin)
	assert.Equal(t, 502, rr.Code, "the peer could not be reached")
	assert.Equal(t, "wwd_error", errCode(rr))
}

func TestControl_WWDNotRunningIs503(t *testing.T) {
	mux := newModule(t, client.New(filepath.Join(t.TempDir(), "none.sock")))
	rr := send(mux, "PUT", "/api/pool/hosts/air26/worker", workerJSON, asAdmin)
	assert.Equal(t, 503, rr.Code)
	assert.Equal(t, ErrWWDUnavailable, errCode(rr))
}

// pool.control.v1 is announced for the real module registered in a real core, and goes with it.
func TestInfo_AnnouncesPoolControlV1ForTheRealModule(t *testing.T) {
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
	require.Contains(t, caps(), "pool.control.v1")
	require.NoError(t, m.Stop(context.Background()))
	require.NotContains(t, caps(), "pool.control.v1")
}
