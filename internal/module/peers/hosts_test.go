// internal/module/peers/hosts_test.go
package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
)

var inboundTokenPattern = regexp.MustCompile(`^pdxp_[0-9a-f]{32}$`)

// newHostsTestCore builds a *core.Core with CfgPath pointing at a fresh
// temp file, so hosts.go's UpdateConfig calls actually persist and tests
// can assert on disk state via config.Load.
func newHostsTestCore(t *testing.T, hostID, alias, adminToken string, hosts []config.PeerHost) (*core.Core, string) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := &config.Config{
		HostID: hostID,
		Token:  adminToken,
		Peers:  config.PeersConfig{Alias: alias, Hosts: append([]config.PeerHost(nil), hosts...)},
	}
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	c.CfgPath = cfgPath

	// Seed the file with the same initial state so a test asserting
	// "nothing persisted" after a failed mutation reads back the true
	// pre-existing state rather than a config.Load default (empty file).
	if err := config.WriteFile(cfgPath, *cfg); err != nil {
		t.Fatalf("seed cfgPath: %v", err)
	}

	return c, cfgPath
}

// newHostsTestModule builds a *Module sufficient to exercise the hosts
// routes: sessions/owners are empty fakes (unused by hosts.go), and fetch
// defaults to production fetchRemote/newRemoteClient unless overridden.
func newHostsTestModule(c *core.Core, fetch fetchFunc) *Module {
	m := newTestModule(c, &fakeSessions{}, &fakeOwners{}, "", ipeers.DefaultLiveness(), &fakeClock{times: []time.Time{time.Now()}}, 2*time.Second)
	if fetch != nil {
		m.fetch = fetch
	}
	return m
}

func adminPrincipal() *middleware.Principal {
	p := middleware.Principal{Kind: middleware.PrincipalAdmin}
	return &p
}

func hostPrincipal(alias string) *middleware.Principal {
	p := middleware.Principal{Kind: middleware.PrincipalHost, Alias: alias}
	return &p
}

// doHostsRequest builds a fresh mux from m's routes and serves one request,
// optionally injecting a principal into the request context the way
// PeerAuth would have.
func doHostsRequest(t *testing.T, m *Module, method, target string, body any, principal *middleware.Principal) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}

	req := httptest.NewRequest(method, target, reader)
	ctx := context.Background()
	if principal != nil {
		ctx = middleware.WithPrincipal(ctx, *principal)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req.WithContext(ctx))
	return rr
}

func loadCfg(t *testing.T, path string) config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load(%s): %v", path, err)
	}
	return cfg
}

// assertNoTokenKeys walks a decoded JSON body and fails if any object key
// is literally "token", or "inbound_token" when not allowed.
func assertNoTokenKeys(t *testing.T, body []byte, allowInboundToken bool) {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	var walk func(node any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			for k, val := range n {
				if k == "token" {
					t.Fatalf("response contains forbidden key %q: %s", k, body)
				}
				if k == "inbound_token" && !allowInboundToken {
					t.Fatalf("response contains forbidden key %q: %s", k, body)
				}
				walk(val)
			}
		case []any:
			for _, e := range n {
				walk(e)
			}
		}
	}
	walk(v)
}

// fixedEnvelopeFetch returns a fetchFunc that always returns env, err
// regardless of arguments.
func fixedEnvelopeFetch(env ipeers.Envelope, err error) fetchFunc {
	return func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		return env, err
	}
}

// failIfCalledFetch fails the test if the fetch seam is ever invoked, for
// asserting a validation error short-circuits before any network call.
func failIfCalledFetch(t *testing.T) fetchFunc {
	return func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		t.Helper()
		t.Fatal("fetch should not have been called")
		return ipeers.Envelope{}, nil
	}
}

// ---- GET /api/peers/hosts ----

