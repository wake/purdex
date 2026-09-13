# Spec — Local daemon install & update from the Purdex app

**Status:** v4 — after codex round 3 (`task-mu00ztq3-alws01`: 0 Blocker, 5 Important; dispositions in §8). Approved for plan: rounds converged to precision items only.
**Follows:** `docs/superpowers/specs/2026-04-18-statusline-and-daemon-rebuild-design.md` §7–§13 (daemon dev rebuild, local only). This spec fills the "cross-machine daemon" gap that design explicitly left open.

## 0. Summary

Today a daemon can only update itself by rebuilding from a git checkout on
the same machine (`POST /api/dev/daemon/rebuild`). A second machine (the new
Air, arm64) will run the Purdex app and needs its own daemon, but will not
carry a checkout of this repo or a Go toolchain.

The fix: the **app installs and updates the daemon on the machine it runs
on**. The app already talks to the Mini daemon to update itself; it now also
asks that daemon for a `pdx` binary built for the local OS/arch, writes it to
disk, generates a config on first install, and (re)starts it. The Settings →
Development page grows a third block, *Local daemon*, under the existing
*Daemon* (rebuild) block.

Two shippable phases:

| Phase | Component | Deliverable |
|---|---|---|
| **A** | Go daemon | build-info package, `pdx version --json`, version in `/api/health`, `GET /api/dev/daemon/download`, dev mode on by default |
| **B** | Electron + SPA | `electron/local-daemon.ts` (status / install / ensureRunning), IPC + preload, *Local daemon* UI block, auto-add host, dev mode on by default |

Phase B depends only on Phase A's HTTP surface, not on any shared code.

## 1. Decisions (settled with the user, do not reopen)

