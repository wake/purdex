# P-A Implementation Plan — embed Nexen as the pdx `nex` module

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Plan v2 (after codex plan review `task-mu1mwztp-a9d9q2`: 1 Blocker, 7 Majors,
4 Minors — all applied; disposition table at the end).

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
then a gitignored `go.work` points at the local N1 worktree
(`/Users/wake/Workspace/wake/nexen/.claude/worktrees/n1-library-seams`) and
`go.mod` carries a placeholder pseudo-version that Task 12 replaces.

## Global Constraints

- **TDD, no exceptions.** Failing test first, run it, implement, run again.
  Each task is one commit. The only tests allowed to be green on first run
  are the ones a task explicitly labels *characterization* (they pin
  existing behaviour).
- **Tasks run serially, in the order listed.** The verification rule below
  requires the whole tree green at every commit, which a second agent's
  in-progress red test would break; `git commit --only` isolates files, not
  test runs. (If wall-clock matters, T2–T6 may run in **separate
  worktrees** off this branch and be merged in order — never in this
  worktree concurrently.)
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
- **Existing tests are not edited** except where a task says so explicitly
  (Task 4's CORS header assertions). If a task cannot pass otherwise, stop
  and report.
- **No real `claude` or `cswap` in tests.** Engine-level tests use the fake
  claude script below and a fake `cswap` (`#!/bin/sh\necho '{}'`) written
  into `t.TempDir()`, passed via `ClaudeBin` / `CswapBin`. No test touches
  the daemon's real `DataDir`, `PATH`, `~/.config/pdx`, `$(go env GOENV)`
  or the global git config; PATH tests use `t.Setenv`, build-preflight
  tests use `GOENV=<tmp>` and `GIT_CONFIG_GLOBAL=<tmp>`.
- **Package boundaries:** `internal/module/nex` imports from Nexen only
  `lab.protype.tw/wake/nexen`, `…/api`, `…/config`, `…/sandbox` (spec I6);
  it imports `internal/core` and `internal/config` from pdx. `internal/config`
  imports Nexen's `sandbox` only (for `ValidName`). `cmd/pdx` imports
  Nexen's `cmd/nex/client` for Task 10.
- **Nexen symbol names** are those of N1 spec §4.4.2 / §7. If the merged N1
  differs, the task adapts to N1 — N1 is the authority — and the deviation
  is noted in the commit message. Current N1 facts the tests rely on:
  `config.Config.Validate()` does **not** check that roots exist (that is
  the resolver's job inside `Assemble`); `Validate` does reject negative
  durations and unknown profile names.

### Fake claude fixture (Tasks 7c, 11)

Written into `t.TempDir()` as `claude`, `chmod 0755`. Nexen's
`adapter/testdata/fake-claude-ok.sh` shape; sufficient for delegate and
send because every turn is a fresh process (init → one assistant message →
result; `result` makes the adapter close stdin, then `cat` sees EOF):

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
go.mod / go.sum                              T1, T12  require nexen, go 1.26; final pin
.gitignore                                   T1       go.work, go.work.sum
Makefile                                     T1       check-goenv target
scripts/check-goenv.sh                       T1       the preflight (testable with env overrides)
scripts/check-goenv_test.sh                  T1       two-path shell test
README.md                                    T1       build prerequisites
internal/config/nex.go                       T2       NexConfig, defaults, validate, expand
internal/config/nex_test.go                  T2
internal/config/config.go                    T2       Nex field, Clone slices, Load hook
internal/core/config_handler.go              T2       reject "nex" in PUT
internal/core/config_handler_test.go         T2       (append only)
internal/core/core.go                        T3a, T9  Closer, CloseModules; Mounted
internal/core/core_test.go                   T3a, T9  (append only)
cmd/pdx/shutdown.go                          T3b      runShutdown + serveAndWait
cmd/pdx/shutdown_test.go                     T3b
cmd/pdx/main.go                              T3b, T9, T10  use serveAndWait; register nex; "nex" subcommand
internal/middleware/middleware.go            T4       Last-Event-ID in CORS
internal/middleware/middleware_test.go       T4       (edit assertions listed in the task)
internal/module/nex/recover.go               T5
internal/module/nex/recover_test.go          T5
internal/module/nex/pathpolicy.go            T6
internal/module/nex/pathpolicy_test.go       T6
internal/module/nex/build_config.go          T7a      pdx cfg → nexen config.Config + Options
internal/module/nex/build_config_test.go     T7a
internal/module/nex/module.go                T7b, T7c Module: Init/Start/Stop/Close; RegisterRoutes
internal/module/nex/module_test.go           T7b      Init/lifecycle with real Assemble failure cases
internal/module/nex/mount_test.go            T7c      I3 I4 I5 I11 I14 with real Assemble + fake claude
internal/module/nex/imports_test.go          T7a      I6
internal/core/info_handler.go                T9       nex: {configured, mounted}
internal/core/info_handler_test.go           T9       (append only)
cmd/pdx/nex.go                               T10      pdx nex wrapper
cmd/pdx/nex_test.go                          T10
cmd/pdx/http_chain_test.go                   T11      (append) I2 probe matrix + nex preflight
```

---

### Task 1: Module dependency and build preflight

**Files:** `go.mod`, `go.sum`, `.gitignore`, `Makefile`,
`scripts/check-goenv.sh`, `scripts/check-goenv_test.sh`, `README.md`.

- `go.mod`: `go 1.26.0`; add `require lab.protype.tw/wake/nexen v0.0.0-<placeholder pseudo of the N1 worktree HEAD>`
  by hand. **Do not run `go mod tidy` in this task** — nothing imports the
  module yet and tidy would drop the require. Run `go mod download` (via
  `go.work`) and `go build ./...`. Tidy happens in T7a, the first import.
  Confirm `go list -m modernc.org/sqlite` reports ≥ 1.54.0.
- `.gitignore`: add `go.work` and `go.work.sum`. Create the local `go.work`
  (`go work init . /Users/wake/Workspace/wake/nexen/.claude/worktrees/n1-library-seams`) — **not committed**.
- `scripts/check-goenv.sh` (sh, no bash-isms):
  ```sh
  #!/bin/sh
  # Preflight for building pdx with the private Nexen module (spec §3.1).
  ok=1
  case ",$(go env GOPRIVATE)," in *,lab.protype.tw,*|*lab.protype.tw*) ;; *)
    echo 'missing: go env -w GOPRIVATE=lab.protype.tw'; ok=0;; esac
  if ! git config --global --get-all 'url.ssh://git@lab.protype.tw:9079/.insteadOf' 2>/dev/null | grep -qx 'https://lab.protype.tw/'; then
    echo 'missing: git config --global url."ssh://git@lab.protype.tw:9079/".insteadOf "https://lab.protype.tw/"'; ok=0
  fi
  [ "$ok" = 1 ]
  ```
  (The v1 `--get-urlmatch` form was wrong: the subsection is the ssh URL and
  the *value* is the https prefix.)
- `Makefile`: `check-goenv: ; @sh scripts/check-goenv.sh` and `build: check-goenv`.
- `README.md`: build section with the two one-time commands and the cold
  cache verification command (spec §6 step 0).

**Tests (write first):** `scripts/check-goenv_test.sh` runs the preflight
twice under isolated env — `GOENV=$tmp/goenv GIT_CONFIG_GLOBAL=$tmp/gitconfig`
— once with both settings written into those files (`go env -w` honours
`GOENV`; `git config --file`) expecting exit 0, once with the files empty
expecting exit 1 and both `missing:` lines. Also `make -n build` lists
`check-goenv` first. The existing `go test ./internal/store/...` run on
sqlite 1.54 is recorded in the commit body (MVS regression).

- [ ] preflight test written and failing (script absent)
- [ ] script + Makefile + README, test green; `go build ./...` green
- [ ] `go test ./internal/store/...` green on sqlite 1.54, output in commit body
- [ ] commit `build: require lab.protype.tw/wake/nexen, go 1.26, goenv preflight`

### Task 2: `[nex]` config section

**Files:** create `internal/config/nex.go`, `internal/config/nex_test.go`;
edit `internal/config/config.go` (`Nex NexConfig` field `toml:"nex" json:"nex"`,
defaults, `Clone`, validation call in `Load`); edit
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

// Validate is the pdx-side layer (spec §4.2 layer 1). Every error names the key.
//   - enabled && no roots → "nex.repo_roots: at least one root required when nex.enabled"
//   - sandbox names must pass nexen sandbox.ValidName → "nex.sandbox.max_profile: unknown profile %q"
//   - claude_bin / cswap_bin / path_prepend[i] / roots, after "~" expansion, must be absolute
//   - non-empty timeouts parse with time.ParseDuration
// It does not touch the filesystem.
func (n *NexConfig) Validate(home string) error

// Expanded returns a copy with "~" expanded (roots, claude_bin, cswap_bin, path_prepend) and paths cleaned.
func (n NexConfig) Expanded(home string) NexConfig
```
`Load` calls `cfg.Nex.Validate(home)` after decoding. `Clone` clones the
three slices.

