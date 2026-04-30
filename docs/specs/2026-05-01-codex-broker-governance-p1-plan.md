# Plan — Codex broker governance P1 (Inventory, read-only)

**Spec**: `docs/specs/2026-05-01-codex-broker-governance-spec.md`
**Date**: 2026-05-01
**Branch**: `worktree-governance-p1-inventory`
**Baseline**: origin/main `c02299b7` (alpha.279)
**Scope**: implement Phase P1 only. P2/P3 are designed in the same spec but ship in subsequent worktrees.

This plan implements PR-A (per spec §9). PR-B (Lights L2) is independent and not part of this plan.

---

## 1. Goal

Ship a Go package `internal/codexbroker` and one HTTP endpoint that enumerates every codex broker process / state directory / socket directory visible on the host, returning a structured inventory with full attribution and a closed-list anomaly schema. **Read-only**: zero side effects on broker processes or filesystem state.

Acceptance is defined entirely by spec §4.4 (AC1–AC12).

## 2. Non-goals

- No process signalling (graceful or otherwise).
- No socket / state-dir cleanup.
- No automatic triggers (boot reconcile, tick, ExitWorktree hook).
- No SPA UI.
- No quarantine.json or audit dumps.
- No launch-registry persistence.

These are P2/P3 work.

## 3. Architecture

```
internal/codexbroker/
  doc.go                  package overview, links spec
  types.go                BrokerRecord, JobCounts, Anomaly, SourceMask, AnomalyCode constants
  paths.go                resolve plugin data root, state-dir glob, cxc-* glob; pure functions
  fs.go                   FS interface (Open/Read/Stat/Lstat/EvalSymlinks/Glob); osFS impl; recording FS for tests
  process.go              ProcessLister interface; psLister impl (calls ps); fakeLister for tests
  argv.go                 parse `--cwd` / `--endpoint` / `--pid-file` from argv string; pure
  hash.go                 brokerKey computation: EvalSymlinks + canonical-case + NFC + sha256[:16]
  scan_process.go         enumerate broker processes via ProcessLister
  scan_state.go           enumerate state dirs with broker.json
  scan_socket.go          enumerate cxc-* dirs
  reconcile.go            merge three sources by (brokerKey, pid, lstart); emit anomalies
  inventory.go            Scanner type tying it all together; singleflight for handler
  handler.go              HTTP handler: GET /api/codex/brokers
  module.go               module wiring + RegisterRoutes(mux)

  testdata/
    state/                state-dir fixtures
      live/               broker.json + state.json + jobs/ (live broker fixture)
      historical/         state.json + jobs/ only (no broker.json)
      malformed/          truncated broker.json
      perms/              unreadable dir (chmod 000 in test setup)
    cxc/                  socket-dir fixtures
      live-pid/           broker.pid → live pid
      dead-pid/           broker.pid → dead pid
      orphan/             no matching state dir
    ps/                   canned ps stdout for table tests
      one-broker.txt
      duplicate-runtime.txt
      argv-truncated.txt
      no-broker.txt

  *_test.go               unit tests
  inventory_live_test.go  integration (build tag `integration`)
```

**Wiring**: a new module struct registered in daemon `cmd/pdx/main.go` analogous to how `internal/module/agent` registers (spec §4.3 confirms route shape).

**No persistence**: P1 is stateless — every `GET /api/codex/brokers` is a fresh scan. No config keys are added.

## 4. Task breakdown

Tasks are ordered to minimise rework. Each task is small enough for a single TDD cycle (test → impl → verify), commit boundary annotated.

### Task A — Package skeleton + types

**Files**: `doc.go`, `types.go`, `types_test.go`

**Scope**:
- Define `BrokerRecord`, `JobCounts`, `Anomaly`, `SourceMask`, `SourceProcess`/`SourceStateDir`/`SourceSocket` constants per spec §4.1.
- Define `AnomalyCode` typed string + the closed-list constants from spec §4.2 (15 codes).
- **Maintain `AllAnomalyCodes []AnomalyCode`** in `types.go` as the canonical literal slice. `Anomaly.Code` is typed `AnomalyCode` (not free-form string) so non-listed codes cause compile errors.
- Stringer / JSON marshalling for `SourceMask` and `AnomalyCode`.

**Acceptance**:
- Compile cleanly.
- `TestAnomalyCodes_ClosedList`: asserts `AllAnomalyCodes` has exactly 15 entries (spec §4.2 count) and contains a hard-coded literal expected slice (one line per code copied verbatim from spec). Adding a new code without updating spec + this literal list breaks the test.
- `TestAnomalyCodes_Unique`: asserts every code in `AllAnomalyCodes` is unique (no duplicates).
- `TestBrokerRecord_RoundTrip`: round-trips `BrokerRecord` through `encoding/json` without loss.

