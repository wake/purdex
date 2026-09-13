# Peer Bridge P1 Implementation Plan — Local inventory

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Plan v2 (after codex plan review `task-mu00ormj-62khmb`: 1 Blocker, 6 Majors,
5 Minors — all applied).

**Goal:** `pdx peers` and `GET /api/peers` list every agent session on the
local host as a peer record (spec §4.2) with a resolvable address (spec §4.1),
joining Purdex's tmux + agent-owner knowledge with Claude Code's own session
registry. Read-only; nothing is written to `~/.claude`. The `/api/peers`
prefix is locked down (spec §4.6) **before** any route exists under it.

**Architecture:** A new leaf package `internal/peers` holds everything pure:
registry parsing + liveness, the join that produces `PeerRecord`s, and
address resolution. A new daemon module `internal/module/peers` wires it to
the session provider and to an owner-resolver service the agent module
starts exporting, and serves the endpoint under a 2 s soft budget. The CLI
is a thin client of the endpoint.

**Tech Stack:** Go 1.26 (net/http, `encoding/json`, `os/exec` via
`internal/agent.ReadProcessInfo`) · no SPA changes.

**Spec:** `docs/specs/2026-09-13-peer-bridge-spec.md` v3, §4.1, §4.2, §4.6, §5 P1.

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
  worktree root. All three must pass before every commit.
- **Worktree path:** every command runs from
  `/Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-peer-bridge`
  (prefix every Bash call with `cd <that path> &&`); every Edit/Write uses
  that absolute prefix.
- **Never touch `~/.claude/sessions` in tests.** The registry directory is a
  parameter everywhere; tests use `t.TempDir()`.
- **No `ps` forks in unit tests.** Liveness and start-time lookups are
  injected function fields with defaults; tests substitute fakes.
- **Existing tests are not edited.** If a task cannot pass without changing an
  existing test, stop and report.
- **Package boundaries:** `internal/peers` imports only stdlib and
  `internal/agent` (for `ReadProcessInfo`); it must never import
  `internal/module/*`. `internal/module/peers` may import
  `internal/module/session` and `internal/module/agent` (verified acyclic:
  `module/peers → module/agent → module/session`).
- **Go test packages:** new packages use internal tests (`package peers` in
  both `internal/peers` and `internal/module/peers`; `package middleware`;
  `package main` in `cmd/pdx`). `internal/module/agent` tests are internal
  (`package agent`).

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/peers/registry.go` *(new)* | Parse `~/.claude/sessions/<pid>.json`, liveness (`Entry`, `ReadRegistry`) |
| `internal/peers/record.go` *(new)* | `PeerRecord`, `AgentInfo`, `Build(...)` — the pure join |
| `internal/peers/address.go` *(new)* | `Resolve`, `SplitAddress`, `HostMatches` — spec §4.1 |
| `internal/module/agent/pane_owner.go` *(modify)* | `PaneOwner.Status` |
| `internal/module/agent/owner_resolver.go` *(new)* | `OwnerResolver` interface + registration under `agent.owner-resolver` |
| `internal/config/config.go` *(modify)* | `PeersConfig{Alias}` + `(Config).PeerAlias()` (note: `WriteFile` lives in `internal/config/hostid.go:73`) |
| `internal/middleware/peer_route_auth.go` *(new)* | `PeerRouteAuth` — `/api/peers` never open, no tickets |
| `internal/module/peers/module.go` *(new)* | Module wiring, `GET /api/peers`, soft budget + envelope |
| `cmd/pdx/main.go` *(modify)* | guard wiring; register module; `peers` subcommand |
| `cmd/pdx/peers.go` *(new)* | `pdx peers [--json]` client + table formatter |

---

# Phase A — Pure package `internal/peers`

### Task 1: Registry reader with injected liveness

**Files:** create `internal/peers/registry.go`, `internal/peers/registry_test.go`.

**Wire struct (private) — exactly these JSON tags; unknown fields ignored:**
```go
type registryFile struct {
    PID        int    `json:"pid"`
    SessionID  string `json:"sessionId"`
    Cwd        string `json:"cwd"`
    ProcStart  string `json:"procStart"`
    Version    string `json:"version"`
    Tmux       string `json:"tmux"`
    Inbox      string `json:"messagingSocketPath"`
    Name       string `json:"name"`
    NameSource string `json:"nameSource"`
    Status     string `json:"status"`
}
```
Required (non-zero) for an entry to be accepted: `pid`, `sessionId`,
`procStart`, `messagingSocketPath`. Everything else may be empty. A field of
the wrong JSON type is a decode error ⇒ skipped.

**Public API:**
```go
package peers