func TestHandleListHosts_NonAdminForbidden(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/hosts", nil, hostPrincipal("peer-a"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleListHosts_NoPrincipalForbidden(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/hosts", nil, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleListHosts_Empty(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/hosts", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Hosts []map[string]any `json:"hosts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Hosts == nil || len(got.Hosts) != 0 {
		t.Fatalf("hosts = %+v, want empty non-nil slice", got.Hosts)
	}
}

func TestHandleListHosts_RowShapeAndNeverLeaksTokens(t *testing.T) {
	hosts := []config.PeerHost{
		{Alias: "peer-a", URL: "https://a.example", HostID: "a:111", Token: "outbound-a", InboundToken: "inbound-a", AllowBypass: true},
		{Alias: "peer-b", URL: "https://b.example", HostID: "", Token: "", InboundToken: "inbound-b"},
	}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/hosts", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "outbound-a") || strings.Contains(rr.Body.String(), "inbound-a") || strings.Contains(rr.Body.String(), "inbound-b") {
		t.Fatalf("body leaks a secret token value: %s", rr.Body.String())
	}
	assertNoTokenKeys(t, rr.Body.Bytes(), false)

	var got struct {
		Hosts []struct {
			Alias           string `json:"alias"`
			URL             string `json:"url"`
			HostID          string `json:"host_id"`
			Verified        bool   `json:"verified"`
			HasToken        bool   `json:"has_token"`
			HasInboundToken bool   `json:"has_inbound_token"`
			AllowBypass     bool   `json:"allow_bypass"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2", got.Hosts)
	}
	a := got.Hosts[0]
	if a.Alias != "peer-a" || a.URL != "https://a.example" || a.HostID != "a:111" || !a.Verified || !a.HasToken || !a.HasInboundToken || !a.AllowBypass {
		t.Errorf("peer-a row = %+v, unexpected", a)
	}
	b := got.Hosts[1]
	if b.Alias != "peer-b" || b.Verified || b.HasToken || !b.HasInboundToken || b.AllowBypass {
		t.Errorf("peer-b row = %+v, unexpected", b)
	}
}

// ---- POST /api/peers/hosts ----

func TestHandleAddHost_NoToken_UnverifiedAndPersisted(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "peer-a",
		"url":   "https://a.example",
	}, adminPrincipal())

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Alias        string `json:"alias"`
		URL          string `json:"url"`
		HostID       string `json:"host_id"`
		InboundToken string `json:"inbound_token"`
		Verified     bool   `json:"verified"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Alias != "peer-a" || got.URL != "https://a.example" || got.HostID != "" || got.Verified {
		t.Errorf("response = %+v, unexpected", got)
	}
	if !inboundTokenPattern.MatchString(got.InboundToken) {
		t.Errorf("inbound_token = %q, want match %s", got.InboundToken, inboundTokenPattern.String())
	}

	reloaded := loadCfg(t, cfgPath)
	idx := reloaded.Peers.FindPeerHostByAlias("peer-a")
	if idx == -1 {
		t.Fatalf("alias not persisted: %+v", reloaded.Peers.Hosts)
	}
	h := reloaded.Peers.Hosts[idx]
	if h.HostID != "" {
		t.Errorf("persisted host_id = %q, want empty", h.HostID)
	}
	if h.InboundToken != got.InboundToken {
		t.Errorf("persisted inbound_token = %q, want %q", h.InboundToken, got.InboundToken)
	}
}

func TestHandleAddHost_WithToken_VerifiedAndHostIDPersisted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-tok" {
			t.Errorf("Authorization = %q, want Bearer secret-tok", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"host_id":"air:1","ok":true,"partial":false,"peers":[]}`))
	}))
	defer srv.Close()

	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, nil) // production fetchRemote against the real server

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air",
		"url":   srv.URL,
		"token": "secret-tok",
	}, adminPrincipal())

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		HostID   string `json:"host_id"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Verified || got.HostID != "air:1" {
		t.Errorf("response = %+v, want verified=true host_id=air:1", got)
	}

	reloaded := loadCfg(t, cfgPath)
	idx := reloaded.Peers.FindPeerHostByAlias("air")
	if idx == -1 {
		t.Fatalf("alias not persisted")
	}
	if reloaded.Peers.Hosts[idx].HostID != "air:1" {
		t.Errorf("persisted host_id = %q, want air:1", reloaded.Peers.Hosts[idx].HostID)
	}
	if reloaded.Peers.Hosts[idx].Token != "secret-tok" {
		t.Errorf("persisted token = %q, want secret-tok", reloaded.Peers.Hosts[idx].Token)
	}
}

func TestHandleAddHost_BadToken_502NothingPersisted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, nil)

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air",
		"url":   srv.URL,
		"token": "bad-tok",
	}, adminPrincipal())

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
		t.Fatalf("alias should not be persisted: %+v", reloaded.Peers.Hosts)
	}
}