**Verification**: `go test ./internal/codexbroker/...` (just compiles + types tests pass).

**Commit**: `feat(codexbroker): add BrokerRecord types + closed anomaly set (P1 task A)`

---

### Task B — `paths.go`: plugin data root + globs

**Files**: `paths.go`, `paths_test.go`

**Scope**:
- Function `pluginDataRoot()` returning `~/.claude/plugins/data/codex-openai-codex/state` (override via env `PDX_CODEX_STATE_ROOT` for tests).
- Function `socketGlobRoots()` returning `[$TMPDIR, /var/folders/*/T]` (deduped, env override for tests).
- All pure, no I/O.

**Acceptance**:
- `TestPaths_DefaultRoots` checks defaults.
- `TestPaths_EnvOverride` checks `PDX_CODEX_STATE_ROOT` override.

**Commit**: `feat(codexbroker): plugin data root + socket-glob path resolution (P1 task B)`

---

### Task C — FS abstraction

**Files**: `fs.go`, `fs_test.go`

**Scope**:
- Minimal interface `FS` with only the operations actually needed: `Open(path) (io.ReadCloser, error)`, `Stat(path) (FileInfo, error)`, `Lstat(path) (FileInfo, error)`, `EvalSymlinks(path) (string, error)`, `Glob(pattern) ([]string, error)`, `ReadDir(path) ([]DirEntry, error)`.
- `osFS` implementation calling `os.*` and `filepath.*` directly. **No claim of context-awareness**: `os.Stat` etc. are not cancellable; deadline is enforced at the orchestrator level by goroutine + `context.AfterFunc`, not by passing ctx into every fs call.
- `RecordingFS` test helper that wraps an underlying FS and records every method call (path, op). Used by Task K to assert read-only.

**Why a custom interface, not `io/fs.FS`**: stdlib `fs.FS` is rooted (no abs paths) and has no `EvalSymlinks` / `Glob` / `Lstat`. We need all of those. The interface stays minimal — six methods.

**Acceptance**:
- `TestOsFS_BasicOps` against a temp dir.
- `TestRecordingFS_Records` confirms call recording matches expected access pattern.
- `TestRecordingFS_RejectsMutation` (forward-looking: if any mutation method is added in future, recording fs explicitly fails the test). Currently the interface has zero mutation methods, but the test guards against drift.

**Commit**: `feat(codexbroker): FS interface + osFS + RecordingFS (P1 task C)`

---

### Task D — Process lister abstraction

**Files**: `process.go`, `process_test.go`

**Scope**:
- Interface `ProcessLister` with one method: `List(ctx) ([]RawProcess, error)` returning rows of `(pid, ppid, lstart, rss, cmdline)`.
- `psLister` impl: invokes `ps -eo pid=,ppid=,lstart=,rss=,command=` with context-aware exec; parses each line.
- `fakeLister` test helper backed by canned text fixtures from `testdata/ps/`.
- Filtering to `app-server-broker.mjs serve` is done in scan_process.go (Task F), not here.
- `lstart` parser handles macOS `Thu May  1 13:14:15 2026` format and Linux variants; failure → `lstart_unparseable`-grade error returned per row, not whole-scan failure.

**Acceptance**:
- `TestPsLister_ParsesMlabFormat` against canned fixture.
- `TestPsLister_LstartFormats` parameterised over macOS + Linux samples.
- `TestPsLister_RespectsContext` cancels mid-read and asserts caller sees `ctx.Err()`.

**Commit**: `feat(codexbroker): ProcessLister interface + psLister + canned fixtures (P1 task D)`

---

### Task E — Argv parser + brokerKey hash

**Files**: `argv.go`, `argv_test.go`, `hash.go`, `hash_test.go`

**Scope**:
- `parseBrokerArgv(cmdline string) (BrokerArgv, error)`: extracts `--cwd`, `--endpoint`, `--pid-file`. Handles quoted values, escaped spaces. Returns `argv_truncated` sentinel error when cmdline is suspect (no `--cwd` after `serve` keyword). Implementation choice: prefer `github.com/mattn/go-shellwords` (already in go.mod) over hand-rolled parser to reduce LOC and edge-case bugs.
- `brokerKey(rawCwd string, fs FS) (key string, resolved string, anomaly *AnomalyCode)`: applies `fs.EvalSymlinks` → canonical-case → NFC → sha256 → hex[:16]. Returns `cwd_unresolvable` anomaly with raw-cwd-based key when EvalSymlinks fails. **All path operations go through the injected `FS` interface** so tests can mock `EvalSymlinks` mappings without touching real filesystem (decouples from APFS specifics).
- Case-fold detection via `unix.Pathconf` (`golang.org/x/sys/unix`) for `_PC_CASE_SENSITIVE`. On non-Darwin/non-Linux, default case-sensitive. Build-tagged: `hash_darwin.go`, `hash_linux.go`, `hash_other.go`.

