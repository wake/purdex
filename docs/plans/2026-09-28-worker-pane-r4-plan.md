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
- **`result.total_cost_usd` is session-cumulative and survives `--resume`**
  (Nexen spec §1.2, measured: 0.0528 → 0.0643 → 0.1126 across two results of
  one `-p` and one `--resume`; `modelUsage.inputTokens` 18 → 36 → 46). The
  same holds for `modelUsage` token counts.

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
- **Cost bug**: `lib/nex/cost-summary.ts` sums `total_cost_usd` (:177) and
  `modelUsage` tokens over every top-level `result`. Given the cumulative
  fact above, a worker with N turns shows roughly the sum of N running
  totals — over-counted from the second turn on. `ExecutionView.tsx:143`
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
| **R4-B** | cost: fix the cumulative over-count; header / panel read the rollup when present; hand-over hover (Q3) | ~200 | R1 + attack + critic (money) |
| **R4-C** | dock running tasks (cold start, reconnect re-read, inspect) + subagent close-out line (Q2) | ~300 | R1 |
| **R4-D** | sidebar and Host › Nex rollups (cost, turns, last tool, running, activity) | ~200 | R1 |

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
    start∪end shape (all end fields optional/null while running).
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
  arrived live. Otherwise the snapshot row wins, and a row that state holds as
  running but a `state=running` snapshot omits is dropped (it ended and we
  missed the `task_end`). Closed rows in state that the snapshot omits are
  kept (a running-only snapshot never lists them).
- Tests: the fixture `lib/nex/__fixtures__/cc-2.1.275-sleep6.jsonl` gets a
  sibling with the two new kinds interleaved (hand-written from the contract
  payloads); assert `messages.length` and turn count are identical with and
  without them, and that `tasks` ends with one completed row. A
  replay-then-snapshot test for the #83 correction.

---

## R4-B — cost

### T2.1 `costSummary`: cumulative, not additive (TDD)

- Per top-level `result`, `total_cost_usd` is the running total of its
  session. Walk results in seq order keeping `prev` (the last cumulative
  total seen): a turn's own spend = `cur − prev` when `cur ≥ prev`, else
  (`cur < prev`: a new session started — `session_expired`, a fresh
  delegate) the turn starts a new chain and its spend = `cur`. `totalUsd` =
  Σ own spends (= Σ chain maxima).
- Tokens: same delta rule per model in `modelUsage` (key by model name; a
  model new to the chain → its value; a drop in any counter → new chain for
  the whole frame, matching the cost rule). The `usage` fallback (older CC,
  F5) is per-message, not cumulative — keep it additive, and document the
  distinction in the header comment next to F4–F6.
- Replace F4's wording ("`totalUsd` is Σ total_cost_usd") with the measured
  cumulative fact and its source (Nexen spec §1.2).
- Tests: the three-result sequence from Nexen §1.2 (0.0528 / 0.0643 /
  0.1126 → turns 0.0528 / 0.0115 / 0.0483, total 0.1126); a chain reset;
  a turn with no cost (`0`) between two costed turns keeps `prev`; the
  existing F6 subagent-result exclusion and overflow tests still pass; the
  existing fixtures `cost-subagent-06GBGXTW.json` etc. are re-asserted with
  corrected expected totals (state the old and new number in the commit).

### T2.2 Header and panel prefer the rollup; hand-over note (Q3)

- `ExecutionHeader`: the number shown = `summary.cost_usd` when it is a
  finite number (rollup daemon), else `cost.totalUsd` from T2.1. The per-turn
  breakdown in the tooltip / `CostPanel` stays from `costSummary`.
- When the two disagree by more than a cent, the panel shows the rollup as
  the total and notes "per-turn figures are from this pane's history" — no
  attempt to reconcile.
- **Q3**: when `summary.resume_session_id` is set (turn 1 resumed a
  conversation that existed before this worker — a hand-over), the tooltip
  and panel add `execution.cost.includesPriorHistory` ("Includes spend from
  before the hand-over" / 「含接續前的費用」). Not shown otherwise, because
  then there is no prior spend.
- Tests: header shows rollup when present / falls back when absent or null;
  note appears only with `resume_session_id`; locale parity.

---

## R4-C — dock and subagents

### T3.1 Task table in the pane (TDD)

- `ExecutionView` (or a `useWorkerTasks(hostId, id)` hook): when
  `selectWorkerRollup(hostId)` is non-null, fetch `/tasks?state=running`
  **before** the scoped SSE subscribes, and subscribe from its `cursor`
  (check how the current SSE hook passes `Last-Event-ID`; if it always
  resumes from history's last seq, the snapshot's rows are still applied
  first and duplicates are idempotent — record which in the PR).
- **After every SSE reconnect** (the existing `sse` state leaving
  `reconnecting` for `open`), re-fetch `/tasks?state=running` and apply it as
  the snapshot (#83).
- No rollup capability → no fetch, `tasks` stays empty, dock as today.
- Tests: fetch order (snapshot before subscribe), reconnect triggers exactly
  one re-fetch, old daemon makes no request.

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
- Tests: each status, usage partially present, no task.

---

## R4-D — lists

### T4.1 Sidebar row

- `ExecutionRowCompact`, only when the row carries rollup fields:
  - after the age: the cost (`$0.11`, `formatUsd`) when `cost_usd` is a
    number; nothing when `null`;
  - a small running badge (`Terminal` icon + count) when `running_tasks >
    0`;
  - the state dot's tooltip = the activity (`normalizePhase`):
    `tool` → "Running {{tool}}", `model` → "Thinking", `starting` →
    "Starting", `idle` → "Idle", `queued` → "Queued", `ended` → the
    existing ended label.
  - Q3's hand-over note on the cost's `title` when `resume_session_id`.
- Tests: with / without rollup fields, `cost_usd: null`, running 0 / 2,
  unknown phase reads as Thinking.

### T4.2 Host › Nex table

- `NexExecutionRow`: columns `cost`, `turns`, `last tool`, `running` after
  `observers`; `—` when the field is absent (old daemon). Update the table
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
