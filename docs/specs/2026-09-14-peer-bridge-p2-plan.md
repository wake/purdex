# Peer Bridge P2 Implementation Plan — Host registry, scoped auth, fan-out

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Plan v2 (after codex plan review `task-mu04hrgt-453err`: 1 Blocker, 7 Majors,
3 Minors — all applied).

**Goal:** A daemon knows its peer hosts. `pdx peers --all` lists every agent
session across every configured host in one table, per-host failures inline.
Each configured host has its own inbound credential; a remote daemon can call
this daemon's peer routes with it and nothing else. Pairing is two `host add`
calls, one per side, and every config change goes through the running daemon.

**Architecture:** Spec §4.3 (host registry, pairing, fan-out, redaction) and
§4.6 (auth matrix). The `/api/peers` prefix gets its **own middleware chain**
in `cmd/pdx/main.go` — CORS → IPWhitelist → PairingGuard → `PeerAuth` → mux —
so a host credential never meets the admin-only `TokenAuth` (R2 defense
review: adding a branch to a wrapper in front of `TokenAuth` cannot work,
the inner layer would still reject it). `PeerAuth` resolves a *principal*
(admin or one configured host) into the request context; handlers read it
to enforce admin-only operations. The P1 envelope type moves to
`internal/peers.Envelope` so the daemon module, the fan-out client and the
CLI share one wire type.

**Tech Stack:** Go 1.26 · no SPA changes.

**Spec:** `docs/specs/2026-09-13-peer-bridge-spec.md` v3 §4.3, §4.6, §5 P2.
P1 plan (`2026-09-14-peer-bridge-p1-plan.md`) describes the code this builds on.

## Global Constraints

- **TDD, no exceptions.** Failing test first, run it, implement, run again.
  Each task is one commit.
- **Commit messages in English**; conversation replies in Traditional Chinese.
  Every commit ends with:
  ```
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_014SuaVEyNBiabLee6WB69Y5
  ```
- **Verification:** `go build ./... && go vet ./... && go test ./...` from the
  worktree root, green before every commit.
- **Worktree path:** every command runs from
  `/Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-peer-bridge-p2`
  (prefix every Bash call with `cd <that path> &&`); every Edit/Write uses
  that absolute prefix.
- **Existing tests outside this feature are not edited.** Tests that P1
  introduced (`internal/middleware/peer_route_auth_test.go`,
  `internal/module/peers/*_test.go`, `cmd/pdx/peers_test.go`,
  `internal/config` peers tests) are this feature's own and may be changed
  where a task says so. `internal/core/config_handler_test.go` may gain new
  assertions but existing ones stay.
- **No network in unit tests** beyond `httptest` servers on loopback.
- **Secrets never reach a response or a log.** Every config response goes
  through `config.Redacted`. No `log.Printf` prints a `Config`, a `PeerHost`,
  or a token.
- **Package boundaries:** `internal/peers` stays a leaf (stdlib +
  `internal/agent`). `internal/middleware` may import `internal/config` but
  never `internal/module/*` (a middleware test importing the peers module
  would form a test import cycle, because the module imports middleware for
  `PrincipalFrom`). `cmd/pdx/main.go` already imports `internal/module/peers`
  to register it; `cmd/pdx/peers.go` (the CLI) must not.
- **Config snapshots, never shared slices.** Every reader that leaves
  `CfgMu` (auth, fan-out, handlers) works on `Config.Clone()` / a cloned
  `Hosts` slice taken under the read lock. Every writer goes through
  `Core.UpdateConfig`. Run `go test -race ./internal/core/ ./internal/module/peers/
  ./internal/middleware/` once per task in Phases B–C.
- **Token format:** `pdxp_` + 32 lowercase hex chars from `crypto/rand`
  (16 bytes). Constant-time comparison everywhere (`crypto/subtle`).
