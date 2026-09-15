// cmd/pdx/nex_test.go
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/internal/config"
)

// --- test helpers -----------------------------------------------------

// nexTestConfig returns a lookupConfig stub (matching config.Load's own
// signature, so production code can pass config.Load directly) that
// always returns cfg, ignoring the requested path.
func nexTestConfig(cfg config.Config) func(string) (config.Config, error) {
	return func(string) (config.Config, error) { return cfg, nil }
}

// nexUnreachableAddr is a base URL nothing listens on: connections to it
// fail (almost always immediately, "connection refused") rather than
// hang, so a test that must prove some other address won along
// precedence still runs fast if the implementation is wrong.
const nexUnreachableAddr = "http://127.0.0.1:1/api/nex"

// countingProbe wraps probeNexCapabilities (nex.go's real implementation)
// with a call counter, so tests exercise the real HTTP probe behavior
// against the httptest server standing in for the daemon while still
// asserting how many times runNex invoked it.
func countingProbe() (probe func(base, token string) (int, error), calls *int32) {
	var n int32
	return func(base, token string) (int, error) {
		atomic.AddInt32(&n, 1)
		return probeNexCapabilities(base, token)
	}, &n
}

// nexBaseFromServer builds the "http://<Bind>:<Port>/api/nex" base URL
// runNex would derive from a config pointed at srv, and the config that
// would produce it.
func nexBaseFromServer(t *testing.T, srv *httptest.Server, token string) (config.Config, string) {
	t.Helper()
	host, portStr, err := splitHostPort(srv.URL)
	if err != nil {
		t.Fatalf("split host/port %q: %v", srv.URL, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	cfg := config.Config{Bind: host, Port: port, Token: token}
	return cfg, "http://" + srv.Listener.Addr().String() + "/api/nex"
}

// lsOKHandler answers any request with a 200 and a minimal valid `nex ls`
// page body, recording the last request's path and Authorization header.
func lsOKHandler(gotPath, gotAuth *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.URL.Path
		*gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"items":[],"next_cursor":""}`))
	}
}

// --- config-only resolution --------------------------------------------

func TestRunNex_ConfigOnly(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(lsOKHandler(&gotPath, &gotAuth))
	defer srv.Close()

	cfg, _ := nexBaseFromServer(t, srv, "cfg-tok")
	probe, calls := countingProbe()

	var stdout, stderr bytes.Buffer
	code := runNex([]string{"ls"}, &stdout, &stderr, fakeGetenv(nil), nexTestConfig(cfg), probe)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.HasPrefix(gotPath, "/api/nex/v1/") {
		t.Errorf("request path = %q, want prefix /api/nex/v1/", gotPath)
	}
	if gotAuth != "Bearer cfg-tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer cfg-tok")
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Errorf("probe called %d times, want 0 (success path)", atomic.LoadInt32(calls))
	}
}

// --- precedence: addr flag > env > config -------------------------------

func TestRunNex_AddrFlagBeatsEnvAndConfig(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(lsOKHandler(&gotPath, &gotAuth))
	defer srv.Close()

	lookupCalled := false
	lookup := func(string) (config.Config, error) {
		lookupCalled = true
		return config.Config{}, nil
	}

	var stdout, stderr bytes.Buffer
	code := runNex(
		[]string{"--addr", srv.URL + "/api/nex", "--token", "tok", "ls"},
		&stdout, &stderr,
		fakeGetenv(map[string]string{"PDX_NEX_ADDR": nexUnreachableAddr}),
		lookup,
		func(string, string) (int, error) { return 0, nil },
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok")
	}
	if lookupCalled {
		t.Errorf("lookupConfig was called, want it skipped (addr and token both resolved without it)")
	}
}

func TestRunNex_AddrEnvBeatsConfig(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(lsOKHandler(&gotPath, &gotAuth))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runNex(
		[]string{"--token", "tok", "ls"},
		&stdout, &stderr,
		fakeGetenv(map[string]string{"PDX_NEX_ADDR": srv.URL + "/api/nex"}),
		func(string) (config.Config, error) {
			// Would be wrong if consulted for addr: an unreachable host.
			return config.Config{Bind: "127.0.0.1", Port: 1, Token: "cfg-tok"}, nil
		},
		func(string, string) (int, error) { return 0, nil },
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok")
	}
}

// --- precedence: token flag > env > config ------------------------------

func TestRunNex_TokenFlagBeatsEnvAndConfig(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(lsOKHandler(&gotPath, &gotAuth))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runNex(
		[]string{"--addr", srv.URL + "/api/nex", "--token", "flag-tok", "ls"},
		&stdout, &stderr,
		fakeGetenv(map[string]string{"PDX_NEX_TOKEN": "env-tok"}),
		func(string) (config.Config, error) { return config.Config{Token: "cfg-tok"}, nil },
		func(string, string) (int, error) { return 0, nil },
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotAuth != "Bearer flag-tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer flag-tok")
	}
}

func TestRunNex_TokenEnvBeatsConfig(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(lsOKHandler(&gotPath, &gotAuth))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runNex(
		[]string{"--addr", srv.URL + "/api/nex", "ls"},
		&stdout, &stderr,
		fakeGetenv(map[string]string{"PDX_NEX_TOKEN": "env-tok"}),
		func(string) (config.Config, error) { return config.Config{Token: "cfg-tok"}, nil },
		func(string, string) (int, error) { return 0, nil },
	)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotAuth != "Bearer env-tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer env-tok")
	}
}

// --- addr without token: the config token never leaves for a non-local addr

func TestRunNex_AddrWithoutToken_RefusesConfigToken(t *testing.T) {
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqCount, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"items":[],"next_cursor":""}`))
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
	}{
		{name: "addr flag", args: []string{"--addr", srv.URL + "/api/nex", "ls"}},
		{name: "addr env", args: []string{"ls"}, env: map[string]string{"PDX_NEX_ADDR": srv.URL + "/api/nex"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookupCalled := false
			lookup := func(string) (config.Config, error) {
				lookupCalled = true
				return config.Config{Bind: "127.0.0.1", Port: 1, Token: "cfg-tok"}, nil
			}
			probe, calls := countingProbe()

			var stdout, stderr bytes.Buffer
			code := runNex(tc.args, &stdout, &stderr, fakeGetenv(tc.env), lookup, probe)

			if code != 2 {
				t.Fatalf("code = %d, want 2; stderr=%q", code, stderr.String())
			}
			const want = "pdx nex: --addr given without --token / PDX_NEX_TOKEN (the local config token is not sent to a non-local address)"
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
			if lookupCalled {
				t.Errorf("lookupConfig was called; the config token must not be consulted for an explicit --addr")
			}
			if n := atomic.LoadInt32(&reqCount); n != 0 {
				t.Errorf("server saw %d request(s), want 0", n)
			}
			if atomic.LoadInt32(calls) != 0 {
				t.Errorf("probe called %d times, want 0", atomic.LoadInt32(calls))
			}
		})
	}
}

