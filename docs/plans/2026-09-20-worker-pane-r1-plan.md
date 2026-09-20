# Plan — worker pane R1: the room frame

Spec: `docs/specs/2026-09-20-worker-pane-views-spec.md` v1.0. This plan covers
**R1 only** (spec §11.1: one left edge, turn grouping, one folding rule, the
operation block, subagent nesting, header slimming, the dock, the input).
R2 (chat + the `mode` field), R3 (search / quick replies) and R4 (the §9 wire
batch) get their own plans, in that order, and nothing here may depend on new
wire data (spec §10 Q5).

Anchors measured on `242937a5` (`worktree-pane-design`, which contains all of
`origin/main` at alpha.415). Every path below is relative to the worktree root;
SPA paths are under `spa/`.

## Working rules for every task

- TDD: the failing test first, then the minimum code, then green.
- One commit per task, `git commit --only <paths>` (parallel subagents share
  the index).
- Subagents prefix every Bash with `cd <worktree>/spa && ` for SPA work.
- Type check is `npx tsc --noEmit -p tsconfig.app.json` — a bare `tsc
  --noEmit` is a no-op in this repo.
- Tests `npx vitest run`, lint `pnpm run lint`, build `pnpm run build`.
- Both locale files change together or `src/locales/locale-completeness.test.ts`
  fails.

## PR split

R1 is far past the 800-line / 20-file ceiling, so it ships as seven PRs in
this order. Each one leaves the pane in a working, reviewable state.

