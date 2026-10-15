# Fleet status — plan (rev 3: F-0 … F-2b)

Spec: `docs/specs/2026-10-14-fleet-status-spec.md` **rev 2** (2026-10-16). Facts: spec §3 (origin/main alpha.742).

Rev 3 (2026-10-16) rewrites rev 2 for spec rev 2: a primary and a standby, 1 s frames over one long-lived SSH session per
host, the App reading the primary only, the sidebar host cells. Rev 2's details that still hold are kept (the
`probe_hosts` collection contract, alias validation, the error classification, the App's generations, the lifted editors).
**Flow: the quick one** (the user wants the hosts on screen within hours, 10-16): this plan gets one focused subagent
review; each PR a subagent full review; no codex round. The daemon PRs still go past 1f (hostconfig, `cmd/pdx/main.go`,
device scope). The gate's heavy slots are 2 on mlab since 04:4x; run one test command at a time per PR anyway.

## 1. Design choices (for the reviewer)

- **Module `fleet`** (`internal/module/fleet`) over the leaf package `internal/fleet` (F-1a). `resources` gains no SSH.
- **The own host comes from the A line's `fleet.LocalSource`** (spec §7), resolved in `Init` from the registry key
  `fleet.LocalSourceKey` (`"fleet.local-source"`, defined with the types in F-0); missing → an empty `FakeLocal` (the cell
  shows 「沒有資料」). The module calls `Start()` when it becomes primary and `Stop()` when it steps down.
- **Roles live in the module**, the network through the peer path (`HostCaller` / `peers.HostRoutePolicy`), like the
  watcher's `/api/peers/watch/*`. A role engine with an injected clock and caller decides; it never blocks the push.
- **One SSH session per remote host** (`exec.CommandContext("ssh", argv…)`, fixed argv, the loop script a compile-time
  constant, stdin `/dev/null`); frames read line by line from stdout with a size cap; stderr read locally only.
- **One snapshot, one push**: an aggregator holds the newest fast and slow reading per host; a 1 s ticker builds the push
  (fast of all hosts; slow of the hosts whose slow part changed since the last push).
- **Nothing persisted but the role state** (`~/.config/pdx/fleet/state.json`); no sample reaches SQLite or the log.

## 2. Phases

| PR | content | depends on | est. lines |
|---|---|---|---|
| F-0 | `internal/fleet/local.go`: the §7 types, `Proc`, `LocalSourceKey`; `FakeLocal` | — | ~120 (in review) |
| F-1b | hostconfig rows `probe_hosts` + `fleet`; module skeleton; `state.json`; role engine; peer routes | F-0 | ~750 |
| F-1c | loop scripts + frame parser; session manager; composite / zone / queue; aggregator; `GET /api/fleet`, `fleet.changed`, `GET /api/fleet/ssh-aliases`, `fleet.v1`, device routes | F-0 (F-1b for the wiring task only) | ~800 |
| A line | `LocalSource` in the resources sampler (probe-dedupe plan §F.1), registered under `LocalSourceKey` | F-0 | (A line) |
| F-2a | SPA data: types / parse, `useFleetStore` (primary by term, stale), the in-memory history, `fleet.v1`, the event branch; Hosts page section writing both daemons (reworks #2879 / #2881; #2880 stands) | F-1c's payload (fixtures until merged) | ~750 |
| F-2b | SPA sidebar host cells, the narrow column | F-2a | ~450 |

F-1b, F-1c, F-2a start in parallel. F-1c's last task wires the role callbacks after F-1b merges. F-2b starts on F-2a's
store with fixtures.

Every PR: TDD, one commit per task, a mutation table in the PR body (each named mutation made, the test seen red,
reverted). Daemon: `go test` of the touched packages, `-race` on the module package (it has goroutines), `gofmt -l`,
`go vet` of those packages; never `go test ./...`. SPA: `vitest related --run` on the changed files, `tsc -b --force`,
`eslint` on the changed files; never the full vitest or the full lint (CLAUDE.md test tiers).

## 3. F-1b — config, module, roles

- **T-1b.1 `probe_hosts` collection** — rev 2's T-1b.1 unchanged (normaliser, ≤ 16 rows, ids, the `colors` shape of
  `spa/src/lib/host-color.ts`, `fleet.ValidAlias` → `unknown_ssh_alias: <alias>`, lenient reader dropping rows that no
  longer validate, `putHandler`, GET field `probeHosts`, reader key).
- **T-1b.2 `fleet` row**: `{order: [alias…], peer_ssh_alias: string, enabled: bool}`; `order` 1–4 unique aliases matching
  `^[a-z0-9][a-z0-9-]{0,31}$` (default `["air26","mlab"]`); `peer_ssh_alias` empty or `fleet.ValidAlias`; `enabled`
  default true. `PUT /api/hostconfig/fleet` (single row, `baseRevision`, 409, 400 text), lenient read, GET field `fleet`.