// --- --addr / PDX_NEX_ADDR normalization ---------------------------------

// TestRunNex_AddrTrailingSlashNormalized: a base given with a trailing
// slash (`http://h:p/api/nex/`) must not produce `/api/nex//v1/...`
// request paths — the path is cleaned and the trailing slash stripped
// before the client and the probe see it.
func TestRunNex_AddrTrailingSlashNormalized(t *testing.T) {
	for _, tc := range []struct {
		name string
		via  string // "flag" or "env"
	}{
		{name: "flag", via: "flag"},
		{name: "env", via: "env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotAuth string
			srv := httptest.NewServer(lsOKHandler(&gotPath, &gotAuth))
			defer srv.Close()

			args := []string{"--token", "tok", "ls"}
			env := map[string]string{}
			if tc.via == "flag" {
				args = append([]string{"--addr", srv.URL + "/api/nex/"}, args...)
			} else {
				env["PDX_NEX_ADDR"] = srv.URL + "/api/nex/"
			}

			var stdout, stderr bytes.Buffer
			code := runNex(args, &stdout, &stderr, fakeGetenv(env),
				func(string) (config.Config, error) { t.Fatal("lookupConfig must not run"); return config.Config{}, nil },
				func(string, string) (int, error) { return 0, nil })

			if code != 0 {
				t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
			}
			if !strings.HasPrefix(gotPath, "/api/nex/v1/") {
				t.Errorf("request path = %q, want prefix /api/nex/v1/ (no doubled slash)", gotPath)
			}
			if strings.Contains(gotPath, "//") {
				t.Errorf("request path = %q contains a doubled slash", gotPath)
			}
		})
	}
}

// TestRunNex_AddrNormalizedBaseReachesProbe: the not-enabled message (and
// the probe request itself) use the normalized base, not the raw --addr.
func TestRunNex_AddrNormalizedBaseReachesProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	var probedBase string
	probe := func(base, token string) (int, error) {
		probedBase = base
		return probeNexCapabilities(base, token)
	}

	var stdout, stderr bytes.Buffer
	code := runNex([]string{"--addr", srv.URL + "/api/nex/", "--token", "tok", "ls"},
		&stdout, &stderr, fakeGetenv(nil), nexTestConfig(config.Config{}), probe)

	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%q", code, stderr.String())
	}
	wantBase := srv.URL + "/api/nex"
	if probedBase != wantBase {
		t.Errorf("probe base = %q, want normalized %q", probedBase, wantBase)
	}
	want := "nex: not enabled on this host (GET " + wantBase + "/v1/capabilities → 404; set [nex] enabled = true, or check --addr)"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