**Acceptance** (all tests use fake `FS` injecting `EvalSymlinks` mappings; no real fs dependency):
- `TestParseArgv_StandardForm` against the literal mlab observation.
- `TestParseArgv_QuotedCwd` with cwd containing spaces.
- `TestParseArgv_TruncatedReturnsAnomaly`.
- `TestBrokerKey_PrivateVarSymlink`: fake FS maps both `/var/foo` and `/private/var/foo` to `/private/var/foo`; assert identical key.
- `TestBrokerKey_CaseFold_OnInsensitiveVolume`: fake FS reports case-insensitive; assert `/Foo` and `/foo` produce same key.
- `TestBrokerKey_CaseFold_OnSensitiveVolume`: fake FS reports case-sensitive; assert `/Foo` and `/foo` produce different keys.
- `TestBrokerKey_NFCNormalised`: NFD vs NFC unicode inputs hash identically.
- `TestBrokerKey_UnresolvableReturnsAnomaly`: fake FS returns error from EvalSymlinks; key derived from raw cwd; anomaly = `cwd_unresolvable`.

**Commit**: `feat(codexbroker): argv parser + canonical brokerKey hash (P1 task E)`

---

### Task F — Process scanner

**Files**: `scan_process.go`, `scan_process_test.go`

**Scope**:
- `scanProcesses(ctx, lister ProcessLister, fs FS) ([]processCandidate, []Anomaly, error)`.
- Filters lister output to rows whose cmdline matches the broker pattern.
- For each broker row: parse argv (Task E), compute brokerKey, populate `processCandidate{pid, ppid, lstart, rss, rawCwd, cwdResolved, endpoint, pidFile, anomalies}`.
- Per-row failures (argv truncated, brokerKey unresolvable) become anomalies on the candidate, not whole-scan failures.

**Acceptance**:
- `TestScanProcesses_OneBroker` against canned ps + fake fs.
- `TestScanProcesses_DuplicateRuntime` returns two candidates with identical brokerKey.
- `TestScanProcesses_ArgvTruncated` produces candidate with anomaly, not skipped.
- `TestScanProcesses_NoBrokers` returns empty without error.

**Commit**: `feat(codexbroker): process layer scanner (P1 task F)`

---

### Task G — State-dir scanner

**Files**: `scan_state.go`, `scan_state_test.go`

**Scope**:
- `scanStateDirs(ctx, fs FS, root string) ([]stateCandidate, []Anomaly)`.
- Glob `root/*` directories. For each:
  - If `broker.json` absent → skip (historical, out of scope per spec §1).
  - Read & parse `broker.json` (5 fields per spec §3.2). Malformed → `broker_json_unreadable` anomaly + skip dir.
  - Read & parse `state.json` (subset per spec §3.2). Malformed → `state_json_unreadable` anomaly; emit candidate with empty JobCounts.
  - Roll up `JobCounts` (queued/running/completed/failed/cancelled/unknown) and `LastJobUpdatedAt`.
  - brokerKey is the directory suffix (post-`-`, last 16 hex).
- Per-dir read budget 100ms (context with timeout); on exceed → `state_json_unreadable` or `broker_json_unreadable` anomaly + skip.

**Acceptance**:
- `TestScanStateDirs_Live` against fixture dir with broker.json + state.json + jobs.
- `TestScanStateDirs_Historical_Skipped` against fixture without broker.json.
- `TestScanStateDirs_MalformedBrokerJSON` produces anomaly, dir skipped, neighbouring dirs unaffected.
- `TestScanStateDirs_StateJSONReadFails_RecordStillEmitted` confirms record present with zero JobCounts and `state_json_unreadable` anomaly.
- `TestScanStateDirs_PerDirTimeout` injects delay > budget; exceeded dir becomes anomaly, others succeed.
- `TestScanStateDirs_JobCountsByStatus` covers all six status buckets including `unknown`.
- `TestScanStateDirs_LastJobUpdatedAt_MaxOfJobs` (drives **AC5**): fixture with three jobs at distinct `updatedAt`; assert returned value is the max.
- `TestScanStateDirs_LastJobUpdatedAt_NilWhenNoJobs` (drives **AC5**): fixture with empty `jobs[]`; assert nil.
- `TestScanStateDirs_LastJobUpdatedAt_NilWhenStateUnreadable` (drives **AC5**): fixture with malformed state.json; assert nil + anomaly.