type Entry struct {
    PID        int
    SessionID  string
    Name       string
    NameSource string
    Cwd        string
    Tmux       string // "<session>:@<win>.%<pane>" or ""
    Inbox      string // messagingSocketPath
    ProcStart  string // raw registry string, e.g. "Sun Sep 13 15:22:36 2026"
    Version    string
    Status     string // "idle" | "busy" | ""
}
func (e Entry) TmuxSessionName() string // text before the first ':'; "" if no ':'
func (e Entry) TmuxPaneID() string      // "%…" after the last '.'; "" if absent

type Liveness struct {
    Stat      func(path string) error
    PidAlive  func(pid int) bool
    StartTime func(pid int) (time.Time, error)
}
// DefaultLiveness: os.Stat; syscall.Kill(pid, 0) == nil; agent.ReadProcessInfo(pid).StartTime.
func DefaultLiveness() Liveness

// ProcStartLayout is Claude Code's registry format (UTC ctime).
const ProcStartLayout = "Mon Jan _2 15:04:05 2006"
// ParseProcStart parses a registry procStart string as UTC.
func ParseProcStart(s string) (time.Time, error)

// ReadRegistry parses every "<pid>.json" in dir and returns the live ones.
// skipped counts every file considered and rejected (name mismatch, decode
// error, missing required field, bad procStart, dead). err is non-nil only
// when dir cannot be listed.
func ReadRegistry(dir string, live Liveness) (entries []Entry, skipped int, err error)
```

**Liveness rule:** live iff `Stat(Inbox)==nil` && `PidAlive(pid)` &&
`StartTime(pid)` returns no error && `StartTime(pid).Truncate(time.Second)
.Equal(ParseProcStart(ProcStart).Truncate(time.Second))`. `Equal` compares
instants, so a `time.Local` value from `ps` and the UTC registry string
match when they denote the same moment.

**Fixture** (embed verbatim in the test as `const fixture76973`; this is the
real shape from mlab):
```json
{"pid":76973,"sessionId":"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c","cwd":"/Users/wake/Workspace/wake/purdex","startedAt":1789314156000,"procStart":"Sun Sep 13 15:22:36 2026","version":"2.1.270","peerProtocol":1,"peerFeatures":["notify_idle","reply_across_default_dirs","artifact_yield"],"kind":"interactive","entrypoint":"cli","pidDomain":"darwin","tmux":"mt1:@10.%10","messagingSocketPath":"/tmp/cc-socks/76973.sock","name":"purdex-47","nameSource":"derived","nameSince":1789314156000,"updatedAt":1789314156100,"status":"busy","statusUpdatedAt":1789314156100}
```

**Tests (write first):**
- fixture in `76973.json`, fake liveness all-true with
  `StartTime → 2026-09-13T15:22:36Z` ⇒ one Entry: PID 76973, SessionID,
  Cwd, Tmux `mt1:@10.%10`, Inbox, ProcStart raw string, Version `2.1.270`,
  Name `purdex-47`, NameSource `derived`, Status `busy`;
  `TmuxSessionName()=="mt1"`, `TmuxPaneID()=="%10"`.
- same instant, different zone: `StartTime` returning
  `time.Date(2026,9,13,23,22,36,0, time.FixedZone("CST", 8*3600))` ⇒ live.
- 1 s off ⇒ skipped=1; `PidAlive=false` ⇒ skipped; `Stat` error ⇒ skipped;
  `StartTime` error ⇒ skipped.
- missing `sessionId` ⇒ skipped; `pid` as string (`"pid":"76973"`) ⇒
  skipped; `procStart: "yesterday"` ⇒ skipped; file named `76973.json`
  containing `"pid":76974` ⇒ skipped; unreadable file (mode 000, skip on
  root) ⇒ skipped; malformed JSON ⇒ skipped.
- `.key`, `.json.tmp.abc`, `notes.txt` in dir ⇒ ignored, **not** counted in
  skipped (they never matched the name pattern).
- missing dir ⇒ `err != nil`, entries nil.
- `Entry{Tmux:""}` ⇒ both accessors `""`; `Tmux:"a:b"` (no pane) ⇒
  session `a`, pane `""`.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): read Claude Code session registry with liveness`

