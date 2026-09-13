# Peer Bridge P1 Implementation Plan — Local inventory

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `pdx peers` and `GET /api/peers` list every agent session on the
local host as a peer record (spec §4.2) with a resolvable address (spec §4.1),
joining Purdex's tmux + agent-owner knowledge with Claude Code's own session
registry. Read-only; nothing is written to `~/.claude`.

**Architecture:** A new leaf package `internal/peers` holds everything pure:
registry parsing + liveness, the join that produces `PeerRecord`s, and
address resolution. A new daemon module `internal/module/peers` wires it to
the session provider and to an owner-resolver service the agent module
starts exporting, and serves the endpoint under a 2 s budget. The CLI is a
thin client of the endpoint.

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
  `/Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-peer-bridge`;
  every Edit/Write uses that absolute prefix.
- **Never touch `~/.claude/sessions` in tests.** The registry directory is a
  parameter everywhere; tests use `t.TempDir()`.
- **No `ps` forks in unit tests.** Liveness and start-time lookups are
  injected function fields with defaults; tests substitute fakes.
- **Existing tests are not edited.** If a task cannot pass without changing an
  existing test, stop and report.
- **Go test packages:** new packages use internal tests (`package peers`,
  `package peers` under `internal/module/peers`). `internal/module/agent`
  tests are internal (`package agent`).

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/peers/registry.go` *(new)* | Parse `~/.claude/sessions/<pid>.json`, liveness (`Entry`, `ReadRegistry`) |
| `internal/peers/record.go` *(new)* | `PeerRecord`, `AgentInfo`, `Build(...)` — the pure join |
| `internal/peers/address.go` *(new)* | `Resolve(records, addr)` — spec §4.1 human-address resolution |
| `internal/module/agent/owner_resolver.go` *(new)* | `OwnerResolver` interface + registration under `agent.owner-resolver` |
| `internal/module/peers/module.go` *(new)* | Module wiring, `GET /api/peers`, soft budget + envelope |
| `internal/middleware/peer_route_auth.go` *(new)* | `PeerRouteAuth` — /api/peers never open, no tickets |
| `internal/config/config.go` *(modify)* | `PeersConfig{Alias}` + `(Config).PeerAlias()` |
| `cmd/pdx/main.go` *(modify)* | register module; `peers` subcommand |
| `cmd/pdx/peers.go` *(new)* | `pdx peers [--json]` client + table formatter |

---

# Phase A — Pure package `internal/peers`

### Task 1: Registry reader with injected liveness

**Files:** create `internal/peers/registry.go`, `internal/peers/registry_test.go`.

**Interfaces (produce):**
```go
package peers

// Entry is one live Claude Code session as its own registry describes it.
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

// TmuxSessionName returns the "<session>" part of Tmux, or "".
func (e Entry) TmuxSessionName() string
// TmuxPaneID returns the "%<pane>" part of Tmux, or "".
func (e Entry) TmuxPaneID() string

type Liveness struct {
    Stat      func(path string) error            // default os.Stat wrapper
    PidAlive  func(pid int) bool                 // default syscall.Kill(pid, 0) == nil
    StartTime func(pid int) (time.Time, error)   // default agent.ReadProcessInfo(pid).StartTime
}
func DefaultLiveness() Liveness

