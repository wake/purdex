# P-A Implementation Plan — embed Nexen as the pdx `nex` module

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Plan v1 (before codex plan review).

**Goal:** a `pdx` binary that mounts Nexen's v1 HTTP API at `/api/nex/`
behind the daemon's own auth chain, opt-in via `[nex] enabled`, with a
correct shutdown order, a launchd-safe `PATH`, and a `pdx nex …` CLI
wrapper. Spec: `docs/specs/2026-09-15-pa-nex-module-spec.md` v2.

**Architecture:** one new module package `internal/module/nex` (config
mapping, PATH policy, recoverer, mount, lifecycle); a `Closer` seam in
`internal/core`; a `NexConfig` section in `internal/config`; a shutdown
coordinator and the `nex` subcommand in `cmd/pdx`. The Go side never calls
Nexen's `Service`/`Store`/`Bus` — HTTP only (spec I6).

**Tech Stack:** Go 1.26 · `lab.protype.tw/wake/nexen` (N1) · net/http ·
no SPA changes.

**Prerequisite:** N1 merged in `wake/nexen` and its merge commit known. Until
then, Task 1's `go.work` points at the local N1 worktree
(`/Users/wake/Workspace/wake/nexen/.claude/worktrees/n1-library-seams`) and
the `go.mod` pseudo-version is filled in at PR time (Task 11 checks it).

## Global Constraints

- **TDD, no exceptions.** Failing test first, run it, implement, run again.
  Each task is one commit.
- **Commit messages in English**; replies in Traditional Chinese. Every
  commit ends with:
  ```
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_016zeaapxv52rU3gssx84473
  ```
- **Verification before every commit:** `go build ./... && go vet ./... &&
  go test ./...` from the worktree root. (SPA untouched — no vitest run.)
- **Worktree path:** every command runs from
  `/Users/wake/Workspace/wake/purdex/.claude/worktrees/pa-nex-module`
  (prefix every Bash call with `cd <that path> &&`); every Edit/Write uses
  that absolute prefix.
- **Parallel subagents in the same worktree commit with
  `git commit --only <files>`** so a shared index never sweeps another
  task's files in.
- **Existing tests are not edited** except where a task below says so
  explicitly (Task 3's `main` refactor, Task 4's CORS header list). If a
  task cannot pass otherwise, stop and report.
- **No real `claude` in tests.** Engine-level tests use a fake claude shell
  script written into `t.TempDir()` (pattern below). No test touches the
  daemon's real `DataDir`, `PATH`, or `~/.config/pdx`; PATH tests run in a
  subprocess or restore via `t.Setenv`.
- **Package boundaries:** `internal/module/nex` imports from Nexen only
  `lab.protype.tw/wake/nexen`, `…/api`, `…/config`, `…/sandbox` (spec I6);
  it imports `internal/core` and `internal/config` from pdx. `internal/config`
  imports Nexen's `sandbox` only (for `ValidName`). `cmd/pdx` imports
  Nexen's `cmd/nex/client` for Task 9.
- **Nexen symbol names** are those of N1 spec §4.4.2 / §7. If the merged N1
  differs, the task adapts to N1 — N1 is the authority — and the deviation
  is noted in the commit message.

### Fake claude fixture (used by Tasks 7, 10)

Write this into `t.TempDir()` as `claude`, `chmod 0755`, pass its path as
`ClaudeBin`. It is Nexen's `adapter/testdata/fake-claude-ok.sh` shape:

```sh
#!/bin/sh
head -n 1 >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-fake","capabilities":[]}'
echo '{"type":"assistant","session_id":"sess-fake","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}'
echo '{"type":"result","session_id":"sess-fake","subtype":"success"}'
cat >/dev/null
```

---

## File Structure

