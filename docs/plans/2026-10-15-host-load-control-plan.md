# Host load control — plan

Spec: `docs/specs/2026-10-15-host-load-control-spec.md` (user decisions D1–D6 final).
Coordinator: purdex-1f. Members: lease member `_51epu8` (G0, G1, L1–L3), A-line member `_bgz9kz` (G3, G2 — moved 10-15 so G2 runs beside G1).
Test tiers (CLAUDE.md 測試分層) apply to every task: affected packages only, `-race` only where concurrency
changed, `make lint`; the lead runs full suites at release. Every PR: codex R1 + R2 (attack → critic).

## G0 — measurements (no code)

Serial, one command at a time, host idle (all other lines paused). For `go test ./internal/store/`,
`go build ./...`, vitest full (`--maxWorkers=3`), `go test -p 3 ./internal/module/team/`: wall time and max RSS
with and without `taskpolicy -b`; 5 s samples of load1, `kern.memorystatus_vm_pressure_level`,
`kern.memorystatus_level`, pageouts/swapouts. Second batch (D7): `go test ./...` with `-p 8` + `nice 10` vs `-p 3` +
`taskpolicy -b`; vitest full with `--maxWorkers=8` + `nice` vs `--maxWorkers=3`. Output: defaults for
`max_page_rate`, `reserve_cores`, `mem_per_worker_mb` and the priority mode per class (spec Q2, Q3).

**Done 10-15** (results and the chosen defaults: spec §5.1). The two `go test ./...` runs were repeated on a quiet
host after the first pair overlapped other sessions' work; the vitest pair is kept as lightly disturbed.

## G1 — `pdx gate` core (two PRs: G1a execution core + module schema, G1b tuning surface)

New package `internal/gate` (pure + OS seams) and `cmd/pdx/gate_cmd.go`. Modules per spec §5 "Modules and
configuration" (D8): `intercept`, `slots`, `admission`, `parallelism`, `priority`, `lifetime`, `reap`, `cache` — one
file, one Config type and one table test each; `gate.Run` composes them. Defaults per D7 (`slots.heavy` 1, `light` 3,
`parallelism.mode` auto = cores − 2 bounded by memory, `priority.mode` nice 10).

- **G1a**: items 1–9 below, the module schema, config re-read at every gate start and every waiting poll, `auto`
  values computed per job, the source of each applied value kept in the holder record.
- **G1b**: `pdx gate set <module>.<key>=<value>` (atomic write), `[gate.profiles.<name>]` + `pdx gate profile use`,
  the decision log (JSON lines: class, slot, wait + reasons, parameters + sources, exit, duration), `pdx gate status`
  showing every module's effective values and sources.

1. `Classify(argv) Class` for `go`, `pnpm`, `npx`, `vitest`, `tsc` per spec §3 — table test (≥ 40 rows,
   incl. `go test ./pkg/` light, `go test ./...` heavy, `go version` none, `pnpm check` heavy, `pnpm install` none,
   `npx vitest run src/a.test.ts` light, `npx vitest run` heavy, `pnpm -r test` heavy).
2. `Inject(argv, env, cfg)`: `GOFLAGS -p=<go_p>` when absent; vitest `--maxWorkers=<n>` when absent (same rules as
   mod R7 `rewriteMaxWorkers`, which it replaces); pnpm `--workspace-concurrency=<n>` for recursive runs. `auto` width
   = cores − `reserve_cores`, lowered to fit available memory ÷ `mem_per_worker_mb`; table tests with injected host
   numbers.
3. Slots: `Acquire(class)` over `~/.config/pdx/gate/<class>-<k>.lock` with `syscall.Flock`; the lock fd is passed
   to the child (`ExtraFiles`) so the tree holds it; tickets in `queue/<class>/` (unix-ns + pid, each flocked by its
   waiter); admit only when live older tickets < free slots.
4. Nesting: `PDX_GATE_HELD=<class>-<k>:<token>`; pass through only when the token matches the slot's record, the
   slot's lock is held (non-blocking probe), and the caller descends from the holder pid (ppid walk); a light
   holder never covers an inner heavy command.
5. Pressure: `PressureReader` (level + pageout/swapout delta over 10 s); one sampler at a time via `pressure.lock`,
   `pressure.json` written temp+rename, readers accept ≤ 5 s old; no delta yet → level only; keep last-good, 3
   consecutive failures → level only → unreadable level → fail-open with a stderr line.
6. Disk floor for heavy: `statfs` of cwd and `$TMPDIR` vs `disk_min_gb`; refuse with the numbers.
7. Run: stdin not a TTY → new process group, forward SIGINT/SIGTERM/SIGHUP to the group, after exit SIGTERM the
   group then SIGKILL after 5 s; stdin a TTY → stay in the foreground group (as `cmd/pdx/lease_run.go:38-45,223-255`)
   and reap descendants by a ppid walk. Priority per the `priority` module (`nice` 10 by default; `background` =
   `taskpolicy -b`; `--fg` = none); exit with the child's code (128+n on signal).