| # | Decision | Why |
|---|---|---|
| D1 | The **app** installs the daemon on its own machine (Electron main does the file/process work). Not daemon-pull, not daemon-push. | The app already holds every host's token; no new daemon↔daemon credential. The user operates from the app. |
| D2 | Binary source is the same host the app updates itself from (`hostOrder[0]`, i.e. the Mini daemon with the repo). | Matches the existing app-update convention; one source of truth. |
| D3 | Cross-compile per request (`goos`/`goarch` query params). Only arm64 gets real-machine verification this round; amd64 is covered by unit tests only. | The 2019 Air is x86_64 but will not run a daemon for now. Cross-compilation costs two env vars, so it stays in. |
| D4 | New daemon binds the machine's **Tailscale IP** (`100.64.0.0/10` interface) when one exists, else `127.0.0.1`. | Same as the Mini. Other tailnet devices can reach the new daemon. |
| D5 | App start: if a managed daemon is installed but not running, **start it automatically**. Never auto-*update*. | The new machine has no booter/launchd; the app is the only launcher. |
| D6 | **Dev mode is on by default** in both the app and the daemon: `PDX_DEV_MODE` disables only when set to `"0"`. Config `dev.update` stays as the daemon's first gate. | Single user; launching with an env var every time is friction with no upside. |
| D7 | Install path `~/.config/pdx/bin/pdx`; config at `~/.config/pdx/config.toml` (the daemon's existing default `DataDir`). | Everything pdx-related in one place; `pdx` remains usable from a shell. |
| D8 | A daemon that is running but not at the managed path (e.g. the Mini's `repo/bin/pdx`) is **externally managed**: the UI shows it and offers no Install/Update. | Prevents the app on the Mini from clobbering the repo daemon. |
| D9 | The generated config sets `dev.update = false`. | No repo on the target machine; the app owns updates. `PDX_DEV_MODE` still enables dev-mode logging via D6. |

## 2. Phase A — Go daemon

### 2.1 `internal/buildinfo`

New leaf package (no imports beyond stdlib) so both `core` (health) and
`dev` (rebuild/download) can read the baked-in identity without a cycle.

```go
package buildinfo

// Set at link time via -ldflags "-X github.com/wake/purdex/internal/buildinfo.Hash=…".
var (
    Hash    = "unknown" // short git hash
    Version = "unknown" // contents of VERSION
)
```

- `Makefile` `LDFLAGS` injects both (`HASH := $(shell git log -1 --format=%h)`,
  `VERSION := $(shell cat VERSION)`).
- `dev.BakedInHash` is **removed**; `daemon.go` and its tests read
  `buildinfo.Hash`. The rebuild handler's ldflags string is updated to the
  new symbol path and also injects `Version`.

### 2.2 `pdx version [--json]`

New subcommand in `cmd/pdx/main.go`.

- Plain: `pdx 1.0.0-alpha.334 (d5147bc) darwin/arm64`
- `--json`: `{"version":"1.0.0-alpha.334","hash":"d5147bc","goos":"darwin","goarch":"arm64"}`

Exit 0 always. Used by Electron to read the *on-disk* binary's identity
(works when the daemon is not running).

### 2.3 `/api/health` carries identity

`core.HandleHealth` adds `"version"` and `"hash"` from `buildinfo`. This is
the *running* daemon's identity — after an update it differs from the
on-disk binary until restart. `/api/health` is unauthenticated today and
stays so; a version string is not a secret in a single-user tailnet.

`core.Version` (`internal/core/info_handler.go:18`, still `"dev"`, feeds
`/api/info.purdex_version`) is **removed**; `/api/info` reads
`buildinfo.Version` so one daemon never reports two versions.

### 2.4 Dev-mode gate: `internal/devmode`

```go
package devmode
// Enabled reports whether dev features are on. Default on; PDX_DEV_MODE=0 turns them off.
func Enabled() bool { return os.Getenv("PDX_DEV_MODE") != "0" }
```

Replaces the two `os.Getenv("PDX_DEV_MODE") == "1"` reads
(`internal/module/dev/module.go`, `internal/module/agent/probe_orchestrator.go`).
Log line in `module.go` updated to say `PDX_DEV_MODE=0` disables. The
`config.Dev.Update` gate in `cmd/pdx/main.go` is unchanged.

### 2.4b The pid file is never unlinked; `serve` exits when the lock is held

`releasePidLock` (`cmd/pdx/daemon.go:166`) unlocks and then unlinks the pid
file, and `runStop`'s SIGKILL branch unlinks it too (`:317`). Any ordering of
unlink and unlock leaves a window in which a starting `serve` can open the
old inode, flock it after the unlink, and then hold a lock on a file nobody
can see — `pdx status`/`stop` report "not running" for a live daemon.

Fix: **the pid file is permanent.** `releasePidLock` only `LOCK_UN`s and
closes; the SIGKILL branch does not remove the file. `isDaemonRunning`
already decides by *lock state*, not existence (`:181–191`), so an unlocked
leftover file reads as stopped — no behaviour change for `start`/`stop`/
`status`. Additionally `serve` **exits** (`log.Fatalf`) when
`acquirePidLock` fails instead of logging and continuing
(`cmd/pdx/main.go:116`): two daemons sharing one `data_dir` would also share
the SQLite files, so continuing was never safe. Test: open → release → flock
interleaving keeps the second locker visible to `isDaemonRunning`.

### 2.5 `GET /api/dev/daemon/download?goos=<os>&goarch=<arch>`

Registered next to `/api/dev/daemon/check` in the dev module (so it inherits
the `dev.update` + devmode gates and bearer auth).

**Request**

- `goos` ∈ `{darwin, linux}`, `goarch` ∈ `{arm64, amd64}`; anything else →
  `400 {"error":"unsupported target"}`. Both required (no defaulting to the
  host — the caller knows what it needs).

**Behaviour**

1. Acquire the **shared** `daemonRebuildMu` (`TryLock`; busy →
   `409 {"error":"build in progress"}`). It is held for the whole request,
   cache hit included, so a `/rebuild` cannot exec the server while a
   download is being served, and two downloads never race in `bin/dist/`.
2. Capture identity **once**: `hash := git -C repoRoot log -1 --format=%h`
   (3 s timeout; empty on failure → `500 {"error":"git hash unavailable"}`),
   `version := VERSION` file (trimmed; `"unknown"` if missing). The same
   `hash` is the cache key, the ldflags value and the response header — the
   handler never re-reads HEAD.
3. Artifact path `<repoRoot>/bin/dist/pdx-<goos>-<goarch>-<hash>`. Exists →
   cache hit. Else run
   `go build -ldflags "<buildinfo.Hash=hash> <buildinfo.Version=version>" -o <artifact>.tmp ./cmd/pdx`
   with `GOOS`/`GOARCH`/`CGO_ENABLED=0` appended to the inherited env,
   5-minute timeout tied to `m.stopCtx`, then rename `.tmp` → artifact.
   Build failure → `500 {"error":"build failed","detail":"<last 4 KB of output>"}`
   and the `.tmp` is removed. A dirty checkout is not detected — same as
   `/rebuild` today; the hash names the commit, not the tree.
4. Prune every `pdx-<goos>-<goarch>-*` in `bin/dist/` except the artifact
   just chosen and any `*.tmp`, so the directory holds one artifact per
   target. (Unlinking a file another request has open is safe on macOS —
   the fd stays valid — and the mutex means there is no such request anyway.)
5. Serve with `http.ServeContent` (so `Content-Length` is exact) and headers
   `Content-Type: application/octet-stream`, `X-Pdx-Hash`, `X-Pdx-Version`,
   `X-Pdx-Sha256` (hex of the artifact, computed per request — ~20 MB, a few
   ms), `Content-Disposition: attachment; filename="pdx"`.
6. **Deadline.** The whole request — build plus transfer — has a 6-minute
   budget: the build context (step 3) and a
   `http.NewResponseController(w).SetWriteDeadline(start + 6 min)` on the
   response, since the server has no global `WriteTimeout`
   (`cmd/pdx/main.go:210`). Client disconnect cancels the build. The mutex
   is released when the handler returns, whether by completion, error or
   deadline, so a slow reader cannot hold it indefinitely. A 409 means
   "busy" (build *or* transfer); the caller retries later, it is not queued.

**Why a plain GET and not SSE.** The app-update `/download` is also a plain
GET; cross-compiling `pdx` takes ~10–40 s on the Mini, well inside a fetch
timeout, and the caller shows a spinner. The rebuild endpoint streams because
it ends by exec-ing the server; this one does not.

**Why `CGO_ENABLED=0`.** `modernc.org/sqlite` is pure Go; disabling cgo makes
the cross-build independent of the host's C toolchain and SDK.

**Refactor.** The `go build` invocation and pipe/scanner plumbing inline in
`handleDaemonRebuild` move into

```go
type buildTarget struct{ GOOS, GOARCH string } // zero value = host
func (m *DevModule) buildBinary(ctx context.Context, t buildTarget, hash, version, out string, sink func(line string)) error
```

in `internal/module/dev/build.go`. It does **not** look up git; callers pass
the identity they captured. `handleDaemonRebuild` keeps its existing
best-effort hash lookup (empty hash on git failure still builds — the
existing test at `daemon_test.go:40` has no git repo) and passes its SSE
writer as sink; `handleDaemonDownload` requires a hash (step 2) and passes a
4 KB ring buffer. `/rebuild` behaviour is unchanged; its tests pass with no
edits beyond the `buildinfo` rename.

### 2.6 Tests (Phase A)

- `buildinfo`: none needed (two vars).
- `pdx version`: golden output for plain and `--json` with vars set via
  test-local assignment.
- health: response contains `version`/`hash`.
- devmode: table — unset → true, `"1"` → true, `"0"` → false, `"false"` → true.
- download handler (`httptest`; the module gets a `gitHashFn func() string`
  field defaulting to the real `git log` so tests can pin the hash, and
  builds run the real `go build` on a throwaway module in `t.TempDir()`
  exactly like `TestHandleDaemonRebuild_BuildsInTempRepo` — a cross-compiled
  empty `main` takes well under a second):
  - 400 on bad/missing `goos`/`goarch`
  - 500 when `gitHashFn` returns `""`
  - cache hit: pre-create artifact → served byte-for-byte, no build ran
    (mtime unchanged), headers `X-Pdx-Hash`/`X-Pdx-Version`/`X-Pdx-Sha256`
    correct, `Content-Length` equals the file size
  - cache miss: artifact appears at the hashed path, `file`-style magic
    matches the requested arch (Mach-O `0xCFFAEDFE`, CPU type field
    `amd64`/`arm64`), no `.tmp` left behind
  - build failure (module with a compile error) → 500 with detail, no
    artifact, no `.tmp`
  - 409 while `daemonRebuildMu` is held
  - stale artifacts for the same target are pruned; other targets and
    `*.tmp` untouched
  - `PDX_DEV_MODE=0` → `RegisterRoutes` registers nothing (404)
- rebuild handler: existing tests green after the `buildBinary` extraction.
- Auth/CORS wiring for `/api/dev/*` and `/api/health` is the existing outer
  mux in `cmd/pdx/main.go:195` and is not re-tested here.

## 3. Phase B — Electron + SPA

### 3.1 `electron/local-daemon.ts`

Pure Node module, no Electron imports. Its side effects (`fs`, `spawn`,
`fetch`, `os.networkInterfaces`, `os.hostname`, clock) are injected through
a `deps` object so tests need no module mocking. TOML is read with
`smol-toml` (new devDependency, ~10 KB, pure TS); the config is written by
us once and rewritten by the daemon (`EnsureHostID`, `config/hostid.go:35`)
so it must be parsed, not line-scanned.

**Paths**

```
dataDir  = ~/.config/pdx            (the daemon's default; a config with a custom
binDir   = dataDir/bin               data_dir is treated as external — see status)
binPath  = binDir/pdx
newPath  = binDir/pdx.new
cfgPath  = dataDir/config.toml
pidPath  = <effective data_dir>/pdx.pid
```

**Serialisation.** Every public operation (`status`, `install`, `start`,
`restart`, `ensureRunning`) runs through one in-process promise queue
(`withLock`). Concurrent IPC calls (two windows, or `ensureRunning` racing
a click) wait their turn; the SPA's disabled button is a convenience, not
the guard. Public methods enqueue exactly once and call private,
non-enqueuing helpers (`statusUnlocked`, `startDaemon`, …) — no
self-deadlock. `main.ts` runs the **entire** `applyUpdate` (download →
extract → `app.exit(0)`) inside the same queue via an exported
`localDaemon.withLock(fn)`, so an app update and a daemon operation are
mutually exclusive for their whole duration, not just at their start.

**`status(): Promise<LocalDaemonStatus>`**

```ts
interface LocalDaemonStatus {
  managed: 'none' | 'managed' | 'external'
  reason?: string                     // why 'external' (human-readable)
  binPath: string
  installed: { version: string; hash: string; goos: string; goarch: string } | null
  alive: { pid: number } | null       // a process that IS our managed binary (see Ownership)
  running: { version: string; hash: string; url: string } | null
  config: { bind: string; port: number; hasToken: boolean } | null
  target: { goos: 'darwin' | 'linux'; goarch: 'arm64' | 'amd64' }
  tools: { tmux: string | null }      // resolved tmux path on the launch PATH, see start
}
```

- `config`: parse `cfgPath` with `smol-toml`; apply the Go defaults
  (`bind 127.0.0.1`, `port 7860`, `data_dir ~/.config/pdx`). Missing file →
  `null`. A `data_dir` other than the default → `managed: 'external'`,
  `reason: 'custom data_dir'` (we do not manage relocated installs).
- `installed`: `binPath` exists → `binPath version --json` (2 s timeout);
  parse failure → all fields `'unknown'`.
- `running`: `GET http://<bind>:<port>/api/health` (1.5 s) → `ok` →
  `{version, hash, url}`. Health without `hash` (pre-Phase-A daemon) →
  `'unknown'`, which does **not** block Update.
- **Candidate pid**: the integer in `pidPath`, or `null`. The pid file is
  permanent (§2.4b) and Node cannot read flock state, so the number is only
  a *candidate* — a rebooted machine can hand it to an unrelated process.
- **Ownership / liveness** — one `lsof`-based procedure, `resolveOwner()`:
  1. **Executable of the candidate**: if a candidate pid exists and
     `process.kill(pid, 0)` succeeds, run
     `/usr/sbin/lsof -nP -a -p <pid> -d txt -F0pfn`. Parse strictly: the
     output is *sets* separated by `\n`, each set a sequence of
     NUL-terminated `<letter><value>` fields; group by `p`, then by `f`.
     The pid is **ours** iff some `f` set with value `txt` has an `n` whose
     `realpath` equals `realpath(binPath)`. Never fall back to string
     comparison of unresolved paths.
     - ours → `alive = { pid }`.
     - not ours (or `kill` failed) → the pid is stale; `alive = null`.
  2. **Owner of the endpoint**: run
     `/usr/sbin/lsof -nP -a -iTCP:<port> -sTCP:LISTEN -F0pfn` (all
     processes). Parse the same way; a listener **matches our endpoint**
     when its `n` is `<bind>:<port>` or `*:<port>`. Collect the `p` of every
     matching listener.
  3. **Decision**:
     - a matching listener whose pid ≠ `alive.pid` (including when
       `alive === null`) → `managed: 'external'`,
       `reason: 'port <port> is served by pid <p>'` (or `'running daemon is
       <path>'` when its `txt` resolves to a different binary).
     - `alive` and (no matching listener, or the listener is `alive.pid`) →
       `'managed'`. A managed process that holds the pid file but has not
       started listening yet (migration in progress) is therefore
       **managed-unhealthy**, and **Restart** is offered rather than a
       refusal.
     - `alive === null`, no matching listener: `binPath` exists → `'managed'`
       (stopped; a stale pid is ignored), else `'none'`.
  4. **Timeouts**: each `lsof` gets 5 s (10 s total); on timeout the child
     is killed, partial output discarded, and the result is
     `managed: 'external'`, `reason: 'ownership check timed out'`. `-d txt`
     narrows the output, not the scan cost, so the bound is mandatory.
  Ownership is re-evaluated **immediately before `stop`** inside
  `install`/`restart`; a status from minutes earlier is never trusted for a
  destructive step. `running !== null` with `alive === null` and no
  matching listener cannot happen (something answered health on the port);
  treat it as `'external'`, `reason: 'health answered but no listener found'`.
- `target`: `process.platform` → `darwin`/`linux` (else throw);
  `process.arch` → `arm64` / `x64 → amd64`.
- `tools.tmux`: `which tmux` under the **launch PATH** (below); `null` when
  absent.

**Launch PATH.** An app opened from Finder inherits
`/usr/bin:/bin:/usr/sbin:/sbin`; the daemon execs `tmux` from PATH
(`internal/tmux/executor.go:114`) and `pdx start` inherits the app's env
(`daemon.go:246`), so a Finder-launched daemon would pass health and then
fail every tmux call. `launchEnv()` builds the env once per process:
`process.env` with `PATH` replaced by the user's shell PATH, plus
`PDX_DEV_MODE=1`. Resolution order, each with stdin closed and a 5 s
timeout, taking the first that yields a non-empty PATH:

1. `execFile($SHELL || '/bin/zsh', ['-ilc', script])` where the shell
   script is `printf '\n%s%s%s\n' '<S>' "$PATH" '<S>'` and `<S>` is a
   per-call sentinel `PDX_PATH_<32 hex from crypto.randomBytes(16)>`
   interpolated into the script text. No NUL bytes anywhere in argv (Node
   rejects them); only the two sentinel occurrences delimit the value, so
   banners, prompts or plugin output on either side cannot leak into PATH,
   and a sentinel collision is impossible in practice. Interactive **is**
   required: the measured `-lc` PATH on the Mini lacks `~/.local/bin`
   (where `claude` lives) and nvm, which live in `.zshrc`; `-ilc` took
   0.44 s. A result is valid only if both sentinels are found and the text
   between them is non-empty.
2. The same with `-lc` (login files only).
3. `process.env.PATH` prefixed with
   `/opt/homebrew/bin:/usr/local/bin:` + `join(os.homedir(), '.local/bin')`
   (never a literal `~`).

Used for `pdx start`, `pdx stop`, `pdx version` and the `which tmux` probe.
`status().tools.tmux === null` is shown as a warning in the UI and blocks
nothing (the user may install tmux afterwards).

**`install(daemonUrl, token, onProgress): Promise<LocalDaemonResult>`**

```ts
interface LocalDaemonResult {
  url: string; token: string; hash: string; version: string; hostname: string
  bindNote?: string
}
```

Refuses (throws) when `status().managed === 'external'`. Steps, each
reported through `onProgress(step)`:

0. `prepare` — `mkdir -p binDir` (a fresh machine has no `~/.config/pdx`;
   the daemon only creates it on its own first start, `daemon.go:220`).
1. `download` — `GET <daemonUrl>/api/dev/daemon/download?goos=&goarch=` with
   bearer, 6-minute overall timeout (the source may need to cross-compile).
   Non-200 → throw with the body's `error`/`detail`. Stream to `newPath`
   (any stale `pdx.new` is overwritten), **await the write stream's
   `finish`/close**, then compare: bytes written === `Content-Length`, and
   SHA-256 of the file === `X-Pdx-Sha256`. Mismatch → delete `newPath`,
   throw. `chmod 0755`.
2. `verify` — `newPath version --json` (2 s). Must parse, and its
   `goos`/`goarch` must equal `target` and its `hash` must equal
   `X-Pdx-Hash`. Else delete `newPath` and throw (`"downloaded binary does
   not run: <stderr>"` / `"identity mismatch"`). Nothing has been stopped yet.
3. `configure` — only when `cfgPath` does not exist: write to
   `cfgPath + '.tmp'` with mode `0600` and rename into place:

   ```toml
   bind = "<tailscale ip | 127.0.0.1>"
   port = 7860
   token = "purdex_<40 hex from crypto.randomBytes(20)>"

   [dev]
   update = false
   ```

   Tailscale IP: the IPv4 addresses of non-internal interfaces that fall in
   `100.64.0.0/10`; on macOS additionally require the interface name to
   start with `utun`. Exactly one candidate → use it. Zero or more than one
   → `127.0.0.1`, and the result carries `bindNote` explaining why (the
   user can edit the config and restart). `host_id` is left for the daemon
   to mint (`EnsureHostID`).
4. `stop` — when `alive !== null` (our binary is running, healthy or not).
   Re-run `resolveOwner()` first; not `managed` → throw. Then spawn
   `binPath stop` under `launchEnv()`, await exit with a **35 s** budget
   (`pdx stop` itself waits 30 s then SIGKILLs, `daemon.go:305`), then poll
   until `process.kill(alive.pid, 0)` fails **and** the port refuses
   connections (≤ 5 s more). Any timeout → throw before touching `binPath`.
5. `swap` — `rename(newPath, binPath)`.
6. `start` — `startDaemon()` below.
7. `register` — read `token`, `bind`, `port` back from the (possibly
   daemon-rewritten) config; return `LocalDaemonResult` with
   `hostname = os.hostname()`.

**Failure contract.** Failure before `stop` begins leaves the previous
daemon running and its binary untouched. Failure after `stop` has sent its
signal but before `swap` guarantees only that the old *binary* is
unreplaced — the old *process* may already be gone; the UI re-queries
`status()` and offers **Start** if the result is `managed` and not alive.
Failure in `start` after `swap` is reported with `pdx start`'s stderr (it
prints the last 20 log lines); the new binary is in place and the UI
offers **Start**.

**`startDaemon()`** (private) — spawn `binPath start` with `launchEnv()`,
`cwd: os.homedir()`, and await its exit (≤ 70 s). `pdx start` is a short-lived
launcher: it forks the real daemon into its own process group (`Setpgid`,
`daemon.go:250`) with stdio on the log file, waits for `/api/health` (60 s
window), then exits 0 — or exits 1 with the last 20 log lines on stderr,
which we surface verbatim. Because the daemon is not in the app's process
group it survives the app quitting; no `detached`/`unref` is needed. After
exit 0, `GET /api/health` once more and require `hash` to equal the
on-disk binary's hash (`pdx version --json`); a mismatch (another service on
the port answered 200) throws `"port <n> is served by something else"`.

