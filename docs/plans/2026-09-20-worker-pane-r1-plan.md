# Plan — worker pane R1: the room frame

Spec: `docs/specs/2026-09-20-worker-pane-views-spec.md` v1.0. This plan covers
**R1 only** (spec §11.1: one left edge, turn grouping, one folding rule, the
operation block, subagent nesting, header slimming, the dock, the input).
R2 (chat + the `mode` field), R3 (search / quick replies) and R4 (the §9 wire
batch) get their own plans, in that order, and nothing here may depend on new
wire data (spec §10 Q5).

Anchors measured on `242937a5` (alpha.415) and **re-verified on `84a11276`
(alpha.446, 2026-09-25)**: `git diff --stat 242937a5 origin/main` over every
component and `lib/nex` module this plan names returns no change to any of
them, so every line number below still holds. The two files that did move are
`spa/src/locales/{en,zh-TW}.json` (heavy i18n churn on other lines — new keys
are appended against the current content, never onto a remembered position)
and `spa/src/types/tab.ts` (+37, R2's `mode` field territory, untouched by
R1). Every path below is relative to the worktree root; SPA paths are under
`spa/`.

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

## Codex plan review (`task-mufvbewy-wlbk4h`, 2026-09-25)

One round against this plan plus the spec, `gpt-5.6-sol`. Fourteen findings,
two critical, every one at confidence ≥ 0.91, so all of them entered the main
table. **All fourteen are folded into the plan below** — the two critical ones
would have made PR-4 fail to compile (#1: `PartialMessageGroup` still imports
the components PR-4 deletes) and left a whole class of turn silently merged
into its neighbour (#2: a `message_accepted` with no text leaves no trace in
`messages`). The rest are recorded where they land: #3 moved the
`parent_tool_use_id` declaration into PR-2, #4 put thinking through the fold
rule, #5 replaced an invented 20-row diff budget with the shared ladder, #6
replaced the turn's `foldKeys` prop with registration, #7 made pairing
positional so a repeated `tool_use_id` cannot show one result twice, #8 added
the out-of-order and exactly-once pairing tests, #9 added the four ticker
integration tests, #10 gave the preview a byte budget, #11 stopped the fold
affordance promising lines it cannot reveal, #12 re-asserted the contracts of
the three deleted components, #13 fixed a self-contradiction about #1227, and
#14 dropped the `title` attribute PR-1 was going to keep the address in.

## PR-2 review (R1 `review-mufwx2bu-g5pzhj`, attack `review-mufx0mwj-93whae`, critic `review-mufx6d8a-oipti6`)

Five findings, all agreed by the critic, all folded into the tasks above
before the fixes were written. Four of them are the same failure in different
clothes — **an affordance, a pairing or a boundary that claims something the
data does not support**:

- **A1** — `daemonTruncated` alone made a body "collapsible", so a truncated
  body the preview already showed whole rendered a `+0 lines` button.
- **A2** — the bidirectional pairing queue let a stale orphan result be
  claimed by a call that arrived later, so the call rendered the wrong body.
  This one **overturns plan-review #8**, which had asserted that a result
  arriving before its call should still pair: the critic showed the case
  cannot occur (`useExecutionSubscription.ts:173-192` finishes paging history
  before opening the SSE; `applyDurableEvent` only accepts increasing seq).
  Pairing is forward-only.
- **A3** — de-duplicating `turnStarts` by message index swallowed the second
  of two boundaries that share an index, which is precisely the textless-turn
  case the field was added for.
- **A4** — `unregister(key)` swept every turn, so one turn's unmount stripped
  a key another turn still had mounted.
- **R1-1 / A5** — the 1 KB threshold counted UTF-16 code units, so a 1.2 KB
  Han body measured 400 and was shown whole; and the preview cut by UTF-16
  index, which can leave a lone surrogate.

A5 is a design decision of this plan's own (#5, "byte length is
`text.length`") being wrong on its face for this machine: the argument was
that UTF-16 length never under-reports **ASCII**, in a project whose agent
output is largely Chinese.

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
3. **The preview has one budget, in lines and in bytes.** A 4 KB single line
   is as unreadable as 400 lines, and three 350-character lines are over the
   spec's 1 KB bar while being under its 6-line bar. So the preview takes
   lines until either budget runs out, cuts the line that crosses it, and
   counts any cut as "something is hidden". No second `max-height` mechanism
   anywhere, and no separate diff budget either (T3.2).
