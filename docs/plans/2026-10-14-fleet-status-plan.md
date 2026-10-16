# Fleet status — plan (rev 3.1: F-0 … F-2b)

Spec: `docs/specs/2026-10-14-fleet-status-spec.md` **rev 2** (2026-10-16). Facts: spec §3 (origin/main alpha.742).

Rev 3 (2026-10-16) rewrote rev 2 for spec rev 2. **Rev 3.1** takes the focused plan review (subagent, 3 critical, 8
important, 6 minor, all taken): the peer routes are POSTs through `HostCaller` with `host_id`; both roles heartbeat; the
liveness age counts every host's reading; `fleet.changed` carries the term and is opt-in; module dependencies and the
capability place corrected; own alias rules; the alias shape of `config`; the SSH session's argv, process group,
per-frame cap, newest-wins hand-off, 30 s kill rule; one page-rate window everywhere and a zone latch; the PRs split so
they really run in parallel.

**Flow: the quick one** (the user wants the hosts on screen within hours, 10-16): one focused plan review (done); each PR
one subagent full review; no codex round. Daemon PRs also go past 1f (hostconfig, `cmd/pdx/main.go`, core events, device
scope). The gate's heavy slots are 2 on mlab; run one test command at a time per PR anyway.

## 1. Design choices (for the reviewer)

- **Module `fleet`** (`internal/module/fleet`) over the leaf `internal/fleet`. `resources` gains no SSH.
- **Dependencies** `["peers", "hostconfig", "resources"]`: `HostCaller` (`peers.HostCallerKey`), the hostconfig readers, and
  the A line's `fleet.LocalSourceKey` (registered in resources' `Init`); missing key → an empty `FakeLocal`, logged once.
- **Role engine** pure over an injected clock and a `Peer` (`Role(ctx, req) (RoleAnswer, error)`, `Announce(ctx, term)`),
  exposed as `fleet.RoleView` (F-0) so the measuring side (F-1c2) builds against `FakeRole` in parallel.
- **One SSH session per remote host**, fixed argv (spec §5), own process group, stdin `/dev/null`, stdout read by a
  marker-aware reader with a per-frame cap, stderr read locally only.
- **One snapshot, one push**: an aggregator holds the newest fast / slow reading per host; a 1 s ticker sends
  `fleet.changed` to the opt-in subscribers.
- **Nothing persisted but `state.json`**; no sample reaches SQLite or the log.

## 2. Phases

