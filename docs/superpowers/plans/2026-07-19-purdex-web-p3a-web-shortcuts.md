# Purdex Web P3a — Web 鍵盤快捷鍵層 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓 app-level 快捷鍵在純瀏覽器（無 Electron 選單 accelerator）也能運作——抽出共享 action executor，兩邊（Electron IPC / web keydown）都只餵 action id；web 端加 keydown 分發與焦點規則。

**Architecture:** 把 `useShortcuts` 內的 action 分派邏輯抽成純函式 `executeShortcutAction(action)`（單一事實來源）；`useShortcuts` 的 Electron `onShortcut` 效果改為呼叫它（行為不變）。新增 web binding manifest（`KeyboardEvent → action`，僅 web 相關 app 動作）+ `useWebShortcuts()` hook（web-only、window keydown → match → `executeShortcutAction` + preventDefault）。

**Tech Stack:** React 19 / TypeScript / Vitest。

## Global Constraints

- **共享 executor**：Electron 與 web **只餵 action id** 給同一個 `executeShortcutAction`；不得各自複製分派邏輯。
- **web-only**：`useWebShortcuts` 僅在 `!isElectron` 掛載 keydown；Electron 續走既有 IPC，二者互斥不重複觸發。
- **瀏覽器保留鍵限制（已知，不修）**：`Cmd/Ctrl+T`、`+W`、`+N`、部分 `Cmd/Ctrl+數字` 由瀏覽器保留、不會觸發頁面 keydown → web 無法攔截。manifest 仍可列入（保留鍵永不到達 handler，可攔者照攔）；文件化此限制。Electron-only 動作（`new-window`、browser pane 的 `go-back`/`go-forward`/`reload`/`focus-url`/`print`）**不列入** web manifest。
- **焦點規則**：所有 web 快捷鍵皆為 primary(Cmd/Ctrl)-modified 組合（無單鍵 app 快捷鍵），故文字輸入/終端輸入不受影響；handler 僅對 manifest 命中者 `preventDefault`+執行，其餘放行。此即焦點規則（不需複雜 input 偵測）。
- primary modifier 判定：`e.metaKey || e.ctrlKey`（跨平台接受任一）。
- 測試：`cd spa && npx vitest run`；Lint / Build 同 SPA 慣例。
- 每個 task 獨立 commit。

---

## File Structure

- **Create** `spa/src/lib/shortcut-actions.ts` — `executeShortcutAction(action: string): void`（自 `useShortcuts` 抽出的完整分派邏輯）。
- **Modify** `spa/src/hooks/useShortcuts.ts` — Electron `onShortcut` 效果改呼叫 `executeShortcutAction`；移除內嵌分派邏輯。
- **Create** `spa/src/lib/shortcut-actions.test.ts` — executor 行為測試。
- **Create** `spa/src/lib/web-shortcuts.ts` — `WEB_SHORTCUTS` manifest + `matchWebShortcut(e: KeyboardEvent): string | null`。
- **Create** `spa/src/lib/web-shortcuts.test.ts` — manifest 對映測試。
- **Create** `spa/src/hooks/useWebShortcuts.ts` — web-only keydown 分發 hook。
- **Modify** `spa/src/App.tsx` — 呼叫 `useWebShortcuts()`（在既有 `useShortcuts()` 旁）。

---

## Task 1: 抽出共享 executeShortcutAction

**Files:**
- Create: `spa/src/lib/shortcut-actions.ts`
- Modify: `spa/src/hooks/useShortcuts.ts`
- Test: `spa/src/lib/shortcut-actions.test.ts`

**Interfaces:**
- Produces: `executeShortcutAction(action: string): void` — 依 action id 執行（switch-tab-* / prev-next-tab / close-tab / new-tab / open-settings|hosts|history / reopen-closed-tab / switch-workspace-* / prev-next-workspace / tab-level registry dispatch）。

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/shortcut-actions.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { executeShortcutAction } from './shortcut-actions'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'

describe('executeShortcutAction', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  })

  it('new-tab → 新增一個 new-tab 分頁並設為 active', () => {
    executeShortcutAction('new-tab')
    const s = useTabStore.getState()
    expect(s.tabOrder.length).toBe(1)
    const id = s.tabOrder[0]
    expect(s.tabs[id].layout).toBeTruthy()
    expect(s.activeTabId).toBe(id)
  })

  it('open-settings → 開啟 settings singleton 分頁', () => {
    executeShortcutAction('open-settings')
    const s = useTabStore.getState()
    const hasSettings = Object.values(s.tabs).some((t) =>
      JSON.stringify(t.layout).includes('settings'),
    )
    expect(hasSettings).toBe(true)
  })

  it('未知 action → 不 throw', () => {
    expect(() => executeShortcutAction('nonexistent-xyz')).not.toThrow()
  })
})
```

> 註：`useTabStore` / `useWorkspaceStore` 的 `setState` 欄位以現況為準（對齊既有 store 測試的 reset pattern，必要時補齊其他 mutable 欄位以免 cross-test leak，見 [[feedback_zustand_harness_setstate]]）。

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/shortcut-actions.test.ts`
Expected: FAIL（模組不存在）。

