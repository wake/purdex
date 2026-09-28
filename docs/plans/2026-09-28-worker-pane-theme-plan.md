# Worker Pane Theme & Display Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the worker pane a theme layer (first theme: Purdex) and fix eight display gaps: type size and density, GFM tables, the user input band, rail brightness, scroll memory, per-turn footer, sidebar light/icon/title, and file upload.

**Architecture:** A closed set of `--wt-*` CSS custom properties, set on the worker pane root from a registered theme object. Room and chat read only those variables. Scroll and turn timing are fixed in the transcript hook and the reducer. The sidebar reuses `useAgentStore` by projecting worker state under an `exec:` key. Upload is a daemon endpoint in the `nex` module that saves inside the execution's cwd, plus input chips.

**Tech Stack:** React 19, Zustand 5, Tailwind 4, Vitest, react-markdown 10 + remark-gfm; Go net/http daemon.

**Spec:** `docs/specs/2026-09-28-worker-pane-theme-spec.md` (read §2 decisions first — they are not open for change).

## Global Constraints

- pnpm, not npm. Run tests with `cd spa && npx vitest run <path>`, lint with `cd spa && pnpm run lint`, build with `cd spa && pnpm run build`, type-check with `cd spa && npx tsc -p tsconfig.app.json --noEmit`. Daemon: `go test ./internal/module/nex/...`.
- Every Bash command in a subagent starts with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/worker-theme && ` (or `.../spa && `).
- One commit per task. Commit with `git commit --only <files>` and end the message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Icons are Phosphor. User-visible strings go in `spa/src/locales/en.json` and `zh-TW.json`, **except the turn footer, which is fixed English** (spec F1).
- The footer's time uses `Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' })` so it follows the system 12/24-hour setting (F2). No date in the text; the full date goes in `title`.
- Interrupted turns get no footer. Failed turns always get one; an unknown `execution.terminal` reason counts as failed (F3).
- The sidebar light must follow the terminal definition in spec §8.2 row by row (L1).
- Title precedence: `session_title` (capability-gated) → pre-handoff terminal title → `firstLine(brief)`, then `' - ' + basename(cwd)`. Never use `last-prompt` (N1).
- Phase size cap: one PR ≤ 800 changed lines or ≤ 20 files. Split if a phase exceeds it.
- Mutation check per task: after the task is green, revert the core line(s) and confirm the new test fails. Record it in the commit body as `mutation: <what was reverted> → <test> failed`.

## Review Focus

1. **A reader scrolled up while a hidden tab keeps streaming** should return to the same spot. Pinned in Task B1 (`hidden growth keeps position`).
2. **A failed turn whose only signal is `execution.terminal` with an unrecognised reason** must still show the red footer and an error light, never nothing. Pinned in B2 (`unknown terminal reason → failed`) and C1 (`failed → error`).
3. **A table wider than the pane** scrolls inside its own wrapper, not the transcript. Pinned in A2 (`table is wrapped in overflow-x-auto`).
4. **An active worker tab** must not be marked unread when its turn ends, and switching to it must clear unread. Pinned in C3 (`active exec tab is not unread`, `activating exec tab marks read`).
5. **Sending while an upload is still in flight** must be blocked with a visible reason, not send the text without the file. Pinned in D2 (`send blocked while uploading`).

---

## Phase A — theme layer + typography, tables, user band, rail (PR-A)

### Task A1: Worker theme registry, settings store, pane root vars

**Files:**
- Create: `spa/src/lib/worker-theme/types.ts`, `spa/src/lib/worker-theme/registry.ts`, `spa/src/lib/worker-theme/purdex.ts`, `spa/src/lib/worker-theme/registry.test.ts`
- Create: `spa/src/stores/useWorkerSettingsStore.ts`, `spa/src/stores/useWorkerSettingsStore.test.ts`
- Create: `spa/src/components/settings/WorkerSettingsSection.tsx`, `spa/src/components/settings/WorkerSettingsSection.test.tsx`
- Modify: `spa/src/lib/register-modules/index.tsx:253-266` (add `settings` to the `execution` module), `spa/src/lib/settings-order.ts` (add `MODULE_WORKER`), `spa/src/components/execution/ExecutionView.tsx` (root attrs), locales

**Interfaces:**
- Produces:
  - `type WorkerThemeVar = 'font-size' | 'line-height' | 'block-gap' | 'heading-weight' | 'list-marker-color' | 'list-indent' | 'user-band-bg' | 'user-band-fg' | 'user-band-prefix-color' | 'rail-color' | 'footer-color' | 'footer-error-color' | 'table-border' | 'table-header-bg' | 'code-font'`
  - `interface WorkerTheme { id: string; labelKey: string; vars: Record<WorkerThemeVar, string> }`
  - `registerWorkerTheme(theme: WorkerTheme): void`, `getWorkerTheme(id: string | undefined): WorkerTheme` (unknown → purdex), `listWorkerThemes(): WorkerTheme[]`
  - `workerThemeStyle(theme: WorkerTheme): Record<string, string>` → `{ '--wt-font-size': '14px', ... }`
  - `useWorkerSettingsStore`: `{ theme: string; iconStyle: 'mono' | 'color' | 'custom'; customIcon: string; setTheme(id): void; setIconStyle(s): void; setCustomIcon(icon: string): void }`, persisted as `purdex-worker-settings` (`customIcon` is a plain string; `''` = no custom icon, never `null`)

- [ ] **Step 1: Write failing tests**

```ts
// registry.test.ts
import { getWorkerTheme, listWorkerThemes, workerThemeStyle } from './registry'
it('falls back to purdex for an unknown or missing id', () => {
  expect(getWorkerTheme('nope').id).toBe('purdex')
  expect(getWorkerTheme(undefined).id).toBe('purdex')
})
it('lists purdex', () => { expect(listWorkerThemes().map((t) => t.id)).toContain('purdex') })
it('maps vars to --wt- custom properties', () => {
  const style = workerThemeStyle(getWorkerTheme('purdex'))
  expect(style['--wt-font-size']).toBe('14px')
  expect(Object.keys(style).every((k) => k.startsWith('--wt-'))).toBe(true)
})
```

