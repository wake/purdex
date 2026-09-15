# Spec — P-A: embed Nexen as the `nex` module of the pdx daemon

Status: v2 (v1 → v2 after codex spec review `task-mu1mhggb-c9tm86`; 5 must-fix,
7 should-fix, 1 nit — all dispositioned in §10)
Date: 2026-09-15
Branch: `worktree-pa-nex-module`
Scope: daemon (Go) + `pdx` CLI. **No SPA change, no removal of existing
modules.** Depends on Nexen N1 (`lab.protype.tw/wake/nexen`, spec
`docs/specs/2026-09-15-n1-library-seams-spec.md` in that repo).

Series: N1 (Nexen seams) → **P-A (this)** → P-B (execution pane) → P-B2
(rendering polish) → P-C (launch UI + handoff) → P-D (remove stream / relay /
bridge / M0 execution+dispatch). Decisions that bind the whole series are
recorded in the session memory `kickoff_nexen_into_purdex`; this spec does
not re-argue them.

## 1. Problem

Purdex needs a headless-agent execution engine on every host it already runs
on (mlab, a26): commander sessions that fan out `claude -p` workers, phone
takeover of a desktop session, Aigora escalations. Nexen is that engine, but
it is a separate daemon with its own deployment, and the user will not
maintain a second cross-host deployment. Purdex's own headless path (stream
handoff via `pdx relay`, plus the never-enabled M0 `execution`/`dispatch`
modules) is a weaker duplicate of Nexen's adapter.

P-A puts Nexen inside `pdx` as one more module, so the existing pdx
deployment path (local daemon install, dev update) carries it, and every
later phase (SPA pane, launch UI, Aigora, Swift) talks to one contract at
`/api/nex/v1/*` on whichever host owns the execution.

## 2. Goals / non-goals

### Goals

1. A `pdx` binary built from this branch serves Nexen's full v1 HTTP API
   under `/api/nex/` on the daemon's existing listener, authenticated by the
   daemon's existing token chain.
2. `nex --addr http://<host>:7860/api/nex --token <pdx token> …` and the new
   `pdx nex …` wrapper both work unchanged against it.
3. Persisted execution state survives a pdx restart the way it survives a
   `nex daemon` restart (§4.5 states exactly what is and is not preserved).
4. Nexen is opt-in per host (`[nex] enabled`). Once enabled, an
   initialization failure is a daemon start failure — same rule as every
   other module — and a **request-time** panic inside Nexen is contained to
   that request. No stronger isolation is claimed.

### Non-goals (explicitly later phases)

- Any SPA change (P-B / P-C). This phase is verified with `pdx nex` and
  `curl` only. **But** P-A fixes the transport P-B will use (§4.3), because
  the choice affects CORS and auth here.
- Removing `stream`, `relay`, `bridge`, `execution`, `dispatch`, `history`
  (P-D). They keep running side by side; the M0 `execution` module keeps its
  route `GET /api/execution/{id}` — no route collides with `/api/nex/`.
- Cross-host `pdx nex --host <alias>` from the CLI. Peer credentials are
  scoped to `/api/peers` by design (peer-bridge spec §4); a remote host's
  general API needs that host's own token, which the local daemon does not
  hold. The SPA already has per-host tokens, so cross-host works from the UI
  in P-B without server-side proxying. Revisit if the commander scenario
  needs CLI fan-out across hosts.
- Multi-account (`claude_accounts`). N1 keeps it in `config.Config`, but pdx
  does not expose it in P-A: its tokens come from `<ID>_CLAUDE_CODE_OAUTH_TOKEN`
  environment variables which a launchd/Finder-started daemon does not have.
  Single ambient account only; the field is left zero.
- Editing `[nex]` through `PUT /api/config` (§4.2 says what P-A does with
  it). The Settings UI for it is P-C.
- Hot reload of `[nex]`: the module reads it once at Init; a change needs a
  daemon restart.
- A sidecar process model to keep turns alive across pdx restarts.
- Mirroring Nexen events onto Purdex's host-event WebSocket. Consumers use
  Nexen's own SSE (`/api/nex/v1/events`).
- A per-child environment seam in Nexen (§4.6 explains the process-wide
  workaround and why it is the interim answer).

## 3. What N1 delivers that this phase consumes

| N1 symbol | Used for |
|---|---|
| `nexen.Assemble(ctx, Options{Config, Auth, PublicPrefix, ClaudeBin}) (*System, error)` | the whole wiring; **`Options.Config` must already be validated** (N1 §4.4.2) |
| `(*System).Handler` (auth-wrapped mux, no webui) / `.Shutdown(ctx)` / `.Close()` | mount + lifecycle |
| `config.Config` + `(*Config).Validate()` | built from `[nex]`, validated by pdx **before** Assemble, never loaded from a TOML file |
| `api.AuthenticatorFunc` | pdx principal |
| `api.Deps.PublicPrefix` semantics | rendered paths carry `/api/nex`; routing does not |
| `sandbox.ValidName` | validating `[nex.sandbox]` names at pdx config load |
| `cmd/nex/client.Run(out, args, baseURL, token)` | the `pdx nex` wrapper |

### 3.1 Module resolution and build

`go.mod` requires `lab.protype.tw/wake/nexen` at a pseudo-version of the N1
merge commit and moves to `go 1.26.0` (Nexen's floor; toolchain 1.26.0 is
installed). `modernc.org/sqlite` resolves to Nexen's 1.54.0 by MVS; the
existing pdx store tests (`internal/store/*_test.go`, incl. `pragma_test.go`)
are the regression net.

No `replace` in `go.mod`. A gitignored `go.work` pointing at a local Nexen
checkout is the dev-loop mechanism (`go.work` and `go.work.sum` are added to
`.gitignore`).