```
go.mod / go.sum                              Task 1  require nexen, go 1.26
.gitignore                                   Task 1  go.work, go.work.sum
Makefile                                     Task 1  check-goenv target
README.md                                    Task 1  build prerequisites
internal/config/nex.go                       Task 2  NexConfig, defaults, validate, expand
internal/config/nex_test.go                  Task 2
internal/config/config.go                    Task 2  Nex field, Clone slices, Load hook
internal/core/core.go                        Task 3  Closer, CloseModules
internal/core/core_test.go                   Task 3  (append only)
internal/core/config_handler.go              Task 2  reject "nex" in PUT
internal/core/config_handler_test.go         Task 2  (append only)
internal/core/info_handler.go                Task 8  nex: {configured, mounted}
internal/core/info_handler_test.go           Task 8  (append only)
internal/middleware/middleware.go            Task 4  Last-Event-ID in CORS
internal/middleware/middleware_test.go       Task 4  (append only)
internal/module/nex/module.go                Task 7  Module, Init/RegisterRoutes/Start/Stop/Close
internal/module/nex/module_test.go           Task 7
internal/module/nex/build_config.go          Task 7  pdx cfg → nexen config.Config + Options
internal/module/nex/build_config_test.go     Task 7
internal/module/nex/pathpolicy.go            Task 6
internal/module/nex/pathpolicy_test.go       Task 6
internal/module/nex/recover.go               Task 5
internal/module/nex/recover_test.go          Task 5
internal/module/nex/imports_test.go          Task 7  I6
internal/module/nex/mount_test.go            Task 10 I3 I4 I5 I11 I14 with real Assemble + fake claude
cmd/pdx/shutdown.go                          Task 3  runShutdown coordinator
cmd/pdx/shutdown_test.go                     Task 3
cmd/pdx/main.go                              Task 3, 8  use runShutdown; register nex when enabled; "nex" subcommand
cmd/pdx/nex.go                               Task 9  pdx nex wrapper
cmd/pdx/nex_test.go                          Task 9
cmd/pdx/http_chain_test.go                   Task 10 (append) I2 probe matrix
```

---

### Task 1: Module dependency and build prerequisites

**Files:** `go.mod`, `go.sum`, `.gitignore`, `Makefile`, `README.md`.

