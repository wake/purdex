// internal/module/peers/hosts_test.go
package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
func newHostsTestModule(t *testing.T, c *core.Core, fetch fetchFunc) *Module {
	t.Helper()
	m := newTestModule(t, c, &fakeSessions{}, &fakeOwners{}, "", ipeers.DefaultLiveness(), &fakeClock{times: []time.Time{time.Now()}}, 2*time.Second)
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

// TestValidHostID pins Item 5's acceptance rule for a peer-reported
// host_id: non-empty, at most 128 bytes, and every rune printable and not
// a space — so a control character (e.g. an ANSI escape), a length far
// beyond any legitimate host_id, or an empty value is never accepted.
func TestValidHostID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"empty", "", false},
		{"typical", "mlab:abc123", true},
		{"exactly 128 bytes", strings.Repeat("a", 128), true},
		{"129 bytes", strings.Repeat("a", 129), false},
		{"ansi escape", "\x1b[31mred\x1b[0m", false},
		{"contains a space", "has space", false},
		{"contains a tab", "has\ttab", false},
		{"contains a newline", "has\nnewline", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validHostID(tc.id); got != tc.want {
				t.Errorf("validHostID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

// TestSanitizeLearnedAlias_RejectsUnsafe pins for a peer's self-reported
// alias (spec §7) the posture validHostID already holds for its
// self-reported host_id: it is attacker-controlled, and it flows into this
// host's config, into address strings and into this host's own terminal
// output. Anything config.ValidateAlias refuses — empty, something that is
// not one safe URL path segment, a reserved dot name, a control character,
// an oversized value, or a collision with the local alias in any case —
// must come back as "" rather than be adopted.
func TestSanitizeLearnedAlias_RejectsUnsafe(t *testing.T) {
	for _, bad := range []string{"", "has/slash", "..", "esc\x1b[31m", strings.Repeat("a", 65), "mlab", "MLAB"} {
		if got := sanitizeLearnedAlias(bad, "mlab"); got != "" {
			t.Errorf("alias %q survived as %q", bad, got)
		}
	}
	if got := sanitizeLearnedAlias("air26", "mlab"); got != "air26" {
		t.Errorf("safe alias = %q, want %q", got, "air26")
	}
}

// ---- GET /api/peers/hosts ----

func TestHandleListHosts_NonAdminForbidden(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/hosts", nil, hostPrincipal("peer-a"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleListHosts_NoPrincipalForbidden(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/hosts", nil, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleListHosts_Empty(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, nil)

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
	m := newHostsTestModule(t, c, nil)

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
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, nil) // production fetchRemote against the real server

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
	m := newHostsTestModule(t, c, nil)

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
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", OK: false, Error: "boom"}, nil))

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
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", OK: false, Error: longErr}, nil))

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
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "", OK: true}, nil))

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

// TestHandleAddHost_RemoteInvalidHostIDTooLong_502NothingPersisted pins
// Item 5: a remote reporting a host_id over 128 bytes must be rejected as
// invalid (502), not truncated and stored.
func TestHandleAddHost_RemoteInvalidHostIDTooLong_502NothingPersisted(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	longHostID := strings.Repeat("y", 300)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: longHostID, OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://a.example", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid host_id") {
		t.Errorf("body = %s, want mention of invalid host_id", rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
		t.Fatalf("alias should not be persisted")
	}
}

// TestHandleAddHost_RemoteInvalidHostIDAnsiEscape_502NothingPersisted pins
// Item 5: a remote reporting a host_id containing a control character
// (e.g. an ANSI escape) must be rejected as invalid, not stored verbatim.
func TestHandleAddHost_RemoteInvalidHostIDAnsiEscape_502NothingPersisted(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "\x1b[31mair:1\x1b[0m", OK: true}, nil))

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