The repo is private on Gitea: the go-import meta is served unauthenticated
but the https clone returns 401 (N1 §4.3). Two **machine-level, one-time**
settings on every machine that builds pdx (today only mlab) make every
`go build` work regardless of who launched the process:

```sh
go env -w GOPRIVATE=lab.protype.tw          # persisted in $(go env GOENV), no shell env needed
git config --global url."ssh://git@lab.protype.tw:9079/".insteadOf "https://lab.protype.tw/"
```

`go env -w` rather than `Makefile`/shell exports because the daemon's own
dev-update build (`internal/module/dev/build.go`) runs `go build` with the
daemon's inherited environment, which for a Finder/launchd-started daemon
has no shell profile. Both settings are documented in `README.md` (build
section) with the acceptance command from §6 step 0; `Makefile` gains a
`check-goenv` target that fails fast with the two commands printed if either
is missing, and `build` depends on it.

## 4. Design

### 4.1 Module shape — `internal/module/nex`

```go
// engine is the private seam over *nexen.System: the three things the
// module needs from an assembled Nexen (serve, drain, release). Tests
// substitute a fake engine through assembleFn; production goes through
// realAssemble (a thin adapter over nexen.Assemble that captures the
// *nexen.System pointer in closures — never copies it).
type engine struct {
    handler  http.Handler
    shutdown func(context.Context) error
    close    func() error
}

type Module struct {
    core *core.Core
    sys  engine        // not *nexen.System — see engine
    opts nexen.Options // built in Init, kept for tests/logging
    // + assemble / isDir / logf seams, defaulting to production values
}

func (m *Module) Name() string           { return "nex" }
func (m *Module) Dependencies() []string { return nil }
```

- `Init(c)`: in order — (1) `c.Cfg.Nex.Expanded(home)`: expand `~` in
  roots, `claude_bin`, `cswap_bin` and `path_prepend` against
  `os.UserHomeDir()` (an error here is an Init error); (2) apply the PATH
  policy (§4.6) to the expanded `path_prepend`; (3) `buildOptions`:
  translate the expanded config → `config.Config` (§4.2) and run
  `cfg.Validate()`; (4) `MkdirAll(<DataDir>/nex)`; (5) `assemble` →
  `nexen.Assemble(ctx, opts)`. Steps 1, 3, 4 and 5 each turn an error into
  `Init`'s error (`nex: init: …`), so daemon start fails with the message
  naming the offending `[nex]` key or Nexen's own reason. The
  `ctx` handed to Assemble is `context.Background()` — Assemble's own work
  (open store, startup reconcile) must not be cancelled by the daemon's
  module ctx, which `main.go` cancels first thing on shutdown; cancellation
  of live turns is `Shutdown`'s job, not the ctx's.
  Assemble's startup reconcile marks turns without a live process
  `orphaned` and their executions `idle`, and SIGKILLs any process it finds
  still running for such a turn — exactly the standalone daemon's behaviour.
- The module is only **added** to Core when `[nex] enabled = true` (§4.2), so
  a host that never opted in has no `[nex]` code path at all.
- `RegisterRoutes(mux)`: one line of routing (§4.4).
- `Start(ctx)`: log
  `nex: serving /api/nex (host_id=…, data_dir=…, claude_bin=…, profiles=…, path_prepend=…)`
  where `path_prepend=` is the *applied* prefix (existing dirs only, joined
  by the list separator), not the full `PATH`. Nothing else — Assemble
  already started everything.
- `Stop(ctx)`: `return m.sys.shutdown(ctx)`. Returning the error is correct:
  `Core.StopModules` collects and continues (`core.go:107`), and the message
  ("not every turn's ending was confirmed inside the budget") belongs in the
  log with the module name.
- `Close() error`: `m.sys.close()`. New optional interface, see §4.5.

Nothing in the module reaches into `System.Service` / `.Store` / `.Bus`.
The Go side of pdx talks to Nexen the way every other consumer does — over
HTTP (N1 §7 recommendation). This is an invariant (§5 I6), enforced by an
import test on the package.

### 4.2 Config — `[nex]`

Complete, paste-able section with defaults:

```toml
[nex]
enabled       = false            # P-A default; the module is not even registered when false
# Admission roots (Nexen fail-closed allowlist). NOT a filesystem sandbox: a
# trusted agent can still edit files under them. "repo" roots are the user's
# checkouts the daemon never writes git history into; "service" roots are
# directories the service layer owns (e.g. Aigora rooms later).
repo_roots    = ["~/Workspace"]
service_roots = []
claude_bin    = ""               # "" = LookPath("claude") inside Assemble, after path_prepend
cswap_bin     = ""               # "" = Nexen default
# Prepended (in this order, deduplicated, existing dirs only) to the daemon
# PROCESS PATH at nex Init. [] disables. See §4.6 for why this is process-wide.
path_prepend  = ["~/.local/bin", "/opt/homebrew/bin", "/usr/local/bin"]

[nex.sandbox]
max_profile     = "trusted"      # ValidName; "handoff" must be set here explicitly to be usable
default_profile = "trusted"

[nex.timeouts]                   # optional; "0s" = Nexen default
lease_ttl = "120s"
interrupt = "0s"
turn      = "0s"
```

Mapping applied in `Init`:

