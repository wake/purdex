# App 快捷鍵系統 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 為 Electron shell 加入 Menu accelerator 快捷鍵，透過 keybinding registry pattern 管理，支援 tab 切換、Settings、History 開啟。

**Architecture:** Electron main process 建立 keybinding registry（action → accelerator 映射），產生 Menu template 並設定 `Menu.setApplicationMenu()`。Menu click 透過統一 IPC channel `shortcut:execute` 將 action 傳到 renderer process。SPA 側 `useShortcuts` hook 監聽 IPC 並 dispatch 到對應 store action。

**Tech Stack:** Electron Menu / IPC / contextBridge / React hook / Zustand stores / Vitest

---

## File Structure

| 檔案 | 動作 | 職責 |
|------|------|------|
| `electron/keybindings.ts` | 新增 | Keybinding registry：default bindings 定義 + Menu template 產生 |
| `electron/main.ts` | 修改 | import keybindings，建立 Menu，click handler 發送 IPC |
| `electron/preload.ts` | 修改 | 加入 `onShortcut(callback)` API |
| `spa/src/hooks/useShortcuts.ts` | 新增 | 統一 shortcut listener，dispatch 到 stores |
| `spa/src/hooks/useShortcuts.test.ts` | 新增 | useShortcuts 測試 |
| `spa/src/types/electron.d.ts` | 修改 | 擴充 `ElectronAPI` type 加入 `onShortcut` |
| `spa/src/App.tsx` | 修改 | 掛載 useShortcuts hook，移除硬編碼 Cmd+Shift+T |

---

### Task 1: Keybinding Registry（electron/keybindings.ts）

**Files:**
- Create: `electron/keybindings.ts`

- [ ] **Step 1: 建立 keybinding registry 檔案**

```typescript
// electron/keybindings.ts
import type { MenuItemConstructorOptions } from 'electron'

export interface KeybindingDef {
  action: string
  accelerator: string
  label: string
  menuCategory: 'App' | 'Tab' | 'View' | 'Edit'
}

const DEFAULT_KEYBINDINGS: KeybindingDef[] = [
  // Tab switching by index
  { action: 'switch-tab-1', accelerator: 'CommandOrControl+1', label: 'Tab 1', menuCategory: 'Tab' },
  { action: 'switch-tab-2', accelerator: 'CommandOrControl+2', label: 'Tab 2', menuCategory: 'Tab' },
  { action: 'switch-tab-3', accelerator: 'CommandOrControl+3', label: 'Tab 3', menuCategory: 'Tab' },
  { action: 'switch-tab-4', accelerator: 'CommandOrControl+4', label: 'Tab 4', menuCategory: 'Tab' },
  { action: 'switch-tab-5', accelerator: 'CommandOrControl+5', label: 'Tab 5', menuCategory: 'Tab' },
  { action: 'switch-tab-6', accelerator: 'CommandOrControl+6', label: 'Tab 6', menuCategory: 'Tab' },
  { action: 'switch-tab-7', accelerator: 'CommandOrControl+7', label: 'Tab 7', menuCategory: 'Tab' },
  { action: 'switch-tab-8', accelerator: 'CommandOrControl+8', label: 'Tab 8', menuCategory: 'Tab' },
  { action: 'switch-tab-last', accelerator: 'CommandOrControl+9', label: 'Last Tab', menuCategory: 'Tab' },
  // Tab navigation
  { action: 'prev-tab', accelerator: 'CommandOrControl+Alt+Left', label: 'Previous Tab', menuCategory: 'Tab' },
  { action: 'next-tab', accelerator: 'CommandOrControl+Alt+Right', label: 'Next Tab', menuCategory: 'Tab' },
  // Reopen
  { action: 'reopen-closed-tab', accelerator: 'CommandOrControl+Shift+T', label: 'Reopen Closed Tab', menuCategory: 'Tab' },
  // View
  { action: 'open-settings', accelerator: 'CommandOrControl+,', label: 'Settings', menuCategory: 'App' },
  { action: 'open-history', accelerator: 'CommandOrControl+Y', label: 'History', menuCategory: 'View' },
]

export function getDefaultKeybindings(): KeybindingDef[] {
  return DEFAULT_KEYBINDINGS
}

export function buildMenuTemplate(
  bindings: KeybindingDef[],
  send: (action: string) => void,
): MenuItemConstructorOptions[] {
  const byCategory = new Map<string, MenuItemConstructorOptions[]>()
  for (const b of bindings) {
    const items = byCategory.get(b.menuCategory) ?? []
    items.push({
      label: b.label,
      accelerator: b.accelerator,
      click: () => send(b.action),
    })
    byCategory.set(b.menuCategory, items)
  }

  const isMac = process.platform === 'darwin'

  const appMenu: MenuItemConstructorOptions = {
    label: 'tmux-box',
    submenu: [
      ...(isMac ? [{ role: 'about' as const }] : []),
      ...(byCategory.get('App') ?? []),
      { type: 'separator' as const },
      ...(isMac
        ? [
            { role: 'hide' as const },
            { role: 'hideOthers' as const },
            { role: 'unhide' as const },
            { type: 'separator' as const },
          ]
        : []),
      { role: 'quit' as const },
    ],
  }

  const tabMenu: MenuItemConstructorOptions = {
    label: 'Tab',
    submenu: [
      // Index switching (1-8)
      ...(byCategory.get('Tab') ?? []).filter((i) =>
        (i as { label: string }).label.match(/^Tab \d$/),
      ),
      // Last Tab
      ...(byCategory.get('Tab') ?? []).filter(
        (i) => (i as { label: string }).label === 'Last Tab',
      ),
      { type: 'separator' as const },
      // Navigation
      ...(byCategory.get('Tab') ?? []).filter((i) =>
        ['Previous Tab', 'Next Tab'].includes((i as { label: string }).label),
      ),
      { type: 'separator' as const },
      // Reopen
      ...(byCategory.get('Tab') ?? []).filter(
        (i) => (i as { label: string }).label === 'Reopen Closed Tab',
      ),
    ],
  }

  const viewMenu: MenuItemConstructorOptions = {
    label: 'View',
    submenu: [...(byCategory.get('View') ?? [])],
  }

  const editMenu: MenuItemConstructorOptions = {
    label: 'Edit',
    submenu: [
      { role: 'undo' as const },
      { role: 'redo' as const },
      { type: 'separator' as const },
      { role: 'cut' as const },
      { role: 'copy' as const },
      { role: 'paste' as const },
      { role: 'selectAll' as const },
    ],
  }

  return [appMenu, editMenu, tabMenu, viewMenu]
}
```

