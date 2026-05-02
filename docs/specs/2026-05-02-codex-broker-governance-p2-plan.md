# Plan — Codex broker governance P2 (Decision + Kill, manual API only)

**Spec**: `docs/specs/2026-05-01-codex-broker-governance-spec.md`
**Date**: 2026-05-02
**Branch**: `codex-broker-governance-p2`
**Baseline**: origin/main `76e8e6ab` (alpha.284)
**Scope**: PR-C per spec §9 line 581. Builds on P1 inventory (shipped alpha.280 PR #792). No automatic triggers — only manual via `POST /api/codex/brokers/sweep`.

---

## 1. Goal

After P2 merges, the daemon can evaluate three predicates (A: active execution, B: recent delivery readable, C: live ownership) for any `BrokerRecord` from the P1 inventory, apply stale-running detection to suppress false positives on lagging `state.json`, apply emergency overrides E1/E2, and — when a manual `POST /api/codex/brokers/sweep?mode=apply` is received — execute the full graceful → SIGTERM → SIGKILL → cleanup → audit-dump sequence. Quarantine state (E2 plus foreign-broker quarantine) is persisted to `audit/quarantine.json` and restored on boot.

No automatic triggers ship in this PR. Every kill in P2 requires explicit human action via the sweep API.

Acceptance is defined by: all unit tests green, integration test passes, `mode=dry-run` correctly classifies the ~50 orphan brokers visible on mlab, `mode=apply&brokerKey=<known-orphan>` (operator-explicit override per spec §5.1 line 371) produces a complete `audit/orphan-*.json` preimage + postscript, unfiltered `mode=apply` (no `brokerKey`) issues zero kills against any foreign broker on mlab (mass-kill safety), and zero false-positive kills in the dry-run run.

---

## 2. Non-goals

- Automatic triggers: 30 s tick, boot reconcile, ExitWorktree hook — all P3 (PR-D).
- SPA dashboard — P4 (PR-F).
- `codex.broker.*` config keys persisted in `~/.purdex/config.toml` — P3. P2 uses compile-time defaults, overrideable via env for tests.
- Codex CLI lifecycle RFC — upstream, deferred per spec §7.
- `GET /api/codex/brokers` augmentation with predicate results — P3 (spec §6.3).
- WS broadcast `codex.broker.changed` — P3.
- Audit retention pruner goroutine — deferred to P3 Start() hook; P2 writes dumps but does not prune.

---

## 3. Architecture

New files added to `internal/codexbroker/`. No existing P1 file is modified except `module.go` (Task R wiring) and `types.go` (Task A additive P2 types). All P2 additions are purely additive.

```
internal/codexbroker/
  # --- P2 new files ---
  decision.go           PredicateResult, DecisionResult, KillRule, ConflictRule; evaluator entry point
  decision_test.go
  staleness.go          stale-running detection per spec §5.2; pid *int decode + cmdline verify
  staleness_test.go
  override.go           E1 (consecutive ENOENT scan, daemon-lifetime tracker) + E2 (fingerprint mismatch)
  override_test.go
  quarantine.go         quarantine.json atomic R/W + schema + boot-restore
  quarantine_test.go
  launchregistry.go     (brokerKey, pid, lstart) → (tmuxPane, callerSessionID, launchedAt) persist
  launchregistry_test.go
  killer.go             kill sequence Steps 0-6 per spec §5.4
  killer_test.go
  audit.go              orphan-<key>-<ts>.json write (preimage + postscript); spec §5.5
  audit_test.go
  sockverify.go         Darwin libproc + Linux /proc/*/fd/* + lsof fallback; Step 6 prerequisite
  sockverify_darwin.go  CGo-free libproc binding via syscall.Syscall
  sockverify_linux.go   /proc walk
  sockverify_other.go   lsof-only fallback
  sockverify_test.go
  sweep.go              POST /api/codex/brokers/sweep handler + singleflight gate
  sweep_test.go
  sweep_integration_test.go  build tag `integration`

  # --- P1 files modified ---
  types.go              additive: P2 types (PredicateResult, DecisionResult, QuarantineEntry, LaunchEntry, AuditDump)
  module.go             register sweep route + pass audit/quarantine/launchregistry into Module
```

**Concurrency control**: `sweep.go` uses two layers of `sync.RWMutex` to serialise mutating sweeps without coalescing distinct requests (see Task Q for full design). A daemon-process-wide `globalApplyMu` excludes `__all__` apply from any other concurrent apply or dry-run. A per-`brokerKey` `sync.Map[brokerKey]*sync.RWMutex` serialises filtered apply against itself and against dry-run on the same broker. Dry-run uses read-locks; apply uses write-locks. This replaces the originally-considered `singleflight.Group` (which would coalesce distinct apply requests and silently drop one — see plan-review round 1 finding #4).

**Launch-registry signal in P2**: `launch-registry.json` is loaded read-only at boot (Task I) and consulted by the decision layer (Task F) as the authoritative ownership signal. P2 does NOT populate the registry from spawn paths — that is a P3 / Lights PR-G concern. Therefore on mlab the registry will initially be empty, and **every** broker without a registry entry must be classified as `foreign_quarantine` (per spec §2 + §5.1 line 371: "foreign-broker is quarantine-only — never automatically killed by tick or boot reconcile"). The lookup-miss is a *signal*, not an error; it suppresses kill regardless of predicate state.

**Graceful RPC**: Step 2 connects to `BrokerRecord.Endpoint` (unix socket) and sends a minimal JSON shutdown request. Broker's own handler is responsible for draining children. If the endpoint is absent or the RPC times out, the sequence falls through to SIGTERM. P2 does not define the RPC wire format beyond what the spec requires — treat graceful as best-effort; SIGTERM is the reliable path.

**Platform split for sockverify**: macOS exposes `proc_pidinfo(PROC_PIDLISTFDS)` via `libproc.h`. Rather than CGo (which would require a C toolchain in CI), bind via `syscall.Syscall` directly against the Darwin `libsystem_kernel` dylib using the syscall number for `proc_pidinfo`. Linux walks `/proc/<pid>/fd/` symlinks. Both platforms fall back to `lsof -nP -- <sockPath>` with a 1 s timeout when the preferred path returns an error.

---

## 4. Task breakdown

Tasks are ordered to minimise rework. Each task is one TDD cycle; commit boundary annotated. All file paths are relative to `internal/codexbroker/`.

---

### Task A — P2 type extensions in `types.go`

**Files**: `types.go` (modify, additive only)

**Scope**:
- Add `PredicateResult{A, B, C bool; ADetail, BDetail, CDetail string}` — per predicate evidence summary for the decision trace.
- Add `DecisionResult{Predicates PredicateResult; IdleSeconds int; OverrideE1, OverrideE2 bool; Kill bool; Reason string; AnomaliesAdded []AnomalyCode}`. `AnomaliesAdded` carries anomalies the decision layer infers (e.g. `AnomalyForeignOwner` when launch-registry lookup misses) without mutating the immutable `BrokerRecord`. The kill rule (Task F) and audit dump (Task K) consume this slice in addition to `rec.Anomalies`.
- Add `SweepRequest{Mode string; BrokerKey string}` and `SweepResponse{DryRun bool; Evaluated []BrokerDecision; Applied []string; Errors []string}` where `BrokerDecision` pairs `BrokerRecord` with `DecisionResult`.
- Add `QuarantineEntry` and `QuarantineFile` structs matching the spec §5.3 JSON schema (line 408-422).
- Add `LaunchEntry{BrokerKey, Pid, Lstart, TmuxPane, CallerSessionID, LaunchedAt string}` and `LaunchRegistry{Version int; Entries []LaunchEntry}`.
- Add `AuditDump` struct matching spec §5.5 JSON schema (line 481-502).
- Add `StateJobLite{ID, Status string; UpdatedAt time.Time; CompletedAt *time.Time; Pid *int}` — the minimal per-job record predicates A and B need from `state.json.jobs[]`. P1's `BrokerRecord` only carries the rollup `JobCounts` + `LastJobUpdatedAt`, so P2 must read per-job detail itself. The `*int` Pid is essential for the spec §5.2 stale-running rule (lines 386-390).
- Add helper `ReadStateJobs(fs FS, stateDir string) ([]StateJobLite, error)` that opens `<stateDir>/state.json` and decodes only the `jobs[]` slice into `StateJobLite`. Returns `(nil, nil)` if the file is absent or the jobs array is empty (consistent with P1 anomaly tagging — file-absent is reported separately). Errors only propagate for genuine parse failures so predicate A can degrade gracefully.
- Add `E1State{BrokerKey string; FirstSeenAt time.Time; FirstConfirmedAt *time.Time}`. The struct lives in `types.go` so the daemon-lifetime tracker (Module field, see Task G) and the override evaluator (Task G) share the same shape.
- No removal of existing P1 types.

**TDD tests**:
- `TestDecisionResult_JSONRoundTrip` — encode + decode without loss including non-empty `AnomaliesAdded`.
- `TestQuarantineEntry_ExpiresAt` — verify time arithmetic from `quarantinedAt + 7*24h`.
- `TestAuditDump_JSONRoundTrip` — round-trip including `killSequence.stepLatencyMs`.
- `TestReadStateJobs_HappyPath` — fixture `state.json` with 3 jobs decodes into 3 `StateJobLite` with correct `*int` Pid (one nil, two populated) and `*time.Time` CompletedAt.
- `TestReadStateJobs_FileMissing` — returns `(nil, nil)`, no error.
- `TestReadStateJobs_EmptyJobs` — `state.json` exists but `jobs:[]` → returns `(nil, nil)`.
- `TestReadStateJobs_MalformedJSON` — returns parse error so predicate A can fall through to a documented degraded path.

**Acceptance**: `go test ./internal/codexbroker/...` compiles and new type tests pass.

**Commit**: `feat(codexbroker): P2 type extensions — decision/quarantine/audit/registry types (P2 task A)`

---

### Task B — `staleness.go`: stale-running detection

**Files**: `staleness.go`, `staleness_test.go`

**Pre-condition**: Task A done (uses `PredicateResult`).

**Scope**:
- `IsStaleRunning(job StateJobLite, lister ProcessLister, threshold time.Duration) (stale bool, detail string)`. `StateJobLite` is the shared minimal job record introduced in Task A (`{ID, Status string; UpdatedAt time.Time; CompletedAt *time.Time; Pid *int}`).
- Per spec §5.2 lines 378-390: decode `pid` as `*int`; if nil and age > threshold → stale; if non-nil, verify via `lister.List` that pid is still alive and cmdline matches an expected broker task-worker shape (`app-server-broker.mjs`); mismatch → stale.
- `StaleRunningCount(jobs []StateJobLite, lister ProcessLister, threshold time.Duration) int` — rolls up stale count for the decision layer.
- Threshold default constant `DefaultStaleRunningThreshold = 1 * time.Hour`.

**TDD tests**:
- `TestIsStaleRunning_NilPidBeyondThreshold` → stale=true.
- `TestIsStaleRunning_NilPidWithinThreshold` → stale=false.
- `TestIsStaleRunning_NonNilPidDead` → stale=true (fakeLister returns empty).
- `TestIsStaleRunning_NonNilPidAliveWrongCmdline` → stale=true (fakeLister returns pid with non-broker cmdline).
- `TestIsStaleRunning_NonNilPidAliveCorrectCmdline` → stale=false.
- `TestStaleRunningCount_MixedJobs` — 3 jobs, 2 stale → returns 2.

**Acceptance**: all stale-running tests pass; `go vet` clean.

**Commit**: `feat(codexbroker): stale-running detection per §5.2 (P2 task B)`

---

### Task C — `decision.go`: predicate A (active execution)

**Files**: `decision.go`, `decision_test.go`

**Pre-condition**: Task B done.

**Scope**:
- `EvalPredicateA(ctx context.Context, rec BrokerRecord, jobs []StateJobLite, lister ProcessLister, dialer Dialer) (bool, string)`. `jobs` is the per-job slice produced by `ReadStateJobs` (Task A) — caller is `EvalDecision` (Task F). Predicate A does not re-read `state.json` itself.
- Strategy: (1) attempt RPC `thread/list` on `rec.Endpoint` via `dialer`; if ≥1 thread with `status=active` or pending approval → true; if RPC unreachable but `jobs` contains an entry with `status ∈ {queued, running}` that is NOT stale (Task B `IsStaleRunning(job, lister, ...) == false`) → true (conflict rule: RPC stall does not penalise broker, per spec §5.1 lines 367-368); (2) stale-running downgrade applies only to running entries — `IsStaleRunning` per job; if all running entries are stale → contributes false.
- RPC budget: `gracefulShutdownTimeoutSeconds` default 5 s (compile-time constant).
- `threadListResponse` private struct for JSON decode of broker RPC response — minimal: `{threads: [{id, status}]}`.
- Note on test caller convention: tests construct `[]StateJobLite` directly; production callers use `ReadStateJobs(fs, rec.StateDir)` upstream — when that helper returns an error, `EvalDecision` (Task F) feeds an empty slice and tags `state_json_unreadable` anomaly into `result.AnomaliesAdded` (degraded path).

**TDD tests**:
- `TestEvalPredicateA_RPCActiveThread` — fake dialer returns active thread → true.
- `TestEvalPredicateA_RPCUnreachable_StateRunning_NotStale` → true (conflict rule); `jobs[]` contains a running job with non-nil pid that the fake lister reports alive with broker cmdline.
- `TestEvalPredicateA_RPCUnreachable_StateRunning_Stale` → false; running job pid is nil and `UpdatedAt` is older than `DefaultStaleRunningThreshold`.
- `TestEvalPredicateA_RPCDown_StateJobsStale_ReturnsFalse` — RPC dialer returns error; `jobs[]` contains 2 running jobs both stale (one nil-pid past threshold, one non-nil-pid that lister reports dead) → predicate A returns false. This is the explicit fallback-correctness test the predicate signature change enables.
- `TestEvalPredicateA_RPCUnreachable_StateQueued` → true.
- `TestEvalPredicateA_RPCOk_NoThreads_StateEmpty` → false.
- `TestEvalPredicateA_RPCTimeout_StateEmpty` → false (RPC timeout ≠ active).
- `TestEvalPredicateA_RPCDown_NilJobs` → false (degraded path: caller passed empty slice because `ReadStateJobs` failed).

**Acceptance**: predicate A tests all green.

**Commit**: `feat(codexbroker): predicate A — active execution (P2 task C)`

---

### Task D — `decision.go`: predicate B (recent delivery readable)

**Files**: `decision.go` (extend), `decision_test.go` (extend)

**Pre-condition**: Task A done.

**Scope**:
- `EvalPredicateB(rec BrokerRecord, jobs []StateJobLite, fs FS, window time.Duration) (bool, string)`. Receives the same shared `[]StateJobLite` slice the decision composer (Task F) read once via `ReadStateJobs` — predicate B does not re-read `state.json`.
- For each job with `CompletedAt` non-nil and within `window` and `Status == "completed"`, checks `fs.Stat(stateDir/jobs/<id>.json)` exists and `fs.Open` + decode succeeds (has `result` or `rendered` field). Any one such job → true.
- `DefaultRecentResultWindow = 30 * time.Minute`.
- Uses `FS` interface from P1 — no real I/O in tests.

**TDD tests**:
- `TestEvalPredicateB_CompletedJobWithinWindow_FilePresent` → true.
- `TestEvalPredicateB_CompletedJobWithinWindow_FileMissing` → false.
- `TestEvalPredicateB_CompletedJobBeyondWindow` → false.
- `TestEvalPredicateB_FailedJobWithinWindow` → false (only `completed` status counts).
- `TestEvalPredicateB_NoJobs` → false.
- `TestEvalPredicateB_MalformedJobFile` → false (parseable is required).

**Acceptance**: predicate B tests all green.

**Commit**: `feat(codexbroker): predicate B — recent delivery readable (P2 task D)`

---

### Task E — `decision.go`: predicate C (live ownership)

**Files**: `decision.go` (extend), `decision_test.go` (extend), `launchregistry.go` (stub enough for Task E; full impl is Task I)

**Pre-condition**: Tasks A, I-stub (launch registry lookup interface only).

**Scope**:
- `EvalPredicateC(rec BrokerRecord, fs FS, registry LaunchRegistryReader) (bool, string)` where `LaunchRegistryReader` is a minimal interface `{ Lookup(brokerKey string) (*LaunchEntry, bool); Empty() bool }`.
- Per spec §5.1 lines 365-366: `cwd` path must exist with definitive `os.Stat` success; then at least one of: (a) `registry.Lookup(brokerKey)` returns an entry with `tmuxPane` still alive (verify via `tmux list-panes -F '#{pane_id}'` exec, cached for the scan duration), (b) `callerSessionID` matches a recognised live session (P2: check against daemon's session map if accessible; if no session tracking yet in P2, treat as false — not blocking), (c) explicit lease exists (not in P2 scope).
- Transient `cwd` stat failures (ESTALE, EIO, EACCES) → fall through; definitive ENOENT → C=false.
- `DefaultCwdStatBudget = 50 * time.Millisecond`.
- **Important**: predicate C only reports ownership *evidence*. The "no registry entry → quarantine, never auto-kill" foreign-broker contract (spec §5.1 line 371) is enforced one layer up in Task F by inspecting `registry.Empty()` / `registry.Lookup` directly — predicate C must not silently absorb that responsibility, because predicate C false alone (without ownership context) is also the legitimate outcome for a purdex-owned broker whose pane has just closed.

**TDD tests**:
- `TestEvalPredicateC_CwdExists_PaneAlive` → true (fake registry + fake tmux pane list).
- `TestEvalPredicateC_CwdExists_PaneGone` → false.
- `TestEvalPredicateC_CwdENOENT` → false.
- `TestEvalPredicateC_CwdTransientError` → fallthrough: C evaluates pane ownership without cwd gate (per spec §5.3 E1 two-scan requirement, transient is not ENOENT).
- `TestEvalPredicateC_NoRegistryEntry` → false (foreign broker, no ownership evidence).
- `TestEvalPredicateC_RegistryEntry_NoPane` → false (pane gone).

**Acceptance**: predicate C tests all green.

**Commit**: `feat(codexbroker): predicate C — live ownership (P2 task E)`

---

### Task F — `decision.go`: conflict rule + kill rule

**Files**: `decision.go` (extend), `decision_test.go` (extend)

**Pre-condition**: Tasks C, D, E done.

**Scope**:
- `EvalDecision(ctx context.Context, rec BrokerRecord, opts DecisionOpts) DecisionResult` composing A/B/C.
  - `DecisionOpts` carries: `FS`, `ProcessLister`, `Dialer`, `Registry LaunchRegistryReader`, `IdleTimeout time.Duration`, `ResultWindow time.Duration`, `StaleThreshold time.Duration`.
  - **Reads per-job state once**: calls `ReadStateJobs(opts.FS, rec.StateDir)` once at the top of evaluation; passes the resulting `[]StateJobLite` to predicate A and (per §5.2) to predicate B. On `ReadStateJobs` error, feeds an empty slice and adds `state_json_unreadable` to `result.AnomaliesAdded` so audit trace explains the degraded predicate result.
- **Foreign-broker pre-filter** (spec §5.1 line 371 + §2 line 44): evaluated **before** the kill rule so it strictly precedes the idle-timeout kill path.
  - Inputs: `opts.Registry`. The registry MAY be `nil` (defensive — Module wiring failure), `Empty()==true` (P2 expected steady state on mlab), or have entries.
  - Detection: `isForeign := opts.Registry == nil || opts.Registry.Empty() || lookupMiss(opts.Registry, rec.BrokerKey)`. If `isForeign` is true OR `rec.Anomalies` already contains `AnomalyForeignOwner` (forward-compatible with future P1 changes) → set `result.AnomaliesAdded += AnomalyForeignOwner` (only if not already in `rec.Anomalies` to avoid duplicate audit entries), `result.Kill = false`, `result.Reason = "foreign_quarantine"`. Predicate trace is still populated for forensics.
  - Note: `EvalDecision` always returns `Kill=false` for foreign brokers; it has no notion of "operator override" because that is a transport-layer (sweep handler) concern, not a decision-layer concern. The sweep handler (Task Q) implements spec §5.1 line 371 ("Operator may still issue a manual sweep with `mode=apply&brokerKey=<...>` to override") by treating an explicit `brokerKey` filter on `mode=apply` as an explicit operator decision to kill that specific broker even when `Reason == "foreign_quarantine"`. No separate `force` flag is introduced — the explicit `brokerKey` IS the override semantic, matching the spec wording verbatim. Tested in Task Q with `TestSweepHandler_BrokerKeyApply_OverridesForeignQuarantine`.
- **Conflict rule**: any predicate true → `Kill=false` (positive liveness wins, spec §5.1 lines 367-368).
- **Kill rule**: `Kill = ¬A ∧ ¬B ∧ ¬C ∧ idleSeconds >= idleTimeout.Seconds()` per spec §5.1 line 371. `idleSeconds` is derived from `rec.LastJobUpdatedAt` (P1 field) vs now; nil `LastJobUpdatedAt` → treated as `age = ∞` (most permissive kill direction — broker never dispatched a job is a valid orphan candidate).
- Return `DecisionResult` with full predicate trace + `AnomaliesAdded` slice.

**TDD tests**:
- `TestEvalDecision_ATrue_NoKill`.
- `TestEvalDecision_BTrue_NoKill`.
- `TestEvalDecision_CTrue_NoKill`.
- `TestEvalDecision_AllFalse_IdleExpired_Kill` — registry has matching entry; only then idle-timeout kill path triggers.
- `TestEvalDecision_AllFalse_IdleNotExpired_NoKill`.
- `TestEvalDecision_ForeignBroker_AllFalse_NoKill` — `rec.Anomalies` already carries `AnomalyForeignOwner` (forward-compat path).
- `TestEvalDecision_RegistryMissing_AllForeignQuarantine` — registry `Empty()==true`; **all** brokers (regardless of A/B/C) get `AnomaliesAdded=[AnomalyForeignOwner]`, `Kill=false`, `Reason="foreign_quarantine"`. Mlab-realistic test for the P2-launches-without-spawn-hook scenario.
- `TestEvalDecision_RegistryNil_AllForeignQuarantine` — defensive: `opts.Registry == nil` treated identically.
- `TestEvalDecision_RegistryHasOtherKey_ThisIsForeign` — registry populated but lookup miss for this brokerKey → `Reason="foreign_quarantine"`.
- `TestEvalDecision_NilLastJobUpdatedAt_IdleInfinite_Kill` (idle=∞ treated as expired) — uses populated registry to clear the foreign filter.

**Acceptance**: decision composition tests all green; `go test -race ./internal/codexbroker/...` clean.

**Commit**: `feat(codexbroker): conflict rule + kill rule (P2 task F)`

---

### Task G — `override.go`: E1 (consecutive ENOENT scan, daemon-lifetime tracker)

**Files**: `override.go`, `override_test.go`

**Pre-condition**: Task A done (provides shared `E1State`).

**Scope**:
- `E1Tracker` struct: `{ Mu sync.Mutex; States map[string]*E1State }` (key = `brokerKey`). Owned by `Module` (see Task R wiring); injected into `SweepHandler`. Daemon-lifetime in-memory only — **not** sweep-local. Spec §5.3 line 399 requires two ENOENT observations ≥ 60 s apart, which CANNOT be satisfied by a per-sweep map because each `POST /sweep` is an independent HTTP request and any sweep-local state is discarded on response. Daemon restart resets the tracker (acceptable: the 60 s clock starts again, which is conservative — spec does not require persistence).
- `(*E1Tracker).Observe(rec BrokerRecord, now time.Time) (triggered bool, state E1State)`:
  - Acquire `Mu`; defer release.
  - If `rec.Anomalies` contains `AnomalyCwdMissing` (definitive ENOENT, not transient):
    - If `States[rec.BrokerKey] == nil` → store new `E1State{BrokerKey, FirstSeenAt: now}`; return `(false, ...)`.
    - Else if `now - prior.FirstSeenAt >= 60s` → set `prior.FirstConfirmedAt = &now`; return `(true, *prior)`.
    - Else (gap < 60s) → return `(false, *prior)` without mutation.
  - If `rec.Anomalies` does NOT contain `AnomalyCwdMissing` (cwd recovered or only transient errors): clear `States[rec.BrokerKey]` so a future ENOENT chain restarts cleanly. Return `(false, E1State{})`.
- `(*E1Tracker).Snapshot() map[string]E1State` — for debug endpoints / tests.
- `(*E1Tracker).Reset(brokerKey string)` — for `mode=apply` post-kill cleanup so a respawned broker doesn't inherit a stale 60 s clock.
- Constructor: `NewE1Tracker() *E1Tracker` returns ready-to-use tracker.
- The override decision flows: `EvalDecision` (Task F) calls `opts.E1Tracker.Observe(rec, now)`; if triggered, sets `result.OverrideE1 = true`. `DecisionOpts` gains an `E1Tracker *E1Tracker` field (already passed via Module).

**TDD tests**:
- `TestE1Tracker_FirstObservation_NotTriggered` — single Observe call → state stored, not triggered.
- `TestE1Tracker_SecondObservation_TooSoon` — two Observes 30 s apart → not triggered, state retained.
- `TestE1Tracker_DryRunThenApplyAfter60s_Triggers` — two Observes ≥60 s apart (simulating a dry-run sweep at t=0 followed by an apply sweep at t≥60 s) → second call returns triggered=true. **This is the core regression test for the daemon-lifetime promotion** — would fail with the original sweep-local design.
- `TestE1Tracker_TransientError_NoE1` — Observe with `AnomalyCwdTransientStatError` only → tracker does NOT record state; subsequent ENOENT starts fresh 60 s clock.
- `TestE1Tracker_CwdRecovered_StateCleared` — Observe with ENOENT, then Observe with no anomaly → state cleared; next ENOENT restarts clock.
- `TestE1Tracker_ConcurrentObserves_Safe` — `go test -race`: 100 concurrent Observes for distinct brokerKeys do not corrupt the map.
- `TestE1Tracker_Reset_ClearsState` — Reset(key) drops the state; subsequent Observe is fresh-first.

**Acceptance**: E1 tests all green; `go test -race ./internal/codexbroker/...` clean.

**Commit**: `feat(codexbroker): E1 emergency override — daemon-lifetime tracker (P2 task G)`

---

### Task H — `override.go` + `quarantine.go`: E2 + quarantine persist + boot restore

**Files**: `override.go` (extend), `quarantine.go`, `quarantine_test.go`, `override_test.go` (extend)

**Pre-condition**: Task A, Task G done.

**Scope** — `override.go` extension:
- `EvalE2(rec BrokerRecord, knownFingerprint *BrokerFingerprint) (triggered bool, detail string)` where `BrokerFingerprint{Executable, Cmdline, PidFile string}`.
- Fingerprint mismatch: `(pid, lstart, executable, cmdline)` differs from most-recently-known fingerprint for this `brokerKey` → E2 triggered → quarantine, not kill. Per spec §5.3 line 400.

**Scope** — `quarantine.go`:
- `QuarantineStore` with `Load(path string) (*QuarantineFile, error)`, `Save(path string, qf *QuarantineFile) error` (atomic: write to `path.tmp`, `os.Rename`), `AddEntry(qf *QuarantineFile, entry QuarantineEntry) *QuarantineFile`, `PurgeExpired(qf *QuarantineFile, now time.Time) *QuarantineFile`.
- `QuarantineFile` matches spec §5.3 JSON schema (line 408-422) with `version=1`.
- `IsQuarantined(qf *QuarantineFile, key string) bool`.
- Boot restore: `Module.Init` calls `QuarantineStore.Load`; subsequent sweep calls check `IsQuarantined` before emitting `Kill=true`.

**TDD tests**:
- `TestEvalE2_FingerprintMatch_NoTrigger`.
- `TestEvalE2_FingerprintMismatch_Triggered`.
- `TestQuarantineStore_RoundTrip` — write + read back.
- `TestQuarantineStore_AtomicWrite` — simulate write failure; old file intact.
- `TestQuarantineStore_PurgeExpired` — expired entries removed, active retained.
- `TestIsQuarantined_Found` / `TestIsQuarantined_NotFound`.

**Acceptance**: E2 + quarantine tests all green; no file left in half-written state.

**Commit**: `feat(codexbroker): E2 override + quarantine.json persist + boot restore (P2 task H)`

---

### Task I — `launchregistry.go`: schema + persistence + lookup

**Files**: `launchregistry.go`, `launchregistry_test.go`

**Pre-condition**: Task A done.

**Scope**:
- `LaunchRegistry` with `Load(path string) (*LaunchRegistryFile, error)`, `Save(path string, f *LaunchRegistryFile) error` (atomic rename), `Register(f *LaunchRegistryFile, e LaunchEntry) *LaunchRegistryFile`, `Remove(f *LaunchRegistryFile, brokerKey string, pid int) *LaunchRegistryFile`, `Lookup(f *LaunchRegistryFile, brokerKey string) (*LaunchEntry, bool)`, `Empty(f *LaunchRegistryFile) bool`.
- `LaunchRegistryFile{Version int; Entries []LaunchEntry}`.
- `LaunchEntry{BrokerKey, TmuxPane, CallerSessionID string; Pid int; Lstart time.Time; LaunchedAt time.Time}`.
- Path: `<pluginDataRoot>/launch-registry.json`.
- `LaunchRegistryReader` interface (`Lookup(key string) (*LaunchEntry, bool)` + `Empty() bool`) satisfied by `*LaunchRegistryFile`. Used by Task E predicate C and Task F decision composer.
- **P2 does not populate the registry automatically** (that's a P3 spawn-hook concern). The registry file may be absent on mlab initially — `Load` returns an empty registry on `os.IsNotExist`. P2 reads but does not write during sweep; only `Module.Init` loads it. Tests use fixture files.
- **Lookup-miss is a quarantine signal, not an error.** The registry is the *authoritative* ownership source per spec §2 + §5.1 line 371. When `Lookup(brokerKey)` returns `false` (or `Empty()` returns true), the decision layer (Task F) MUST classify the broker as `foreign_quarantine`. This is essential because P2 ships before the spawn-hook lands — on mlab, every visible broker will have no registry entry, and any other interpretation would risk auto-killing the operator's own work. Documented contract: `LaunchRegistryReader` consumers must inspect `Empty()` first to distinguish "no purdex broker is tracked yet" (P2 steady state) from "this specific brokerKey is foreign" (post-spawn-hook).

**TDD tests**:
- `TestLaunchRegistry_LoadMissing` — returns empty registry, `Empty()==true`, no error.
- `TestLaunchRegistry_RoundTrip` — register + save + load + lookup.
- `TestLaunchRegistry_Remove` — remove existing entry, lookup returns false; `Empty()` reflects post-removal state.
- `TestLaunchRegistry_LookupMissing` — returns false (entries present but key absent).
- `TestLaunchRegistry_Empty_ZeroEntries` / `TestLaunchRegistry_Empty_AfterRegister` — `Empty()` switches false after first `Register`.
- `TestLaunchRegistry_AtomicSave` — simulate rename failure; old file intact.

**Acceptance**: launch registry tests all green; lookup satisfies `LaunchRegistryReader` interface.

**Commit**: `feat(codexbroker): launch registry — schema + persist + lookup (P2 task I)`

---

### Task J — `killer.go`: Step 0 re-verify identity

**Files**: `killer.go`, `killer_test.go`

**Pre-condition**: Task A done; staleness.go (Task B) done.

**Scope**:
- `VerifyIdentity(rec BrokerRecord, lister ProcessLister) (ok bool, detail string)`: re-fetch `ps` for `rec.PID`; confirm `lstart` matches (±1 s tolerance for lstart string round-trip) and cmdline contains `app-server-broker.mjs`. If mismatch → returns false, caller aborts kill and triggers E2 quarantine for this brokerKey. Per spec §5.4 Step 0, lines 434-437.
- `KillSequence` struct holding: `Rec BrokerRecord`, `Lister ProcessLister`, `Dialer Dialer`, `Signaller Signaller`, `FS FS`, `AuditDir string`, `GracefulTimeout, TermTimeout, KillTimeout time.Duration`.
- Expose `KillResult{GracefulOk, TermOk, KillOk bool; StepLatencyMs [3]int64; CleanedUp bool; Err error}`.

**TDD tests**:
- `TestVerifyIdentity_Match` — fakeLister returns matching pid+lstart+cmdline → ok=true.
- `TestVerifyIdentity_LstartMismatch` → ok=false.
- `TestVerifyIdentity_PidGone` — fakeLister returns no matching pid → ok=false.
- `TestVerifyIdentity_CmdlineMismatch` → ok=false.

**Acceptance**: identity verification tests green; `KillSequence` struct compiles.

**Commit**: `feat(codexbroker): kill sequence Step 0 — identity re-verify (P2 task J)`

---

### Task K — `audit.go` + `killer.go` Step 1: audit preimage write

**Files**: `audit.go`, `audit_test.go`, `killer.go` (extend)

**Pre-condition**: Task J done.

**Scope** — `audit.go`:
- `WritePreimage(auditDir string, rec BrokerRecord, decision DecisionResult, logLines []string) (filePath string, err error)`: writes `audit/orphan-<brokerKey>-<unixTs>.json` with the full `AuditDump` struct (spec §5.5 schema, lines 481-502). Uses atomic write (tmp + rename). `logLines` is the last 200 lines of `broker.log` if readable; empty slice if absent.
- `AppendPostscript(filePath string, result KillResult) error`: opens the existing JSON, adds `killSequence` section, re-encodes, atomic write. Best-effort (P2 logs error but does not fail).
- `ReadBrokerLogTail(stateDir string, fs FS, lines int) []string` — reads `stateDir/broker.log` tail.

**Scope** — `killer.go` extension:
- `KillSequence.Run(ctx context.Context) (KillResult, error)` Step 1 implementation: call `WritePreimage` before any signalling; abort whole sequence if write fails (audit is non-negotiable per spec §5.4 line 465).

**TDD tests** — `audit.go`:
- `TestWritePreimage_HappyPath` — reads back file, validates JSON schema.
- `TestWritePreimage_AtomicWrite` — simulate rename failure; old file intact.
- `TestAppendPostscript_AddsKillSequence` — existing preimage file gets postscript added.
- `TestReadBrokerLogTail_LastNLines` — fixture file with 300 lines → last 200 returned.
- `TestReadBrokerLogTail_FileMissing` → empty slice, no error.

**TDD tests** — `killer.go`:
- `TestKillSequence_AbortIfPreimageWriteFails` — audit dir unwritable → Run returns error, no signal sent.

**Acceptance**: audit preimage tests green; Step 1 commits before any signal.

**Commit**: `feat(codexbroker): kill Step 1 — audit preimage write (P2 task K)`

---

### Task L — `killer.go` Step 2: graceful RPC shutdown

**Files**: `killer.go` (extend), `killer_test.go` (extend)

**Pre-condition**: Task K done.

**Scope**:
- Step 2: `KillSequence.stepGraceful(ctx context.Context) bool` — dial `rec.Endpoint` (unix socket) via `Dialer`, send `{"method":"shutdown"}` POST to the broker's local HTTP, wait `GracefulTimeout` (default 5 s). Returns true if broker process exits within budget. Failure → log + return false; caller proceeds to Step 3.
- Dialer already in P1 `ScannerOpts`; `KillSequence` carries its own `Dialer` field (separate instance from Scanner).

**TDD tests**:
- `TestStepGraceful_Success` — fake dialer accepts connection, fake signaller checks process gone → true.
- `TestStepGraceful_RPCTimeout` — fake dialer hangs beyond budget → false, no panic.
- `TestStepGraceful_EndpointMissing` — `rec.Endpoint == ""` → false immediately, no dial attempt.

**Acceptance**: graceful step tests green.

**Commit**: `feat(codexbroker): kill Step 2 — graceful RPC shutdown (P2 task L)`

---

### Task M — `killer.go` Steps 3 + 4: SIGTERM + SIGKILL

**Files**: `killer.go` (extend), `killer_test.go` (extend)

**Pre-condition**: Task L done.

**Scope**:
- Step 3: `stepSIGTERM(ctx context.Context) bool` — `getpgid(pid)` via `unix.Getpgid`; refuse if `pgid <= 1` (log + abort); call `Signaller.Kill(-pgid, syscall.SIGTERM)`; poll lister until process gone or `TermTimeout` exceeded (default 5 s). Per spec §5.4 lines 448-453.
- Step 4: `stepSIGKILL(ctx context.Context) bool` — `Signaller.Kill(-pgid, syscall.SIGKILL)`; poll 2 s budget. Per spec §5.4 line 455.
- `unix.Getpgid` requires `golang.org/x/sys/unix`.

**TDD tests**:
- `TestStepSIGTERM_ProcessExitsGracefully` — fake signaller records SIGTERM; fake lister returns empty on second poll → true.
- `TestStepSIGTERM_Timeout_ProcStillAlive` → false, proceeds to Step 4.
- `TestStepSIGTERM_PgidLeOne_Refused` — simulate `getpgid` returning 1 → abort, log, return false.
- `TestStepSIGKILL_KillsStubbornProcess` — fakeLister returns empty after SIGKILL → true.
- `TestStepSIGKILL_2sTimeout_Partial` — fakeLister never returns empty → false + KillOk=false in result.

**Acceptance**: SIGTERM/SIGKILL step tests green; no kill to pgid ≤ 1 possible.

**Commit**: `feat(codexbroker): kill Steps 3+4 — SIGTERM + SIGKILL (P2 task M)`

---

### Task N — `killer.go` Step 5: verify family gone

**Files**: `killer.go` (extend), `killer_test.go` (extend)

**Pre-condition**: Task M done.

**Scope**:
- Step 5: `stepVerifyGone(ctx context.Context) bool` — re-run `lister.List` and scan for any pid in the process group sharing the same `lstart` as `rec`. Best-effort: if lister fails, log and return true (not blocking cleanup). Timeout: 2 s. Per spec §5.4 lines 457-459.

**TDD tests**:
- `TestStepVerifyGone_AllGone` → true.
- `TestStepVerifyGone_StrayChildRemains` — fakeLister returns a child with matching lstart → false.
- `TestStepVerifyGone_ListerError` → true (best-effort).

**Acceptance**: verify-gone tests green.

**Commit**: `feat(codexbroker): kill Step 5 — verify family gone (P2 task N)`

---

### Task O — `sockverify.go` + `killer.go` Step 6: cleanup + socket-inode verify

**Files**: `sockverify.go`, `sockverify_darwin.go`, `sockverify_linux.go`, `sockverify_other.go`, `sockverify_test.go`, `killer.go` (extend)

**Pre-condition**: Task N done.

**Scope** — `sockverify.go`:
- Interface `SocketVerifier` with `AnyPidHoldsSocket(sockPath string, timeout time.Duration) (held bool, err error)`.
- `sockverify_darwin.go`: bind `proc_pidinfo(PROC_PIDLISTFDS)` via `syscall.Syscall6` (no CGo). Enumerate all pids from `ps`; for each alive pid call `proc_pidinfo` to list open FDs; compare inode against `stat(sockPath).Ino`. 1 s total budget. Per spec §5.4 lines 470-472.
- `sockverify_linux.go`: walk `/proc/*/fd/` symlinks; readlink each; compare against `sockPath`. 1 s budget.
- `sockverify_other.go`: `lsof -nP -- <sockPath>` with 1 s timeout. Per spec §5.4 line 472.
- If budget exceeded or both strategies fail → return `held=true` (conservative: defer cleanup).

**Scope** — `killer.go` Step 6:
- `stepCleanup(ctx context.Context, verifier SocketVerifier) bool` — if `rec.SocketDir != ""`: call `verifier.AnyPidHoldsSocket(sockPath, 1*time.Second)`; if held → defer (log, return false); if not held → `os.RemoveAll(rec.SocketDir)`. Then call `AppendPostscript`. Per spec §5.4 lines 461-462.

**TDD tests** — `sockverify_test.go` (build tag `!integration`):
- `TestSocketVerifier_OtherFallback_NoHolders` — fake `lsof` output with no matches → held=false.
- `TestSocketVerifier_OtherFallback_Held` — fake lsof with matching PID → held=true.
- `TestSocketVerifier_Timeout_ReturnsHeld` — fake lsof hangs → held=true.

**TDD tests** — `killer_test.go` extension:
- `TestStepCleanup_SocketNotHeld_Removed` — fake verifier returns held=false; assert `os.RemoveAll` called on socketDir.
- `TestStepCleanup_SocketHeld_Deferred` — fake verifier returns held=true; dir NOT removed.
- `TestStepCleanup_NoSocketDir_SkipsVerify` — `rec.SocketDir == ""` → cleanup skipped, postscript still written.

**Acceptance**: sockverify + cleanup tests green; `go build ./...` clean on Darwin and Linux (build tags compile cleanly).

**Commit**: `feat(codexbroker): sockverify + kill Step 6 — socket-inode verify + cleanup (P2 task O)`

---

### Task P — `audit.go`: postscript + full KillSequence.Run wiring

**Files**: `killer.go` (extend — wire all steps), `audit.go` (AppendPostscript — Task K stub becomes full impl), `killer_test.go` (extend)

**Pre-condition**: Tasks J–O all done.

**Scope**:
- Wire `KillSequence.Run(ctx)` to call Steps 0-6 in order; record `KillResult.StepLatencyMs` for each of the three signal steps; return final `KillResult`.
- `AppendPostscript` (Task K stub): full implementation reads preimage file, merges `killSequence` section, re-encodes, atomic rename.

**TDD tests**:
- `TestKillSequence_HappyPath_AllSteps` — fake lister + signaller + verifier + dialer; assert `KillResult{GracefulOk: ?, TermOk: true, KillOk: false, CleanedUp: true}` (graceful timeout → SIGTERM kills it → Step 4 not needed → verify gone → cleanup).
- `TestKillSequence_StepLatencyMs_Populated` — assert all 3 latencies > 0 after a run with fake delays.
- `TestKillSequence_E2Abort_IdentityMismatch` — Step 0 mismatch → KillResult.Err != nil, no signals sent.

**Acceptance**: full KillSequence integration tests green; `go test -race ./internal/codexbroker/...` clean.

**Commit**: `feat(codexbroker): wire full kill sequence Steps 0-6 + audit postscript (P2 task P)`

---

### Task Q — `sweep.go`: HTTP handler + singleflight gate

**Files**: `sweep.go`, `sweep_test.go`

**Pre-condition**: Tasks F, G, H, I, P all done (decision + kill + quarantine + registry available).

**Scope**:
- `SweepHandler` struct holding `Scanner *Scanner`, `Quarantine *QuarantineStore`, `Registry *LaunchRegistryFile`, `E1Tracker *E1Tracker` (daemon-lifetime, see Task G), `KillerFactory func(BrokerRecord) *KillSequence`, plus the concurrency primitives below.
- **Concurrency design** (replaces the originally-considered `singleflight.Group` per round-1 finding #4): `singleflight` semantics coalesce concurrent calls to share *one* result, which is wrong for sweeps — a dry-run sneaking in front of an apply would cause the apply to read the dry-run response and silently skip kills (operation lost); a `__all__` apply colliding with a filtered apply on the same broker could double-signal (PID-reuse race). The replacement uses two layers of `sync.RWMutex`:
  - `globalApplyMu sync.RWMutex` (one per `SweepHandler`). Acquired in **write** mode by `__all__ apply`; in **read** mode by every other request (filtered apply + any dry-run). This blocks `__all__ apply` from running concurrently with anything else, while letting non-`__all__` work proceed in parallel.
  - `perBrokerMu sync.Map[brokerKey]*sync.RWMutex`. After releasing/holding the outer `globalApplyMu` read-lock, filtered apply acquires the per-broker mutex in **write** mode for its specific brokerKey; filtered or `__all__` dry-run acquires it in **read** mode. Construction uses `LoadOrStore(&sync.RWMutex{})` for one-shot init.
  - **Lock order is fixed**: `globalApplyMu` always before `perBrokerMu`. `__all__ apply` only ever holds `globalApplyMu.Lock()` (does not touch any per-broker mutex — the global write-lock already excludes everything else). This makes deadlock impossible by construction (no inversion possible).
  - Mapping query params → lock acquisition pattern (round-2 finding #2 — unfiltered dry-run case made explicit per consulting recommendation Path X):
    | mode | brokerKey | globalApplyMu | perBrokerMu(brokerKey) | rationale |
    |------|-----------|---------------|------------------------|-----------|
    | dry-run | set | RLock | RLock | filtered dry-run touches one broker; cannot race with apply on a different broker, only with apply on same broker (RLock vs Lock blocks correctly). |
    | dry-run | unset (`__all__`) | **Lock (write)** | n/a | unfiltered dry-run iterates the entire inventory; specifying which per-broker mutexes to take is undefined at request time (newly discovered brokers mid-scan would not be locked). Taking the global write-lock keeps the design simple, prevents apply on any broker from racing the dry-run snapshot, and is acceptable because dry-run latency is bounded (no kill, just inventory + decision compute). Trade-off: blocks parallel dry-runs — accepted because the dashboard expectation is one operator-initiated sweep at a time, not a high-QPS endpoint. |
    | apply | set | RLock | Lock (write) | filtered apply mutates one broker; per-broker write-lock excludes other apply on same key + serialises against same-broker dry-run. |
    | apply | unset (`__all__`) | Lock (write) | n/a | unfiltered apply needs full exclusivity to avoid double-signal across overlapping `__all__` invocations. |
- `func (h *SweepHandler) HandleSweep(w http.ResponseWriter, r *http.Request)`:
  - Method guard: 405 for non-POST.
  - Parse `mode` query param (default `dry-run`); `apply` enables kills.
  - Parse optional `brokerKey` filter. **No `force` flag** — round-2 finding #1 consulting recommendation Path D: `mode=apply&brokerKey=<X>` is itself the spec §5.1 line 371 operator override; introducing a separate `force` flag was redundant scope creep beyond spec.
  - Acquire locks per the table above; defer release in reverse order.
  - Run `Scanner.Scan(ctx)` to get current inventory.
  - For each `BrokerRecord` (filtered by brokerKey if set): check quarantine (`IsQuarantined`) → skip if quarantined; call `EvalDecision`; populate `SweepResponse.Evaluated`.
  - **Kill semantics — apply path** (spec §5.1 line 371): the sweep handler decides whether to honour `DecisionResult.Reason == "foreign_quarantine"` based on whether the operator made an explicit per-broker decision:
    | mode | brokerKey | DecisionResult.Reason | Kill issued? | Audit reason |
    |------|-----------|-----------------------|--------------|--------------|
    | apply | unset (`__all__`) | (any) | only if `Kill=true` AND not `foreign_quarantine` | per `DecisionResult.Reason` |
    | apply | set | `foreign_quarantine` | **yes** — `brokerKey` filter is the operator's explicit override per spec §5.1 line 371 | `manual_sweep_override` |
    | apply | set | (anything else where `Kill=true`) | yes | per `DecisionResult.Reason` |
    | apply | set | `Kill=false` and not `foreign_quarantine` | no — broker is alive (predicate true) | n/a |
    
    Mlab safety: unfiltered `mode=apply` on the 50+ pre-existing brokers issues **zero** kills because every broker is `foreign_quarantine` (registry empty) and the unfiltered path requires `Reason != "foreign_quarantine"`. The only path to kill a foreign broker is `mode=apply&brokerKey=<X>` — the operator naming a specific broker is the override.
  - If kill is to be issued per the table above: call `KillSequence.Run`; on success call `h.E1Tracker.Reset(rec.BrokerKey)` so a respawned broker doesn't inherit a stale 60 s clock; record outcome in `SweepResponse.Applied` or `SweepResponse.Errors`.
  - Return `200 JSON SweepResponse`.
- Timeout: 30 s for `mode=apply` (accounts for graceful + SIGTERM + SIGKILL budget); 10 s for `mode=dry-run`. Lock waits are bounded by the request context — if `globalApplyMu` is held by an `__all__ apply` already in flight, a queued request waits up to its own context deadline; on timeout the handler returns 503 + a `Retry-After` hint rather than starving.

**TDD tests**:
- `TestSweepHandler_DryRun_NoKills` — mode=dry-run, one broker qualifies for kill → evaluated list populated, applied empty.
- `TestSweepHandler_Apply_KillsOrphan` — mode=apply, fake KillSequence → applied list populated; populated registry has the brokerKey so foreign filter does not block.
- `TestSweepHandler_BrokerKeyFilter` — brokerKey=X → only record with matching key evaluated.
- `TestSweepHandler_QuarantinedSkipped` — broker in quarantine → not in evaluated list.
- `TestSweepHandler_ApplyAll_RegistryEmpty_NoKills` — unfiltered mode=apply with empty registry on 50 brokers; **zero** kills issued; all decisions are `foreign_quarantine`. Mlab mass-kill safety regression test.
- `TestSweepHandler_BrokerKeyApply_OverridesForeignQuarantine` — `mode=apply&brokerKey=<X>` with empty registry → `Reason="foreign_quarantine"` BUT KillSequence IS invoked because the explicit `brokerKey` filter is itself the spec §5.1 line 371 operator override; audit dump reason=`manual_sweep_override`. This is the round-2 finding #1 acceptance regression test.
- `TestSweepHandler_BrokerKeyApply_AlivePredicate_NoKill` — `mode=apply&brokerKey=<X>` where predicate A is true (broker actively running) → kill NOT issued (operator override does not kill an alive broker; only suppresses the foreign-quarantine guard).
- `TestSweepHandler_MethodNotPost_405`.
- `TestSweepHandler_ScanFailure_503`.

**Concurrency TDD tests** (replace the originally-planned singleflight test; round-1 finding #4 + round-2 finding #2 unfiltered dry-run exclusivity):
- `TestSweepHandler_ConcurrentApplyApply_SerializedPerBroker` — two goroutines POST `apply&brokerKey=X` concurrently; assert KillSequence.Run is invoked exactly twice in serial (not concurrently), and the second invocation observes the post-first-kill state. No double-signal in the same lstart window.
- `TestSweepHandler_AllApply_BlocksFilteredApply_SameBroker` — start `__all__ apply` (slow KillSequence stub), then start `apply&brokerKey=X`; assert the second request blocks until the first completes; assert no two `KillSequence.Run` invocations are in flight at the same time.
- `TestSweepHandler_FilteredDryRun_ReadLock_NoBlockOtherFilteredDryRun` — two concurrent `dry-run&brokerKey=X` and `dry-run&brokerKey=Y` requests run their `Scanner.Scan` in parallel (barrier-style fake Scanner). Confirms read-lock semantics for filtered dry-runs across distinct brokers.
- `TestSweepHandler_UnfilteredDryRun_Exclusive` — round-2 finding #2 regression: concurrent unfiltered `mode=dry-run` + any other request (including another unfiltered dry-run, filtered apply, `__all__ apply`) → second request blocks until the first releases the global write-lock. Asserts the consulting recommendation Path X invariant: unfiltered dry-run is fully exclusive, preventing any apply from racing the inventory snapshot.
- `TestSweepHandler_DryRunBlocksApplySameBroker` — filtered dry-run for brokerKey=X holds RLock; concurrent filtered apply&brokerKey=X must wait. Asserts ordering invariant (no kill executes while a same-broker dry-run snapshot is mid-flight).
- `TestSweepHandler_AllApplyExclusive` — concurrent `__all__ apply` + any other request: assert only one of them is in flight at a time, regardless of mode/brokerKey of the other.
- `TestSweepHandler_LockTimeout_503` — long-running `__all__ apply` + queued request whose context deadline expires before the global lock is released: assert 503 + `Retry-After` header, no panic, no goroutine leak.
- `TestSweepHandler_Concurrency_NoDeadlock` (`go test -race`): 50 mixed concurrent requests (random mix of filtered/unfiltered dry-run, filtered apply, `__all__ apply`), all complete within a generous wallclock budget, no race detector hits.

**Acceptance**: sweep handler tests green; mlab unfiltered apply against empty registry issues zero kills (mass-kill safety); operator-explicit `mode=apply&brokerKey=<X>` correctly overrides foreign quarantine and triggers KillSequence.

**Commit**: `feat(codexbroker): POST /api/codex/brokers/sweep handler + RWMutex serialisation (P2 task Q)`

---

### Task R — Module wiring (register sweep route + boot quarantine restore)

**Files**: `module.go` (modify), `module_test.go` (extend), `cmd/pdx/main.go` (no change needed — Module already registered)

**Pre-condition**: Task Q done.

**Scope**:
- Add `SweepHandler`, `QuarantineStore`, `LaunchRegistryFile`, `E1Tracker` fields to `Module`.
- `Module.Init`: after constructing Scanner, load `quarantine.json` (error-tolerant: missing file is empty registry), load `launch-registry.json` (same), construct `E1Tracker := NewE1Tracker()` (no persisted state — daemon-lifetime in-memory only), construct `SweepHandler` injecting all four (Scanner, QuarantineStore, LaunchRegistryFile, E1Tracker).
- `Module.RegisterRoutes`: add `POST /api/codex/brokers/sweep`.
- No changes to `GET /api/codex/brokers` handler.
- No `Module.Start` goroutine (P3 concern — audit pruner + tick will live here).
- Daemon restart drops `E1Tracker` state — documented as acceptable (spec §5.3 only requires two scans ≥60 s apart; restart conservatively restarts the 60 s clock).

**TDD tests**:
- `TestModule_RegistersSweepRoute` — mux has both GET and POST paths after init.
- `TestModule_Init_QuarantineMissing_OK` — no quarantine.json → init succeeds with empty quarantine.
- `TestModule_Init_E1TrackerEmpty` — fresh `Module.Init` produces an `E1Tracker` whose `Snapshot()` returns an empty map.

**Acceptance**: daemon builds and starts; both routes visible; `go test ./internal/codexbroker/...` green.

**Commit**: `feat(codexbroker): wire sweep route + boot quarantine restore into module (P2 task R)`

---

### Task S — Integration test (build tag `integration`)

**Files**: `sweep_integration_test.go`

**Pre-condition**: Task R done.

**Scope**:
- Build tag `//go:build integration`.
- Spawn a real `app-server-broker.mjs serve --cwd <tmpdir>` broker. `t.Cleanup` immediately registered to `SIGKILL` + `RemoveAll`.
- Wait for broker.json + socket (poll 5 s).
- Run `POST /api/codex/brokers/sweep?mode=dry-run` against a live daemon instance (or via direct `SweepHandler` with a real Scanner).
- Assert: the spawned broker appears in `evaluated`, all three predicates evaluated, `Kill=false` for the live broker.
- Kill the broker externally (SIGTERM). Wait 2 s.
- Run `POST .../sweep?mode=apply&brokerKey=<key>` (operator-explicit override per spec §5.1 line 371).
- Assert: `applied` list contains the brokerKey; `audit/orphan-<key>-*.json` exists; preimage + postscript both present; audit `reason` is `manual_sweep_override` (because integration test runs with empty registry — same path mlab will exercise); `cxc-*` dir removed.
- Additional safety assertion: prior to the operator-explicit apply, run `POST .../sweep?mode=apply` (unfiltered, no brokerKey) → `applied` list MUST be empty even though the dead broker is in `evaluated` with `Reason="foreign_quarantine"`. This integration-tests the round-2 finding #1 mass-kill safety property.

**Acceptance**:
- `go test -tags=integration ./internal/codexbroker/...` passes on mlab with node available.
- Zero broker processes remain after `t.Cleanup` returns.
- Documented in PR description as "must run manually on mlab before merge".

**Commit**: `test(codexbroker): integration test — dry-run + apply sweep on real broker (P2 task S)`

---

### Task T — Live verification on mlab + PR

**Scope** (operational, no code commit):
- Build daemon: `go build -o bin/pdx ./cmd/pdx` in worktree.
- Restart daemon with new build.
- **Dry-run sweep** against all brokers: `curl -X POST -H "X-Pdx-Token: ..." "http://100.64.0.2:7860/api/codex/brokers/sweep?mode=dry-run" | jq '.evaluated | length'`. Verify ≥ 50 brokers evaluated. Spot-check 5 records: predicates A/B/C match expected hand-evaluated state for known-idle orphans.
- **Mass-kill safety pre-check**: issue `?mode=apply` (no brokerKey) first. Verify `applied` list is **empty** despite ≥ 50 brokers in `evaluated` (because every record has `Reason="foreign_quarantine"` while registry is empty pre-PR-G). If even one kill fires here, STOP — the unfiltered safety guard is broken and should not ship.
- **Apply sweep on one known-orphan via operator override**: identify a broker with all predicates false + idle > 30 min from the dry-run output. Issue `?mode=apply&brokerKey=<key>` (the operator-explicit `brokerKey` filter is the spec §5.1 line 371 override). Verify `applied` list populated, audit file written with `reason="manual_sweep_override"`, cxc-* dir removed (or defer logged if socket still held).
- Verify `GET /api/codex/brokers` still returns 200 after sweep (P1 endpoint untouched).
- Capture output for PR description body including: evaluated count, one sample DecisionResult trace, audit file content, before/after process count via `ps -ef | grep app-server-broker | wc -l`.
- Open PR via `gh pr create`.

**Acceptance**: PR opened, CI green, test plan in PR body.

---

## 5. Build sequence

```
A (P2 types) → B (staleness)
                     ↓
       C (predicate A) → D (predicate B) → E (predicate C)
                                                     ↓
                                               F (kill rule)
                                              / \
                                     G (E1)   H (E2 + quarantine)
                                              I (launch registry)
                                              ↓
                                        J (Step 0 re-verify)
                                              ↓
                                        K (Step 1 audit preimage)
                                              ↓
                                        L (Step 2 graceful RPC)
                                              ↓
                                        M (Steps 3+4 TERM+KILL)
                                              ↓
                                        N (Step 5 verify gone)
                                              ↓
                                        O (sockverify + Step 6 cleanup)
                                              ↓
                                        P (wire KillSequence.Run + postscript)
                                              ↓
                                        Q (sweep handler)
                                              ↓
                                        R (module wiring)
                                              ↓
                                        S (integration test)
                                              ↓
                                        T (live verify + PR)
```

**Checkpoint after every task**: `go test ./internal/codexbroker/... && go build ./...`.

**Subagent split** (to avoid context overflow): dispatch subagent 1 for tasks A-K; checkpoint with full test run; dispatch subagent 2 for tasks L-T. Subagent 2 receives the task-A–K commit SHA as its baseline.

---

## 6. TDD discipline

Each task follows: write failing test → minimal production code → green → refactor if needed → commit. No implementation-first allowed. Commit subjects carry the task letter, e.g. `feat(codexbroker): predicate B — recent delivery readable (P2 task D)`.

Between task groups (after F, after P), run `go test -race ./internal/codexbroker/...` and fix any race before proceeding.

---

## 7. Verification gates

| Gate | Command | When |
|---|---|---|
| Compile | `go build ./...` | After every commit |
| Vet | `go vet ./internal/codexbroker/...` | After every commit |
| Unit tests | `go test ./internal/codexbroker/...` | After every commit |
| Race | `go test -race ./internal/codexbroker/...` | After tasks F, P, R |
| Integration | `go test -tags=integration ./internal/codexbroker/...` | Before PR open (mlab only) |
| Lint | `gofmt -d` clean | Before push |
| Live dry-run | 50+ brokers evaluated with correct predicates | Before PR open |
| Live apply | 1 known-orphan killed + audit dump verified | Before PR open (manual) |

---

## 8. PR review plan

| Round | Reviewer | Focus |
|---|---|---|
| R1 | codex standard | Spec alignment, Go idioms, test quality, build-tag correctness. |
| R2 (3-parallel) | codex adversarial | See focus prompts below. |

**R2 focus text**:

| Reviewer | Focus prompt seed |
|---|---|
| Attack | Find: PID-reuse race between Step 0 verify and Step 3 SIGTERM (window where pid is reused between check and kill); fingerprint forgery via argv injection; quarantine bypass (path where `Kill=true` is returned despite quarantine entry); kill loop if broker respawns during sweep; pgid=1 edge case on abnormal broker invocation; concurrent-apply correctness — confirm sweep handler RWMutex layering serialises every (apply, apply) and (apply, dry-run) pair on the same brokerKey, no double-signal in same lstart window, no deadlock between `__all__ apply` and filtered requests; lock-acquisition starvation under heavy `__all__ apply` load. |
| Defense | Verify: foreign-broker quarantine boundary — confirm unfiltered `mode=apply` (no `brokerKey`) NEVER kills a broker whose registry lookup misses; the only kill path for foreign brokers is operator-explicit `mode=apply&brokerKey=<X>` (the `brokerKey` filter IS the spec §5.1 line 371 override — no separate `force` flag); mlab-empty-registry case classifies all 50+ brokers as `foreign_quarantine` with zero kills issued via the unfiltered path; launch registry missing fallback — predicate C must return false (not panic) when registry absent; E1 transient-ENOENT false positive — ESTALE/EIO/EACCES must not accumulate toward E1; `mode=apply` behaviour is a strict superset of `mode=dry-run` evaluated list (no extra kills not in dry-run); audit preimage must be committed before any signal reaches broker; **scope correction validation** (round-2 finding #1) — judge whether deferring registry-write to PR-G is acceptable given the safety property holds, or whether spec §5.1 line 369 wording demands spawn-write in P2. |
| Health | Check: `codexbroker` package SRP — `decision.go` vs `killer.go` vs `sweep.go` responsibility boundaries; `audit.go` not leaking into `killer.go` beyond the two defined call sites; `sockverify_darwin.go` / `sockverify_linux.go` / `sockverify_other.go` — are the three files the right split or should interface be extracted to `sockverify.go`; file sizes (anything > 300 LOC needs justification); test fixture ownership clear (no cross-task shared mutable fixtures). |

---

## 9. Risks specific to P2 implementation

| Risk | Implementation-time mitigation |
|---|---|
| PID-reuse between Step 0 and Step 3 | Step 0 re-verifies `(pid, lstart, cmdline)` immediately before `WritePreimage`. Total window between Step 0 check and SIGTERM is ≤ 5 s graceful budget + negligible. If reuse occurs within that window, E2 quarantine on next sweep catches it. Documented as acceptable known-window risk. |
| Killing broker mid-RPC (data loss) | Predicate A checks RPC `thread/list` AND state.json queued/running; predicate B checks recent completedAt + parseable jobs/<id>.json. Both gates must fail before kill proceeds. `jobs/<id>.json` durability is broker-independent (result is on disk). |
| False-positive kill on idle broker | Idle timeout gate (default 30 min) AND all three predicates false required. Foreign-broker gets quarantine-only path. E1 requires two confirmed ENOENT scans ≥ 60 s apart. Dry-run mode available for operator review before apply. |
| Daemon crash during kill sequence | Step 1 writes audit preimage before any signal. Atomic rename ensures preimage is either complete or absent (no partial JSON). On daemon restart, `quarantine.json` is loaded; broker is re-evaluated from scratch; previous kill result in audit dir provides forensics. |
| Killing wrong broker (pgid ≤ 1) | Step 3 always calls `unix.Getpgid(pid)` and refuses to signal if `pgid <= 1`. Test `TestStepSIGTERM_PgidLeOne_Refused` enforces this. |
| Network-mounted cwd transient stat failure triggering E1 | E1 requires definitive `ENOENT` (`os.IsNotExist` strictly). ESTALE / EIO / EACCES / generic timeout feed `cwd_transient_stat_error` anomaly and skip E1 accumulation. `TestE1Tracker_TransientError_NoE1` enforces this. |
| **E1 cross-sweep state lost** (sweep handler can't accumulate observations) | `E1Tracker` is daemon-lifetime in-memory (Task G), owned by `Module`, injected into `SweepHandler`. Two sweeps ≥60 s apart now correctly trigger E1. Restart resets the 60 s clock — documented as conservative behaviour, not a regression. `TestE1Tracker_DryRunThenApplyAfter60s_Triggers` is the regression test. |
| Quarantine file corrupt on boot | `QuarantineStore.Load` treats `json.Unmarshal` failure as an empty registry + logs a warning. Boot continues. Corrupt file is renamed to `.bak-<ts>` for forensics. |
| Concurrent-sweep correctness (operations lost / double-signal) | `SweepHandler` uses two-layer `sync.RWMutex` (Task Q): `globalApplyMu` excludes `__all__ apply` from any other request; `perBrokerMu` (sync.Map of *RWMutex) serialises filtered apply per brokerKey. Lock order is fixed (`globalApplyMu` → `perBrokerMu`) — deadlock impossible by construction. Replaces the originally-planned `singleflight.Group`, which would have coalesced distinct apply requests and silently dropped operations. Tested by `TestSweepHandler_ConcurrentApplyApply_SerializedPerBroker`, `TestSweepHandler_AllApplyExclusive`, `TestSweepHandler_Concurrency_NoDeadlock` under `-race`. |
| **Unfiltered dry-run vs filtered apply race on same broker** (round-2 finding #2) | Unfiltered dry-run (`mode=dry-run` with no `brokerKey`) cannot pre-declare which per-broker mutexes it will need (broker set is only known after `Scanner.Scan` — and a broker may appear mid-scan). Solution per consulting Path X: unfiltered dry-run takes `globalApplyMu.Lock()` (write-mode). This blocks ALL other sweep requests for the duration of the dry-run, including parallel dry-runs. Trade-off accepted because dry-runs are operator-initiated and bounded in latency (no kill, only inventory + decision compute). `TestSweepHandler_UnfilteredDryRun_Exclusive` is the regression test. Filtered dry-run keeps the cheaper RLock+RLock path. |
| macOS no /proc for sockverify | `sockverify_darwin.go` uses `proc_pidinfo` via `syscall.Syscall6` (no CGo, no fork). If `proc_pidinfo` returns EPERM, falls back to `lsof -nP` with 1 s timeout. Budget-exceeded or both-failed → `held=true` (conservative defer). |
| Symlink/realpath edge cases for cwd | All cwd comparisons in P2 use `EvalSymlinks` chain inherited from P1 — same `FS.EvalSymlinks` call path as P1. No new cwd hashing in P2; comparisons use the resolved `BrokerRecord.CwdResolved` field. |
| **Mlab 50+ existing brokers mass-kill** (P2 ships before spawn-hook populates registry) | `EvalDecision` foreign-broker pre-filter (Task F): empty/missing registry → `Reason="foreign_quarantine"`, `Kill=false`, regardless of A/B/C. Sweep handler unfiltered `mode=apply` (no `brokerKey`) NEVER kills foreign brokers — every record's `Reason="foreign_quarantine"` and unfiltered apply requires `Reason != "foreign_quarantine"`. `TestEvalDecision_RegistryMissing_AllForeignQuarantine` and `TestSweepHandler_ApplyAll_RegistryEmpty_NoKills` are mlab-safety regression tests. The only path to kill a foreign broker is operator-explicit `?mode=apply&brokerKey=<X>` (the brokerKey filter IS the spec §5.1 line 371 override — no separate `force` flag, per round-2 finding #1 consulting recommendation Path D). |

---

## 10. Estimate

| Task | Est. LOC (impl + test) | Notes |
|---|---|---|
| A types | 130 + 80 | Additive to types.go (+ StateJobLite + ReadStateJobs helper + E1State + tests) |
| B staleness | 60 + 80 | |
| C pred A | 90 + 130 | RPC + jobs param + RPC-down stale fallback test |
| D pred B | 50 + 80 | shared []StateJobLite |
| E pred C | 70 + 90 | tmux pane verify |
| F kill rule | 90 + 110 | compose A/B/C + foreign-broker pre-filter + ReadStateJobs orchestration |
| G E1 override | 80 + 110 | daemon-lifetime tracker + concurrent-safe map + Reset/Snapshot |
| H E2 + quarantine | 100 + 120 | atomic file I/O |
| I launch registry | 80 + 100 | |
| J Step 0 verify | 40 + 60 | |
| K Step 1 audit | 90 + 100 | preimage + log tail |
| L Step 2 graceful | 50 + 70 | RPC dial |
| M Steps 3+4 | 70 + 90 | SIGTERM + SIGKILL |
| N Step 5 verify | 30 + 50 | |
| O sockverify + Step 6 | 120 + 80 | 3 platform files |
| P wire KillSequence.Run | 50 + 60 | |
| Q sweep handler | 125 + 220 | two-layer RWMutex + brokerKey-as-override (no `force`) + 8 concurrency tests under -race incl. unfiltered-dry-run exclusivity |
| R module wiring | 40 + 40 | |
| S integration test | 0 + 150 | |
| T live verify + PR | 0 + 0 | operational |
| **Total** | **~1355 + ~1800** | **~3155 lines** |

Production LOC ~1355 is slightly above spec §9 PR-C range of 700-1000 for the "decision predicates + emergency overrides + kill sequence + audit" surface; the +145 over the original estimate is the cost of the round-1 + round-2 plan-review fixes (foreign-broker pre-filter, daemon-lifetime E1Tracker, StateJobLite + ReadStateJobs helper, two-layer RWMutex sweep concurrency, brokerKey-as-override semantic with explicit scope correction). Test density is intentional. Reviewer note: each line of impl over the original estimate maps directly to a regression test for a specific finding.

**Cycle time estimate**: codex plan review (1 round, 0-1 medium findings expected) → subagent 1 TDD tasks A–K (~6-8 h) → subagent 2 TDD tasks L–T (~5-7 h) → PR open → R1 standard (~1 h review) → R2 three-parallel adversarial → finding triage and fixes → squash → bump.

---

## 11. Out of scope reaffirmation

This plan does **not** implement:

- P3: 30 s tick, boot reconcile, ExitWorktree hook, kqueue/inotify watchers, inventory cache TTL.
- P3: `codex.broker.*` config key persistence in `~/.purdex/config.toml`.
- P3: WS broadcast `codex.broker.changed`.
- P3: daemon metrics (`codex_brokers_total`, kill histograms, etc.).
- P4: SPA dashboard.
- L1/L2/L3/L4: Lights series.
- Upstream codex CLI lifecycle RFC.
- Historical state-dir disk cleanup (dirs without `broker.json`).

**Promises P2 makes about future phases**:

1. **Launch registry write point is P3 / Lights PR-G — explicit scope correction from spec §5.1 line 369**: spec wording reads "Purdex must persist a `(brokerKey, pid, lstart) → (tmuxPane, callerSessionID, launchedAt)` mapping at broker spawn time ... The registry schema and persistence point are P2 implementation work but are surfaced here so P1 inventory does not foreclose the design." Plan v3 implements the **schema and persistence I/O** (atomic load/save in `launchregistry.go`) and the **read API** (`Lookup`, `Empty`) but does NOT implement the **spawn-time write** because the daemon currently has no broker-spawn path it owns (verified by codex consulting against `internal/module/session/handler.go`, `internal/tmux/executor.go`, `internal/agent/codex/hooks.go`, `cmd/pdx/hook.go` — purdex creates tmux sessions and installs hook commands, but the broker process is spawned by codex CLI / opencode plugin from those panes; no daemon-owned spawn callback exists). Adding a spawn callback would require coordination with the codex CLI / opencode plugin lifecycle and is therefore deferred to PR-G (Lights spawn-hook integration). P3 will additionally hook ExitWorktree → `Remove`. To compensate for the missing write, P2 ships the **explicit operator override** path: `mode=apply&brokerKey=<X>` overrides foreign-quarantine per spec §5.1 line 371. Reviewer should note this as a **scope correction**, not an omission, and judge whether the safety property (unfiltered apply NEVER mass-kills foreign brokers) plus the explicit override is sufficient for production rollout. If reviewer disagrees and requires spawn-write in P2, the next plan revision must add a Lights-coordination subtask and bump LOC estimate substantially.
2. **Sweep handler is the P3 tick's code path**: `SweepHandler.HandleSweep` logic is reused by the P3 tick goroutine by calling `sweep.go`'s core function directly — the HTTP surface is not duplicated.
3. **Predicate result fields in BrokerRecord for P3 augmented GET**: `DecisionResult` is defined as a standalone struct in P2; P3 adds it to `BrokerRecord` in the `GET /api/codex/brokers` response per spec §6.3.