// TestHandleAddHost_LearnedHostIDCarriesOurToken_502NothingPersisted pins
// the fix round 1 regression (#1152): a learned host_id that equals (or
// embeds) our own outbound token — the very Bearer this request sent —
// passes validHostID's shape check (length/printable/no-whitespace only)
// but must still be refused. Persisting it would serve our token back out
// of every hostRow.host_id forever.
func TestHandleAddHost_LearnedHostIDCarriesOurToken_502NothingPersisted(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	const tok = "pdxp_deadbeefdeadbeefdeadbeefdeadbeef"
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: tok, OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://a.example", "token": tok,
	}, adminPrincipal())

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid host_id") {
		t.Errorf("body = %s, want mention of invalid host_id", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), tok) {
		t.Fatalf("body leaks the token: %s", rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
		t.Fatalf("alias should not be persisted")
	}
}

func TestHandleAddHost_RemoteHostIDEqualsLocal_400(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "local:1", OK: true}, nil))

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

// TestHandleAddHost_AdoptsPublishedAlias pins spec §7.2: a POST with no
// alias adopts the one the peer publishes for itself, so the address
// "air26/..." means the same thing on both machines.
func TestHandleAddHost_AdoptsPublishedAlias(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:aaa", Alias: "air26", OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"url": "https://air.example", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Alias    string `json:"alias"`
		HostID   string `json:"host_id"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Alias != "air26" {
		t.Errorf("response alias = %q, want air26", got.Alias)
	}
	if got.HostID != "air:aaa" || !got.Verified {
		t.Errorf("response = %+v, want verified=true host_id=air:aaa", got)
	}

	reloaded := loadCfg(t, cfgPath)
	idx := reloaded.Peers.FindPeerHostByAlias("air26")
	if idx == -1 {
		t.Fatalf("learned alias not persisted: %+v", reloaded.Peers.Hosts)
	}
	if reloaded.Peers.Hosts[idx].HostID != "air:aaa" {
		t.Errorf("persisted host_id = %q, want air:aaa", reloaded.Peers.Hosts[idx].HostID)
	}
}

// TestHandleAddHost_ExplicitAliasWins pins the other half of §7.2: the
// published alias is only a fallback. An operator who names the host keeps
// that name, whatever the peer calls itself.
func TestHandleAddHost_ExplicitAliasWins(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:aaa", Alias: "air26", OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://air.example", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Alias string `json:"alias"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Alias != "air" {
		t.Errorf("response alias = %q, want air", got.Alias)
	}

	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") == -1 {
		t.Fatalf("explicit alias not persisted: %+v", reloaded.Peers.Hosts)
	}
	if reloaded.Peers.FindPeerHostByAlias("air26") != -1 {
		t.Errorf("published alias was adopted despite an explicit one: %+v", reloaded.Peers.Hosts)
	}
}

// TestHandleAddHost_PublishedAliasCollisionIs409 pins §7.3: a learned
// alias colliding with an already-configured host is NOT auto-suffixed
// ("air26-2" would be unportable in a new way, which is the problem this
// phase exists to remove). The add fails naming the alias so the operator
// can supply one explicitly. config.ValidateAlias cannot catch this — it
// compares only against the LOCAL alias — so the check is the handler's
// own, and the body naming the alias is what distinguishes it from
// UpdateConfig's generic concurrent-change re-check.
func TestHandleAddHost_PublishedAliasCollisionIs409(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air26", URL: "https://old.example", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:aaa", Alias: "air26", OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"url": "https://air.example", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "air26") {
		t.Errorf("body = %s, want it to name the colliding alias air26", rr.Body.String())
	}

	reloaded := loadCfg(t, cfgPath)
	if len(reloaded.Peers.Hosts) != 1 {
		t.Fatalf("hosts = %+v, want only the single pre-existing entry", reloaded.Peers.Hosts)
	}
	if reloaded.Peers.Hosts[0].URL != "https://old.example" {
		t.Errorf("pre-existing host was overwritten: %+v", reloaded.Peers.Hosts[0])
	}
}

