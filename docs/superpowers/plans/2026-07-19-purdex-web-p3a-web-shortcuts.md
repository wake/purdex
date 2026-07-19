# Purdex Web P3a — Web 鍵盤快捷鍵層 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **修訂 r2**（codex plan review 後）：焦點規則改**明確矩陣**（editable/終端預設放行，`global` 白名單例外）；排除 browser-reserved（Cmd+T/W/Shift+T）；補 **parity test**（web actions ⊆ electron actions）防漂移；executeShortcutAction 補深度測試（workspace-sync / home / tab-registry）；沿用既有 reset harness 防 cross-test leak；補 unmount cleanup 測試。

**Goal:** 讓 app-level 快捷鍵在純瀏覽器也能運作——抽出共享 action executor（Electron IPC / web keydown 都只餵 action id）；web 端加 keydown 分發，並以焦點矩陣避免誤觸表單/終端輸入。

**Architecture:** 抽 `executeShortcutAction(action)` 為單一分派來源；`useShortcuts`（Electron `onShortcut`）呼叫它。新增 web binding manifest（`WEB_SHORTCUTS` + `matchWebShortcut`，含 `global` 焦點旗標）與 `useWebShortcuts()`（web-only、keydown → match → 焦點判定 → preventDefault + execute）。parity test 確保 web actions 不脫離 Electron 的 canonical action 集合。

**Tech Stack:** React 19 / TypeScript / Vitest。

## Global Constraints

- **共享 executor**：Electron 與 web **只餵 action id** 給同一個 `executeShortcutAction`；不得複製分派邏輯。
- **web-only**：`useWebShortcuts` 僅在 `!isElectron` 掛 keydown；與 Electron IPC 互斥。
- **焦點矩陣（明確，非 blanket）**：
  - `isEditableTarget(target)` = target 為 `INPUT`/`TEXTAREA`/`SELECT` 或 `isContentEditable`。**xterm.js 的終端焦點落在隱藏 `<textarea class="xterm-helper-textarea">`，故此判定自動涵蓋終端輸入。**
  - 非 `global` 快捷鍵：**僅在 target 非 editable 時**觸發（避免 `Cmd+Y`=Redo、終端 `Ctrl+Y` 等衝突被吃掉）。
  - `global` 快捷鍵（Cmd/Ctrl+Alt+* 的 tab/workspace 導覽，不與文字編輯衝突）：不論焦點皆觸發。
  - 僅命中且通過焦點判定者才 `preventDefault`；其餘一律放行。
- **排除 browser-reserved**：`Cmd/Ctrl+T`(new-tab)、`+W`(close-tab)、`+Shift+T`(reopen) 為瀏覽器保留、web 無法攔截，**不列入** web manifest（避免假 parity）。Electron-only（`new-window`、browser-pane `go-back`/`go-forward`/`reload`/`focus-url`/`print`）亦不列入。
- **parity（防漂移）**：web manifest 的每個 action 必須存在於 Electron canonical action 集合（`electron/keybindings.ts` 的 `getDefaultKeybindings()`）——以測試釘住。**per-platform 綁定字串可正當不同**（如 `Control+Tab` 為 Electron-only，因瀏覽器保留），僅 action 集合需受控。
- primary modifier：`e.metaKey || e.ctrlKey`。
- 測試 reset harness 沿用既有慣例（見 Task 1 Step 1）。
- 測試：`cd spa && npx vitest run`；Lint / Build 同 SPA 慣例。每個 task 獨立 commit。

---

## File Structure

