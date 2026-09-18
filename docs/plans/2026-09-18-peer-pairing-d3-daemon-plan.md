# Peer Pairing D3 — daemon inbound-token rotation: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an inbound peer token be replaced without a window in which the peer is locked out: a transition field (`inbound_token_prev`), a matcher that accepts either token, an in-memory record of which token the peer most recently presented, three admin routes (`rotate` / `rotate/commit` / `rotate/cancel`) whose commit and cancel are gated on that record, and the CLI verbs — plus the daemon-side masking of an echoed outbound token (#1152).

**Architecture:** Config gains one field; `MatchInboundToken` compares both fields with no early exit and reports which one matched; `PeerAuth` copies that onto the `Principal`. The peers module keeps `map[alias]"current"|"prev"` in memory (fail-closed after restart), records it at the two host-principal entry points before any refusal, resets it on `rotate`, and reads it inside the `UpdateConfig` closure for the two gates. `hostRow` gains two never-secret fields; the only response ever carrying the new token is `rotate`'s 200. `#1152` is one helper applied to every remote-derived string in `fetchHostResult`.

**Tech Stack:** Go 1.26 (`net/http` 1.22 pattern mux), `crypto/subtle`, `go test -race`, `text/tabwriter`.

**Spec:** `docs/specs/2026-09-18-peer-pairing-ui-spec.md` — §3 D-7 (three steps, gates on evidence), D-8, §6 (this phase: 6.1 config, 6.2 matcher + record, 6.3 routes, 6.4 flow, 6.5 PUT interaction, 6.6 CLI), §8.3 (tests + mutations), §9 D3 (real-machine acceptance), §12 rows 1 and 6. Issue #1152 (mask an echoed outbound token).

## Global Constraints

- Run every command from the worktree root: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-pairing-d3`. Prefix every Bash call with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-pairing-d3 && `.
- Commit with `git add <files> && git commit --only <files>` naming each file — the repo root has a version-controlled `pdx` binary that `go build` overwrites; never `git add -A`, never `-am`, never expand `$(git diff --name-only)`.
- Tests: `go test -race -count=1 ./internal/config/ ./internal/middleware/ ./internal/module/peers/ ./cmd/pdx/`; vet: `go vet ./...`; build: `make build` (writes `bin/pdx`, not the root `pdx`). `internal/module/agent` has a known flake when run as a whole package (#1092) — it is not in this plan's set.
- `cmd/pdx` must not import `internal/module/peers`; wire shapes are mirrored as `cli*` structs in `cmd/pdx/peers.go`, tags kept in sync by hand.
- **Token values leave the daemon in exactly two responses**: POST 201 (existing) and `rotate` 200 (new). `hostRow`, `Redacted()`, list, put, commit, cancel never carry `token`, `inbound_token` or `inbound_token_prev` values.
- Every value from a peer (`Error`, `Alias`, `DaemonVersion`, `HostID`, `UnknownRegistryFiles`) is attacker-controlled: bounded with `boundRemoteText`, and — from this phase — scrubbed of our own outbound token (`h.Token`) before it leaves `fetchHostResult`.
- `last_inbound_auth` is **in memory only** (spec §6.2): no persistence, no write per request beyond a map store under the module's own mutex. Lock order is `core.CfgMu` → `m.rotMu`, never the reverse; `noteInboundAuth` takes only `rotMu`.
- New routes under `/api/peers/hosts/…` are admin-only by construction (`HostRoutePolicy` admits host principals to `GET /api/peers` scope local and `POST /api/peers/deliver` only); `requireAdmin` is the in-handler defense-in-depth. Do not touch `HostRoutePolicy`, `#1120`, or `internal/core/pairing_handler.go`.
- §6.5: a `rotate` landing between a PUT's snapshot and its commit makes that PUT 409 `entry changed concurrently`. This is correct and stays; a test pins it, and a second test pins that a PUT *started after* the rotation succeeds.
- `force` is honoured by the daemon (`{force:true}` bypasses either gate); the CLI requires `--force` spelled out; the page (D4) never sends it.
- Commit messages: one task = one commit; end each with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.

## Facts the plan relies on (measured at `a26bb948`)

- `config.PeerHost` (`internal/config/config.go:53–60`): `Alias, URL, HostID, Token, InboundToken, AllowBypass`. `MatchInboundToken(bearer) (PeerHost, bool)` (`:112–130`): skips empty `InboundToken`, `subtle.ConstantTimeCompare` on every non-empty one, last match wins, empty bearer never matches. `Redacted()` (`:177–189`) blanks `Token` and `InboundToken` per host. `config.NewPeerToken()` mints `pdxp_` + 32 hex.
- `middleware.Principal{Kind, Alias, HostID}` (`internal/middleware/peer_auth.go:23–27`); `PeerAuth` (`:52–79`) is `MatchInboundToken`'s only caller; mounted in `cmd/pdx/http_chain.go:32` with `peersmod.HostRoutePolicy`. Tests: `internal/middleware/peer_auth_test.go` (helpers `nextRecordingPrincipal`, `twoHosts`, `allowAll`).
- Peers module (`internal/module/peers/module.go`): `Module` struct at `:131`; `RegisterRoutes` at `:289–304`; `handlePeers` at `:310` (host principal allowed only for scope `""`/`local`); `fetchHostResult` at `:648–790` (`boundRemoteText` at `:93`); `putHostAfterSnapshot` test seam at `:183–186`. `hosts.go`: `hostRow`/`toHostRow` at `:34–52`, `addHostResponse` at `:68`, `putHostRequest` at `:84`, `writeJSONError`/`writeAPIError`/`apiError` at `:90–104`, `mintInboundToken(adminToken)` at `:167`, `handlePutHost` at `:405–536` (`toHostRow(*h)` captured inside the closure at `:527`), `handleListHosts` at `:239–254`. `deliver.go`: `handleDeliver` at `:124`; principal read at `:133`; first refusals `:138–148`; `hostLimit.Allow` at `:171`.
- Test fixtures (`internal/module/peers/hosts_test.go`): `newHostsTestCore(t, hostID, alias, adminToken, hosts) (*core.Core, cfgPath)`, `newHostsTestModule(t, c, fetch)`, `adminPrincipal()`, `hostPrincipal(alias)` (HostID `""`), `doHostsRequest(t, m, method, target, body, principal)`, `loadCfg(t, path)`, `assertNoTokenKeys(t, body, allowInboundToken)`, `inboundTokenPattern`, `fixedEnvelopeFetch(env, err)`, `failIfCalledFetch(t)`, and the barrier-fetch pattern at `:1131–1170`. Deliver tests use `newDeliverEnv(t, envOpts{hosts: …})` with `e.m`, `e.hostCtx()`, `e.post(ctx, body)`, `assertRefused`.
- CLI (`cmd/pdx/peers.go`, 1044 lines): `peersUsage` `:70–76`; `peersInvocation` `:110–121`; `peersHostVerbArity` `:127–134`; `parsePeersInvocation` `:151–265` (flags anywhere, `--token`, `--allow-bypass=`, unknown `-x` → `unknownFlag`); `cliHostRow` `:661–669`; `runPeersHostCmd` dispatch `:770–790`; `runPeersHostAdd` prints the token at `:837–839`; `runPeersHostRename` `:872–907` (pattern for a one-request verb); `formatHostsTable` `:925–943`; `reportPeersAPIError`/`extractPeersErrorMessage` `:1023–1044`.

## File map

| file | responsibility in this phase |
|---|---|
| `internal/config/config.go` | `PeerHost.InboundTokenPrev`; `MatchInboundToken` returns `(host, usedPrev, ok)`, compares both fields; `Redacted()` blanks prev |
| `internal/config/peers_test.go` | matcher tests (both tokens, last-match-wins fixture, prev-only, Redacted) |
| `internal/middleware/peer_auth.go` | `Principal.UsedPrevToken`; `PeerAuth` sets it |
| `internal/middleware/peer_auth_test.go` | prev-token principal test |
| `internal/module/peers/rotation.go` (new) | `rotMu`/`lastInbound` accessors: `noteInboundAuth`, `lastInboundAuth`, `resetInboundAuth`, `confirmCancelledRotation`; `hostRowFor` |
| `internal/module/peers/module.go` | struct fields; `handlePeers` notes the principal; `RegisterRoutes` adds three routes; `fetchHostResult` scrubs `h.Token` (#1152) |
| `internal/module/peers/deliver.go` | `handleDeliver` notes the principal before any refusal |
| `internal/module/peers/hosts.go` | `hostRow` two fields; list/put use `m.hostRowFor` |
| `internal/module/peers/hosts_rotate.go` (new) | `handleRotateHost`, `handleRotateCommit`, `handleRotateCancel` |
| `internal/module/peers/hosts_rotate_test.go` (new) | routes, gates, idempotency, §6.5 interleavings, PeerAuth end-to-end, record semantics |
| `internal/module/peers/module_test.go` | #1152 scrub test through `scope=all` and the verify route |
| `cmd/pdx/peers.go`, `cmd/pdx/peers_test.go` | `rotate` verb (`--commit`/`--cancel`/`--force`), `ROTATION` column, cli structs |
| `docs/plans/2026-09-18-peer-pairing-d3-mutations.md` (new) | mutation record (§8.3 deliverable) |

---

### Task 1: config — `InboundTokenPrev`, two-field matcher, `Redacted`

**Files:**
- Modify: `internal/config/config.go` (`PeerHost` `:53`, `MatchInboundToken` `:112`, `Redacted` `:177`)
- Test: `internal/config/peers_test.go` (append)

**Interfaces:**
- Produces: `PeerHost.InboundTokenPrev string` (toml/json `inbound_token_prev`); `func (p PeersConfig) MatchInboundToken(bearer string) (host PeerHost, usedPrev bool, ok bool)`. Task 2 updates the only caller.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/peers_test.go`:

```go
// TestMatchInboundToken_PendingRotation_BothTokensSameAlias pins spec §6.2:
// while a rotation is pending, the old token (prev) and the new one
// (current) both authenticate as the same alias, and usedPrev is true only
// for the old one.
func TestMatchInboundToken_PendingRotation_BothTokensSameAlias(t *testing.T) {
	peers := config.PeersConfig{Hosts: []config.PeerHost{
		{Alias: "air", InboundToken: "pdxp_11111111111111111111111111111111", InboundTokenPrev: "pdxp_00000000000000000000000000000000"},
	}}
	h, usedPrev, ok := peers.MatchInboundToken("pdxp_11111111111111111111111111111111")
	if !ok || h.Alias != "air" || usedPrev {
		t.Fatalf("current: ok=%v alias=%q usedPrev=%v; want ok air false", ok, h.Alias, usedPrev)
	}
	h, usedPrev, ok = peers.MatchInboundToken("pdxp_00000000000000000000000000000000")
	if !ok || h.Alias != "air" || !usedPrev {
		t.Fatalf("prev: ok=%v alias=%q usedPrev=%v; want ok air true", ok, h.Alias, usedPrev)
	}
}

// TestMatchInboundToken_LastMatchWinsAcrossPrev pins "no early exit across
// configured non-empty token fields" without a comparison seam (spec §8.3):
// the bearer equals entry 1's current AND entry 3's prev; the result must
// be entry 3 with usedPrev — an implementation that returns on the first
// match, or that never looks at prev, yields entry 1.
func TestMatchInboundToken_LastMatchWinsAcrossPrev(t *testing.T) {
	const bearer = "pdxp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	peers := config.PeersConfig{Hosts: []config.PeerHost{
		{Alias: "one", InboundToken: bearer},
		{Alias: "two", InboundToken: "pdxp_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", InboundTokenPrev: "pdxp_cccccccccccccccccccccccccccccccc"},
		{Alias: "three", InboundToken: "pdxp_dddddddddddddddddddddddddddddddd", InboundTokenPrev: bearer},
	}}
	h, usedPrev, ok := peers.MatchInboundToken(bearer)
	if !ok || h.Alias != "three" || !usedPrev {
		t.Fatalf("got ok=%v alias=%q usedPrev=%v; want ok three true", ok, h.Alias, usedPrev)
	}
}

