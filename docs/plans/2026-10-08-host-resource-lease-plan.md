# Host resource lease — plan

Spec: `docs/specs/2026-10-08-host-resource-lease-spec.md` (§1 R1–R8 final; §3.1 measurements M-R1–M-R3; §4 model D-1…D-7; §6 phases). Author: member δ (`mlab/_bx5os3`), lead purdex-1f (`mlab/_vqnjx1`).

Format: contracts, rules, named tests and mutation gates; implementers write the code (TDD: each named test is written red first). Line numbers are as of `3cf2cf2f` (origin/main); re-check before editing.

## PR map

| PR | Phase | Content | Size (est.) | Deploy |
|---|---|---|---|---|
| **P0-1** | measure | `internal/resources`: wire types, D-1 host math, process-tree attribution, darwin sampler | ~650 lines, 8 files | — (library) |
| **P0-2** | measure | module `internal/module/resources` (5 s ticker), session roots from the peers registry, `GET /api/resources`, `pdx lease ls` (measured), `pdx team` CPU／MEM | ~700 lines, 12 files | daemon + CLI |
| **P1-1** | leases | host setting `resources` (hostconfig key, strict normalizer); pure admission engine (D-2 charge, D-3 queue) | ~650 lines, 8 files | — |
| **P1-2a** | leases | `resources.db` lease store, module wiring and boot (Task 1.5b), `POST／GET?wait／DELETE /api/resources/leases` with the wake-up protocol (Tasks 1.3, 1.4, 1.5b) | ~750 lines, 9 files | — (routes live but nothing calls them yet) |
| **P1-2b** | leases | admission loop under `stateMu`, per-lease use, sweeper (deadline → overrun grant, holder gone, `max_hold`, vanished), WS `resources.changed` (Tasks 1.5, 1.6) | ~750 lines, 8 files | daemon |
| **P1-3** | leases | CLI `pdx lease run／acquire／release`, full `ls`; skill `pdx-lease`; embed list | ~700 lines, 8 files | CLI + `pdx setup` |
| **P2-1** | interception | mod `hooks/lease.js`: classifier, `--maxWorkers=3` rewrite, acquire／`next(e)`／release, context; `register.js` one line; guard test | ~750 lines, 6 files | mod (`pdx setup`) |
| **P3-1** | learning | per-kind learned weight (bounded EWMA of observed charge), per-kind concurrency cap; shown in `ls` | ~500 lines, 7 files | daemon |
| (App) | — | contract only here (§ App contract); UI by the interface lead | — | SPA |

P0-1 → P0-2 → P1-1 → P1-2a → P1-2b → P1-3 → P2-1 → P3-1 (nine PRs), each stacked on the previous merge (not on an open PR). Each phase is useful alone and can be stopped after its last PR.

## Common rules

