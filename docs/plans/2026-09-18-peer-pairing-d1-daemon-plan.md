# Peer Pairing D1 — daemon verify route + alias rename: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the daemon one admin route that verifies a single configured peer entry (`POST /api/peers/hosts/{alias}/verify`), let `PUT /api/peers/hosts/{alias}` rename an entry, and expose both as `pdx peers host verify|rename`.

**Architecture:** The verify route is literally one `scope=all` fan-out row — it calls the existing `fetchHostResult` and drops the peer rows — so the page and `pdx peers --all` can never disagree about a host. Rename is a new optional `alias` field on the existing PUT, validated with `config.ValidateAlias` plus case-insensitive uniqueness, both re-checked inside the `UpdateConfig` closure. `fetchHostResult` gains the one translation it lacks (`ok:false` with empty error text → `"peer reported ok=false"`) so no row anywhere carries a bare `ok:false`.

**Tech Stack:** Go 1.26 (`net/http` 1.22 pattern mux, `modernc.org/sqlite` unused here), `go test -race`, `text/tabwriter` for CLI tables.

**Spec:** `docs/specs/2026-09-18-peer-pairing-ui-spec.md` — §2.2 (facts), §3 D-1/D-5/D-6, §4 (this phase), §8.1 (tests + mutations), §9 D1 (real-machine acceptance).

## Global Constraints

- Run every command from the worktree root: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/phase-d-pairing-ui`. Prefix every Bash call with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/phase-d-pairing-ui && `.
- Commit with `git commit --only <files>` naming each file — the repo root has a version-controlled `pdx` binary that `go build` overwrites; never `git add -A`.
- Tests: `go test -race -count=1 ./internal/module/peers/ ./internal/config/ ./cmd/pdx/`; lint: `go vet ./...`; build: `make build` (writes `bin/pdx`, not the root `pdx`).
- `cmd/pdx` must not import `internal/module/peers` (it would pull daemon code into the CLI); wire shapes are mirrored as `cli*` structs in `cmd/pdx/peers.go`, tags kept in sync by hand.
- Every value that comes from a peer (`self_alias`, `daemon_version`, `error`, `host_id`) is attacker-controlled: bounded with `boundRemoteText` in the daemon, `sanitizeCell` before it reaches a terminal in the CLI, never quoted back into an error message unbounded.
- No route in this phase persists anything except the rename. Verify is read-only (spec D-6).
- Do not touch `#1120`, `HostRoutePolicy` (new routes under `/api/peers/hosts/…` are admin-only by construction — spec §2.2), or the App↔daemon pairing code in `internal/core/pairing_handler.go`.
- Commit messages: one task = one commit; end each with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.

## File map

| file | responsibility in this phase |
|---|---|
| `internal/module/peers/module.go` | `fetchHostResult`: translate `OK:false, Error:""`; `RegisterRoutes`: add the verify route |
| `internal/module/peers/hosts_verify.go` (new) | `verifyHostResponse` + `handleVerifyHost` — the only code for the verify route |
| `internal/module/peers/hosts_verify_test.go` (new) | verify route tests |
| `internal/module/peers/hosts.go` | `putHostRequest.Alias`; rename validation + commit in `handlePutHost` |
| `internal/module/peers/hosts_test.go` | rename tests (appended) |
| `internal/module/peers/module_test.go` | fan-out test for the `ok:false` translation |
| `cmd/pdx/peers.go` | verbs `verify`, `rename`; `--json` allowed for `verify`; usage text |
| `cmd/pdx/peers_test.go` | CLI tests |

---

### Task 1: `fetchHostResult` never yields a bare `ok:false`

**Files:**
- Modify: `internal/module/peers/module.go` (inside `fetchHostResult`, the `rowErr := env.Error` block around line 727)
- Test: `internal/module/peers/module_test.go`

**Interfaces:**
- Produces: `fetchHostResult` returning `HostResult{OK:false, Error:"peer reported ok=false"}` when the envelope has `OK:false` and `Error:""`. Task 2's route inherits it.

- [ ] **Step 1: Write the failing fan-out test**

Append to `internal/module/peers/module_test.go`, right after `TestHandlePeers_ScopeAll_RemoteErrorBounded`:

```go
// TestHandlePeers_ScopeAll_RemoteNotOKWithoutText pins spec §4.1: a peer
// that answers ok=false with NO error text must still produce a row whose
// Error names the cause. Before this, the row copied env.Error verbatim and
// came back as {ok:false, error:""} — and the verify route (hosts_verify.go)
// reuses this function, so the page would have shown a red row with no
// reason. verifyHost already used this exact string for the add/put 502.
func TestHandlePeers_ScopeAll_RemoteNotOKWithoutText(t *testing.T) {
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID: "host-a:111",
			OK:     false,
			Error:  "",
			Peers:  []ipeers.PeerRecord{},
		})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "host-a", URL: srv.URL, Token: "tok-a", HostID: "host-a:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	row := got.Hosts[1]
	if row.OK {
		t.Errorf("row = %+v, want ok=false", row)
	}
	if row.Error != "peer reported ok=false" {
		t.Errorf("row.Error = %q, want %q", row.Error, "peer reported ok=false")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -race -count=1 ./internal/module/peers/ -run TestHandlePeers_ScopeAll_RemoteNotOKWithoutText -v`