**Commit**: `feat(codexbroker): state-dir scanner with per-dir budget (P1 task G)`

---

### Task H — Socket-dir scanner

**Files**: `scan_socket.go`, `scan_socket_test.go`

**Scope**:
- `scanSockets(ctx, fs FS, roots []string) ([]socketCandidate, []Anomaly)`. **Pure read of fs**: glob, ReadFile (for `broker.pid`), Stat (for `broker.sock`). Per-dir budget 50ms.
- For each `cxc-*/` dir:
  - Read `broker.pid` (single integer line).
  - Stat the socket file presence.
  - Populate `socketCandidate{sockDir, pidFromFile, sockExists}`.
- **Live/dead pid classification is NOT done here** — that requires the process-scan result and is performed in `reconcile.go` (Task I). This keeps Task H independent of Task F and avoids passing process state into the socket scanner.

**Acceptance**:
- `TestScanSockets_PopulatesCandidate` against fixture with broker.pid + broker.sock.
- `TestScanSockets_BrokerPidUnreadable` produces candidate with `pidFromFile == 0` and a per-dir anomaly.
- `TestScanSockets_PerDirTimeout` injects delay > budget; exceeded dir becomes anomaly, others succeed.
- `TestScanSockets_NoMatchingStateDir_SyntheticKey`: candidate gets `unknown:<sha256(sockPath)[:16]>` synthetic key for later reconcile resolution.

**Commit**: `feat(codexbroker): socket-dir scanner (P1 task H)`

---

### Task I — Reconcile

**Files**: `reconcile.go`, `reconcile_test.go`

**Scope**:
- `reconcile(ctx context.Context, fs FS, processC []processCandidate, stateC []stateCandidate, socketC []socketCandidate) ([]BrokerRecord, []Anomaly)`. Takes ctx + fs because reconcile performs `os.Stat` on cwd paths and `pid-alive` checks on socket pidfiles.
- Implements rules from spec §4.2:
  - Match by `(brokerKey, pid, lstart)` runtime identity for process layer.
  - Merge state-layer / socket-layer fragments into a process record only when canonical cwd consistent.
  - Multiple processes same brokerKey → multiple records, each tagged `duplicate_runtime`.
  - State dir without process match → `state_dir_orphan`.
  - Process without state-dir match → `process_orphan`.
  - Socket without state-dir + no process → standalone record with `socket_orphan`.
  - broker.json.pid mismatch → `broker_json_pid_mismatch`.
  - Distinct canonical cwd hashing to same brokerKey → `broker_key_collision`, records remain distinct.
- **Live/dead pid classification for socketCandidates** (moved here from Task H): a socket whose `pidFromFile` does not appear in any processC entry → `socket_orphan`. This is the only place where process + socket data are joined.
- **Foreign-broker classification**: P1 has no Purdex launch registry, so we cannot positively confirm Purdex ownership. Rule: a process is tagged `foreign_owner` iff its argv lacks the `--cwd` value being a path under the user's home directory **and** there is no `CODEX_COMPANION_SESSION_ID` env in the process — but env reading is out of P1 scope (requires per-pid `/proc/*/environ` on Linux or `proc_pidinfo PROC_PIDARGVENVINFO` on Darwin). For P1, **all brokers are simply not tagged `foreign_owner`** — the anomaly code is reserved in the closed list for P2 to populate once launch-registry is in place. Documented in the AnomalyCode constant comment. This is acceptable because P1 does not act on foreign-owner status.
- For each record, perform `cwd_exists` classification (ENOENT vs ESTALE/EIO/EACCES vs OK) via `fs.Stat(cwdResolved)` with a per-record 50ms wall-clock cap (enforced by orchestrator-level deadline, not by passing ctx into Stat).

**Acceptance**:
- `TestReconcile_AllThreeSources` (process+state+socket) → one record, all bits set.
- `TestReconcile_ProcessOnly` → one record `Sources=process`, anomaly `process_orphan`.
- `TestReconcile_StateOnly` → one record `Sources=state-dir`, anomaly `state_dir_orphan`.
- `TestReconcile_SocketOnly` → one record `Sources=socket`, anomaly `socket_orphan`.
- `TestReconcile_DuplicateRuntime` → two records, same brokerKey, anomaly `duplicate_runtime`.
- `TestReconcile_BrokerJSONPidMismatch` → single record covering both layers, anomaly `broker_json_pid_mismatch`.
- `TestReconcile_BrokerKeyCollision` → two records, distinct cwd, anomaly `broker_key_collision`.
- `TestReconcile_CwdENOENT` → record with `cwd_missing` anomaly.
- `TestReconcile_CwdEACCES` → record with `cwd_transient_stat_error` anomaly, **not** `cwd_missing`.
- `TestReconcile_AllAnomalyCodesEmittedSomewhere` ranges over `AllAnomalyCodes` (Task A) and asserts each code is emitted by at least one fixture across all of `internal/codexbroker`'s tests, verified by aggregating all `Anomaly{Code}` values written in any test in the package (drives **AC12**). Implementation: each code-producing test calls a shared `coverageRecorder.Mark(code)` helper; this final test asserts `coverageRecorder.HasAll(AllAnomalyCodes)`. `foreign_owner` is exempt in P1 (see Task I scope) — `coverageRecorder.HasAllExcept(foreign_owner)`.
- `TestReconcile_SocketOrphan_PidNotInProcessScan` (drives AC2 negative path): socketCandidate's `pidFromFile` not in processC → record tagged `socket_orphan`.

