// internal/module/peers/hosts_verify_test.go
package peers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
)

type verifyBody struct {
	Alias         string `json:"alias"`
	HostID        string `json:"host_id"`
	OK            bool   `json:"ok"`
	Error         string `json:"error"`
	SelfAlias     string `json:"self_alias"`
	DaemonVersion string `json:"daemon_version"`
}

func decodeVerify(t *testing.T, body []byte) verifyBody {
	t.Helper()
	var v verifyBody
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("unmarshal verify body: %v; body=%s", err, body)
	}
	return v
}

// TestHandleVerifyHost_OK pins the happy path (spec §4.1): a 200 whose
// ok/self_alias/daemon_version come from the peer's envelope, and whose
// host_id is the CONFIGURED one.
func TestHandleVerifyHost_OK(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-tok", InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{
		HostID: "air:1", Alias: "air26", OK: true, DaemonVersion: "1.0.0-alpha.377", Peers: []ipeers.PeerRecord{},
	}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	got := decodeVerify(t, rr.Body.Bytes())
	want := verifyBody{Alias: "air", HostID: "air:1", OK: true, SelfAlias: "air26", DaemonVersion: "1.0.0-alpha.377"}
	if got != want {
		t.Errorf("body = %+v, want %+v", got, want)
	}
	assertNoTokenKeys(t, rr.Body.Bytes(), false)
	if strings.Contains(rr.Body.String(), "out-tok") || strings.Contains(rr.Body.String(), "in-a") {
		t.Fatalf("body leaks a token value: %s", rr.Body.String())
	}
}

// TestHandleVerifyHost_NoOutboundToken_NoDial pins that an entry without an
// outbound token answers without any network call (mutation: remove the
// Token=="" short-circuit in fetchHostResult → the fake fetch is called).
func TestHandleVerifyHost_NoOutboundToken_NoDial(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	got := decodeVerify(t, rr.Body.Bytes())
	if got.OK || got.Error != "no outbound token" {
		t.Errorf("body = %+v, want ok=false error=%q", got, "no outbound token")
	}
}

// TestHandleVerifyHost_TransportError_200NotOK: a dial failure is a result,
// not a 5xx — the page renders it as a red line with the cause.
func TestHandleVerifyHost_TransportError_200NotOK(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-tok", InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{}, errors.New("dial tcp: connection refused")))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	got := decodeVerify(t, rr.Body.Bytes())
	if got.OK || got.Error != "dial tcp: connection refused" || got.HostID != "air:1" {
		t.Errorf("body = %+v", got)
	}
}

// TestHandleVerifyHost_HostIDMismatch pins the compare-when-configured rule
// (spec §2.2): the envelope's host_id differs from the stored one → ok=false
// (mutation: drop the mismatch branch → ok=true).
func TestHandleVerifyHost_HostIDMismatch(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-tok", InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "someone-else:9", OK: true, Peers: []ipeers.PeerRecord{}}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, adminPrincipal())
	got := decodeVerify(t, rr.Body.Bytes())
	if got.OK || !strings.HasPrefix(got.Error, "host_id mismatch: got ") {
		t.Errorf("body = %+v, want ok=false with a host_id mismatch error", got)
	}
	if got.HostID != "air:1" {
		t.Errorf("host_id = %q, want the configured value", got.HostID)
	}
}

// TestHandleVerifyHost_RemoteNotOKWithoutText: the Task 1 translation, seen
// through the verify route.
func TestHandleVerifyHost_RemoteNotOKWithoutText(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-tok", InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", OK: false, Peers: []ipeers.PeerRecord{}}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, adminPrincipal())
	got := decodeVerify(t, rr.Body.Bytes())
	if got.OK || got.Error != "peer reported ok=false" {
		t.Errorf("body = %+v, want ok=false error=%q", got, "peer reported ok=false")
	}
}

// TestHandleVerifyHost_SelfAliasBounded: self_alias and daemon_version are
// attacker-controlled display values; they are bounded, not validated
// (mutation: drop boundRemoteText → a 5 KB alias comes back whole).
func TestHandleVerifyHost_SelfAliasBounded(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-tok", InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	huge := strings.Repeat("a", 5000)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", Alias: huge, OK: true, DaemonVersion: huge, Peers: []ipeers.PeerRecord{}}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, adminPrincipal())
	got := decodeVerify(t, rr.Body.Bytes())
	if !got.OK {
		t.Fatalf("body = %+v, want ok=true", got)
	}
	if len(got.SelfAlias) > 210 || !strings.HasSuffix(got.SelfAlias, "…") {
		t.Errorf("self_alias length = %d, want bounded with an ellipsis", len(got.SelfAlias))
	}
	if len(got.DaemonVersion) > 210 || !strings.HasSuffix(got.DaemonVersion, "…") {
		t.Errorf("daemon_version length = %d, want bounded with an ellipsis", len(got.DaemonVersion))
	}
}

