# Spec — Local daemon install & update from the Purdex app

**Status:** v1 — draft for codex review
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

### 2.5 `GET /api/dev/daemon/download?goos=<os>&goarch=<arch>`

Registered next to `/api/dev/daemon/check` in the dev module (so it inherits
the `dev.update` + devmode gates and bearer auth).

**Request**

- `goos` ∈ `{darwin, linux}`, `goarch` ∈ `{arm64, amd64}`; anything else →
  `400 {"error":"unsupported target"}`. Both required (no defaulting to the
  host — the caller knows what it needs).

**Behaviour**

1. Resolve `hash := git -C repoRoot log -1 --format=%h` (3 s timeout; empty on
   failure → `500`), `version := VERSION` file (trimmed; `"unknown"` if
   missing).
2. Artifact path: `<repoRoot>/bin/dist/pdx-<goos>-<goarch>-<hash>`.
3. If the artifact exists → serve it (cache hit). Else acquire the
   **shared** `daemonRebuildMu` (`TryLock`; busy → `409 {"error":"build in progress"}`),
   run `go build -ldflags <buildinfo Hash+Version> -o <artifact>.tmp ./cmd/pdx`
   with `GOOS`/`GOARCH` appended to the inherited env and `CGO_ENABLED=0`,
   5-minute timeout tied to `m.stopCtx`, then rename `.tmp` → artifact.
   Build failure → `500 {"error":"build failed","detail":"<last 4 KB of output>"}`;
   the `.tmp` is removed.
4. Serve with `Content-Type: application/octet-stream`,
   `Content-Length`, `X-Pdx-Hash: <hash>`, `X-Pdx-Version: <version>`,
   `Content-Disposition: attachment; filename="pdx"`.
5. Before serving, prune other `pdx-<goos>-<goarch>-*` files for the same
   target so `bin/dist/` holds at most one artifact per target.

**Why a plain GET and not SSE.** The app-update `/download` is also a plain
GET; cross-compiling `pdx` takes ~10–40 s on the Mini, well inside a fetch
timeout, and the caller shows a spinner. The rebuild endpoint streams because
it ends by exec-ing the server; this one does not.

**Why `CGO_ENABLED=0`.** `modernc.org/sqlite` is pure Go; disabling cgo makes
the cross-build independent of the host's C toolchain and SDK.

**Refactor.** The `go build` invocation, pipe/scanner plumbing and hash
lookup currently inline in `handleDaemonRebuild` move into
`buildBinary(ctx, target, out string, sink func(line string)) (hash string, err error)`
in `internal/module/dev/build.go`. `handleDaemonRebuild` calls it with the
host target and its SSE writer as sink; `handleDaemonDownload` calls it with
a ring-buffer sink. Behaviour of `/rebuild` is unchanged (its tests must
pass without modification other than the `buildinfo` rename).

### 2.6 Tests (Phase A)

- `buildinfo`: none needed (two vars).
- `pdx version`: golden output for plain and `--json` with vars set via
  test-local assignment.
- health: response contains `version`/`hash`.
- devmode: table — unset → true, `"1"` → true, `"0"` → false, `"false"` → true.
- download handler (`httptest`, `buildCmd` injected exactly as the existing
  rebuild tests inject `execSelf`/`buildCmd`):
  - 400 on bad/missing `goos`/`goarch`
  - cache hit: pre-create artifact → served, build not invoked, headers set
  - cache miss: build invoked with `GOOS`/`GOARCH`/`CGO_ENABLED=0` in env,
    `-o` points at `.tmp`, then served from the renamed artifact
  - build failure → 500 with detail, no artifact left behind
  - 409 while `daemonRebuildMu` is held
  - stale artifacts for the same target are pruned
- rebuild handler: existing tests green after the `buildBinary` extraction.

## 3. Phase B — Electron + SPA

### 3.1 `electron/local-daemon.ts`

