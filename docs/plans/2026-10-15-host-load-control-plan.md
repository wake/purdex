# Host load control — plan

Spec: `docs/specs/2026-10-15-host-load-control-spec.md` (user decisions D1–D6 final).
Coordinator: purdex-1f. Members: lease member `_51epu8` (G0, G1, G2, L1–L3), A-line member `_bgz9kz` (G3).
Test tiers (CLAUDE.md 測試分層) apply to every task: affected packages only, `-race` only where concurrency
changed, `make lint`; the lead runs full suites at release. Every PR: codex R1 + R2 (attack → critic).

## G0 — measurements (no code)

Serial, one command at a time, host idle (all other lines paused). For `go test ./internal/store/`,
`go build ./...`, vitest full (`--maxWorkers=3`), `go test -p 3 ./internal/module/team/`: wall time and max RSS
with and without `taskpolicy -b`; 5 s samples of load1, `kern.memorystatus_vm_pressure_level`,
`kern.memorystatus_level`, pageouts/swapouts. Output: defaults for `max_page_rate` and per-class QoS (spec Q2, Q3).

## G1 — `pdx gate` core (one PR)

New package `internal/gate` (pure + OS seams) and `cmd/pdx/gate_cmd.go`.

1. `Classify(argv) Class` for `go`, `pnpm`, `npx`, `vitest`, `tsc` per spec §3 — table test (≥ 40 rows,
   incl. `go test ./pkg/` light, `go test ./...` heavy, `go version` none, `pnpm check` heavy, `pnpm install` none,
   `npx vitest run src/a.test.ts` light, `npx vitest run` heavy, `pnpm -r test` heavy).
2. `Inject(argv, env, cfg)`: `GOFLAGS -p=<go_p>` when absent; vitest `--maxWorkers=<n>` when absent (same rules as
   mod R7 `rewriteMaxWorkers`, which it replaces); pnpm `--workspace-concurrency=<n>` for recursive runs.
3. Slots: `Acquire(class)` over `~/.config/pdx/gate/<class>-<k>.lock` with `syscall.Flock`; tickets in
   `queue/<class>/` (unix-ns + pid, each flocked by its waiter); admit only when live older tickets < free slots.
4. Pressure: `PressureReader` interface (`sysctl` level + pageout/swapout delta over 10 s; shared cache file
   `pressure.json`, ≤ 5 s old); admission requires level 1 and rate < `max_page_rate`; unreadable → admit.
5. Run: child in a new process group (`SysProcAttr{Setpgid: true}`), env `PDX_GATE_HELD`, `taskpolicy -b` unless
   `--fg` or the class default says otherwise (G0); forward SIGINT/SIGTERM/SIGHUP to the group; after exit,
   SIGTERM the group, SIGKILL after 5 s; release; exit with the child's code (128+n on signal).
6. Holder record JSON beside the lock (argv scrubbed); `pdx gate status [--json]` reads locks + records + tickets
   (no daemon).
7. `[gate]` config section; `enabled = false` → pass-through; fail-open when the dir cannot be opened.

Tests: real flock in temp dirs (two goroutines + two processes), fairness (an older ticket wins), dead waiter's
ticket ignored, nested pass-through, group reap with a `sleep` tree (no survivor after reap), signal forwarding,
pressure 2 holds / 1 admits, fail-open on an unreadable dir. Mutations listed in the brief must go red.

## G2 — interception (one PR)

1. Shims (`internal/gate/shim`): generated POSIX sh scripts `go`, `pnpm`, `npx`, `vitest`, `tsc` in
   `~/.config/pdx/shims/`, each `exec "$PDX_BIN" gate --auto --real "<resolved real path>" -- "$@"`; the real path is
   resolved at setup time **excluding the shim dir** (no recursion) and re-resolved when missing.
2. `pdx setup` writes the shims and the delimited `~/.zshenv` block (`export PATH="$HOME/.config/pdx/shims:$PATH"`,
   guarded so it is added once per shell), idempotent; `pdx setup --uninstall-gate` removes both. air26 the same.
3. `pdx lease run …` becomes a deprecated alias: maps `--kind test-full|build` → heavy, `test-pkg|lint-full` → light,
   ignores `--ask/--queue/--wait`, runs through the gate, prints one deprecation line.
4. Fast path: class `none` execs the real binary without loading config (hot path for `go env`, `pnpm install`).
5. Tests: setup idempotence on a temp `HOME`; shim resolution never points into the shim dir; a fresh
   `zsh -c 'command -v go'` resolves to the shim (skipped without zsh); alias mapping table; `none` latency
   (< 20 ms for `go version` through the shim on mlab, measured once, not a unit test).

Deploy G1+G2 (+G3a if ready) as one release; then acceptance (spec §10). Other lines resume after it passes.

## G3 — daemon steady cost (three PRs, A-line member)

- G3a shared process snapshot: one snapshot per ≥ 2 s for the daemon; `agent.ReadProcessInfo` and the peers
  registry read it; counting seam test (N entries → one snapshot).
- G3b team sweeper: dirty flag on writes, `ListOpen` at most every 5 s otherwise; liveness on the snapshot.
- G3c conversation WS: wake on feed change, 2 s fallback poll.
Before/after: 30 s pprof with the host idle (lead captures; target ≤ 10 % of one core).

## L1 — retire the lease in the mod (one PR, after G2 is deployed)

Delete `hooks/lease.js`, `registerLease` (register.js), `skills/pdx-lease/`, `lease.notice.v1` from `CAPS`
(workbook.js), lease notice submission in events.js; keep only the minimal rewrite for direct
`node_modules/.bin/{vitest,tsc}` calls by agents (spec Q4) — or drop it if G0/G2 show shims suffice (decide in
review). Update `embed_test.go` guards (file list, `registerLease` scan, the three lease tests). Bump `VERSION`
(coordinate with RT-4 #2870 — whichever merges second takes the next number). Add `skills/pdx-gate/SKILL.md`
(short: what is gated, how to read `pdx gate status`, never bypass with `&`, `pdx gate -- <script>` for sequences).

## L2 — retire the lease in the daemon and CLI (one or two PRs; deletions may exceed 800 lines with a file list)

Remove the files and routes in spec §6 "Remove"; `pdx lease` verbs except the `run` alias; `promptq.KindNotice`,
`CapLeaseNotice`, `modevents.CapLeaseNoticeV1` and their branches; team `LeadNoticer` if no other caller (verify
with grep in the PR; `PauseNotifyKey` stays — push uses it for pauses). Settings: drop the lease fields; unknown keys
ignored on read, dropped on write (hostconfig test with a stored v2 document). Store: migration step drops
`resource_leases` and `resource_positions`. `team_show_cmd.go` keeps reading session attribution.

## L3 — rework the kept parts (one PR)

Zone memory input = gate's pressure reader (shared code); `host_minutes`: lease columns written 0, new
`heavy_running`, `light_running`, `gate_waiting` from slot records; disk guard checked by the gate before a heavy
slot and by the sampler for the alert; `pdx host report` replaces `pdx lease report`; the strategy report script
(`test-strategy-report.sh`) reads the new columns.

## D — docs and scripts (lead)

CLAUDE.md (測試分層: `pdx gate`, `-count=1` only at release), lead scripts (`pdx lease run` → `pdx gate`,
drop `-count=1` outside `test-at.sh`), memories, CHANGELOG. Aigora: their lead is told by the user (do not
message other leads).

## Order

G0 → G1 → G2 → release (G1, G2, G3a) → acceptance → other lines resume → L1 → L2 → L3 (G3b/G3c as they finish).