```tsx
// ExecutionView root (add to ExecutionView.test.tsx, using its existing render helper)
it('sets the worker theme on the pane root', () => {
  renderView()
  const root = screen.getByTestId('execution-view')
  expect(root.dataset.workerTheme).toBe('purdex')
  expect(root.style.getPropertyValue('--wt-font-size')).toBe('14px')
})
```

The settings section test renders `WorkerSettingsSection`, checks there is a theme select with the option `Purdex`, and checks that choosing it calls `setTheme('purdex')`.

- [ ] **Step 2: Run and see them fail.** `cd spa && npx vitest run src/lib/worker-theme src/stores/useWorkerSettingsStore.test.ts src/components/execution/ExecutionView.test.tsx`

- [ ] **Step 3: Implement.** The Purdex values (colours reference app theme variables so every app theme works):

```ts
// purdex.ts
export const PURDEX_THEME: WorkerTheme = {
  id: 'purdex',
  labelKey: 'worker.theme.purdex',
  vars: {
    'font-size': '14px',
    'line-height': '1.5',            // tuned in A3 against the terminal row height
    'block-gap': 'calc(var(--wt-line-height) * 1em)',   // one line (spec §5.1)
    'heading-weight': '600',
    'list-marker-color': 'var(--text-muted)',
    'list-indent': '1.5em',
    'user-band-bg': 'color-mix(in srgb, var(--text-primary) 9%, transparent)',
    'user-band-fg': 'var(--text-primary)',
    'user-band-prefix-color': 'var(--text-muted)',
    'rail-color': 'color-mix(in srgb, var(--text-muted) 55%, transparent)',
    'footer-color': 'var(--text-muted)',
    'footer-error-color': 'var(--status-error)',
    'table-border': 'var(--border-default)',
    'table-header-bg': 'color-mix(in srgb, var(--text-primary) 6%, transparent)',
    'code-font': 'Menlo, Monaco, monospace',
  },
}
```

`registry.ts` keeps a `Map`, registers `PURDEX_THEME` at import, and `workerThemeStyle` maps each key to `--wt-<key>`. If the `execution-view` test id does not already exist on the root, add it. In `ExecutionView`:

```tsx
const themeId = useWorkerSettingsStore((s) => s.theme)
const theme = getWorkerTheme(themeId)
// on the root element:
data-worker-theme={theme.id} style={workerThemeStyle(theme) as CSSProperties}
```

Register the settings section:

```ts
settings: [{ localId: 'worker', scope: 'purdex', order: SETTINGS_ORDER.MODULE_WORKER,
  labelKey: 'settings.section.worker', component: WorkerSettingsSection }],
```

In this task `WorkerSettingsSection` renders only the Theme select. The icon options are added in C4. Check the variable names used in `purdex.ts` against `spa/src/styles/themes.css` and correct any that differ, for example if `--border-default` is actually named differently.

- [ ] **Step 4: Tests pass.** Run the same command, then the mutation check: make `getWorkerTheme` return `undefined` for unknown ids and confirm the fallback test fails.
- [ ] **Step 5: Commit** `feat(spa): worker theme registry and Purdex theme`

### Task A2: GFM tables (render + search parity)

**Files:**
- Modify: `spa/package.json` (`pnpm add remark-gfm` inside `spa/`; use the version compatible with react-markdown 10, i.e. `remark-gfm@^4`)
- Modify: `spa/src/components/room/RoomProse.tsx`, `spa/src/lib/nex/markdown-text.ts`
- Test: `spa/src/components/room/RoomProse.test.tsx`, the existing markdown-text parity test (find it with `grep -rl "proseText" spa/src --include=*.test.ts*`)

**Interfaces:** `proseText(content)` stays the same signature. RoomProse gains `remarkPlugins={[remarkGfm]}` and a `components.table` override.

- [ ] **Step 1: Failing tests**

```tsx
const TABLE = '| a | b |\n|---|---|\n| 1 | 2 |'
it('renders a GFM table', () => {
  render(<RoomProse content={TABLE} />)
  expect(screen.getByRole('table')).toBeInTheDocument()
  expect(screen.getAllByRole('columnheader').map((c) => c.textContent)).toEqual(['a', 'b'])
})
it('table is wrapped in overflow-x-auto', () => {
  render(<RoomProse content={TABLE} />)
  expect(screen.getByRole('table').parentElement).toHaveClass('overflow-x-auto')
})
```

Add `TABLE` and `'~~gone~~ and https://x.test'` cases to the parity test that compares `proseText(src)` with the rendered `textContent`.

- [ ] **Step 2: Run.** They should fail: there is no table role yet, and parity fails once RoomProse gets GFM but markdown-text.ts does not.
- [ ] **Step 3: Implement**

```tsx
// RoomProse.tsx
import remarkGfm from 'remark-gfm'
const COMPONENTS = {
  table: ({ node: _n, ...props }: ComponentProps<'table'> & { node?: unknown }) => (
    <div className="overflow-x-auto"><table {...props} /></div>
  ),
}
<ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]} components={COMPONENTS}>
```

```ts
// markdown-text.ts
import remarkGfm from 'remark-gfm'
const processor = unified().use(remarkParse).use(remarkGfm).use(remarkRehype, { allowDangerousHtml: true })
```

Update the header comment in `markdown-text.ts`: RoomProse now runs GFM, and `TABLE_ELEMENTS` is now reachable. The wrapper div adds no text, so parity holds.

- [ ] **Step 4: Pass + mutation.** Remove `remarkGfm` from `markdown-text.ts` only and confirm the parity test fails.
- [ ] **Step 5: Commit** `feat(spa): render GFM tables in worker prose`

### Task A3: Worker prose typography and lists

**Files:**
- Modify: `spa/src/index.css` (add a `.worker-prose` block after the Tiptap block), `spa/src/components/room/RoomProse.tsx` (swap `prose-sm` sizing for `worker-prose`)
- Test: `spa/src/components/room/RoomProse.test.tsx`

