# Editor 切換 focus + markdown wysiwyg viewState — Implementation Plan (#857)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 切換到 editor 分頁時 markdown editor 自動 focus（不論 editor 早於或晚於 isActive ready），且 wysiwyg(Tiptap) 切回時保留 scroll 位置 + 游標 selection。

**Architecture:** BUG1 = editor ready 時若 isActive 補 focus（Monaco `handleMount` / Tiptap one-shot ready effect），以 ref 取最新 isActive 避免 closure stale，既有 `[isActive]` effect 保留。BUG2 = 比照 Monaco viewState 模式，新增 `tiptapViewState` paneState 欄位 + save/restore：unmount 從 live `editorRef` 存 selection/scroll，one-shot 在 editor ready 後 restore（selection→scroll→focus）。selection restore 邏輯抽純函式 `resolveRestoreSelection` 以真實 PM doc 單元測。

**Tech Stack:** React 19 / Zustand 5 / Vitest 4 / @tiptap/react 3 / @tiptap/pm 3 (ProseMirror) / @monaco-editor/react。

## Global Constraints

- Spec SOT：`docs/specs/2026-06-26-md-editor-focus-scroll-spec.md`（v3）。所有 AC / Invariant 編號引用該檔。
- Package manager：**pnpm**；測試 `cd spa && npx vitest run <file>`；lint `cd spa && pnpm run lint`；build `cd spa && pnpm run build`。
- Worktree：`.claude/worktrees/md-editor-focus-scroll/`；所有路徑相對 repo root。Edit/Write 絕對路徑必須含 `.claude/worktrees/md-editor-focus-scroll/` 前綴。
- PM import 一律 `@tiptap/pm/state`（`TextSelection`, `Selection`）/ `@tiptap/pm/model`（測試建 schema/doc）—— 直接依賴，勿用 `prosemirror-*`。
- Editor 型別：`import { type Editor } from '@tiptap/react'`。
- I1：focus 補強只在 editor ready **且** isActive=true 觸發；isActive=false 不得 focus。
- I2：selection restore 用 **inlineContent 前置檢查**（`$from.parent.inlineContent && $to.parent.inlineContent` → `TextSelection.create`，否則 `Selection.near($from)`）。**不可**用 try/catch —— `TextSelection.create` 對非法位置不 throw、只 warn（已本地實證）。
- I3：restore 順序 = selection → scrollTop → focus(若 isActive)。
- I5：one-shot restore（`didRestoreRef` 守門）在 editor ready 後執行；初次 content sync 必須跳過（`hasInitializedRef`），不得重設 restore 後的 selection/scroll。
- I6：`EditorPane` Tiptap 分支加 `key={buffer.modelId}`（堵 transient 跨 buffer reuse + 對齊 Monaco；known-limitation 不寫整合回歸測試）。
- 測試前提 M1–M4（spec §5.0）：`useEditor` mock 走 `null→editor` transition；editor mock 含可變 `state.selection`/`state.tr`/`view.dispatch`；`EditorPane` 的 `TiptapEditor` mock capture props；selection 純函式測用真實 PM。
- Commit 分組（PR squash-merge 收斂為 spec §6 的 2 邏輯 commit）：
  - **focus**（Task 1–2）：`fix(editor): focus editor on activation even when it mounts after isActive (#857)`
  - **viewState**（Task 3–6）：`feat(editor): persist scroll + cursor for markdown wysiwyg editor (#857)`

---

## File Structure

| 檔案 | 責任 | 動作 |
|------|------|------|
| `spa/src/components/editor/MonacoWrapper.tsx` | Monaco ready-path focus | Modify |
| `spa/src/components/editor/MonacoWrapper.test.tsx` | AC1 | Modify |
| `spa/src/components/editor/TiptapEditor.tsx` | Tiptap focus + viewState save/restore | Modify |
| `spa/src/components/editor/TiptapEditor.test.tsx` | AC2/AC3/AC5/AC8 | Modify |
| `spa/src/components/editor/tiptapSelection.ts` | selection 合法 restore 純函式（I2） | **Create** |
| `spa/src/components/editor/tiptapSelection.test.ts` | AC6/AC7（真實 PM doc） | **Create** |
| `spa/src/stores/useEditorStore.ts` | `tiptapViewState` 欄位 + `saveTiptapViewState` | Modify |
| `spa/src/stores/useEditorStore.test.ts` | AC4 | Modify |
| `spa/src/components/editor/EditorPane.tsx` | wysiwyg 分支傳 viewState props | Modify |
| `spa/src/components/editor/__tests__/EditorPane.test.tsx` | AC9（wiring） | Modify |

---

## Task 1: Monaco ready-path focus

**Files:**
- Modify: `spa/src/components/editor/MonacoWrapper.tsx`
- Test: `spa/src/components/editor/MonacoWrapper.test.tsx`

**Interfaces:**
- Consumes: 既有 `Props`（含 `isActive`）。
- Produces: 無對外新 API；行為 = `handleMount` 時若 isActive 則 `ed.focus()`。

