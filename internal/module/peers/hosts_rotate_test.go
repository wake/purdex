package peers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
)

const (
	rotTokCur  = "pdxp_11111111111111111111111111111111"
	rotTokPrev = "pdxp_00000000000000000000000000000000"
)

func pendingHost() config.PeerHost {
	return config.PeerHost{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-a", InboundToken: rotTokCur, InboundTokenPrev: rotTokPrev}
}

// prevPrincipal / curPrincipal are what PeerAuth would build for a dial
// with rotTokPrev / rotTokCur: the fingerprint is what the record binds
// to; UsedPrevToken is kept because PeerAuth still sets it.
func prevPrincipal(alias string) *middleware.Principal {
	p := middleware.Principal{Kind: middleware.PrincipalHost, Alias: alias, HostID: "air:1", UsedPrevToken: true, TokenFingerprint: config.TokenFingerprint(rotTokPrev)}
	return &p
}

func curPrincipal(alias string) *middleware.Principal {
	p := middleware.Principal{Kind: middleware.PrincipalHost, Alias: alias, HostID: "air:1", TokenFingerprint: config.TokenFingerprint(rotTokCur)}
	return &p
}

// principalFor is a host principal that presented tok — for tests whose
// entry does not use the pendingHost() token layout.
func principalFor(alias, tok string) *middleware.Principal {
	p := middleware.Principal{Kind: middleware.PrincipalHost, Alias: alias, HostID: "air:1", TokenFingerprint: config.TokenFingerprint(tok)}
	return &p
}

func listRow(t *testing.T, m *Module, alias string) hostRow {
	t.Helper()
	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/hosts", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var body struct{ Hosts []hostRow }
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, h := range body.Hosts {
		if h.Alias == alias {
			return h
		}
	}
	t.Fatalf("alias %q not in list", alias)
	return hostRow{}
}

// ---- the record (spec §6.2) ----

func TestHostRow_RotationPendingAndLastInboundAuth_NoTokenValues(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	row := listRow(t, m, "air")
	if !row.RotationPending || row.LastInboundAuth != "" {
		t.Fatalf("row = %+v; want rotation_pending=true last_inbound_auth=\"\"", row)
	}
	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/hosts", nil, adminPrincipal())
	assertNoTokenKeys(t, rr.Body.Bytes(), false)
	if s := rr.Body.String(); strings.Contains(s, rotTokCur) || strings.Contains(s, rotTokPrev) || strings.Contains(s, "inbound_token_prev") {
		t.Fatalf("list leaks a token value or the prev key: %s", s)
	}
}

func TestHandlePeers_HostPrincipalRecordsLastInboundAuth(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	if rr := doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air")); rr.Code != http.StatusOK {
		t.Fatalf("GET /api/peers (current) = %d; body=%s", rr.Code, rr.Body.String())
	}
	if got := listRow(t, m, "air").LastInboundAuth; got != "current" {
		t.Fatalf("after current dial: last_inbound_auth = %q, want current", got)
	}
	if rr := doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, prevPrincipal("air")); rr.Code != http.StatusOK {
		t.Fatalf("GET /api/peers (prev) = %d", rr.Code)
	}
	if got := listRow(t, m, "air").LastInboundAuth; got != "prev" {
		t.Fatalf("after prev dial: last_inbound_auth = %q, want prev (most recent wins)", got)
	}
	// An admin principal is not a peer dial and never touches the record.
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, adminPrincipal())
	if got := listRow(t, m, "air").LastInboundAuth; got != "prev" {
		t.Fatalf("admin GET changed the record to %q", got)
	}
}

// A delivery that is REFUSED (here: host_unverified, the entry's HostID is
// "" so the principal has none) still records the authentication — the
// fact recorded is "this bearer authenticated", true whether or not the
// request is then refused (spec §6.2).
func TestHandleDeliver_RefusedDeliveryStillRecords(t *testing.T) {
	h := pendingHost()
	h.HostID = ""
	c, _ := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{h})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	p := middleware.Principal{Kind: middleware.PrincipalHost, Alias: "air", UsedPrevToken: true, TokenFingerprint: config.TokenFingerprint(rotTokPrev)}
	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/deliver", map[string]any{}, &p)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("deliver status = %d, want 403 (host_unverified); body=%s", rr.Code, rr.Body.String())
	}
	if got := listRow(t, m, "air").LastInboundAuth; got != "prev" {
		t.Fatalf("refused delivery did not record: last_inbound_auth = %q, want prev", got)
	}
}