`handlePutConfig`: add `Nex json.RawMessage \`json:"nex"\`` (non-pointer)
to `configUpdateRequest`; `len(req.Nex) > 0` (which includes the literal
`null`) → `400 "nex is not editable via API in this version; edit config.toml and restart"`.

**Tests (write first):**
- defaults: absent section ⇒ `DefaultNexConfig()`; `enabled=false` with no
  roots is valid.
- `enabled=true`, no roots ⇒ error mentions `nex.repo_roots`.
- `max_profile="handoff"` valid; `"yolo"` ⇒ error names `nex.sandbox.max_profile`.
- `claude_bin="~/bin/claude"` ⇒ `Expanded` gives `<home>/bin/claude`;
  `"bin/claude"` ⇒ error names `nex.claude_bin`.
- `path_prepend=["~/.local/bin","rel"]` ⇒ error names `nex.path_prepend[1]`.
- `repo_roots=["rel/x"]` ⇒ error names `nex.repo_roots[0]`.
- `lease_ttl="12x"` ⇒ error names `nex.timeouts.lease_ttl`.
- TOML round trip: `WriteFile` then `Load` preserves every field.
- `Clone`: mutating each of the clone's `RepoRoots[0]`, `ServiceRoots[0]`,
  `PathPrepend[0]` leaves the original (three assertions).