- `go.mod`: `go 1.26.0`; `require lab.protype.tw/wake/nexen v0.0.0-<pseudo>`
  (placeholder pseudo-version of the N1 worktree HEAD for now — Task 11
  replaces it with the merge commit's). `go mod tidy`; confirm
  `modernc.org/sqlite` resolved to ≥ 1.54.0.
- `.gitignore`: add `go.work` and `go.work.sum`. Create a local `go.work`
  (`go work init . /Users/wake/Workspace/wake/nexen/.claude/worktrees/n1-library-seams`) —
  **not committed**.
- `Makefile`: target `check-goenv` that fails unless
  `go env GOPRIVATE` contains `lab.protype.tw` and
  `git config --global --get-urlmatch url.insteadOf https://lab.protype.tw/`
  prints the ssh URL; on failure it prints the two commands from spec §3.1.
  `build` depends on `check-goenv`.
- `README.md`: build section with the two one-time commands and the cold
  cache verification command (spec §6 step 0).

**Tests (write first):** a shell-level check is enough here —
`make check-goenv` exits 0 on mlab; `env GOPRIVATE= make check-goenv`… is
not testable without clobbering `go env`, so: `make -n build` shows
`check-goenv` runs first. `go build ./...` and the existing `go test ./...`
pass with the new sqlite (this is the regression run for the MVS bump —
record the `internal/store` test output in the commit message).

- [ ] go.mod / go.work in place, `go build ./...` green
- [ ] `go test ./internal/store/...` green on sqlite 1.54
- [ ] `make -n build` shows check-goenv; `make check-goenv` green on mlab
- [ ] commit `build: require lab.protype.tw/wake/nexen, go 1.26, goenv preflight`

### Task 2: `[nex]` config section

**Files:** create `internal/config/nex.go`, `internal/config/nex_test.go`;
edit `internal/config/config.go` (`Nex NexConfig` field with `toml:"nex" json:"nex"`,
defaults, `Clone`, validation hook in `Load`); edit
`internal/core/config_handler.go` + append to its test.

**Public API:**
```go
package config

type NexConfig struct {
    Enabled      bool              `toml:"enabled"       json:"enabled"`
    RepoRoots    []string          `toml:"repo_roots"    json:"repo_roots"`
    ServiceRoots []string          `toml:"service_roots" json:"service_roots"`
    ClaudeBin    string            `toml:"claude_bin"    json:"claude_bin"`
    CswapBin     string            `toml:"cswap_bin"     json:"cswap_bin"`
    PathPrepend  []string          `toml:"path_prepend"  json:"path_prepend"`
    Sandbox      NexSandboxConfig  `toml:"sandbox"       json:"sandbox"`
    Timeouts     NexTimeoutsConfig `toml:"timeouts"      json:"timeouts"`
}
type NexSandboxConfig struct {
    MaxProfile     string `toml:"max_profile"     json:"max_profile"`
    DefaultProfile string `toml:"default_profile" json:"default_profile"`
}
type NexTimeoutsConfig struct {
    LeaseTTL  string `toml:"lease_ttl" json:"lease_ttl"`
    Interrupt string `toml:"interrupt" json:"interrupt"`
    Turn      string `toml:"turn"      json:"turn"`
}

// DefaultNexConfig: Enabled false, PathPrepend {"~/.local/bin","/opt/homebrew/bin","/usr/local/bin"},
// Sandbox {"trusted","trusted"}, Timeouts all "".
func DefaultNexConfig() NexConfig

// Validate is the pdx-side layer (spec §4.2 layer 1). It expands "~" and
// reports the offending key in every error. It does not touch the filesystem.
//   - enabled && len(RepoRoots)+len(ServiceRoots)==0 → "nex.repo_roots: at least one root required when nex.enabled"
//   - sandbox names must pass nexen sandbox.ValidName → "nex.sandbox.max_profile: unknown profile %q"
//   - claude_bin/path_prepend entries, after expansion, must be absolute
//   - timeouts parse with time.ParseDuration when non-empty
func (n *NexConfig) Validate(home string) error

// Expanded returns a copy with "~" expanded (roots, claude_bin, cswap_bin, path_prepend) and paths cleaned.
func (n NexConfig) Expanded(home string) NexConfig
```
`Load` calls `cfg.Nex.Validate(home)` after decoding (defaults merged the
same way the other sections are). `Clone` clones the three slices.

`handlePutConfig`: add `Nex *json.RawMessage \`json:"nex,omitempty"\`` to
`configUpdateRequest`; if non-nil → `400 "nex is not editable via API in this version; edit config.toml and restart"`.

**Tests (write first):**
- defaults: absent section ⇒ `DefaultNexConfig()`; `enabled=false` with no
  roots is valid.
- `enabled=true`, no roots ⇒ error mentions `nex.repo_roots`.
- `max_profile="handoff"` valid; `"yolo"` ⇒ error names `nex.sandbox.max_profile`.
- `claude_bin="~/bin/claude"` ⇒ `Expanded` gives `<home>/bin/claude`;
  `claude_bin="bin/claude"` ⇒ error (relative).
- `path_prepend=["~/.local/bin","rel"]` ⇒ error names `nex.path_prepend[1]`.
- `lease_ttl="12x"` ⇒ error names `nex.timeouts.lease_ttl`.
- TOML round trip: `WriteFile` then `Load` preserves every field
  (including `[nex.sandbox]`, `[nex.timeouts]`).
- `Clone`: mutating the clone's `Nex.RepoRoots[0]` leaves the original.
- `handlePutConfig` with `{"nex":{"enabled":true}}` ⇒ 400; with
  `{"upload_dir":"/tmp/x"}` on a config carrying a `[nex]` section ⇒ 200
  and the persisted file still has the identical `[nex]` block (I15).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(config): [nex] section with pdx-side validation; PUT /api/config rejects nex`

### Task 3: `core.Closer`, `CloseModules`, and the shutdown coordinator

**Files:** edit `internal/core/core.go` (+ append tests); create
`cmd/pdx/shutdown.go`, `cmd/pdx/shutdown_test.go`; edit `cmd/pdx/main.go`
(replace the inline signal goroutine and add `<-done`).

**Public API:**
```go
// internal/core
type Closer interface{ Close() error }
// CloseModules calls Close on every module implementing Closer, in reverse
// registration order, and joins the errors. Modules without Closer are skipped.
func (c *Core) CloseModules() error

// cmd/pdx
type shutdownTarget interface {        // satisfied by *core.Core
    StopModules(context.Context) error
    CloseModules() error
}
type shutdownServer interface {        // satisfied by *http.Server
    Shutdown(context.Context) error
    Close() error
}
// runShutdown executes spec §4.5's sequence once trigger fires and closes done
// when the LAST step has returned. cancel is the module ctx cancel.
// Every error is logged with its step name and never aborts the sequence.
func runShutdown(trigger <-chan struct{}, cancel context.CancelFunc,
    target shutdownTarget, srv shutdownServer, budget time.Duration,
    logf func(string, ...any)) (done <-chan struct{})
```
`main.go`: `done := runShutdown(sigCh-as-struct-chan, cancel, c, srv, 10*time.Second, log.Printf)`;
after `srv.Serve` returns: `<-done`. The existing `c.StopModules` /
`srv.Shutdown` lines move into `runShutdown`; nothing else in `main`
changes. (`shutdownBudget = 10 * time.Second` becomes a named const.)

**Tests (write first):**
- `CloseModules`: three modules, the middle one implements `Closer` and
  records; only it is called; two closers ⇒ reverse order; a closer error
  is returned joined, the other closer still runs.
- `runShutdown` with fakes recording `[]string` steps:
  - normal: `cancel`, `StopModules`, `Shutdown`, `CloseModules`; `done`
    closed after `CloseModules` returned; `srv.Close` **not** called.
  - `Shutdown` returns `context.DeadlineExceeded` ⇒ `Close` called next,
    then `CloseModules`.
  - `StopModules` returns error ⇒ logged; sequence continues.
  - `StopModules` blocks past `budget` (fake sleeps `budget+50ms`, ignores
    ctx) ⇒ the sequence still completes after it returns and `done` closes.
  - real `httptest.Server` + `*http.Server`: a handler that holds a
    streaming response open until a channel closes; `Shutdown(ctx)` with a
    50 ms budget times out, `Close()` runs, `CloseModules` runs after
    `Close` returned — assert by timestamps recorded in the fakes (I7).
- `main.go` compile + the existing `cmd/pdx` tests unchanged.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(core): optional Closer + CloseModules; main waits for the full shutdown sequence`

### Task 4: CORS allows `Last-Event-ID`

**Files:** edit `internal/middleware/middleware.go:88`; append test.

Change `Access-Control-Allow-Headers` to
`Authorization, Content-Type, Last-Event-ID`. Existing CORS tests that
assert the exact old string must be updated in the same commit (this is the
one explicitly allowed edit); grep first and list them in the commit body.

**Tests (write first):** `OPTIONS /x` with
`Access-Control-Request-Headers: last-event-id, authorization` ⇒ 204 and the
allow-headers value contains `Last-Event-ID`; a `GET` still passes through.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `fix(middleware): allow Last-Event-ID in CORS for SSE resume through /api/nex`

### Task 5: Request recoverer

**Files:** create `internal/module/nex/recover.go`, `recover_test.go`.

**API:** `func recoverer(logf func(string, ...any), next http.Handler) http.Handler`
per spec §4.4 (no writer wrap; re-panics `http.ErrAbortHandler`; logs
method, path, value, stack; attempts `WriteHeader(500)`).

**Tests (write first):**
- handler panics before writing ⇒ 500, log line contains method+path+value,
  next request to a healthy handler on the same server ⇒ 200.
- handler writes headers, flushes (assert `w.(http.Flusher)` ok — i.e. the
  writer reaching `next` is the original), writes a line, then panics ⇒
  client received the line; the server keeps serving.
- handler panics with `http.ErrAbortHandler` ⇒ the panic propagates (test
  with `recover()` around `ServeHTTP` in a raw call, not via a server).
- `http.Hijacker` assertion succeeds through the recoverer (type assertion
  on the writer passed to `next`, using `httptest.NewRecorder`-incompatible
  path: use a real server and a handler that asserts).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(nex): request-scoped panic recoverer that preserves Flusher/Hijacker`

### Task 6: PATH policy

**Files:** create `internal/module/nex/pathpolicy.go`, `pathpolicy_test.go`.

**API:**
```go
// composePath implements spec §4.6: prefix = dedupe(entries) filtered by
// isDir; result = prefix ++ (split(current, sep) minus prefix entries),
// re-joined. Pure.
func composePath(current string, entries []string, isDir func(string) bool) string
// applyPathPolicy reads/writes os.Getenv/Setenv("PATH") via composePath and
// returns the final PATH and whether it changed.
func applyPathPolicy(entries []string, isDir func(string) bool) (final string, changed bool)
```

**Tests (write first)** (`composePath`, table-driven, `sep` = `os.PathListSeparator`):
- launchd PATH `/usr/bin:/bin:/usr/sbin:/sbin`, entries `[A, B, C]`, `isDir`
  false for B ⇒ `A:C:/usr/bin:/bin:/usr/sbin:/sbin`.
- entries already present in the middle of PATH ⇒ moved to the front, not
  duplicated.
- duplicate entries in config ⇒ one occurrence.
- entries `[]` ⇒ current unchanged (identical string).
- idempotent: `composePath(composePath(x), …) == composePath(x)`.
- `applyPathPolicy` with `t.Setenv("PATH", …)` ⇒ env updated; second call
  reports `changed=false`.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(nex): deterministic PATH prepend policy for launchd-started daemons`

### Task 7: The `nex` module — config mapping, Init, mount, lifecycle

**Files:** create `internal/module/nex/module.go`, `build_config.go`,
`build_config_test.go`, `module_test.go`, `imports_test.go`.

**API:**
```go
package nex

const RoutePrefix = "/api/nex"

// Deps allow tests to substitute the engine.
type Deps struct {
    Assemble func(context.Context, nexen.Options) (System, error) // default wraps nexen.Assemble
    IsDir    func(string) bool                                     // default os.Stat-based
    Now      func() time.Time
    Logf     func(string, ...any)
}
// System is the subset of *nexen.System the module uses (so a fake can stand in).
type System interface {
    Handler() http.Handler            // adapter over nexen.System.Handler field
    Shutdown(context.Context) error
    Close() error
}
func New() *Module
func NewWithDeps(d Deps) *Module

// buildOptions maps pdx config to Nexen (spec §4.2 table). It validates via
// (*config.Config).Validate() and returns the ready Options.
// Errors: empty hostID → "nex: host_id is empty; cannot derive principal".
func buildOptions(hostID, dataDir string, n pdxconfig.NexConfig, shutdownBudget time.Duration) (nexen.Options, error)
```
`Init(c)`: `applyPathPolicy` → `buildOptions(c.Cfg.HostID, c.Cfg.DataDir, c.Cfg.Nex.Expanded(home), shutdownBudget)`
→ `MkdirAll(DataDir)` → `d.Assemble(context.Background(), opts)`; store
`sys`. `RegisterRoutes`: `mux.Handle(RoutePrefix+"/", http.StripPrefix(RoutePrefix, recoverer(logf, sys.Handler())))`.
`Start`: the log line. `Stop(ctx)`: `return sys.Shutdown(ctx)`. `Close()`:
`sys.Close()`.

**Tests (write first):**
- `buildOptions`: full mapping asserted field by field (HostID, DataDir =
  `<data>/nex`, roots, sandbox policy, timeouts parsed, ShutdownTimeout =
  budget, `PublicPrefix == "/api/nex"`, `ClaudeBin` passthrough,
  `Auth` non-nil and `Authenticate(anyReq)` returns `"pdx:<host>"`);
  empty HostID ⇒ error; zero timeouts ⇒ after `Validate` the Config carries
  Nexen's defaults (I13: assert `LeaseTTL` non-zero); invalid root (not
  absolute) ⇒ Nexen's Validate error surfaces.