// Even a delivery refused because the daemon is stopping (503, the very
// first refusal in handleDeliver) records the authentication.
func TestHandleDeliver_StoppingStillRecords(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	m.stopCancel()
	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/deliver", map[string]any{}, curPrincipal("air"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("deliver while stopping = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if got := listRow(t, m, "air").LastInboundAuth; got != "current" {
		t.Fatalf("stopping refusal did not record: %q", got)
	}
}

// The record is keyed by alias and must follow the entry: a rename moves
// it, a delete clears it (so an entry re-created under the same alias does
// not inherit a stranger's evidence).
func TestInboundAuthRecord_FollowsRenameAndDelete(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air"))

	if rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"alias": "air26"}, adminPrincipal()); rr.Code != http.StatusOK {
		t.Fatalf("rename = %d; body=%s", rr.Code, rr.Body.String())
	}
	if got := listRow(t, m, "air26").LastInboundAuth; got != "current" {
		t.Fatalf("record did not follow the rename: %q", got)
	}
	if rr := doHostsRequest(t, m, http.MethodDelete, "/api/peers/hosts/air26", nil, adminPrincipal()); rr.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rr.Code)
	}
	if rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts", map[string]string{"alias": "air26", "url": "https://b.example"}, adminPrincipal()); rr.Code != http.StatusCreated {
		t.Fatalf("re-add = %d; body=%s", rr.Code, rr.Body.String())
	}
	if got := listRow(t, m, "air26").LastInboundAuth; got != "" {
		t.Fatalf("re-created entry inherited a record: %q", got)
	}

	// FindPeerHostByAlias is case-insensitive; the record is keyed by the
	// entry's STORED alias, so a request spelled differently must still
	// move (and clear) the right record.
	c, _ = newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m = newHostsTestModule(t, c, failIfCalledFetch(t))
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air"))
	if rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/AIR", map[string]any{"alias": "air26"}, adminPrincipal()); rr.Code != http.StatusOK {
		t.Fatalf("rename via upper-case path = %d; body=%s", rr.Code, rr.Body.String())
	}
	if got := listRow(t, m, "air26").LastInboundAuth; got != "current" {
		t.Fatalf("record did not follow a rename addressed as AIR: %q", got)
	}
	if rr := doHostsRequest(t, m, http.MethodDelete, "/api/peers/hosts/AIR26", nil, adminPrincipal()); rr.Code != http.StatusNoContent {
		t.Fatalf("delete via upper-case path = %d", rr.Code)
	}
	m.rotMu.Lock()
	_, stale := m.lastInbound["air26"]
	m.rotMu.Unlock()
	if stale {
		t.Fatal("delete addressed as AIR26 left the record under air26")
	}
}

// ---- routes (spec §6.3) ----

type rotateBody struct {
	Alias        string `json:"alias"`
	InboundToken string `json:"inbound_token"`
}

func rotate(t *testing.T, m *Module, alias string) (*httptest.ResponseRecorder, rotateBody) {
	t.Helper()
	rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/"+alias+"/rotate", nil, adminPrincipal())
	var b rotateBody
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &b); err != nil {
			t.Fatalf("decode rotate: %v; body=%s", err, rr.Body.String())
		}
	}
	return rr, b
}

func gate(t *testing.T, m *Module, alias, verb string, force bool) *httptest.ResponseRecorder {
	t.Helper()
	var body any
	if force {
		body = map[string]any{"force": true}
	}
	return doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/"+alias+"/rotate/"+verb, body, adminPrincipal())
}

func errorOf(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct{ Error string }
	_ = json.Unmarshal(rr.Body.Bytes(), &e)
	return e.Error
}