**Commit**: `feat(codexbroker): three-source reconcile with closed anomaly set (P1 task I)`

---

### Task J — Inventory orchestration + singleflight

**Files**: `inventory.go`, `inventory_test.go`

**Scope**:
- `Scanner` struct holding FS, ProcessLister, plugin-data root, socket roots.
- `Scanner.Scan(ctx) (Result, error)` runs:
  1. Process scan (sequential, must finish first; required for socket+state reconciliation).
  2. State and socket scans in parallel (goroutines bounded by 2).
  3. Reconcile.
  4. Build `Result` (= response struct: scannedAt, scanDurationMs, deadlineMs, partial, brokers, summary).
- Total deadline default 800ms, configurable via constructor option.
- `partial=true` set when any source returns ctx.Err()/timeout/partial result; `scanSourceTimeouts` populated.
- `503` returned (as a sentinel error) only when process scan fails AND ctx not cancelled (i.e. ps unavailable not "ran out of time").
- Use `golang.org/x/sync/singleflight` to coalesce concurrent `Scan()` calls.

**Acceptance**:
- `TestScanner_HappyPath` returns full result on fixtures.
- `TestScanner_PartialOnTimeout` injects per-source delay; result has `partial=true` and `scanSourceTimeouts` populated.
- `TestScanner_PsFailureProducesSentinelError` for the 503 path.
- `TestScanner_Singleflight` two concurrent Scan() calls share one underlying scan.
- `TestScanner_DeadlineRespected` total wall-clock < deadline + 100ms tolerance under heavy fixture.
- `TestScanner_P95UnderDeadline` (drives **AC9**): 50 successive `Scan(ctx)` calls against an mlab-shaped fixture (60 state dirs, 123 simulated process rows). Records wall-clock per call; asserts p95 ≤ deadline (800 ms). Uses `RecordingFS` + `fakeLister` so timing is reproducible; no real fs/ps. Live mlab p95 is verified separately in Task O.

**Commit**: `feat(codexbroker): scanner orchestration with deadline + singleflight (P1 task J)`

---

### Task K — Read-only audit test

**Files**: `read_only_audit_test.go`, plus a small refactor to `scanner.go` to inject `Dialer` and `Signaller` interfaces.

**Scope**:
- **Refactor (precondition)**: introduce two thin interfaces in the package:
  - `Dialer` with one method `Dial(network, address string) (net.Conn, error)`. P1 Scanner takes a `Dialer` field. Production `osDialer` calls `net.Dial`. P1 implementation **never calls `Dialer.Dial`** (no broker RPC in P1) — the interface exists solely so a `recordingDialer` can detect any future regression that adds an RPC call.
  - `Signaller` with one method `Kill(pid int, sig syscall.Signal) error`. Same pattern: never called in P1, recorded for regression detection.
- Test setup constructs `Scanner` with `RecordingFS` + `recordingDialer` + `recordingSignaller` + `recordingProcessLister`.
- Run `Scan(ctx)` against fully-populated fixtures.
- Assert recorded operations:
  - **fs**: only methods on the read-only interface (Open/Stat/Lstat/EvalSymlinks/Glob/ReadDir/Read). RecordingFS forbids any other method by design (interface has zero mutation methods, but the assertion is a live count).
  - **dialer**: zero `Dial` calls.
  - **signaller**: zero `Kill` calls.
  - **processLister**: only `List` calls; zero signal/exec invocations.
- Snapshot fixture state mtimes pre/post; assert all unchanged.

**Acceptance**:
- `TestReadOnly_NoFsMutation` against full fixture (mtime unchanged + only read-method calls).
- `TestReadOnly_NoBrokerSocketConnect`: asserts `recordingDialer.CallCount() == 0` after Scan. **This is the primary defence against a future regression that adds RPC**.
- `TestReadOnly_NoSignalsSent`: asserts `recordingSignaller.CallCount() == 0`.