- `handlePutConfig`: `{"nex":{"enabled":true}}` ⇒ 400; `{"nex":null}` ⇒ 400;
  `{"upload_dir":"/tmp/x"}` on a config with a full `[nex]` section ⇒ 200
  and the persisted file's `[nex]` block is byte-identical (I15).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(config): [nex] section with pdx-side validation; PUT /api/config rejects nex`

### Task 3a: `core.Closer` and `CloseModules`

**Files:** edit `internal/core/core.go` (+ append tests).

```go
type Closer interface{ Close() error }
// CloseModules calls Close on every module implementing Closer, in reverse
// registration order, joining errors; modules without Closer are skipped.
func (c *Core) CloseModules() error
```

**Tests (write first):** three modules, only the middle implements `Closer`
⇒ only it is called; two closers ⇒ reverse order; a closer error is joined
and the other closer still runs; zero closers ⇒ nil.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(core): optional Closer interface and CloseModules`

### Task 3b: Shutdown coordinator and serve-and-wait

**Files:** create `cmd/pdx/shutdown.go`, `cmd/pdx/shutdown_test.go`; edit
`cmd/pdx/main.go` (the signal goroutine and the `Serve` call are replaced
by one `serveAndWait` call).

**API:**
```go
const shutdownBudget = 10 * time.Second

type shutdownTarget interface { StopModules(context.Context) error; CloseModules() error } // *core.Core
type shutdownServer interface { Shutdown(context.Context) error; Close() error }          // *http.Server
type server interface { shutdownServer; Serve(net.Listener) error }

// serveAndWait runs srv.Serve(ln) and guarantees spec §4.5's sequence runs
// exactly once — triggered by the first of: a value on sig, or Serve
// returning (error or ErrServerClosed) — and does not return until the
// sequence's last step (CloseModules) has returned. Sequence:
//   cancel(); ctx = WithTimeout(budget)
//   StopModules(ctx)            error logged, continue
//   srv.Shutdown(ctx)           error (incl. deadline) logged → srv.Close()
//   CloseModules()              error logged
// The SAME ctx is passed to StopModules and Shutdown (N1 rule 1).
// Returns Serve's error unless it is http.ErrServerClosed.
func serveAndWait(srv server, ln net.Listener, sig <-chan os.Signal,
    cancel context.CancelFunc, target shutdownTarget, budget time.Duration,
    logf func(string, ...any)) error