**Interfaces:** CSS class `worker-prose`. It reads `--wt-font-size`, `--wt-line-height`, `--wt-block-gap`, `--wt-heading-weight`, `--wt-list-marker-color`, `--wt-list-indent`, `--wt-table-border`, `--wt-table-header-bg` and `--wt-code-font`.

- [ ] **Step 1: Failing test.** jsdom does not compute CSS, so assert the class contract. RoomProse's body carries `worker-prose` and no longer carries `prose-sm`:

```tsx
it('uses the worker prose scale, not prose-sm', () => {
  render(<RoomProse content={'# h\n\ntext'} />)
  const body = screen.getByTestId('room-prose').querySelector('[data-search-unit], .worker-prose')!
  expect(body).toHaveClass('worker-prose')
  expect(body).not.toHaveClass('prose-sm')
})
```

- [ ] **Step 2: Run → fail.**
- [ ] **Step 3: Implement.** In RoomProse: the outer div becomes `className="max-w-[90ch] text-text-primary"`, and the body div becomes `className="prose prose-invert worker-prose max-w-none"`. In `index.css`:

```css
/* === Worker prose (worker theme, spec §5) — every size and colour comes
   from the pane root's --wt-* vars. Headings are body-size, bold only. === */
.worker-prose { font-size: var(--wt-font-size); line-height: var(--wt-line-height); }
.worker-prose :where(p, ul, ol, pre, table, blockquote, h1, h2, h3, h4, h5, h6) {
  margin-top: 0; margin-bottom: var(--wt-block-gap);
}
.worker-prose :where(> :last-child) { margin-bottom: 0; }
.worker-prose :where(h1, h2, h3, h4, h5, h6) {
  font-size: 1em; line-height: inherit; font-weight: var(--wt-heading-weight); margin-top: 0;
}
.worker-prose :where(ul, ol) { padding-left: var(--wt-list-indent); }
.worker-prose :where(li) { margin: 0; padding-left: 0; }
.worker-prose :where(li)::marker { color: var(--wt-list-marker-color); font-weight: 400; }
.worker-prose :where(li > p) { margin: 0; }
.worker-prose :where(code, pre) { font-family: var(--wt-code-font); font-size: 1em; }
.worker-prose :where(table) { font-size: 1em; border-collapse: collapse; margin-top: 0; }
.worker-prose :where(th, td) { border: 1px solid var(--wt-table-border); padding: 0.2em 0.6em; }
.worker-prose :where(thead th) { background: var(--wt-table-header-bg); font-weight: var(--wt-heading-weight); }
```

`padding-left` on `ul`/`ol` puts the marker in the indent, so wrapped lines align under the item text (a hanging indent). The marker starts at the pane's text edge because `::marker` sits in that padding.

- [ ] **Step 4: Line-height calibration.** In the same task, measure the terminal row height from xterm (Menlo 14, `lineHeight: 1`). Run in the browser console of a terminal pane: `document.querySelector('.xterm-rows > div').getBoundingClientRect().height`. If you cannot open a browser, run `playwright cli -s=worker-theme open https://purdex.mlab.host` and evaluate the same expression. Set `'line-height'` in `purdex.ts` to `height / 14` rounded to 2 decimals, but never below `1.35` (a CJK readability floor; spec §5.1 says tune it on the device). Record the measured height and the chosen value in the commit body.
- [ ] **Step 5: Tests pass + commit** `feat(spa): worker prose uses body-size headings and terminal density`

### Task A4: Full-width user band and brighter rail

**Files:**
- Modify: `spa/src/components/room/RoomUserLine.tsx`, `spa/src/components/room/RoomTranscript.tsx` (padding so the band can bleed), `spa/src/components/room/OperationBlock.tsx:207`, `spa/src/components/room/SubagentBlock.tsx:91-93`
- Test: `spa/src/components/room/RoomTranscript.test.tsx`, `OperationBlock.test.tsx`, `SubagentBlock.test.tsx`

**Interfaces:** RoomUserLine keeps its props. `data-testid="room-user-mark"` is removed; `room-user-prefix` is added.

- [ ] **Step 1: Failing tests**

```tsx
it('draws the user line as a full-width band with a › prefix', () => {
  render(<RoomUserLine text="ping" />)
  const line = screen.getByTestId('room-user-line')
  expect(line.className).toContain('bg-[var(--wt-user-band-bg)]')
  expect(line.className).toContain('-mx-4')          // bleeds to the transcript's edges (p-4)
  expect(screen.getByTestId('room-user-prefix').textContent).toBe('›')
  expect(screen.queryByTestId('room-user-mark')).toBeNull()
})
it('pending band is dimmed', () => {
  render(<RoomUserLine text="x" pending />)
  expect(screen.getByTestId('room-user-line')).toHaveClass('opacity-60')
})
// OperationBlock / SubagentBlock
expect(screen.getByTestId('op-rail').className).toContain('border-[var(--wt-rail-color)]')
expect(screen.getByTestId('subagent-rail').className).toContain('border-[var(--wt-rail-color)]')
```

Update any existing test that asserts `room-user-mark` (grep for it) to use the new contract.

- [ ] **Step 2: Run → fail.**
- [ ] **Step 3: Implement**

```tsx
// RoomUserLine.tsx
<div data-testid="room-user-line"
  className={`-mx-4 px-4 py-1 flex items-baseline gap-2 bg-[var(--wt-user-band-bg)] text-[var(--wt-user-band-fg)] text-[length:var(--wt-font-size)] leading-[var(--wt-line-height)]${pending ? ' opacity-60' : ''}`}>
  <span data-testid="room-user-prefix" aria-hidden="true" className="shrink-0 text-[var(--wt-user-band-prefix-color)]">›</span>
  <p data-search-unit={searchUnit} className="min-w-0 whitespace-pre-wrap break-words">{text}</p>
  {children}
</div>
```

`-mx-4` matches RoomTranscript's `p-4`. If you change one, change the other, and say so in a comment on both. In OperationBlock and SubagentBlock replace `border-border-subtle` with `border-[var(--wt-rail-color)]` on the rail elements only.

- [ ] **Step 4: Pass + mutation.** Put `border-border-subtle` back and confirm the rail test fails.
- [ ] **Step 5: Commit** `feat(spa): full-width user band and brighter rail in the room`