- [ ] **Step 3: 抽出實作**

建 `spa/src/lib/shortcut-actions.ts`：把 `useShortcuts.ts` 內 `window.electronAPI.onShortcut(({ action }) => { ... })` callback 的**整個 body**（自 `const tabState = useTabStore.getState()` 起，到 `if (import.meta.env.DEV) { console.warn(...) }` 止）搬進：

```ts
// spa/src/lib/shortcut-actions.ts
// Shared shortcut action executor. Both the Electron menu-accelerator IPC path
// (useShortcuts) and the web keydown path (useWebShortcuts) feed action ids
// here — single source of dispatch, no duplicated logic.
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
  // ... （原封搬入 useShortcuts callback body：activateTab / visibleIds / 各 action 分支 / tab-level registry / DEV warn）
}
```

（import 路徑因移到 `lib/` 由 `../stores`/`../types`/`./` 起算——注意 `tab-lifecycle`/`tab-shortcut-registry`/`pane-tree` 原本在 `useShortcuts.ts` 是 `../lib/...`，搬到 `lib/` 後改為 `./...`；`stores`/`types`/`features` 由 `../` 起算。）

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

- [ ] **Step 4: 跑測試確認通過 + 既有測試回歸**

Run: `cd spa && npx vitest run src/lib/shortcut-actions.test.ts src/hooks/useShortcuts.test.ts`
Expected: PASS（既有 useShortcuts 測試若透過 mock `onShortcut` 觸發 action，仍應綠——分派邏輯等價）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/shortcut-actions.ts spa/src/lib/shortcut-actions.test.ts spa/src/hooks/useShortcuts.ts
git commit -m "refactor(shortcuts): extract shared executeShortcutAction (P3a)"
```

---

## Task 2: web binding manifest + matchWebShortcut

**Files:**
- Create: `spa/src/lib/web-shortcuts.ts`
- Test: `spa/src/lib/web-shortcuts.test.ts`

**Interfaces:**
- Produces:
  - `interface WebShortcut { primary: boolean; alt?: boolean; shift?: boolean; key: string; action: string }`
  - `WEB_SHORTCUTS: readonly WebShortcut[]`
  - `matchWebShortcut(e: Pick<KeyboardEvent,'key'|'metaKey'|'ctrlKey'|'altKey'|'shiftKey'>): string | null`

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/lib/web-shortcuts.test.ts
import { describe, it, expect } from 'vitest'
import { matchWebShortcut } from './web-shortcuts'

function ev(over: Partial<KeyboardEvent>): Pick<KeyboardEvent,'key'|'metaKey'|'ctrlKey'|'altKey'|'shiftKey'> {
  return { key: '', metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...over }
}

describe('matchWebShortcut', () => {
  it('Cmd+, → open-settings', () => {
    expect(matchWebShortcut(ev({ key: ',', metaKey: true }))).toBe('open-settings')
  })
  it('Ctrl+, → open-settings（跨平台 primary）', () => {
    expect(matchWebShortcut(ev({ key: ',', ctrlKey: true }))).toBe('open-settings')
  })
  it('Cmd+Alt+ArrowRight → next-tab', () => {
    expect(matchWebShortcut(ev({ key: 'ArrowRight', metaKey: true, altKey: true }))).toBe('next-tab')
  })
  it('Cmd+Alt+ArrowDown → next-workspace', () => {
    expect(matchWebShortcut(ev({ key: 'ArrowDown', metaKey: true, altKey: true }))).toBe('next-workspace')
  })
  it('Cmd+Alt+1 → switch-workspace-1', () => {
    expect(matchWebShortcut(ev({ key: '1', metaKey: true, altKey: true }))).toBe('switch-workspace-1')
  })
  it('無 primary modifier → null', () => {
    expect(matchWebShortcut(ev({ key: ',' }))).toBeNull()
  })
  it('大小寫不敏感（字母）：Cmd+Shift+H → open-hosts', () => {
    expect(matchWebShortcut(ev({ key: 'H', metaKey: true, shiftKey: true }))).toBe('open-hosts')
  })
  it('未對映鍵 → null', () => {
    expect(matchWebShortcut(ev({ key: 'q', metaKey: true }))).toBeNull()
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
// keybindings (electron/keybindings.ts) that is meaningful in a plain browser.
// Excludes Electron-only actions (new-window, browser-pane nav). Some combos
// (Cmd/Ctrl+T/W/N, some Cmd/Ctrl+digit) are browser-reserved and never reach a
// page keydown — listing them is harmless (they simply never fire here).
export interface WebShortcut {
  primary: boolean // Cmd (mac) or Ctrl — matched as metaKey||ctrlKey
  alt?: boolean
  shift?: boolean
  key: string // normalized: single letters lowercased; named keys as-is (ArrowLeft, ',', digits)
  action: string
}

export const WEB_SHORTCUTS: readonly WebShortcut[] = [
  // Tab index (browser may reserve Cmd+digit — harmless if never delivered)
  { primary: true, key: '1', action: 'switch-tab-1' },
  { primary: true, key: '2', action: 'switch-tab-2' },
  { primary: true, key: '3', action: 'switch-tab-3' },
  { primary: true, key: '4', action: 'switch-tab-4' },
  { primary: true, key: '5', action: 'switch-tab-5' },
  { primary: true, key: '6', action: 'switch-tab-6' },
  { primary: true, key: '7', action: 'switch-tab-7' },
  { primary: true, key: '8', action: 'switch-tab-8' },
  { primary: true, key: '9', action: 'switch-tab-last' },
  // Tab nav
  { primary: true, alt: true, key: 'ArrowLeft', action: 'prev-tab' },
  { primary: true, alt: true, key: 'ArrowRight', action: 'next-tab' },
  // Tab actions (Cmd+T/W browser-reserved on web; listed for parity, may not fire)
  { primary: true, key: 't', action: 'new-tab' },
  { primary: true, key: 'w', action: 'close-tab' },
  { primary: true, shift: true, key: 't', action: 'reopen-closed-tab' },
  // App / views
  { primary: true, key: ',', action: 'open-settings' },
  { primary: true, shift: true, key: 'h', action: 'open-hosts' },
  { primary: true, key: 'y', action: 'open-history' },
  // Workspace nav
  { primary: true, alt: true, key: '0', action: 'switch-workspace-home' },
  { primary: true, alt: true, key: '1', action: 'switch-workspace-1' },
  { primary: true, alt: true, key: '2', action: 'switch-workspace-2' },
  { primary: true, alt: true, key: '3', action: 'switch-workspace-3' },
  { primary: true, alt: true, key: '4', action: 'switch-workspace-4' },
  { primary: true, alt: true, key: '5', action: 'switch-workspace-5' },
  { primary: true, alt: true, key: '6', action: 'switch-workspace-6' },
  { primary: true, alt: true, key: '7', action: 'switch-workspace-7' },
  { primary: true, alt: true, key: '8', action: 'switch-workspace-8' },
  { primary: true, alt: true, key: '9', action: 'switch-workspace-9' },
  { primary: true, alt: true, key: 'ArrowUp', action: 'prev-workspace' },
  { primary: true, alt: true, key: 'ArrowDown', action: 'next-workspace' },
]

function normalizeKey(key: string): string {
  // Single printable letters → lowercase; everything else (ArrowLeft, ',', digits) as-is.
  return key.length === 1 && /[a-zA-Z]/.test(key) ? key.toLowerCase() : key
}

export function matchWebShortcut(
  e: Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>,
): string | null {
  const primary = e.metaKey || e.ctrlKey
  if (!primary) return null
  const key = normalizeKey(e.key)
  for (const s of WEB_SHORTCUTS) {
    if (s.primary !== primary) continue
    if (!!s.alt !== e.altKey) continue
    if (!!s.shift !== e.shiftKey) continue
    if (normalizeKey(s.key) !== key) continue
    return s.action
  }
  return null
}
```