// TestHandleAddHost_NoAliasAnywhereIs400 pins the floor: with neither an
// explicit alias nor a published one there is no name to file the host
// under, and nothing is invented.
func TestHandleAddHost_NoAliasAnywhereIs400(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:aaa", Alias: "", OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"url": "https://air.example", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if len(reloaded.Peers.Hosts) != 0 {
		t.Fatalf("nothing should be persisted: %+v", reloaded.Peers.Hosts)
	}
}

// TestHandleAddHost_NoTokenNoAlias_400WithoutDialing pins that the
// published alias is only available where a verify actually happens: with
// no outbound token there is no envelope, so an omitted alias is simply
// missing and the add is refused without any network call.
func TestHandleAddHost_NoTokenNoAlias_400WithoutDialing(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"url": "https://air.example",
	}, adminPrincipal())

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if len(reloaded.Peers.Hosts) != 0 {
		t.Fatalf("nothing should be persisted: %+v", reloaded.Peers.Hosts)
	}
}

// TestHandleAddHost_UnsafePublishedAliasNotAdoptedNorEchoed pins the
// posture sanitizeLearnedAlias exists for: a peer's self-reported alias is
// attacker-controlled data. An unsafe one is not adopted, AND it is not
// quoted back into this host's own terminal output — the refusal is the
// generic "the peer published none", never a message carrying the peer's
// own bytes.
func TestHandleAddHost_UnsafePublishedAliasNotAdoptedNorEchoed(t *testing.T) {
	for _, bad := range []string{"esc\x1b[31mred", "has/slash", "..", strings.Repeat("a", 65), "local"} {
		t.Run(bad, func(t *testing.T) {
			c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
			m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:aaa", Alias: bad, OK: true}, nil))

			rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
				"url": "https://air.example", "token": "tok",
			}, adminPrincipal())

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "the peer published none") {
				t.Errorf("body = %s, want the generic no-alias refusal", rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), bad) {
				t.Errorf("body echoes the peer's unsafe alias %q: %s", bad, rr.Body.String())
			}
			reloaded := loadCfg(t, cfgPath)
			if len(reloaded.Peers.Hosts) != 0 {
				t.Fatalf("nothing should be persisted: %+v", reloaded.Peers.Hosts)
			}
		})
	}
}

// TestHandleAddHost_LearnedAliasSelfPairing_Refused pins that adopting a
// published alias opens no back door around the self-pairing refusal: the
// peer that answered is this very daemon, and neither the alias nor the
// entry survives.
func TestHandleAddHost_LearnedAliasSelfPairing_Refused(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "local:1", Alias: "loopback", OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"url": "https://air.example", "token": "tok",
	}, adminPrincipal())

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cannot pair a host with itself") {
		t.Errorf("body = %s, want the self-pairing refusal", rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if len(reloaded.Peers.Hosts) != 0 {
		t.Fatalf("nothing should be persisted: %+v", reloaded.Peers.Hosts)
	}
}

func TestHandleAddHost_TokenEqualsAdminToken_400(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "admin-secret", nil)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
			m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, nil) // production fetchRemote against the real server

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
		"http://h:7860?", // ForceQuery: RawQuery == "" but a trailing "?" is present
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)
			m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, fetch)

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
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", OK: true}, nil))

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
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:Y", OK: true}, nil))

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

// TestHandlePutHost_RemoteInvalidHostID_502OldValuesKept pins Item 5: a
// PUT verify against a remote reporting an invalid host_id (too long) is
// rejected outright (502) before ever reaching UpdateConfig, leaving the
// entry's existing token/host_id untouched.
func TestHandlePutHost_RemoteInvalidHostID_502OldValuesKept(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:X", Token: "old-tok", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	longHostID := strings.Repeat("z", 300)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: longHostID, OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{
		"token": "new-tok",
	}, adminPrincipal())

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
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