- **Aliases** are compared case-insensitively (`strings.EqualFold`), may not
  contain `/`, may not be empty, and may not equal the local alias.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/config/config.go` *(modify)* | `PeerHost`, `PeersConfig.Hosts`, `Redacted()`, `NewPeerToken()`, `FindPeerHost*` helpers |
| `internal/core/config_handler.go` *(modify)* | GET/PUT config responses via `config.Redacted` |
| `internal/core/core.go` *(modify)* | `(*Core).UpdateConfig(mutate)` — lock, mutate, persist, notify |
| `internal/peers/envelope.go` *(new)* | `Envelope` (moved from module `response`), `HostResult`, `AllEnvelope` |
| `internal/middleware/peer_auth.go` *(new, replaces `peer_route_auth.go`)* | `PeerAuth`, `Principal`, `PrincipalFrom` |
| `cmd/pdx/main.go` *(modify)* | separate chain for the `/api/peers` prefix |
| `internal/module/peers/client.go` *(new)* | `fetchRemote(ctx, url, token) (Envelope, error)` with 3 s timeout + body cap |
| `internal/module/peers/hosts.go` *(new)* | `/api/peers/hosts` handlers (list/add/set-token/remove) |
| `internal/module/peers/module.go` *(modify)* | `scope=all` fan-out, principal checks, routes |
| `cmd/pdx/peers.go` *(modify)* | `--all`, `host add|set-token|remove|list` |

---

# Phase A — Config and core

### Task 1: `PeerHost` config, redaction, token minting

**Files:** modify `internal/config/config.go` (+ new `internal/config/peers_test.go`
or extend the P1 peers tests).

**Produce:**
```go
type PeerHost struct {
    Alias        string `toml:"alias"         json:"alias"`
    URL          string `toml:"url"           json:"url"`
    HostID       string `toml:"host_id"       json:"host_id"`        // "" until verified
    Token        string `toml:"token"         json:"token"`          // outbound: what we present to that host
    InboundToken string `toml:"inbound_token" json:"inbound_token"`  // what that host must present to us
    AllowBypass  bool   `toml:"allow_bypass"  json:"allow_bypass"`
}
type PeersConfig struct {
    Alias string     `toml:"alias" json:"alias"`
    Hosts []PeerHost `toml:"hosts" json:"hosts"`
}

// Redacted returns a deep copy with Token, HostID, every Peers.Hosts[i].Token
// and .InboundToken blanked. The receiver is never mutated (Hosts is copied).
func (c Config) Redacted() Config
// NewPeerToken returns "pdxp_" + 32 hex chars from crypto/rand.
func NewPeerToken() (string, error)
// FindPeerHostByAlias: case-insensitive alias match; index or -1.
func (p PeersConfig) FindPeerHostByAlias(alias string) int
// MatchInboundToken compares bearer against every host's non-empty
// InboundToken in constant time and returns the matching host (copy) and
// true; an empty bearer never matches.
func (p PeersConfig) MatchInboundToken(bearer string) (PeerHost, bool)
// ValidateAlias: matches ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$, is not "." or
// "..", and is not EqualFold(localAlias). (One URL path segment, no escaping
// needed; ServeMux never canonicalises it.)
func ValidateAlias(alias, localAlias string) error
// Clone deep-copies every slice (Allow, AllowedPaths, Stream.Presets,
// Detect.CCCommands, Peers.Hosts) so a mutation of the copy never touches
// the receiver's backing arrays.
func (c Config) Clone() Config
```
`Hosts` must serialize as `[[peers.hosts]]` tables in TOML (check the TOML
library used by `config.Load`/`WriteFile` — `github.com/BurntSushi/toml` or
`pelletier/go-toml`; read `go.mod`).

**Tests:** round-trip `WriteFile`→`Load` with two hosts preserving every
field incl. `allow_bypass` (the file must contain `[[peers.hosts]]`);
`Redacted` blanks all four secret fields and leaves the original intact
(mutate the copy's Hosts, assert original unchanged); `Clone` then append to
the copy's `Peers.Hosts` and edit `Detect.CCCommands[0]` ⇒ original
unchanged; `ValidateAlias` accepts `air`, `air.2026`, `air-2_x`, rejects
`""`, `"."`, `".."`, `"a/b"`, `"a b"`, `"?x"`, `"#"`, `"%41"`, 65 chars, and
the local alias in any case; `NewPeerToken` shape (regex `^pdxp_[0-9a-f]{32}$`) and two
calls differ; `MatchInboundToken` hits the right host among three, misses on
empty bearer, misses when the host's InboundToken is empty; `ValidateAlias`
cases; `FindPeerHostByAlias("AIR")` finds `air`.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(config): peer hosts with per-host inbound tokens and redaction`

### Task 2: Config responses redacted everywhere; `Core.UpdateConfig`

**Files:** modify `internal/core/config_handler.go`, `internal/core/core.go`;
extend `internal/core/config_handler_test.go` (new assertions only) and add
`internal/core/update_config_test.go`.