| pdx | Nexen `config.Config` |
|---|---|
| `Cfg.HostID` | `HostID` (→ `nex.host` label, `capabilities.host_id`). **Empty is an Init error when nex is enabled** (`EnsureHostID` tolerates a persist failure and leaves it empty; a principal `pdx:` with nothing after the colon is not acceptable audit data, and letting Nexen's Validate substitute the hostname would make the two ids disagree). |
| `Cfg.DataDir + "/nex"` | `DataDir` (`nex.db`, `credentials/`) |
| `Cfg.Nex.RepoRoots` / `.ServiceRoots` | `RepoRoots` / `ServiceRoots`, `~` expanded and made absolute by pdx |
| `Cfg.Nex.Sandbox` | `Sandbox` |
| `Cfg.Nex.Timeouts` | `LeaseTTL` / `InterruptTimeout` / `TurnTimeout` |
| `shutdownBudget` (10 s, §4.5) | `ShutdownTimeout` |
| — | `Addr`, `Principals`, `ClaudeAccounts`: left zero |

Validation, two layers with distinct jobs:

1. pdx `config.Load` — only what pdx knows and Nexen cannot say better:
   `enabled` requires **at least one of** `repo_roots` / `service_roots`
   to be non-empty (error
   `nex.repo_roots / nex.service_roots: at least one root required when nex.enabled`);
   `max_profile`/`default_profile` pass
   `sandbox.ValidName` (N1 no longer exports the profile table; `sandbox.Lookup`
   is the read accessor if a value is ever needed); `claude_bin`, if set, is absolute after `~`
   expansion; `path_prepend` entries are absolute after expansion;
   durations parse. Errors name the `[nex]` key.

   **HOME-less processes.** `Load` passes `os.UserHomeDir()`'s result as
   `home`, and `""` when that fails (a launchd/Finder-started `pdx` without
   `HOME`). A `~`/`~/…` entry then cannot be expanded: when the section is
   **not** enabled the shape check for that entry is skipped — the default
   `path_prepend` contains `~/.local/bin`, and a host that never opted into
   nex must not fail every `pdx` subcommand over it; when it **is** enabled
   the error is `nex.<key>: HOME is not set, cannot expand "~/…"`. Relative
   entries without a leading `~` are rejected either way.

   An empty `max_profile` / `default_profile` is passed through as empty
   and becomes Nexen's fail-closed default, `readonly`; the Start log spells
   it out as `readonly (nexen fail-closed default)`.
2. `config.Config.Validate()` — Nexen's rules (defaults, roots fail-closed,
   profile clamp, durations, `DataDir` non-empty), run by pdx in `Init`
   **before** `Assemble` (N1 §4.4.2 requires a validated Config). pdx does
   not re-implement any of these.

`enabled = false` (or the section absent): `main.go` does not add the
module. `/api/nex/*` is a 404 like any unknown route (after the outer auth
chain), `pdx nex` reports "not enabled" (§4.7), nothing is opened under
`DataDir`, no PATH change is made.

`Validate` still runs on the section even when `enabled = false`, so a
malformed profile name in a disabled section fails daemon start —
fail-fast by design; only the runtime effects (PATH, DataDir, mount) are
skipped.

`config.Clone()` clones `Nex.RepoRoots`, `Nex.ServiceRoots`,
`Nex.PathPrepend` (the aliasing rule every other slice follows).
`PUT /api/config` **does not accept** a `nex` key in P-A (400
`unsupported_field`, consistent with how the handler treats other
non-editable fields); a round trip through `UpdateConfig` preserves the
persisted section byte-for-byte because `WriteFile` writes the whole struct.
Editing is by hand until P-C.

### 4.3 Authentication, principal, and the SSE transport decision

The nex handler is mounted **inside** the daemon's general chain
(`CORS → IPWhitelist → PairingGuard → TokenAuth`, `cmd/pdx/http_chain.go`).
Nexen still requires an `Authenticator` and refuses to run without one
(N1 §4.2: nil → 401). pdx supplies:

```go
api.AuthenticatorFunc(func(r *http.Request) (string, error) {
    if c := r.Header.Get("X-Pdx-Client"); c != "" && clientIDPattern.MatchString(c) {
        return "pdx:" + hostID + "/" + c, nil   // clientIDPattern = ^[A-Za-z0-9._-]{1,64}$
    }
    return "pdx:" + hostID, nil
})
```

- The principal id is the audit field on the operation events
  (`execution.delegated`, `message_accepted`, lease owner). `pdx:<host_id>`
  says "someone holding this host's daemon credential". pdx has no per-user
  identity; inventing one here would be fiction. The colon is fine (N1 does
  not parse principal ids).
- **Per-client suffix (`X-Pdx-Client`).** Nexen treats the same principal
  as the same writer: two clients presenting `pdx:<host_id>` would
  silently re-mint each other's control lease instead of being told the
  execution is held. A client may therefore name itself with the optional
  request header `X-Pdx-Client`; when the value matches
  `^[A-Za-z0-9._-]{1,64}$` the principal is `pdx:<host_id>/<client>`. An
  absent or malformed value is **ignored, not rejected** (bare host
  principal, nothing logged) — the header is a courtesy for lease
  arbitration, not a credential. The P-B SPA sends a per-client id (one
  per tab/session); `pdx nex` does **not** send it, because `attach` and
  `send` run in different processes and must share one principal.
- The Authenticator never checks the token itself. That is correct **only
  while the handler is unreachable except through the general chain**. §5
  I2 states that invariant as "an unauthenticated request never reaches the
  Nexen handler" and tests it with a probe handler substituted for
  `sys.Handler` in the real `newOuterHandler`, across methods (GET, POST,
  SSE GET), credentials (none, wrong bearer, valid bearer, fresh ticket on
  a plain request — not reached since the codex R2 follow-up —, fresh
  ticket on a WebSocket upgrade, reused ticket, peer inbound token),
  pairing state, IP-whitelist rejection, and un-normalized paths
  (`/api/nex/../foo`, `//api/nex/v1/…`). The
  `OPTIONS` preflight is answered by CORS before auth — that is existing
  behaviour for every route and is asserted, not changed.