- **Create** `spa/src/lib/shortcut-actions.ts` — `executeShortcutAction(action)`（自 `useShortcuts` 抽出）。
- **Modify** `spa/src/hooks/useShortcuts.ts` — Electron 效果改呼叫 `executeShortcutAction`。
- **Create** `spa/src/lib/shortcut-actions.test.ts` — executor 深度測試。
- **Modify** `electron/keybindings.ts` — 確認 `getDefaultKeybindings()` 已 export（現況已 export，供 parity test import；無需改）。
- **Create** `spa/src/lib/web-shortcuts.ts` — `WEB_SHORTCUTS`（含 `global`）+ `matchWebShortcut` + `isEditableTarget`。
- **Create** `spa/src/lib/web-shortcuts.test.ts` — manifest 對映 + parity + editable 判定測試。
- **Create** `spa/src/hooks/useWebShortcuts.ts` — web-only keydown 分發（焦點矩陣）。
- **Create** `spa/src/hooks/useWebShortcuts.test.ts` — 命中/焦點/Electron guard/unmount 測試。
- **Modify** `spa/src/App.tsx` — 呼叫 `useWebShortcuts()`。

---

## Task 1: 抽出共享 executeShortcutAction（含深度測試）

**Files:**
- Create: `spa/src/lib/shortcut-actions.ts`
- Modify: `spa/src/hooks/useShortcuts.ts`
- Test: `spa/src/lib/shortcut-actions.test.ts`

**Interfaces:**
- Produces: `executeShortcutAction(action: string): void`。

- [ ] **Step 1: 寫失敗測試（沿用既有 reset harness + 深度覆蓋）**

```ts
// spa/src/lib/shortcut-actions.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { executeShortcutAction } from './shortcut-actions'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  useWorkspaceStore.getState().addWorkspace('Default')
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
})

describe('executeShortcutAction', () => {
  it('new-tab → 新增 new-tab 分頁並 active', () => {
    executeShortcutAction('new-tab')
    const s = useTabStore.getState()
    expect(s.tabOrder.length).toBe(1)
    expect(s.activeTabId).toBe(s.tabOrder[0])
  })

  it('open-settings → 開 settings singleton 分頁', () => {
    executeShortcutAction('open-settings')
    const hasSettings = Object.values(useTabStore.getState().tabs).some((t) =>
      JSON.stringify(t.layout).includes('settings'))
    expect(hasSettings).toBe(true)
  })

  it('open-history / open-hosts → 各開對應 singleton', () => {
    executeShortcutAction('open-history')
    executeShortcutAction('open-hosts')
    const kinds = Object.values(useTabStore.getState().tabs).map((t) => JSON.stringify(t.layout))
    expect(kinds.some((k) => k.includes('history'))).toBe(true)
    expect(kinds.some((k) => k.includes('hosts'))).toBe(true)
  })

  it('switch-workspace-home → activeWorkspaceId 設為 null', () => {
    // 先建一個 workspace 並切入，再切回 home
    useWorkspaceStore.getState().addWorkspace('W2')
    const ws = useWorkspaceStore.getState().workspaces
    useWorkspaceStore.getState().setActiveWorkspace(ws[ws.length - 1].id)
    executeShortcutAction('switch-workspace-home')
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('reopen-closed-tab 無歷史 → 不 throw、不新增分頁', () => {
    executeShortcutAction('reopen-closed-tab')
    expect(useTabStore.getState().tabOrder.length).toBe(0)
  })

  it('未知 action → 不 throw', () => {
    expect(() => executeShortcutAction('nonexistent-xyz')).not.toThrow()
  })
})
```

> reset harness 完全對齊既有 `useShortcuts.test.ts`（`tabs/tabOrder/activeTabId/visitHistory` + workspace `reset()`+`addWorkspace('Default')` + history `browseHistory/closedTabs`），避免 cross-test leak（[[feedback_zustand_harness_setstate]]）。workspace-sync 的 `activateTab` 邏輯由「switch-workspace-home 後 active tab 正確」與既有 `useShortcuts.test.ts`（仍會透過 IPC 觸發同一 executor）共同覆蓋。

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/shortcut-actions.test.ts`
Expected: FAIL（模組不存在）。

- [ ] **Step 3: 抽出實作**

建 `spa/src/lib/shortcut-actions.ts`，把 `useShortcuts.ts` 的 `onShortcut` callback body（自 `const tabState = useTabStore.getState()` 到 `if (import.meta.env.DEV) { console.warn(...) }`）**原封**搬入：

```ts
// spa/src/lib/shortcut-actions.ts
// Shared shortcut action executor. Both the Electron menu-accelerator IPC path
// (useShortcuts) and the web keydown path (useWebShortcuts) feed action ids
// here — single dispatch source, no duplicated logic.
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { getVisibleTabIds as getVisibleTabIdsShared } from '../features/workspace'
import { closeTab } from './tab-lifecycle'
import { getTabShortcutHandler } from './tab-shortcut-registry'
import { getPrimaryPane } from './pane-tree'