// ReadRegistry parses every "<pid>.json" in dir and returns the live ones.
// Unreadable / malformed / dead entries are skipped and counted in skipped.
func ReadRegistry(dir string, live Liveness) (entries []Entry, skipped int, err error)
```

**Rules:**
- File name must match `^(\d+)\.json$`; the `pid` field must equal the name.
- Live iff `Stat(Inbox) == nil` && `PidAlive(pid)` && `StartTime(pid)`
  equals `ProcStart` parsed with layout `"Mon Jan _2 15:04:05 2006"` in UTC
  (`time.ParseInLocation(..., time.UTC)`), compared with `Equal` after
  truncating both to seconds. A `StartTime` error ⇒ not live.
- `err` is non-nil only when `dir` cannot be listed.

**Tests (write first):**
- parses a fixture identical to the spec §3.1 shape (copy the real field set:
  `pid, sessionId, cwd, startedAt, procStart, version, peerProtocol,
  peerFeatures, kind, entrypoint, pidDomain, tmux, messagingSocketPath, name,
  nameSource, nameSince, updatedAt, status, statusUpdatedAt`) → one Entry with
  every field mapped, `TmuxSessionName()=="mt1"`, `TmuxPaneID()=="%10"`.
- dead pid ⇒ skipped; missing socket ⇒ skipped; start-time mismatch by 1 s ⇒
  skipped; equal ⇒ live.
- `procStart` with local-vs-UTC difference: the fixture `"Sun Sep 13 15:22:36
  2026"` matches a fake `StartTime` returning `2026-09-13T15:22:36Z`.
- malformed JSON, pid mismatch, `.key` files and `.json.tmp` ⇒ skipped, no
  error; missing dir ⇒ `err != nil`.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): read Claude Code session registry with liveness`

### Task 2: Pure join → `PeerRecord`

**Files:** create `internal/peers/record.go`, `internal/peers/record_test.go`.

**Interfaces (consume):** `Entry` (Task 1). **Produce:**
```go
type SessionSummary struct { // what the module extracts from session.SessionInfo
    Code, Name, Cwd, TmuxInstance string
}
type Owner struct {          // what the module extracts from agent.PaneOwner
    AgentType, SessionID, Cwd, TmuxPaneID string
    LastSeenAt int64
    Status string            // "" in P1 (Purdex agent status is not surfaced yet)
}
type AgentInfo struct {
    Type      string `json:"type"`       // cc | codex | opencode | proxy
    SessionID string `json:"session_id,omitempty"`
    PeerName  string `json:"peer_name,omitempty"`
    PID       int    `json:"pid,omitempty"`
    ProcStart string `json:"proc_start,omitempty"`
    Inbox     string `json:"inbox,omitempty"`
    Status    string `json:"status,omitempty"`
    Version   string `json:"version,omitempty"`
}
type PeerRecord struct {
    Host         string     `json:"host"`
    HostID       string     `json:"host_id"`
    Address      string     `json:"address"`
    SessionCode  string     `json:"session_code"`
    SessionName  string     `json:"session_name"`
    TmuxInstance string     `json:"tmux_instance"`
    Cwd          string     `json:"cwd,omitempty"`
    Agent        *AgentInfo `json:"agent"`
    Deliverable  bool       `json:"deliverable"`
    Reason       string     `json:"reason"`   // "" | no_agent | not_cc | inbox_dead | proxy | ambiguous
}
type BuildInput struct {
    HostID, Alias string
    Sessions      []SessionSummary
    Owners        map[string]Owner   // by session code; absent ⇒ not resolved
    Unresolved    map[string]bool    // codes whose owner lookup hit the deadline
    Entries       []Entry
    ProxyPIDs     map[int]bool       // empty in P1; kept so P3 needs no signature change
}
func Build(in BuildInput) []PeerRecord
```

**Rules (spec §4.2):**
1. One record per session, sorted by `SessionName`. `Address =
   Alias + "/" + SessionName`.
2. Owner absent and code in `Unresolved` ⇒ `Agent: nil, Reason: ""` (the
   envelope's `partial` flag says why). Owner absent otherwise ⇒ `Agent: nil,
   Reason: "no_agent"`.
3. Owner type `codex` / `opencode` ⇒ `Agent{Type, SessionID, Status}`,
   `Deliverable=false, Reason="not_cc"`.
4. Owner type `cc`: candidates = live entries with `SessionID ==
   owner.SessionID`. 0 ⇒ `Agent{Type:"cc", SessionID}` only,
   `Reason="inbox_dead"`. 1 ⇒ full `AgentInfo`, `Deliverable=true`. >1 ⇒
   the entry whose `TmuxPaneID()` equals `owner.TmuxPaneID` is used **iff
   it is the only such entry**; otherwise `Reason="ambiguous"`, `Agent`
   filled from the owner only (spec §4.2, R2 #9).
5. Entries not consumed by rule 4 and whose `TmuxSessionName()` is not any
   listed session ⇒ extra records: `SessionCode=""`, `SessionName=""`,
   `Address = Alias + "/cc:" + Name`, `Cwd=entry.Cwd`, full `AgentInfo`,
   `Deliverable=true`. Sorted after the tmux rows by `PeerName`.
6. Any entry whose PID is in `ProxyPIDs` ⇒ `Agent.Type="proxy"`,
   `Deliverable=false, Reason="proxy"`, never produces a `cc:` row.
7. `AgentInfo.Status` for cc comes from the entry; for others from the owner.

**Tests:** one table per rule above, plus a golden test that reproduces the
mlab state in the spec (`mt1` cc live; `aigora3` shell only; an outside-tmux
cc entry; a resumed session with two entries where the pane tiebreak picks
one; a two-entry case with no pane match ⇒ ambiguous).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): build peer records from sessions, owners and registry`

