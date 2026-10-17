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

1. Delivery: the resources module hands each recorded event to the watch module's queue (`watch.EventSink`, found in the
   registry). A message is `[負載守門] …` text with the event id. It is sent to the watcher with the daemon-to-daemon route
   `POST /api/peers/watch/event` (`HostCaller`, in `HostRoutePolicy`, `HostLimiter` and a body cap like the other watch
   routes), not with `peers.Sender`: `/api/peers/send` needs a live session on this host as the origin, and a daemon has
   none. The daemon of the watcher's host notifies its own watcher with `SendSelfNotice` from the watcher's own inbox (the
   stand-down notice's path); a watcher on the sending host takes the same local function. Folding per (host, kind) per 10
   min, later events counted into one message when the window ends; E3 never folded. The queue is a file
   (`outbox.json`): a message is frozen when made, a retry sends the same text (30 s, doubling, up to 5 min) and nothing is
   counted twice; the sender removes it only on a clear success. The receiver keeps the delivered event ids
   (`delivered.json`, the latest 2000), so a retry or a lost reply never gives the watcher an event twice. A failed send
   makes the locator look again (W2a); a failed notify on the receiving host reconciles its holder (W3a-2b).
2. Backlog: when a watcher is woken or started, each host sends one summary of its unhandled events since its last
   delivered id (counts by kind and label, the newest five, and `pdx watch log --all --unhandled`).
3. Handled: `pdx watch ack <id>… | --all [--host <alias>] --note "<one line>"` (note required, one line, ≤ 500 bytes) marks events
   handled in the ack log beside the records (`acks.jsonl`: id, note, time, who gave the order, from which host; the first ack of an
   event stands, a batch is written whole or not at all, a damaged file is kept as `acks.jsonl.bad-<time>`); `GET /api/watch/unhandled`
   → `{count}` for this host (W5's badge). Split in two PRs: **W2b-2a** this host's part, **W2b-2b** the rest.
4. Other hosts (W2b-2b; spec §12.3 "Acking events of another host"): `POST /api/watch/ack` sends the ids of another paired host to that
   host's `POST /api/peers/watch/ack` and `--all --host X` to X (host route in `HostRoutePolicy`, host principal, `HostLimiter`, body
   cap; it handles only the events it recorded, never passes an ack on, records who/from-host with the note, sends `watch.events` for its
   own events once per operation; the answer says what became of every id and the CLI exits non-zero for any that was not handled).
   `GET /api/peers/watch/log` was written as a GET and is a `POST /api/peers/watch/log` (`HostCaller` only POSTs); the daemon answers
   `GET /api/watch/log?host=<alias>|all` by asking the paired hosts, and `pdx watch log --host <alias> | --all` uses it. The messages
   to the watcher name only commands and flags that exist (#3071; a test reads the messages and the CLI's own flags).
5. For the watcher area (B line spec `docs/specs/2026-10-16-watcher-area-spec.md` §8 A3; admin token; the writes 403
   to device tokens and not in `HostRoutePolicy`): `GET /api/watch/events?since=&limit=` → `{events}` newest first
   with the fields that spec lists (the record's fields plus `status` `pending|handled`, `note`, `handled_at`, `handled_by`,
   `handled_from_host`); the `watch.events` push (spec §12.6: `{host_id, unhandled, cause}`, one per record / ack operation, the count
   read and sent under one lock); `watch.v1` announced by `/api/info` while the module is ready. No skip route (user decision 10-16: no
   "skip all" button). The local write route stays admin only and is not a host route; the peer ack route is one (2026-10-17, lead).
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

### GQ — queue order: front, back, hold (#3034; lease member, one PR)

Spec §5.2. The queue is `internal/gate/slots.go`: tickets `queue/<class>/<unix-ns>-<pid>-<n>` held by flock, served in
name order by `TryTake` → `liveBefore` under `<class>.admit`; `Waiters()` lists them.
1. One lock for every queue change. Under the class's admit lock (`<class>.admit`, as `TryTake` holds it): `SetRank`
   (open the ticket, check its flock is still held, write `<ticket>.rank.new` and rename it; after the rename check the
   ticket again and, if it is gone, remove the rank and answer "no longer waiting"), `ClearRank`, `Ticket.Drop` (now
   takes the admit lock briefly to remove the ticket and its rank together), and the scans' clean-up of a dead gate's
   ticket and of a rank file whose ticket is gone. So a rank never outlives its ticket unseen, and the CLI never reports
   success for a ticket that is no longer waiting.
2. One snapshot for every reader. `Pool.Snapshot()` under the admit lock: the holders (incl. late and over-size ones),
   the free capacity, and the live tickets with their ranks in serving order (spec §5.2: front by `at`, then by ticket
   name on a tie; normal and back by name; hold ones listed after, never counted ahead; an expired `until` reads as
   normal; a rank file that cannot be read, is malformed, or names an unknown rank reads as **normal** with a
   `rank_error` shown — arrival order, today's behaviour, so a broken file never strands a waiter). `TryTake`,
   `pdx gate status`, `pdx gate queue` and would-wait all read from it, so counts, places and ranks always agree.
3. `TryTake(t)` returns `Take{Slot, Ahead, AheadFront, Rank}` (the waiter's own rank, by, reason, until): no slot when
   the ticket is held (reason `held`) or when the tickets ahead in serving order use up the free capacity. Late claims
   keep their place (spec §5.2): `TakeLate` takes capacity without a ticket and ignores ranks, because the command is
   already running (D9); its holder counts against the pool as today.
4. The wait loop reads its own rank on every poll, also on a poll where the admission check (pressure) keeps it from
   calling `TryTake` (a `Snapshot` read then), and keeps `held_s` as the sum of the polls' intervals spent in `hold`
   (several holds, a hold during a pressure wait, an expiry, a release and a new hold all add up correctly); the
   decision record gains `rank` (the last one) and `held_s`. The wait line names a hold (`held by <by>: <reason>`,
   with `until`) or the front tickets ahead (`behind N moved to the front`).
5. would-wait answers for a hypothetical new `normal` ticket at the end of the normal group: ahead = live front +
   normal tickets (back and hold excluded), against the holders and free capacity of the same snapshot.
6. CLI `pdx gate queue [--class heavy|light] [--json]` (the snapshot) and `pdx gate queue front|back|hold|normal
   <pid|ticket>… --reason "<line>" [--until <dur|RFC 3339>]`: a pid names that gate's ticket in either class; reason
   required (one line, ≤ 200 bytes, no control characters); `--until` only with `hold`; a ticket no longer waiting is
   reported for that ticket, the others go on; prints old → new per ticket. `by` = the session id the gate reads from
   the environment, else `person`. `Waiter` / status JSON gain `rank`, `rank_reason`, `rank_by`, `held_until`,
   `rank_error`.
7. The watcher's skill (W3b.4) gets the procedure: read the queue, move with a reason, tell the person; it never asks
   anyone to cut their command down (spec §5.2).
Tests: order table (front before normal before back; two fronts by `at`, a tie by ticket name; a hold skipped and not
counted ahead; an expired hold is normal; malformed JSON, unknown rank, unreadable file and a bad `until` read as normal
with `rank_error`); a held waiter is not served while a slot is free, and is served after `normal`; others pass a hold;
races under `-race`: `SetRank` against `Drop` (no orphan rank, "no longer waiting" reported), a rank written between two
`TryTake`s is honoured by the second, `Snapshot` during rank writes and drops (no duplicate place, order matches ranks);
`TakeLate` while a front waiter queues → the late claim takes the slot and the front waiter is next once it ends; a pool
shrunk with an over-size holder; `held_s` across two holds, a hold during a pressure wait and an expiry; would-wait:
back and hold tickets do not make a new command wait, a front one does; CLI: missing reason refused, `--until` without
`hold` refused, unknown pid reported, old → new printed; status JSON fields; mutation on the order comparison, the
hold check and the would-wait count.

### GC — conflicts: two kinds of command that do not run together (#3058; lease member, one PR, after GQ)

Spec §5.3. Built on GQ's admit lock and `Snapshot` (the serving order) of one class; nothing here changes how ranks order
the queue. Codex plan review 10-17 (task-mvb5792n-bnh6m5) found the first draft unsafe — rules across classes under two
locks, and a taken slot whose record had no argv until later — so rules are per class and the take publishes its argv.
0. **The take publishes the command (first, before any rule).** `EnqueueFor(info)` keeps `info` on the `Ticket` (in
   memory); `TryTake(t)` writes the slot's placeholder record (#3049, `writePlaceholder(k, info)`) with the argv, cwd and
   session of `t`'s `WaiterInfo`, still inside the class's admit lock. The take never reads the ticket file back: that
   write ignores its errors and may leave an empty or partial file (it is only for the listing), so it cannot be what a
   rule matches. A placeholder that cannot be written already fails the take and lets the lock go (today's
   `writePlaceholder` error path), so a slot is never handed out without its argv on disk. `TakeLate(info WaiterInfo)`
   does the same (the caller in `run.go` passes the running command's argv). `SetHolder` later overwrites the record as
   today (atomic, temp + rename). Tests: right after `TryTake`/`TakeLate` returns, `Held()` shows the argv (no window with
   an empty record), also when the ticket file was left empty (the take uses the ticket's in-memory info); a placeholder
   write that fails → no slot, lock free; `-race` with a reader polling `Held()` during takes.
1. `commandWords(argv) []string` (spec §5.3 "the command's words"), cases spelled out, each with a table test: `npx`,
   `pnpx`, `bunx`, `bun x` `<tool …>`; `pnpm|npm|yarn|bun exec|x <tool …>` with the runner options before the subcommand
   skipped together with their values for `runnerValueOpts` (both `--opt value` and `--opt=value`), and a `--` right
   after `exec`/`x` skipped; `pnpm <tool …>` when `Classify` knows the tool; the tool word (argv[0] and the one after a
   runner) by `filepath.Base` (`/repo/node_modules/.bin/tsc`, `./node_modules/.bin/tsc`, `/opt/homebrew/bin/pnpm`);
   `pnpm run <script>` and `npm run <script>` stay as they are (scripts are not opened). One function used only by the
   matcher; `classifyRunner` is not rewritten in this PR (it may share `runnerValueOpts`).
2. `matchesPrefix(pattern, words)`: the pattern's words equal the first len(pattern) command words (as
   `InterceptConfig.Classify` matches a rule, but on words, after item 1). `Conflict{Class, A, B, Reason, By string; At
   time.Time}`; `conflicts(rule, x, y)` is symmetric (A–B or B–A) and requires both commands to be of the rule's class.
3. Storage: `conflicts.json` in the gate directory (`{"rules": [...]}`), read with the same tolerance as a rank file
   (unreadable or malformed → no rules, shown as `conflicts_error` in status; a broken file never strands a waiter), and
   read **inside the admit lock** by `Snapshot`, so the walk always uses the rules as they are at the take.
4. The walk in `Snapshot`, after GQ's serving order, for one class: capacity = the pool's free slots exactly as `TryTake`
   computes it today (`overHeld()` already counted, so an over-size holder is not counted twice); holders = every locked
   slot's record, late and over-size included. For each ticket in order (hold tickets skipped): **blocked** if a holder of
   the class or any earlier front/normal ticket conflicts with it under a rule of the class (record which, and the rule);
   else **served** if capacity is left (and take one); else **waiting for capacity**. A blocked ticket takes no capacity.
   `TryTake(t)` gives a slot exactly when the walk marks `t` served; otherwise `Take` carries `Blocked{With, Rule}` besides
   GQ's `Ahead`/`Rank`. Late claims (`TakeLate`) do not consult rules (spec §5.3); their records are in the walk.
5. The wait loop's line for a blocked waiter: `waits for <slot or ticket>: <a> × <b> (<reason>)`; it keeps `conflict_s`
   like GQ's `held_s`; the decision record gains `conflict` (the last rule) and `conflict_s`.
6. would-wait (GQ.5): a hypothetical new `normal` ticket at the end of the normal group, walked the same way.
7. CLI `pdx gate conflict [--json]`, `pdx gate conflict add --class <c> "<a>" "<b>" --reason "<line>"`, `pdx gate conflict
   remove <n | --class <c> "<a>" "<b>">`: take `conflicts.lock` (flock), re-read `conflicts.json`, resolve an index
   against what was read, decide, write temp + rename, release; print what was found and what was left. Reason required
   (one line, ≤ 200 bytes, no control characters); a pattern must have at least a tool word; class `heavy` or `light`; an
   existing pair (same class, either order) → "already there", nothing written; a missing one → "no such rule". `by` =
   the session id from the environment, else `person`; `at` = now.
8. `pdx gate status [--json]`: the rules (index, class, a, b, reason, by, at), `conflicts_error`, and per waiter
   `conflict_with` and `conflict_rule`.
9. The watcher's notes (W3b.4 / the manual watcher): when a measured pair thrashes, add a rule with the measurement as its
   reason and tell the person; remove it when the measurement no longer holds.
Tests: `commandWords` table — every case of item 1, plus `npx --package typescript -- tsc -b` (value and `--`), `pnpm
--filter spa exec tsc -b`, `pnpm --filter=spa exec tsc -b`, `npm exec -- tsc -b`, `yarn dlx tsc` (stays `yarn dlx tsc`:
dlx is not exec), `pnpm exec ./node_modules/.bin/tsc -b`, `/opt/homebrew/bin/pnpm tsc -b`, `pnpm run build` (stays a
script); `matchesPrefix` — `tsc -b` vs `tsc -b --force` (match) and `tsc --force -b` (no match), `vitest related` vs
`vitest --project related run` (no match: not a prefix), a pattern longer than the command (no match), same-pattern rule,
a rule of the other class (no effect); the walk — a ticket that conflicts with a holder is blocked and a non-conflicting
ticket behind it takes the free slot; a ticket behind a blocked one that conflicts with it waits even with a slot free;
two tickets that conflict with each other and no holder → the first is served, the second blocked; a front ticket that
conflicts with a holder is blocked and does not hold back non-conflicting normal ones; a hold ticket that conflicts with a
later one does not block it; an over-size holder (pool shrunk) still blocks and is not counted twice against capacity,
with a blocked ticket ahead and a free normal one behind; a late claim is never blocked and, once taken, blocks a
conflicting waiter; a `no_record` holder blocks nothing; a rule removed → the blocked ticket is served on the next poll.
Races under `-race` (internal/gate): two gates whose commands conflict, two slots free, taking at once → exactly one is
served (item 0 makes the second see the first's argv); `TakeLate` and a conflicting `TryTake` at once, two slots free,
repeated → the outcome follows who took the admit lock first, read from the slot numbers (both take the lowest free
slot): the late claim on slot 1 → the waiter is blocked; the waiter on slot 1 → the late claim on slot 2 and both hold
(spec §5.3: a late claim ignores rules) — never the waiter served beside a late claim that took the lock before it; the
same two in a fixed order (`TakeLate` returns, then `TryTake`; and the reverse) give each outcome deterministically; two
waiters that read the old (empty) rules, a rule added, then both `TryTake` → the
rule holds (rules read inside the lock); `Snapshot` while ranks and rules change. would-wait for a command that conflicts
with a holder / a front ticket / a back ticket (back: does not make it wait). CLI: missing reason refused, empty pattern
refused, add twice → one rule, remove by index and by pair, two adds at once both land, add and remove of the same pair at
once → the result matches the order the lock was taken and both print what they found; a malformed `conflicts.json` →
no rules and `conflicts_error`; status JSON fields. Mutation on the prefix match, the runner-word skipping, the blocked
check (holder side and ticket-ahead side), capacity not taken by a blocked ticket, and the argv in the take's placeholder.

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

Order: W1a → W1b → W2a → W2b → W3a → GQ → GC → W3c → W3b → W4 (GC needs GQ's `Snapshot`; W4 only needs W1a's
`redact.Argv` and may go earlier; W3c is
the lease member's, independent of W3a, and only has to land before W3b's skill lets the watcher set 0). W3a can be
deployed alone (it registers the manual watcher; W2 delivers to it); start, wake and rest arrive with W3b. The set
goes live as a whole when W1b–W4 are merged and deployed; W5 follows.