func TestRotate_MintsFreshToken_PrevIsOld_RecordReset(t *testing.T) {
	h := config.PeerHost{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-a", InboundToken: rotTokPrev}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "admin-secret", []config.PeerHost{h})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	// A dial on the entry's token seen BEFORE the rotation ("current" then;
	// it would derive "prev" after) must not survive into the new epoch:
	// the row reads "" right after a rotate (spec §6.2).
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, principalFor("air", rotTokPrev))

	rr, b := rotate(t, m, "air")
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate = %d; body=%s", rr.Code, rr.Body.String())
	}
	if b.Alias != "air" || !inboundTokenPattern.MatchString(b.InboundToken) || b.InboundToken == rotTokPrev || b.InboundToken == "admin-secret" {
		t.Fatalf("rotate body = %+v", b)
	}
	got := loadCfg(t, cfgPath).Peers.Hosts[0]
	if got.InboundToken != b.InboundToken || got.InboundTokenPrev != rotTokPrev {
		t.Fatalf("persisted = cur %q prev %q; want cur=new prev=old", got.InboundToken, got.InboundTokenPrev)
	}
	row := listRow(t, m, "air")
	if !row.RotationPending || row.LastInboundAuth != "" {
		t.Fatalf("after rotate row = %+v; want pending, record reset to \"\"", row)
	}
	// While pending, BOTH tokens authenticate via the real matcher.
	if _, prev, ok := loadCfg(t, cfgPath).Peers.MatchInboundToken(rotTokPrev); !ok || !prev {
		t.Fatal("old token no longer authenticates while pending")
	}
	if _, prev, ok := loadCfg(t, cfgPath).Peers.MatchInboundToken(b.InboundToken); !ok || prev {
		t.Fatal("new token does not authenticate as current")
	}
}

func TestRotate_409WhenPending_404Unknown_403Host(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	if rr, _ := rotate(t, m, "air"); rr.Code != http.StatusConflict || errorOf(t, rr) != "rotation already pending" {
		t.Fatalf("rotate while pending = %d %q", rr.Code, errorOf(t, rr))
	}
	if rr, _ := rotate(t, m, "ghost"); rr.Code != http.StatusNotFound {
		t.Fatalf("rotate unknown = %d", rr.Code)
	}
	if rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/rotate", nil, hostPrincipal("air")); rr.Code != http.StatusForbidden {
		t.Fatalf("rotate as host principal = %d, want 403", rr.Code)
	}
	// requireAdmin guards the two gates too (defence in depth under HostRoutePolicy).
	for _, verb := range []string{"commit", "cancel"} {
		if rr := doHostsRequest(t, m, http.MethodPost, "/api/peers/hosts/air/rotate/"+verb, nil, hostPrincipal("air")); rr.Code != http.StatusForbidden {
			t.Fatalf("%s as host principal = %d, want 403", verb, rr.Code)
		}
	}
}

// Commit gate (spec §6.3, §8.3): refused until the peer has been seen on the
// NEW token in this epoch; most recent wins; force bypasses.
func TestRotateCommit_Gate(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	if rr := gate(t, m, "air", "commit", false); rr.Code != http.StatusConflict || errorOf(t, rr) != "rotation unconfirmed" {
		t.Fatalf("commit with no dial = %d %q; want 409 rotation unconfirmed", rr.Code, errorOf(t, rr))
	}
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, prevPrincipal("air"))
	if rr := gate(t, m, "air", "commit", false); rr.Code != http.StatusConflict {
		t.Fatalf("commit after OLD-token dial = %d; want 409", rr.Code)
	}
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air"))
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, prevPrincipal("air"))
	if rr := gate(t, m, "air", "commit", false); rr.Code != http.StatusConflict {
		t.Fatalf("commit after new-then-OLD dial = %d; want 409 (most recent wins, not ever-seen)", rr.Code)
	}
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air"))
	rr := gate(t, m, "air", "commit", false)
	if rr.Code != http.StatusOK {
		t.Fatalf("commit after NEW-token dial = %d; body=%s", rr.Code, rr.Body.String())
	}
	assertNoTokenKeys(t, rr.Body.Bytes(), false)
	got := loadCfg(t, cfgPath).Peers.Hosts[0]
	if got.InboundToken != rotTokCur || got.InboundTokenPrev != "" {
		t.Fatalf("after commit: cur %q prev %q; want cur=new prev=\"\"", got.InboundToken, got.InboundTokenPrev)
	}
	if _, _, ok := loadCfg(t, cfgPath).Peers.MatchInboundToken(rotTokPrev); ok {
		t.Fatal("old token still authenticates after commit")
	}
	// Idempotent: no prev → 200 no-op, so a lost response is safely retried.
	if rr := gate(t, m, "air", "commit", false); rr.Code != http.StatusOK {
		t.Fatalf("commit when not pending = %d; want 200 no-op", rr.Code)
	}
}