**Produce:**
```go
// UpdateConfig is the single serialised writer of the runtime config:
//   1. CfgMu.Lock(); next := c.Cfg.Clone()
//   2. err := mutate(&next); if err != nil → Unlock, return err (nothing changed)
//   3. if CfgPath != "" { if err := config.WriteFile(CfgPath, next); err != nil → Unlock, return err (c.Cfg untouched) }
//   4. *c.Cfg = next   // commit: pointer identity preserved for other holders
//   5. CfgMu.Unlock(); c.NotifyConfigChange()   // AFTER unlock: agent callbacks take RLock
func (c *Core) UpdateConfig(mutate func(cfg *config.Config) error) error
```
The mutation runs on a deep copy, so a mutate that edits or deletes a
`Peers.Hosts` element can never touch the live backing array, and neither a
mutate error nor a write error changes runtime state.

**Existing writers move onto it** (read all three first; `config.WriteFile`
is in `internal/config/hostid.go` and calls `os.MkdirAll` — a missing parent
dir does not make it fail):
- `handleGetConfig` / `handlePutConfig` (`config_handler.go`): responses
  encode `Redacted()` of the current config (GET: under RLock; PUT: the
  committed config). `handlePutConfig`'s body becomes one `UpdateConfig`
  call carrying its validation + field assignments; status codes and
  messages unchanged so its tests stay green unedited.
- `handleTokenAuth` (`token_handler.go:29`) and `handlePairSetup`
  (`pairing_handler.go:122`) currently `WriteFile` a copy **after**
  releasing the lock — a window in which a hosts mutation can be overwritten
  on disk, and both share `WriteFile`'s fixed `<path>.tmp`. Route their
  writes through `UpdateConfig`, dropping their own `WriteFile` +
  `NotifyConfigChange` calls so nothing is written twice. Their existing
  tests must stay green unedited; if one cannot, stop and report.

**Tests:** GET and PUT config with a configured peer host ⇒ response JSON
has `peers.hosts[0].token == ""` and `inbound_token == ""` while `alias`,
`url`, `host_id`, `allow_bypass` survive; `UpdateConfig` persists (read the
file back with `config.Load`); write failure — `CfgPath` whose parent is a
**regular file** (the pattern `config_handler_test.go` already uses) ⇒
error and in-memory config byte-identical to before; mutate error ⇒ no file
write (mtime/contents unchanged) and no change; mutate that edits
`Peers.Hosts[0].Token` and one that deletes `Hosts[1]` ⇒ live config only
changes after commit, and a slice captured before the call is untouched;
mutate returning error after editing the copy ⇒ live config unchanged; an
`OnConfigChange` callback that takes `CfgMu.RLock` and re-reads the config
runs exactly once on success (no deadlock — run with `-race`), zero times
on failure; token and pairing handlers still pass their existing tests.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(core): redact peer secrets in config responses; UpdateConfig helper`

# Phase B — Wire types and auth

### Task 3: `peers.Envelope` shared wire type

**Files:** create `internal/peers/envelope.go`; modify
`internal/module/peers/module.go` (delete its private `response`, use
`ipeers.Envelope`), `cmd/pdx/peers.go` (delete `peersResponse`, use
`peers.Envelope`); adjust their tests only where the type name changes.

**Produce:**
```go
// Envelope is GET /api/peers' body for one host.
type Envelope struct {
    HostID  string       `json:"host_id"`
    OK      bool         `json:"ok"`
    Error   string       `json:"error,omitempty"`
    Partial bool         `json:"partial"`
    Peers   []PeerRecord `json:"peers"` // never null
}
// HostResult is one host's row in a scope=all response.
type HostResult struct {
    Alias  string `json:"alias"`
    HostID string `json:"host_id"`   // configured or learned; "" if unknown
    OK     bool   `json:"ok"`
    Error  string `json:"error,omitempty"`
    Partial bool  `json:"partial"`
    Peers  []PeerRecord `json:"peers"` // never null
}
// AllEnvelope is GET /api/peers?scope=all's body.
type AllEnvelope struct {
    Hosts []HostResult `json:"hosts"` // local host first
}
```
JSON of `Envelope` must be byte-identical to P1's `response` (golden test:
marshal a fixture and compare to the literal P1 shape). Files whose tests
mention the old type names and must be updated: `internal/module/peers/module_test.go`
(`response` → `ipeers.Envelope`), `cmd/pdx/peers_test.go` (`peersResponse` →
`peers.Envelope`); assertions unchanged.

- [ ] tests written and failing
- [ ] implementation, all green (P1 module + CLI tests still pass)
- [ ] commit `refactor(peers): share the inventory envelope between daemon and CLI`

### Task 4: `PeerAuth` principal middleware and its own chain

**Files:** create `internal/middleware/peer_auth.go`,
`internal/middleware/peer_auth_test.go`; delete
`internal/middleware/peer_route_auth.go` and its test (their cases move into
the new test file); modify `cmd/pdx/main.go`.

**Produce:**
```go
type PrincipalKind string
const (
    PrincipalAdmin PrincipalKind = "admin"
    PrincipalHost  PrincipalKind = "host"
)
type Principal struct {
    Kind   PrincipalKind
    Alias  string // host only
    HostID string // host only; "" when the entry is unverified
}
// PrincipalFrom returns the principal PeerAuth stored, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool)