- `Init` order with fake Assemble capturing `Options` and a fake `isDir`:
  PATH applied **before** Assemble (record order); `DataDir/nex` created;
  Assemble error ⇒ `Init` error wrapping it (I12: table of causes — missing
  HostID, unwritable DataDir via a file where the dir should be,
  `ClaudeBin` set to a non-existent path is Nexen's error and just needs
  to surface).
- `RegisterRoutes` + fake System handler echoing `r.URL.Path`: request to
  `/api/nex/v1/x` reaches the fake with path `/v1/x`; `/api/nexus` ⇒ 404.
- `Stop` returns the fake's Shutdown error unchanged; `Close` calls the fake.
- `imports_test.go` (I6): parse the package with `go/parser`
  (`parser.ImportsOnly`) and assert no import path under
  `lab.protype.tw/wake/nexen/` other than `/api`, `/config`, `/sandbox`.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(nex): module wiring — config mapping, PATH policy, mount, Stop/Close`

### Task 8: Registration gate, `/api/info`, and the start log

**Files:** edit `cmd/pdx/main.go` (`registerServeModules`), edit
`internal/core/info_handler.go` (+ append test), edit `cmd/pdx` tests that
cover `registerServeModules` if any (append only).

- `registerServeModules`: `if cfg.Nex.Enabled { c.AddModule(nex.New()) } else { log.Printf("nex: disabled") }`.
- `Core` gains `Mounted(name string) bool` (true if a module with that name
  was added) — one method, tested.
- `/api/info` adds `"nex": {"configured": c.Cfg.Nex.Enabled, "mounted": c.Mounted("nex")}`.

**Tests (write first):**
- `registerServeModules` with `Nex.Enabled=false` ⇒ no module named `nex`;
  `true` ⇒ present (module list inspection via `Mounted`).
- info handler: both combinations of configured/mounted render correctly.
- I1 end to end (fake engine): enabled=false daemon → after a valid bearer,
  `GET /api/nex/v1/capabilities` is 404 and `DataDir/nex` absent and PATH
  untouched (check via `os.Getenv` in-process with `t.Setenv`).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(nex): register only when enabled; /api/info reports configured/mounted`

