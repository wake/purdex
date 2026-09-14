# Spec — P-A: embed Nexen as the `nex` module of the pdx daemon

Status: v1 (draft, before codex spec review)
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
   daemon's existing token/ticket chain.
2. `nex --addr http://<host>:7860/api/nex --token <pdx token> …` and the new
   `pdx nex …` wrapper both work unchanged against it.
3. Executions survive a pdx restart the way they survive a `nex daemon`
   restart: orphaned turns are reconciled to `idle` and can be continued.
4. Nexen is opt-in per host (`[nex] enabled`) and a failure inside it cannot
   take the type-1 daemon down.

### Non-goals (explicitly later phases)

- Any SPA change (P-B / P-C). This phase is verified with `pdx nex` and
  `curl` only.
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
- Hot reload of `[nex]`. `PUT /api/config` may persist the section but the
  module reads it once at Init; a change needs a daemon restart (logged).
- A sidecar process model to keep turns alive across pdx restarts.
- Mirroring Nexen events onto Purdex's host-event WebSocket. Consumers use
  Nexen's own SSE (`/api/nex/v1/events`).

## 3. What N1 delivers that this phase consumes

| N1 symbol | Used for |
|---|---|
| `nexen.Assemble(ctx, Options{Config, Auth, PublicPrefix, ClaudeBin}) (*System, error)` | the whole wiring |
| `(*System).Handler` (auth-wrapped mux, no webui) / `.Shutdown(ctx)` / `.Close()` | mount + lifecycle |
| `config.Config` + `(*Config).Validate()` | built from `[nex]`, never from a TOML file |
| `api.AuthenticatorFunc` | pdx principal |
| `api.Deps.PublicPrefix` semantics | rendered paths carry `/api/nex`; routing does not |
| `sandbox.ValidName` / `sandbox.Policy` | validating `[nex].sandbox` at pdx config load |
| `cmd/nex/client.Run(out, args, baseURL, token)` | the `pdx nex` wrapper |

Version pin: `go.mod` requires `lab.protype.tw/wake/nexen` at a
pseudo-version of the N1 merge commit. No `replace` in `go.mod`; a
gitignored `go.work` pointing at a local checkout is the dev-loop mechanism.
Building needs `GOPRIVATE=lab.protype.tw` and
`git config --global url."ssh://git@lab.protype.tw:9079/".insteadOf "https://lab.protype.tw/"`
(Gitea serves the go-import meta unauthenticated but the https clone of a
private repo returns 401 — verified in N1 §4.3). Both are documented in
`README.md` and set inside `Makefile` (`GOPRIVATE` only; the git rewrite is a
one-time per-machine step, and builds only ever run on mlab).

`go.mod` also moves to `go 1.26.0` (Nexen's floor; toolchain 1.26.0 is
installed) and `modernc.org/sqlite` resolves to Nexen's 1.54.0 by MVS. The
existing pdx store tests are the regression net for that bump.

## 4. Design

### 4.1 Module shape — `internal/module/nex`

```go
type Module struct {
    core *core.Core
    sys  *nexen.System
    opts nexen.Options // built in Init, kept for tests/logging
}

func (m *Module) Name() string           { return "nex" }
func (m *Module) Dependencies() []string { return nil }
```

- `Init(c)`: translate `c.Cfg.Nex` → `nexen.Options` (§4.2), apply the PATH
  policy (§4.6), call `nexen.Assemble`. Assemble opens `<DataDir>/nex/nex.db`,
  runs the startup reconcile pair (orphaned turns → `orphaned`, execution
  → `idle`; live orphan processes SIGKILLed — exactly the standalone daemon's
  behaviour, and the reason executions survive restarts), and returns the
  handler. An `Assemble` error fails `Init`, which fails daemon start —
  same as any other module. **Except**: the module is only added to Core
  when `[nex] enabled = true` (§4.2), so a host that never opted in cannot be
  broken by it.