// A prev with no current is not a state the API produces, but a hand-edited
// config may hold one; the field is a token, not a flag, so it authenticates.
func TestMatchInboundToken_PrevOnlyStillAuthenticates(t *testing.T) {
	peers := config.PeersConfig{Hosts: []config.PeerHost{
		{Alias: "air", InboundToken: "", InboundTokenPrev: "pdxp_00000000000000000000000000000000"},
	}}
	h, usedPrev, ok := peers.MatchInboundToken("pdxp_00000000000000000000000000000000")
	if !ok || h.Alias != "air" || !usedPrev {
		t.Fatalf("got ok=%v alias=%q usedPrev=%v; want ok air true", ok, h.Alias, usedPrev)
	}
	if _, _, ok := peers.MatchInboundToken(""); ok {
		t.Fatal("empty bearer must never match an empty current")
	}
}

func TestRedacted_BlanksInboundTokenPrev(t *testing.T) {
	cfg := config.Config{Peers: config.PeersConfig{Hosts: []config.PeerHost{
		{Alias: "air", Token: "out", InboundToken: "in", InboundTokenPrev: "in-prev"},
	}}}
	got := cfg.Redacted().Peers.Hosts[0]
	if got.Token != "" || got.InboundToken != "" || got.InboundTokenPrev != "" {
		t.Fatalf("Redacted left a token value: %+v", got)
	}
}
```

Also update the existing `TestMatchInboundToken` (`:310`) to the three-value signature: every `host, ok := peers.MatchInboundToken(...)` becomes `host, _, ok := …` and every `_, ok := …` becomes `_, _, ok := …`. Grep the package: `rg -n "MatchInboundToken\(" internal/config/`.

- [ ] **Step 2: Run to verify they fail**

Run: `go test -count=1 ./internal/config/ -run 'MatchInboundToken|Redacted_BlanksInboundTokenPrev'`
Expected: compile error (three-value return / unknown field).

- [ ] **Step 3: Implement**

In `internal/config/config.go`:

```go
type PeerHost struct {
	Alias        string `toml:"alias"         json:"alias"`
	URL          string `toml:"url"           json:"url"`
	HostID       string `toml:"host_id"       json:"host_id"`       // "" until verified
	Token        string `toml:"token"         json:"token"`         // outbound: what we present to that host
	InboundToken string `toml:"inbound_token" json:"inbound_token"` // what that host must present to us
	// InboundTokenPrev is the outgoing inbound token during a rotation
	// (spec §6): non-empty means a rotation is pending and BOTH values
	// authenticate. Cleared by rotate/commit (drop the old) or
	// rotate/cancel (restore the old as the only one).
	InboundTokenPrev string `toml:"inbound_token_prev" json:"inbound_token_prev"`
	AllowBypass      bool   `toml:"allow_bypass"  json:"allow_bypass"`
}
```

```go
// MatchInboundToken returns the host whose InboundToken OR InboundTokenPrev
// equals bearer, and whether it was the prev one. Every non-empty field of
// every entry is compared with subtle.ConstantTimeCompare and there is no
// early exit; when more than one field matches, the LAST one wins (an
// operator who pasted one token into two entries gets a deterministic
// answer, not a random one). An empty bearer never matches.
func (p PeersConfig) MatchInboundToken(bearer string) (host PeerHost, usedPrev bool, ok bool) {
	if bearer == "" {
		return PeerHost{}, false, false
	}
	matchIdx := -1
	bearerBytes := []byte(bearer)
	for i, h := range p.Hosts {
		if h.InboundToken != "" && subtle.ConstantTimeCompare(bearerBytes, []byte(h.InboundToken)) == 1 {
			matchIdx, usedPrev = i, false
		}
		if h.InboundTokenPrev != "" && subtle.ConstantTimeCompare(bearerBytes, []byte(h.InboundTokenPrev)) == 1 {
			matchIdx, usedPrev = i, true
		}
	}
	if matchIdx == -1 {
		return PeerHost{}, false, false
	}
	return p.Hosts[matchIdx], usedPrev, true
}
```

(Keep whatever the current function returns after the loop — read `:126–130` and preserve it, adding the two extra values.) In `Redacted()` add `out.Peers.Hosts[i].InboundTokenPrev = ""` beside the `InboundToken` line.

- [ ] **Step 4: Run to verify they pass**

Run: `go test -race -count=1 ./internal/config/` — Expected: PASS. `go build ./...` will now fail in `internal/middleware` (caller) — that is Task 2; do not commit a broken build: **do Task 2's Step 3 minimal caller fix (`host, _, matched := …`) in the same commit if needed**, or better, proceed to Task 2 before committing and commit both tasks' files in two commits back to back once the tree builds. Chosen rule: **commit Task 1 and Task 2 together as one commit** (they are one interface change).

---

### Task 2: middleware — `Principal.UsedPrevToken`

**Files:**
- Modify: `internal/middleware/peer_auth.go` (`Principal` `:23`, `PeerAuth` `:70–77`)
- Test: `internal/middleware/peer_auth_test.go` (append)

**Interfaces:**
- Produces: `Principal.UsedPrevToken bool` — true when the bearer matched `InboundTokenPrev`. Task 3 reads it.

- [ ] **Step 1: Write the failing test**

Append to `internal/middleware/peer_auth_test.go`:

```go
// TestPeerAuthPrevTokenSetsUsedPrevToken: during a rotation the old token
// still authenticates as the same host, and the principal says so.
func TestPeerAuthPrevTokenSetsUsedPrevToken(t *testing.T) {
	peersFn := func() config.PeersConfig {
		return config.PeersConfig{Hosts: []config.PeerHost{
			{Alias: "beta", HostID: "host-beta", InboundToken: "tok-new", InboundTokenPrev: "tok-old"},
		}}
	}
	for _, tc := range []struct {
		bearer   string
		wantPrev bool
	}{{"tok-new", false}, {"tok-old", true}} {
		next, called, seen := nextRecordingPrincipal()
		h := middleware.PeerAuth(func() string { return "admin-secret" }, peersFn, allowAll)(next)
		req := httptest.NewRequest("GET", "/api/peers", nil)
		req.Header.Set("Authorization", "Bearer "+tc.bearer)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 || !*called {
			t.Fatalf("%s: want 200 and next called, got %d called=%v", tc.bearer, rec.Code, *called)
		}
		if seen.Kind != middleware.PrincipalHost || seen.Alias != "beta" || seen.UsedPrevToken != tc.wantPrev {
			t.Fatalf("%s: principal = %+v, want host beta UsedPrevToken=%v", tc.bearer, *seen, tc.wantPrev)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test -count=1 ./internal/middleware/ -run PrevToken` → compile error (`UsedPrevToken` unknown).

- [ ] **Step 3: Implement**

```go
type Principal struct {
	Kind   PrincipalKind
	Alias  string // host only
	HostID string // host only; "" when the entry is unverified
	// UsedPrevToken is true when a host principal authenticated with the
	// entry's InboundTokenPrev (a rotation is pending and the peer is still
	// on the old token). The peers module records it per alias (spec §6.2).
	UsedPrevToken bool
}
```

In `PeerAuth`: `if host, usedPrev, matched := peers().MatchInboundToken(bearer); matched { … p := Principal{Kind: PrincipalHost, Alias: host.Alias, HostID: host.HostID, UsedPrevToken: usedPrev} … }`.

- [ ] **Step 4: Run** — `go test -race -count=1 ./internal/config/ ./internal/middleware/ && go build ./...` → PASS, builds.

- [ ] **Step 5: Commit (Tasks 1 + 2)**

```bash
git add internal/config/config.go internal/config/peers_test.go internal/middleware/peer_auth.go internal/middleware/peer_auth_test.go
git commit --only internal/config/config.go internal/config/peers_test.go internal/middleware/peer_auth.go internal/middleware/peer_auth_test.go -m "feat(peers): inbound_token_prev — both tokens authenticate, principal says which (spec §6.1–6.2)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: module — the in-memory record, `hostRow` fields, noting at both entry points

**Files:**
- Create: `internal/module/peers/rotation.go`
- Modify: `internal/module/peers/module.go` (struct `:131`; `handlePeers` `:310`), `internal/module/peers/deliver.go` (`handleDeliver` `:124–133`), `internal/module/peers/hosts.go` (`hostRow` `:34`; list `:249–252`; put `:527`)
- Test: `internal/module/peers/hosts_rotate_test.go` (new; the record tests go here so Task 4 appends the route tests to the same file)

**Interfaces:**
- Produces:

```go
// rotation.go
func (m *Module) noteInboundAuth(p middleware.Principal)            // host principals only; "current"|"prev" by p.UsedPrevToken
func (m *Module) lastInboundAuth(alias string) string               // "" | "current" | "prev"
func (m *Module) resetInboundAuth(alias string)                     // rotate: new epoch
func (m *Module) confirmCancelledRotation(alias string)             // cancel: "prev" → "current" (the old token is current again)
func (m *Module) hostRowFor(h config.PeerHost) hostRow              // toHostRow + RotationPending + LastInboundAuth
// hosts.go
type hostRow struct { …; RotationPending bool `json:"rotation_pending"`; LastInboundAuth string `json:"last_inbound_auth"` }
```

- [ ] **Step 1: Write the failing tests**

Create `internal/module/peers/hosts_rotate_test.go`:

```go
package peers

import (
	"net/http"
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
```

Add `"encoding/json"` and `"strings"` to the imports. (`handleDeliver` with a bare `newHostsTestModule` may refuse earlier than `host_unverified` — e.g. `stopCtx`; whatever the first refusal is, the test only needs `403` **and** the record set. If the module built by `newHostsTestModule` panics on a nil collaborator before reaching the principal switch, build the module with `newDeliverEnv(t, envOpts{hosts: []config.PeerHost{h}})` and use `e.m` / `e.post` instead — read `deliver_test.go:64–150` and pick whichever works; say which in the report.)

- [ ] **Step 2: Run to verify they fail** — `go test -count=1 ./internal/module/peers/ -run 'RotationPending|RecordsLastInboundAuth|RefusedDeliveryStillRecords'` → compile error (`RotationPending`, `LastInboundAuth` unknown).

- [ ] **Step 3: Implement**

Create `internal/module/peers/rotation.go`:

```go
package peers

import (
	"sync"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
)

// Rotation bookkeeping (spec §6.2). lastInbound remembers, per alias, which
// of the entry's two inbound tokens the peer MOST RECENTLY authenticated
// with: "current" or "prev". It is in memory on purpose: after a daemon
// restart it reads "" and both commit and cancel are refused until the peer
// dials again — fail-closed, in the state (both tokens valid) the daemon was
// already in. Persisting it would buy nothing (a stored value is older than
// a fresh dial, and the gates want the freshest) and would cost a write on
// every host-principal request.
//
// Lock order: core.CfgMu → rotMu. The gates read the record inside an
// UpdateConfig closure (CfgMu held) and take rotMu there; noteInboundAuth
// takes rotMu only and never CfgMu, so the two can never deadlock.

const (
	inboundAuthCurrent = "current"
	inboundAuthPrev    = "prev"
)

// noteInboundAuth records which token a host principal presented. Called at
// the two places a host principal is served — handlePeers and handleDeliver
// — BEFORE any policy or rate-limit refusal can return: the fact recorded is
// "this bearer authenticated", which is true whether or not the request is
// then refused. Admin principals are not peer dials and are ignored.
func (m *Module) noteInboundAuth(p middleware.Principal) {
	if p.Kind != middleware.PrincipalHost || p.Alias == "" {
		return
	}
	v := inboundAuthCurrent
	if p.UsedPrevToken {
		v = inboundAuthPrev
	}
	m.rotMu.Lock()
	if m.lastInbound == nil {
		m.lastInbound = map[string]string{}
	}
	m.lastInbound[p.Alias] = v
	m.rotMu.Unlock()
}

// lastInboundAuth is "" | "current" | "prev" for alias in the current
// rotation epoch.
func (m *Module) lastInboundAuth(alias string) string {
	m.rotMu.Lock()
	defer m.rotMu.Unlock()
	return m.lastInbound[alias]
}

// resetInboundAuth starts a new epoch: rotate calls it so a "current" seen
// before this rotation can never satisfy this rotation's commit gate.
func (m *Module) resetInboundAuth(alias string) {
	m.rotMu.Lock()
	delete(m.lastInbound, alias)
	m.rotMu.Unlock()
}

// confirmCancelledRotation keeps the record truthful after a cancel: the
// token the peer was last seen on (prev) is the current one again, so a
// record of "prev" becomes "current". After a cancel there is no prev, so
// the record can only read "" or "current" (spec §6.2).
func (m *Module) confirmCancelledRotation(alias string) {
	m.rotMu.Lock()
	if m.lastInbound[alias] == inboundAuthPrev {
		m.lastInbound[alias] = inboundAuthCurrent
	}
	m.rotMu.Unlock()
}

// hostRowFor is toHostRow plus the two rotation fields. Never a token value.
func (m *Module) hostRowFor(h config.PeerHost) hostRow {
	row := toHostRow(h)
	row.RotationPending = h.InboundTokenPrev != ""
	row.LastInboundAuth = m.lastInboundAuth(h.Alias)
	return row
}
```

In `module.go`'s `Module` struct add (near `titleMu`):

```go
	// Rotation record (rotation.go): alias → "current" | "prev", in memory.
	rotMu       sync.Mutex
	lastInbound map[string]string
```

In `handlePeers`, immediately after `w.Header().Set(...)`:

```go
	// Spec §6.2: a host principal that reached this handler authenticated
	// with one of its entry's two inbound tokens; remember which, before
	// any refusal below.
	if p, ok := middleware.PrincipalFrom(r.Context()); ok {
		m.noteInboundAuth(p)
	}
```

In `handleDeliver`, move the `principal, ok := middleware.PrincipalFrom(r.Context())` line **above** the `stopCtx` check and add right after it:

```go
	if ok {
		m.noteInboundAuth(principal) // before every refusal, including "daemon is stopping" (spec §6.2)
	}
```

(Keep `refuseUnaudited` where it is; it closes over `principal`, which is now declared earlier.)

In `hosts.go`: add to `hostRow`

```go
	// Rotation state (spec §6.1): pending = inbound_token_prev is set;
	// last_inbound_auth = "" | "current" | "prev" — which token the peer
	// most recently presented in this epoch (in memory, rotation.go).
	RotationPending bool   `json:"rotation_pending"`
	LastInboundAuth string `json:"last_inbound_auth"`
```

and change `rows[i] = toHostRow(h)` (list) and `row = toHostRow(*h)` (put) to `m.hostRowFor(...)`. `toHostRow` itself stays (POST's 201 body uses `addHostResponse`, not a row).

- [ ] **Step 4: Run** — `go test -race -count=1 ./internal/module/peers/ -run 'RotationPending|RecordsLastInboundAuth|RefusedDeliveryStillRecords|TestHandleListHosts|TestHandlePutHost'` → PASS; then the whole package `go test -race -count=1 ./internal/module/peers/` → PASS (existing hostRow-shape tests may compare full structs — update expected values to include the two new zero-valued fields where a test constructs a `hostRow{}` literal; do not weaken assertions).

- [ ] **Step 5: Commit**

```bash
git add internal/module/peers/rotation.go internal/module/peers/module.go internal/module/peers/deliver.go internal/module/peers/hosts.go internal/module/peers/hosts_rotate_test.go
git commit --only internal/module/peers/rotation.go internal/module/peers/module.go internal/module/peers/deliver.go internal/module/peers/hosts.go internal/module/peers/hosts_rotate_test.go -m "feat(peers): remember which inbound token a peer last presented; hostRow rotation fields (spec §6.1–6.2)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: routes — `rotate`, `rotate/commit`, `rotate/cancel`

**Files:**
- Create: `internal/module/peers/hosts_rotate.go`
- Modify: `internal/module/peers/module.go` (`RegisterRoutes`)
- Test: `internal/module/peers/hosts_rotate_test.go` (append)

**Interfaces:**
- Produces routes (spec §6.3): `POST /api/peers/hosts/{alias}/rotate` → `200 {alias, inbound_token}` | `409 rotation already pending` | 404; `POST …/rotate/commit` body `{force?}` → `200 hostRow` (idempotent) | `409 rotation unconfirmed` | 404; `POST …/rotate/cancel` body `{force?}` → `200 hostRow` | `409 no rotation pending` | `409 rotation unconfirmed` | 404. Error strings exactly as quoted (the CLI matches on them).

- [ ] **Step 1: Write the failing tests**

Append to `hosts_rotate_test.go`:

```go
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
```

Add imports `"context"`, `"net/http/httptest"`, `"sync"`, and `ipeers "github.com/wake/purdex/internal/peers"`.

- [ ] **Step 2: Run to verify they fail** — `go test -count=1 ./internal/module/peers/ -run 'Rotate|PeerAuth_EndToEnd|Put_AfterRotate'` → 404s from the mux (routes missing).

- [ ] **Step 3: Implement**

Create `internal/module/peers/hosts_rotate.go`:

```go
package peers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wake/purdex/internal/config"
)

// Inbound-token rotation routes (spec §6.3). All three are admin-only by
// construction (HostRoutePolicy) and by requireAdmin; all three mutate
// inside one UpdateConfig closure, re-finding the entry by alias under the
// lock, and the two gates read the in-memory record (rotation.go) inside
// that closure. Neither gate is a proof about the future; each is a proof
// that the operation is not ALREADY known to be a lock-out (D-7).

// rotateResponse is the second and last response that carries a live
// inbound-token value (the first is POST 201).
type rotateResponse struct {
	Alias        string `json:"alias"`
	InboundToken string `json:"inbound_token"`
}

type rotateGateRequest struct {
	Force bool `json:"force"`
}

// decodeGateRequest reads an optional {force} body: empty body = no force,
// invalid JSON = 400.
func decodeGateRequest(r *http.Request) (rotateGateRequest, error) {
	var req rotateGateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		return req, err
	}
	return req, nil
}

// handleRotateHost: prev := current; current := mint(); record := "".
func (m *Module) handleRotateHost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")

	m.core.CfgMu.RLock()
	adminToken := m.core.Cfg.Token
	m.core.CfgMu.RUnlock()
	tok, err := mintInboundToken(adminToken)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var resp rotateResponse
	err = m.core.UpdateConfig(func(cfg *config.Config) error {
		i := cfg.Peers.FindPeerHostByAlias(alias)
		if i == -1 {
			return &apiError{http.StatusNotFound, "unknown alias"}
		}
		h := &cfg.Peers.Hosts[i]
		if h.InboundTokenPrev != "" {
			return &apiError{http.StatusConflict, "rotation already pending"}
		}
		h.InboundTokenPrev = h.InboundToken
		h.InboundToken = tok
		// New epoch: a "current" seen before this rotation must never
		// satisfy this rotation's commit gate.
		m.resetInboundAuth(h.Alias)
		resp = rotateResponse{Alias: h.Alias, InboundToken: tok}
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// handleRotateCommit: prev := "". Idempotent (no prev → 200 no-op); refused
// while the peer has not been seen on the NEW token in this epoch, unless
// force.
func (m *Module) handleRotateCommit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")
	req, err := decodeGateRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}

	var row hostRow
	err = m.core.UpdateConfig(func(cfg *config.Config) error {
		i := cfg.Peers.FindPeerHostByAlias(alias)
		if i == -1 {
			return &apiError{http.StatusNotFound, "unknown alias"}
		}
		h := &cfg.Peers.Hosts[i]
		if h.InboundTokenPrev == "" {
			row = m.hostRowFor(*h)
			return nil
		}
		if !req.Force && m.lastInboundAuth(h.Alias) != inboundAuthCurrent {
			return &apiError{http.StatusConflict, "rotation unconfirmed"}
		}
		h.InboundTokenPrev = ""
		row = m.hostRowFor(*h)
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(row)
}

// handleRotateCancel: current := prev; prev := "". Refused when nothing is
// pending (there is nothing safe to restore) and, unless force, when the
// peer was not last seen on the OLD token (it may already hold the new one).
func (m *Module) handleRotateCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")
	req, err := decodeGateRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}

	var row hostRow
	err = m.core.UpdateConfig(func(cfg *config.Config) error {
		i := cfg.Peers.FindPeerHostByAlias(alias)
		if i == -1 {
			return &apiError{http.StatusNotFound, "unknown alias"}
		}
		h := &cfg.Peers.Hosts[i]
		if h.InboundTokenPrev == "" {
			return &apiError{http.StatusConflict, "no rotation pending"}
		}
		if !req.Force && m.lastInboundAuth(h.Alias) != inboundAuthPrev {
			return &apiError{http.StatusConflict, "rotation unconfirmed"}
		}
		h.InboundToken = h.InboundTokenPrev
		h.InboundTokenPrev = ""
		m.confirmCancelledRotation(h.Alias)
		row = m.hostRowFor(*h)
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(row)
}
```

Check `apiError`'s field names at `hosts.go:95–104` and match them (the plan assumes positional `{status, msg}` as D1 used). In `module.go` `RegisterRoutes` add after the verify route:

```go
	mux.HandleFunc("POST /api/peers/hosts/{alias}/rotate", m.handleRotateHost)
	mux.HandleFunc("POST /api/peers/hosts/{alias}/rotate/commit", m.handleRotateCommit)
	mux.HandleFunc("POST /api/peers/hosts/{alias}/rotate/cancel", m.handleRotateCancel)
```

- [ ] **Step 4: Run** — `go test -race -count=1 ./internal/module/peers/` → PASS (whole package; §6.5's existing PUT tests must still pass unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/module/peers/hosts_rotate.go internal/module/peers/module.go internal/module/peers/hosts_rotate_test.go
git commit --only internal/module/peers/hosts_rotate.go internal/module/peers/module.go internal/module/peers/hosts_rotate_test.go -m "feat(peers): rotate / rotate/commit / rotate/cancel, gated on the peer's most recent token (spec §6.3)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: #1152 — scrub our outbound token from everything a peer says

**Files:**
- Modify: `internal/module/peers/module.go` (`fetchHostResult` `:648–790`; a helper next to `boundRemoteText` `:93`)
- Test: `internal/module/peers/module_test.go` (append) and `internal/module/peers/hosts_verify_test.go` (append)

**Interfaces:**
- Produces: `func redactSecret(s, secret string) string` — `s` with every occurrence of a non-empty `secret` replaced by `[redacted]`; unchanged when `secret == ""`.

- [ ] **Step 1: Write the failing tests**

Append to `module_test.go` (next to `TestHandlePeers_ScopeAll_RemoteErrorBounded`):

```go
// TestHandlePeers_ScopeAll_PeerEchoesOurTokenIsRedacted (#1152): a peer
// that answers with OUR outbound token inside any of its free-text fields
// must not get that token into a row — the row is what the CLI prints, the
// verify route returns and the Peers page renders.
func TestHandlePeers_ScopeAll_PeerEchoesOurTokenIsRedacted(t *testing.T) {
	const tok = "pdxp_deadbeefdeadbeefdeadbeefdeadbeef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID: "host-a:111", OK: false, Error: "you sent " + tok + " to me",
			Alias: "self-" + tok, DaemonVersion: "v-" + tok,
			UnknownRegistryFiles: []string{"/tmp/" + tok}, Peers: []ipeers.PeerRecord{},
		})
	}))
	defer srv.Close()
	hosts := []config.PeerHost{{Alias: "host-a", URL: srv.URL, Token: tok, HostID: "host-a:111"}}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, &fakeSessions{}, &fakeOwners{owners: map[string]agent.PaneOwner{}}, "", ipeers.DefaultLiveness(), &fakeClock{times: []time.Time{time.Unix(0, 0)}}, 2*time.Second)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers?scope=all", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if s := rr.Body.String(); strings.Contains(s, tok) {
		t.Fatalf("outbound token echoed into the aggregate: %s", s)
	}
	if !strings.Contains(rr.Body.String(), "[redacted]") {
		t.Fatalf("expected a [redacted] marker; body=%s", rr.Body.String())
	}
}
```

(Match this fixture's construction to the existing `TestHandlePeers_ScopeAll_RemoteErrorBounded` — copy its exact `newTestCoreWithHosts`/`newTestModule` call and request helper; the names above are what the D1 plan used.) Append to `hosts_verify_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify they fail** — `go test -count=1 ./internal/module/peers/ -run 'EchoesOurToken'` → FAIL (token present).