- When `Cfg.Token == ""` (IP-whitelist-only daemon) TokenAuth passes every
  request through; Nexen then also passes, with the same principal. That is
  today's trust model for every other route and is not widened here.
- `/api/peers` (PeerAuth chain) routes to the shared mux only for
  `/api/peers…` paths; a peer token presented to `/api/nex/…` goes through
  the general chain and gets 401 from TokenAuth. I2 asserts the Nexen probe
  is not reached; the exact status is whatever the chain says (401), not
  contractually 404.

**SSE transport (binding on P-B, Swift, Aigora).** Nexen's SSE reads the
resume cursor from the `Last-Event-ID` **header** only, and pdx's one-time
tickets are consumed on first use. A native `EventSource` cannot set
headers and reconnects on its own with the *old* URL — the second connect
would present a spent ticket and get 401, and a hand-built new `EventSource`
cannot carry `Last-Event-ID`. Therefore:

- Browser clients (P-B) consume `/api/nex/v1/events` with **`fetch` +
  `ReadableStream`**, sending `Authorization: Bearer <host token>` (which
  the SPA already holds per host and uses for every REST call) and
  `Last-Event-ID: <cursor>` on every (re)connect. No ticket is involved.
  Swift (`URLSession`) and Aigora (server-side) set headers natively.
- pdx CORS gains `Last-Event-ID` in `Access-Control-Allow-Headers`
  (`internal/middleware/middleware.go`). Without it the browser's
  preflight for the fetch fails. This is the one P-A change made *for* P-B.
  `X-Pdx-Client` (the per-client principal suffix above) is CORS-allowed
  too (codex R2 follow-up), so a browser can send it on the same fetch.
- Tickets authenticate WebSocket upgrades only (since the codex R2
  follow-up: `TokenAuth` consults `?ticket=` only when
  `websocket.IsWebSocketUpgrade(r)` — `Connection: Upgrade` +
  `Upgrade: websocket`); on any other request shape a ticket is neither
  consulted nor consumed. Every `/api/nex/…` request therefore needs the
  bearer. (Before this a one-time WS ticket could drive Nexen mutation REST
  under `/api/nex/`.) A WS upgrade carrying a fresh ticket still passes
  the chain on a nex path — the chain cannot know Nexen has no WS routes;
  the handler answers 404 there. I2 pins both.

### 4.4 Mount and prefix

```go
mux.Handle("/api/nex/", http.StripPrefix("/api/nex", recoverer(m.sys.Handler)))
```

with `Options.PublicPrefix = "/api/nex"` (a constant that also passes
N1's `api.ValidatePublicPrefix`, asserted once in a test so a future edit
cannot introduce `//`, `?`, `#` or percent-escapes) so the paths Nexen renders into
responses (`capabilities.lease.*.path`, attach `stream_url`) are
origin-relative absolute paths including the prefix, per N1 §4.1's contract
note. Routing and Nexen's draining check see `/v1/...` because `StripPrefix`
rewrites `r.URL.Path` (verified in N1).

`recoverer` is new and pdx-owned (there is no reusable one in the repo):

```go
func recoverer(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if p := recover(); p != nil {
                log.Printf("nex: panic serving %s %s: %v\n%s", r.Method, r.URL.Path, p, debug.Stack())
                w.WriteHeader(http.StatusInternalServerError) // no-op (logged by net/http) if headers already sent
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

It passes the **original** `ResponseWriter` through — no wrapper — so
`http.Flusher` (SSE) and `http.Hijacker` are preserved by construction.
For a streaming response that panics after headers were written, the
request simply ends; the client sees a dropped SSE connection and reconnects
with `Last-Event-ID`. `http.ErrAbortHandler` is re-panicked as net/http
expects. The recoverer wraps only the nex subtree; no other pdx route gains
it in this phase. It does not and cannot catch panics in Nexen's background
goroutines (adapter read loops, reconcile) — those are Nexen's
responsibility, as they are in the standalone daemon.

### 4.5 Lifecycle and shutdown

Today `main.go` runs the shutdown sequence in the signal goroutine
(cancel ctx → `StopModules(shutdownCtx)` → `srv.Shutdown(shutdownCtx)`)
while the main goroutine returns as soon as `srv.Serve` returns — so any
step placed after `srv.Shutdown` can be cut off by process exit, and the
`defer`red closes in `main` (e.g. the meta store) race it.

Nexen's contract (N1 §4.4.2) needs `sys.Shutdown` **before** the server
drains and `sys.Close` **after** it: closing the store while an SSE handler
is still running would be a use-after-close.

Changes (additive):

```go
// core
type Closer interface{ Close() error }   // optional; modules owning resources the
                                          // HTTP layer may still be using during drain
func (c *Core) CloseModules() error       // reverse registration order; errors joined;
                                          // modules without Closer are skipped