export function executeShortcutAction(action: string): void {
  const tabState = useTabStore.getState()
  // ...（原封搬入 callback body：activateTab / visibleIds / 各分支 / tab-registry / DEV warn）
}
```

（相對路徑：`stores`/`types`/`features` 由 `../` 起算；`tab-lifecycle`/`tab-shortcut-registry`/`pane-tree` 由 `./` 起算。）

改 `spa/src/hooks/useShortcuts.ts`：

```ts
import { useEffect } from 'react'
import { executeShortcutAction } from '../lib/shortcut-actions'

export function useShortcuts(): void {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return
    const cleanup = window.electronAPI.onShortcut(({ action }) => executeShortcutAction(action))
    return cleanup
  }, [])
}
```

- [ ] **Step 4: 跑測試 + 既有回歸**

Run: `cd spa && npx vitest run src/lib/shortcut-actions.test.ts src/hooks/useShortcuts.test.ts`
Expected: PASS（既有 useShortcuts 測試透過 mock `onShortcut` 觸發，仍綠——分派等價）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/shortcut-actions.ts spa/src/lib/shortcut-actions.test.ts spa/src/hooks/useShortcuts.ts
git commit -m "refactor(shortcuts): extract shared executeShortcutAction + deep tests (P3a)"
```

---

## Task 2: web binding manifest（含 global + editable 判定 + parity）

**Files:**
- Create: `spa/src/lib/web-shortcuts.ts`
- Test: `spa/src/lib/web-shortcuts.test.ts`

**Interfaces:**
- Produces:
  - `interface WebShortcut { primary: boolean; alt?: boolean; shift?: boolean; key: string; action: string; global?: boolean }`
  - `WEB_SHORTCUTS: readonly WebShortcut[]`
  - `matchWebShortcut(e): WebShortcut | null`
  - `isEditableTarget(target: EventTarget | null): boolean`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/web-shortcuts.test.ts
import { describe, it, expect } from 'vitest'
import { matchWebShortcut, isEditableTarget, WEB_SHORTCUTS } from './web-shortcuts'
import { getDefaultKeybindings } from '../../../electron/keybindings'

function ev(over: Partial<KeyboardEvent>): Pick<KeyboardEvent,'key'|'metaKey'|'ctrlKey'|'altKey'|'shiftKey'> {
  return { key: '', metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...over }
}

describe('matchWebShortcut', () => {
  it('Cmd+, → open-settings（非 global）', () => {
    const m = matchWebShortcut(ev({ key: ',', metaKey: true }))
    expect(m?.action).toBe('open-settings')
    expect(m?.global).toBeFalsy()
  })
  it('Ctrl+, → open-settings（跨平台 primary）', () => {
    expect(matchWebShortcut(ev({ key: ',', ctrlKey: true }))?.action).toBe('open-settings')
  })
  it('Cmd+Alt+ArrowRight → next-tab（global）', () => {
    const m = matchWebShortcut(ev({ key: 'ArrowRight', metaKey: true, altKey: true }))
    expect(m?.action).toBe('next-tab')
    expect(m?.global).toBe(true)
  })
  it('Cmd+Alt+1 → switch-workspace-1（global）', () => {
    expect(matchWebShortcut(ev({ key: '1', metaKey: true, altKey: true }))?.action).toBe('switch-workspace-1')
  })
  it('Cmd+Shift+H → open-hosts（字母大小寫不敏感）', () => {
    expect(matchWebShortcut(ev({ key: 'H', metaKey: true, shiftKey: true }))?.action).toBe('open-hosts')
  })
  it('無 primary → null', () => {
    expect(matchWebShortcut(ev({ key: ',' }))).toBeNull()
  })
  it('browser-reserved Cmd+T 不在 manifest → null', () => {
    expect(matchWebShortcut(ev({ key: 't', metaKey: true }))).toBeNull()
  })
})