- [ ] **Step 3: Implement**

In `module.go`, below `boundRemoteText`:

```go
// redactSecret replaces every occurrence of secret in s with "[redacted]".
// fetchHostResult applies it, with the entry's OWN outbound token, to every
// remote-derived string: a malicious peer receives that token as our Bearer
// and could otherwise echo it back into a row that the CLI prints, the
// verify route returns and the Peers page renders (#1152). The peer
// already holds the token, so this hides nothing from it; it keeps our own
// admin surfaces from displaying a value the API never returns.
func redactSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}
```

In `fetchHostResult`, right after the `h.Token == ""` early return, define `bound := func(s string) string { return boundRemoteText(redactSecret(s, h.Token)) }` and use `bound(...)` instead of `boundRemoteText(...)` for `env.HostID` (mismatch message), `rowErr`, each `unknown[i]`, `env.DaemonVersion`, `env.Alias`; and `Error: redactSecret(err.Error(), h.Token)` in the fetch-error branch. `validHostID(env.HostID)` is unchanged (a host_id containing the token is simply invalid).

- [ ] **Step 4: Run** — `go test -race -count=1 ./internal/module/peers/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/module/peers/module.go internal/module/peers/module_test.go internal/module/peers/hosts_verify_test.go
git commit --only internal/module/peers/module.go internal/module/peers/module_test.go internal/module/peers/hosts_verify_test.go -m "fix(peers): scrub our outbound token from everything a peer echoes back (#1152)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: CLI — `pdx peers host rotate`, `ROTATION` column

**Files:**
- Modify: `cmd/pdx/peers.go` (`peersUsage` `:70`; `peersInvocation` `:110`; `peersHostVerbArity` `:127`; `parsePeersInvocation` `:151–265`; `cliHostRow` `:661`; `runPeersHostCmd` `:770`; `formatHostsTable` `:925`; new `runPeersHostRotate` next to `runPeersHostRename`)
- Test: `cmd/pdx/peers_test.go` (append; read its existing fake-daemon pattern for `verify`/`rename` first — `rg -n "func Test.*Rename|httptest.NewServer" cmd/pdx/peers_test.go`)

**Interfaces:**
- Grammar (spec §6.6):
  ```
  pdx peers host rotate <alias> [--config <path>]            # prints the new token like add does
  pdx peers host rotate <alias> --commit [--force] [--config <path>]
  pdx peers host rotate <alias> --cancel [--force] [--config <path>]
  ```
  `--commit`/`--cancel` are mutually exclusive and only valid for `rotate`; `--force` requires one of them; all three are boolean flags. `list` gains a `ROTATION` column: `-` | `pending` | `pending, confirmed` (confirmed = `last_inbound_auth == "current"`).
- Exit codes: 0 on 200; 1 on any non-200 (409 `rotation unconfirmed` gets the on-peer instruction), 2 on grammar errors.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/pdx/peers_test.go` (adapt the fake daemon helper the file already uses for `verify`/`rename` — a `httptest.NewServer` whose handler records the request and returns a canned body, and a config file pointing `Bind`/`Port` at it):

