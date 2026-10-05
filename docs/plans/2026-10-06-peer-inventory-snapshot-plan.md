# Plan — peer inventory: one process snapshot per pass

Spec: `docs/specs/2026-10-06-peer-inventory-snapshot-spec.md` (owner: coordinator `mlab/_0le0d2`). Base: `6cbc29f7` (origin/main, alpha.486), worktree `peer-inventory-snapshot`.

D1–D8 were approved by the coordinator on 2026-10-06 and are written into the spec as「統籌核准的推導」. Two additions came with the approval:
- the start time is comparable to the second;
- the caller audit for empty `ExePath` / `Argv`. That audit refined D2.

**Codex plan review** (job `task-muvrbqpl-ozar9e`, gpt-5.6-sol, spec read with it) returned 11 findings:

| # | Finding | Disposition |
|---|---|---|
| 1 | D2 breaks R3 parity | Already fixed by the D2 refinement |
| 4 | Snapshot-failure retry | Applied: B2 / B3 snapshot provider |
| 5 | Lazy args can mix two processes | Applied: D10 identity re-check |
| 7 | Escaping coverage | Applied: D2 ASCII fast path + `ps` fallback, measured exotic cases |
| 9 | Wrapper timing | Applied: B2 step 4 notes |
| 10 | Production wiring is not counted | Applied: B2 test 9 and B3 test 1 arm the per-PID paths to fail |
| 11 | B1 drops a test | Applied: B1 step 5 |
| 2 | D5 | Kept, coordinator-approved |
| 3 | D6 | Kept, coordinator-approved |
| 8 | D4 timing | Kept (D4 approved); point-in-time semantics written as D10 and tested in B3 |
| 6 | "`ps comm` is not argv0" | **Rejected with evidence.** `exec -a weird-argv0 sleep 30` gives `ps -o comm=` → `weird-argv0`, and five exotic-argv probes give a `comm` equal to argv[0] (§0). The A1 parity test pins it. |

## 0. Measurements behind this plan (mlab, 2026-10-06)