### Task 2: Pure join → `PeerRecord`

**Files:** create `internal/peers/record.go`, `internal/peers/record_test.go`.

**Produce:**
```go
type SessionSummary struct { Code, Name, Cwd, TmuxInstance string }
type Owner struct {
    AgentType, SessionID, Cwd, TmuxPaneID string
    LastSeenAt int64
    Status     string // Purdex agent status of the owning frame (Task 4)
}
type AgentInfo struct {
    Type      string `json:"type"`                 // cc | codex | opencode | proxy
    SessionID string `json:"session_id,omitempty"`
    PeerName  string `json:"peer_name,omitempty"`
    PID       int    `json:"pid,omitempty"`
    ProcStart string `json:"proc_start,omitempty"`
    Inbox     string `json:"inbox,omitempty"`
    Status    string `json:"status,omitempty"`
    Version   string `json:"version"`              // ALWAYS present; "" when unknown (spec §4.2)
}
type PeerRecord struct {
    Host         string     `json:"host"`
    HostID       string     `json:"host_id"`
    Address      string     `json:"address"`
    SessionCode  string     `json:"session_code"`   // always present
    SessionName  string     `json:"session_name"`   // always present
    TmuxInstance string     `json:"tmux_instance"`  // always present
    Cwd          string     `json:"cwd,omitempty"`
    Agent        *AgentInfo `json:"agent"`          // always present, null when none
    Deliverable  bool       `json:"deliverable"`
    Reason       string     `json:"reason"`         // always present: "" | no_agent | not_cc | inbox_dead | proxy | ambiguous
}
type BuildInput struct {
    HostID, Alias string
    Sessions      []SessionSummary
    Owners        map[string]Owner // by session code; absent ⇒ no owner
    Unresolved    map[string]bool  // codes whose owner lookup did not run
    Entries       []Entry
    ProxyPIDs     map[int]bool     // empty in P1; kept so P3 needs no signature change
}
func Build(in BuildInput) []PeerRecord
```

**Rules (spec §4.2):**
1. One record per session, sorted by `SessionName` (ties by `Code`).
   `Address = Alias + "/" + SessionName`; `Host = Alias`.
2. Code in `Unresolved` ⇒ `Agent: nil, Reason: ""`. Owner absent and not
   unresolved ⇒ `Agent: nil, Reason: "no_agent"`.
3. Owner type not `cc` ⇒ `Agent{Type: owner.AgentType, SessionID, Status:
   owner.Status, Version: ""}`, `Deliverable=false, Reason="not_cc"`.
4. Owner type `cc`: candidates = live entries with `SessionID ==
   owner.SessionID` **and** `PID ∉ ProxyPIDs`. 0 ⇒ `Agent{Type:"cc",
   SessionID, Status: owner.Status}`, `Reason="inbox_dead"`. 1 ⇒ full
   `AgentInfo` from the entry (Status from the entry), `Deliverable=true`.
   >1 ⇒ those whose `TmuxPaneID()==owner.TmuxPaneID`; exactly one ⇒ use it;
   zero or several ⇒ `Reason="ambiguous"`, `Agent{Type:"cc", SessionID,
   Status: owner.Status}`.
5. **Outside-tmux rows:** every live entry whose `TmuxSessionName()` is not
   the `Name` of any session in `Sessions` gets a record — regardless of
   whether rule 4 also used it: `SessionCode=""`, `SessionName=""`,
   `TmuxInstance=""`, `Address = Alias + "/cc:" + Name`, `Cwd=entry.Cwd`,
   full `AgentInfo`, `Deliverable=true`. Entries in `ProxyPIDs` get the same
   row with `Agent.Type="proxy"`, `Deliverable=false, Reason="proxy"`.
   Sorted after the tmux rows by `PeerName` (ties by PID).