func TestHandleAddHost_RemoteNotOK_502(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", OK: false, Error: "boom"}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://a.example", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
		t.Fatalf("alias should not be persisted")
	}
}

// TestHandleAddHost_RemoteErrorBounded_502 pins Item 3: a remote peer's
// (attacker-controlled) error text must be bounded and prefixed before it
// reaches this host's own 502 body, rather than passed through verbatim.
func TestHandleAddHost_RemoteErrorBounded_502(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	longErr := strings.Repeat("x", 300)
	m := newHostsTestModule(c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", OK: false, Error: longErr}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://a.example", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(got.Error, "peer: ") {
		t.Errorf("error = %q, want prefix %q", got.Error, "peer: ")
	}
	if !strings.HasSuffix(got.Error, "…") {
		t.Errorf("error = %q, want to end with an ellipsis", got.Error)
	}
	if len(got.Error) > 210 {
		t.Errorf("error length = %d bytes, want <= 210; error=%q", len(got.Error), got.Error)
	}
	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
		t.Fatalf("alias should not be persisted")
	}
}

func TestHandleAddHost_RemoteEmptyHostID_502(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "", OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://a.example", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
		t.Fatalf("alias should not be persisted")
	}
}

func TestHandleAddHost_RemoteHostIDEqualsLocal_400(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "local:1", OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://a.example", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
		t.Fatalf("alias should not be persisted")
	}
}

func TestHandleAddHost_TokenEqualsAdminToken_400(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "admin-secret", nil)
	m := newHostsTestModule(c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://a.example", "token": "admin-secret",
	}, adminPrincipal())

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "token equals admin token") {
		t.Errorf("body = %s, want mention of token equals admin token", rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
		t.Fatalf("alias should not be persisted")
	}
}

func TestHandleAddHost_DuplicateAliasCaseInsensitive_409(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "Air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://a2.example",
	}, adminPrincipal())

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleAddHost_InvalidAlias(t *testing.T) {
	cases := []string{"a/b", "..", "local"}
	for _, alias := range cases {
		t.Run(alias, func(t *testing.T) {
			c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
			m := newHostsTestModule(c, failIfCalledFetch(t))

			rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
				"alias": alias, "url": "https://a.example",
			}, adminPrincipal())
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("alias=%q status = %d, want 400; body=%s", alias, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandleAddHost_InvalidURL(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "not-a-url",
	}, adminPrincipal())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleAddHost_URLNormalization_TrailingSlashStripped pins Item 1:
// a trailing slash on the submitted URL must not survive into the stored
// (and echoed) URL, so baseURL+"/api/peers" in fetchRemote never produces
// a double slash. The remote server itself doubles as the proof: it fails
// the test if it ever sees a path other than exactly "/api/peers".
func TestHandleAddHost_URLNormalization_TrailingSlashStripped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/peers" {
			t.Errorf("remote request path = %q, want /api/peers", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"host_id":"air:1","ok":true,"partial":false,"peers":[]}`))
	}))
	defer srv.Close()

	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, nil) // production fetchRemote against the real server

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": srv.URL + "/", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		URL      string `json:"url"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Verified || got.URL != srv.URL {
		t.Errorf("response = %+v, want verified=true url=%s (no trailing slash)", got, srv.URL)
	}

	reloaded := loadCfg(t, cfgPath)
	idx := reloaded.Peers.FindPeerHostByAlias("air")
	if idx == -1 {
		t.Fatalf("alias not persisted")
	}
	if reloaded.Peers.Hosts[idx].URL != srv.URL {
		t.Errorf("persisted url = %q, want %q (no trailing slash)", reloaded.Peers.Hosts[idx].URL, srv.URL)
	}
}

// TestHandleAddHost_URLValidation_RejectsUnsafeComponents pins Item 1's
// rejection of components with no meaning for a peer host URL: a query
// string, a fragment, and embedded userinfo. Each must 400 with an
// "invalid url" message and persist nothing.
func TestHandleAddHost_URLValidation_RejectsUnsafeComponents(t *testing.T) {
	cases := []string{
		"https://a.example?x=1",
		"https://a.example#f",
		"https://u:p@a.example",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
			m := newHostsTestModule(c, failIfCalledFetch(t))

			rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
				"alias": "air", "url": raw,
			}, adminPrincipal())
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("url=%q status = %d, want 400; body=%s", raw, rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "invalid url") {
				t.Errorf("url=%q body = %s, want mention of invalid url", raw, rr.Body.String())
			}
			reloaded := loadCfg(t, cfgPath)
			if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
				t.Fatalf("url=%q alias should not be persisted", raw)
			}
		})
	}
}