// PeerAuth authenticates every request it sees (it is mounted only on the
// /api/peers prefix — it does no prefix matching itself):
//   1. a non-empty admin token presented as Bearer ⇒ PrincipalAdmin;
//   2. else a Bearer matching a configured host's InboundToken ⇒ PrincipalHost;
//   3. else 401.
// ?ticket= is never consulted. An empty admin token disables (1) only.
// hostAllowed decides whether a host principal may reach this request at
// all (admin may reach everything); a refused host gets 403.
func PeerAuth(adminToken func() string, peers func() config.PeersConfig,
              hostAllowed func(r *http.Request) bool) func(http.Handler) http.Handler
```
Also export `func WithPrincipal(ctx context.Context, p Principal) context.Context`
so handler tests in other packages can build a request context without
running the middleware.

**P2 host policy** — create `internal/module/peers/policy.go`:
```go
// HostRoutePolicy says which requests a host principal may make in P2:
// GET /api/peers (exact path) with no scope or scope=local. Everything else
// (hosts routes, scope=all, any other method) is admin-only.
func HostRoutePolicy(r *http.Request) bool
```
with `policy_test.go` covering each row. `main.go` passes it to `PeerAuth`.

**main.go refactor for testability:** extract the chain construction into
`cmd/pdx/http_chain.go`:
```go
// newOuterHandler builds the daemon's outer http.Handler: /api/health
// (CORS only), the /api/peers prefix chain (PeerAuth, no TokenAuth) and the
// general chain (today's, minus PeerRouteAuth) for everything else.
func newOuterHandler(c *core.Core, mux http.Handler, allow []string) http.Handler
```
```go
tokenFn := func() string { RLock; return c.Cfg.Token }
peersFn := func() config.PeersConfig { RLock; p := c.Cfg.Peers; p.Hosts = append([]config.PeerHost(nil), p.Hosts...); return p }
isPairing := func() bool { return c.Pairing.Get() == core.StatePairing }
peerChain := middleware.CORS(middleware.IPWhitelist(allow)(middleware.PairingGuard(isPairing)(
    middleware.PeerAuth(tokenFn, peersFn, peersmod.HostRoutePolicy)(mux))))
general   := middleware.CORS(middleware.IPWhitelist(allow)(middleware.PairingGuard(isPairing)(
    middleware.TokenAuth(tokenFn, c.Tickets)(mux))))