| What | Result |
|---|---|
| one `ps -p … -o …` fork | 2.1 ms average (20 runs, idle) |
| one tmux round trip (`display-message` or `list-panes -a`) | 4.2 ms (10 runs each) |
| `sysctl kern.proc.all` (742 processes) | 0.6 ms |
| `kern.procargs2` for all 742 PIDs | 5.2 ms (546 readable, 196 not: other users' processes) |
| live inventory now (`pdx peers --json`, 3 runs) | 2021 / 2021 / 2019 ms, `partial:true` |
| `ps -o comm=` | is argv[0]: `exec -a weird-argv0 sleep` → `weird-argv0`; leading spaces trimmed |
| `ps` escaping of printable ASCII | verbatim, including space, quotes, backslash; an empty argument gives a double space |
| `ps` escaping of anything else | not a simple rule: TAB → `\011`; DEL → `^?`; byte `0x80` → `M^@`; `0xff` verbatim; U+00A0 verbatim; U+3000 → `\xe3M^@M^@`; U+200B → `\040^K`; CJK and emoji verbatim |
| `ps` on a zombie | `comm` = `args` = `<defunct>` |
| `ps` on another user's process (PID 1) | `/sbin/launchd`, from `proc_pidpath`; `kern.procargs2` gives `EINVAL` |
| `ps -o args=` with a 20 000-byte argument | not truncated |
| empty argv[0] | `ps` and a `procargs2` parser that skips the NUL padding agree (both start at argv[1]) |

Shape of the inventory on mlab: 21 tmux sessions, 21 panes, 18 registry files.

Spec §2 (a) and (b) (coordinator-approved) name the two costs the plan removes beyond the spec's list:
- the per-session `panesOfSession` scan, ≈ 1.6 s;
- the registry's per-entry `ps` forks, ≈ 150 ms.

Projected inventory after the change:
- two pane listings, 8 ms;
- one `sysctl`, about 1 ms;
- about 10 µs per process read;
- sqlite reads;
- the existing session list and two instance probes, about 13 ms.

Total: under 60 ms.

## 1. Decisions

D1–D8 are in the spec (R1 / R2 / R3 / §4 / §5 blocks). This section adds only the implementation detail.

- **D1 — darwin sysctl.**
  - `lstart` text: `time.Unix(p_starttime.tv_sec, 0).Format(psLstartLayout)` in `time.Local`.
  - `ProcessInfo.StartTime` is that same `time.Unix(sec, 0)`: whole seconds, `time.Local`.
  - The parity test asserts the text, `Equal`, `Location() == time.Local`, and `Nanosecond() == 0`.
- **D2 (refined) — the args fast path is printable ASCII only. Everything else falls back to today's `ps`.**
  - The `kern.procargs2` argv is used directly only when **every byte of every argv string is in `0x20–0x7e`**. For that set, `ps` is measured to be verbatim:
    - `comm` = argv[0];
    - `args` = argv joined by `" "`;
    - then today's `normalizeExecutablePath` / `strings.Fields` / errors.
  - Anything else falls back to today's two reads, `ps -p <pid> -o comm=` and `-o args=`, through the existing code path (one shared helper). There is no attempt to re-implement `ps`'s escaping: the measured rules in §0 are not one algorithm.
  - The fallback covers:
    - any other byte;
    - an unreadable `procargs2` (other users' processes, zombies).
  - `PPID` and `StartTime` always come from the snapshot.
  - In practice the walk and the registry reach CC (`node …/claude …`), shells and tmux, whose argv is plain ASCII, so they take zero forks. A CC started with a CJK prompt argument costs 2 forks when it is read. That is still exact, and still far below today's 4 per PID.
- **D3 — Linux:** one `ps -A -o pid=,ppid=,lstart=` per snapshot. `/proc` exe and cmdline on first `Read`, with today's errors.
- **D9 — `Executor.PaneSessionID` has no production caller after B.** It stays: removing it touches every fake. A follow-up issue is filed when B merges.
- **D10 (coordinator-approved, on the D4 condition) — the snapshot is a point-in-time view for the owner lookups. `Read` re-checks identity.** Codex #5 and #8.
  - For the owner walk, `Alive`, `StartTime` and `PPID` describe the process table at the moment of the snapshot. That is what spec R2 asks for.
  - `ExePath` / `Argv` are read later, on first `Read`.
    - **darwin:** `Read` re-reads `kern.proc.pid.<pid>` (sysctl, no fork) **after** the args read. If the PID is gone or its start time differs, `Read` returns an error wrapping the exported `agent.ErrProcessChanged`. The owner walk treats that as an unreadable process (Indeterminate → excluded).
    - **Linux:** keeps today's level, where the per-PID reader also mixes a `ps` PPID with later `/proc` reads (code comment).
  - A PID absent from the snapshot gives an error wrapping the exported `agent.ErrNotInSnapshot`.
  - **The registry never relies on point-in-time liveness.** That is the coordinator's condition: `localEnvelope` also serves send, deliver and reply (`send.go:286`, `deliver.go:257`, `reply.go:109`).
    - `PidAlive` stays `kill(pid, 0)`, a syscall evaluated at the read.
    - `Info` uses the snapshot only when `Read` succeeds. On `ErrNotInSnapshot` or `ErrProcessChanged` it calls today's per-PID `ReadProcessInfo`.
    - So every registry verdict is today's. B3 pins each case.
  - `ReadRegistryDiag` has no side effects (no file removed, only a log-once warning set). Checked 2026-10-06.

## Working rules

- TDD: write the failing test first, run it, and see it fail on the assertion it targets. One commit per task, using `git commit --only <files>`.
- Every subagent Bash command starts with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-inventory-snapshot && `.
- Gates before each PR:
  - `go build ./...`
  - `go vet ./...`
  - `go test ./internal/agent/... ./internal/tmux/... ./internal/module/agent/... ./internal/module/peers/... ./internal/peers/...`, then the full `go test ./...`
  - `GOOS=linux go vet ./internal/agent/... ./internal/tmux/...`
  - `gofmt -l` is empty
- Linux tests also run in an OrbStack container when the image is available: `golang:1.26`, module cache mounted read-only, `go test ./internal/agent/ ./internal/tmux/`. The PR states whether that ran or only the cross-compile did.
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

1. **`ps` seam.** Add `var runPS = func(ctx context.Context, args ...string) ([]byte, error)`, wrapping `exec.CommandContext(ctx, "ps", args...).Output()`. Route every existing `ps` call in the package through it: `readProcessStartTime`, `readProcessPPID`, and darwin `comm=` / `args=`, all with `context.Background()`, so behaviour is unchanged.
   - Factor darwin's `comm` + `args` reads (with `normalizeExecutablePath`, `strings.Fields` and their errors) into `readCommArgsPS(pid) (exePath string, argv []string, err error)`.
   - `readProcessInfoPlatform` and the snapshot fallback both call it.
2. **`ProcessView`** (exported, `process_snapshot.go`): `Alive(pid) bool`, `StartTime(pid) (string, error)`, `Read(pid) (ProcessInfo, error)`.
   - `StartTime` is the trimmed text `ps -p <pid> -o lstart=` prints, which is what `store.Frame.ProcessStartTime` holds.
   - The doc says it is one point-in-time view for one pass (D10).
3. **`ProcessSnapshot`** implements it:
   - `SnapshotProcesses(ctx) (*ProcessSnapshot, error)` calls `snapshotProcessesPlatform(ctx)`.
   - It stores, per PID: `ppid`, `lstart`, `start`, and `startErr` (Linux).
   - `Read` builds `ExePath` / `Argv` on first use through `procArgsPlatform(pid, entry)` and remembers the result, failure included. A `sync.Mutex` guards the lazy fill.
   - `Read` / `StartTime` of a PID not in the snapshot return an error wrapping the exported `ErrNotInSnapshot`. `Alive` returns `false`.
   - A failed identity re-check wraps the exported `ErrProcessChanged` (D10).
   - `pid <= 0` → `invalid pid`.
4. **darwin.**
   - `unix.SysctlKinfoProcSlice("kern.proc.all")`. Use `P_pid`, `Eproc.Ppid`, `P_starttime.Sec` (D1).
   - `procArgsPlatform`:
     1. `unix.SysctlRaw("kern.procargs2", pid)`: `argc` (4 bytes LE), the exec path, then NUL padding.
     2. Skip **all** the padding, exactly as `ps` does (empty argv[0] row in §0), then read `argc` strings.
     3. If the read succeeds and every byte is in `0x20–0x7e`, build `comm` / `args` directly (D2). Otherwise call `readCommArgsPS(pid)`.
     4. Then re-check identity: `unix.SysctlKinfoProc("kern.proc.pid", pid)`. If it is missing, or `P_starttime.Sec` ≠ the snapshot's, return an error (D10).
     5. Bound every slice index by the buffer length, and treat a malformed buffer as unreadable, which means the fallback.
5. **Tests** (`process_snapshot_test.go`, darwin parts skipped elsewhere):
   1. **Parity, plain.** For `os.Getpid()`, `os.Getppid()` and a spawned `sleep 30`:
      - `snap.Read(pid)` equals `ReadProcessInfo(pid)` field by field;
      - `StartTime` via `Equal`, plus `Location() == time.Local` and `Nanosecond() == 0` on both;
      - `snap.StartTime(pid)` equals the trimmed `ps -p <pid> -o lstart=` output;
      - `Alive` is true.
   2. **Parity, exotic argv** (D2). Spawn the test binary in a sleep-only helper mode (the `GO_WANT_…_HELPER` pattern `process_info_test.go` already uses) with `exec.Cmd.Args` set to each measured case:
      - an empty argument, `a b`, quotes;
      - leading spaces in argv[0];
      - empty argv[0];
      - TAB / NL / backslash;
      - DEL;
      - bytes `0x80` / `0xff`;
      - U+0085, U+00A0, U+3000, U+200B, U+202E;
      - CJK, emoji;
      - a 20 000-byte argument.

      Each must equal `ReadProcessInfo` field by field.
   3. **Parity, unreadable** (D2). PID 1, and a zombie: `exec.Command("/bin/sh", "-c", "exit 0").Start()`, not `Wait`ed until cleanup, polled until the snapshot shows it. Both must equal `ReadProcessInfo` field by field (the zombie's `Argv` is `["<defunct>"]`).
   4. **Fork count**, with `runPS` counting:
      - `SnapshotProcesses` + `Read` + `StartTime` of self, parent and the plain `sleep` → **0**;
      - `Read` of a non-ASCII-argv helper → **2**;
      - `Read` of PID 1 → **2**;
      - `ReadProcessInfo(self)` → **4** (proves the counter sees today's forks).
   5. **Identity re-check** (D10). Spawn `sleep 30`, snapshot, kill and reap it, then `Read` → error.
      - A seam variant: `kernProcPid` is a package var, so the test can return a different start time for a live PID, and `Read` must fail.
   6. **Missing PID** (`999999`, `-1`): `Read` and `StartTime` error, `Alive` is false.
   7. **Malformed `procargs2`** (`parseProcArgs` table test): short buffer, no NUL, `argc` larger than the strings present → no panic; unreadable → fallback.
6. Mutations:
   - widen the fast path to all bytes → exotic parity turns red;
   - change the `lstart` layout → plain parity turns red;
   - make `Read` call `ReadProcessInfo` → fork count turns red;
   - drop the identity re-check → test 5 turns red;
   - fall back to empty `ExePath` / `Argv` → unreadable parity turns red.

### A2 — Linux snapshot

Files:
- `internal/agent/process_snapshot_linux.go` (new)
- `internal/agent/process_snapshot_linux_test.go` (new)

1. `snapshotProcessesPlatform`: `runPS(ctx, "-A", "-o", "pid=,ppid=,lstart=")`. Per line:
   - `pid` and `ppid` are the first two fields;
   - `lstart` is the rest of the line, trimmed;
   - `start, startErr` = `time.ParseInLocation(psLstartLayout, lstart, time.Local)`;
   - lines that do not parse are skipped.
2. `procArgsPlatform`: today's `/proc` code, moved over unchanged. `readlink exe` → `normalizeExecutablePath` → `filepath.Clean`, and the `cmdline` split. Errors stay errors. `Read` returns `startErr` when it is set. A comment notes D10's Linux level.
3. Tests (Linux only): plain parity, missing PID, and a fork count of **1** per snapshot. Run in OrbStack when available, otherwise cross-compile only (`GOOS=linux go test -c`). The PR says which.

### A3 — `ListAllPanes`

Files:
- `internal/tmux/executor.go`
- `internal/tmux/fake_executor.go`
- `internal/tmux/list_panes_test.go` (new)

1. `type PaneLocation struct{ PaneID, SessionID, PanePID string }`. Add `ListAllPanes(ctx context.Context) ([]PaneLocation, error)` to `Executor`. The doc says:
   - ONE round trip for every pane of the server;
   - the inventory reads it twice per pass, once to enumerate and once to re-confirm membership (spec R2);
   - it takes ctx for the reason `PaneSessionID` does.
2. Real: `exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_id} #{session_id} #{pane_pid}")`, parsed by a pure `parsePaneLocations(out []byte) ([]PaneLocation, error)`. Blank lines are skipped; any other line that is not exactly `%N $N N` makes the listing an error (spec D5), because skipping it would turn an untrustworthy listing into a pane that reads as gone. The separator is a space: tmux without a UTF-8 locale rewrites TAB in `-F` output to `_` (alpha.340), and none of the three values can contain a space.
3. Fake:
   - rows come from `paneSessionIDs`;
   - `PanePID` uses `ActivePanePID`'s lookup (`activePanePIDs`, then `panePIDs`, then `"fake-active-pid"`);
   - sorted by pane id;
   - `ctx.Err()` first, and again after taking its lock, so a deadline that passes while it waits is honoured as the real bounded read honours it;
   - knob `SetListAllPanesError(err)`.
4. Tests:
   - parser: good lines, a short line, a blank line, trailing newline;
   - the fake mirrors `SetPaneSessionID` / `SetPanePID` / `ForgetPaneSessionID`;
   - an expired ctx errors;
   - the knob errors.
5. Mutation: split on TAB → the parser test turns red.

PR A: codex R1 + R2. Focus on:
- D1 / D2 parity and the fast-path boundary;
- `procargs2` bounds;
- the D10 re-check.

---

## Phase B (PR B, stacked on A) — the owner pass and the inventory

### B1 — the walk reads through a `ProcessView` (pure refactor)

Files:
- `internal/module/agent/ancestor.go`
- `internal/module/agent/pane_owner.go`
- `internal/module/agent/provenance_handler.go`
- `internal/module/agent/ancestor_test.go`
- `internal/module/agent/pane_owner_test.go`
- `internal/module/agent/provenance_handler_test.go`

1. `liveProcs struct{ read procReader }` implements `agentpkg.ProcessView` through `isPidAliveFn`, `processStartTimeFn` and `read`. `classifyAncestor` passes `liveProcs{read: readProcessInfoFn}`, so the hook path reads the same PIDs in the same order (the `provenance_test.go:170` premise).
2. `walkPaneAncestry(paneID, startPID, agentType, procs agentpkg.ProcessView, opts)`. The candidate check uses `procs.Alive` / `procs.StartTime`, and the step uses `procs.Read`.
3. `resolvePaneOwners(ctx, paneID string, panePID int, procs agentpkg.ProcessView)`:
   - survivors are decided by `procs.Alive` / `procs.StartTime`;
   - `ctxGuardedReader` becomes `ctxGuardedProcs`, which guards `Read` only, the same reads as today.
4. `provenance_handler.go` keeps today's flow: it resolves the pane PID with `resolvePanePIDFn` (an error → the pane contributes nothing) and passes `liveProcs{read: newMemoProcReader(readProcessInfoFn)}`.
5. Test edits (setup only):
   - `ancestor_test.go` walk calls wrap their reader as `liveProcs{read: …}`;
   - `pane_owner_test.go` calls pass the fixture pane PID (the value they gave `withPanePID`) and `liveProcs{read: …}`.
   - **`TestResolvePaneOwners_PanePIDUnresolvable_EmptyResultNoError` moves, it is not deleted** (codex #11). Re-add it in this commit at handler level (`TestHandleSessionProvenance_PanePIDUnresolvable_PaneContributesNothing`):
     - `attachPane(fake, "%5", "$0", "not-a-pid")` with a seeded root;
     - assert `found:false`, `err == nil` through `ResolveSessionOwner`, and zero process reads (`withRecordedReads`).
     - The same setup keeps working in B2, where the listing's PID fails to parse.
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
   - `OwnerResult{Owner PaneOwner; Found bool; Err error}`.
   - `type ProcessSource func() (agentpkg.ProcessView, error)`: the pass calls it **at most once**, on the first pane it walks (codex #4).
   - `OwnerPass`: `Resolve(ctx, code)` and `Confirm(ctx) map[string]OwnerResult`.
   - `OwnerPassResolver` = `OwnerResolver` + `NewOwnerPass(src ProcessSource) OwnerPass`, where nil `src` means the pass takes its own via `takeProcSnapshotFn`.

   The doc states the contract:
   - one pass is one process view and two listings;
   - `Resolve` answers are provisional until `Confirm`;
   - single goroutine;
   - `Confirm` once.
2. `owner_pass.go`. Seam: `var takeProcSnapshotFn ProcessSource`, wrapping `agentpkg.SnapshotProcesses(context.Background())`. Return `nil, err`, never a typed nil.

   **`Resolve(ctx, code)`:**
   1. `frames == nil`, `tmux == nil` or `code == ""` → `found:false`. Expired ctx → `Err`.
   2. **First use:**
      - `frames.ListAll()`;
      - if there are no frames, stop: no listing, no process source;
      - otherwise call `tmux.ListAllPanes(ctx)` (listing 1);
      - index the distinct framed panes, in `ListAll` order, by `EncodeSessionID(row.SessionID)`, with the pane PID parsed like `resolvePanePID`.

      A missing row, a bad id or a bad PID excludes the pane. A failure here is sticky: every `Resolve` reports it as `Err`.
   3. The process view comes from `src` (or `takeProcSnapshotFn`), called once, on the first pane walked. A failure is sticky `Err` and the source is never called again.
   4. For each of the session's panes, call `resolvePaneOwners(ctx, pane, pid, view)`. An error → `Err`. Keep the panes with owners as candidates.
   5. Read `ctx.Err()` after the loop → `Err`. No candidates → final `found:false` (D6). Otherwise the session is pending.

   **`Confirm(ctx)`:**
   1. No pending session → return the results.
   2. Otherwise call `ListAllPanes(ctx)` once (listing 2). A listing error, **or `ctx.Err()` read after the listing returns**, gives every pending session `Err` (D5, D6).
   3. Otherwise, per pending session:
      - keep a candidate pane only if the listing still places it in a session that encodes to `code`; absent means dropped;
      - drop owners with an empty `SessionID`;
      - pick with `betterOwner`.

   The reasoning in today's comments (`provenance_handler.go:105-220`) moves here with the code. Drop the "four `ps` forks" rationale where it no longer applies.
3. `resolveSessionOwnerErr` keeps `provenanceTimeout`, then runs `NewOwnerPass(nil)` → `Resolve` → `Confirm` → `results[code]`. Delete `panesOfSession` and `paneStillInSession`. `newMemoProcReader` moves to `ancestor_test.go`.
4. Fixture edits (setup only, spec D8):
   - `withProcessTree`, `withLivePids`, `withProcessReadError` and `withProcessTreeSequence` also install `takeProcSnapshotFn`. It returns `liveProcs{read: newMemoProcReader(readProcessInfoFn)}`, built when the pass calls it, so `withSlowReads`, `withRecordedReads` and `withProcessReadHook`, installed later, still apply. That makes it one memoised fixture view per pass, and `TestHandleSessionProvenance_MemoizesReadsAcrossPanes` now pins "one view shared by every pane of the pass".
   - Executor wrappers override `ListAllPanes`, where they used to override `PaneSessionID`. Each wrapper's doc comment is rewritten for the batched shape, and its assertions stay.
     - `countingExecutor`: counter renamed `listCalls`. Its assertion keeps its meaning: zero tmux reads once the deadline has expired.
     - `recheckExecutor`: call 1 is listing 1 and call 2 is listing 2.
       - `blockAfter: 1` blocks listing 2 until ctx is done. That is the cancelled-re-check case.
       - `sleepAfter: 1` answers listing 2 correctly, using `context.Background()`, after the deadline. That is the "listing succeeded, then the clock is read" case codex #9 asks to tell apart. Both cases already have their own tests: `RecheckIsCancellable` and `RecheckAnswersAfterTheDeadline_NotAnAnswer`.
     - `paneTargetedRecheckExecutor` becomes "block listing 2". Its test keeps two panes and `found:false`.
       - It now pins that the pane `%5` candidate, already walked and **un-adopted** when the shared re-check is cancelled, is not reported.
       - Under the batch there is no earlier adoption to discard, so the comment says that what the test protects is "a candidate is never an answer without a re-check that completed in time".
     - `blockingEnumerationExecutor` blocks listing 1.
5. New tests (`owner_pass_test.go`):
   1. **Fork count (R2).**
      - Setup: 3 sessions, each with a root;
      - a counting `takeProcSnapshotFn`;
      - an executor counting `ListAllPanes`.
      - Assert:
        - all 3 are answered with their own root;
        - snapshots = **1**;
        - listings = **2**;
        - the same with 6 sessions.
   2. **Lazy.** With no frames, a pass over 2 codes gives `found:false, err:nil`, with 0 listings and 0 snapshots.
   3. **Moved pane, batch.**
      - Session A's pane moves to B between listing 1 and listing 2 (`withProcessReadHook` → `SetPaneSessionID`).
      - A gets `found:false`.
      - B gets `found:false`: it never owned the pane at enumeration.
      - Session C, untouched in the same pass, is still answered.
   4. **Listing 2 fails** (`SetListAllPanesError` between `Resolve` and `Confirm`) → the candidate session gets `Err`. A no-candidate session in the same pass keeps `found:false, err:nil`.
   5. **Deadline between `Resolve` and `Confirm`** → candidates get `Err`. A no-candidate session that `Resolve` already finalised keeps its answer.
   6. **Unresolvable pane.** A pane absent from listing 1, or with an unparseable PID, contributes nothing: no error, no process reads.
   7. **Listing 1 fails** → every `Resolve` gets `Err`, and the process source is never called.
   8. **Caller's source.** `NewOwnerPass(src)` calls `src` exactly once and never calls `takeProcSnapshotFn`, and every walk read goes through `src`'s view (recorded). A failing `src` → every session `Err`, and `src` is called once.
   9. **Real processes, darwin** (codex #10). Setup:
      - `takeProcSnapshotFn` is left at its production value;
      - `readProcessInfoFn`, `processStartTimeFn` and `isPidAliveFn` are armed to `t.Fatal` if called;
      - a frame is seeded for an orphaned `sleep` (spawned as `sh -c 'sleep 30 & echo $!'`, so its PPID is 1), with the `ProcessStartTime` taken from a real snapshot;
      - the fake listing gives the sleep's PID as the pane PID.

      Assert:
      - the pass answers that frame as the owner;
      - the per-PID seams were never called;
      - listings = 2.
6. Mutations:
   - `Confirm` skips the membership filter → test 3 and `TestHandleSessionProvenance_PaneMovedAfterEnumeration_NotAnAnswer` turn red;
   - snapshot per `Resolve` → test 1 turns red;
   - listing per `Resolve` → test 1 turns red;
   - drop the ctx read after listing 2 → `RecheckAnswersAfterTheDeadline_NotAnAnswer` turns red;
   - a listing-2 error drops panes instead of failing → test 4 turns red;
   - call `src` again after a failure → test 8 turns red;
   - walk through `liveProcs` instead of the view → test 9 turns red.

### B3 — `/api/peers` runs one pass and shares one snapshot with the registry

Files:
- `internal/module/peers/module.go`
- `internal/module/peers/module_test.go`
- `internal/module/peers/fakes_test.go`

1. New `Module` field `snapshotProcs func(ctx context.Context) (agentpkg.ProcessView, error)`. Its default in `New` wraps `agentpkg.SnapshotProcesses`, with no typed nil.
2. `localEnvelope`, after the session list:
   - call `procs, procsErr := m.snapshotProcs(invCtx)` **once**;
   - `live := m.liveness`;
   - if `procsErr == nil`, use `inventoryLiveness(live, procs)` (D4 / D10):
     - `PidAlive` and `Stat` are kept;
     - `Info(pid)` = `procs.Read(pid)`, except that `ErrNotInSnapshot` / `ErrProcessChanged` fall back to `live.Info(pid)`;
     - `StartTime` is derived from that `Info`;
   - on error, log it, and the registry keeps today's per-PID liveness: that is today's behaviour, used only on this failure path;
   - `ReadRegistryDiag(m.registryDir, live)`.
3. `pass := m.ownerPass(func() (agentpkg.ProcessView, error) { return procs, procsErr })`. That means the pass reuses the inventory's one snapshot, or its error, and never takes a second.
   - It calls `NewOwnerPass(src)` when `m.owners` is an `agent.OwnerPassResolver`.
   - Otherwise it uses `perSessionPass`, which calls `ResolveSessionOwner` inside `Resolve` and returns the stored answers from `Confirm`. This mirrors the `contextSessionLister` fallback.
4. The loop:
   - keeps its `m.now()` deadline check unchanged;
   - calls `pass.Resolve(invCtx, s.Code)`;
   - after the loop, maps `pass.Confirm(invCtx)` through today's rules (`Err` → `unresolved`, `Found` → `owners`).

   The generation re-probe stays after `Confirm`. Add `var _ agent.OwnerPassResolver = (*agent.Module)(nil)` next to the existing assertions.
5. Test setup (spec D8):
   - `newTestModule` and the options constructor set `snapshotProcs` to a view built from the test's fake `Liveness` (`livenessView`: `Alive` → `PidAlive`; `Read` → `Info`, or a `ProcessInfo` carrying `StartTime(pid)` when `Info` is nil);
   - `fakeOwners` / `ctxRecordingOwners` implement only `ResolveSessionOwner`, so they run through `perSessionPass`, and their call-order, clock and context assertions are untouched.
6. New tests:
   1. **One snapshot, shared.** Setup:
      - a counting `snapshotProcs`;
      - `m.liveness.Info` / `StartTime` armed to `t.Fatal` (codex #10). `PidAlive` stays a fake answering alive: by D10 it is still the per-read `kill` check;
      - a fake pass resolver whose `NewOwnerPass` records `src` and calls it.

      Assert:
      - 1 snapshot;
      - `src()` returns that same view;
      - a live registry entry the view knows is classified from the view (`Info` from the view; the armed base `Info` is never called);
      - `Confirm` is called once;
      - every session is resolved through the pass.
   2. **Snapshot failure.** `snapshotProcs` errors. Assert:
      - it was called once;
      - the registry used `m.liveness`;
      - `src()` returns the error;
      - the inventory answers with those sessions unresolved and `partial`.
   3. **`Confirm` errors for one code** → that row is unresolved and the envelope is `partial`. A `Found` code → owner row.
   4. **Budget.** Sessions after the `m.now()` deadline never reach `Resolve`.
   5. **D10, registry verdicts equal today's.** A table over `inventoryLiveness` with a fake view and a counting fake `m.liveness`:
      - in the view and unchanged → `Info` from the view, base `Info` not called;
      - absent from the view (`ErrNotInSnapshot`) → base `Info` called, and its answer used;
      - `ErrProcessChanged` → base `Info` called;
      - any other view error → returned as is (unclassifiable, as today);
      - `PidAlive` is always base `PidAlive`.

      End to end through `ReadRegistryDiag`: an entry the view lacks but `kill` says is alive is classified exactly as base `Info` says, not "dead".
7. Mutations:
   - snapshot inside the loop → test 1 turns red;
   - give the registry `m.liveness` while the snapshot succeeded → test 1 turns red;
   - give the pass `nil` instead of `src` → test 2 turns red: the snapshot count becomes 2 through the real pass, which test 1 also covers with a counting resolver;
   - drop the `ErrNotInSnapshot` fallback → test 5 turns red;
   - answer `PidAlive` from the view → test 5 turns red.

### B4 — gates, mutation report, PR B

1. Run the full gates, plus the darwin real-process tests.
2. Record every mutation result in the PR body.
3. PR B: codex R1 + R2 (attack → critic). Focus on:
   - D5 / D6 / D10 semantics;
   - `Confirm` ordering;
   - the `ProcessSource` single-call contract;
   - the `perSessionPass` fallback;
   - the snapshot shared with the registry.

---

## Acceptance (after merge; the deploy is coordinated by `mlab/_0le0d2`)

1. Report merge and bump to the coordinator and **wait for the restart slot**. Do not restart the mlab daemon on my own.
2. After the restart, run `pdx peers --json` 10 times and record:
   - each run's wall time (target ≤ 300 ms);
   - that no run prints `partial: … not resolved within budget`.
3. Compare each session's owner (agent type, session id, pane) with the pre-deploy baseline captured 2026-10-06 (`scratchpad/baseline/run{1,2,3}.json`). Every session resolved in the baseline must resolve to the same owner. Sessions unresolved in the baseline must now resolve.
4. Check the status bar of `purdex3` (and of one more late-alphabet session): it shows its peer address, not `tmux:purdex3`.
