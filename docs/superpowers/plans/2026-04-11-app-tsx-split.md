# App.tsx 拆分 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 將 `spa/src/App.tsx` 從 409 行降至 ~180 行，消除混合職責，使其回歸純組裝層角色。

**Architecture:** 提取 3 個獨立模組（GlobalUndoToast 元件、useElectronIpc hook、useWorkspaceWindowActions hook），在 useTabWorkspaceActions 新增 `openSingletonAndSelect` 統一 4 處重複的 singleton tab 開啟模式，最後將 App.tsx 殘留的 inline lambda 全部提為具名 callback。

**Tech Stack:** React 19 / Zustand 5 / Vitest / @testing-library/react

**Issue:** #281 — Closes #202, #219, #225, #231, #237, #243, #261

**Baseline:** 131 test files, 1209 tests 全通過

---

### Task 1: 提取 GlobalUndoToast 元件

**Files:**
- Create: `spa/src/components/GlobalUndoToast.tsx`
- Modify: `spa/src/App.tsx:1-70` (移除元件定義，加 import)

- [ ] **Step 1: 建立 `spa/src/components/GlobalUndoToast.tsx`**

```tsx
import { useEffect, useRef } from 'react'
import { useUndoToast } from '../stores/useUndoToast'
import { useI18nStore } from '../stores/useI18nStore'

export function GlobalUndoToast() {
  const toast = useUndoToast((s) => s.toast)
  const dismiss = useUndoToast((s) => s.dismiss)
  const t = useI18nStore((s) => s.t)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (!toast) return
    timerRef.current = setTimeout(() => dismiss(), 5000)
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [toast, dismiss])

  if (!toast) return null

  return (
    <div className="fixed bottom-4 left-1/2 -translate-x-1/2 bg-zinc-800 border border-zinc-700 rounded-lg px-4 py-3 flex items-center gap-3 shadow-lg z-50">
      <span className="text-sm text-zinc-300">
        {toast.message}
      </span>
      <button
        className="text-sm text-blue-400 hover:text-blue-300 font-medium cursor-pointer"
        onClick={() => {
          toast.restore()
          dismiss()
        }}
      >
        {t('hosts.undo')}
      </button>
    </div>
  )
}
```

- [ ] **Step 2: 修改 App.tsx — 移除內嵌定義，改為 import**

移除 App.tsx L1–L70 的 `GlobalUndoToast` function 定義和其相關 imports（`useUndoToast`, `useI18nStore`, `useRef` 只用於 GlobalUndoToast 的話也一併清理）。

在 import 區加入：
```tsx
import { GlobalUndoToast } from './components/GlobalUndoToast'
```

注意 App.tsx 自身仍使用 `useRef` 嗎？否。App() 函式內無 useRef 呼叫，可從 React import 中移除。`useI18nStore` 也只被 GlobalUndoToast 使用，可移除。`useUndoToast` 也只被 GlobalUndoToast 使用，可移除。

最終 App.tsx 的 React import 變為：
```tsx
import { useCallback, useEffect, useMemo, useState } from 'react'
```

- [ ] **Step 3: 執行測試確認無回歸**

