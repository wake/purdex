package peers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
)

const (
	rotTokCur  = "pdxp_11111111111111111111111111111111"
	rotTokPrev = "pdxp_00000000000000000000000000000000"
)

func pendingHost() config.PeerHost {
	return config.PeerHost{Alias: "air", URL: "https://a.example", HostID: "air:1", Token: "out-a", InboundToken: rotTokCur, InboundTokenPrev: rotTokPrev}
}

func prevPrincipal(alias string) *middleware.Principal {
	p := middleware.Principal{Kind: middleware.PrincipalHost, Alias: alias, HostID: "air:1", UsedPrevToken: true}
	return &p
}

func curPrincipal(alias string) *middleware.Principal {
	p := middleware.Principal{Kind: middleware.PrincipalHost, Alias: alias, HostID: "air:1"}
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

	p := middleware.Principal{Kind: middleware.PrincipalHost, Alias: "air", UsedPrevToken: true}
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
	// A "current" seen BEFORE the rotation must not survive into the new epoch.
	doHostsRequest(t, m, http.MethodGet, "/api/peers", nil, curPrincipal("air"))

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

func TestRotateGates_BadJSON400_Unknown404(t *testing.T) {
	c, _ := newHostsTestCore(t, "local:1", "local", "", []config.PeerHost{pendingHost()})
	m := newHostsTestModule(t, c, failIfCalledFetch(t))
	for _, verb := range []string{"commit", "cancel"} {
		req := httptest.NewRequest(http.MethodPost, "/api/peers/hosts/air/rotate/"+verb, strings.NewReader("{"))
		req = req.WithContext(middleware.WithPrincipal(context.Background(), *adminPrincipal()))
		mux := http.NewServeMux()
		m.RegisterRoutes(mux)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s bad json = %d", verb, rr.Code)
		}
		if rr := gate(t, m, "ghost", verb, false); rr.Code != http.StatusNotFound {
			t.Fatalf("%s unknown = %d", verb, rr.Code)
		}
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
	handler := middleware.PeerAuth(
		func() string { c.CfgMu.RLock(); defer c.CfgMu.RUnlock(); return c.Cfg.Token },
		func() config.PeersConfig { c.CfgMu.RLock(); defer c.CfgMu.RUnlock(); return c.Cfg.Peers },
		HostRoutePolicy,
	)(mux)
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
// stands on the evidence it checked; the note is not lost — the row then
// reads last_inbound_auth "prev" with no rotation pending, which is the
// §6.4 last row (verify shows red, rotate again). The seam fires the dial
// after the check; the lock, not timing, orders it.
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
	if row.RotationPending || row.LastInboundAuth != "prev" {
		t.Fatalf("late evidence lost: row = %+v; want not pending, last_inbound_auth prev", row)
	}
}
