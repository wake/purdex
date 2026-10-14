# Fleet status and probe-only hosts — plan (F-1a … F-2b)

Spec: `docs/specs/2026-10-14-fleet-status-spec.md`. Facts in the spec §3 (origin/main, 2026-10-14).

## 1. Design choices (for the reviewer)

- **A new daemon module `fleet`** (`internal/module/fleet`), not a growth of `resources`: `resources` is the A line's lease sampler and must not gain SSH; `fleet` reads the local sample through a narrow interface (`internal/resources` types + a registry key the resources module registers: `resources.HostSampleReader{ LatestHost() (HostUse, bool) }`) and the monitor's disk the same way (`monitor` registers `DiskReader{ Disk() (usedPercent float64, ok bool) }`). Both providers are small additions reviewed by 1f (resources is A-line code).
- **SSH is `exec.CommandContext("ssh", …)` with a fixed argv** — `-o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes -- <alias> <script>` where the script is a compile-time constant; the alias is validated twice (on save against the parsed `~/.ssh/config`, and before each call by the same regex). No shell on the local side.
- **Probe-host definitions live in the prober's host config** (`hostconfig` key `probe_hosts`), so the look follows the definition to every App; the App never stores a probe host locally.
- **Zone for every host** = the resources rule (`ZoneLatch`, its thresholds from the resources settings) + the disk rule (85 / 95). For daemon hosts the `fleet` module recomputes the zone with disk included from the same sample (the `resources` zone stays the lease's own).
- **Push, not poll**: `fleet.changed` carries the whole payload (4 hosts × a dozen fields), throttled to one per 5 s and only on a visible change (zone, status, rounded number, trend).

## 2. Phases

| PR | content | reviewer extra | est. lines |
|---|---|---|---|
| F-1a | `fleet` module: config + alias reader + prober + parsers + zone / trend | 1f (resources / monitor reader interfaces) | ~750 |
| F-1b | `GET /api/fleet`, `fleet.changed`, `GET /api/fleet/ssh-aliases`, `PUT /api/hostconfig/probe-hosts`, `fleet.v1`, device route | 1f | ~500 |
| F-2a | SPA data: `useFleetStore`, `fleet.v1` probe, event branch, API; Hosts page section + dialog | — | ~700 |
| F-2b | SPA bottom block: expanded rows, collapsed strip, narrow light, i18n | — | ~550 |

Every PR: TDD, one commit per task, mutation table; daemon: `go test -race` on the touched packages + `gofmt -l` / `go vet`, never the full `go test ./...`; SPA: affected vitest while developing, full vitest (`--maxWorkers=3`) + `tsc -b --force` + lint before merge on a fresh origin/main. codex R1 → attacker → critic.

## 3. F-1a — the `fleet` module

- **T-1.1 Config**: `hostconfig` key `probe_hosts` = `[{id, name, ssh_alias, icon, colors, icon_weight}]` (≤ 16 entries; `id` server-generated 8 hex on create; `name` 1–40 chars; `icon` a Phosphor name or empty; `colors` the App's per-mode colour object, validated as `#rrggbb` strings; `ssh_alias` per T-1.2). Normaliser rejects unknown keys and bad values (400 `bad_request` with the field).
- **T-1.2 Alias reader** `internal/fleet/sshconfig.go`: parse `~/.ssh/config` of the daemon user — `Host` lines' literal names (skip any with `*`, `?` or `!`), `Include` followed one level (relative to `~/.ssh`), comments and `Match` blocks ignored; cached by mtime. `ValidAlias(a)` = regex `^[A-Za-z0-9._-]{1,64}$` and present in the list.
- **T-1.3 Prober** `internal/module/fleet/prober.go`: per host a goroutine with a 60 s ticker (jittered ± 5 s), one `ssh` call (argv in §1, `exec.CommandContext` with a 10 s context, stdin `/dev/null`, stdout capped at 64 KiB, stderr capped at 4 KiB and redacted into `error`); results into an in-memory table; a config change restarts the affected goroutines; `Stop` cancels all.
- **T-1.4 Parsers** `internal/fleet/parse.go`: the script prints sections separated by marker lines (`@@uname`, `@@ncpu`, …) so one parser handles both OSes; Darwin `vm_stat` → in-use % as `internal/resources` computes it for mlab (reuse its function if exported, else port it with a test against the same fixture); Linux `MemTotal − MemAvailable`; `df` used % of the chosen mount.
- **T-1.5 Zone and trend** `internal/fleet/zone.go`: a `ZoneLatch` per host (from `internal/resources`) fed `{Mem, Load1, NCPU, Pressure: 0}`, then the disk rule; trend from the last 10 samples with the resources trend rule (exported helper or a ported copy with a shared fixture test).
- **T-1.6 Providers** (1f): `resources.HostSampleReaderKey` (`LatestHost()`), `monitor.DiskReaderKey` (`Disk()`), each registered in its module's `Init`; read in `fleet.Start`, tolerated when missing.
- Tests: config validation (each field, unknown key, 17 entries; M: accept unknown keys); alias reader (wildcards skipped, `Include`, `Match` ignored, missing file → empty; M: accept `*`); the argv is exactly the fixed one with the alias after `--` (M: shell string → injection test with `a;rm` fails); a fake `ssh` on `PATH` (test binary) answering Darwin / Linux fixtures, a timeout (→ offline, `last_ok_at` kept), garbage (→ error), a config change restarting one host only; zone with disk 84 / 85 / 95 and hysteresis (M: disk ignored); trend rising / falling / flat.

## 4. F-1b — API and events

- `GET /api/fleet` (§6 shape): `self` from `LatestHost()` + `Disk()` + the fleet zone / trend for self; `probes` from the prober table joined with the config (look fields). `fleet.changed` via `core.Events.Broadcast` on a visible change, ≤ 1 per 5 s (trailing send).
- `GET /api/fleet/ssh-aliases` → `{aliases: [...]}`; `PUT /api/hostconfig/probe-hosts` through `putHandler` with the T-1.1 normaliser (it also refuses an alias not in the reader's list → 400 `unknown_ssh_alias`).
- `fleet.v1` in `core/info_handler.go` when the module is ready; device routes: `GET /api/fleet` allowed; the alias list and the PUT stay admin-only.
- Tests: the GET shape with and without providers (no resources → self `unknown`); the event on a zone change and not on an unchanged sample (M: send every tick), throttle; device token: GET 200, alias list 403, PUT 403 (M: allow-list the PUT); unknown alias refused.

## 5. F-2a — SPA data and the Hosts page

- `FLEET_CAPABILITY`; probe beside the others (`lib/team/unattended-support.ts`), invalidated on disconnect, forgotten on re-point / removal.
- `lib/fleet/{types,parse,api,fleet-ws}.ts` (`send`-based API; strict parse; `fleet.changed` writes the store), `useFleetStore` (per daemon host: `self`, `probes`, `updatedAt`; a daemon host the App cannot reach → offline with the time it went away).
- Initial `GET /api/fleet` on connect (per connection generation).
- Hosts page: under a `fleet.v1` host, 「只探查的主機」 with rows (badge, name, SSH host, status), 「新增」, edit and remove; the dialog (name, SSH host picker from `GET /api/fleet/ssh-aliases`, `HostIconField`, `HostColorField`); save → PUT the whole list; errors shown in the dialog (`unknown_ssh_alias`, `bad_request` field).
- Tests: parse each status; event updates without a fetch; offline on disconnect (M: keep the last numbers as live); dialog validation and the PUT body; remove asks once.

## 6. F-2b — the bottom block

- `components/fleet/FleetRows.tsx` (expanded) above the labelled buttons in `BottomNav`'s wide-rows look; `FleetStrip.tsx` replaces the icon buttons in the wide one-row look (the toggle stays); `FleetLight.tsx` above the buttons in the narrow look (worst zone, tooltip list).
- Each host's look: daemon hosts via `useHostLook`; probe-only hosts from their definition (`resolveHostColors(look, mode)` → `HostBadge`).
- Zone colours from the theme (`status-success` / `status-warning` / `status-error` / text-muted for offline); trend arrows; 「離線（n 分鐘前）」.
- Most loaded = worst zone, then the highest of CPU / memory / disk.
- i18n under `fleet.*` (zh-TW / en), completeness test.
- Tests: expanded rows per host kind; collapsed strip hides the four buttons and expands on click (M: buttons still rendered); the most-loaded label and nothing when all normal; narrow light colour = worst; offline rendering; a row click opens the Hosts page at that host.