outer := http.NewServeMux()
outer.Handle("GET /api/health", middleware.CORS(http.HandlerFunc(c.HandleHealth)))
outer.Handle("/api/peers", peerChain)
outer.Handle("/api/peers/", peerChain)
outer.Handle("/", general)
```
`runServe` calls `newOuterHandler`; nothing else in `runServe` changes. Go's
ServeMux prefers the more specific pattern, so `/api/peers` and
`/api/peers/…` never reach `general`, and `/api/peersx` never reaches
`peerChain` (both muxes use the same escaped-path matching, so there is no
cross-chain path). CORS preflight (`OPTIONS`) reaches `CORS` first in both
chains, as today.

**Tests — unit on `PeerAuth` in `internal/middleware/peer_auth_test.go`
(`package middleware_test`, policy stubbed with a func literal), and the
composition test in `cmd/pdx/http_chain_test.go` (`package main`) calling
`newOuterHandler` with a `core.Core` built the way `owner_resolver_test.go`
builds one (fake tmux, `core.NewServiceRegistry()`), a ticket-validator fake
on `c.Tickets` if the field type allows, else a stub `TicketValidator`
passed through a test seam:**
- admin bearer ⇒ next called, principal admin; empty admin token + admin
  bearer ⇒ 401; host bearer ⇒ principal host with alias/host_id; host bearer
  on a disallowed request ⇒ 403 and next NOT called; wrong bearer ⇒ 401;
  `?ticket=x` ignored (401 without bearer; validator fake never called);
  two hosts with tokens ⇒ the right one matched; a host with empty
  InboundToken never matches an empty bearer.
- composition: `/api/peers?ticket=ok` ⇒ 401 with the ticket validator not
  called; `/api/sessions?ticket=ok` ⇒ 200 (general chain unchanged);
  `/api/peers/hosts` with host bearer ⇒ 403; `/api/peers` with host bearer ⇒
  200 and the handler sees `PrincipalHost`; `/api/peersx` ⇒ general chain
  (TokenAuth semantics, i.e. 200 with empty admin token); empty admin
  token: `/api/sessions` 200, `/api/peers` 401, `/api/peers` with a valid
  host bearer 200.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(middleware): PeerAuth principal for the /api/peers chain`

# Phase C — Daemon endpoints

### Task 5: Fan-out client and `scope=all`

**Files:** create `internal/module/peers/client.go`, `client_test.go`;
modify `internal/module/peers/module.go`, `module_test.go`, `fakes_test.go`.

**Produce (client):**
```go
// fetchRemote GETs <baseURL>/api/peers with the bearer, 3 s total timeout,
// 16 MiB body cap, and decodes an Envelope. Any non-200 status — including
// every 3xx, because the client never follows redirects — is an error
// "HTTP <code>". A decode failure is an error. The bearer is sent only to
// baseURL's host.
func fetchRemote(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error)
// newRemoteClient returns &http.Client{Timeout: 3s, CheckRedirect: func(...) error { return http.ErrUseLastResponse }}.
func newRemoteClient() *http.Client
```
The module gets a `client *http.Client` field (default `newRemoteClient()`)
and a `fetch func(ctx, client, baseURL, bearer) (Envelope, error)` seam
defaulting to `fetchRemote`; `New()` initialises both, and every test helper
that builds a `Module` literal directly must set them (update the P1 helper
in `module_test.go` accordingly).

**Handler (`GET /api/peers`):**
- Read `PrincipalFrom(r.Context())`. `scope=all` with a non-admin principal
  ⇒ 403 (defence in depth; the policy already refuses). Tests build the
  context with `middleware.WithPrincipal`.
- Take one config snapshot under `CfgMu.RLock` at the top: local `HostID`,
  `PeerAlias()`, and a cloned `Peers.Hosts`; every later step uses the
  snapshot (a concurrent hosts mutation must not be observed mid-request).
- Replace P1's `TestHandlePeers_ScopeAllRejected` (400) with the new
  admin/host cases; keep every other P1 test.
- `scope=all`: run the local inventory (existing code, refactored into
  `m.localEnvelope(r.Context()) ipeers.Envelope`) and, in parallel goroutines,
  `fetch` every host whose `Token != ""`, each with its own 3 s context.
  Build `AllEnvelope{Hosts}`: local first as `HostResult{Alias: local alias,
  HostID: local host id, ...envelope}`, then hosts in config order. Failure
  ⇒ `{ok:false, error, peers:[]}`. If a remote's `host_id` differs from a
  configured non-empty `HostID` ⇒ that row `ok:false, error:"host_id
  mismatch: got <x>"`. Hosts without a token are still listed as
  `ok:false, error:"no outbound token"`.
- No `scope`/`scope=local`: today's behaviour, using the shared `localEnvelope`.
- Any other `scope` ⇒ 400.