// TestHandleVerifyHost_NeverWrites pins spec D-6: an ok=true verify on an
// entry with host_id "" (only producible by a hand-edited config) leaves the
// config file byte-for-byte unchanged (mutation: add a "learn host_id"
// write → the file differs).
func TestHandleVerifyHost_NeverWrites(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", Token: "out-tok", InboundToken: "in-a"}}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", hosts)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", Alias: "air26", OK: true, Peers: []ipeers.PeerRecord{}}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, adminPrincipal())
	got := decodeVerify(t, rr.Body.Bytes())
	if !got.OK || got.HostID != "air:1" {
		t.Fatalf("body = %+v, want ok=true with the learned host_id reported (not stored)", got)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("config file changed by a verify:\nbefore=%s\nafter=%s", before, after)
	}
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	if m.core.Cfg.Peers.Hosts[0].HostID != "" {
		t.Errorf("in-memory host_id = %q, want still empty", m.core.Cfg.Peers.Hosts[0].HostID)
	}
}

func TestHandleVerifyHost_UnknownAlias_404(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", nil)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/ghost/verify", nil, adminPrincipal())
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleVerifyHost_AliasCaseInsensitive: FindPeerHostByAlias is
// case-insensitive, so the route is too; the response echoes the CONFIGURED
// spelling.
func TestHandleVerifyHost_AliasCaseInsensitive(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-tok", InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", OK: true, Peers: []ipeers.PeerRecord{}}, nil))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/AIR/verify", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := decodeVerify(t, rr.Body.Bytes()); got.Alias != "air" {
		t.Errorf("alias = %q, want configured spelling %q", got.Alias, "air")
	}
}

func TestHandleVerifyHost_HostPrincipalForbidden(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-tok", InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, hostPrincipal("air"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	rr = doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("no-principal status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// verifyCtxKey is a sentinel planted in the request context so the fake
// fetch can prove the dial runs under the REQUEST's context (a client that
// goes away cancels it) and not under context.Background().
type verifyCtxKey struct{}

// doHostsRequestWithContext is doHostsRequest with a caller-supplied base
// context (doHostsRequest itself always starts from context.Background()).
func doHostsRequestWithContext(t *testing.T, m *Module, method, target string, base context.Context, principal *middleware.Principal) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(method, target, nil)
	ctx := base
	if principal != nil {
		ctx = middleware.WithPrincipal(ctx, *principal)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req.WithContext(ctx))
	return rr
}

// TestHandleVerifyHost_UsesConfiguredURLAndToken: the fetch dials the
// entry's url with its outbound token, under the request's context
// (sentinel present), bounded by remoteFetchTimeout (deadline present).
func TestHandleVerifyHost_UsesConfiguredURLAndToken(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-tok", InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	var gotURL, gotBearer string
	m := newHostsTestModule(t, c, func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		gotURL, gotBearer = baseURL, bearer
		if _, ok := ctx.Deadline(); !ok {
			t.Error("fetch context has no deadline; want remoteFetchTimeout applied")
		}
		if ctx.Value(verifyCtxKey{}) != "request" {
			t.Error("fetch context does not descend from the request context")
		}
		return ipeers.Envelope{HostID: "air:1", OK: true, Peers: []ipeers.PeerRecord{}}, nil
	})

	base := context.WithValue(context.Background(), verifyCtxKey{}, "request")
	doHostsRequestWithContext(t, m, http.MethodPost, "/api/peers/hosts/air/verify", base, adminPrincipal())
	if gotURL != "https://a.example" || gotBearer != "out-tok" {
		t.Errorf("fetch(url=%q, bearer=%q), want the entry's url and outbound token", gotURL, gotBearer)
	}
}

// The transport-error and host_id-mismatch branches carry remote text too.
func TestHandleVerifyHost_ErrorBranchesRedactAndBound(t *testing.T) {
	const tok = "pdxp_deadbeefdeadbeefdeadbeefdeadbeef"
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: tok, InboundToken: "in-a"}}
	long := strings.Repeat("x", 5000)
	for name, fetch := range map[string]fetchFunc{
		"transport": fixedEnvelopeFetch(ipeers.Envelope{}, errors.New("dial "+tok+" "+long)),
		"mismatch":  fixedEnvelopeFetch(ipeers.Envelope{HostID: "other:" + tok + long, OK: true, Peers: []ipeers.PeerRecord{}}, nil),
	} {
		c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
		m := newHostsTestModule(t, c, fetch)
		rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, adminPrincipal())
		body := rr.Body.String()
		if rr.Code != http.StatusOK || strings.Contains(body, tok) || len(body) > 2*maxRemoteTextBytes {
			t.Fatalf("%s: status=%d len=%d leaked=%v", name, rr.Code, len(body), strings.Contains(body, tok))
		}
	}
}

func TestHandleVerifyHost_PeerEchoesOurTokenIsRedacted(t *testing.T) {
	const tok = "pdxp_deadbeefdeadbeefdeadbeefdeadbeef"
	hosts := []config.PeerHost{{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: tok, InboundToken: "in-a"}}
	c, _ := newHostsTestCore(t, "local:1", "local", "", hosts)
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", OK: false, Error: "echo " + tok, Alias: tok}, nil))
	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/verify", nil, adminPrincipal())
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), tok) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