```
`main.go`: `sigCh` stays `chan os.Signal`; the goroutine body moves into
`serveAndWait`; `runServe` does `if err := serveAndWait(srv, listener, sigCh, cancel, c, shutdownBudget, log.Printf); err != nil { log.Printf("server error: %v", err) }`.
Behaviour preserved: a `Serve` failure still logs and returns — but now
after modules were stopped and closed, instead of exiting with them
half-open.

**Tests (write first)** — fakes record `[]string` steps with timestamps and
the ctx they received:
- signal path: order `cancel, StopModules, Shutdown, CloseModules`; `Close`
  **not** called; `serveAndWait` returns nil (fake Serve returns
  `ErrServerClosed` after Shutdown).
- Serve-fails-first path (fake Serve returns `errors.New("bind lost")`
  immediately, no signal ever sent): the full sequence still runs; return
  value is that error.
- signal **and** Serve error racing: sequence runs exactly once (counter).
- `Shutdown` returns `context.DeadlineExceeded` ⇒ `Close` next, then `CloseModules`.
- `StopModules` error ⇒ logged, sequence continues.
- `StopModules` ignores ctx and sleeps `budget+50ms` ⇒ sequence still
  completes; `Shutdown` receives an already-expired ctx (assert
  `ctx.Err() != nil` when called).
- same-ctx: the ctx recorded by `StopModules` and by `Shutdown` are the
  same value (`==`).
- **wait proof**: fake `CloseModules` blocks on a channel; assert
  `serveAndWait` has not returned (select with timeout) until the channel
  is released, then it returns.
- real `*http.Server` + `httptest` listener: handler writes headers,
  flushes, then blocks until a test channel closes; test waits for the
  handler to signal "flushed", then sends the signal; with `budget` 50 ms
  `Shutdown` times out, `Close` runs, `CloseModules` runs after `Close`
  returned (timestamps) (I7). Test cleanup closes the channel.

- [ ] tests written and failing
- [ ] implementation (+ `main.go` switch), all green; existing `cmd/pdx` tests untouched and green
- [ ] commit `feat(pdx): serveAndWait runs the full shutdown sequence once and waits for it`

### Task 4: CORS allows `Last-Event-ID`

**Files:** edit `internal/middleware/middleware.go:88`; edit
`internal/middleware/middleware_test.go` **only** where an assertion pins
the exact old `Access-Control-Allow-Headers` string (grep for it; list the
edited test names in the commit body).

**Tests (write first):** `OPTIONS /x` with
`Access-Control-Request-Headers: last-event-id, authorization` ⇒ 204 and
allow-headers contains `Last-Event-ID`; a `GET` still passes through.

- [ ] test written and failing
- [ ] implementation, all green
- [ ] commit `fix(middleware): allow Last-Event-ID in CORS for SSE resume through /api/nex`

### Task 5: Request recoverer

**Files:** create `internal/module/nex/recover.go`, `recover_test.go`.

**API:** `func recoverer(logf func(string, ...any), next http.Handler) http.Handler`
per spec §4.4 (no writer wrap; re-panics `http.ErrAbortHandler`; logs
method, path, value, stack; attempts `WriteHeader(500)`).

**Tests (write first)** (real `httptest.Server`; the server's `ErrorLog`
captured so net/http's "superfluous WriteHeader" does not pollute output):
- panic before writing ⇒ 500; log contains method, path, value; next
  request to a healthy route ⇒ 200.
- handler asserts `w.(http.Flusher)` ok, writes headers, flushes one line,
  then panics ⇒ client reads that line **and then EOF** (response ended);
  server keeps serving.
- `http.ErrAbortHandler` ⇒ propagates (raw `ServeHTTP` call wrapped in
  `recover()` in the test).
- handler asserts `w.(http.Hijacker)` ok through the recoverer.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(nex): request-scoped panic recoverer that preserves Flusher/Hijacker`

### Task 6: PATH policy (pure)

**Files:** create `internal/module/nex/pathpolicy.go`, `pathpolicy_test.go`.

```go
// composePath (spec §4.6): prefix = dedupe(entries) filtered by isDir;
// result = prefix ++ (split(current) minus prefix entries). Pure. entries
// must already be "~"-expanded absolute paths (the caller's job).
func composePath(current string, entries []string, isDir func(string) bool) string
// applyPathPolicy: os.Getenv/Setenv("PATH") through composePath.
func applyPathPolicy(entries []string, isDir func(string) bool) (final string, changed bool)
```

