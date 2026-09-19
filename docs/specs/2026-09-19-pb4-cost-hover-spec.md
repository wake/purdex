# Spec — P-B4: exec pane cost hover / panel (client-side)

- Status: v1.0 draft (2026-09-19)
- Predecessors: P-B (`2026-09-15-pb-execution-pane-spec.md`, header §4.3.3),
  P-B2 / P-B3 (tool activity, N2 facts). This is the last of the user's
  decision #5 (tool summaries, line-numbered diffs, **cost hover**,
  typewriter — all must-haves).
- User decisions (do not reopen): **client-side** — everything comes from
  the `result` frames the pane already holds; no Nexen rollup (revisit if
  the Swift client needs one). Anchor: the `$0.08` in the exec-pane header
  (`ExecutionHeader.tsx:63`, value from `ExecutionView.tsx:109`). Reuse
  `HoverTooltip` / `FloatingPanel`; do not build a third popover.
- Display reference: Nexen console rendering spec R13 (hover shows one
  line; expanding grows downward only; the numbers are per turn: duration ·
  cost · output tokens · ttft; expanded: per-model split, environment).
- Pure SPA; no daemon / Nexen change; `origin/main` alpha.408.

## 1. Problem

The header shows a single `$0.08` — the sum of `total_cost_usd` over every
message in the store (`ExecutionView.tsx:109`, cast through
`{ total_cost_usd?: number }`). There is no way to see which turn cost
what, how the tokens split (output vs cache read vs cache write), how long
the API took versus the wall clock, which models were billed, or how close
the host's quota window is. Every one of those facts is already in the
`result` frame Claude Code emits per turn; the SPA just never reads them.

## 2. Goals / non-goals

### Goals

- G1 A pure `costSummary(messages)` that folds the top-level `result`
  frames into per-turn rows and totals (cost, tokens by class, API vs wall
  time, API rounds, models), tolerant of every older / partial frame shape.
- G2 Header cost becomes an anchor: **hover** → one-line summary
  (`HoverTooltip`); **click** → `FloatingPanel` with the per-turn table,
  totals, per-model split and (when the daemon reports one) the host's
  quota window.
- G3 Subagent usage is neither double-counted nor dropped (F6).
- G4 Nothing shows a partial sum as if it were the total (F8).
- G5 `ResultMessage` typed for the fields read, additive, every field
  optional.

### Non-goals

- Server-side rollups, cross-execution / per-host cost totals, price
  tables (the daemon's `costUSD` is the source of truth; the SPA never
  multiplies tokens by a rate).
- Cache-miss explanations, `iterations[]`, `ttft_stream_ms`,
  `time_to_request_ms`, `first_content_frame_ms` — present in the frame
  (F2) but not shown; the panel shows `ttft_ms` only.
- Persisting anything; the panel is derived on open.
- Theming beyond the existing tokens; the panel uses `FloatingPanel`'s
  chrome as is.

## 3. Measured facts this design rests on

Measured 2026-09-19 on mlab daemon alpha.405 (`cf11a7f3`, nexen v0.12.0,
Claude Code 2.1.27x) — history pages saved as fixtures
`spa/src/lib/nex/__fixtures__/cost-turns-06GB2ZFD.json` (execution
`06GB2ZFDHNCW2ZWQ33EG9D1ZXM`, 12 turns, filtered to lifecycle + `result`
kinds with payloads verbatim) and `cost-subagent-06GBGXTW.json`
(`06GBGXTWQBYTSZG1T71JV6784G`, one turn that spawned one subagent via the
`Agent` tool under the `trusted` profile; whole page).

