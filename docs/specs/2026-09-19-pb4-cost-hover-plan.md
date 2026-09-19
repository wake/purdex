# Plan — P-B4: exec pane cost hover / panel

- Spec: `2026-09-19-pb4-cost-hover-spec.md` (v1.1). Rule ids (C1–C7, C3a,
  H1–H3, P1–P6, F1–F12) refer to it.
- Worktree: `.claude/worktrees/pb4-cost-hover`, branch
  `worktree-pb4-cost-hover`, based on `origin/main` alpha.408 (`a9046291`).
  Two PRs: P-B4.1 (tasks 1–3), P-B4.2 (tasks 4–5). P-B4.2 starts after
  P-B4.1 merges; fast-forward the branch to `origin/main` and continue.
- Every task: subagent, TDD (failing test first, then the code), one
  commit with `git commit --only <files>` (new files `git add` first).
  Every Bash call prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb4-cost-hover/spa &&`.
  Verify per task: `npx vitest run <changed test files>`; before each PR:
  `npx vitest run && pnpm run lint && npx tsc -p tsconfig.app.json --noEmit && pnpm run build`,
  then `git diff --stat origin/main...HEAD -- ':!docs' ':!spa/src/lib/nex/__fixtures__'`
  must report ≤ 800 changed lines (spec §5); if not, the PR is split
  before it is opened.
- Tasks are sequential within a PR. No daemon, Nexen or hook change.

## Measured baseline (2026-09-19, worktree at alpha.408)

- `spa/src/components/execution/ExecutionHeader.tsx` 83 lines: props
  14–26 (`costUsd: number` at 16), cost span at 63
  (`<span>${costUsd.toFixed(2)}</span>`), turns span at 62.
- `spa/src/components/execution/ExecutionView.tsx` 195 lines: `costUsd`
  memo at 109, `<ExecutionHeader … costUsd={costUsd}` at 154;
  `st.historyLoaded` gates the body at 162.
- `spa/src/components/HoverTooltip.tsx` 105 lines: delay 800 ms,
  `placement` `top | right`, anchor = `parentElement` of the hidden
  marker span, portal with `role="tooltip"`; tests use
  `vi.useFakeTimers()` + `advanceTimersByTime(799)` / `(1)`.
- `spa/src/components/FloatingPanel.tsx` 263 lines: `FloatingPanel({title,
  anchorRef, onClose, width = 320, testId = 'floating-panel', children})`;
  usage example `hosts/HostIconField.tsx:60`.
- `spa/src/lib/nex/message-types.ts` 84 lines: `ResultMessage` at 39–44
  (`type, subtype, total_cost_usd?, session_id?, duration_ms?`), file
  header says "byte-identical to the originals" (P-D.3).
- `spa/src/lib/nex/format-duration.ts` 40 lines (`formatDuration(ms)`).
- `spa/src/lib/nex/nex-api.ts:73` `fetchNexHost(hostId): Promise<NexHostInfo>`;
  `lib/nex/types.ts:122` `NexQuota`, `:150` `NexHostInfo.quota`.
- Tests: `ExecutionHeader.test.tsx` 100 lines (`costUsd: 0` in the base
  props at 27), `ExecutionView.test.tsx` 813 lines (renders the header
  through the real component; it has **no** `$0` assertion today — the
  integration cases in Task 3 are new), `HoverTooltip.test.tsx` 97 lines.
- i18n: `execution.turns` at `locales/en.json:781`; `t(key, params)`
  interpolates `{{name}}`.
- Fixtures (committed with the spec):
  `lib/nex/__fixtures__/cost-turns-06GB2ZFD.json` (36 items: 12 `result`
  + lifecycle; sums — cost 0.2576558, `duration_api_ms` 65 032,
  `duration_ms` 93 086, `num_turns` 22, Σ `modelUsage` output 4478,
  haiku cost 0.000969 on seq 8; seq 8 `usage.output_tokens` 364 vs
  `modelUsage` 376; seq 974 cache read 46 401 vs 80 127; seq 64 / 130
  `error_during_execution`; seq 100 zero turn) and
  `cost-subagent-06GBGXTW.json` (18 items; one `result`, cost 0.0881726,
  `subagent_stats.spawned 1`; frames seq 997 / 999 carry
  `parent_tool_use_id`).

## PR 1 — P-B4.1 summary + header hover

### Task 1 — types + `format-cost.ts`

Files: `lib/nex/message-types.ts`, `lib/nex/format-cost.ts` (new),
`lib/nex/format-cost.test.ts` (new).

1. Tests: `formatUsd(0.0299658)` → `$0.0300`; `formatUsd(0)` → `$0.0000`;
   `formatUsd(1.5, 2)` → `$1.50`; `formatUsd(-1)` / `NaN` / `Infinity` →
   `$—`. `formatTokens(0)` → `0`; `364` → `364`; `999` → `999`; `1000` →
   `1k`; `11244` → `11.2k`; `999950` → `1M` (rounding crosses; assert
   whatever the implementation defines and document it — recommended:
   compute in k first, if ≥ 1000k switch to M); `1_234_567` → `1.2M`;
   `NaN` → `—`; `-5` → `—`.
2. Code: helpers as spec §4.4; `ResultMessage` additive fields (§4.5)
   with a one-line comment per field naming the CC key; amend the file
   header ("P-D.3 moved verbatim; P-B4 added the optional cost fields").
3. Commit: `feat(spa): ResultMessage cost fields + formatUsd / formatTokens`.

### Task 2 — `cost-summary.ts` (C1–C7)

Files: `lib/nex/cost-summary.ts` (new), `lib/nex/cost-summary.test.ts` (new).

1. Tests (import both fixtures as JSON; pass `items.map(i => i.payload)`
   filtered to the kinds the reducer would push — i.e. every non-lifecycle
   payload; note the reducer never pushes lifecycle kinds, so filter
   `kind` not starting with `execution.` / `lease.`):
   - 12 turns, `index` 1..12 in seq order; `totalUsd` `toBeCloseTo(0.2576558, 7)`;
     `apiMs 65032`, `durationMs 93086`, `rounds 22`; `tokens.output 4478`.
   - Turn 1 (seq 8): `tokens.output 376` (not 364), `tokens.input 911`,
     `models` `['claude-haiku-4-5', 'claude-sonnet-5']` (canonical names).
   - Turn 12 (seq 974): `tokens.cacheRead 80127`.
   - Turn 6 (seq 64): `subtype 'error_during_execution'`, `isError true`,
     `costUsd 0`, `apiMs 0`, `rounds 2`, `tokens` equal to the zero object
     (usage present with zeros → not null). Turn 1: `isError false`. A
     synthetic `{type:'result', is_error: true}` (no subtype) → `isError
     true`; `{subtype: 'success', is_error: false}` → false.
   - Turn 8 (seq 100): `costUsd 0`, `rounds 0`, `durationMs 10`.
   - `models`: two entries sorted by cost desc; haiku `costUsd`
     `toBeCloseTo(0.000969)`; Σ `models.costUsd` ≈ `totalUsd` (7 dp).
   - Subagent fixture: `turns.length 1`, `totalUsd` ≈ 0.0881726.
   - Mutation guard: append a synthetic `{type:'result', parent_tool_use_id:
     'toolu_x', total_cost_usd: 1}` to the subagent payloads → `totalUsd`
     unchanged and `turns.length` still 1 (fails if C1's filter is
     dropped).
   - Fallback (C3): a result with `usage` only (no `modelUsage`) → tokens
     from `usage`, `models` empty, `unsplitTurns` +1 when its cost > 0; a
     result with neither → `tokens null`; `usage` with only
     `output_tokens` → the other three are 0; `unsplitTurns` on the
     fixture is 0.
   - Partially valid `modelUsage` (C3): one valid entry + one with
     `outputTokens: 'x'` → tokens / models from the valid entry only, the
     malformed one absent from `models`; all entries malformed +
     `usage` present → `usage` fallback and `unsplitTurns` +1; `modelUsage:
     []` / `null` → same as absent.
   - Hostile: `total_cost_usd: 'x'`, `NaN`, `-1` → 0; `num_turns: 1.5` →
     kept (finite ≥ 0; integers not required); no throw on
     `{type:'result'}` alone or on `modelUsage: { __proto__: {...} }`.
   - Non-result messages (`assistant`, `user`) contribute nothing;
     `messages: []` → all zeros, `turns []`, `models []`.
2. Code: pure module, `obj()` from `content-blocks.ts`, own-key iteration
   over `modelUsage`, `Number.isFinite` guards.
3. Commit: `feat(spa): costSummary — per-turn cost / token / timing rollup from result frames`.

### Task 3 — header anchor + tooltip (H1–H2)

Files: `components/execution/ExecutionHeader.tsx`,
`components/execution/ExecutionView.tsx`, both tests, `locales/en.json`,
`locales/zh-TW.json`.

1. Tests (ExecutionHeader): `cost={null}` → button `execution-cost`
   disabled with text `$…`; `cost={summary}` (build with `costSummary`
   from the fixture payloads) → text `$0.26`, enabled; hover the button
   with fake timers 800 ms → `role="tooltip"` text
   `12 turns · $0.2577 · 4.5k out · 1m 05s API / 1m 33s wall`; leaving
   before 800 ms → no tooltip. Update the base props (`costUsd: 0` →
   `cost: costSummary([])`, so the existing `$0.00` assertion still
   holds). Tests (ExecutionView, new integration cases using the file's
   existing store harness): (a) `historyLoaded: false` → `execution-cost`
   text `$…` and disabled; (b) after `setHistoryLoaded(true)` with two
   top-level `result` messages (0.01 + 0.02) → `$0.03` enabled; (c) a
   third `result` applied through `applyEvents` while mounted → `$0.06`
   (P6 live update); (d) a subagent `result` (parent non-null, cost 1)
   applied → text unchanged (G3 through the real wiring).
2. Code: `ExecutionHeader` prop `cost: CostSummary | null` replaces
   `costUsd`; button per H1 (`type="button"`, `data-testid="execution-cost"`,
   `disabled={!cost}`, `className` muted + `hover:underline`); `<HoverTooltip
   placement="top">{line}</HoverTooltip>` inside the button (its anchor is
   the parent element) rendered only when `cost`; `line` from
   `t('execution.cost.summary', {...})` with `formatUsd(totalUsd)`,
   `formatTokens(tokens.output)`, `formatDuration(apiMs)`,
   `formatDuration(durationMs)`. `ExecutionView`: `const cost = useMemo(()
   => (st.historyLoaded ? costSummary(st.messages) : null), [st.messages,
   st.historyLoaded])`; pass `cost={cost}`. The click handler is a no-op
   until Task 4 (do not add `aria-expanded` yet). i18n keys
   `execution.cost.summary`, `execution.cost.loading` (§4.6).
3. Commit: `feat(spa): exec header cost is an anchor with a hover summary`.

PR 1 checklist as above. Title `feat(spa): P-B4.1 — cost summary rollup + header hover`.

## PR 2 — P-B4.2 panel + quota

### Task 4 — `CostPanel` (P1–P4, P6) + H3

Files: `components/execution/CostPanel.tsx` (new),
`components/execution/CostPanel.test.tsx` (new),
`components/execution/ExecutionHeader.tsx` (+ test), locales.

1. Tests (CostPanel, rendered with a fixture summary and a stub
   `anchorRef`): totals line text; tokens row four cells with
   `formatTokens` values; models list two rows (sonnet first), note
   absent when `unsplitTurns === 0` and present with `n` when > 0 (use a
   summary built from payloads where one `modelUsage` is deleted);
   turns table 12 rows, rows 6 and 10 have `data-testid="turn-error"`
   with `title="error_during_execution"`, row 8 shows `$0.0000`, a row
   built from a `tokens: null` turn shows `—` in the token cells;
   `max-h-64 overflow-auto` on the table wrapper and `scrollTop` set to
   `scrollHeight` on mount (jsdom: assert through
   `Object.defineProperty(HTMLElement.prototype, 'scrollHeight', …)` +
   a spy on the `scrollTop` setter — note the variant used). Tests
   (ExecutionHeader): click `execution-cost` → `cost-panel` visible and
   `aria-expanded="true"`; Escape → closed; click again → closed
   (FloatingPanel ignores the anchor's own mousedown); **mousedown on
   another header element (e.g. the interrupt button) → closed** (the
   outside-click wiring through `anchorRef`).
2. Code: `CostPanel({ summary, hostId, anchorRef, onClose })` using
   `FloatingPanel` (`width={440}`, `testId="cost-panel"`); rows as spec
   P1–P4; `ExecutionHeader` holds `open` state + `useRef<HTMLButtonElement>`
   and renders `<CostPanel …/>` when open (needs `hostId` — add
   `hostId: string` to `ExecutionHeaderProps`; `ExecutionView` has it).
   i18n keys §4.6 except quota.
3. Commit: `feat(spa): CostPanel — per-turn cost table, tokens, models`.

### Task 5 — quota row (P5)

Files: `components/execution/CostPanel.tsx` (+ test), locales.

1. Tests: mock `fetchNexHost` (`vi.mock('../../lib/nex/nex-api')`):
   resolves `{active_account: 'wake@x', quota: null}` → no `cost-quota`
   row; resolves `{active_account: 'wake@x', quota: {five_hour_pct: 34,
   seven_day_pct: 12, resets_at: 0, source: 'usage_api'}}` → row label
   `Host quota — wake@x` and text `5h 34% · 7d 12% · usage_api`; rejects
   → no row, no console error; **stale response**: unmount before a
   deferred promise resolves, then resolve it → no state update (assert
   via a `setState` spy or simply that no act warning / error is
   emitted and the test completes) — this is the `cancelled`-flag
   contract, not an HTTP abort; fetched exactly once per mount even when
   `summary` is re-rendered with a new object.
2. Code: `useEffect` on mount with a `cancelled` flag; `useState<NexQuota
   | null>`; i18n `execution.cost.quota`.
3. Commit: `feat(spa): CostPanel shows the host quota window when the daemon reports one`.

PR 2 checklist as above. Title `feat(spa): P-B4.2 — cost panel with per-turn table and quota`.

## After PR 2

- Real-machine acceptance (spec §6); archive the two fixture executions,
  remove `/Users/wake/Workspace/tmp-pb4-fixture`.
- Bump PR (no codex); main checkout `git pull --ff-only`.