- **T-1b.3 Module skeleton**: `Name "fleet"`, `Dependencies() = ["hostconfig"]`; `Init` resolves the hostconfig readers and
  `LocalSourceKey` (missing → `FakeLocal`, logged once); `Start` / `Stop` (cancel, wait, no goroutine left);
  `registerServeModules` in `cmd/pdx/main.go`.
- **T-1b.4 State**: `state.json` `{role, term, last_heard_ms}` written atomically (temp + rename) on every change; read in
  `Start`; a torn or missing file = `{standby, 0}`.
- **T-1b.5 Role engine** (`internal/module/fleet/role.go`, pure over an injected clock and a `Peer` interface
  `Role(ctx) (RoleAnswer, error)` / `Announce(ctx, term)`): spec §4 — start sequence (other primary → standby and adopt its
  term; unreachable → primary, `term+1`; other standby / starting → first in `order` primary with `max+1`), heartbeat every
  30 s with a 5 s timeout, alive = answered and `newest_age_ms ≤ 10000`, takeover with `term = highest+1` + announce, step
  down on a higher term from any source, equal terms → first in `order` stays, `enabled=false` → standby forever and
  answers `standby`. Outputs: `OnPrimary()` / `OnStandby()` callbacks (called once per change) and `State()`.
- **T-1b.6 Peer routes**: `GET /api/peers/fleet/role` → `{role, term, order, newest_age_ms, alias}`;
  `POST /api/peers/fleet/announce {term, alias}` → applies "higher term wins"; both in `peers.HostRoutePolicy`; the `Peer`
  implementation over `HostCaller` (`to_host_id` resolved from the alias in `order` through the peers inventory).
- Tests: normalisers (each field, unknown key, bounds; M: accept unknown keys); 409 / 400 text; lenient read drops a row
  with a gone alias; registration (M: drop the AddModule line). Role engine table with a fake clock and fake peer — start:
  each of the three branches; heartbeat: alive / not answering / answering with age 11 s → takeover with `term+1` and one
  announce (M: alive ignores the age); a higher term in a heartbeat answer or an announce → step down, `OnStandby` once
  (M: keep primary); equal terms → order decides; a recovered old primary that sees a primary → standby (no switch back;
  M: switch back by order); `enabled=false`; restart reads `state.json` (torn file → standby 0). Routes: shape, policy
  entries (M: route missing from the policy → peer call refused).

## 4. F-1c — measuring and the API

- **T-1c.1 Loop scripts** (`internal/fleet/loop.go`, constants): Darwin and Linux per spec §5; each second one fast frame
  `@@f <unix_ms>` … `@@end`; every fifth second also `@@s` with `@@top_cpu`, `@@top_mem` (one `ps`), and `@@gate`
  (`pdx gate status --json` from `$HOME/.local/bin/pdx` then `$HOME/.config/pdx/bin/pdx`; absent → `@@gate none`). Darwin
  CPU from one long-running `iostat -n0 -w1` merged into the frames; Linux CPU from `/proc/stat` deltas with shell
  built-ins. **Capture real output** once from mlab, air26 and mserv (and air-2019 when awake) into
  `internal/fleet/testdata/` (scrub user names and hostnames) before writing the parser; measure the loop's remote CPU
  over 60 s on each (spec target ≤ 15 ms / s) and put the numbers in the PR body.
- **T-1c.2 Frame parser** (`internal/fleet/frame.go`): `ParseFast` → `LocalFast` (+ disk %, free GB), `ParseSlow` →
  `LocalSlow` (gate JSON into `gate.Status`, `none` → nil); Linux PSI → level (spec §5 values); page rate from the counter
  delta of two frames (first frame → rate unknown); a frame over 64 KiB or without `@@end` → error.
- **T-1c.3 Session manager** (`internal/module/fleet/session.go`): one goroutine per remote host while primary: start the
  ssh (`fleet.Args` + `ServerAliveInterval=5`, `ServerAliveCountMax=2`), read frames, publish to the aggregator; no frame
  for 5 s → `offline/timeout`; exit classified as F-1a's `classify255`; restart after 5 s, backoff doubling to 60 s, reset
  on a good frame; 5 unparsable frames in a row → `error/parse` + restart; `ValidAlias` before each start (`alias_gone`);
  stderr to the log per spec (≤ 1 line per host and code per hour). `Stop` kills every ssh and waits.
