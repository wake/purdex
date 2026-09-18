package peers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
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
