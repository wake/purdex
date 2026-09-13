# Peer Bridge P2 Implementation Plan — Host registry, scoped auth, fan-out

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Plan v1.

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
  `internal/agent`). `internal/middleware` may import `internal/config`.
  `cmd/pdx` never imports `internal/module/peers`.
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
// ValidateAlias: non-empty, no '/', not EqualFold(localAlias).
func ValidateAlias(alias, localAlias string) error
```
`Hosts` must serialize as `[[peers.hosts]]` tables in TOML (check the TOML
library used by `config.Load`/`WriteFile` — `github.com/BurntSushi/toml` or
`pelletier/go-toml`; read `go.mod`).

**Tests:** round-trip `WriteFile`→`Load` with two hosts preserving every
field incl. `allow_bypass`; `Redacted` blanks all four secret fields and
leaves the original intact (mutate the copy's Hosts, assert original
unchanged); `NewPeerToken` shape (regex `^pdxp_[0-9a-f]{32}$`) and two
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
// UpdateConfig runs mutate under CfgMu, persists to CfgPath (when set),
// rolls back the in-memory config if the write fails, and calls
// NotifyConfigChange on success. It returns mutate's error unchanged
// without persisting.
func (c *Core) UpdateConfig(mutate func(cfg *config.Config) error) error
```
`handleGetConfig` and `handlePutConfig` encode `c.Cfg.Redacted()` (taken
under the read lock) instead of blanking `Token`/`HostID` inline. The
rollback pattern in `handlePutConfig` (`snapshot := *c.Cfg` … `*c.Cfg =
snapshot`) is what `UpdateConfig` generalises; refactor `handlePutConfig` to
use `UpdateConfig` only if the existing tests stay green unedited — else
leave it and just switch its response to `Redacted()`.

**Tests:** GET and PUT config with a configured peer host ⇒ response JSON
has `peers.hosts[0].token == ""` and `inbound_token == ""` while `alias`,
`url`, `host_id`, `allow_bypass` survive; `UpdateConfig` persists (read the
file back with `config.Load`), rolls back on write failure (CfgPath in a
non-existent directory ⇒ error, in-memory config unchanged), and returns a
mutate error without writing; a callback registered with `OnConfigChange`
fires once on success and not on failure.

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
marshal a fixture and compare to the literal P1 shape).

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
**P2 host policy** (a package-level `func HostRoutePolicy(r) bool` in
`internal/module/peers`, passed in from main.go): host principals may call
`GET /api/peers` with no `scope` query parameter (or `scope=local`); every
other method/path/`scope=all` is refused with 403.

**main.go chain:**
```go
peerChain := middleware.CORS(middleware.IPWhitelist(cfg.Allow)(
    middleware.PairingGuard(isPairing)(
        middleware.PeerAuth(tokenFn, peersFn, peersmod.HostRoutePolicy)(mux))))
outerMux.Handle("/api/peers", peerChain)
outerMux.Handle("/api/peers/", peerChain)
outerMux.Handle("/", generalChain) // exactly today's chain minus PeerRouteAuth
```
`peersFn` reads `c.Cfg.Peers` under `CfgMu.RLock` (a copy).

**Tests (unit on `PeerAuth`, then a composition test that builds `outerMux`
exactly as main.go does with fakes):**
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
// 16 MiB body cap, and decodes an Envelope. Non-200 ⇒ error "HTTP <code>".
func fetchRemote(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error)
```
The module gets a `client *http.Client` field (default `&http.Client{Timeout:
3 * time.Second}`) and a `fetch func(...)` seam defaulting to `fetchRemote`.

**Handler (`GET /api/peers`):**
- Read `PrincipalFrom(r.Context())`. `scope=all` with a non-admin principal
  ⇒ 403 (defence in depth; the policy already refuses).
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

**Verify** = `fetch(ctx 3 s, url, token)`; must return `ok` HTTP 200 with a
non-empty `host_id`; if the entry already has a non-empty `HostID` that
differs ⇒ 409 `host_id mismatch`. `host_id` equal to the local `HostID` ⇒
400 `cannot pair a host with itself`.

**Tests (httptest module with a `core.Core` whose `CfgPath` is a temp file):**
add without token ⇒ 201, `inbound_token` matches the regex, file on disk
contains the host with empty `host_id`; add with token against an httptest
"remote" returning `{host_id:"air:1"}` ⇒ 201 `verified:true` and `host_id`
persisted; add with bad token (remote 401) ⇒ 502 and nothing persisted;
duplicate alias (case-insensitive) ⇒ 409; alias with `/` or equal to local
alias ⇒ 400; PUT token verifies and stores; PUT allow_bypass only; PUT on
unknown ⇒ 404; DELETE ⇒ 204 then GET list lacks it; list never contains
`token`/`inbound_token` keys (JSON key assertion); host principal on any
hosts route ⇒ 403 (through `HostRoutePolicy`).

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
- Unknown subcommand / missing args ⇒ usage to stderr, exit 2. All flags
  parsed by the existing hand parser extended, unknown flags still exit 2.

**Tests:** table golden for `--all` with a healthy remote and an unreachable
one; each `host` subcommand against `httptest` asserting method, path and
body; error passthrough; usage exit 2.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(cli): pdx peers --all and host add|set-token|remove|list`

# Phase E — Acceptance (main session)

### Task 8: Two isolated daemons paired both ways on mlab

- [ ] Build `bin/pdx`; write `/tmp/pdx-p2/a/config.toml` (`host_id="p2-a:aaaaaa"`, `bind=127.0.0.1`, `port=7861`, `token="atoken"`, `data_dir=/tmp/pdx-p2/a/data`, `[peers] alias="a"`) and `/tmp/pdx-p2/b/config.toml` (`p2-b:bbbbbb`, 7862, `token=""` — empty admin token on purpose, alias `b`). Copy the production `agent_events.db` snapshot into `a/data` only (as in P1 acceptance). Start both in tmux windows `pdx-p2a`, `pdx-p2b`.
- [ ] Pairing: `pdx peers host add b http://127.0.0.1:7862 --config a.toml` → T1; `pdx peers host add a http://127.0.0.1:7861 --token T1 --config b.toml` → T2 + `verified: yes` (b's admin token is empty, so this proves the daemon API works with… note: b's CLI uses b's admin token which is empty ⇒ b's own `/api/peers/hosts` refuses the CLI with 401. Expected per §4.6. Set b's token to `btoken` instead, restart b, redo.) Then `pdx peers host set-token b T2 --config a.toml` → `verified: yes`.
- [ ] `pdx peers --all --config a.toml` shows `a` rows (cc sessions) and `b` rows (all `no_agent`, empty registry snapshot); `--config b.toml` shows both the other way.
- [ ] Wrong inbound token: `curl -H 'Authorization: Bearer nope' http://127.0.0.1:7862/api/peers` ⇒ 401; T1 against 7861 ⇒ 200; T1 against `7861/api/peers?scope=all` ⇒ 403; T1 against `7861/api/peers/hosts` ⇒ 403.
- [ ] `curl http://127.0.0.1:7861/api/config -H 'Authorization: Bearer atoken' | jq .peers` shows hosts with empty `token`/`inbound_token`.
- [ ] Stop b; `pdx peers --all --config a.toml` shows `b  (unreachable: …)` and exit 0.
- [ ] Tear down both daemons, `rm -rf /tmp/pdx-p2`; paste the tables and curl codes into the PR.