func TestRotateCommit_ForceBypassesGate(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	if rr := gate(t, m, "air", "commit", true); rr.Code != http.StatusOK {
		t.Fatalf("forced commit = %d; body=%s", rr.Code, rr.Body.String())
	}
	if got := loadCfg(t, cfgPath).Peers.Hosts[0]; got.InboundTokenPrev != "" {
		t.Fatal("forced commit left prev")
	}
}

// Cancel gate: refused unless the peer was last seen on the OLD token
// (cancelling after it switched to the new one would lock it out).
func TestRotateCancel_Gate(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	if rr := gate(t, m, "air", "cancel", false); rr.Code != http.StatusConflict || errorOf(t, rr) != "rotation unconfirmed" {
		t.Fatalf("cancel with no dial = %d %q; want 409 rotation unconfirmed", rr.Code, errorOf(t, rr))
	}
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air"))
	if rr := gate(t, m, "air", "cancel", false); rr.Code != http.StatusConflict {
		t.Fatalf("cancel after NEW-token dial = %d; want 409", rr.Code)
	}
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, prevPrincipal("air"))
	rr := gate(t, m, "air", "cancel", false)
	if rr.Code != http.StatusOK {
		t.Fatalf("cancel after OLD-token dial = %d; body=%s", rr.Code, rr.Body.String())
	}
	assertNoTokenKeys(t, rr.Body.Bytes(), false)
	got := loadCfg(t, cfgPath).Peers.Hosts[0]
	if got.InboundToken != rotTokPrev || got.InboundTokenPrev != "" {
		t.Fatalf("after cancel: cur %q prev %q; want cur=old prev=\"\"", got.InboundToken, got.InboundTokenPrev)
	}
	if _, _, ok := loadCfg(t, cfgPath).Peers.MatchInboundToken(rotTokCur); ok {
		t.Fatal("new token still authenticates after cancel")
	}
	// The peer's token is current again; the record says so (spec §6.2: only "" or "current" after cancel).
	if row := listRow(t, m, "air"); row.RotationPending || row.LastInboundAuth != "current" {
		t.Fatalf("after cancel row = %+v", row)
	}
	if rr := gate(t, m, "air", "cancel", false); rr.Code != http.StatusConflict || errorOf(t, rr) != "no rotation pending" {
		t.Fatalf("cancel when not pending = %d %q; want 409 no rotation pending", rr.Code, errorOf(t, rr))
	}
}

func TestRotateCancel_ForceBypassesGate(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air")) // peer already on the new token
	if rr := gate(t, m, "air", "cancel", true); rr.Code != http.StatusOK {
		t.Fatalf("forced cancel = %d; body=%s", rr.Code, rr.Body.String())
	}
	if got := loadCfg(t, cfgPath).Peers.Hosts[0]; got.InboundToken != rotTokPrev || got.InboundTokenPrev != "" {
		t.Fatalf("forced cancel: %+v", got)
	}
}