**`start(): Promise<LocalDaemonResult>`** — for the UI's *Start* button and
for `ensureRunning`. Refuses when `managed !== 'managed'` or when `alive`;
runs `startDaemon()` then step 7, so a recovered daemon returns the same
registration payload as a fresh install.

**`restart(): Promise<LocalDaemonResult>`** — steps 4 → 6 → 7 without a
download. For the case where the on-disk binary is already current but the
running process is older (a crashed earlier update, or a manual copy).

**`ensureRunning(): Promise<'started' | 'already-running' | 'not-installed' | 'external' | 'failed'>`**

`status()` → `managed === 'managed' && alive === null` → `start()`,
retried up to 3× with 5 s spacing (a saved Tailscale bind can be a few seconds late after
a reboot; `pdx start` fails fast on `EADDRNOTAVAIL`). Called once from
`app.whenReady()` after IPC registration; result logged, never thrown. Not
called when `PDX_DEV_MODE === '0'`. It never touches `pdx.new`; a download
interrupted by a force-quit is simply overwritten by the next install.

### 3.2 IPC and preload

In `electron/main.ts`, inside the existing dev-gated block:

```
dev:local-daemon-status   → status()
dev:local-daemon-install  (daemonUrl, token?) → install(...), progress on 'dev:local-daemon-progress'
dev:local-daemon-start    → start()    (returns LocalDaemonResult)
dev:local-daemon-restart  → restart()  (returns LocalDaemonResult)
```