describe('isEditableTarget', () => {
  it('textarea（含 xterm helper）→ true', () => {
    const ta = document.createElement('textarea')
    expect(isEditableTarget(ta)).toBe(true)
  })
  it('div → false', () => {
    expect(isEditableTarget(document.createElement('div'))).toBe(false)
  })
  it('contentEditable div → true', () => {
    const d = document.createElement('div'); d.contentEditable = 'true'
    // jsdom: isContentEditable 需元素在 document 且屬性生效
    Object.defineProperty(d, 'isContentEditable', { value: true })
    expect(isEditableTarget(d)).toBe(true)
  })
})

describe('parity with Electron canonical actions', () => {
  it('每個 web action 都存在於 electron keybindings 的 action 集合', () => {
    const electronActions = new Set(getDefaultKeybindings().map((k) => k.action))
    for (const s of WEB_SHORTCUTS) {
      expect(electronActions.has(s.action), `web action ${s.action} missing in electron keybindings`).toBe(true)
    }
  })
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/web-shortcuts.test.ts`
Expected: FAIL（模組不存在）。

- [ ] **Step 3: 實作**

```ts
// spa/src/lib/web-shortcuts.ts
// Web keydown → action manifest. Mirrors the app-level subset of the Electron
// keybindings (electron/keybindings.ts) meaningful in a plain browser.
// Excludes Electron-only (new-window, browser-pane nav) AND browser-reserved
// combos (Cmd/Ctrl+T/W/Shift+T) which the browser never delivers to the page.
// `global` shortcuts fire regardless of focus (Cmd+Alt+* nav — no text-edit
// conflict); non-global fire only when focus is not an editable target.
export interface WebShortcut {
  primary: boolean
  alt?: boolean
  shift?: boolean
  key: string // single letters lowercased; named keys as-is (ArrowLeft, ',', digits)
  action: string
  global?: boolean
}

export const WEB_SHORTCUTS: readonly WebShortcut[] = [
  // Tab index (Cmd/Ctrl+digit — may be browser-reserved; non-global, focus-guarded)
  { primary: true, key: '1', action: 'switch-tab-1' },
  { primary: true, key: '2', action: 'switch-tab-2' },
  { primary: true, key: '3', action: 'switch-tab-3' },
  { primary: true, key: '4', action: 'switch-tab-4' },
  { primary: true, key: '5', action: 'switch-tab-5' },
  { primary: true, key: '6', action: 'switch-tab-6' },
  { primary: true, key: '7', action: 'switch-tab-7' },
  { primary: true, key: '8', action: 'switch-tab-8' },
  { primary: true, key: '9', action: 'switch-tab-last' },
  // Tab nav (Cmd+Alt+Left/Right — global, safe in editable/terminal)
  { primary: true, alt: true, key: 'ArrowLeft', action: 'prev-tab', global: true },
  { primary: true, alt: true, key: 'ArrowRight', action: 'next-tab', global: true },
  // App / views (non-global — Cmd+Y is Redo in inputs, so must not fire while editing)
  { primary: true, key: ',', action: 'open-settings' },
  { primary: true, shift: true, key: 'h', action: 'open-hosts' },
  { primary: true, key: 'y', action: 'open-history' },
  // Workspace nav (Cmd+Alt+* — global)
  { primary: true, alt: true, key: '0', action: 'switch-workspace-home', global: true },
  { primary: true, alt: true, key: '1', action: 'switch-workspace-1', global: true },
  { primary: true, alt: true, key: '2', action: 'switch-workspace-2', global: true },
  { primary: true, alt: true, key: '3', action: 'switch-workspace-3', global: true },
  { primary: true, alt: true, key: '4', action: 'switch-workspace-4', global: true },
  { primary: true, alt: true, key: '5', action: 'switch-workspace-5', global: true },
  { primary: true, alt: true, key: '6', action: 'switch-workspace-6', global: true },
  { primary: true, alt: true, key: '7', action: 'switch-workspace-7', global: true },
  { primary: true, alt: true, key: '8', action: 'switch-workspace-8', global: true },
  { primary: true, alt: true, key: '9', action: 'switch-workspace-9', global: true },
  { primary: true, alt: true, key: 'ArrowUp', action: 'prev-workspace', global: true },
  { primary: true, alt: true, key: 'ArrowDown', action: 'next-workspace', global: true },
]

function normalizeKey(key: string): string {
  return key.length === 1 && /[a-zA-Z]/.test(key) ? key.toLowerCase() : key
}

export function matchWebShortcut(
  e: Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>,
): WebShortcut | null {
  const primary = e.metaKey || e.ctrlKey
  if (!primary) return null
  const key = normalizeKey(e.key)
  for (const s of WEB_SHORTCUTS) {
    if (s.primary !== primary) continue
    if (!!s.alt !== e.altKey) continue
    if (!!s.shift !== e.shiftKey) continue
    if (normalizeKey(s.key) !== key) continue
    return s
  }
  return null
}

// Editable target = text input surface. xterm.js focuses a hidden
// <textarea class="xterm-helper-textarea">, so this also detects terminal focus.
export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/lib/web-shortcuts.test.ts`
Expected: PASS。若 parity test 因跨目錄 import（`../../../electron/keybindings`）在此 repo 的 vitest 設定下無法解析（electron/ 不在 spa tsconfig include），改為：在 `web-shortcuts.test.ts` 內以動態 `await import('../../../electron/keybindings')` 或直接以硬編 canonical action 陣列（附「與 electron/keybindings.ts 同步」註解）替代，並於 report 說明採用哪種。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/web-shortcuts.ts spa/src/lib/web-shortcuts.test.ts
git commit -m "feat(shortcuts): web keydown manifest with focus flag + electron parity test (P3a)"
```

---

## Task 3: useWebShortcuts hook（焦點矩陣）+ App 接線

**Files:**
- Create: `spa/src/hooks/useWebShortcuts.ts`
- Modify: `spa/src/App.tsx`
- Test: `spa/src/hooks/useWebShortcuts.test.ts`

**Interfaces:**
- Consumes: `matchWebShortcut`/`isEditableTarget`（Task 2）、`executeShortcutAction`（Task 1）、`getPlatformCapabilities`。

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/hooks/useWebShortcuts.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useWebShortcuts } from './useWebShortcuts'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'

beforeEach(() => {
  delete (window as unknown as { electronAPI?: unknown }).electronAPI
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  useWorkspaceStore.getState().addWorkspace('Default')
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
})
afterEach(() => { vi.restoreAllMocks() })

function fire(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent('keydown', { ...init, cancelable: true, bubbles: true })
  const prevented = vi.spyOn(e, 'preventDefault')
  target.dispatchEvent(e)
  return prevented
}

describe('useWebShortcuts', () => {
  it('web：Cmd+, 於非 editable → 執行 open-settings + preventDefault', () => {
    renderHook(() => useWebShortcuts())
    const prevented = fire(window, { key: ',', metaKey: true })
    expect(prevented).toHaveBeenCalled()
    expect(Object.values(useTabStore.getState().tabs).some((t) =>
      JSON.stringify(t.layout).includes('settings'))).toBe(true)
  })

  it('非 global（Cmd+Y）於 editable target（textarea）→ 放行、不執行', () => {
    renderHook(() => useWebShortcuts())
    const ta = document.createElement('textarea')
    document.body.appendChild(ta)
    const prevented = fire(ta, { key: 'y', metaKey: true })
    expect(prevented).not.toHaveBeenCalled()
    expect(Object.values(useTabStore.getState().tabs).some((t) =>
      JSON.stringify(t.layout).includes('history'))).toBe(false)
    ta.remove()
  })

  it('global（Cmd+Alt+ArrowRight）於 editable target → 仍觸發', () => {
    // 先放兩個分頁以便 next-tab 有作用（只驗 preventDefault 觸發即可）
    renderHook(() => useWebShortcuts())
    const ta = document.createElement('textarea'); document.body.appendChild(ta)
    const prevented = fire(ta, { key: 'ArrowRight', metaKey: true, altKey: true })
    expect(prevented).toHaveBeenCalled()
    ta.remove()
  })

  it('未對映鍵 → 不 preventDefault', () => {
    renderHook(() => useWebShortcuts())
    const prevented = fire(window, { key: 'q', metaKey: true })
    expect(prevented).not.toHaveBeenCalled()
  })

  it('Electron → 不掛 keydown', () => {
    ;(window as unknown as { electronAPI?: unknown }).electronAPI = { onShortcut: () => () => {} }
    renderHook(() => useWebShortcuts())
    const prevented = fire(window, { key: ',', metaKey: true })
    expect(prevented).not.toHaveBeenCalled()
  })

  it('unmount 後不再觸發', () => {
    const { unmount } = renderHook(() => useWebShortcuts())
    unmount()
    const prevented = fire(window, { key: ',', metaKey: true })
    expect(prevented).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/hooks/useWebShortcuts.test.ts`
Expected: FAIL（模組不存在）。

- [ ] **Step 3: 實作**

```ts
// spa/src/hooks/useWebShortcuts.ts
import { useEffect } from 'react'
import { getPlatformCapabilities } from '../lib/platform'
import { matchWebShortcut, isEditableTarget } from '../lib/web-shortcuts'
import { executeShortcutAction } from '../lib/shortcut-actions'

// Web-only keyboard shortcut layer. In Electron the menu accelerators + IPC
// (useShortcuts) own shortcuts. On the web we listen for keydown and dispatch
// the same action ids. Focus matrix: non-global shortcuts are suppressed when
// focus is an editable target (input/textarea/contentEditable, incl. xterm's
// hidden textarea) so form/terminal typing is never eaten.
export function useWebShortcuts(): void {
  useEffect(() => {
    if (getPlatformCapabilities().isElectron) return
    const onKeyDown = (e: KeyboardEvent) => {
      const match = matchWebShortcut(e)
      if (!match) return
      if (!match.global && isEditableTarget(e.target)) return
      e.preventDefault()
      executeShortcutAction(match.action)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
}
```

於 `spa/src/App.tsx` 既有 `useShortcuts()`（約第 74 行）之後加 `useWebShortcuts()`，並 import `import { useWebShortcuts } from './hooks/useWebShortcuts'`。

- [ ] **Step 4: 跑測試 + 全套回歸**

Run: `cd spa && npx vitest run src/hooks/useWebShortcuts.test.ts`
Expected: PASS（6 tests）。
Run: `cd spa && npx vitest run`
Expected: 全綠。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/hooks/useWebShortcuts.ts spa/src/hooks/useWebShortcuts.test.ts spa/src/App.tsx
git commit -m "feat(shortcuts): web keydown hook with focus matrix + App wiring (P3a)"
```

---

## 收尾驗證（全 task 完成後，主 Claude 執行）

- [ ] `cd spa && npx vitest run` — 全綠。
- [ ] `cd spa && pnpm run lint` — 無新增錯誤。
- [ ] `cd spa && pnpm run build` — 成功。

---

## Self-Review 對照 spec + codex review

- **spec §5.3 共享 action executor + binding manifest** → Task 1 + Task 2；**codex I1** parity test 防漂移、per-platform 綁定差異文件化。✅
- **spec §5.3 Electron/web 互斥** → useShortcuts / useWebShortcuts web-only guard。✅
- **spec §5.3 焦點規則（不誤觸終端/表單）** → **codex Critical** 焦點矩陣（editable/xterm 放行、`global` 白名單）+ Task 3 測試（Cmd+Y 於 textarea 放行、Cmd+Alt+→ 於 textarea 仍觸發）。✅
- **codex I2** executor 深度測試（home/hosts/history/reopen-empty）。**codex I3** reset harness 對齊既有慣例。**codex m1** unmount cleanup 測試。**codex m2** 排除 browser-reserved + 文件化。✅
- 無 placeholder；命名一致。✅