Expected: FAIL with `row.Error = "", want "peer reported ok=false"`.

- [ ] **Step 3: Implement the translation**

In `internal/module/peers/module.go`, `fetchHostResult`, replace:

```go
	rowErr := env.Error
	if rowErr != "" {
		rowErr = "peer: " + boundRemoteText(rowErr)
	}
```

with:

```go
	rowErr := env.Error
	if rowErr != "" {
		rowErr = "peer: " + boundRemoteText(rowErr)
	} else if !env.OK {
		// A peer that says ok=false and nothing else still gets a named
		// cause: this row is what the verify route (hosts_verify.go) and
		// the page render, and {ok:false, error:""} would be a red row
		// with no reason. Same string verifyHost uses for the 502 case.
		rowErr = "peer reported ok=false"
	}
```

- [ ] **Step 4: Run the test and the package**

Run: `go test -race -count=1 ./internal/module/peers/`
Expected: PASS (all tests, including the new one).

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/peers/module.go internal/module/peers/module_test.go -m "fix(peers): name the cause when a peer answers ok=false with no error text

fetchHostResult copied env.Error verbatim, so a peer that reported
ok=false and nothing else produced {ok:false, error:\"\"} — a red row
with no reason. Use the string verifyHost already uses for the same
case. Spec: docs/specs/2026-09-18-peer-pairing-ui-spec.md §4.1.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `POST /api/peers/hosts/{alias}/verify`

**Files:**
- Create: `internal/module/peers/hosts_verify.go`
- Create: `internal/module/peers/hosts_verify_test.go`
- Modify: `internal/module/peers/module.go` (`RegisterRoutes`, after the `DELETE /api/peers/hosts/{alias}` line)

**Interfaces:**
- Consumes: `(m *Module) fetchHostResult(ctx, h config.PeerHost) ipeers.HostResult` (module.go), `requireAdmin`, `writeJSONError` (hosts.go), `m.core.CfgMu` / `m.core.Cfg.Peers.FindPeerHostByAlias`.
- Produces: the JSON body below, mirrored by Task 4's `cliVerifyHostResponse`:

```json
{ "alias": "air", "host_id": "wakes-air-2026:oa6drb", "ok": true,
  "self_alias": "air26", "daemon_version": "1.0.0-alpha.376" }
{ "alias": "air", "host_id": "wakes-air-2026:oa6drb", "ok": false, "error": "no outbound token",
  "self_alias": "", "daemon_version": "" }
```

- [ ] **Step 1: Write the failing tests**

Create `internal/module/peers/hosts_verify_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race -count=1 ./internal/module/peers/ -run TestHandleVerifyHost -v`
Expected: every test FAILs with status 404/405 (route not registered) or a compile error if helper names differ — fix helper names against `hosts_test.go`, not the plan, if so.

- [ ] **Step 3: Implement the handler**

Create `internal/module/peers/hosts_verify.go`:

```go
// internal/module/peers/hosts_verify.go
package peers

import (
	"encoding/json"
	"net/http"

	"github.com/wake/purdex/internal/config"
)

// verifyHostResponse is POST /api/peers/hosts/{alias}/verify's 200 body:
// one scope=all fan-out row for this entry, minus its peer rows. Every
// probe outcome is a 200 with ok — "no outbound token", a transport
// error, "peer: <bounded>", "peer reported ok=false", "host_id mismatch:
// got <bounded>", "peer returned an invalid host_id" — the exact strings
// fetchHostResult produces, so this route and `pdx peers --all` can never
// disagree about one host. host_id is the configured value, or the learned
// one when the entry had none. self_alias and daemon_version are the
// peer's own words, bounded for display and NOT validated (v4 spec §7.1).
// Never a token value.
type verifyHostResponse struct {
	Alias         string `json:"alias"`
	HostID        string `json:"host_id"`
	OK            bool   `json:"ok"`
	Error         string `json:"error,omitempty"`
	SelfAlias     string `json:"self_alias"`
	DaemonVersion string `json:"daemon_version"`
}

// handleVerifyHost serves POST /api/peers/hosts/{alias}/verify (spec
// §4.1): admin-only, body ignored, reads the entry under the lock, then
// runs fetchHostResult on the copy OUTSIDE the lock — the same probe one
// scope=all row is built by. It never writes: an entry that lacks a
// host_id has the learned one REPORTED, not stored (spec D-6; that state
// is only reachable by hand-editing the config, and a probe that is
// idempotent and side-effect-free is worth more than closing it).
func (m *Module) handleVerifyHost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")

	m.core.CfgMu.RLock()
	idx := m.core.Cfg.Peers.FindPeerHostByAlias(alias)
	var h config.PeerHost
	if idx != -1 {
		h = m.core.Cfg.Peers.Hosts[idx]
	}
	m.core.CfgMu.RUnlock()

	if idx == -1 {
		writeJSONError(w, http.StatusNotFound, "unknown alias")
		return
	}

	res := m.fetchHostResult(r.Context(), h)
	_ = json.NewEncoder(w).Encode(verifyHostResponse{
		Alias:         res.Alias,
		HostID:        res.HostID,
		OK:            res.OK,
		Error:         res.Error,
		SelfAlias:     res.SelfAlias,
		DaemonVersion: res.DaemonVersion,
	})
}
```

In `internal/module/peers/module.go`, `RegisterRoutes`, add after the DELETE line:

```go
	mux.HandleFunc("POST /api/peers/hosts/{alias}/verify", m.handleVerifyHost)
```

Also add one sentence to the package comment at the top of `module.go` where the hosts routes are listed, e.g. after "management and settings routes": `POST /api/peers/hosts/{alias}/verify (hosts_verify.go) probes one entry the way a scope=all row does;`.

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./internal/module/peers/ -run 'TestHandleVerifyHost|TestHandlePeers_ScopeAll' -v`
Expected: PASS. Then `go test -race -count=1 ./internal/module/peers/` → PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/peers/hosts_verify.go internal/module/peers/hosts_verify_test.go internal/module/peers/module.go -m "feat(peers): POST /api/peers/hosts/{alias}/verify — probe one entry as a scope=all row

Admin-only, read-only. Runs fetchHostResult on the entry and answers with
that row minus its peer rows, so the page and pdx peers --all can never
disagree about a host. Spec: docs/specs/2026-09-18-peer-pairing-ui-spec.md §4.1, D-1, D-6.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: rename via `PUT /api/peers/hosts/{alias}` `{alias}`

**Files:**
- Modify: `internal/module/peers/hosts.go` (`putHostRequest` ~line 70; `handlePutHost` ~lines 390–470)
- Test: `internal/module/peers/hosts_test.go` (append)

**Interfaces:**
- Consumes: `config.ValidateAlias(alias, localAlias string) error`, `PeersConfig.FindPeerHostByAlias(alias) int` (case-insensitive), `apiError{status, msg}`.
- Produces: `putHostRequest.Alias string \`json:"alias"\`` — empty = unchanged; the PUT response `hostRow` carries the new alias. Task 5's CLI sends `{"alias": "<new>"}`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/module/peers/hosts_test.go`:

```go
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
```

`hosts_test.go` does **not** import `os` today — add `"os"` to its import block (the collision/invalid tests read the config file bytes).

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_Rename|TestHandlePutHost_EmptyAliasIsUnchanged' -v`
Expected: the rename tests FAIL (alias unchanged → `new alias not persisted`, collision returns 200, etc.); `EmptyAliasIsUnchanged` already passes (that is fine — it is a regression guard).

- [ ] **Step 3: Implement**

In `internal/module/peers/hosts.go`:

(a) `putHostRequest`:

```go
// putHostRequest is PUT /api/peers/hosts/{alias}'s body. All three fields
// are optional and independent: Token (when non-empty) verifies and stores
// an outbound token; AllowBypass (when non-nil) sets AllowBypass; Alias
// (when non-empty) renames the entry (spec §4.2) — validated with
// config.ValidateAlias and for case-insensitive uniqueness exactly as an
// operator-typed name at POST is, because the value the page sends here is
// the peer's own self-reported alias (v4 spec §7.2: a learned alias must
// clear the same bar).
type putHostRequest struct {
	Token       string `json:"token"`
	AllowBypass *bool  `json:"allow_bypass"`
	Alias       string `json:"alias"`
}
```

(b) In `handlePutHost`, extend the pre-lock snapshot to capture the local alias:

```go
	m.core.CfgMu.RLock()
	idx := m.core.Cfg.Peers.FindPeerHostByAlias(alias)
	var existingURL, existingInboundToken string
	if idx != -1 {
		existingURL = m.core.Cfg.Peers.Hosts[idx].URL
		existingInboundToken = m.core.Cfg.Peers.Hosts[idx].InboundToken
	}
	adminToken := m.core.Cfg.Token
	localAlias := m.core.Cfg.PeerAlias()
	m.core.CfgMu.RUnlock()
```

(c) After the `tokenEqualsAdmin` check and BEFORE the verify (a request this host will refuse must not cost the peer a round trip — same rule as handleAddHost), add the fast-path rename checks:

```go
	// A rename is validated before the verify below dials anyone, for the
	// same reason handleAddHost validates an explicit alias first: a
	// request this host will refuse anyway must not cost a network round
	// trip. Uniqueness excludes the entry itself, so a case-only change
	// ("air" → "Air") is not a collision. Both checks run again under the
	// lock at commit.
	renaming := req.Alias != ""
	if renaming {
		if err := config.ValidateAlias(req.Alias, localAlias); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		m.core.CfgMu.RLock()
		other := m.core.Cfg.Peers.FindPeerHostByAlias(req.Alias)
		m.core.CfgMu.RUnlock()
		if other != -1 && other != idx {
			writeJSONError(w, http.StatusConflict, fmt.Sprintf(
				"alias %q is already used by another host", req.Alias))
			return
		}
	}
```

(d) Inside the `UpdateConfig` closure, **immediately after** the existing `if verifying { … }` identity re-check block's last `return &apiError{…}` (the `cannot pair a host with itself` check) and **before** the two writes `h.Token = req.Token` / `h.HostID = learnedHostID`, add:

```go
		if renaming {
			if err := config.ValidateAlias(req.Alias, cfg.PeerAlias()); err != nil {
				return &apiError{http.StatusBadRequest, err.Error()}
			}
			if other := cfg.Peers.FindPeerHostByAlias(req.Alias); other != -1 && other != i {
				return &apiError{http.StatusConflict, fmt.Sprintf(
					"alias %q is already used by another host", req.Alias)}
			}
		}
```

and make `h.Alias = req.Alias` the **last** write in the closure, immediately before `row = toHostRow(*h)`:

```go
		if renaming {
			h.Alias = req.Alias
		}
		row = toHostRow(*h)
```

This means splitting the existing `if verifying { … }` block into two: the first keeps the three re-checks (`entry changed concurrently`, `host_id mismatch`, `cannot pair a host with itself`) and returns on failure; the rename re-checks go between; a second `if verifying { h.Token = req.Token; h.HostID = learnedHostID }` performs the writes. A rename refusal must return before **any** field is written (spec §4.2 step 3). The closure therefore reads, in this order and no other: find entry → identity re-checks (`verifying`) → rename re-checks (`renaming`) → token/host_id writes (`verifying`) → allow_bypass write → alias write → `row = toHostRow(*h)`.

Update the `handlePutHost` doc comment's first sentence to mention the third optional field: "an optional Alias renames the entry (spec §4.2), validated and uniqueness-checked before the verify and again under the lock; the alias write is the last one in the closure".

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./internal/module/peers/`
Expected: PASS, including every pre-existing `TestHandlePutHost_*`.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/peers/hosts.go internal/module/peers/hosts_test.go -m "feat(peers): rename a configured host via PUT /api/peers/hosts/{alias} {alias}

Optional, independent of token/allow_bypass. Validated with
config.ValidateAlias and case-insensitive uniqueness (excluding the entry
itself) before the verify dials anyone, and again inside the UpdateConfig
closure; the alias write is the closure's last. This is the primitive the
Peers page uses to adopt a peer's self-reported alias — the daemon does not
know it is 'adopting', the value clears the operator bar. Spec §4.2, D-5.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: CLI `pdx peers host verify <alias> [--json]`

**Files:**
- Modify: `cmd/pdx/peers.go` (`peersUsage`, `peersHostVerbArity`, `parsePeersInvocation` host-mode checks, `runPeersHostCmd`, new `cliVerifyHostResponse` + `runPeersHostVerify`)
- Test: `cmd/pdx/peers_test.go` (append)

**Interfaces:**
- Consumes: Task 2's response body; existing `doPeersRequest(method, url, payload, token, timeout) (peersHTTPResult, error)`, `reportPeersTransportErr`, `reportPeersAPIError`, `sanitizeCell`, `writeTestConfig(t, addr, token)`.
- Produces: exit 0 + `ok` line on `ok:true`; exit 1 + `FAILED: <error>` on `ok:false`; the drift line uses the same wording `pdx peers --all` prints (`alias drift: peer calls itself <self>`).

- [ ] **Step 1: Write the failing tests**

Append to `cmd/pdx/peers_test.go`:

```go
// --- host verify (spec §4.4) -------------------------------------------------

func TestRunPeersCmd_HostVerify_OKWithDrift(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"alias": "air", "host_id": "wakes-air-2026:oa6drb", "ok": true,
			"self_alias": "air26", "daemon_version": "1.0.0-alpha.377",
		})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "verify", "air", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/peers/hosts/air/verify" {
		t.Errorf("request = %s %s, want POST /api/peers/hosts/air/verify", gotMethod, gotPath)
	}
	if gotAuth != "Bearer admin-tok" {
		t.Errorf("Authorization = %q, want the admin token", gotAuth)
	}
	out := stdout.String()
	for _, want := range []string{"ok", "wakes-air-2026:oa6drb", "1.0.0-alpha.377", "alias drift: peer calls itself air26"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}
}