`dev:apply-update` wraps its whole body in `localDaemon.withLock(...)`.

`preload.ts` adds, inside the same conditional spread:

```ts
localDaemonStatus: () => ipcRenderer.invoke('dev:local-daemon-status'),
localDaemonInstall: (daemonUrl, token) => ipcRenderer.invoke('dev:local-daemon-install', daemonUrl, token),
localDaemonStart: () => ipcRenderer.invoke('dev:local-daemon-start'),
localDaemonRestart: () => ipcRenderer.invoke('dev:local-daemon-restart'),
onLocalDaemonProgress: (cb) => { …same shape as onUpdateProgress… },
```

`spa/src/types/electron.d.ts` gains `ElectronLocalDaemonStatus` /
`ElectronLocalDaemonResult` and the five methods (all optional, like
the other dev methods).

### 3.3 Dev mode default (Electron)

`main.ts` (top, before anything reads it):

```ts
// Dev features are on unless explicitly disabled. See spec D6.
if (process.env.PDX_DEV_MODE === undefined) process.env.PDX_DEV_MODE = '1'
```

The three existing `=== '1'` gates (`main.ts`, `preload.ts`) and
`updater.ts`'s `devUpdateEnabled: !!process.env.PDX_DEV_MODE` become
`!== '0'`. Preload runs in its own context but shares the process env, so
the default set in main is visible there; the `!== '0'` form also makes
each site correct on its own.