// TestRunNex_AddrRootPathBecomesEmpty: `http://h:p/` (bare root) is the
// same as `http://h:p` — the client then requests `/v1/...`.
func TestRunNex_AddrRootPathBecomesEmpty(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(lsOKHandler(&gotPath, &gotAuth))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runNex([]string{"--addr", srv.URL + "/", "--token", "tok", "ls"},
		&stdout, &stderr, fakeGetenv(nil), nexTestConfig(config.Config{}),
		func(string, string) (int, error) { return 0, nil })

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.HasPrefix(gotPath, "/v1/") {
		t.Errorf("request path = %q, want prefix /v1/", gotPath)
	}
}

// TestRunNex_AddrMalformedExit2: a base URL that is not
// http(s)://host[:port][/path] — no scheme, a query, a fragment, an
// unsupported scheme, an empty host — is refused with exit 2 before any
// config load or request.
func TestRunNex_AddrMalformedExit2(t *testing.T) {
	for _, tc := range []struct {
		name string
		addr string
	}{
		{name: "no scheme", addr: "h:1/api/nex"},
		{name: "query", addr: "http://h:1/api/nex?x=y"},
		{name: "fragment", addr: "http://h:1/api/nex#f"},
		{name: "unsupported scheme", addr: "ftp://h:1/api/nex"},
		{name: "empty host", addr: "http:///api/nex"},
		{name: "bare host no scheme", addr: "localhost:7860"},
	} {
		for _, via := range []string{"flag", "env"} {
			t.Run(tc.name+"/"+via, func(t *testing.T) {
				args := []string{"--token", "tok", "ls"}
				env := map[string]string{}
				if via == "flag" {
					args = append([]string{"--addr", tc.addr}, args...)
				} else {
					env["PDX_NEX_ADDR"] = tc.addr
				}
				var stdout, stderr bytes.Buffer
				code := runNex(args, &stdout, &stderr, fakeGetenv(env),
					func(string) (config.Config, error) { t.Fatal("lookupConfig must not run"); return config.Config{}, nil },
					func(string, string) (int, error) { t.Fatal("probe must not run"); return 0, nil })
				if code != 2 {
					t.Fatalf("code = %d, want 2; stderr=%q", code, stderr.String())
				}
				want := "pdx nex: --addr must be http(s)://host[:port][/path] without query or fragment (got " + strconv.Quote(tc.addr) + ")"
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr = %q, want %q", stderr.String(), want)
				}
			})
		}
	}
}

// --- config base URL: wildcard binds resolve to a connectable loopback --

func TestNexBaseURLFromConfig(t *testing.T) {
	for _, tc := range []struct {
		bind string
		want string
	}{
		{"127.0.0.1", "http://127.0.0.1:7860/api/nex"},
		{"100.64.0.2", "http://100.64.0.2:7860/api/nex"},
		{"", "http://127.0.0.1:7860/api/nex"},
		{"0.0.0.0", "http://127.0.0.1:7860/api/nex"},
		{"::", "http://[::1]:7860/api/nex"},
		{"[::]", "http://[::1]:7860/api/nex"},
		{"::1", "http://[::1]:7860/api/nex"},
		{"fd00::2", "http://[fd00::2]:7860/api/nex"},
	} {
		if got := nexBaseURL(config.Config{Bind: tc.bind, Port: 7860}); got != tc.want {
			t.Errorf("nexBaseURL(Bind=%q) = %q, want %q", tc.bind, got, tc.want)
		}
	}
}

func TestRunNex_ConfigWildcardBindReachesLoopback(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(lsOKHandler(&gotPath, &gotAuth))
	defer srv.Close()

	cfg, _ := nexBaseFromServer(t, srv, "cfg-tok")
	cfg.Bind = "0.0.0.0" // httptest listens on 127.0.0.1; 0.0.0.0 is not dialable
	probe, calls := countingProbe()

	var stdout, stderr bytes.Buffer
	code := runNex([]string{"ls"}, &stdout, &stderr, fakeGetenv(nil), nexTestConfig(cfg), probe)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotAuth != "Bearer cfg-tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer cfg-tok")
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Errorf("probe called %d times, want 0 (success path)", atomic.LoadInt32(calls))
	}
}

// --- usage text steers to PDX_NEX_TOKEN over --token ----------------------

