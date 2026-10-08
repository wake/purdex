# Interface language U1-2 — lights v2 (daemon) — plan

Spec: `docs/specs/2026-10-08-interface-u1-spec.md` §2 (N5, N6), §3, §6 (the mod event channel, shipped in alpha.604), **§7 (lights v2 contract)**, §9 (current-state map), §10. Cross-line: `docs/specs/2026-10-08-worker-status-deltas-design.md` §3.5 (the `nex.*` `epoch` / `bseq` on `/ws/host-events`).

Format as the U1-1 plan: contracts, rules, named tests and mutation gates; implementers write the code (TDD: each named test is written red first). Line numbers are as of `61d6931f`; re-check before editing.

Eight PRs (seven rows, a-3 split in two), each ≤ 800 lines diff / ≤ 20 files (estimates include tests; a PR that grows past the limit splits again before review):

| PR | Content | Depends on | Est. lines |
|---|---|---|---|
| **U1-2a-1** | mod + registry: every batch carries `cwd` / `interactive`; the heartbeat mirrors the error outcome and the background tasks (closes the U1-1 acceptance gap); **`tool.approved`** from a `ui.render` `ToolUse` observer (M-U1-6) | — | ~450 |
| **U1-2a-2** | `internal/lights`: the pure per-stream mod state machine (§7 table, dots, background kind, heartbeat reconcile) | — | ~650 |
| **U1-2a-3** | agent module: subscribe to the registry, worker, **mod overlay** on every projection, `source` / `background` on `NormalizedEvent` | a-2 | ~900 → split: **a-3a** subscriber + overlay + `tool.approved` in `internal/lights` (~530), **a-3b** worker, eviction, probe gate (~400, depends on a-3a) |
| **U1-2a-4** | hook fallback fixes: `is_interrupt` → idle, subagent `StopFailure` keeps the main status, `Stop` background symbol, per-pane error guard | a-3 | ~550 |
| **U1-2b-1** | per-tmux-session priority aggregation (error > waiting > running > idle), session background = highest across panes | a-3 | ~400 |
| **U1-2b-2** | the **hook emit slot**: every `hook` frame is read fresh, stamped `(epoch, seq)` and broadcast inside one mutex | b-1 | ~680 |
| **U1-2b-3** | complete snapshot: opt-in `agent=v2` → one `agent.snapshot` frame (complete list, high-water seq); legacy replay frames gain `snapshot: true` | b-2 | ~450 |

a-1 and a-2 are independent and can be built in parallel (different files). The rest is a chain. a-1 is the only PR that touches the mod; it can merge and deploy ahead of the others (its daemon half only fills registry fields). **Release gate: all eight merged** — the acceptance (restart recovery of error / background / cwd) needs a-1 as much as b-3.

Common rules:
- Go: `make lint` clean; `go test` for touched packages; `-race` only for the touched package, one package at a time (`internal/lights`, `internal/module/agent`, `internal/modevents`).
- Mod (a-1 only): `claude plugin validate cmd/pdx/plugin/purdex --strict`, `claude plugin test cmd/pdx/plugin/purdex`, `go test ./cmd/pdx/plugin/`, quoted in the PR body. M-U1-2 / M-U1-3 / M-U1-5 hold: no new registration, `$` only into top-level functions of `events.js`; the guard test stays green.
- Each task one commit; parallel subagents in one worktree commit with `git commit --only <files>`.
- Resource rules (mlab 16 GB): affected tests only during development; the full vitest is not needed (no SPA change); `go test -race` per package, one at a time.

---

## U1-2a-1 — mod and registry: stream identity and mirrors survive a daemon restart

Problem (U1-1 acceptance): a restarted daemon starts with an empty registry; a stream that resumes has no `session.start`, so `cwd` stays empty and `interactive` false. Lights v2 also needs, after a restart, the parts of the live state no later event repeats: an error outcome and the background tasks.

### Task A1-1 — `hooks/events.js`

- Module state gains `cwd: ''`, `lastError: false`, `background: null` (`{tasks, crons}` or null).
- `startReporter`: `ev.cwd = String(e.cwd ?? '')`. `session.switch` keeps `cwd` (same process).
- `body(events)` adds `cwd: ev.cwd` and `interactive: true` to the envelope (the reporter only runs for interactive sessions; U4 may send `false`).
- `turnStarted` (main): `lastError = false`. `turnCompleted` without `agentId`: `lastError = (e.reason === 'error')`. `sessionSwitch` and `startReporter`: `lastError = false`.
- `stopped` stores the same `{tasks, crons}` it enqueues as `ev.background`. `startReporter` resets it to null; `sessionSwitch` keeps it (the daemon clears the symbol on a switch and this mirror restores it within one beat if the tasks are still there; the next `classic.Stop` refreshes it either way).
- `beat`: when `$.agent.list()` throws, `agents` is **left out** (not `[]`, which would clear every dot until the next beat); data gains `error: ev.lastError` (always present), `background: ev.background` when non-null. The mod keeps `asks` as a `Map(tool_use_id → 'permission' | 'question')` (question = AskUserQuestion / ExitPlanMode, also when `tool.check` asks for one); the heartbeat's `asks` stays a plain id list.
- **`tool.approved`** (coordinator ruling after M-U1-6): `on('ui.render', { component: 'ToolUse' }, onToolUseRender).catch(($, e, next) => next(e))` — observe only, always `return next(e)` unchanged, O(1) per redraw. When `e.props.tool_use_id` is an open **permission** ask and `e.props.isRunning === true`: delete it from `asks` and enqueue `tool.approved {tool_use_id}`. A refusal or Esc needs nothing new (`tool.end` / `turn.complete` follow). Question asks still leave only at `tool.end`. `internal/modevents` `KnownTypes` gains `tool.approved`; spec §6.3 gains its row.
- No new hook registration; nothing else changes.