4. **Duration is shown at ≥ 1 s** (spec §3.1.1 #3) until #1229 lands.
5. **Byte length is UTF-8 bytes** when N2 gives no `total_bytes`, measured
   by a code-point walk rather than a `TextEncoder` allocation per render.
   An earlier draft used `text.length` and argued it never under-reports
   ASCII — true, and irrelevant here: agent output on this machine is largely
   Chinese, where it under-reports threefold.
6. **No new theme tokens.** Room's palette is `status-{error,warning,success}`,
   `text-*`, `border-subtle`, `accent` — which is exactly what clears the
   nine `TODO: theme token` comments the spec §3.2 lists.
7. **Responsive behaviour uses Tailwind 4 container queries**, not a resize
   listener. The pane is the container; jsdom has no `ResizeObserver`, and a
   CSS-only rule needs no stub.
8. **`#1264` masks, and the full value goes nowhere.** The row's job is to
   say *which* account the quota belongs to, so `wake.gs@gmail.com` renders
   as `wa…@gmail.com` — and the address is **not** kept in a `title`
   attribute. A leak into the DOM is still a leak: hover shows it and any
   capture reads it, which is the whole of what #1264 is about (codex plan
   review #14).

9. **A turn's boundaries are recorded, not inferred.** The reducer writes
   `turnStarts` when the daemon says a turn opened, so a boundary survives an
   `execution.message_accepted` whose payload carried no text. Deriving the
   boundary from "the next user-looking message" was the first draft and it
   lost exactly those turns (codex plan review #2).

10. **Every foldable thing registers itself.** Expand-all operates on what
    the fold store was told is inside a turn, not on a list of keys a caller
    remembered to pass down — which is how a diff or a thinking block ends up
    silently exempt (codex plan review #6).

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
  @ as the separator` (`a@b@c.io` → `a@…@c.io`: the local part is `a@b`,
  which is over the two-character floor, so it masks — an earlier draft of
  this line wrote `a@b@c.io`, which is what splitting on the *first* `@`
  would give and therefore contradicted the case's own name).

  Wherever a test claims the address is nowhere in the DOM, assert on
  `document.body.innerHTML`, not on the `container` that `render` returns:
  `FloatingPanel` portals into the body
  (`spa/src/components/FloatingPanel.tsx:262`), so a container-scoped
  assertion passes while the panel leaks.

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
  - `localLines = lines.length`, `totalLines = src.totalLines ?? localLines`,
    `bytes = src.totalBytes ?? utf8Length(src.text)`. **UTF-8 bytes, not
    `String.length`.** N2's `total_bytes` is a byte count and spec §4.2's
    first row says 1 KB, so the fallback has to measure the same thing: 400
    Han characters are ~1200 bytes and `String.length` calls them 400, which
    on this machine — where agent output is largely Chinese — silently shows
    a 1.2 KB body whole (R1's P2, attack A5, critic agreed).
  - `level = src.truncated || totalLines > FOLD_MEDIUM_MAX_LINES ? 2
      : totalLines > FOLD_WHOLE_MAX_LINES || bytes > FOLD_WHOLE_MAX_BYTES ? 1
      : 0`.
  - `if (src.severity === 'error' && level > 0) level -= 1` — one step less
    folded, never below 0.
  - `take = level === 2 ? FOLD_PREVIEW_LARGE : level === 1 ? FOLD_PREVIEW_MEDIUM : 0`.
  - **The preview has a byte budget as well as a line budget.** Lines are
    taken until either `take` lines or `FOLD_WHOLE_MAX_BYTES` characters are
    consumed (again UTF-8 bytes); the line that crosses the byte budget is
    cut at the remaining room, and any line longer than
    `FOLD_LINE_MAX_CHARS` is cut there first. **Every cut lands on a Unicode
    code-point boundary** — slicing by UTF-16 index leaves a lone surrogate
    and the browser draws a replacement glyph (attack A5). Every cut sets
    `clamped`. This is what makes the spec table's first row
    hold: a body over 1 KB is never "shown whole", however few lines it has
    (codex plan review #10 — three 350-character lines used to pass the
    line-count test and come back `collapsible: false`).
  - `hiddenLines = Math.max(0, localLines - previewLines.length)` when the
    body is folded, and **exactly `0` when `collapsible` is false** — the
    field means "how much this affordance is hiding", and a body shown whole
    hides nothing. Without that clause the formula returns the body's own
    line count for a short body (`previewLines` is `[]` there), which reads
    as `+3 lines` to any consumer that renders the count before checking
    `collapsible`. Counted
    against **the body we actually have**, never against N2's `total_lines`.
    An affordance must be able to reveal what it promises (codex plan review
    #11: a 3-line body with `total_lines: 900` used to advertise `+897 lines`
    and then expand to three lines).
  - `daemonTruncated = src.truncated === true || totalLines > localLines` —
    when N2 counts more lines than the body carries, the daemon cut it
    whatever the flag says, and the expanded view has to say so.
  - `collapsible = level > 0 && (hiddenLines > 0 || clamped)` — **not**
    `daemonTruncated`. An affordance exists to reveal something, and a body
    the daemon cut but the preview shows whole has nothing left to reveal;
    the old clause produced a `+0 lines` button that expanded to the same
    text (attack A1). The truncation is still reported — `daemonTruncated`
    stays on the plan and `FoldedOutput` prints its note beside the body
    whether or not there is a button. When `collapsible` is false,
    `previewLines` is `[]`.
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
  **`folds a body that is over a kilobyte in six lines`** (three 350-char
  lines → `collapsible === true`, `clamped === true`, the preview is at most
  1024 characters — the #10 guard, and it must fail before the byte budget is
  written); **`counts only the lines it can reveal`** (3-line `text` with
  `totalLines: 900` → `hiddenLines === 0` and `daemonTruncated === true`,
  **not** `hiddenLines === 897` — the #11 guard); **`uses N2's count to pick
  the fold level`** (a **10-line** body with `totalLines: 900` →
  `previewLines.length === 3`: ten local lines alone would be medium and
  preview six, so a preview of three proves the level came from N2's count.
  The 3-line body an earlier draft used here could not tell level 1 from
  level 2 at all — its preview was the whole body either way — and once
  `collapsible` stopped counting `daemonTruncated` it could not even stay
  collapsible. The case was named after something it never checked.);
  **`hides nothing when it shows the body whole`** (3-line body →
  `collapsible === false` **and** `hiddenLines === 0`);
  **`does not offer to expand an empty daemon-truncated body`**
  (`{ text: '', truncated: true }` → `collapsible === false`,
  `daemonTruncated === true` — the A1 guard);
  **`does not offer to expand a truncated body the preview shows whole`**
  (`{ text: 'a\nb\nc\nd', truncated: true, severity: 'error' }` →
  `collapsible === false`);
  **`measures a Han body in UTF-8 bytes`** (`'中'.repeat(400)` on one line →
  `collapsible === true`, because it is ~1200 bytes — the A5 guard, and it
  must fail while the fallback is `String.length`);
  **`measures an emoji body in UTF-8 bytes`** (`'😀'.repeat(300)`);
  **`never cuts a surrogate pair`** (`'a'.repeat(399) + '😀' + 'x'.repeat(10)`
  with `totalBytes: 2000` → the preview ends on a whole code point); `handles an empty
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

/** `${messageIndex}:${blockIndex}` — a block's position, which is unique even when a tool_use_id is not. */
export type BlockKey = string
export const blockKey = (m: number, b: number): BlockKey => `${m}:${b}`

export interface OperationIndex {
  /** The call block's own position → the result that answers THAT call. */
  resultForCall: Map<BlockKey, OperationResult>
  /** Result blocks already shown by a call; the renderer skips exactly these. */
  consumedResults: Set<BlockKey>
}

/** `string` → itself; `[{type:'text',text}]` → the texts joined by '\n'; anything else → JSON. */
export function toolResultText(content: unknown): string
export function indexOperations(messages: StreamMessage[]): OperationIndex
```

  **Pairing is one-to-one and positional** (codex plan review #7). An
  id-keyed map pairs *every* call that shares an id with the *same* result,
  so a repeated `tool_use_id` renders one result twice while the second
  result block vanishes. The rule instead is: walk the list once in order
  keeping one FIFO per id of calls still waiting for an answer, and give
  each `tool_result` to the **oldest unanswered call with that id**. A result
  with no waiting call is an orphan and **stays** one.

  **Pairing is forward-only.** An earlier draft queued results too, so a
  result could pair with a call that arrives *later*; the attack review found
  what that does to `[result X 'stale'], [call X], [result X 'fresh']` — the
  call renders the stale body and the fresh one floats off as an orphan. The
  critic then established that the reverse case cannot arise at all:
  `useExecutionSubscription.ts:173-192` pages history forward from `after=0`
  and only opens the SSE once history is in, and `applyDurableEvent` accepts
  by strictly increasing `lastSeq` (`event-reducer.ts:186-200`), so a
  `tool_result` is never in the final array ahead of its `tool_use`. The
  bidirectional rule generalised over a path that does not exist, and bought
  a wrong body for it. A call with
  no result is simply absent from `resultForCall`; a result with no call is
  absent from `consumedResults` and renders as an orphan. The reducer's own
  duplicate-id merge (`tool-activity.ts:216`) keeps one `ToolActivity` for
  both calls — that is the N2 overlay's business and is left alone; only the
  raw body is paired positionally.

  `toolResultText` is the fix for the raw-JSON hand-back in #1263: today
  `ConversationMessages.tsx:102` does `JSON.stringify(block.content)` for any
  non-string content, which is exactly what a subagent hand-back is.

- Test `spa/src/lib/nex/operations.test.ts`: `pairs a call with the result in
  the next message`; `leaves an unanswered call unpaired` (no
  `resultForCall` entry); `records a result whose call is not in the list`
  (history page boundary: not in `consumedResults`, so it renders as an
  orphan); **`gives each of two calls sharing an id its own result`**
  (two `tool_use` blocks with id `X`, two results → the first call gets the
  first result, the second the second, and **both** result blocks are
  consumed — the #7 guard); **`leaves the second of two same-id calls
  unanswered when only one result arrives`**; **`leaves a result that precedes its call
  as an orphan and gives the call the result that follows it`**
  (`[result X 'stale'], [call X], [result X 'fresh']` → the call shows
  `fresh`, `stale` renders as an orphan — the A2 guard); **`consumes every
  result exactly once`** (property over a mixed list:
  `consumedResults.size + orphanCount === total tool_result blocks`);
  `flattens a text-block content
  array` (`[{type:'text',text:'done'}]` → `'done'`); `joins several text
  blocks with a newline`; `falls back to JSON for an unknown content shape`;
  `keeps a string content as is`; `ignores a block with no tool_use_id`.

### T2.3 the reducer records turn starts; the message types learn `parent_tool_use_id` (TDD)

Spec §4.1 says turn boundaries are **explicit in the data, not inferred**.
Deriving them from "the next visible user line" is an inference, and codex
plan review #2 found where it breaks: `execution.message_accepted` and
`execution.delegated` only append a bubble when the payload carries `text` /
`brief` (`event-reducer.ts:190,198` — a site-wide stream strips them), so a
real turn boundary can leave no trace in `messages` at all. The reducer is
already looking at those events, so it records the boundary itself. This is
not new wire data (spec §10 Q5): it is an event the SPA has always received.

- `spa/src/lib/nex/event-reducer.ts` — `ExecutionState` gains

```ts
  /**
   * Index into `messages` where each turn begins, in ascending order
   * (spec §4.1). Written when the daemon says a turn opened
   * (execution.message_accepted / execution.delegated), **before** the
   * bubble those events may or may not append — so the boundary is exact
   * even when the payload carried no text.
   */
  turnStarts: number[]
```

  `defaultExecutionState()` seeds it `[]`. Both arms push
  `next.messages.length` before appending their bubble. **Repeated indexes
  are kept.** An earlier draft de-duplicated by index, reasoning that a
  second `message_accepted` at the same length meant the first appended
  nothing — but that is exactly the case this field exists for: a turn whose
  payload carried no text, ended, and was followed by another turn produces
  two boundaries at the same index, and dropping one merges two real turns
  (attack A3, critic agreed, citing spec §4.1). The seq guard in
  `applyDurableEvent` already makes it impossible to apply one event twice,
  so a dedupe protects nothing. `groupTurns` therefore has to tolerate an
  empty range (`start === end`).

- `spa/src/lib/nex/message-types.ts:20-37` — `AssistantMessage` and
  `UserMessage` each gain
  `/** CC parent_tool_use_id — non-null on a subagent's own frames. */
  parent_tool_use_id?: string | null`, matching `ResultMessage:55`. The field
  lands here, in PR-2, rather than in PR-5: `turns.ts` (PR-4) and
  `indexOperations` both read it, so leaving the declaration until PR-5 would
  make PR-4 depend on a contract that does not exist yet (codex plan review
  #3).

- Tests in `spa/src/lib/nex/event-reducer.test.ts`: `records a turn start on
  message_accepted`; `records a turn start even when the payload has no text`
  (the boundary the message list cannot show); `records a turn start on
  delegated`; **`records two boundaries at the same index when the first turn appended
  nothing`** (`message_accepted` with no text → `execution.terminal` →
  `message_accepted` with text ⇒ `turnStarts` is `[0, 0]`, **not** `[0]` —
  the A3 guard); `keeps turn starts in
  ascending order across a history replay`; `a subagent frame does not record
  a turn start`; `turn starts survive a duplicate seq` (the seq guard returns
  the same state).

### T2.4 `FoldedOutput` — the fold affordance (TDD)

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
  `data-testid="fold-less"` button. `plan.collapsible === false` renders the
  body with no button at all. **The `data-testid="fold-daemon-truncated"`
  note carrying `t('room.fold.daemon_truncated')` renders whenever
  `plan.daemonTruncated` is set — collapsed, expanded, or with no button at
  all.** It reports a fact about the payload, not about the fold, and after
  A1 a truncated body often has no button to hang it on.

- New locale keys in **both** `src/locales/en.json` and `zh-TW.json`
  (`src/locales/locale-completeness.test.ts` fails if only one moves):
  `room.fold.more` (`"+{{n}} lines"` / `"還有 {{n}} 行"`),
  `room.fold.less` (`"collapse"` / `"收合"`),
  `room.fold.show_all` (`"show all"` / `"顯示全部"`),
  `room.fold.daemon_truncated` (`"the daemon cut this output at 8 KB"` /
  `"輸出在 8 KB 處被 daemon 截斷"`).

- Test `spa/src/components/room/FoldedOutput.test.tsx`: `renders the preview
  and the count`; `renders the body whole when it is not collapsible`;
  `calls onToggle`; **`shows the daemon-truncation note even when there is no button`**
  (`collapsible: false`, `daemonTruncated: true` → the note renders and
  `fold-more` does not — the A1 guard at the render level);
  `says "show all" when only a clamp is hiding content`; `never joins lines
  with a space` (a 3-line body's preview contains `'\n'` — the #1265
  regression guard).

### T2.5 pane-level fold memory (TDD)

Component-local `useState` dies on remount (spec §3.2). The memory lives in
the transcript and is read through a context.

- Create `spa/src/components/room/fold-context.tsx`:

```tsx
export interface FoldStore {
  isExpanded(key: string): boolean
  toggle(key: string): void
  /** Every foldable thing announces itself, so expand-all knows what "all" is. */
  register(turnIndex: number, key: string): void
  /** Takes the turn too: the same key can be live in two turns at once. */
  unregister(turnIndex: number, key: string): void
  setTurn(turnIndex: number, expanded: boolean): void
}
export const FoldContext = createContext<FoldStore | null>(null)
/** Provided by RoomTurnGroup so a nested block does not have to be told its turn. */
export const TurnIndexContext = createContext<number>(-1)
export function useFoldStore(): FoldStore   // throws outside a provider
export function useFoldMemory(): FoldStore  // the provider's implementation hook
/** Registers `key` under the surrounding turn for the component's lifetime and returns [expanded, toggle]. */
export function useFold(key: string): [boolean, () => void]
```

  `useFoldMemory` keeps a `useState<Record<string, boolean>>({})` plus a
  `useRef<Map<number, Map<string, number>>>` of the keys registered per turn
  (`setTurn` iterates `keys.keys()`; iterating the Map itself yields
  `[key, count]` pairs and would write entry arrays into `expanded`)
  **with a reference count** — nothing guarantees a key is unique across the
  pane, and an `unregister` that swept every turn let one turn's unmount
  strip a key another turn still had mounted (attack A4). `unregister`
  decrements its own `(turnIndex, key)` and drops the entry at zero;
  unknown keys read `false`. `setTurn` writes every key registered under that
  turn in one update.

  **Registration, not a precomputed key list** (codex plan review #6). A turn
  holds more foldable things than its operations: each operation's output,
  its raw input, its diff (`${key}:diff`), each thinking block, and later
  each subagent. A `foldKeys: string[]` prop passed down from the turn would
  have to enumerate all of them and would silently miss the ones added later
  — which is exactly how expand-all would come to move the outputs and leave
  the diffs closed. Every foldable component calls `useFold`, which registers
  under `TurnIndexContext` on mount and unregisters on unmount, so
  "everything in this turn" is a fact the store holds rather than a list a
  caller maintains.

- Test `spa/src/components/room/fold-context.test.tsx` with
  `renderHook` and a small harness component: `defaults to collapsed`;
  `toggles one key`; `setTurn expands every key registered in that turn in
  one update`; `setTurn leaves another turn alone`; **`expands a diff key and
  a thinking key registered by nested components`** (the #6 guard: the
  harness renders an operation, a diff and a thinking block, and one
  `setTurn` opens all three); `unregisters a key when its component
  unmounts`; **`keeps a key registered in one turn when the same key
  unmounts in another`** (two turns each mount `useFold('op-1')`, turn 0's
  unmounts, `setTurn(1, true)` still reaches it — the A4 guard);
  **`keeps a key registered while another component in the same turn still
  holds it`** (the reference count's only guard: a per-turn `Set` passes the
  A4 test above, so without this one the implementation can regress to a Set
  and the suite stays green);
  `keeps state across a child remount`.

### T2.6 PR-2

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

  **Contracts inherited from the three components PR-3 deletes** (codex plan
  review #12 — these are P-B2 / P-B3 behaviours whose only guards are the
  test files T3.3 removes, so they are re-asserted here or they are gone):
  `falls back to the unknown-tool label when the block has no name`
  (`execution.tool.unknown` — **note the real fallback lives at the call
  site**, `ToolUseBlock.tsx:26`'s `block.name ?? t(…)`, and `OperationBlock`
  receives an already-resolved `tool: string`. The block keeps an empty-string
  guard of its own, but **T3.3's call site still needs the `??`**: `??` fires
  on an absent name, the block's guard on an empty one);
  `an N2 status outranks the raw frame's is_error`
  (**`facts.status: 'done'`** with `isError: true` → the ok dot. An earlier
  draft wrote `'ok'`, which is not a member of `ToolActivity['status']` at
  all — it is `ToolResultBlock`'s internal *tone*);
  `a raw result does not downgrade a denial` (`facts.status: 'denied'` with
  `isError: false` stays denied — asserted on the dot colour, the rail's
  warning fill and the struck-through name, since the new DOM has no denied
  badge); `shows no duration when **either** clock is unknown`
  (`startedAt: 0` **and**, separately, `endedAt: 0`; the existing tests guard
  each one, and "both" would leave half the contract uncovered);
  `prefers the daemon's durationMs over the clock difference` (**the two
  numbers have to straddle the 1 s threshold**: a clock difference of 6.2 s
  against `durationMs: 1200` shows `1.2s`. The existing test's 26 ms against
  100 ms would be invisible either way and assert nothing);
  `shows the aborted badge` (the wrench icon it also checked today is gone —
  the status dot replaced it);
  `renders a truncated diff that has no hunks` (the daemon dropped them all,
  so only the note shows); `renders the elapsed timer only while running` (two test ids,
  `op-elapsed` and `op-duration`, which is how the existing tests tell the
  running badge from the finished one).

  **A negative assertion needs a positive control.** Three of these read
  "shows no X"; against a stub that renders nothing they pass without
  touching the implementation. Each is paired with the case that *does* show
  X (1000 ms shows a duration, a second input key shows the toggle, a known
  clock shows the badge), and the pair is what makes the guard real.

### T3.2 the diff display budget and the last theme tokens (#1227) (TDD)

Spec §4.4 asks for `ToolDiffView` "with the display budget from #1227
applied: fold hunks beyond the first N lines, per §4.2's rule". Today it
renders every hunk it is given, up to the daemon's 2000-line cap.

**The budget is `foldPlan`, not a second set of numbers** (codex plan review
#5). An earlier draft of this task invented `DIFF_PREVIEW_ROWS = 20`, which
would have been exactly the third truncation mechanism spec §3.2 lists as the
defect, and R1's whole claim is "one folding rule for every block type".

- `spa/src/components/ToolDiffView.tsx` moves to
  `spa/src/components/room/ToolDiffView.tsx` and gains:
  - a budget taken from the shared ladder: flatten the hunks to their
    `diffRows` rows (hunk headers excluded from the count, kept with their
    hunk), then call `foldPlan({ text: rows.map(r => r.text).join('\n'),
    totalLines: rows.length, truncated: diff.truncated })` and render the
    first `plan.previewLines.length` **rows** — so a diff of ≤ 6 rows shows
    whole, ≤ 40 shows 6, more shows 3, and a daemon-truncated diff shows 3,
    identically to every output in the pane. The remainder collapses behind
    one `data-testid="diff-more"` button reading
    `t('room.fold.more', { n: plan.hiddenLines })`. `foldPlan` decides the
    rows; only the rendering of a row is this component's business.
  - expansion goes through `useFold(`${foldKey}:diff`)`, so the key is
    registered with the surrounding turn and expand-all reaches it (T2.5).
  - the two hard-coded row tints become `bg-status-success/10` and
    `bg-status-error/10`, which clears the last `TODO: theme token`
    (`ToolDiffView.tsx:4,17,18`) and finishes spec §3.2's theme debt list.
- `ToolResultBlock`'s old import site is already gone (T3.3 deletes it);
  `OperationBlock` imports the new path.
- Tests in `spa/src/components/room/ToolDiffView.test.tsx` (moved from
  `components/`): the existing rendering tests unchanged, plus `renders every
  row of a diff of six rows with no button`; `folds a 30-row diff to six
  rows` (button says 24 — the same ladder as an output of 30 lines);
  `folds a 100-row diff to three rows`; `folds a daemon-truncated diff to
  three rows whatever its size`; `expands to the full diff`; `keeps the
  daemon-truncation note visible while collapsed`; `tints add and del rows
  with theme tokens` (no `bg-[#` in the className); **`folds a diff and an
  output of the same size identically`** (the one-rule guard: 30 rows and 30
  lines produce the same `hiddenLines`).

### T3.3 swap the block into `ConversationMessages` (TDD)

- `spa/src/components/ConversationMessages.tsx`:
  - call `indexOperations(messages)` in a `useMemo`;
  - wrap the list in `<FoldContext.Provider value={useFoldMemory()}>`;
  - a `tool_use` block renders `OperationBlock` with
    `result={index.resultForCall.get(blockKey(i, j)) ?? null}` and the
    `facts` looked up the same own-key way `ToolResultBlock` is looked up
    today (`Object.hasOwn(tools, id)`);
  - **the own-key lookup contract lands here** (it has no home in
    `OperationBlock`, which takes no `tools` map): `ConversationMessages.test.tsx`
    must assert that a `tools` map keyed `constructor` does not reach
    `Object.prototype`, and that a prototype-chain entry is ignored. Those are
    the two guards `ToolUseBlock.test.tsx` holds today, and deleting that file
    without re-asserting them here loses them for good.
  - a `tool_result` block whose `blockKey(i, j)` is in
    `index.consumedResults` renders `null` (its call already showed it); one
    that is not renders an orphan
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
  block's expansion across a re-render`; **`renders each of two same-id calls
  with its own result`** (the #7 guard at the render level: two blocks, two
  distinct bodies, and no leftover standalone result); **`renders every
  result exactly once`** over a list mixing paired, orphan and duplicate
  cases. Update
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

### T4.1 `lib/nex/turns.ts` — turns from the boundaries the reducer recorded (TDD)

The boundaries come from `state.turnStarts` (PR-2 T2.3), which the reducer
wrote when the daemon said a turn opened. `turns.ts` only shapes them into
ranges and finds each range's opening line. Nothing is inferred from "the
next user-looking message", so the exceptions codex plan review #2 found
(an `execution.message_accepted` whose payload carried no `text`, a
site-wide stream that strips `brief`) keep their boundary.

- Create `spa/src/lib/nex/turns.ts`:

```ts
// spa/src/lib/nex/turns.ts — spec §4.1: a turn is a container, not a
// decoration, and its boundaries are explicit in the data. The reducer
// records them in ExecutionState.turnStarts when execution.message_accepted
// / execution.delegated arrive; this module turns that list into ranges and
// locates each range's opening user line. Pure: no React, no store.
import type { StreamMessage } from './message-types'

/** The sentinel CC sends for an interrupt; it is a user text block but not an opening line. */
export const INTERRUPT_TEXT = '[Request interrupted by user]'

export interface RoomTurn {
  /** Index into `messages` of the first message in this turn. */
  start: number
  /** Exclusive end. */
  end: number
  /**
   * The opening user line's index, or null when the turn has none — a
   * leading group before the first boundary, or a boundary whose payload
   * carried no text. The turn container exists either way.
   */
  openerIndex: number | null
}

/** A user message that reads as the human's own line (not a tool result, not the interrupt sentinel, not a subagent's prompt). */
export function isOpeningLine(msg: StreamMessage): boolean
export function groupTurns(messages: StreamMessage[], turnStarts: readonly number[]): RoomTurn[]
```

  `groupTurns`: the ranges are `turnStarts` (clamped to
  `[0, messages.length]`, sorted, **repeats kept** — a repeat is an empty
  turn, not a duplicate) with `end` = the next start
  or `messages.length`; a leading range is prepended when the first start is
  not 0. `openerIndex` is the first index in the range for which
  `isOpeningLine` holds, else null. `isOpeningLine` requires
  `msg.type === 'user'`, `msg.parent_tool_use_id == null` (a subagent's
  prompt is never the human's line — #1263), and a `text` block whose text is
  not `INTERRUPT_TEXT`. A slash command **is** an opening line.

- Test `spa/src/lib/nex/turns.test.ts`: `groups a single turn`; `starts a new
  turn at each recorded boundary`; `puts messages before the first boundary
  in a leading group`; **`keeps a boundary whose payload had no text`** (a
  start index with no opening line → a turn with `openerIndex: null`, **not**
  a merge into the previous turn — the #2 guard); `does not treat a
  tool_result-only user message as the opening line`; `does not treat the
  interrupt sentinel as the opening line`; `treats a slash command as the
  opening line`; `does not treat a subagent prompt as the opening line`
  (`parent_tool_use_id` set); `returns an empty array for an empty list`;
  `ignores a boundary past the end of the list`; **`keeps an empty turn as its own range`** (`turnStarts` `[0, 0]` → two
  ranges, the first empty); `covers every index exactly once` (property: the ranges
  partition `[0, messages.length)`).

### T4.2 `RoomTurn` — the container and its hover strip (TDD)

- Create `spa/src/components/room/RoomTurnGroup.tsx`:

```tsx
export interface RoomTurnGroupProps {
  index: number
  children: ReactNode
}
```

  A `<section data-testid="room-turn" data-turn-index={index}>` that provides
  `TurnIndexContext` (T2.5) to everything inside it, with **no border, no
  rule, no per-turn duration or cost** (spec Q1). A
  `group-hover:opacity-100 opacity-0` strip in the top-right carries
  `expand all` / `collapse all`, wired to `useFoldStore().setTurn(index, …)`.
  It takes **no** `foldKeys` prop: the store learns the keys from the blocks
  that register them, which is the only way the diffs and thinking blocks are
  reached too (codex plan review #6).

- Test `spa/src/components/room/RoomTurnGroup.test.tsx`: `draws no separator`
  (root className has no `border-t` and no `divide`); `shows no per-turn cost
  or duration`; **`expand all opens an operation, its diff and a thinking
  block inside the turn`** (render all three as real children, click once,
  assert all three expanded); `collapse all closes them`; `leaves a
  neighbouring turn untouched`.

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
    it already does; the change is what happens when it is not empty).
    `ThinkingBlock.tsx` becomes `room/RoomThinking.tsx` and is folded by the
    same rule as everything else (codex plan review #4): its header reads
    `t('room.thinking', { words })`, its default state comes from
    `foldPlan({ text: content })` — a two-line thought is simply shown, a
    long one folds to six or three lines with the usual `+N lines` — and its
    expansion goes through `useFold(`${key}:thinking`)` so it survives a
    remount and answers expand-all. Today's `useState(false)` is the third
    of the three ad-hoc fold mechanisms spec §3.2 lists, and renaming it
    without this change would leave that defect in place.
    `ThinkingIndicator` is untouched.

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
  **`shows a short thought whole and folds a long one`** (the #4 guard: a
  2-line thought has no `fold-more`, a 100-line thought shows three lines);
  **`keeps the optimistic pending line inside a turn container`** (the
  pendingLocal line is not in `messages` and has no recorded boundary, so
  `RoomTranscript` renders it inside a provisional `room-turn` whose
  `data-turn-index` is one past the last real turn — otherwise the line sits
  outside every container and expand-all cannot see anything it holds, and
  the container would jump when `message_accepted` lands); `swaps the
  provisional turn for the real one when the accepted message arrives`
  (re-render with the durable bubble → still one turn, not two).

### T4.4 point the pane at `RoomTranscript` (TDD)

- `spa/src/components/execution/ExecutionView.tsx:11,175-186` — import and
  render `RoomTranscript`; the `children` slot keeps the `pendingLocal` line.
- **Rewire `PartialMessageGroup` before deleting anything** (codex plan
  review #1, the one that would have made this PR fail to compile):
  `PartialMessageGroup.tsx:9,11` still imports `MessageBubble` and
  `ThinkingBlock`, and `:27,29` render them — PR-3 only replaced its
  `ToolCallBlock`. Point `:27` at `RoomProse` (dropping the `role` prop) and
  `:29` at `RoomThinking`, keeping `streaming` on both so the typewriter and
  its cursor behave exactly as P-B2 built them (spec §3: the typewriter is
  on the "do not touch" list).
- Only then delete `spa/src/components/ConversationMessages.tsx`,
  `ConversationMessages.test.tsx`, `MessageBubble.tsx`,
  `MessageBubble.test.tsx`, `ThinkingBlock.tsx`, `ThinkingBlock.test.tsx`
  (the last two live on as `room/RoomProse.tsx`, `room/RoomThinking.tsx`).
- `spa/src/components/PartialMessageGroup.test.tsx` gains the partial
  regression guards the rename would otherwise drop: `renders a streaming
  text block with the cursor`; `renders a streaming thinking block with the
  cursor`; `renders blocks in ascending index order`; `renders nothing for an
  invisible block`. Run `grep -rn "MessageBubble\|ThinkingBlock" src` after
  the deletions and paste the (empty) output into the task report — an import
  left behind is a compile error, not a test failure, and `tsc` is the only
  thing that catches it.
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

`parent_tool_use_id` is already declared on `AssistantMessage` /
`UserMessage` — PR-2 T2.3 added it, because `turns.ts` in PR-4 reads it too
(codex plan review #3).

### T5.1 `indexOperations` learns the parent link (TDD)

- `spa/src/lib/nex/operations.ts` — `OperationIndex` gains
  `childrenByParent: Map<string, number[]>` (message indexes, ascending, whose
  `parent_tool_use_id` matches) and `childIndexes: Set<number>` (every index
  that belongs to some child, so the top level can skip them).
- Test in `operations.test.ts`: `collects a subagent's frames under its Task`;
  `keeps child indexes out of the top level`; `handles a child whose parent id
  matches no call in the list`; `keeps children in seq order`.

### T5.2 `SubagentBlock` — the nested rail (TDD)

- Create `spa/src/components/room/SubagentBlock.tsx`. Rendered by
  `OperationBlock` when `childrenByParent` has entries for its tool_use id (the
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

### T5.3 the reducer stops dropping a subagent's tool events (#1228) (TDD)

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

- **`ExecutionView`'s ticker now sees child tools, and that is a behaviour
  change no reducer test can catch** (codex plan review #9). `anyRunning` is
  `Object.values(st.tools).some(status === 'running')` over the whole map
  (`ExecutionView.tsx:114`), and it drives both `useElapsedTicker` and —
  through `showThinking` — the dots. Four integration tests in
  `spa/src/components/execution/ExecutionView.test.tsx`, each driving the
  store through real frames:
  `the ticker stops when a child's tool result arrives`;
  `the main result stops the ticker even when a child tool never reported`
  (the abort inside `endTurn` is the backstop);
  `a child's own result frame does not stop the ticker while the parent turn
  is still running`;
  `a running child tool suppresses the thinking dots` — which is the intended
  reading of `showThinking`: something is visibly happening, it is just
  happening one rail down.
- New locale key (both files): `room.subagent.summary`
  (`"{{name}} · {{tools}} tools"` / `"{{name}} · {{tools}} 個工具"`).

### T5.4 PR-5

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
- Follow-ups this plan deliberately leaves open: #1226, #1229 (ms durations;
  the ≥ 1 s rule above is the interim), #1234, #1235. **#1227 is closed by
  PR-3 T3.2**, not deferred — an earlier draft of this section said both
  (codex plan review #13).
