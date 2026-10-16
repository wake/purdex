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

## W — load watcher (spec §12; A-line member; one PR per row below, ≤ 800 lines / 20 files each)

Revised 10-15 after the A-line member's code reading (13 points) and codex plan review `task-mv9jbifb-t4u53r`
(22 findings; lead's `reports/codex-watch-plan-review.md`).

**Cost rule (every W PR).** No fork is added to the daemon's steady state. The resources sampler already forks one
`vm_stat` and one `ps` per 5 s tick (`internal/resources/sample.go`); W extends those two outputs instead of reading
anything else. Command lines are read only for a tree already over a threshold (`agent.SharedProcessSnapshot` +
`ReadFresh`, a handful of pids). The gate's `HostReader` is **not** used by the daemon (it forks its own `vm_stat` and
can wait up to 4 s on `pressure.lock`). Proof per PR: an integration test with the `runCmd` fork counter
(`sample.go`) over a table with many trees and no tree over threshold → exactly the sampler's two forks per tick and no
argv read; and, lead-run before and after W1b, three 30 s idle pprof captures (host quiet, no session busy), CPU =
mean of the three, target unchanged (daemon ≤ 10 % of one core).

**Settings**: the `watch` row in each daemon's host config, read at every pass, per host (no cross-host sync now),
keys and defaults in spec §12.2 (thresholds, `targets`, `mode`, `patrol_idle_min`, `auto_wake`, `model`, `workdir`,
`start_prompt`, `wake_prompt`). W1b-3 adds the row with `enabled` and the thresholds; W2a adds the rest. The
thresholds are first values; W0 and the live record tune them (spec §12.7).

### W1a — plumbing (no behaviour change)

1. `ps` gets a fifth column `ucomm` in the same fork (`ps -axo pid=,ppid=,pcpu=,rss=,ucomm=`; `ucomm` may contain
   spaces: the parser takes the rest of the line); `Proc` gains `Comm`; `parsePSFields` and its tests.
2. `vm_stat` parsing keeps `Pageouts` and `Swapouts` in `HostRaw` (today they are dropped).
3. `gate.ClassifyReason(argv, Env) (Class, Reason)`: `Reason` ∈ {`heavy`, `light`, `packaging`, `watch_mode`,
   `known_passthrough`, `unknown`}; `Classify` keeps its signature and calls it. Table tests for every reason.
4. The gate's argv scrubber (`internal/gate/scrub.go`) moves to `internal/redact` as `Argv(argv, max)` (gate keeps
   120, watch uses 200); its tests move with it.

### W1b — detector and record

1. `internal/watch` (pure, injected clock): `Detector.Pass(now, procs, raw, slots, roots) []Event`, called on
   **every** sampler tick (5 s; spec §12.1 updated to the tick).
   - Tree root = the child of a shell (`Comm` ∈ zsh, bash, sh, fish) or of `tmux`/`launchd` when there is no shell;
     session roots (`resources.Root`) are used only to attribute a tree to a session (ppid walk + the root's start
     time against `SharedProcessSnapshot`, so a reused pid is not attributed), never as tree roots.
   - Managed = the tree has a `pdx gate` ancestor (`Comm` pdx + argv check for the few candidates) **or** contains a
     slot holder's gate pid or command pid (`gate.Pool.Held` for heavy and light, pools built from the live gate
     config each pass). Managed trees are skipped for E1.
   - E1 episode: a tree whose mean cores over the last `t_s` ≥ `x_cores` or RSS ≥ `y_mb` for `t_s` → one event when
     it crosses, none while it stays over; re-armed when the tree ends or stays under for `t_s`. Label from
     `ClassifyReason` on the root's argv: `gate_gap` (heavy/light outside the gate), `unknown`, `allowed` (packaging
     or a known pass-through); all three are recorded and delivered with the label.
   - Signature = tool basename + subcommand/script word (paths, values dropped). `new` = first sighting of the
     signature on this host, from a persisted set (`~/.config/pdx/watch/state.json`: signatures with first/last
     seen and count; orphan first-seen times; written atomically, read at start).
   - E2: pressure level (`raw.Pressure`) ≥ 2 for `pressure_t_s`, or page rate (tick-to-tick delta of
     pageouts+swapouts) > `page_rate` for `page_t_s`; episode semantics as E1.
   - E3: a slot `Orphaned` for ≥ 15 min (first-seen time persisted), with the slot name and the holder pids from
     `lsof` on the slot lock (run only for an orphaned slot, at most once per pass); E4: a slot held ≥
     `lifetime.alert_after_min`; both once per holder (pid + start).
   - Not events: trees under the thresholds however long they live (D11).