**Tests (write first)** (table-driven, `os.PathListSeparator`):
- launchd PATH, entries `[A,B,C]`, `isDir(B)=false` ⇒ `A:C:<launchd PATH>`.
- entry already in the middle of PATH ⇒ moved to the front, once.
- duplicate entries ⇒ once. `entries=[]` ⇒ identical string. Idempotent.
- `applyPathPolicy` with `t.Setenv` ⇒ env updated; second call `changed=false`.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(nex): deterministic PATH prepend policy`

### Task 7a: Options mapping (`buildOptions`) and the import invariant

**Files:** create `internal/module/nex/build_config.go`, `build_config_test.go`,
`imports_test.go`. First real import of Nexen ⇒ run `go mod tidy` here and
commit `go.sum` changes.

```go
package nex
const RoutePrefix = "/api/nex"
// buildOptions maps pdx config to Nexen (spec §4.2 table), runs
// (*config.Config).Validate(), and returns ready Options. n must already be
// Expanded(). Errors: empty hostID → "nex: host_id is empty; cannot derive principal";
// Validate errors wrapped with "nex: config: ".
func buildOptions(hostID, dataDir string, n pdxconfig.NexConfig, shutdownBudget time.Duration) (nexen.Options, error)
```

**Tests (write first):**
- full mapping asserted field by field on the returned `Options.Config`:
  `HostID`, `DataDir == filepath.Join(dataDir,"nex")`, roots, sandbox
  policy, parsed timeouts, `ShutdownTimeout == budget`; `PublicPrefix ==
  RoutePrefix`; `ClaudeBin` passthrough; `Auth != nil` and
  `Auth.Authenticate(httptest.NewRequest(...))` returns `("pdx:<host>", nil)`.
- I13: input with all timeouts `""` ⇒ returned `Config.LeaseTTL` (and the
  others Nexen defaults) are non-zero — i.e. `Validate` ran and applied
  defaults to the value `Assemble` will receive.
- empty hostID ⇒ error.
- a Validate-detected error surfaces with the `nex: config:` prefix — use a
  **negative duration** (`lease_ttl = "-1s"`), which pdx's layer lets
  through and Nexen's `Validate` rejects. (Root existence is *not* a
  Validate concern — see Global Constraints — and is tested in T7b.)
- `imports_test.go` (I6): `go/parser` with `parser.ImportsOnly` over the
  package's non-test files ⇒ no import path under
  `lab.protype.tw/wake/nexen/` other than `/api`, `/config`, `/sandbox`.

- [ ] tests written and failing
- [ ] implementation, `go mod tidy`, all green
- [ ] commit `feat(nex): map [nex] config to nexen.Options; enforce import boundary`

### Task 7b: Module Init / Start / Stop / Close

**Files:** create `internal/module/nex/module.go`, `module_test.go`.

```go
type Module struct{ /* core, sys engine, opts, logf */ }
func New() *Module