// The gate body is exactly one JSON value or nothing: truncated JSON,
// trailing garbage after a valid object, and a second object are all 400
// (codex F1 — a decoder that stops at the first value would silently
// accept `{"force":true} garbage`). `{}` and an empty body still reach
// the gate (409 here: the record is empty).
func TestRotateGates_BadJSON400_Unknown404(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	raw := func(verb, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/peers/hosts/air/rotate/"+verb, strings.NewReader(body))
		req = req.WithContext(middleware.WithPrincipal(context.Background(), *adminPrincipal()))
		mux := http.NewServeMux()
		m.RegisterRoutes(mux)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}
	for _, verb := range []string{"commit", "cancel"} {
		for _, body := range []string{"{", `{"force":true} garbage`, `{"force":true}{"force":true}`} {
			if rr := raw(verb, body); rr.Code != http.StatusBadRequest || errorOf(t, rr) != "invalid json" {
				t.Fatalf("%s body %q = %d %q; want 400 invalid json", verb, body, rr.Code, errorOf(t, rr))
			}
		}
		for _, body := range []string{"{}", ""} {
			if rr := raw(verb, body); rr.Code != http.StatusConflict || errorOf(t, rr) != "rotation unconfirmed" {
				t.Fatalf("%s body %q = %d %q; want 409 rotation unconfirmed (the body was accepted, the gate refused)", verb, body, rr.Code, errorOf(t, rr))
			}
		}
		if rr := gate(t, m, "ghost", verb, false); rr.Code != http.StatusNotFound {
			t.Fatalf("%s unknown = %d", verb, rr.Code)
		}
	}
	// A pending entry whose record is intact still commits on a `{}` body
	// and on an empty body — the strictness is about the body's shape,
	// not about force.
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air"))
	if rr := raw("commit", ""); rr.Code != http.StatusOK {
		t.Fatalf("commit with empty body after a current dial = %d; body=%s", rr.Code, rr.Body.String())
	}
}

// ---- §6.5: rotate racing a PUT ----

// A PUT {token} whose verify is in flight when a rotate lands must 409
// "entry changed concurrently" and leave the rotated tokens untouched.
func TestRotate_DuringBlockedPut_Put409_TokensUntouched(t *testing.T) {
	h := config.PeerHost{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-a", InboundToken: rotTokPrev}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{h})
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	m := newHostsTestModule(t, c, func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		entered <- struct{}{}
		<-release
		return ipeers.Envelope{HostID: "air:1", OK: true, Peers: []ipeers.PeerRecord{}}, nil
	})

	var put *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		put = doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"token": "out-new"}, adminPrincipal())
	}()
	<-entered
	rr, b := rotate(t, m, "air")
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate = %d", rr.Code)
	}
	close(release)
	wg.Wait()

	if put.Code != http.StatusConflict || errorOf(t, put) != "entry changed concurrently" {
		t.Fatalf("PUT = %d %q; want 409 entry changed concurrently", put.Code, errorOf(t, put))
	}
	got := loadCfg(t, cfgPath).Peers.Hosts[0]
	if got.InboundToken != b.InboundToken || got.InboundTokenPrev != rotTokPrev || got.Token != "out-a" {
		t.Fatalf("PUT disturbed the rotated entry: %+v", got)
	}
}

// A PUT STARTED after the rotation snapshots the new token and must succeed.
func TestPut_AfterRotate_Succeeds(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, fixedEnvelopeFetch(ipeers.Envelope{HostID: "air:1", OK: true, Peers: []ipeers.PeerRecord{}}, nil))
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/hosts/air", map[string]any{"token": "out-new"}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT while pending = %d; body=%s", rr.Code, rr.Body.String())
	}
	got := loadCfg(t, cfgPath).Peers.Hosts[0]
	if got.Token != "out-new" || got.InboundToken != rotTokCur || got.InboundTokenPrev != rotTokPrev {
		t.Fatalf("PUT changed rotation state: %+v", got)
	}
}

// ---- PeerAuth end-to-end (spec §8.3): real middleware + real handlers ----