6. An entry never produces a `cc:` row by matching a pane without a
   `sessionId` match, and a session never gets an agent from an entry whose
   `SessionID` differs from its owner's (no pane fallback).

**Tests:** one case per rule, plus:
- resumed session: two live entries, same `SessionID`, panes `%10` and
  `%11`, owner pane `%10` ⇒ `%10` chosen, deliverable.
- two live entries same `SessionID`, **both** pane `%10` ⇒ `ambiguous`.
- no pane match ⇒ `ambiguous`.
- entry with matching pane but different `SessionID` ⇒ `inbox_dead` for the
  session (rule 6), and that entry gets its own `cc:` row only if its
  `TmuxSessionName()` is not a listed session.
- entry with `Tmux:""` that rule 4 used ⇒ also appears as a `cc:` row.
- JSON assertions (`json.Marshal` then key inspection): a `not_cc` row has
  `"version":""` present; a shell row has `"agent":null`, `"reason":"no_agent"`;
  an unresolved row has `"agent":null`, `"reason":""`; every record has
  `session_code`, `session_name`, `tmux_instance`, `reason` keys.
- golden reproduction of the spec's mlab state (`mt1` cc live; `aigora3`
  shell; one outside-tmux cc entry; one codex session with `Status:"busy"`).
- `Build` with no sessions and no entries ⇒ `[]PeerRecord{}` (non-nil,
  empty — marshals to `[]`).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): build peer records from sessions, owners and registry`

### Task 3: Address resolution

**Files:** create `internal/peers/address.go`, `internal/peers/address_test.go`.

**Produce:**
```go
var ErrNotFound = errors.New("peer not found")
type AmbiguousError struct{ Session string; Candidates []PeerRecord }
func (e *AmbiguousError) Error() string

// Resolve implements spec §4.1 for ONE host's records; host has already
// been matched by the caller. Tiers, in order: tmux session name; session
// code; "cc:<peer_name>". The first tier with ≥1 match decides: exactly one
// ⇒ that record; several ⇒ *AmbiguousError (never falls through to a lower
// tier). No tier matches ⇒ ErrNotFound. Empty session ⇒ ErrNotFound.
func Resolve(records []PeerRecord, session string) (PeerRecord, error)
// SplitAddress: "<host>/<session>" → host, session, ok; ok=false when there
// is no '/', either side is empty, or session contains another '/'.
func SplitAddress(addr string) (host, session string, ok bool)
// HostMatches: want equals alias or hostID, case-insensitively (strings.EqualFold).
func HostMatches(want, alias, hostID string) bool
```
Matching of session name / code / peer name is exact and case-sensitive.
`cc:` matches any record whose `Agent != nil && Agent.PeerName == name &&
Agent.Type != "proxy"` — including cc sessions inside tmux.

**Tests:** each tier resolves; name tier beats code tier when one record's
name equals another record's code; two records with the same name ⇒
`AmbiguousError` with both, and a third record whose *code* equals that
name is **not** consulted; `cc:` resolves a tmux-hosted cc session; `cc:`
skips proxy rows; `cc:` on a name two records share ⇒ ambiguous; unknown ⇒
`ErrNotFound`; `""` ⇒ `ErrNotFound`; `SplitAddress` cases (`a/b` ok,
`a/`, `/b`, `ab`, `a/b/c` not ok); `HostMatches("Mini-Lab","mini-lab",
"mini-lab:278cbm")` and `("MINI-LAB:278CBM", …)` true, `("mini", …)` false.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): resolve human addresses to peer records`

# Phase B — Daemon wiring

### Task 4: `PaneOwner.Status` and the owner-resolver service

**Files:** modify `internal/module/agent/pane_owner.go` (struct at line 18;
construction at line 149) and `internal/module/agent/module.go` (`Init`,
immediately after `c.Registry.Register("agent.module", m)` at ~line 215);
create `internal/module/agent/owner_resolver.go`,
`internal/module/agent/owner_resolver_test.go`.