- F1 The `result` frame's top-level keys: `type, subtype, is_error,
  duration_ms, duration_api_ms, num_turns, result, session_id,
  total_cost_usd, usage, modelUsage, permission_denials, uuid, stop_reason,
  ttft_ms, ttft_stream_ms, first_content_frame_ms, time_to_request_ms,
  result_index, queued_turn_count, subagent_stats, terminal_reason,
  api_error_status, fast_mode_state, fast_mode_disabled_reason`.
- F2 `usage`: `{input_tokens, output_tokens, cache_creation_input_tokens,
  cache_read_input_tokens, output_tokens_details: {thinking_tokens},
  cache_creation: {ephemeral_1h_input_tokens, ephemeral_5m_input_tokens},
  server_tool_use, service_tier, iterations[], speed, inference_geo}`.
- F3 `modelUsage`: keyed by model id, each `{inputTokens, outputTokens,
  cacheReadInputTokens, cacheCreationInputTokens, webSearchRequests,
  costUSD, contextWindow, maxOutputTokens, thinkingTokens, canonicalModel,
  provider, costBasis}`.
- F4 **`total_cost_usd === Σ modelUsage[*].costUSD`** on all 13 result
  frames measured (12 + subagent run), to floating-point equality.
- F5 **`usage` is not the whole turn when more than one model ran**: seq 8
  (haiku + sonnet) has `usage.output_tokens 364` but
  `Σ modelUsage.outputTokens 376` and `usage.input_tokens 2` vs `911`;
  seq 974 `usage.output_tokens 103` vs `701`, `cache_read 46401` vs
  `80127`. `usage` reflects one model's messages, `modelUsage` all of
  them. ⇒ token breakdowns sum `modelUsage`; `usage` is the fallback for
  frames without `modelUsage` (older CC).
- F6 **Subagents do not emit their own `result`**: the subagent run has
  frames with `parent_tool_use_id = toolu_01DCjy…` (`user`, `assistant`)
  but a single top-level `result` (`parent_tool_use_id: null`,
  `subagent_stats.spawned: 1, completed: 1`) whose `total_cost_usd` 0.088
  (vs 0.027 for the identical brief without a subagent) and
  `modelUsage.cacheReadInputTokens 46503` include the subagent's usage.
  The subagent `assistant` frames carry `message.usage` of their own —
  that is the same spend seen twice. ⇒ sum **only** `result` frames with
  `parent_tool_use_id == null`; never add subagent `message.usage`. (The
  reducer keeps subagent frames in `messages` — P-B2 F1 — so the filter
  must be explicit.)
- F7 `subtype` values seen: `success` (10), `error_during_execution` (2:
  one with cost 0 / `num_turns 2` / `duration_api_ms 0`, one with cost
  0.0057). A `success` with `num_turns 0`, cost 0, `duration_ms 10`
  exists (seq 100 — the turn withdrawn by an interrupt before the first
  request). `num_turns` counts API round-trips inside one Nexen turn (1–3
  here), not Nexen turns.
- F8 History is paged to the end before the SSE opens and `historyLoaded`
  is set only after the last page (`useExecutionSubscription.ts`
  `connect()`: loop until `next_cursor === 0`); `ExecutionView` already
  renders a loading state while `!historyLoaded`. ⇒ the sum is never
  partial once the header is rendered; live turns append. The guard is
  kept explicit anyway (§4.2).
- F9 The pane's stream is execution-scoped and the site-wide stripping
  table (contract §3.5) names only `execution.delegated.brief`,
  `execution.message_accepted.text`, `tool_use.{input, primary_arg}`,
  `tool_result.{output, diff, file}` — `result` is never stripped.
- F10 Quota: `GET /api/nex/v1/host` → `NexHostInfo.quota:
  {five_hour_pct, seven_day_pct, resets_at, source} | null`; not in a
  store — `NexEngineStatus.tsx:75` fetches it locally per render key.
  Under the `trusted` profile on mlab, `quota.source` is `usage_api`.
- F11 Anchors available: `HoverTooltip` (portal, 800 ms delay,
  `placement: 'top' | 'right'`, content = children, anchor = parent
  element), `FloatingPanel` (portal, draggable, `anchorRef`, `width`,
  Escape / outside-click / × close, `title`). `ExecutionHeader.tsx` is 83
  lines, `ExecutionView.tsx` 195.
- F12 `formatDuration` (`lib/nex/format-duration.ts`) renders ms as
  `x.xs` / `Nm SSs`; `ExecutionHeader.test.tsx` fixes `costUsd: 0` and the
  `$0.00` text; `ExecutionView` computes `costUsd` with `useMemo` over
  `st.messages`.

## 4. Design

### 4.1 `lib/nex/cost-summary.ts` (pure)

```ts
export interface TokenTotals { input: number; output: number; cacheRead: number; cacheWrite: number }
export interface TurnCost {
  index: number                 // 1-based among top-level result frames, in seq order
  costUsd: number               // total_cost_usd (0 when absent)
  tokens: TokenTotals | null    // Σ modelUsage, else usage, else null
  durationMs: number | null     // wall clock
  apiMs: number | null          // duration_api_ms
  ttftMs: number | null
  rounds: number | null         // num_turns
  subtype: string               // 'success' | 'error_during_execution' | …; '' when absent
  models: string[]              // canonicalModel (or key) list, order of appearance
}
export interface ModelCost { model: string; costUsd: number; tokens: TokenTotals }
export interface CostSummary {
  turns: TurnCost[]
  totalUsd: number
  tokens: TokenTotals           // Σ over turns that had tokens
  durationMs: number; apiMs: number   // Σ (absent → 0)
  rounds: number
  models: ModelCost[]           // aggregated over all turns, sorted by costUsd desc
  unsplitTurns: number          // costed turns (costUsd > 0) without a modelUsage split (C5 note)
}
export function costSummary(messages: readonly StreamMessage[]): CostSummary
```

Rules:
- C1 A turn is a message with `type === 'result'` and
  `parent_tool_use_id == null` (F6). Order = array order (= seq order).
- C2 `costUsd = total_cost_usd` when a finite number ≥ 0, else 0.
  `totalUsd = Σ costUsd`. This is the number the header shows today; the
  header keeps computing it from the same function so hover and header
  cannot disagree.
- C3 Tokens per turn: if `modelUsage` is an object with ≥ 1 entry whose
  four token fields are finite numbers → sum them (F5); else if `usage`
  has the four snake_case fields → use them; else `null`. Model list per
  turn from `modelUsage` keys (`canonicalModel` when a string, else the
  key).
- C4 `durationMs` / `apiMs` / `ttftMs` / `rounds`: the field when a
  finite number ≥ 0, else `null`. Totals treat `null` as 0.
- C5 `models`: fold `modelUsage` across turns by canonical model:
  `costUsd = Σ costUSD` (finite numbers only), tokens summed. Frames
  without `modelUsage` contribute nothing here (their cost is still in
  `totalUsd`; the panel says so with a "n turns without a model split"
  note when `unsplitTurns > 0`).
- C6 Shape tolerance: every read is by type check; a hostile / partial
  frame yields zeros and nulls, never NaN, never a throw. Keys are read
  with `Object.hasOwn`-style own-key iteration for `modelUsage`.
- C7 Idempotent and referentially stable inputs → identical outputs
  (memoised by the caller on `st.messages`).

### 4.2 Header anchor

`ExecutionHeader` gains `cost: CostSummary | null` **in place of**
`costUsd: number`; `ExecutionView` passes
`historyLoaded ? costSummary(st.messages) : null` (memoised on
`st.messages` / `historyLoaded`).

- H1 The `$x.xx` span becomes a `<button data-testid="execution-cost">`
  with the same text (`$${totalUsd.toFixed(2)}`), muted colour, hover
  underline; `cost === null` → text `$…`, `disabled`, no tooltip (F8 /
  G4).
- H2 Hover / focus → `HoverTooltip placement="top"` with one line:
  `{n} turns · $x.xxxx · {out}k out · {api} API / {wall} wall`
  (i18n key `execution.cost.summary`, placeholders `turns`, `usd`, `out`,
  `api`, `wall`). `turns` = `cost.turns.length` — the number of `result`
  frames, which can lag `summary.turn_count` by the running turn; the
  tooltip counts what it summed.
- H3 Click → toggles the panel (§4.3). While open the button carries
  `aria-expanded="true"`; `FloatingPanel`'s outside-click rule already
  ignores the anchor.

### 4.3 `components/execution/CostPanel.tsx`

`FloatingPanel title={t('execution.cost.title')} width={440}
anchorRef={costButtonRef} testId="cost-panel"`; content, top to bottom:

- P1 **Totals** line: `$total` (4 dp) · `{n} turns` · `{rounds} API rounds`
  · `{api} API / {wall} wall`.
- P2 **Tokens** row (four cells, `tabular-nums`): output · input · cache
  read · cache write, each `formatTokens` (`364`, `11.2k`, `1.3M`). Hidden
  when no turn had tokens.
- P3 **Models** list: one row per `ModelCost` — model · `$cost` · `out`
  tokens; plus the C5 note when applicable. Hidden when empty.
- P4 **Turns** table (`data-testid="cost-turns"`, `max-h-64 overflow-auto`,
  chronological, newest last, auto-scrolled to the bottom on open): `#`,
  `$cost` (4 dp), out, cache read, API / wall (`formatDuration`), rounds,
  and a status cell: empty for `success`, `t('execution.cost.turn_error')`
  in the error colour for `error_during_execution` / `is_error`, the raw
  `subtype` (muted) otherwise. A turn with `tokens === null` shows `—` in
  the token cells.