- [ ] **Step 1: 寫失敗測試（AC1）**

在 `MonacoWrapper.test.tsx` 既有 mock（`@monaco-editor/react` 把 `onMount` 立即以 fake editor 呼叫）下，新增：

```tsx
it('focuses the editor when it mounts while the pane is already active (AC1)', () => {
  // editor ready 晚於 isActive：mount 當下 isActive 已是 true
  render(<MonacoWrapper {...baseProps} isActive={true} />)
  // handleMount 觸發時應補 focus
  expect(focusSpy).toHaveBeenCalled()
})
```

> 若既有 test 尚無 `focusSpy`，在 fake editor 上加 `focus: focusSpy`（mirror 既有 `restoreViewState`/`addAction` spy 模式）。`baseProps` 沿用既有 helper；無則就地組 props（`content/language/modelId/isActive/initialViewState:null/onChange/onCursorChange/onViewStateChange/onSave` 全給 noop）。

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/editor/MonacoWrapper.test.tsx -t "AC1"`
Expected: FAIL（`focusSpy` 未被呼叫 — 既有 `handleMount` 不 focus）。

- [ ] **Step 3: 實作 — handleMount 補 focus + isActiveRef**

`MonacoWrapper.tsx`：新增 `isActiveRef` 同步，`handleMount` 末尾依 `isActiveRef.current` 補 focus。

```tsx
// 在既有 refs 區（onViewStateChangeRef 附近）新增：
const isActiveRef = useRef(isActive)

// 在既有 useEffect 區新增同步 effect：
useEffect(() => {
  isActiveRef.current = isActive
}, [isActive])
```

在 `handleMount` 內、`editorRef.current = ed` 與 `restoreViewState` 之後、`addAction` 之前插入：

```tsx
if (isActiveRef.current) {
  ed.focus()
}
```

> `handleMount` 是 `useCallback([initialViewState, onCursorChange])`，不含 `isActive` —— 用 `isActiveRef` 才能讀到最新值（closure stale 防護）。既有 `[isActive]` focus effect（`:78-81`）保留不動。

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/components/editor/MonacoWrapper.test.tsx`
Expected: PASS（含既有測試）。

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/MonacoWrapper.tsx spa/src/components/editor/MonacoWrapper.test.tsx
git commit -m "fix(editor): focus Monaco on mount when pane already active (#857)"
```

---

## Task 2: Tiptap ready-path focus（one-shot effect skeleton）

**Files:**
- Modify: `spa/src/components/editor/TiptapEditor.tsx`
- Test: `spa/src/components/editor/TiptapEditor.test.tsx`

**Interfaces:**
- Consumes: 既有 `Props`（含 `isActive`）。
- Produces: `isActiveRef`、`didRestoreRef`、一個 deps `[editor]` 的 one-shot ready effect（Task 4 會在此 effect 內加 selection/scroll restore）。行為 = editor 從 null→truthy 後若 isActive 補 `focusEditable()`，只跑一次。

- [ ] **Step 1: 強化 mock helper + 寫失敗測試（AC2/AC3）**

`TiptapEditor.test.tsx`：在 mock 區下方新增 helper（M1/M2），並改 `useEditorSpy` 預設先回 `undefined`（null transition）。

```tsx
// M2: 可變 state 的 mock editor
function makeMockEditor(overrides: Record<string, unknown> = {}) {
  return {
    getMarkdown: () => 'hello',
    commands: { setContent: vi.fn() },
    state: {
      selection: { from: 1, to: 1 },
      tr: { setSelection: vi.fn().mockReturnThis() },
    },
    view: { dispatch: vi.fn() },
    ...overrides,
  }
}
```

`beforeEach` 改為預設 editor 不存在（讓既有「editor 立即存在」的測試各自在 body 內 `useEditorSpy.mockReturnValue(makeMockEditor())` 後再 render；既有 4 個 render 測試前各補一行）：

```tsx
beforeEach(() => {
  focusSpy.mockReset()
  useEditorSpy.mockReturnValue(makeMockEditor()) // 既有測試維持「editor 已就緒」
})
```

新增 AC2/AC3：

```tsx
it('focuses on the null→editor ready transition when active (AC2, M1)', () => {
  useEditorSpy.mockReturnValue(undefined) // editor 尚未 ready
  const { rerender } = render(<TiptapEditor content="# Hi" isActive={true} onChange={() => {}} onSave={() => {}} />)
  focusSpy.mockClear()
  useEditorSpy.mockReturnValue(makeMockEditor()) // editor ready
  rerender(<TiptapEditor content="# Hi" isActive={true} onChange={() => {}} onSave={() => {}} />)
  expect(focusSpy).toHaveBeenCalled()
})