Run: `cd spa && npx vitest run`
Expected: 131 test files, 1209 tests 全通過

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/GlobalUndoToast.tsx spa/src/App.tsx
git commit -m "refactor: extract GlobalUndoToast from App.tsx"
```

---

### Task 2: 新增 openSingletonAndSelect 到 useTabWorkspaceActions

**Files:**
- Modify: `spa/src/features/workspace/hooks.ts:195-213` (新增方法 + export)
- Modify: `spa/src/features/workspace/hooks.test.ts` (新增測試)
- Modify: `spa/src/features/workspace/index.ts` (無需改，hook 整體 re-export)

- [ ] **Step 1: 寫失敗測試**

在 `spa/src/features/workspace/hooks.test.ts` 新增：

```ts
describe('openSingletonAndSelect', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('creates singleton tab, inserts into active workspace, and selects it', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WS1')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId: string
    act(() => {
      tabId = result.current.openSingletonAndSelect({ kind: 'hosts' })
    })

    // Tab was created
    expect(useTabStore.getState().tabs[tabId!]).toBeDefined()
    // Tab is active
    expect(useTabStore.getState().activeTabId).toBe(tabId!)
    // Tab is in workspace
    const updatedWs = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)
    expect(updatedWs!.tabs).toContain(tabId!)
    // Workspace active tab is set
    expect(updatedWs!.activeTabId).toBe(tabId!)
  })

  it('reuses existing singleton tab instead of creating duplicate', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WS1')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId1: string
    let tabId2: string
    act(() => {
      tabId1 = result.current.openSingletonAndSelect({ kind: 'hosts' })
    })
    act(() => {
      tabId2 = result.current.openSingletonAndSelect({ kind: 'hosts' })
    })

    expect(tabId1!).toBe(tabId2!)
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(1)
  })

  it('works without active workspace (standalone tabs)', () => {
    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId: string
    act(() => {
      tabId = result.current.openSingletonAndSelect({ kind: 'settings', scope: 'global' })
    })

    expect(useTabStore.getState().tabs[tabId!]).toBeDefined()
    expect(useTabStore.getState().activeTabId).toBe(tabId!)
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/features/workspace/hooks.test.ts`
Expected: FAIL — `openSingletonAndSelect` 不存在

- [ ] **Step 3: 實作 openSingletonAndSelect**

在 `spa/src/features/workspace/hooks.ts` 檔案頂部加入 import：
```ts
import type { PaneContent } from '../../types/tab'
```

在 `useTabWorkspaceActions` 函式內，`handleClearRenameError` 之後加入：
```ts
  const openSingletonAndSelect = useCallback((content: PaneContent) => {
    const tabId = useTabStore.getState().openSingletonTab(content)
    useWorkspaceStore.getState().insertTab(tabId)
    handleSelectTab(tabId)
    return tabId
  }, [handleSelectTab])
```

在 return 物件中加入 `openSingletonAndSelect`：
```ts
  return {
    contextMenu,
    setContextMenu,
    contextMenuHasRightUnlocked,
    handleSelectWorkspace,
    handleSelectTab,
    handleCloseTab,
    handleAddTab,
    handleReorderTabs,
    handleContextMenu,
    handleMiddleClick,
    handleContextAction,
    renameTarget,
    renameError,
    handleRenameConfirm,
    handleRenameCancel,
    handleClearRenameError,
    openSingletonAndSelect,
  }
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/features/workspace/hooks.test.ts`
Expected: PASS

- [ ] **Step 5: 執行全部測試確認無回歸**

Run: `cd spa && npx vitest run`
Expected: 131 test files, all passed

- [ ] **Step 6: Commit**

```bash
git add spa/src/features/workspace/hooks.ts spa/src/features/workspace/hooks.test.ts
git commit -m "feat: add openSingletonAndSelect to useTabWorkspaceActions"
```

---

### Task 3: 提取 useElectronIpc hook

**Files:**
- Create: `spa/src/hooks/useElectronIpc.ts`
- Modify: `spa/src/App.tsx:98-163` (移除 4 個 useEffect)

此 hook 收納 4 個 Electron IPC side-effect：`signalReady`、`onTabReceived`、`onWorkspaceReceived`、`onBrowserViewOpenInTab`。同時修正 #231（`onWorkspaceReceived` 的 catch 範圍過大）。

- [ ] **Step 1: 建立 `spa/src/hooks/useElectronIpc.ts`**

```ts
import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { openBrowserTab } from '../lib/open-browser-tab'
import type { Tab } from '../types/tab'

/**
 * Registers all Electron IPC listeners as React effects.
 * No-op when window.electronAPI is absent (SPA-only mode).
 */
export function useElectronIpc() {
  // Signal SPA ready
  useEffect(() => {
    window.electronAPI?.signalReady()
  }, [])

  // Receive single tab from tear-off/merge
  useEffect(() => {
    if (!window.electronAPI) return
    return window.electronAPI.onTabReceived((tabJson: string, replace: boolean) => {
      try {
        const tab = JSON.parse(tabJson)
        if (tab && tab.id && tab.layout) {
          if (replace) {
            useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
          }
          useTabStore.getState().addTab(tab)
          useTabStore.getState().setActiveTab(tab.id)
          useWorkspaceStore.getState().insertTab(tab.id)
        }
      } catch { /* ignore malformed tab JSON */ }
    })
  }, [])

  // Receive workspace from tear-off/merge
  // Fix #231: narrow catch to JSON.parse only
  useEffect(() => {
    if (!window.electronAPI?.onWorkspaceReceived) return
    return window.electronAPI.onWorkspaceReceived((payload: string, replace: boolean) => {
      let parsed: { workspace: { id: string; tabs: string[]; activeTabId?: string }; tabData: Tab[] }
      try {
        parsed = JSON.parse(payload)
      } catch {
        return // malformed JSON — discard
      }

      const { workspace, tabData } = parsed
      if (!workspace?.id || !Array.isArray(tabData)) return

      const tabMap = new Map(tabData.map((t: Tab) => [t.id, t]))
      workspace.tabs = workspace.tabs.filter((id: string) => tabMap.has(id))

      if (replace) {
        useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
        useWorkspaceStore.getState().reset()
      }

      for (const tab of tabData) {
        if (tab?.id && tab?.layout) useTabStore.getState().addTab(tab)
      }

      useWorkspaceStore.getState().importWorkspace(workspace)
      if (replace) {
        useWorkspaceStore.getState().setActiveWorkspace(workspace.id)
      }
      const activeTab = (workspace.activeTabId && tabMap.has(workspace.activeTabId))
        ? workspace.activeTabId
        : workspace.tabs[0]
      if (activeTab) useTabStore.getState().setActiveTab(activeTab)
    })
  }, [])

  // Open browser tab from mini browser / WebContentsView link click
  useEffect(() => {
    if (!window.electronAPI?.onBrowserViewOpenInTab) return
    return window.electronAPI.onBrowserViewOpenInTab((url: string) => {
      openBrowserTab(url)
    })
  }, [])
}
```

- [ ] **Step 2: 修改 App.tsx — 移除 4 個 effect，加 import**

移除 App.tsx 中以下區塊：
- L98–101: `signalReady` effect
- L103–121: `onTabReceived` effect
- L123–155: `onWorkspaceReceived` effect
- L157–163: `onBrowserViewOpenInTab` effect

加入 import 和 hook 呼叫：
```tsx
import { useElectronIpc } from './hooks/useElectronIpc'
```

在 `useNotificationDispatcher()` 之後加入：
```tsx
useElectronIpc()
```

清理不再需要的 App.tsx imports：
- `openBrowserTab` — 只被 onBrowserViewOpenInTab 使用，移除
- `type Tab` — 檢查是否還有其他用途。App.tsx 中 `displayTabs: Tab[]` 仍使用 → 保留

- [ ] **Step 3: 執行測試確認無回歸**

Run: `cd spa && npx vitest run`
Expected: all passed

- [ ] **Step 4: Commit**

```bash
git add spa/src/hooks/useElectronIpc.ts spa/src/App.tsx
git commit -m "refactor: extract useElectronIpc hook from App.tsx

Fixes #231 — narrow onWorkspaceReceived catch to JSON.parse only"
```

---

### Task 4: 提取 useWorkspaceWindowActions hook

**Files:**
- Create: `spa/src/hooks/useWorkspaceWindowActions.ts`
- Modify: `spa/src/App.tsx:226-269` (移除 4 個函式)

- [ ] **Step 1: 建立 `spa/src/hooks/useWorkspaceWindowActions.ts`**

```ts
import { useCallback } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'

/**
 * Workspace tear-off / merge handlers for Electron multi-window.
 * Returns no-op-safe handlers — callers don't need to check electronAPI.
 */
export function useWorkspaceWindowActions() {
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const tabs = useTabStore((s) => s.tabs)

  const prepareWorkspacePayload = useCallback((wsId: string) => {
    const ws = workspaces.find(w => w.id === wsId)
    if (!ws || ws.tabs.length === 0) return null
    const tabData = ws.tabs.map(id => tabs[id]).filter(Boolean)
    if (tabData.length === 0) return null
    return { ws, payload: JSON.stringify({ workspace: ws, tabData }) }
  }, [workspaces, tabs])

  const removeWorkspaceFromStore = useCallback((tabIds: string[], wsId: string) => {
    const { tabs: currentTabs, tabOrder: currentTabOrder } = useTabStore.getState()
    const newTabs = { ...currentTabs }
    const newTabOrder = currentTabOrder.filter(id => !tabIds.includes(id))
    for (const id of tabIds) delete newTabs[id]
    useTabStore.setState({ tabs: newTabs, tabOrder: newTabOrder, activeTabId: null })
    useWorkspaceStore.getState().removeWorkspace(wsId)
    const wsState = useWorkspaceStore.getState()
    const newActiveWs = wsState.activeWorkspaceId
      ? wsState.workspaces.find(w => w.id === wsState.activeWorkspaceId)
      : null
    const syncedTabId = newActiveWs?.activeTabId ?? newActiveWs?.tabs[0] ?? newTabOrder[0] ?? null
    if (syncedTabId) useTabStore.getState().setActiveTab(syncedTabId)
  }, [])

  const handleWsTearOff = useCallback(async (wsId: string) => {
    if (!window.electronAPI) return
    const prepared = prepareWorkspacePayload(wsId)
    if (!prepared) return
    try {
      await window.electronAPI.tearOffWorkspace(prepared.payload)
      removeWorkspaceFromStore(prepared.ws.tabs, wsId)
    } catch { /* IPC failed — keep data intact */ }
  }, [prepareWorkspacePayload, removeWorkspaceFromStore])

  const handleWsMergeTo = useCallback(async (wsId: string, targetWindowId: string) => {
    if (!window.electronAPI) return
    const prepared = prepareWorkspacePayload(wsId)
    if (!prepared) return
    try {
      await window.electronAPI.mergeWorkspace(prepared.payload, targetWindowId)
      removeWorkspaceFromStore(prepared.ws.tabs, wsId)
    } catch { /* IPC failed — keep data intact */ }
  }, [prepareWorkspacePayload, removeWorkspaceFromStore])

  return { handleWsTearOff, handleWsMergeTo }
}
```

- [ ] **Step 2: 修改 App.tsx — 移除函式，加 import + hook 呼叫**

移除 App.tsx 中以下區塊：
- `prepareWorkspacePayload` 的 useCallback（整個定義）
- `removeWorkspaceFromStore` 的 useCallback（整個定義）
- `handleWsTearOff` 的 useCallback（整個定義）
- `handleWsMergeTo` 的 useCallback（整個定義）

加入：
```tsx
import { useWorkspaceWindowActions } from './hooks/useWorkspaceWindowActions'
```

在 `useElectronIpc()` 之後加入：
```tsx
const { handleWsTearOff, handleWsMergeTo } = useWorkspaceWindowActions()
```

- [ ] **Step 3: 執行測試確認無回歸**

Run: `cd spa && npx vitest run`
Expected: all passed

- [ ] **Step 4: Commit**

```bash
git add spa/src/hooks/useWorkspaceWindowActions.ts spa/src/App.tsx
git commit -m "refactor: extract useWorkspaceWindowActions hook from App.tsx"
```

---

### Task 5: 將 App.tsx inline lambda 提為具名 callback

**Files:**
- Modify: `spa/src/App.tsx` (將 JSX 內所有多行 inline lambda 提為具名 useCallback)

此 task 消除 JSX 中散落的 store 操作，讓 render 區塊只剩 prop 傳遞。

- [ ] **Step 1: 提取 ActivityBar 和 StatusBar 的 inline handlers**

在 App.tsx 的 `openWsSettings` 定義之後、JSX return 之前，加入以下具名 callback：

```tsx
  const handleSelectHome = useCallback(() => {
    useWorkspaceStore.getState().setActiveWorkspace(null)
    const firstStandalone = standaloneTabIds[0]
    if (firstStandalone) {
      handleSelectTab(firstStandalone)
    } else {
      useTabStore.getState().setActiveTab(null)
    }
  }, [standaloneTabIds, handleSelectTab])

  const handleAddWorkspace = useCallback(() => {
    if (workspaces.length === 0 && tabOrder.length > 0) {
      const ws = useWorkspaceStore.getState().addWorkspace('Workspace 1')
      setMigrateDialog({ wsId: ws.id, wsName: ws.name })
    } else {
      const count = workspaces.length + 1
      const ws = useWorkspaceStore.getState().addWorkspace(`Workspace ${count}`)
      openWsSettings(ws.id)
    }
  }, [workspaces.length, tabOrder.length, openWsSettings])

  const handleOpenHosts = useCallback(() => {
    openSingletonAndSelect({ kind: 'hosts' })
  }, [openSingletonAndSelect])

  const handleOpenSettings = useCallback(() => {
    openSingletonAndSelect({ kind: 'settings', scope: 'global' })
  }, [openSingletonAndSelect])

  const handleViewModeChange = useCallback((tabId: string, paneId: string, mode: 'terminal' | 'stream') => {
    useTabStore.getState().setViewMode(tabId, paneId, mode)
  }, [])

  const handleNavigateToHost = useCallback((hostId: string) => {
    openSingletonAndSelect({ kind: 'hosts' })
    useHostStore.getState().setActiveHost(hostId)
  }, [openSingletonAndSelect])

  const handleMigrateConfirm = useCallback(() => {
    if (!migrateDialog) return
    tabOrder.forEach((tabId) => {
      useWorkspaceStore.getState().insertTab(tabId, migrateDialog.wsId)
    })
    setMigrateDialog(null)
    openWsSettings(migrateDialog.wsId)
  }, [migrateDialog, tabOrder, openWsSettings])

  const handleMigrateSkip = useCallback(() => {
    setMigrateDialog(null)
    useWorkspaceStore.getState().setActiveWorkspace(null)
  }, [])
```

注意：`openSingletonAndSelect` 來自 Task 2 新增的 `useTabWorkspaceActions` 回傳值。需要在 App.tsx 的 destructuring 中加入它：

```tsx
  const {
    contextMenu,
    setContextMenu,
    contextMenuHasRightUnlocked,
    handleSelectWorkspace,
    handleSelectTab,
    handleCloseTab,
    handleAddTab,
    handleReorderTabs,
    handleContextMenu,
    handleMiddleClick,
    handleContextAction,
    renameTarget,
    renameError,
    handleRenameConfirm,
    handleRenameCancel,
    handleClearRenameError,
    openSingletonAndSelect,
  } = useTabWorkspaceActions(displayTabs)
```

同時 `openWsSettings` 可以簡化為使用 `openSingletonAndSelect`：
```tsx
  const openWsSettings = useCallback((wsId: string) => {
    openSingletonAndSelect({ kind: 'settings', scope: { workspaceId: wsId } })
  }, [openSingletonAndSelect])
```

- [ ] **Step 2: 簡化 JSX — 替換所有 inline lambda 為具名 reference**

ActivityBar 區塊變為：
```tsx
          <ActivityBar
            workspaces={workspaces}
            activeWorkspaceId={activeStandaloneTabId ? null : activeWorkspaceId}
            activeStandaloneTabId={activeStandaloneTabId}
            onSelectWorkspace={handleSelectWorkspace}
            onSelectHome={handleSelectHome}
            standaloneTabIds={standaloneTabIds}
            onAddWorkspace={handleAddWorkspace}
            onReorderWorkspaces={(ids) => useWorkspaceStore.getState().reorderWorkspaces(ids)}
            onContextMenuWorkspace={handleWsContextMenu}
            onOpenHosts={handleOpenHosts}
            onOpenSettings={handleOpenSettings}
          />
```

StatusBar 區塊變為：
```tsx
            <StatusBar
              activeTab={activeTab ?? null}
              onViewModeChange={handleViewModeChange}
              onNavigateToHost={handleNavigateToHost}
            />
```

MigrateTabsDialog 區塊變為：
```tsx
        {migrateDialog && (
          <MigrateTabsDialog
            tabCount={tabOrder.length}
            workspaceName={migrateDialog.wsName}
            onMigrate={handleMigrateConfirm}
            onSkip={handleMigrateSkip}
          />
        )}
```

- [ ] **Step 3: 清理不再需要的 imports**

移除 App.tsx 中不再直接使用的 imports：
- `useHostStore` — 檢查：`handleNavigateToHost` 用了 → 保留
- `isStandaloneTab` — `standaloneTabIds` 計算用了 → 保留

- [ ] **Step 4: 執行測試確認無回歸**

Run: `cd spa && npx vitest run`
Expected: all passed

- [ ] **Step 5: 執行 lint 確認無警告**

Run: `cd spa && pnpm run lint`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add spa/src/App.tsx
git commit -m "refactor: extract inline handlers to named callbacks in App.tsx"
```

---

### Task 6: 最終驗證 + 清理

**Files:**
- Verify: `spa/src/App.tsx` (確認行數 ≤ 200)
- Verify: all tests pass
- Verify: lint clean

- [ ] **Step 1: 確認 App.tsx 行數**

Run: `wc -l spa/src/App.tsx`
Expected: ≤ 200 行

- [ ] **Step 2: 確認所有測試通過**

Run: `cd spa && npx vitest run`
Expected: all passed（含 Task 2 新增的 3 個測試）

- [ ] **Step 3: 確認 lint 無錯誤**

Run: `cd spa && pnpm run lint`
Expected: no errors

- [ ] **Step 4: 確認 build 成功**

Run: `cd spa && pnpm run build`
Expected: 成功產出 dist/

- [ ] **Step 5: 檢視最終 App.tsx 結構**

確認 App.tsx 呈現以下結構：
1. Imports
2. `export default function App()` 
3. Store subscriptions（5 行）
4. Hook 呼叫（7 行：useRelayWsManager, useMultiHostEventWs, useRouteSync, useShortcuts, useNotificationDispatcher, useElectronIpc, useWorkspaceWindowActions）
5. Derived state（visibleTabIds, displayTabs, standaloneTabIds, activeStandaloneTabId）
6. useTabWorkspaceActions destructuring
7. 具名 callback handlers
8. JSX return — 純組裝，無 inline 業務邏輯
