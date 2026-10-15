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

## 2. Goals and non-goals

Goals
- G1. No heavy command on mlab or air26 runs outside a slot, whoever starts it (agent Bash, subagent, script,
  `pnpm` script, codex sandbox, a member's `go test` inside a helper script).
- G2. New heavy work is not admitted while the host is under memory pressure; work already running is never killed.
- G3. A heavy job uses at most its share: capped parallelism and background QoS (E cores only), so the P cores
  stay for the agents, the daemon and the person.
- G4. A slot is held exactly as long as its process tree lives; no expiry, no leak, no orphan.
- G5. The gate works with the daemon down, slow or restarting.
- G6. Idle daemon CPU from ≈30 % to ≤ 10 % of one core; no `ps` fork per registry entry.

Non-goals
- CI offload (GitHub Actions for Purdex, Gitea Actions for Aigora): separate spec, after this.
- Preemption (pausing or killing running jobs under pressure).
- Windows/Linux hosts (mserv keeps running what it runs; Linux has cgroups, a later concern).

## 3. Heavy-command classes

| Class | Commands (examples) | Slots | Notes |
|---|---|---|---|
| heavy | `go test` with more than one package or `./...`, `go build ./...`, `go vet ./...`; `vitest run` with no file argument; `tsc -b` / `tsc -p` of a whole project; `pnpm check`, `pnpm -r test` | `heavy_slots` (default 2) | full suites |
| light | `go test` of one package; `vitest run <files>` / `vitest related`; `go build ./cmd/x` | `light_slots` (default 4) | targeted runs must not wait behind a 20-minute suite |
| none | everything else (`go version`, `go env`, `node -e`, `vitest --version`) | — | passes through untouched |

A light job that turns out to be broad (e.g. a pattern matching hundreds of files) is still light: the cap
on its parallelism (§5) bounds the damage. Classification is a pure function with a table test.

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
  `~/.config/pdx/shims/`, each: `exec pdx gate --auto --real <real binary> -- "$@"`; class `none` execs the real
  binary at once (no extra process beyond one `pdx` exec). `pnpm` is classified by its subcommand/script name
  (`check`, `test`, `typecheck`, `lint`, `-r`/`--recursive` → heavy; `vitest <files>` → light; install/add/etc. → none);
  `npx`/`pnpm exec`/`pnpm vitest|tsc` by the tool and its arguments. The shims inject the parallelism caps (§5).
- **PATH precedence** (measured: nvm's bin precedes `~/.local/bin`): the shim dir must come before nvm. Two ways,
  chosen in the plan after measuring Q3: (i) `pdx setup` prepends the shim dir in `~/.zshenv` (read by every zsh,
  interactive or not) — new shells only; running Claude Code sessions keep their snapshot until restarted, which the
  pause allows; (ii) `pdx setup` places wrappers in the active nvm bin dir and re-asserts them on each setup.
  The `go` shim may also sit in `~/.local/bin` (already ahead of Homebrew) for immediate effect.
- **Not covered by shims:** a direct `node_modules/.bin/vitest`, or a node program that starts vitest through its
  JS API. Aigora's `check.ts` already wraps each step in `pdx lease run` (becomes the gate alias, nested); for
  agents, the mod keeps a minimal rewrite that prefixes such commands with `pdx gate --auto --` (the only mod part
  that remains; no daemon call).
- **Nesting:** the gate exports `PDX_GATE_HELD=<class>-<k>` to the child; a gate or shim that sees it passes
  through (vitest workers, `tsc` inside `pnpm check`, `go test` inside a gated script). Children of a held job
  count against its slot (jobserver rule).
- **Explicit use:** scripts may call `pdx gate -- <script>` to hold one slot for a whole sequence (release gate).
- **Fail-open:** if the gate directory cannot be opened (codex read-only sandbox), run the command directly
  and print one line to stderr. Codex prompts already forbid full suites.

## 5. The gate (daemon-free)

- **Slots.** `~/.config/pdx/gate/<class>-<k>.lock`, k = 1…N, taken with `flock(LOCK_EX|LOCK_NB)` from Go
  (`syscall.Flock`; no external tool). The lock lives on an fd the gate holds until the child's process group is
  gone; the kernel drops it if the gate dies (no expiry, no `max_hold`, no stale holder).
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
  `max_page_rate` (from `vm_stat`/`host_statistics64`, sampled by the gate itself; a cached sample younger
  than 5 s in `~/.config/pdx/gate/pressure.json` may be reused by concurrent gates). Unreadable → admit
  (fail-open), with a stderr line.
- **Parallelism and QoS for the child.**
  - Go: add `-p=<go_p>` to `GOFLAGS` when absent (default 3); leave `GOMAXPROCS` alone.
  - vitest: the shim adds `--maxWorkers=<n>` (default 3) when the command has no worker limit (as the mod's R7 did).
  - pnpm: `--workspace-concurrency=<n>` (default 2) for recursive runs when absent.
  - The real load is slots × per-command parallelism: the defaults (2 heavy × 3) target the 6 E cores.
  - Run the child with background QoS (`taskpolicy -b`, inherited by children), so it is scheduled on E cores.
    `taskpolicy -b` also throttles I/O, so I/O-bound steps (`go build`, `pnpm install`) may slow down a lot: the
    plan measures one go test package, vitest full and `go build ./...` with and without it, and the default per
    class follows the numbers. `--fg` opts out (the lead's release gate when the host is otherwise idle).
- **Process group.** The child starts in a new process group. SIGINT/SIGTERM/SIGHUP to the gate are forwarded
  to the group; when the child exits, the gate sends SIGTERM to whatever is left in the group, SIGKILL after
  5 s, then releases the slot. Exit code = the child's.
- **Config.** `[gate]` in `~/.config/pdx/config.toml` (read by the gate directly): `heavy_slots`,
  `light_slots`, `go_p`, `vitest_workers`, `max_page_rate`, `enabled`. Defaults suit a 10-core host.

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
- daemon plumbing that only served lease notices: `modevents.CapLeaseNoticeV1`, `promptq.KindNotice` /
  `CapLeaseNotice` and their hand-out branch, team `LeadNoticer` (if nothing else uses it — plan verifies).
- settings: `kinds`, `deadline_s`, `warmup_s`, `floor_pct`, `max_hold_s`, `ewma_half_life_s`,
  `heavy_min_weight`, `park_s`, `gap_s`, `load_cap_pct`, `alert_lease_mem_pct`, `mode`. Unknown keys in a
  stored hostconfig are ignored on read and dropped on the next write.
- data: `resource_leases`, `resource_positions` — a store migration drops them (deployed schema: the step is
  additive-safe for a rollback to a binary that would recreate them empty).

Keep and rework:
- host sampling (`sample*.go`, `hostuse.go`, `ewma.go`, `roots.go`, `sampler_loop.go`, session attribution
  `attribute.go`/`session_attr.go` — `pdx team show` uses it);
- zones: the memory input becomes the pressure level + page rate (same signal as the gate), CPU load stays
  informational;
- alerts (`events.go`): danger and stuck rules on the new zone; `lease_notice` removed;
- disk guard: checked by the gate before taking a heavy slot (refuse with a clear message when below the
  floor) and by the daemon's sampler for the alert;
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
| G1 | `pdx gate` core: slots, holder records, nesting env, process group reap, pressure admission, QoS, parallelism env, config, fail-open; unit tests (fake clock/pressure reader, real flock in temp dirs) | — |
| G2 | interception: go shim + classifier, Node hook, `pdx setup` installs both and writes the Claude Code `env`; `pdx lease run` → gate alias | G1, §4 final |
| G3 | daemon steady cost: shared process snapshot, team sweeper cadence, WS change notification | — (parallel to G1/G2) |
| L1 | retire the lease in the mod (lease.js, skill, CAPS, embed guards, protocol bump) | G2 deployed |
| L2 | retire the lease in the daemon + CLI (routes, verbs, notices, promptq kind, settings, DB drop) | L1 |
| L3 | rework kept parts: zone on pressure, host_minutes columns from slot records, disk guard in the gate, `pdx host report` | L2 |
| D | docs/scripts: CLAUDE.md, skill `pdx-gate`, lead scripts (`-count=1`, `pdx gate`), Aigora note | G2 |

Rollout: G1+G2(+G3) deploy first with the lease switched to `advise` on both hosts (no double gating);
then L1–L3. Other lines resume after G2 is deployed and its acceptance (§10) passes.

## 10. Acceptance (after G2 deploy, on mlab)

1. Three heavy jobs started together (two `go test ./...` in throwaway worktrees, one vitest full) → at most
   `heavy_slots` run; the third waits and starts when one ends; `pdx gate status` shows holders and waiters.
2. Nested: `pnpm check` in Aigora (or a test script calling `go test` twice) holds one slot; inner steps do not wait.
3. A gated script started from a subagent and from a plain zsh (not through the mod) is gated the same way.
4. Kill the gate of a running vitest full → no `node (vitest N)` left after 5 s; the slot is free.
5. Pressure: with an injected reader, level 2 holds new heavy and light jobs; running ones continue.
6. Daemon stopped: gating still works; `pdx gate status` reads the slot records directly.
7. Load: a reference workload (vitest full + `go test ./...` + `tsc -b --force` started together) keeps
   interactive latency acceptable — load1 and `memorystatus_vm_pressure_level` logged every 10 s; no swap growth.
8. Codex sandbox: a gated command inside a read-only sandbox runs (fail-open) with the stderr line.

## 11. Open questions (for the plan)

- Q1. PATH precedence for the shims: `~/.zshenv` prepend (new shells; restart sessions during the pause) vs
  wrappers in the nvm bin dir (immediate; re-asserted by `pdx setup`). Measure whether Claude Code's Bash
  snapshot keeps the session-start `PATH`.
- Q2. `taskpolicy -b` cost per class (I/O throttling): measure before choosing the default.
- Q3. Pressure thresholds (`max_page_rate`): measure on mlab with a known workload.
- Q4. The minimal mod rewrite (direct `node_modules/.bin/…` calls by agents): keep it in L1, or rely on shims only.

Survey sources: Getty/loadguard, leahneukirchen/nq, Nukesor/pueue, discoteq/flock, fl4p/macos-oom-guard, GNU make
job slots, vitest "improving performance", Turborepo `run`, pnpm recursive, Electron environment variables
(links in `research-intercept-tools.md`).