```go
func TestParsePeersInvocation_Rotate(t *testing.T) {
	cases := []struct {
		args   []string
		ok     bool
		commit bool
		cancel bool
		force  bool
	}{
		{[]string{"host", "rotate", "air"}, true, false, false, false},
		{[]string{"host", "rotate", "air", "--commit"}, true, true, false, false},
		{[]string{"host", "rotate", "air", "--cancel", "--force"}, true, false, true, true},
		{[]string{"host", "rotate", "air", "--commit", "--cancel"}, false, false, false, false},
		{[]string{"host", "rotate", "air", "--force"}, false, false, false, false},
		{[]string{"host", "rotate"}, false, false, false, false},
		{[]string{"host", "rotate", "a/b"}, false, false, false, false},
		{[]string{"host", "verify", "air", "--commit"}, false, false, false, false},
		{[]string{"host", "rotate", "air", "--json"}, false, false, false, false},
	}
	for _, tc := range cases {
		inv, _, ok := parsePeersInvocation(tc.args)
		if ok != tc.ok {
			t.Errorf("%v: ok=%v want %v", tc.args, ok, tc.ok)
			continue
		}
		if ok && (inv.rotateCommit != tc.commit || inv.rotateCancel != tc.cancel || inv.rotateForce != tc.force) {
			t.Errorf("%v: parsed %+v", tc.args, inv)
		}
	}
}

func TestRunPeersHostRotate_PrintsNewToken(t *testing.T) {
	var gotPath string
	srv, cfgPath := fakePeersDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"alias":"air","inbound_token":"pdxp_11111111111111111111111111111111"}`)
	})
	defer srv.Close()
	var out, errb bytes.Buffer
	code := runPeersCmd([]string{"host", "rotate", "air", "--config", cfgPath}, &out, &errb)
	if code != 0 || gotPath != "POST /api/peers/hosts/air/rotate" {
		t.Fatalf("code=%d path=%q err=%s", code, gotPath, errb.String())
	}
	if !strings.Contains(out.String(), "pdxp_11111111111111111111111111111111") || !strings.Contains(out.String(), "rotated air") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunPeersHostRotate_Commit_UnconfirmedExplainsOnPeer(t *testing.T) {
	var gotBody string
	srv, cfgPath := fakePeersDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":"rotation unconfirmed"}`)
	})
	defer srv.Close()
	var out, errb bytes.Buffer
	code := runPeersCmd([]string{"host", "rotate", "air", "--commit", "--config", cfgPath}, &out, &errb)
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(errb.String(), "rotation unconfirmed") || !strings.Contains(errb.String(), "pdx peers host verify") || !strings.Contains(errb.String(), "--force") {
		t.Fatalf("stderr = %q", errb.String())
	}
	if strings.Contains(gotBody, `"force":true`) {
		t.Fatalf("commit sent force without --force: %s", gotBody)
	}
}

