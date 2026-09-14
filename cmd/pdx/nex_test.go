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
	if !strings.Contains(stderr.String(), "nex: not enabled on this host (set [nex] enabled = true)") {
		t.Errorf("stderr = %q, want the not-enabled message", stderr.String())
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