### 3.4 SPA — *Local daemon* block

`DevEnvironmentSection.tsx` is already 426 lines; the new block is its own
component `LocalDaemonSection.tsx` in the same folder, rendered by
`DevEnvironmentSection` after the *Daemon* block, and only when
`window.electronAPI?.localDaemonStatus` exists (Electron only; the web build
never sees it).

Props: `daemonBase`, `token` (the same source-host values the parent already
derives), `latestHash` (the parent's `daemonCheck.latest_hash`, so the
block can say "update available" without a second `/check`).

States and controls:

| `managed` | Shows | Button |
|---|---|---|
| `none` | "No daemon installed on this machine" + target arch | **Install** |
| `managed`, `alive === null` | installed version/hash | **Start** (`start()`) · **Update** when `installed.hash !== latestHash` |
| `managed`, running | running version/hash, URL | **Update** when `installed.hash !== latestHash`; else **Restart** when `running.hash !== installed.hash` ("on-disk <hash> not yet running"); else "Up to date" |
| `managed`, `alive` but `running === null` | "daemon process <pid> is alive but not answering" | **Restart** |
| `external` | "A daemon is running at <url> but is not managed by this app" | none |

During install the block shows the progress step and disables buttons.
Both `install` and `start` resolve to a `LocalDaemonResult`; on either
success the block runs one idempotent `registerLocalHost(result)`:

- a host with the same `ip:port` exists → `updateHost(id, { token })` only
  if that host's token is empty/`null` (never overwrite a live token);
- otherwise `addHost({ name: result.hostname, ip, port, token })`.

So a first install whose `start` failed still registers the host when the
user presses *Start*. Errors render inline; the *Start* button is offered
after a post-swap start failure. `tools.tmux === null` renders a warning
line ("tmux not found on the daemon's PATH — install it with Homebrew");
`bindNote` from a fresh install renders as an info line.

`status()` is refreshed on mount, after every install/start, and when the
parent's `daemonCheck` changes.

### 3.5 Tests (Phase B)

- `local-daemon.test.ts` (vitest; `createLocalDaemon(deps)` with an
  in-memory fs, scripted `spawn`, scripted `fetch`, fixed interfaces/hostname):
  - `status`: none / managed-stopped / managed-running / external (repo
    daemon at another path, pid unknown, custom `data_dir`) matrix; version
    parse failure → `'unknown'`; arch mapping `x64 → amd64`; `tools.tmux`
    null vs path.
  - `launchEnv`: uses the login-shell PATH; falls back to the prefixed PATH
    on failure; always sets `PDX_DEV_MODE=1`.
  - `install`: refuses on external; short body vs `Content-Length` → throw,
    `pdx.new` removed, no stop; SHA-256 mismatch → same; `verify` arch or
    hash mismatch → same; writes config only when absent, mode 0600, via
    tmp+rename; bind selection (one `utun` in 100.64/10 → it; none → loopback;
    two → loopback + `bindNote`); stop → swap → start order; `pdx stop`
    timeout → throw with old binary intact; start env carries the launch
    PATH and `PDX_DEV_MODE=1`; post-start health hash mismatch → throw;
    returns the token read back from the config on re-install.
  - `resolveOwner` (with canned `lsof -F0` output, including multi-set
    output with `\n` between sets and a `txt` entry that is a dylib before
    the executable): ours+listener-ours → managed/alive; ours+no-listener →
    managed/alive (unhealthy → Restart); stale pid (txt ≠ binPath, no
    listener) → managed/stopped; listener owned by another pid → external
    with the pid in the reason; `lsof` timeout → external
    `'ownership check timed out'` and the child killed; realpath is used
    (symlinked `binPath`).
  - ownership re-checked immediately before `stop` (a status that changed
    in between aborts the install).
  - liveness vs health: alive + health timeout → `stop` still runs before
    `swap`; `alive === null` + `binPath` present → `start` allowed.
  - `launchEnv`: argv contains no NUL; the PATH is extracted between the
    sentinels even with banner text before and after.
  - `install` on a filesystem with no `~/.config/pdx` at all succeeds.
  - `start`: refuses unless managed and not alive; returns a `LocalDaemonResult`.
  - `restart`: stop → start → register, no download.
  - `ensureRunning`: five outcomes; retries 3× on failure; never touches
    `pdx.new`.
  - `withLock`: two concurrent `install` calls run sequentially; an
    `install` arriving during a `withLock`-wrapped app update waits for it;
    public methods do not deadlock when they call each other's helpers.