- Go: `make lint` clean (gofmt incl. doc comments — no `''` in comments, see memory `reference_gofmt_doc_comment_quotes`); `go test` for the touched packages only; `-race` for `internal/resources` and `internal/module/resources`, **one package at a time** (mlab resource rules, quoted below).
- Mod (P1-3 skill, P2-1): `claude plugin validate cmd/pdx/plugin/purdex --strict`, `claude plugin test cmd/pdx/plugin/purdex`, `go test ./cmd/pdx/plugin/`; manual, quoted in the PR body.
- Each task is one commit; parallel subagents in one worktree commit with `git commit --only <files>`.
- **Hot paths (#1777, #1794):** nothing in this plan runs on a hook event or on the agent sweep. The sampler has its own ticker; the lease handlers read the latest sample, never take one.
- **Fail open (D-5):** every client path (CLI, mod) that cannot reach the daemon, gets a bad answer or times out runs the command anyway and says so on stderr / in context.
- mlab resource rules, copied into every subagent brief verbatim: (1) during development run only the affected tests; (2) the full vitest only once before merge, `--maxWorkers=3`, and **ask the coordinator first with `pdx msg`** (one session at a time); (3) `go test -race` only for affected packages, one package at a time.

---

## P0 — measure

### P0-1 — `internal/resources` (library)

#### Task 0.1 — wire types

File: `internal/resources/wire.go`, `wire_test.go`.

- `type Snapshot struct` (JSON names in parentheses): `SampledAt time.Time (sampled_at, RFC 3339 UTC)`, `Available bool (available)`, `Reason string (reason, omitempty — "unsupported_platform" | "sample_failed")`, `Capacity int (capacity, always 100)`, `Host HostUse (host)`, `Sessions []SessionUse (sessions)`, `Mode string (mode — "measure" in P0)`. P1 adds `Leases`, `Waiters` (omitted in P0; the field names are reserved now so the App contract is stable).
- `type HostUse struct`: `Measured int (measured — 0…100, D-1)`, `CPU float64 (cpu — load1/ncpu × 100)`, `Mem float64 (mem — vm_stat in use %)`, `Load1 float64 (load1)`, `NCPU int (ncpu)`, `MemBytes uint64 (mem_bytes)`, `MemUsedBytes uint64 (mem_used_bytes)`, `Pressure int (pressure — 1/2/4, 0 unknown)`, `MemorystatusLevel int (memorystatus_level — 0…100, −1 unknown)`, `PcpuTotal float64 (pcpu_total — Σ pcpu over all processes / ncpu, for P3 calibration)`, `Full bool (full — R5's "host full")`.
  - Rule for `Full` (R5): `load1 ≥ ncpu` **or** `mem ≥ 90` **or** `pressure ≥ 2`. (R5 says "load = cores, or memory 90 %"; pressure warn counts as 90 per D-1.)
- `type SessionUse struct`: `SessionID (session_id)`, `PID (pid)`, `Tmux (tmux, omitempty)`, `Cwd (cwd, omitempty)`, `CPU float64 (cpu — Σ pcpu / ncpu, host %)`, `Mem float64 (mem — Σ rss / memsize × 100)`, `Use int (use — ceil(max(cpu, mem)))`, `RSSBytes uint64 (rss_bytes)`, `Pcpu float64 (pcpu — raw Σ pcpu, 100 = one core)`, `Procs int (procs)`.
- Constants: `Capacity = 100`, `SampleInterval = 5 * time.Second`, `PressureWarnFloor = 90`, `PressureCriticalFloor = 100`, `MemFullPercent = 90`.

Tests: `TestSnapshot_JSONShape` (golden JSON of a fixed snapshot: field names, omitempty, RFC 3339 UTC), `TestSnapshot_ReservedLeaseFieldsAbsentInP0`.

#### Task 0.2 — D-1 host math

File: `internal/resources/hostuse.go`, `hostuse_test.go`.

- `type HostRaw struct { Load1 float64; NCPU int; MemBytes uint64; PageSize, Free, Inactive, Speculative uint64; Pressure int; MemorystatusLevel int; PcpuSum float64 }` — what the sampler read.
- `func ComputeHost(r HostRaw) HostUse`: `cpu = 100·load1/ncpu`; `available = (free+inactive+speculative)·pagesize`, `mem = 100·(1 − available/memsize)` clamped to [0, 100]; `measured = ceil(max(cpu, mem))`, then `max(measured, 90)` when pressure = 2, `100` when pressure = 4; clamp [0, 100]. NCPU ≤ 0 or MemBytes = 0 → `cpu`/`mem` 0 and the sample is marked unusable by the caller (`Available=false, Reason="sample_failed"`).
- `cpu` above 100 is kept in `HostUse.CPU` (shown as is) but `Measured` clamps to 100.

Tests: `TestComputeHost_Table` — rows: idle (load 1/10, mem 40 % → 40), CPU-bound (load 12/10 → 100, `Full`), memory 91 % → 91 and `Full`, pressure 2 with mem 76 % → 90 and `Full` (M-R1's real reading), pressure 4 → 100, ncpu 0 → unusable, available > memsize → mem 0. Each row asserts `Measured`, `CPU`, `Mem`, `Full`.
Mutation gates: drop the pressure floor → the pressure-2 row red; use `free+inactive` without speculative → a row built with speculative ≠ 0 red; `Full` without the memory clause → the 91 % row red.

#### Task 0.3 — process-tree attribution

File: `internal/resources/attribute.go`, `attribute_test.go`.

- `type Proc struct { PID, PPID int; Pcpu float64; RSSBytes uint64 }`; `type Root struct { SessionID string; PID int; ProcStart string; Tmux, Cwd string }`.
- `func Attribute(procs []Proc, roots []Root, ncpu int, memBytes uint64) []SessionUse`: build `children[ppid]`; for each root whose pid is in `procs`, walk its descendants breadth-first; **a descendant that is itself another root is not walked into** (it is attributed to its own root — a session started from inside another session's shell is not double-counted); a cycle guard (visited set). Sum `Pcpu` and `RSSBytes` (root included), count procs, compute `cpu`, `mem`, `use` as Task 0.1. Roots absent from `procs` are dropped. Output sorted by `use` desc, then session id.
- Two roots with the same pid (registry duplicates): keep the first by session id order; log nothing here (caller logs).

Tests: `TestAttribute_TreeSums` (root + MCP child + bash → vitest → 3 workers: sums and procs = 6), `TestAttribute_NestedRootNotDoubleCounted` (root A's bash starts `claude` root B: A excludes B's subtree, B has its own), `TestAttribute_MissingRootDropped`, `TestAttribute_CycleGuard` (corrupt ppid loop terminates), `TestAttribute_Units` (pcpu 250 on ncpu 10 → cpu 25; rss 1.6 GiB on 16 GiB → mem 10; use 25).
Mutation gates: walk into nested roots → `NestedRootNotDoubleCounted` red; forget the root's own rss → `TreeSums` red.

#### Task 0.4 — darwin sampler

Files: `internal/resources/sample.go` (interface + parsing), `sample_darwin.go`, `sample_other.go`, `sample_test.go`.

- `type Sampler interface { Sample(ctx context.Context) (HostRaw, []Proc, error) }`; `func NewSampler() Sampler` (darwin: real; other platforms: returns `ErrUnsupported` — the module then publishes `Available=false, Reason="unsupported_platform"`).
- darwin (M-R1): `unix.SysctlRaw("vm.loadavg")` decoded as 3 × `uint32` + 4 pad + `uint64 fscale` (length must be 24, else error); `unix.SysctlUint32("hw.ncpu")`, `unix.SysctlUint64("hw.memsize")`, `unix.SysctlUint32("kern.memorystatus_vm_pressure_level")` (error → 0), `unix.SysctlUint32("kern.memorystatus_level")` (error → −1); one `vm_stat` fork (`exec.CommandContext`, 2 s timeout) parsed for page size, free, inactive, speculative — **copy** `parseDarwinVMStat` from `internal/module/monitor/host_system.go:100` and extend it with speculative (monitor stays untouched; the duplicate is ~40 lines, noted in the PR); one `ps -axo pid=,ppid=,pcpu=,rss=` fork (2 s timeout, `LC_ALL=C` in its env so the decimal point is `.`) parsed to `[]Proc` (rss KiB → bytes; malformed lines skipped and counted, not fatal; zero valid lines → error). `PcpuSum` = Σ pcpu.
- Seams: `sysctlRaw`, `sysctlU32`, `sysctlU64`, `runCmd` package vars for tests.

Tests: `TestParseLoadavg` (a captured 24-byte buffer → 17.59; wrong length → error), `TestParseVMStat_Speculative` (captured M-R1 text), `TestParsePS` (captured lines incl. a malformed one and a locale comma — the comma line is skipped), `TestSampler_PressureAndLevelErrorsAreSoft` (seams fail only for those two sysctls → sample ok, pressure 0, level −1), `TestSampler_VMStatFailureFails`.
Mutation gate: read `ldavg[1]` (load5) instead of `[0]` → `TestParseLoadavg` red.

P0-1 acceptance: unit tests; `go test -race ./internal/resources/`; `make lint`.

### P0-2 — module, API, CLI

#### Task 0.5 — session roots from the peers registry

Files: `internal/module/peers/origin_resolver.go` (one method + a `readRegistry = ipeers.ReadRegistry` package var seam), `origin_resolver_roots_test.go`, `internal/agent/process_snapshot.go` (one method).

- 〔review #13〕 `func (s *ProcessSnapshot) Start(pid int) (time.Time, error)` returns the entry's parsed start (`e.start`, or `e.startErr`) **without** reading argv — the registry's `StartTime` callback takes a `time.Time` (`internal/peers/registry.go:93-95`) and `StartTime(pid)` returns the lstart text. An entry whose start cannot be read is classified unknown and left out by `ReadRegistryDiag` (`registry.go:394-430`); that is the existing rule and it stays (such a session shows no row until it can be read).

- `func (r *OriginResolver) ProcessRoots(snap *agent.ProcessSnapshot) ([]resources.Root, error)`: `ipeers.ReadRegistry(r.m.registryDir, live)` with a `Liveness` whose `PidAlive` and `StartTime` read **only** `snap` (`snap.Alive`, `snap.Start`); **`Info` stays nil** — `snap.Read` reads argv and falls back to a `ps` fork for non-ASCII argv or another user's process (`internal/agent/process_snapshot_darwin.go:162-185`), so it is not used; with `Info` nil `IsProxy` is always false (`internal/peers/registry.go:61`), and proxies are dropped by `r.m.proxyPIDs()` instead; `Stat` = `os.Stat`; `Zombie` nil; map to `Root{SessionID, PID, ProcStart, Tmux, Cwd}`.
- Exposed to the resources module through a new interface `resources.RootSource` and the existing `OriginResolverKey` registration (the resolver already is a registered service, `internal/module/peers/module.go:379`); no new registration.
- Why the registry and not `agent_frames`: it lists every CC session including ones outside tmux (R1: every session may lease), costs no fork, and does not touch `internal/module/agent` (U1-2 in progress there). Codex frames are not leasing sessions (D-6).

Tests: `TestProcessRoots_SkipsProxiesAndDead` (temp registry dir with four files: live, dead pid, start-time mismatch, a pid in `proxyPIDs()`; a fake snapshot), `TestProcessRoots_NoFork` (the `readRegistry` seam captures the `Liveness`: `Info == nil`, and its `PidAlive`／`StartTime` answer from the fake snapshot — calling them for a pid the snapshot lacks returns dead／error, never consults anything else), `TestProcessRoots_UnreadableStartLeftOut` (snapshot `Start` error → no root), `TestProcessRoots_StatErrorIsUnknown` (a non-ENOENT `Stat` error → no root, no panic), `TestSnapshotStart_NoArgvRead` (in `internal/agent`: `Start` on a pid whose argv read would fail still answers).
Mutation gate: set `Info` to `snap.Read` → `NoFork` red.

#### Task 0.6 — module `resources`

Files: `internal/module/resources/module.go`, `sampler_loop.go`, `handler.go`, tests; `cmd/pdx/main.go` (`registerServeModules` adds it after `peersMod`).

- Name `resources`, dependencies `peers`. `Init`: look up `OriginResolverKey` as `resources.RootSource` (absent → roots empty, logged once).
- `Start`: one goroutine, `time.NewTicker(SampleInterval)`, first sample immediately. Per tick: `Sample(ctx)` (3 s budget), `agent.SnapshotProcesses(ctx)` (start times for roots), `ProcessRoots(snap)`, `Attribute(...)`, `ComputeHost(...)`; store the snapshot under `mu` (atomic pointer). A failed tick keeps the previous snapshot but sets `Available=false, Reason="sample_failed"` after **3** consecutive failures (one flaky fork does not blank the App). Logs one line per transition (ok → failing → ok), never per tick.
- `Stop`: cancel + wait for the goroutine (bounded by the shared shutdown context).
- Tick duration is measured and kept as `sample_ms` in the snapshot (debug field, `omitempty`) so the cost is visible (M-R1 says ~25–35 ms at load 17).

Tests: `TestModule_TicksOnOwnTicker` (fake clock seam / short interval; two snapshots with increasing `sampled_at`), `TestModule_FailingSampleKeepsLastThenMarksUnavailable` (fail × 2 → still available; × 3 → `sample_failed`; recover → available), `TestModule_StopJoins`, `TestModule_UnsupportedPlatform` (sampler returns `ErrUnsupported` → `Available=false`, `unsupported_platform`, no retry loop logging).
Mutation gate: mark unavailable on the first failure → `FailingSampleKeepsLast…` red.

#### Task 0.7 — `GET /api/resources`

File: `internal/module/resources/handler.go`, `handler_test.go`.

- `GET /api/resources` → the latest `Snapshot` as JSON (TokenAuth like every `/api/*`). Before the first sample: 200 with `Available=false, Reason="warming_up"`. Never takes a sample itself (hot path rule).
- `?session=<id>` filters `sessions` to one id (used by the mod in P2 and by `pdx team`'s join; unknown id → empty list, 200).

Tests: `TestAPI_SnapshotShape` (contract test: decode into `resources.Snapshot`, all host fields present), `TestAPI_WarmingUp`, `TestAPI_SessionFilter`, `TestAPI_NeverSamples` (a sampler seam counting calls: handler calls → count unchanged).

#### Task 0.8 — `pdx lease ls` (measured) and `pdx team` CPU／MEM

Files: `cmd/pdx/lease_cmd.go`, `lease_cmd_test.go`, `cmd/pdx/team_cmd.go`, `team_cmd_test.go`, `cmd/pdx/main.go` (`case "lease"`).

- `pdx lease ls [--json] [--config]` — no inbox needed (works from any shell). Table: first line `host  <measured>/100  load <load1>/<ncpu>  mem <mem>%  pressure <normal|warn|critical|->  [FULL]`; then `SESSION  PID  CPU  MEM  RSS  PROCS  TMUX` per session (session id shortened to 8, `-` cells, `sanitizeCell`). `--json` prints the daemon's body compacted. 404 from an older daemon → one stderr line "這個 daemon 沒有 /api/resources，請先更新 daemon unsupported", exit `ExitError`.
- `pdx team`: after `/api/team`, one `GET /api/resources` (same client, 5 s attempt timeout); two new columns **CPU** and **MEM** after CTX, joined by `member.session_id`, both in D-1 units — host % with no decimals (e.g. `4%`, `2%`; review #14); RSS bytes stay in `pdx lease ls`; any failure or a missing session → `-`, and **no** error line (the team table must never break because of the resources call). `--json` is unchanged (the daemon's team view as is).

Tests: `TestLeaseLs_Table` (fake daemon), `TestLeaseLs_OldDaemon404`, `TestLeaseLs_JSON`, `TestTeam_CPUMEMJoined`, `TestTeam_ResourcesFailureShowsDash` (500 / timeout / 404 → table still printed, `-` cells, exit 0), `TestTeam_JSONUnchanged`.
Mutation gate: propagate the resources error → `ResourcesFailureShowsDash` red.

#### P0 acceptance and deploy (after P0-2 merges and the lead bumps)

1. Lead deploys the daemon (`bin/pdx` rm → cp → mv, restart). δ verifies: `pdx lease ls` shows the host line and this session's row; `pdx team` (from the lead) shows CPU／MEM for members.
2. Cost check: `sample_ms` over 10 minutes (from `GET /api/resources` every 30 s): median and max recorded in the spec §3.1; the daemon's own CPU (`ps -o pcpu -p <daemon pid>`) before/after, recorded.
3. Cross-check one row: a session's `rss_bytes` against `ps -o rss=` of its tree summed by hand (within 5 %).

---

## P1 — voluntary leases

### P1-1 — setting and admission engine

#### Task 1.1 — host setting `resources`

Files: `internal/module/hostconfig/resources.go`, `resources_test.go`, `handler.go` (`emptyFor`, `handleGet` map), `module.go` (route + registry), `internal/resources/settings.go` (the type, shared with the CLI).

- Key `resources`, generic collection mode (`PUT /api/hostconfig/resources` with `{items, baseRevision}`, 409 on stale), so the App can edit it later. Registered as `resources.SettingsKey` for the resources module (type-asserted reader, as `RelaySwitchReader`).
- `type Settings struct` (all optional in JSON; defaults applied by `Effective()`): `Mode string` (`off|measure|advise|lease`, default **`lease`** once P1 ships — see "Decisions for the lead"), `Kinds map[string]int` (D-7 weights: `test-full 45, build 35, test-pkg 15, lint-full 10`; 1–100 each; unknown kind names allowed — P2 only matches the built-ins, `pdx lease run --kind <custom>` uses them), `DeadlineS int` (300, 10–590), `WarmupS int` (20, 0–300), `FloorPct int` (50, 0–100), `MaxHoldS int` (3600, 60–86400), `EWMAHalfLifeS int` (15, 5–300).
- Strict normalizer (like `relayFields`, `internal/module/hostconfig/relay.go:60-76`): unknown top-level fields → 400; out-of-range → 400 naming the field; duplicate JSON keys already rejected by the handler.
- `mode` semantics: `off` — sampler stopped, lease routes answer `{"granted":true,"mode":"off"}` immediately; `measure` — sampler on, leases granted immediately and **not recorded**; `advise` — admission computed and recorded, but every request is granted immediately with `would_wait: true|false` (the CLI prints a warning when true); `lease` — real waiting (D-3).

Tests: `TestResourcesSettings_Defaults`, `TestResourcesSettings_RejectsUnknownField`, `TestResourcesSettings_Ranges` (table per field), `TestResourcesSettings_PutGetRoundTrip` (through the hostconfig handler, revision increments, stale → 409).
Mutation gate: accept unknown fields → `RejectsUnknownField` red.

#### Task 1.2 — admission engine (pure)

Files: `internal/resources/admit.go`, `admit_test.go`, `charge.go`, `charge_test.go`.

- `type Lease struct { ID string; Weight int; GrantedAt time.Time; Measured float64 /* EWMA, host % */; Samples int }`; `type Waiter struct { ID string; Weight int; EnqueuedAt, Deadline time.Time }`.
- `func Charge(l Lease, now time.Time, s Settings) float64`: `now − GrantedAt < Warmup` → `Weight`; else `max(Measured, Floor/100 × Weight)` (D-2).
- `func UpdateEWMA(prev float64, sample float64, dt time.Duration, halfLife time.Duration, first bool) float64` (first sample = the sample itself).
- `func Admit(host HostUse, leases []Lease, leaseUse map[string]float64, waiters []Waiter, now time.Time, s Settings) (grant []string, overrun []string)`:
  - `committed = Σ Charge(active)`; `unleased = max(0, host.Measured − Σ leaseUse[active])` (D-2; `leaseUse` is the latest raw measured use of each lease's tree, not the EWMA);
  - walk waiters in FIFO order; a waiter fits when `committed + unleased + w ≤ 100`, or when `w > 100` and there are no active leases; a fitting waiter is granted and its weight added to `committed` for the rest of the walk; a non-fitting one is skipped (a later lighter one may still fit — D-3);
  - a waiter whose `Deadline ≤ now` is granted with overrun, regardless of fit (R6).
  - 〔review #10〕 `host.Available == false` (warming up, three failed samples, unsupported platform): `unleased = 0` — leases still queue against each other's charges (that accounting needs no measurement), unknown work just cannot block, and deadlines still overrun. Never a refusal.
- No I/O, no clock reads (now passed in): table-tested.

Tests: `TestAdmit_Table` — rows: empty host grants; `unleased` blocks (host 80 measured, no leases, w 45 → waits — the "unknown heavy work blocks" property); two `test-full` (45 + 45 + idle 5 → second waits... third waits); warmup charges weight, after warmup charges EWMA, floor applies; a light later waiter passes a heavy earlier one only when the heavy one does not fit; deadline → overrun grant even when full; `w > 100` granted only with no active leases; `leaseUse` larger than host measured → `unleased` 0, not negative; host unavailable with measured 80 → a w 45 waiter fits when no charges, waits behind a 60 charge. `TestCharge_WarmupFloorEWMA`, `TestUpdateEWMA_HalfLife` (after one half-life the step is halfway).
Mutation gates: drop `unleased` → the "host 80" row red; let a later waiter pass when the earlier one fits → the FIFO row red; skip overrun → the deadline row red; no floor → the floor row red.

### P1-2 — store, API, sweeper, WS (two PRs: P1-2a = Tasks 1.3, 1.4, 1.5b; P1-2b = Tasks 1.5, 1.6)

#### Task 1.3 — `resources.db` lease store

Files: `internal/module/resources/store.go`, `store_test.go`.

- SQLite `resources.db` in the data dir (WAL, busy_timeout 5000, like `host_config.db`). Table `resource_leases(id TEXT PK, client_id TEXT UNIQUE, state TEXT CHECK(state IN ('waiting','held','ended')), kind TEXT, weight INTEGER, session_id TEXT, holder_pid INTEGER, holder_start TEXT, scope TEXT CHECK(scope IN ('process','session-new')), tool_use_id TEXT, created_at, deadline_at, lease_until, granted_at, ended_at INTEGER /* unix ms */, overrun INTEGER, end_reason TEXT /* released|cancelled|holder_gone|expired|abandoned|vanished */, waited_ms INTEGER, peak_use REAL, mean_use REAL, ewma REAL, samples INTEGER, empty_samples INTEGER, baseline TEXT /* JSON [{pid, start_unix_ms}], scope session-new */)`; index on `state`. 〔review #7, #8〕 The baseline carries each process's start time (an entry is excluded only when pid **and** start match, so a reused pid is charged); `ewma`／`samples`／`empty_samples` are written on each sampler tick for held rows (a handful of rows per 5 s) so a restart resumes the charge instead of re-warming.
- Methods mirror team's approval store shape (`internal/module/team/store.go:308-407`): `Create(req) (row, created bool)` idempotent on `client_id`; `RenewLease(id, until)` (`MAX`, waiting only); `Grant(id, now, overrun) bool` (CAS `state='waiting'`); `End(id, reason, now) bool` (CAS `state!='ended'`); `CloseIfExpired(id, now)` (CAS re-checks `lease_until` in the same statement, a renewal racing it wins); `ExtendWaiting(until)` (boot grace); `Active() / Waiting()`; `Recent(n)` for `ls` (ended in the last hour incl. overruns); retention: ended rows older than 7 days deleted on the hourly tick.

Tests: `TestStore_CreateIdempotent`, `TestStore_RenewMaxOnly`, `TestStore_GrantCAS` (grant after end → false), `TestStore_CloseIfExpiredLosesToRenew` (renew in between → not closed), `TestStore_BootGrace`, `TestStore_Retention`.
Mutation gate: `RenewLease` without `MAX` → `RenewMaxOnly` red; `CloseIfExpired` without the CAS re-check → `LosesToRenew` red.

#### Task 1.4 — routes

Files: `internal/module/resources/lease_handler.go`, `lease_handler_test.go`, `internal/resources/wire_lease.go`.

- `POST /api/resources/leases` body `{client_id (uuid v4, required), kind?, weight?, wait_s? (default Settings.DeadlineS, max 590), session_id?, holder_pid, holder_start, scope ("process"|"session-new"), tool_use_id?}`. Exactly one of `kind`／`weight` (unknown kind → 400 `unknown_kind`; weight 1–200). `scope=session-new` requires `session_id` and resolves the origin `{pid, proc_start}` from the registry (`ProcessRoots`) — unknown session → falls back to `process` scope on the holder and says so (`scope_fallback: true`). Answer `{id, state, granted, overrun, would_wait?, position?, waited_ms, host: {measured, full}, mode}`; `granted` immediately when it fits (or mode is not `lease`). Idempotent replay returns the same row.
- `GET /api/resources/leases/{id}?wait=N` (N ≤ 25): long-poll until state changes or N s; every call renews `lease_until = now + 30 s` while waiting (`LeaseS`, as team). Held / ended rows answer at once.
- `DELETE /api/resources/leases/{id}` → `End(released)` for held, `End(cancelled)` for waiting; already ended → 200 with the row (idempotent).
- 〔review #11〕 `DELETE /api/resources/leases?client_id=<uuid>` — the same, by the creator's client id: a client that lost the create／poll answer can still release what it may have been granted. Unknown client id → 200 `{"state":"none"}` (nothing to release is not an error).
- 〔review #5〕 **Wake-up protocol.** The module keeps one generation channel `gen chan struct{}`; every state transition (create, grant, end of any reason, boot reconcile) closes it and installs a new one, under `stateMu`. A long-poll takes `stateMu`, reads the row, captures the current `gen`, releases the lock, then waits on `gen`, its `wait` timer or the request context — so a transition between the read and the wait cannot be missed, and nothing is per-row in memory (a restart needs no rebuild: a new poll reads the persisted row). After a wake it re-reads the row and answers if the state moved, else waits again within the remaining time.
- `GET /api/resources` gains `leases` (held: id, kind, weight, charge, use, session_id, age_s, overrun) and `waiters` (id, kind, weight, position, waited_s, deadline_in_s) and `recent` (last 20 ended, for `ls`).
- `mode=off`: POST answers granted without a row; GET/DELETE of an unknown id → 404 `no_lease`.

Tests: `TestLeases_GrantImmediatelyWhenFits`, `TestLeases_WaitThenGrantOnRelease` (two clients; release of the first wakes the second's long-poll within one admission pass), `TestLeases_IdempotentCreate`, `TestLeases_PollRenews`, `TestLeases_DeleteWaitingCancels`, `TestLeases_ModeAdviseGrantsWithWouldWait`, `TestLeases_ModeOffNoRow`, `TestLeases_SessionNewFallsBackToProcess`, `TestLeases_Validation` (table: both kind and weight, neither, weight 0 / 201, bad client_id, wait 600), `TestLeases_DeleteByClientID` (granted row released by client id; unknown id → `none`), `TestPoll_GrantBetweenReadAndWait` (a seam fires a grant right after the row read → the poll returns granted, not after `wait`), `TestPoll_DeleteWakes`, `TestPoll_AbandonWakes`, `TestPoll_AfterRestartObservesPersistedRow` (new module on the same DB: a waiting row's poll waits and then sees its grant).
Mutation gate: capture `gen` after releasing `stateMu` → `GrantBetweenReadAndWait` red.

#### Task 1.5 — admission loop and sweeper

Files: `internal/module/resources/admission_loop.go`, `admission_loop_test.go`, `sampler_loop.go` (per-lease use).

- **One serialization boundary (review #4).** A single module mutex `stateMu` covers every read-decide-write on lease rows: the admission pass holds it from reading `Active()`／`Waiting()` through `Admit` to the last `Grant`; create, delete, every sweeper end, boot reconcile and the per-tick use update take it too. So no transition can land between the snapshot `Admit` decided on and the grants it issues. A `Grant` CAS that still fails (a bug or an external writer) aborts the pass and re-runs it once from fresh rows. DB writes under the lock are single-row statements (WAL, sub-millisecond); long-polls never hold it while waiting (Task 1.4).
- **Admission pass** runs (a) after every sampler tick, (b) after every create / release / cancel / end, (c) **on every 1 s sweeper tick while any waiter exists** (review #3: a deadline is honoured within 1 s even with no sample and no change — mode `off`／`measure` never has waiters). It reads the latest sample only; grants close the generation channel (Task 1.4).
- **Per-lease measured use** (on the sampler tick): scope `process` → the holder pid's tree (`Attribute` with the holder as root, other roots excluded); scope `session-new` → the descendants of the session's CC pid that are **not in the lease's baseline** (pid + start, review #7), with their descendants. 〔review #6〕 A session's concurrent `session-new` leases see one combined new tree (the union of their non-baseline descendants), measured once and **split evenly** among them, so Σ lease use never exceeds the tree; a lease's tree is "empty" for `vanished` when that combined tree is empty. (A tool call has no OS-level marker, so an exact split is not possible; the even split keeps admission's sums exact.) Each lease's EWMA, `peak_use`, `mean_use` updated (the mean feeds P3).
- **Sweeper** (1 s tick, as team's `runSweeper`, `internal/module/team/sweeper.go:21`): waiting rows past `lease_until` → `abandoned`; held rows whose holder is Gone → `holder_gone` — holder liveness from the latest process snapshot (pid absent, or start time differs at second precision; **unknown = alive**, as `LeadPresence`), checked every tick from the cached snapshot (no fork); held rows past `granted_at + MaxHoldS` → `expired`; scope `session-new` rows whose tracked tree has been **empty for 2 consecutive samples after warmup** → `vanished` (catches a mod that crashed between acquire and release). Deadline overruns are granted by the admission pass, not ended.
- **Boot**: `ExtendWaiting(boot + 30 s)`; held rows keep their state and are re-checked by the sweeper on its first tick after the first sample.

Tests: `TestLoop_GrantsOnTick` (host use drops below the threshold → the waiter is granted on the next tick), `TestLoop_OverrunAtDeadline` (stays full → granted at the deadline with `overrun`, `waited_ms ≈ deadline`), `TestSweeper_HolderGone`, `TestSweeper_HolderStartMismatchIsGone`, `TestSweeper_UnknownIsAlive` (snapshot missing start time → held), `TestSweeper_MaxHold`, `TestSweeper_VanishedSessionNew`, `TestSweeper_AbandonedWaiter`, `TestLoop_SessionNewExcludesBaseline` (MCP child present at grant is not charged; a new bash → vitest tree is), `TestLoop_BaselinePidReusedIsCharged` (baseline pid 500 with start A; pid 500 now has start B → charged), `TestLoop_TwoLeasesOneSessionNotDoubleCounted` (two `session-new` leases, one new tree of use 40 → 20 each, Σ 40), `TestLoop_OverrunWithNoSampleNoChange` (sampler stopped by a seam; the 1 s tick grants at the deadline), `TestLoop_SweeperEndDuringPassCannotLeakWeight` (a seam blocks the pass after `Waiting()`; a sweeper end of waiter A must wait for `stateMu`; B behind A is granted in the same or next pass, never blocked by A's weight), `TestLoop_LostGrantCASReruns` (seam makes the first `Grant` CAS fail → one re-run grants the next waiter). `-race` on the package.
Mutation gates: treat unknown as gone → `UnknownIsAlive` red; exclude baseline by pid only → `BaselinePidReusedIsCharged` red; charge each lease the whole new tree → `TwoLeasesOneSession…` red; admission only on sample ticks → `OverrunWithNoSampleNoChange` red; sweeper ends without `stateMu` → `SweeperEndDuringPass…` red; no vanished rule → `VanishedSessionNew` red.

#### Task 1.5b — module wiring and boot (review #2, #8, #9)

Files: `internal/module/resources/module.go`, `module_test.go`, `boot.go`, `boot_test.go`.

- **Dependencies** `peers`, `hostconfig` (the settings reader, `resources.SettingsKey`).
- **Init**: open `resources.db` (data dir; a failure is logged and the module runs measure-only — lease routes answer the `mode=measure` grant; the daemon never fails for it); look up the roots source and the settings reader.
- **RegisterRoutes**: `GET /api/resources`, `POST /api/resources/leases`, `GET|DELETE /api/resources/leases/{id}`, `DELETE /api/resources/leases?client_id=`.
- **Start**, in this order: (1) boot reconcile under `stateMu` — `ExtendWaiting(boot + 30 s)` (`MAX`, as team `store.go:395-408`); held rows load their persisted `ewma`／`samples`／`empty_samples`; (2) the sampler loop; (3) the 1 s sweeper + admission tick; (4) the WS subscribe hook. Until the first sample, admission runs with `Available=false` (Task 1.2 rule): persisted held rows still count by charge, persisted waiters whose deadline has passed overrun on the first tick, holder liveness is **not** judged (no snapshot yet → unknown = alive) until the first snapshot exists.
- **Stop**: cancel both loops, join, then close the DB; idempotent.
- **Settings live** (review #9): `Effective()` re-read through the hostconfig reader on every sampler tick and on every POST (one indexed SQLite read; nothing to subscribe to — the hostconfig PUT has no change hook, `internal/module/hostconfig/handler.go:108-163`). Mode transitions take effect within one tick: `off` → the sampler skips its reads (the ticker keeps running, so turning it back on needs no restart), `measure` → sampling on, POST grants without a row, existing waiters are granted (not overrun) on the next pass, `advise`／`lease` → as Task 1.1.

Tests: `TestModule_WiresRoutesAndLoops` (POST → GET → DELETE through the mux), `TestModule_StopJoinsAndClosesDB`, `TestModule_DBOpenFailureRunsMeasureOnly`, `TestBoot_ExtendsWaitingGrace`, `TestBoot_HeldResumesCharge` (persisted `ewma 30, samples 9` → charge 30 right after boot, not the weight), `TestBoot_PastDeadlineOverrunsOnFirstTick`, `TestBoot_NoLivenessBeforeFirstSnapshot` (a held row whose holder is gone is not ended until the first snapshot, then ended), `TestSettings_ModeTransitions` (lease → off → lease without restart; lease → measure releases waiters as grants), `TestSettings_WeightChangeAppliesToNextPOST`.

#### Task 1.6 — WS `resources.changed`

Files: `internal/module/resources/events.go`, `events_test.go`; `internal/resources/wire_lease.go` (`EventType = "resources.changed"`).

- Through the existing `core.Events.BroadcastEvent` (as team, `internal/module/team/module.go:457-466`; no change to `internal/core/events.go`). Value: the same JSON as `GET /api/resources` without `recent`.
- Throttle: at most one per 5 s; sent on the sampler tick **only while something is held or waiting**, plus once right after a grant / end / create (coalesced into the 5 s window: a change inside the window schedules one trailing send at the window's end); nothing while idle. A new subscriber gets one snapshot event on connect (as team's `sendSnapshot`).

Tests: `TestEvents_ThrottledToFiveSeconds` (10 changes in 1 s → 1 immediate + 1 trailing), `TestEvents_SilentWhenIdle`, `TestEvents_SnapshotOnSubscribe`.

### P1-3 — CLI and skill

#### Task 1.7 — `pdx lease acquire／release`

Files: `cmd/pdx/lease_cmd.go`, `lease_cmd_test.go`.

- `pdx lease acquire (--kind K | --weight N) [--wait 5m] [--session <sid>] [--tool-use <id>] [--holder-pid P] [--client-id <uuid>] [--json]`: POST (client id = `--client-id` when given — the mod's, review #11 — else a fresh uuid v4; `daemonclient.Idempotent()`), then the loop of `cmd/pdx/lead.go:195-228` (25 s polls, 35 s attempt timeout, 3 hung polls → unavailable) until granted; prints one JSON line `{id, granted, overrun, waited_ms, host_measured}`. `--session` given → `scope=session-new` (the mod's call); else `process` scope with `--holder-pid` (default: the parent pid of `pdx`, i.e. the shell that will run the command). SIGINT／SIGTERM → best-effort `DELETE` (3 s), exit 12 (as `lead`). **Fail open**: daemon unreachable／4xx／5xx／3 hung polls → print `{"granted":true,"fail_open":"<reason>"}` and exit 0 (the caller runs the command; D-5).
- `pdx lease release (<id> | --client-id <uuid>)`: DELETE (by id, or by client id — review #11), 3 s, errors → stderr line, exit 0 (release is best effort; the sweeper is the backstop).

Tests: `TestRelease_ByClientID`, `TestAcquire_GrantedImmediately`, `TestAcquire_PollsUntilGranted`, `TestAcquire_FailOpenWhenDaemonDown` (no listener → exit 0, `fail_open`), `TestAcquire_FailOpenOnHungPolls`, `TestAcquire_SIGINTCancels`, `TestRelease_BestEffort`.
Mutation gate: exit non-zero on a down daemon → `FailOpenWhenDaemonDown` red.

#### Task 1.8 — `pdx lease run` and full `ls`

Files: `cmd/pdx/lease_run.go`, `lease_run_test.go`, `cmd/pdx/lease_cmd.go` (`ls`).

- `pdx lease run (--kind K | --weight N) [--wait 5m] -- <cmd…>`: acquire with `--holder-pid = os.Getpid()` (the `pdx` process holds; scope `process` — the command is its child, so the lease's tree is exactly the command); then `exec.Command` the child with stdio inherited, own process group; forward SIGINT／SIGTERM／SIGHUP to the group; exit with the child's code (signal → 128+n). Release on exit (deferred, also after a signal). When it waited ≥ 1 s, one stderr line before the child starts: `pdx lease: 等了 37 秒主機資源（負載 72/100）` and, on overrun, `pdx lease: 等滿 5 分鐘，超量放行（已記錄）`. Fail open → stderr `pdx lease: daemon 連不上，直接執行` and run.
- `ls` (full): adds `HOLDERS` (session, kind, weight, charge, use, age) and `WAITERS` (position, kind, weight, waited, deadline in) sections and `RECENT` (last ended, with `超量` marked for overruns — R6 "visible in `pdx lease ls`").

Tests: `TestRun_ExitCodePassthrough`, `TestRun_SignalForwarded` (child traps SIGTERM and exits 7 → `pdx` exits 7), `TestRun_ReleaseAfterChildExit` (fake daemon sees DELETE), `TestRun_WaitedLine`, `TestRun_FailOpenRuns`, `TestLs_HoldersWaitersRecentOverrun`.

#### Task 1.9 — skill `pdx-lease` and embed list

Files: `cmd/pdx/plugin/purdex/skills/pdx-lease/SKILL.md`, `cmd/pdx/plugin/embed_test.go` (file list).

- Text (Chinese, short): what counts as heavy (D-7 table), "wrap heavy commands you run outside the mod's reach in `pdx lease run --kind <k> --`" (P1 — before P2 ships, all heavy commands), "a wait is the host being full, not a hang: do not retry, do not kill", "never `run_in_background` a heavy command to dodge the queue", `pdx lease ls` to look. Once P2 ships (Task 2.4) the skill says the mod covers foreground Bash and the coordinator-slot rule is retired.
- `pdx setup` already deploys the plugin tree; the skill ships with it (no setup code change).

Tests: embed list includes the skill; `TestSkill_LeaseMentionsRun` (pin the `pdx lease run` line).

#### P1 acceptance and deploy

1. Lead deploys daemon + CLI and runs `pdx setup --agent cc` (backup `~/.claude/settings.json` first, never printed).
2. δ, throwaway: in two shells `pdx lease run --weight 60 -- sleep 30` and `pdx lease run --weight 60 -- sleep 5` → the second waits until the first ends (stderr line), `ls` shows holder + waiter.
3. Overrun: `PUT /api/hostconfig/resources` with `deadline_s: 10` (restore after), two `--weight 90` runs → the second overruns at ~10 s and `ls` shows it under RECENT as 超量.
4. Holder gone: `kill -9` a `pdx lease run` → its row ends `holder_gone` within ~1 s of the next sample.
5. Mode `advise` and `off` each once (`would_wait`, no row); restore `lease`.

---

## P2 — mod interception

### Task 2.1 — classifier (pure, in the mod)

File: `cmd/pdx/plugin/purdex/hooks/lease.js` (top-level functions), `hooks/lease.test.ts`.

- `classify(command) → {kind, rewrite?}` or `null`. Split the command on `&&`, `||`, `;`, `|` (outside quotes; a parse failure → `null`, i.e. not intercepted — fail open); strip leading `cd <dir>`, env assignments (`FOO=bar`), and `timeout N`. A segment matches when (D-7):
  - `test-full`: `(npx|pnpm( exec)?|yarn)? vitest run` (or `vitest` with `run` anywhere in its args) with **no** positional path argument, no `-t`/`--testNamePattern`, no `--project` filter; `go test` whose package args include `./...` or `...` and no `-run`; `make test`.
  - `build`: `pnpm (run )?build`, `npm run build`, `tsc -b`, `vite build`, `pnpm (run )?electron:build`, `electron-vite build`.
  - `test-pkg`: `go test -race <pkg>` with exactly one package path not ending in `...` and no `-run`.
  - `lint-full`: `go vet ./...`, `eslint .`, `pnpm (run )?lint`.
  - The heaviest matching segment decides the kind.
- Not intercepted: any segment already invoking `pdx lease run`; `e.run_in_background === true` (R8); a command the classifier cannot parse.
- `rewriteMaxWorkers(command)` (R7): for each `test-full` vitest segment with no `--maxWorkers`/`--max-workers`/`--poolOptions…maxThreads`, append ` --maxWorkers=3` to that segment (before a trailing `| …` pipe or `2>&1`); returns `{command, changed}`.

Tests (`lease.test.ts`, table; ~60 rows from §3 "Heavy commands seen in this repo" and the facts table): `cd spa && npx vitest run` → test-full + rewrite; `npx vitest run src/lib/foo.test.ts` → null; `npx vitest run -t "x"` → null; `npx vitest run --maxWorkers=3` → test-full, no rewrite; `go test -race ./...` → test-full; `go test -race ./internal/module/team/` → test-pkg; `go test ./cmd/pdx -run TestX` → null; `make test` → test-full; `cd spa && pnpm run build` → build; `pnpm run electron:build` → build; `go vet ./...` → lint-full; `pdx lease run --kind build -- pnpm run build` → null; `echo "npx vitest run"` → null (quoted, not a command); `npx vitest run 2>&1 | tail -30` → rewrite lands before `2>&1`; `FOO=1 npx vitest run` → test-full; unparseable quotes → null.
Mutation gates: drop the `-t` exclusion → its row red; append `--maxWorkers=3` after the pipe → the pipe row red; intercept `run_in_background` → the hook test (2.2) red.

### Task 2.2 — the hook

Files: `hooks/lease.js` (`registerLease(on)`), `hooks/lease.test.ts`, `hooks/register.js` (import + one call), `cmd/pdx/plugin/embed_test.go` (file list, pinned registration).

- `on('tool.call', { tool: 'Bash' }, async ($, e, next) => …)` (matcher required: `events.js` already holds the unmatched `tool.call`; the guard test is fail-closed, `cmd/pdx/plugin/embed_test.go:357-382`). `$` is used only in top-level function declarations of `lease.js` (M-U1-5).
- Flow: not heavy / background / already wrapped → `return next(e)`. Heavy → (a) rewrite (R7) if needed; (b) 〔review #1〕 `sid = await $.session.id()` **in this call** (as `ask.js:171`; no module state — a `/clear` or resume is picked up by the next call, and `tool.call`'s `e` carries no session id), `clientId` = a uuid v4 from `crypto.getRandomValues` (as `events.js:88-95` builds its stream id; where the environment has no `crypto` — `events.js:281` — the hook omits `--client-id` and releases by the returned id only); (c) `acquire = await $.process.run([cfg.bin, 'lease', 'acquire', '--kind', kind, '--session', sid, '--tool-use', e.tool_use_id, '--client-id', clientId, '--json', ...configArgs], { timeoutMs: 600_000 })` (cfg from `pdx.json` as `ask.js:45-62`; the daemon's deadline is ≤ 590 s, so the 10 min `$.process.run` cap is never the binding limit); a reject, non-zero exit or junk → treat as `fail_open`; (d) `r = await next(rewritten e)`; (e) `finally`: release **by client id** (`pdx lease release --client-id <clientId>`, 5 s, errors swallowed) whenever an acquire was started — granted, fail-open or junk alike (review #11: a grant whose answer was lost is released too; a client id the daemon never saw answers `none`); (f) return `{...r, context: [...(r.context ?? []), note]}` when there is anything to say, else `r` unchanged.
- Residual leak paths (review #11) and their backstop: the CC process dies between acquire and release → `holder_gone` (scope `session-new` holds on the CC origin); the mod is torn down mid-call while CC lives (plugin reload) → `vanished` once the new tree is empty after warmup, else `max_hold`.
- `note` (Chinese, one line each, only the parts that apply): rewritten → `Purdex mod 把 --maxWorkers=3 加進這個指令（主機資源規則 R7），實際執行的是：<command>`; waited ≥ 1 s → `這個指令先等了 37 秒主機資源（負載 72/100），不是卡住，不要重試`; overrun → `等滿 5 分鐘超量放行，已記錄`; fail open → nothing (silence; the daemon may be down for a restart).
- `.catch(($, e, next) => next(e))` — in a `.catch` handler `next` is replay-safe (`next.called` ⇒ the settled result, nothing beneath runs again; the U1-1 plan's citation, d.ts L1158–1170), so a throw after the tool ran never re-runs it, and a throw before `next` runs the command unchanged (fail open).
- Subagent calls (`e.agentId`) are intercepted the same way (the lease is the session's).
- `register.js`: `import { registerLease } from './lease.js'` and `registerLease(on)` **after** `registerEvents(on)` (the reporter's unmatched hook wraps ours, so its `tool.start`／`tool.end` time includes the wait — the wait is part of the call the person sees). Told to the lead and the interface lead before the PR (cross-line rule).

Tests (kit, fake `process.run` recording argv and answering, fake tool beneath counting runs): `heavy foreground Bash acquires, runs the rewritten command once, releases`, `background Bash is not intercepted`, `non-heavy Bash passes through untouched (no process.run)`, `acquire fails → command runs unchanged except the rewrite, no release call`, `release runs even when the tool throws`, `a throw after next never re-runs the tool`, `context names the rewrite and the wait`, `subagent Bash is intercepted`, `already wrapped in pdx lease run → untouched`, `after /clear the lease carries the new session id` (`$.session.id()` fake changes between two calls → the second acquire argv has the new sid), `acquire answers junk → the command runs and release --client-id is still called with the same client id`, `acquire times out → release --client-id is still called`. Plus `relay.test.ts` and `events.test.ts` stay green with `lease.js` registered (the existing reporter-on runs now pass through one more hook).
Guard (review #12 — exact edits in `cmd/pdx/plugin/embed_test.go`): the file list (`:15`) gains `hooks/lease.js`; the scanner's allowed-call regex (`:55-65`, today `registerAsk|registerEvents`) gains `registerLease`, and its error text and the positive scanner fixtures (`:105-110`) are updated to name it; the registration table (`:379-382`) gains `{"hooks/lease.js", "tool.call", "{ tool: 'Bash' }"}`; `register.js` pins the import and `registerLease(on)`. New cases: `TestScanRegistrations_AllowsRegisterLease`, and an injected `registerLeas(on)` typo still fails closed.
Mutation gates: release outside `finally` → `release runs even when the tool throws` red; drop the background check → its test red; unmatched registration → guard red.

### Task 2.3 — real-session check (M-R4)

- Throwaway tmux session + `claude --model haiku` (interactive this time, not `-p`) with a `--plugin-dir` copy of the built plugin, in a scratch git checkout of the repo: ask for `cd spa && npx vitest run src/lib/<one small>.test.ts` (not intercepted) and a fake heavy command made cheap by a temporary custom kind (`PUT resources` with `kinds: {"test-full": 95}` and a `pdx lease run --weight 90 -- sleep 40` holding the host from another shell) → the session's Bash waits, then runs; context line seen in the transcript; `ls` shows the session as holder. Record in spec §3.1.

### Task 2.4 — retire the slot rule

- `skills/pdx-lease/SKILL.md`: foreground heavy Bash is covered by the mod; only scripted heavy work needs `pdx lease run`. The lead updates `brief-common.md` (its file) to drop "ask the coordinator for a full-vitest slot" — δ proposes the text in the PR body.

#### P2 acceptance and deploy

Lead deploys the mod (`pdx setup --agent cc`); new sessions pick it up (running sessions keep the old mod until restart — said in the deploy note). δ watches `pdx lease ls` during the next real full vitest from any session.

---

## P3 — learning

### Task 3.1 — learned weight per kind

Files: `internal/module/resources/learn.go`, `learn_test.go`, `store.go` (`kind_stats(kind PK, mean REAL, n INTEGER, updated_at)`), `internal/resources/admit.go` (weight lookup).

- On each lease end with ≥ 3 samples after warmup: `kind_stats.mean ← EWMA(mean, lease.mean_use, α = 0.2)`, `n++`. The effective default weight of a kind = `clamp(round(mean × 1.2), 0.5 × configured, 1.5 × configured)` once `n ≥ 5`, else the configured weight. Explicit `--weight` is never adjusted.
- `pdx lease ls` shows per kind: configured, learned, n.

Tests: `TestLearn_NeedsFiveSamples`, `TestLearn_BoundedToHalfAndOneAndHalf`, `TestLearn_ExplicitWeightUntouched`.

### Task 3.2 — per-kind concurrency cap

- Setting `caps: {"test-full": 1}` (0 = none, default none): admission treats a waiter whose kind is at its cap as not fitting (still subject to the deadline overrun — R3/R6: no hard refusal).

Tests: `TestAdmit_CapHoldsSecondTestFull`, `TestAdmit_CapStillOverrunsAtDeadline`.

---

## App contract (for the interface lead, `mlab/_b84f5i`)

- **Read:** `GET /api/resources` → `Snapshot` (Task 0.1 + Task 1.4 additions). Fields the App needs: `available`, `host.measured` (0–100), `host.full`, `host.pressure`, `host.cpu`, `host.mem`, `sessions[] {session_id, use, cpu, mem, rss_bytes}`, and from P1 `leases[] {kind, weight, charge, use, session_id, age_s, overrun}`, `waiters[] {kind, weight, position, waited_s, deadline_in_s}`.
- **Push:** WS host event `resources.changed`, value = the snapshot without `recent`, ≤ 1 per 5 s, only while held／waiting (plus a snapshot on subscribe). While idle the App polls `GET /api/resources` if it shows the indicator continuously (suggested 15 s; the daemon samples every 5 s anyway).
- **Write:** `PUT /api/hostconfig/resources` (P1-1) if the App wants a settings page.
- Placement, look, and whether per-session use appears on tabs are the interface lead's call; P0 is enough to draw a host indicator.

## Decisions for the lead

1. **Default `mode` after P1 deploys: `lease`** (proposed). P1 is voluntary only, so `lease` changes nothing for sessions that do not call `pdx lease`; P2 then intercepts immediately on its deploy. Alternative: ship P2 under `advise` for a day to watch `would_wait` before turning `lease` on.
2. **Memory metric (M-R1):** keep D-1's vm_stat formula (stricter; ≈ 25 points above `100 − memorystatus_level` at load 17). With it and R5's 90 %, the host reads "full" whenever pressure goes to warn (seen once tonight). If that proves too eager in P0's real numbers, P0 acceptance step 2 gives the data to revisit.
3. **`vanished` release** (Task 1.5) is an addition to D-4: a mod lease whose tracked tree stayed empty for two samples after warmup ends. It only shortens a leaked lease (mod crashed between acquire and release); it cannot end a running command's lease while its processes exist.
4. **Registry, not frames, for session roots** (Task 0.5): every CC session (incl. outside tmux) and no change in `internal/module/agent`; codex frames are not leasing sessions.

## Plan review fold-in (codex `task-muzq7z7x-128h3c`, 2026-10-09)

All 14 findings accepted (none rebutted); the tasks above carry the fixes.

| # | Sev | Finding | Disposition |
|---|---|---|---|
| 1 | crit | P2 `sid` undefined; `/clear`／resume not covered | Task 2.2: `sid = await $.session.id()` inside the hook on **every** call (as `ask.js:171`), so a `/clear` or resume is picked up with no module state; test `after /clear the lease carries the new session id` |
| 2 | crit | No task wires store／routes／loops into the module lifecycle | New **Task 1.5b** (Init opens `resources.db`, RegisterRoutes, Start／Stop, depends on `hostconfig`) |
| 3 | crit | Deadline overrun only on a sample tick or a change | Task 1.5: the 1 s sweeper tick also runs the admission pass whenever a waiter exists; test `overrun at the deadline with no sample and no change` |
| 4 | crit | Admission and sweeper not one serialization boundary | Task 1.5: one module mutex `stateMu` held across read → `Admit` → every `Grant`; every transition (create, delete, sweeper end, boot reconcile) takes it; a lost CAS re-runs the pass; tests named there |
| 5 | imp | Long-poll lost wakeup, cancel／abandon／restart | Task 1.4: generation channel (closed and replaced on every transition, captured under `stateMu` with the row read); tests for each path |
| 6 | crit | `session-new` leases of one session double-count | Task 1.5: the session's new tree is measured once and **split evenly** among its concurrent `session-new` leases (Σ stays exact); test `two concurrent leases in one session are not double-counted` |
| 7 | imp | `baseline_pids` without identity | Task 1.3／1.5: baseline is `[{pid, start}]`, excluded only when both match; test with a reused pid |
| 8 | imp | Restart semantics of held／waiting rows | Task 1.5b: boot order spelled out; `ewma`, `samples`, `empty_samples` persisted; tests named there |
| 9 | imp | Settings not applied live; missing dependency | Task 1.5b: dependency `hostconfig`; settings re-read on every tick and every POST; mode transition tests |
| 10 | imp | Admission when the sample is unavailable | Task 1.2: `Available=false` → `unleased = 0` (leases still queue against each other, deadlines still overrun); test row |
| 11 | imp | Acquire granted but the answer lost → leaked lease | Task 1.4 `DELETE …?client_id=`; Task 1.7 `--client-id`; Task 2.2 generates the client id in the hook and releases by it in `finally` when no id came back; tests named there |
| 12 | imp | Guard allow-list hard-codes `registerAsk|registerEvents` | Task 2.2 lists the exact `embed_test.go` changes (regex `:55-65`, messages and fixtures `:105-110`) |
| 13 | imp | No-fork test needs a seam; `StartTime` string vs `time.Time` | Task 0.5: `readRegistry` package var seam; new `ProcessSnapshot.Start(pid) (time.Time, error)` (no argv read); tests for unreadable start and non-ENOENT `Stat` |
| 14 | minor | `pdx team` MEM unit vs D-1 | Task 0.8: both CPU and MEM are host % (D-1 units); RSS stays in `pdx lease ls` |