This task is the primary AC7 evidence per spec §4.4.

**Commit**: `test(codexbroker): read-only audit guard with Dialer/Signaller interfaces (P1 task K, AC7)`

---

### Task L — HTTP handler

**Files**: `handler.go`, `handler_test.go`

**Scope**:
- `Handler` struct holding only `*Scanner`. **Auth is enforced by the existing daemon middleware that wraps the mux** (verified by reading `cmd/pdx/main.go`); the handler itself does no token check. Adding auth in the handler would duplicate middleware behaviour.
- `func (h *Handler) HandleBrokers(w http.ResponseWriter, r *http.Request)`:
  - 405 for non-GET.
  - Wraps `r.Context()` with `context.WithTimeout(ctx, 800ms)` (configurable via Scanner option, not request).
  - Calls `Scanner.Scan(ctx)`.
  - On success → 200 JSON.
  - On `Scanner.Scan` returning the 503-sentinel → 503 with structured error.
- JSON shape exactly matches spec §4.3.

**Acceptance**:
- `TestHandler_Get_200` returns valid JSON with all summary fields.
- `TestHandler_Method_405` for POST/PUT/DELETE.
- `TestHandler_PsFailure_503`.
- `TestHandler_PartialResultIs200` (not 503) when a source timed out.
- `TestHandler_RespectsClientDeadline` if r.Context() has earlier deadline.

**Commit**: `feat(codexbroker): GET /api/codex/brokers handler (P1 task L)`

---

### Task M — Module wiring + daemon registration

**Files**: `module.go`, `module_test.go`, `cmd/pdx/main.go` (modify).

**Scope**:
- Module struct with `RegisterRoutes(mux *http.ServeMux)` method registering `GET /api/codex/brokers`.
- Constructed in `cmd/pdx/main.go`'s module-registration block (parallel to existing `internal/module/agent` registration; `internal/core/core.go` is **not** modified).
- Auth: relies on existing daemon-level middleware that already wraps the mux. No additional middleware in the new module.
- No persistence, no goroutines, no shutdown hooks needed in P1.

**Acceptance**:
- `TestModule_RegistersRoute` checks the mux has the path after registration.
- Manual: daemon builds, starts, `curl -H "X-Pdx-Token: ..." http://127.0.0.1:7860/api/codex/brokers` returns valid JSON.

**Commit**: `feat(codexbroker): wire module into pdx daemon (P1 task M)`

---

### Task N — Integration test (build tag)

**Files**: `inventory_live_test.go`

**Scope**:
- Build tag `//go:build integration`.
- Spawns a real `app-server-broker.mjs serve --cwd <tmpdir> --endpoint unix:<sockpath> --pid-file <pidfile>`.
- **`t.Cleanup` registered immediately after spawn** to guarantee broker is reaped even if the test panics or asserts fail later. Cleanup steps in order: (a) read pid from pidfile, (b) `syscall.Kill(-pgid, SIGTERM)` with 2s wait, (c) `syscall.Kill(-pgid, SIGKILL)` if still alive, (d) `os.RemoveAll(<tmpdir>)`. The cleanup is robust to partial spawn failures (pidfile may not exist).
- Waits for broker.json + socket creation (poll up to 5s).
- Calls `Scanner.Scan(ctx)`.
- Asserts at least one `BrokerRecord` with all three `Sources` bits set, matching the spawned pid (drives AC2).
- Captures `ps` snapshot before/after Scan; asserts identical broker pid set (supplementary AC7 evidence).

**Acceptance**:
- `go test -tags=integration ./internal/codexbroker/...` passes on a host with `node` and `app-server-broker.mjs` available.
- Documented in PR description as "integration tests must be run on mlab manually before merge".
- Manual run leaves zero new broker processes after `t.Cleanup` returns (verified via `ps -ef | grep app-server-broker | wc -l` before/after).

**Commit**: `test(codexbroker): integration test against real app-server-broker (P1 task N)`

---

### Task O — Live verification on mlab + PR

**Scope**:
- Build daemon `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/governance-p1-inventory && go build -o bin/pdx ./cmd/pdx`.
- Stop existing daemon; start new build.
- Run live verification per spec §10:
  1. `curl ... | jq '.summary'` against mlab.
  2. Compare `summary.total` vs `find ~/.claude/plugins/data/codex-openai-codex/state -name 'broker.json' | wc -l` (note: counts state-dir-only candidates; combined with process-only ones gives the inventory total).
  3. Spot-check three brokers' attribution.
  4. **AC9 live evidence**: 20 successive curl invocations; record wall-clock; assert p95 ≤ 800 ms.
  5. **AC7 live evidence**: run endpoint twice; verify `ps -ef | grep app-server-broker | wc -l` unchanged; verify `find ~/.claude/plugins/data/codex-openai-codex/state -newer <ts-before>` returns no fs mutations.