- P5 **Quota** (F10): on open, `fetchNexHost(hostId)` once; while pending
  nothing; on success with non-null `quota` → `t('execution.cost.quota')`
  row: `5h {five_hour_pct}% · 7d {seven_day_pct}% · {source}` with the
  plain text (`QuotaBar` is a private function inside
  `NexEngineStatus.tsx:33`; lifting it is not this phase's job). `quota: null` or a fetch error → row absent;
  errors are swallowed (the panel is about cost, quota is a courtesy).
  The fetch is cancelled on close / unmount (`cancelled` flag as in
  `NexEngineStatus`).
- P6 The panel re-renders live: a `result` frame landing while it is open
  adds a row (the memo on `st.messages` changes); the quota is **not**
  refetched on every frame — only on open.

### 4.4 Formatting helpers (`lib/nex/format-cost.ts`, pure)

- `formatUsd(v, dp = 4)` → `$0.0300`; negative / non-finite → `$—`.
- `formatTokens(n)` → `< 1000` as is; `< 1e6` → `12.3k` (one decimal,
  trailing `.0` dropped); else `1.2M`. Non-finite → `—`.
- Durations via the existing `formatDuration`.

### 4.5 Types

`ResultMessage` (`message-types.ts`) gains, all optional:
`is_error?: boolean`, `duration_api_ms?: number`, `num_turns?: number`,
`ttft_ms?: number`, `parent_tool_use_id?: string | null`, `usage?: {
input_tokens?: number; output_tokens?: number; cache_read_input_tokens?:
number; cache_creation_input_tokens?: number }`, `modelUsage?:
Record<string, { inputTokens?: number; outputTokens?: number;
cacheReadInputTokens?: number; cacheCreationInputTokens?: number;
costUSD?: number; canonicalModel?: string }>`. The P-D.3 "byte-identical"
note on the file header is amended to say P-B4 added fields.

### 4.6 i18n (en + zh-TW)

`execution.cost.title` ("Cost" / "成本"), `execution.cost.summary`
("{{turns}} turns · {{usd}} · {{out}} out · {{api}} API / {{wall}} wall"),
`execution.cost.turns` ("turns"), `execution.cost.rounds` ("API rounds" /
"API 回合"), `execution.cost.tokens.output` / `.input` / `.cache_read` /
`.cache_write`, `execution.cost.models` ("Models"), `execution.cost.models_note`
("{{n}} turns without a per-model split"), `execution.cost.turn` ("#"),
`execution.cost.turn_error` ("error" / "錯誤"), `execution.cost.quota`
("Quota" / "額度"), `execution.cost.loading` ("$…").

### 4.7 What does not change

Reducer, store, subscription hook, `messages` contents, the header's
other fields and both lease-backed actions, `formatDuration`,
`HoverTooltip` / `FloatingPanel` themselves.

## 5. Phases

Two PRs, each ≤ 800 lines, TDD by subagent, codex R1 + R2 per PR.

### P-B4.1 — summary + header hover (no panel)

- Task 1 `message-types.ts` additive fields (§4.5) + `format-cost.ts` +
  tests (`formatUsd` / `formatTokens` edge cases).
- Task 2 `cost-summary.ts` C1–C7 + tests on both fixtures: 12 turns with
  the exact `totalUsd` (Σ of the twelve `total_cost_usd`, asserted as a
  literal), seq 8 tokens from `modelUsage` (out 376, not 364), seq 974
  cache read 80127, `models` has two entries with haiku's cost 0.000969,
  seq 64 / 100 rows with cost 0, `null` tokens for a frame stripped of
  both `usage` and `modelUsage`; subagent fixture → exactly one turn,
  cost 0.0881726, and a synthetic subagent `result` (parent non-null)
  is ignored; hostile frames (NaN, strings, `modelUsage: []`) → zeros;
  mutation guard: a test that fails if the subagent filter is removed
  (add a synthetic subagent result with cost 1 and assert the total
  unchanged).
- Task 3 `ExecutionHeader` H1–H2 (`cost` prop replaces `costUsd`;
  `ExecutionView` computes via `costSummary`), tests: `$…` + disabled
  when `null`; text unchanged for a summary; tooltip text after the
  hover delay (fake timers, as `HoverTooltip` tests do); existing
  ExecutionHeader / ExecutionView assertions updated from `costUsd`.

### P-B4.2 — panel + quota

- Task 4 `CostPanel` P1–P4 + tests (rows, error cell, `—` cells, note,
  auto-scroll), mounted from `ExecutionHeader` H3 (`useRef` on the
  button, `open` state, `aria-expanded`).
- Task 5 P5 quota (mocked `fetchNexHost`: null → no row; value → row;
  reject → no row; unmount before resolve → no state update).

## 6. Acceptance (real machine, mlab, worktree dev server :5175)

1. Open `06GB2ZFDHNCW2ZWQ33EG9D1ZXM` (archived; opens read-only): header
   `$0.26` (Σ of the twelve turns = 0.2576558), hover → `12 turns ·
   $0.2577 · 4.5k out · 1m 05s API / 1m 33s wall` (fixture sums: output
   4478 from `modelUsage`, API 65 032 ms, wall 93 086 ms, 22 rounds); click →
   panel with 12 rows, rows 6 and 10 marked error, row 8 `$0.0000`,
   models `claude-sonnet-5` and `claude-haiku-4-5` with the haiku cost
   `$0.0010`; quota row present (`usage_api`).
2. Open `06GBGXTWQBYTSZG1T71JV6784G`: 1 turn, `$0.0882`, no double count
   (the subagent's frames are in the conversation, the panel has one row).
3. Live: send a small turn to a fresh execution with the panel open → a
   row appends without closing the panel; header updates.
4. `$…` state: throttle the network (devtools) or open a long execution
   and observe the button disabled until history is in — or accept the
   unit test if not reproducible interactively.
5. Archive the executions created for acceptance; remove
   `/Users/wake/Workspace/tmp-pb4-fixture`.

## 7. Risks

- `modelUsage` is a newer CC field; older transcripts / other providers
  (P3 codex) will lack it — C3 falls back to `usage`, C5 leaves models
  empty and says so.
- `FloatingPanel` inside the header: the header is `text-xs` and the
  panel is a portal, so no clipping; the anchor is the button.
- `HoverTooltip` uses `whitespace-nowrap`; the summary line is ~60 chars,
  fine at the pane's widths (≥ 600 px).

## 8. Open questions

None blocking. `ttft_ms` is shown per turn only (P4) — promote to the
tooltip if it proves useful.

## 9. Review log

- (pending) codex plan + spec review.