func TestRunPeersCmd_HostVerify_NoDriftWhenSameCaseInsensitive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "host_id": "a:1", "ok": true, "self_alias": "AIR", "daemon_version": "x"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"host", "verify", "air", "--config", cfgPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "drift") {
		t.Errorf("stdout = %q, want no drift line for a case-only difference", stdout.String())
	}
}

func TestRunPeersCmd_HostVerify_Failed_Exit1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "host_id": "a:1", "ok": false, "error": "no outbound token", "self_alias": "", "daemon_version": ""})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "verify", "air", "--config", cfgPath}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "FAILED: no outbound token") {
		t.Errorf("stdout = %q, want FAILED line", stdout.String())
	}
}

// The peer's self_alias is attacker-controlled and lands in a terminal:
// sanitizeCell must escape it (mutation: print it raw → the ESC byte reaches stdout).
func TestRunPeersCmd_HostVerify_SelfAliasSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "host_id": "a:1", "ok": true, "self_alias": "evil\x1b[31mred", "daemon_version": "v\x07"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	runPeersCmd([]string{"host", "verify", "air", "--config", cfgPath}, &stdout, &stderr)
	if strings.ContainsAny(stdout.String(), "\x1b\x07") {
		t.Errorf("stdout contains a raw control byte: %q", stdout.String())
	}
}