func TestPeerAuth_EndToEnd_PendingRotation_CommitGate(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "admin-secret", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	handler := peerAuthChain(c, m, mux)
	do := func(method, target, bearer string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	if rr := do(http.MethodGet, "/api/peers", rotTokPrev, ""); rr.Code != http.StatusOK {
		t.Fatalf("old token GET /api/peers = %d", rr.Code)
	}
	if got := listRow(t, m, "air").LastInboundAuth; got != "prev" {
		t.Fatalf("old-token dial through real PeerAuth = %q, want \"prev\"", got)
	}
	if rr := do(http.MethodPost, "/api/peers/hosts/air/rotate/commit", "admin-secret", "{}"); rr.Code != http.StatusConflict {
		t.Fatalf("commit after old-token dial = %d; want 409", rr.Code)
	}
	if rr := do(http.MethodGet, "/api/peers", rotTokCur, ""); rr.Code != http.StatusOK {
		t.Fatalf("new token GET /api/peers = %d", rr.Code)
	}
	if rr := do(http.MethodPost, "/api/peers/hosts/air/rotate/commit", "admin-secret", "{}"); rr.Code != http.StatusOK {
		t.Fatalf("commit after new-token dial = %d; body=%s", rr.Code, rr.Body.String())
	}
	if rr := do(http.MethodGet, "/api/peers", rotTokPrev, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("old token after commit = %d; want 401", rr.Code)
	}
	// Host principals can never reach the rotation routes.
	if rr := do(http.MethodPost, "/api/peers/hosts/air/rotate", rotTokCur, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("host principal rotate = %d; want 403 from HostRoutePolicy", rr.Code)
	}
	if got := loadCfg(t, cfgPath).Peers.Hosts[0]; got.InboundTokenPrev != "" {
		t.Fatalf("prev not cleared: %+v", got)
	}
}

// ---- the gate is atomic against the record ----

// A dial that authenticates while a commit is between its gate check and
// its write is FUTURE evidence: the check and the write happen under one
// hold of rotMu, so the dial's note lands only after the write. The commit
// stands on the evidence it checked; the note is not lost — it is stored,
// but it names the token the commit just dropped, so the row derives
// last_inbound_auth "" with no rotation pending: the peer has not been seen
// on any token the entry still has, which is the §6.4 last row (verify
// shows red, rotate again). The seam fires the dial after the check; the
// lock, not timing, orders it.
func TestRotateCommit_DialAfterGateCheckIsNotLost(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air"))

	done := make(chan *httptest.ResponseRecorder, 1)
	fired := false
	m.rotateAfterGate = func() {
		if fired {
			return
		}
		fired = true
		go func() { done <- doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, prevPrincipal("air")) }()
	}
	rr := gate(t, m, "air", "commit", false)
	if rr.Code != http.StatusOK {
		t.Fatalf("commit = %d; body=%s", rr.Code, rr.Body.String())
	}
	if dial := <-done; dial.Code != http.StatusOK {
		t.Fatalf("late dial = %d", dial.Code)
	}
	if got := loadCfg(t, cfgPath).Peers.Hosts[0]; got.InboundTokenPrev != "" {
		t.Fatalf("commit did not clear prev: %+v", got)
	}
	row := listRow(t, m, "air")
	if row.RotationPending || row.LastInboundAuth != "" {
		t.Fatalf("late note misjudged: row = %+v; want not pending, last_inbound_auth \"\" (its token was just dropped)", row)
	}
	// The note itself was stored — it is the DERIVATION that reads "": the
	// dropped token fingerprints to nothing the entry still has.
	m.rotMu.Lock()
	stored, ok := m.lastInbound["air"]
	m.rotMu.Unlock()
	if !ok || stored.fp != config.TokenFingerprint(rotTokPrev) {
		t.Fatalf("late note not stored: %+v ok=%v", stored, ok)
	}
}