```

`main.go`:

```go
shutdownDone := make(chan struct{})
go func() {                      // signal goroutine
    <-sigCh
    cancel()
    ctx, c := context.WithTimeout(context.Background(), shutdownBudget) // 10 s
    defer c()
    if err := core.StopModules(ctx); err != nil { log.Printf("stop modules: %v", err) }
    if err := srv.Shutdown(ctx); err != nil {    // deadline hit → force
        log.Printf("http shutdown: %v; forcing close", err)
        srv.Close()
    }
    if err := core.CloseModules(); err != nil { log.Printf("close modules: %v", err) }
    close(shutdownDone)
}()
… srv.Serve(listener) …
<-shutdownDone                   // main waits for the whole sequence
```

What the 10 s budget does and does not bound — stated so nobody reads it as
a hard cap: it is **one deadline shared** by `StopModules` and
`srv.Shutdown` (N1 rule 1); Nexen's `ShutdownTimeout` is set to the same
value so its bounded interrupt cannot outlive it. It does **not** bound
`Stop` implementations that ignore their ctx (the `agent` module's `Wait()`
is one) nor `CloseModules`, which has no ctx by design (closing a store is
not cancellable). N1 (PR #59, C4) made `System.Shutdown` retryable — draining and the bus
close happen once, the live-turn interrupt can be called again with a fresh
ctx — but P-A does **not** retry: the budget is shared and already spent by
then; a second wait would only delay `Close`. After `srv.Close()` net/http
has closed listeners and connections but in-flight handler goroutines may
still be unwinding;
`sys.Close` right after is the same order the standalone daemon uses
(N1 rule 3) and is accepted as-is — a handler that loses the store under it
fails that one request during a forced shutdown.

**Where `nex.Stop` sits.** `Core.InitModules` topologically sorts the
modules and `StopModules` walks that order in reverse. With `[nex]`
enabled the sort (Kahn's algorithm, zero-dependency modules first in
registration order) puts `nex` 7th of 13 — 8th of 14 when the `dev`
module is registered, which sorts first and therefore stops last — so in
reverse order `dispatch`, `peers`, `stream`, `monitor`, `fs` and `agent`
stop **before** it. Those `Stop`s ignore their ctx but are sub-second in practice, so the
interrupt ladder inside `sys.Shutdown` sees a slightly reduced budget
rather than a starved one — accepted for P-A and tracked for a design
pass (acceptance record, observation 6). A future ctx-aware `Stop`
elsewhere, or a module that legitimately takes seconds to stop, must be
placed with this in mind.

`Config.ShutdownTimeout` is set from the same budget only because Nexen's
`Validate` requires it; the field is consumed by the standalone
`nex daemon` alone and is **inert in the embedded path** — the bound on
`sys.Shutdown` is the ctx `Stop` receives (rule 1 above).

**What a pdx restart does to executions** — three cases, tested separately
(§6 steps 3a–3c), because the outcomes differ:

| Case | Live turn's outcome | Execution afterwards | Recorded |
|---|---|---|---|
| Graceful (`SIGTERM`, interrupt confirmed within budget) | ended by Nexen's interrupt ladder | `idle`, resumable | `execution.interrupted` (source: daemon shutdown) |
| Graceful but unconfirmed (budget exceeded) | process killed by the ladder's SIGKILL or left running | reconciled at next Init: `orphaned` → `idle` | `execution.turn_orphaned` with `process_killed` as observed |
| Crash / `kill -9` of pdx | process orphaned; killed at next Init if still alive | `orphaned` → `idle` | `execution.turn_orphaned` |

Preserved in every case: every event already appended to `nex.db`, the
execution row, and the claude session transcript up to the last message
claude wrote. **Not** preserved: the tail of the in-progress turn (partial
assistant output that was only on the transient stream), tool actions that
were mid-flight, uncommitted edits the agent had made and would have
continued — those are exactly what an interactive session loses when its
terminal is killed. pdx restarts far more often than a standalone `nex
daemon` (every rebuild / dev update); this is accepted for P-A and is the
trigger for a sidecar process model if it hurts in practice.

### 4.6 Process environment for agent children

Nexen copies the daemon's own `PATH`/`HOME`/`SHELL`/`TMPDIR`/`LANG` to every
`claude -p` child (`account.BaseEnvKeys`) and nothing else. A daemon started
by launchd or Finder has `PATH=/usr/bin:/bin:/usr/sbin:/sbin`; Nexen finds
`claude` via `Options.ClaudeBin`, but the child's own `git`, `node`, `gh`,
`pnpm` would not resolve, and `claude_bin` alone does not fix that.

Policy: at nex `Init` (before Assemble, before anything else the module
does), compute `prefix = dedupe(expand(path_prepend))`, keep only entries
that are absolute and exist as directories, and set the **process** `PATH`
to `prefix ++ (existing PATH minus the prefix entries)`. So the configured
order is exactly the resulting front of `PATH`, running Init twice is a
no-op, and `path_prepend = []` disables the policy. The result is logged
once.

This is process-wide on purpose and **not** harmless in the abstract: after
it, every executable pdx resolves by name (`git`, `tmux`, `go` in the dev
module, …) resolves through the prepended dirs first. It is accepted
because (a) it only applies on hosts that opted into nex, (b) the default
dirs are the ones every interactive shell on these machines already puts
first, so the daemon ends up resolving the *same* binaries the user does
rather than different ones, and (c) Nexen has no per-child env seam
(N1 §5.5) and adding one is a contract change that belongs to a later
Nexen round — when it lands, `path_prepend` becomes a per-child setting and
the process-wide behaviour is removed. Same precedent as the LANG fix
(`internal/locale`, `main.go:101`), which is also process-wide.

`claude_bin` empty → `exec.LookPath("claude")` inside Assemble runs after
the PATH policy, so the default works for a Finder-launched daemon on a
standard Homebrew/npm install. A host with claude elsewhere sets it.

### 4.7 CLI — `pdx nex <subcommand> …`

Thin wrapper over `client.Run`:

- base URL = `http://<Cfg.Bind>:<Cfg.Port>/api/nex` from the local
  `config.toml` (`--config <path>` selects the file, default
  `~/.config/pdx/config.toml`), token = `Cfg.Token`. A wildcard bind is a
  listen address, not a dialable one, so `""`/`0.0.0.0` become
  `127.0.0.1` and `::`/`[::]` become `[::1]` (IPv6 literals bracketed via
  `net.JoinHostPort`) — like the statusline proxy, except `::` maps to `::1`
  (the proxy maps it to `127.0.0.1`).