func TestRunPeersCmd_HostVerify_JSONPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "host_id": "a:1", "ok": true, "self_alias": "air26", "daemon_version": "x"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"host", "verify", "air", "--json", "--config", cfgPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	var v map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
		t.Fatalf("stdout is not JSON: %v; %q", err, stdout.String())
	}
	if v["self_alias"] != "air26" {
		t.Errorf("json self_alias = %v, want air26", v["self_alias"])
	}
}

func TestRunPeersCmd_HostVerify_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown alias"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"host", "verify", "ghost", "--config", cfgPath}, &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown alias") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestParsePeersInvocation_HostVerifyGrammar(t *testing.T) {
	if _, _, ok := parsePeersInvocation([]string{"host", "verify"}); ok {
		t.Error("verify with no alias accepted")
	}
	if _, _, ok := parsePeersInvocation([]string{"host", "verify", "a", "b"}); ok {
		t.Error("verify with two positionals accepted")
	}
	if _, _, ok := parsePeersInvocation([]string{"host", "verify", "a", "--token", "x"}); ok {
		t.Error("verify with --token accepted")
	}
	if _, _, ok := parsePeersInvocation([]string{"host", "list", "--json"}); ok {
		t.Error("--json accepted for a verb other than verify")
	}
	inv, _, ok := parsePeersInvocation([]string{"host", "verify", "a", "--json"})
	if !ok || !inv.jsonOutput || inv.verb != "verify" {
		t.Errorf("verify --json: inv=%+v ok=%v", inv, ok)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race -count=1 ./cmd/pdx/ -run 'HostVerify' -v`
Expected: FAIL — `verify` is an unknown verb (exit 2, usage on stderr).

- [ ] **Step 3: Implement**

In `cmd/pdx/peers.go`:

(a) `peersUsage` — add two lines (rename's is Task 5's but adding both now keeps the constant edited once):

```go
const peersUsage = "usage: pdx peers [--json] [--all] [--config <path>]\n" +
	"       pdx peers host add [<alias>] <url> [--token <t>] [--config <path>]\n" +
	"       pdx peers host set-token <alias> <token> [--allow-bypass=true|false] [--config <path>]\n" +
	"       pdx peers host verify <alias> [--json] [--config <path>]\n" +
	"       pdx peers host rename <alias> <new-alias> [--config <path>]\n" +
	"       pdx peers host remove <alias> [--config <path>]\n" +
	"       pdx peers host list [--config <path>]"
```

(b) `peersHostVerbArity`:

```go
var peersHostVerbArity = map[string][]int{
	"add":       {1, 2},
	"set-token": {2},
	"verify":    {1},
	"rename":    {2},
	"remove":    {1},
	"list":      {0},
}
```

(c) `parsePeersInvocation`, host-mode section: replace

```go
	inv.hostMode = true
	if inv.all || inv.jsonOutput {
		return peersInvocation{}, "", false
	}
```

with

```go
	inv.hostMode = true
	if inv.all {
		return peersInvocation{}, "", false
	}
```

and, after `inv.verb = positionals[1]` and the arity check, add:

```go
	// --json is a query-form flag; among host verbs only verify has a
	// JSON body worth passing through.
	if inv.jsonOutput && inv.verb != "verify" {
		return peersInvocation{}, "", false
	}
```

The existing `default: // remove, list` arm already rejects `--token` / `--allow-bypass` for any other verb; update its comment to `// verify, rename, remove, list`.

(d) `runPeersHostCmd` switch — add:

```go
	case "verify":
		return runPeersHostVerify(cfg, base, inv, stdout, stderr)
	case "rename":
		return runPeersHostRename(cfg, base, inv, stdout, stderr)
```

(`runPeersHostRename` is Task 5; to keep this task compiling, add it in Task 5 and leave the `rename` case out until then — i.e. add only the `verify` case now.)

(e) Wire struct + runner, placed after `cliPutHostRequest`:

```go
// cliVerifyHostResponse mirrors internal/module/peers.verifyHostResponse:
// one scope=all row for one entry, minus its peer rows.
type cliVerifyHostResponse struct {
	Alias         string `json:"alias"`
	HostID        string `json:"host_id"`
	OK            bool   `json:"ok"`
	Error         string `json:"error"`
	SelfAlias     string `json:"self_alias"`
	DaemonVersion string `json:"daemon_version"`
}

// runPeersHostVerify implements `pdx peers host verify <alias> [--json]`:
// POST /api/peers/hosts/{alias}/verify, exit 0 on ok, 1 otherwise. The
// drift line uses the wording `pdx peers --all` prints so the two agree
// word for word; every remote value passes through sanitizeCell because
// it is the peer's own text landing in a terminal.
func runPeersHostVerify(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	alias := inv.positionals[0]

	result, err := doPeersRequest(http.MethodPost, base+"/"+url.PathEscape(alias)+"/verify", nil, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status != http.StatusOK {
		return reportPeersAPIError(result, stderr)
	}

	var resp cliVerifyHostResponse
	if err := json.Unmarshal(result.body, &resp); err != nil {
		fmt.Fprintln(stderr, "pdx peers: invalid response")
		return 1
	}

	if inv.jsonOutput {
		// Re-encoded, not echoed: the daemon's body is trusted-shaped but
		// re-encoding guarantees one JSON document with a trailing newline.
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(stderr, "pdx peers: %v\n", err)
			return 1
		}
		if resp.OK {
			return 0
		}
		return 1
	}

	if !resp.OK {
		fmt.Fprintf(stdout, "%s  FAILED: %s\n", sanitizeCell(resp.Alias), sanitizeCell(resp.Error))
		return 1
	}
	fmt.Fprintf(stdout, "%s  ok  host_id %s  daemon %s\n",
		sanitizeCell(resp.Alias), sanitizeCell(resp.HostID), sanitizeCell(resp.DaemonVersion))
	if resp.SelfAlias != "" {
		fmt.Fprintf(stdout, "  self alias: %s\n", sanitizeCell(resp.SelfAlias))
	}
	if resp.SelfAlias != "" && !strings.EqualFold(resp.SelfAlias, resp.Alias) {
		fmt.Fprintf(stdout, "  alias drift: peer calls itself %s\n", sanitizeCell(resp.SelfAlias))
	}
	return 0
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./cmd/pdx/`
Expected: PASS, including every pre-existing `TestRunPeersCmd_*` and `TestParsePeersInvocation*` (the `--json`-in-host-mode rule moved but did not loosen: `list --json` is still rejected by the new test).

- [ ] **Step 5: Commit**

```bash
git commit --only cmd/pdx/peers.go cmd/pdx/peers_test.go -m "feat(pdx): peers host verify <alias> [--json]

POST /api/peers/hosts/{alias}/verify from the CLI; exit 0 on ok, 1
otherwise; the drift line reuses the wording of pdx peers --all. --json is
now accepted in host mode for this verb only. Spec §4.4.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: CLI `pdx peers host rename <alias> <new-alias>`

**Files:**
- Modify: `cmd/pdx/peers.go` (`cliPutHostRequest`, `runPeersHostCmd`, new `runPeersHostRename`)
- Test: `cmd/pdx/peers_test.go` (append)

**Interfaces:**
- Consumes: Task 3's `{"alias": "<new>"}` on PUT; Task 4's usage/arity entries.
- Produces: `renamed <old> -> <new>` on stdout, exit 0; API errors via `reportPeersAPIError` (exit 1).

- [ ] **Step 1: Write the failing tests**

Append to `cmd/pdx/peers_test.go`:

```go
// --- host rename (spec §4.4) -------------------------------------------------

func TestRunPeersCmd_HostRename(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"alias": "air26", "url": "http://100.64.0.4:7860", "host_id": "a:1",
			"verified": true, "has_token": true, "has_inbound_token": true, "allow_bypass": false,
		})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "rename", "air", "air26", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodPut || gotPath != "/api/peers/hosts/air" {
		t.Errorf("request = %s %s, want PUT /api/peers/hosts/air", gotMethod, gotPath)
	}
	if gotBody["alias"] != "air26" {
		t.Errorf("body = %v, want alias air26", gotBody)
	}
	if _, has := gotBody["token"]; has && gotBody["token"] != "" {
		t.Errorf("body = %v, want no token", gotBody)
	}
	if !strings.Contains(stdout.String(), "renamed air -> air26") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestRunPeersCmd_HostRename_Conflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": `alias "air26" is already used by another host`})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"host", "rename", "air", "air26", "--config", cfgPath}, &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "already used by another host") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunPeersCmd_HostRename_EscapesAlias(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "b"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	runPeersCmd([]string{"host", "rename", "a?b", "b", "--config", cfgPath}, &stdout, &stderr)
	if gotPath != "/api/peers/hosts/a%3Fb" {
		t.Errorf("path = %q, want the alias percent-escaped", gotPath)
	}
}