### Task 9: `pdx nex …` CLI

**Files:** create `cmd/pdx/nex.go`, `cmd/pdx/nex_test.go`; edit `cmd/pdx/main.go`
(`case "nex":`).

**API:**
```go
// runNex resolves addr/token (flags > env PDX_NEX_ADDR/PDX_NEX_TOKEN > config.toml)
// and delegates to client.Run. On client error it probes GET <base>/v1/capabilities:
// 404 → prints the not-enabled message; otherwise the original error. Exit 1 on error.
func runNex(args []string, stdout, stderr io.Writer, lookupConfig func() (*config.Config, error), probe func(base, token string) (int, error)) int
```
Flags parsed with `flag.NewFlagSet("nex", flag.ContinueOnError)`; the first
non-flag token and everything after go to `client.Run` verbatim
(`fs.Args()`).

**Tests (write first)** (httptest server standing in for the daemon):
- no flags/env, config `{Bind:"127.0.0.1", Port:<test>, Token:"t"}` ⇒
  request hits `<test>/api/nex/v1/...` with `Authorization: Bearer t`.
- `--addr` overrides config; `PDX_NEX_ADDR` overrides config but not `--addr`.
- server returns 404 for everything ⇒ stderr contains `not enabled on this
  host`, exit 1 (I10).