func TestRunPeersHostRotate_CommitForce_SendsForce(t *testing.T) {
	var gotPath, gotBody string
	srv, cfgPath := fakePeersDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"alias":"air","url":"http://x","host_id":"air:1","verified":true,"has_token":true,"has_inbound_token":true,"allow_bypass":false,"rotation_pending":false,"last_inbound_auth":"current"}`)
	})
	defer srv.Close()
	var out, errb bytes.Buffer
	code := runPeersCmd([]string{"host", "rotate", "air", "--commit", "--force", "--config", cfgPath}, &out, &errb)
	if code != 0 || gotPath != "/api/peers/hosts/air/rotate/commit" || !strings.Contains(gotBody, `"force":true`) {
		t.Fatalf("code=%d path=%q body=%q err=%s", code, gotPath, gotBody, errb.String())
	}
	if !strings.Contains(out.String(), "committed air") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunPeersHostRotate_Cancel(t *testing.T) {
	var gotPath string
	srv, cfgPath := fakePeersDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"alias":"air","url":"http://x","host_id":"air:1","verified":true,"has_token":true,"has_inbound_token":true,"allow_bypass":false,"rotation_pending":false,"last_inbound_auth":"current"}`)
	})
	defer srv.Close()
	var out, errb bytes.Buffer
	if code := runPeersCmd([]string{"host", "rotate", "air", "--cancel", "--config", cfgPath}, &out, &errb); code != 0 || gotPath != "/api/peers/hosts/air/rotate/cancel" {
		t.Fatalf("code=%d path=%q err=%s", code, gotPath, errb.String())
	}
	if !strings.Contains(out.String(), "cancelled rotation for air") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestFormatHostsTable_RotationColumn(t *testing.T) {
	out := formatHostsTable([]cliHostRow{
		{Alias: "a", URL: "http://a", HostID: "a:1"},
		{Alias: "b", URL: "http://b", HostID: "b:1", RotationPending: true},
		{Alias: "c", URL: "http://c", HostID: "c:1", RotationPending: true, LastInboundAuth: "current"},
	})
	if !strings.Contains(out, "ROTATION") {
		t.Fatalf("no ROTATION header: %s", out)
	}
	for _, want := range []string{"pending, confirmed", "\tpending\t", "\t-\t"} {
		_ = want
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.HasSuffix(strings.TrimSpace(lines[1]), "-") || !strings.HasSuffix(strings.TrimSpace(lines[2]), "pending") || !strings.HasSuffix(strings.TrimSpace(lines[3]), "pending, confirmed") {
		t.Fatalf("table:\n%s", out)
	}
}
```

If the file has no `fakePeersDaemon(t, handler) (*httptest.Server, cfgPath string)` helper, add one modelled on whatever the existing `verify`/`rename` tests do (a server + a temp config with `bind`/`port` from `srv.Listener.Addr()` and `token = "adm"`). Delete the no-op `for … _ = want` loop if you keep the suffix assertions.

- [ ] **Step 2: Run to verify they fail** — `go test -count=1 ./cmd/pdx/ -run 'Rotate|RotationColumn'` → compile errors.

- [ ] **Step 3: Implement**

In `cmd/pdx/peers.go`:

(a) `peersUsage`: add after the `rename` line:
```
	"       pdx peers host rotate <alias> [--commit|--cancel] [--force] [--config <path>]\n" +
```
(b) `peersInvocation`: add `rotateCommit, rotateCancel, rotateForce bool`.
(c) `peersHostVerbArity`: `"rotate": {1},`.
(d) `parsePeersInvocation`: in the flag switch add `case a == "--commit": inv.rotateCommit = true`, `case a == "--cancel": inv.rotateCancel = true`, `case a == "--force": inv.rotateForce = true`. In the query-form branch, reject when any of the three is set (they are host-only). In the host branch's verb switch add:
```go
	case "rotate":
		if inv.hasToken || inv.allowBypass != nil || inv.jsonOutput {
			return peersInvocation{}, "", false
		}
		if inv.rotateCommit && inv.rotateCancel {
			return peersInvocation{}, "", false
		}
		if inv.rotateForce && !inv.rotateCommit && !inv.rotateCancel {
			return peersInvocation{}, "", false
		}
```
and in the `default:` arm (verify, rename, remove, list) also reject `inv.rotateCommit || inv.rotateCancel || inv.rotateForce`. (`add`/`set-token` arms: add the same rejection.)
(e) `cliHostRow`: add `RotationPending bool \`json:"rotation_pending"\`` and `LastInboundAuth string \`json:"last_inbound_auth"\``; add
```go
// cliRotateResponse mirrors internal/module/peers.rotateResponse — with
// POST 201, the only responses that carry a live inbound-token value.
type cliRotateResponse struct {
	Alias        string `json:"alias"`
	InboundToken string `json:"inbound_token"`
}

type cliRotateGateRequest struct {
	Force bool `json:"force,omitempty"`
}
```
(f) `runPeersHostCmd`: `case "rotate": return runPeersHostRotate(cfg, base, inv, stdout, stderr)`.
(g) The verb:

```go
// runPeersHostRotate implements the three forms of `pdx peers host rotate`
// (spec §6.6). The plain form prints the new token the way add does — this
// is the value the PEER must be given (`pdx peers host set-token <us> <tok>`
// over there). --commit and --cancel hit the daemon's gates; a 409
// "rotation unconfirmed" is explained in terms of what to do next, because
// a CLI on this host cannot make the peer dial us (spec §6.4).
func runPeersHostRotate(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	alias := inv.positionals[0]
	target := base + "/" + url.PathEscape(alias) + "/rotate"

	if !inv.rotateCommit && !inv.rotateCancel {
		result, err := doPeersRequest(http.MethodPost, target, nil, cfg.Token, peersRequestTimeout)
		if err != nil {
			return reportPeersTransportErr(err, stderr)
		}
		if result.status != http.StatusOK {
			return reportPeersAPIError(result, stderr)
		}
		var resp cliRotateResponse
		if err := json.Unmarshal(result.body, &resp); err != nil || resp.InboundToken == "" {
			fmt.Fprintln(stderr, "pdx peers: invalid response")
			return 1
		}
		fmt.Fprintf(stdout, "rotated %s: both the old and the new inbound token are accepted until you --commit\n", sanitizeCell(resp.Alias))
		fmt.Fprintf(stdout, "new inbound token for %s to use (pdx peers host set-token <this host> <token> over there):\n", sanitizeCell(resp.Alias))
		fmt.Fprintf(stdout, "  %s\n", resp.InboundToken)
		return 0
	}

	verb := "commit"
	if inv.rotateCancel {
		verb = "cancel"
	}
	reqBody, err := json.Marshal(cliRotateGateRequest{Force: inv.rotateForce})
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}
	result, err := doPeersRequest(http.MethodPost, target+"/"+verb, reqBody, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status == http.StatusConflict && extractPeersErrorMessage(result.body) == "rotation unconfirmed" {
		if verb == "commit" {
			fmt.Fprintf(stderr, "pdx peers: rotation unconfirmed — the peer has not presented the NEW token yet. Give it the token (set-token over there), run `pdx peers host verify <this host's alias>` on the peer, then retry; or --force if the peer is gone for good.\n")
		} else {
			fmt.Fprintf(stderr, "pdx peers: rotation unconfirmed — the peer was not last seen on the OLD token (it may already hold the new one). Run `pdx peers host verify <this host's alias>` on the peer, then retry; or --force if you are sure.\n")
		}
		return 1
	}
	if result.status != http.StatusOK {
		return reportPeersAPIError(result, stderr)
	}
	var row cliHostRow
	if err := json.Unmarshal(result.body, &row); err != nil {
		fmt.Fprintln(stderr, "pdx peers: invalid response")
		return 1
	}
	if verb == "commit" {
		fmt.Fprintf(stdout, "committed %s: the old inbound token is revoked\n", sanitizeCell(row.Alias))
	} else {
		fmt.Fprintf(stdout, "cancelled rotation for %s: the old inbound token is the only one again\n", sanitizeCell(row.Alias))
	}
	return 0
}
```

(h) `formatHostsTable`: header `…\tALLOW_BYPASS\tROTATION`, row `…, yesNo(h.AllowBypass), rotationCell(h)` with

```go
func rotationCell(h cliHostRow) string {
	switch {
	case !h.RotationPending:
		return "-"
	case h.LastInboundAuth == "current":
		return "pending, confirmed"
	default:
		return "pending"
	}
}
```

Update the comment above `formatHostsTable` and any existing table test that pins the header string.

- [ ] **Step 4: Run** — `go test -race -count=1 ./cmd/pdx/ && go vet ./... && make build` → PASS, builds `bin/pdx`. `bin/pdx peers host` (no args) must print the usage including the rotate line.

- [ ] **Step 5: Commit**

```bash
git add cmd/pdx/peers.go cmd/pdx/peers_test.go
git commit --only cmd/pdx/peers.go cmd/pdx/peers_test.go -m "feat(cli): pdx peers host rotate [--commit|--cancel] [--force]; ROTATION column (spec §6.6)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Mutation record (§8.3 deliverable)