### Task 3: Address resolution

**Files:** create `internal/peers/address.go`, `internal/peers/address_test.go`.

**Produce:**
```go
var ErrNotFound = errors.New("peer not found")
type AmbiguousError struct{ Candidates []PeerRecord }
func (e *AmbiguousError) Error() string

// Resolve implements spec §4.1 for ONE host's records. host is the part
// before "/", already matched by the caller; session is the part after.
func Resolve(records []PeerRecord, session string) (PeerRecord, error)
// SplitAddress returns host, session for "<host>/<session>"; ok=false when
// there is no "/" or either side is empty.
func SplitAddress(addr string) (host, session string, ok bool)
// HostMatches reports whether want equals alias or hostID, case-insensitively.
func HostMatches(want, alias, hostID string) bool
```

**Rules:** match order tmux name → code → `cc:<peer_name>`; a name that
matches more than one record at the same tier ⇒ `AmbiguousError` with all
candidates; a `cc:` form never matches proxy rows; matching is exact and
case-sensitive for session/code/peer name.

**Tests:** each tier, ambiguity at each tier, `cc:` skipping proxy rows,
`SplitAddress` edge cases, `HostMatches` case-insensitivity.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): resolve human addresses to peer records`

# Phase B — Daemon wiring

### Task 4: Agent module exports an owner resolver

**Files:** create `internal/module/agent/owner_resolver.go`,
`internal/module/agent/owner_resolver_test.go`; modify
`internal/module/agent/module.go` (`Init`, next to the
`c.Registry.Register("agent.module", m)` line, ~215).

**Produce:**
```go
// OwnerResolverKey is the service registry key the peers module looks up.
const OwnerResolverKey = "agent.owner-resolver"

type OwnerResolver interface {
    ResolveSessionOwner(ctx context.Context, code string) (PaneOwner, bool)
}
```
`(*Module).ResolveSessionOwner` is a one-line exported wrapper around the
existing `resolveSessionOwner` (`provenance_handler.go:93`); the module
registers `OwnerResolver(m)` under `OwnerResolverKey` in `Init`.

**Tests:** after `Init` on a module built the way the existing
`provenance_handler_test.go` builds one, `c.Registry.Get(OwnerResolverKey)`
returns a value asserting to `OwnerResolver`, and calling it on an unknown
code returns `found=false` (delegation, no behaviour change).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(agent): expose session owner resolver as a service`

### Task 5: `peers.alias` config

**Files:** modify `internal/config/config.go` (+ its test file).