func TestNexUsageRecommendsEnvToken(t *testing.T) {
	if !strings.Contains(nexUsage, "PDX_NEX_TOKEN") {
		t.Errorf("nexUsage does not mention PDX_NEX_TOKEN:\n%s", nexUsage)
	}
	var stdout, stderr bytes.Buffer
	code := runNex([]string{"--bogus"}, &stdout, &stderr, fakeGetenv(nil),
		func(string) (config.Config, error) {
			t.Fatal("lookupConfig must not run on a usage error")
			return config.Config{}, nil
		},
		func(string, string) (int, error) { t.Fatal("probe must not run on a usage error"); return 0, nil })
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "PDX_NEX_TOKEN") {
		t.Errorf("usage on stderr does not recommend PDX_NEX_TOKEN:\n%s", stderr.String())
	}
}

// --- args pass-through: delegate --cwd/--brief reach the server verbatim

func TestRunNex_DelegateArgsPassThrough(t *testing.T) {
	type delegateBody struct {
		Brief  string `json:"brief"`
		Mounts []struct {
			Path string `json:"path"`
		} `json:"mounts"`
	}
	var got delegateBody
	var gotPath, gotMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "exec-1", "state": "queued"})
	}))
	defer srv.Close()

	cfg, _ := nexBaseFromServer(t, srv, "tok")
	probe, calls := countingProbe()

	var stdout, stderr bytes.Buffer
	code := runNex([]string{"delegate", "--cwd", "/x", "--brief", "b"}, &stdout, &stderr, fakeGetenv(nil), nexTestConfig(cfg), probe)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/nex/v1/executions" {
		t.Errorf("request = %s %s, want POST /api/nex/v1/executions", gotMethod, gotPath)
	}
	if got.Brief != "b" {
		t.Errorf("brief = %q, want %q", got.Brief, "b")
	}
	if len(got.Mounts) != 1 || got.Mounts[0].Path != "/x" {
		t.Errorf("mounts = %+v, want [{Path: /x}]", got.Mounts)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Errorf("probe called %d times, want 0 (success path)", atomic.LoadInt32(calls))
	}
}

// --- error path: daemon 404s everything -> not-enabled message ----------

func TestRunNex_ServerAllNotFound_PrintsNotEnabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`not found`))
	}))
	defer srv.Close()

	cfg, _ := nexBaseFromServer(t, srv, "tok")
	probe, calls := countingProbe()

	var stdout, stderr bytes.Buffer
	code := runNex([]string{"ls"}, &stdout, &stderr, fakeGetenv(nil), nexTestConfig(cfg), probe)

	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%q", code, stderr.String())
	}
	_, base := nexBaseFromServer(t, srv, "tok")
	want := "nex: not enabled on this host (GET " + base + "/v1/capabilities → 404; set [nex] enabled = true, or check --addr)"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want the not-enabled message %q", stderr.String(), want)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Errorf("probe called %d times, want 1", atomic.LoadInt32(calls))
	}
}

// --- error path: daemon enabled, but the resource itself 404s -----------

func TestRunNex_CapabilitiesOK_ShowNotFound_PrintsOriginalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/nex/v1/capabilities" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"execution_not_found"}`))
	}))
	defer srv.Close()

	cfg, _ := nexBaseFromServer(t, srv, "tok")
	probe, calls := countingProbe()

	var stdout, stderr bytes.Buffer
	code := runNex([]string{"show", "x"}, &stdout, &stderr, fakeGetenv(nil), nexTestConfig(cfg), probe)

	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "execution_not_found") {
		t.Errorf("stderr = %q, want it to contain execution_not_found", stderr.String())
	}
	if strings.Contains(stderr.String(), "not enabled on this host") {
		t.Errorf("stderr = %q, must not contain the not-enabled message", stderr.String())
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Errorf("probe called %d times, want 1", atomic.LoadInt32(calls))
	}
}

// --- no subcommand: client's own usage error, probe never consulted -----

func TestRunNex_NoSubcommand_ClientUsageError(t *testing.T) {
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqCount, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg, _ := nexBaseFromServer(t, srv, "tok")
	probe, calls := countingProbe()

	var stdout, stderr bytes.Buffer
	code := runNex(nil, &stdout, &stderr, fakeGetenv(nil), nexTestConfig(cfg), probe)

	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%q", code, stderr.String())
	}
	if stderr.String() == "" {
		t.Errorf("stderr is empty, want client.Run's usage error")
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Errorf("probe called %d times, want 0 (no subcommand given)", atomic.LoadInt32(calls))
	}
	if atomic.LoadInt32(&reqCount) != 0 {
		t.Errorf("server saw %d request(s), want 0", atomic.LoadInt32(&reqCount))
	}
}