// TestHandlePutHost_LearnedHostIDCarriesOurToken_502OldValuesKept mirrors
// the add-host regression (#1152 fix round 1) for PUT: an entry with no
// host_id yet must not learn one that equals (or embeds) the token this
// very request sent as its Bearer, even though it passes validHostID's
// shape check.
func TestHandlePutHost_LearnedHostIDCarriesOurToken_502OldValuesKept(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	const tok = "pdxp_deadbeefdeadbeefdeadbeefdeadbeef"
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: tok, OK: true}, nil))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{
		"token": tok,
	}, adminPrincipal())

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), tok) {
		t.Fatalf("body leaks the token: %s", rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	idx := reloaded.Peers.FindPeerHostByAlias("air")
	if idx == -1 {
		t.Fatalf("host disappeared")
	}
	if reloaded.Peers.Hosts[idx].HostID != "" {
		t.Errorf("persisted host_id = %q, want unchanged empty", reloaded.Peers.Hosts[idx].HostID)
	}
}

func TestHandlePutHost_TokenEqualsAdminToken_400(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "admin-secret", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, fetch)

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
	m := newHostsTestModule(t, c, fetch)

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

// TestHandlePutHost_ConcurrentRecreateSameURL_409NothingRecreated pins
// Item 3: a PUT's verify races a DELETE + re-POST of the SAME alias at the
// SAME url. Because POST always mints a fresh InboundToken, the re-created
// entry is not the one the in-flight verify ran against, even though its
// URL happens to match — the URL-only re-check the old code used would
// wrongly let this commit through. The commit must also re-check the
// entry's identity (InboundToken, captured pre-lock) and refuse, leaving
// the re-created entry's own token/host_id/allow_bypass untouched.
func TestHandlePutHost_ConcurrentRecreateSameURL_409NothingRecreated(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	fetch := func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		entered <- struct{}{}
		<-release
		return ipeers.Envelope{HostID: "air:1", OK: true, Peers: []ipeers.PeerRecord{}}, nil
	}
	m := newHostsTestModule(t, c, fetch)

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
	// Same alias, same url, no token: re-created unverified with a fresh
	// InboundToken minted by POST.
	addResult := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air", "url": "https://a.example",
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
		t.Fatalf("host missing after concurrent recreate")
	}
	h := reloaded.Peers.Hosts[idx]
	if h.URL != "https://a.example" || h.Token != "" || h.HostID != "" || h.AllowBypass {
		t.Errorf("persisted host = %+v, want the re-created entry untouched (empty token/host_id, allow_bypass false)", h)
	}
}

// TestHandlePutHost_RenameOnly_ConcurrentRecreate_409NotRenamed forces the
// interleaving a rename-only PUT can hit — DELETE + re-POST at the same
// alias between the handler's pre-lock snapshot and its commit — through
// the putHostAfterSnapshot seam, since a rename-only request has no fetch
// to block on. The hoisted identity re-check must refuse it.
func TestHandlePutHost_RenameOnly_ConcurrentRecreate_409NotRenamed(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "in-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	fired := false
	m.putHostAfterSnapshot = func() {
		if fired {
			return
		}
		fired = true
		if rr := doHostsRequest(t, m, http.MethodDelete, "/api/peers/hosts/air", nil, adminPrincipal()); rr.Code != http.StatusNoContent {
			t.Errorf("delete status = %d; body=%s", rr.Code, rr.Body.String())
		}
		if rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{"alias": "air", "url": "https://a.example"}, adminPrincipal()); rr.Code != http.StatusCreated {
			t.Errorf("re-add status = %d; body=%s", rr.Code, rr.Body.String())
		}
	}

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": "air26"}, adminPrincipal())
	if rr.Code != http.StatusConflict {
		t.Fatalf("PUT status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air26") != -1 || reloaded.Peers.FindPeerHostByAlias("air") == -1 {
		t.Errorf("rename landed on the re-created entry: %+v", reloaded.Peers.Hosts)
	}
}

// ---- DELETE /api/peers/hosts/{alias} ----