- Overrides, checked before config: `--addr` / `--token` flags, then
  `PDX_NEX_ADDR` / `PDX_NEX_TOKEN` — so a commander session can point a
  worker CLI at another host by hand until cross-host lands. Flags must
  come before the subcommand (the stdlib flag parser stops at the first
  non-flag argument).
- **Token/addr rule.** `--addr` / `PDX_NEX_ADDR` **requires** a token from
  `--token` / `PDX_NEX_TOKEN`. The local `config.toml` token is the
  credential of *this* host's daemon and is never sent to an address the
  user typed: `pdx nex: --addr given without --token / PDX_NEX_TOKEN (the
  local config token is not sent to a non-local address)`, exit 2, no
  config load, no request. Prefer `PDX_NEX_TOKEN` over `--token` — argv is
  visible to other local users (`ps`), the environment is not; the usage
  text says so.
- **`--addr` shape (codex R2 follow-up).** `--addr` / `PDX_NEX_ADDR` must
  be `http(s)://host[:port][/path]`: scheme `http` or `https`, non-empty
  host, no query, no fragment. The path is cleaned and a trailing slash
  stripped (`/api/nex/` → `/api/nex`; `/` → empty, so requests hit
  `/v1/…` at the root). Anything else:
  `pdx nex: --addr must be http(s)://host[:port][/path] without query or fragment (got "<value>")`,
  exit 2, no config load, no request. The probe URL and the not-enabled
  message use the normalized base. The config-derived base is built
  canonical and is not normalized.
- Exit codes: 0 success; 1 any error from `client.Run`, config load or the
  not-enabled path; **2** for flag/usage errors (unknown or incomplete
  `--addr`/`--token`/`--config`, a malformed `--addr`, `--addr` without a
  token) — nothing is loaded or requested on exit 2.
- Everything after the flags is passed to `client.Run` verbatim, so
  `pdx nex delegate --cwd … --brief …`, `pdx nex ls`, `pdx nex watch <id>`,
  `pdx nex attach --control <id>`, `pdx nex send …`, `pdx nex interrupt …`
  are the same words as the `nex` CLI. `pdx nex` with no args prints
  `client.Run`'s own usage error.
- "Not enabled" detection does **not** parse `client.Run`'s error strings
  (they are unstructured, and Nexen's own 404s such as
  `execution_not_found` must surface as themselves). When `client.Run`
  returns an error, the wrapper probes `GET <base>/v1/capabilities` once;
  a 404 there means the mount does not exist and the wrapper prints
  `nex: not enabled on this host (GET <base>/v1/capabilities → 404; set [nex] enabled = true, or check --addr)`
  — the probed URL is in the message because a mistyped `--addr` is
  indistinguishable from a disabled host; anything else re-prints
  `client.Run`'s error unchanged. The probe only runs on the error path,
  so a normal invocation costs one request.

Rationale: a26 has `pdx` but not `nex`; the commander scenario needs the
verbs on every host with zero flags.

### 4.8 Observability

- Start log line (§4.1) when enabled; `nex: disabled` at daemon start when
  not, so "why is /api/nex 404" is answerable from `pdx.log`.
- `GET /api/info` gains `"nex": {"configured": bool, "mounted": bool}` —
  `configured` mirrors the **in-memory** `[nex] enabled`, `mounted` is
  whether the module actually registered in this process. They differ
  when `enabled` was changed after start (restart required), which is the
  question a client will ask. The authoritative feature list remains
  `GET /api/nex/v1/capabilities`.

  **Documented limitation (until the Settings UI, P-C).** The in-memory
  config is what `PUT /api/config` writes back *whole* (`UpdateConfig`
  clones, mutates, `WriteFile`s the entire struct). A hand edit of `[nex]`
  in `config.toml` that is not followed by a restart is therefore
  overwritten by the next `PUT /api/config` from any client — the file
  reverts to the section the daemon loaded at start. `configured` reports
  that in-memory value, never re-reads the file.
- Nexen's own logging goes through the standard `log` package into
  `pdx.log` with its existing prefixes; no adapter.

## 5. Invariants (each has a test)

