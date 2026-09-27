# Plan — worker pane R4: running tasks, subagent close-out, list rollups

Spec: `docs/specs/2026-09-20-worker-pane-views-spec.md` v1.0 — §4.5
(subagent cost / end time), §4.6 (the dock with running shells), §7 (the ❌
rows), §9 (wire asks A1–A4), §11 R4. R1 shipped as alpha.448–454, R2 as
455–456, R3 as 461–463.

Wire: Nexen **v0.13.1** (`cae157e`, tag `v0.13.1`, deployed 2026-09-27 by
nexen-a2). Contract: nexen repo `docs/contract/capability-matrix.md` §0
`worker_rollup`, §3 "`task_start`／`task_end` 的 payload ＋ dock／列表 rollup",
§3.5 (site-stream stripping); `docs/contract/consumer-guide.md` §9 "worker
狀態：dock、activity、列表 rollup"; spec `docs/specs/2026-09-27-worker-rollup-spec.md`.

Anchors measured on `a657859b` (alpha.463) in worktree `pane-r4`. Paths are
relative to the worktree root; SPA paths are under `spa/`.

## User decisions for R4 (2026-09-28, do not reopen)

| # | decision |
|---|---|
| Q1 | Bumping the pin deletes `~/.config/pdx/nex/nex.db` **outright, no backup**. |
| Q2 | Subagents show **no cost field** at all (Nexen's `task_end.cost_usd` is always `null`). Show tokens / tool uses / duration only; no data → no placeholder. |
| Q3 | The worker's cost (now "this conversation's cumulative cost") is shown as a plain number; its **hover says it includes spend from before the hand-over**. |

Earlier decisions still bind: room is the default and chat stays manual; the
dock lives in the pane and only in room (spec §5, R2 critic F9); "worker" is
the user-facing word, `execution` stays in code.

## Facts this plan relies on (measured)

Wire (Nexen contract, v0.13.1):

- Feature detect = presence of `capabilities.worker_rollup`
  (`{task_kinds, task_statuses, activity_phases, subagent_cost:false, …}`);
  **never compare versions**. Absent = old daemon: no task events, no rollup
  fields. air26's daemon may stay old for a while — every R4 surface must
  render as today without it.
- `task_start` / `task_end` are durable Nexen kinds (not `execution.*`
  prefixed). `task_start`: `task_id, turn_id, kind (shell|subagent|other),
  task_type, tool_use_id|null, parent_tool_use_id|null, description,
  command?, subagent_type?, backgrounded, started_at` (daemon observation
  time). `task_end`: `task_id, turn_id, kind, tool_use_id|null, status
  (completed|failed|killed|lost), provider_status|null, ended_at, closed_by
  (provider|daemon), summary?, usage?{total_tokens,tool_uses,duration_ms},
  cost_usd:null`. One of each per `task_id`; `task_end` is the complete final
  state (replace, don't merge).
- `GET /v1/executions/{id}/tasks?state=running|all` → `{items, cursor}`;
  items are the merged start∪end shape. Cold start = snapshot first, then SSE
  from `cursor`; duplicates possible (idempotent by `task_id`), gaps not —
  except #83: **after every SSE reconnect, re-read `/tasks`** to correct.
- Summary rollup (list and single GET): `cost_usd` (number|null — the resume
  chain's cumulative cost; includes pre-hand-over history when turn 1 resumed
  an outside session), `last_tool?{name,tool_use_id,at}`, `running_tasks`
  (always present), `activity{phase, tool?{name,tool_use_id,since}, open_tools,
  since?}`, `turn_count` (always on list rows; omitempty on single GET).
  `activity.phase` is an **open set**: unknown → treat as `model`.
- The site-level stream strips `task_start.command/description` and
  `task_end.summary`; the scoped stream (`?execution_id=`) and history keep
  them. The pane uses the scoped stream.
- **`result.total_cost_usd` is sometimes cumulative, sometimes not.** Nexen
  spec §1.2 measured it cumulative within one `-p` and across `--resume`
  (0.0528 → 0.0643 → 0.1126; `modelUsage.inputTokens` 18 → 36 → 46). But
  Purdex's own recording `spa/src/lib/nex/__fixtures__/cost-turns-06GB2ZFD.json`
  (CC 2.1.27x, one session `c191a5a0`, 12 top-level results) goes 0.0300 /
  0.0095 / 0.0106 / 0.0091 / 0.0105 / 0 / 0.0294 / 0 / 0.0208 / 0.0057 /
  0.0603 / 0.0717 — independent per resumed turn; only the last two (seq
  958 → 974, sonnet `outputTokens` 598 → 701, Δ 103 = 974's
  `usage.output_tokens`) accumulate. `modelUsage[*].costUSD` and
  `duration_api_ms` follow the same pattern; `num_turns` and `duration_ms`
  do not. So neither "always add" nor "always take the latest" is right; a
  frame must prove it continues the previous one (T2.1). Reported to
  nexen-a2 on 2026-09-28, because Nexen's `chainCost` ("latest per chain")
  under-counts the fixture's pattern (≈ 0.07 instead of ≈ 0.19).

Purdex:

- `go.mod:11` pins `lab.protype.tw/wake/nexen v0.12.0`. Engine wiring
  `internal/module/nex/module.go:46-58` (`realAssemble`) against
  `engine_iface.go`; `/api/nex/*` is a catch-all `StripPrefix` proxy
  (`module.go:306-316`), so `/tasks` needs no Go route. `nex.db` =
  `<pdx DataDir>/nex/nex.db` (`build_config.go:81`). Nexen refuses a DB whose
  `PRAGMA user_version` differs; Purdex then soft-fails the module
  (`module.go:215` → `softFail`) and `/api/nex/*` answers 503 until the file
  is deleted.
- Reducer `spa/src/lib/nex/event-reducer.ts`: `isLifecycleKind` (:85,
  `execution.*`/`lease.*`), `isToolEventKind` (:94); **every other kind is
  appended to `messages`** (:207-208). So on a v0.13 daemon `task_start` /
  `task_end` would land in `messages` today — rendered as null by
  `MessageRow.tsx:210`, but counted by turn grouping, chat grouping and the
  search index. This is a live bug the moment the daemon is upgraded, which
  is why T1.3 ships with the pin.
- Types `spa/src/lib/nex/types.ts:13-43` (`ExecutionSummary`: no rollup
  fields; `resume_session_id?` present), `NexCapabilities` :65-104
  (`tool_events?` is the feature-detect precedent). List sanitising
  `lib/nex/validate-executions.ts:33-58`. API `lib/nex/nex-api.ts`
  (`nexFetch` :26, `getExecution` :118; no tasks call). Capabilities cached in
  `stores/useNexHostStore.ts` (`byHost[hostId].capabilities`; selector
  pattern `selectHandoffReady` :38-45).
- **Cost bug**: `lib/nex/cost-summary.ts` sums `total_cost_usd` (:177),
  `byModel.costUsd` (:183-189) and `modelUsage` tokens over every top-level
  `result`. Wherever results do accumulate (several results in one process,
  or a CC build that carries totals across `--resume`), that double-counts. `ExecutionView.tsx:143`
  feeds it to `ExecutionHeader` (tooltip `execution.cost.summary`, :170-196)
  and `CostPanel`.
- Dock `components/room/WorkerDock.tsx` (props `sse, observers, lease?,
  isMine`; :9 notes "R4 adds a `shells` prop"), mounted at
  `ExecutionView.tsx:314`, room only.
- Subagents: `components/room/SubagentBlock.tsx` (props `name, toolCount,
  foldKey, depth, renderChildren`; folded text `room.subagent.summary`
  "`{{name}} · {{tools}} tools`"), built in `MessageRow.tsx:77-95`
  (`OperationAt`, tool count `countToolCalls` :31). The Task call's
  `tool_use_id` is the key `task_start.tool_use_id` points at.
- Lists: sidebar `components/executions/ExecutionRowCompact.tsx` (dot, brief
  first line, ↩ when `origin` is a same-host tmux session, relative age);
  Host › Nex table `components/hosts/nex/NexExecutionRow.tsx:58-118`
  (+ snapshot in `NexExecutionsTable.test.tsx`). List refresh is a debounced
  refetch on any site-stream frame (`execution-list-effects.ts:165-186`), so
  rollup fields update without new plumbing.
- Locales `spa/src/locales/{en,zh-TW}.json` (parity test
  `locale-completeness.test.ts`); plurals `_one` / `_other`.

## Not in R4

- **Killing a task from the dock** (spec §4.6 "kill/inspect action per
  row"): Nexen has no task-kill verb. The dock rows are read-only; "inspect"
  = scroll to the Bash / Task call that started it (T3.2). Kill goes to a
  follow-up issue for a Nexen ask.
- A pane-header status line from `activity`: the pane already shows what the
  worker is doing live (running tool rows, typing). `activity` is used where
  no transcript is loaded — the lists (R4-D).
- Per-subagent cost (Q2).

## Working rules

As R1–R3: TDD, one commit per task with `git commit --only`, subagents prefix
every Bash with `cd <worktree>/spa && ` (or `cd <worktree> && ` for Go), type
check `npx tsc --noEmit -p tsconfig.app.json`, `pnpm run lint`, Go
`go test ./internal/module/nex/...` and `go vet ./...`, both locale files
together, Phosphor icons, **no kill-type commands** (no `pkill` / `kill` /
`killall` of any kind). Only one worktree has subagents at a time.

## PR split

| PR | content | est. (non-test) | review |
|---|---|---|---|
| **R4-A** | pin v0.13.1; wire types, capability selector, reducer routes task events out of `messages` into a task table, `/tasks` fetch | ~300 (Go ~10) | R1 + attack + critic (wire) |
| **R4-B** | cost: continuation needs token evidence (fixes the double count); hand-over hover (Q3) | ~200 | R1 + attack + critic (money) |
| **R4-C** | dock running tasks (cold start, reconnect re-read, inspect) + subagent close-out line (Q2) | ~300 | R1 |
| **R4-D** | sidebar and Host › Nex rollups (cost, turns, last tool, running, activity) | ~200 | R1 |

Plan review (Claude reviewer, standing in for codex until 2026-09-30): 7
findings, all applied — #1 (critical) "a drop means a new chain" under-counts
the recorded fixture; continuation now needs token evidence (T2.1), and the
counter-example went to nexen-a2; #2 `byModel.costUsd` / `duration_api_ms`
are cumulative too, and two more test files change (T2.1); #3 subscribing
from the `/tasks` cursor would break the reducer's single high-water mark;
cold start replays history, the snapshot only corrects after (re)open
(T3.1); #4 the snapshot merge dropped tasks started while it was in flight —
`startSeq` guard (T1.3); #5 subagents without children (T3.3); #6 the Q3
note needs turn 1's resume to have succeeded (T2.2); #7 header vs rollup lag
— moot, the header no longer reads the rollup (T2.2).

Order A → B → C → D; B, C, D only depend on A. Deploy after R4-A merges (see
§Deploy); B–D are SPA-only.

---

## R4-A — wire

### T1.1 Go: bump the pin

- `go get lab.protype.tw/wake/nexen@v0.13.1 && go mod tidy`.
- Compile against `engine_iface.go` / `realAssemble`; adapt only what 0.13
  renamed (expected: nothing — additive release). `go build ./...`,
  `go test ./internal/module/nex/... ./cmd/pdx/...`, `go vet ./...`.
- If a test fixture DB or golden in `internal/module/nex` carries schema v5,
  regenerate it; do not add any migration.
- No new route (catch-all proxy).

### T1.2 SPA types, capability, API (TDD)

- `types.ts`:
  - `ExecutionSummary` += `cost_usd?: number | null`, `last_tool?:
    {name: string; tool_use_id: string; at: number}`, `running_tasks?:
    number`, `activity?: WorkerActivity`; comment on `turn_count` updated
    (list rows now carry it on a rollup daemon).
  - `WorkerActivity { phase: string; tool?: {name; tool_use_id; since};
    open_tools: number; since?: number }`.
  - `TaskKind = 'shell' | 'subagent' | 'other'`, `TaskStatus = 'running' |
    'completed' | 'failed' | 'killed' | 'lost'`, `WorkerTask` = the merged
    start∪end shape (all end fields optional/null while running) plus the
    client-only `startSeq: number` (T1.3).
  - `NexCapabilities.worker_rollup?: { task_kinds: string[]; task_statuses:
    string[]; activity_phases: string[]; subagent_cost: boolean }`.
- `validate-executions.ts`: coerce the four rollup fields — non-finite or
  negative `cost_usd` → `null`; `running_tasks` / `turn_count` non-negative
  integers else dropped; `last_tool` / `activity` dropped unless their
  required keys have the right types.
- `lib/nex/activity.ts` (new): `normalizePhase(phase) → 'queued' | 'starting'
  | 'model' | 'tool' | 'idle' | 'ended'` — **unknown → `'model'`**.
- `lib/nex/tasks.ts` (new): `parseTask(payload|item) → WorkerTask | null`
  (guards every field; unknown `kind` → `'other'`; unknown `status` on an
  end → `'failed'`, mirroring the server's fail-closed rule),
  `applyTaskEvent(table, kind, payload)` — `task_start` upserts
  `status:'running'` **only if the id is not already closed** (a replayed
  start after its end must not reopen it), `task_end` replaces the row
  wholesale; `runningTasks(table)`.
- `nex-api.ts`: `fetchExecutionTasks(hostId, id, state = 'running')` →
  `{items: WorkerTask[], cursor: number}` via `nexFetch`, items through
  `parseTask`, invalid ones dropped.
- `stores/useNexHostStore.ts`: `selectWorkerRollup(hostId)` (object or
  `null`), mirroring `selectHandoffReady`.
- Tests: `tasks.test.ts` (start then end; end then replayed start stays
  closed; duplicate end idempotent; unknown kind/status; missing
  `tool_use_id` is `null`; `cost_usd` ignored), `activity.test.ts`
  (unknown phase → model, `awaiting_input` → model), `validate-executions`
  cases for each field, `nex-api` tasks call shape.

### T1.3 Reducer: task events are not messages (TDD)

- `event-reducer.ts`: `isTaskEventKind(kind)` = `task_start | task_end`;
  handled next to `isToolEventKind` — never appended to `messages`, never a
  turn boundary, never touches the partial; they update `state.tasks`
  (`Record<task_id, WorkerTask>`) through `applyTaskEvent`.
- `applySnapshot(state, items)` (reducer action or exported helper) merges a
  `/tasks` response. The snapshot is the authority for *running* rows (#83
  guidance), but closure is final (each `task_id` ends exactly once): a row
  already closed in state is never reopened by a snapshot that still says
  running — that snapshot was read before a `task_end` that has since
  arrived live. Otherwise the snapshot row wins. Each row records
  `startSeq` (the `ev.seq` of its `task_start`; rows first seen in a
  snapshot get `startSeq = snapshot.cursor`). A row state holds as running
  but the snapshot omits is dropped **only if `startSeq ≤ snapshot.cursor`**
  (it ended and we missed the `task_end`); a newer row started live while
  the snapshot was in flight is kept (Nexen reads `cursor` before the rows,
  `api/tasks.go:138-153`). Closed rows the snapshot omits are kept.
- Tests: the fixture `lib/nex/__fixtures__/cc-2.1.275-sleep6.jsonl` gets a
  sibling with the two new kinds interleaved (hand-written from the contract
  payloads); assert `messages.length` and turn count are identical with and
  without them, and that `tasks` ends with one completed row. A
  replay-then-snapshot test for the #83 correction.

---

## R4-B — cost

### T2.1 `costSummary`: a frame must prove it continues (TDD)

- **Shared rule with Nexen** (agreed with nexen-a2 2026-09-28; Nexen v0.13.2
  implements the same in `chainCost` so the list and the pane show the same
  number — keep the wording in sync if either side changes). Walk top-level
  `result` frames in seq order:
  1. A frame without a usable `modelUsage` never becomes `prev`; it
     contributes its own `total_cost_usd` if finite and > 0 (older CC that
     only gives `usage`), else nothing. A frame **with** `modelUsage` but a
     missing / non-finite `total_cost_usd` contributes nothing and does not
     become `prev` either.
  2. The first frame with `modelUsage` is independent.
  3. After that, a frame is **cumulative** ⇔ for every model in
     `prev.modelUsage`, this frame's `outputTokens` ≥ prev's (a model
     missing here counts as 0 ⇒ not cumulative), **and** Σ over this
     frame's models of (this `outputTokens` − prev's, prev missing = 0) ≥
     this frame's `usage.output_tokens`.
  4. Cumulative → cost contribution = this `total_cost_usd` − prev's; if
     that is negative, treat the frame as independent. Otherwise the
     contribution = this frame's `total_cost_usd`.
  5. `totalUsd` = Σ contributions (non-finite → the existing saturating
     rules). No session / `resumed_from` logic — evidence only.
  The frame then becomes `prev`. For the panel's breakdown, a cumulative
  frame's per-model cost and tokens and `duration_api_ms` are the deltas
  against `prev`, each clamped at 0; `num_turns` and `duration_ms` are
  always per-frame.
- Rewrite the F4 note in the header comment: the measured fact is now "some
  results are running totals" with both sources (Nexen spec §1.2; fixture
  06GB2ZFD seq 958 → 974), and why continuation needs token evidence.
- Tests: Nexen §1.2's three-frame sequence (0.0528 / 0.0643 / 0.1126,
  inputTokens 18 / 36 / 46 → turns 0.0528 / 0.0115 / 0.0483, total 0.1126);
  fixture `cost-turns-06GB2ZFD.json` — ten independent frames plus seq
  974 as a continuation of 958 (turn 974 = 0.0114, total drops by 0.0603;
  write the old and new totals in the test and the commit); a model
  disappearing breaks continuation (seq 8 → 17: haiku gone); a zero-cost
  frame in between keeps `prev`; F6 subagent-result exclusion and the
  overflow tests unchanged.
- Expected values also change in `CostPanel.test.tsx` and
  `ExecutionHeader.test.tsx` (they read the same fixtures) — update them in
  the same commit.

### T2.2 Hand-over note (Q3)

- The header and panel keep reading `costSummary` (T2.1): the pane has the
  full history, updates on every `result` without waiting for a summary
  refetch, and is not exposed to the `chainCost` question above. The
  rollup `cost_usd` is used only where no history is loaded (R4-D).
- **Q3**: when `summary.resume_session_id` is set (only `handoff.go:236`
  sets it; Nexen uses it on turn 1 only, `launch.go:307-320`) **and** turn 1
  actually resumed — i.e. it did not end `rejected` / `session_expired`
  (`launch.go:321-324`; the implementer confirms which summary field
  carries that, `reject_reason` or `last_turn_reason`, and writes the answer
  in the PR) — the tooltip and panel add
  `execution.cost.includesPriorHistory` ("Includes spend from before the
  hand-over" / 「含接續前的費用」). Not shown otherwise.
- Tests: note with a successful hand-over; absent without
  `resume_session_id`; absent when turn 1's resume was rejected; locale
  parity.

---

## R4-C — dock and subagents

### T3.1 Task table in the pane (TDD)

- **Cold start needs no snapshot**: `useExecutionSubscription.ts:167-192`
  already replays history from 0 up to `obs.cursor`, then opens SSE with
  `Last-Event-ID = store.lastSeq` (the reducer's single high-water mark,
  `event-reducer.ts:196` — do not change it). `task_start` / `task_end` are
  durable, so the replay rebuilds `tasks` completely.
- **#83 correction**: in `useExecutionSubscription`'s `onStatus`, next to
  `refetchSummary` (:228), re-fetch `/tasks?state=running` and apply it as
  the snapshot (T1.3) on the first `open` and every `reconnecting → open`,
  and when a kicked pane resumes on a new stream. Only when
  `selectWorkerRollup(hostId)` is non-null; otherwise no request and `tasks`
  stays empty (dock as today). Not in `ExecutionView`.
- Tests: first open fetches once; each reconnect fetches once; old daemon
  makes no request; a snapshot that arrives after a live `task_end` does not
  reopen the task.

### T3.2 Dock shows running tasks

- `WorkerDock` gets `tasks: WorkerTask[]` (running only, oldest first).
  Collapsed row (spec §4.6):
  `▸ 2 running  ● pnpm dev (4m)  ● tail -f log (2m)` — label = `command`
  for shells, `description` otherwise, first line, truncated; elapsed from
  `started_at` (a 30 s tick while any are shown). The existing sse /
  observers / lease facts follow after the tasks.
  Expanded: one row per task — kind icon (Phosphor `Terminal` for shell,
  `Robot` for subagent, `Circle` otherwise), full label, elapsed, and a
  button that scrolls the transcript to its `tool_use_id` (reuse R3's
  `data-search-unit` / fold `expand` path to reveal it inside a fold). No
  `tool_use_id` → no button.
- When there are no running tasks the dock looks exactly as today.
- Tests: collapsed text with 0 / 1 / 3 tasks, shell vs subagent label,
  elapsed formatting, inspect button calls the scroll handler with the id,
  and is absent without an id.

### T3.3 Subagent close-out (Q2)

- `SubagentBlock` gets an optional `task?: WorkerTask` looked up by the Task
  call's `tool_use_id` (`kind === 'subagent'`).
  - running → the summary adds elapsed (`· 12s`, ticking);
  - `completed` → `· 26k tokens · 8 tools · 12s` from `usage` (each part
    only when present — **no cost, no placeholder**; Q2);
  - `failed` → same plus a red `failed`; `killed` → neutral `stopped`;
    `lost` → neutral `interrupted` (contract: lost is "unknown", not a
    failure — never red).
- Without a task row (old daemon, or not found) the block is exactly as
  today (tool count from `countToolCalls`).
- `SubagentBlock` is only built when the Task call has children
  (`MessageRow.tsx:79-80`). A background subagent, or one whose frames have
  not arrived, has none — then the same status / usage suffix goes on the
  Agent/Task call's own operation header instead.
- Tests: each status, usage partially present, no task, a subagent task
  with no children.

---

## R4-D — lists

### T4.1 Sidebar row

- `ExecutionRowCompact`, only when the row carries rollup fields:
  - after the age: the cost (`$0.11`, `formatUsd`) when `cost_usd` is a
    number; nothing when `null`. **Shown only when
    `capabilities.worker_rollup.cost_basis === "result_evidence"`** (Nexen
    v0.13.2, the shared rule of T2.1). v0.13.1 says `"session_cumulative"`
    and under-counts, so its cost is hidden; any other / unknown value is
    hidden too. Bump the pin to ≥ v0.13.2 in R4-D if R4-A shipped 0.13.1;
  - a small running badge (`Terminal` icon + count) when `running_tasks >
    0`;
  - the state dot's tooltip = the activity (`normalizePhase`):
    `tool` → "Running {{tool}}", `model` → "Thinking", `starting` →
    "Starting", `idle` → "Idle", `queued` → "Queued", `ended` → the
    existing ended label.
  - Q3's hand-over note on the cost's `title` when `resume_session_id`.
- Tests: with / without rollup fields, `cost_usd: null`, running 0 / 2,
  unknown phase reads as Thinking.

### T4.1b Sanitise the single GET too (R4-A review A4)

- `getExecution` (`nex-api.ts:121`) passes the summary through unvalidated;
  list rows go through `validate-executions`. Export the rollup coercion and
  apply it to the single GET's rollup fields only (never reject the whole
  summary), before any view reads `cost_usd` / `running_tasks` /
  `activity` / `last_tool` from `st.summary`. Test with the same hostile
  values as the list tests.

### T4.2 Host › Nex table

- `NexExecutionRow`: columns `cost`, `turns`, `last tool`, `running` after
  `observers`; `—` when the field is absent (old daemon). The cost column
  follows T4.1's `cost_basis` gate (`—` otherwise). Update the table
  snapshot.
- Tests: row with and without rollup fields.

---

## Deploy (after R4-A merges)

mlab, in the main checkout after ff (reference `reference_pdx_daemon_runtime`):

```
make build BIN=bin/pdx.new
./bin/pdx stop
rm ~/.config/pdx/nex/nex.db ~/.config/pdx/nex/nex.db-shm ~/.config/pdx/nex/nex.db-wal   # Q1: no backup
mv bin/pdx.new bin/pdx        # new inode
env PDX_DEV_MODE=1 ./bin/pdx start
curl -s http://100.64.0.2:7860/api/health          # hash = merge commit
curl -s …/api/nex/v1/capabilities | jq '.worker_rollup'   # present
```

air26: same steps when its daemon is next updated; until then the SPA runs
its old-daemon path there. Tell the user which host is on which.

## Follow-ups to file

- Nexen ask: a task kill verb (dock kill action, spec §4.6).
- The CostPanel per-turn breakdown after a chain reset shows the new
  chain's first turn in full; if that confuses, mark chain starts.