Pure Node module (no Electron imports except `app.getPath('home')` fallback
via `os.homedir()`), unit-tested with vitest like `updater.ts`.

**Paths**

```
dataDir  = ~/.config/pdx
binDir   = dataDir/bin
binPath  = binDir/pdx
newPath  = binDir/pdx.new
cfgPath  = dataDir/config.toml
pidPath  = dataDir/pdx.pid
logPath  = dataDir/logs/pdx.log
```

**`status(): Promise<LocalDaemonStatus>`**

```ts
interface LocalDaemonStatus {
  managed: 'none' | 'managed' | 'external'   // see D8
  binPath: string
  installed: { version: string; hash: string; goos: string; goarch: string } | null  // from `pdx version --json`
  running: { version: string; hash: string; url: string } | null              // from /api/health
  config: { bind: string; port: number; hasToken: boolean } | null           // parsed from config.toml
  target: { goos: 'darwin' | 'linux'; goarch: 'arm64' | 'amd64' }           // what install would request
}
```

- `installed`: `binPath` exists → run `binPath version --json` (2 s timeout);
  parse failure → `{version:'unknown', hash:'unknown', …}`.
- `config`: parse `bind`/`port`/`token` lines from `config.toml` with a
  minimal TOML-line reader (top-level `key = value` only; the file is ours).
  Missing file → `null` and the URL falls back to `127.0.0.1:7860`.
- `running`: `GET http://<bind>:<port>/api/health` (1.5 s timeout) → `ok`
  → `{version, hash, url}`; `version`/`hash` default `'unknown'` when the
  running daemon predates Phase A.
- `managed`: `binPath` exists → `'managed'`; else `running` non-null →
  `'external'`; else `'none'`.
- `target`: `process.platform` → goos (`darwin`/`linux`; anything else throws),
  `process.arch` → `arm64`/`x64 → amd64`.

**`install(daemonUrl, token, onProgress): Promise<InstallResult>`**

Refuses immediately when `status().managed === 'external'`.

1. `download` — `GET <daemonUrl>/api/dev/daemon/download?goos=&goarch=` with
   bearer; non-200 → throw with the body's `error`/`detail`. Stream to
   `newPath` (mkdir -p `binDir`), `chmod 0755`. Capture `X-Pdx-Hash`.
2. `verify` — run `newPath version --json`; must parse and report the same
   `goos`/`goarch` as `target`, else delete `newPath` and throw
   (`"downloaded binary does not run: <stderr>"`). This catches a wrong-arch
   or truncated download before anything is stopped.
3. `configure` — only when `cfgPath` does not exist: write

   ```toml
   bind = "<tailscale ip | 127.0.0.1>"
   port = 7860
   token = "purdex_<40 hex from crypto.randomBytes(20)>"

   [dev]
   update = false
   ```

   Tailscale IP = first non-internal IPv4 in `os.networkInterfaces()` inside
   `100.64.0.0/10`. `host_id` is left unset so the daemon mints its own
   (existing behaviour; see `feedback_hostid_not_local`).
4. `stop` — if `running`: spawn `binPath stop`, wait ≤ 10 s. If `binPath`
   does not exist yet but a pid file does (should not happen for a managed
   install; defensive), run `newPath stop`.
5. `swap` — `rename(newPath, binPath)`.
6. `start` — spawn `binPath start` detached, `stdio: 'ignore'`, env
   `{...process.env, PDX_DEV_MODE: '1'}`, `cwd: os.homedir()`. `pdx start`
   itself waits for `/api/health` (60 s window) and exits non-zero with the
   last 20 log lines on failure; we surface its stderr.
7. Return `{ url: 'http://<bind>:<port>', token, hash, version }` — `token`
   read back from `config.toml` (so re-installs return the existing one).

`onProgress(step)` is called with the step names above; the IPC layer
forwards them on `dev:local-daemon-progress` exactly like
`dev:update-progress`.