// A failed persist must not let cancel's record rewrite (prev → current)
// happen anyway: that would pass the NEXT un-forced commit's gate while the
// peer is still on the OLD token — the very lock-out the gate exists to
// prevent.
func TestRotateCancel_FailedWriteLeavesRecordAndGateIntact(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, prevPrincipal("air")) // peer is on the OLD token

	// Break persistence: a regular file as CfgPath's parent makes
	// config.WriteFile fail with ENOTDIR (same trick as
	// TestPutConfigRollsBackOnWriteFailure in internal/core).
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	c.CfgPath = filepath.Join(blocker, "config.toml")

	if rr := gate(t, m, "air", "cancel", false); rr.Code != http.StatusInternalServerError {
		t.Fatalf("cancel with failed write = %d; want 500; body=%s", rr.Code, rr.Body.String())
	}
	c.CfgMu.RLock()
	entry := c.Cfg.Peers.Hosts[0]
	c.CfgMu.RUnlock()
	if entry.InboundTokenPrev == "" {
		t.Fatal("in-memory config no longer pending despite failed write")
	}
	if got := m.lastInboundAuth(entry); got != "prev" {
		t.Fatalf("record no longer says the peer is on the OLD token despite failed write: last_inbound_auth = %q, want \"prev\"", got)
	}
	if rr := gate(t, m, "air", "commit", false); rr.Code != http.StatusConflict || errorOf(t, rr) != "rotation unconfirmed" {
		t.Fatalf("commit after failed cancel = %d %q; want 409 rotation unconfirmed (peer still on old token)", rr.Code, errorOf(t, rr))
	}
}

// ---- the record is bound to the token, not to the moment (final review C1) ----

// A dial that passed PeerAuth with tX BEFORE an admin rotate (prev := tX,
// current := tX', record reset) but whose HANDLER note lands AFTER the
// reset must read "prev", not "current": the peer only holds tX, and a
// commit on that evidence would lock it out. The matcher's observation
// (under CfgMu.RLock) precedes the rotate's reset and the handler's second
// note follows it; the record stores WHICH token (by fingerprint) and
// derives the state against the entry at read time, so either note reads
// "prev". pause holds the request between PeerAuth and the handler, and
// the rotate lands in between.
func TestRotate_DialAuthenticatedBeforeRotateNotesAfterReset_ReadsPrev(t *testing.T) {
	const tX = rotTokPrev
	h := config.PeerHost{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-a", InboundToken: tX}
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "admin-secret", []config.PeerHost{h})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))

	handler, entered, release := pausedPeerAuthChain(c, m)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- dialThrough(handler, tX) }()
	<-entered
	rr, b := rotate(t, m, "air")
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate = %d; body=%s", rr.Code, rr.Body.String())
	}
	close(release)
	if dial := <-done; dial.Code != http.StatusOK {
		t.Fatalf("pre-rotate dial = %d; body=%s", dial.Code, dial.Body.String())
	}
	if got := loadCfg(t, cfgPath).Peers.Hosts[0]; got.InboundToken != b.InboundToken || got.InboundTokenPrev != tX {
		t.Fatalf("rotated entry = cur %q prev %q", got.InboundToken, got.InboundTokenPrev)
	}

	if got := listRow(t, m, "air").LastInboundAuth; got != "prev" {
		t.Fatalf("note for tX landing after the rotate reads %q, want \"prev\" (tX is now the entry's prev)", got)
	}
	if rr := gate(t, m, "air", "commit", false); rr.Code != http.StatusConflict || errorOf(t, rr) != "rotation unconfirmed" {
		t.Fatalf("commit on a pre-rotate dial = %d %q; want 409 rotation unconfirmed (the peer only holds tX)", rr.Code, errorOf(t, rr))
	}
}

// pausedPeerAuthChain is the real PeerAuth chain (peerAuthChain) with a
// pause handler between PeerAuth and the routes: the FIRST request through
// it closes entered once PeerAuth has matched (and, in production shape,
// observed) its bearer, then blocks until release is closed. Every later
// request passes straight through. Channels, not sleeps, order the race.
func pausedPeerAuthChain(c *core.Core, m *Module) (handler http.Handler, entered <-chan struct{}, release chan<- struct{}) {
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	in := make(chan struct{})
	out := make(chan struct{})
	var once sync.Once
	pause := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(in); <-out })
		mux.ServeHTTP(w, r)
	})
	return peerAuthChain(c, m, pause), in, out
}