- Capture output for PR description.
- Open PR via `gh pr create` with body documenting baseline counts, AC1–AC12 evidence, and integration test result.

**Acceptance**:
- PR opened, all CI checks green.
- Test plan in PR body matches spec §10.

**Commit**: PR open (no code commit; this task is operational).

---

## 5. Build sequence

```
A (types) → B (paths) → C (FS abstraction) → D (process lister)
                                             ↓
                                             E (argv + hash)
                                             ↓
                                             F (process scanner)
                                            / | \
                                           G  H  (state, socket — parallel)
                                            \ | /
                                              ↓
                                              I (reconcile)
                                              ↓
                                              J (inventory orchestration)
                                              ↓
                                              K (read-only audit) — sequential gate before merge
                                              ↓
                                              L (handler)
                                              ↓
                                              M (module wiring)
                                              ↓
                                              N (integration test)
                                              ↓
                                              O (live verify + PR)
```

A–F can be implemented sequentially in one focused subagent session. G/H can fork two subagents in parallel after F is done. I onwards is sequential.

## 6. TDD discipline

Each task follows: **write failing test → minimal impl → green → refactor → commit**. No "implementation first then tests later" allowed. Subagent dispatcher enforces this per kickoff §371 and `feedback_subagent_tdd_priority`.

## 7. Verification gates

| Gate | What | When |
|---|---|---|
| Compile | `go build ./...` | After every commit |
| Vet | `go vet ./internal/codexbroker/...` | After every commit |
| Test | `go test ./internal/codexbroker/...` | After every commit |
| Lint | `gofmt -d` clean | Before push |
| Integration | `go test -tags=integration ./internal/codexbroker/...` | Before PR open |
| Live verify | spec §10 manual checks | Before PR open |
| AC checklist | every AC1–AC12 mapped to a passing test | Before PR open |

## 8. PR review plan (per project CLAUDE.md)

| Round | Reviewer | Focus |
|---|---|---|
| R1 | codex standard | Standard cross-model code review (focus: spec alignment, Go idioms, test quality). |
| R2 (3-parallel) | codex adversarial | See focus-text guidance below. |

PR-A is medium scope (~600 LOC); both rounds are required per CLAUDE.md.

**R2 focus text (specify when dispatching)**:

| Reviewer | Focus prompt seed |
|---|---|
| Race/timeout/process | Find: goroutine leaks, deadline not propagated to goroutines, `ps` fork amplification under repeated polling, partial result accidentally returning 503, any signal/write/connect path the scanner could trigger, `context.AfterFunc` lifecycle, singleflight key correctness. |
| API/P2 prereq | Verify: response shape exactly matches spec §4.3; runtime identity `(brokerKey, pid, lstart)` is the unit of `BrokerRecord` (not just `brokerKey`); `duplicate_runtime` is multiple records, not merged; `foreign_owner` reserved but not populated in P1 — and P2 launch-registry hook points are not blocked by current data shape. |
| File quality / SRP | Per-file SRP: `scan_process.go` only does process layer, `scan_state.go` only state, `scan_socket.go` only socket — no cross-layer leakage; build tags handled cleanly (`hash_darwin.go` / `hash_linux.go` / `hash_other.go`); test fixture ownership clear (no shared mutation); single file LOC reasonable; testdata layout matches plan §3. |

**Commit consolidation before PR**: TDD work produces ~14 atomic commits. Before opening the PR, squash-organise (interactive rebase, **not** `--amend` of merged commits) into 5-7 logical commits aligned with reviewable units:

1. `feat(codexbroker): types + paths + fs abstraction (P1 foundation)` — Tasks A+B+C
2. `feat(codexbroker): process discovery (lister + argv + brokerKey + scanner)` — Tasks D+E+F
3. `feat(codexbroker): state-dir + socket-dir scanners` — Tasks G+H
4. `feat(codexbroker): three-source reconcile with closed anomaly set` — Task I
5. `feat(codexbroker): inventory orchestration with deadline + singleflight` — Task J
6. `feat(codexbroker): GET /api/codex/brokers handler + module wiring` — Tasks L+M
7. `test(codexbroker): read-only audit + integration test` — Tasks K+N

Per-task TDD discipline still applies during development; reorganisation is a final step before push.

## 9. Risks specific to implementation