2. Record: `~/.config/pdx/watch/events-YYYYMMDD.jsonl`, `O_APPEND`, one line per event: `id` (host + ulid), kind,
   label, host, at, signature, argv (`redact.Argv`, 200), cwd, session, peak cores and MB, duration, `new`, slot and
   holders for E3/E4, `handled` false. Files older than 30 days removed at the first pass of a day.
3. Wiring: `sampler_loop.go` calls the detector after a successful sample, **independent of the resources lease
   mode** (with mode `off` the tick still samples when `watch.enabled`); a failed sample skips the pass; a detector,
   record or state I/O error is logged and never stops the sampler, the publish or the lease.
4. `pdx watch log [--since D] [--kind K] [--json]` reads the local files directly (daemon down is fine).
Tests: synthetic tables (over/under each threshold; managed by ancestor vs by holder; episode crossing, staying,
re-arming; signature normalization incl. `composer test:integration`, `vendor/bin/phpunit --testsuite integration`,
`pnpm exec vitest run`, `pnpm run electron:build` → `allowed`; session attribution with a reused pid); restart keeps
`new` and orphan times; rotation with an injected clock; failure seams (state write, record write, lsof) leave the
sampler's publish intact; the fork-counter test above; mutation on every threshold comparison.

### W2a — where the watcher is (settings, holder, discovery)