**Tests:** `fetchRemote` against `httptest`: 200 ⇒ envelope; 401 ⇒ error
mentions 401; slow server (sleep > timeout with a 50 ms client) ⇒ error;
oversize body ⇒ error; bearer header asserted. `scope=all`: two fake hosts
(one healthy httptest, one closed) ⇒ three rows, local first, healthy
`ok:true` with its peers, closed `ok:false` with error, order preserved;
host_id mismatch row; host without token row; host principal on
`scope=all` ⇒ 403; admin ⇒ 200. Deterministic: sort nothing, rely on config
order; goroutines write into a pre-sized slice by index.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): scope=all fan-out across configured hosts`

### Task 6: Host registry endpoints and pairing

**Files:** create `internal/module/peers/hosts.go`, `hosts_test.go`; modify
`module.go` (routes).

**Routes (all admin-only — the policy refuses host principals):**
| Route | Body | Behaviour |
|---|---|---|
| `GET /api/peers/hosts` | – | `{hosts:[{alias, url, host_id, verified: host_id!="", has_token, has_inbound_token, allow_bypass}]}` — never the tokens |
| `POST /api/peers/hosts` | `{alias, url, token?}` | validate alias (§ constraints; 400) and url (absolute http/https; 400); alias unique (409); mint `inbound_token`; if `token` given, verify first (below) — failure ⇒ 502 `{error}` and nothing persisted; then `UpdateConfig` append; 201 `{alias, url, host_id, inbound_token, verified}` — the only response that ever contains `inbound_token` |
| `PUT /api/peers/hosts/{alias}` | `{token?, allow_bypass?}` | unknown alias 404; if `token` given verify then store `Token` + learned `HostID`; `allow_bypass` when present; 200 with the list row shape |
| `DELETE /api/peers/hosts/{alias}` | – | 404 / 204 |

**Verify** = `fetch(ctx 3 s, url, token)` **outside any lock**; the
predicate is `err == nil && env.OK && env.HostID != ""`. Then commit inside
`UpdateConfig`, re-checking under the lock: alias still unique / entry still
present (else 409 `alias changed concurrently` / 404), the entry's `HostID`
is empty or equal to the learned one (else 409 `host_id mismatch`), and the
learned `host_id` is not the local `HostID` (400 `cannot pair a host with
itself`). Any failure leaves `Token`, `HostID`, `AllowBypass` and the file
untouched. `PathValue("alias")` is matched case-insensitively via
`FindPeerHostByAlias`.

**Tests (httptest module with a `core.Core` whose `CfgPath` is a temp file;
remote fixture is a full envelope `{"host_id":"air:1","ok":true,"partial":false,"peers":[]}`):**
add without token ⇒ 201, `inbound_token` matches the regex, file on disk
contains the host with empty `host_id`; add with token against the fixture
⇒ 201 `verified:true` and `host_id` persisted; add with bad token (remote
401) ⇒ 502 and nothing persisted; remote returns `ok:false` or empty
`host_id` ⇒ 502; remote `host_id` equal to local ⇒ 400; PUT token when the
entry already has a different `host_id` ⇒ 409 and the old token kept;
duplicate alias (case-insensitive) ⇒ 409; invalid alias (`a/b`, `..`, local
alias) ⇒ 400; PUT token verifies and stores; PUT allow_bypass only; PUT on
unknown ⇒ 404; DELETE ⇒ 204 then GET list lacks it; list never contains
`token`/`inbound_token` keys (JSON key assertion); **concurrency**: two
parallel POSTs with the same alias gated by a barrier inside the fake
verify ⇒ exactly one 201 and one 409, file has one entry; PUT verify in
flight while a DELETE removes the entry ⇒ PUT 404, nothing re-created;
**two real modules pairing both ways** through two httptest servers (each
module's `fetch` is the real `fetchRemote`) ⇒ both entries verified, and a
`scope=all` on each shows the other; **capability of an inbound-token
holder**: with the composition chain from Task 4, the token can `GET
/api/peers` (200) but not `scope=all` (403), any hosts route (403), or
`GET /api/config` (401 — general chain, admin token non-empty); alias
containing `.` (`air.2026`) survives add → set-token → remove.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): host registry endpoints with two-step pairing`

# Phase D — CLI

### Task 7: `pdx peers --all` and `pdx peers host …`

**Files:** modify `cmd/pdx/peers.go`, `cmd/pdx/peers_test.go`.

**Behaviour:**
- `pdx peers --all [--json]` → `GET /api/peers?scope=all`; table adds a
  leading `HOST` column (alias) and prints one line per failed host:
  `<alias>  (unreachable: <error>)`; `--json` passthrough. Exit 0 when the
  local row is ok (remote failures are rows, not errors); 1 otherwise.
- `pdx peers host list` → table `ALIAS URL HOST_ID VERIFIED TOKEN INBOUND ALLOW_BYPASS`
  (yes/no columns).
