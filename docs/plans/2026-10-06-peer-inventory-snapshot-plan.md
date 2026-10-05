# Plan — peer inventory: one process snapshot per pass

Spec: `docs/specs/2026-10-06-peer-inventory-snapshot-spec.md` (owner: coordinator `mlab/_0le0d2`). Base: `6cbc29f7` (origin/main, alpha.486), worktree `peer-inventory-snapshot`.

## 0. Measurements behind this plan (mlab, 2026-10-06)

| What | Cost |
|---|---|
| one `ps -p … -o …` fork | 2.1 ms average (20 runs, idle) |
| one tmux round trip (`display-message` or `list-panes -a`) | 4.2 ms (10 runs each) |
| `sysctl kern.proc.all` (742 processes) | 0.6 ms |
| `kern.procargs2` for all 742 PIDs | 5.2 ms (546 readable, 196 not) |
| live inventory now (`pdx peers --json`, 3 runs) | 2021 / 2021 / 2019 ms, `partial:true` |

Shape of the inventory on mlab: 21 tmux sessions, 21 panes, 18 registry files.

What the spec's cost list leaves implicit, and the plan fixes too:

- **`panesOfSession` dominates.** It runs once per session, and each run asks tmux for the session of every pane that has a frame (`provenance_handler.go:269-298`). That is sessions × framed panes round trips: about 21 × 18 × 4.2 ms ≈ 1.6 s.
- **The registry read forks too.** `ipeers.DefaultLiveness().Info` is `agent.ReadProcessInfo` (`internal/peers/registry.go:118`), which is four `ps` forks per registry entry: 18 × 4 × 2.1 ms ≈ 150 ms idle. It runs inside the same 2 s budget (`module.go:483` sets the deadline, `:505` reads the registry), so R5's 300 ms is not safe unless it shares the snapshot. See D4.

Projected inventory after the change:
- two pane listings, 8 ms;
- one `sysctl`, about 1 ms;
- about 10 µs per process read;
- sqlite reads;
- the existing session list and two instance probes, about 13 ms.

Total: under 60 ms.

## 1. Decisions (derived; reported to the coordinator with this plan)