- Dev-mode gate: `PDX_DEV_MODE` unset → IPC registered; `'0'` → not.
- `LocalDaemonSection.test.tsx`: the `managed` rows render the right
  copy/buttons (including `reason` and the tmux warning); Update button
  visibility vs `latestHash`; `registerLocalHost` adds a host once, updates
  only an empty token, never overwrites a live one; the same registration
  runs after *Start*; install error renders and re-enables.

## 4. Operational notes

- **Gatekeeper.** A file written by `fs` from a fetch body carries no
  `com.apple.quarantine` xattr, so the unsigned Go binary executes — the
  same situation as the Mini's `make build` output. If Apple changes this,
  the `verify` step fails loudly with the OS error and the fix is a single
  `xattr -d` in step 1.
- **Prerequisites on the target machine.** `tmux` (Homebrew) and whichever
  agent CLIs the user wants (`claude`, `codex`, …) must be installed and on
  the login-shell PATH; the app does not install them. The *Local daemon*
  block warns when `tmux` is missing.
- **Fresh machine bootstrap.** Install `Purdex.app` (built on the Mini) →
  open → add the Mini as a host (existing pairing flow) → Settings →
  Development → *Local daemon* → **Install**. The new daemon appears in the
  host list automatically.
- **Manual acceptance on the arm64 Air** (listed in the PR-B description):
  Install from `none`; the new host appears and can create a tmux session
  and attach; quit the app → daemon still answers `/api/health`; relaunch →
  `ensureRunning` reports `already-running`; `pdx stop` from a shell →
  relaunch app → daemon started; push a commit on the Mini → *Update
  available* → Update → running hash changes, tmux sessions survive.
- **Mini after this ships.** Its own app shows the repo daemon as
  `external` (D8). Mini deploy is unchanged: `make build` → `pdx stop` →
  `pdx start` (the env var is no longer needed, D6).
- **Reboot on the new machine.** Open the app; D5 starts the daemon. tmux
  sessions are gone after a reboot regardless — that is what Workspace
  Snapshot / Tab Rebuild are for.

## 5. Out of scope

- Background auto-update / polling on the target machine.
- launchd / login-item registration for the daemon (the app is the launcher).
- Intel real-machine verification (D3).
- Updating `hostOrder[0]` selection logic in `DevEnvironmentSection` (a
  source-host picker) — the source is the Mini by construction.
- Code-signing the downloaded binary.
- Removing the `PDX_DEV_MODE` gate entirely.

## 6. Review dispositions — codex round 1 (`task-mu00ipjj-5tblb1`)