| PR | content | depends on | est. lines (code + tests) |
|---|---|---|---|
| F-0 | #2969: §7 types, `Proc`, `LocalSourceKey`, `RoleView` / `RoleState`, `FakeLocal`, `FakeRole` | — | ~250 |
| F-1b1 | hostconfig rows `probe_hosts` + `fleet` (normalisers, lenient readers, GET fields, PUT routes, reader keys) | — | ~900 |
| F-1b2 | module skeleton + registration; `state.json`; role engine; `POST /api/peers/fleet/{role,announce}` + policy | F-0, F-1b1 | ~1000 |
| F-1c1 | leaf: loop scripts, captured fixtures, frame parser, page-rate window, composite, zone latch, queue summary | F-0 | ~1000 |
| F-1c2 | sessions, aggregator, `GET /api/fleet`, `fleet.changed` + `FeatureFleetV1`, ssh-aliases route, `fleet.v1`, device scope; wired to `RoleView` | F-0, F-1c1 (F-1b1 for the probe rows, F-1b2 for the real `RoleView` at the end) | ~1100 |
| A line | `LocalSource` in the resources sampler, registered in its `Init` | F-0 | (A line) |
| F-2a1 | SPA types / parse, `useFleetStore`, history, `fleet.v1` probe, `fleet=v1` opt-in and branch (reworks #2879) | spec §6 (fixtures) | ~900 |
| F-2a2 | SPA Hosts page: probe-host section writing both daemons, `fleet` row editor (reworks #2881; #2880 stands) | F-2a1, #2880 | ~800 |
| F-2b | SPA `CountBadge`, host cells, narrow column, interim clicks, i18n | F-2a1 | ~600 |

Line counts include tests; over 800 lines / 20 files is accepted for these because each is one reviewable unit and tests
are about half (lead's call, 10-16, as for #2900) — the PR body lists code and test lines separately.

Parallel from the start: **F-1b1, F-1c1, F-2a1**. Then F-1b2 (after F-1b1), F-1c2 (after F-1c1; uses `FakeRole` until
F-1b2 merges, then its last task wires the real one), F-2a2 and F-2b (after F-2a1).

Every PR: TDD, one commit per task, a mutation table in the PR body (each named mutation made, the test seen red,
reverted). Daemon: `go test` of the touched packages, `-race` on `internal/module/fleet`, `gofmt -l`, `go vet` of those
packages; never `go test ./...`. SPA: `vitest related --run` on the changed files, `tsc -b --force`, `eslint` on the
changed files; never the full vitest or the full lint.

## 3. F-1b1 — host config rows

- **T-1b1.1 `probe_hosts` collection** (hostconfig files): `KeyProbeHosts`; `normalizeProbeHosts(raw, cur)` —
  `[{id, name, ssh_alias, icon, colors, icon_weight}]`, ≤ 16 rows; `id` kept when it matches a current row,
  server-generated 8 hex when empty; `name` 1–40 chars; `icon` a Phosphor name or empty; `icon_weight` the App's weights;
  `colors` exactly the App's `HostConfig.colors` shape (`spa/src/lib/host-color.ts`): keyed by mode (`console` |
  `terminal` | `execution`, each optional), each `{main: {color: "#rrggbb", alpha: 0–100 int}, middle?: {color?, alpha},
  light?: {color?, alpha}}`, unknown modes / layers / keys rejected; unknown row keys rejected; `ssh_alias` through
  `fleet.ValidAlias` (failure → `ValidationError` `unknown_ssh_alias: <alias>`). `PUT /api/hostconfig/probe-hosts` via
  `putHandler` (`{items, baseRevision}`, 409 with the current copy, 400 plain text); `readers[KeyProbeHosts]` lenient (a
  stored row that no longer validates — alias gone included — dropped with a reason); `emptyFor` → `[]`; GET field
  `probeHosts`; reader `ProbeHostsKey` → `ProbeHosts() (rows, revision int64, err)` registered in `Init`.
- **T-1b1.2 `fleet` row**: `{order: [alias…], peer_ssh_alias, enabled}` per spec §4: `order` 1–4, each passing the alias
  shape rule of `config.ValidateSelfAlias`, unique case-insensitively (own-alias membership and peer resolution are
  checked by the module at run time, not here: the config must save while a peer is offline); `peer_ssh_alias` empty or
  `fleet.ValidAlias`; `enabled` default true; default row `{["air26","mlab"], "", true}`. `PUT /api/hostconfig/fleet`
  (single row, `baseRevision`, 409, 400 text), lenient read, GET field `fleet`, reader `FleetConfigKey`.
- Tests: each field and bound, unknown keys (M: accept unknown keys), id kept / generated, 409 / 400 text, a stored row
  with a gone alias dropped on read, defaults, device-token PUT → 403 (existing scope rule, asserted).

## 4. F-1b2 — module and roles

- **T-1b2.1 Skeleton**: `Name "fleet"`, `Dependencies() = ["peers","hostconfig","resources"]`; `Init` resolves the keys
  (§1); `Start` / `Stop` (cancel, wait, no goroutine left); `registerServeModules` in `cmd/pdx/main.go` after the three.
- **T-1b2.2 State file** (spec §4): read in `Start`, written on every change, torn / missing → standby 0, write failure logged.
- **T-1b2.3 Own alias**: `Cfg.Peers.Alias`; empty or not in `order` → role off with a reason (re-checked when the `fleet`
  row's revision moves, read on every heartbeat tick).
- **T-1b2.4 Role engine** (`role.go`): spec §4 in full — start (three branches + announce once the other answers after an
  unreachable start), heartbeat from both roles every 30 s with a timeout of max(5 s, `peers.InterDaemonTimeout`),
  liveness (`newest_age_ms ≤ 10 000`; null alive for 60 s after the primary's term began), takeover (`highest+1`,
  persist, announce), step-down on a higher term from any source (answer, announcement, the caller's role / term in a
  heartbeat call), equal terms both primary → the later in `order` steps down, `off` handling, the `order` mismatch
  warning (once per hour). Implements `fleet.RoleView`; `OnChange` called once per change, in order. Needs a
  `newestAge func() (time.Duration, bool)` supplied by the aggregator (F-1c2; nil until wired → null).
- **T-1b2.5 Routes**: `POST /api/peers/fleet/role` and `POST /api/peers/fleet/announce` (spec §4 bodies and replies; the
  reply carries `host_id`; `to_host_id` not this host → 409 `wrong_host`); both in `peers.HostRoutePolicy` (POST, exact
  path) with `policy_test.go` cases; the `Peer` implementation over `HostCaller.Call` with `HostIDOf(alias)`.
- Tests (fake clock, fake peer): start ×3; heartbeat alive / silent / age 11 s → takeover `term+1` + one announce (M:
  alive ignores the age); **partitioned start → two primaries → the link returns → one primary within one heartbeat**
  (M: the primary does not heartbeat → still two primaries); equal terms → the later in `order` steps down (M: both stay);
  a recovered old primary sees a primary → standby (M: switch back by order); **a primary with no own-host source but
  fresh remote readings is not taken over** (M: age from the own host only → flap); null age: alive for 60 s, dead after;
  `off` (own alias missing) answers `off` and never becomes primary; `enabled=false` on the primary → stops, standby takes
  over next beat; `state.json` torn → standby 0; routes: `wrong_host`, reply `host_id`, policy entries (M: route missing
  from the policy → the caller gets `unsupported`).

## 5. F-1c1 — the leaf: scripts, frames, load

- **T-1c1.1 Capture first**: run the draft loop once on mlab, air26 and mserv (air-2019 if awake) for 20 s each, save the
  raw output to `internal/fleet/testdata/loop-{darwin,linux}-*.txt` (user names and hostnames scrubbed), and measure the
  remote CPU over 60 s (`times` in the loop); the numbers go in the PR body (spec target ≤ 15 ms / s; over → report, do not
  change rates).
- **T-1c1.2 Loop scripts** (`loop.go`, constants): spec §5 — frames `@@f` … `@@end` each second; every fifth also `@@s`,
  `@@top_cpu`, `@@top_mem` (one `ps`), `@@gate` … `@@gate_end` (multi-line JSON from `pdx gate status --json`, or
  `@@gate none`); Darwin CPU from one `iostat -n0 -w1` merged in; Linux `/proc/stat` with built-ins; exit on a failed
  write, `trap` that kills the children. `LoopArgs(alias)` = spec §5 argv with the options before `--`.
- **T-1c1.3 Frame reader and parser** (`frame.go`): a reader over `io.Reader` that splits on the markers with a 64 KiB cap
  per frame (no `bufio.Scanner` default token limit); `ParseFast` → `LocalFast` + disk %, free GB; `ParseSlow` →
  `LocalSlow` (gate JSON into `gate.Status`; a bad gate section → `Gate` nil and a flag, the lists kept); Linux PSI →
  level, no PSI → level 0 (unknown).
- **T-1c1.4 Page-rate window** (`rate.go`): counters kept for `hostmem.RateWindow`, rate over the window (fewer than two
  points → unknown), the same function for own and remote hosts.
- **T-1c1.5 Load** (`load.go`): `Composite` (spec §5), the memory zone latch (spec §5 enter / leave, 10 s, one level at a
  time) combined with F-1a's `DiskLatch`, `QueueSummary(status, fast, settings)` with `admission_held` (`Status.Admission`
  when the field exists — the A line adds it; until it lands, from `Status.Settings`' effective `admission.*`).
- Tests: parser on the captured fixtures (Darwin, Linux, PSI levels, no PSI, gate JSON multi-line / `none` / broken →
  queue blank but lists kept, a frame without `@@end`, a frame over 64 KiB; M: accept a frame without `@@end`, M: a broken
  gate section fails the whole frame); rate over the window vs one frame (M: one-frame delta); composite table incl. disk
  84 vs 86 and CPU 95 at level 1 → neutral (M: colour by score); latch enter / stay / leave with a fake clock (M: no
  10 s hold → flicker); `LoopArgs` exact argv incl. `-T`, `ControlMaster=no`, options before `--`, alias `a;rm` refused.

## 6. F-1c2 — sessions, aggregator, API

- **T-1c2.1 Session** (`internal/module/fleet/session.go`): one goroutine per remote host while primary; `exec.Cmd` with
  `SysProcAttr{Setpgid: true}`; stop = cancel → signal the group (TERM, KILL after 2 s) → wait for the stdout reader to
  end → return; frames handed on through a newest-wins slot (never blocks the reader); a frame older than 2 s when taken is
  dropped; no frame 5 s → `offline/timeout` with the session kept, 30 s → kill and restart; exit classified by F-1a's
  `classify255`; restart 5 s, doubling to 60 s, reset on a good frame, every wait cut by a stop; five unparsable fast
  frames in a row → `error/parse` + restart (count reset on a good one); `ValidAlias` before each start (`alias_gone`);
  stderr to the log (≤ 1 line per host and code per hour). `OnStandby` then `OnPrimary` in quick succession never runs
  two sessions for one host (the old one is waited for).
- **T-1c2.2 Aggregator** (`fleet.go`): rows for the own host (`LocalSource` + `LocalDisk` at each 1 s tick), the other
  daemon host (`peer_ssh_alias`) and the probe hosts (config rows; on the hostconfig revision: start new, stop removed,
  restart on an alias change, look-only change updates without a restart); timestamps = receive time; supplies
  `newestAge` to the role engine (the newest reading of any host).
- **T-1c2.3 Push**: core `FeatureFleetV1` in `featuresOf` (`fleet=v1`) and a best-effort newest-wins broadcast for it
  (`internal/core/events.go`); a 1 s ticker sends `fleet.changed {term, primary, hosts}` (spec §6; slow parts only when
  changed) while primary; nothing while standby / off.
- **T-1c2.4 API**: `GET /api/fleet` (primary / standby / off, spec §6; `id` = the daemon host id or the probe row id);
  `GET /api/fleet/ssh-aliases`; `fleet.v1` via `capabilityList()` + `moduleReady("fleet")` (`internal/core/info_handler.go`);
  device scope (`cmd/pdx/device_scope.go`): `GET /api/fleet` allowed, the alias list admin-only.
- **T-1c2.5 Wiring** (after F-1b2 merges): `RoleView.OnChange` → primary: `LocalSource.Start()` + sessions + ticker;
  standby / off: stop all three.
- Tests: fake ssh binary on an injected path — frames then silence → offline at 5 s, killed and restarted at 30 s (M:
  restart at 5 s); exit 255 + auth stderr → `error/auth`, stderr absent from the wire; backoff 5 → 10 → 20 s and reset;
  **stop with a child that holds the pipe (a `ProxyCommand`-like grandchild) → returns, no process left** (M: kill the ssh
  pid only → hangs); newest-wins (a slow consumer sees only the newest frame); quick standby → primary does not double a
  session; `-race`, no goroutine left. Push: one per tick; slow only when changed (M: every slow part each tick); nothing
  as standby (M: push as standby); an un-opted subscriber gets nothing (M: broadcast to all). API shapes; device token: GET
  200, alias list 403.

## 7. F-2a1 — SPA data

- **T-2a1.1 Types and parse** `lib/fleet/{types,parse,api}.ts` (spec §6: primary / standby / off answers, `Host`, `fast`,
  `slow`, `Queue`, codes as a union; unknown code → `parse`).
- **T-2a1.2 Store** `useFleetStore`: per daemon host `{role, term, generation}`; the primary = highest term among those
  answering `primary` (equal → earlier in `order`); `hosts` = its snapshot with daemon hosts mapped to `HostConfig`
  through `lib/host-daemon-id.ts`; `fleet.changed` merged from the current primary only; a push carrying a higher term from
  another daemon → re-read both and switch; replies and pushes written only under the generation they were requested in
  (bumped on disconnect / re-point / removal); no push 10 s or the primary gone → `waitingPrimary`, last values kept, both
  re-read every 5 s; only a standby known → `primaryElsewhere`.
- **T-2a1.3 History** `lib/fleet/history.ts` (memory only): per host and item, 1 s points for 10 min, 10 s buckets
  `{avg, max}` for 24 h; `series(hostId, item, range)` for 5 min / 1 h / 6 h / 24 h.
- **T-2a1.4 Capability and events**: `fleet.v1` probe beside the others (`lib/team/unattended-support.ts`), invalidated
  on disconnect, forgotten on re-point / removal; `fleet=v1` added to the host-events URL in `useMultiHostEventWs` and the
  `fleet.changed` branch.
- Tests: parse; primary choice (M: first answer wins); higher-term push from the other daemon → re-read and switch (M:
  ignore it); stale → `waitingPrimary`, values kept, 5 s re-read (M: clear values); a standby push ignored; generation
  rule (late reply after disconnect does not write); the URL carries `fleet=v1`; history buckets avg / max, range slicing,
  nothing in storage (M: write to localStorage).

## 8. F-2a2 — the Hosts page

- Spec §8.2: the 「只探查的主機」 section once (on the overview of any `fleet.v1` host); the dialog (name, icon and colour
  via #2880's lifted pickers, the SSH alias **per daemon** from each daemon's `GET /api/fleet/ssh-aliases`); save = PUT to
  both with each one's revision; a 409 from either reloads both and says the list changed elsewhere; 400 text in the
  dialog; remove asks once. The `fleet` row editor (order, alias for the other daemon host) on each daemon host's
  overview. Highlight-once for the interim click (F-2b).
- Tests: the dialog writes both daemons with their own alias and revision (M: one alias for both); 409 from one reloads
  both; 400 text; remove asks once; the `fleet` row editor round trip; highlight once and clear.

## 9. F-2b — the sidebar host cells

- `components/fleet/CountBadge.tsx` (spec §8.1; reused later by the watcher shield), `HostCell.tsx` (box, badge, ring),
  `FleetCells.tsx` (one row under the BottomNav in the wide bar; a column in the narrow bar); tooltip (name, zone word, the
  five values, queue summary, 「等待主方」 / 「主方不在這台 App」); interim click targets (spec §8.1); i18n `fleet.*`
  (zh-TW / en) and the completeness test.
- Tests: box colours and the white icon; offline hatch + dashed ring; ring length = composite, colour = zone (M: colour by
  score); badge only on daemon hosts with waiters and not offline, its three colours (M: badge at 0 waiters); four cells
  in one row at 240 px; the narrow column; click targets; the two tooltip states.

## 10. Rev 3.2 additions (user, 10-16): F-2b2, F-2c, F-2d

Spec §6 (`POST /api/fleet/probe-check`), §8.1 (restyle) and §8.2 (probe hosts' own entries). Quick flow; each PR one
subagent full review; F-2c also goes past 1f (`internal/module/fleet`).

- **F-2b2** — the cells' restyle (spec §8.1): `HostBadge` with the user's badge settings in place of the coloured box,
  `ContextRing` itself (a dot in the middle, through an optional prop that leaves every existing caller's output the same)
  in place of the cell's own ring, zone → ok / warn / danger tone, the `TeamMemberBeads` layout (`gap-0.5`, no separators,
  wrap). Screenshots from a harness of the real components, dark and light, approved by the user before merge. Tests:
  the badge and ring components with the host's colours and the zone tone (M: normal back to grey), offline without an
  arc, the row wraps (M: no `flex-wrap`).
- **F-2c** — `internal/module/fleet`: `POST /api/fleet/probe-check {ssh_alias}` → `ValidAlias`, then
  `leaf.Runner{}.Run(ctx, alias)` (one shot, its own timeout, `execstat` tag `fleet.ssh-run` already), mapped to `{ssh,
  measure, error, reading}` as spec §6; a per-alias single flight (a second request while one runs waits for the same
  answer); admin only (device token 403, not in the peer policy); never stderr in the answer. Tests: each `Code` → its
  `{ssh, measure}` (M: `remote_exit` as ssh failed), the reading on ok, an unknown alias → `alias_gone` without running
  ssh, device token 403, single flight (two requests, one run), the stderr absent. Commands: `go test -race
  ./internal/module/fleet/`, `go test ./internal/fleet/`, `go test ./internal/execstat/`, `go vet`, `gofmt -l`.
- **F-2d** — SPA, the Hosts page (spec §8.2):
  - `HostSidebar` gains the 「觀察主機」 group after the paired hosts: badge, name, status dot (`useFleetStore` snapshot),
    `+` on the heading. Selecting one selects `probe:<id>` (the page's selection key; no sub-pages). The selected probe
    entry's row collapses like the others (the collapse fix of 10-16).
  - `components/hosts/ProbeHostPage.tsx`: the header; 樣式與連線 inline (the fields of `ProbeHostDialog` moved into a
    form component both use, the draft kept in `useFleetDraftStore` so a tab switch keeps it); 儲存 through the existing
    `config-store` write to both daemons (409 / 400 as today); 刪除 with a confirmation.
  - `ProbeCheck` (`lib/fleet/probe-check.ts` + a component): calls each fleet daemon's `POST /api/fleet/probe-check` with
    that daemon's alias, on open, after an alias change is saved, and on 「重新檢查」; one line per daemon host with the two
    marks and the reason words of spec §8.2; an offline daemon host 「{host} 離線，無法檢查」; a 404 (a daemon before F-2c)
    「這台 daemon 還不支援檢查」.
  - The overview's `ProbeHostsSection` becomes the short list (rows open the entry) with 「新增觀察主機」.
  - The watcher area's W5c1 reuses the page's form and `ProbeCheck` for a probe host's 設定.
  - Tests: the sidebar group and selection; the form round trip writes both daemons with their own alias (M: one alias for
    both); the check lines for ok / auth / unreachable / remote_exit / offline / 404 (M: read `unknown` as ✓); the draft
    survives a tab switch (real `TabContent`); the overview list opens the entry.
- Order: F-2b2 now (priority); F-2c and F-2d in parallel after it (F-2d mocks the route until F-2c lands; its check lines
  show the 404 text against a daemon without it).