Tests (`events.test.ts`, existing kit): `every batch carries cwd and interactive`, `cwd survives /clear`, `heartbeat carries error after a main turn ends in error and clears it at the next main turn`, `a subagent turn ending in error does not set the heartbeat error`, `heartbeat mirrors the last background and a new session.start forgets it`, `a permission ask leaves on the ToolUse render with isRunning true and reports tool.approved once`, `a question ask is not approved by a render`, `a render of a tool with no open ask reports nothing`, `the render hook returns next(e) unchanged`, `heartbeat omits agents when agent.list throws`. Mutation gates (added): approving question asks → the question test red; no delete on approval → the "once" test red.
Mutation gates: `cwd` left out of `body` → the first test red; `lastError` set by a subagent turn → the subagent test red.

### Task A1-2 — wire and registry

Files: `internal/modevents/wire.go`, `registry.go`, tests.

- `Batch` gains `CWD string json:"cwd"` and `Interactive bool json:"interactive"` (absent in an older mod → zero values; unknown-field tolerance is unchanged).
- `Apply`: after the per-stream header update, `if b.CWD != "" { in.CWD = b.CWD }`; `if b.Interactive { in.Interactive = true }`. `session.start` keeps setting both as before (same values).
- Spec §6.2 (envelope) and §6.3 (heartbeat row: `error`, `background`) are updated in this PR.

Tests: `TestApply_EnvelopeFillsCwdAndInteractive` (a first batch of heartbeats only, as after a daemon restart → `CWD` and `Interactive` set), `TestApply_EnvelopeWithoutCwdKeepsSessionStartCwd`, `TestDecodeBatch_EnvelopeFieldsOptional`.
Mutation gate: drop the envelope copy → `EnvelopeFillsCwdAndInteractive` red.

---

## U1-2a-2 — `internal/lights`: the mod state machine (pure)

New package `internal/lights` (imports `internal/modevents` for the event type only; nothing in `internal/module`). No I/O, no clock reads: callers pass `now`.

### Types

```go
type Background string // "workflow" | "monitor" | "schedule" | ""

type Dot struct{ ID string; StartedAt int64 } // ms, the spawn event's `at`

type StreamState struct {
    Stream, SID string
    LastEvent   time.Time       // daemon receive time of the last applied event
    TurnID      string          // main turn in progress
    Asks        map[string]bool // open asks by tool_use_id (synthetic key "check:<seq>" when a check has none)
    Compacting  bool
    Err         bool
    Ended       bool
    Dots        map[string]Dot  // keyed by agent id
    Background  Background
}
```

`func NewStreamState(stream string) *StreamState`, `func (s *StreamState) Apply(ev modevents.Event, now time.Time) (changed bool)`, `func (s *StreamState) Status() agentpkg.Status`, `func (s *StreamState) Live(now time.Time) bool` (`!Ended && now.Sub(LastEvent) <= LiveWindow`, `LiveWindow = 30 * time.Second`), `func BackgroundKind(tasks []Task, crons int) Background`, `func (s *StreamState) DotList() []Dot` (sorted by StartedAt, then ID). `changed` reports a change of `Status()`, `DotList()`, `Background` or `SID` — not of `LastEvent` (the caller tracks liveness separately).

### Rules (spec §7; "main" = no `agent_id`)

| event | effect |
|---|---|
| any | `SID = ev.SID`, `LastEvent = now`, `Ended = false` (any later event reopens, like the registry) |
| `session.start` | reset: `TurnID`, `Asks`, `Compacting`, `Err`, `Dots`, `Background` cleared |
| `session.switch` | same reset (new conversation; the heartbeat mirror restores a background that is still there) |
| `session.end` reason ∉ {clear, resume} | `Ended = true`, `Background` cleared; reason clear / resume: nothing (a switch follows) |
| `turn.start` (main) | `TurnID = turn_id`, `Asks` cleared, `Err = false` |
| `turn.complete` main | `TurnID = ""`, `Asks` cleared, `Err = (reason == "error")` |
| `turn.complete` with `agent_id` | remove that dot |
| `tool.check` decision `ask` | add `tool_use_id` (or `check:<seq>`) to `Asks` |
| `tool.start` tool ∈ {AskUserQuestion, ExitPlanMode} (main or subagent) | add `tool_use_id` to `Asks` |
| `tool.end` | remove `tool_use_id` from `Asks` |
| `tool.approved` (a-3a) | remove `tool_use_id` from `Asks` |
| `compact.start` / `compact.end` main, `trigger ≠ precompute` | `Compacting = true / false` |
| `agent.spawn` without `workflow_run_id` | add dot `{agent_id, at}`; with `workflow_run_id`: nothing (N6) |
| `background` | `Background = BackgroundKind(tasks, crons)` |
| `heartbeat` | reconcile, below |
| `usage`, unknown | nothing |

`tool.approved {tool_use_id}` (M-U1-6) removes that ask like `tool.end` does — added in **U1-2a-3a** on top of a-2 (a-2 was already in review), with `TestStatus_ApprovedLeavesWaiting` (one ask approved → running; two asks, one approved → still waiting; a new ask afterwards → waiting again).

`Status()`: `Ended` → clear; `Err` → error; `len(Asks) > 0` → waiting; `TurnID != "" || Compacting` → running; else idle.