func dialThrough(handler http.Handler, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// ---- the observation is atomic with the match (codex F2) ----

// The record says "current" (the peer dialled with the new token). Then a
// request with the OLD token passes PeerAuth — that bearer has
// authenticated — and pauses before its handler. A commit landing now must
// be 409: the peer has just proven it still presents the old token, and
// dropping it would lock the peer out. Before F2 the match and the note
// were two critical sections: the gate saw only the stale "current", the
// commit went through (200), and the paused request then noted a token the
// entry no longer had. The matcher now observes the match INSIDE its
// CfgMu.RLock hold, which the commit's UpdateConfig (CfgMu.Lock) must wait
// for — so the observation is visible to the gate.
func TestRotateCommit_RequestMatchedOnOldTokenBeforeHandler_Is409(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "admin-secret", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air")) // record: "current"
	handler, entered, release := pausedPeerAuthChain(c, m)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- dialThrough(handler, rotTokPrev) }() // matches the OLD token, then pauses
	<-entered

	if rr := gate(t, m, "air", "commit", false); rr.Code != http.StatusConflict || errorOf(t, rr) != "rotation unconfirmed" {
		t.Fatalf("commit while an old-token request has authenticated but not reached its handler = %d %q; want 409 rotation unconfirmed", rr.Code, errorOf(t, rr))
	}
	close(release)
	if dial := <-done; dial.Code != http.StatusOK {
		t.Fatalf("old-token dial = %d; body=%s", dial.Code, dial.Body.String())
	}
	if got := loadCfg(t, cfgPath).Peers.Hosts[0]; got.InboundToken != rotTokCur || got.InboundTokenPrev != rotTokPrev {
		t.Fatalf("rotation state changed: cur %q prev %q", got.InboundToken, got.InboundTokenPrev)
	}
	row := listRow(t, m, "air")
	if !row.RotationPending || row.LastInboundAuth != "prev" {
		t.Fatalf("row = %+v; want pending with last_inbound_auth \"prev\"", row)
	}
}

// Symmetric for cancel: the record says "prev", a NEW-token request passes
// PeerAuth and pauses, and a cancel landing now must be 409 — the peer has
// just presented the new token, and restoring the old one would drop it.
func TestRotateCancel_RequestMatchedOnNewTokenBeforeHandler_Is409(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "local:1", "local", "admin-secret", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, prevPrincipal("air")) // record: "prev"
	handler, entered, release := pausedPeerAuthChain(c, m)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- dialThrough(handler, rotTokCur) }() // matches the NEW token, then pauses
	<-entered

	if rr := gate(t, m, "air", "cancel", false); rr.Code != http.StatusConflict || errorOf(t, rr) != "rotation unconfirmed" {
		t.Fatalf("cancel while a new-token request has authenticated but not reached its handler = %d %q; want 409 rotation unconfirmed", rr.Code, errorOf(t, rr))
	}
	close(release)
	if dial := <-done; dial.Code != http.StatusOK {
		t.Fatalf("new-token dial = %d; body=%s", dial.Code, dial.Body.String())
	}
	if got := loadCfg(t, cfgPath).Peers.Hosts[0]; got.InboundToken != rotTokCur || got.InboundTokenPrev != rotTokPrev {
		t.Fatalf("rotation state changed: cur %q prev %q", got.InboundToken, got.InboundTokenPrev)
	}
	row := listRow(t, m, "air")
	if !row.RotationPending || row.LastInboundAuth != "current" {
		t.Fatalf("row = %+v; want pending with last_inbound_auth \"current\"", row)
	}
}

// Init installs the observer on the core (the way the session module
// installs TmuxAliveFunc), and what it installs is the record's writer.
func TestInit_InstallsHostAuthObserver(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), Peers: config.PeersConfig{Hosts: []config.PeerHost{pendingHost()}}}
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	c.Registry.Register(session.RegistryKey, &fakeSessions{})
	c.Registry.Register(agent.OwnerResolverKey, &fakeOwners{})
	m := New(nil, nil)
	if err := m.Init(c); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if c.HostAuthObserver == nil {
		t.Fatal("Init did not install core.HostAuthObserver")
	}
	c.HostAuthObserver("air", config.TokenFingerprint(rotTokCur))
	if got := m.lastInboundAuth(pendingHost()); got != "current" {
		t.Fatalf("observer note derives %q, want \"current\"", got)
	}
}