- [ ] **Step 2: Commit**

```bash
git add electron/keybindings.ts
git commit -m "feat: add keybinding registry with default bindings and menu template builder"
```

---

### Task 2: Electron Main — 建立 Menu 並發送 IPC

**Files:**
- Modify: `electron/main.ts`

- [ ] **Step 1: 在 main.ts 加入 Menu 建構**

在 `main.ts` 頂部加入 import：

```typescript
import { app, BrowserWindow, ipcMain, Menu } from 'electron'
import { getDefaultKeybindings, buildMenuTemplate } from './keybindings'
```

在 `app.whenReady().then(() => {` 區塊中，`windowManager.createWindow()` 之前加入：

```typescript
  // Keyboard shortcuts — build and apply menu
  const keybindings = getDefaultKeybindings()
  const menuTemplate = buildMenuTemplate(keybindings, (action) => {
    const focused = BrowserWindow.getFocusedWindow()
    if (focused && !focused.isDestroyed()) {
      focused.webContents.send('shortcut:execute', { action })
    }
  })
  Menu.setApplicationMenu(Menu.buildFromTemplate(menuTemplate))
```

- [ ] **Step 2: Commit**

```bash
git add electron/main.ts
git commit -m "feat: wire keybinding menu into Electron main process"
```

---

### Task 3: Preload — 暴露 onShortcut API

**Files:**
- Modify: `electron/preload.ts`

- [ ] **Step 1: 在 preload.ts 加入 onShortcut**

在 `contextBridge.exposeInMainWorld('electronAPI', {` 物件中，`signalReady` 之後加入：

```typescript
  // Keyboard Shortcuts
  onShortcut: (callback: (payload: { action: string }) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, payload: { action: string }) =>
      callback(payload)
    ipcRenderer.on('shortcut:execute', handler)
    return () => ipcRenderer.removeListener('shortcut:execute', handler)
  },
```

- [ ] **Step 2: Commit**

```bash
git add electron/preload.ts
git commit -m "feat: expose onShortcut IPC listener in preload"
```

---

### Task 4: 擴充 ElectronAPI TypeScript 型別

**Files:**
- Modify: `spa/src/types/electron.d.ts`

- [ ] **Step 1: 在 electron.d.ts 的 Window.electronAPI 中加入 onShortcut**

在 `signalReady: () => void` 之後加入：

```typescript
    // Keyboard Shortcuts
    onShortcut: (callback: (payload: { action: string }) => void) => () => void
```

- [ ] **Step 2: Commit**

```bash
git add spa/src/types/electron.d.ts
git commit -m "feat: add onShortcut type to ElectronAPI interface"
```

---

### Task 5: useShortcuts Hook — 測試