Heartbeat reconcile (repairs lost events, e.g. Esc without `turn.complete`): `TurnID = turn_id` (empty when absent); `Asks` = exactly the listed ids when `asks` is present (the mod already dropped an approved ask from its mirror, so a beat never pulls it back); absent / null fields leave their part of the state alone (`asks`, `compacting`, `agents` — the mod omits `agents` when `$.agent.list()` throws; a-2 review); `Compacting = compacting`; `Err = error` when the field is present (U1-2a-1 mods), unchanged otherwise; dots = agents with status ∈ {pending, running, waiting} (`$.agent.list()` never lists workflow agents, d.ts) — listed-active ids not yet dotted are added with `StartedAt = ev.at`, dotted ids not listed-active are removed; `background` present → `Background = BackgroundKind(...)`.

`BackgroundKind`: any task `type == "workflow"` → workflow; else any `monitor` → monitor; else `crons > 0` → schedule; else "" (`shell` and `subagent` tasks show nothing, N6). The CC hook payload lists only in-flight tasks (d.ts `StopHookInput.background_tasks`), so status is not filtered.

Approval (user / coordinator ruling 2026-10-08, replaces the earlier known limitation): a permission ask leaves waiting on `tool.approved`, which the mod reports ~16 ms after the user approves in **any** terminal (M-U1-6). A new ask enters waiting again. Known gap: a subagent's tool row may not be drawn, so its approval is not seen and its ask leaves at `tool.end`.