// engine is the private seam over *nexen.System so tests can substitute.
type engine struct {
    handler  http.Handler
    shutdown func(context.Context) error
    close    func() error
}
// assembleFn defaults to a thin adapter over nexen.Assemble producing engine.
type assembleFn func(context.Context, nexen.Options) (engine, error)
```
Private fields `assemble assembleFn`, `isDir func(string) bool`, `logf`
with production defaults; tests in the same package set them directly (the
repo's private-function-field convention, as in `peers`/`dev`).

`Init(c)`:
1. `n := c.Cfg.Nex.Expanded(home)` — **expansion first**, so `~/.local/bin`
   is a real directory by the time it is checked.
2. `applyPathPolicy(n.PathPrepend, m.isDir)`; log once.
3. `opts, err := buildOptions(c.Cfg.HostID, c.Cfg.DataDir, n, shutdownBudget)`.
4. `os.MkdirAll(opts.Config.DataDir, 0o755)`.
5. `m.sys, err = m.assemble(context.Background(), opts)`.
`Start`: log line. `Stop(ctx)`: `return m.sys.shutdown(ctx)`. `Close()`:
`return m.sys.close()`.

**Tests (write first):**
- order: fake `assemble` records `os.Getenv("PATH")` at call time; with
  `t.Setenv("PATH", launchd)` and `path_prepend=["~/.local/bin", "<tmp existing>", "<tmp missing>"]`
  (`isDir` real, `~/.local/bin` created under a fake `home` via `t.Setenv("HOME", tmp)`)
  ⇒ PATH seen by `assemble` starts with `<home>/.local/bin:<tmp existing>:`
  and does not contain `<tmp missing>`; second `Init` on a fresh Module
  leaves PATH identical; `path_prepend=[]` ⇒ PATH untouched (I9 through
  Init).
- fake `assemble` captures `Options`: `DataDir` exists after Init;
  `Options` equals `buildOptions` output.
- `Stop` returns the fake's error unchanged; `Close` calls the fake's close.
- I12 with the **real** `nexen.Assemble` (no fake claude needed for these —
  they fail before spawning), each expecting `Init` to return an error
  naming the cause:
  - `ClaudeBin` set to a path that does not exist;
  - `ClaudeBin` set to an existing non-executable file;
  - `ClaudeBin == ""` with `PATH` = a temp dir containing no `claude`
    (`path_prepend=[]`) ⇒ LookPath failure;
  - a repo root that is a regular file;
  - `DataDir` whose `nex` entry is a regular file (MkdirAll fails);
  - empty HostID (from `buildOptions`).
  Each uses a fake `CswapBin`.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(nex): module Init/Stop/Close with PATH policy and real Assemble failure paths`

### Task 7c: Mount and engine-level invariants

**Files:** edit `internal/module/nex/module.go` (`RegisterRoutes`); create
`mount_test.go`.

`RegisterRoutes(mux)`: `mux.Handle(RoutePrefix+"/", http.StripPrefix(RoutePrefix, recoverer(m.logf, m.sys.handler)))`.

**Tests (write first)** — real `nexen.Assemble` via `Init` on a fresh
`http.ServeMux` (no outer chain), fake claude + fake cswap, `t.TempDir()`
DataDir and root, `httptest.Server`:
- routing: `/api/nex/v1/capabilities` ⇒ 200; `/api/nexus/v1/capabilities` ⇒ 404.
- I3: `capabilities.lease.renew.path` and `.release.path` start with
  `/api/nex/v1/`; delegate then `attach(observe)` ⇒ `stream_url` starts
  with `/api/nex/v1/events?`.
- I4: `GET /api/nex/v1/executions/exc_nope` ⇒ 404 with Nexen's
  `execution_not_found` body.
- I5: after `Stop(ctx)`: `POST /api/nex/v1/executions` ⇒ 503 `draining`;
  `GET /api/nex/v1/executions` ⇒ 200.
- I11: delegate, wait for the turn's `result` via the events endpoint,
  `attach(control)` ⇒ lease owner `pdx:<host>`; `send` with the lease ⇒
  events now contain `execution.delegated` **and**
  `execution.message_accepted`, both with `principal_id == "pdx:<host>"`.
- I14 (engine half): `GET /api/nex/v1/events?execution_id=<id>` with
  `Last-Event-ID: <seq of the 1st event>` (read from the history page)
  yields the 2nd event first (read one SSE frame, then close).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(nex): mount under /api/nex with prefix rendering; engine-level invariants`

### Task 8 — (merged into 7c)

### Task 9: Registration gate, `/api/info`, and `Core.Mounted`

**Files:** edit `cmd/pdx/main.go` (`registerServeModules`), edit
`internal/core/core.go` (`Mounted`) + append core test, edit
`internal/core/info_handler.go` + append test.

- `Core.Mounted(name string) bool` — true if a module with that `Name()`
  was added.
- `registerServeModules`: `if cfg.Nex.Enabled { c.AddModule(nex.New()) } else { log.Printf("nex: disabled") }`.
- `/api/info` adds `"nex": {"configured": <Cfg.Nex.Enabled>, "mounted": c.Mounted("nex")}`.

**Tests (write first):**
- `Mounted` true/false.
- `registerServeModules` with `Nex.Enabled=false` ⇒ `!Mounted("nex")`;
  `true` ⇒ `Mounted("nex")` (the existing monitor registration test's
  config has `Nex` zero ⇒ still disabled, unchanged).
- info handler renders both fields for both combinations (set `Cfg.Nex.Enabled`
  and add/omit a stub module named `nex`).
- I1 end to end: enabled=false daemon (`registerServeModules` + real mux +
  `newOuterHandler` with `Token="t"`), `GET /api/nex/v1/capabilities` with
  `Bearer t` ⇒ 404; `<DataDir>/nex` absent; PATH unchanged (`t.Setenv`).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(nex): register only when enabled; /api/info reports configured/mounted`