- server returns 200 for capabilities but 404 `execution_not_found` for
  `show x` ⇒ stderr contains `execution_not_found`, not the enabled message.
- no subcommand ⇒ `client.Run`'s usage error, exit 1.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(cli): pdx nex wraps the Nexen client against the local daemon`

### Task 10: Engine-mounted invariants and the auth matrix

**Files:** create `internal/module/nex/mount_test.go`; append to
`cmd/pdx/http_chain_test.go`.

**mount_test.go** — real `nexen.Assemble` via the module, fake claude
fixture, `t.TempDir()` DataDir, a root = `t.TempDir()`, `httptest.Server`
around `mux` (no outer chain — these are engine invariants):
- I3: `GET /v1/capabilities` (after prefix) ⇒ `lease.renew.path` begins
  `/api/nex/v1/`; delegate + `attach(observe)` ⇒ `stream_url` begins
  `/api/nex/v1/events?`.
- I4: `GET /api/nex/v1/executions/exc_nope` ⇒ 404 with Nexen's
  `execution_not_found` body.
- I5: after `module.Stop(ctx)`: `POST /api/nex/v1/executions` ⇒ 503 body
  `draining`; `GET /api/nex/v1/executions` ⇒ 200.
- I11: delegate ⇒ events for that id contain `execution.delegated` with
  `principal_id == "pdx:<host>"`; `attach(control)` response lease owner is
  the same.