| # | Sev | Disposition | Where |
|---|---|---|---|
| 1 | Blocker | **Accepted.** Ownership = running pid's executable equals `binPath`; anything else is `external` and refused. | §3.1 status |
| 2 | Important | **Accepted.** `stop` budget 35 s + health-refused poll; timeout aborts before swap. `releasePidLock` order fixed in Phase A. | §3.1 stop, §2.4b |
| 3 | Important | **Accepted.** Await the `pdx start` launcher (no detach), surface stderr, post-start health hash must equal on-disk hash; app-quit survival in manual acceptance. | §3.1 startDaemon, §4 |
| 4 | Important | **Accepted.** One promise queue in `local-daemon.ts`; `dev:apply-update` awaits `idle()`; `pdx.new` is never started, only overwritten. | §3.1 Serialisation, §3.2 |
| 5 | Important | **Accepted.** `smol-toml` parser + Go defaults; custom `data_dir` → external; config written 0600 via tmp+rename, never overwritten. | §3.1 config/configure |
| 6 | Important | **Accepted.** `launchEnv()` from the login shell's PATH; `tools.tmux` probe + UI warning; prerequisites and tmux-session check in §4. | §3.1 Launch PATH, §3.4, §4 |
| 7 | Important | **Accepted in part.** Mutex now covers cache hits and prune, so rebuild cannot exec mid-download and prune cannot race. Not accepted: "rebuild must wait for downloads" beyond that — the mutex already provides it. | §2.5 steps 1, 4 |
| 8 | Important | **Accepted.** Identity captured once per request; `buildBinary` takes hash/version as inputs. Dirty-checkout detection explicitly out of scope, matching `/rebuild`. | §2.5 steps 2–3 |
| 9 | Important | **Accepted.** `Content-Length` and `X-Pdx-Sha256` verified after stream close; `verify` checks hash against `X-Pdx-Hash`; 6-minute download budget. | §2.5 step 5, §3.1 download/verify |
| 10 | Minor | **Accepted.** `core.Version` removed; `/api/info` reads `buildinfo.Version`. | §2.3 |
| 11 | Important | **Accepted.** Test section rewritten around a real temp-module build + `gitHashFn` pin; rebuild keeps best-effort hash. Middleware wiring is existing behaviour and stays untested here (stated). | §2.5 refactor, §2.6 |
| 12 | Important | **Accepted.** `start()` returns `LocalDaemonResult`; `registerLocalHost` is idempotent and fills an empty token. | §3.1 start, §3.4 |
| 13 | Minor | **Accepted.** `utun` + 100.64/10, exactly-one rule with `bindNote` fallback; `ensureRunning` retries 3× for late interfaces. | §3.1 configure, ensureRunning |

## 7. Review dispositions — codex round 2 (`task-mu00stga-h47932`)

| # | Sev | Disposition | Where |
|---|---|---|---|
| 1 | Blocker | **Accepted.** `lsof -d txt` path match + `lsof -iTCP:<port> -sTCP:LISTEN` listener match; re-checked right before `stop`. (`ps -o comm=` matched on the Mini only because `pdx start` execs with an absolute `argv[0]`.) | §3.1 Ownership, stop |
| 2 | Important | **Accepted.** Liveness (`kill -0` on the pid-file pid) is separate from health; alive-but-unhealthy is stopped before swap; new `restart()` + **Restart** button for `running.hash ≠ installed.hash` and for alive-unhealthy; unknown hash never blocks Update. | §3.1, §3.2, §3.4 |
| 3 | Important | **Accepted.** Pid file is permanent (unlock + close only, SIGKILL branch too); `serve` exits when the lock is held. | §2.4b |
| 4 | Important | **Accepted.** `applyUpdate` runs entirely inside `localDaemon.withLock`; public methods enqueue once and call non-enqueuing helpers. | §3.1 Serialisation, §3.2 |
| 5 | Important | **Accepted with one change.** NUL-sentinel parsing, stdin closed, 5 s timeout, `join(homedir, '.local/bin')`. Kept `-i` as the first attempt (measured: `-lc` on the Mini lacks `~/.local/bin` and nvm, which are set in `.zshrc`); `-lc` is the second attempt, static prefix third. | §3.1 Launch PATH |
| 6 | Important | **Accepted.** Full-request lock kept (agrees with codex); 6-minute build+transfer deadline via `SetWriteDeadline`; disconnect cancels build; 409 = busy, not queued. | §2.5 step 6 |
| 7 | Important | **Accepted.** Step 0 `mkdir -p binDir`; test with an empty home. | §3.1 install |
| 8 | Minor | **Accepted.** Failure contract split at "stop began"; UI re-queries and offers Start. | §3.1 Failure contract |

## 8. Review dispositions — codex round 3 (`task-mu00ztq3-alws01`)

| # | Sev | Disposition | Where |
|---|---|---|---|
| 1 | Important | **Accepted.** The pid-file number is a candidate only; `alive` requires the `lsof txt` match. A reused pid reads as managed-stopped, not external. | §3.1 Ownership steps 1, 3 |
| 2 | Important | **Accepted.** `alive` added to `LocalDaemonStatus`; a listener is no longer required for `managed` — only a *foreign* listener on our endpoint makes it external; `ensureRunning`/`start` key off `alive`. | §3.1 status, ensureRunning, §3.4 |
| 3 | Important | **Accepted.** Exact `-F0pfn` set/field parsing spelled out; listener `n` must be `<bind>:<port>` or `*:<port>`; realpath only. | §3.1 Ownership steps 1–2 |
| 4 | Important | **Accepted.** 5 s per `lsof`, 10 s total, kill on timeout, result external with a timeout reason. | §3.1 Ownership step 4 |
| 5 | Important | **Accepted with a simpler design.** Dropped NUL entirely: a per-call random sentinel with newline framing; argv never contains NUL. | §3.1 Launch PATH |