- `pdx peers host add <alias> <url> [--token <t>]` → POST; prints
  ```
  added <alias> (<url>)  verified: yes|no
  inbound token for <alias> to use when adding this host:
    <inbound_token>
  ```
  on stdout; exit 1 on 4xx/5xx with the server's `error` on stderr.
- `pdx peers host set-token <alias> <token> [--allow-bypass=true|false]` → PUT.
- `pdx peers host remove <alias>` → DELETE.
- **Grammar** (hand parser, replaces P1's): `pdx peers [--json] [--all]
  [--config <path>]` and `pdx peers host <add|set-token|remove|list> [args…]
  [--config <path>] [--token <t>] [--allow-bypass=true|false]`. Flags may
  appear anywhere after `peers`; `host` must be the first positional and
  its verb the second; arity is strict (`add` = 2 positionals, `set-token`
  = 2, `remove` = 1, `list` = 0) — extra or missing positionals, a flag
  missing its value, an unknown flag, a flag valid only for another form
  (`--all` with `host`, `--token` without `host add|set-token`) ⇒ usage
  line on stderr, exit 2, before any config load or request. Alias is
  placed in the URL path as-is (validated server-side; the client also
  refuses aliases containing `/`).
- Replace P1's `TestRunPeersCmd_UnknownFlag` (it used `--all`) with
  `--bogus`; keep its assertions.

**Tests:** table golden for `--all` with a healthy remote and an unreachable
one; each `host` subcommand against `httptest` asserting method, path
(`/api/peers/hosts/air.2026` for a dotted alias) and body; error
passthrough; every grammar rejection above ⇒ exit 2 with no request made
(assert the test server saw zero requests).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(cli): pdx peers --all and host add|set-token|remove|list`

# Phase E — Acceptance (main session)

### Task 8: Two isolated daemons paired both ways on mlab

- [ ] Build `bin/pdx`; write `/tmp/pdx-p2/a/config.toml` (`host_id="p2-a:aaaaaa"`, `bind="127.0.0.1"`, `port=7861`, `token="atoken"`, `data_dir="/tmp/pdx-p2/a/data"`, `upload_dir="/tmp/pdx-p2/a/upload"`, `[peers] alias="a"`) and `/tmp/pdx-p2/b/config.toml` (`p2-b:bbbbbb`, 7862, `token="btoken"`, its own dirs, alias `b`). Both admin tokens are non-empty on purpose: a normal start with an empty token mints one (`initPairing`), so the empty-token matrix is only provable in the Task 4 composition test. Both daemons read the same `~/.claude/sessions`, so `b` will list every tmux session with live cc entries but no owner frames ⇒ `no_agent` rows (its `data_dir` has no agent frames); copy the production `agent_events.db` snapshot into `a/data` only (as in P1 acceptance). Start both in tmux windows `pdx-p2a`, `pdx-p2b`.
- [ ] Pairing: `pdx peers host add b http://127.0.0.1:7862 --config /tmp/pdx-p2/a/config.toml` → prints T1; `pdx peers host add a http://127.0.0.1:7861 --token T1 --config /tmp/pdx-p2/b/config.toml` → prints T2, `verified: yes`; `pdx peers host set-token b T2 --config /tmp/pdx-p2/a/config.toml` → `verified: yes`; `host list` on both shows the other as verified.
- [ ] `pdx peers --all --config /tmp/pdx-p2/a/config.toml` shows `a` rows (cc sessions deliverable) and `b` rows (same sessions, `no_agent`); `--config /tmp/pdx-p2/b/config.toml` the other way round.
- [ ] Wrong inbound token: `curl -H 'Authorization: Bearer nope' http://127.0.0.1:7862/api/peers` ⇒ 401; T1 against `7861/api/peers` ⇒ 200; T1 against `7861/api/peers?scope=all` ⇒ 403; T1 against `7861/api/peers/hosts` ⇒ 403; T1 against `7861/api/config` ⇒ 401.
- [ ] `curl http://127.0.0.1:7861/api/config -H 'Authorization: Bearer atoken' | jq .peers` shows the host with empty `token`/`inbound_token`; `cat /tmp/pdx-p2/a/config.toml` shows a `[[peers.hosts]]` table with both tokens present.
- [ ] Stop b; `pdx peers --all --config /tmp/pdx-p2/a/config.toml` shows `b  (unreachable: …)` and exit 0.
- [ ] Tear down both daemons, `rm -rf /tmp/pdx-p2`; paste the tables and curl codes into the PR.