1. Settings: the rest of the `watch` row (spec §12.2) with validation; `pdx watch config [get|set <key>=<value>]`
   through the daemon API (the App's settings page uses the same API in W5).
2. Holder state on each host (`~/.config/pdx/watch/holder.json`, atomic): this host's watcher session id, awake or
   resting, last turn and last delivery times. `GET /api/peers/watch/holder` (added to `peers.HostRoutePolicy`)
   answers "a watcher awake here / resting here (resumable) / none"; an older peer's 404 = "no watch support".
3. Discovery and cache on every host: the remembered watcher (host + session) is used until a delivery fails or it
   is gone; then (1) awake on a host in `targets` → use it; (2) the last watcher's host reachable → ask it to wake it
   (`POST /api/peers/watch/wake`); (3) ask the first reachable host in `targets` to start one
   (`POST /api/peers/watch/start`). (2) and (3) only with `auto_wake`. Calls with `HostCaller` (`to_host_id`).
4. Daemons compare `targets` (through `holder`'s answer) and log a warning when they differ.
5. CLI: `pdx watch status` (this host's view: remembered watcher, holder state, targets).
Tests: two daemons in the peer-bridge harness — awake elsewhere is used; resting is woken on its host; none → start
on the first reachable target; first target down → the second; the cache is not re-queried while deliveries
succeed; an old peer; mismatched targets warn.

### W2b — delivery, backlog, handled

1. Delivery: each host sends its events to the watcher with `peers.Sender` (the `/api/peers/send` path `pdx msg`
   uses; across hosts by the peer address), `[負載守門] …` text with the event id. Folding per (host, kind) per 10
   min, later events counted into the next message; E3 never folded; a failed send triggers discovery (W2a) and is
   retried from a persisted pending queue, without counting twice.
2. Backlog: when a watcher is woken or started, each host sends one summary of its unhandled events since its last
   delivered id (counts by kind and label, the newest five, and `pdx watch log --all --unhandled`).
3. Handled: `pdx watch ack <id>… | --all [--host] --note "<one line>"` (note required, one line, ≤ 500 bytes) marks
   events handled (a small ack log beside the records: id, status, note, at); `GET /api/watch/unhandled` → `{count}`
   for this host (W5's badge).
4. `GET /api/peers/watch/log` (policy entry) and `pdx watch log --host <alias> | --all`.
5. For the watcher area (B line spec `docs/specs/2026-10-16-watcher-area-spec.md` §8 A3; admin token; the writes 403
   to device tokens and not in `HostRoutePolicy`): `GET /api/watch/events?since=&limit=` → `{events}` newest first
   with the fields that spec lists (the record's fields plus `status` `pending|handled`, `note`, `handled_at`); the
   `watch.events` push (spec §12.6: `{host_id, unhandled, cause}`, one per record / ack operation). No skip route
   (user decision 10-16: no "skip all" button).
Tests: folding isolation per host and kind; E3 not folded; retry without double counting; restart with a pending
queue; backlog on wake/start (and none twice); ack without a note refused, ack and the count, the note in
`/api/watch/events`; one `watch.events` per operation (ack of three
ids → one push), none for a refused ack; device token → 403 on the writes.

### W3a — the `pdx-watch` tmux session: registration, reconcile, control helpers (daemon)

Spec §12.3–§12.4 (user decisions 10-16: one fixed tmux session `pdx-watch`, active and passive start, `agent_command`).
Replaces the earlier "system spawn" design: no team plumbing. W3a starts and wakes nothing: it is the part that knows
which session is the watcher and can type into its pane safely; W3b uses it. It is deployable alone (the manual
watcher of 10-16 is registered by it and events reach it through W2).

0. Owner answer carries the process (agent module, additive): `PaneOwner` (`internal/module/agent/pane_owner.go`)
   gains `PID` and `ProcessStartTime`, copied in `resolvePaneOwners` from the frame (`store.Frame` already has both);
   no existing caller reads them. Test: the owner answer of a live root frame carries the frame's pid and start time.
1. Reconcile (spec §12.4): `reconcile(ctx)` in the watch module, the only writer of `holder.json`'s state. It resolves
   the code of the tmux session `pdx-watch` and asks the agent module's `OwnerResolver` (`agent.owner-resolver`) for
   its root owner; a watcher is an owner with `AgentType == "cc"` whose `TmuxPaneID` is the session's first pane. The
   holder gains the frame id, the pane id, the `$N` session id, the owner's `PID` and `ProcessStartTime` (step 0), and
   the transcript path; `since` is kept for the same frame and set now for a new one. Owner gone while `awake` →
   `resting`, session id and transcript path kept. A lookup error changes nothing. Triggers (spec §12.4): the agent
   module's existing `TerminalSessions.SubscribeSessionStart` (verified top-level runs; filtered on `TmuxSession ==
   "pdx-watch"`, and an `Overflow` event reconciles too), the module's `Start`, a holder read (peer route, status)
   when the last reconcile is older than 30 s, a failed delivery to the watcher, each W3b step and every 10 s while it
   waits, and a 5 min ticker. No SessionEnd subscription and no mod change.
2. Registration = reconcile finding a new frame: announce to the other hosts (`POST /api/peers/watch/announce`
   carrying the holder answer; added to `peers.HostRoutePolicy`, an older peer's 404 ignored), then apply the "two awake" rule (spec §12.3) against the answers of the other targets:
   when the other host's watcher wins, deliver the stand-down notice to this host's watcher through `peers.Sender`.
   The holder answer (`HolderAnswer`, the wire of `/api/peers/watch/holder`) gains `since`; the locator's step (1)
   picks among several awake answers by the same rule instead of by `targets` order.
3. Pane helpers, through the person's tmux server (no `-L`), every tmux call through `execstat` with a tag:
   `ensureSession(workdir)` (create `pdx-watch` detached in `workdir` when missing; record `$N` and the first pane's
   `%N`); `paneAtShell()` (one `display-message -p` for `pane_current_command`, `pane_in_mode` and the ids: a shell
   command, not in a mode, ids equal to the recorded ones; different ids → look the session up again first; used for
   the early "is it busy" answer, not as the guard of the typing);
   `commandLine(agentCommand, resumeID, prompt)` (spec §12.4: `--resume` only with a UUID, the prompt as one POSIX
   single-quoted word; refuses control characters); `typeLine(line)` (spec §12.4: `load-buffer -b pdx-watch-line -`
   with the line on stdin, then one `if-shell -F -t %N '<shell and not in mode>' 'paste-buffer -d -p -b
   pdx-watch-line -t %N ; send-keys -t %N Enter'`, the command strings fixed text; afterwards the buffer still
   existing = the condition was false → `delete-buffer`, `pane_busy`); `endAgent(holder)` (reconcile; same frame →
   `send-keys -l -t %N /exit` and Enter; W3b's grace and signals use the holder's pid + start time, re-checked before
   each signal).
4. `watch` row: `agent_command` replaces `model` (spec §12.2) in `watchsettings.Settings`, its `Default()` and the
   normaliser in `hostconfig/watch.go` (the only consumers today: `grep -rn '\.Model\b'` shows `hostconfig/watch.go`
   alone). Validation: one line, ≤ 512 bytes, no control characters; `start_prompt`/`wake_prompt` ≤ 4 KB, no control
   characters. A stored or PUT body with `model` and no `agent_command` → `claude --dangerously-skip-permissions
   --model <model>`; both → 400. GET always answers `agent_command`. `pdx watch config` shows and sets
   `agent_command`.
5. Status (the watcher area's §8 A3, B line; spec §12.6): `/api/watch/status` adds the watcher events go to (host id,
   alias, session id, state, `tmux_session: "pdx-watch"`) or none, and this host's holder with the pane ids;
   `watch.changed` on the event stream with the same fields, emitted when one of them changes (compared with the last
   emitted value, so a reconcile that changes nothing, or only `last_turn`/`last_delivery`, emits nothing).
Tests (tmux under `-L <label>` with `unset TMUX`, never the default server; the owner resolver faked at its
interface, plus one test with the real agent module registered in core):
- registration: a cc root owner of `pdx-watch`'s first pane registers; a request naming `pdx-watch` from a subagent,
  a codex agent, a second pane, or with a session id that is not the owner's registers nothing; a late SessionStart
  of an earlier run does not replace the current frame; a lookup error leaves the holder as it was.
- backfill: a watcher already running when the module starts (the 10-16 manual one) is registered by `Start`'s
  reconcile; an agent that ended while the daemon was down reads as `resting` with its id; an agent that ends while
  the daemon runs reads as `resting` at the next holder read past 30 s and after a failed delivery (no SessionEnd);
  an `Overflow` SessionStart event reconciles.
- two awake: simultaneous registrations on two daemons (peer-bridge harness) → both locators pick the same, exactly
  one stand-down notice, to the loser's own watcher; `targets` in opposite orders → the same pick; the announce
  arriving after the other host's holder answer → the same pick; equal `since` → smaller host id.
- typing: `commandLine` with `'`, `"`, `$(…)`, backticks, `;`, `&&` in the prompt → the shell (a real `sh -c 'printf
  %s …'` round trip) gets the prompt back byte for byte; a newline, NUL or ESC → refused; `--resume` with a non-UUID →
  refused; a pane running a program (`sleep`) → `pane_busy`, nothing typed, the buffer deleted; the pane turning busy
  after `paneAtShell` said shell (start `sleep` between the two calls) → still nothing typed, because the guard is the
  `if-shell -F` of `typeLine`; a recreated `pdx-watch` (new `$N`/`%N`) → looked up again, the old pane id never used.
- settings: a 741–744 row with `model` → GET answers `agent_command`, GET → PUT unchanged round trips; PUT with
  `model` only → stored as `agent_command`; PUT with both → 400; restart reads the stored row; over-long or multi-line
  values refused.
- status and event: fields present for awake, resting and none; `watch.changed` once per change, none for a no-op
  reconcile or a `last_turn` update; mutation on the "two awake" comparison and on the shell check.

### W3b — the watcher on its host: start, wake, rest, prompts, slot counts

1. Start. Active (`POST /api/watch/start`, `pdx watch start`, the App's button; admin token, device tokens 403, not in
   `HostRoutePolicy`): the W2a discovery with `auto_wake` taken as on — an awake watcher → 200 with it; else wake the
   last one or start on the first reachable target (spec §12.3) → 202 `{host}` (the chosen host's alias and id), or
   409 `{cooldown_until}` while the chosen host's cooldown runs. Passive (`/api/peers/watch/start`, or a local event
   with nobody awake and `auto_wake`). On the host that starts it: W3a reconcile → verified watcher there: success, nothing typed → `ensureSession` → `paneAtShell` (else
   `pane_busy`, reported) → announce (`/api/peers/watch/announce`; simultaneous announcements → the smaller host id
   starts, the other stands down) → `typeLine(commandLine(agent_command, "", start_prompt))` → success when reconcile
   registers a new frame within 3 min; none → failed, retried after a 30 min cooldown (persisted). One start or wake
   at a time on this host (a mutex plus the persisted in-progress mark, so a restart mid-start does not start twice).
2. Wake (`POST /api/watch/wake {prompt}`, `/api/peers/watch/wake` or a local event): reconcile → holder `resting`, no
   owner, pane at a shell, the kept transcript on disk → `typeLine(commandLine(agent_command, <kept id>, wake_prompt
   followed by the request's prompt, joined by a space; the whole ≤ 4 KB, no control characters, else 400))`; success = reconcile registers that same session id within 3 min. No kept id or no transcript →
   start (1). An awake watcher → nothing to do.
3. Rest (mode `patrol`): idle = no delivery and no turn of the watcher for `patrol_idle_min` → send "time to rest" →
   on the watcher's answer, or after 5 min, W3a `endAgent` (`/exit` to the same frame only) → 5 min later, the same
   frame still there → SIGTERM to its pid after re-checking the start time, SIGKILL 10 s later on the same check. The
   holder turns `resting` by reconcile (every 10 s while the rest step waits); the session id is kept; the tmux
   session stays. `resident`: no countdown.
4. Built-in prompts and the skill `skills/pdx-watch/SKILL.md` (spec §12.4): pull the `purdex-watch` branch in
   `workdir` at start and wake, commit and push after notes change, read the backlog, what each event and label means,
   what it may and must not do, `pdx watch ack`, the rest handshake, the stand-down notice. `start_prompt` /
   `wake_prompt` in the settings replace the built-ins when set.
5. Slot counts (spec §12.4 "Slot counts", user decision 10-16): the skill's procedure (read `pdx gate status --json`,
   `pdx gate set --expect <read value> slots.<class>=<n>` here or `ssh <alias> pdx gate set …` there, read back, tell
   the person host, class, old → new and why; no bounds or rate limit beyond the gate's range 0–64, user decision
   10-16). The CLI side
   needs one addition, made here: `pdx gate set` gains `--expect <old>` (the write is refused when the current value
   differs, so two writers cannot undo each other unseen) and prints the value read back. The other host and the
   message are the watcher's behaviour (the skill), checked in the live acceptance below.
6. Lead sets up, once: the `purdex-watch` branch of the mlab repo (from its main, notes under `watch/`) and the
   `workdir` checkout `~/Workspace/wake/mlab-purdex-watch` on a26 and mlab (a separate clone), and trusts that folder
   in Claude Code once on each host (the first-run "trust this folder?" question would stop a daemon-started watcher).
   Done on mlab 10-16 (branch pushed, f4aab9a; the manual watcher runs there); a26 still to do.
Tests: start on an event with nobody awake; start with a verified watcher already there → nothing typed; start with a
busy pane → `pane_busy`, nothing typed; start that never registers → failed and cooldown, a restart mid-start does
not start twice; two hosts announce at once → one start; wake a resting one → the same id registers; wake with the
transcript gone → a start; patrol rest after the idle time (answer / no answer), `/exit` only to the same frame, no
signal to a pid whose start time changed (pid reused), SIGKILL only after SIGTERM's 10 s; resident never rests;
custom prompts used; `pdx gate set --expect`: a raise and a lower each succeed with the right old value and print the
read-back, a changed value is refused and leaves the file as it was.
Live acceptance (after deploy, mlab, with the person): the watcher is asked to raise `slots.heavy` 1 → 2, then to
lower it back; each time its message names the host, the class, old → new and why, and `pdx gate status --json`
shows the new value; asked to pause light, it sets `slots.light=0`, a `vitest related` then waits with the reason
`paused`, and it raises light again when asked; the person changes `slots.heavy` by hand between the watcher's read
and its write → its `--expect` write is refused and it reads again instead of overwriting; the same raise on a26 by
`ssh air26 pdx gate set …` from mlab.

### W3c — the gate: a slot count of 0 pauses the class (lease member, one PR, before W3b)

User decision 10-16 (spec §5 `slots`): `slots.heavy` / `slots.light` accept 0. Today
`parseSlots` reads them with `r.Int(…, 1, 64)` and a 0 is refused (back to the default), so the watcher must not set
0 until this ships.
1. `parseSlots` takes 0..64; `pdx gate set slots.<class>=0` and profiles accept it.
2. A class at 0: a new command of it waits (never starts, never fails), its wait reason is `paused` (in the waiter's
   line, `pdx gate status`, `decisions.jsonl` `wait_reasons`, and the mod's would-wait text), and it starts on the
   first poll after the count is raised; running commands and the other class are untouched. `pdx gate status
   [--json]` shows the class as paused (`"paused": true` per class in the JSON, for the fleet's queue view).
3. Late claims (#2996b) and overflow slots of a paused class: none are taken while it is 0.
Tests: 0 accepted by parse, set and a profile; a heavy command at `slots.heavy=0` waits with `paused` and starts after
`set slots.heavy=1`; light unaffected while heavy is paused; a running command is not touched by the change to 0;
status text and JSON; no late claim while paused; mutation on the 0 comparison.

### W4 — `pdx gate free <class>-<k>`

Lists the holders (`lsof` on the slot lock: pid, command via `redact.Argv`, cwd, start, session), refuses a slot
whose gate is alive unless `--force`, asks for `y` (anything else aborts; `--yes` skips), SIGTERM, then SIGKILL only
for those still alive after 5 s, then reports whether the slot is free (another holder left → "still held by …").
Tests: live gate refused / `--force`; answer not `y`; SIGTERM enough → no SIGKILL; SIGKILL only after 5 s; still held
by another holder; scrubbed output.

### W5 — UI (B line, when it resumes)

Spec §12.6 with the W2/W3 API: `/api/watch/status` (the watcher events go to: host id, alias, session id, state,
`pdx-watch`; or none) and `watch.changed` (W3a.5) to bind the watcher tab to the holder host's `pdx-watch` and rebind
it on a failover; `POST /api/watch/start` and `/api/watch/wake` (W3b); `/api/watch/unhandled`, `/api/watch/events`,
and the `watch.events` push (W2b.5); the `watch` settings API incl. `agent_command` and the two prompts. The B line writes its own spec section and
plan (`docs/specs/2026-10-16-watcher-area-spec.md`), and owns its tests: the tab follows a failover, "resting" and
"none" shown apart.

Order: W1a → W1b → W2a → W2b → W3a → W3c → W3b → W4 (W4 only needs W1a's `redact.Argv` and may go earlier; W3c is
the lease member's, independent of W3a, and only has to land before W3b's skill lets the watcher set 0). W3a can be
deployed alone (it registers the manual watcher; W2 delivers to it); start, wake and rest arrive with W3b. The set
goes live as a whole when W1b–W4 are merged and deployed; W5 follows.