// TestHandleAddHost_Concurrent_SameAlias gates two parallel POSTs of the
// same alias behind a barrier inside the fake verify so both requests pass
// their pre-checks before either commits: exactly one succeeds (201) and
// the other loses the race at UpdateConfig's re-check (409), and the file
// ends up with exactly one entry.
func TestHandleAddHost_Concurrent_SameAlias(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	fetch := func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		entered <- struct{}{}
		<-release
		return ipeers.Envelope{HostID: "remote:1", OK: true, Peers: []ipeers.PeerRecord{}}, nil
	}
	m := newHostsTestModule(c, fetch)

	results := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
				"alias": "dup", "url": "https://dup.example", "token": "tok",
			}, adminPrincipal())
		}()
	}

	<-entered
	<-entered
	close(release)
	wg.Wait()

	codes := []int{results[0].Code, results[1].Code}
	sort.Ints(codes)
	if codes[0] != http.StatusCreated || codes[1] != http.StatusConflict {
		t.Fatalf("codes = %v, want [201 409]; bodies=%s / %s", codes, results[0].Body.String(), results[1].Body.String())
	}

	reloaded := loadCfg(t, cfgPath)
	count := 0
	for _, h := range reloaded.Peers.Hosts {
		if strings.EqualFold(h.Alias, "dup") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("persisted %d entries for alias dup, want 1: %+v", count, reloaded.Peers.Hosts)
	}
}

// ---- PUT /api/peers/hosts/{alias} ----

func TestHandlePutHost_TokenVerifiesAndStores(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{
		"token": "new-tok",
	}, adminPrincipal())

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Alias    string `json:"alias"`
		HostID   string `json:"host_id"`
		Verified bool   `json:"verified"`
		HasToken bool   `json:"has_token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Alias != "air" || !got.Verified || got.HostID != "air:1" || !got.HasToken {
		t.Errorf("response = %+v, unexpected", got)
	}
	if strings.Contains(rr.Body.String(), "new-tok") {
		t.Fatalf("body leaks token value: %s", rr.Body.String())
	}

	reloaded := loadCfg(t, cfgPath)
	idx := reloaded.Peers.FindPeerHostByAlias("air")
	if idx == -1 || reloaded.Peers.Hosts[idx].Token != "new-tok" || reloaded.Peers.Hosts[idx].HostID != "air:1" {
		t.Errorf("persisted host = %+v", reloaded.Peers.Hosts)
	}
}

func TestHandlePutHost_AllowBypassOnly(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{
		"allow_bypass": true,
	}, adminPrincipal())

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		AllowBypass bool   `json:"allow_bypass"`
		HostID      string `json:"host_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.AllowBypass || got.HostID != "" {
		t.Errorf("response = %+v, unexpected", got)
	}

	reloaded := loadCfg(t, cfgPath)
	idx := reloaded.Peers.FindPeerHostByAlias("air")
	if idx == -1 || !reloaded.Peers.Hosts[idx].AllowBypass {
		t.Errorf("persisted host = %+v", reloaded.Peers.Hosts)
	}
}