func TestParsePeersInvocation_HostRenameGrammar(t *testing.T) {
	if _, _, ok := parsePeersInvocation([]string{"host", "rename", "a"}); ok {
		t.Error("rename with one positional accepted")
	}
	if _, _, ok := parsePeersInvocation([]string{"host", "rename", "a", "b", "--json"}); ok {
		t.Error("rename --json accepted")
	}
	if _, _, ok := parsePeersInvocation([]string{"host", "rename", "a", "b", "--allow-bypass=true"}); ok {
		t.Error("rename --allow-bypass accepted")
	}
	inv, _, ok := parsePeersInvocation([]string{"host", "rename", "a", "b"})
	if !ok || inv.verb != "rename" || len(inv.positionals) != 2 {
		t.Errorf("rename a b: inv=%+v ok=%v", inv, ok)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race -count=1 ./cmd/pdx/ -run 'HostRename' -v`
Expected: `TestParsePeersInvocation_HostRenameGrammar` passes (Task 4 added the arity); the three `TestRunPeersCmd_HostRename*` FAIL with exit 2 / usage (no `rename` case in `runPeersHostCmd`).

- [ ] **Step 3: Implement**

In `cmd/pdx/peers.go`:

(a) `cliPutHostRequest`:

```go
// cliPutHostRequest mirrors PUT /api/peers/hosts/{alias}'s body. Alias is
// the rename field (spec §4.2); omitted when empty so set-token bodies
// stay byte-identical to before.
type cliPutHostRequest struct {
	Token       string `json:"token"`
	AllowBypass *bool  `json:"allow_bypass,omitempty"`
	Alias       string `json:"alias,omitempty"`
}
```

(b) `runPeersHostCmd` — add `case "rename": return runPeersHostRename(cfg, base, inv, stdout, stderr)`.

(c) After `runPeersHostSetToken`:

```go
// runPeersHostRename implements `pdx peers host rename <alias> <new-alias>`:
// a PUT with only the alias field. Validation and uniqueness are the
// daemon's; the CLI only carries the words.
func runPeersHostRename(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	oldAlias, newAlias := inv.positionals[0], inv.positionals[1]
	reqBody, err := json.Marshal(cliPutHostRequest{Alias: newAlias})
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}

	result, err := doPeersRequest(http.MethodPut, base+"/"+url.PathEscape(oldAlias), reqBody, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status != http.StatusOK {
		return reportPeersAPIError(result, stderr)
	}

	var row cliHostRow
	if err := json.Unmarshal(result.body, &row); err != nil {
		fmt.Fprintln(stderr, "pdx peers: invalid response")
		return 1
	}

	fmt.Fprintf(stdout, "renamed %s -> %s\n", sanitizeCell(oldAlias), sanitizeCell(row.Alias))
	return 0
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=1 ./cmd/pdx/`
Expected: PASS. Confirm `TestRunPeersCmd_HostSetToken_*` still pass (the `omitempty` on `Alias` keeps their bodies unchanged).

- [ ] **Step 5: Commit**

```bash
git commit --only cmd/pdx/peers.go cmd/pdx/peers_test.go -m "feat(pdx): peers host rename <alias> <new-alias>

Thin wrapper over PUT /api/peers/hosts/{alias} {alias}. Spec §4.4.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: mutation pass, vet, build, deploy-readiness

**Files:**
- Modify (temporarily, then revert with `git checkout --`): `internal/module/peers/module.go`, `internal/module/peers/hosts.go`, `internal/module/peers/hosts_verify.go`, `cmd/pdx/peers.go`
- Create (the only committed file of this task): `docs/plans/2026-09-18-peer-pairing-d1-mutations.md`

**Interfaces:** none — this task proves the tests from Tasks 1–5 guard what they claim (spec §8.1: mutation tests are a deliverable).

- [ ] **Step 1: Run each mutation and record the red**

For each row, apply the mutation with a one-line edit, run the named test, confirm it FAILS, then `git checkout -- <file>` to revert. Record the exact failing assertion line in the note file.

| # | mutation (one edit) | run | must FAIL |
|---|---|---|---|
| M1 | `module.go` `fetchHostResult`: delete the `else if !env.OK { rowErr = "peer reported ok=false" }` branch | `go test -count=1 ./internal/module/peers/ -run 'RemoteNotOKWithoutText'` | both `TestHandlePeers_ScopeAll_RemoteNotOKWithoutText` and `TestHandleVerifyHost_RemoteNotOKWithoutText` |
| M2 | `module.go` `fetchHostResult`: change `if h.Token == ""` to `if false` | `go test -count=1 ./internal/module/peers/ -run 'NoOutboundToken'` | `TestHandleVerifyHost_NoOutboundToken_NoDial` (fatal: fetch should not have been called) |
| M3 | `module.go` `fetchHostResult`: change `if h.HostID != "" && env.HostID != h.HostID` to `if false` | `-run 'TestHandleVerifyHost_HostIDMismatch'` | `TestHandleVerifyHost_HostIDMismatch` |
| M4 | `module.go` `fetchHostResult`: replace `SelfAlias: boundRemoteText(env.Alias)` with `SelfAlias: env.Alias` | `-run 'TestHandleVerifyHost_SelfAliasBounded'` | `TestHandleVerifyHost_SelfAliasBounded` |
| M5 | `hosts_verify.go`: after `res := m.fetchHostResult(...)`, add `if res.OK && h.HostID == "" { _ = m.core.UpdateConfig(func(cfg *config.Config) error { i := cfg.Peers.FindPeerHostByAlias(alias); if i != -1 { cfg.Peers.Hosts[i].HostID = res.HostID }; return nil }) }` | `-run 'TestHandleVerifyHost_NeverWrites'` | `TestHandleVerifyHost_NeverWrites` |
| M6 | `hosts.go` `handlePutHost`: delete the fast-path `if other != -1 && other != idx { … 409 … }` block AND the in-closure `if other := …; other != -1 && other != i { … }` block | `-run 'TestHandlePutHost_Rename_(CollisionCaseInsensitive_409Unchanged|ConcurrentCollision_409)$'` | `TestHandlePutHost_Rename_CollisionCaseInsensitive_409Unchanged` and `TestHandlePutHost_Rename_ConcurrentCollision_409` |
| M7 | `hosts.go` `handlePutHost`: delete only the in-closure uniqueness re-check | `-run 'TestHandlePutHost_Rename_ConcurrentCollision_409'` | `TestHandlePutHost_Rename_ConcurrentCollision_409` |
| M8 | `hosts.go` `handlePutHost`: change both `other != idx` / `other != i` to `true` (exact-only self-exclusion removed) | `-run 'TestHandlePutHost_Rename_CaseChangeOfOwnAliasOK'` | `TestHandlePutHost_Rename_CaseChangeOfOwnAliasOK` |
| M9 | `hosts.go` `handlePutHost`: delete the fast-path `config.ValidateAlias` call (keep the in-closure one) | `-run 'TestHandlePutHost_Rename(_Invalid_400|InvalidWithToken_400NoDial)$'` | `TestHandlePutHost_RenameInvalidWithToken_400NoDial` (fatal: fetch should not have been called). `_Invalid_400` alone still passes — the closure catches the value — which is why the no-dial test exists (Task 3) |
| M10 | `cmd/pdx/peers.go` `runPeersHostVerify`: replace `sanitizeCell(resp.SelfAlias)` with `resp.SelfAlias` in the drift line | `go test -count=1 ./cmd/pdx/ -run 'SelfAliasSanitized'` | `TestRunPeersCmd_HostVerify_SelfAliasSanitized` |
| M11 | `cmd/pdx/peers.go` `runPeersHostVerify`: change `!strings.EqualFold(resp.SelfAlias, resp.Alias)` to `resp.SelfAlias != resp.Alias` | `-run 'NoDriftWhenSameCaseInsensitive'` | `TestRunPeersCmd_HostVerify_NoDriftWhenSameCaseInsensitive` |

- [ ] **Step 2: Confirm the tree is clean**

Run: `git status --short` — expected: empty. Every mutation was reverted; nothing from this task's Step 1 may be committed.

- [ ] **Step 3: Write the mutation record and commit it**

Create `docs/plans/2026-09-18-peer-pairing-d1-mutations.md` with a table: mutation id, edit, command, the failing test name and its assertion text as printed. Every row M1–M11 must show a red.

```bash
git commit --only docs/plans/2026-09-18-peer-pairing-d1-mutations.md -m "docs: D1 mutation-test record

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 4: Full verification**

Run, in order, and paste the tail of each into the PR description:

```bash
gofmt -l internal cmd            # expected: no output
go vet ./...                     # expected: no output
go test -race -count=1 ./...     # expected: ok for every package
make build                       # expected: bin/pdx written; `git status --short` shows NO change to the root `pdx`
bin/pdx peers host verify air    # on mlab: prints `air  ok  host_id wakes-air-2026:oa6drb  daemon 1.0.0-alpha.376` + self alias + drift line, exit 0
```

If `git status --short` shows `M pdx` after `make build`, the Makefile's `BIN` is not `bin/pdx` — stop and report; do not commit the binary.

- [ ] **Step 5: Real-machine acceptance (spec §9 D1) — record, do not skip**

The running mlab daemon is alpha.376 and does not have the route yet; acceptance for the route needs the new binary running. Do **not** restart the production daemon from a subagent. Instead:

1. `bin/pdx peers host verify air` against the *running* 376 daemon → expect `pdx peers: 404 …` / route-not-found on stderr, exit 1 (proves the CLI reaches the daemon and reports a daemon error verbatim).
2. Report to the orchestrator that the daemon restart (`pdx stop && pdx start` from the main repo after `make build` there, per CLAUDE.md) and the live `verify` / `curl -X POST …/api/peers/hosts/air/verify` checks are pending on the human; the orchestrator runs them after merge+deploy and records the output in the PR or the bump PR.

---

## Self-review

**Spec coverage (§4 + §8.1):**
- §4.1 route, 404, every `ok:false` string, no persistence, bounded self_alias/daemon_version, admin-only → Task 2 (+ Task 1 for the `ok=false` string).
- §4.2 rename: validate, uniqueness excluding self, both re-run in closure, alias write last, token+alias together → Task 3.
- §4.3 consequences are documentation (helper names follow-up issue is opened at PR time, §10) — no code.
- §4.4 CLI verify / rename, exit codes, drift wording, sanitizeCell → Tasks 4, 5.
- §8.1 rows: "zero fetch calls" (M2), mismatch (M3), never writes (M5), 404/403 (Task 2), rename 200/409/case-change/400/token+alias/concurrent/no-dial-on-invalid (Task 3, M6–M9), CLI grammar (Tasks 4–5). The one §8.1 row not literally listed as a mutation — "`self_alias`/`daemon_version` bounded" — is M4.
- `--json` for verify was in the spec's CLI grammar; the parser change is the smallest that admits it (Task 4c) and is pinned by `TestParsePeersInvocation_HostVerifyGrammar`.

**Placeholders:** none — every step has the code; Task 6 M5's mutation is spelled out as the exact edit.

**Type consistency:** `verifyHostResponse` (Task 2) ↔ `cliVerifyHostResponse` (Task 4) field-for-field; `putHostRequest.Alias` (Task 3) ↔ `cliPutHostRequest.Alias` (Task 5); `runPeersHostVerify` / `runPeersHostRename` names match between `runPeersHostCmd` and their definitions; `peersHostVerbArity` gains both verbs in Task 4 so Task 5's grammar test is already green when it runs.

**Out of this plan (later phases):** `inbound_token_prev`, `last_inbound_auth`, rotate routes (D3); anything in `spa/` (D2, D4).