8. Holder record JSON beside the lock (argv scrubbed, token); `pdx gate status [--json]` reads locks + records +
   tickets (no daemon) and flags "lock free, recorded pid alive".
9. `[gate]` config with one section per module; `enabled = false` (global or per module) → that part passes through;
   fail-open only on `EPERM`/`EACCES`/`EROFS` for the gate dir; every other error fail-closed with a message.
10. `cache` module: `go test -count=1` outside the release profile → warn (default) / strip / off.

Tests: real flock in temp dirs (two goroutines + two processes); fairness (an older ticket wins); dead waiter's
ticket ignored; nested pass-through with a valid token; forged value, stale token, foreign (non-descendant) caller
and light→heavy all take their own slot; group reap with a `sleep` tree (no survivor); SIGKILL of the gate keeps the
slot taken until the tree ends; TTY path does not setpgid (pty test or seam); signal forwarding; pressure 2 holds /
1 admits; concurrent samplers produce one complete `pressure.json`; fail-open only for the three errnos, fail-closed
for a file in place of the dir; disk floor refuses. Mutations listed in the brief must go red.

## G2 — interception (one PR)

1. Shims (`internal/gate/shim`): generated POSIX sh scripts `go`, `pnpm`, `npx`, `vitest`, `tsc` in
   `~/.config/pdx/shims/`, each `exec "$PDX_BIN" gate --auto --tool <name> -- "$@"`. The gate resolves the real binary
   **at every run** from the current `PATH` minus the shim dir, follows symlinks, and refuses a target inside the shim
   dir (no recursion; an `nvm use` is followed).
2. `pdx setup` writes the shims and the delimited `~/.zshenv` block, which prepends only when `:$PATH:` lacks the
   shim dir and prints nothing; `pdx setup --uninstall-gate` removes exactly the block (content before/after kept;
   duplicated or unclosed old blocks repaired). air26 the same.
3. `pdx lease run …` becomes a deprecated alias: maps `--kind test-full|build` → heavy, `test-pkg|lint-full` → light,
   ignores `--ask/--queue/--wait`, runs through the gate, prints one deprecation line.
4. Fast path: class `none` execs the real binary without loading config (hot path for `go env`, `pnpm install`).
5. Tests: setup idempotence on a temp `HOME`; resolution never points into the shim dir (also through a symlink
   alias), follows a changed `PATH` (two fake node dirs); a fresh `zsh -c 'command -v go'` resolves to the shim and
   a nested `zsh -c` does not grow `PATH` (skipped without zsh); uninstall keeps surrounding `~/.zshenv` content and
   repairs a duplicated block; the `.zshenv` block prints nothing; alias mapping table; `none` latency (< 20 ms for
   `go version` through the shim on mlab, measured once, not a unit test).

Deploy G1+G2 (+G3a if ready) as one release; then acceptance (spec §10). Other lines resume after it passes.

## G3 — daemon steady cost (three PRs, A-line member)

- G3a shared process snapshot: one snapshot per ≥ 2 s for the whole daemon, read by **every** reader:
  `agent.ReadProcessInfo`, the peers registry (`internal/peers/registry.go:84-101,153-176`), the resources sampler's
  two `procSnapshot` calls per tick (`internal/module/resources/sampler_loop.go:55-58,105-147`), `ProcessRoots`, team
  liveness. Tests: resources + peers + team inside one TTL window take one snapshot (counting seam); PID reuse told
  apart by start time; argv/exe/proxy classification unchanged (mutations for each).
- G3b team sweeper: dirty flag on writes, `ListOpen` at most every 5 s otherwise; liveness on the snapshot.
- G3c conversation WS: wake on feed change, 2 s fallback poll.
Before/after: 30 s pprof with the host idle (lead captures; target ≤ 10 % of one core).

## L1 — retire the lease in the mod (one PR, after G2 is deployed)