// TestHandlePutHost_ResponseReflectsCommittedValue pins Item 6: the PUT
// response must be built from the entry as THIS request's own commit left
// it, not from a later re-read of the live config. A config-change
// callback fires synchronously inside UpdateConfig (after commit, before
// UpdateConfig returns to the handler) and races a second, unrelated
// update in that flips allow_bypass back to false — proving the response
// still reports true, the value this PUT itself set.
func TestHandlePutHost_ResponseReflectsCommittedValue(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(c, failIfCalledFetch(t))

	// A plain bool guard, not sync.Once: NotifyConfigChange re-invokes this
	// same callback for the nested UpdateConfig call below (same
	// goroutine), and sync.Once.Do is not reentrant — it would deadlock on
	// its own internal mutex.
	var fired bool
	c.OnConfigChange(func() {
		if fired {
			return
		}
		fired = true
		if err := c.UpdateConfig(func(cfg *config.Config) error {
			i := cfg.Peers.FindPeerHostByAlias("air")
			if i == -1 {
				return nil
			}
			cfg.Peers.Hosts[i].AllowBypass = false
			return nil
		}); err != nil {
			t.Errorf("concurrent update: %v", err)
		}
	})

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{
		"allow_bypass": true,
	}, adminPrincipal())

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		AllowBypass bool `json:"allow_bypass"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.AllowBypass {
		t.Errorf("response allow_bypass = %v, want true (the value THIS request committed, not a value a concurrent update wrote afterwards)", got.AllowBypass)
	}
}

func TestHandlePutHost_UnknownAlias_404(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/ghost", map[string]any{
		"allow_bypass": true,
	}, adminPrincipal())
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlePutHost_HostIDMismatch_409OldTokenKept(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:X", Token: "old-tok", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:Y", OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{
		"token": "new-tok",
	}, adminPrincipal())

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	idx := reloaded.Peers.FindPeerHostByAlias("air")
	if idx == -1 {
		t.Fatalf("host disappeared")
	}
	if reloaded.Peers.Hosts[idx].Token != "old-tok" || reloaded.Peers.Hosts[idx].HostID != "air:X" {
		t.Errorf("persisted host = %+v, want token/host_id unchanged", reloaded.Peers.Hosts[idx])
	}
}

func TestHandlePutHost_TokenEqualsAdminToken_400(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "admin-secret", hosts)
	m := newHostsTestModule(c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{
		"token": "admin-secret",
	}, adminPrincipal())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandlePutHost_ConcurrentDelete_404NothingRecreated gates a PUT's
// verify behind a barrier, deletes the entry while the verify is in
// flight, then releases the verify: the PUT's commit-under-lock re-check
// must see the entry gone and refuse to recreate it.
func TestHandlePutHost_ConcurrentDelete_404NothingRecreated(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	fetch := func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		entered <- struct{}{}
		<-release
		return ipeers.Envelope{HostID: "air:1", OK: true, Peers: []ipeers.PeerRecord{}}, nil
	}
	m := newHostsTestModule(c, fetch)

	var putResult *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		putResult = doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{
			"token": "new-tok",
		}, adminPrincipal())
	}()

	<-entered

	delResult := doHostsRequest(t, m, http.MethodDelete, "/api/peers/hosts/air", nil, adminPrincipal())
	if delResult.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", delResult.Code, delResult.Body.String())
	}

	close(release)
	wg.Wait()

	if putResult.Code != http.StatusNotFound {
		t.Fatalf("PUT status = %d, want 404; body=%s", putResult.Code, putResult.Body.String())
	}

	reloaded := loadCfg(t, cfgPath)
	if len(reloaded.Peers.Hosts) != 0 {
		t.Fatalf("host recreated: %+v", reloaded.Peers.Hosts)
	}
}

// TestHandlePutHost_ConcurrentURLChange_409 gates a PUT's verify behind a
// barrier, then while it is in flight deletes and re-adds the alias
// pointing at a different URL, then releases the verify: the verify ran
// against the OLD url, so committing its learned token/host_id against the
// entry now sitting at a NEW url would be wrong. The commit-under-lock
// re-check must see the url changed and refuse, leaving the re-added
// entry's own token/host_id untouched.
func TestHandlePutHost_ConcurrentURLChange_409(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	fetch := func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		entered <- struct{}{}
		<-release
		return ipeers.Envelope{HostID: "air:1", OK: true, Peers: []ipeers.PeerRecord{}}, nil
	}
	m := newHostsTestModule(c, fetch)

	var putResult *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		putResult = doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{
			"token": "new-tok",
		}, adminPrincipal())
	}()

	<-entered

	delResult := doHostsRequest(t, m, http.MethodDelete, "/api/peers/hosts/air", nil, adminPrincipal())
	if delResult.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", delResult.Code, delResult.Body.String())
	}
	addResult := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://different.example",
	}, adminPrincipal())
	if addResult.Code != http.StatusCreated {
		t.Fatalf("re-add status = %d, want 201; body=%s", addResult.Code, addResult.Body.String())
	}

	close(release)
	wg.Wait()

	if putResult.Code != http.StatusConflict {
		t.Fatalf("PUT status = %d, want 409; body=%s", putResult.Code, putResult.Body.String())
	}

	reloaded := loadCfg(t, cfgPath)
	idx := reloaded.Peers.FindPeerHostByAlias("air")
	if idx == -1 {
		t.Fatalf("host missing after concurrent url change")
	}
	h := reloaded.Peers.Hosts[idx]
	if h.URL != "https://different.example" || h.Token != "" || h.HostID != "" {
		t.Errorf("persisted host = %+v, want the re-added url with empty token/host_id", h)
	}
}

// ---- DELETE /api/peers/hosts/{alias} ----

func TestHandleDeleteHost_204ThenGoneFromList(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodDelete, "/api/peers/hosts/air", nil, adminPrincipal())
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}

	listRR := doHostsRequest(t, m, http.MethodGet, "/api/peers/hosts", nil, adminPrincipal())
	if strings.Contains(listRR.Body.String(), "air") {
		t.Fatalf("list still contains air: %s", listRR.Body.String())
	}

	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
		t.Fatalf("still persisted: %+v", reloaded.Peers.Hosts)
	}
}

func TestHandleDeleteHost_UnknownAlias_404(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodDelete, "/api/peers/hosts/ghost", nil, adminPrincipal())
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// TestAliasWithDot_SurvivesAddSetTokenRemove exercises the full lifecycle
// for an alias containing a dot, which must round-trip through
// PathValue("alias") and FindPeerHostByAlias without being mistaken for a
// file extension or path boundary.
func TestAliasWithDot_SurvivesAddSetTokenRemove(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"host_id":"air2026:1","ok":true,"partial":false,"peers":[]}`))
	}))
	defer srv.Close()

	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(c, nil)

	addRR := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air.2026", "url": srv.URL,
	}, adminPrincipal())
	if addRR.Code != http.StatusCreated {
		t.Fatalf("add status = %d, want 201; body=%s", addRR.Code, addRR.Body.String())
	}

	putRR := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air.2026", map[string]any{
		"token": "tok-for-air",
	}, adminPrincipal())
	if putRR.Code != http.StatusOK {
		t.Fatalf("put status = %d, want 200; body=%s", putRR.Code, putRR.Body.String())
	}

	reloaded := loadCfg(t, cfgPath)
	idx := reloaded.Peers.FindPeerHostByAlias("air.2026")
	if idx == -1 || reloaded.Peers.Hosts[idx].HostID != "air2026:1" {
		t.Fatalf("persisted host = %+v", reloaded.Peers.Hosts)
	}

	delRR := doHostsRequest(t, m, http.MethodDelete, "/api/peers/hosts/air.2026", nil, adminPrincipal())
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", delRR.Code, delRR.Body.String())
	}
	reloaded = loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air.2026") != -1 {
		t.Fatalf("still persisted after delete: %+v", reloaded.Peers.Hosts)
	}
}