- [ ] **Step 4: 跑測試確認通過**

Run: `cd spa && npx vitest run src/lib/web-shortcuts.test.ts`
Expected: PASS（8 tests）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/web-shortcuts.ts spa/src/lib/web-shortcuts.test.ts
git commit -m "feat(shortcuts): web keydown → action binding manifest (P3a)"
```

---

## Task 3: useWebShortcuts hook + App 接線

**Files:**
- Create: `spa/src/hooks/useWebShortcuts.ts`
- Modify: `spa/src/App.tsx`
- Test: `spa/src/hooks/useWebShortcuts.test.ts`

**Interfaces:**
- Consumes: `matchWebShortcut`（Task 2）、`executeShortcutAction`（Task 1）、`getPlatformCapabilities`。

- [ ] **Step 1: 寫失敗測試**

```ts
// spa/src/hooks/useWebShortcuts.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useWebShortcuts } from './useWebShortcuts'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'

describe('useWebShortcuts', () => {
  beforeEach(() => {
    delete (window as unknown as { electronAPI?: unknown }).electronAPI // web
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  })
  afterEach(() => { vi.restoreAllMocks() })

  it('web：Cmd+, keydown → 執行 open-settings 並 preventDefault', () => {
    renderHook(() => useWebShortcuts())
    const e = new KeyboardEvent('keydown', { key: ',', metaKey: true, cancelable: true })
    const prevented = vi.spyOn(e, 'preventDefault')
    window.dispatchEvent(e)
    expect(prevented).toHaveBeenCalled()
    const hasSettings = Object.values(useTabStore.getState().tabs).some((t) =>
      JSON.stringify(t.layout).includes('settings'))
    expect(hasSettings).toBe(true)
  })

  it('web：未對映鍵不 preventDefault', () => {
    renderHook(() => useWebShortcuts())
    const e = new KeyboardEvent('keydown', { key: 'q', metaKey: true, cancelable: true })
    const prevented = vi.spyOn(e, 'preventDefault')
    window.dispatchEvent(e)
    expect(prevented).not.toHaveBeenCalled()
  })

  it('Electron：不掛 keydown（不觸發）', () => {
    ;(window as unknown as { electronAPI?: unknown }).electronAPI = { onShortcut: () => () => {} }
    renderHook(() => useWebShortcuts())
    const e = new KeyboardEvent('keydown', { key: ',', metaKey: true, cancelable: true })
    window.dispatchEvent(e)
    expect(Object.keys(useTabStore.getState().tabs).length).toBe(0)
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
import { matchWebShortcut } from '../lib/web-shortcuts'
import { executeShortcutAction } from '../lib/shortcut-actions'

// Web-only keyboard shortcut layer. In Electron the menu accelerators + IPC
// (useShortcuts) own shortcuts; on the web there is no menu, so we listen for
// keydown and dispatch the same action ids. All web shortcuts are
// primary(Cmd/Ctrl)-modified, so text/terminal typing is unaffected.
export function useWebShortcuts(): void {
  useEffect(() => {
    if (getPlatformCapabilities().isElectron) return
    const onKeyDown = (e: KeyboardEvent) => {
      const action = matchWebShortcut(e)
      if (!action) return
      e.preventDefault()
      executeShortcutAction(action)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
}
```

於 `spa/src/App.tsx`，在既有 `useShortcuts()`（第 74 行附近）之後加：

```ts
  useWebShortcuts()
```

並 import：`import { useWebShortcuts } from './hooks/useWebShortcuts'`。

- [ ] **Step 4: 跑測試確認通過 + 全套回歸**

Run: `cd spa && npx vitest run src/hooks/useWebShortcuts.test.ts`
Expected: PASS（3 tests）。
Run: `cd spa && npx vitest run`
Expected: 全綠。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/hooks/useWebShortcuts.ts spa/src/hooks/useWebShortcuts.test.ts spa/src/App.tsx
git commit -m "feat(shortcuts): web-only keydown dispatch hook + App wiring (P3a)"
```

---

## 收尾驗證（全 task 完成後，主 Claude 執行）

- [ ] `cd spa && npx vitest run` — 全綠。
- [ ] `cd spa && pnpm run lint` — 無新增錯誤。
- [ ] `cd spa && pnpm run build` — 成功。

---

## Self-Review 對照 spec

- **spec §5.3 快捷鍵——共享 action executor + binding manifest** → Task 1（`executeShortcutAction`）+ Task 2（`WEB_SHORTCUTS`/`matchWebShortcut`）。✅
- **spec §5.3 Electron IPC 與 web keydown 只餵 action、互斥不重複** → Task 1 useShortcuts 呼叫 executor；Task 3 useWebShortcuts web-only guard。✅
- **spec §5.3 焦點規則** → Global Constraints：全 primary-modified 組合、無單鍵 app 快捷鍵，故文字/終端輸入不受影響；僅命中 manifest 才 preventDefault。✅
- **spec §4 Tier C：browser-pane/Electron-only 動作** → web manifest 排除 new-window / browser nav。✅
- 無 placeholder；`executeShortcutAction`/`matchWebShortcut`/`WEB_SHORTCUTS`/`useWebShortcuts` 命名一致。✅