### Task A5: PR-A gate

- [ ] Run the full suite `cd spa && npx vitest run`, then lint, tsc and build. All must be green.
- [ ] Push and open PR-A, then run review per CLAUDE.md (R1 → attack → critic).

---

## Phase B — scroll memory and turn footer (PR-B)

### Task B1: Follow only from the bottom, per-pane scroll memory

**Files:**
- Create: `spa/src/lib/nex/transcript-scroll-memory.ts` (+ test)
- Modify: `spa/src/hooks/useTranscriptScroll.ts`, `spa/src/components/room/RoomTranscript.tsx`, `spa/src/components/chat/ChatTranscript.tsx`, `spa/src/components/execution/ExecutionView.tsx` (pass `paneId` and the view)
- Test: `spa/src/components/room/transcript-scroll.test.tsx`

**Interfaces:**
- Produces: `interface ScrollMemo { scrollTop: number; atBottom: boolean; view: 'room' | 'chat'; firstTurn: number | null }`, `readScrollMemo(paneId: string): ScrollMemo | undefined`, `writeScrollMemo(paneId: string, memo: ScrollMemo): void`, `forgetScrollMemo(paneId: string): void` (memory only, a module `Map`)
- `useTranscriptScroll(external, hold, memory?: { paneId: string; view: 'room' | 'chat' })`
- Transcripts gain an optional prop `scrollMemoryKey?: string` (the paneId), passed by ExecutionView.

- [ ] **Step 1: Failing tests** (reuse the scroll-box helpers already in `transcript-scroll.test.tsx`)

```tsx
it('hidden growth keeps position: scrolled up + new message with the bar closed → scrollTop unchanged', ...)
it('scrolled down but short of the bottom + new message → scrollTop unchanged', ...)  // top rises from 100 to 400, bottom at 1000
it('growth during follow()\'s own smooth scroll keeps following', ...)
it('at bottom + new message → follows', ...)            // existing behaviour, keep
it('remount restores scrollTop from memory', ...)       // unmount, remount with same scrollMemoryKey → same scrollTop
it('remount at bottom → jumps to bottom', ...)
it('view switch honours only atBottom; otherwise scrolls the remembered first turn into view', ...)  // ≥3 turns, first one scrolled out: remembers turn 1 or 2, not 0
it('the search bar placement wins over restore on mount', ...)  // holdScroll true on mount → restore skipped
```