- I14 (engine half): `GET /api/nex/v1/events?execution_id=<id>` with
  `Last-Event-ID: <seq of the 1st event>` yields the 2nd event first.

**http_chain_test.go** (I2) — `newOuterHandler` with a probe handler mounted
at `/api/nex/` on the inner mux that records whether it was reached;
`Cfg.Token = "t"`, one peer host with inbound token `p`:

| credential / state | GET | POST | GET `Accept: text/event-stream` | probe reached |
|---|---|---|---|---|
| none | | | | ✗ |
| `Bearer wrong` | | | | ✗ |
| `Bearer t` | | | | ✓ |
| `?ticket=<fresh>` | | | | ✓ |
| same ticket again | | | | ✗ |
| `Bearer p` (peer token) at `/api/nex/…` | | | | ✗ |
| pairing state active, `Bearer t` | | | | ✗ (chain's 503/409 — assert not reached only) |
| non-whitelisted RemoteAddr, `Bearer t` | | | | ✗ |
| `Bearer t`, path `/api/nex/../api/health` | | | | ✗ (net/http redirects/cleans; assert probe not reached) |
| `Bearer t`, path `//api/nex/v1/x` | | | | ✗ |
| `OPTIONS` no credential | 204 | | | ✗ (CORS answers) |

- [ ] tests written and failing
- [ ] implementation (no production change expected; if any invariant
      fails, fix in the responsible earlier task's files and note it)
- [ ] commit `test(nex): engine-mounted invariants and outer-chain auth matrix`

### Task 11: Pin N1 and live check on mlab (spec §6)

**Files:** `go.mod` / `go.sum` (final pseudo-version), PR description.

- After N1 merges: `go get lab.protype.tw/wake/nexen@<merge-sha>`, remove
  the local `go.work` entry for the N1 worktree (keep the file gitignored),
  `go mod tidy`, full `go build/vet/test`.
- Step 0: cold-cache build over non-interactive ssh
  (`GOWORK=off GOMODCACHE=$(mktemp -d)`), then `pnpm run electron:build`,
  then a dev-update rebuild triggered through the running daemon.
- Steps 1–7 of spec §6 on mlab with the real daemon (`[nex] enabled = true`,
  `max_profile = "handoff"`), each with the command run and the relevant
  `pdx.log` / `pdx nex events` excerpt pasted into the PR.
- Step 6 on a26 after the App's local daemon update.

- [ ] go.mod pinned to the N1 merge commit
- [ ] §6 steps 0–7 recorded in the PR
- [ ] commit `build: pin nexen to N1 merge <sha>`

---

## Task dependency

```
T1 ──┬─ T2 ─┐
     ├─ T3  │
     ├─ T4  ├─ T7 ─ T8 ─┬─ T9
     ├─ T5 ─┤           └─ T10 ─ T11
     └─ T6 ─┘
```
T2–T6 are independent of each other and may run as parallel subagents
(`git commit --only`). T7 needs T2, T5, T6. T8 needs T7 and T3. T9 and T10
need T8. T11 needs everything and N1 merged.