**Produce:**
```go
// pane_owner.go: add to PaneOwner
Status string // string(frame.Status) — the owning frame's Purdex agent status

// owner_resolver.go
const OwnerResolverKey = "agent.owner-resolver"
type OwnerResolver interface {
    ResolveSessionOwner(ctx context.Context, code string) (PaneOwner, bool)
}
func (m *Module) ResolveSessionOwner(ctx context.Context, code string) (PaneOwner, bool) {
    return m.resolveSessionOwner(ctx, code)
}
```
`Init` registers `c.Registry.Register(OwnerResolverKey, OwnerResolver(m))`
right after `"agent.module"`. It sits **after** the session-provider check,
so with no session provider it is not registered (same as the other
services) — the peers module hard-asserts it instead.

**Tests (`package agent`, reuse existing helpers — read
`handler_test.go:67` `newTestModule` and `provenance_handler_test.go:27`
`newProvenanceQueryModule`, `codeOf`, `attachPane` before writing):**
- service registration: build `c := core.New(core.CoreDeps{Config: &cfg,
  Tmux: fake, Registry: core.NewServiceRegistry()})` (mirror an existing
  test that constructs a Core in this package; if none does, construct
  exactly as `cmd/pdx/main.go` does minus the stores), register a
  `fakeFastSessionProvider` under `session.RegistryKey`, call `m.Init(c)`,
  assert `c.Registry.Get(OwnerResolverKey)` yields an `OwnerResolver`.
- delegation, unknown code ⇒ `found=false`.
- delegation, known owner: replicate the smallest existing
  `provenance_handler_test.go` success case (a pane attached to a session
  with a verified root frame) through `ResolveSessionOwner` and assert
  `found=true`, `AgentType`, `SessionID`, and `Status` equal to the frame's
  status string.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(agent): expose session owner resolver with frame status`

### Task 5: `peers.alias` config

**Files:** modify `internal/config/config.go` (+ `config_test.go`).

**Produce:**
```go
type PeersConfig struct {
    Alias string `toml:"alias" json:"alias"`
}
// Config gains: Peers PeersConfig `toml:"peers" json:"peers"`
// PeerAlias returns Peers.Alias, or HostID up to the first ':' when unset.
func (c Config) PeerAlias() string
```
**Tests:** unset + `HostID "mini-lab:278cbm"` ⇒ `"mini-lab"`; set ⇒ as set;
host id without `:` ⇒ whole id; empty host id ⇒ `""`; round-trips through
`WriteFile` (`internal/config/hostid.go:73`) then `Load`.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(config): peers.alias with host_id-derived default`

### Task 6: `PeerRouteAuth` — `/api/peers` never open (before any route exists)

**Files:** create `internal/middleware/peer_route_auth.go`,
`internal/middleware/peer_route_auth_test.go`; modify `cmd/pdx/main.go`
(middleware chain, ~lines 195-205).

**Spec:** §4.6. On `/api/peers` and every sub-path: a non-empty admin token
must be presented as a Bearer header (constant-time compare, prefix
case-insensitive, as `TokenAuth`), `?ticket=` is never accepted and the
ticket validator is never consulted, and an **empty** configured admin token
yields 401. Other paths pass to `next` unchanged.

**Produce:**
```go
// PeerRouteAuth guards prefix (and prefix + "/…") — see spec §4.6.
func PeerRouteAuth(prefix string, tokenFn func() string) func(http.Handler) http.Handler
```
Prefix match: `path == prefix || strings.HasPrefix(path, prefix+"/")`.

**Wiring in main.go:**
```go
middleware.PairingGuard(...)(
    middleware.PeerRouteAuth("/api/peers", tokenFn)(
        middleware.TokenAuth(tokenFn, c.Tickets)(mux)))
```
where `tokenFn` is the existing closure hoisted into a variable. IPWhitelist,
PairingGuard, CORS and the `/api/health` exception are untouched.

**Tests:**
- unit: correct bearer ⇒ next called; wrong / missing ⇒ 401; `?ticket=x` ⇒
  401 and next not called; empty admin token ⇒ 401; `/api/peers/hosts` ⇒
  guarded; `/api/peersx`, `/api/sessions` ⇒ next called without checks.
- **composition** (the exact chain from main.go, with a ticket validator
  fake that records calls and would return true): `GET /api/peers?ticket=ok`
  ⇒ 401 and the validator was **not** called; `GET
  /api/sessions?ticket=ok` ⇒ 200 (TokenAuth path unchanged); empty admin
  token: `/api/sessions` ⇒ 200, `/api/peers` ⇒ 401.