- **T-1c.4 Composite, zone, queue** (`internal/fleet/load.go`): `Composite(fast, disk)` (spec §5 formula); `Zone` = the
  §2.6 rule with `DiskLatch` (replace F-1a's `ZoneLatch`-based probe zone; F-1a's trend is no longer used); `QueueSummary
  (gate.Status, fast, settings)` incl. `admission_held` (`Status.Admission` when present, else level / page rate against
  the host's effective `admission.*` from `Status.Settings`).
- **T-1c.5 Aggregator and push** (`internal/module/fleet/fleet.go`): rows for own host (LocalSource + `LocalDisk` at each
  1 s tick), the other daemon host (`peer_ssh_alias`), and the probe hosts (config rows; reconciled on the hostconfig
  revision as rev 2's T-1b.4); a 1 s ticker sends `fleet.changed` (spec §6: all fast parts; slow parts that changed;
  status changes); nothing while standby.
- **T-1c.6 API**: `GET /api/fleet` (standby / primary shapes, spec §6), `GET /api/fleet/ssh-aliases`, `fleet.v1` in the
  capabilities when the module is ready, device scope: `GET /api/fleet` + the event allowed, the alias list admin-only.
- **T-1c.7 Wiring** (after F-1b merges): `OnPrimary` → `LocalSource.Start()` + sessions + ticker; `OnStandby` → stop all
  three; `newest_age_ms` for the role route = age of the own host's newest fast reading.
- Tests: parser on the captured fixtures (Darwin, Linux; PSI levels; gate JSON present / `none`; torn frame; 64 KiB; M:
  accept a frame without `@@end`); page rate from two frames (M: rate from one frame); composite table incl. disk 84 vs 86
  and CPU 95 with level 1 → neutral zone (M: colour by score); session manager with a fake ssh binary on an injected path
  (frames then silence → offline after 5 s; exit 255 + auth stderr → `error/auth` with no stderr on the wire; restart
  backoff 5 → 10 → 20 s, reset after a good frame; `Stop` leaves no process and no goroutine, `-race`); push: one per tick,
  slow parts only when changed (M: send every slow part each tick), nothing while standby (M: push as standby); API shapes;
  device token: GET 200, alias list 403.

## 5. F-2a — SPA data and the Hosts page

- **T-2a.1 Types and parse** `lib/fleet/{types,parse,api}.ts` for spec §6 (standby / primary answers, `Host`, `fast`, `slow`,
  `Queue`, codes as a union).
- **T-2a.2 Store** `useFleetStore`: per daemon host `{role, term, generation}`; the primary = the host answering `primary`
  with the highest term; `hosts` = the primary's snapshot; `fleet.changed` merged only from the current primary and only
  under the generation it was requested in (rev 2's generation rule); no push for 10 s or the primary disconnects →
  `waitingPrimary` with the last values kept; on a role change re-read `GET /api/fleet` from both.
- **T-2a.3 History** `lib/fleet/history.ts` (memory only, never persisted): per host and item, 1 s points for the last 10 min
  and 10 s buckets `{avg, max}` for 24 h; `series(hostId, item, range)` for the watcher area (5 min / 1 h / 6 h / 24 h).
- **T-2a.4 Capability and events**: `fleet.v1` probe beside the others (`lib/team/unattended-support.ts`), invalidated on
  disconnect, forgotten on re-point / removal; the `fleet.changed` branch in `useMultiHostEventWs`.
- **T-2a.5 Hosts page**: rev 2's T-2a.4 dialog and section, changed per spec §8.2 — the section once (on the Hosts page's
  overview of any `fleet.v1` host, listing the shared rows), the dialog asking the SSH alias **per daemon** (each from that
  daemon's alias list), save = PUT to both with each one's revision; a 409 from either reloads both. The `fleet` row
  (order, alias for the other daemon host) on each daemon host's overview.
- Tests: parse; primary choice (two primaries → higher term; M: first answer wins); stale → `waitingPrimary` and values kept
  (M: clear values); a push from the standby ignored (M: merge any host's push); generation rule (late GET after
  disconnect does not write); history buckets avg / max and range slicing, memory only (M: write to localStorage); dialog
  writes both daemons with their own alias and revision, 409 from one reloads both.

## 6. F-2b — the sidebar host cells

- `components/fleet/HostCell.tsx` per spec §8.1 (box, queue badge, composite ring), `FleetCells.tsx` (one row under the
  BottomNav in the wide bar, a column in the narrow bar), tooltip (name, zone word, five values, queue summary,
  「等待主方」).
- **Click, interim**: until the watcher area lands, a daemon host's cell opens that host on the Hosts page; a probe host's
  opens the Hosts page's probe-host section with that row highlighted once. The watcher area spec replaces this.
- i18n `fleet.*` (zh-TW / en) and the completeness test.
- Tests: the box colours and the white icon; offline hatch + dashed ring; ring length = composite, colour = zone (M: colour
  by score); badge only on daemon hosts with waiters, its three colours (M: badge at 0 waiters); one row at the default
  sidebar width (four cells fit 240 px); the narrow column; click targets; 「等待主方」 tooltip.