### Task 10: `pdx nex …` CLI

**Files:** create `cmd/pdx/nex.go`, `cmd/pdx/nex_test.go`; edit `cmd/pdx/main.go`
(`case "nex":`).

```go
// runNex resolves addr/token — flags > env (PDX_NEX_ADDR / PDX_NEX_TOKEN) >
// config.toml — and calls client.Run(stdout, fs.Args(), base, token). On a
// client error it calls probe(base, token) exactly once: 404 → prints
// "nex: not enabled on this host (set [nex] enabled = true)"; otherwise the
// original error verbatim. Exit 1 on any error.
func runNex(args []string, stdout, stderr io.Writer, env func(string) string,
    lookupConfig func() (*config.Config, error), probe func(base, token string) (int, error)) int
```

**Tests (write first)** (`httptest` server standing in for the daemon,
recording every request; `probe` is a counting fake unless the test says
"real"):
- config only ⇒ request to `<srv>/api/nex/v1/…` with `Authorization: Bearer <cfg token>`.
- precedence: `--addr` > `PDX_NEX_ADDR` > config; `--token` > `PDX_NEX_TOKEN` > config
  (four cases).
- args pass through verbatim: `runNex([]string{"delegate","--cwd","/x","--brief","b"})`
  ⇒ the fake server sees the delegate request `client.Run` builds for
  those args (assert cwd/brief in the body).
- success path: `probe` called **0** times.
- server 404 for everything ⇒ stderr has the not-enabled message, exit 1,
  probe called once (I10).
- server 200 on capabilities, 404 `execution_not_found` on `show x` ⇒
  stderr contains `execution_not_found`, not the enabled message.
- no subcommand ⇒ `client.Run`'s usage error, exit 1, probe not called.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(cli): pdx nex wraps the Nexen client against the local daemon`

### Task 11: Outer-chain auth matrix and nex preflight (characterization)

**Files:** append to `cmd/pdx/http_chain_test.go`.

These pin behaviour that T3b–T9 already produce; they are expected green on
first run and are labelled characterization. Setup: inner `http.ServeMux`
with `/api/nex/` → a probe handler recording `reached`; outer =
`newOuterHandler(c, inner, []string{"127.0.0.1"})`; `Cfg.Token="t"`; one
peer host with inbound token `p`; requests via `outer.ServeHTTP` directly
(no client, so dirty paths are not auto-redirected); fresh tickets from
`c.Tickets.Generate()` per row; pairing via `c.Pairing.Set(core.StatePairing)`;
IP rejection via `RemoteAddr = "10.9.8.7:1"`.