- **D1 — darwin uses sysctl, zero forks (R3's second option).**
  - `kern.proc.all` gives `PID`, `PPID` and the start time.
  - `kern.procargs2` gives argv.
  - Measured against `ps` on live processes, PPID, `lstart` and argv0/argv match byte for byte.
  - **`lstart` is formatted in Go** as `time.Unix(p_starttime.tv_sec, 0)` in `psLstartLayout`. ps itself prints `strftime("%c")` in the daemon's locale. Measured:
    - `en_US` gives the layout;
    - `zh_TW` gives `二 10月/ 6 05:11:40 2026`;
    - the live daemon runs `LANG=LC_ALL=en_US.UTF-8`.
  - This adds no new assumption. Today's reader already fails in any locale whose `%c` is not `psLstartLayout`: `readProcessStartTime` parses with that layout, so `ReadProcessInfo` errors and every owner walk comes back empty. So wherever owner lookups work today, the Go-formatted string equals `ps -o lstart=`.
  - Why not one `ps -A`: `comm` (argv0) and `args` both contain spaces, so no single `ps` line splits unambiguously. Example: `/System/Library/CoreServices/Software Update.app/…`. `ExePath` needs argv0.
- **D2 — R3 parity holds for every process whose argument area is readable, and that covers every process an owner walk or a registry entry reaches.** Those are the user's own processes: CC, the pane shell, the tmux server.
  - For processes the daemon may not read (other users' processes, zombies), `ps` falls back to `proc_pidpath`. `x/sys/unix` does not expose it, and a raw `SYS_PROC_INFO` call is not worth it.
  - For those processes the snapshot reports `PID`, `PPID` and `StartTime` exactly, with `ExePath ""` and `Argv nil`, and no error.
  - Answers do not change, because the owner walk reads only `PPID`, liveness and the start time. **This departs from the letter of R3 for that class of process. Flagged for the coordinator.**
  - `ps` escapes control bytes in `comm` and `args` as octal (`\t` → `\011`, `\n` → `\012`). Backslash and printable UTF-8 pass through (measured). The snapshot applies the same escaping before it builds `ExePath` and `strings.Fields(args)`.
- **D3 — Linux forks per PID today:** `readProcessPPID` and `readProcessStartTime` are both `ps -p`. So Linux changes too:
  - one `ps -A -o pid=,ppid=,lstart=` per snapshot. `lstart` is the last column, so it parses unambiguously.
  - `ExePath` and `Argv` keep coming from `/proc/<pid>/exe` and `/proc/<pid>/cmdline`, read when a PID is first asked for. These are file reads, not forks.
- **D4 — the registry read in `/api/peers` uses the same snapshot (R2 and R5).**
  - Its `Liveness.PidAlive` and `Liveness.Info` are answered by the pass's snapshot. `Stat` stays `os.Stat`.
  - The other registry reader (`module.go:343`, the send path) is unchanged.
- **D5 — a failed pane listing is a failed lookup, not "no owner" (#988).**
  - Today a re-read that fails drops that one pane. That stays true for a pane that is merely absent from a successful listing.
  - A listing that fails as a whole means nothing could be checked. Every session that listing would have answered reports `Err` and becomes unresolved / `partial`.
- **D6 — what the batched re-check changes.**
  - A session whose panes produced no owners is final at `Resolve`. It needs no re-check, which matches today: it never reached `paneStillInSession`.
  - Sessions with candidates are confirmed together by listing 2. The deadline is read again after that listing. If it has expired, every candidate session is "no answer".
  - Today, a session confirmed before the deadline kept its answer. Under the 2 s budget and a ~50 ms pass, this only differs with a hung tmux, and then listing 1 has already spent the budget.
- **D7 — two PRs. Each phase is at most 800 lines or 20 files.**
  - **A**, primitives with no behaviour change: the snapshot and `ListAllPanes`.
  - **B**, the wiring and the end-to-end tests. B is stacked on A.
  - One bump after B merges.
- **D8 — what changes in existing tests (setup only, no expectation edits).** Listed per task below.
  - One test moves level: `TestResolvePaneOwners_PanePIDUnresolvable_EmptyResultNoError` tests the per-pane `resolvePanePIDFn` call, which no longer exists. Its expectations (no owners, no error, no reads) move unchanged to the pass, where an unparseable or missing pane PID is now handled (B2 test 6).
- **D9 — `Executor.PaneSessionID` has no production caller after B.** It stays: removing it touches every fake. Recorded as a follow-up issue.

## Working rules

- TDD: write the failing test first, run it, and see it fail on the assertion it targets. One commit per task, using `git commit --only <files>`.
- Every subagent Bash command starts with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-inventory-snapshot && `.
- Gates before each PR:
  - `go build ./...`
  - `go vet ./...`
  - `go test ./internal/agent/... ./internal/tmux/... ./internal/module/agent/... ./internal/module/peers/... ./internal/peers/...`, then the full `go test ./...`
  - `GOOS=linux go vet ./internal/agent/... ./internal/tmux/...`
  - `gofmt -l` is empty
- Linux tests also run in an OrbStack container when the image is available: `golang:1.26`, module cache mounted read-only, `go test ./internal/agent/ ./internal/tmux/`.
- House style:
  - comments say *why*, in the density of the surrounding files;
  - no comment narrates the change.
- Mutation testing is a deliverable. Each task lists the mutations to run and reports which test turned red.

---

## Phase A (PR A) — primitives

### A1 — `ProcessSnapshot` and the `ps` seam (darwin)

Files:
- `internal/agent/process_snapshot.go` (new)
- `internal/agent/process_snapshot_darwin.go` (new)
- `internal/agent/process_snapshot_test.go` (new)
- `internal/agent/process_info.go`
- `internal/agent/process_info_darwin.go`

Steps:

1. **`ps` seam.** Add `var runPS = func(ctx context.Context, args ...string) ([]byte, error)`, wrapping `exec.CommandContext(ctx, "ps", args...).Output()`. Route all four existing `ps` invocations through it: `readProcessStartTime`, `readProcessPPID`, and darwin `comm=` / `args=`. Use `context.Background()`, so behaviour is unchanged. Its comment says it exists so a test can count forks.
2. **`ProcessView`** (exported, `process_snapshot.go`): `Alive(pid) bool`, `StartTime(pid) (string, error)`, `Read(pid) (ProcessInfo, error)`.
   - `StartTime` returns the trimmed text `ps -p <pid> -o lstart=` would print. That is what `store.Frame.ProcessStartTime` is compared against.
   - The doc says it is one consistent view of the process table for one pass.
3. **`ProcessSnapshot`** implements it:
   - `SnapshotProcesses(ctx) (*ProcessSnapshot, error)` calls `snapshotProcessesPlatform(ctx)`.
   - It stores, per PID: `ppid`, `lstart string`, `start time.Time`, and `startErr error` (Linux only, D3).
   - `Read` fills `ExePath` and `Argv` on first use through `procArgsPlatform(pid)` and remembers the result, failure included. A `sync.Mutex` guards that lazy fill.
   - `Read` / `StartTime` of a PID not in the snapshot return an error. `Alive` returns `false`.
   - `pid <= 0` → `invalid pid`, as `ReadProcessInfo` does.
4. **darwin.**
   - `unix.SysctlKinfoProcSlice("kern.proc.all")`. Use `P_pid`, `Eproc.Ppid`, and `P_starttime.Sec` → `time.Unix(sec, 0)`. `lstart` is that time formatted in `psLstartLayout` (D1).
   - `procArgsPlatform`: `unix.SysctlRaw("kern.procargs2", pid)` returns `argc` (4 bytes LE), the exec path, NUL padding, then `argc` strings.
     - `comm` = `psVis(argv[0])`.
     - `args` = the `psVis` of each argument, joined by `" "`.
     - Then reuse today's exact steps: `normalizeExecutablePath(comm)`, `strings.Fields(args)`, and the same "empty args/argv" errors.
     - Unreadable (`EINVAL`/`EPERM`) or `argc == 0` → `ExePath ""`, `Argv nil`, `nil` error (D2).
   - `psVis`: bytes `< 0x20` and `0x7f` become `\%03o`, and everything else is verbatim. The comment cites the measured `ps` output (D2).
5. **Tests** (`process_snapshot_test.go`):
   - **Parity, darwin only (skip elsewhere).** Spawn these:
     - `sleep 30`;
     - an `exec.Cmd` with `Path: "/bin/sleep"` and `Args: {"中文 x\ty\nz\\w", "30"}` (the odd-argv0 case).

     For each spawned process, and for `os.Getpid()` and `os.Getppid()`:
     - `snap.Read(pid)` equals `ReadProcessInfo(pid)` field by field (`StartTime` via `Equal`);
     - `snap.StartTime(pid)` equals the trimmed `ps -p <pid> -o lstart=` output;
     - `snap.Alive(pid)` is true.
   - **Missing PID** (`999999` and `-1`): `Read` and `StartTime` error, `Alive` is false.
   - **Fork count, darwin.** With `runPS` counting:
     - `SnapshotProcesses` + `Read(self)` + `StartTime(self)` → **0**;
     - `ReadProcessInfo(self)` → **4** (proves the counter sees today's forks).
   - **Unreadable process (darwin):** `snap.Read(1)` returns `PPID 0`, a non-zero `StartTime`, `ExePath ""` and no error. This pins D2.
6. Mutations:
   - drop `psVis` → the odd-argv0 parity case turns red;
   - read `lstart` from `P_starttime` with a different layout → parity turns red;
   - make `Read` call `ReadProcessInfo` → fork count turns red.

### A2 — Linux snapshot

Files:
- `internal/agent/process_snapshot_linux.go` (new)
- `internal/agent/process_snapshot_linux_test.go` (new, Linux-only parity)

Steps:

1. `snapshotProcessesPlatform`: `runPS(ctx, "-A", "-o", "pid=,ppid=,lstart=")`. Per line:
   - `pid` and `ppid` are the first two fields;
   - `lstart` is the rest of the line, trimmed;
   - `start, startErr` = `time.ParseInLocation(psLstartLayout, lstart, time.Local)`;
   - unparseable `pid`/`ppid` lines are skipped.
2. `procArgsPlatform`: today's `/proc` code moved over unchanged: `readlink exe` → `normalizeExecutablePath` → `filepath.Clean`, and the `cmdline` split. Errors stay errors (Linux has no D2 class). `Read` returns `startErr` when it is set, which matches today.
3. Tests (Linux only): the same parity and missing-PID cases as A1, and a fork count of **1** per snapshot. Run them in the OrbStack container when available. Otherwise `GOOS=linux go vet` plus `go test -c` compile, and the PR states which was done.

### A3 — `ListAllPanes`

Files:
- `internal/tmux/executor.go`
- `internal/tmux/fake_executor.go`
- `internal/tmux/executor_test.go` (or a new `list_panes_test.go`)

Steps:

1. `type PaneLocation struct{ PaneID, SessionID, PanePID string }`. Add `ListAllPanes(ctx context.Context) ([]PaneLocation, error)` to `Executor`. The doc says:
   - it is ONE round trip for every pane of the server;
   - the inventory reads it twice per pass, once to enumerate and once to re-confirm membership (spec R2);
   - it takes ctx for the same reason `PaneSessionID` does.
2. Real: `exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_id} #{session_id} #{pane_pid}")`, parsed by a pure `parsePaneLocations(out []byte)`. Exactly three fields per line, otherwise the line is skipped.
   - The separator is a space, not TAB: tmux without a UTF-8 locale rewrites TAB in `-F` output to `_` (alpha.340). None of the three values can contain a space.
3. Fake:
   - rows come from `paneSessionIDs` (only panes with an id configured);
   - `PanePID` uses the same lookup as `ActivePanePID` (`activePanePIDs`, then `panePIDs`, then `"fake-active-pid"`);
   - sorted by pane id;
   - `ctx.Err()` first, as `PaneSessionID` does;
   - new knob `SetListAllPanesError(err)`.
4. Tests:
   - `parsePaneLocations`: good lines, a short line, a blank line, trailing newline;
   - fake listing mirrors `SetPaneSessionID` / `SetPanePID` / `ForgetPaneSessionID`;
   - an expired ctx errors;
   - the error knob errors.
5. Mutation: split on TAB → the parser test turns red.

PR A: R1 + R2 by codex (`gpt-5.6-sol`). The focus is D1/D2 parity and the `procargs2` parsing bounds.

---

## Phase B (PR B, stacked on A) — the owner pass and the inventory

### B1 — the walk reads through a `ProcessView` (pure refactor, no behaviour change)

Files:
- `internal/module/agent/ancestor.go`
- `internal/module/agent/pane_owner.go`
- `internal/module/agent/provenance_handler.go`
- `internal/module/agent/ancestor_test.go`
- `internal/module/agent/pane_owner_test.go`

1. `liveProcs struct{ read procReader }` implements `agentpkg.ProcessView` through the package seams: `isPidAliveFn`, `processStartTimeFn`, `read`. It is the hook path's view: `classifyAncestor` passes `liveProcs{read: readProcessInfoFn}`, so the hook path reads the same PIDs in the same order as before (the `provenance_test.go:170` premise).
2. `walkPaneAncestry(paneID, startPID, agentType, procs agentpkg.ProcessView, opts)`. The candidate check uses `procs.Alive` / `procs.StartTime`, and the step uses `procs.Read`.
3. `resolvePaneOwners(ctx, paneID string, panePID int, procs agentpkg.ProcessView)`:
   - the pane PID is now an argument; the pass supplies it from the listing in B2;
   - survivors are decided by `procs.Alive` / `procs.StartTime`;
   - `ctxGuardedReader` becomes `ctxGuardedProcs`, which guards `Read` only, the same reads as today.
4. `provenance_handler.go` keeps today's flow for this commit: it resolves the pane PID with `resolvePanePIDFn` and passes `liveProcs{read: newMemoProcReader(readProcessInfoFn)}`.
5. Test edits (setup only, D8):
   - the 12 `walkPaneAncestry` calls in `ancestor_test.go` wrap their reader as `liveProcs{read: …}`;
   - the `resolvePaneOwners` calls in `pane_owner_test.go` pass the fixture's pane PID (the value they gave `withPanePID`) and `liveProcs{read: …}`;
   - `TestResolvePaneOwners_PanePIDUnresolvable_EmptyResultNoError` is deleted here and re-added at the pass level in B2.
6. Gate: the whole `internal/module/agent` suite passes with no assertion edited.

### B2 — the owner pass and the provenance endpoint on it

Files:
- `internal/module/agent/owner_resolver.go`
- `internal/module/agent/owner_pass.go` (new)
- `internal/module/agent/owner_pass_test.go` (new)
- `internal/module/agent/provenance_handler.go`
- `internal/module/agent/provenance_handler_test.go`
- `internal/module/agent/owner_resolver_test.go`
- `internal/module/agent/ancestor_test.go`

1. Public types (`owner_resolver.go`):
   - `OwnerResult{Owner PaneOwner; Found bool; Err error}`;
   - `OwnerPass` with `Resolve(ctx, code)` and `Confirm(ctx) map[string]OwnerResult`;
   - `OwnerPassResolver` = `OwnerResolver` + `NewOwnerPass(procs agentpkg.ProcessView) OwnerPass`, where nil `procs` means the pass takes its own.

   The doc states the contract:
   - one pass is one process view and two listings;
   - `Resolve` answers are provisional until `Confirm`;
   - single goroutine;
   - `Confirm` is called once.
2. `owner_pass.go`. Seam: `var takeProcSnapshotFn = func() (agentpkg.ProcessView, error)`, wrapping `agentpkg.SnapshotProcesses`. Return `nil, err`, never a typed nil.

   **`Resolve(ctx, code)`:**
   1. `frames == nil`, `tmux == nil` or `code == ""` → `found:false`. Expired ctx → `Err`.
   2. **First use:**
      - `frames.ListAll()`;
      - if there are no frames, stop: no listing, no snapshot;
      - otherwise call `tmux.ListAllPanes(ctx)` (listing 1);
      - index the distinct framed panes in `ListAll` order (`pane_id ASC`) by `EncodeSessionID(row.SessionID)`, with the pane PID parsed like `resolvePanePID`.

      A missing row, a bad id or a bad PID excludes that pane, as `panesOfSession` / `resolvePanePID` do today. A failure here is sticky for the pass and every `Resolve` reports it as `Err`.
   3. The process view is taken the first time a pane is walked: either the `procs` the caller passed, or `takeProcSnapshotFn()`. A snapshot failure is sticky `Err`.
   4. For each of the session's panes, call `resolvePaneOwners(ctx, pane, pid, view)`. An error → `Err`. Keep the panes whose owners are non-empty as candidates.
   5. Read `ctx.Err()` after the loop → `Err`. No candidates → final `found:false` (D6). Otherwise the session is pending.

   **`Confirm(ctx)`:**
   1. No pending session → return the results.
   2. Otherwise call `ListAllPanes(ctx)` once (listing 2). A listing error, **or `ctx.Err()` read after it**, gives every pending session `Err` (D5, D6).
   3. Otherwise, per pending session:
      - keep a candidate pane only if the listing still places it in a session that encodes to `code`; absent means dropped, as today;
      - drop owners with an empty `SessionID`;
      - pick with `betterOwner`.

   The reasoning in today's comments moves here with the code: the join-pane window, the deadline read after the re-check, and why a partial walk is not an answer. Drop the "four `ps` forks" rationale where it no longer applies.
3. `resolveSessionOwnerErr` keeps `provenanceTimeout`, then runs `NewOwnerPass(nil)` → `Resolve` → `Confirm` → `results[code]`. Delete `panesOfSession` and `paneStillInSession`. `newMemoProcReader` moves to `ancestor_test.go` (test fixtures).
4. Fixture edits (setup only, D8):
   - `withProcessTree`, `withLivePids`, `withProcessReadError` and `withProcessTreeSequence` also install `takeProcSnapshotFn`. It returns `liveProcs{read: newMemoProcReader(readProcessInfoFn)}`, built at pass time so later wrappers apply: `withSlowReads`, `withRecordedReads`, `withProcessReadHook`. That makes it one memoised fixture view per pass. `TestHandleSessionProvenance_MemoizesReadsAcrossPanes` keeps its expectation and now pins "one view shared by every pane of the pass".
   - The executor wrappers override `ListAllPanes` instead of `PaneSessionID`. In each, call 1 is listing 1 and call 2 is listing 2:
     - `countingExecutor`: counter renamed `listCalls`; the assertion still means "zero tmux reads after the deadline";
     - `recheckExecutor`;
     - `paneTargetedRecheckExecutor`: blocks listing 2 — the batch re-check is one read, so "a cancelled re-check discards the earlier owner" holds as written;
     - `blockingEnumerationExecutor`.
5. New tests (`owner_pass_test.go`):
   1. **Fork count (R2).**
      - Setup: 3 sessions, each with a root;
      - `takeProcSnapshotFn` counts calls;
      - the executor counts `ListAllPanes`.
      - Run a pass over the 3 codes. Assert:
        - all 3 are answered with their own root;
        - snapshots = **1**;
        - listings = **2**;
        - the same with 6 sessions.
   2. **Lazy.** With no frames, a pass over 2 codes gives `found:false, err:nil`, with 0 listings and 0 snapshots.
   3. **Moved pane, batch.**
      - Session A's pane moves to B between listing 1 and listing 2 (`withProcessReadHook` → `SetPaneSessionID`).
      - A gets `found:false` and B gets `found:false` (B never owned it at enumeration).
      - Session C, untouched in the same pass, is still answered.
   4. **Listing 2 fails** (`SetListAllPanesError` after `Resolve`) → the session with candidates gets `Err`. A session with no candidates in the same pass keeps `found:false, err:nil` (D5, D6).
   5. **Deadline between `Resolve` and `Confirm`** (cancel ctx) → candidates get `Err`. Also: `Confirm` with an expired ctx makes no listing whose answer is used.
   6. **Unresolvable pane** (moved from B1). A pane absent from listing 1, or whose PID does not parse, contributes nothing: no error, no process reads.
   7. **Listing 1 fails** → every `Resolve` gets `Err`, and no snapshot is taken.
   8. **Caller's view.** `NewOwnerPass(view)` never calls `takeProcSnapshotFn`, and the walk reads through `view`.
6. Mutations:
   - `Confirm` skips the membership filter → test 3 and `TestHandleSessionProvenance_PaneMovedAfterEnumeration_NotAnAnswer` turn red;
   - snapshot per `Resolve` → test 1 turns red;
   - listing per `Resolve` → test 1 turns red;
   - drop the ctx read after listing 2 → `TestHandleSessionProvenance_RecheckAnswersAfterTheDeadline_NotAnAnswer` turns red;
   - a listing-2 error drops panes instead of failing → test 4 turns red.

### B3 — `/api/peers` runs one pass and shares its snapshot with the registry

Files:
- `internal/module/peers/module.go`
- `internal/module/peers/module_test.go`
- `internal/module/peers/fakes_test.go`

1. New `Module` field `snapshotProcs func(ctx context.Context) (agentpkg.ProcessView, error)`. Its default in `New` wraps `agentpkg.SnapshotProcesses`, with no typed nil.
2. `localEnvelope`, after the session list:
   - `procs, err := m.snapshotProcs(invCtx)`. On error, log it and set `procs = nil`.
   - `live := m.liveness`; if `procs != nil`, use `inventoryLiveness(live, procs)` (D4: `PidAlive` = `procs.Alive`, `Info` = `procs.Read`, `StartTime` derived from `Read`, `Stat` kept).
   - `ReadRegistryDiag(m.registryDir, live)`.
3. `pass := m.ownerPass(procs)`: `NewOwnerPass(procs)` when `m.owners` is an `agent.OwnerPassResolver`. Otherwise `perSessionPass`, which calls `ResolveSessionOwner` inside `Resolve` and returns the stored answers from `Confirm`. This mirrors the `contextSessionLister` fallback.
4. The loop:
   - keeps its `m.now()` deadline check unchanged;
   - calls `pass.Resolve(invCtx, s.Code)`;
   - after the loop, maps `pass.Confirm(invCtx)` through today's rules (`Err` → `unresolved`, `Found` → `owners`).

   The generation re-probe stays after `Confirm`. Add `var _ agent.OwnerPassResolver = (*agent.Module)(nil)` next to the existing assertions.
5. Test setup (D8):
   - `newTestModule` and the options constructor set `snapshotProcs` to a view built from the test's fake `Liveness` (`livenessView`: `Alive` → `PidAlive`; `Read` → `Info`, or a `ProcessInfo` carrying `StartTime(pid)` when `Info` is nil);
   - existing `fakeOwners` / `ctxRecordingOwners` implement only `ResolveSessionOwner`, so they run through `perSessionPass`, and their call-order, clock and context assertions are untouched.
6. New tests:
   1. **One snapshot per inventory.** A counting `snapshotProcs` and a fake pass resolver that records its view. Assert:
      - 1 snapshot;
      - `NewOwnerPass` gets that same view;
      - the registry's liveness answers from it (a registry entry whose PID only the view knows is alive);
      - `Confirm` is called once;
      - every session is resolved through the pass.
   2. **Snapshot failure.** The registry falls back to `m.liveness`, the pass gets nil, and the inventory still answers.
   3. **`Confirm` returns `Err` for one code** → that row is unresolved and the envelope is `partial`. A `Found` code → owner row.
   4. **Budget.** Sessions after the `m.now()` deadline are never handed to `Resolve` (fake pass) — the existing semantics through the new path.
7. Mutations:
   - take the snapshot inside the loop → test 1 turns red;
   - pass `m.liveness` to the registry → test 1 turns red.

### B4 — gates, mutation report, PR B

1. Run the full gates (Working rules), plus the darwin real-process parity test.
2. Record every mutation result in the PR body.
3. PR B: codex R1 + R2 (attack → critic). The focus is D5/D6 semantics, `Confirm` ordering, the peers fallback, and the snapshot shared with the registry.

---

## Acceptance (after merge; the deploy is coordinated by `mlab/_0le0d2`)

1. Report merge and bump to the coordinator and **wait for the restart slot**. Do not restart the mlab daemon on my own.
2. After the restart, run `pdx peers --json` 10 times and record:
   - each run's wall time (target ≤ 300 ms);
   - that no run prints `partial: … not resolved within budget`.
3. Compare each session's owner (agent type, session id, pane) with the pre-deploy baseline captured 2026-10-06 (`scratchpad/baseline/run{1,2,3}.json`). Every session resolved in the baseline must resolve to the same owner. Sessions unresolved in the baseline must now resolve.
4. Check the status bar of `purdex3` (and of one more late-alphabet session): it shows its peer address, not `tmux:purdex3`.
