# Plan — P-B3: exec pane consumes Nexen N2 `tool_use` / `tool_result`

- Spec: `2026-09-19-pb3-tool-events-spec.md` (v1.1). Rule ids (N0–N7,
  R1–R6, F1–F9) refer to it.
- Worktree: `.claude/worktrees/pb3-tool-events`, branch
  `worktree-pb3-tool-events`, based on `origin/main` alpha.405
  (`cf11a7f3`). Three PRs: P-B3.1 (tasks 1–4), P-B3.2 (tasks 5a–8),
  P-B3.3 (tasks 9–10). Each later PR starts after the previous one merges;
  fast-forward the branch to `origin/main` and continue.
- Every task: subagent, TDD (failing test first, then the code), one
  commit with `git commit --only <files>`. Every Bash call prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pb3-tool-events/spa &&`.
  Verify per task: `npx vitest run <changed test files>`; before each PR:
  `npx vitest run && pnpm run lint && npx tsc -p tsconfig.app.json --noEmit && pnpm run build`
  (bare `npx tsc --noEmit` is a no-op in `spa/`).
- **Tasks are sequential within a PR** (each commit compiles and is green
  on its own; later tasks import what earlier ones add). Do not run the
  subagents of one PR in parallel.
- No daemon, `bin/pdx`, nexen pin or hook change. `nex-sse.ts`,
  `useExecutionSubscription.ts`, `partial.ts` are not edited.

## Measured baseline (2026-09-19, worktree at alpha.405)

- `spa/src/lib/nex/event-reducer.ts` 237 lines: `N2_TOOL_KINDS` at 70–71,
  early return at 150–156 (inside `applyDurableEvent`, before
  `applyTurnRules`); `applyTurnRules` 133–142 with the subagent early
  return at 137; `TURN_ENDING_KINDS` 124–127.
- `spa/src/lib/nex/tool-activity.ts` 72 lines: `ToolActivity` 7–12,
  `ToolCallActivity` 21–25, `toToolCallActivity` 32–43,
  `recordToolStarts` 45–52, `recordToolEnds` 54–64 (skip on
  `done | error` at 60), `endTurn` 66–72.
- `spa/src/lib/nex/content-blocks.ts` 11 lines: `obj()`, `contentBlocks()`.
- `spa/src/lib/nex/types.ts`: `NexCapabilities` from line 65 (no
  `tool_events`).
- `spa/src/components/ToolCallBlock.tsx` 103 lines: `getSummary` 23–40,
  `TimingBadge` 42–60, header 64–95 (name span at 86, summary at 87–89,
  badge at 90).
- `spa/src/components/ToolUseBlock.tsx` 22 lines.
- `spa/src/components/ToolResultBlock.tsx` 47 lines: props `{content,
  isError}`, header summary at 13.
- `spa/src/components/ConversationMessages.tsx` 156 lines: user
  `tool_result` branch at 94–97 (`ToolResultBlock content isError`).
- Tests: `tool-activity.test.ts` 111, `event-reducer.test.ts` 282 (the
  alpha.405 guard test at 23–30), `exec-wire-replay.test.ts` (golden
  replay pattern to copy), `ToolCallBlock.test.tsx` 158 (snapshots at 150
  / 156 → `__snapshots__/ToolCallBlock.test.tsx.snap`), `ToolUseBlock
  .test.tsx` 46, `ToolResultBlock.test.tsx` 47, `ConversationMessages
  .test.tsx` 226 (R2 group at 166–185).
- i18n: `spa/src/locales/{en,zh-TW}.json`, keys `execution.tool.aborted` /
  `execution.tool.unknown` at line 801–802; `t(key, params)` interpolates
  `{{name}}`.
- Fixture already committed (`2cf315c5`):
  `spa/src/lib/nex/__fixtures__/n2-tool-events-06GBBX07.json` — the
  history page `{items: [24 events, seq 909–932], next_cursor: 0}` of
  execution `06GBBX0791PP0RQ4WSWDFY5FPM`, verbatim. N2 events at seq 915
  / 918 (Read, `duration_ms` 26, `file.lines` 4), 921 / 923 (Edit, 24,
  `diff` +1 −1, one hunk `[" hello","-world","+nexen"," three"]`
  `old_start 1 old_lines 3 new_start 1 new_lines 3`), 926 / 928 (Bash,
  752, no `file` / `diff`). Tool ids: `toolu_01Lx7qX6AhKu6WyY8nvCRcKi`,
  `toolu_01GaRhSM7bitfwbtCFxeoYTP`, `toolu_01QggSrAB1mvWdYVjNM62jWa`.

## PR 1 — P-B3.1 reducer + types (no visible change)

### Task 1 — `tool_events` capability type + `ToolActivity` v2 shape

Files: `lib/nex/types.ts`, `lib/nex/tool-activity.ts`, `lib/nex/types.test.ts`
(exists), `lib/nex/tool-activity.test.ts`, `components/ToolCallBlock.tsx`,
`components/ToolCallBlock.test.tsx`, `locales/en.json`, `locales/zh-TW.json`.

1. Test (types.test.ts): a `NexCapabilities` literal with
   `tool_events: {output_max_bytes: 8192, diff_max_lines: 2000}` type-checks
   and one without it also does (optional). Test (tool-activity.test.ts):
   `toToolCallActivity({name, startedAt: 100, endedAt: 200, status:
   'denied', durationMs: 7}, 0)` → `{status: 'denied', startedAt: 100,
   endedAt: 200, durationMs: 7}`; a `done` entry with `durationMs: 26`
   carries it through; a `done` entry without `durationMs` → variant has
   `durationMs: undefined` (property absent). Test (ToolCallBlock):
   `activity {status: 'denied', startedAt: 100, endedAt: 200}` → badge
   `data-testid="tool-denied"` with text `denied`; existing snapshots
   unchanged (run, do not update).
2. Code: `types.ts` add `tool_events?: { output_max_bytes: number;
   diff_max_lines: number }` with the §4.5 doc comment. `tool-activity.ts`:
   `ToolActivity` v2 exactly as spec §4.2 (+ `DiffHunk` export),
   `ToolCallActivity` finished variant gains `durationMs?: number | null`
   and a `denied` variant `{status: 'denied'; startedAt; endedAt;
   durationMs?}`; `toToolCallActivity` maps `denied` (with `endedAt` guard
   like done/error); `recordToolEnds` skips `denied` too (N5).
   `ToolCallBlock.TimingBadge`: replace the `default` branch with an
   exhaustive check (`const _exhaustive: never = activity; return null`)
   and add the `denied` case (badge only — the strike-through name and
   the wrench/colour rules are Task 6). i18n `execution.tool.denied`
   en "denied" / zh-TW "已拒絕". This is the only renderer touch in PR 1;
   without it the widened union falls through `default` silently (codex
   finding 5).
3. Commit: `feat(spa): ToolActivity v2 — N2 overlay fields, denied status, tool_events capability type`.

### Task 2 — N1 / N2 / N6 rules in `tool-activity.ts`

Files: `lib/nex/tool-activity.ts`, `lib/nex/tool-activity.test.ts`.

1. Tests (pure functions, not yet wired to the reducer) —
   `recordN2ToolUse(s, payload, at)` and `recordN2ToolResult(s, payload, at)`:
   - N1 unseen → entry `{name, startedAt: at, endedAt: null, status:
     'running', primaryArg, known}`.
   - N1 seen (from A1) → `startedAt`, `status` untouched; `primaryArg` /
     `known` set; `name` kept when non-empty.
   - N1 `primary_arg: null` → `primaryArg: null`; key absent → property
     absent; `known` non-boolean → absent.
   - N2 seen running → `status 'done'`, `endedAt: at`, `durationMs 26`,
     `output` facts without `text`, `file`, `diff` copied; `status
     'error'` → `'error'`; `'denied'` → `'denied'` even when the entry is
     already `'done'`; unknown status string → status unchanged but facts
     still copied.
   - N2 unseen → entry created with `startedAt: 0`, `name` from payload or
     `''` when null.
   - N7 duplicate id: an existing `running` entry `{name: 'Bash'}`
     receives a `tool_result` with `name: null, message_id: null,
     block_index: null, duration_ms: null, status: 'ok'` → `status
     'done'`, `durationMs: null`, `name` still `'Bash'`, `endedAt: at`.
     Then `toToolCallActivity` of that entry → finished variant with
     `durationMs: null` (R2 falls back to `endedAt − startedAt`).
   - N2 after A3 `aborted` → corrected to the mapped status.
   - N2 seen with `endedAt` already set (A2 ran first) → `endedAt` kept,
     not overwritten.
   - N6: missing / non-string `tool_use_id` → same state object returned;
     `diff` with non-array `hunks` → no `diff` property; `output` with
     non-numeric `total_lines` → no `output` property; `duration_ms`
     `null` → `durationMs: null`.
   - Idempotence (N3): applying the same payload twice → deep-equal entry.
2. Code: the two functions, using `obj()` from `content-blocks.ts` and
   small local guards (`num`, `bool`, `strOrNull`); hunks validated per
   element (`old_start`, `old_lines`, `new_start`, `new_lines` numbers,
   `lines` array of strings) — any bad hunk drops the whole `diff`.
3. Commit: `feat(spa): N2 tool_use / tool_result overlay rules (N1, N2, N6)`.

### Task 3 — wire the kinds through the reducer

Files: `lib/nex/event-reducer.ts`, `lib/nex/event-reducer.test.ts`,
`lib/nex/tool-activity.test.ts`.

1. Tests: rewrite the alpha.405 guard test (event-reducer.test.ts 23–30)
   into: N2 kinds advance `lastSeq`, are **not** appended to `messages`,
   and now populate `tools` (assert `durationMs: 12`). New: subagent N2
   (`parent_tool_use_id: 'toolu_parent'`) → `tools` untouched, `lastSeq`
   advanced (N0); N2 `tool_result` does not clear `pendingSend` / does not
   change `turnLive` / `partial`; N1 fail-safe: a synthetic sequence
   `tool_use(seq 1), assistant(seq 2)` (the N2 event carrying the
   **lower** seq — a real batch never does this, F2) → single entry with
   `startedAt` from the N2 event, A1 skipping, **and** `messages` contains
   the `assistant` frame (the seq guard must not have eaten it); the
   sequence `assistant, tool_use, user, tool_result, execution.terminal`
   → entry `done`, not `aborted` (N4).
2. Code: delete `N2_TOOL_KINDS` and the early return; in
   `applyTurnRules` add `if (ev.kind === 'tool_use') return
   recordN2ToolUse(s, p, ev.created_at)` / `tool_result` likewise (after
   the subagent early return, before the turn-ending check — the N2 kinds
   are not in `TURN_ENDING_KINDS` so order is only for readability); in
   `applyDurableEvent`, treat the two kinds as "not a message": the
   non-lifecycle push at 158–162 must skip them (introduce
   `isToolEventKind(kind)` next to `isLifecycleKind`). Update the module
   header comment.
3. Commit: `feat(spa): exec-pane reducer consumes the N2 tool_use / tool_result kinds`.

### Task 4 — golden replay of the fixture + equivalence

Files: `lib/nex/__fixtures__/n2-tool-events-06GBBX07.json` (add to git),
`lib/nex/n2-replay.test.ts` (new).

1. Tests (pattern: `exec-wire-replay.test.ts`):
   - Replay all 24 events through `applyDurableEvent` (payload = `item.payload`,
     `created_at` = `item.created_at`, `seq` = `item.seq`): `tools` has
     exactly the three ids; Read `{status: 'done', durationMs: 26,
     file: {lines: 4}, primaryArg: {key: 'file_path'}, known: true}` and
     no `diff`; Edit `diff: {added: 1, removed: 1, hunks: [one hunk with
     4 lines], truncated: false}`; Bash `durationMs: 752`, no `file`, no
     `diff`, `output: {totalLines: 1, totalBytes: 18, truncated: false,
     hasNonText: false}`; `messages` length = **16** (15 provider
     passthrough frames + the synthetic user bubble from
     `execution.delegated.brief`; hard-coded literal);
     `turnLive false`, `pendingSend false` after `execution.terminal`.
   - Equivalence: replay with the `tool_use` / `tool_result` items
     filtered out → same three ids, same `status`, same `startedAt` /
     `endedAt` (F2/F3: `endedAt − startedAt` = 26 / 24 / 752), **no**
     `durationMs` / `primaryArg` / `known` / `output` / `file` / `diff`
     properties (use `toEqual` against the P-B2 shape, not
     `toMatchObject` — this is the mutation guard: stubbing the N2 rules
     would make the full replay equal the filtered one and fail the
     `durationMs` assertions above).
   - Do **not** add a "swap array order, keep seq" replay: with the seq
     guard the swapped `assistant` would be dropped and the N1 fail-safe
     entry would mask that (codex finding 1). The fail-safe is covered by
     Task 3's synthetic renumbered sequence; the golden replay asserts the
     **exact** `messages` count (raw kinds only) so a dropped raw frame
     fails loudly.
2. Commit: `test(spa): golden replay of a real N2 history page + raw-only equivalence`.

PR 1 checklist: full `npx vitest run`, lint, `tsc -p tsconfig.app.json`,
build. PR title `feat(spa): P-B3.1 — consume Nexen N2 tool events in the
exec-pane reducer`. Body links the spec §4.1–4.3 and the fixture
execution id.

## PR 2 — P-B3.2 header summary, status, duration, facts line

### Task 5a — baseline snapshots of `ToolResultBlock` (unchanged component)

Files: `components/ToolResultBlock.test.tsx`,
`components/__snapshots__/ToolResultBlock.test.tsx.snap` (new).

1. Add four `toMatchSnapshot()` cases against the **unmodified**
   component: collapsed ok, collapsed error, expanded ok, expanded error
   (content `'line one\nline two'`, long content > 80 chars for the `...`
   summary). Commit the `.snap`. No source change in this task.
2. Commit: `test(spa): baseline snapshots for ToolResultBlock before P-B3.2`.

Every later task in PR 2 and PR 3 runs these snapshots **without**
`-u`; a diff means the no-`facts` DOM moved and the task must fix the
component, not the snapshot.

### Task 5 — `tool-summary.ts` (R1)

Files: `lib/nex/tool-summary.ts` (new), `lib/nex/tool-summary.test.ts`
(new), `components/ToolCallBlock.tsx` (move `getSummary` out; behaviour
unchanged).

1. Tests: `toolSummary('Read', input, {primaryArg: {key, value}})` →
   value verbatim (no truncation, even > 80 chars); `known: false` with
   `input {a: 1, b: 'x', c: {d: 1}, e: 2}` → `a: 1, b: x, c: {"d":1}`
   (first three keys, scalars verbatim, objects JSON); `known: false`
   with empty input → `''`; `known: true, primaryArg: null` (F9) → client
   table result (`getSummary`); no entry → client table; entry without
   `primaryArg` / `known` (raw-only) → client table. Existing
   `ToolCallBlock` tests keep passing untouched.
2. Code: `export function toolSummary(tool, input, entry?: Pick<ToolActivity,
   'primaryArg' | 'known'>): string`; `getSummary` moved here and exported
   (used by nothing else). `ToolCallBlock` imports `getSummary` from the
   module for now (Task 6 switches it to `toolSummary`).
3. Commit: `feat(spa): toolSummary — primary_arg first, R10 key fallback, client table last`.

### Task 6 — `ToolCallBlock` R1–R3 + `ToolUseBlock` entry passthrough

Files: `components/ToolCallBlock.tsx`, `components/ToolUseBlock.tsx`,
`components/ToolCallBlock.test.tsx`, `components/ToolUseBlock.test.tsx`.

1. Tests (ToolCallBlock): new optional prop `summaryEntry?: Pick<ToolActivity,
   'primaryArg' | 'known'>` — with `primaryArg` the header shows its
   value (sliced to 80); `activity {status: 'done', startedAt: 100,
   endedAt: 200, durationMs: 26}` → badge `26ms` (not `100ms`);
   `durationMs: null` → falls back to `endedAt − startedAt`; `denied` →
   name has `line-through`, badge `data-testid="tool-denied"` with
   `t('execution.tool.denied')`, wrench icon, no error colour; existing
   snapshots unchanged (run the snapshot tests, do not update them).
   Tests (ToolUseBlock): entry with `primaryArg` → header shows the
   server value even when `block.input` differs.
2. Code: `TimingBadge` uses `durationMs` when `typeof === 'number'`;
   `denied` case; name span class conditional; `summary = rawInput ?? toolSummary(tool, input, summaryEntry)`
   sliced to `SUMMARY_MAX`. `ToolUseBlock` passes `summaryEntry={entry}`.
   (`execution.tool.denied` and the badge itself landed in Task 1.)
3. Commit: `feat(spa): tool call header — server primary_arg, duration_ms, denied rendering`.

### Task 7 — `tool-result-facts.ts` (R4 helper)

Files: `lib/nex/tool-result-facts.ts` (new), `lib/nex/tool-result-facts.test.ts`
(new), locales.

1. Tests: `toolResultFacts(facts, t)` returns `string[]` segments:
   `file.lines 4` → `['4 lines']`; `diff {added 1, removed 1}` → `['+1 −1']`
   (U+2212 minus); `diff {added 0, removed 0, hunks: []}` → `['+0 −0']`;
   `file` + `diff` both → lines first then diff; `output {totalLines 3}`
   alone → `['3 lines']`; `output {totalLines 1}` alone → `[]`;
   `truncated` → appends `'truncated'`; `hasNonText` → appends
   `'non-text'`; undefined facts → `[]`. `t` is a stub returning the
   interpolated en string.
2. Code + i18n keys `execution.tool.lines` ("{{n}} lines" / "{{n}} 行"),
   `execution.tool.truncated` ("truncated" / "已截斷"),
   `execution.tool.non_text` ("non-text" / "含非文字").
3. Commit: `feat(spa): toolResultFacts — line count, +N −M, truncation markers from N2 facts`.

### Task 8 — `ToolResultBlock.facts` + `ConversationMessages` lookup

Files: `components/ToolResultBlock.tsx`, `components/ToolResultBlock.test.tsx`,
`components/ConversationMessages.tsx`, `components/ConversationMessages.test.tsx`.

1. Tests (ToolResultBlock): the Task 5a snapshots still pass unchanged
   (no `facts` → byte-identical DOM); with `facts {file: {lines: 4}}` →
   header contains `data-testid="tool-result-facts"` with `4 lines`; with
   `diff` → `+1 −1`; `facts {status: 'denied'}` + `isError: true` → the
   neutral (non-error) classes, `data-testid="tool-result-denied"` badge
   with `t('execution.tool.denied')`, `Prohibit` icon instead of
   `XCircle`, and `tool-result-content` still shows the raw content (R3
   result override — codex finding 2).
   Tests (ConversationMessages): a `user` `tool_result` block whose
   `tool_use_id` has a `tools` entry with `diff` → facts span present; no
   entry → absent; `tools` undefined → absent.
2. Code: `facts?: ToolResultFacts` prop (`Partial<Pick<ToolActivity,
   'output' | 'file' | 'diff' | 'status'>>` — `status` is required on the
   entry, `Partial` lets tests and callers pass only the facts they have;
   codex finding 6), `const denied = facts?.status === 'denied'` drives
   the colour / icon selection ahead of `isError`, facts span after the summary
   (`text-text-muted tabular-nums flex-shrink-0`, segments joined by
   ` · `); `ConversationMessages` passes `facts={tools?.[block.tool_use_id ?? '']}`.
3. Commit: `feat(spa): tool result header shows N2 facts`.

PR 2 checklist as PR 1. Title `feat(spa): P-B3.2 — tool call summary, status and result facts from N2`.

## PR 3 — P-B3.3 diff view

### Task 9 — `diff-lines.ts` walker

Files: `lib/nex/diff-lines.ts` (new), `lib/nex/diff-lines.test.ts` (new).

1. Tests: `diffRows(hunk)` for the fixture hunk → `[{kind: 'ctx', old: 1,
   new: 1, text: 'hello'}, {kind: 'del', old: 2, new: null, text: 'world'},
   {kind: 'add', old: null, new: 2, text: 'nexen'}, {kind: 'ctx', old: 3,
   new: 3, text: 'three'}]`; a `\ No newline at end of file` line →
   `{kind: 'meta', old: null, new: null, text: 'No newline at end of file'}`
   and does not advance either counter; a second hunk starts from its own
   `old_start` / `new_start`; an unknown first char → treated as context
   (fail-safe, still advances both).
2. Commit: `feat(spa): diffRows — unified-hunk line numbering`.

### Task 10 — `ToolDiffView` mounted in `ToolResultBlock`

Files: `components/ToolDiffView.tsx` (new), `components/ToolDiffView.test.tsx`
(new), `components/ToolResultBlock.tsx`, `components/ToolResultBlock.test.tsx`,
locales.

1. Tests (ToolDiffView): renders one `data-testid="diff-hunk"` per hunk
   with the `@@ -1,3 +1,3 @@` header; rows carry `data-kind` add / del /
   ctx / meta; number cells right-aligned tabular-nums (class assertion);
   `truncated: true` → trailing `data-testid="diff-truncated"` row with
   `t('execution.tool.diff_truncated')`; `hunks: []` → renders nothing
   (null). Tests (ToolResultBlock): expanded body with `facts.diff` that
   has hunks → `ToolDiffView` above the raw content; without → not
   rendered; collapsed → not rendered.
2. Code + i18n `execution.tool.diff_truncated` ("diff truncated by the
   daemon" / "diff 已由 daemon 截斷"). Colours: `bg-[#1f2a1f]` add,
   `bg-[#2a1f1f]` del, same TODO comment as the neighbours.
3. Commit: `feat(spa): ToolDiffView — unified diff with line numbers for Edit / Write results`.

PR 3 checklist as PR 1. Title `feat(spa): P-B3.3 — line-numbered diff view for N2 tool results`.

## After PR 3

- Real-machine acceptance per spec §6 (worktree dev server :5175).
- Follow-up issues (labels: type + `spa`): subagent tool tracking (raw +
  N2 together), console R16 error-expanded default, cost hover per turn
  (R13) as a P-B4 candidate, theme tokens for the tool blocks (existing
  TODO, may already exist — search before opening), **seq guard drops a
  lower-seq live event for good under nexen #83's reorder** (spec §7;
  `bug`, `spa`, references nexen #83).
- Bump PR (`VERSION` + `CHANGELOG.md`), no codex; then `git pull
  --ff-only` on the main checkout.