| row | method(s) | expected status | probe |
|---|---|---|---|
| no credential | GET, POST, GET+`Accept: text/event-stream` | 401 | ✗ |
| `Bearer wrong` | same three | 401 | ✗ |
| `Bearer t` | same three | 200 (probe's) | ✓ |
| `?ticket=<fresh>` | same three (fresh each) | 200 | ✓ |
| same ticket reused | GET | 401 | ✗ |
| `Bearer p` at `/api/nex/v1/x` | GET | 401 | ✗ |
| pairing active, `Bearer t` | GET | 503 | ✗ |
| `RemoteAddr` off-list, `Bearer t` | GET | 403 | ✗ |
| `Bearer t`, `/api/nex/../api/health` | GET | (301 from ServeMux cleaning) | ✗ |
| `Bearer t`, `//api/nex/v1/x` | GET | (301) | ✗ |
| `OPTIONS /api/nex/v1/events` with `Access-Control-Request-Headers: last-event-id, authorization`, no credential | OPTIONS | 204, allow-headers contains `Last-Event-ID` | ✗ (I14 outer half) |

- [ ] matrix written (characterization; green expected)
- [ ] if any row fails: fix in the responsible task's files, note in commit
- [ ] commit `test(pdx): outer-chain auth matrix for /api/nex and SSE preflight`

### Task 12: Pin N1 and live check on mlab (spec §6)

**Files:** `go.mod` / `go.sum` (final pseudo-version), PR description.

- After N1 merges: `go get lab.protype.tw/wake/nexen@<merge-sha>`, remove
  the N1 entry from the local `go.work`, `go mod tidy`, then the full
  `GOWORK=off go build ./... && go vet ./... && go test ./...` (proves
  `go.mod` alone is sufficient).
- Step 0: cold-cache build over non-interactive ssh
  (`GOWORK=off GOMODCACHE=$(mktemp -d) go build ./cmd/pdx`), then
  `pnpm run electron:build`, then a dev-update rebuild through the running
  (Finder-launched) daemon.
- Spec §6 steps 1–7 on mlab with the real daemon (`[nex] enabled = true`,
  `max_profile = "handoff"`), command + `pdx.log` / `pdx nex events`
  excerpt each, pasted into the PR.

- [ ] go.mod pinned to the N1 merge commit; `GOWORK=off` build/vet/test green
- [ ] step 0 (cold cache, electron:build, dev-update rebuild)
- [ ] step 1 host/quota + start log
- [ ] step 2 delegate + watch to result
- [ ] step 3a graceful restart → idle → send continues
- [ ] step 3b restart during a long tool call → path logged → idle
- [ ] step 3c `kill -9` → reconcile `process_killed=true` → send continues
- [ ] step 4 two parallel workers from this Claude Code session; interrupt one
- [ ] step 5 handoff round trip, same cwd, old process confirmed gone
- [ ] step 6 a26 via App local daemon update: steps 1–2 there
- [ ] step 7 preflight + `Last-Event-ID` resume with curl
- [ ] commit `build: pin nexen to N1 merge <sha>`

---

## Task order

T1 → T2 → T3a → T3b → T4 → T5 → T6 → T7a → T7b → T7c → T9 → T10 → T11 → T12
(serial; see Global Constraints for the separate-worktree option).

## Codex plan review disposition (`task-mu1mwztp-a9d9q2`)

| # | Level | Finding | Applied |
|---|---|---|---|
| 1 | Blocker | `--get-urlmatch` cannot verify the insteadOf rule | T1: `--get-all 'url.<ssh>.insteadOf'` grep for the https prefix; script + two-path test under `GOENV`/`GIT_CONFIG_GLOBAL` |
| 2 | Major | `sigCh` type mismatch; unconditional `<-done` hangs when `Serve` fails first | T3b: `serveAndWait` owns Serve + trigger (signal or Serve return, once); Serve-fails-first test |
| 3 | Major | I7 does not prove production waits nor same deadline | T3b: blocking-`CloseModules` wait proof; same-ctx assertion; SSE test waits for flush before signal |
| 4 | Major | `Validate` does not check roots; I12 cases missing; fake Assemble can't prove causes | T7a uses negative duration; T7b runs the real `Assemble` for six failure causes |
| 5 | Major | PATH applied before `~` expansion | T7b: `Expanded` first; I9 tested through `Init` with `HOME` faked |
| 6 | Major | `*json.RawMessage` lets `{"nex":null}` through; Clone test partial | T2: non-pointer `RawMessage`, null case; three-slice alias test |
| 7 | Major | I11 missed `message_accepted` | T7c: control lease + `send`, both operation events asserted |
| 8 | Major | Parallel TDD in one worktree conflicts with whole-tree verification; T10 "red first" impossible | Serial order; T11 relabelled characterization; engine tests moved into T7c |
| 9 | Minor | `System` interface vs field; `Now` unused; no `NewWithDeps` convention | T7b: private `engine` struct + private function fields; `Now` removed |
| 10 | Minor | pairing status vague; preflight not tested on the nex path | T11: 503 pinned; `OPTIONS /api/nex/v1/events` row |
| 11 | Minor | I13 weak; CLI precedence/passthrough/probe-count untested | T7a: zero-timeout input ⇒ defaults on returned Options; T10: four precedence cases, passthrough, probe count |
| 12 | Minor | `go mod tidy` before any import drops the require; `go.work` masks go.mod | T1: no tidy, `go mod download`; T7a tidies; T12 `GOWORK=off` full verification |