**Files:**
- Create: `spa/src/hooks/useShortcuts.test.ts`

- [ ] **Step 1: 撰寫 useShortcuts 測試**

```typescript
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { useShortcuts } from './useShortcuts'

// Helper: set up electronAPI mock
function mockElectronAPI() {
  let shortcutCallback: ((payload: { action: string }) => void) | null = null
  const cleanup = vi.fn()
  ;(window as unknown as Record<string, unknown>).electronAPI = {
    onShortcut: (cb: (payload: { action: string }) => void) => {
      shortcutCallback = cb
      return cleanup
    },
    signalReady: () => {},
  }
  return {
    fire: (action: string) => shortcutCallback?.({ action }),
    cleanup,
  }
}

function seedTabs(count: number) {
  const store = useTabStore.getState()
  const tabs = Array.from({ length: count }, (_, i) =>
    createTab({ kind: 'new-tab' }),
  )
  tabs.forEach((t) => store.addTab(t))
  store.setActiveTab(tabs[0].id)
  return tabs
}

describe('useShortcuts', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useWorkspaceStore.getState().workspaces.forEach((ws) =>
      useWorkspaceStore.getState().deleteWorkspace?.(ws.id),
    )
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
  })

  it('does nothing when electronAPI is not available', () => {
    const { unmount } = renderHook(() => useShortcuts())
    // Should not throw
    unmount()
  })

  it('cleans up listener on unmount', () => {
    const { cleanup } = mockElectronAPI()
    const { unmount } = renderHook(() => useShortcuts())
    unmount()
    expect(cleanup).toHaveBeenCalled()
  })

  describe('switch-tab-{n}', () => {
    it('switches to tab by index', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(5)
      renderHook(() => useShortcuts())

      fire('switch-tab-3')
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })

    it('ignores out-of-range index', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(2)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('switch-tab-5')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })
  })

  describe('switch-tab-last', () => {
    it('switches to the last tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(4)
      renderHook(() => useShortcuts())

      fire('switch-tab-last')
      expect(useTabStore.getState().activeTabId).toBe(tabs[3].id)
    })
  })

  describe('prev-tab / next-tab', () => {
    it('cycles to previous tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[1].id)
      renderHook(() => useShortcuts())

      fire('prev-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })

    it('wraps around from first to last', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('prev-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })

    it('cycles to next tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('next-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[1].id)
    })

    it('wraps around from last to first', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[2].id)
      renderHook(() => useShortcuts())

      fire('next-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })
  })

  describe('open-settings', () => {
    it('opens a settings singleton tab', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      renderHook(() => useShortcuts())

      fire('open-settings')
      const state = useTabStore.getState()
      const settingsTab = Object.values(state.tabs).find((t) => {
        const pane = t.layout
        return pane.type === 'leaf' && pane.content.kind === 'settings'
      })
      expect(settingsTab).toBeDefined()
      expect(state.activeTabId).toBe(settingsTab!.id)
    })
  })

  describe('open-history', () => {
    it('opens a history singleton tab', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      renderHook(() => useShortcuts())

      fire('open-history')
      const state = useTabStore.getState()
      const historyTab = Object.values(state.tabs).find((t) => {
        const pane = t.layout
        return pane.type === 'leaf' && pane.content.kind === 'history'
      })
      expect(historyTab).toBeDefined()
      expect(state.activeTabId).toBe(historyTab!.id)
    })
  })

  describe('reopen-closed-tab', () => {
    it('reopens the last closed tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(2)
      // Close the second tab
      const closedTab = tabs[1]
      useHistoryStore.getState().recordClose(closedTab)
      useTabStore.getState().closeTab(closedTab.id)
      renderHook(() => useShortcuts())

      fire('reopen-closed-tab')
      expect(useTabStore.getState().tabs[closedTab.id]).toBeDefined()
      expect(useTabStore.getState().activeTabId).toBe(closedTab.id)
    })
  })
})
```

- [ ] **Step 2: 執行測試，確認全部 FAIL**

```bash
cd spa && npx vitest run src/hooks/useShortcuts.test.ts
```

預期：FAIL — `useShortcuts` 模組不存在。

- [ ] **Step 3: Commit**

```bash
git add spa/src/hooks/useShortcuts.test.ts
git commit -m "test: add useShortcuts hook tests (red)"
```

---

### Task 6: useShortcuts Hook — 實作

**Files:**
- Create: `spa/src/hooks/useShortcuts.ts`

- [ ] **Step 1: 實作 useShortcuts hook**