it('does NOT focus on ready transition when inactive (AC3, I1)', () => {
  useEditorSpy.mockReturnValue(undefined)
  const { rerender } = render(<TiptapEditor content="# Hi" isActive={false} onChange={() => {}} onSave={() => {}} />)
  focusSpy.mockClear()
  useEditorSpy.mockReturnValue(makeMockEditor())
  rerender(<TiptapEditor content="# Hi" isActive={false} onChange={() => {}} onSave={() => {}} />)
  expect(focusSpy).not.toHaveBeenCalled()
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/editor/TiptapEditor.test.tsx -t "AC2"`
Expected: FAIL（ready transition 不補 focus）。

- [ ] **Step 3: 實作 — isActiveRef + one-shot ready effect**

`TiptapEditor.tsx`：新增 refs 與 effect。

```tsx
// imports 區
import { useEffect, useRef } from 'react'

// component 內，containerRef 附近：
const isActiveRef = useRef(isActive)
const didRestoreRef = useRef(false)

useEffect(() => {
  isActiveRef.current = isActive
}, [isActive])
```

在既有 `useEditor({...})` 之後、既有 `[isActive]` effect 之前，新增 one-shot effect：

```tsx
// One-shot ready handler (Task 4 will prepend selection/scroll restore here, before focus).
useEffect(() => {
  if (!editor) return
  if (didRestoreRef.current) return
  didRestoreRef.current = true
  if (isActiveRef.current) focusEditable()
}, [editor])
```

> 既有 `[isActive]` effect（`:63-66`）與 mouseDown focus（`:75-78`）保留不動。one-shot effect 與 `[isActive]` effect 某些時序可能都 focus，無害（AC1/AC2 只驗「至少一次」）。

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/components/editor/TiptapEditor.test.tsx`
Expected: PASS（AC2/AC3 + 既有 5 測試）。

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/TiptapEditor.tsx spa/src/components/editor/TiptapEditor.test.tsx
git commit -m "fix(editor): focus Tiptap on ready transition when pane active (#857)"
```

---

## Task 3: store — tiptapViewState 欄位 + saveTiptapViewState

**Files:**
- Modify: `spa/src/stores/useEditorStore.ts`
- Test: `spa/src/stores/useEditorStore.test.ts`

**Interfaces:**
- Produces:
  - `TiptapViewState = { scrollTop: number; selection: { from: number; to: number } | null }`
  - `EditorPaneState.tiptapViewState: TiptapViewState | null`（初始 `null`）
  - action `saveTiptapViewState(paneId: string, viewState: TiptapViewState | null): void`

- [ ] **Step 1: 寫失敗測試（AC4）**

`useEditorStore.test.ts` 新增：

```tsx
it('tiptapViewState defaults to null and saveTiptapViewState writes it (AC4)', () => {
  const store = useEditorStore.getState()
  store.openBuffer('k1', 'hello', { language: 'markdown' })
  store.attachPane('p1', 'k1')
  expect(useEditorStore.getState().paneStates['p1'].tiptapViewState).toBeNull()

  store.saveTiptapViewState('p1', { scrollTop: 120, selection: { from: 3, to: 7 } })
  expect(useEditorStore.getState().paneStates['p1'].tiptapViewState).toEqual({
    scrollTop: 120,
    selection: { from: 3, to: 7 },
  })
})

it('saveTiptapViewState on a missing pane is a no-op (AC4)', () => {
  const before = useEditorStore.getState().paneStates
  useEditorStore.getState().saveTiptapViewState('nope', { scrollTop: 1, selection: null })
  expect(useEditorStore.getState().paneStates).toBe(before)
})
```

> 對齊既有測試的 `openBuffer`/`attachPane` 用法（見既有 `it('keeps shared buffer state separate...')`）；若既有 `beforeEach` 已 `clearAllBuffers`，沿用。

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/stores/useEditorStore.test.ts -t "AC4"`
Expected: FAIL（`tiptapViewState` undefined / `saveTiptapViewState` 不是 function）。

- [ ] **Step 3: 實作**

`useEditorStore.ts`：

型別（`EditorPaneState` 上方）：

```tsx
export interface TiptapViewState {
  scrollTop: number
  selection: { from: number; to: number } | null
}
```

`EditorPaneState`（行 27-33）加欄位：

```tsx
export interface EditorPaneState {
  bufferKey: string
  editorMode: EditorMode
  showDiff: boolean
  cursorPosition: { line: number; column: number }
  monacoViewState: editor.ICodeEditorViewState | null
  tiptapViewState: TiptapViewState | null
}
```

`EditorState` interface（`saveMonacoViewState` 宣告下方，行 50 附近）加：

```tsx
  saveTiptapViewState: (paneId: string, viewState: TiptapViewState | null) => void
```

`createPaneState`（行 56-64）加初值：

```tsx
    monacoViewState: null,
    tiptapViewState: null,
```

action（`saveMonacoViewState` 實作之後，行 303 附近，mirror 之）：

```tsx
  saveTiptapViewState: (paneId, viewState) => set((s) => {
    const paneState = s.paneStates[paneId]
    if (!paneState) return s
    return {
      paneStates: {
        ...s.paneStates,
        [paneId]: {
          ...paneState,
          tiptapViewState: viewState,
        },
      },
    }
  }),
```

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/stores/useEditorStore.test.ts`
Expected: PASS（AC4 + 既有測試，含「keeps shared buffer state separate」會看到新欄位但因用 `toMatchObject`/個別欄位斷言通常不受影響；若有 `toEqual` 整個 paneState 的既有測試，補上 `tiptapViewState: null`）。

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useEditorStore.ts spa/src/stores/useEditorStore.test.ts
git commit -m "feat(editor): add tiptapViewState pane field + saveTiptapViewState action (#857)"
```

---

## Task 4: selection 合法 restore 純函式（I2）

**Files:**
- Create: `spa/src/components/editor/tiptapSelection.ts`
- Test: `spa/src/components/editor/tiptapSelection.test.ts`

**Interfaces:**
- Produces:
  - `resolveRestoreSelection(doc: ProsemirrorNode, saved: { from: number; to: number }): Selection`
  - 單一路徑：各自 clamp from/to → `try TextSelection.create` → catch `Selection.near` → catch `Selection.atEnd`。永不 throw。

- [ ] **Step 1: 寫失敗測試（AC6/AC7，真實 PM）**

`tiptapSelection.test.ts`：用 `@tiptap/pm/model` 建最小 schema/doc，`@tiptap/pm/state` 驗結果。

```ts
import { describe, it, expect } from 'vitest'
import { Schema } from '@tiptap/pm/model'
import { TextSelection, Selection } from '@tiptap/pm/state'
import { resolveRestoreSelection } from './tiptapSelection'

const schema = new Schema({
  nodes: { doc: { content: 'paragraph+' }, paragraph: { content: 'text*' }, text: {} },
})
// doc: <p>hello world</p>  → text 位置 1..12, doc.content.size = 13
function makeDoc(text = 'hello world') {
  return schema.node('doc', null, [schema.node('paragraph', null, text ? [schema.text(text)] : [])])
}

it('preserves a legal range selection (AC6)', () => {
  const doc = makeDoc()
  const sel = resolveRestoreSelection(doc, { from: 1, to: 6 })
  expect(sel).toBeInstanceOf(TextSelection)
  expect(sel.from).toBe(1)
  expect(sel.to).toBe(6)
})

it('clamps an out-of-range selection to a legal inline position (AC7)', () => {
  const doc = makeDoc() // size 13; pos 13 is after the paragraph (parent=doc, not inline)
  expect(() => resolveRestoreSelection(doc, { from: 999, to: 1000 })).not.toThrow()
  const sel = resolveRestoreSelection(doc, { from: 999, to: 1000 })
  // clamp -> 13 (inlineContent false) -> Selection.near -> 12 (inline). Must NOT
  // assert only `to <= size`: create(doc,13,13) returns an illegal 13..13 that
  // ALSO satisfies `<= size`, so that weak assertion would false-green the old
  // try/catch path. Assert the endpoints land in inlineContent instead.
  expect(doc.resolve(sel.from).parent.inlineContent).toBe(true)
  expect(doc.resolve(sel.to).parent.inlineContent).toBe(true)
  expect(sel.from).toBeLessThan(doc.content.size)
})

it('falls back to a legal selection for an illegal (non-textblock) position (AC7)', () => {
  const doc = makeDoc()
  // pos 0 is before the paragraph → resolve(0).parent is the doc node
  // (inlineContent === false) → degrade to Selection.near (NOT via a throw —
  // TextSelection.create would silently return an illegal 0..0 here).
  const sel = resolveRestoreSelection(doc, { from: 0, to: 0 })
  expect(sel).toBeInstanceOf(Selection)
  // near(resolve(0)) lands inside the textblock (pos >= 1)
  expect(sel.from).toBeGreaterThanOrEqual(1)
})

it('never throws on an empty doc (AC7)', () => {
  const doc = makeDoc('')
  expect(() => resolveRestoreSelection(doc, { from: 5, to: 9 })).not.toThrow()
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/editor/tiptapSelection.test.ts`
Expected: FAIL（`resolveRestoreSelection` 不存在）。

- [ ] **Step 3: 實作**

`tiptapSelection.ts`：

```ts
import type { Node as ProsemirrorNode } from '@tiptap/pm/model'
import { Selection, TextSelection } from '@tiptap/pm/state'

const clamp = (n: number, lo: number, hi: number) => Math.min(Math.max(n, lo), hi)

/**
 * Resolve a saved {from,to} into a legal ProseMirror Selection (I2).
 *
 * NOTE: TextSelection.create does NOT throw on non-textblock positions — it
 * only console.warn's and returns an *illegal* selection (verified locally:
 * create(doc,0,0) -> TextSelection 0..0). So we MUST check inlineContent up
 * front rather than relying on try/catch. clamp first (doc.resolve throws
 * RangeError out of range); keep the range when both ends are inline content,
 * otherwise degrade to Selection.near (which never throws).
 */
export function resolveRestoreSelection(
  doc: ProsemirrorNode,
  saved: { from: number; to: number },
): Selection {
  const max = doc.content.size
  const from = clamp(saved.from, 0, max)
  const to = clamp(saved.to, 0, max)
  const $from = doc.resolve(from)
  const $to = doc.resolve(to)
  if ($from.parent.inlineContent && $to.parent.inlineContent) {
    return TextSelection.create(doc, from, to)
  }
  return Selection.near($from)
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/components/editor/tiptapSelection.test.ts`
Expected: PASS（4 測試）。

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/tiptapSelection.ts spa/src/components/editor/tiptapSelection.test.ts
git commit -m "feat(editor): add legal Tiptap selection restore helper (#857)"
```

---

## Task 5: TiptapEditor viewState save/restore wiring

**Files:**
- Modify: `spa/src/components/editor/TiptapEditor.tsx`
- Test: `spa/src/components/editor/TiptapEditor.test.tsx`

**Interfaces:**
- Consumes: `resolveRestoreSelection`（Task 4）、`TiptapViewState`（Task 3，re-declare 或 import）、Task 2 的 `didRestoreRef`/`isActiveRef`/one-shot effect。
- Produces: props `initialViewState?: TiptapViewState | null`、`onViewStateChange?: (vs: TiptapViewState) => void`。unmount 從 `editorRef.current` 存 viewState；one-shot effect 擴充為 selection→scroll→focus；初次 content sync 跳過。

- [ ] **Step 1: 寫失敗測試（AC5/AC8）**

`TiptapEditor.test.tsx`：mock `./tiptapSelection`（隔離純函式，專注 wiring/順序），新增測試。

```tsx
// 檔案 mock 區新增：
vi.mock('./tiptapSelection', () => ({
  resolveRestoreSelection: vi.fn(() => ({ __fake: 'selection' })),
}))
```

```tsx
it('saves scrollTop + live selection on unmount from editorRef (AC5, M2)', () => {
  const ed = makeMockEditor({ state: { selection: { from: 4, to: 9 }, tr: { setSelection: vi.fn().mockReturnThis() } } })
  useEditorSpy.mockReturnValue(ed)
  const onViewStateChange = vi.fn()
  const { unmount, container } = render(
    <TiptapEditor content="hi" isActive={false} initialViewState={null}
      onChange={() => {}} onViewStateChange={onViewStateChange} onSave={() => {}} />,
  )
  // 模擬使用者捲動
  const scrollRoot = container.querySelector('[data-testid="tiptap-scroll-root"]') as HTMLElement
  Object.defineProperty(scrollRoot, 'scrollTop', { value: 88, writable: true, configurable: true })
  unmount()
  expect(onViewStateChange).toHaveBeenCalledWith({ scrollTop: 88, selection: { from: 4, to: 9 } })
})

it('restores selection AND scroll BEFORE focus on ready (AC8, I3)', () => {
  const dispatch = vi.fn()
  const setSelection = vi.fn().mockReturnValue('TR')
  const ed = makeMockEditor({
    state: { selection: { from: 1, to: 1 }, doc: {}, tr: { setSelection } },
    view: { dispatch },
  })
  const initial = { scrollTop: 50, selection: { from: 2, to: 5 } }
  // focusSpy reads the scroll container's scrollTop at focus time → proves
  // scroll was already restored BEFORE focus (I3). Query the DOM inside the
  // spy (the container is mounted in the same commit; a captured variable
  // would still be undefined when the effect runs).
  let scrollAtFocus = -1
  focusSpy.mockImplementation(() => {
    const root = document.querySelector('[data-testid="tiptap-scroll-root"]') as HTMLElement | null
    if (scrollAtFocus === -1 && root) scrollAtFocus = root.scrollTop
  })
  useEditorSpy.mockReturnValue(ed)
  render(
    <TiptapEditor content="hi" isActive={true} initialViewState={initial}
      onChange={() => {}} onViewStateChange={() => {}} onSave={() => {}} />,
  )
  // (1) selection restore goes through resolveRestoreSelection (mocked → {__fake})
  expect(setSelection).toHaveBeenCalledWith({ __fake: 'selection' })
  // (2) selection dispatch happens before the first focus call
  expect(dispatch).toHaveBeenCalled()
  expect(dispatch.mock.invocationCallOrder[0]).toBeLessThan(focusSpy.mock.invocationCallOrder[0])
  // (3) scroll was restored before focus (scrollTop already 50 at focus time)
  expect(scrollAtFocus).toBe(50)
})
```

> AC8 以 `invocationCallOrder` 驗 selection restore（`view.dispatch`）早於 `focusEditable`（`focusSpy`）—— 順序契約而非真實 caret scroll（jsdom 不支援，spec §5.0/M4）。scroll 早於 focus 由 Step 3 的 effect 內同步順序保證。

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/editor/TiptapEditor.test.tsx -t "AC5"`
Expected: FAIL（無 `onViewStateChange` 行為 / `editorRef`）。

- [ ] **Step 3: 實作 — props / editorRef / unmount / 擴充 restore / 跳過初次 sync**

`TiptapEditor.tsx`：

imports：

```tsx
import { useEditor, EditorContent, type Editor } from '@tiptap/react'
import { resolveRestoreSelection } from './tiptapSelection'
import type { TiptapViewState } from '../../stores/useEditorStore'
```

`Props` 加兩個可選 props：

```tsx
interface Props {
  content: string
  isActive: boolean
  initialViewState?: TiptapViewState | null
  onChange: (markdown: string) => void
  onViewStateChange?: (viewState: TiptapViewState) => void
  onSave: () => void
}
```

簽名解構加 `initialViewState`、`onViewStateChange`。新增 refs（containerRef 附近）：

```tsx
const editorRef = useRef<Editor | null>(null)
const onViewStateChangeRef = useRef(onViewStateChange)
const hasInitializedRef = useRef(false)

useEffect(() => {
  onViewStateChangeRef.current = onViewStateChange
}, [onViewStateChange])
```

`useEditor(...)` 之後同步 editorRef：

```tsx
useEffect(() => {
  editorRef.current = editor
}, [editor])
```

改既有 content-sync effect（`:54-61`）—— **跳過初次**（I5）：

```tsx
useEffect(() => {
  if (!editor) return
  if (!hasInitializedRef.current) {
    hasInitializedRef.current = true // 首次 content 已由 useEditor({ content }) 設好，勿覆寫 restore
    return
  }
  if (internalUpdateRef.current) {
    internalUpdateRef.current = false
    return
  }
  editor.commands.setContent(content, { emitUpdate: false, contentType: 'markdown' })
}, [content, editor])
```

擴充 Task 2 的 one-shot effect（加 selection→scroll，focus 之前）：

```tsx
useEffect(() => {
  if (!editor) return
  if (didRestoreRef.current) return
  didRestoreRef.current = true
  const vs = initialViewState
  if (vs?.selection) {
    const sel = resolveRestoreSelection(editor.state.doc, vs.selection)
    editor.view.dispatch(editor.state.tr.setSelection(sel))
  }
  if (vs && containerRef.current) {
    containerRef.current.scrollTop = vs.scrollTop
  }
  if (isActiveRef.current) focusEditable()
}, [editor])
```

新增 unmount cleanup（從 live editorRef 存）。**必須 `useLayoutEffect`**（非 `useEffect`，見下）：

```tsx
useLayoutEffect(() => {
  return () => {
    onViewStateChangeRef.current?.({
      scrollTop: containerRef.current?.scrollTop ?? 0,
      selection: editorRef.current
        ? { from: editorRef.current.state.selection.from, to: editorRef.current.state.selection.to }
        : null,
    })
  }
}, [])
```

> `editorRef` 而非 render 閉包的 `editor`：`[]` cleanup 閉包初次抓的 `editor` 為 null（finding #3）。
> **`useLayoutEffect` 而非 `useEffect`**（實測）：`useEffect` 的 passive cleanup 在 React detach DOM ref 之後跑，`containerRef.current` 已 null、`scrollTop` 讀成 0；`useLayoutEffect` cleanup 在 ref detach 前跑，能讀正確 scrollTop。`editorRef` 是普通實例 ref（不被 detach），兩種 effect 都讀得到。記得 `import { useLayoutEffect } from 'react'`。

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/components/editor/TiptapEditor.test.tsx`
Expected: PASS（AC2/AC3/AC5/AC8 + 既有測試）。

> 注意：mock `./tiptapSelection` 後，AC8 的 `editor.state.tr.setSelection` 須回非 undefined（`makeMockEditor` 的 `tr.setSelection` 用 `mockReturnValue('TR')` 或 `mockReturnThis()`）。

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/editor/TiptapEditor.tsx spa/src/components/editor/TiptapEditor.test.tsx
git commit -m "feat(editor): Tiptap saves/restores scroll + cursor viewState (#857)"
```

---

## Task 6: EditorPane wiring

**Files:**
- Modify: `spa/src/components/editor/EditorPane.tsx`
- Test: `spa/src/components/editor/__tests__/EditorPane.test.tsx`

**Interfaces:**
- Consumes: `TiptapEditor` 的 `initialViewState`/`onViewStateChange`（Task 5）、`saveTiptapViewState`（Task 3）、`paneState.tiptapViewState`。
- Produces: wysiwyg 分支把 store 與 TiptapEditor 接起來。

- [ ] **Step 1: 改 mock 並寫失敗測試（AC9/M3）**

`__tests__/EditorPane.test.tsx`：把 TiptapEditor mock 改成 capture props（M3）。

```tsx
const tiptapPropsSpy = vi.hoisted(() => vi.fn())
vi.mock('../TiptapEditor', () => ({
  TiptapEditor: (props: { initialViewState: unknown; onViewStateChange: (vs: unknown) => void }) => {
    tiptapPropsSpy(props)
    return (
      <button
        data-testid="tiptap-editor"
        onClick={() => props.onViewStateChange({ scrollTop: 42, selection: { from: 2, to: 3 } })}
      />
    )
  },
}))
```

新增測試（沿用既有切 wysiwyg 的模式 — 見既有行 154 `setEditorMode(pane.id, 'wysiwyg')`）：

```tsx
it('passes tiptapViewState into TiptapEditor and saves it back on change (AC9)', async () => {
  const pane = createPane('/notes/editor.md')
  // 預先放一個 viewState 到 store
  // （render markdown buffer → 切 wysiwyg）
  render(<EditorPane pane={pane} isActive={true} />)
  await waitFor(() => screen.getByTestId('editor-status-bar'))
  act(() => { useEditorStore.getState().setEditorMode(pane.id, 'wysiwyg') })
  act(() => { useEditorStore.getState().saveTiptapViewState(pane.id, { scrollTop: 7, selection: { from: 1, to: 1 } }) })

  await waitFor(() => screen.getByTestId('tiptap-editor'))
  // initialViewState 確實傳入
  expect(tiptapPropsSpy).toHaveBeenLastCalledWith(
    expect.objectContaining({ initialViewState: { scrollTop: 7, selection: { from: 1, to: 1 } } }),
  )
  // onViewStateChange 回呼確實寫回 store
  fireEvent.click(screen.getByTestId('tiptap-editor'))
  expect(useEditorStore.getState().paneStates[pane.id].tiptapViewState).toEqual({ scrollTop: 42, selection: { from: 2, to: 3 } })
})
```

> 確認 `EditorPane` 的 props 形狀（既有測試怎麼 render `<EditorPane .../>`）；沿用既有 render 簽名。buffer 的 language 必須是 `markdown` 才會走 wysiwyg 分支（`createPane` 用 `.md`；若 language 由 stat/副檔名推導，確保 markdown）。

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/components/editor/__tests__/EditorPane.test.tsx -t "AC9"`
Expected: FAIL（TiptapEditor 未收到 `initialViewState` / 點擊不寫回 store）。

- [ ] **Step 3: 實作**

`EditorPane.tsx` wysiwyg 分支（行 435-440）。加 `key={buffer.modelId}`（I6，防禦性對齊 Monaco 分支 `:422` — 換 buffer 必經 attachPane reset→raw 使 Tiptap unmount，故此 key 是一致性/防未來而非修當前 bug）：

```tsx
            <TiptapEditor
              key={buffer.modelId}
              content={buffer.content}
              isActive={isActive}
              initialViewState={paneState?.tiptapViewState ?? null}
              onChange={(md) => useEditorStore.getState().updateContent(key, md)}
              onViewStateChange={(vs) => useEditorStore.getState().saveTiptapViewState(paneId, vs)}
              onSave={handleSave}
            />
```

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/components/editor/__tests__/EditorPane.test.tsx`
Expected: PASS（AC9 + 既有測試）。

- [ ] **Step 5: 全套驗證 + Commit**

```bash
cd spa && npx vitest run src/components/editor src/stores/useEditorStore.test.ts && pnpm run lint && pnpm run build
```
Expected: 全 PASS、lint clean、build 成功。

```bash
git add spa/src/components/editor/EditorPane.tsx spa/src/components/editor/__tests__/EditorPane.test.tsx
git commit -m "feat(editor): wire Tiptap viewState through EditorPane (#857)"
```

---

## Self-Review（plan vs spec）

**Spec coverage：**
- G1 focus → Task 1（Monaco AC1）+ Task 2（Tiptap AC2/AC3）。
- G2 viewState → Task 3（store AC4）+ Task 4（selection helper AC6/AC7）+ Task 5（save/restore AC5/AC8）+ Task 6（wiring AC9）。
- I1（active-only focus）→ Task 1/2 `isActiveRef` gate + AC3。
- I2（inlineContent 前置檢查）→ Task 4 純函式 `resolveRestoreSelection` + AC6/AC7。
- I3（restore→focus 順序）→ Task 5 effect 內順序 + AC8。
- I4（同生命週期）→ Task 3 paneState 欄位、隨 pane 清。
- I5（one-shot + 跳過初次 sync）→ Task 5 `didRestoreRef` + `hasInitializedRef`。
- I6（Tiptap key）→ Task 6 `key={buffer.modelId}`（堵 transient reuse；known-limitation 不寫整合回歸測試）。
- M1–M4（mock 前提）→ Task 2 `makeMockEditor`/null-transition、Task 4 真實 PM、Task 6 prop capture。
- N1–N4（non-goals）：未碰 Monaco viewState / persist / reload / 模式轉換邏輯。✓

**plan-review（round-1）處置（明示）：**
- Finding 1（high，已修）：`TextSelection.create` 對非法位置**不 throw、只 warn**（已本地用真實 PM 實證：`create(doc,0,0)` 回 `TextSelection 0..0`）。`resolveRestoreSelection`（Task 4）改為 **inlineContent 前置檢查**而非 try/catch；spec I2/§4.2/AC7 同步更新。
- Finding 2（round-1 high → round-2 修正）：`attachPane` 是 commit 後 effect，切到另一已載入 markdown buffer 的**第一個 render** 仍讀舊 `paneState.editorMode='wysiwyg'` → transient TiptapEditor reuse **可達一個 render**（最終穩定態為 Monaco/raw）。採 `key={buffer.modelId}`（Task 6）堵此 transient + 對齊 Monaco。**known-limitation 不寫回歸測試**（使用者決定）：mock TiptapEditor 測不出真 cleanup 副作用；「驗 remount with new modelId」因最終態 Monaco 而 query 不到；真正可觀測的（transient 污染 B 的 `tiptapViewState`）需 unmock 整合測試（jsdom 跑 ProseMirror，成本高），危害極小（一個 render，最壞 `scrollTop:0`）不值得。
- Finding 1b（round-2 high，已修）：AC7 越界 case 原只驗 `to <= size`，**假綠** —— `create(doc,size,size)` 回非法 `size..size` 也滿足。已改驗退化結果落在 `inlineContent`（本地實證 `near(resolve(13))` 回 `12..12`、inline true）。
- Finding 3（round-2 medium，已修）：AC8 原只驗 `dispatch < focus`，未驗 scroll 順序。已補「focus 時 scroll container `scrollTop` 已 = restore 值」+ 驗 `tr.setSelection` 收到 helper 回傳值。
- 設計：selection restore 抽純函式 `resolveRestoreSelection` 以真實 PM doc 單元測 AC6/AC7（對齊 M4）；TiptapEditor 測試 mock 此 helper 專注 wiring/順序。

**Placeholder scan：** 無 TODO/TBD；每 code step 含完整 code。

**Type consistency：** `TiptapViewState`（Task 3 export）→ Task 5 import 同名；`resolveRestoreSelection(doc, {from,to}): Selection`（Task 4）→ Task 5 同簽名呼叫；`saveTiptapViewState(paneId, vs|null)`（Task 3）→ Task 5/6 同呼叫。`didRestoreRef`/`isActiveRef`（Task 2 建）→ Task 5 沿用同名。

**待驗風險（實作時注意）：**
- Task 2 改 `beforeEach` 預設後，既有 4 個「editor 已就緒」測試需確認仍綠（`makeMockEditor()` 提供 `commands.setContent` 等）。
- Task 6 既有 wysiwyg 測試（行 154+）會經過改動後的 TiptapEditor mock（now capture props 並 render `<button>`）；確認既有斷言（`data-editor-mode` 等查 status bar、不依賴 tiptap mock 內容）不破。

---

## PR Review 處置（R1 + R2 三平行）— code 已更新，本段為軌跡

> 實作以 code/spec 為 SOT；以下偏離了上方 Task 原文，spec v6 已同步。

- **R1 P2（unmount 未 ready 覆寫）**：unmount cleanup 加 `if (!didRestoreRef.current) return`（editor 從未 ready / restore 未跑 → 不寫回，避免把既存 viewState 覆寫成 `scrollTop:0`）。
- **R2 D1（unmount race，high）**：`editorRef` 同步 effect + one-shot restore effect 改 **`useLayoutEffect`**（非上方 Task 5 寫的 `useEffect`）—— 否則 editor-ready commit 後立刻 unmount 時，layout cleanup 早於 passive effect，看到 `editorRef=null`/`didRestoreRef=false` → viewState 遺失。AC5 改走 `null→editor` transition（M1）才測得到 cleanup 讀 `editorRef` 而非閉包。
- **R2 D2（NodeSelection 降級，medium）**：`TiptapViewState.selection` 加 `type: 'text' | 'node'`；save 以 `instanceof NodeSelection` 判定；`resolveRestoreSelection` 先處理 node（`NodeSelection.isSelectable` → `NodeSelection.create`）再 fall through text path。Task 4 測試加 hr schema 的 node 還原 + 退化。所有既有 `selection: {from,to}` literal 補 `type`。
- **R2 A1（stale paneState，high）+ H3 → R3 修正**：原以 props gating（`bufferKey===key` 傳 null）+ 判「lazy 不可達」。**R3 推翻**：`React.lazy` cache 後同步 mount，transient 可達（第二次起），props-null gating 反而讓 Tiptap mount with null 鎖 `didRestoreRef` 漏 restore。**改 gating render**：EditorPane wysiwyg 分支僅在 `paneState?.bufferKey === key` 才 render `TiptapEditor`，否則 loading fallback（stale 時根本不 mount）。**有 regression test**（warm lazy cache + freeze attachPane 重現 transient；移除 gate 即紅 = 非 vacuous）。
- **R2 H2（AC1 mock-limited）**：AC1 測試加註解標明同步 mock 限制。