**Files:**
- Create: `docs/plans/2026-09-18-peer-pairing-d3-mutations.md` (format of `…-d1-mutations.md`)

Each row: apply the one-edit mutation, run the named test(s) with `go test -count=1 <pkg> -run '<name>'`, record FAIL + the assertion text, revert with `git checkout -- <file>`; `git status --short` empty before writing the record.

| # | mutation (exact edit) | file | must go red |
|---|---|---|---|
| M1 | in `MatchInboundToken`, delete the `InboundTokenPrev` comparison | `config.go` | `PendingRotation_BothTokensSameAlias` (old token 401), `LastMatchWinsAcrossPrev` (entry one), `PrevOnlyStillAuthenticates` |
| M2 | in `MatchInboundToken`, `return p.Hosts[i], usedPrev, true` inside the loop on the first current match (early exit) | `config.go` | `LastMatchWinsAcrossPrev` |
| M3 | in `Redacted`, drop the `InboundTokenPrev = ""` line | `config.go` | `TestRedacted_BlanksInboundTokenPrev` |
| M4 | in `PeerAuth`, `UsedPrevToken: false` | `peer_auth.go` | `TestPeerAuthPrevTokenSetsUsedPrevToken` |
| M5 | in `handleRotateCommit`, delete the gate `if !req.Force && … != inboundAuthCurrent` | `hosts_rotate.go` | `TestRotateCommit_Gate` (first commit is 200), `TestPeerAuth_EndToEnd_PendingRotation_CommitGate` |
| M6 | in `handleRotateCommit`, `h.InboundTokenPrev = ""` deleted (commit that does not clear prev) | `hosts_rotate.go` | `TestRotateCommit_Gate` (old token still authenticates) |
| M7 | in `handleRotateCancel`, delete the `!= inboundAuthPrev` gate | `hosts_rotate.go` | `TestRotateCancel_Gate` (first cancel is 200) |
| M8 | in `handleRotateCancel`, drop `h.InboundToken = h.InboundTokenPrev` (cancel that does not restore) | `hosts_rotate.go` | `TestRotateCancel_Gate` (cur ≠ old) |
| M9 | in `handleRotateCancel`, treat `""` as prev: gate becomes `last != inboundAuthPrev && last != ""` — **the row the memory file says must stay red** | `hosts_rotate.go` | `TestRotateCancel_Gate` (cancel with no dial must be 409) |
| M10 | in `handleRotateCommit`, treat `""` as current: gate becomes `last != inboundAuthCurrent && last != ""` | `hosts_rotate.go` | `TestRotateCommit_Gate` (commit with no dial must be 409) |
| M11 | in `handleRotateHost`, delete `m.resetInboundAuth(h.Alias)` | `hosts_rotate.go` | `TestRotate_MintsFreshToken_PrevIsOld_RecordReset` (record still "current") |
| M12 | in `noteInboundAuth`, make the record sticky: `if _, seen := m.lastInbound[p.Alias]; seen { return }` | `rotation.go` | `TestHandlePeers_HostPrincipalRecordsLastInboundAuth` (prev after current), `TestRotateCommit_Gate` (new-then-old step) |
| M13 | in `handleDeliver`, move `m.noteInboundAuth(principal)` below the principal switch | `deliver.go` | `TestHandleDeliver_RefusedDeliveryStillRecords` |
| M14 | in `hostRowFor`, `RotationPending = false` always | `rotation.go` | `TestHostRow_RotationPendingAndLastInboundAuth_NoTokenValues`, `TestFormatHostsTable_RotationColumn` is CLI-only (not this) |
| M15 | in `handleRotateHost`, skip the `InboundTokenPrev != ""` check | `hosts_rotate.go` | `TestRotate_409WhenPending_404Unknown_403Host` |
| M16 | in `handlePutHost`'s closure, drop `|| h.InboundToken != existingInboundToken` | `hosts.go` | `TestRotate_DuringBlockedPut_Put409_TokensUntouched` (PUT 200, tokens disturbed) |
| M17 | in `fetchHostResult`, `bound := boundRemoteText` (no redact) | `module.go` | both `EchoesOurTokenIsRedacted` tests |
| M18 | in `redactSecret`, return `s` unchanged | `module.go` | same two |
| M19 | in `parsePeersInvocation`, drop the `rotateCommit && rotateCancel` rejection | `cmd/pdx/peers.go` | `TestParsePeersInvocation_Rotate` |
| M20 | in `runPeersHostRotate`, always send `Force: true` | `cmd/pdx/peers.go` | `TestRunPeersHostRotate_Commit_UnconfirmedExplainsOnPeer` |
| M21 | in `rotationCell`, return `"pending"` for confirmed too | `cmd/pdx/peers.go` | `TestFormatHostsTable_RotationColumn` |
| M22 | in `confirmCancelledRotation`, do nothing | `rotation.go` | `TestRotateCancel_Gate` (row after cancel reads "prev") |

