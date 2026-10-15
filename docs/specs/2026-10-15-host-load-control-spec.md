# Host load control: gate heavy commands below the agents, retire the lease (spec)

Date: 2026-10-15. Coordinator: purdex-1f. Line: A.
Status: **draft for plan** — §4 updated with the open-source tool survey (10-15).
Survey: `~/.local/share/purdex-lead/reports/research-intercept-tools.md`.
Replaces: `docs/specs/2026-10-08-host-resource-lease-spec.md`, `…-lease-v2-spec.md`, `…-lease-v2-amend-a-spec.md`
(the lease parts; host sampling, zones, alerts and the disk guard are kept and reworked here).

## 0. Why

On 2026-10-15 mlab (Mac mini M4, 4 P + 6 E cores, 16 GB) went to load 66 with ~5 GB in the compressor and
2 GB of swap while only ~2.5 cores were busy: the full-suite commands that agent sessions spawn (vitest full,
`go test ./...`, `tsc -b --force`, `pnpm check`) stacked up. One full suite alone uses every core (`go test`
runs `-p` = ncpu packages, vitest ncpu−1 workers). Measured (host_minutes, 10-13…10-15): 0 heavy jobs → load
8.2, 1 → 21.7, 2 → 23.0, 4 → 30.0. The user stopped all mlab work.

The lease (`pdx lease`, v1/v2/amend A, P2e/P2f, load cap) did not hold the host, for structural reasons:

1. **Bypass.** It is entered from the mod (Bash tool calls) and from `pdx lease run`. Commands inside scripts,
   subagents' scripts and codex sandboxes never ask (10-15: a gate script's unleased `tsc -b --force`; an inner
   step after the outer lease expired).
2. **Wrong memory signal.** The zone reads `memory_pressure`'s free %; it said 55 % free while the host was
   swapping. CPU never holds a waiter (G1 (c), `trend_cpu_dppm = 0`).
3. **Time-based holding.** `max_hold_s` releases a live 60-minute check; the job keeps running unmanaged.
4. **Orphans.** Stopping the top process left vitest workers running (ppid 1).
5. **The daemon in the loop.** Every grant needs the daemon; under swap the daemon times out
   (`origin_unknown`, `no_answer`), so the control fails exactly when it is needed.

## 1. User decisions (2026-10-15, do not reopen)