| # | Invariant |
|---|---|
| I1 | With `[nex] enabled = false` or absent: no module registered, no PATH change, `DataDir/nex` not created, and — **after** passing the outer auth chain — `/api/nex/v1/capabilities` is 404. |
| I2 | An unauthenticated request never reaches the Nexen handler. Probe handler in the real `newOuterHandler`: no credential, wrong bearer, fresh ticket on a plain GET / POST / SSE GET (WS-only tickets, codex R2 follow-up), spent ticket, peer inbound token, pairing-mode, non-whitelisted IP, `/api/nex/../x`, `//api/nex/v1/x` → probe not reached, status is the chain's. Valid bearer (GET, POST, SSE GET) and a fresh ticket on a WebSocket upgrade → reached. |
| I3 | Responses render the prefix: `capabilities.lease.renew.path` starts with `/api/nex/v1/`; `attach` returns `stream_url` starting with `/api/nex/v1/events?`. |
| I4 | `{id}` path values resolve under the prefix: `GET /api/nex/v1/executions/<unknown>` returns Nexen's `execution_not_found` 404 body, not pdx's generic 404. |
| I5 | Draining through the mount: after `Stop`, `POST /api/nex/v1/executions` → 503 `draining` while `GET /api/nex/v1/executions` still answers. |
| I6 | `internal/module/nex` imports `lab.protype.tw/wake/nexen`, `…/api`, `…/config`, `…/sandbox` only — never `execution`, `store`, `bus`, `adapter`. (Import-list test.) |
| I7 | Shutdown sequence, end to end with a fake `System` and a real `httptest` server holding one open SSE request: `StopModules` → `srv.Shutdown` (times out because of the open stream) → `srv.Close` → `CloseModules`, in that order; `main`'s goroutine does not return before `CloseModules` completed; a `Stop` that returns an error does not skip later steps; a `Stop` that ignores ctx and exceeds the budget still lets the sequence complete after it returns. |
| I8 | A panic in a nex handler yields 500 on that request and the daemon serves the next request; a panic after an SSE response started ends that response without affecting others; `Flush` works through the recoverer; `http.ErrAbortHandler` propagates. |
| I9 | `Init` with a launchd-style `PATH` and `path_prepend` of three dirs (one non-existent) results in `PATH` = the two existing dirs in configured order, then the original entries; a second `Init` leaves it unchanged; `path_prepend = []` leaves `PATH` untouched. |
| I10 | `pdx nex`: addr/token from config; `--addr`/env override it; against a server with no mount, an error is reported as "not enabled"; against a mount where `client.Run` fails with `execution_not_found`, that message is printed unchanged (probe returned 200). |
| I11 | Every **operation** event (`execution.delegated`, `execution.message_accepted`) and the lease owner carry `pdx:<host_id>`, read back from `GET /api/nex/v1/executions/<id>/events` after a delegate in an httptest daemon with a fake claude fixture. |
| I12 | `Init` fails (daemon does not start) for: empty `HostID`, `DataDir/nex` not creatable/writable, `claude_bin` set but missing or not executable, `claude_bin` empty and no `claude` on the (post-policy) PATH, invalid duration, root that is not a directory. Each error names the cause. |
| I13 | `Init` calls `Config.Validate()` before `Assemble`, and a Config with a field left at zero arrives at Assemble with Nexen's default applied (fake Assemble captures `Options`). |
| I14 | CORS preflight for `/api/nex/v1/events` with `Access-Control-Request-Headers: last-event-id, authorization` is allowed; an SSE request through the mount with `Last-Event-ID: <seq>` resumes from `seq+1` (fake `System` handler echoes the header it received). |
| I15 | `PUT /api/config` with a `nex` key → 400; a round trip of `UpdateConfig` with a mutation elsewhere leaves the persisted `[nex]` section identical; `Clone()` does not alias the three slices. |

## 6. Acceptance

Automated (`go test ./...`, no real claude): I1–I15, using `httptest`
around `newOuterHandler`, a fake `System`/`Assemble` where the invariant is
about pdx wiring, and Nexen's own fake-claude fixture pattern where the
invariant is about the mounted engine (I3, I4, I5, I11).

Manual, on mlab, before merge (recorded in the PR):

0. **Build chain**: over non-interactive ssh (`ssh mlab 'cd … && GOWORK=off GOMODCACHE=$(mktemp -d) go build ./cmd/pdx'`)
   the module downloads and both `pnpm run electron:build` architectures
   build; then the daemon's own dev update rebuild (`/api/dev/update/…`)
   succeeds from a Finder-launched pdx. Proves `go env -w` + `insteadOf`
   are sufficient without shell env.
1. `[nex] enabled = true`, `repo_roots = ["~/Workspace/wake"]`,
   `[nex.sandbox] max_profile = "handoff"`; restart daemon; `pdx nex host`
   shows quota; `pdx.log` has the start line with the resolved PATH.
2. `pdx nex delegate --cwd ~/Workspace/wake/purdex --brief "say hi and exit"`
   → `pdx nex watch <id>` streams to `result`.
3. Restart semantics, one long-running brief each:
   3a. `pdx daemon restart` (SIGTERM) → log shows the interrupt confirmed →
   `show` is `idle` → `send "continue"` resumes the conversation.
   3b. same with a brief whose turn is inside a long tool call → either
   the ladder's SIGKILL or the next Init's reconcile — `show` is `idle`,
   the log says which path, `events` has the matching event.
   3c. `kill -9` the daemon → next start logs the reconcile with
   `process_killed=true` → `send` resumes.
4. From this Claude Code session: `pdx nex delegate` two workers in parallel,
   `pdx nex ls` shows both, `pdx nex interrupt` one of them — the commander
   loop end to end without an SPA.
5. Handoff round trip with the CLI only, same cwd throughout: in tmux,
   `claude` in `~/Workspace/wake/purdex`, note the session id, `/exit`,
   confirm the process is gone; `pdx nex delegate --sandbox-profile handoff
   --session-id <sid> --cwd ~/Workspace/wake/purdex --brief "…"`; `watch` to
   `result`; `pdx nex interrupt` / confirm no live turn; then in tmux
   `claude --resume <sid>` in the same directory shows the delegated turn.
   `pdx hook` invocations inside the handoff turn are visible in the event
   stream (hook frames) and are no-ops (no tmux) — expected, not a bug.
6. a26: install the new binary via the App's local daemon update, repeat
   steps 1–2 there. Confirms the PATH policy and `claude` lookup on a
   Finder-launched daemon.
7. `curl -X OPTIONS` preflight with `Last-Event-ID` and a `curl -N` SSE
   through `/api/nex/v1/events?execution_id=…` with a `Last-Event-ID`
   header resume from the middle of a finished execution.

## 7. Risks

| Risk | Mitigation |
|---|---|
| Private-module build friction on a machine without the two one-time settings | `make check-goenv` fails fast with the exact commands; README documents them; §6 step 0 proves the chain from a cold cache |
| sqlite 1.46 → 1.54 changes behaviour in pdx's own stores | pdx store tests + `PRAGMA` tests already in repo |
| Frequent pdx restarts interrupt workers | §4.5 table states exactly what is lost; `execution.turn_orphaned` / `interrupted` are observable; sidecar model is the escape hatch |
| Process-wide PATH prepend changes which `git`/`tmux`/`go` pdx runs | Defaults match the user's shell; opt-in with nex; fixed, logged order; removed once Nexen grows a per-child env seam |
| Principal `pdx:<host_id>` is coarse | Documented; per-user identity is not something pdx has anywhere else |
| N1 lands with a different `Options` shape | This spec names N1 §4.4.2 symbols exactly; drift is a compile error |
| A Nexen background-goroutine panic takes the daemon down | Not covered by the recoverer (stated in §4.4); same exposure as the standalone daemon; Nexen's own tests are the guard |