Failure at any step leaves the previous binary in place except after
`swap`; a failed `start` after swap is reported with the log tail and the
UI offers *Start* again (the binary is already the new one).

**`start(): Promise<void>`** — step 6 alone (for the UI's *Start* button
and for `ensureRunning`). Throws with `pdx start`'s stderr on failure.

**`ensureRunning(): Promise<'started' | 'already-running' | 'not-installed' | 'failed'>`**

`status()` → `managed === 'managed' && !running` → `start()`. Called once
from `app.whenReady()` after IPC registration; result logged, never thrown.
Not called when `PDX_DEV_MODE === '0'`.

### 3.2 IPC and preload

In `electron/main.ts`, inside the existing dev-gated block:

```
dev:local-daemon-status   → status()
dev:local-daemon-install  (daemonUrl, token?) → install(...), progress on 'dev:local-daemon-progress'
dev:local-daemon-start    → start()
```

`preload.ts` adds, inside the same conditional spread:

```ts
localDaemonStatus: () => ipcRenderer.invoke('dev:local-daemon-status'),
localDaemonInstall: (daemonUrl, token) => ipcRenderer.invoke('dev:local-daemon-install', daemonUrl, token),
localDaemonStart: () => ipcRenderer.invoke('dev:local-daemon-start'),
onLocalDaemonProgress: (cb) => { …same shape as onUpdateProgress… },
```

`spa/src/types/electron.d.ts` gains `ElectronLocalDaemonStatus` /
`ElectronLocalDaemonInstallResult` and the four methods (all optional, like
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
| `managed`, not running | installed version/hash | **Start** (`start()`) · **Update** when `installed.hash !== latestHash` |
| `managed`, running | running version/hash, URL; "on-disk <hash> — restart pending" when on-disk ≠ running | **Update** when `installed.hash !== latestHash`, else "Up to date" |
| `external` | "A daemon is running at <url> but is not managed by this app" | none |

During install the block shows the progress step and disables buttons.
On success it calls `useHostStore.addHost({ name: os hostname from result, ip, port, token })`
**only if no host already has that `ip:port`**; the result includes
`hostname` (`os.hostname()`) for the name. Errors render inline; the *Start*
button is offered after a post-swap start failure.

`status()` is refreshed on mount, after every install/start, and when the
parent's `daemonCheck` changes.

### 3.5 Tests (Phase B)

- `local-daemon.test.ts` (vitest, `fs`/`child_process`/`net` mocked as in
  `updater.test.ts`/`signing.test.ts`):
  - `status`: none / managed-stopped / managed-running / external matrix;
    version parse failure → `'unknown'`; arch mapping `x64 → amd64`.
  - `install`: refuses on external; writes config only when absent; config
    content (bind = tailscale IP when present, else loopback; token format;
    `[dev] update = false`); `verify` failure removes `pdx.new` and does not
    stop the running daemon; stop → swap → start order; start env carries
    `PDX_DEV_MODE=1`; returns existing token on re-install.
  - `ensureRunning`: four outcomes.
- Dev-mode gate: `PDX_DEV_MODE` unset → IPC registered; `'0'` → not.
- `LocalDaemonSection.test.tsx`: the four `managed` rows render the right
  copy/buttons; Update button visibility vs `latestHash`; `addHost` called
  once with the result and not when a host with the same `ip:port` exists;
  install error renders and re-enables.

## 4. Operational notes

- **Gatekeeper.** A file written by `fs` from a fetch body carries no
  `com.apple.quarantine` xattr, so the unsigned Go binary executes — the
  same situation as the Mini's `make build` output. If Apple changes this,
  the `verify` step fails loudly with the OS error and the fix is a single
  `xattr -d` in step 1.
- **Fresh machine bootstrap.** Install `Purdex.app` (built on the Mini) →
  open → add the Mini as a host (existing pairing flow) → Settings →
  Development → *Local daemon* → **Install**. The new daemon appears in the
  host list automatically.
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
