# Spec — Codex broker / app-server governance

**Issue**: #668
**Date**: 2026-05-01
**Scope**: Daemon-side lifecycle manager for orphaned codex `app-server-broker.mjs` processes and their state directories. Spec covers governance phases **P1 (Inventory, read-only)**, **P2 (Decision + Kill)** and **P3 (Trigger)** as one document; each phase ships as an independent PR.
**This worktree implements P1 only.** P2/P3 will be implemented in subsequent worktrees referencing this same spec.

---

## 1. Symptom & current state

`mlab` (2026-05-01) live measurement:

| Metric | Count | Notes |
|---|---|---|
| `app-server-broker.mjs serve` processes (orphan, PPID=1) | ~50 | wrapper layer |
| Total `codex.*app-server` processes (broker + node + rust) | 123 | three-layer tree per broker |
| State directories under `~/.claude/plugins/data/codex-openai-codex/state/` | 60 | many for worktrees long-since deleted |
| Memory per broker tree (RSS) | ~130 MB | broker(36) + node(34) + rust(59) |
| Eldest orphan etime | 33 h+ | broker outlived its caller |

Worktree-deleted state still has its broker running, holding 130 MB and a unix socket. This has crossed the reliability cliff once already (issue #668). With current accumulation (~7.8 GB combined RSS) the host is at risk again.

The codex CLI ships **no upstream lifecycle manager**: `app-server-broker.mjs` daemonises itself with `setsid`/`detach` and the codex companion only spawns brokers — nothing reaps them.

## 2. The three invariants (plus pressure signal)

A `codex broker` process must satisfy at least one of **I1–I3** to remain alive. A broker that fails all three is an orphan candidate. **I4 is a pressure signal, not an invariant** — it never overrides I1–I3 and is excluded from the kill predicate.

| # | Invariant | Definition |
|---|---|---|
| **I1** | Execution | Broker has an active codex thread, a queued/running job, **or a pending approval request awaiting human input**. |
| **I2** | Delivery | Result for some completed job is **readable** by `/codex:result <id>` (i.e. `jobs/<id>.json` exists, parseable, contains `result` or `rendered`). Note: *readable*, not yet *read*. Companion's idempotent `/codex:result` always re-reads from disk, so a broker is **not** required to keep result in memory. |
| **I3** | Ownership | A live caller, session, pane, or worktree still references this broker. Concretely: `--cwd` path exists, **and** at least one of (a) tmux pane referenced via `TMUX_PANE` env still in `tmux list-panes`, (b) registered claude-code session id alive, (c) explicit lease via daemon API (P2/P3 prerequisite — see §5.1). |

I1 and I2 are **execution invariants** (broker still has work or freshly-finished work). I3 is the **ownership invariant** (someone still cares).

| Signal | Use |
|---|---|
| **I4 (pressure)** | Combined RSS + open FDs + child count above pressure thresholds. Used only to **prioritise** sweep order when multiple brokers fail I1–I3 simultaneously. Never the reason to kill on its own. |

The decision rule (P2): kill if `¬I1 ∧ ¬I2 ∧ ¬I3` **and** idle timeout exceeded.

**Foreign-broker treatment**: a broker not launched by Purdex (no Purdex launch-registry entry, no `CODEX_COMPANION_SESSION_ID` we recognise) cannot satisfy I3.(a) or I3.(b) by definition. For such brokers, P2 defaults to **dry-run / quarantine only** — never automatic kill — until an explicit lease (I3.(c)) or operator decision exists. P1 marks these as `foreign_owner` in `Anomalies`.

## 3. Broker attribution

### 3.1 Process layer — what to scan

`ps -eo pid,ppid,lstart,rss,command` filtered by:

```
command ~ /node .*scripts/app-server-broker\.mjs serve/
```

Each match yields:

| Field | Source | Notes |
|---|---|---|
| `pid` | `ps` | always |
| `ppid` | `ps` | `1` after setsid; non-1 only briefly during spawn |
| `lstart` | `ps -o lstart=` | start time, used for PID-reuse defence |
| `rss` | `ps` | for I4 (informational) |
| `cwd` | argv `--cwd <path>` | **not** in `broker.json`; must parse argv |
| `endpoint` | argv `--endpoint unix:<sock>` | unix socket path |
| `pidFile` | argv `--pid-file <path>` | matches `broker.json.pidFile` |

Child processes of the broker (`node codex app-server`, then `codex` rust binary) are reaped together — they're in the broker's process group and die with `killpg`.

### 3.2 State-directory layer — what to read

Layout (from `kickoff_codex_broker_and_lights_governance.md` §330 verified live):

```
~/.claude/plugins/data/codex-openai-codex/state/<workspace-basename>-<sha256(realpath)[:16]>/
  broker.json    — present iff broker is currently alive (cleaned on graceful exit)
  state.json     — durable index of jobs[]; survives broker death
  jobs/
    <jobId>.json — per-job durable result (the unit of I2)
    <jobId>.log
```

`broker.json` schema (verified):

```json
{
  "endpoint": "unix:/var/folders/.../T/cxc-XXXXXX/broker.sock",
  "pidFile":  "/var/folders/.../T/cxc-XXXXXX/broker.pid",
  "logFile":  "/var/folders/.../T/cxc-XXXXXX/broker.log",
  "sessionDir":"/var/folders/.../T/cxc-XXXXXX",
  "pid":      <int>
}
```

`state.json` schema (relevant subset):

```json
{
  "version": 1,
  "config":  { "stopReviewGate": bool },
  "jobs": [
    {
      "id":          "task-<slug>",
      "status":      "queued"|"running"|"completed"|"failed"|"cancelled",
      "phase":       "<phase>",
      "createdAt":   "<RFC3339>",
      "updatedAt":   "<RFC3339>",
      "completedAt": "<RFC3339>"|null,
      "pid":         <int>|null,         // task-worker pid; null after completion
      "logFile":     "<abs path>",
      "request":     { "cwd": "<path>", ... }
    },
    ...
  ]
}
```

Important: a directory **without** `broker.json` but **with** `state.json` and `jobs/` is **historical** — its broker has exited cleanly. These directories are out of scope for governance (no live process to reap; no socket to clean). Disk-space cleanup is a separate concern (future enhancement).

### 3.3 Socket layer — what to verify

Each live broker owns `/var/folders/.../T/cxc-XXXXXX/` containing `broker.sock`, `broker.pid`, `broker.log`. The directory uses `mktemp` semantics; correlation back to a state directory is via `broker.json.sessionDir` ⇄ argv `--endpoint`.

Socket-only orphans (`cxc-XXXXXX/` with no live pid in `broker.pid`) are **stale sockets**. P2 cleanup removes them after verifying no live pid holds the inode.

### 3.4 Cross-layer correlation key vs runtime identity

**Correlation key** (cross-layer, cross-restart). The algorithm is **byte-identical to codex CLI's own** `scripts/lib/state.mjs::resolveStateDir`:

```
brokerKey = sha256(realpath(--cwd) || rawCwd)[:16]
```

where `realpath || rawCwd` means: try to evaluate symlinks; on failure, hash the raw value verbatim. **No NFC normalisation, no case-fold** — codex does neither, and because the codex-written state-dir suffix is the only authoritative key on disk, our hash MUST produce the same bytes. Touching the bytes between `realpath` and `sha256` would make Purdex's keys diverge from codex's directory names, breaking three-layer reconciliation. (This was caught during P1 integration testing on a real APFS host: case-fold lowered `/Users/...` to `/users/...` and produced 0/42 triple-source matches; removing case-fold restored 33/66.)

**Runtime identity** (the unit of `BrokerRecord` in P1, the unit of decision in P2):

```
brokerInstanceID = (brokerKey, pid, lstart)
```

`(pid, lstart)` defends against PID-reuse within a daemon run. `brokerKey` correlates across daemon restarts.

**Collision handling**: 16 hex chars yields ~10⁻¹⁹ collision probability over realistic worktree counts, but the failure mode (silent merge of two distinct workspaces) is unrecoverable. Reconcile must verify that all records sharing a `brokerKey` resolve to the same canonical (`realpath(cwd)`, state-dir basename); mismatches produce a `broker_key_collision` anomaly and the records remain distinct (multiple `BrokerRecord` entries with overlapping `brokerKey`).

**Edge cases**:

- `realpath(--cwd)` fails (cwd no longer exists, EACCES, ESTALE): record is emitted with raw cwd, `brokerKey = sha256(rawCwd)[:16]` (matches codex's own fallback), and a `cwd_unresolvable` anomaly. State-dir correlation by suffix may then mismatch — that becomes a `state_dir_no_match` anomaly. Never silently skip the record.
- macOS `/var → /private/var` symlink: covered by `realpath`/`EvalSymlinks` before hashing — codex uses `realpathSync.native`, which behaves the same.
- macOS APFS case-preserving + case-insensitive volumes: **not** folded into the primary key; codex hashes the case-preserved bytes. P2 may add a separate collision-detection pass that flags `/Foo` vs `/foo` divergence as `broker_key_collision`, but the primary hash remains byte-faithful.
- Unicode normalisation drift (NFC vs NFD on filenames): same as case — codex hashes raw bytes, so distinct normalisation forms intentionally produce distinct keys. Cross-form divergence is a P2 anomaly check, not a P1 hash input.
- Argv truncation (rare but possible if cwd contains shell metachars or non-UTF-8 bytes): record is emitted with truncated cwd + `argv_truncated` anomaly.

## 4. Phase P1 — Inventory (this PR)

**Goal**: Daemon can enumerate all codex brokers visible on the host with full attribution. **Read-only** — no kills, no graceful shutdowns, no socket cleanup.

### 4.1 Data model

New package `internal/codexbroker`. Core types:

```go
package codexbroker

// BrokerRecord is one runtime instance. Multiple records can share Key.
type BrokerRecord struct {
    // Identity
    Key         string    // brokerKey = sha256(realpath(cwd))[:16]; correlation only, not unique
    PID         int       // 0 if no live process matched (state-dir-only or socket-only record)
    Lstart      time.Time // zero if PID==0; required when PID>0 for PID-reuse defence

    // Process layer (zero values if no process matched)
    PPID        int
    RSSBytes    int64
    Cwd         string    // raw argv value
    CwdResolved string    // EvalSymlinks(Cwd); empty if unresolvable
    Endpoint    string    // unix:<sock>
    SocketDir   string    // dirname(sock)
    PidFile     string

    // State layer (zero values if no matching state dir)
    StateDir    string
    HasBrokerJSON bool
    BrokerJSONPID int     // pid claimed by broker.json; 0 if file absent/unparseable
    StateJSONReadable bool

    // Diagnostic-only fields (raw inventory, NOT decision input in P1).
    // P2 may consume these but must add staleness/freshness checks of its own.
    JobCounts        JobCounts  // queued/running/completed/failed/cancelled
    LastJobUpdatedAt *time.Time // max(state.json.jobs[].updatedAt); nil if no jobs or read failed

    // Ownership signal (raw)
    CwdExists   bool      // os.Stat(CwdResolved or Cwd) succeeds with ENOENT/transient distinguished
    CwdStatErr  string    // "" if CwdExists; otherwise classified: "ENOENT"|"ESTALE"|"EACCES"|"EIO"|"other"

    // Discovery diagnostics
    Sources     SourceMask // bitmask: process / state-dir / socket
    Anomalies   []Anomaly  // structured, not free-form
}

type Anomaly struct {
    Code   string // see §4.2 for the closed list
    Detail string // operator-readable
}

type JobCounts struct {
    Queued    int
    Running   int
    Completed int
    Failed    int
    Cancelled int
    Unknown   int // status not in known set; defensive against schema drift
}

type SourceMask uint8
const (
    SourceProcess  SourceMask = 1 << iota  // seen in ps
    SourceStateDir                         // state-dir with broker.json
    SourceSocket                           // cxc-* dir with live socket
)
```

**Diagnostic-only fields**: `JobCounts`, `LastJobUpdatedAt`, `CwdExists`, `BrokerJSONPID` are exposed for operator visibility and to let P2 build its decision logic on a stable foundation. **P1 does not use them as decision input** — `GET /api/codex/brokers` returns them as raw observations. P2 will add staleness/freshness/conflict checks before consuming them.

When `state.json` cannot be read (permission, malformed, transient I/O), `JobCounts` stays at zero and `LastJobUpdatedAt` stays nil; the record is still emitted with `state_json_unreadable` anomaly. Job-rollup failure must never cause the broker record to disappear from the inventory.

### 4.2 Discovery

Three independent scans, then **reconcile by `(brokerKey, pid, lstart)` runtime identity**:

1. **Process scan** (`ps -eo pid,ppid,lstart,rss,command` via `processLister` interface for testability): yields candidates with raw cwd argv, endpoint, pidFile, lstart. `brokerKey` derived from `EvalSymlinks(--cwd)` per §3.4.
2. **State-dir scan** (`~/.claude/plugins/data/codex-openai-codex/state/*/broker.json`): yields candidates with pidFile, endpoint, sessionDir, pid. `brokerKey` is the directory suffix. State dirs without `broker.json` are skipped (historical, out of scope per §1).
3. **Socket scan** (`/var/folders/*/T/cxc-*` and `$TMPDIR/cxc-*`): yields candidates with sockDir + pidFile content. `brokerKey` is recovered by walking back through any state-dir whose `broker.json.sessionDir` matches; orphan sockets without a matching state dir get `brokerKey = "unknown:" + sha256(sockPath)[:16]`.

**Reconcile rules**:

- Two records merge into one `BrokerRecord` iff **all** of: same `brokerKey`, same `pid` (when both have one), same `lstart` (when both have one), and consistent canonical cwd. State-dir-only and socket-only fragments without process correlation become separate records.
- Multiple live processes with the same `brokerKey` produce **multiple** records (one per `(pid, lstart)`); each is tagged `duplicate_runtime` anomaly.
- A process whose `cwd` resolves to a `brokerKey` matching a state dir whose `broker.json.pid` differs from the process pid produces a single record covering both layers, tagged `broker_json_pid_mismatch`.
- A `brokerKey` collision (different canonical cwd → same hash prefix) keeps records distinct and tags both with `broker_key_collision`.

**Anomaly classes** for P1 (closed list — extensions need spec amendment):

| Code | Condition |
|---|---|
| `broker_json_pid_mismatch` | state-dir broker.json.pid ≠ process pid found in ps |
| `state_dir_orphan` | state-dir has broker.json but no live process matches its pid |
| `process_orphan` | process in ps but no matching state dir |
| `socket_orphan` | socket dir exists but pid in broker.pid is dead |
| `cwd_unresolvable` | `EvalSymlinks(--cwd)` failed; raw cwd preserved |
| `cwd_missing` | cwd path classified ENOENT (definitive) |
| `cwd_transient_stat_error` | cwd stat returned ESTALE / EIO / EACCES / other (not ENOENT) |
| `lstart_unparseable` | ps lstart format unexpected (defensive) |
| `argv_truncated` | ps argv could not parse `--cwd` cleanly |
| `state_json_unreadable` | state.json missing, malformed, or unreadable |
| `broker_json_unreadable` | broker.json present but malformed or unreadable |
| `duplicate_runtime` | multiple live processes share `brokerKey` |
| `broker_key_collision` | distinct canonical cwd hash to same `brokerKey` |
| `state_dir_no_match` | process record could not be matched to any state dir |
| `foreign_owner` | broker has no Purdex launch-registry entry / unknown `CODEX_COMPANION_SESSION_ID` (informational in P1; consumed by P2) |

### 4.3 HTTP API

New endpoint, registered in a fresh `internal/codexbroker` module:

```
GET /api/codex/brokers
  → 200 OK (always when ps is usable; partial results are 200 with partial=true)
    {
      "scannedAt":      "<RFC3339>",
      "scanDurationMs": <int>,
      "deadlineMs":     <int>,        // total scan budget actually applied
      "partial":        <bool>,       // true iff any scan source hit timeout / read error
      "brokers":        [BrokerRecord, ...],
      "summary": {
        "total":         <int>,
        "withProcess":   <int>,
        "withStateDir":  <int>,
        "withSocket":    <int>,
        "anomalyCount":  <int>,
        "duplicateRuntimeCount": <int>,
        "scanSourceTimeouts": ["process"|"stateDir"|"socket", ...]
      }
    }
  → 503 only when ps cannot be invoked AND no state-dir / socket data is available
        (i.e. no inventory of any kind can be produced)
```

**Timeout / partial-result discipline**:

| Scope | Default | Behaviour on exceed |
|---|---|---|
| Total scan deadline | 800 ms | Sources still in flight return what they have; record `partial=true`; missing sources tagged in `scanSourceTimeouts` |
| Per-process scan (ps invocation) | 500 ms | If ps fails outright → 503 (no other source can fill the gap on its own); if ps returns partial output → 200 partial |
| Per state-dir read | 100 ms | Dir skipped + per-record `state_json_unreadable` or `broker_json_unreadable` anomaly |
| Per socket-dir stat | 50 ms | Skipped + `socket_orphan` anomaly when applicable |
| Per cwd `os.Stat` | 50 ms | `CwdExists=false`, `CwdStatErr="timeout"`, anomaly `cwd_transient_stat_error` |

The endpoint is **synchronous** in P1; with 60 dirs / 123 procs on mlab the warm scan completes well under deadline. Caching, WS push, and inotify/kqueue invalidation are deferred to P3.

Auth: requires `X-Pdx-Token` (same as other dev endpoints). No browser CORS exposure. **Path redaction**: cwd values may contain user paths but are already exposed via `ps`; no extra redaction in this endpoint. Audit/log output (P2) will redact.

### 4.4 Acceptance criteria

| # | Criterion |
|---|---|
| AC1 | On mlab live state, endpoint returns ≥ 50 brokers with `Sources` populated correctly per record. |
| AC2 | A broker that exists in process layer + state dir + socket reports `Sources = process \| state-dir \| socket` (all three bits set). |
| AC3 | A historical state dir (no broker.json) does **not** appear in results. |
| AC4 | A broker whose state.json has `running` job rolls up into `JobCounts.Running >= 1`. (Diagnostic field; not used for any decision in P1.) |
| AC5 | `LastJobUpdatedAt` reflects max(updatedAt) across `state.json.jobs[]`; nil iff no jobs or state.json unreadable. (Diagnostic field; not used for any decision in P1.) |
| AC6 | Anomalies present in current mlab state are surfaced (e.g. `state_dir_orphan` for the 60-active-dirs vs 50-live-process delta). |
| AC7 | **Read-only purity**: scanner makes no syscalls that mutate state. Verified by three checks: (a) no `connect()` to broker sockets (test uses fake socket; counts connects); (b) no writes/unlinks/chmods/touches to any path under `~/.claude/plugins/data/codex-openai-codex/state/` or `/var/folders/*/T/cxc-*/` — fixture FS records all fs syscalls and asserts an exclusively-read access pattern; (c) `processLister` interface never invokes signal-sending syscalls — assert via fake. The `ps process count unchanged after two scans` check is supplementary, not the primary AC7 evidence. |
| AC8 | Per-source partial failure does not collapse the whole scan. Examples: one state dir unreadable → others reported, bad dir surfaces `state_json_unreadable` anomaly, response is `200 partial=true`. Per-cwd ENOENT → record still emitted with `cwd_missing` anomaly. ESTALE / EIO / EACCES → `cwd_transient_stat_error` anomaly. |
| AC9 | Endpoint returns within total scan deadline (default 800 ms) at p95 on mlab live state. Returning at deadline with `partial=true` is acceptable; throwing 503 is only acceptable when ps cannot be invoked at all. |
| AC10 | Multiple live broker processes with the same `brokerKey` produce **multiple** records (not merged), each tagged `duplicate_runtime`. |
| AC11 | When `EvalSymlinks(--cwd)` fails, record is emitted with raw cwd, `CwdResolved` empty, `cwd_unresolvable` anomaly. |
| AC12 | All anomaly codes emitted at runtime appear in the closed list of §4.2; new codes require spec amendment (compile-time enum check in Go). |

### 4.5 Out of scope (deferred to P2/P3)

- Decision logic (idle timeout, kill triggers)
- Any process signalling
- Socket cleanup
- Audit dump
- WS broadcast
- SPA UI
- Historical state-dir cleanup (disk-space concern; separate spec)

### 4.6 Test plan (P1)

Unit tests using fixture filesystems:

- `discovery_state_dir_test.go` — fixtures under `internal/codexbroker/testdata/state/` covering: live broker, historical (no broker.json), unreadable dir, malformed broker.json, jobs[] with each status.
- `discovery_process_test.go` — table-driven over canned `ps` output (we wrap `ps` behind a `processLister` interface for testability; no real `ps` calls in unit tests).
- `discovery_socket_test.go` — fixture cxc-* dirs with live/dead pid in broker.pid.
- `reconcile_test.go` — three-source reconcile matrix; covers every anomaly class in §4.2 (closed list); includes `duplicate_runtime` (AC10), `cwd_unresolvable` (AC11), `broker_key_collision`.
- `read_only_audit_test.go` — uses a fake processLister that records every method call (asserts no signal-sending), and a recording fs (asserts only Open/Read/Stat/Lstat/EvalSymlinks; no Write/Remove/Chmod/Chtimes/Connect). Validates AC7.
- `timeout_partial_test.go` — fake fs with injected delay per source; asserts `partial=true`, `scanSourceTimeouts` populated, deadline respected. Validates AC8/AC9.
- `anomaly_closed_set_test.go` — compile-time enum check + reflection-based test that every anomaly emitted in any other test maps to a code in the §4.2 list. Validates AC12.
- `handler_test.go` — endpoint integration; verifies AC1–AC12 against a fully fixtured environment.

Integration test (`internal/codexbroker/inventory_live_test.go`, build-tag `integration`):
- Spawns a real `app-server-broker.mjs serve` against a temp cwd
- Verifies inventory reports `Sources = process | state-dir | socket` (AC2)
- Captures `ps` snapshot before/after; asserts identical broker pid set (supplementary AC7 evidence; the structural assertion is in `read_only_audit_test.go`)
- Cleans up via signalling the broker (out-of-band; not via this package).

Live verification on mlab: documented in PR test plan, manual.

---

## 5. Phase P2 — Decision + Kill (subsequent PR)

This section locks the design so P1 doesn't accidentally close any doors.

### 5.1 Decision evaluation

For each `BrokerRecord` in the inventory, compute three predicates:

| Predicate | True (broker is "still in use") iff |
|---|---|
| **A. Active execution** | RPC to `endpoint` (e.g. `thread/list`) returns ≥ 1 thread with `status=active`, **OR** broker reports a pending approval awaiting user input, **OR** `state.json.jobs[]` contains a job with `status ∈ {queued, running}` that passes staleness check (§5.2). |
| **B. Recent delivery readable** | `state.json.jobs[]` contains a job whose `completedAt` is within `recentResultWindow` (default 30 min) **AND** the corresponding `jobs/<id>.json` exists and is parseable. |
| **C. Live ownership** | `cwd` path exists with definitive `os.Stat` success (transient failures fall through, see §5.3 E1) **AND** at least one of: (a) tmux pane mapped to this `brokerKey` via Purdex launch registry (P2 prerequisite, see below), (b) registered Purdex caller session alive, (c) explicit lease via daemon API. |

**Conflict rule (positive liveness wins)**: predicates are evaluated independently; any `True` keeps the broker alive. RPC and state.json can disagree (state.json lags; RPC says no thread but state.json shows running). Both signals are evaluated; only §5.2 stale-running can downgrade a `running` state.json entry. RPC unreachable while state.json shows fresh running → A is True (do not penalise broker for momentary RPC stall).

**Launch registry as P2 prerequisite for I3.(a) / I3.(b)**: Purdex must persist a `(brokerKey, pid, lstart) → (tmuxPane, callerSessionID, launchedAt)` mapping at broker spawn time and remove it at confirmed broker death. Without this registry, C cannot map brokers to panes/sessions reliably (state.json `request.cwd` is not unique per pane). The registry schema and persistence point are **P2 implementation work** but are surfaced here so P1 inventory does not foreclose the design.

**Kill rule**: kill iff `¬A ∧ ¬B ∧ ¬C` **and** broker has been idle (no job dispatched, no thread activity) for at least `idleTimeout` (default 30 min, daemon-config tunable). **Foreign-broker treatment** (per §2): if no Purdex launch-registry entry exists for this `brokerKey`, the broker is `quarantine`-only — never automatically killed by tick or boot reconcile. Operator may still issue a manual sweep with `mode=apply&brokerKey=<...>` to override.

### 5.2 Stale-running detection

A `state.json` job stuck at `status=running` is **not** evidence of execution if:

```
job.status == "running"
  AND now - job.updatedAt > staleRunningThreshold  (default 1 h)
  AND (job.pid == nil OR job.pid is dead OR job.pid is reused by an unrelated process)
  → mark job as terminal-abandoned for governance purposes
  → does not satisfy predicate A
```

**Schema caveat**: `state.json.jobs[].pid` is documented as `int|null` (verified live: completed jobs have `pid: null`). Whether running jobs *always* populate pid is **not** confirmed by current evidence; one observed sample shows running jobs with non-null pid, but the schema is permissive. P2 must therefore:

- decode pid as `*int`, never assume non-nil;
- when pid is nil and `updatedAt` is older than `staleRunningThreshold`, treat as terminal-abandoned (most permissive interpretation that defends correctness);
- when pid is non-nil and process is alive, additionally verify cmdline / start-time match an expected task-worker shape, to defend against PID reuse. A mismatch is treated the same as dead.

Detection runs in P2; raw `JobCounts.Running` is in P1 (`BrokerRecord.JobCounts.Running`) as diagnostic-only. A separate `StaleRunning` count is added by P2 after applying the staleness rule above.

### 5.3 Emergency override

Two conditions, with strict definitions to avoid false positives:

| # | Override | Trigger condition | Action |
|---|---|---|---|
| **E1** | Workspace gone | `os.Stat(cwd)` returns **definitive `ENOENT`**, observed on **two consecutive scans** ≥ 60 s apart, AND boot reconcile (if since occurred) also reports gone. ESTALE / EIO / EACCES / EPERM / generic timeout do **not** trigger E1 — they tag the broker `cwd_transient_stat_error` and fall through to normal predicate evaluation. | Kill, with audit dump tagged `workspace_gone`. If state.json still has `running` jobs, additionally tagged `workspace_gone_running` (operator-investigable). |
| **E2** | PID-reuse suspicion | `(pid, lstart, executable, cmdline)` fingerprint mismatches the most-recently-known fingerprint for this `brokerKey`. | **Quarantine** — not killed. Recorded persistently (see below). Operator decides. |

**Quarantine persistence** (closes the boot-reconcile gap):

Quarantine state is **not** in-memory only. Quarantine entries are written to `~/.claude/plugins/data/codex-openai-codex/audit/quarantine.json`:

```json
{
  "version": 1,
  "entries": [
    {
      "brokerKey":  "<16hex>",
      "pid":        <int>,
      "lstart":     "<RFC3339>",
      "quarantinedAt": "<RFC3339>",
      "reason":     "pid_reuse_suspicion",
      "fingerprint": { "executable": "...", "cmdline": "...", "pidFile": "..." },
      "expiresAt":  "<RFC3339>"   // quarantineRetentionDays default 7
    }
  ]
}
```

Boot reconcile reads quarantine.json before any decision pass. Quarantined brokers are excluded from automatic kill until either: (a) operator clears the entry via `POST /api/codex/brokers/quarantine/{brokerKey}/release`, (b) the entry expires, or (c) the live broker's fingerprint matches the quarantined entry's expected predecessor (PID rotated again, mismatch resolved).

Explicitly **not** in emergency-override:

- TMUX_PANE closed: a pane closing only removes one ownership lease; task-worker children may still be active. Falls through to predicate C.
- PPID=1 + age > 24 h: pressure signal (I4), not a correctness invariant. Used only to prioritise sweep order.
- Foreign owner (no Purdex launch registry entry): handled per §2 / §5.1 as quarantine-only, not E1/E2.

### 5.4 Kill sequence

```
Step 0: Re-verify the runtime identity (pid, lstart, cmdline) for this brokerKey.
        If mismatch → abort kill, tag broker for E2 quarantine. Prevents
        killing a respawned broker with reused pid.

Step 1: Capture audit preimage. Write audit/orphan-<brokerKey>-<unixTs>.json
        containing the full BrokerRecord, last 200 lines of broker.log, and
        decision trace (predicates A/B/C, idle seconds, override flags). This
        runs before any signalling so the audit survives even if the daemon
        itself crashes mid-kill.

Step 2: Graceful — connect to endpoint and send shutdown RPC. Budget:
        gracefulShutdownTimeoutSeconds (default 5).
        ↓ failed/timeout/RPC unavailable

Step 3: Resolve target process group: pgid = getpgid(pid). After codex's
        setsid(), pgid is normally equal to pid, but never assume — always
        getpgid first. Refuse to signal if pgid <= 1 (defensive against
        kernel/argv weirdness). Then syscall.Kill(-pgid, SIGTERM). Budget:
        termTimeoutSeconds (default 5).
        ↓ failed/timeout

Step 4: syscall.Kill(-pgid, SIGKILL). Budget: 2s for kernel-level reaping.

Step 5: Verify all pids in the original (pid, lstart) family are gone via
        further ps scans + waitpid(-1, WNOHANG) where parent is daemon
        (not in our case). Best-effort.

Step 6: Cleanup cxc-* dir only after socket-inode verify (see below).
        Update audit dump with kill-result postscript.
```

**Audit ordering rationale**: writing the audit *before* SIGKILL (Step 1, before any signalling) ensures the dump survives daemon crashes during the kill sequence. The post-kill postscript (Step 6) is best-effort — the preimage is the durable record. This corrects the earlier draft which had audit at the end.

**`syscall.Kill(-pgid, sig)` portability**: POSIX-defined behaviour on both macOS and Linux when `pgid > 0`. Go's `syscall.Kill` maps directly to the POSIX `kill(2)` syscall. Refusing `pgid <= 1` defends against init / kernel idle / parser corruption.

**Socket-inode verification** (Step 6 prerequisite): macOS does not expose `/proc/*/fd`. Two strategies:

1. **Preferred**: use Darwin's `libproc` (`proc_pidinfo` + `PROC_PIDLISTFDS`) to enumerate open fds for any live pid and check whether any references the socket inode. On Linux, walk `/proc/*/fd/*` symlinks. Both are no-fork.
2. **Fallback**: spawn `lsof -nP -- <sockPath>` with a 1 s timeout. Acceptable for daemons under low memory pressure; spec acknowledges this is a bounded fallback when libproc binding is unavailable.

If neither verification succeeds within budget, cleanup is **deferred** — the cxc-* dir remains; next sweep retries. This prevents removing a socket still in use by a stray live pid.

The graceful shutdown intentionally targets the broker's own RPC, not the underlying app-server. Broker's shutdown handler reaps its children correctly when it works; `killpg` is the fallback for when it doesn't.

### 5.5 Audit dump

`~/.claude/plugins/data/codex-openai-codex/audit/orphan-<brokerKey>-<unixTs>.json`:

```json
{
  "killedAt":      "<RFC3339>",
  "reason":        "idle_timeout"|"workspace_gone"|"pid_reuse_quarantine"|"manual_sweep",
  "broker":        <full BrokerRecord at decision time>,
  "decision": {
    "predicateA":  bool,
    "predicateB":  bool,
    "predicateC":  bool,
    "idleSeconds": int,
    "overrideE1":  bool,
    "overrideE2":  bool
  },
  "killSequence": {
    "gracefulOk":  bool,
    "termOk":      bool,
    "killOk":      bool,
    "stepLatencyMs": [int, int, int]
  },
  "brokerLogTail": ["<line1>", ...]
}
```

Audit dumps are retained for `auditRetentionDays` (default 14, daemon-config) before pruning.

## 6. Phase P3 — Trigger (subsequent PR)

### 6.1 Trigger strategies

| Trigger | Action | Scope |
|---|---|---|
| Daemon boot | Full reconcile against fresh ps + state-dir + socket scan; rebuild any internal cache from scratch (do not trust persisted snapshot). Read `audit/quarantine.json` to restore quarantine state. | All brokers. |
| 30 s tick | Light scan + decision pass; kill those past idle timeout that fail A∧B∧C. **Singleflight gate**: only one tick scan in flight at a time; manual sweep API uses the same gate to prevent fork amplification. **Jitter**: tick interval is `30s ± 5s` to avoid synchronised herds across multiple daemons. **Deadline**: total tick budget is 5 s; any source that exceeds is partial-recorded and retried next tick. | All brokers. |
| State-dir / socket-dir watcher | `kqueue` (macOS) / `inotify` (Linux) on the codex state root and `cxc-*` glob roots, used **only as cache invalidation** for the inventory cache (see below). Does not replace ps reconciliation — process death does not always emit a fs event, so periodic ps reconciliation remains the source of truth. | All brokers. |
| ExitWorktree hook | Targeted reconcile + immediate decision for the brokerKey corresponding to the exited worktree's realpath. | One brokerKey. |
| Manual API | `POST /api/codex/brokers/sweep?mode=dry-run\|apply&brokerKey=<...>` — defaults to dry-run; `mode=apply` actually kills. Subject to singleflight gate. | All or filtered. |

**Inventory cache** (P3 augmentation of P1): tick scans populate an in-memory cache keyed by `(brokerKey, pid, lstart)` with `cachedAt` timestamp. `GET /api/codex/brokers` may serve from cache when `now - cachedAt < cacheTTL` (default 10 s) to avoid scan amplification under repeated polling. Cache is invalidated by fs watcher events. Cache misses fall through to live scan with the same singleflight gate.

### 6.2 Idle-timeout configuration

Daemon config keys (all override-able via `~/.purdex/config.toml` or env):

| Key | Default | Meaning |
|---|---|---|
| `codex.broker.idleTimeoutSeconds` | 1800 | No dispatch + no thread activity for this long → idle expired. Reset on dispatch. |
| `codex.broker.staleRunningThresholdSeconds` | 3600 | `running` job not updated for this long with dead pid → terminal-abandoned. |
| `codex.broker.recentResultWindowSeconds` | 1800 | Predicate B's lookback window. |
| `codex.broker.gracefulShutdownTimeoutSeconds` | 5 | Step 1 budget. |
| `codex.broker.termTimeoutSeconds` | 5 | Step 2 budget. |
| `codex.broker.auditRetentionDays` | 14 | Audit dump retention. |
| `codex.broker.scanIntervalSeconds` | 30 | Tick cadence. |

### 6.3 Observability

| Surface | What |
|---|---|
| `GET /api/codex/brokers` | List inventory + decision predicates evaluated (P3 augments P1). |
| `POST /api/codex/brokers/sweep?mode=...` | Manual trigger; dry-run is default. |
| WS topic `codex.broker.changed` | Broadcast on add/remove/state-change. |
| Daemon metrics | `codex_brokers_total`, `codex_brokers_orphan`, `codex_broker_kills_total{reason}`, `codex_broker_decision_latency_ms` (histogram), `codex_broker_kill_failures_total{step}`, `codex_broker_audit_writes_total`. |

SPA UI (P4 — separate PR) consumes these.

## 7. Out of scope

- **P4 SPA dashboard** — Settings → Development → Codex Brokers. Separate PR after P3.
- **Lights L1/L2/L3/L4** — separate PRs (kickoff §82-89).
- **Historical state-dir disk cleanup** — directories without `broker.json`. Separate spec; lower priority.
- **Upstream RFC to OpenAI codex** — kickoff §364: deferred ~1 month after governance ships.

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| `ps` argv parsing brittle to codex CLI version changes | Pin parser to current 1.0.2; integration test in CI verifies argv shape; spec mandates argv parse failure → `argv_truncated` anomaly, not silent skip. |
| State-dir scan races against codex writing `broker.json` | Read with retry-on-EOF (≤3 attempts, 50 ms backoff) + treat truncated JSON as `broker_json_unreadable` anomaly. |
| PID-reuse on long-running daemon | E2 quarantine + persisted `audit/quarantine.json` + `(pid, lstart)` runtime identity in `BrokerRecord` + reconcile on boot rebuilds from scratch. |
| Killing a broker that's mid-RPC with a live caller (data loss) | Predicate A covers active threads + pending approval; predicate B covers fresh results; graceful shutdown gives broker chance to drain; result durability guaranteed by `jobs/<id>.json` (read by `/codex:result`, broker-independent). |
| False-positive kill on legitimately-idle broker | Idle timeout + emergency-override must both fail; foreign-broker treatment defaults to quarantine; user can surface via dry-run sweep before applying. |
| Daemon crash during kill sequence | Audit preimage written before any signalling (§5.4 Step 1, before SIGTERM); reconcile-on-boot picks up partial cleanup; quarantine.json restored on boot. |
| Kill loop (broker respawned by upstream during sweep) | §5.4 Step 0 re-verifies (pid, lstart, cmdline) before signalling; respawn during sweep produces a new `BrokerRecord` and a `duplicate_runtime` anomaly, not double-kill. |
| Symlink / realpath / Unicode / case-fold edge cases | All cwd comparisons go through `EvalSymlinks` + canonical-case + NFC normalisation per §3.4; tests cover macOS `/var → /private/var` and APFS case-insensitive volumes. |
| **Foreign / non-Purdex broker treated as orphan** | §2 + §5.1: no Purdex launch-registry entry → quarantine-only; never auto-killed by tick or boot reconcile. |
| **Duplicate runtime per `brokerKey`** | §3.4 + §4.2 reconcile yields multiple `BrokerRecord` entries (not merged); each tagged `duplicate_runtime`; P2 evaluates each independently. |
| **Argv truncation / non-UTF-8 / shell metacharacters in cwd** | argv parse failure → `argv_truncated` anomaly + raw cwd preserved; never silent skip. |
| **Network-mounted cwd (NFS / sshfs) transient stat failure** | §5.3 E1 only triggers on confirmed `ENOENT` observed twice ≥ 60 s apart; ESTALE / EIO / EACCES / timeout → `cwd_transient_stat_error` anomaly + fall through to A/B. |
| **Process group not equal to pid** | §5.4 Step 3 always calls `getpgid(pid)` first; refuses signal if `pgid <= 1`. |
| **Daemon crash + restart while quarantine / lease state in flight** | All destructive action gates on persisted state: `audit/quarantine.json` + launch-registry persisted at spawn. Boot reconcile reads both before any decision pass. Brokers without persisted ownership evidence default to quarantine, not kill. |
| **Scan amplification under polling / many tick callers** | §6.1 singleflight gate + jitter + cache TTL. `GET /api/codex/brokers` serves from cache within TTL; cache bypass requires explicit `?fresh=1`. |
| **`state.json` schema drift / oversized file** | §4.1 `JobCounts.Unknown` defends against new statuses; per-state-dir read budget 100 ms with truncation → anomaly; future enhancement: bounded `jobs[]` length with overflow flag. |
| **Token / log redaction for paths** | `/api/codex/brokers` is auth-gated by `X-Pdx-Token`; cwd values are exposed (already public via ps); audit logs (P2) redact based on a path-allowlist. |

## 9. PR breakdown

| PR | Phase | Scope | LOC est. | Reviews |
|---|---|---|---|---|
| **PR-A** | P1 | This worktree. `internal/codexbroker/` package: process/state/socket scanners + reconcile + `BrokerRecord` + `GET /api/codex/brokers`. **Read-only**, no kills. | 400-700 | 1× standard codex |
| PR-B | L2 | Lights — proxy detach on Stop. **Strictly independent of governance**: only mutates frame/subagent projection state in `internal/module/agent/frame_ops.go`; does **not** terminate codex broker, does **not** consume `/api/codex/brokers`, does **not** depend on `internal/codexbroker`. If product semantics later require Stop to also stop the broker, that becomes a P2/P3 concern, not PR-B. | 50-150 | 1× standard codex |
| PR-C | P2 | Decision predicates + emergency overrides + kill sequence + audit dump. **No automatic triggers yet** (only manual via dry-run/apply API). | 700-1000 | 2× codex (standard + 3-parallel adversarial) |
| PR-D | P3 | Triggers (boot reconcile, tick, ExitWorktree hook, sweep API), config keys, metrics, WS broadcast. | 300-500 | 1× standard + 1× adversarial |
| PR-E | L1 | OpenCode subagent idle filter. Independent. | 50-150 | 1× standard |
| PR-F | P4 | SPA dashboard. | 500-900 | 1× standard |
| PR-G | L3 | Codex spawn/close hook. Independent. | 200-300 | 2× codex |
| PR-H | L4 | OpenCode SOT migration. Independent. | 100-150 | 1× standard |

Each PR followed by its own `chore: bump version` PR per project convention.

## 10. Verification & live testing

For PR-A specifically, before merge:

1. mlab `pdx` daemon running PR-A build, `curl -H "X-Pdx-Token: ..." http://100.64.0.2:7860/api/codex/brokers | jq '.summary'`
2. Verify `summary.total` matches `ls ~/.claude/plugins/data/codex-openai-codex/state/ | wc -l` minus historical dirs (`find ... -name 'broker.json' | wc -l`).
3. Spot-check three brokers: process layer cwd matches state dir suffix sha256 prefix, anomalies populated where expected.
4. Run endpoint twice in succession; verify `ps -ef | grep app-server-broker | wc -l` is unchanged (AC7 — no kills).
5. Document baseline in PR description for future P2 verification.