- `RegisterRoutes(mux)`: one line of routing (§4.4).
- `Start(ctx)`: log `nex: serving /api/nex (host_id=…, data_dir=…, claude_bin=…, profiles=…)`.
  Nothing else — Assemble already started everything.
- `Stop(ctx)`: `m.sys.Shutdown(ctx)` (drain + bounded-interrupt live turns).
  Its error is logged and **not** returned (N1 §4.4.2 rule 2: never stop the
  remaining shutdown steps).
- `Close() error`: `m.sys.Close()`. New optional interface, see §4.5.

Nothing in the module reaches into `System.Service` / `.Store` / `.Bus`.
The Go side of pdx talks to Nexen the way every other consumer does — over
HTTP (N1 §7 recommendation). This is an invariant (§5 I6), enforced by an
import test on the package.

### 4.2 Config — `[nex]`

```toml
[nex]
enabled       = false            # default false in P-A; P-B flips to true
repo_roots    = ["~/Workspace"]  # → config.RepoRoots   (developer directories, read-only for the daemon)
service_roots = []               # → config.ServiceRoots (service-layer directories, e.g. Aigora rooms later)
claude_bin    = ""               # → Options.ClaudeBin; "" = LookPath at Assemble (after §4.6)
cswap_bin     = ""               # → config.CswapBin; "" = Nexen default
path_prepend  = ["~/.local/bin", "/opt/homebrew/bin", "/usr/local/bin"]  # §4.6

[nex.sandbox]
max_profile     = "trusted"      # → sandbox.Policy.MaxProfile
default_profile = "trusted"      # → sandbox.Policy.DefaultProfile

[nex.timeouts]                   # all optional; zero = Nexen default
lease_ttl        = "120s"
interrupt        = "0s"
turn             = "0s"
```

Mapping, applied in `Init`:

| pdx | Nexen `config.Config` |
|---|---|
| `Cfg.HostID` | `HostID` (→ `nex.host` label, `capabilities.host_id`) |
| `Cfg.DataDir + "/nex"` | `DataDir` (`nex.db`, `credentials/`) |
| `Cfg.Nex.RepoRoots` / `.ServiceRoots` | `RepoRoots` / `ServiceRoots` (with `~` expanded by pdx — Nexen's Validate does not expand roots) |
| `Cfg.Nex.Sandbox` | `Sandbox` |
| `Cfg.Nex.Timeouts` | `LeaseTTL` / `InterruptTimeout` / `TurnTimeout` |
| **10 s** (pdx's shutdown budget in `main.go`) | `ShutdownTimeout` — kept equal to pdx's so the "one budget" rule holds (§4.5) |
| — | `Addr`, `Principals`, `ClaudeAccounts`: left zero |

Validation happens twice, deliberately: pdx's `config.Load` checks the
things only pdx knows (`enabled` requires at least one root; `max_profile`
and `default_profile` pass `sandbox.ValidName`; `claude_bin`, if set, is an
absolute path), so a typo is reported at daemon start with the `[nex]` key
name; then `nexen.Config.Validate()` runs inside Assemble with Nexen's own
rules (roots fail-closed, profile clamp, durations). pdx never re-implements
the second set.

`enabled = false` (or the section absent) means `main.go` does not add the
module at all: `/api/nex/*` is a 404 like any unknown route, `pdx nex`
reports `nex: not enabled on this host` from the same 404, and nothing is
opened under `DataDir`.

### 4.3 Authentication and principal

The nex handler is mounted **inside** the daemon's general chain
(`CORS → IPWhitelist → PairingGuard → TokenAuth`, `cmd/pdx/http_chain.go`).
By the time a request reaches Nexen it has already passed pdx auth: bearer
token, or a one-time ticket in `?ticket=` (the mechanism the SPA uses for
WebSockets, and what it will use for `EventSource` in P-B, since browsers
cannot set headers on SSE).

Nexen still requires an `Authenticator` and refuses to run without one
(N1 §4.2: nil → 401). pdx supplies:

```go
api.AuthenticatorFunc(func(*http.Request) (string, error) {
    return "pdx:" + hostID, nil
})
```

- The principal id is the audit field on `execution.delegated`,
  `message_accepted`, lease owner. `pdx:<host_id>` says "someone holding this
  host's daemon credential". pdx does not have per-user identity; inventing
  one here would be fiction.
- The Authenticator never checks the token itself. That is correct **only
  while the handler is unreachable except through the general chain** — an
  invariant (§5 I2) with a test that mounts the real outer handler and shows
  `/api/nex/v1/capabilities` is 401 without a token and 200 with one.
- When `Cfg.Token == ""` (IP-whitelist-only daemon) TokenAuth passes every
  request through; Nexen then also passes, with the same principal. That is
  today's trust model for every other route and is not widened here.
- `/api/peers` (PeerAuth chain) does not route to `/api/nex/` and must not:
  peer credentials are `/api/peers`-scoped (I2 covers this).

### 4.4 Mount and prefix

```go
mux.Handle("/api/nex/", http.StripPrefix("/api/nex", recoverer(m.sys.Handler)))
```

with `Options.PublicPrefix = "/api/nex"` so the paths Nexen renders into
responses (`capabilities.lease.*.path`, attach `stream_url`) are
origin-relative absolute paths including the prefix, per N1 §4.1's contract
note. Routing and Nexen's draining check see `/v1/...` because `StripPrefix`
rewrites `r.URL.Path` (verified in N1).

`recoverer` is pdx's: Nexen's handlers do not swallow panics (N1 §7 note 5).
It logs the stack with the request path and returns 500; it must not touch a
response that has already started (SSE) — for a streaming handler a panic
after the headers are written ends the response, which the client sees as a
dropped SSE connection and reconnects with `Last-Event-ID`. The recoverer
wraps only the nex subtree; no other pdx route gains it in this phase.

### 4.5 Lifecycle and shutdown order

Today `main.go` does: cancel ctx → `c.StopModules(shutdownCtx)` →
`srv.Shutdown(shutdownCtx)`. Nexen's contract (N1 §4.4.2) needs
`sys.Shutdown` **before** the server drains and `sys.Close` **after** it —
closing the store while an SSE handler is still running would be a
use-after-close.

Change (in `core` + `main.go`, additive):

```go
// core
type Closer interface{ Close() error }   // optional; modules that own resources
                                          // the HTTP layer may still be using
func (c *Core) CloseModules() error       // reverse topological order, errors joined
```

`main.go` sequence becomes: cancel ctx → `StopModules(shutdownCtx)` →
`srv.Shutdown(shutdownCtx)`; on timeout `srv.Close()` → `CloseModules()`.
Same `shutdownCtx` (10 s) for all steps, matching Nexen's
`ShutdownTimeout` (§4.2) so a slow interrupt cannot eat the server's drain
budget and then also be granted a second timer.

Consequence to state plainly: **a pdx restart interrupts every live turn on
that host.** pdx restarts far more often than a standalone `nex daemon`
(every rebuild / dev update). Nexen makes this an interruption, not a loss —
the next Init's reconcile marks the turn `orphaned`, the execution returns
to `idle`, and `send` continues the conversation via `--resume`. The
`execution.turn_orphaned` event records `process_killed`. This is accepted
for P-A and listed as the trigger for a future sidecar model if it hurts in
practice.

### 4.6 Process environment for agent children

Nexen copies the daemon's own `PATH`/`HOME`/`SHELL`/`TMPDIR`/`LANG` to every
`claude -p` child (`account.BaseEnvKeys`) and nothing else. Two pdx-specific
consequences:

1. **PATH.** A daemon started by launchd or Finder has `PATH=/usr/bin:/bin:
   /usr/sbin:/sbin`. Nexen finds `claude` via `Options.ClaudeBin` regardless,
   but the child's own `git`, `node`, `gh`, `pnpm` would not resolve. Policy:
   at `Init`, before Assemble, prepend each existing directory in
   `[nex].path_prepend` (default `~/.local/bin`, `/opt/homebrew/bin`,
   `/usr/local/bin`) to the **process** `PATH` if not already present, and
   log the result once. Process-wide because Nexen has no per-child env seam
   (N1 §5.5 — adding one is a contract change deferred). Same pattern as the
   existing LANG fix (`internal/locale`, `main.go:101`), and harmless for
   everything else pdx spawns.
2. **LANG** is already exported by `main.go` when missing (the tmux `-F`
   incident). Nexen inherits it; nothing to add.
3. `claude_bin` empty → `exec.LookPath("claude")` runs inside Assemble
   **after** step 1, so the default works for a Finder-launched daemon on a
   standard Homebrew/npm install. A host with claude somewhere unusual sets
   the path explicitly.

### 4.7 CLI — `pdx nex <subcommand> …`

Thin wrapper over `client.Run`:

- base URL = `http://<Cfg.Bind>:<Cfg.Port>/api/nex` from the local
  `config.toml` (exactly how `pdx msg` finds its daemon), token = `Cfg.Token`.
- Overrides: `--addr` / `--token`, and `PDX_NEX_ADDR` / `PDX_NEX_TOKEN`
  (checked before config, so a commander session can point a worker CLI at
  another host by hand until cross-host lands).
- Everything after the flags is passed to `client.Run` verbatim, so
  `pdx nex delegate --cwd … --brief …`, `pdx nex ls`, `pdx nex watch <id>`,
  `pdx nex attach --control <id>`, `pdx nex send …`, `pdx nex interrupt …`
  are the same words as the `nex` CLI. `pdx nex` with no args prints
  `client.Run`'s own usage error.
- 404 from `/api/nex/…` is mapped to `nex: not enabled on this host
  (set [nex] enabled = true)`; every other error is `client.Run`'s.

Rationale: a26 has `pdx` but not `nex`; the commander scenario needs the
verbs on every host with zero flags.

### 4.8 Observability

- Start log line (§4.1) and a `nex: disabled` line when the section is
  absent, so "why is /api/nex 404" is answerable from `pdx.log`.
- `GET /api/info` gains `"nex": {"enabled": bool}`. Nothing more — the
  authoritative feature list is `GET /api/nex/v1/capabilities`.
- Nexen's own logging goes through the standard `log` package into
  `pdx.log` with its existing prefixes; no adapter.

## 5. Invariants (each has a test)

| # | Invariant |
|---|---|
| I1 | With `[nex] enabled = false` or absent: no module registered, `/api/nex/v1/capabilities` → 404, `DataDir/nex` not created. |
| I2 | Through the real outer handler: `/api/nex/v1/capabilities` is 401 without credentials, 200 with the bearer token, 200 with a valid one-time ticket; via the `/api/peers` chain it is not reachable (404). |
| I3 | Responses render the prefix: `capabilities.lease.renew.path` starts with `/api/nex/v1/`; `attach` returns `stream_url` starting with `/api/nex/v1/events?`. |
| I4 | `{id}` path values resolve under the prefix (`GET /api/nex/v1/executions/<id>` for an unknown id returns Nexen's `execution_not_found` 404, not pdx's generic 404). |
| I5 | Draining: after `Stop`, `POST /api/nex/v1/executions` → 503 `draining` while a `GET` of a list still answers (N1's rule, observed through the mount). |
| I6 | `internal/module/nex` imports `lab.protype.tw/wake/nexen`, `…/api`, `…/config`, `…/sandbox` only — never `execution`, `store`, `bus`, `adapter`. (Import-list test.) |
| I7 | Shutdown order: `Stop` calls `System.Shutdown`; `Close` calls `System.Close`; `Core.CloseModules` runs after `StopModules` and is called by `main` after the server has stopped. Fake `System` records the order. |
| I8 | A panic in a nex handler yields 500 on that request and the daemon keeps serving the next request; the panic and path are logged. |
| I9 | `Init` with a launchd-style `PATH` ends with the configured prepend dirs first (only those that exist), idempotent on a second call. |
| I10 | `pdx nex` resolves addr/token from config; `--addr`/env override it; a 404 from the mount prints the "not enabled" message and exits non-zero. |
| I11 | Principal on every recorded event is `pdx:<host_id>` (read back from `GET /api/nex/v1/executions/<id>/events` after a delegate in an httptest daemon with a fake claude binary). |

## 6. Acceptance

Automated (`go test ./...`, no real claude): I1–I11 above, using `httptest`
around `newOuterHandler` plus a fake `claude` script fixture the way Nexen's
own adapter tests do (`testdata`).

Manual, on mlab, before merge (recorded in the PR):

1. `[nex] enabled = true`, `repo_roots = ["~/Workspace/wake"]`,
   `max_profile = "handoff"`; restart daemon; `pdx nex host` shows quota.
2. `pdx nex delegate --cwd ~/Workspace/wake/purdex --brief "say hi and exit"`
   → `pdx nex watch <id>` streams to `result`.
3. Restart pdx mid-turn (a long brief) → log shows the orphan reconcile →
   `pdx nex show <id>` is `idle` → `pdx nex send <id> "continue"` works.
4. From this Claude Code session: `pdx nex delegate` two workers in parallel,
   `pdx nex ls` shows both, `pdx nex interrupt` one of them — the commander
   loop end to end without an SPA.
5. Handoff round trip with the CLI only: `pdx nex delegate --sandbox-profile
   handoff --session-id <sid of a tmux claude>` after `/exit`; then
   `interrupt`; then `claude --resume <sid>` in tmux shows the delegated
   turn. (P-C automates this; P-A proves the engine side.)
6. a26: install the new binary via the App's local daemon update, repeat
   step 2 there. Confirms the PATH policy on a Finder-launched daemon.

## 7. Risks

| Risk | Mitigation |
|---|---|
| Private-module build friction (`GOPRIVATE`, ssh rewrite) breaks `electron:build` on a fresh machine | Documented in README; `Makefile` sets `GOPRIVATE`; builds only run on mlab today |
| sqlite 1.46 → 1.54 changes behaviour in pdx's own stores | pdx store tests + `PRAGMA` tests already in repo (`internal/store/pragma_test.go`) |
| Orphan reconcile at every pdx restart kills a worker mid-edit | Stated in §4.5; `execution.turn_orphaned` is observable; sidecar model is the escape hatch |
| PATH policy changes env for unrelated pdx children | Prepend only, only existing dirs, logged once; identical dirs a user shell would have |
| Principal `pdx:<host_id>` is coarse | Documented; per-user identity is not something pdx has anywhere else |
| N1 lands with a different `Options` shape | This spec names N1 §4.4.2 symbols exactly; any drift is caught at compile time, not at runtime |

## 8. Open questions for review

1. `enabled` default `false` in P-A, `true` in P-B — or `true` now, since the
   route is inert without a client? (Leaning false: blast radius until the
   handler has run in anger on both hosts.)
2. `Closer` optional interface vs. reordering `StopModules` after
   `srv.Shutdown` for every module. (Leaning `Closer`: other modules' `Stop`
   assume the server is still up for in-flight WS teardown.)
3. Is process-wide `PATH` prepend acceptable, or should pdx refuse to start
   nex with a bare launchd `PATH` and make the user configure `claude_bin`
   plus nothing else? (Leaning prepend: the child needs `git` too.)

## 9. Related

- Nexen N1 spec / plan (`docs/specs/2026-09-15-n1-library-seams-{spec,plan}.md` in `wake/nexen`)
- `docs/specs/2026-09-13-peer-bridge-spec.md` §4 — why peer credentials do not reach `/api/nex`
- `docs/specs/2026-07-19-m0-dispatch-integration.md` — the M0 modules P-D will remove
- `internal/locale` — precedent for fixing a launchd environment at start