- [ ] **Step 2: Run → the first and the remount tests fail.**
- [ ] **Step 3: Implement**
  - `observe()` must make `atBottom` positional (codex plan review #3): today it only clears the flag when the reader moves **up**, so a reader who scrolls down but stops short of the bottom still counts as at-bottom. New rule: near bottom → `atBottom = true`; otherwise `atBottom = false`, **except** while a smooth scroll that `follow()` itself started is still travelling down (`smoothTarget.current !== null && top >= lastTop.current`). Set `smoothTarget` in `follow()` when it issues a smooth scroll, and clear it when the bottom is reached or the reader moves up.
  - In `follow()`, replace `if ((holding.current || releaseHold.current) && !atBottom.current) return` with `if (!atBottom.current && scrolled.current) return`. The first call after mount still decides by memory (below). Keep `release()`: it sets `atBottom=false`, which now stops following without the separate flag. Keep `releaseHold` only if an existing test needs it, and update the file's header comment to state the new single rule: "growth follows only a reader at the bottom".
  - Memory: `observe()` writes `{scrollTop, atBottom, view, firstTurn}` when `memory` is present. `firstTurn` is the `data-turn-index` of the first element in `el.querySelectorAll('[data-turn-index]')` (iterate the whole NodeList) whose `getBoundingClientRect().bottom > el.getBoundingClientRect().top`. Chat must carry `data-turn-index` too; it already renders `RoomTurnGroup` with `chrome={false}`.
  - First `follow()` after mount: if `holding` is set, keep the current R1-1 behaviour. Otherwise, if a memo exists and `!memo.atBottom`: when `memo.view === memory.view`, set `el.scrollTop = memo.scrollTop` (clamped). Otherwise scroll `[data-turn-index="${memo.firstTurn}"]` into view with `block: 'start'`. Set `atBottom=false`, `scrolled=true` and return. If there is no memo or it is at the bottom, use today's jump.
  - Do not call `forgetScrollMemo` on unmount. It is called when the pane is closed: find the pane-removal path with `grep -rn "removePane\|closeTab" spa/src/stores` and add the call there in the smallest way. If that crosses a store boundary awkwardly, skip it; the map holds only a few numbers per pane.
- [ ] **Step 4: Pass + mutation.** Restore the old `holding || releaseHold` guard and confirm "hidden growth keeps position" fails. Also run the R3 search tests `src/components/room/TranscriptSearch.test.tsx`; they must stay green.
- [ ] **Step 5: Commit** `fix(spa): worker transcript keeps the reader's position`

### Task B2: Reducer keeps turn timing and outcome

**Files:**
- Modify: `spa/src/lib/nex/event-reducer.ts`, `spa/src/lib/nex/turns.ts`
- Test: `spa/src/lib/nex/event-reducer.test.ts`, `spa/src/lib/nex/turns.test.ts`

**Interfaces:**
- Produces:
  - `type TurnOutcome = 'ok' | 'failed' | 'interrupted'`
  - `interface TurnMeta { startAt: number; endAt: number | null; outcome: TurnOutcome | null; durationMs: number | null }`
  - `ExecutionState.turnMeta: TurnMeta[]`, index-aligned with `turnStarts`
  - `RoomTurn.boundary: number | null`, the index into `turnStarts` (null for the leading turn `groupTurns` adds when the first start is not 0)
- Consumes: `markTurnStart`, `endTurn(s, createdAt)`, `TURN_ENDING_KINDS`.

**Outcome rules** (spec §7.1). Before coding, list every `execution.terminal` reason with `grep -rn "last_turn_reason\|terminal_reason" ~/Library/Caches/go/pkg/mod/lab.protype.tw/wake/nexen@v0.13.2/docs/contract/capability-matrix.md`, and write the mapping table into the code comment:
- `result` (top level): `is_error === true || (subtype !== '' && subtype !== 'success')` → `failed`, else `ok`. `durationMs = duration_ms` if it is a finite number.
- `execution.interrupted`, `execution.terminated`, `execution.archived` → `interrupted`.
- `execution.error`, `execution.rejected`, `execution.turn_stalled`, `execution.turn_orphaned` → `failed`.
- `execution.terminal`: a reason in the documented "user/normal" set → keep any outcome already set by a `result`, else `ok` for normal completion or `interrupted` for user interrupt. Anything else, including an unknown reason → `failed`.
- An outcome is written once per turn. A later event in the same turn only fills `endAt` / `durationMs` if they are null. The exception: `interrupted` overrides a `failed` or `ok` that came from a `result` in the same turn, because CC emits a result after an interrupt.
- `durationMs` fallback: `endAt - startAt` when both are > 0.

- [ ] **Step 1: Failing tests**

```ts
it('records ok with duration from result.duration_ms', ...)            // message_accepted(created_at 1000) → result{duration_ms: 5000, created_at: 7000}
it('records failed for result.is_error', ...)
it('interrupt then result → interrupted', ...)
it('unknown terminal reason → failed', ...)
it('subagent result does not end or stamp the main turn', ...)          // parent_tool_use_id set
it('duration falls back to endAt - startAt', ...)
it('replay from scratch yields identical turnMeta', ...)               // apply same event list twice to fresh state
// turns.test.ts
it('boundary maps each turn to its turnStarts index; leading implicit turn has null', ...)
```

- [ ] **Step 2: Run → fail.**
- [ ] **Step 3: Implement.** `markTurnStart(s, createdAt)` pushes `{startAt: createdAt, endAt: null, outcome: null, durationMs: null}`. The callers pass `ev.created_at`. Pass `ev` and the payload into the turn-ending branch so the outcome can be computed; keep `endTurn` doing what it does now, plus stamping the last meta entry. `defaultExecutionState()` gets `turnMeta: []`. `groupTurns` sets `boundary = i - offset`, where `offset` is 1 if it unshifted a leading 0, else 0, and gives the leading turn `boundary: null`.
- [ ] **Step 4: Pass + mutation.** Map an unknown terminal reason to `ok` and confirm the test fails.
- [ ] **Step 5: Commit** `feat(spa): reducer records per-turn timing and outcome`

### Task B3: Turn footer

**Files:**
- Create: `spa/src/lib/nex/turn-footer.ts` (+ test), `spa/src/components/room/TurnFooter.tsx` (+ test)
- Modify: `RoomTranscript.tsx`, `ChatTranscript.tsx`, `ExecutionView.tsx` (pass `turnMeta`)

**Interfaces:**
- `formatTurnDuration(ms: number): string`: under 1000 → `<1s`, under 60 s → `12s`, under 1 h → `38m 2s`, otherwise `1h 5m`.
- `turnFooterText(meta: TurnMeta, fmt: (ms: number) => string): { text: string; tone: 'normal' | 'error'; title: string } | null`. Returns null for `interrupted`, a null outcome, or a null `endAt`.
- `TurnFooter({ meta, timeFormat? })`. `timeFormat` defaults to `new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' })` and can be injected for tests. The title uses `new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' })`.
- Transcripts take the prop `turnMeta?: readonly TurnMeta[]` and render `<TurnFooter meta={turnMeta[turn.boundary]} />` as the last child of each turn whose `boundary !== null`. Skip the last turn while `turnLive`: its meta has `endAt: null`, so it renders nothing anyway.

- [ ] **Step 1: Failing tests**

```ts
expect(formatTurnDuration(2282000)).toBe('38m 2s')
expect(formatTurnDuration(10000)).toBe('10s')
expect(formatTurnDuration(400)).toBe('<1s')
expect(formatTurnDuration(3900000)).toBe('1h 5m')
const h24 = new Intl.DateTimeFormat('en-GB', { hour: 'numeric', minute: '2-digit' })
const h12 = new Intl.DateTimeFormat('en-US', { hour: 'numeric', minute: '2-digit' })
// ok → '✻ Worked for 38m 2s · done 17:48' with h24, '… done 5:48 PM' with h12
// failed → '✻ Failed after 12s · 17:48', tone error
// no duration → '✻ Done · 17:48' / '✻ Failed · 17:48'
// interrupted → null ; endAt null → null
```

Component tests: the footer renders with `data-testid="turn-footer"` and has a `title` containing the date. The error tone uses the class `text-[var(--wt-footer-error-color)]`. It is not a search unit (no `data-search-unit`). The room transcript test shows a footer after a completed turn and none after an interrupted turn. Chat shows the same.

- [ ] **Step 2: Run → fail.**
- [ ] **Step 3: Implement.** Build the text from the formatted time: `new Date(meta.endAt)` with the injected formatter. The text is fixed English, not from the locale files. Footer classes: `mt-1 text-[length:var(--wt-font-size)] text-[var(--wt-footer-color)] select-none`.
- [ ] **Step 4: Pass + mutation.** Make `turnFooterText` return text for `interrupted` and confirm the test fails.
- [ ] **Step 5: Commit** `feat(spa): per-turn footer with duration and completion time`

### Task B4: PR-B gate

Same gate as A5.

---

## Phase C — sidebar light, icon, title (PR-C)

### Task C1: Pure worker → agent status projection

**Files:**
- Create: `spa/src/lib/nex/worker-agent-status.ts` (+ test)

**Interfaces:**
- `execAgentCode(executionId: string): string` → `'exec:' + executionId`. `isExecAgentCode(code: string): boolean`. `executionIdOfAgentCode(code: string): string | null`.
- `providerAgentType(provider: string): string`: `claude` → `cc`, `codex` → `codex`, anything else passes through.
- `projectWorkerStatus(input: WorkerStatusInput): WorkerProjection` where
  - `interface WorkerStatusInput { state: string; turnLive: boolean; lastOutcome: TurnOutcome | null; hasTurn: boolean; archived: boolean; runningSubagents: { task_id: string; subagent_type?: string; started_at: number | null }[] }`
  - `interface WorkerProjection { status: 'running' | 'idle' | 'error' | 'clear'; subagents: SubagentRef[] }`
- The rules are the spec §8.2 table:
  1. `archived` or `state === 'terminated'` → `clear`
  2. `state === 'rejected'` → `error`
  3. `turnLive`, or state `queued`/`running` → `running`
  4. `lastOutcome === 'failed'` or `state === 'failed'` → `error`
  5. otherwise, if `hasTurn` → `idle` (this covers ok and interrupted)
  6. no turn yet and not running → `idle`
  - The error guard falls out of rule 3 vs. rule 4: error persists until the next turn goes live.
  - Subagents: `{ id: task_id, type: subagent_type ?? 'subagent', started_at: started_at ?? 0, is_proxy: false, delegating: false }` (match the `SubagentRef` fields in `useAgentStore.ts:45-54`).

- [ ] **Step 1: Failing test.** Write one table-driven test with a row per §8.2 line, plus: "error then new turn live → running", "error then idle state without a new turn → still error", "interrupted → idle", "rejected → error", "archived → clear".
- [ ] **Step 2–4:** Run → fail, implement, pass. Mutation: swap rules 3 and 4 and confirm "error then new turn live → running" fails.
- [ ] **Step 5: Commit** `feat(spa): project worker state onto the agent light definition`

### Task C2: Feed the projection into useAgentStore

**Files:**
- Create: `spa/src/hooks/useWorkerAgentProjection.ts` (+ test), mounted once at app level next to the other app-lifetime hooks (find them in `spa/src/App.tsx`, or wherever `useNotificationDispatcher` is mounted)
- Modify: `spa/src/stores/useAgentStore.ts` only if a helper is needed; prefer calling the existing `handleNormalizedEvent`

**Interfaces:**
- Consumes: `projectWorkerStatus`, `execAgentCode`, `providerAgentType`, `useExecutionStore`, `useExecutionListStore`, `useTabStore`, `useExecutionListStore.getState().subscribe(hostId)`
- Produces: for each execution pane in any tab, the hook calls `useAgentStore.getState().handleNormalizedEvent(hostId, execAgentCode(id), ev)` **only when the projection changes**. `ev` is `{ agent_type, status, subagents, raw_event_name, broadcast_ts: Date.now(), detail: {} }`. `raw_event_name` must be a name the notification pipeline already understands (codex plan review #1: `notification-content.ts:21-53` returns null for unknown names, `event-name.ts:6-14` normalises only the Pdx* set): `'Stop'` for idle, `'StopFailure'` for error, `'UserPromptSubmit'` for running, `'SessionEnd'` for clear. `detail` for Stop carries `last_assistant_message` = the last top-level assistant text of the turn (first 300 chars; absent from a list-row source); for StopFailure `error` = the failing turn's reason (`result.subtype`, or the lifecycle kind / terminal reason). The status `clear` goes through the same call, and the store clears the key.
- Sources, in priority order: `useExecutionStore.executions[executionKey(host, id)]` when present (live), taking `turnLive`, `summary.state`, `summary.archived`, `turnMeta.at(-1)?.outcome`, and running subagents from `runningTasks(tasks)`. Otherwise the list row from `useExecutionListStore.byHost[host].items`, taking `state`, `archived`, `running_tasks` (a count only, so `subagents: []`), `hasTurn = (turn_count ?? 0) > 0`, `turnLive = state === 'running'`, and `lastOutcome = state === 'failed' ? 'failed' : null`.
- Keep a refcounted list subscription for every host that has at least one execution tab, and release it when no execution tab remains on that host.
- The execution's host id comes from `resolve-host.ts`, following what `ExecutionPaneWrapper` does in `register-modules/index.tsx:121-134`.

- [ ] **Step 1: Failing tests.** Seed the tab store with one execution tab and the execution store with a live state, then assert `useAgentStore.getState().statuses[compositeKey(h, 'exec:E1')] === 'running'`. Flip it to ended-ok and assert `idle` and `unread` true (the tab is not active in this test). Set failed and assert `error`. With no live state, fall back to the list row. Removing the tab releases the list subscription. The same projection applied twice dispatches once (spy on `handleNormalizedEvent`).
- [ ] **Step 2–4:** Run → fail, implement, pass. Mutation: remove the change-detection and confirm the "dispatches once" test fails.
- [ ] **Step 5: Commit** `feat(spa): project worker panes into the agent store`

### Task C3: Tab icon, icon setting, title

**Files:**
- Create: `spa/src/lib/nex/worker-tab-title.ts` (+ test), `spa/src/lib/worker-icon.tsx` (+ test)
- Modify: `spa/src/hooks/useTabDisplay.ts`, `spa/src/components/tab-icon-map.tsx` (add `Robot`), `spa/src/lib/agent-icons.tsx` (export colour variants), `spa/src/components/settings/WorkerSettingsSection.tsx` (icon options), `spa/src/types/tab.ts` (optional `fromTitle?: string` on execution content), `spa/src/lib/nex/handoff.ts` (`HandToNexArgs` gains `fromTitle?: string`; `executionContentFor(hostId, executionId, from?, fromTitle?)` writes it; `handToNex` passes `args.fromTitle`), `spa/src/components/HandoffConfirmDialog.tsx:51` (the only `handToNex` caller: pass the source session's own `pane_title` for `(hostId, sessionCode)` with the agent marker stripped, regardless of the dynamic-tab-name settings; undefined when there is no pane title. **Not** the tab's composed `displayTitle`: that already ends in ` - <session name>` (doubling the suffix) or is just the session name, which would outrank the brief — controller ruling during C3 review), locales

**Interfaces:**
- `workerTabTitle({ sessionTitle, fromTitle, brief, cwd }: { sessionTitle?: string | null; fromTitle?: string; brief?: string; cwd?: string }): string | null` → `primary + ' - ' + basename(cwd)`, or just `primary` if there is no cwd. Returns null if there is no primary. `sessionTitle` is passed only when the host capability says the field exists. Until phase E, callers pass `undefined`.
- `workerIcon(provider: string, style: 'mono' | 'color' | 'custom', opts: { ccVariant; codexVariant; customIcon: string }): TabIconComponent` (`customIcon` is `''` when none is set, never `null`)
  - `mono` → `getAgentIcon(providerAgentType(provider), {ccVariant, codexVariant})`
  - `color` → `claudecode-color` (bot) or `claude-color` (star) for cc; `codex-color` for codex, including when codexVariant is `openai`, because OpenAI has no colour mark
  - `custom` → the Phosphor icon named by `customIcon` (`''` falls back like an unrecognised name), resolved the same way the workspace icon picker resolves names; fall back to `Robot`
  - Unknown provider → `Robot`.
- `useTabDisplay`: when the primary pane is `execution`, it sets `hostId` and `sessionCode = execAgentCode(id)` so `useSessionAgentIndicator` finds the light. It overrides `agentIcon` with `workerIcon(...)` and sets `displayTitle = workerTabTitle(...) ?? baseLabel`, reading the summary from the execution store, or the list row.

- [ ] **Step 1: Failing tests:** `executionContentFor(..., fromTitle)` stores it and a handoff through `HandoffConfirmDialog` writes the source tab's title into the new pane content; the title precedence (all combinations including no cwd and whitespace-only brief); `workerIcon` for each style and provider; `ICON_MAP.Robot` is defined; `useTabDisplay` for an execution tab returns a non-undefined `IconComponent`, `agentStatus` from the store, and the title `brief - repo`; the settings section switches `iconStyle` and shows the picker only for `custom`.
- [ ] **Step 2–4:** Run → fail, implement, pass. Check the colour logos on a dark theme via playwright (`playwright cli -s=worker-theme`). If one is illegible, note it in the commit and wrap that icon with a light variant (spec §8.3). Mutation: drop `Robot` from `ICON_MAP` and confirm the test fails.
- [ ] **Step 5: Commit** `feat(spa): worker tab icon, icon setting and title`

### Task C4: Active tab, unread, notifications understand exec keys

**Files:**
- Modify: `spa/src/lib/active-session.ts:17`, `spa/src/lib/pane-tree.ts:91` (`findTabBySessionCode`), `spa/src/hooks/useNotificationDispatcher.ts` (~231-253 name lookup, 331-373 click)
- Test: the existing tests of those files plus new cases

**Interfaces:**
- `getActiveSessionInfo()` also returns `{ hostId, sessionCode: execAgentCode(id) }` when the primary pane is `execution`. Check every caller (`main.tsx`, `useAgentStore`, `useNotificationDispatcher`, `pane-tree`) for code that assumes a tmux code. Each caller must either be exec-safe or skip exec codes explicitly (`isExecAgentCode`).
- `findTabBySessionCode(tabs, hostId, code)` matches an execution primary pane when `code === execAgentCode(content.executionId)`.
- Notification name: for exec codes, use `workerTabTitle(...)` (built in C3, which runs before this task), fed from the same summary source as the tab (execution store, else list row), falling back to the execution id. A test asserts the notification title equals the tab's `displayTitle` for the same worker.
- Click: for exec codes, focus the tab that `findTabBySessionCode` finds. Never create a tmux tab. If no tab is found, do nothing except `markRead`.

- [ ] **Step 1: Failing tests:** "worker idle builds notification content (title = worker title, body = last assistant text)" and "worker error builds StopFailure content" (call `buildNotificationContent` with the event C2 dispatches, and run the dispatcher's `shouldNotify` with an exec key that has an open tab); "active exec tab is not unread" (store with the active tab = exec, handleNormalizedEvent idle → unread false), "activating exec tab marks read" (main.tsx subscription path or its extracted function), "notification click focuses the exec tab", "no tmux tab is created for exec click", "hasTab true for exec with an open tab".
- [ ] **Step 2–4:** Run → fail, implement, pass. Mutation: revert the `active-session.ts` change and confirm "active exec tab is not unread" fails.
- [ ] **Step 5: Commit** `feat(spa): active tab, unread and notifications cover worker tabs`

### Task C5: PR-C gate

Same gate as A5, plus one manual check: open two worker tabs and confirm the light changes running → idle, and that unread clears when you switch to the tab.

---

## Phase D — upload by path reference (PR-D)

Measured 2026-09-28: under `standard` (acceptEdits) and `readonly` (dontAsk), `claude -p` **refuses Read outside the cwd** (`permission_denials` on `~/.config/pdx/uploads/...`). mlab's default profile is `standard`. So uploads are saved **inside the execution's cwd** under `.purdex-uploads/<executionId>/`, and that directory holds a `.gitignore` containing `*` so it never shows in git.

### Task D1: Daemon endpoint

**Files:**
- Create: `internal/module/nex/upload.go`, `internal/module/nex/upload_test.go`
- Modify: `internal/module/nex/module.go:306-317` (route), `internal/module/agent/upload.go` (export `createDedupFile` as `CreateDedupFile` in a small shared place: move it to `internal/fsutil/dedup.go` with its test and have agent call it; this is a pure move)

**Interfaces:**
- `POST /api/nex/executions/{id}/uploads`, multipart field `file`. Returns 200 `{"path": "<abs>", "name": "<final filename>", "size": <bytes>}`.
- Errors: 503 `nex_unavailable` (engine guard, as in `take_to_terminal.go:43-97`), 404 `execution_not_found` (via `m.getExecution`), 409 `execution_ended` (state terminated or archived), 413 `file_too_large` (cap 50 MiB, enforced with `http.MaxBytesReader`), 400 `missing_file`.
- Directory: `filepath.Join(exec.Cwd, ".purdex-uploads", execID)` with `MkdirAll 0755`. Write `.purdex-uploads/.gitignore` with `*\n` if it is absent (O_EXCL; ignore "exists"). The filename goes through `filepath.Base`. Refuse if the resolved directory is not under `exec.Cwd` after `filepath.Clean` (defence against an id with separators; the id also goes through the existing id validation if there is one).
- Auth: the principal comes from `m.principal(r)`, like take-to-terminal.

- [ ] **Step 1: Failing Go tests:** success (file content, returned abs path is under cwd, `.gitignore` is created with `*`), dedup (second same name → `name-1.ext`), path traversal filename (`../../x` → saved as `x` inside the dir), 404 unknown id, 409 terminated, 413 over the cap (use a small cap via a package var in the test), missing file 400.
- [ ] **Step 2: Run** `go test ./internal/module/nex/ -run Upload` → fail.
- [ ] **Step 3: Implement.** Follow the take-to-terminal handler structure for guard, principal and error writing.
- [ ] **Step 4: Pass + mutation.** Remove the `.gitignore` write and confirm its test fails. Run `go vet ./internal/...`.
- [ ] **Step 5: Commit** `feat(daemon): upload a file into a worker's cwd`

### Task D2: Input chips, drop, paste, send composition

**Files:**
- Create: `spa/src/lib/nex/worker-upload.ts` (+ test), `spa/src/components/room/UploadChips.tsx` (+ test)
- Create: `spa/src/hooks/useWorkerUploads.ts` (+ test)
- Modify: `spa/src/hooks/useExecutionActions.ts` (`handleSend` → `Promise<boolean>`, `SendOptions.draftText`), `spa/src/components/room/QuickReplyDock.tsx` caller if typed, `spa/src/lib/nex/nex-api.ts` (add `uploadWorkerFile`), `spa/src/components/room/WorkerInput.tsx`, `spa/src/components/execution/ExecutionView.tsx` (pass `hostId` and `executionId` to WorkerInput and wrap the drop area), locales

**Interfaces:**
- `uploadWorkerFile(hostId: string, executionId: string, file: File): Promise<{ path: string; name: string; size: number }>` via `pinnedHostFetch` to `/api/nex/executions/${encodeURIComponent(id)}/uploads`.
- `type Chip = { key: string; name: string; status: 'uploading' | 'done' | 'failed'; path?: string; error?: string; previewUrl?: string }`
- `composeWithAttachments(text: string, chips: Chip[]): string` → `text` + (if any done chips) `'\n\n' + chips.map((c) => `[file: ${c.path}]`).join('\n')`. With no text, it returns only the file lines.
- `canSend(chips): { ok: true } | { ok: false; reason: 'uploading' | 'failed' }`
- **Chip state lives in ExecutionView, not WorkerInput** (codex plan review #2/#7): WorkerInput is re-keyed by `draft` and remounts when a failed send restores the draft, and the pane-wide drop target is ExecutionView's root. New hook `useWorkerUploads(hostId, executionId): { chips: Chip[]; add(files: File[]): void; remove(key: string): void; clear(): void }` in `spa/src/hooks/useWorkerUploads.ts`, used by ExecutionView; WorkerInput receives `chips`, `onRemoveChip`, `onAddFiles(files)` (paste and the `+` button) as props and renders `<UploadChips>` above the textarea.
- `useExecutionActions.handleSend` returns `Promise<boolean>` (true when `sendMessage` resolved and the attempt was not superseded; false on the re-entrancy no-op and on failure). ExecutionView's send wrapper: `if (!canSend(chips).ok) return; const ok = await handleSend(composeWithAttachments(text, chips)); if (ok) clear()`. On failure the chips stay and the draft restore works as today (the draft holds only the typed text, not the file lines — pass `restoreDraft` text explicitly if `handleSend` would otherwise restore the composed string: add an optional `draftText` to `SendOptions`). Update the existing QuickReplyDock caller (it ignores the result). WorkerInput's own `send()` keeps clearing the textarea immediately, as today.
- Drag-drop covers the whole pane (handlers on ExecutionView's root), reusing the TerminalView overlay pattern (`TerminalView.tsx:61-110,178-187`, `data-testid="drop-overlay"`, `t('upload.drop_files')`), with paste handled in the textarea (`e.clipboardData.files`) and a `+` button that opens a hidden `<input type="file" multiple>`. Image chips show a thumbnail from `URL.createObjectURL`, revoked on remove and on unmount.

- [ ] **Step 1: Failing tests:** `handleSend` resolves true / false (success, failure, re-entrant no-op); a failed send keeps the chips and restores only the typed text as draft; a drop fired on the pane root (not the input) adds chips; `composeWithAttachments` cases; `canSend`; "send blocked while uploading" (the Enter key does not call onSend and a status line shows `t('worker.upload.wait')`); a failed chip blocks send with `t('worker.upload.failed_remove')`; removing a chip unblocks send; paste of a file calls onUpload; drop shows the overlay and uploads each file in order; a successful send clears the chips; the text is composed exactly.
- [ ] **Step 2–4:** Run → fail, implement, pass. Mutation: make `canSend` always ok and confirm the blocked-send test fails.
- [ ] **Step 5: Commit** `feat(spa): attach files to a worker message by path`

### Task D3: PR-D gate + deploy note

Same gate as A5, plus `go test ./...` and `go vet ./...`. After merge the mlab daemon must be rebuilt and redeployed (the reference is `reference_pdx_daemon_runtime.md`: `bin/pdx` rm → cp → mv, then restart).

---

## Phase E — Nexen features (separate plan)

This phase is blocked on nexen-c3's two PRs (image attachments, `session_title`). When nexen-c3 reports the contract location and version, write `docs/plans/<date>-worker-pane-theme-e-plan.md`:

- Pin the new Nexen version.
- Send images as native attachments, capability-gated. Fall back to path reference with a notice when over the limit or the capability is missing.
- Show replay thumbnails.
- Pass `summary.session_title?.text` into `workerTabTitle`, capability-gated.

## Execution order and gates

A1 → A2 → A3 → A4 → A5 (PR-A merge → bump) → B1 → B2 → B3 → B4 (merge → bump) → C1 → C2 → C3 (title/icon) → C4 (active/unread/notifications) → C5 (merge → bump) → D1 → D2 → D3 (merge → bump → daemon deploy). Each PR runs the review from CLAUDE.md (codex R1, then attack, then critic, with sol and low effort). Before each bump, `git fetch` VERSION, because other sessions bump too.