| PR | content | est. (added lines) |
|---|---|---|
| **PR-1** | #1264 — the cost panel stops naming the account; the hover summary closes behind the panel | ~90 |
| **PR-2** | the pure layer: the fold rule, call⊕result pairing, the fold memory, `FoldedOutput` | ~630 |
| **PR-3** | `OperationBlock` replaces the two cards; the diff display budget (#1227); #1265 | ~500 added / ~700 deleted |
| **PR-4** | one left edge + the turn container: `RoomTranscript` replaces `ConversationMessages` | ~700 |
| **PR-5** | subagent attribution and nesting (#1263, #1228) | ~500 |
| **PR-6** | header slimming, the worker-info popover, the dock | ~600 |
| **PR-7** | the input: full width, borderless, capped | ~250 |

**Review budget — needs a decision before PR-2 is opened.** CLAUDE.md asks for
R1 + the two-round R2 on every PR; seven PRs would be twenty-one codex runs on
a Pro weekly quota. Proposal: **PR-1** no codex (two small functions and their
call sites); **PR-2, PR-3 and PR-4** get the full R1 + attack + critic — they
carry the rule everything else inherits and the rewrite of the renderer;
**PR-5, PR-6 and PR-7** get R1 only, escalating to the two-round treatment if
R1 returns a critical. Ask the user before PR-2 is opened; if they want the
book followed, the plan is unchanged apart from the number of dispatches.

## Design decisions this plan makes (not in the spec)

1. **Two affordances per operation, not one.** The spec drops the card but
   never says where the raw tool input goes. Today expanding a `ToolCallBlock`
   shows `JSON.stringify(input)`; losing that would lose information. So the
   argument line carries its own `show input` toggle (only when the input has
   keys beyond the primary arg), and the output fold is a separate control.
   Each control does exactly one thing.
2. **`expand all` / `collapse all` lives on the turn's hover strip.** Spec
   §4.2 puts it "on the turn separator", but Q1 removed the separator. A
   control that only appears on hover draws nothing at rest, so Q1 holds.
3. **Fold levels use one clamp mechanism.** A 4 KB single line is as
   unreadable as 400 lines, so preview lines are clamped to
   `FOLD_LINE_MAX_CHARS` and that clamp counts as "something is hidden". No
   second `max-height` mechanism anywhere.
4. **Duration is shown at ≥ 1 s** (spec §3.1.1 #3) until #1229 lands.
5. **Byte length is `text.length`** (UTF-16 units) when N2 gives no
   `total_bytes`. It never under-reports ASCII, and it keeps `foldPlan` free
   of a `TextEncoder` allocation per render.
6. **No new theme tokens.** Room's palette is `status-{error,warning,success}`,
   `text-*`, `border-subtle`, `accent` — which is exactly what clears the
   nine `TODO: theme token` comments the spec §3.2 lists.
7. **Responsive behaviour uses Tailwind 4 container queries**, not a resize
   listener. The pane is the container; jsdom has no `ResizeObserver`, and a
   CSS-only rule needs no stub.
8. **`#1264` masks, it does not remove.** The row's job is to say *which*
   account the quota belongs to, so `wake.gs@gmail.com` renders as
   `wa…@gmail.com` with the full value in `title`.

---

## PR-1 — the cost panel stops printing the account address (#1264)

### T1.1 `maskAccount` (TDD)

- Create `spa/src/lib/nex/mask-account.ts`:

```ts
// spa/src/lib/nex/mask-account.ts — the quota row names the account the host
// is logged in as (#1264). A worker pane ends up in screenshots, so the
// address is masked to its first two characters plus the domain; the full
// value stays reachable through the element's `title`.
const MASK = '…'

export function maskAccount(account: string): string {
  const at = account.lastIndexOf('@')
  if (at <= 0) return account.length <= 4 ? account : account.slice(0, 2) + MASK
  const local = account.slice(0, at)
  const domain = account.slice(at)
  return (local.length <= 2 ? local : local.slice(0, 2) + MASK) + domain
}
```

- Test `spa/src/lib/nex/mask-account.test.ts`:
  `masks the local part of an address` (`wake.gs@gmail.com` →
  `wa…@gmail.com`), `keeps a short local part whole` (`ab@x.io` → `ab@x.io`),
  `masks a bare handle` (`wakeliu` → `wa…`), `leaves a short bare handle`
  (`abcd` → `abcd`), `leaves an empty string` (`''` → `''`), `treats the last
  @ as the separator` (`a@b@c.io` → `a@b@c.io`).

### T1.2 wire it into `CostPanel` (TDD)

- `spa/src/components/execution/CostPanel.tsx:179` — the span becomes

```tsx
<span className="text-text-muted truncate" title={host.active_account}>
  {t('execution.cost.quota', { account: maskAccount(host.active_account) })}
</span>
```

- Test in `spa/src/components/execution/CostPanel.test.tsx`:
  `does not render the raw account address` — render with
  `active_account: 'wake.gs@gmail.com'`, assert
  `screen.queryByText(/wake\.gs@gmail\.com/)` is null and
  `getByTestId('cost-quota')` contains `wa…@gmail.com`.

### T1.3 the cost tooltip closes when the panel opens (spec §3.1.1 #8)

The hover tooltip stays open behind `CostPanel`, repeating the line the panel
already shows.

- `spa/src/components/execution/ExecutionHeader.tsx:90` — the tooltip renders
  only while the panel is closed: `{cost && !costOpen && <HoverTooltip …>}`,
  and `aria-describedby` drops to `undefined` in the same condition so the
  reference never points at an absent node.
- Test in `ExecutionHeader.test.tsx`: `hides the hover summary while the cost
  panel is open` — open the panel, assert the tooltip id is gone from the DOM
  and the button has no `aria-describedby`.

### T1.4 PR-1

- `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json`, `npx vitest run`.
- PR body closes #1264 and answers spec §3.1.1 #8. No codex (one pure
  function, one masked call site, one render condition).

---

## PR-2 — the fold rule and the pairing layer

Pure modules plus one leaf component, all behind tests and wired to nothing.
PR-3 renders them.

### T2.1 `lib/nex/fold.ts` — the volume rule (TDD)

- Create `spa/src/lib/nex/fold.ts`:

```ts
// spa/src/lib/nex/fold.ts — spec §4.2's one folding rule, for every block
// type. Pure: no React, no store. Driven by what N2 already sends
// (output.total_lines / total_bytes / truncated) and falling back to the
// body itself when a block has no N2 overlay.
export const FOLD_WHOLE_MAX_LINES = 6
export const FOLD_WHOLE_MAX_BYTES = 1024
export const FOLD_MEDIUM_MAX_LINES = 40
export const FOLD_PREVIEW_MEDIUM = 6
export const FOLD_PREVIEW_LARGE = 3
/** A preview line longer than this is cut; the cut counts as hidden content. */
export const FOLD_LINE_MAX_CHARS = 400

export type FoldSeverity = 'normal' | 'error'

export interface FoldSource {
  text: string
  /** N2 `output.total_lines`; absent → counted from `text`. */
  totalLines?: number
  /** N2 `output.total_bytes`; absent → `text.length`. */
  totalBytes?: number
  /** N2 `output.truncated` — the daemon itself cut the payload at 8 KB. */
  truncated?: boolean
  /** `error` and `denied` fold one step less (spec §4.2). */
  severity?: FoldSeverity
}

export interface FoldPlan {
  /** Lines to show while collapsed; empty when the body is shown whole. */
  previewLines: string[]
  /** The body's true line count (N2's when it has one). */
  totalLines: number
  /** totalLines − previewLines.length, never below 0. */
  hiddenLines: number
  /** At least one preview line was cut at FOLD_LINE_MAX_CHARS. */
  clamped: boolean
  /** false → render the body whole, draw no affordance. */
  collapsible: boolean
  /** N2 said the daemon truncated the payload; the expanded view says so. */
  daemonTruncated: boolean
}

export function firstLine(text: string): string
export function foldPlan(src: FoldSource): FoldPlan
```

  Implementation rules, in order:
  - `lines = text.length === 0 ? [] : text.split('\n')`; a single trailing
    `'\n'` does not add an empty last line (`'a\n'` is one line).
  - `totalLines = src.totalLines ?? lines.length`,
    `bytes = src.totalBytes ?? src.text.length`.
  - `level = src.truncated || totalLines > FOLD_MEDIUM_MAX_LINES ? 2
      : totalLines > FOLD_WHOLE_MAX_LINES || bytes > FOLD_WHOLE_MAX_BYTES ? 1
      : 0`.
  - `if (src.severity === 'error' && level > 0) level -= 1` — one step less
    folded, never below 0.
  - `take = level === 2 ? FOLD_PREVIEW_LARGE : level === 1 ? FOLD_PREVIEW_MEDIUM : 0`.
  - `previewLines` = first `take` lines, each cut to `FOLD_LINE_MAX_CHARS`
    (`clamped` true if any was cut).
  - `hiddenLines = Math.max(0, totalLines - previewLines.length)`.
  - `collapsible = level > 0 && (hiddenLines > 0 || clamped || daemonTruncated)`;
    when false, `previewLines` is `[]`.
  - `firstLine(text)` = `text.split('\n', 1)[0] ?? ''`, cut to
    `FOLD_LINE_MAX_CHARS` — **never** the lines joined by a space (#1265).

- Test `spa/src/lib/nex/fold.test.ts`:
  `shows a short body whole` (3 lines → `collapsible === false`,
  `previewLines: []`); `folds a medium body to six lines` (20 lines →
  6 preview, `hiddenLines === 14`); `folds a large body to three lines`
  (100 lines → 3 preview, `hiddenLines === 97`); `treats a daemon-truncated
  body as large` (4 lines, `truncated: true` → 3 preview,
  `daemonTruncated === true`); `an error folds one step less`
  (100 lines + `severity: 'error'` → 6 preview); `an error under the medium
  cap is shown whole` (20 lines + error → `collapsible === false`); `a single
  huge line is clamped` (one 5000-char line → `clamped === true`,
  `collapsible === true`, preview line is 400 chars, `hiddenLines === 0`);
  `prefers N2's line count over the local one` (`text` of 3 lines with
  `totalLines: 900` → 3 preview, `hiddenLines === 897`); `handles an empty
  body` (`''` → `totalLines === 0`, not collapsible); `does not count a
  trailing newline as a line` (`'a\n'` → `totalLines === 1`);
  `firstLine takes the first line, not the joined body`
  (`'1 # Pane\n2\n3 - x'` → `'1 # Pane'`); `firstLine clamps a long first
  line`.

### T2.2 `lib/nex/operations.ts` — pairing calls with results (TDD)

A `tool_use` block lives in an assistant message and its `tool_result` in the
next user message. The block renders both together, so the pairing has to be
resolved over the whole list first.

- Create `spa/src/lib/nex/operations.ts`:

```ts
// spa/src/lib/nex/operations.ts — pairs a tool_use block with the
// tool_result that answers it (spec §4.2: one call is one block), across the
// message boundary that separates them. Pure: no React, no store.
import type { StreamMessage } from './message-types'

export interface OperationResult {
  /** The result body as text; a structured content array is flattened to its text blocks. */
  text: string
  isError: boolean
}

export interface OperationIndex {
  /** tool_use_id → the result that answered it. */
  resultsById: Map<string, OperationResult>
  /** tool_use ids that appear as a tool_use block somewhere in the list. */
  callIds: Set<string>
}

/** `string` → itself; `[{type:'text',text}]` → the texts joined by '\n'; anything else → JSON. */
export function toolResultText(content: unknown): string
export function indexOperations(messages: StreamMessage[]): OperationIndex
```

  `toolResultText` is the fix for the raw-JSON hand-back in #1263: today
  `ConversationMessages.tsx:102` does `JSON.stringify(block.content)` for any
  non-string content, which is exactly what a subagent hand-back is.

- Test `spa/src/lib/nex/operations.test.ts`: `pairs a call with the result in
  the next message`; `leaves an unanswered call unpaired` (`resultsById` has
  no entry, `callIds` does); `records a result whose call is not in the list`
  (history page boundary: `resultsById` has it, `callIds` does not);
  `takes the last result when an id repeats`; `flattens a text-block content
  array` (`[{type:'text',text:'done'}]` → `'done'`); `joins several text
  blocks with a newline`; `falls back to JSON for an unknown content shape`;
  `keeps a string content as is`; `ignores a block with no tool_use_id`.

### T2.3 `FoldedOutput` — the fold affordance (TDD)

- Create `spa/src/components/room/FoldedOutput.tsx`:

```tsx
export interface FoldedOutputProps {
  text: string
  plan: FoldPlan
  expanded: boolean
  onToggle: () => void
  /** 'error' tints the body text. */
  tone?: 'normal' | 'error'
}
```

  Collapsed: `<pre>` of `plan.previewLines.join('\n')` then a button
  `data-testid="fold-more"` reading `t('room.fold.more', { n: hiddenLines })`
  — `+166 lines`. When `hiddenLines === 0 && plan.clamped` the button reads
  `t('room.fold.show_all')`. Expanded: the whole `text` plus a
  `data-testid="fold-less"` button, and, when `plan.daemonTruncated`, a
  `<span data-testid="fold-daemon-truncated">` carrying
  `t('room.fold.daemon_truncated')`. `plan.collapsible === false` renders the
  body with no button at all.

- New locale keys in **both** `src/locales/en.json` and `zh-TW.json`
  (`src/locales/locale-completeness.test.ts` fails if only one moves):
  `room.fold.more` (`"+{{n}} lines"` / `"還有 {{n}} 行"`),
  `room.fold.less` (`"collapse"` / `"收合"`),
  `room.fold.show_all` (`"show all"` / `"顯示全部"`),
  `room.fold.daemon_truncated` (`"the daemon cut this output at 8 KB"` /
  `"輸出在 8 KB 處被 daemon 截斷"`).

- Test `spa/src/components/room/FoldedOutput.test.tsx`: `renders the preview
  and the count`; `renders the body whole when it is not collapsible`;
  `calls onToggle`; `shows the daemon-truncation note only when expanded`;
  `says "show all" when only a clamp is hiding content`; `never joins lines
  with a space` (a 3-line body's preview contains `'\n'` — the #1265
  regression guard).

### T2.4 pane-level fold memory (TDD)

Component-local `useState` dies on remount (spec §3.2). The memory lives in
the transcript and is read through a context.

- Create `spa/src/components/room/fold-context.tsx`:

```tsx
export interface FoldStore {
  isExpanded(key: string): boolean
  toggle(key: string): void
  setAll(keys: string[], expanded: boolean): void
}
export const FoldContext = createContext<FoldStore | null>(null)
export function useFoldStore(): FoldStore   // throws outside a provider
export function useFoldMemory(): FoldStore  // the provider's implementation hook
```

  `useFoldMemory` keeps a `useState<Record<string, boolean>>({})`; unknown
  keys read `false`. `setAll` writes every key in one update (expand-all /
  collapse-all).

- Test `spa/src/components/room/fold-context.test.tsx` with
  `renderHook`: `defaults to collapsed`; `toggles one key`; `setAll expands
  every key in one update`; `keeps state across a child remount`.

### T2.5 PR-2

- `npx vitest run`, `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json`.
- Nothing is wired yet: this PR adds the pure layer and one leaf component,
  every one of them covered by its own tests. The PR body says so, and names
  PR-3 as the one that renders them.

---

## PR-3 — the operation block

The pane keeps today's bubble layout in this PR; only what a tool call
looks like changes. That keeps the diff reviewable and lets the block land
before the transcript is rewritten.

### T3.1 `OperationBlock` — the call ⊕ result block (TDD)

- Create `spa/src/components/room/OperationBlock.tsx`. Props:

```tsx
export interface OperationBlockProps {
  tool: string
  input: Record<string, unknown>
  /** Absent → status is derived from `result.isError` alone (a subagent's call, PR-5). */
  activity?: ToolCallActivity
  summaryEntry?: Pick<ToolActivity, 'primaryArg' | 'known'>
  /** N2 facts for the result: output volume, file, diff, status. */
  facts?: ToolResultFacts
  /** null → the call has not been answered yet. */
  result: OperationResult | null
  /** Stable key for the pane-level fold memory (the tool_use id). */
  foldKey: string
}
```

  DOM, in one `<div data-testid="operation-block">` with **no border and no
  background** (spec §3.1.1 #1):
  - header row: the status dot (`<span data-testid="op-dot">`, 6 px, colours
    below), the tool name (`font-semibold text-text-primary`; `line-through
    text-text-muted` when denied, as today), the argument
    (`text-text-muted whitespace-pre-wrap break-all`, **not** truncated —
    spec §4.2 — so `SUMMARY_LIMIT` is no longer applied here), then the
    duration on the right, rendered only when `>= 1000` ms.
  - `show input` toggle (`data-testid="op-input-toggle"`), rendered only when
    `Object.keys(input).length > 0 && !(summaryEntry?.primaryArg && Object.keys(input).length === 1)`.
    Expanded → `<pre data-testid="op-input">` on the rail with
    `JSON.stringify(input, null, 2)`.
  - the rail: `<div data-testid="op-rail" className="ml-[3px] border-l border-border-subtle pl-3">`
    holding the diff (when `facts.diff` has hunks or is truncated) and the
    output.
  - output via `FoldedOutput` (T2.4).
  - error / denied: the rail gets `bg-status-error/10` / `bg-status-warning/10`
    and `severity: 'error'` goes to `foldPlan` — success gets **no** fill
    (spec §3.1.1 #2 inverts today's behaviour).
  - mid-stream (`activity.status === 'streaming'`): the name plus
    `<span data-testid="op-arg-pending">…</span>`, never the half-assembled
    JSON (spec §3.1.1 #7).

  Dot colours: `running`/`streaming` → `CircleNotch` spinner
  (`text-text-secondary animate-spin`); `done` → `bg-status-success`;
  `error` → `bg-status-error`; `denied` → `bg-status-warning`;
  `aborted` → `bg-text-muted`; no activity → `bg-status-error` when
  `result.isError`, else `bg-status-success`, and `bg-text-muted` when there
  is no result yet.

- Test `spa/src/components/room/OperationBlock.test.tsx`: `renders the call
  and its result in one block` (one `operation-block`, no second card);
  `has no border or background on success` (assert the root's className has
  no `border` and no `bg-`); `an error fills the rail`; `a denial fills the
  rail with the warning tone and strikes the name`; `does not truncate a long
  argument` (a 300-char `primary_arg` renders whole); `hides a sub-second
  duration` (`durationMs: 420` → no `op-duration`); `shows a duration at one
  second` (`1200` → `1.2s`); `shows a placeholder while the input streams`
  (no raw JSON in the DOM); `folds a 100-line result to three lines`;
  `keeps an error result one step less folded`; `reveals the raw input on
  demand`; `renders a diff above the output`; `renders an unanswered call
  with no rail`.

### T3.2 the diff display budget and the last theme tokens (#1227) (TDD)

Spec §4.4 asks for `ToolDiffView` "with the display budget from #1227
applied: fold hunks beyond the first N lines, per §4.2's rule". Today it
renders every hunk it is given, up to the daemon's 2000-line cap.

- `spa/src/components/ToolDiffView.tsx` moves to
  `spa/src/components/room/ToolDiffView.tsx` and gains:
  - a budget: rows are emitted until `DIFF_PREVIEW_ROWS = 20` is reached
    (counting every `diffRows` row across hunks, header rows excluded); the
    remainder collapses behind one `data-testid="diff-more"` button reading
    `t('room.fold.more', { n: remaining })`, using the same copy as every
    other fold. A diff at or under the budget draws no button.
  - expansion is remembered through the same `useFoldStore`, keyed
    `` `${foldKey}:diff` `` — so a diff's state survives a remount like every
    other block's.
  - the two hard-coded row tints become `bg-status-success/10` and
    `bg-status-error/10`, which clears the last `TODO: theme token`
    (`ToolDiffView.tsx:4,17,18`) and finishes spec §3.2's theme debt list.
- `ToolResultBlock`'s old import site is already gone (T3.3 deletes it);
  `OperationBlock` imports the new path.
- Tests in `spa/src/components/room/ToolDiffView.test.tsx` (moved from
  `components/`): the existing rendering tests unchanged, plus `renders every
  row of a small diff with no button`; `folds a diff past the budget`
  (30 rows → 20 rendered, button says 10); `expands to the full diff`;
  `keeps the daemon-truncation note visible while collapsed`; `tints add and
  del rows with theme tokens` (no `bg-[#` in the className).

### T3.3 swap the block into `ConversationMessages` (TDD)

- `spa/src/components/ConversationMessages.tsx`:
  - call `indexOperations(messages)` in a `useMemo`;
  - wrap the list in `<FoldContext.Provider value={useFoldMemory()}>`;
  - a `tool_use` block renders `OperationBlock` with
    `result={index.resultsById.get(block.id) ?? null}` and the `facts` looked
    up the same own-key way `ToolResultBlock` is looked up today
    (`Object.hasOwn(tools, id)`);
  - a `tool_result` block whose `tool_use_id` is in `index.callIds` renders
    `null` (its call already showed it); one that is not renders an orphan
    `OperationBlock` with `tool={facts?.file?.path ?? t('execution.tool.unknown')}`
    and `input={{}}`.
  - `ToolUseBlock.tsx`, `ToolCallBlock.tsx`, `ToolResultBlock.tsx` and their
    tests are **deleted** in this task; `PartialMessageGroup.tsx:11,31-34`
    switches its streaming `tool_use` to `OperationBlock` with
    `activity={{status:'streaming', rawInput: block.partialJson}}`,
    `result={null}`, `foldKey={`partial-${block.index}`}`.
    `SUMMARY_LIMIT` stays exported from `tool-summary.ts` (the R10 preview
    still uses it); `SUMMARY_MAX` goes away with `ToolCallBlock`.
- Tests: update `spa/src/components/ConversationMessages.test.tsx` — `pairs a
  call with its result into one block` (one `operation-block`, no
  `tool-result-block`); `renders an orphan result on its own`; `remembers a
  block's expansion across a re-render`. Update
  `spa/src/components/PartialMessageGroup.test.tsx` for the new child.
- New locale key in **both** `src/locales/en.json` and `zh-TW.json`:
  `room.op.show_input` (`"input"` / `"輸入"`).

### T3.4 PR-3

- `npx vitest run`, `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json`,
  `pnpm run build`.
- Real-machine check on the worktree's own dev server
  (`pnpm dev --port 5175 --host 100.64.0.2`), **not** the main checkout's
  :5174 — a mutation run on a live page would reach the real daemon.
- The diff is ~500 added and ~700 deleted: the deletions are the three
  components the new block replaces (`ToolCallBlock`, `ToolResultBlock`,
  `ToolUseBlock`) and their tests. The 800-line ceiling is counted on the
  added side; the PR body says this explicitly so a reviewer does not read
  the total as a split failure.
- PR body closes #1265 and #1227 and states which of spec §3.1.1's findings
  it answers (1, 2, 3, 5, 7).

---

## PR-4 — one left edge and the turn container

### T4.1 `lib/nex/turns.ts` — turn boundaries from the message list (TDD)

Turn boundaries are derivable from `messages` alone, so R1 needs no new wire
data: a turn opens on the user line that `execution.message_accepted` (or the
delegate brief) appended, and runs to the next one.

- Create `spa/src/lib/nex/turns.ts`:

```ts
// spa/src/lib/nex/turns.ts — spec §4.1: a turn is a container, not a
// decoration. Derived from the durable message list alone (R1 may not depend
// on new wire data): a turn opens at the user text line the daemon appended
// for message_accepted / the delegate brief, and ends where the next one
// begins. Pure: no React, no store.
import type { StreamMessage } from './message-types'

/** The sentinel CC sends for an interrupt; it is a user text block but not a turn opener. */
export const INTERRUPT_TEXT = '[Request interrupted by user]'

export interface RoomTurn {
  /** Index into `messages` of the first message in this turn. */
  start: number
  /** Exclusive end. */
  end: number
  /** The opening user line's index, or null for a leading orphan group. */
  openerIndex: number | null
}

export function isTurnOpener(msg: StreamMessage): boolean
export function groupTurns(messages: StreamMessage[]): RoomTurn[]
```

  `isTurnOpener`: `msg.type === 'user'`, `msg.parent_tool_use_id == null`
  (a subagent's prompt never opens a turn — #1263), and its content has a
  `text` block whose text is not `INTERRUPT_TEXT`. A slash command **does**
  open a turn.

- Test `spa/src/lib/nex/turns.test.ts`: `groups a single turn`; `starts a new
  turn at each user line`; `puts leading assistant messages in an opener-less
  group`; `does not open a turn on a tool_result-only user message`; `does not
  open a turn on the interrupt sentinel`; `opens a turn on a slash command`;
  `does not open a turn on a subagent prompt` (`parent_tool_use_id` set);
  `returns an empty array for an empty list`; `covers every index exactly once`
  (property: the turns' ranges partition `[0, messages.length)`).

### T4.2 `RoomTurn` — the container and its hover strip (TDD)

- Create `spa/src/components/room/RoomTurnGroup.tsx`:

```tsx
export interface RoomTurnGroupProps {
  index: number
  /** Fold keys of every operation in this turn (for expand-all / collapse-all). */
  foldKeys: string[]
  children: ReactNode
}
```

  A `<section data-testid="room-turn" data-turn-index={index}>` with
  **no border, no rule, no per-turn duration or cost** (spec Q1). A
  `group-hover:opacity-100 opacity-0` strip in the top-right carries
  `expand all` / `collapse all`, wired to `useFoldStore().setAll`.

- Test `spa/src/components/room/RoomTurnGroup.test.tsx`: `draws no separator`
  (root className has no `border-t` and no `divide`); `shows no per-turn cost
  or duration`; `expand all sets every fold key`; `collapse all clears them`.

### T4.3 `RoomTranscript` — one left edge (TDD)

- Create `spa/src/components/room/RoomTranscript.tsx`, taking over from
  `ConversationMessages` with the same props minus nothing (the pane is the
  only caller since P-D removed Stream mode — verified: `grep -rn
  "ConversationMessages" src --include='*.tsx' | grep -v test` returns only
  `execution/ExecutionView.tsx`).

  Layout rules:
  - the scroll container keeps today's auto-scroll effect verbatim
    (`messages`, `scrollKey`, `partialVersion` deps);
  - everything renders at **one x**: no `justify-end`, no `max-w-[75%]`, no
    `max-w-[90%]`. Prose is capped by measure only
    (`max-w-[90ch]`); code, output, diffs and tables get the full width;
  - a user line is `<div data-testid="room-user-line">` with a gutter mark
    (`<span data-testid="room-user-mark" className="bg-accent">`, a 2 px
    vertical bar) and `text-text-primary font-medium` — **not** a bubble;
  - the interrupt sentinel and the slash command keep their meaning but lose
    the bubble geometry and the hard-coded hexes: interrupt →
    `text-status-error italic` with the `Prohibit` icon; slash command →
    `text-status-warning font-mono` with the `TerminalWindow` icon. This
    clears the `TODO: theme token` at lines 117, 129, 130;
  - assistant prose keeps `MessageBubble`'s markdown body but the component is
    reduced to the assistant arm and renamed `RoomProse.tsx`
    (`role` prop removed, `streaming` kept). `MessageBubble.tsx` and its test
    are deleted; the user arm has no reader left.
  - `ExecutionView.tsx:178-185`'s optimistic `pendingLocal` bubble moves to
    the same left edge with the same gutter mark, styled as a user line with
    `opacity-60`;
  - thinking renders **only** when `block.thinking` is non-empty (spec §4.3 —
    it already does; the change is that `RoomThinking` shows
    `t('room.thinking', { words })` instead of a bare caret, and nothing at
    all for an empty block). `ThinkingBlock.tsx` is renamed
    `room/RoomThinking.tsx` and gains the word count; `ThinkingIndicator`
    is untouched.

- Test `spa/src/components/room/RoomTranscript.test.tsx` (adapted from
  `ConversationMessages.test.tsx`, which is deleted with its component):
  `renders every message at one left edge` (no element in the tree has
  `justify-end`); `renders a user line with a gutter mark, not a bubble`
  (no `user-bubble` test id anywhere); `caps prose at a reading measure but
  not output` (`room-prose` has `max-w-[90ch]`, `operation-block` does not);
  `groups messages into turn containers` (two user lines → two
  `room-turn`); `renders the interrupt sentinel without a bubble`;
  `renders a slash command at the left edge`; `renders nothing for an empty
  thinking block`; `renders a word count for a thinking block with text`;
  `keeps the optimistic pending line at the left edge`.

### T4.4 point the pane at `RoomTranscript` (TDD)

- `spa/src/components/execution/ExecutionView.tsx:11,175-186` — import and
  render `RoomTranscript`; the `children` slot keeps the `pendingLocal` line.
- Delete `spa/src/components/ConversationMessages.tsx`,
  `ConversationMessages.test.tsx`, `MessageBubble.tsx`,
  `MessageBubble.test.tsx`, `ThinkingBlock.tsx`, `ThinkingBlock.test.tsx`
  (the last two live on as `room/RoomProse.tsx`, `room/RoomThinking.tsx`).
- `spa/src/components/execution/ExecutionView.test.tsx`: the assertions that
  name `user-bubble` become `room-user-line`.
- New locale keys (both files): `room.thinking`
  (`"Thought · {{words}} words"` / `"思考 · {{words}} 字"`),
  `room.turn.expand_all` (`"expand all"` / `"全部展開"`),
  `room.turn.collapse_all` (`"collapse all"` / `"全部收合"`).

### T4.5 PR-4

- Full gate (vitest / lint / tsc / build) plus the worktree dev server check
  against a real worker on mlab: run one turn with a Bash call, a Read, an
  Edit and a failing command, and confirm by screenshot that there is one
  left edge, no card frames, and the failure is the loudest thing on screen.

---

## PR-5 — subagent attribution and nesting (#1263, #1228)

### T5.1 `parent_tool_use_id` on the message types (TDD)

- `spa/src/lib/nex/message-types.ts:20-37` — `AssistantMessage` and
  `UserMessage` each gain
  `/** CC parent_tool_use_id — non-null on a subagent's own frames. */
  parent_tool_use_id?: string | null`, matching `ResultMessage:55`.
- Test: a type-level test is not meaningful here; T5.2's index tests cover it.

### T5.2 `indexOperations` learns the parent link (TDD)

- `spa/src/lib/nex/operations.ts` — `OperationIndex` gains
  `childrenByParent: Map<string, number[]>` (message indexes, ascending, whose
  `parent_tool_use_id` matches) and `childIndexes: Set<number>` (every index
  that belongs to some child, so the top level can skip them).
- Test in `operations.test.ts`: `collects a subagent's frames under its Task`;
  `keeps child indexes out of the top level`; `handles a child whose parent id
  matches no call in the list`; `keeps children in seq order`.

### T5.3 `SubagentBlock` — the nested rail (TDD)

- Create `spa/src/components/room/SubagentBlock.tsx`. Rendered by
  `OperationBlock` when `childIndexes` for its `foldKey` is non-empty (the
  `Task` / `Agent` case). Collapsed: one line —
  `t('room.subagent.summary', { name, tools, duration })` →
  `Task · analyse notes.md · 8 tools · 12s`. Expanded: the child's own
  messages, rendered by the same `RoomTranscript` body renderer, one indent
  deeper on a second rail.
  - To avoid a cycle, the per-message renderer moves out of `RoomTranscript`
    into `room/render-message.tsx` exporting
    `renderMessage(msg: StreamMessage, ctx: RenderCtx): ReactNode`;
    `RoomTranscript` and `SubagentBlock` both call it. `RenderCtx` carries
    `{ index: OperationIndex; tools?: Record<string, ToolActivity>; now: number; keyPrefix: string; depth: number }`.
  - The hand-back result renders `toolResultText(content)`, never
    `JSON.stringify` (#1263).
- Test `spa/src/components/room/SubagentBlock.test.tsx`: `folds a subagent to
  one line`; `expands into the child's own blocks`; `renders the child's
  tools one indent deeper`; `renders the hand-back as text, not JSON`;
  `renders the child's prompt as a subagent line, not the user's`;
  `puts the hand-back after the child's own output`.

### T5.4 the reducer stops dropping a subagent's tool events (#1228) (TDD)

- `spa/src/lib/nex/event-reducer.ts:150-160` — the guard at :154 currently
  returns `s` for **every** non-lifecycle frame with a non-null
  `parent_tool_use_id`, which drops the child's N2 tool events and its raw
  tool starts/ends as well. Narrow it: a child frame still may not touch
  `turnLive`, `partial`, or end the turn, but it **does** get
  `recordN2ToolUse` / `recordN2ToolResult` / `recordToolStarts` /
  `recordToolEnds` (tool_use ids are globally unique, so the same `tools` map
  is the right home).

```ts
const child = !isLifecycleKind(ev.kind) && p.parent_tool_use_id != null
if (ev.kind === 'tool_use') return recordN2ToolUse(s, p, ev.created_at)
if (ev.kind === 'tool_result') return recordN2ToolResult(s, p, ev.created_at)
if (child) {
  if (ev.kind === 'assistant') return recordToolStarts(s, p, ev.created_at)
  if (ev.kind === 'user') return recordToolEnds(s, p, ev.created_at)
  return s
}
if (TURN_ENDING_KINDS.has(ev.kind)) return endTurn(s, ev.created_at)
…
```

- Test in `spa/src/lib/nex/event-reducer.test.ts`: `records a subagent's tool
  timing`; `a subagent's result does not end the main turn` (`turnLive` stays
  true — the existing guard's real job); `a subagent's frames do not touch the
  main partial`; `a subagent's N2 facts land on its own tool entry`;
  `endTurn still aborts a child's running tool` (the parent turn ended, so the
  child cannot still be running).
- New locale key (both files): `room.subagent.summary`
  (`"{{name}} · {{tools}} tools"` / `"{{name}} · {{tools}} 個工具"`).

### T5.5 PR-5

- Full gate plus a real-machine run that delegates a `Task`, confirming the
  child's prompt is not a user line, its tools sit on the nested rail, and the
  hand-back reads as text. PR body closes #1263 and #1228.

---

## PR-6 — the header, the worker-info popover, and the dock

### T6.1 slim the header (TDD)

- `spa/src/components/execution/ExecutionHeader.tsx` becomes one row: the
  state dot + state, the worker's name (the cwd basename, `font-medium
  text-text-primary`, opening the popover on click), the cost button (P-B4,
  unchanged), then the actions. `observers`, `lease` and `sse` are **removed**
  from the header — they move to the dock (T6.3) — and `turn_count` is dropped
  outright (spec §4.7).
- Actions: `Interrupt` and `Terminate` stay as today, but `Terminate` carries
  `text-status-error` from the start (not only after the first click; the
  confirm step is unchanged), and `Take to terminal` is separated by a
  `<span className="w-px h-4 bg-border-subtle" />` because it changes the
  pane's binding rather than interrupting a turn (spec §1).
- Narrow: the header root is a `@container`; the name gets `truncate min-w-0`,
  and at `@max-md` the cost button and the two lease-backed actions collapse
  into an overflow menu (`data-testid="header-overflow"`, the existing
  `FloatingPanel`).
- Tests in `ExecutionHeader.test.tsx`: `shows state, name, cost and the
  actions`; `does not show observers, lease, sse or turns`; `styles terminate
  as destructive before the first click`; `separates take-to-terminal from
  the interrupt actions`; `renders an overflow trigger for narrow widths`.

### T6.2 the worker-info popover (TDD)

- Create `spa/src/components/execution/WorkerInfoPanel.tsx` on the existing
  `FloatingPanel`, anchored to the name: provider, effective/requested
  profile, full cwd (selectable), session id, archived flag.
- Test `WorkerInfoPanel.test.tsx`: `shows the provider, profile and full
  cwd`; `shows the session id when there is one`; `closes on Escape`.
- Locale keys: `room.info.provider`, `room.info.profile`, `room.info.cwd`,
  `room.info.session`, `room.info.archived`.

### T6.3 the dock (TDD)

- Create `spa/src/components/room/WorkerDock.tsx`, rendered **inside the
  pane** (spec Q3) between the transcript and the input, with its own hairline
  top border. Collapsed to one row:
  `● live · 2 observers · lease: you`. Expanded: a small table, one row per
  fact. Props: `{ sse, observers, lease, isMine }`.
  Spec §4.6's background-shell rows need wire ask A1 and are **not** in R1;
  the component takes no `shells` prop yet, so R4 adds one rather than
  reshaping it.
- `ExecutionView.tsx` renders it above `StreamInput`.
- Test `WorkerDock.test.tsx`: `shows the live state, observers and lease in
  one row`; `expands into a table`; `marks the lease as yours`; `says no
  lease when there is none`.
- Locale keys: `room.dock.observers`, `room.dock.lease`, `room.dock.sse`,
  `room.dock.expand`, `room.dock.collapse`.

### T6.4 PR-6

- Full gate plus a narrow-width screenshot at ~900 px (the `k-narrow.png`
  case) showing the header no longer overflowing.

---

## PR-7 — the input

### T7.1 `WorkerInput` — full width, borderless, capped (TDD)

- `git mv spa/src/components/StreamInput.tsx spa/src/components/room/WorkerInput.tsx`
  (same for the test), then:
  - drop `onAttach`, `showAttach` and `onHandoffToTerm` and the button row
    they rendered — no caller passes them (`grep -rn "showAttach\|onHandoffToTerm"
    src --include='*.tsx' | grep -v test` returns only `ExecutionView.tsx:194`
    passing `showAttach={false}`);
  - the wrapper loses `mx-2 mb-2 border rounded-xl` and becomes
    `w-full border-t border-border-subtle bg-surface-input` — full width, one
    hairline separator above it (spec §3.1.1 #6);
  - `autoGrow` clamps: `const MAX_INPUT_PX = 200;
    ta.style.height = Math.min(ta.scrollHeight, MAX_INPUT_PX) + 'px';
    ta.style.overflowY = ta.scrollHeight > MAX_INPUT_PX ? 'auto' : 'hidden'`;
  - `stream.input.placeholder` is renamed `worker.input.placeholder` in both
    locale files (the last `stream.*` key this pane reads).
- Tests in `WorkerInput.test.tsx`: the existing behaviour tests move over
  unchanged (Enter sends, Shift+Enter newlines, disabled state, `initialValue`
  seeding, focus), plus `draws no border box` (className has no `rounded-xl`,
  no `border ` other than `border-t`); `caps its height` (a 40-line value →
  `style.height === '200px'` and `overflowY === 'auto'`); `renders no attach
  button`.

### T7.2 PR-7

- Full gate. PR body notes that search and quick replies (spec §4.8's Collie
  borrowings) are R3, not this PR.

---

## After R1

- Plan R2 (chat + `mode` on `ExecutionContent`), then R3 (search, quick
  replies), then R4 once the §9 batch has been sent to Nexen and shipped.
- Follow-ups this plan deliberately leaves open: #1226, #1227 (diff display
  budget — `ToolDiffView` is reused as is), #1229 (ms durations; the ≥ 1 s
  rule above is the interim), #1234, #1235.