Delete `hooks/lease.js` except the minimal rewrite for direct `node_modules/.bin/{vitest,tsc}` calls by agents (kept,
spec Q4; it moves to a small `hooks/gate.js` that only prefixes `pdx gate --auto --`, no daemon call), `registerLease`
(register.js), `skills/pdx-lease/`, `lease.notice.v1` from `CAPS` (workbook.js), lease notice submission in events.js.
Every embed guard that names the lease changes with it (`cmd/pdx/plugin/embed_test.go:19-37` file list and
registration inventory, `:72-82` hand-off regex and scanner grammar, `:393-430` classic.Stop ownership and the
background-task test, `:587-605` `registerLease` scan, `:608-665` the two skill contracts); the scanner is narrowed to
the new `registerGate` hand-off. Protocol: bump `VERSION` (coordinate with RT-4 #2870 — whichever merges second takes
the next number) and update the exact assertions (`relay.test.ts:35-63`, `member.test.ts:707-710`); check the daemon's
numeric feature gates (`internal/team/wire_relay.go:317-320`) still enable half/handoff for the new number, and test a
mixed pair (old mod + new daemon, new mod + old daemon). Add `skills/pdx-gate/SKILL.md` (what is gated, how to read
`pdx gate status`, never bypass with `&`, `pdx gate -- <script>` for sequences).

Added 10-15 (spec §6, D12): `gate.js` also prefixes `vendor/bin/{phpunit,pest}` (and `./` forms), and moves a
foreground Bash call that would wait to `run_in_background: true` with a note (holder, time held, place, "starts by
itself, you will be told, do not rerun"); a call that can start at once is left in the foreground. Tests: busy →
background with the note; free → unchanged; a subagent's call the same; a rewrite that changes the permission verdict
is not made.

## L2 — rework the kept parts first (one PR; deployable while the lease tables still exist)

Zone memory input = the gate's pressure reader (shared code); `noteMinute` stops reading `resource_leases`
(`internal/module/resources/minute.go:41-62`): lease columns written 0, new `heavy_running`, `light_running`,
`gate_waiting` from slot records; the report handler stops reading `resource_leases`/`resource_positions`
(`report_handler.go:16-22,49-61,126-153`) and `pdx host report` replaces `pdx lease report` (the old verb prints a
pointer); hostconfig: split strict PUT from lenient stored-read in `internal/module/hostconfig/resources.go:30-44,71-75,125-148`
(update `resources_test.go:42-56`, add a stored-document-with-every-retired-key test and default GET fixtures);
`/api/resources` keeps `Snapshot.Sessions` — `cmd/pdx/team_cmd_resources_test.go` gains mutations (sessions removed
from the wire, attribution removed → red). The strategy report script reads the new columns.

## L3 — retire the lease in the daemon and CLI (one or two PRs; after L2 is deployed; deletions may exceed 800 lines with a file list)

Remove the files and routes in spec §6 "Remove"; `pdx lease` verbs except the `run` alias; `promptq.KindNotice`,
`CapLeaseNotice`, `modevents.CapLeaseNoticeV1` and their branches; team `LeadNoticer` if no other caller (verify
with grep in the PR; `PauseNotifyKey` stays — push uses it for pauses). Settings: drop the lease fields (the lenient
read from L2 keeps old documents valid). Store: migration step drops `resource_leases` and `resource_positions`
(nothing reads them since L2).

## D — docs and scripts (lead)

CLAUDE.md (測試分層: `pdx gate`, `-count=1` only at release), lead scripts (`pdx lease run` → `pdx gate`,
drop `-count=1` outside `test-at.sh`), memories, CHANGELOG. Aigora: their lead is told by the user (do not
message other leads).

## Order

G0 → G1 → G2 → release (G1, G2, G3a, G3b, G3c — G3b/G3c finished early and ride the first release) → acceptance → other lines resume → L1 → L2 (deploy) → L3. Each L phase is deployed before the next starts.

Load watcher (spec §12): W0 (threshold evaluation, lead, now) and, in parallel from now (A-line member), W1 detection
and record → W2 registration and delivery → W3 brief, skill and auto-start → W4 `pdx gate free`; W5 UI when the B line
resumes. The set goes live as a whole when W1–W4 are done and is tuned while it runs. W1–W5 do not depend on L1–L3;
each phase one PR; the W plan (tasks per phase) is reviewed by codex with §12 before W1 starts.

## W — load watcher (spec §12; A-line member; each phase one PR)

Cost rule for every W phase: no new fork per tick. The resources sampler already runs one `ps -axo pid,ppid,pcpu,rss`
per 5 s tick (`internal/resources/sample.go`); W reuses that list. Command lines are read only for a tree that is over a
threshold, through the shared process snapshot (`agent.SharedProcessSnapshot`, G3a). Pressure comes from the gate's
reader (`gate.NewHostReader`, the same `pressure.json` sample the gates share). Lead captures a 30 s idle pprof before
and after W1 (daemon target stays ≤ 10 % of one core).

### W1 — detection and record (daemon)

1. `internal/watch` (pure, table tests): `Detector.Pass(now, procs, managed, pressure, slots) []Event`.
   - Trees: from the sampler's `[]Proc` (pid, ppid, pcpu, rss), rooted at the child of a shell (`zsh`, `bash`, `sh`
     by comm) or at a session root (`resources.Root`); a tree containing a gate holder's gate pid or command pid
     (slot records, `gate.Pool.Held`) is **managed** and skipped for E1.
   - E1: per tree, a rolling window of `T` s; fires when mean cores ≥ `X` or RSS ≥ `Y` MB over the window. Signature =
     tool basename + subcommand/script word, paths and flag values dropped; first sighting per signature per host →
     `new: true`, later → counted in the record only.
   - E2: pressure level ≥ 2 for `T2`, or page rate > `R` for `T3` (from `gate.HostReader`).
   - E3/E4: from `gate.Pool.Held()`: `Orphaned` for ≥ 15 min (E3), held ≥ `lifetime.alert_after_min` (E4); once per
     holder (pid + start), not per pass.
   - Not events: trees under `X` and `Y` however long they live (D11).
