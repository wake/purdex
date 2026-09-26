# Plan — worker pane R2: chat, the `mode` field, the switch

Spec: `docs/specs/2026-09-20-worker-pane-views-spec.md` v1.0 — §1 (vocabulary,
the `mode` field), §5 (chat), §6 (switching), §4.7 (header). R1 shipped as
alpha.448–454 (plan `docs/plans/2026-09-20-worker-pane-r1-plan.md`); R2 builds
on its components and adds no wire data (spec §10 Q5).

Anchors measured on `72fe188d` (alpha.453 + PR-7 base) in worktree `pane-r2`.
Every path is relative to the worktree root; SPA paths are under `spa/`.

## User decisions for R2 (2026-09-26, do not reopen)

| # | decision |
|---|---|
| D1 | `mode` **syncs across devices** with the tab (Profile Sync carries the layout as-is). Switching to chat on the phone switches that same tab on the desktop. |
| D2 | The switch is **one view menu shared with Take to terminal** (spec §1's option): items 指揮室 / 聊天 / 終端機; the terminal item reads as an action (it interrupts a turn and moves the session). |
| D3 | (spec §6, Q2) `room` is always the default; nothing picks chat automatically. |

## Facts this plan relies on (measured)

- `PaneContent`'s execution arm: `spa/src/types/tab.ts:124`
  `{ kind: 'execution'; executionId: string; host?: string; from?: ExecutionFrom }`;
  `ExecutionContent` at `:134`.
- Creators of `kind: 'execution'`: `lib/nex/handoff.ts:78-80`
  (`executionContentFor`), `components/executions/ExecutionsView.tsx:56`,
  `hooks/useHeadlessLaunchSubmit.ts:67`, `hooks/useRouteSync.ts:115`,
  `lib/deeplink/deeplinkResolver.ts:23`. None needs to set `mode`: absent
  means `room` (D3), so no creator changes and no migration
  (alpha: no persist migration — project rule).
- `contentMatches` (`lib/pane-utils.ts:59-62`) compares `executionId` + `host`
  only, so `openSingletonTab` finds an existing pane whatever its mode — the
  mode must stay out of it.
- Persistence: `useTabStore` persists `tabs[*].layout` whole
  (`stores/useTabStore.ts:995-1013`, v3, migrations spread `...content`).
- Profile Sync: `lib/profile/projections.ts:48-51` whitelists
  `tabs.*.layout` and strips only `sizes` at any depth; `isLayoutShape`
  (`lib/profile/sections.ts:165-180`) checks only `content.kind`;
  `host-identity.ts:509-529` maps execution content with `{ ...content, host }`.
  So `mode` travels untouched — D1 needs no projection change, therefore no
  `SECTION_SCHEMA_ORDINAL` bump. T1.1 pins this with a round-trip test.
- Updating content: `setPaneContent(tabId, paneId, content)` replaces the
  whole content (`stores/useTabStore.ts:699-718`).
- `ExecutionView` gets content via `ExecutionPaneWrapper`
  (`lib/register-modules/index.tsx:102-118`), which passes `executionId`,
  `from`, `tabId`, `paneId` — not the content object.
- `ExecutionHeader` (`components/execution/ExecutionHeader.tsx`): Take to
  terminal is `data-testid="take-back"` at `:163-170` (wide) and
  `overflow-take-back` at `:187-194` (narrow overflow).
- Room building blocks to reuse: `lib/nex/turns.ts` (`groupTurns`,
  `isOpeningLine`, `INTERRUPT_TEXT`), `lib/nex/operations.ts`
  (`indexOperations`, `childrenByParent`, `childIndexes`, `blockKey`),
  `components/room/{OperationBlock,ToolDiffView,FoldedOutput,fold-context,
  SubagentBlock,MessageRow,render-message,RoomProse}`.
- Aigora (`~/Workspace/wake/aigora/packages/web/src/styles.css:2280`): the AI
  bubble is `width: fit-content; border-radius: 8px; padding: 7px 11px` on a
  faint tinted surface with a hairline border. The human side is flat there,
  but spec §2/§5 ask for left/right bubbles, so R2 keeps a right-hand bubble
  for the user and borrows Aigora's geometry for both.
- Narrow: `hooks/useIsMobile.ts` exists but nothing imports it; the header
  already uses Tailwind container queries (`@container`, `@max-md:`). Chat
  uses container queries too, so a narrow pane on a desktop behaves like a
  phone.

## Working rules for every task

Same as R1: TDD, one commit per task with `git commit --only`, subagents
prefix every Bash with `cd <worktree>/spa && `, type check with
`npx tsc --noEmit -p tsconfig.app.json`, both locale files change together,
Phosphor icons, **no kill-type commands**.

## PR split

| PR | content | est. (added, non-test) |
|---|---|---|
| **R2-A** | the `mode` field and its setter; the view menu; chat renders prose, user lines, typewriter (own streaming renderer, T1.3b); header and dock chat variants | ~450 |
| **R2-B** | the shared operation classifier (T2.0); chat's tool line per turn, the `Edited …` line, the failed-tool line, expand in place | ~350 |

Plan review (Claude reviewer, standing in for codex while its quota is out
until 2026-09-30): 5 findings, all applied — #1 turn-group chrome (T1.3),
#2 streaming blocks (T1.3b, critical), #3 controlled view menu (T1.2),
#4 shared classifier (T2.0), #5 chat header branch (T1.2).

Review: R2-A gets R1 + attack + critic (it changes the pane content contract
that Profile Sync carries); R2-B gets R1 only, escalating on a critical.

---

## R2-A

### T1.1 `mode` on the execution content (TDD)

- `spa/src/types/tab.ts:124` — the execution arm gains
  `mode?: ExecutionViewMode`, with
  `export type ExecutionViewMode = 'room' | 'chat'` beside `ExecutionFrom`.
  Optional: absent reads as `room` (D3). The comment states that it is a
  **view**, not a binding (spec §1) — terminal is not a member.
- `spa/src/lib/nex/view-mode.ts` (new):
  `export function viewModeOf(content: ExecutionContent): ExecutionViewMode`
  (`content.mode === 'chat' ? 'chat' : 'room'` — anything unknown from an
  older or newer client reads as room) and
  `export function withViewMode(content: ExecutionContent, mode: ExecutionViewMode): ExecutionContent`
  (spreads, never drops `from` / `host`).
- `ExecutionPaneWrapper` passes `mode={viewModeOf(content)}` and an
  `onModeChange` that calls
  `useTabStore.getState().setPaneContent(tabId, pane.id, withViewMode(content, m))`
  — read the **current** content from the store at call time, not the
  render-time closure, so a concurrent `from` or host remap is not undone.
- Tests:
  - `lib/nex/view-mode.test.ts`: `reads an absent mode as room`; `reads an
    unknown mode as room`; `keeps from and host when setting the mode`.
  - `lib/pane-utils.test.ts`: `an execution pane matches whatever its mode`
    (room vs chat content with the same id/host → match).
  - `lib/profile/*.test.ts` (whichever file already round-trips a tabs
    payload): `carries an execution pane's mode through a tabs round trip`
    (project → apply → the same `mode`), and through the host-id remap
    (`mapContent`) — this is D1's guard and the reason no ordinal bump is
    needed. If the round trip does drop it, **stop and report**: that is a
    projection change and needs the ordinal bump + wire marker the file's
    header describes.
  - `register-modules` (or `ExecutionView.test.tsx`, whichever mounts the
    wrapper): `switching the view writes mode to the pane content` and
    `switching keeps the pane's from`.

### T1.2 the view menu (TDD)

- `spa/src/components/execution/ViewModeMenu.tsx` (new), on the existing
  `FloatingPanel`, **controlled** like `CostPanel` / `WorkerInfoPanel`:
  `ExecutionHeader` owns `viewOpen` and the trigger's ref, and passes
  `anchorRef` + `onClose`. That is what lets the header's existing
  anchor-lost effect (`ExecutionHeader.tsx:88-110`, which `FloatingPanel`
  does not do by itself) close and refocus it — a self-contained menu would
  be invisible to that effect (plan review #3). Trigger: the current view's icon + label
  (`ListBullets` for 指揮室 — `Rows` already means a split layout in
  `StatusBar`/`TitleBar` — and `ChatsCircle` for 聊天; Phosphor), `data-testid="view-mode"`.
  Items: `view-mode-room`, `view-mode-chat` (radio semantics:
  `role="menuitemradio"`, `aria-checked`), a separator, then
  `view-mode-terminal` — present only when `onTakeBack` is, labelled with the
  existing `takeback.button` text and a `Terminal` icon, styled as an action
  (not a radio), disabled while `takeBackBusy`.
- `ExecutionHeader` gains `mode` and `onModeChange` props. The wide
  `take-back` button (`:163-170`) is **replaced** by the menu trigger, behind
  the same separator. At `@max-md` the trigger hides and the overflow panel
  (`:171-199`) shows the same three items under a small "view" label,
  replacing `overflow-take-back`. The breakpoint-close / refocus logic added
  in PR-6 covers the new panel the same way it covers the cost panel (the
  anchor-lost check must include the view menu's anchor).
- **The chat branch of the header** (spec §5 "header keeps state + cost
  only"; plan review #5) is built here, since `mode` is already threaded
  through: when `mode === 'chat'` the header renders state, the cost button
  and the overflow trigger at **every** width — no name / info popover, no
  wide action row, no inline view trigger — and the overflow holds
  Interrupt, Terminate and the three view items. This is a `mode` branch,
  not a breakpoint, so it is asserted directly (no container-query mock).
- Locale: `room.view.label` (`"View"` / `"顯示方式"`), `room.view.room`
  (`"Room"` / `"指揮室"`), `room.view.chat` (`"Chat"` / `"聊天"`).
- Tests (`ViewModeMenu.test.tsx`, `ExecutionHeader.test.tsx`):
  `marks the current view`; `choosing chat calls onModeChange('chat')`;
  `the terminal item calls onTakeBack and is not a radio`; `has no terminal
  item without onTakeBack`; `the header no longer draws a separate take-back
  button`; `the overflow lists the three view items at narrow widths`;
  `the view menu closes when its anchor loses its box` (reuse PR-6's
  ResizeObserver mock); `chat's header shows state, cost and the overflow
  only`; `chat's overflow holds interrupt, terminate and the view items at
  wide widths too`.
- Existing tests that click `take-back` / `overflow-take-back` move to
  `view-mode-terminal` (grep both ids across `src`).

### T1.3 `ChatTranscript` — bubbles, prose and user lines (TDD)

- `spa/src/components/chat/ChatTranscript.tsx` (new) takes the same props as
  `RoomTranscript` minus `showThinking` (chat never shows thinking) and
  renders, per spec §5:
  - the **agent** on the left: `ChatBubble side="agent"` wrapping
    `RoomProse` (markdown, `streaming` kept for the typewriter);
  - **you** on the right: `ChatBubble side="user"`, plain text (never
    markdown — same rule as `RoomUserLine`);
  - bubble geometry from Aigora: `rounded-lg px-[11px] py-[7px]
    w-fit`, max width `max-w-[85%] @md:max-w-[70ch]`; the agent bubble on
    `bg-surface-secondary` with a `border-border-subtle` hairline, the user
    bubble on `bg-accent-muted` (existing token). The transcript root is `@container`.
  - **thinking**: nothing, in durable and partial alike (spec §5: "a turn
    that is only thinking shows the typewriter, then nothing"). Read as: while
    a thought streams, chat shows only the `ThinkingIndicator` dots, never the
    thought's text; once finalised, nothing. Today the dots switch **off** the
    moment a thinking block gets text (`ExecutionView.tsx:154`,
    `partialHasVisibleContent` in `lib/nex/partial.ts:43-64`) and
    `RoomThinking` takes over — so chat needs its own rule, see T1.3b
    (plan review #2);
  - a subagent's frames (`childIndexes`) stay out of the top level, as in
    room;
  - the interrupt sentinel: a centred muted system line
    (`text-status-error italic text-xs`, `Prohibit` icon), not a bubble;
  - a slash command: a user bubble with `font-mono`;
  - the optimistic pending line: a user bubble at `opacity-60`, in the same
    position room puts it;
  - turns: `groupTurns(messages, turnStarts)` still wraps each turn in a
    `RoomTurnGroup` so fold memory and the turn index keep working. Today the
    group **always** draws its hover expand/collapse strip
    (`RoomTurnGroup.tsx:28-58`), which chat must not (spec §5, minimum
    ceremony; plan review #1): `RoomTurnGroup` gains `chrome?: boolean`
    (default `true`), chat passes `false` and gets only the section +
    `TurnIndexContext`. Test in `RoomTurnGroup.test.tsx`: `draws no fold
    strip without chrome`;
  - auto-scroll: the same effect and deps as `RoomTranscript`.
  - **Tool operations render nothing in this task** — T2.1 adds the chat
    lines. Until then a turn's tools are simply absent from chat (R2-A is not
    user-reachable as a finished chat until R2-B lands; the menu still ships,
    so the PR body says chat is "text only" in this PR).
- Tests (`chat/ChatTranscript.test.tsx`): `puts the agent on the left and you
  on the right`; `renders your line as text, not markdown`; `hides thinking
  entirely`; `shows the typewriter while a thought streams, then nothing`;
  `keeps a subagent's frames out of the top level`; `renders the interrupt
  sentinel as a system line`; `renders a slash command in a user bubble`;
  `draws the pending line as a dimmed user bubble`; `caps bubble width`
  (className check); `wraps each turn in a turn group`.

### T1.3b chat's streaming blocks (TDD)

`PartialMessageGroup` (`components/PartialMessageGroup.tsx:47-60`) hard-codes
room's renderers: `text` → `RoomProse`, `thinking` → `RoomThinking` (room's
fold chrome), `tool_use` → `OperationBlock`. Reused as-is it would put a
thought's text and a live room block into chat (plan review #2).

- `components/chat/ChatPartialGroup.tsx` (new), same inputs as
  `PartialMessageGroup`: `text` → `RoomProse streaming` inside an agent
  `ChatBubble`; `thinking` → nothing; `tool_use` → nothing in R2-A (R2-B
  counts it in the turn's running tools line). Keys follow
  `PartialMessageGroup`'s `partialKey` (messageId-scoped).
- `ExecutionView`'s dots rule gets a chat variant: in chat, `showThinking`
  ignores thinking blocks when asking whether the partial has visible content
  (`partialHasVisibleText` — the text-only half of `partialHasVisibleContent`,
  exported from `lib/nex/partial.ts`), so the dots stay on while a thought
  streams and go off when prose starts. Room's rule is unchanged.
- Tests: `chat/ChatPartialGroup.test.tsx` — `streams text into an agent
  bubble with the cursor`; `renders nothing for a streaming thought`;
  `renders nothing for a streaming tool_use`. `lib/nex/partial.test.ts` —
  `partialHasVisibleText ignores thinking`. `ExecutionView.test.tsx` —
  `chat keeps the dots on while a thought streams`; `room still hands a
  streaming thought to RoomThinking`.

### T1.4 the pane switches (TDD)

- `ExecutionView` takes `mode` + `onModeChange` (from T1.1) and renders
  `RoomTranscript` or `ChatTranscript` from the same state; the header gets
  them for the menu.
- In chat (spec §5): **no dock** (`WorkerDock` not rendered); the header
  keeps **state + cost + the overflow menu** — the name/info popover and the
  wide action row are hidden, and Interrupt / Terminate / the view items live
  in the overflow at every width (chat is phone-first). `WorkerInput` is the
  same in both.
- Switching is local and instant: no daemon call, no refetch, the execution
  store and the WS subscription are untouched (assert the subscribe hook runs
  once across a switch).
- Tests (`ExecutionView.test.tsx`): `renders the room by default`; `renders
  chat when the pane says chat`; `switching does not resubscribe`; `chat has
  no dock`; `chat's header shows state and cost and folds the actions`.

### T1.5 R2-A

- Full gate (vitest / lint / tsc / build). PR body: D1–D3, "chat is text-only
  until R2-B", no ordinal bump and why.

---

## R2-B

### T2.0 one classifier for room and chat (TDD)

Room decides an operation's status in `resolveStatus`, a private function in
`components/room/OperationBlock.tsx:71-80` (activity status ⊕ N2 facts ⊕
`result.isError`, in that precedence), and enumerates operations inline in
`MessageRow.tsx`. Chat must sort a turn's operations by exactly the same rule,
or "failed" in chat drifts from "failed" in room (plan review #4).

- Move `resolveStatus` verbatim to `lib/nex/operation-status.ts` and import it
  back into `OperationBlock` (pure move, its own commit; byte-compare the
  function body before/after).
- Add `classifyTurnOperations(messages, turn, index, tools, factsFor)` there:
  walks the turn's top-level messages (skipping `childIndexes`), returns
  `{ key: BlockKey; msgIndex; kind: 'plain' | 'edited' | 'failed' }[]` in
  order — `failed` when `resolveStatus` says `error` / `denied`, else
  `edited` when the result carries `facts.diff`, else `plain`. A Task is
  `plain` (its subagent expands inside the room block).
- Tests `lib/nex/operation-status.test.ts`: `classifies an error and a denied
  call as failed`; `classifies a diff as edited`; `a failed edit is failed,
  not edited`; `skips a subagent's own calls`; `keeps call order`;
  `a running call is plain`.

### T2.1 chat's operation lines (TDD)

Per turn, chat folds the room's operation blocks into quiet lines (spec §5):

- **`chat/ChatToolsLine.tsx`** — one per turn that has at least one plain
  operation: `t('chat.tools_used', { count })` (`"Used {{count}} tools"` /
  `"使用了 {{count}} 個工具"`, `Wrench` icon, muted, left side, no bubble).
  Expanding shows the turn's plain operations **as room's `OperationBlock`s
  in place** (spec: "nothing is unreachable"), including a Task's
  `SubagentBlock`. Its fold key is `${keyPrefix}-turn-${turnIndex}:chat-tools`
  via `useFold`, so it registers with the turn group.
- **`chat/ChatEditedLine.tsx`** — an operation whose result carries a diff
  (`facts.diff`) is not counted in the tools line; it gets
  `t('chat.edited', { file, added, removed })`
  (`"Edited {{file}} (+{{added}} −{{removed}})"` / `"已編輯 {{file}}（+{{added}} −{{removed}}）"`,
  `file` = basename of `diff.path`, U+2212 minus), expanding into the same
  `ToolDiffView`.
- **`chat/ChatFailedLine.tsx`** — an operation whose status is `error` or
  `denied` is never hidden: one red line `name · first line of the error`
  (`text-status-error`, `Warning` icon), expanding into the room
  `OperationBlock`. Not counted in the tools line either.
- **Placement**: the tools line sits at the position of the turn's first
  plain operation; edited and failed lines sit at their own positions. A turn
  with no plain operations draws no tools line.
- A running operation counts in the tools line; while any is running the
  line reads `t('chat.tools_running', { count })` (`"Using {{count}} tools…"` /
  `"正在使用 {{count}} 個工具…"`) — spec §5 keeps the pane honest about work
  in progress without a spinner per tool.
- Tests (`chat/ChatTranscript.test.tsx` + one file per line component):
  `folds a turn's tools into one line`; `expands the line into the room
  blocks in place`; `gives an edit its own Edited line with the stat`;
  `never hides a failed tool`; `a denied tool is a failed line`; `places the
  tools line at the turn's first operation`; `draws no tools line for a turn
  with none`; `says the tools are running while one is`; `a Task counts as a
  tool and expands into its subagent`; `expand-all opens the tools line`.

### T2.2 R2-B

- Full gate. PR body closes the chat half of spec §11.2.

---

## After R2

- R3 (search, quick replies — spec §4.8), then R4 after the §9 wire batch.
- Deferred: whether a narrow viewport should pick chat by itself (spec §6,
  Q2 — only after chat has been used).