**Produce:**
```go
type PeersConfig struct {
    Alias string `toml:"alias" json:"alias"`
}
// on Config:  Peers PeersConfig `toml:"peers" json:"peers"`
// PeerAlias returns Peers.Alias, or HostID up to the first ':' when unset.
func (c Config) PeerAlias() string
```
**Tests:** unset ⇒ `"mini-lab"` from `"mini-lab:278cbm"`; set ⇒ as set;
host id without `:` ⇒ whole id; round-trips through `WriteFile`/`Load`.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(config): peers.alias with host_id-derived default`

### Task 6: `peers` module and `GET /api/peers`

**Files:** create `internal/module/peers/module.go`,
`internal/module/peers/module_test.go`; modify `cmd/pdx/main.go`
(`registerServeModules`: `c.AddModule(peersmod.New())` after `agent`).

**Module contract:**
- `Name()="peers"`, `Dependencies()=[]string{"session","agent"}`.
- `Init`: `c.Registry.Get(session.RegistryKey)` → `session.SessionProvider`;
  `c.Registry.Get(agent.OwnerResolverKey)` → `agent.OwnerResolver`. Either
  missing ⇒ `Init` returns an error (hard assert, as
  `session/handler.go:415` does). Stores `c` for config access
  (`c.CfgMu.RLock` on every request: `HostID`, `PeerAlias()`).
- Fields with defaults, overridable in tests: `registryDir` (default
  `filepath.Join(home, ".claude", "sessions")`), `liveness`
  (`peers.DefaultLiveness()`), `budget` (2 s), `now`.
- `RegisterRoutes`: `GET /api/peers`.

**Handler:**
```go
type response struct {
    HostID  string             `json:"host_id"`
    OK      bool               `json:"ok"`
    Error   string             `json:"error,omitempty"`
    Partial bool               `json:"partial"`
    Peers   []peers.PeerRecord `json:"peers"`
}
```
1. `ListSessions()` error ⇒ `ok:false, error, peers:[]`, HTTP 200.
2. `ReadRegistry` error ⇒ same.
3. **Soft budget** (spec §4.2, R2 #8): `deadline := now().Add(budget)`; for
   each session in list order, if `now().After(deadline)` put the code in
   `Unresolved` and skip, else call `ResolveSessionOwner(r.Context(), code)`
   and let it finish (the existing resolver cannot interrupt a process read;
   its own 5 s `provenanceTimeout` still bounds it). `partial =
   len(Unresolved) > 0`. No cross-session memo in P1.
4. `peers.Build(...)`, encode.
5. `?scope=all` in P1 ⇒ HTTP 400 `{"error":"scope=all not supported yet"}`
   so P2 can claim it without a silent behaviour change.

**Tests (httptest, fakes for provider and resolver, temp registry dir):**
- happy path: two sessions, one cc owner with a live entry ⇒ deliverable
  record; one shell ⇒ `no_agent`; envelope `ok:true, partial:false`.
- resolver that blocks until ctx is done, budget 50 ms ⇒ `partial:true`,
  the blocked session has `agent:null`, the others resolved.
- provider error ⇒ `ok:false`.
- `scope=all` ⇒ 400.
- `Init` without the resolver service ⇒ error.
- budget test: a fake `now` that jumps past the deadline after the first
  resolver call ⇒ second session `Unresolved`, `partial:true`, first session
  resolved.

- [ ] tests written and failing
- [ ] implementation, all green (`go build ./...` proves main.go wiring)
- [ ] commit `feat(peers): GET /api/peers local inventory endpoint`

### Task 6b: `PeerRouteAuth` — peer routes never open

**Files:** create `internal/middleware/peer_route_auth.go`,
`internal/middleware/peer_route_auth_test.go`; modify `cmd/pdx/main.go`
(the middleware chain at ~195-205).

**Spec:** §4.6 (R2 #2). On `/api/peers` and everything under it: a non-empty
admin token is required as a Bearer header, `?ticket=` is never accepted, and
an **empty** configured admin token yields 401 (unlike `TokenAuth`, which
opens every route when the token is empty). Every other path passes through
untouched to the existing chain.

**Produce:**
```go
// PeerRouteAuth guards the /api/peers prefix. It never opens the routes when
// the admin token is unset and never accepts one-time tickets. Requests
// outside the prefix are passed to next unchanged (TokenAuth still runs).
func PeerRouteAuth(prefix string, tokenFn func() string) func(http.Handler) http.Handler
```
Wiring: `PeerRouteAuth("/api/peers", tokenFn)(TokenAuth(tokenFn, c.Tickets)(mux))`
— placed *inside* `PairingGuard`, *outside* `TokenAuth`, so IPWhitelist,
PairingGuard and the `/api/health` exception are unchanged. A request that
passes `PeerRouteAuth` also passes `TokenAuth` (same bearer), so the double
check costs nothing and keeps `TokenAuth` untouched.

**Tests:**
- `/api/peers` with correct bearer ⇒ next called; wrong bearer ⇒ 401; no
  header ⇒ 401; `?ticket=x` with a validator that would accept it ⇒ 401;
  empty admin token ⇒ 401.
- `/api/peersx` and `/api/sessions` ⇒ passed to next regardless (prefix
  match is on path segments: `/api/peers` and `/api/peers/…`, not
  `/api/peersx`).
- Constant-time comparison (`crypto/subtle`), Bearer prefix
  case-insensitive, same as `TokenAuth`.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(middleware): lock /api/peers behind a non-empty admin bearer`