- [ ] **Step 1: Run every row, record, revert**
- [ ] **Step 2: `git status --short` shows only the record; commit**

```bash
git add docs/plans/2026-09-18-peer-pairing-d3-mutations.md
git commit --only docs/plans/2026-09-18-peer-pairing-d3-mutations.md -m "docs(plan): D3 mutation-test record (M1–M22 all red)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## After the tasks (main session)

1. `git push -u origin worktree-peer-pairing-d3`; PR `feat(peers): Peer Pairing D3 — inbound-token rotation with a gated commit/cancel (+ #1152)`. Diff is daemon + CLI; if it exceeds ~800 lines of non-test code, split CLI (Task 6) into its own stacked PR.
2. Codex: R1 → attacker (focus: the two gates' TOCTOU window inside the closure vs `noteInboundAuth`; lock order; `MatchInboundToken` no-early-exit; §6.5; PUT/rotate/rename interleavings — a rename during a pending rotation orphans the record under the old alias → the gates fail closed, say so; redact coverage) → critic. Incremental re-review after fixes.
3. Deploy both daemons before acceptance: mlab `make build && pdx stop && pdx start && pdx version`; air26 `ssh air26 '~/.config/pdx/bin/pdx stop'; scp bin/pdx air26:~/.config/pdx/bin/pdx; ssh air26 '~/.config/pdx/bin/pdx start && ~/.config/pdx/bin/pdx version'` (binary is arm64 on both). **`make build` overwrites the root `pdx`? — no: it writes `bin/pdx`; confirm with `git status --short` after the build.**
4. Real-machine acceptance (spec §9 D3), recorded on the PR: on mlab `pdx peers host rotate air26` → prints new token; `pdx peers host rotate air26 --commit` → refused `rotation unconfirmed`, exit 1, with the on-peer instruction; on air `pdx peers host set-token mini-lab <new>` (verifies air→mlab with the new token); `pdx peers --all` green on both; mlab `pdx peers host list` → `pending, confirmed`; `--commit` → 200, `host list` → `-`. Negative: `rotate air26` again, do **not** push; on air `pdx peers --all` still green (old token honoured); mlab `host list` → `pending` (not confirmed — air just dialled with the old one); `pdx peers host rotate air26 --cancel` → 200; `pdx peers --all` still green both ways.
5. Merge, bump (`git fetch`, read `VERSION`), deploy is already done, update the memory file.

## Self-review against the spec

- §6.1 config field, `Redacted`, two row fields → Tasks 1, 3. ✔
- §6.2 matcher (both fields, no early exit, last match wins, prev-only authenticates), `Principal.UsedPrevToken`, in-memory record noted at `handlePeers` + `handleDeliver` **before any refusal**, reset on rotate, in-memory on purpose → Tasks 1–3. ✔ (Deviation: the record stores `"current"|"prev"` rather than `{usedPrev, at}` — `at` is never served; and cancel rewrites `"prev"` → `"current"` so the spec's "can only read `""` or `"current"` afterwards" holds literally.)
- §6.3 three routes, exact statuses/messages, idempotent commit, both gates + force, mint never admin, all inside one closure with the record read under `rotMu` inside it → Task 4. ✔
- §6.4 is the App's protocol (D4); the CLI refusal text says what to do on the peer → Task 6. ✔
- §6.5 pinned by `TestRotate_DuringBlockedPut_Put409_TokensUntouched` + `TestPut_AfterRotate_Succeeds` → Task 4. ✔
- §6.6 CLI grammar, output, `ROTATION` column → Task 6. ✔
- §8.3 every row → Tasks 1–6 tests; mutations → Task 7 (M9/M10 are the "`""` counts as prev/current" rows the memory file insists must stay red). ✔
- #1152 → Task 5. ✔
- Types consistent: `MatchInboundToken` three-value everywhere (config tests, middleware); `hostRow` field names `rotation_pending`/`last_inbound_auth` match `cliHostRow`; `rotateResponse` ↔ `cliRotateResponse`; error strings `rotation already pending` / `rotation unconfirmed` / `no rotation pending` identical in handlers, tests and the CLI match.