| Risk | Mitigation |
|---|---|
| `app-server-broker.mjs` not available in CI | Integration test has `//go:build integration`; CI runs only unit tests. Mlab manual run before merge. |
| `unix.Pathconf` missing on linux/freebsd builds | Conditional build (`hash_darwin.go` / `hash_linux.go` / `hash_other.go`) — non-Darwin/non-Linux defaults to case-sensitive. |
| Daemon mux uses non-stdlib router | Verified via `internal/core/core.go` and `internal/module/agent/module.go` — uses standard `*http.ServeMux` with method patterns (Go 1.22+). Compatible. |
| `golang.org/x/sync/singleflight` not in go.mod | Add as part of Task J commit; `go mod tidy` afterwards. |
| `golang.org/x/sys/unix` for Pathconf | Verify via `go list -m golang.org/x/sys` in Task E; should already be transitively present. |
| `golang.org/x/text/unicode/norm` for NFC normalisation | New direct dependency. Add as part of Task E commit; `go mod tidy` afterwards. |
| `github.com/mattn/go-shellwords` for argv parsing | Verify presence via `go list -m`; if absent, add as part of Task E commit. |
| Windows build break (no `unix.Pathconf`) | `hash_other.go` build tag is `!darwin && !linux`. CI matrix may include Windows; provide a Windows stub returning `caseSensitive=true`. Test runs gated by `runtime.GOOS != "windows"` where APFS specifics matter. |
| `ps` argv truncation on long cwd | Use `ps -ww` (BSD) / `ps -eww` to disable truncation. Document the exact ps invocation in Task D code comment. |
| Multi-user host (visible brokers from other UIDs) | spec §4 is "visible on host" — accepted. P1 does not filter by UID; `BrokerRecord` does not carry UID in v1. Add to spec §8 Risks via amendment if this becomes a concern. |

## 10. Estimate

| Task | Est. LOC (impl + test) | Est. effort |
|---|---|---|
| A types | 80 + 60 | 30 min |
| B paths | 30 + 50 | 20 min |
| C fs | 90 + 110 | 1 h |
| D process | 100 + 130 | 1.5 h |
| E argv + hash | 90 + 160 | 2 h |
| F scan_process | 70 + 100 | 1 h |
| G scan_state | 130 + 200 | 2 h |
| H scan_socket | 60 + 90 | 1 h |
| I reconcile | 180 + 280 | 3 h |
| J inventory | 110 + 160 | 2 h |
| K audit | 0 + 120 | 1 h |
| L handler | 70 + 130 | 1.5 h |
| M wiring | 30 + 40 | 30 min |
| N integration | 0 + 120 | 1 h |
| **Total** | **~1040 + ~1750** | **~18 h** |

LOC estimate is ~2.8K but spec range was 400-700 for impl. Reviewing: impl alone is ~1040, which exceeds the spec's upper bound (700). Test code makes the diff larger; this is intentional (TDD-first project + strict AC coverage). PR will note the size justification.

## 11. Out of scope reaffirmation

This plan does **not** implement:

- §5.1 predicates A/B/C
- §5.2 stale-running detection
- §5.3 emergency overrides
- §5.4 kill sequence
- §6.1 trigger strategies (boot reconcile, tick, ExitWorktree hook, manual sweep)
- §6.2 config keys
- §6.3 metrics / WS broadcast
- §7 SPA dashboard
- Quarantine / launch registry / audit dump

Each is reserved for P2/P3/P4 PRs per spec §9.

**Promises P1 makes about future phases** (so reviewers can trust the door is open):

1. **No broker RPC / socket connect in P1**: `Dialer` interface exists but is never invoked. P2 will add the broker-side `thread/list` RPC for predicate A here. Verified by Task K.
2. **Launch registry is a P2 prerequisite**: spec §5.1 requires Purdex to persist `(brokerKey, pid, lstart) → (tmuxPane, callerSessionID, launchedAt)` at broker spawn time. P1 does not introduce this registry but `BrokerRecord` already carries `(brokerKey, pid, lstart)` so P2 can join cleanly.
3. **Inventory cache + watcher belongs to P3**: P1 has `singleflight` to coalesce concurrent calls, but no time-based cache and no kqueue/inotify watchers. P3 (per spec §6.1) adds these. The current `Scanner` struct has space for a `cache *inventoryCache` field that P3 will populate.
4. **Foreign-owner classification is reserved for P2**: P1 includes the `foreign_owner` anomaly code in the closed list (spec §4.2) but does not populate it because P1 has no positive-ownership signal. P2 introduces launch-registry; brokers without a registry entry then become `foreign_owner` and are quarantine-only by default (spec §2 + §5.1). Test `TestReconcile_AllAnomalyCodesEmittedSomewhere` exempts `foreign_owner` in P1 via `coverageRecorder.HasAllExcept(foreign_owner)`.