- [ ] tests written and failing
- [ ] implementation, all green (`go build ./...` proves the wiring)
- [ ] commit `feat(middleware): lock /api/peers behind a non-empty admin bearer`

### Task 7: `peers` module and `GET /api/peers`

**Files:** create `internal/module/peers/module.go`,
`internal/module/peers/fakes_test.go`, `internal/module/peers/module_test.go`;
modify `cmd/pdx/main.go` (`registerServeModules`: `c.AddModule(peersmod.New())`
after the agent module).

**Module contract:**
```go
type Module struct {
    core        *core.Core
    sessions    session.SessionProvider
    owners      agent.OwnerResolver
    registryDir string                 // default filepath.Join(home, ".claude", "sessions")
    liveness    peers.Liveness         // default peers.DefaultLiveness()
    budget      time.Duration          // default 2 * time.Second
    now         func() time.Time       // default time.Now
}
func New() *Module
func (m *Module) Name() string            { return "peers" }
func (m *Module) Dependencies() []string  { return []string{"session", "agent"} }
func (m *Module) Init(c *core.Core) error // hard-asserts both services; error text names the missing key
func (m *Module) RegisterRoutes(mux *http.ServeMux) // "GET /api/peers"
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }
```

**Response envelope:**
```go
type response struct {
    HostID  string             `json:"host_id"`
    OK      bool               `json:"ok"`
    Error   string             `json:"error,omitempty"`
    Partial bool               `json:"partial"`
    Peers   []peers.PeerRecord `json:"peers"` // never null: []peers.PeerRecord{} when empty
}
```

**Handler steps:**
1. `deadline := m.now().Add(m.budget)` — first statement.
2. `?scope=all` ⇒ HTTP 400 `{"error":"scope=all not supported yet"}`.
3. Read `HostID`, `PeerAlias()` under `c.CfgMu.RLock`.
4. `ListSessions()` error ⇒ HTTP 200 `{ok:false, error, partial:false, peers:[]}`.
5. `ReadRegistry(registryDir, liveness)` error ⇒ same shape.
6. For each session in list order: if `!m.now().Before(deadline)` ⇒ add to
   `Unresolved`; else `owner, ok := m.owners.ResolveSessionOwner(r.Context(),
   code)`; `ok` ⇒ `Owners[code] = Owner{…, Status: owner.Status}`.
   `partial = len(Unresolved) > 0`.
7. `peers.Build(...)`; encode `{ok:true}`.

**`fakes_test.go`:** a `fakeSessions` implementing the full
`session.SessionProvider` (`ListSessions`, `GetSession`, `UpdateMeta`,
`HandleTerminalWS`, `TmuxInstance` — read `internal/module/session/provider.go`
for the exact signatures) returning canned `SessionInfo`s or an error; a
`fakeOwners` with a `map[string]agent.PaneOwner` and a call log; a
`fakeClock` with a settable sequence of times.

**Tests:**
- happy path: sessions `mt1` (cc owner, live entry in temp registry) and
  `aigora3` (no owner) ⇒ `ok:true, partial:false`; `mt1` deliverable with
  `peer_name`; `aigora3` `reason:"no_agent"`.
- soft budget: clock returns `t0` at handler start, then `t0+3s` on the
  next call ⇒ first session resolved (resolver called once), second
  `Unresolved` (resolver **not** called), `partial:true`; the second
  record has `agent:null, reason:""`.
- budget consumed before any session: clock `t0, t0+3s` where the second
  read happens at the first session ⇒ all unresolved, `partial:true`.
- boundary: second read exactly `t0+2s` ⇒ treated as expired.
- provider error ⇒ `ok:false`, `peers:[]` (assert the JSON has `"peers":[]`
  not `null`).
- registry dir missing ⇒ `ok:false`.
- `scope=all` ⇒ 400.
- `Init` with an empty registry ⇒ error mentioning `session.provider`; with
  only sessions ⇒ error mentioning `agent.owner-resolver`.

- [ ] tests written and failing
- [ ] implementation, all green (`go build ./...` proves main.go wiring)
- [ ] commit `feat(peers): GET /api/peers local inventory endpoint`