2. Record: `~/.config/pdx/watch/events-YYYYMMDD.jsonl`, appended with `O_APPEND`, one JSON line per event (kind,
   host, at, signature, argv scrubbed by `internal/redact` then cut to 200 bytes, cwd, session id from
   `resources.Attribute`'s roots, peak cores and MB, duration, `new`); files older than 30 days removed at the first
   pass of a day.
3. Settings `[watch]` in `~/.config/pdx/config.toml`, read at every pass like the gate's (no restart):
   `x_cores` (1.5), `y_mb` (1024), `t_s` (60), `pressure_t_s` (120), `page_rate` (50), `page_t_s` (60) — first values,
   tuned from W0 and the live record.
4. Wire into `internal/module/resources/sampler_loop.go` after the sample (one call per tick; a detector error is
   logged, never stops the sampler).
5. `pdx watch log [--since D] [--kind K] [--json]`: reads the local record files directly (works with the daemon
   down).
Tests: synthetic proc tables (a tree over/under each threshold; managed vs unmanaged; a tree that crosses the window
boundary; signature normalization incl. `composer test:integration`, `vendor/bin/phpunit --testsuite integration`,
`pnpm exec vitest run`); record rotation with an injected clock; mutation on each threshold comparison.

### W2 — registration and delivery

1. Daemon: `POST /api/watch/register {ref}`, `DELETE /api/watch/register`, `GET /api/watch` (current watcher,
   registered at, by host); stored in a small table (deployed-schema rule: additive migration).
2. Propagation: a registration is pushed to every paired host through the existing paired-host API (the same channel
   the peer bridge uses); each host keeps the latest by registration time; the replaced watcher gets one message.
3. Liveness: the registration ends when the registry no longer has the ref's session (relay lineage keeps it).
4. Delivery: per host, events go to the watcher with the daemon's own peer send (the path `pdx msg` uses);
   folding: one message per kind per host per 10 min, later ones counted into the next; E3 never folded; a send that
   fails is retried on the next pass, kept in the record either way.
5. CLI: `pdx watch register` (the calling session's ref), `pdx watch status`, `pdx watch unregister`,
   `pdx watch log --host <alias>|--all` (through each host's `GET /api/watch/log`).
Tests: register/replace/expire; fold timing with an injected clock; a send failure is retried; cross-host with two
daemons in a test harness (as the peer bridge tests do).

### W3 — the watcher session and auto-start

1. `cmd/pdx/plugin/purdex/skills/pdx-watch/SKILL.md` and the built-in brief: the events, what the watcher may and must
   not do (spec §12.3), how to read `pdx watch log` and `pdx gate status`, and to register itself first.
2. Profile Sync keys `watch.auto_start` (true), `watch.model` (opus), `watch.home` (the host alias, default mlab);
   read through the existing profile settings path.
3. Auto-start: on E1/E2/E3 with no live watcher, the daemon of `watch.home` (an event elsewhere asks it through the
   paired-host API) spawns a session with the brief and model (the spawn path `pdx spawn` uses, without a team), at
   most one start per 30 min.
Tests: no watcher + event → one spawn; a watcher present → none; two events within 30 min → one spawn; settings read
from the profile.

### W4 — `pdx gate free <class>-<k>`

Lists the holders (`lsof` on the slot's lock: pid, command scrubbed, cwd, start, session), refuses a slot whose gate is
alive unless `--force`, asks for `y` (or `--yes`), SIGTERM, 5 s, SIGKILL, then reports whether the slot is free.
Tests: a held slot with a live gate is refused; an orphaned holder (a `sleep` keeping fd 3) is ended and the slot is
free; `--yes` skips the question; the decision record gets one line.

### W5 — UI (B line, when it resumes)

Sidebar icon at the right of the host list's top row (none / active / unhandled count), the watcher area as its own
tab region (watcher conversation, event log of all hosts with host/kind filters, "start the watcher"). Spec and plan
by the B line from spec §12.5.