// ---- integration: two real modules pairing both ways ----

// buildOuterHandler mirrors cmd/pdx/http_chain.go's newOuterHandler at a
// scope sufficient for these tests: PeerAuth on /api/peers (+ subtree),
// TokenAuth on everything else. CORS/IPWhitelist/PairingGuard are omitted
// since none of these tests exercise them.
func buildOuterHandler(c *core.Core, mux http.Handler) http.Handler {
	tokenFn := func() string {
		c.CfgMu.RLock()
		defer c.CfgMu.RUnlock()
		return c.Cfg.Token
	}
	peersFn := func() config.PeersConfig {
		c.CfgMu.RLock()
		defer c.CfgMu.RUnlock()
		p := c.Cfg.Peers
		p.Hosts = append([]config.PeerHost(nil), p.Hosts...)
		return p
	}

	peerChain := middleware.PeerAuth(tokenFn, peersFn, HostRoutePolicy)(mux)
	general := middleware.TokenAuth(tokenFn, nil)(mux)

	outer := http.NewServeMux()
	outer.Handle("/api/peers", peerChain)
	outer.Handle("/api/peers/", peerChain)
	outer.Handle("/", general)
	return outer
}

func doRequestBearer(t *testing.T, h http.Handler, method, target, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestCapability_InboundTokenHolder pins what an inbound-token holder (a
// host principal) may reach through the real PeerAuth/TokenAuth chain:
// GET /api/peers yes, scope=all no, any hosts route no, and the general
// chain (here represented by /api/config) not at all.
func TestCapability_InboundTokenHolder(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "peer-a", InboundToken: "inbound-secret", HostID: "peer-a:1"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "admin-secret", hosts)
	m := newHostsTestModule(c, nil)
	m.registryDir = t.TempDir()
	m.liveness = allLiveLiveness(time.Now())

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	chain := buildOuterHandler(c, mux)

	if rr := doRequestBearer(t, chain, http.MethodGet, "/api/peers", "inbound-secret"); rr.Code != http.StatusOK {
		t.Errorf("GET /api/peers = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if rr := doRequestBearer(t, chain, http.MethodGet, "/api/peers?scope=all", "inbound-secret"); rr.Code != http.StatusForbidden {
		t.Errorf("GET /api/peers?scope=all = %d, want 403", rr.Code)
	}
	if rr := doRequestBearer(t, chain, http.MethodGet, "/api/peers/hosts", "inbound-secret"); rr.Code != http.StatusForbidden {
		t.Errorf("GET /api/peers/hosts = %d, want 403", rr.Code)
	}
	if rr := doRequestBearer(t, chain, http.MethodPost, "/api/peers/hosts", "inbound-secret"); rr.Code != http.StatusForbidden {
		t.Errorf("POST /api/peers/hosts = %d, want 403", rr.Code)
	}
	if rr := doRequestBearer(t, chain, http.MethodPut, "/api/peers/hosts/x", "inbound-secret"); rr.Code != http.StatusForbidden {
		t.Errorf("PUT /api/peers/hosts/x = %d, want 403", rr.Code)
	}
	if rr := doRequestBearer(t, chain, http.MethodDelete, "/api/peers/hosts/x", "inbound-secret"); rr.Code != http.StatusForbidden {
		t.Errorf("DELETE /api/peers/hosts/x = %d, want 403", rr.Code)
	}
	if rr := doRequestBearer(t, chain, http.MethodGet, "/api/config", "inbound-secret"); rr.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/config = %d, want 401", rr.Code)
	}
}

// TestPairing_TwoRealModulesBothWays wires two real *core.Core + *Module
// pairs behind httptest servers and drives the actual two-step pairing
// handshake: each side POSTs the other as an unverified host, then PUTs
// the token the other side minted, causing a real fetchRemote call
// through real PeerAuth. Both ends up verified, and a scope=all fan-out on
// each shows the other.
func TestPairing_TwoRealModulesBothWays(t *testing.T) {
	coreA, _ := newHostsTestCore(t, "hostA:1", "a", "", nil)
	moduleA := newHostsTestModule(coreA, nil)
	moduleA.registryDir = t.TempDir()
	moduleA.liveness = allLiveLiveness(time.Now())
	muxA := http.NewServeMux()
	moduleA.RegisterRoutes(muxA)
	serverA := httptest.NewServer(buildOuterHandler(coreA, muxA))
	defer serverA.Close()

	coreB, _ := newHostsTestCore(t, "hostB:1", "b", "", nil)
	moduleB := newHostsTestModule(coreB, nil)
	moduleB.registryDir = t.TempDir()
	moduleB.liveness = allLiveLiveness(time.Now())
	muxB := http.NewServeMux()
	moduleB.RegisterRoutes(muxB)
	serverB := httptest.NewServer(buildOuterHandler(coreB, muxB))
	defer serverB.Close()

	// B learns about A first, unverified (no token yet — B doesn't have
	// anything A trusts).
	rr := doHostsRequest(t, moduleB, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "a", "url": serverA.URL,
	}, adminPrincipal())
	if rr.Code != http.StatusCreated {
		t.Fatalf("B add A: status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var bAddResp struct {
		InboundToken string `json:"inbound_token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &bAddResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tokenForA := bAddResp.InboundToken // what A must present to B

	// A adds B, presenting tokenForA — this makes a REAL network call to
	// serverB's GET /api/peers through real PeerAuth.
	rr = doHostsRequest(t, moduleA, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "b", "url": serverB.URL, "token": tokenForA,
	}, adminPrincipal())
	if rr.Code != http.StatusCreated {
		t.Fatalf("A add B: status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var aAddResp struct {
		HostID       string `json:"host_id"`
		Verified     bool   `json:"verified"`
		InboundToken string `json:"inbound_token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &aAddResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !aAddResp.Verified || aAddResp.HostID != "hostB:1" {
		t.Fatalf("A add B response = %+v, want verified=true host_id=hostB:1", aAddResp)
	}
	tokenForB := aAddResp.InboundToken // what B must present to A

	// B completes the handshake with a PUT presenting tokenForB — this
	// makes a REAL network call to serverA's GET /api/peers.
	rr = doHostsRequest(t, moduleB, http.MethodPut, "/api/peers/hosts/a", map[string]any{
		"token": tokenForB,
	}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("B put A token: status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var bPutResp struct {
		HostID   string `json:"host_id"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &bPutResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !bPutResp.Verified || bPutResp.HostID != "hostA:1" {
		t.Fatalf("B put A response = %+v, want verified=true host_id=hostA:1", bPutResp)
	}

	// scope=all on A should now show B's row as OK.
	ctxAdmin := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rrAll := doGetPeersWithContext(t, moduleA, "/api/peers?scope=all", ctxAdmin)
	if rrAll.Code != http.StatusOK {
		t.Fatalf("A scope=all: status = %d, body=%s", rrAll.Code, rrAll.Body.String())
	}
	var allA ipeers.AllEnvelope
	if err := json.Unmarshal(rrAll.Body.Bytes(), &allA); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(allA.Hosts) != 2 || allA.Hosts[1].Alias != "b" || !allA.Hosts[1].OK || allA.Hosts[1].HostID != "hostB:1" {
		t.Fatalf("A scope=all hosts = %+v, want [local, b OK]", allA.Hosts)
	}

	// scope=all on B should now show A's row as OK.
	rrAll = doGetPeersWithContext(t, moduleB, "/api/peers?scope=all", ctxAdmin)
	if rrAll.Code != http.StatusOK {
		t.Fatalf("B scope=all: status = %d, body=%s", rrAll.Code, rrAll.Body.String())
	}
	var allB ipeers.AllEnvelope
	if err := json.Unmarshal(rrAll.Body.Bytes(), &allB); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(allB.Hosts) != 2 || allB.Hosts[1].Alias != "a" || !allB.Hosts[1].OK || allB.Hosts[1].HostID != "hostA:1" {
		t.Fatalf("B scope=all hosts = %+v, want [local, a OK]", allB.Hosts)
	}
}