func TestHandleDeleteHost_204ThenGoneFromList(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "inbound-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

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
	m := newHostsTestModule(t, c, nil)

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

func adminTokenFn(c *core.Core) func() string {
	return func() string {
		c.CfgMu.RLock()
		defer c.CfgMu.RUnlock()
		return c.Cfg.Token
	}
}

// peerAuthChain is the real PeerAuth over m's core, wired the way
// production is (cmd/pdx/http_chain.go + Init): Init installs
// m.noteInboundFP as the core's HostAuthObserver, and the production
// HostMatcher calls it under CfgMu.RLock.
func peerAuthChain(c *core.Core, m *Module, next http.Handler) http.Handler {
	c.HostAuthObserver = m.noteInboundFP
	return middleware.PeerAuth(adminTokenFn(c), HostMatcher(c), HostRoutePolicy)(next)
}

// buildOuterHandler mirrors cmd/pdx/http_chain.go's newOuterHandler at a
// scope sufficient for these tests: PeerAuth on /api/peers (+ subtree),
// TokenAuth on everything else. CORS/IPWhitelist/PairingGuard are omitted
// since none of these tests exercise them.
func buildOuterHandler(c *core.Core, mux http.Handler) http.Handler {
	tokenFn := adminTokenFn(c)
	peerChain := middleware.PeerAuth(tokenFn, HostMatcher(c), HostRoutePolicy)(mux)
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
	m := newHostsTestModule(t, c, nil)
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
	moduleA := newHostsTestModule(t, coreA, nil)
	moduleA.registryDir = t.TempDir()
	moduleA.liveness = allLiveLiveness(time.Now())
	muxA := http.NewServeMux()
	moduleA.RegisterRoutes(muxA)
	serverA := httptest.NewServer(buildOuterHandler(coreA, muxA))
	defer serverA.Close()

	coreB, _ := newHostsTestCore(t, "hostB:1", "b", "", nil)
	moduleB := newHostsTestModule(t, coreB, nil)
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

// --- rename (spec §4.2) ------------------------------------------------------

func TestHandlePutHost_Rename_PersistsAndOldAliasGone(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out", InboundToken: "in-a", AllowBypass: true}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": "air26"}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var row struct {
		Alias           string `json:"alias"`
		HostID          string `json:"host_id"`
		HasToken        bool   `json:"has_token"`
		HasInboundToken bool   `json:"has_inbound_token"`
		AllowBypass     bool   `json:"allow_bypass"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if row.Alias != "air26" || row.HostID != "air:1" || !row.HasToken || !row.HasInboundToken || !row.AllowBypass {
		t.Errorf("row = %+v, want alias renamed and every other field kept", row)
	}

	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") != -1 {
		t.Errorf("old alias still present: %+v", reloaded.Peers.Hosts)
	}
	i := reloaded.Peers.FindPeerHostByAlias("air26")
	if i == -1 {
		t.Fatalf("new alias not persisted: %+v", reloaded.Peers.Hosts)
	}
	h := reloaded.Peers.Hosts[i]
	if h.Token != "out" || h.InboundToken != "in-a" || h.HostID != "air:1" || !h.AllowBypass || h.URL != "https://a.example" {
		t.Errorf("persisted entry = %+v, want only the alias changed", h)
	}

	if rr := doHostsRequest(t, m, http.MethodDelete, "/api/peers/hosts/air", nil, adminPrincipal()); rr.Code != http.StatusNotFound {
		t.Errorf("DELETE old alias status = %d, want 404", rr.Code)
	}
}

func TestHandlePutHost_Rename_CollisionCaseInsensitive_409Unchanged(t *testing.T) {
	hosts := []config.PeerHost{
		{Alias: "air", URL: "https://a.example", InboundToken: "in-a"},
		{Alias: "Mini", URL: "https://m.example", InboundToken: "in-m"},
	}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	before, _ := os.ReadFile(cfgPath)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": "mini"}, adminPrincipal())
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "already used by another host") {
		t.Errorf("body = %s, want the collision message", rr.Body.String())
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Errorf("config changed on a refused rename")
	}
}

// A rename that only changes case is the entry colliding with ITSELF, which
// is not a collision (mutation: make uniqueness exclude only exact matches
// → this 409s).
func TestHandlePutHost_Rename_CaseChangeOfOwnAliasOK(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "in-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": "Air"}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if len(reloaded.Peers.Hosts) != 1 || reloaded.Peers.Hosts[0].Alias != "Air" {
		t.Errorf("persisted = %+v, want exactly one entry spelled Air", reloaded.Peers.Hosts)
	}
}

// TestHandlePutHost_Rename_CaseOnly_AfterConcurrentDeleteShiftsIndex pins
// that the fast-path self-match is decided from one snapshot: with two
// hosts, the first is deleted (via the seam) after the handler's initial
// snapshot, shifting the target from index 1 to 0; a case-only rename of
// the target must still succeed, not 409 against its own new index.
func TestHandlePutHost_Rename_CaseOnly_AfterConcurrentDeleteShiftsIndex(t *testing.T) {
	hosts := []config.PeerHost{
		{Alias: "first", URL: "https://f.example", InboundToken: "in-f"},
		{Alias: "air", URL: "https://a.example", InboundToken: "in-a"},
	}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	fired := false
	m.putHostAfterSnapshot = func() {
		if fired {
			return
		}
		fired = true
		if rr := doHostsRequest(t, m, http.MethodDelete, "/api/peers/hosts/first", nil, adminPrincipal()); rr.Code != http.StatusNoContent {
			t.Errorf("delete status = %d; body=%s", rr.Code, rr.Body.String())
		}
	}

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": "Air"}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if len(reloaded.Peers.Hosts) != 1 || reloaded.Peers.Hosts[0].Alias != "Air" || reloaded.Peers.Hosts[0].InboundToken != "in-a" {
		t.Errorf("persisted = %+v, want exactly the renamed second entry", reloaded.Peers.Hosts)
	}
}

func TestHandlePutHost_Rename_Invalid_400(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "in-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	before, _ := os.ReadFile(cfgPath)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	for _, bad := range []string{"..", "has/slash", "local", "LOCAL", strings.Repeat("a", 65), "esc\x1b[31m"} {
		rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": bad}, adminPrincipal())
		if rr.Code != http.StatusBadRequest {
			t.Errorf("alias %q: status = %d, want 400; body=%s", bad, rr.Code, rr.Body.String())
		}
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Errorf("config changed on a refused rename")
	}
}

// Rename plus token in one PUT: the verify dials the url found under the
// OLD alias and the commit finds the entry by the OLD alias; the final
// write renames it.
func TestHandlePutHost_RenameWithToken_VerifiesThenRenames(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "in-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	var gotURL string
	m := newHostsTestModule(t, c, func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		gotURL = baseURL
		return ipeers.Envelope{HostID: "air:1", OK: true, Peers: []ipeers.PeerRecord{}}, nil
	})

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": "air26", "token": "new-tok"}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if gotURL != "https://a.example" {
		t.Errorf("verify dialled %q, want the entry's url", gotURL)
	}
	reloaded := loadCfg(t, cfgPath)
	i := reloaded.Peers.FindPeerHostByAlias("air26")
	if i == -1 || reloaded.Peers.Hosts[i].Token != "new-tok" || reloaded.Peers.Hosts[i].HostID != "air:1" {
		t.Errorf("persisted = %+v, want renamed entry with the verified token and host_id", reloaded.Peers.Hosts)
	}
}

// The in-closure uniqueness re-check (mutation: remove it → this passes
// the rename through and two entries share a name). A rename+token PUT's
// fake fetch blocks; meanwhile another host is POSTed with the target
// alias; released, the PUT must 409 and persist nothing of its own.
func TestHandlePutHost_Rename_ConcurrentCollision_409(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "in-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	fetch := func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		entered <- struct{}{}
		<-release
		return ipeers.Envelope{HostID: "air:1", OK: true, Peers: []ipeers.PeerRecord{}}, nil
	}
	m := newHostsTestModule(t, c, fetch)

	var putResult *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		putResult = doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": "air26", "token": "new-tok"}, adminPrincipal())
	}()
	<-entered

	addResult := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{"alias": "AIR26", "url": "https://other.example"}, adminPrincipal())
	if addResult.Code != http.StatusCreated {
		t.Fatalf("add status = %d, want 201; body=%s", addResult.Code, addResult.Body.String())
	}

	close(release)
	wg.Wait()

	if putResult.Code != http.StatusConflict {
		t.Fatalf("PUT status = %d, want 409; body=%s", putResult.Code, putResult.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if reloaded.Peers.FindPeerHostByAlias("air") == -1 {
		t.Errorf("original entry lost: %+v", reloaded.Peers.Hosts)
	}
	i := reloaded.Peers.FindPeerHostByAlias("air")
	if i != -1 && reloaded.Peers.Hosts[i].Token != "" {
		t.Errorf("refused PUT persisted its token: %+v", reloaded.Peers.Hosts[i])
	}
}

// An invalid rename must be refused BEFORE the verify dials anyone (spec
// §4.2 mirrors handleAddHost's rule). Mutation M9: drop the fast-path
// ValidateAlias → the fake fetch is called.
func TestHandlePutHost_RenameInvalidWithToken_400NoDial(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": "..", "token": "new-tok"}, adminPrincipal())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// Empty alias in the body means "unchanged", so {allow_bypass:true} alone
// still works exactly as before this phase.
func TestHandlePutHost_EmptyAliasIsUnchanged(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "in-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": "", "allow_bypass": true}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	reloaded := loadCfg(t, cfgPath)
	if i := reloaded.Peers.FindPeerHostByAlias("air"); i == -1 || !reloaded.Peers.Hosts[i].AllowBypass {
		t.Errorf("persisted = %+v, want alias kept and allow_bypass set", reloaded.Peers.Hosts)
	}
}

// TestHandleAddHost_SelfAliasChangedDuringVerify_409 pins codex F2 on the
// self-alias spec (#1196): handleAddHost validates the alias against a
// pre-lock snapshot of the local alias, then dials the peer to verify,
// then commits. A self-alias change landing in that window (here: the
// fake peer's verify handler renames this host to the very alias being
// added, exactly what a concurrent PUT /api/peers/settings {alias} does)
// must be caught by the commit closure: 409, no entry written. Without the
// under-lock re-check the entry would go in and <alias>/<name> would be
// ambiguous on this host.
func TestHandleAddHost_SelfAliasChangedDuringVerify_409(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The add path is between its pre-lock snapshot and its commit;
		// no config lock is held while it waits on us.
		if err := c.UpdateConfig(func(cfg *config.Config) error {
			cfg.Peers.Alias = "air"
			return nil
		}); err != nil {
			t.Errorf("concurrent self-alias change: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"host_id":"air:1","ok":true,"partial":false,"peers":[]}`))
	}))
	defer srv.Close()

	m := newHostsTestModule(t, c, nil) // production fetchRemote against the real server

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{
		"alias": "air",
		"url":   srv.URL,
		"token": "secret-tok",
	}, adminPrincipal())

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "local alias") {
		t.Errorf("body = %s, want the local-alias collision text", rr.Body.String())
	}

	reloaded := loadCfg(t, cfgPath)
	if len(reloaded.Peers.Hosts) != 0 {
		t.Fatalf("nothing should be persisted: %+v", reloaded.Peers.Hosts)
	}
	if reloaded.Peers.Alias != "air" {
		t.Errorf("on-disk Peers.Alias = %q, want air (the concurrent change itself stands)", reloaded.Peers.Alias)
	}
}