```typescript
import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'

export function useShortcuts(): void {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return

    const cleanup = window.electronAPI.onShortcut(({ action }) => {
      const tabState = useTabStore.getState()
      const { tabOrder } = tabState

      if (action.startsWith('switch-tab-')) {
        if (action === 'switch-tab-last') {
          const lastId = tabOrder[tabOrder.length - 1]
          if (lastId) tabState.setActiveTab(lastId)
        } else {
          const index = parseInt(action.replace('switch-tab-', ''), 10) - 1
          const targetId = tabOrder[index]
          if (targetId) tabState.setActiveTab(targetId)
        }
        return
      }

      if (action === 'prev-tab' || action === 'next-tab') {
        if (tabOrder.length === 0) return
        const currentIdx = tabState.activeTabId
          ? tabOrder.indexOf(tabState.activeTabId)
          : -1
        const delta = action === 'next-tab' ? 1 : -1
        const nextIdx = (currentIdx + delta + tabOrder.length) % tabOrder.length
        tabState.setActiveTab(tabOrder[nextIdx])
        return
      }

      if (action === 'open-settings') {
        const tabId = tabState.openSingletonTab({ kind: 'settings', scope: 'global' })
        const wsId = useWorkspaceStore.getState().activeWorkspaceId
        if (wsId) {
          useWorkspaceStore.getState().addTabToWorkspace(wsId, tabId)
          useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tabId)
        }
        return
      }

      if (action === 'open-history') {
        const tabId = tabState.openSingletonTab({ kind: 'history' })
        const wsId = useWorkspaceStore.getState().activeWorkspaceId
        if (wsId) {
          useWorkspaceStore.getState().addTabToWorkspace(wsId, tabId)
          useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tabId)
        }
        return
      }

      if (action === 'reopen-closed-tab') {
        const tab = useHistoryStore.getState().reopenLast()
        if (tab) {
          tabState.addTab(tab)
          tabState.setActiveTab(tab.id)
        }
        return
      }
    })

    return cleanup
  }, [])
}
```

- [ ] **Step 2: 執行測試，確認全部 PASS**

```bash
cd spa && npx vitest run src/hooks/useShortcuts.test.ts
```

預期：全部 PASS。

- [ ] **Step 3: Commit**

```bash
git add spa/src/hooks/useShortcuts.ts
git commit -m "feat: implement useShortcuts hook for Electron menu shortcut dispatch"
```

---

### Task 7: 整合 — App.tsx 掛載 hook + 移除硬編碼

**Files:**
- Modify: `spa/src/App.tsx`

- [ ] **Step 1: 在 App.tsx 加入 useShortcuts，移除舊的 Cmd+Shift+T**

在 import 區塊加入：

```typescript
import { useShortcuts } from './hooks/useShortcuts'
```

在 `useRouteSync()` 之後加入：

```typescript
  useShortcuts()
```

移除整段硬編碼 `Cmd+Shift+T` 的 useEffect（`App.tsx` 第 76-90 行）：

```typescript
  // --- Keybinding: ⌘+Shift+T / Ctrl+Shift+T — reopen last closed tab ---
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'T') {
        e.preventDefault()
        const tab = useHistoryStore.getState().reopenLast()
        if (tab) {
          useTabStore.getState().addTab(tab)
          useTabStore.getState().setActiveTab(tab.id)
        }
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [])
```

同時移除 `useHistoryStore` import（因為 App.tsx 不再直接使用它，確認其他地方是否還有引用）。

注意：`useHistoryStore` 在 App.tsx 中沒有被其他地方引用，可以安全移除 import。

- [ ] **Step 2: 執行全部測試**

```bash
cd spa && npx vitest run
```

預期：全部 PASS。如果有既有的 App.tsx 測試引用了 Cmd+Shift+T 行為，需同步更新。

- [ ] **Step 3: Commit**

```bash
git add spa/src/App.tsx
git commit -m "feat: wire useShortcuts into App, remove hardcoded Cmd+Shift+T"
```

---

### Task 8: 手動驗證（Electron）

- [ ] **Step 1: 確認 SPA build 成功**

```bash
cd spa && pnpm run build
```

- [ ] **Step 2: 確認 lint 通過**

```bash
cd spa && pnpm run lint
```

- [ ] **Step 3: 在 Electron 中手動測試**

在 Electron .app 中驗證：
- [ ] `Cmd+1` ~ `Cmd+8` 切換到對應 tab
- [ ] `Cmd+9` 切換到最後一個 tab
- [ ] `Cmd+Option+←/→` 前後循環切換 tab
- [ ] `Cmd+,` 開啟 Settings
- [ ] `Cmd+Y` 開啟 History
- [ ] `Cmd+Shift+T` 重開已關閉 tab
- [ ] `Cmd+C/V/X/A` 在 input 中正常運作（Edit menu）
- [ ] Menu bar 顯示正確的快捷鍵提示