| # | Decision |
|---|---|
| D1 | Do the six short-term items: intercept heavy commands below the agents and take a host-wide slot; admit by macOS memory pressure (pressure level + pageout/swapout rate); cap each heavy command's parallelism and lower its priority; liveness instead of a fixed expiry; reap the whole process group; stop disabling Go's test cache (`-count=1`) below the release tier. |
| D2 | Retire the lease (request, queue, offer, notices, positions, `pdx lease` verbs, the mod's lease hooks). **Keep** host sampling, zones, alerts, the disk guard and the report, reworked onto the new signals. |
| D3 | The gate **does not depend on the daemon**: slots are local file locks, released by the kernel when the holder exits. The daemon only observes and reports. |
| D4 | Fix the daemon's own steady cost (≈30 % of one core with the host idle, 10-15 08:50 profile). |
| D5 | Until this is deployed, all other lines stay paused; only the members doing this work resume. |
| D6 | Session count is not the problem; the spawned full-suite commands are (`feedback-load-root-is-test-commands`). |
| D7 | **Finish each heavy command as fast as possible**: one full suite at a time, run near full width (parallelism ≈ cores − 2, bounded by free memory), lowered with `nice` rather than background QoS (`taskpolicy -b` measured 45 % slower on one package); then the next. Concurrency on a saturated CPU does not raise throughput, it slows every job and adds swapping. |
| D8 | The seven items are **independent modules**, each with its own switch and parameters, tunable per situation and **at run time** (config re-read at every gate start and every waiting poll; `auto` values computed from the live host; profiles). |
| D9 | **Packaging is never held back** (10-15): commands that build an artifact (the Purdex App build and the like, seconds to under a minute) pass through the gate; a whole-module compile check (`go build ./...`) stays heavy. |
| D10 | The release tier does **not** accept Go's test cache: the release gate keeps `-count=1` (10-15). |
| D11 | **Load watcher** (10-15): each daemon detects heavy commands outside the gate and high load on its own host, records them, and tells **one** watcher session for all hosts by peer message (§12). Each daemon has an ordered list of hosts that may hold the watcher; it remembers where the watcher is and looks again only when that fails (awake anywhere → the last one woken → a new one on the first reachable host); no switch back after a failover. Modes: resident, or patrol (the daemon counts the idle time and puts it to rest; it is resumed when needed); auto-wake on/off. The watcher's notes live in a dedicated branch, pulled at start and pushed after changes. Settings, including the start and wake prompts, are in each daemon's host config (not Profile Sync). Long-lived, low-usage background loops are not heavy commands and are not reported. |
| D12 | When a heavy command would wait, the mod moves the agent's Bash call to the background with a note (L1), so a session does not sit in the foreground limit. Verified 10-15: a background task survives a relay and its completion notice reaches the new conversation. |
| D13 | `pdx gate free <slot>`: lists the processes holding a slot and ends them after confirmation, only on a person's or the watcher's command; the gate itself never kills a running command (G2). |
| D14 | **Settings live on each host's daemon** (10-15): the watcher's `watch` row and the thresholds in the daemon's host config; the gate's `[gate]` stays a file (the gate does not depend on the daemon, D3) but the App reads and writes it through a daemon API that edits it the way `pdx gate set` does; each host has its own resource-control switch (`gate.enabled`) the App can turn on and off. Not Profile Sync. Each host keeps its own copy. |
| D15 | **Hosts** (10-15): the gate and the queue run on mlab and air26 only. air-2019 and mserv get **no daemon**; they are monitored only (no gate, no E1). All cross-host sampling is the fleet module's (primary a26, standby mlab, §12.8). |

## 2. Goals and non-goals

Goals
- G1. No heavy command on mlab or air26 runs outside a slot, whoever starts it (agent Bash, subagent, script,
  `pnpm` script, codex sandbox, a member's `go test` inside a helper script).
- G2. New heavy work is not admitted while the host is under memory pressure; work already running is never killed.
- G3. A heavy job uses at most its share: capped parallelism and a lowered priority (`nice`, D7), so the agents, the
  daemon and the person preempt it.
- G4. A slot is held as long as its command runs; no expiry, no leak, no orphan. A process that escapes the clean-up
  (a daemonized tmux server) does not keep the slot (§5 Slots).
- G7. A heavy command that runs outside the gate, or a host under pressure, is noticed and reported (§12).
- G5. The gate works with the daemon down, slow or restarting.
- G6. Idle daemon CPU from ≈30 % to ≤ 10 % of one core; no `ps` fork per registry entry.

Non-goals
- CI offload (GitHub Actions for Purdex, Gitea Actions for Aigora): separate spec, after this.
- Preemption (pausing or killing running jobs under pressure). `pdx gate free` (D13) is a person's or the watcher's
  explicit command, never automatic.
- Windows/Linux hosts (mserv keeps running what it runs; Linux has cgroups, a later concern).

## 3. Heavy-command classes

| Class | Commands (examples) | Slots | Notes |
|---|---|---|---|
| heavy | `go test` with more than one package or `./...`, `go build ./...` and other multi-package compile checks, `go vet ./...`; a vitest run with no file or filter; `tsc -b` / `tsc -p` of a whole project; `pnpm check`, `pnpm -r test`; `composer test`, `composer test:*`, `phpunit`/`pest` without a filter or file (incl. `--testsuite`) | `slots.heavy` (default 1, D7) | full suites |
| light | `go test` of one package; `vitest run <files>` / `vitest related`; `phpunit --filter …` or with a test file | `slots.light` (default 3) | targeted runs must not wait behind a full suite |
| none | packaging (D9): `pnpm build`, `pnpm electron:build`, `vite build`, `electron-vite build`, `electron-builder`, `go build -o …`, `go build ./cmd/x`, `composer build`, `pdx lease run --kind build`; vitest in watch mode; everything else (`go version`, `go env`, `node -e`, `vitest --version`, `composer install`) | — | passes through untouched |

A light job that turns out to be broad (e.g. a pattern matching hundreds of files) is still light: the cap
on its parallelism (§5) bounds the damage. Classification is a pure function with a table test:
`Classify(argv, Env)`, where `Env` (stdin a terminal, an agent, CI) decides vitest without a subcommand: vitest 4.1
watches only for a person at a terminal outside CI (`watch: !isCI && stdin.isTTY && !isAgent`), and runs once for an
agent, a script or CI, so there it is a run.

## 4. Interception (DRAFT — pending the tool survey)

Measured facts (10-15):
- `PATH` in agent shells: `~/.bun/bin`, Herd, `~/.nvm/versions/node/v24.14.0/bin`, `~/.local/bin`, …,
  `/opt/homebrew/bin`. A `~/.local/bin/go` shim therefore precedes Homebrew's `go` for every session without
  a restart; `npx`/`pnpm` live in the nvm dir, ahead of `~/.local/bin`.
- `tsc` and `vitest` are not on `PATH`; they run through `npx`, `pnpm exec`, `pnpm run <script>` (pnpm
  prepends `node_modules/.bin`), and Aigora's `scripts/check.mjs` → `capture-run.mjs` → `node`.
  Invocations recorded since 735: `pnpm typecheck` 48, Aigora `capture-run` 30, other `tsc` 11, `pnpm check` 7,
  `npx tsc` 6, `pnpm vitest` 5, other vitest 4, `npx vitest` 4.
- Claude Code is a native binary (Mach-O), so `NODE_OPTIONS` does not affect the agent itself.
- `~/.claude/settings.json` `env` is the one place that reaches every agent tool call (and codex launched from it).

Tool survey (`research-intercept-tools.md`, 10-15): **no existing tool replaces the gate.** Queue tools
(pueue, task-spooler, nq, GNU `sem`, flock(1)) have no memory-pressure awareness, no "already held" bypass
(submitting from inside a job can deadlock pueue/task-spooler) and no command interception. The closest design,
Getty/loadguard (a Claude Code PreToolUse plugin with a host-wide slot pool and "nested agents take no slot"),
is Linux-only (cgroup v2 + systemd). Borrowed: nq's FIFO order by ticket time, the jobserver rule "a child
holds its parent's slot implicitly", loadguard's nested bypass. Rejected: `NODE_OPTIONS --require` (inherited by
every node child incl. pnpm/corepack/workers, Electron ignores or errors under `ELECTRON_RUN_AS_NODE`, no
native flock for a synchronous hook), `VITEST_MAX_WORKERS` (not a documented knob), `ulimit -v/-m` (no effect
on macOS).

Design:
- **`pdx gate [--class heavy|light] [--fg] -- <cmd…>`** runs `<cmd>` under a slot (§5). `pdx gate --auto`
  classifies by argv (§3).
- **Shims, not hooks.** Thin shell shims for `go`, `pnpm`, `npx` (and `vitest`/`tsc` for direct calls) in
  `~/.config/pdx/shims/`, each: `exec pdx gate --auto --tool <name> -- "$@"`; class `none` execs the real
  binary at once (no extra process beyond one `pdx` exec). **The real binary is resolved at every run, not at setup**
  (so an `nvm` switch is followed): walk the current `PATH` skipping the shim dir, resolve symlinks, and refuse a
  result whose real path lies in the shim dir (no recursion, whatever the alias). `pnpm` is classified by its subcommand/script name
  (`check`, `test`, `typecheck`, `lint`, `-r`/`--recursive` → heavy; `vitest <files>` → light; install/add/etc. → none);
  `npx`/`pnpm exec`/`pnpm vitest|tsc` by the tool and its arguments. The shims inject the parallelism caps (§5).
- **PATH precedence** (measured: nvm's bin precedes `~/.local/bin`): the shim dir must come before nvm.
  **`pdx setup` prepends it in `~/.zshenv`.** Measured 10-15: Claude Code runs every Bash tool call as
  `/bin/zsh -c 'source <snapshot>; …'`; the snapshot does not set `PATH`, and `zsh -c` reads `~/.zshenv`
  (a variable defined only there reaches a `zsh -c` started without it). So running sessions pick the shims up on
  their next command, no restart. The user's own terminals get the same shims (intended: every heavy command on
  the host is gated). The block in `~/.zshenv` is delimited (`# >>> pdx gate >>>` … `# <<< pdx gate <<<`), idempotent
  on disk **and at run time** (it prepends only when `:$PATH:` does not already contain the shim dir, so nested
  `zsh -c` do not grow `PATH`), prints nothing, and is removed by `pdx setup --uninstall-gate` exactly (content before
  and after the block kept; a duplicated or unclosed old block is repaired, not left half). Non-zsh callers (launchd, `bash -c` scripts) are covered only where
  they call `pdx gate`/`pdx lease run` themselves.
- **Not covered by shims:** a direct `node_modules/.bin/vitest`, or a node program that starts vitest through its
  JS API. Aigora's `check.ts` already wraps each step in `pdx lease run` (becomes the gate alias, nested); for
  agents, the mod keeps a minimal rewrite that prefixes such commands with `pdx gate --auto --` (the only mod part
  that remains; no daemon call).
- **Nesting (verified, not trusted):** the gate exports `PDX_GATE_HELD=<class>-<k>:<token>` to the child, where
  `<token>` is a random value also written in the holder record. An inner gate passes through only when all hold:
  the token matches the record of that slot, that slot's flock is currently held (a non-blocking probe fails), and
  the inner process is a descendant of the holder pid (ppid walk). A forged, stale or foreign value is ignored (the
  inner gate takes a slot as usual). **No class escalation:** under a `light` holder, an inner `heavy` command takes
  a heavy slot of its own; under a `heavy` holder, inner heavy and light pass through. Children of a held job count
  against its slot (jobserver rule).
- **Explicit use:** scripts may call `pdx gate -- <script>` to hold one slot for a whole sequence (release gate).
- **Fail-open, narrowly:** only when opening/creating the gate directory fails with `EPERM`, `EACCES` or `EROFS`
  (codex read-only sandbox) does the gate run the command directly, with one stderr line. Any other error (the path
  is a file, the disk is full, a lock cannot be taken for a reason other than contention) is **fail-closed**: the
  command is refused with a message saying what to fix. Codex prompts already forbid full suites.

## 5. The gate (daemon-free)

- **Slots.** `~/.config/pdx/gate/<class>-<k>.lock`, k = 1…N, taken with `flock(LOCK_EX|LOCK_NB)` from Go
  (`syscall.Flock`; no external tool). **The lock fd is passed to the child** (`ExtraFiles`, not close-on-exec), so
  the whole process tree holds the same open file description. **When the command ends** (normally, by a signal or a
  timeout) the gate reaps its group and tree, names on stderr any process that still holds the lock (`lsof`; e.g.
  "these processes escaped the clean-up and are still running: 67278 (tmux); the heavy slot is released"), then
  removes the record and unlocks with `LOCK_UN` on its own fd, which releases the lock for every holder of that
  description. Measured 10-15: a `tmux -L … new-session -d` started under the lock keeps fd 3, so without the unlock a
  test that leaks its tmux server would hold the heavy slot for good. **A gate killed with SIGKILL** cannot unlock:
  the slot stays held while any process of the tree lives (no expiry, no `max_hold`, no double booking); `pdx gate
  status` shows "gate gone, tree alive" with the pids holding the lock, and `pdx gate free` (D13) ends them on request.
- **Disk floor (heavy only).** Before taking a heavy slot the gate checks free space on the volume of the cwd and of
  `$TMPDIR` (`statfs`) against `admission.disk_min_gb` (default 10); below it the command is refused with the numbers.
  This replaces the lease's disk preflight from G1 on (the daemon's disk alert stays).
- **Fair order (nq-style).** A waiting gate first writes a ticket `queue/<class>/<unix-ns>-<pid>` and holds a
  flock on it (so a dead waiter's ticket is detectably stale). It may take a slot only when the number of live
  tickets older than its own is smaller than the number of free slots; otherwise it waits (poll with jitter,
  1–3 s), printing its place once per minute to stderr. The ticket is removed when the slot is taken or the gate
  exits. This prevents the starvation that plain lock polling allows.
- **Holder record.** `<class>-<k>.json` beside the lock: pid, pgid, argv (truncated, secrets scrubbed by the
  same rules as the transcript scrubber), cwd, start time, `CLAUDE_SESSION_ID`/agent id when present. The lock
  is the truth; a record without a held lock is stale and ignored.
- **Admission by memory pressure.** Before taking a slot (and while waiting), require
  `kern.memorystatus_vm_pressure_level == 1` **and** pageouts+swapouts over the last 10 s below
  `max_page_rate` (from `host_statistics64`/`vm_stat`). Concurrent gates share one sampler: whoever holds
  `pressure.lock` (flock, non-blocking) takes the sample and writes `pressure.json` atomically (temp file + rename);
  the others read the last complete file if it is younger than 5 s. With no previous sample (no delta yet) only the
  level is used. A read failure keeps the last good sample; only 3 consecutive failures make the gate admit on the
  level alone (and, if the level is unreadable too, fail-open with a stderr line).
- **Parallelism for the child (D7: near full width, one at a time).**
  - `auto` (default): width = cores − `reserve_cores` (2 → 8 on mlab), lowered so that width × `mem_per_worker_mb`
    fits the memory available at admission; computed per job from the live host. `mem_per_worker_mb` is per tool
    (G0: go 150, vitest 700, others 400). vitest is further capped by `vitest_max_workers` (3): at 8 workers the full
    suite was only 19 % faster, reached pressure level 2 and gained 101 timeout failures (§5.1); raise the cap only
    with a clean measurement, after the suite's per-file cost changes.
  - Go: add `-p=<go_p>` to `GOFLAGS` when absent; leave `GOMAXPROCS` alone. vitest: add `--maxWorkers=<n>` when the
    command has no worker limit (replaces the mod's R7). pnpm: `--workspace-concurrency=<n>` for recursive runs.
  - The real load is slots × width; with one heavy slot near full width plus small light jobs, the host stays near its
    cores without the overlap that caused swapping.
- **Priority for the child.** Default `nice` (10): the agents, the daemon and the person preempt it, and it still uses
  the P cores when they are idle. `background` (`taskpolicy -b`: E cores only, I/O throttled; G0: 10–46 % slower on
  every clean pair, §5.1) stays an option for long, non-urgent work, never a default. `--fg` = no change (e.g. the
  release gate on an idle host).
- **Process group.** When stdin is not a terminal (agents' Bash, scripts) the child starts in a new process group;
  SIGINT/SIGTERM/SIGHUP to the gate are forwarded to the group, and when the child exits the gate sends SIGTERM to
  whatever is left in the group, SIGKILL after 5 s. When stdin **is** a terminal the child stays in the foreground
  group (a background group reading the terminal would get SIGTTIN; the current `pdx lease run` already does this,
  `cmd/pdx/lease_run.go`): signals reach it from the terminal, and at exit the gate reaps its descendants by a ppid
  walk instead of a group kill. Exit code = the child's (128+n on a signal).
- **Modules and configuration (D8).** Each of the seven items is a module with its own switch and parameters, its own
  file (`internal/gate/<module>.go`), Config type and table tests; `gate.Run` only composes them.

  | Module | Item | Parameters (default) |
  |---|---|---|
  | `intercept` | 1 | `tools` (go, pnpm, npx, vitest, tsc), `mod_rewrite` (true), extra `[[gate.intercept.rule]]` (match → class, before the built-ins) |
  | `slots` | 1 | `heavy` (1), `light` (3), `order` (fifo) |
  | `admission` | 2 | `enabled`, `max_pressure_level` (1), `max_page_rate` (auto = 50 pages/s over 10 s, G0; the level is the primary signal), `disk_min_gb` (10), `on_unreadable` (level-only) |
  | `parallelism` | 3 | `mode` (auto \| fixed \| off), `reserve_cores` (2), `mem_per_worker_mb` per tool (go 150, vitest 700, others 400), `go_p` / `vitest_workers` / `pnpm_concurrency` (auto), `vitest_max_workers` (3), per-class overrides |
  | `priority` | 4 | `mode` (nice \| background \| none), `nice` (10) |
  | `lifetime` | 5 | `hold` (tree), `alert_after_min` (60 — alert only, never kill) |
  | `reap` | 6 | `enabled`, `grace_s` (5) |
  | `cache` | 7 | `count1` (warn \| strip \| off) — `go test -count=1` outside the release profile |

  **Run-time tuning:** the config (`[gate]` in `~/.config/pdx/config.toml`) is read at every gate start and by every
  waiting gate on each poll, so a change applies to the next command without a restart. `pdx gate set <module>.<key>=<value>`
  writes it atomically; `[gate.profiles.<name>]` overrides a set (`pdx gate profile use release|night|…`). `auto` values
  are computed from the live host per job. Every job appends one decision record (JSON lines: class, slot, wait and its
  reasons, the parameters applied and their source — default / config / profile / auto —, exit code, duration);
  `pdx gate status` shows each module's effective values and sources. Later, an optional tuner (daemon or script) may
  write the same config from the records; the gate itself never depends on it.

### 5.1 G0 measurements (mlab, 2026-10-15)

M4 10 cores / 16 GB. "nice" = `nice -n 10`, "bg" = `taskpolicy -b`. Page rate = pageouts + swapouts, peak over a 10 s
window. Raw data: the lead's `reports/g0-8c/` (samples every 5 s, `/usr/bin/time -l`, vitest per-file JSON).

| Command | Setting | Wall | Peak pressure | Min free | Peak page rate | Clean |
|---|---|---|---|---|---|---|
| `go test ./internal/store/` | plain / bg | 6.2 s / 9.0 s (+46 %) | 1 | 70 % | — | yes |
| `go build ./...` | plain / bg | 6.8 s / 7.9 s (+15 %) | 1 | 69 % | — | yes |
| `go test -p 3 ./internal/module/team/` | plain / bg | 333 s / 365 s (+10 %) | 1 | 68 % | 1.2 | yes |
| `go test -count=1 ./...` | `-p 8` + nice | 382 s | 1 | 72 % | 0.2 | yes (rerun on a quiet host) |
| `go test -count=1 ./...` | `-p 3` + bg | 448 s (+17 %) | 1 | 72 % | 2.0 | yes (rerun on a quiet host) |
| `vitest run` (full) | 3 workers | 2426 s | 1 | 56 % | 17.1 | lightly disturbed |
| `vitest run` (full) | 8 workers + nice | 1965 s (−19 %), 101 timeout failures | **2** | 39 % | 28.1 | lightly disturbed |

Findings: `taskpolicy -b` is slower on every clean pair, so `nice` is the default (Q2). Go runs at full width without
memory pressure. vitest does not scale with workers (≈ 85 % of its wall time is per-file fixed cost: environment,
import, setup) and 8 workers reach pressure level 2, so its width is capped (§5 parallelism). No clean run exceeded
28 pages/s; `max_page_rate` auto = 50 sits above all of them, with the pressure level as the primary signal (Q3).

## 6. Retiring the lease (D2)

Remove:
- daemon `internal/resources`: `admit*.go`, `leaseuse.go`, `position*.go`, `notice_fallback.go` (the
  `lease_notice` rule), `wire_lease.go`, `full.go`; `internal/module/resources`: `lease_handler.go`,
  `admission_pass.go`, `positions*.go`, `position_routes.go`, `notices*.go`, `store_notices.go`,
  `store_positions.go`, the lease parts of `store_tx.go`, `nested.go`, `reasons.go`, `shadow.go`, `sweeper.go`,
  `interactive.go`; the routes `/api/resources/leases…` and `/api/resources/positions…`.
- CLI: `pdx lease acquire|release|confirm|cancel|done|ls|mode`; **keep `pdx lease run` as a deprecated alias of
  `pdx gate`** for one release cycle (16 lead scripts, Aigora `scripts/lib/check.ts`), then remove.
- mod: `hooks/lease.js`, `registerLease`, `skills/pdx-lease/`, `lease.notice.v1` in `CAPS`; the embed guards
  that name them. Protocol: bump `VERSION` in `register.js` (coordinate with RT-4, which also bumps).
- mod, what replaces it (`hooks/gate.js`, L1):
  - the minimal rewrite for direct calls the shims cannot see: `node_modules/.bin/{vitest,tsc}` and
    `vendor/bin/{phpunit,pest}` (and `./` forms) get the `pdx gate --auto --` prefix; a rewrite that would change the
    call's permission verdict is not made (the call runs as is, and the watcher's detection covers it);
  - **auto-background (D12)**: for a foreground Bash call that the gate would make wait (`pdx gate status --json`:
    a heavy or light call with no free slot, or pressure above the admission limit), the hook rewrites the call to
    `run_in_background: true` and adds a note to the result: who holds the slot, for how long, the place in the queue,
    "it starts by itself and you will be told when it ends; do not run it again". A call that can start at once
    stays in the foreground. Tests: busy → background with the note; free → unchanged; a subagent's call the same.
- interim (until L1): csp-plugin's sessions run their integration tests through `composer test:integration`
  (gated by the composer shim) rather than `vendor/bin/phpunit` directly; the lead tells them at the deploy.
- daemon plumbing that only served lease notices: `modevents.CapLeaseNoticeV1`, `promptq.KindNotice` /
  `CapLeaseNotice` and their hand-out branch, team `LeadNoticer` (if nothing else uses it — plan verifies).
- settings: `kinds`, `deadline_s`, `warmup_s`, `floor_pct`, `max_hold_s`, `ewma_half_life_s`,
  `heavy_min_weight`, `park_s`, `gap_s`, `load_cap_pct`, `alert_lease_mem_pct`, `mode`. Today
  `normalizeResources` is shared by PUT and by reading the stored document and rejects unknown keys
  (`internal/module/hostconfig/resources.go`), so removing keys would make every stored document invalid. Split it:
  **PUT stays strict** (unknown keys rejected); **reading a stored document is lenient** (retired keys ignored) and
  the next write drops them. Tests: a stored document with every retired key reads fine and round-trips without them.
- data: `resource_leases`, `resource_positions` — a store migration drops them (deployed schema: the step is
  additive-safe for a rollback to a binary that would recreate them empty).

Keep and rework:
- host sampling (`sample*.go`, `hostuse.go`, `ewma.go`, `roots.go`, `sampler_loop.go`, session attribution
  `attribute.go`/`session_attr.go` — `pdx team show` uses it);
- zones: the memory input becomes the pressure level + page rate (same signal as the gate), CPU load stays
  informational;
- alerts (`events.go`): danger and stuck rules on the new zone; `lease_notice` removed;
- disk guard: in the gate from G1 (§5 disk floor); the daemon's sampler keeps the disk alert;
- `pdx team show` host shares: `/api/resources` keeps `Snapshot.Sessions` (session attribution); a test fails if the
  sessions disappear from the wire (today `cmd/pdx/team_cmd_resources_test.go`).
- `host_minutes`: the lease columns are written as 0; new columns `heavy_running`, `light_running`,
  `gate_waiting` read from the slot records; `pdx lease report` → `pdx host report` (heavy minutes, waits,
  zone seconds) — the strategy report script reads it.

## 7. Daemon steady cost (D4)

Profile 10-15 08:50, host idle (load 2): the daemon used 9.1 s of CPU in 30 s.

| Hot path | Share | Cause | Change |
|---|---|---|---|
| `peers.ReadRegistryDiag` → `agent.ReadProcessInfo` → `ps` | ~14 % | one `ps` fork **per registry entry** per read; the registry is read by the resource sampler (every 5 s), session attribution, `ProcessRoots`, and team liveness | one process snapshot for the whole daemon (in-process `sysctl KERN_PROC_ALL` or one `ps -A` per ≥ 2 s), shared by every reader with a TTL; `ReadProcessInfo` reads it |
| `team.(*Module).tick` (`markGoneMembers`, `endGoneTeams` → `LeadPresence`) | ~11 % | the sweeper ticks every 1 s: `store.ListOpen` each second; liveness work every 10 s re-reads registry + processes | liveness on the shared snapshot; `ListOpen` only when something can have changed (dirty flag on writes), else every 5 s |
| conversation WS `follow` | ~7 % | polls every 500 ms per open conversation | event-driven: wake on the conversation feed's change notification; poll fallback 2 s |
| `agent.handleEvent` | ~6 % | normal hook work | none |

Also fixed with 737: `store.pruneTraceChains` (35 %, #2887/#2889).
Target: idle ≤ 10 % of one core; measured the same way (30 s pprof with the host idle) before and after.

## 8. Test cache (D1 item 6)

`-count=1` disables Go's test cache. Remove it from the PR tier and the lead's non-release scripts; keep it only
in the release gate (`test-at.sh`). `CLAUDE.md` 測試分層 gains one line. vitest: prefer `related --run` /
`--changed` at the PR tier (already in 測試分層).

## 9. Phasing (one phase = one PR, ≤ 800 lines or ≤ 20 files; pure deletions may exceed with a byte-level listing)

| Phase | Content | Depends |
|---|---|---|
| G1 | `pdx gate` core: slots (lock fd inherited by the tree), fair tickets, holder records, verified nesting without class escalation, TTY-aware process group reap, pressure admission with a shared atomic sample, disk floor, QoS, parallelism env, config, narrow fail-open; unit tests (fake clock/pressure reader, real flock in temp dirs, real process trees) | — |
| G2 | interception: go shim + classifier, Node hook, `pdx setup` installs both and writes the Claude Code `env`; `pdx lease run` → gate alias | G1, §4 final |
| G3 | daemon steady cost: shared process snapshot, team sweeper cadence, WS change notification | — (parallel to G1/G2) |
| L1 | retire the lease in the mod (lease.js, skill, CAPS, every embed guard that names the lease, protocol bump with its test updates); `hooks/gate.js`: the minimal rewrite for direct `node_modules/.bin` and `vendor/bin/{phpunit,pest}` calls, auto-background when a call would wait (D12, §6) | G2 deployed |
| L2 | rework the kept parts **before** anything is dropped: zone on pressure, `host_minutes` and the report read slot records instead of `resource_leases`/`resource_positions`, `pdx host report`, lenient hostconfig read | L1 |
| L3 | retire the lease in the daemon + CLI (routes, verbs, notices, promptq kind, settings keys, DB drop) — only after L2 is deployed, so every binary in between still works | L2 deployed |
| D | docs/scripts: CLAUDE.md, skill `pdx-gate`, lead scripts (`-count=1`, `pdx gate`), Aigora note | G2 |
| W0–W5 | load watcher (§12) | W0 now; W1+ after acceptance |

Rollout: G1+G2(+G3) deploy first with the lease switched to `advise` on both hosts (no double gating);
then L1–L3. Other lines resume after G2 is deployed and its acceptance (§10) passes.

## 10. Acceptance (after G2 deploy, on mlab)

1. Three heavy jobs started together (two `go test ./...` in throwaway worktrees, one vitest full) → one runs at a
   time near full width (`slots.heavy` = 1), in arrival order; a light job started meanwhile runs alongside;
   `pdx gate status` shows holders, waiters and each module's effective values. Total wall time is recorded and
   compared with the same three started together without the gate.
1b. Run-time tuning: `pdx gate set slots.heavy=2` while one waits → the waiter starts on its next poll;
   `pdx gate profile use release` changes the applied parameters of the next job (decision record shows the source).
2. Nested: `pnpm check` in Aigora (or a test script calling `go test` twice) holds one slot; inner steps do not wait.
3. A gated script started from a subagent and from a plain zsh (not through the mod) is gated the same way.
4. Kill the gate of a running vitest full → no `node (vitest N)` left after 5 s; the slot is free.
5. Pressure: with an injected reader, level 2 holds new heavy and light jobs; running ones continue.
6. Daemon stopped: gating still works; `pdx gate status` reads the slot records directly.
7. Load: a reference workload (vitest full + `go test ./...` + `tsc -b --force` started together) keeps
   interactive latency acceptable — load1 and `memorystatus_vm_pressure_level` logged every 10 s; no swap growth.
8. Codex sandbox: a gated command inside a read-only sandbox runs (fail-open) with the stderr line.
9. Escaped holder: a gated command that starts `tmux -L <label> new-session -d` and exits → the slot is free at once,
   stderr names the tmux server; `tmux -L <label> kill-server` afterwards (and `unset TMUX` in the test shell).
10. Packaging: `pnpm run electron:build` (and `pdx lease run --kind build -- …`) starts at once while a heavy job holds
   the slot.

## 11. Open questions (for the plan)

- Q1. (resolved 10-15) PATH precedence: `~/.zshenv` prepend reaches running sessions (see §4).
- Q2. (resolved, G0 10-15) `taskpolicy -b` is 10–46 % slower on every clean pair; default `nice` (§5.1).
- Q3. (resolved, G0 10-15) `max_page_rate` auto = 50 pages/s; the pressure level is the primary signal (§5.1).
- Q4. (resolved, codex review 10-15) The minimal mod rewrite for direct `node_modules/.bin/…` calls by agents is kept: shims alone cannot reach them (G1 "whoever starts it").

Survey sources: Getty/loadguard, leahneukirchen/nq, Nukesor/pueue, discoteq/flock, fl4p/macos-oom-guard, GNU make
job slots, vitest "improving performance", Turborepo `run`, pnpm recursive, Electron environment variables
(links in `research-intercept-tools.md`).

## 12. Load watcher (D11–D13)

The gate sees only what passes its shims. The watcher covers the rest: heavy commands that run outside the gate,
a host under pressure, and the states the gate cannot fix by itself (a slot held after its gate died).

### 12.1 Detection and record (W1, daemon)

- The daemon's sampler gains a process pass on every sampler tick (5 s), from the `ps` and `vm_stat` it already
  runs (no new fork). A **tree** is a process and its descendants, rooted at the child of a shell (an agent's Bash, a
  terminal, a script). A tree under a `pdx gate` process, or holding a slot, is managed.
- Events:
  - **E1 `unmanaged_heavy`**: an unmanaged tree using ≥ X cores (average over T s) or ≥ Y MB for T s; one event per
    episode (re-armed when the tree ends or stays under for T s). Labelled by the gate's classifier on the root's
    command: `gate_gap` (a heavy or light command that ran outside the gate), `unknown` (a tool the gate does not
    know), `allowed` (packaging or another pass-through by rule; recorded and delivered, never held, D9). Its command
    signature (tool, subcommand, script name; paths and values dropped) seen for the first time is **new** (kept
    across daemon restarts); later sightings are counted, not re-sent.
  - **E2 `pressure_high`**: pressure level ≥ 2 for ≥ T2, or page rate above R for ≥ T3.
  - **E3 `slot_orphaned`**: a slot held while its gate is gone, for ≥ 15 min (names the holders).
  - **E4 `slot_long`**: a slot held ≥ `lifetime.alert_after_min` (60).
  - Not an event: long-lived, low-usage processes (polling loops); they are not heavy (D11).
- X, Y, T, T2, R, T3 come from W0. Targets: E1 fires for csp's `composer test:integration` and
  `vendor/bin/phpunit --testsuite integration`; never for the Purdex App build, Claude Code sessions, the daemon or a
  browser at rest; a few events a day at most on mlab.
- Record: append-only JSON lines under `~/.config/pdx/watch/` (rotated, 30 days): kind, host, time, command (scrubbed
  by `internal/redact`), cwd, the Claude session found in the ancestry, peak cores and memory, duration, `new`.

### 12.2 Settings (each host's daemon config)

The watcher's settings are a `watch` row in each daemon's host config (the store of the daemon-side settings such as
the team settings), read at every pass; the App's settings page edits them through the daemon API, like the other
daemon settings. Not Profile Sync: Profile Sync carries the App's own stores and the daemon cannot read them
(user decision 10-15). Each host keeps its own copy; a cross-host sync may come later.

| Key | Default | Used by |
|---|---|---|
| `enabled` | true | detection (W1) |
| thresholds (`x_cores`, `y_mb`, `t_s`, `pressure_t_s`, `page_rate`, `page_t_s`) | 1.5, 1024, 60, 120, 50, 60 | detection |
| `targets` | `[a26, mlab]` (host aliases, in order: a26 normally, mlab only when a26 cannot be reached) | where events go and where a new watcher is started |
| `mode` | `patrol` | `resident` (never rests) or `patrol` (rests when idle) |
| `patrol_idle_min` | 120 | the rest countdown |
| `auto_wake` | true | whether an event wakes or starts a watcher |
| `model` | opus | a new watcher |
| `workdir` | `~/Workspace/wake/mlab-purdex-watch` (both hosts) | the watcher's directory on this host: a checkout of the mlab repo used only for its `purdex-watch` branch |
| `start_prompt` | built-in | the first prompt of a new watcher |
| `wake_prompt` | built-in | the prompt that resumes a resting watcher |

### 12.3 Where the watcher is, and delivery (W2)

- **One watcher for all hosts.** Each daemon remembers where the current watcher is (host and session) and sends its
  events there with the peer path (`pdx msg`'s). It looks again only when a delivery fails or the remembered watcher
  is gone, in this order: (1) a watcher awake on any host in `targets` → use it; (2) the last watcher's host is
  reachable → ask it to wake that watcher (same session, resumed); (3) start a new one on the first reachable host in
  `targets`. Steps 2 and 3 run only when `auto_wake` is on; with it off, events are recorded and delivered only to a
  watcher that is awake.
- **One at a time.** Only the host that will hold the watcher starts or wakes it, one start in progress at a time;
  before starting a new one it tells the other hosts, and when two hosts announce at once, the smaller host id wins.
  Daemons compare their `targets` and warn when they differ.
- **No switch back.** After a failover the watcher stays where it is; the order in `targets` matters only when a new
  one has to be started.
- Delivery: at most one message per event kind per host per 10 min (later ones are folded into a count); E3 is never
  folded. A woken or new watcher first gets a summary of the unhandled events since the last delivery.
- `pdx watch log [--since] [--host|--all]` reads the records; `pdx watch ack` marks events handled (the UI's count).

### 12.4 The watcher session (W3)

- **Started and managed by the daemon of the host that holds it**: a session outside any team, in `workdir`, with
  `model` and `start_prompt`; woken with `wake_prompt` by resuming the same session.
- **Rest (patrol mode).** The daemon counts the idle time (no event delivered, no turn of the watcher) and at
  `patrol_idle_min` sends "time to rest"; the watcher writes its notes, pushes them and answers; the daemon then ends
  the session (after 5 min without an answer, it ends it anyway) and keeps the session id for the next wake. In
  `resident` mode there is no countdown.
- **Memory and documents in git.** The watcher works in `workdir`, a checkout of the `purdex-watch` branch of the mlab
  repo (`ssh://git@lab.protype.tw:9079/wake/mlab.git`; branched from its main, never merged back; notes under
  `watch/`): it pulls the branch when it starts or wakes, and commits and pushes after it changes its notes. A watcher
  started on another host after a failover pulls the same branch. The lead makes the checkout once on each host (plan
  W3 step 5); the daemon never clones.
- A built-in brief and skill (`pdx-watch`): the above; what each event and label means; what the watcher may do (tell
  the person, message the session that started the command, propose an intercept rule that is applied only when the
  person agrees, run `pdx gate free` after confirming); what it must not do (end a process that holds no slot; change
  thresholds by itself).
- Live from the first deploy (10-15: "go live and sample while it runs"); thresholds start from W0's first data and
  are tuned on the running system (§12.7).

### 12.5 `pdx gate free <class>-<k>` (W4)

Lists the processes holding the slot's lock (pid, command, cwd, start, session), asks for confirmation (`--yes` for a
watcher that already has the person's agreement), sends SIGTERM, then SIGKILL after 5 s, and reports whether the slot
is free. A slot whose gate is still alive is refused (interrupt that command instead) unless `--force`.

### 12.6 UI (W5, the App; B line)

- The App's load-control UI as a whole (monitoring of the four hosts, the watcher, the queue state and the gate
  settings of each host, D14) is specified by the B line with the user (10-15); the items below are the watcher's part.
- Sidebar: at the right of the sidebar's top row (`HomeRow`, the row that shows the profile name, which the user calls
  the "Main Station" row; not the host list), a watcher icon in three states: none, active, unhandled events (with a
  count). It opens the watcher area.
- The watcher area is a tab region of its own, apart from session tabs: a fixed tab for the watcher session of the
  host that holds it (awake or resting; opening it and asking wakes a resting watcher), the event log of all hosts
  (filter by host and kind, unhandled count), "start the watcher" when there is none, and the `watch` settings of each
  host (§12.2, including the two prompts).

### 12.7 W0: threshold evaluation (now)

- E2: replay `host_minutes` (load1 and memory per minute since 10-09) against candidate thresholds.
- E1: a 48 h process sampler (lead script, every 10 s, processes ≥ 0.25 core or ≥ 300 MB with their ancestry;
  started 10-15 19:3x, ends by itself) replayed against candidate sets, e.g. 1.5 cores or 1 GB for 60 s, and 2 cores
  or 2 GB for 120 s.
- Output: events per day and what each set catches; the values become the `watch.*` defaults and keep being tuned
  from the live record (thresholds are settings, read at every pass).

W1–W4 start now (A-line member), W5 when the B line resumes; none depends on L1–L3. The watcher set goes live as a
whole when W1–W4 are done (W5 can follow). The first stage (G1–G3) ships without waiting for it.

### 12.8 Cross-host sampling: one fleet sampler (D15)

- air-2019 and mserv are monitored only: no gate, no queue, no E1 (no process trees to attribute). The watcher gets E2
  for them, and an `offline` state when they cannot be read (air-2019 sleeps often).
- **One cross-host sampler, the fleet module** (user decision 10-15, through the B line; the fleet status spec
  `docs/specs/2026-10-14-fleet-status-spec.md` is the source of truth, kept by the B line, which also builds it). In
  short: a primary and a standby in the order `[a26, mlab]`; the primary reads every host each second (real CPU use
  from CPU-time deltas, memory use, pressure level, page rate, disk) and every 5 s two lists (top 5 processes by CPU
  and by memory from one `ps`; the gate queue of mlab and a26); fixed rates, never slowed on a busy host; the results
  go to the App only (no database, no per-sample log; `host_minutes` unchanged). The watcher runs no SSH of its own:
  E2 for air-2019 and mserv is judged by the fleet primary on the samples it holds in memory, added after the fleet
  phases land (not part of W1–W4).
- **What the A line owes it:** `pdx gate status --json` runs without the daemon (§5, D3) and stays cheap enough for
  the primary to read it over SSH every 5 s (`pdx` start-up cost: #2925). Its fields are settled with the B line
  together with the UI's other APIs.