# Phase C — CLI

### Task 8: `pdx peers [--json]`

**Files:** create `cmd/pdx/peers.go`, `cmd/pdx/peers_test.go`; modify
`cmd/pdx/main.go` (switch + usage line).

**Types and seams (own copies — `cmd/pdx` must not import
`internal/module/peers`):**
```go
type peersResponse struct {
    HostID  string             `json:"host_id"`
    OK      bool               `json:"ok"`
    Error   string             `json:"error"`
    Partial bool               `json:"partial"`
    Peers   []peers.PeerRecord `json:"peers"`
}
// runPeersCmd does all the work and returns the exit code; runPeers (the
// switch target) is `os.Exit(runPeersCmd(args, os.Stdout, os.Stderr))`.
func runPeersCmd(args []string, stdout, stderr io.Writer) int
func formatPeersTable(resp peersResponse) string
```
Config: parse `--config <path>` from args; `cfg, err := config.Load(path)`;
error ⇒ stderr, exit 1 (do not use `parseConfigPath`, it calls
`log.Fatalf`). URL `http://<Bind>:<Port>/api/peers`; header `Authorization:
Bearer <Token>`; `http.Client{Timeout: 10 * time.Second}`.

**Behaviour and exit codes:**
| Situation | stdout | stderr | exit |
|---|---|---|---|
| transport error / non-200 | – | `pdx peers: <detail>` | 1 |
| body not JSON | – | `pdx peers: invalid response` | 1 |
| `--json` | raw body verbatim | – | `0` if `ok`, else `1` |
| table, `ok:true` | table (+ partial line) | – | 0 |
| table, `ok:false` | – | `pdx peers: <error>` | 1 |

Table columns: `ADDRESS  AGENT  NAME  STATUS  DELIVERABLE  CWD` via
`text/tabwriter`; `AGENT` = `agent.type` or `-`; `NAME` = `peer_name` or
`-`; `STATUS` = `agent.status` or `-`; `DELIVERABLE` = `yes` or `reason`
(or `-` when `reason==""` and `agent==nil`, i.e. unresolved). Partial line:
`(partial: N sessions not resolved within budget)` where N counts records
with `Agent == nil && Reason == ""`.

**Tests:** golden `formatPeersTable` on a fixture with a deliverable cc row,
a `not_cc` codex row, a shell row and an unresolved row (N=1, shell not
counted); `runPeersCmd` against an `httptest.Server` with a temp config
file pointing at it — assert the request path and `Authorization` header —
for: 200 table exit 0; `--json` passthrough exit 0; 200 `ok:false` table ⇒
exit 1 with error on stderr; `--json` with `ok:false` ⇒ body printed, exit
1; 401 ⇒ exit 1; invalid JSON ⇒ exit 1; unreachable server ⇒ exit 1.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(cli): pdx peers lists local agent peers`

# Phase D — Acceptance (main session, not a subagent)

### Task 9: Live check on mlab with an isolated daemon

Never point a second daemon at the production `data_dir`.

- [ ] `go build -o bin/pdx ./cmd/pdx` in the worktree.
- [ ] Write `/tmp/pdx-p1/config.toml`:
  `host_id="p1-test:abc123"`, `bind="127.0.0.1"`, `port=7861`,
  `token="p1test"`, `data_dir="/tmp/pdx-p1/data"`,
  `upload_dir="/tmp/pdx-p1/upload"`. Start `./bin/pdx serve --config
  /tmp/pdx-p1/config.toml` in a tmux window `pdx-p1`.
- [ ] `./bin/pdx peers --config /tmp/pdx-p1/config.toml` — every tmux
  session listed; this session's row shows `cc`, `purdex-47`,
  `deliverable yes`; shell rows `no_agent`; `--json` output validates
  against spec §4.2 keys.
- [ ] `curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:7861/api/peers`
  ⇒ `401`; same with `?ticket=anything` ⇒ `401`; with `-H 'Authorization:
  Bearer p1test'` ⇒ `200`.
- [ ] Stop the daemon (`Ctrl-C` in the window, then `tmux kill-window -t
  pdx-p1`), `rm -rf /tmp/pdx-p1`.
- [ ] Paste the table and the three curl codes into the PR description.