## 8. Resolved questions (v1 §8, closed by the review)

1. `enabled` stays `false` in P-A. P-B may flip the *documentation* default,
   but a host without roots/policy configured is never auto-enabled — Load
   rejects `enabled = true` with no roots.
2. `Closer` optional interface, existing `Stop` order kept, **plus** the main
   goroutine waits for the whole sequence (§4.5). The v1 argument "other
   modules' Stop assume the server is up" was not the reason (stream's
   `Stop` is a no-op); the reason is that `Stop` semantics are "stop doing
   work" and `Close` is "release what handlers may still hold", and only
   the latter must follow the server.
3. Process-wide PATH prepend, opt-in with nex, fixed order, global effect
   acknowledged (§4.6). `claude_bin` alone is not an alternative because the
   child needs `git` too.

## 9. Related

- Nexen N1 spec / plan (`docs/specs/2026-09-15-n1-library-seams-{spec,plan}.md` in `wake/nexen`)
- `docs/specs/2026-09-13-peer-bridge-spec.md` §4 — why peer credentials do not reach `/api/nex`
- `docs/specs/2026-07-19-m0-dispatch-integration.md` — the M0 modules P-D will remove
- `internal/locale` — precedent for fixing a launchd environment at start
- `internal/module/dev/build.go` — the in-daemon `go build` that §3.1 must keep working

## 10. Codex spec review disposition (`task-mu1mhggb-c9tm86`, one round)

| # | Level | Finding | Disposition |
|---|---|---|---|
| 1 | must | Validate placed inside Assemble contradicts N1 §4.4.2 | ✅ §4.1/§4.2: pdx runs `Validate()` before `Assemble`; I13 |
| 2 | must | `CloseModules` after `srv.Shutdown` in the signal goroutine can be cut off by process exit | ✅ §4.5: `shutdownDone` channel, main waits; I7 covers timeout, force close, error paths |
| 3 | must | One-time ticket + native `EventSource` reconnect cannot carry `Last-Event-ID` | ✅ §4.3: transport fixed as fetch-SSE with bearer + `Last-Event-ID` header; CORS allow-header added; I14, §6 step 7 |
| 4 | must | Mapping every 404 to "not enabled" swallows `execution_not_found` | ✅ §4.7: capabilities probe on the error path only; I10 |
| 5 | must | `Makefile` env does not reach the daemon's own `go build` (`dev/build.go`) | ✅ §3.1: machine-level `go env -w GOPRIVATE` + git `insteadOf`; `make check-goenv`; §6 step 0 |
| 6 | should | I2 too narrow; peer path status not necessarily 404; OPTIONS bypass | ✅ I2 rewritten around "probe not reached"; status left to the chain; OPTIONS asserted as existing |
| 7 | should | Restart recovery over-promised; graceful vs orphan conflated | ✅ §4.5 three-case table with what is/isn't preserved; §6 3a–3c |
| 8 | should | 10 s is a shared deadline, not a hard cap; `Server.Close` ≠ handlers exited; Stop may return error | ✅ §4.5 scope paragraph; Assemble ctx = Background; `Stop` returns the error |
| 9 | should | PATH prepend called harmless; order/dedupe unspecified | ✅ §4.6 rewritten: exact algorithm, `[]` disables, global effect acknowledged; I9 |
| 10 | should | Goal 4 over-claims isolation; no reusable recoverer; Flusher | ✅ Goal 4 reworded; §4.4 recoverer spec (no writer wrap, `ErrAbortHandler`); I8 |
| 11 | should | Config persistence contract; `Clone`; `/api/info` staleness; failure cases | ✅ §4.2 (`PUT` rejects `nex`, `Clone` slices), §4.8 `configured`/`mounted`, I12, I15 |
| 12 | should | Empty `HostID` → `pdx:`; I11 over-broad; handoff test needs same cwd / process gone | ✅ §4.2 HostID rule; I11 narrowed to operation events + lease; §6 step 5 |
| 13 | nit | `repo_roots` comment misleading; `max_profile` placement | ✅ §4.2 paste-able TOML with corrected comments |
| 14 | — | final review (whole-branch) | ✅ fix wave: I7 assertion, dead chan, comment, error prefix, second-signal exit, log trim, CLI usage; issues #1032 #1033 #1034 |
| 15 | P2 | codex R1 (PR #1035): `Makefile` default goal no longer builds (`.DEFAULT_GOAL` shadowed by `check-goenv`) | ✅ default goal builds again; second-signal exit armed only after a signal-triggered shutdown |
| 16 | — | R2 Claude attack / defend / hygiene batch (codex out of quota until 2026-09-19; to be re-run then) | ✅ C1 HOME-less `Load` (§4.2) + single `config:`/`nex: init:` prefix; C2 roots rule = repo **or** service (§4.2); C3 `host_id` whitespace; C4 `X-Pdx-Client` principal suffix (§4.3); C5 `pdx nex` addr/token rule, probe URL in message, wildcard-bind base URL, `PDX_NEX_TOKEN` usage (§4.7); C6 Serve-first shutdown still honours a second signal (§4.5); C7 recoverer keeps the panic when `logf` panics; C8 unwritable `DataDir/nex` test; C9 PATH-policy log prints prefix + element count, empty profile spelled `readonly (nexen fail-closed default)`; C10 `ShutdownTimeout` inert in embedded path (§4.5); C11 gofmt `core.go`; docs D1–D8 |