Tests (`state_test.go`, table-driven, each §7 row): `TestStatus_TurnLifecycle`, `TestStatus_AskFromCheckAndAskTools` (incl. a subagent's AskUserQuestion), `TestStatus_ToolEndClearsOnlyItsAsk`, `TestStatus_ErrorUntilNextMainTurn`, `TestStatus_SubagentErrorIsNotMain`, `TestStatus_CompactIsRunningPrecomputeIsNot`, `TestStatus_EndedIsClearAndAnyEventReopens`, `TestStatus_ClearResumeEndIsNotAnEnd`, `TestSwitch_ResetsTurnAndDots`, `TestDots_SpawnAddsWorkflowSpawnDoesNot`, `TestDots_SubagentTurnCompleteRemoves`, `TestHeartbeat_RepairsLostTurnComplete` (turn.start, then a heartbeat without turn_id → idle), `TestHeartbeat_ReconcilesDotsAndAsks`, `TestHeartbeat_ErrorFieldAbsentKeepsErr`, `TestHeartbeat_RestoresBackground`, `TestBackgroundKind_Priority` (workflow > monitor > schedule; shell only → ""), `TestApply_ChangedOnlyOnVisibleChange` (two identical heartbeats → second `changed == false`), `TestLive_Window` (30 s inclusive, ended never live); after review: `TestHeartbeat_AbsentFieldsKeepState`, `TestHeartbeat_AgentsAbsentKeepsDots`, `TestHeartbeat_NullFieldsKeepState`.
Mutation gates: workflow spawn counted → `SpawnAddsWorkflowSpawnDoesNot` red; heartbeat not clearing TurnID → `RepairsLostTurnComplete` red; subagent turn.complete sets Err → `SubagentErrorIsNotMain` red; `changed` true on every heartbeat → `ChangedOnlyOnVisibleChange` red.

---

## U1-2a-3 — agent module: mod overlay

### Task A3-1 — subscription and state map

Files: `internal/module/agent/modlights.go` (new), `module.go`, tests.

- `Dependencies()` adds `"modevents"` so its registry is published before `Init`; `Init` looks it up (`c.Registry.Get(modeventsmod.ServiceName)`); absent → the mod path is off (every overlay lookup misses).
- `Start` subscribes; `Stop` cancels the subscription, then stops the worker (A3-2) and waits for it. The overlay is off until the worker runs (see "The overlay switch" below).
- Module state, guarded by its own `modMu` (never held while calling the store, tmux, or `m.mu`): `modStreams map[string]*lights.StreamState` (by stream), `modBySID map[string]string` (sid → newest stream), `modDirty map[string]struct{}` (sids whose panes must be re-emitted), `modKick chan struct{}` (cap 1). **`modMu` is a leaf lock**: nothing else is taken while it is held (the overlay copies what it needs and releases it).
- The subscriber `func(info modevents.StreamInfo, ev modevents.Event)`: under `modMu`, get or create the state, `changed := st.Apply(ev, now())`, update `modBySID` (a switch moves the old sid out and marks **both** sids dirty), on `changed` add the sid to `modDirty`; release; non-blocking send on `modKick`. **It never touches the frame store, tmux, `m.mu`, the events bus or the registry** (spec §6.4: subscribers must not block and must not lead back into `Apply`). Eviction mirrors the registry: a stream ended > 30 min or idle > 2 h is dropped by the worker tick; when the dropped stream is `modBySID[sid]`, the index is re-pointed to the newest other stream with that sid (or deleted) and the sid is marked dirty (review #6).

### Task A3-2 — worker

- One goroutine: on `modKick` or every `modTick = 5 s`. Each round: take `modDirty` (swap under `modMu`), plus the sids whose `Live(now)` flipped since the last round (stale → the pane falls back to hook status, so it must be re-emitted); for each sid: `frames.ListRootsBySessionID(sid)` → pane ids → `resolvePaneSession(pane)` → tmux session names (deduplicated) → for each session `m.emitSessionState(name, "mod", detail{"mod_event": <type or "stale">})`.
- `emitSessionState` (U1-2a: a thin helper over today's path; U1-2b-2 moves it into the emit slot): `projectionForSession(name)` → `buildProjectionNormalized(...)` → compare with `m.lastEmittedLights[name]` (`{status, background, source, dot ids, representative frame id, agent_type, model}` — every wire field the representative pane decides, so a change of representative with an equal status is still emitted, review #7); emit only when different (`emitNormalizedToCode(resolveSessionCode(name), …)`), update `m.currentStatus` / `m.subagents` via `syncProjectionState`. The hook path also records into `lastEmittedLights` when it emits (it keeps emitting on every hook, as today).
  - Why change-only: the SPA sets unread on **every** idle / waiting / error frame (`useAgentStore.ts:283–305`), so a 10 s heartbeat re-emitting the same idle would re-raise unread after the user read it.
- `RawEventName` of a mod emit is `"mod"` (not a notification name, so `normalizeEventName` and the notification settings never match it); `Detail = {"mod_event": "<type>"}`.

### Task A3-3 — overlay on every projection

- `SessionProjection` gains `Status agentpkg.Status`, `Source string` ("mod" | "hook"), `Background string`. `buildPaneProjection` sets `Status = TopFrame.Status`, `Source = "hook"`.
- `m.applyModOverlay(projections []SessionProjection)` runs at the end of `liveFrameProjections` and `projectPane` (every projection consumer sees the effective state). Per projection: `sid := TopFrame.SessionID`; `stream := modBySID[sid]`; when the state is `Live(now)`:
  - `Status = st.Status()`, `Source = "mod"`, `Background = st.Background`;
  - `Subagents` = the projection's proxy refs (unchanged) + one native ref per mod dot, ordered by `StartedAt`: the projection's existing native ref with the same ID when there is one (keeps `Delegating`, `DelegatingToolUseIDs`, `StartedAt`), else `{ID, Type: TopFrame.AgentType, StartedAt}`. Hook native refs that are not mod dots (workflow agents) are dropped from the projection — not from the frame row.
  - an ended stream is never live, so the pane falls back to its frame (the hook `SessionEnd` or the sweep deletes the frame; the overlay never invents a clear).
- Not live / no stream: unchanged (hook status), `Background` from the hook map of U1-2a-4 (empty until then).
- `buildProjectionNormalized` reads `projection.Status` (falls back to `TopFrame.Status` when empty, for projections built elsewhere in tests), and sets `Source`, `Background`. `syncProjectionState` stores the effective status.
- `NormalizedEvent` (`internal/agent/status.go`) gains `Background string json:"background"` (always present: `""` clears the symbol) and `Source string json:"source"` (`"mod" | "hook"`, always set: the nontmux path and every projection-less frame say `"hook"`, review #4). Spec §7 already names both.
- **Probe gate** (review #9): while a session's representative projection has `Source == "mod"`, probe-driven transitions (`probe_orchestrator.go:353–399`) are skipped — no frame write, no emit; probes are the recovery path for sessions without a live stream. `syncProjectionState` stores the effective status, so the probe's own error / same-status gates (`:362`, `:369`) read what the user sees.
- The hook path is otherwise unchanged in this PR: hooks keep writing `frame.Status` (it is the fallback the moment the stream goes quiet), but while the stream is live the emitted status is the mod's.

Tests (`modlights_test.go`, existing fakes): `TestModOverlay_LiveStreamWinsOverHookStatus` (frame idle by hooks, mod turn.start → emitted running, `source: "mod"`), `TestModOverlay_StaleStreamFallsBackToFrameStatus` (31 s without events → frame status, `source: "hook"`, and the worker emits once on the flip), `TestModOverlay_MatchesBySid` (two panes, only the one whose frame sid equals the stream's sid is overlaid), `TestModOverlay_FollowsClear` (session.switch to a new sid + the hook SessionStart{clear} updating the frame's sid → overlay applies again; before the frame update it does not), `TestModOverlay_DotsKeepDelegatingAndDropWorkflowAgents`, `TestModOverlay_EndedStreamFallsBack`, `TestModWorker_EmitsOnChangeOnly` (heartbeats repeating the same state → one frame), `TestModWorker_MarksBothSidsOnSwitch`, `TestModSubscriber_NeverBlocks` (the frame store blocks on a channel; 1 000 `Apply`s of the subscriber return; the worker emits after release), `TestModSubscriber_NoRegistryReentry` (subscriber runs inside a real `modevents.Registry.Apply` under `-race`; no deadlock), `TestNormalized_BackgroundAlwaysPresent` (marshalled JSON contains `"background":""`), `TestNormalized_NonTmuxSourceIsHook`, `TestModWorker_EmitsWhenRepresentativeChangesWithEqualStatus`, `TestModEviction_RepointsSidIndex` (two streams with one sid; evicting the newer re-points to the older live one and marks the sid dirty), `TestProbe_SkippedWhileModLive` (a probe transition on a mod-live session writes nothing and emits nothing), `TestProbe_ResumesAfterStreamGoesStale`, `TestProbe_ModErrorNotClearedByProbe`.
Mutation gates: overlay keyed by pane instead of sid → `MatchesBySid` red; emitting without the change check → `EmitsOnChangeOnly` red; subscriber calling the store → `NeverBlocks` red (deadline); `omitempty` on background → `BackgroundAlwaysPresent` red; digest without the representative frame id → `EmitsWhenRepresentativeChangesWithEqualStatus` red; eviction leaving the index → `RepointsSidIndex` red; probe gate removed → `SkippedWhileModLive` red.

If this PR passes 800 lines it splits into A3-1+A3-3 (overlay, driven by a test-only kick) and A3-2 (worker, tick, eviction).

### The overlay switch (a-3a / a-3b split, lead ruling 2026-10-08)

Found while checking a-3a's deploy-alone safety: the mod event channel has been live since alpha.604, so an overlay without the A3-2 worker changes the lights at once — and wrongly. The mod queues events and flushes 150 ms after the first (`events.js` `FLUSH_MS`); hooks arrive almost at once. At the end of a turn the `Stop` hook emits first, the overlay still reads the mod's `running` (its `turn.complete` sits in the mod's queue), the frame goes out as `running` / `source: "mod"`, and when `turn.complete` lands 150 ms later it only marks the sid dirty — nothing re-emits, so the light stays `running` until the next hook (usually the user's next prompt). A permission ask's `waiting` has the same ordering race.

- **a-3a** ships the overlay behind a switch, **off by default**: `applyModOverlay` is a no-op until it is on. Deployed alone, a-3a subscribes and keeps the per-stream state, and the lights on the wire are exactly today's; frames only gain `source: "hook"` and `background: ""`. Tests turn the switch on in their helper; `TestModOverlay_OffByDefault` pins the deployed state.
- **a-3b** turns the switch on only once the worker is running, in the same place that starts it; `Stop` turns it off before stopping the worker. a-3b adds two regression tests for the race: `TestModWorker_StopHookBeforeTurnComplete` (a hook `Stop` emit while the mod still says running, then the mod's `turn.complete`: the last emitted status is `idle` within one worker round) and `TestModWorker_PermissionAskBeforeHookOrder` (the same shape for a permission ask's `waiting`).

---

## M-U1-6 — the approval signal (measured 2026-10-08)

CC 2.1.294, default permission mode, a Bash call that needs approval, a throwaway probe mod on `ui.render` {Spinner, ToolProgress, ToolUse} + `tool.check` / `tool.call`: `tool.call` starts, `tool.check` returns `ask` and the dialog opens; `ToolUse{tool_use_id}.isRunning` is `false` the whole time the dialog is open and turns `true` **16 ms** after the user's Enter on "Yes"; `ToolProgress{kind: background_hint}` follows **3.5 s** later (Bash only); `Spinner.mode` stays `tool-use` throughout (no signal). Refusal and Esc were not measured (they end in `tool.end` with an error / `turn.complete`). Recorded in spec §3; the coordinator chose this signal over watching answer keys in the daemon's terminal relay (works in any terminal, no key guessing, no relay change).

## U1-2a-4 — hook fallback fixes (no mod)

Files: `internal/agent/cc/status.go`, `internal/module/agent/handler.go`, `frame_ops.go`, `modlights.go`, tests.

- **`PostToolUseFailure` with `is_interrupt: true` → idle** (spec §7). `deriveCCStatus` returns `Status: idle` for it (any `agent_id`: Esc interrupts the whole turn); without `is_interrupt` it stays detail-only. The handler's detail-only branch (`handler.go:411–479`) keeps its "never resurrect" rule: when the sender's frame exists (`GetByIdentity`), `frames.UpdateStatusAndLastSeen(frame, idle)` (the narrow write, #632 R7), then project and emit; no frame → 200, nothing written, nothing emitted.
- **`StopFailure` carrying `agent_id` does not change the main status.** In `applyFrameEvent`'s `LifecycleStopFailure` branch (`frame_ops.go:518`), a non-empty payload `agent_id` detaches that native ref (when present) and keeps the frame's status, never `error`. The detach uses a status-preserving variant of `mutateSubagentsAndStatusWithRetry` (`frame_ops.go:1730`): each attempt writes the **reloaded** row's status, never a value captured before the first attempt, so a concurrent hook / probe status write that caused the OCC conflict survives (review #5); the "ref absent" and "already detached" fall-throughs keep the status too. No `agent_id` → unchanged (main error). The handler's in-memory status write (`handler.go:607–617`) skips it as well.
- **`Stop` with `background_tasks` / `session_crons` sets the background symbol** (hook source). `m.hookBackground map[frameID]lights.Background` under `modMu`: a root frame's `Stop` sets `lights.BackgroundKind(tasks, len(crons))`; `SessionStart` (any source) and the frame's deletion (SessionEnd, sweep `afterFrameCleared`) remove it. The overlay uses it when the stream is not live.
- **Error guard per pane.** `handler.go:384–409` reads the sender frame's status (`GetByIdentity(pane, pid, start)`; no frame → no guard) instead of `m.currentStatus[session]`, so another pane's error no longer blocks this pane's events. The whitelist (UserPromptSubmit, SessionStart, SessionEnd, Stop except opencode) is unchanged.
- Spec §9's "tests that pin today's behaviour" in this area are rewritten, not deleted: `TestHandleEvent_ErrorGuardBlocksFrameMutation` (now per pane), `TestStopFailure_NativeDetach_Hits` / `_Misses_*` / `_FixtureReplay_DthnPayload` / `_FrameAbsentBeforePreCheck` (status no longer error when `agent_id` is set), `TestStopFailure_NoAgentId_LegacyBehaviour` and `_EmptyAgentId_LegacyBehaviour` unchanged (still error).

New tests: `TestDerive_PostToolUseFailureInterruptIsIdle`, `TestDerive_PostToolUseFailureWithoutInterruptStaysDetailOnly`, `TestHandler_InterruptIdlesExistingFrame`, `TestHandler_InterruptNeverResurrects`, `TestStopFailure_SubagentKeepsMainStatus` (running main, subagent StopFailure → still running, dot removed), `TestStop_BackgroundSymbol` (monitor task → monitor; shell only → ""; crons → schedule; next SessionStart clears), `TestErrorGuard_OtherPaneErrorDoesNotBlock`, `TestErrorGuard_OwnPaneErrorStillBlocks`, `TestStopFailure_SubagentDetachKeepsConcurrentStatus` (the first `UpsertIfUnchanged` conflicts because another writer set the frame to waiting; after the retry the row is waiting with the ref gone).
Mutation gates: `is_interrupt` ignored → `InterruptIdlesExistingFrame` red; subagent StopFailure writes error → `SubagentKeepsMainStatus` red; guard reads the session map → `OtherPaneErrorDoesNotBlock` red.

---

## U1-2b-1 — per-tmux-session priority aggregation

Files: `frame_ops.go` (`projectionSortGreater` and its two callers), tests.

- `projectionRankGreater(candidate, current)`: rank of the effective `Status` (error 4 > waiting 3 > running 2 > idle 1 > clear / empty 0), then `TopFrame.StartedAt`, then `FrameID` — replaces `projectionSortGreater` in `selectSessionProjectionBy` and `liveSessionProjections` (spec §7 "not most recently started pane").
- The selected projection's `Background` becomes the highest across **all** the session's panes (workflow > monitor > schedule); `Subagents`, `AgentType`, `Model` stay the representative pane's (one CC per pane is the norm; a second agent pane's dots are not merged — documented).
- `source` is the representative pane's.
- Because selection now depends on the overlay, a mod change in pane A can change which pane represents the session: the worker (A3-2) already re-emits every session of a dirty sid's panes.

Tests (`projection_test.go`, `select_projection_nocode_test.go`): `TestSelectSession_HighestPriorityPaneWins` (older pane waiting beats newer running), `TestSelectSession_ErrorBeatsWaiting`, `TestSelectSession_TieFallsBackToLatestStart`, `TestSelectSession_BackgroundIsHighestAcrossPanes`, `TestSendSnapshot_CollapsesMultiplePanesInSession` updated to the priority rule.
Mutation gate: compare StartedAt first → `HighestPriorityPaneWins` red.

---

## U1-2b-2 — the hook emit slot and `(epoch, seq)`

Problem: every `hook` frame is built from a projection read earlier, outside any lock, and broadcast later — two emits for one session can be read in one order and broadcast in the other (spec §3 "last-writer-wins"), and a snapshot cannot be ordered against live frames. A sequence number stamped at broadcast fixes ordering only if the state it carries is read in the same critical section.

### Contract

- `hookEmitter` in the agent module: `mu`, `epoch string` (= `core.BootID`), `seq uint64` (one counter for **all** `hook` frames of this daemon process, starting at 0; the first frame has 1).
- `m.emitSession(sessionName string, build func(p *SessionProjection) (agentpkg.NormalizedEvent, bool))`: under `mu`: fresh `projectionForSession(sessionName)` → `build` (false = skip) → `seq++` → `normalized.Epoch, normalized.Seq` → `core.Events.Broadcast(code, "hook", …)` → `syncProjectionState` and `lastEmittedLights` updated. Every `hook` frame goes through it: the handler's three emits, `nontmux.go`, `sweep.go` ×3, the probe orchestrator ×2 (via `broadcastToSession`), the mod worker. Call sites pass a closure that captures what they add (event name, `Detail`, `Model`, provenance and exit envelopes, the degraded-to-clear flag) and builds from the **fresh** projection.
- `NormalizedEvent` gains `Epoch string json:"epoch"` and `Seq uint64 json:"seq"` — in the `value`, always present (the `nex.*` round-2 lesson: a top-level `HostEvent.Seq` is `omitempty`).
- Contiguity: the seq is taken and the frame broadcast to every subscriber inside `mu`, so on one connection consecutive `hook` frames have consecutive seqs; a gap means this subscriber dropped a frame (a non-strict subscriber's full buffer). Rotation at 2^53−1 starts a new epoch (`BootID + "-" + n`); not reachable in practice, tested with a seam.
- Lock order (review #8), total: `hookEmitter.mu` → `m.mu` → `modMu`; the store's and tmux's internal locks are leaves (they never call back into the module). Existing caller-locked projection reads (`captureProbeIntentReevalLocked` `module.go:515`, `snapshotStatuses` `:786`, `lookupTopFrameWith` `:825`) hold `m.mu` and, through the overlay, take `modMu` — consistent with the order. **Nothing may call `emitSession` while holding `m.mu` or `modMu`**; the PR lists every `emitSession` call site with the locks held there (none) in its body. The handler's error guard, frame writes and the subscriber stay outside the slot.
- `TestLockOrder_ConcurrentPaths` (`-race`, 5 s deadline): hook emits, a rename (`renameSessionLocked` + probe reeval), `snapshotStatuses`, mod worker emits and subscribes run concurrently with a slow fake store; the test fails on deadline (deadlock), not on timing.
- Cost: hook emits serialize on one projection read (today each already does that read, unserialized). Logged when a hold exceeds 250 ms.

### Naming against the other `/ws/host-events` versions (cross-line, #1866)

| frame family | where | epoch | counter | meaning |
|---|---|---|---|---|
| `sessions` | top-level `epoch` / `seq` | random per session module | per versioned list read | list version |
| `nex.execution` / `nex.executions.hello` | `value.epoch` / `value.bseq` | projector epoch, rotates on bus loss | contiguous per broadcast | delta order, gap ⇒ reconcile |
| **`hook`** (this PR) | `value.epoch` / `value.seq` | daemon `boot_id` | contiguous per broadcast, all sessions | frame order, gap ⇒ resync |

Each family keeps its own cursor in the SPA; no field is shared across families. The `hook` counter has the `bseq` semantics; whether to rename it `bseq` is listed for the coordinator below (spec §7 says `seq`).

Tests: `TestEmitSlot_SeqContiguousAcrossSessions`, `TestEmitSlot_FreshReadInsideSlot` (two concurrent emits for one session where the first's build blocks: the frame with the higher seq carries the newer state), `TestEmitSlot_AllHookFramesGoThroughIt` (source scan of `internal/module/agent/*.go`, non-test: no `Broadcast(` with `"hook"` outside `emitSession`, fail-closed like the mod guard), `TestEmitSlot_WireJSON` (`"epoch":"<boot>","seq":1` present), `TestEmitSlot_EpochRotatesAtMax`, existing `sweep_broadcast_test.go`, `nontmux_test.go`, probe orchestrator broadcast tests updated to the slot.
Mutation gates: build from the pre-read projection → `FreshReadInsideSlot` red; seq per session → `SeqContiguousAcrossSessions` red; a stray `Broadcast(code, "hook"` → `AllHookFramesGoThroughIt` red.

---

## U1-2b-3 — complete snapshot

- `core.featuresOf` accepts `agent=v2` next to `nex=v1` (either, both; `FeatureAgentV2 = "agent.v2"`). **An `agent.v2` subscriber is strict** like `nex.v1` (`EventSubscriber.strict = Wants(nex.v1) || Wants(agent.v2)`, buffer `optedInSendBuffer`): a frame that does not fit — the snapshot, the last `hook` frame before a quiet period, anything — ends the connection, and the reconnect brings a fresh snapshot (review #1: a non-strict drop of the last frame leaves no gap to detect). Old clients stay best-effort.
- On subscribe, under the emit slot (so the snapshot and every live frame are ordered: anything broadcast before it has `seq ≤ H`, anything after `> H`):
  - an `agent.v2` subscriber gets **one** frame `{type: "agent.snapshot", value: {"epoch": E, "seq": H, "sessions": [{"session": code, "event": NormalizedEvent}]}}` — every session code on this host with an agent (frame projections, the legacy `agent_events` sessions as `sendSnapshot` covers today, and **non-tmux CC sessions** — review #3: `nontmux.go` keeps no state today, so b-2 adds `m.nonTmuxLast map[code]NormalizedEvent` written by the emit slot, removed on a clear frame or after 2 h without an event), each `event` stamped `epoch E, seq H, snapshot: true`, `raw_event_name: "replay"`. Codes not listed have no agent: the SPA clears them (U1-3). An empty host sends `"sessions": []`. `"seq": 0` is spelled out (tested).
  - any other subscriber gets today's per-code replay `hook` frames, unchanged except that each carries `epoch E, seq H, snapshot: true` (old SPA and iOS ignore the new keys).
- `NormalizedEvent` gains `Snapshot bool json:"snapshot,omitempty"`.
- `sendSnapshot` moves under the slot; `replayFromDB` (startup, no subscribers) does not.

Tests: `TestSnapshot_V2IsOneCompleteFrame` (two live sessions + one cleared → two entries, all `seq == H`), `TestSnapshot_V2EmptyHostSpellsEmptyListAndSeqZero`, `TestSnapshot_V2IncludesLegacySessions`, `TestSnapshot_LegacyFramesCarryEpochSeqSnapshot`, `TestSnapshot_OrderedAgainstLiveEmits` (an emit blocked inside the slot while a subscriber connects: the snapshot waits; frames before it ≤ H, after it > H — deterministic, not timing), `TestFeaturesOf_AgentAndNex`, `TestSnapshot_V2IncludesNonTmuxSessions` (and drops one after its clear), `TestStrict_AgentV2DroppedFrameEndsConnection` (a full buffer on the snapshot and on a last `hook` frame both end the subscriber; a legacy subscriber keeps best effort).
Mutation gates (added): `agent.v2` not strict → `DroppedFrameEndsConnection` red; non-tmux map not consulted → `IncludesNonTmuxSessions` red.
Mutation gates: snapshot built outside the slot → `OrderedAgainstLiveEmits` red; cleared session included → `V2IsOneCompleteFrame` red.

Spec §7 "Aggregation and wire" is updated in this PR with the frame shape and the SPA rules U1-3 implements: replace the host's agent state with the snapshot; drop `hook` frames that arrive before it on a connection, and frames with `(epoch, seq) ≤` the last applied; a different epoch is accepted only through a snapshot; a seq gap (`seq ≠ last + 1`) forces a resync (reconnect).

---

## Order with the other lines (SPA)

- No U1-2 PR touches the SPA. Every new field is additive in `value`; `agent.snapshot` is sent only to `agent=v2` subscribers, which no client asks for until U1-3. Old SPA and iOS keep working through every deploy.
- `spa/src/hooks/useMultiHostEventWs.ts`: D-line #1866 PR2b (nex hello / delta routing, `?nex=v1`) lands first; U1-3 rebases on it and adds `agent=v2` to the same query string and its `agent.snapshot` / seq handling. The two families never share a cursor.
- `featuresOf` (b-3) is the one daemon file both lines touch; b-3 rebases on whatever #1866 has merged by then.
- The mod (a-1): member β (lead/team line) does not plan mod changes; `register.js` is not touched by U1-2.

## Acceptance and deploy (after U1-2b-3 merges; the coordinator deploys)

1. Coordinator: bump, then build and swap `bin/pdx` (rm → cp → mv), restart; **`pdx setup --agent cc`** (the mod changed in a-1) with `~/.claude/settings.json` backed up and compared as in U1-1; `pdx.json` still has `mod_socket`.
2. Script: the U1-1b acceptance script (`accept-u1-1b.sh`, lead scratchpad) rewritten as `accept-u1-2.sh`: a throwaway tmux session with an interactive Haiku `claude` **in default permission mode** (no `--dangerously-skip-permissions`), plus a Node 24 `WebSocket` client on `/ws/host-events?agent=v2` (ticket as the SPA gets it; token read into a variable, never printed) recording the `agent.snapshot` and every `hook` frame for the session's code.
3. Checks, each against the recorded frames:
   - snapshot: one `agent.snapshot` frame first, `seq` = high-water, the throwaway code present once it runs;
   - a Bash command that needs permission: `running (source mod)` → `waiting` → answer `1` → `running` → `idle`; seqs contiguous;
   - AskUserQuestion → `waiting` → answer → `idle`;
   - Esc during `sleep 30` → `idle` without a `Stop` hook (mod `turn.complete` aborted, or the heartbeat repair within 10 s);
   - background: a `Monitor` tool call → `background: "monitor"`; a `CronCreate` / ScheduleWakeup → `"schedule"`; cleared at exit;
   - two panes in one tmux session (split-window, a second `claude` running `sleep 60`): first pane waiting on a permission → the session shows `waiting` while the second runs;
   - daemon restart mid-session: within one heartbeat `/api/mod/streams` shows the stream with its `cwd` and `interactive: true`; the reconnected client's first frame is a snapshot with the new epoch; the light matches the screen;
   - no-mod fallback: the same session started with the plugin disabled (`CLAUDE_CODE_PLUGIN_DIRS` unset for that process) → frames carry `source: "hook"`; Esc during a tool → `idle` (is_interrupt).
4. Kill the throwaway sessions; record results in the PR and the kickoff memory.

## Coordinator rulings (2026-10-08, purdex-1f)

1. One `hook` counter per daemon process, field name `seq`, epoch = `boot_id` in `value`, always present. Spec §7 rewritten in the plan PR, including that `hook (epoch, seq)` and `nex.* (epoch, bseq)` are separate counters.
2. Snapshot = one opt-in `agent.snapshot` frame.
3. Multi-pane sessions: dots / agent type / model from the highest-priority pane only — a known limitation (spec §7).
4. #1866 is closed (no further `featuresOf` changes). Member β's PU-1c adds `EventsBroadcaster.BroadcastStrict` to `internal/core/events.go` (strict for every subscriber: a frame that does not fit removes it, so it reconnects for a snapshot). **b-3 rebases on a main that contains PU-1c** and makes `agent.v2` strictness use, or match, that mechanism rather than adding a second one.
- ~~Known limitation accepted: waiting until the approved tool ends~~ — superseded the same evening: the user wants approval to leave yellow at once. After M-U1-6 the coordinator chose the mod signal: `tool.approved` from the `ui.render` `ToolUse` observer (a-1), handled by the state machine in a-3a; no answer-key watching, no `question_asks`, no answered marks; the hook fallback keeps its old rule.
- Every PR must be safe to deploy on its own (the coordinator may deploy main for other lines in between); a-1 changes the mod, so its deploy runs `pdx setup --agent cc`.

## Decisions for the coordinator (as asked; answered above)

1. **`hook` seq scope and name** (review #2): spec §7 says "per session code, monotonic". The plan uses **one counter for all `hook` frames** (still monotonic per code, which is what §7's ordering rule needs, and it lets one snapshot carry one high-water `H`); gap checks are per connection, not per code. b-2 rewrites §7 to say so. Name: keep `seq` or rename to `bseq` (same semantics as #1866). Recommendation: `seq` — the families never share a cursor, and spec §8 (U1-7 list summary `status, epoch, seq, …`) already uses it.
2. **Snapshot shape**: one opt-in `agent.snapshot` frame (this plan) vs. per-code frames plus an end marker. Recommendation: the single frame — atomic, needs no end marker, and an empty host is explicit.
3. **Representative pane** (b-1): dots / agent type / model come from the highest-priority pane only. OK for U1, or merge dots across panes?

## Plan review fold-in (codex `task-muzmukvc-0c6rdu`, 2026-10-08)

| # | Sev / conf | Finding | Disposition |
|---|---|---|---|
| 1 | critical 0.99 | `agent=v2` non-strict: a dropped snapshot or last `hook` frame is undetectable | Accepted: `agent.v2` subscribers are strict (b-3) + test and gate |
| 2 | important 1.00 | global seq vs spec "per session code" | Accepted as a spec change for the coordinator (decision 1); one counter is monotonic per code and gives one snapshot high-water; b-2 rewrites §7 |
| 3 | important 0.98 | non-tmux sessions missing from the snapshot | Accepted: `nonTmuxLast` map in the emit slot, in the snapshot (b-2 / b-3) |
| 4 | important 0.99 | empty `source` on non-tmux frames | Accepted: `source` always `"mod"` or `"hook"` (a-3) |
| 5 | important 0.96 | StopFailure OCC retry would write a stale status | Accepted: status-preserving retry variant + conflict test (a-4) |
| 6 | important 0.91 | `modBySID` dangling after eviction | Accepted: re-point / delete + dirty + test (a-3) |
| 7 | important 0.89 | change-only digest misses a representative change | Accepted: digest includes representative frame id, agent_type, model (a-3) |
| 8 | important 0.90 | lock order not total; caller-locked projection reads | Accepted: total order emitter → `m.mu` → `modMu` (leaf), call-site audit in the PR body, concurrent deadline test (b-2) |
| 9 | important 0.94 | probe vs overlay interaction untested | Accepted: probe transitions skipped while the representative is mod-live + three tests (a-3) |
| 10 | minor 1.00 | "Six PRs"; release gate needs a-1 | Fixed: seven; release gate = all seven |

Size after fold-in: a-3 grows by ~120 lines (probe gate, eviction, digest tests) and is expected to take the planned split (A3-1+A3-3 / A3-2); b-2 grows by ~80 (non-tmux map, lock test).