# Phase C — CLI

### Task 7: `pdx peers [--json]`

**Files:** create `cmd/pdx/peers.go`, `cmd/pdx/peers_test.go`; modify
`cmd/pdx/main.go` (switch + usage line).

**Behaviour:**
- `parseConfigPath(args)` for `--config`; URL
  `http://<Bind>:<Port>/api/peers`; header `Authorization: Bearer <Token>`;
  10 s client timeout. Non-200 or transport error ⇒ message on stderr,
  exit 1.
- `--json` ⇒ raw body to stdout.
- Default ⇒ table: `ADDRESS  AGENT  NAME  STATUS  DELIVERABLE  CWD`, one row
  per record; `AGENT` is `agent.type` or `-`; `NAME` is `peer_name` or `-`;
  `DELIVERABLE` is `yes` or the `reason`. A trailing line `(partial: N
  sessions not resolved within budget)` when `partial`. `ok:false` ⇒ the
  error on stderr, exit 1.
- Column widths computed from content (`text/tabwriter`).

**Tests:** `formatPeersTable(response) string` golden test on a three-record
fixture; `runPeers` against an `httptest.Server` for 200/JSON, 200/table,
401 (exit code captured through an injectable `exit` func like
`hook.go`'s pattern), and `--json` passthrough.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(cli): pdx peers lists local agent peers`

# Phase D — Acceptance (main session, not a subagent)

### Task 8: Live check on mlab

- [ ] `go build -o bin/pdx ./cmd/pdx` in the worktree; run
  `./bin/pdx peers --config ~/.config/pdx/config.toml` against the **running
  main daemon** — expect HTTP 404 (old daemon has no route); this confirms
  the CLI error path. Then start a second daemon from the worktree binary
  on a scratch port (`pdx serve --config <tmp config with port 7861, same
  data_dir read-only use>`) only if the running daemon cannot be restarted;
  otherwise restart the main daemon per `reference_pdx_daemon_runtime` and
  run `pdx peers`.
- [ ] Confirm: every tmux session listed; this session's row shows `cc`,
  `purdex-47`, `deliverable yes`; `aigora3`-style shell rows show `no_agent`;
  `curl -s -o /dev/null -w '%{http_code}' http://100.64.0.2:7860/api/peers`
  (no bearer) and the same with `?ticket=anything` both print `401`;
  `--json` validates against §4.2.
- [ ] Record the output in the PR description.
