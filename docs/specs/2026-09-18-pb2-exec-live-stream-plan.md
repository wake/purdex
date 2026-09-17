# Plan — P-B2: exec mode live streaming

- Spec: `2026-09-18-pb2-exec-live-stream-spec.md` (v1.1). Rule ids (T1–T8,
  D1–D4, A1–A4, R1–R5) refer to it.
- Worktree: `.claude/worktrees/pb2-exec-stream`, branch
  `worktree-pb2-exec-stream`. Two PRs: P-B2.1 (tasks 1–5), P-B2.2 (tasks
  6–10). P-B2.2 starts after P-B2.1 merges; fast-forward the branch to
  `origin/main` and continue.
- Every task: subagent, TDD (failing test first, then the code), one commit
  with `git commit --only <files>`. Every Bash call prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb2-exec-stream/spa &&`.
  Verify per task: `npx vitest run <changed test files>`; before each PR:
  `npx vitest run && pnpm run lint && pnpm run build`.
- No daemon or Nexen change. `nex-sse.ts`, `sse-parser.ts`, `nex-api.ts`
  are not edited.

## Measured baseline (2026-09-18, this worktree at alpha.376)

- `spa/src/lib/nex/event-reducer.ts` 194 lines; `ExecutionState` fields at
  9–34; `frameToEvent` 73–95 returns `null` for `frame.id == null`;
  `applyDurableEvent` 110–194, non-lifecycle push at 114–118.
- `spa/src/stores/useExecutionStore.ts`: `patch()` helper returns `s`
  unchanged when the reducer returns the same object (no entry
  materialised); actions listed at 37–48.
- `spa/src/hooks/useExecutionSubscription.ts`: `onFrame` at 177–190,
  `openStream` closure at 172; `teardown` closes `sseRef`.
- `spa/src/components/ConversationMessages.tsx` 125 lines; props 16–25;
  scroll effect deps `[messages, scrollKey]`.
- `spa/src/components/ToolCallBlock.tsx` 61 lines, props `{tool, input}`;
  `MessageBubble.tsx` props `{role, content}`; `ThinkingBlock.tsx` props
  `{content}`; `ThinkingIndicator.tsx` props `{visible}`.
- `spa/src/lib/stream-ws.ts:16-23` `AssistantMessage.message` has no `id`.
- Existing tests: `event-reducer.test.ts` 22, `useExecutionStore.test.ts`
  11, `useExecutionSubscription.test.ts` 22 (`:71` asserts transient frames
  are dropped — flips in task 4), `ExecutionView.test.tsx` 19,
  `ConversationView.snapshot.test.tsx` (Stream-mode guard).
- Wire sample for the golden test: scratchpad `tp.jsonl` (29 frames, CC
  2.1.275). Task 1 copies it to
  `spa/src/lib/nex/__fixtures__/cc-2.1.275-sleep6.jsonl`.

## P-B2.1 — reducer, store, hook

### Task 1 — types + partial assembly reducer (T1–T8)

Files: `spa/src/lib/nex/event-reducer.ts`, `spa/src/lib/stream-ws.ts`
(add `id?: string` to `AssistantMessage.message`),
`spa/src/lib/nex/event-reducer.test.ts`, new fixture file.

- Add `PartialBlock`, `PartialAssembly`, `ToolActivity` types; add
  `partial: null`, `turnLive: false`, `tools: {}` to `ExecutionState` and
  `defaultExecutionState()`.
- Add `finalizedFor(s, messageId)` (count of `assistant` messages in
  `s.messages` with `message.id === messageId` and null
  `parent_tool_use_id`) and `export function applyTransientFrame(s, kind,
  payload)` implementing T1–T8. Payload access is defensive (`typeof`
  checks), never throws.
- Tests (each a named `it`): T1 subagent frame ignored; T2 `message_start`
  for a new id creates `{finalized: finalizedFor(...), blocks: {}}` and sets
  `turnLive`; T2 repeated `message_start` for the same id returns `s`; T2
  with one matching assistant already in `messages` seeds `finalized: 1`;
  T3 `content_block_start` for `tool_use` records id+name,
  `index < finalized` dropped; T4 delta creates a block when missing (type
  inferred), appends text/thinking/partial_json, `signature_delta` no-op,
  `index < finalized` dropped, delta before any `message_start` creates an
  assembly with `messageId: null, finalized: 0`; T5
  stop/message_delta/message_stop return `s` (same object); T6 snapshot
  assigns blocks, `finalized = finalizedFor` (not min index), no
  toolName on seeded blocks, malformed snapshot returns `s`; T7
  `lease.renewed` returns `s`; T8 `lastSeq`/`messages`/`summaryStale`/`sse`
  untouched.
- Mutation check in the task report: comment out the `index < finalized`
  drop in T4 → name the failing test.

### Task 2 — durable interactions (D1–D4) and tool activity (A1–A4)

Files: `event-reducer.ts`, `event-reducer.test.ts`.

- Extend `applyDurableEvent`: D1 on `assistant` with null
  `parent_tool_use_id` and matching (or null) message id → `delete
  blocks[finalized]`, `finalized += 1`; different id → `partial = null`;
  D2 `turnLive` on running/message_accepted; D3 clear partial + turnLive
  false + A3 abort on the listed kinds; D4 archived. A1/A2 on
  assistant/user blocks with null `parent_tool_use_id`, using
  `ev.created_at`.
- Tests: D1 finalization sequence for a two-block message (text, tool_use)
  interleaved as in F8/F9 — after both `assistant` frames `partial.blocks`
  is empty and `finalized == 2`; D1 different message id → `partial ==
  null`; **reconnect case A**: empty `messages`, snapshot `{blocks:[{index:1,
  text:'b'}]}`, replayed assistant 0 → `blocks[1]` still present,
  `finalized == 1`, a following delta for index 1 appends, assistant 1 →
  blocks empty, `finalized == 2`; **reconnect case B**: assistant 0 already
  in `messages`, same snapshot → `finalized` seeded 1, assistant 1 removes
  block 1; D3 each kind clears; seq idempotency still short-circuits before
  D-rules (replayed frame does not double-finalize); A1 first sighting
  wins; A1/A2 skip subagent frames; A2 error result → `'error'`; A3 aborts
  only `running` tools; A4 `created_at` 0 stored as 0.
- Golden test: replay the fixture through `applyTransientFrame` /
  `applyDurableEvent` in wire order (assign seq to durable frames in
  order, `created_at` = index×100) and assert `messages` has exactly the 5
  durable provider frames (2 assistant, 1 user, 1 result, 1
  rate_limit_event, plus 5 system), `partial == null`, `turnLive ==
  false`, the Bash tool `status == 'done'` with `endedAt > startedAt`.
- Mutation check: replace `delete blocks[finalized]` with "delete the
  lowest index" → reconnect case A must fail; replace `finalizedFor` with
  `0` in T6 → reconnect case B must fail. Name both in the task report.

### Task 3 — store action `applyTransient`

Files: `useExecutionStore.ts`, `useExecutionStore.test.ts`.

- `applyTransient(hostId, executionId, frames: {kind: string; payload: Record<string, unknown>}[])`
  folds with `applyTransientFrame` inside one `patch()`. A fold that
  returns the same object materialises nothing (existing `patch` rule).
- Tests: fold of three deltas produces one block with concatenated text;
  `lease.renewed`-only batch leaves the store without an entry;
  `clearExecution` drops the partial with the rest.

### Task 4 — hook: transient branch + rAF coalescing

Files: `useExecutionSubscription.ts`, `useExecutionSubscription.test.ts`.

- In `openStream`: a `pending: {kind, payload}[]` queue, a `generation`
  counter, and `flush(gen)` that returns without writing when `gen !==
  generation`, otherwise calls `applyTransient` once with the drained
  queue. `onFrame`: `frame.id == null` → parse (`JSON.parse`, failure →
  drop), push, `scheduleFlush()`; durable → `flush(generation)` first,
  then existing path. `scheduleFlush` uses `requestAnimationFrame` when
  defined, else `setTimeout(flush, 16)`, and stores the handle so it can be
  cancelled. `onStatus('connecting' | 'reconnecting')` → `generation += 1`,
  queue cleared, scheduled flush cancelled; `onStatus('closed')`, teardown
  and unmount → same clear/cancel.
- Tests (fake timers, `requestAnimationFrame` stubbed to `setTimeout 16`):
  flip `:71` to assert a `stream_event` text delta reaches
  `partial.blocks[0].text` after the flush and `lastSeq` is unchanged;
  durable-after-transient ordering (delta then `assistant` in the same
  tick → block finalized, not resurrected); queue dropped on
  `reconnecting`; **stale generation**: delta queued → `reconnecting` →
  `open` → `stream_snapshot` frame (flushed) → advance timers so the old
  rAF would fire → store partial equals the snapshot only; queue dropped
  on unmount (no store write after `close()`); malformed transient JSON
  dropped without warning.

### Task 5 — P-B2.1 PR

- `npx vitest run && pnpm run lint && pnpm run build`; open PR titled
  "feat(exec): partial assembly + tool activity in the execution reducer
  (P-B2.1)"; body lists spec rules covered and states "no visible change".
- Reviews per CLAUDE.md: codex R1 standard, R2 three-way (attack:
  reducer sequences / rAF ordering; defend: spec drift vs §4.1–4.3; file
  health: `event-reducer.ts` size after growth — split `partial.ts` out if
  it passes ~350 lines).

## P-B2.2 — renderer

### Task 6 — default-prop snapshots, `useElapsedTicker`, time formatting

First commit of P-B2.2, **before any renderer change**: add
`toMatchSnapshot()` tests for `ToolCallBlock` (collapsed + expanded),
`MessageBubble` (user + assistant), `ThinkingBlock` (collapsed + expanded)
with today's props only. These snapshots stay untouched through tasks
7–9 (spec G5).

Files: new `spa/src/hooks/useElapsedTicker.ts`, new
`spa/src/lib/nex/format-duration.ts` (+ tests).

- `useElapsedTicker(active: boolean): number` returns `Date.now()` that
  updates every 1000 ms while `active`, stable otherwise; clears on
  unmount.
- `formatDuration(ms)`: `<1000` → `0.4s`; `<60000` → `6.2s`; else
  `1m 05s`. Negative → `0.0s`.

### Task 7 — `ToolCallBlock` status + timing (R2, streaming variant of R1)

Files: `ToolCallBlock.tsx`, `ToolCallBlock.test.tsx`, locales.

- New optional props: `status?: 'streaming' | 'running' | 'done' | 'error' | 'aborted'`,
  `startedAt?: number`, `endedAt?: number | null`, `now?: number`,
  `rawInput?: string`. Absent `status` → today's DOM (snapshot-compatible).
- `running`: `CircleNotch` with `animate-spin` replaces `Wrench`; badge
  `formatDuration(max(0, now - startedAt))` only when `startedAt > 0`.
  `done`/`error`: badge from `endedAt - startedAt` (error colour for
  `error`). `aborted`: muted `t('execution.tool.aborted')`. `streaming`:
  spinner, summary = first 80 chars of `rawInput`, detail = raw prefix in
  `<pre>`.
- i18n: `execution.tool.aborted`, `execution.tool.unknown` in en + zh-TW
  (`unknown` is the name placeholder for snapshot-seeded tool blocks,
  passed as `tool` by the caller in task 9).

### Task 8 — streaming cursor on `MessageBubble` / `ThinkingBlock`

Files: `MessageBubble.tsx`, `ThinkingBlock.tsx`, their tests,
`spa/src/index.css` (or the existing global stylesheet — locate the one
that defines `@theme` tokens) for the `.stream-cursor` blink keyframes
using a theme colour token.

- `streaming?: boolean` prop; when true, append
  `<span data-testid="stream-cursor" class="stream-cursor">▌</span>` after
  the content (assistant bubble: after the markdown div; thinking: header
  gets the cursor so it is visible while collapsed).

### Task 9 — `ConversationMessages` partial group + `ExecutionView` wiring (R1, R3, R4)

Files: `ConversationMessages.tsx`, `ConversationMessages.test.tsx`,
`execution/ExecutionView.tsx`, `ExecutionView.test.tsx`.

- Props `partial?`, `tools?`, `now?`. Durable `tool_use` blocks look up
  `tools[block.id]` and pass status/timing. Trailing partial group
  rendered after the list, before `children`, blocks by ascending index
  per R1. Scroll effect adds `partialVersion` (sum of field lengths) to
  its deps.
- `ExecutionView`: `const anyRunning = Object.values(st.tools).some(t => t.status === 'running')`;
  `const now = useElapsedTicker(anyRunning)`; `showThinking` per R3.
- Tests: partial group after durable messages and before `children`;
  index ordering; empty text block renders nothing; snapshot-seeded
  `tool_use` block (no `toolName`) shows `execution.tool.unknown`; no
  `partial` prop → `ConversationView.snapshot.test.tsx` unchanged (run
  it); running tool
  shows spinner + elapsed; R3 truth table in `ExecutionView.test.tsx`
  (observer `turnLive` → dots; partial text → no dots; `pendingSend`
  queued → no dots); `aborted` badge after `execution.turn_orphaned`.

### Task 10 — P-B2.2 PR + acceptance

- Same gates; PR "feat(exec): typewriter and tool activity in the
  execution pane (P-B2.2)"; codex R1 + R2 (attack: render churn / key
  stability; defend: R1–R5 vs spec; file health: `ConversationMessages`
  growth — extract `PartialMessageGroup` if it passes ~180 lines).
- After merge: `pnpm run build`, spec §6 acceptance on mlab (dev server
  :5174 + playwright cli, session name `pb2-exec-stream`), record results
  in the spec's §6 as a dated checklist.

## Bump

After each PR merges: separate bump PR (`VERSION`, `package.json`,
`spa/package.json`, `CHANGELOG.md`), branch reset to `origin/main` first.
