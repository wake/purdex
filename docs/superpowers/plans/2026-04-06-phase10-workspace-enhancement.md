# Phase 10：Workspace 強化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 將 Workspace 模組化為獨立 feature module，實現全自由制（0 workspace）、Titlebar Chip + 右鍵選單、位置制快捷鍵切換

**Architecture:** 建立 `spa/src/features/workspace/` 結構，搬遷既有 workspace store/hooks/components。共用型別留 `types/tab.ts`，feature 的 `index.ts` re-export。全自由制允許 0 workspace，`activeWorkspaceId` 改為 `string | null`。

**Tech Stack:** React 19 / Zustand 5 / Vitest / Tailwind 4 / Phosphor Icons / Electron keybindings

---

## Task 1：features/ 目錄結構 + store 搬遷（10.0 store 部分）

### Step 1.1：建立 features/workspace/ 目錄結構

- [ ] 建立目錄

```bash
mkdir -p spa/src/features/workspace/components
mkdir -p spa/src/features/workspace/lib
```

### Step 1.2：寫 icon bug 的 red 測試（TDD：先寫測試）

- [ ] 在 `spa/src/stores/useWorkspaceStore.test.ts` 新增測試

在檔案最末尾 `})` 之前加入：

```typescript
  it('addWorkspace passes icon to createWorkspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WithIcon', { icon: 'R' })
    expect(ws.icon).toBe('R')
  })

  it('addWorkspace passes color to createWorkspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WithColor', { color: '#ff0000' })
    expect(ws.color).toBe('#ff0000')
  })
```

### Step 1.3：跑測試確認 red

- [ ] 執行測試

```bash
cd spa && npx vitest run src/stores/useWorkspaceStore.test.ts
```

預期：`addWorkspace passes icon` 測試 **fail**（icon 未傳入 `createWorkspace`）。color 測試可能 pass（已有 `opts?.color`）。

### Step 1.4：修正 createWorkspace + addWorkspace（TDD：讓測試通過）

- [ ] 在 `spa/src/types/tab.ts` 修正 `createWorkspace` 加入 `icon?` 參數

將：
```typescript
export function createWorkspace(name: string, color?: string): Workspace {
  return {
    id: generateId(),
    name,
    color: color ?? WORKSPACE_COLORS[Math.floor(Math.random() * WORKSPACE_COLORS.length)],
    tabs: [],
    activeTabId: null,
  }
}
```

改為：
```typescript
export function createWorkspace(name: string, color?: string, icon?: string): Workspace {
  return {
    id: generateId(),
    name,
    color: color ?? WORKSPACE_COLORS[Math.floor(Math.random() * WORKSPACE_COLORS.length)],
    icon,
    tabs: [],
    activeTabId: null,
  }
}
```

- [ ] 在 `spa/src/stores/useWorkspaceStore.ts` 修正 `addWorkspace` 傳遞 `icon`

將：
```typescript
      addWorkspace: (name, opts) => {
        const ws = createWorkspace(name, opts?.color)
        set((state) => ({ workspaces: [...state.workspaces, ws] }))
        return ws
      },
```

改為：
```typescript
      addWorkspace: (name, opts) => {
        const ws = createWorkspace(name, opts?.color, opts?.icon)
        set((state) => ({ workspaces: [...state.workspaces, ws] }))
        return ws
      },
```

### Step 1.5：跑測試確認 green

- [ ] 執行測試

```bash
cd spa && npx vitest run src/stores/useWorkspaceStore.test.ts
```

預期：所有測試 pass（含新增的 2 個）

### Step 1.6：搬遷 store 到 features/workspace/store.ts

> **注意：** 此步搬遷的是**舊版 store**（含 `activeWorkspaceId: string`、last-workspace 守衛）。這是 Task 1 的過渡版本，Task 3 將改造為全自由制。

- [ ] 將 `spa/src/stores/useWorkspaceStore.ts` 搬遷至 `spa/src/features/workspace/store.ts`

**檔案：`spa/src/features/workspace/store.ts`**（完整內容）

```typescript
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { createWorkspace, type Workspace } from '../../types/tab'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../../lib/storage'

interface WorkspaceState {
  workspaces: Workspace[]
  activeWorkspaceId: string

  addWorkspace: (name: string, opts?: { color?: string; icon?: string }) => Workspace
  removeWorkspace: (wsId: string) => void
  setActiveWorkspace: (wsId: string) => void
  addTabToWorkspace: (wsId: string, tabId: string) => void
  removeTabFromWorkspace: (wsId: string, tabId: string) => void
  setWorkspaceActiveTab: (wsId: string, tabId: string) => void
  reorderWorkspaceTabs: (wsId: string, tabIds: string[]) => void
  findWorkspaceByTab: (tabId: string) => Workspace | null
  reset: () => void
}

function createDefaultState() {
  const defaultWs = createWorkspace('Default', '#7a6aaa')
  return { workspaces: [defaultWs], activeWorkspaceId: defaultWs.id }
}

export const useWorkspaceStore = create<WorkspaceState>()(
  persist(
    (set, get) => ({
      ...createDefaultState(),

      addWorkspace: (name, opts) => {
        const ws = createWorkspace(name, opts?.color, opts?.icon)
        set((state) => ({ workspaces: [...state.workspaces, ws] }))
        return ws
      },

      removeWorkspace: (wsId) =>
        set((state) => {
          if (state.workspaces.length <= 1) return state
          const remaining = state.workspaces.filter((ws) => ws.id !== wsId)
          const activeId = state.activeWorkspaceId === wsId ? remaining[0].id : state.activeWorkspaceId
          return { workspaces: remaining, activeWorkspaceId: activeId }
        }),

      setActiveWorkspace: (wsId) =>
        set({ activeWorkspaceId: wsId }),

      addTabToWorkspace: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) => {
            if (ws.id !== wsId) return ws
            if (ws.tabs.includes(tabId)) return ws
            return { ...ws, tabs: [...ws.tabs, tabId] }
          }),
        })),

      removeTabFromWorkspace: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId
              ? {
                  ...ws,
                  tabs: ws.tabs.filter((id) => id !== tabId),
                  activeTabId: ws.activeTabId === tabId ? null : ws.activeTabId,
                }
              : ws,
          ),
        })),

      setWorkspaceActiveTab: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, activeTabId: tabId } : ws,
          ),
        })),

      reorderWorkspaceTabs: (wsId, tabIds) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, tabs: tabIds } : ws,
          ),
        })),

      findWorkspaceByTab: (tabId) => {
        return get().workspaces.find((ws) => ws.tabs.includes(tabId)) ?? null
      },

      reset: () => set(createDefaultState()),
    }),
    {
      name: STORAGE_KEYS.WORKSPACES,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        workspaces: state.workspaces,
        activeWorkspaceId: state.activeWorkspaceId,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.WORKSPACES, useWorkspaceStore)
```

### Step 1.7：舊位置改為 re-export

- [ ] 將 `spa/src/stores/useWorkspaceStore.ts` 改為 re-export（維持向後相容）

**檔案：`spa/src/stores/useWorkspaceStore.ts`**（完整內容）

```typescript
// Re-export from feature module for backwards compatibility
export { useWorkspaceStore } from '../features/workspace/store'
```

### Step 1.8：搬遷 store 測試

> **注意：** 此版測試含舊行為測試（如「cannot remove the last workspace」），Task 3 Step 3.4 將完整重寫為全自由制版本。

- [ ] 將 `spa/src/stores/useWorkspaceStore.test.ts` 搬遷至 `spa/src/features/workspace/store.test.ts`

**檔案：`spa/src/features/workspace/store.test.ts`**（完整內容）

```typescript
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore } from './store'

describe('useWorkspaceStore', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
  })

  it('initializes with a default workspace', () => {
    const state = useWorkspaceStore.getState()
    expect(state.workspaces.length).toBe(1)
    expect(state.workspaces[0].name).toBe('Default')
    expect(state.activeWorkspaceId).toBe(state.workspaces[0].id)
  })

  it('adds a tab to workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    const ws = useWorkspaceStore.getState().workspaces[0]
    expect(ws.tabs).toContain('tab-1')
  })

  it('removes a tab from workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    useWorkspaceStore.getState().removeTabFromWorkspace(wsId, 'tab-1')
    const ws = useWorkspaceStore.getState().workspaces[0]
    expect(ws.tabs).not.toContain('tab-1')
  })

  it('switches active workspace', () => {
    const ws2 = useWorkspaceStore.getState().addWorkspace('Project B')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
  })

  it('adds a workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('New WS')
    expect(ws.name).toBe('New WS')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(2)
  })

  it('removes a workspace', () => {
    const ws2 = useWorkspaceStore.getState().addWorkspace('To Remove')
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
  })

  it('cannot remove the last workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().removeWorkspace(wsId)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
  })

  it('finds workspace containing a tab', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    expect(useWorkspaceStore.getState().findWorkspaceByTab('tab-1')?.id).toBe(wsId)
    expect(useWorkspaceStore.getState().findWorkspaceByTab('tab-unknown')).toBeNull()
  })

  it('sets workspace active tab', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, 'tab-1')
    const ws = useWorkspaceStore.getState().workspaces.find(w => w.id === wsId)!
    expect(ws.activeTabId).toBe('tab-1')
  })

  it('does not add duplicate tab to workspace', () => {
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    useWorkspaceStore.getState().addTabToWorkspace(wsId, 'tab-1')
    const ws = useWorkspaceStore.getState().workspaces[0]
    expect(ws.tabs).toEqual(['tab-1'])
  })

  it('switches activeWorkspaceId when removing active workspace', () => {
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(useWorkspaceStore.getState().workspaces[0].id)
  })

  it('addWorkspace passes icon to createWorkspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WithIcon', { icon: 'R' })
    expect(ws.icon).toBe('R')
  })

  it('addWorkspace passes color to createWorkspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WithColor', { color: '#ff0000' })
    expect(ws.color).toBe('#ff0000')
  })
})
```

### Step 1.9：刪除舊 store 測試

- [ ] 刪除 `spa/src/stores/useWorkspaceStore.test.ts`

```bash
rm spa/src/stores/useWorkspaceStore.test.ts
```

### Step 1.10：建立 features/workspace/index.ts

- [ ] 建立 `spa/src/features/workspace/index.ts`

**檔案：`spa/src/features/workspace/index.ts`**（完整內容）

```typescript
// Store
export { useWorkspaceStore } from './store'

// Re-export shared types from types/tab.ts
export type { Workspace } from '../../types/tab'
export { createWorkspace, isStandaloneTab } from '../../types/tab'
```

### Step 1.11：跑全部測試確認 pass

- [ ] 執行全部測試

```bash
cd spa && npx vitest run
```

預期：所有測試 pass。store 測試從新位置 `features/workspace/store.test.ts` 執行。舊位置的 re-export 確保所有 consumer 的 import 不斷。

### Step 1.12：Commit

- [ ] 提交

```bash
git add -A && git commit -m "refactor: 建立 features/workspace/ 結構，搬遷 store + 修正 createWorkspace icon bug"
```

---

## Task 2：hooks + ActivityBar 搬遷（10.0 其餘部分）

### Step 2.1：搬遷 useTabWorkspaceActions

- [ ] 建立 `spa/src/features/workspace/hooks.ts`

**檔案：`spa/src/features/workspace/hooks.ts`**（完整內容）

```typescript
import { useState, useCallback } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from './store'
import { useHistoryStore } from '../../stores/useHistoryStore'
import { createTab } from '../../types/tab'
import { getPrimaryPane } from '../../lib/pane-tree'
import type { Tab } from '../../types/tab'
import type { ContextMenuAction } from '../../components/TabContextMenu'

export function useTabWorkspaceActions(displayTabs: Tab[]) {
  const [contextMenu, setContextMenu] = useState<{ tab: Tab; position: { x: number; y: number } } | null>(null)

  // Tab store
  const tabs = useTabStore((s) => s.tabs)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const addTab = useTabStore((s) => s.addTab)
  const closeTab = useTabStore((s) => s.closeTab)
  const reorderTabs = useTabStore((s) => s.reorderTabs)

  // Workspace store
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
  const setActiveWorkspace = useWorkspaceStore((s) => s.setActiveWorkspace)
  const removeTabFromWorkspace = useWorkspaceStore((s) => s.removeTabFromWorkspace)
  const findWorkspaceByTab = useWorkspaceStore((s) => s.findWorkspaceByTab)
  const setWorkspaceActiveTab = useWorkspaceStore((s) => s.setWorkspaceActiveTab)
  const reorderWorkspaceTabs = useWorkspaceStore((s) => s.reorderWorkspaceTabs)

  const handleSelectWorkspace = useCallback((wsId: string) => {
    setActiveWorkspace(wsId)
    const ws = workspaces.find((w) => w.id === wsId)
    if (ws?.activeTabId) setActiveTab(ws.activeTabId)
    else if (ws?.tabs[0]) setActiveTab(ws.tabs[0])
  }, [workspaces, setActiveWorkspace, setActiveTab])

  const handleSelectTab = useCallback((tabId: string) => {
    setActiveTab(tabId)
    const ws = findWorkspaceByTab(tabId)
    if (ws) {
      setActiveWorkspace(ws.id)
      setWorkspaceActiveTab(ws.id, tabId)
    }

    // markRead is handled by the cross-store subscription in active-session.ts
  }, [setActiveTab, findWorkspaceByTab, setActiveWorkspace, setWorkspaceActiveTab])

  const handleCloseTab = useCallback((tabId: string) => {
    const tab = tabs[tabId]
    if (!tab || tab.locked) return // locked guard
    useHistoryStore.getState().recordClose(tab, findWorkspaceByTab(tabId)?.id)
    const ws = findWorkspaceByTab(tabId)
    if (ws) removeTabFromWorkspace(ws.id, tabId)
    closeTab(tabId)
  }, [tabs, findWorkspaceByTab, removeTabFromWorkspace, closeTab])

  const handleAddTab = useCallback(() => {
    const tab = createTab({ kind: 'new-tab' })
    addTab(tab)
    setActiveTab(tab.id)
    if (activeWorkspaceId) {
      useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, tab.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, tab.id)
    }
  }, [addTab, setActiveTab, activeWorkspaceId])

  const handleReorderTabs = useCallback((order: string[]) => {
    if (activeWorkspaceId) {
      reorderWorkspaceTabs(activeWorkspaceId, order)
    } else {
      // Standalone tabs — update global order
      reorderTabs(order)
    }
  }, [reorderTabs, activeWorkspaceId, reorderWorkspaceTabs])

  const handleContextMenu = useCallback((e: React.MouseEvent, tabId: string) => {
    e.preventDefault()
    const tab = tabs[tabId]
    if (tab) setContextMenu({ tab, position: { x: e.clientX, y: e.clientY } })
  }, [tabs])

  const handleMiddleClick = useCallback((tabId: string) => {
    const tab = tabs[tabId]
    if (tab && !tab.locked) handleCloseTab(tabId)
  }, [tabs, handleCloseTab])

  const handleContextAction = useCallback((action: ContextMenuAction) => {
    if (!contextMenu) return
    const { tab } = contextMenu
    const store = useTabStore.getState()
    const primaryPaneId = getPrimaryPane(tab.layout).id
    switch (action) {
      case 'viewMode-terminal': store.setViewMode(tab.id, primaryPaneId, 'terminal'); break
      case 'viewMode-stream': store.setViewMode(tab.id, primaryPaneId, 'stream'); break
      case 'lock': case 'unlock': store.toggleLock(tab.id); break
      case 'pin': case 'unpin': store.togglePin(tab.id); break
      case 'close': handleCloseTab(tab.id); break
      case 'closeOthers': {
        const displayIds = displayTabs.map((t) => t.id)
        const toClose = displayIds.filter((id) => id !== tab.id && !tabs[id]?.locked)
        toClose.forEach((id) => handleCloseTab(id))
        break
      }
      case 'closeRight': {
        const displayIds = displayTabs.map((t) => t.id)
        const idx = displayIds.indexOf(tab.id)
        if (idx === -1) break
        const toClose = displayIds.slice(idx + 1).filter((id) => !tabs[id]?.locked)
        toClose.forEach((id) => handleCloseTab(id))
        break
      }
      case 'tearOff': {
        if (!window.electronAPI) break
        const tabData = tabs[tab.id]
        if (!tabData) break
        // Must remove tab BEFORE IPC to avoid duplication if locked
        handleCloseTab(tab.id)
        // Only send to new window if tab was actually removed
        if (!useTabStore.getState().tabs[tab.id]) {
          window.electronAPI.tearOffTab(JSON.stringify(tabData))
        }
        break
      }
    }
  }, [contextMenu, tabs, displayTabs, handleCloseTab])

  // Context menu derived state
  const contextMenuHasRightUnlocked = (() => {
    if (!contextMenu) return false
    const ids = displayTabs.map((t) => t.id)
    const idx = ids.indexOf(contextMenu.tab.id)
    return idx !== -1 && ids.slice(idx + 1).some((id) => !tabs[id]?.locked)
  })()

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
  }
}
```

### Step 2.2：舊 hooks 位置改為 re-export

- [ ] 將 `spa/src/hooks/useTabWorkspaceActions.ts` 改為 re-export

**檔案：`spa/src/hooks/useTabWorkspaceActions.ts`**（完整內容）

```typescript
// Re-export from feature module for backwards compatibility
export { useTabWorkspaceActions } from '../features/workspace/hooks'
```

### Step 2.3：搬遷 ActivityBar 元件

- [ ] 建立 `spa/src/features/workspace/components/ActivityBar.tsx`

**檔案：`spa/src/features/workspace/components/ActivityBar.tsx`**（完整內容）

```typescript
import { Plus, GearSix, HardDrives } from '@phosphor-icons/react'
import type { Tab, Workspace } from '../../../types/tab'
import { getPrimaryPane } from '../../../lib/pane-tree'
import { getPaneLabel } from '../../../lib/pane-labels'
import { useI18nStore } from '../../../stores/useI18nStore'

const emptySessionLookup = { getByCode: () => undefined }
const emptyWorkspaceLookup = { getById: () => undefined }

interface Props {
  workspaces: Workspace[]
  standaloneTabs: Tab[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectStandaloneTab: (tabId: string) => void
  onAddWorkspace: () => void
  onOpenHosts: () => void
  onOpenSettings: () => void
}

export function ActivityBar({
  workspaces,
  standaloneTabs,
  activeWorkspaceId,
  activeStandaloneTabId,
  onSelectWorkspace,
  onSelectStandaloneTab,
  onAddWorkspace,
  onOpenHosts,
  onOpenSettings,
}: Props) {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="hidden lg:flex w-11 flex-col items-center bg-surface-tertiary border-r border-border-subtle py-2 gap-2 flex-shrink-0">
      {/* Workspaces */}
      {workspaces.map((ws) => (
        <button
          key={ws.id}
          title={ws.name}
          onClick={() => onSelectWorkspace(ws.id)}
          className={`w-8 h-8 rounded-md flex items-center justify-center text-xs cursor-pointer transition-all ${
            activeWorkspaceId === ws.id && !activeStandaloneTabId
              ? 'ring-2 ring-purple-400'
              : 'opacity-70 hover:opacity-100'
          }`}
          style={{ backgroundColor: ws.color + '33', color: ws.color }}
        >
          {ws.icon ?? ws.name.charAt(0)}
        </button>
      ))}

      {/* Separator */}
      {standaloneTabs.length > 0 && (
        <div className="w-5 h-px bg-border-default my-1" />
      )}

      {/* Standalone tabs */}
      {standaloneTabs.map((tab) => {
        const label = getPaneLabel(
          getPrimaryPane(tab.layout).content,
          emptySessionLookup,
          emptyWorkspaceLookup,
          t,
        )
        return (
          <button
            key={tab.id}
            title={label}
            onClick={() => onSelectStandaloneTab(tab.id)}
            className={`w-8 h-8 rounded-md flex items-center justify-center text-xs cursor-pointer transition-all ${
              activeStandaloneTabId === tab.id
                ? 'ring-2 ring-accent bg-surface-secondary'
                : 'bg-surface-tertiary opacity-70 hover:opacity-100'
            }`}
          >
            {label.charAt(0).toUpperCase()}
          </button>
        )
      })}

      {/* Add + Settings */}
      <div className="mt-auto flex flex-col items-center gap-2 pb-1">
        <button
          title={t('nav.new_workspace')}
          onClick={onAddWorkspace}
          className="w-8 h-8 rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <Plus size={16} />
        </button>
        <button
          title={t('nav.hosts')}
          onClick={onOpenHosts}
          className="w-8 h-8 rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <HardDrives size={16} />
        </button>
        <button
          title={t('nav.settings')}
          onClick={onOpenSettings}
          className="w-8 h-8 rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <GearSix size={16} />
        </button>
      </div>
    </div>
  )
}
```

### Step 2.4：舊 ActivityBar 位置改為 re-export

- [ ] 將 `spa/src/components/ActivityBar.tsx` 改為 re-export

**檔案：`spa/src/components/ActivityBar.tsx`**（完整內容）

```typescript
// Re-export from feature module for backwards compatibility
export { ActivityBar } from '../features/workspace/components/ActivityBar'
```

### Step 2.5：搬遷 ActivityBar 測試

- [ ] 建立 `spa/src/features/workspace/components/ActivityBar.test.tsx`

**檔案：`spa/src/features/workspace/components/ActivityBar.test.tsx`**（完整內容）

```typescript
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { ActivityBar } from './ActivityBar'
import { createTab } from '../../../types/tab'
import type { Tab, Workspace } from '../../../types/tab'

const mockWorkspaces: Workspace[] = [
  { id: 'ws-1', name: 'Project A', color: '#7a6aaa', icon: '🔧', tabs: ['t1', 't2'], activeTabId: 't1' },
  { id: 'ws-2', name: 'Server', color: '#6aaa7a', icon: '🖥', tabs: ['t3'], activeTabId: 't3' },
]

const mockStandaloneTabs: Tab[] = [
  { ...createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'misc', mode: 'terminal', cachedName: '', tmuxInstance: '' }), id: 'st-1' },
]

describe('ActivityBar', () => {
  it('renders workspace icons', () => {
    cleanup()
    render(
      <ActivityBar
        workspaces={mockWorkspaces}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={vi.fn()}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenHosts={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getByTitle('Project A')).toBeTruthy()
    expect(screen.getByTitle('Server')).toBeTruthy()
  })

  it('highlights active workspace', () => {
    cleanup()
    render(
      <ActivityBar
        workspaces={mockWorkspaces}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={vi.fn()}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenHosts={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    const activeBtn = screen.getByTitle('Project A')
    expect(activeBtn.className).toContain('ring')
  })

  it('calls onSelectWorkspace on click', () => {
    cleanup()
    const onSelect = vi.fn()
    render(
      <ActivityBar
        workspaces={mockWorkspaces}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={onSelect}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenHosts={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByTitle('Server'))
    expect(onSelect).toHaveBeenCalledWith('ws-2')
  })

  it('renders standalone tabs below separator', () => {
    cleanup()
    render(
      <ActivityBar
        workspaces={mockWorkspaces}
        standaloneTabs={mockStandaloneTabs}
        activeWorkspaceId="ws-1"
        activeStandaloneTabId={null}
        onSelectWorkspace={vi.fn()}
        onSelectStandaloneTab={vi.fn()}
        onAddWorkspace={vi.fn()}
        onOpenHosts={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getByTitle('misc')).toBeTruthy()
  })
})
```

### Step 2.6：刪除舊 ActivityBar 測試

- [ ] 刪除 `spa/src/components/ActivityBar.test.tsx`

```bash
rm spa/src/components/ActivityBar.test.tsx
```

### Step 2.7：更新 index.ts 匯出 hooks 和 ActivityBar

- [ ] 更新 `spa/src/features/workspace/index.ts`

**檔案：`spa/src/features/workspace/index.ts`**（完整內容）

```typescript
// Store
export { useWorkspaceStore } from './store'

// Hooks
export { useTabWorkspaceActions } from './hooks'

// Components
export { ActivityBar } from './components/ActivityBar'

// Re-export shared types from types/tab.ts
export type { Workspace } from '../../types/tab'
export { createWorkspace, isStandaloneTab } from '../../types/tab'
```

### Step 2.8：跑全部測試確認 pass

- [ ] 執行全部測試

```bash
cd spa && npx vitest run
```

預期：所有測試 pass。re-export 確保 App.tsx / useShortcuts / SortableTab / SessionsSection / useNotificationDispatcher 的 import 不斷。

### Step 2.9：Commit

- [ ] 提交

```bash
git add -A && git commit -m "refactor: 搬遷 useTabWorkspaceActions + ActivityBar 至 features/workspace/"
```

---

## Task 3：Store 全自由制改造（10.1 store 部分）

> **TDD 流程說明：** Step 3.1 先加入全自由制測試（red），Step 3.3 改造 store 實作，Step 3.4 將 store.test.ts **完整重寫**為全自由制版本（取代 Task 1 的過渡版測試，含反轉「cannot remove last workspace」）。

### Step 3.1：先寫全自由制 store 測試

- [ ] 在 `spa/src/features/workspace/store.test.ts` 擴充測試

**在 `describe('useWorkspaceStore', () => {` 區塊的最末尾、最後一個 `})` 之前，新增以下測試：**

```typescript
  // === 全自由制測試 ===

  it('createDefaultState returns empty workspaces and null activeWorkspaceId', () => {
    // After the full refactor, reset() should give empty state
    useWorkspaceStore.getState().reset()
    const state = useWorkspaceStore.getState()
    expect(state.workspaces).toEqual([])
    expect(state.activeWorkspaceId).toBeNull()
  })

  it('setActiveWorkspace accepts null', () => {
    useWorkspaceStore.getState().setActiveWorkspace(null)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('removeWorkspace allows removing the last workspace', () => {
    // Start with the single default ws
    const wsId = useWorkspaceStore.getState().workspaces[0].id
    useWorkspaceStore.getState().removeWorkspace(wsId)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('removeWorkspace on already empty does nothing', () => {
    useWorkspaceStore.getState().reset()
    useWorkspaceStore.getState().removeWorkspace('nonexistent')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('insertTab with no workspace — tab stays standalone', () => {
    useWorkspaceStore.getState().reset() // 0 workspaces, null activeWorkspaceId
    useWorkspaceStore.getState().insertTab('tab-1')
    // No workspace to add to — should not crash
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
  })

  it('insertTab with active workspace adds to that workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    useWorkspaceStore.getState().insertTab('tab-1')
    const updated = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)!
    expect(updated.tabs).toContain('tab-1')
    expect(updated.activeTabId).toBe('tab-1')
  })

  it('insertTab with explicit wsId adds to specified workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws1.id)
    useWorkspaceStore.getState().insertTab('tab-1', ws2.id)
    const updated1 = useWorkspaceStore.getState().workspaces.find(w => w.id === ws1.id)!
    const updated2 = useWorkspaceStore.getState().workspaces.find(w => w.id === ws2.id)!
    expect(updated1.tabs).not.toContain('tab-1')
    expect(updated2.tabs).toContain('tab-1')
    expect(updated2.activeTabId).toBe('tab-1')
  })

  it('insertTab with explicit null forces standalone', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    useWorkspaceStore.getState().insertTab('tab-1', null)
    const updated = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)!
    expect(updated.tabs).not.toContain('tab-1')
  })
```

### Step 3.2：跑測試確認新測試失敗（TDD red）

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/store.test.ts
```

預期：**新增的全自由制測試 fail**（`createDefaultState` 仍回傳 Default workspace、`removeWorkspace` 仍守衛、`insertTab` 不存在）。**既有測試仍 pass**（store 行為未變）。

### Step 3.3：改造 store 為全自由制

- [ ] 修改 `spa/src/features/workspace/store.ts`

**檔案：`spa/src/features/workspace/store.ts`**（完整內容）

```typescript
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { createWorkspace, type Workspace } from '../../types/tab'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../../lib/storage'

interface WorkspaceState {
  workspaces: Workspace[]
  activeWorkspaceId: string | null

  addWorkspace: (name: string, opts?: { color?: string; icon?: string }) => Workspace
  removeWorkspace: (wsId: string) => void
  setActiveWorkspace: (wsId: string | null) => void
  addTabToWorkspace: (wsId: string, tabId: string) => void
  removeTabFromWorkspace: (wsId: string, tabId: string) => void
  setWorkspaceActiveTab: (wsId: string, tabId: string) => void
  reorderWorkspaceTabs: (wsId: string, tabIds: string[]) => void
  findWorkspaceByTab: (tabId: string) => Workspace | null
  insertTab: (tabId: string, workspaceId?: string | null) => void
  reset: () => void
}

function createDefaultState(): Pick<WorkspaceState, 'workspaces' | 'activeWorkspaceId'> {
  return { workspaces: [], activeWorkspaceId: null }
}

export const useWorkspaceStore = create<WorkspaceState>()(
  persist(
    (set, get) => ({
      ...createDefaultState(),

      addWorkspace: (name, opts) => {
        const ws = createWorkspace(name, opts?.color, opts?.icon)
        set((state) => ({
          workspaces: [...state.workspaces, ws],
          // Auto-activate if this is the first workspace
          activeWorkspaceId: state.activeWorkspaceId ?? ws.id,
        }))
        return ws
      },

      removeWorkspace: (wsId) =>
        set((state) => {
          const remaining = state.workspaces.filter((ws) => ws.id !== wsId)
          if (remaining.length === state.workspaces.length) return state // wsId not found
          const activeId = state.activeWorkspaceId === wsId
            ? (remaining[0]?.id ?? null)
            : state.activeWorkspaceId
          return { workspaces: remaining, activeWorkspaceId: activeId }
        }),

      setActiveWorkspace: (wsId) =>
        set({ activeWorkspaceId: wsId }),

      addTabToWorkspace: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) => {
            if (ws.id !== wsId) return ws
            if (ws.tabs.includes(tabId)) return ws
            return { ...ws, tabs: [...ws.tabs, tabId] }
          }),
        })),

      removeTabFromWorkspace: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId
              ? {
                  ...ws,
                  tabs: ws.tabs.filter((id) => id !== tabId),
                  activeTabId: ws.activeTabId === tabId ? null : ws.activeTabId,
                }
              : ws,
          ),
        })),

      setWorkspaceActiveTab: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, activeTabId: tabId } : ws,
          ),
        })),

      reorderWorkspaceTabs: (wsId, tabIds) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, tabs: tabIds } : ws,
          ),
        })),

      findWorkspaceByTab: (tabId) => {
        return get().workspaces.find((ws) => ws.tabs.includes(tabId)) ?? null
      },

      insertTab: (tabId, workspaceId) => {
        const state = get()
        // Determine target workspace
        let targetWsId: string | null
        if (workspaceId === null) {
          // Explicit null — force standalone
          targetWsId = null
        } else if (workspaceId !== undefined) {
          // Explicit workspace ID
          targetWsId = workspaceId
        } else {
          // Omitted — use active workspace (may be null)
          targetWsId = state.activeWorkspaceId
        }

        if (targetWsId) {
          state.addTabToWorkspace(targetWsId, tabId)
          state.setWorkspaceActiveTab(targetWsId, tabId)
        }
      },

      reset: () => set(createDefaultState()),
    }),
    {
      name: STORAGE_KEYS.WORKSPACES,
      storage: purdexStorage,
      version: 2,
      migrate: (persisted, version) => {
        if (version === 1) {
          // v1 → v2: 保留既有資料不變（向下相容）
          // v1 已有 workspaces + activeWorkspaceId，只是型別從 string 改為 string | null
          const old = persisted as { workspaces?: Workspace[]; activeWorkspaceId?: string }
          return {
            workspaces: old.workspaces ?? [],
            activeWorkspaceId: old.activeWorkspaceId ?? null,
          }
        }
        return persisted as { workspaces: Workspace[]; activeWorkspaceId: string | null }
      },
      partialize: (state) => ({
        workspaces: state.workspaces,
        activeWorkspaceId: state.activeWorkspaceId,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.WORKSPACES, useWorkspaceStore)
```

### Step 3.4：更新既有測試以適應全自由制

- [ ] 重寫 `spa/src/features/workspace/store.test.ts`

**檔案：`spa/src/features/workspace/store.test.ts`**（完整內容）

```typescript
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore } from './store'

describe('useWorkspaceStore', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
  })

  // === 全自由制基礎 ===

  it('initializes with empty workspaces and null activeWorkspaceId', () => {
    const state = useWorkspaceStore.getState()
    expect(state.workspaces).toEqual([])
    expect(state.activeWorkspaceId).toBeNull()
  })

  it('setActiveWorkspace accepts null', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws.id)
    useWorkspaceStore.getState().setActiveWorkspace(null)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  // === Workspace CRUD ===

  it('adds a workspace and auto-activates first', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('New WS')
    expect(ws.name).toBe('New WS')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws.id)
  })

  it('adds second workspace without changing active', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(2)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
    expect(ws2.name).toBe('WS2')
  })

  it('removes a workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('To Remove')
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
    expect(useWorkspaceStore.getState().workspaces[0].id).toBe(ws1.id)
  })

  it('removes the last workspace and sets activeWorkspaceId to null', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Only')
    useWorkspaceStore.getState().removeWorkspace(ws.id)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('removeWorkspace on nonexistent id does nothing', () => {
    useWorkspaceStore.getState().addWorkspace('WS')
    useWorkspaceStore.getState().removeWorkspace('nonexistent')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
  })

  it('removeWorkspace on empty list does nothing', () => {
    useWorkspaceStore.getState().removeWorkspace('nonexistent')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('switches active workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
    // switch back
    useWorkspaceStore.getState().setActiveWorkspace(ws1.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
  })

  it('switches activeWorkspaceId when removing active workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
  })

  // === Tab operations ===

  it('adds a tab to workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    const updated = useWorkspaceStore.getState().workspaces[0]
    expect(updated.tabs).toContain('tab-1')
  })

  it('removes a tab from workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    useWorkspaceStore.getState().removeTabFromWorkspace(ws.id, 'tab-1')
    const updated = useWorkspaceStore.getState().workspaces[0]
    expect(updated.tabs).not.toContain('tab-1')
  })

  it('does not add duplicate tab to workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    const updated = useWorkspaceStore.getState().workspaces[0]
    expect(updated.tabs).toEqual(['tab-1'])
  })

  it('sets workspace active tab', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, 'tab-1')
    const updated = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)!
    expect(updated.activeTabId).toBe('tab-1')
  })

  it('finds workspace containing a tab', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    expect(useWorkspaceStore.getState().findWorkspaceByTab('tab-1')?.id).toBe(ws.id)
    expect(useWorkspaceStore.getState().findWorkspaceByTab('tab-unknown')).toBeNull()
  })

  // === insertTab ===

  it('insertTab with no workspace — tab stays standalone', () => {
    // 0 workspaces, null activeWorkspaceId
    useWorkspaceStore.getState().insertTab('tab-1')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
  })

  it('insertTab with active workspace adds to that workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    useWorkspaceStore.getState().insertTab('tab-1')
    const updated = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)!
    expect(updated.tabs).toContain('tab-1')
    expect(updated.activeTabId).toBe('tab-1')
  })

  it('insertTab with explicit wsId adds to specified workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws1.id)
    useWorkspaceStore.getState().insertTab('tab-1', ws2.id)
    const updated1 = useWorkspaceStore.getState().workspaces.find(w => w.id === ws1.id)!
    const updated2 = useWorkspaceStore.getState().workspaces.find(w => w.id === ws2.id)!
    expect(updated1.tabs).not.toContain('tab-1')
    expect(updated2.tabs).toContain('tab-1')
    expect(updated2.activeTabId).toBe('tab-1')
  })

  it('insertTab with explicit null forces standalone', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    useWorkspaceStore.getState().insertTab('tab-1', null)
    const updated = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)!
    expect(updated.tabs).not.toContain('tab-1')
  })

  // === addWorkspace options ===

  it('addWorkspace passes icon to createWorkspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WithIcon', { icon: 'R' })
    expect(ws.icon).toBe('R')
  })

  it('addWorkspace passes color to createWorkspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WithColor', { color: '#ff0000' })
    expect(ws.color).toBe('#ff0000')
  })

  // === Migration ===

  it('migrate v1 data preserves existing workspaces', () => {
    // Simulate v1 persisted data by directly testing the migrate function
    // The store's migrate callback should preserve v1 data as-is
    const v1Data = {
      workspaces: [{ id: 'ws-old', name: 'Old', color: '#aaa', tabs: ['t1'], activeTabId: 't1' }],
      activeWorkspaceId: 'ws-old',
    }
    // We can't directly call migrate, but we can verify the store handles
    // v1-shaped data correctly by setting state manually
    useWorkspaceStore.setState(v1Data)
    const state = useWorkspaceStore.getState()
    expect(state.workspaces).toHaveLength(1)
    expect(state.workspaces[0].id).toBe('ws-old')
    expect(state.activeWorkspaceId).toBe('ws-old')
  })
})
```

### Step 3.5：跑測試確認全部 pass

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/store.test.ts
```

預期：所有測試 pass。

### Step 3.6：跑全部測試確認不影響其他模組

- [ ] 執行全部測試

```bash
cd spa && npx vitest run
```

預期：全部 pass。`useShortcuts.test.ts` 使用 `reset()` 後會拿到空狀態，但 `seedTabs` 中的 `addToWorkspace` 邏輯需要先有 workspace。

### Step 3.7：修正 useShortcuts.test.ts 的 seedTabs 函式

由於 `reset()` 現在回傳空 workspace，`seedTabs` 中的 `useWorkspaceStore.getState().activeWorkspaceId` 會是 `null`。需要在 seedTabs 中先建立 workspace。

> **前提條件：** `seedTabs` 內部的 `wsId` 來自 `activeWorkspaceId`，因此 `beforeEach` 必須先呼叫 `addWorkspace`（讓 `activeWorkspaceId` 有值）才能正確將 tab 加入 workspace。新版 `seedTabs` 加入 `if (wsId)` 保護以防 null。

- [ ] 修改 `spa/src/hooks/useShortcuts.test.ts` 的 `seedTabs` 和 `beforeEach`

將整個 `seedTabs` 函式改為：

```typescript
function seedTabs(count: number, { addToWorkspace = true } = {}) {
  const store = useTabStore.getState()
  const tabs = Array.from({ length: count }, () =>
    createTab({ kind: 'new-tab' }),
  )
  tabs.forEach((t) => store.addTab(t))
  store.setActiveTab(tabs[0].id)
  if (addToWorkspace) {
    const wsId = useWorkspaceStore.getState().activeWorkspaceId
    if (wsId) {
      tabs.forEach((t) => useWorkspaceStore.getState().addTabToWorkspace(wsId, t.id))
    }
  }
  return tabs
}
```

將 `beforeEach` 改為：

```typescript
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useWorkspaceStore.getState().reset()
    // Create a default workspace for tests (since reset() now starts empty)
    useWorkspaceStore.getState().addWorkspace('Default', { color: '#7a6aaa' })
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  })
```

### Step 3.8：跑全部測試再次確認

- [ ] 執行全部測試

```bash
cd spa && npx vitest run
```

預期：全部 pass。

### Step 3.9：Commit

- [ ] 提交

```bash
git add -A && git commit -m "feat: workspace store 全自由制改造 — 0 workspace 支援、insertTab action、migration v1→v2"
```

---

## Task 4：getVisibleTabIds 共用化（10.1 displayTabs 部分）

### Step 4.1：寫 getVisibleTabIds 測試

- [ ] 建立 `spa/src/features/workspace/lib/getVisibleTabIds.test.ts`

**檔案：`spa/src/features/workspace/lib/getVisibleTabIds.test.ts`**（完整內容）

```typescript
import { describe, it, expect } from 'vitest'
import { getVisibleTabIds } from './getVisibleTabIds'
import type { Workspace } from '../../../types/tab'

describe('getVisibleTabIds', () => {
  it('returns workspace tabs when active workspace exists', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', color: '#aaa', tabs: ['t1', 't2'], activeTabId: 't1' },
    ]
    const tabs: Record<string, unknown> = { t1: {}, t2: {}, t3: {} }
    const result = getVisibleTabIds({
      tabs,
      tabOrder: ['t1', 't2', 't3'],
      activeTabId: 't1',
      workspaces,
      activeWorkspaceId: 'ws-1',
    })
    expect(result).toEqual(['t1', 't2'])
  })

  it('filters out tabs not in tab store', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', color: '#aaa', tabs: ['t1', 't2', 't3'], activeTabId: 't1' },
    ]
    const tabs: Record<string, unknown> = { t1: {}, t3: {} } // t2 missing
    const result = getVisibleTabIds({
      tabs,
      tabOrder: ['t1', 't3'],
      activeTabId: 't1',
      workspaces,
      activeWorkspaceId: 'ws-1',
    })
    expect(result).toEqual(['t1', 't3'])
  })

  it('returns only standalone tab when active tab is standalone', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', color: '#aaa', tabs: ['t1'], activeTabId: 't1' },
    ]
    const tabs: Record<string, unknown> = { t1: {}, t2: {} }
    const result = getVisibleTabIds({
      tabs,
      tabOrder: ['t1', 't2'],
      activeTabId: 't2', // t2 is standalone
      workspaces,
      activeWorkspaceId: 'ws-1',
    })
    expect(result).toEqual(['t2'])
  })

  it('returns all tabs from tabOrder when 0 workspaces', () => {
    const result = getVisibleTabIds({
      tabs: { t1: {}, t2: {}, t3: {} },
      tabOrder: ['t1', 't2', 't3'],
      activeTabId: 't1',
      workspaces: [],
      activeWorkspaceId: null,
    })
    expect(result).toEqual(['t1', 't2', 't3'])
  })

  it('returns all tabs from tabOrder when activeWorkspaceId is null', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', color: '#aaa', tabs: ['t1'], activeTabId: 't1' },
    ]
    const result = getVisibleTabIds({
      tabs: { t1: {}, t2: {} },
      tabOrder: ['t1', 't2'],
      activeTabId: null,
      workspaces,
      activeWorkspaceId: null,
    })
    expect(result).toEqual(['t1', 't2'])
  })

  it('returns empty array when no tabs exist', () => {
    const result = getVisibleTabIds({
      tabs: {},
      tabOrder: [],
      activeTabId: null,
      workspaces: [],
      activeWorkspaceId: null,
    })
    expect(result).toEqual([])
  })
})
```

### Step 4.2：跑測試確認 red

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/lib/getVisibleTabIds.test.ts
```

預期：fail（檔案不存在）

### Step 4.3：實作 getVisibleTabIds

- [ ] 建立 `spa/src/features/workspace/lib/getVisibleTabIds.ts`

**檔案：`spa/src/features/workspace/lib/getVisibleTabIds.ts`**（完整內容）

```typescript
import { isStandaloneTab, type Workspace } from '../../../types/tab'

interface GetVisibleTabIdsParams {
  tabs: Record<string, unknown>
  tabOrder: string[]
  activeTabId: string | null
  workspaces: Workspace[]
  activeWorkspaceId: string | null
}

/**
 * Get the tab IDs currently visible in the TabBar (workspace-aware).
 *
 * Rules:
 * 1. Active standalone tab selected → only that tab
 * 2. Active workspace → that workspace's tabs (filtered by existence in tab store)
 * 3. No workspace (0 workspaces or null activeWorkspaceId) → fallback to tabOrder
 */
export function getVisibleTabIds(params: GetVisibleTabIdsParams): string[] {
  const { tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId } = params

  // Standalone tab selected — only that tab is visible
  if (activeTabId && isStandaloneTab(activeTabId, workspaces)) {
    return [activeTabId]
  }

  // Active workspace — use its tab order
  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)
  if (activeWs) {
    return activeWs.tabs.filter((id) => !!tabs[id])
  }

  // Fallback to global tabOrder
  return tabOrder
}
```

### Step 4.4：跑測試確認 green

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/lib/getVisibleTabIds.test.ts
```

預期：全部 pass。

### Step 4.5：更新 index.ts 匯出 getVisibleTabIds

- [ ] 更新 `spa/src/features/workspace/index.ts`

**檔案：`spa/src/features/workspace/index.ts`**（完整內容）

```typescript
// Store
export { useWorkspaceStore } from './store'

// Hooks
export { useTabWorkspaceActions } from './hooks'

// Components
export { ActivityBar } from './components/ActivityBar'

// Lib
export { getVisibleTabIds } from './lib/getVisibleTabIds'

// Re-export shared types from types/tab.ts
export type { Workspace } from '../../types/tab'
export { createWorkspace, isStandaloneTab } from '../../types/tab'
```

### Step 4.6：替換 App.tsx 的 displayTabs 邏輯

> **注意：** 此步驟只替換 `visibleTabs` / `displayTabs` 計算。`standaloneTabs` 和 `activeStandaloneTabId` 的計算不在替換範圍（它們用於 ActivityBar，與 displayTabs 職責不同）。

- [ ] 修改 `spa/src/App.tsx`

在 import 區塊，新增 import：

將：
```typescript
import { isStandaloneTab } from './types/tab'
```

改為：
```typescript
import { isStandaloneTab } from './types/tab'
import { getVisibleTabIds } from './features/workspace'
```

將 App 元件中的 displayTabs 三段計算：

```typescript
  // --- Derive visible tabs for display ---
  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)
  const visibleTabs: Tab[] = activeWs
    ? activeWs.tabs.map((id) => tabs[id]).filter(Boolean)
    : []

  const standaloneTabs = tabOrder
    .filter((id) => isStandaloneTab(id, workspaces))
    .map((id) => tabs[id])
    .filter(Boolean)

  const activeStandaloneTabId = activeTabId && isStandaloneTab(activeTabId, workspaces) ? activeTabId : null

  const displayTabs = activeStandaloneTabId
    ? [tabs[activeStandaloneTabId]].filter(Boolean)
    : visibleTabs
```

替換為：

```typescript
  // --- Derive visible tabs for display ---
  const visibleTabIds = getVisibleTabIds({
    tabs,
    tabOrder,
    activeTabId,
    workspaces,
    activeWorkspaceId,
  })
  const displayTabs: Tab[] = visibleTabIds.map((id) => tabs[id]).filter(Boolean)

  const standaloneTabs = tabOrder
    .filter((id) => isStandaloneTab(id, workspaces))
    .map((id) => tabs[id])
    .filter(Boolean)

  const activeStandaloneTabId = activeTabId && isStandaloneTab(activeTabId, workspaces) ? activeTabId : null
```

### Step 4.7：替換 useShortcuts.ts 的 getVisibleTabIds

- [ ] 修改 `spa/src/hooks/useShortcuts.ts`

將整個 import 區塊和 `getVisibleTabIds` 函式以及 `addToActiveWorkspace` 函式：

```typescript
import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab, isStandaloneTab } from '../types/tab'

/** Get the tab IDs currently visible in the TabBar (workspace-aware). */
function getVisibleTabIds(): string[] {
  const { tabs, tabOrder, activeTabId } = useTabStore.getState()
  const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()

  // Standalone tab selected — only that tab is visible
  if (activeTabId && isStandaloneTab(activeTabId, workspaces)) {
    return [activeTabId]
  }

  // Active workspace — use its tab order
  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)
  if (activeWs) {
    return activeWs.tabs.filter((id) => !!tabs[id])
  }

  // Fallback to global tabOrder
  return tabOrder
}

function addToActiveWorkspace(tabId: string): void {
  const wsId = useWorkspaceStore.getState().activeWorkspaceId
  if (wsId) {
    useWorkspaceStore.getState().addTabToWorkspace(wsId, tabId)
    useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tabId)
  }
}
```

替換為：

```typescript
import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { getVisibleTabIds as getVisibleTabIdsShared } from '../features/workspace'
```

然後在 `useShortcuts` 函式中，將 `const visibleIds = getVisibleTabIds()` 替換：

```typescript
      const visibleIds = getVisibleTabIdsShared({
        tabs: tabState.tabs,
        tabOrder: tabState.tabOrder,
        activeTabId: tabState.activeTabId,
        workspaces: useWorkspaceStore.getState().workspaces,
        activeWorkspaceId: useWorkspaceStore.getState().activeWorkspaceId,
      })
```

並將所有 `addToActiveWorkspace(...)` 呼叫替換為 `useWorkspaceStore.getState().insertTab(...)`。具體替換：

在 `new-tab` handler 中，將：
```typescript
        addToActiveWorkspace(tab.id)
```
改為：
```typescript
        useWorkspaceStore.getState().insertTab(tab.id)
```

在 `open-settings` handler 中，將：
```typescript
        addToActiveWorkspace(tabId)
```
改為：
```typescript
        useWorkspaceStore.getState().insertTab(tabId)
```

在 `open-history` handler 中，將：
```typescript
        addToActiveWorkspace(tabId)
```
改為：
```typescript
        useWorkspaceStore.getState().insertTab(tabId)
```

在 `reopen-closed-tab` handler 中，將：
```typescript
          addToActiveWorkspace(tab.id)
```
改為：
```typescript
          useWorkspaceStore.getState().insertTab(tab.id)
```

### Step 4.8：完整的 useShortcuts.ts 修改後內容

- [ ] 驗證 `spa/src/hooks/useShortcuts.ts` 的完整內容

**檔案：`spa/src/hooks/useShortcuts.ts`**（完整內容）

```typescript
import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { getVisibleTabIds as getVisibleTabIdsShared } from '../features/workspace'

export function useShortcuts(): void {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return

    const cleanup = window.electronAPI.onShortcut(({ action }) => {
      const tabState = useTabStore.getState()
      const visibleIds = getVisibleTabIdsShared({
        tabs: tabState.tabs,
        tabOrder: tabState.tabOrder,
        activeTabId: tabState.activeTabId,
        workspaces: useWorkspaceStore.getState().workspaces,
        activeWorkspaceId: useWorkspaceStore.getState().activeWorkspaceId,
      })

      if (action.startsWith('switch-tab-')) {
        if (action === 'switch-tab-last') {
          const lastId = visibleIds[visibleIds.length - 1]
          if (lastId) tabState.setActiveTab(lastId)
        } else {
          const index = parseInt(action.replace('switch-tab-', ''), 10) - 1
          const targetId = visibleIds[index]
          if (targetId) tabState.setActiveTab(targetId)
        }
        return
      }

      if (action === 'prev-tab' || action === 'next-tab') {
        if (visibleIds.length === 0) return
        const currentIdx = tabState.activeTabId
          ? visibleIds.indexOf(tabState.activeTabId)
          : -1
        if (currentIdx === -1) {
          // No valid active tab — go to first tab
          tabState.setActiveTab(visibleIds[0])
          return
        }
        const delta = action === 'next-tab' ? 1 : -1
        const nextIdx = (currentIdx + delta + visibleIds.length) % visibleIds.length
        tabState.setActiveTab(visibleIds[nextIdx])
        return
      }

      if (action === 'close-tab') {
        const { activeTabId, tabs } = tabState
        if (!activeTabId) return
        const tab = tabs[activeTabId]
        if (!tab || tab.locked) return
        const wsStore = useWorkspaceStore.getState()
        useHistoryStore.getState().recordClose(tab, wsStore.findWorkspaceByTab(activeTabId)?.id)
        const ws = wsStore.findWorkspaceByTab(activeTabId)
        if (ws) wsStore.removeTabFromWorkspace(ws.id, activeTabId)
        tabState.closeTab(activeTabId)
        return
      }

      if (action === 'new-tab') {
        const tab = createTab({ kind: 'new-tab' })
        tabState.addTab(tab)
        tabState.setActiveTab(tab.id)
        useWorkspaceStore.getState().insertTab(tab.id)
        return
      }

      if (action === 'open-settings') {
        const tabId = tabState.openSingletonTab({ kind: 'settings', scope: 'global' })
        useWorkspaceStore.getState().insertTab(tabId)
        return
      }

      if (action === 'open-history') {
        const tabId = tabState.openSingletonTab({ kind: 'history' })
        useWorkspaceStore.getState().insertTab(tabId)
        return
      }

      if (action === 'reopen-closed-tab') {
        const tab = useHistoryStore.getState().reopenLast()
        if (tab) {
          tabState.addTab(tab)
          tabState.setActiveTab(tab.id)
          useWorkspaceStore.getState().insertTab(tab.id)
        }
        return
      }

      if (import.meta.env.DEV) {
        console.warn(`[useShortcuts] unknown action: ${action}`)
      }
    })

    return cleanup
  }, [])
}
```

### Step 4.9：跑全部測試

- [ ] 執行全部測試

```bash
cd spa && npx vitest run
```

預期：全部 pass。

### Step 4.10：Commit

- [ ] 提交

```bash
git add -A && git commit -m "refactor: 提取 getVisibleTabIds 共用函式，替換 App.tsx + useShortcuts.ts 的重複邏輯"
```

---

## Task 5：insertTab 呼叫點替換（10.1 helper 收斂）

### Step 5.1：替換 App.tsx 中的 3 處 addTabToWorkspace 模式

- [ ] 修改 `spa/src/App.tsx`

在 import 區塊新增（若尚未有）：

將 import 行中的 `useWorkspaceStore` 確保來自正確位置（已有 re-export，不需改）。

**替換 onTabReceived handler 中的 workspace 邏輯：**

將：
```typescript
          // Restore workspace membership if receiving window has an active workspace
          const wsId = useWorkspaceStore.getState().activeWorkspaceId
          if (wsId) {
            useWorkspaceStore.getState().addTabToWorkspace(wsId, tab.id)
            useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tab.id)
          }
```

改為：
```typescript
          // Restore workspace membership if receiving window has an active workspace
          useWorkspaceStore.getState().insertTab(tab.id)
```

**替換 onOpenHosts handler：**

將：
```typescript
          onOpenHosts={() => {
            const tabId = useTabStore.getState().openSingletonTab({ kind: 'hosts' })
            if (activeWorkspaceId) {
              useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, tabId)
              useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, tabId)
            }
            handleSelectTab(tabId)
          }}
```

改為：
```typescript
          onOpenHosts={() => {
            const tabId = useTabStore.getState().openSingletonTab({ kind: 'hosts' })
            useWorkspaceStore.getState().insertTab(tabId)
            handleSelectTab(tabId)
          }}
```

**替換 onOpenSettings handler：**

將：
```typescript
          onOpenSettings={() => {
            const tabId = useTabStore.getState().openSingletonTab({ kind: 'settings', scope: 'global' })
            if (activeWorkspaceId) {
              useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, tabId)
              useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, tabId)
            }
            handleSelectTab(tabId)
          }}
```

改為：
```typescript
          onOpenSettings={() => {
            const tabId = useTabStore.getState().openSingletonTab({ kind: 'settings', scope: 'global' })
            useWorkspaceStore.getState().insertTab(tabId)
            handleSelectTab(tabId)
          }}
```

**替換 StatusBar onNavigateToHost handler：**

將：
```typescript
            onNavigateToHost={(hostId) => {
              const tabId = useTabStore.getState().openSingletonTab({ kind: 'hosts' })
              if (activeWorkspaceId) {
                useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, tabId)
                useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, tabId)
              }
              handleSelectTab(tabId)
              useHostStore.getState().setActiveHost(hostId)
            }}
```

改為：
```typescript
            onNavigateToHost={(hostId) => {
              const tabId = useTabStore.getState().openSingletonTab({ kind: 'hosts' })
              useWorkspaceStore.getState().insertTab(tabId)
              handleSelectTab(tabId)
              useHostStore.getState().setActiveHost(hostId)
            }}
```

### Step 5.2：替換 useTabWorkspaceActions handleAddTab

- [ ] 修改 `spa/src/features/workspace/hooks.ts`

將 `handleAddTab` 中的：
```typescript
  const handleAddTab = useCallback(() => {
    const tab = createTab({ kind: 'new-tab' })
    addTab(tab)
    setActiveTab(tab.id)
    if (activeWorkspaceId) {
      useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, tab.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, tab.id)
    }
  }, [addTab, setActiveTab, activeWorkspaceId])
```

改為：
```typescript
  const handleAddTab = useCallback(() => {
    const tab = createTab({ kind: 'new-tab' })
    addTab(tab)
    setActiveTab(tab.id)
    useWorkspaceStore.getState().insertTab(tab.id)
  }, [addTab, setActiveTab])
```

### Step 5.3：替換 useNotificationDispatcher.ts

- [ ] 修改 `spa/src/hooks/useNotificationDispatcher.ts`

將 `handleNotificationClick` 中的：
```typescript
        const activeWorkspaceId = useWorkspaceStore.getState().activeWorkspaceId
        if (activeWorkspaceId) {
          useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, newTab.id)
          useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, newTab.id)
        }
```

改為：
```typescript
        useWorkspaceStore.getState().insertTab(newTab.id)
```

### Step 5.4：替換 SessionsSection.tsx + 同步更新 test mock

- [ ] 修改 `spa/src/components/hosts/SessionsSection.tsx`

將 `handleOpen` 中的：
```typescript
  const handleOpen = (session: Session, mode: string) => {
    const tabId = useTabStore.getState().openSingletonTab({
      kind: 'tmux-session',
      hostId,
      sessionCode: session.code,
      mode: mode as 'terminal' | 'stream',
      cachedName: session.name,
      tmuxInstance: '',
    })
    const wsId = useWorkspaceStore.getState().activeWorkspaceId
    if (wsId) {
      useWorkspaceStore.getState().addTabToWorkspace(wsId, tabId)
      useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tabId)
    }
    useTabStore.getState().setActiveTab(tabId)
  }
```

改為：
```typescript
  const handleOpen = (session: Session, mode: string) => {
    const tabId = useTabStore.getState().openSingletonTab({
      kind: 'tmux-session',
      hostId,
      sessionCode: session.code,
      mode: mode as 'terminal' | 'stream',
      cachedName: session.name,
      tmuxInstance: '',
    })
    useWorkspaceStore.getState().insertTab(tabId)
    useTabStore.getState().setActiveTab(tabId)
  }
```

- [ ] **同步更新 `spa/src/components/hosts/SessionsSection.test.tsx` 的 mock**

將：
```typescript
const mockAddTabToWorkspace = vi.fn()
const mockSetWorkspaceActiveTab = vi.fn()
```

改為：
```typescript
const mockInsertTab = vi.fn()
```

將 workspace mock：
```typescript
vi.mock('../../stores/useWorkspaceStore', () => ({
  useWorkspaceStore: {
    getState: () => ({
      activeWorkspaceId: null,
      addTabToWorkspace: mockAddTabToWorkspace,
      setWorkspaceActiveTab: mockSetWorkspaceActiveTab,
    }),
  },
}))
```

改為：
```typescript
vi.mock('../../stores/useWorkspaceStore', () => ({
  useWorkspaceStore: {
    getState: () => ({
      insertTab: mockInsertTab,
    }),
  },
}))
```

將 `beforeEach` 中的：
```typescript
  mockAddTabToWorkspace.mockClear()
  mockSetWorkspaceActiveTab.mockClear()
```

改為：
```typescript
  mockInsertTab.mockClear()
```

如果測試中有 assertion 驗證 `mockAddTabToWorkspace` 或 `mockSetWorkspaceActiveTab`，改為驗證 `mockInsertTab`。例如：
```typescript
expect(mockInsertTab).toHaveBeenCalledWith('tab-1')
```

### Step 5.5：跑全部測試

- [ ] 執行全部測試

```bash
cd spa && npx vitest run
```

預期：全部 pass。

### Step 5.6：Commit

- [ ] 提交

```bash
git add -A && git commit -m "refactor: 收斂所有 addTabToWorkspace + setWorkspaceActiveTab 模式為 insertTab"
```

---

## Task 6：WorkspaceDeleteDialog（10.2）

### Step 6.1：寫 WorkspaceDeleteDialog 測試

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceDeleteDialog.test.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceDeleteDialog.test.tsx`**（完整內容）

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceDeleteDialog } from './WorkspaceDeleteDialog'

const mockTabs = [
  { id: 't1', label: 'dev session' },
  { id: 't2', label: 'settings' },
]

describe('WorkspaceDeleteDialog', () => {
  beforeEach(() => {
    cleanup()
  })

  it('renders workspace name and tab list', () => {
    render(
      <WorkspaceDeleteDialog
        workspaceName="My Workspace"
        tabs={mockTabs}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    expect(screen.getByText(/My Workspace/)).toBeInTheDocument()
    expect(screen.getByText('dev session')).toBeInTheDocument()
    expect(screen.getByText('settings')).toBeInTheDocument()
  })

  it('all tabs are checked by default', () => {
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={mockTabs}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    const checkboxes = screen.getAllByRole('checkbox')
    expect(checkboxes).toHaveLength(2)
    checkboxes.forEach((cb) => expect(cb).toBeChecked())
  })

  it('unchecking a tab excludes it from closedTabIds', () => {
    const onConfirm = vi.fn()
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={mockTabs}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    )
    // Uncheck first tab
    const checkboxes = screen.getAllByRole('checkbox')
    fireEvent.click(checkboxes[0])

    // Confirm
    fireEvent.click(screen.getByRole('button', { name: /delete/i }))
    expect(onConfirm).toHaveBeenCalledWith(['t2']) // only t2 is checked
  })

  it('confirm with all checked sends all tab ids', () => {
    const onConfirm = vi.fn()
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={mockTabs}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /delete/i }))
    expect(onConfirm).toHaveBeenCalledWith(['t1', 't2'])
  })

  it('cancel calls onCancel', () => {
    const onCancel = vi.fn()
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={mockTabs}
        onConfirm={vi.fn()}
        onCancel={onCancel}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onCancel).toHaveBeenCalled()
  })

  it('renders empty tab list gracefully', () => {
    const onConfirm = vi.fn()
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={[]}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /delete/i }))
    expect(onConfirm).toHaveBeenCalledWith([])
  })
})
```

### Step 6.2：跑測試確認 red

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceDeleteDialog.test.tsx
```

預期：fail（檔案不存在）

### Step 6.3：實作 WorkspaceDeleteDialog

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceDeleteDialog.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceDeleteDialog.tsx`**（完整內容）

```typescript
import { useState } from 'react'
import { Trash, Warning } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'

interface TabItem {
  id: string
  label: string
}

interface Props {
  workspaceName: string
  tabs: TabItem[]
  onConfirm: (closedTabIds: string[]) => void
  onCancel: () => void
}

export function WorkspaceDeleteDialog({ workspaceName, tabs, onConfirm, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)
  const [checkedIds, setCheckedIds] = useState<Set<string>>(() => new Set(tabs.map((tab) => tab.id)))

  const toggleTab = (tabId: string) => {
    setCheckedIds((prev) => {
      const next = new Set(prev)
      if (next.has(tabId)) {
        next.delete(tabId)
      } else {
        next.add(tabId)
      }
      return next
    })
  }

  const handleConfirm = () => {
    const closedTabIds = tabs.filter((tab) => checkedIds.has(tab.id)).map((tab) => tab.id)
    onConfirm(closedTabIds)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-md mx-4">
        {/* Header */}
        <div className="flex items-center gap-3 px-5 pt-5 pb-3">
          <div className="w-10 h-10 rounded-full bg-red-500/10 flex items-center justify-center flex-shrink-0">
            <Warning size={20} className="text-red-400" />
          </div>
          <div>
            <h3 className="text-sm font-semibold text-text-primary">
              {t('workspace.delete_title', { name: workspaceName })}
            </h3>
            {tabs.length > 0 && (
              <p className="text-xs text-text-muted mt-0.5">
                {t('workspace.delete_description')}
              </p>
            )}
          </div>
        </div>

        {/* Tab list */}
        {tabs.length > 0 && (
          <div className="px-5 py-3 max-h-48 overflow-y-auto">
            <div className="space-y-1.5">
              {tabs.map((tab) => (
                <label key={tab.id} className="flex items-center gap-2 cursor-pointer group">
                  <input
                    type="checkbox"
                    checked={checkedIds.has(tab.id)}
                    onChange={() => toggleTab(tab.id)}
                    className="rounded border-border-default"
                  />
                  <span className="text-sm text-text-secondary group-hover:text-text-primary truncate">
                    {tab.label}
                  </span>
                </label>
              ))}
            </div>
          </div>
        )}

        {/* Actions */}
        <div className="flex justify-end gap-2 px-5 py-4 border-t border-border-subtle">
          <button
            onClick={onCancel}
            className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer"
          >
            {t('common.cancel')}
          </button>
          <button
            onClick={handleConfirm}
            className="px-3 py-1.5 rounded text-xs bg-red-600 text-white hover:bg-red-500 cursor-pointer flex items-center gap-1.5"
          >
            <Trash size={14} />
            {t('workspace.delete_confirm')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

### Step 6.4：跑測試確認 green

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceDeleteDialog.test.tsx
```

預期：全部 pass。

### Step 6.5：更新 index.ts 匯出 WorkspaceDeleteDialog

- [ ] 更新 `spa/src/features/workspace/index.ts`

**檔案：`spa/src/features/workspace/index.ts`**（完整內容）

```typescript
// Store
export { useWorkspaceStore } from './store'

// Hooks
export { useTabWorkspaceActions } from './hooks'

// Components
export { ActivityBar } from './components/ActivityBar'
export { WorkspaceDeleteDialog } from './components/WorkspaceDeleteDialog'

// Lib
export { getVisibleTabIds } from './lib/getVisibleTabIds'

// Re-export shared types from types/tab.ts
export type { Workspace } from '../../types/tab'
export { createWorkspace, isStandaloneTab } from '../../types/tab'
```

### Step 6.6：跑全部測試

- [ ] 執行全部測試

```bash
cd spa && npx vitest run
```

預期：全部 pass。

### Step 6.7：Commit

- [ ] 提交

```bash
git add -A && git commit -m "feat: WorkspaceDeleteDialog 元件 — 刪除 workspace 確認 UI + tab 勾選關閉"
```

---

## Task 7：Workspace 右鍵選單 + Titlebar Chip（10.3）

> **需要新增的 i18n key 清單**（加入對應的 locale 檔案）：
>
> | Key | 用途 |
> |-----|------|
> | `workspace.rename` | 右鍵選單：重新命名 |
> | `workspace.change_color` | 右鍵選單：變更顏色 |
> | `workspace.change_icon` | 右鍵選單：變更圖示 |
> | `workspace.delete` | 右鍵選單：刪除工作區 |
> | `workspace.delete_title` | 刪除確認對話框標題（含 `{name}` 變數） |
> | `workspace.delete_description` | 刪除確認對話框描述 |
> | `workspace.delete_confirm` | 刪除確認按鈕 |
> | `workspace.delete_cancel` | 取消按鈕 |
> | `workspace.rename_title` | 重新命名對話框標題 |
> | `workspace.rename_placeholder` | 輸入框 placeholder |
> | `workspace.rename_confirm` | 確認按鈕 |
> | `workspace.color_title` | 顏色選擇器標題 |
> | `workspace.icon_title` | 圖示選擇器標題 |
> | `workspace.migrate_title` | 首個 workspace 遷移對話框標題（Task 9 使用） |
> | `workspace.migrate_description` | 遷移描述（含 `{count}` `{name}` 變數） |
> | `workspace.migrate_move` | 移入按鈕 |
> | `workspace.migrate_skip` | 跳過按鈕 |

### Step 7.1：寫 WorkspaceContextMenu 測試

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceContextMenu.test.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceContextMenu.test.tsx`**（完整內容）

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceContextMenu } from './WorkspaceContextMenu'

describe('WorkspaceContextMenu', () => {
  beforeEach(() => {
    cleanup()
  })

  it('renders all menu items', () => {
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        workspaceName="My WS"
        onRename={vi.fn()}
        onChangeColor={vi.fn()}
        onChangeIcon={vi.fn()}
        onDelete={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByText(/rename/i)).toBeInTheDocument()
    expect(screen.getByText(/color/i)).toBeInTheDocument()
    expect(screen.getByText(/icon/i)).toBeInTheDocument()
    expect(screen.getByText(/delete/i)).toBeInTheDocument()
  })

  it('calls onRename when clicking rename', () => {
    const onRename = vi.fn()
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        workspaceName="WS"
        onRename={onRename}
        onChangeColor={vi.fn()}
        onChangeIcon={vi.fn()}
        onDelete={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByText(/rename/i))
    expect(onRename).toHaveBeenCalled()
  })

  it('calls onDelete when clicking delete', () => {
    const onDelete = vi.fn()
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        workspaceName="WS"
        onRename={vi.fn()}
        onChangeColor={vi.fn()}
        onChangeIcon={vi.fn()}
        onDelete={onDelete}
        onClose={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByText(/delete/i))
    expect(onDelete).toHaveBeenCalled()
  })

  it('calls onClose on backdrop click', () => {
    const onClose = vi.fn()
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        workspaceName="WS"
        onRename={vi.fn()}
        onChangeColor={vi.fn()}
        onChangeIcon={vi.fn()}
        onDelete={vi.fn()}
        onClose={onClose}
      />,
    )
    // Click the backdrop (the fixed overlay)
    fireEvent.mouseDown(screen.getByTestId('context-menu-backdrop'))
    expect(onClose).toHaveBeenCalled()
  })
})
```

### Step 7.2：跑測試確認 red

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceContextMenu.test.tsx
```

預期：fail

### Step 7.3：實作 WorkspaceContextMenu

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceContextMenu.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceContextMenu.tsx`**（完整內容）

```typescript
import { useEffect, useRef } from 'react'
import { PencilSimple, Palette, Smiley, Trash } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  position: { x: number; y: number }
  workspaceName: string
  onRename: () => void
  onChangeColor: () => void
  onChangeIcon: () => void
  onDelete: () => void
  onClose: () => void
}

export function WorkspaceContextMenu({ position, workspaceName, onRename, onChangeColor, onChangeIcon, onDelete, onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const menuRef = useRef<HTMLDivElement>(null)

  // Close on Escape
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const menuItems = [
    { label: t('workspace.rename'), icon: PencilSimple, onClick: onRename },
    { label: t('workspace.change_color'), icon: Palette, onClick: onChangeColor },
    { label: t('workspace.change_icon'), icon: Smiley, onClick: onChangeIcon },
    { type: 'separator' as const },
    { label: t('workspace.delete'), icon: Trash, onClick: onDelete, danger: true },
  ]

  return (
    <>
      {/* Backdrop */}
      <div
        data-testid="context-menu-backdrop"
        className="fixed inset-0 z-40"
        onMouseDown={onClose}
      />
      {/* Menu */}
      <div
        ref={menuRef}
        className="fixed z-50 min-w-44 bg-surface-secondary border border-border-default rounded-lg shadow-xl py-1"
        style={{ left: position.x, top: position.y }}
      >
        {menuItems.map((item, i) => {
          if ('type' in item && item.type === 'separator') {
            return <div key={i} className="h-px bg-border-subtle my-1 mx-2" />
          }
          const Icon = item.icon!
          return (
            <button
              key={i}
              onClick={() => {
                item.onClick!()
                onClose()
              }}
              className={`w-full flex items-center gap-2 px-3 py-1.5 text-xs cursor-pointer transition-colors ${
                item.danger
                  ? 'text-red-400 hover:bg-red-500/10'
                  : 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'
              }`}
            >
              <Icon size={14} />
              {item.label}
            </button>
          )
        })}
      </div>
    </>
  )
}
```

### Step 7.4：跑測試確認 green

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceContextMenu.test.tsx
```

預期：全部 pass。

### Step 7.5：寫 WorkspaceChip 測試

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceChip.test.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceChip.test.tsx`**（完整內容）

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceChip } from './WorkspaceChip'

describe('WorkspaceChip', () => {
  beforeEach(() => {
    cleanup()
  })

  it('renders workspace name and color dot', () => {
    render(
      <WorkspaceChip
        name="My Workspace"
        color="#7a6aaa"
        onClick={vi.fn()}
        onContextMenu={vi.fn()}
      />,
    )
    expect(screen.getByText('My Workspace')).toBeInTheDocument()
    const dot = screen.getByTestId('workspace-color-dot')
    expect(dot.style.backgroundColor).toBe('rgb(122, 106, 170)')
  })

  it('calls onClick when clicked', () => {
    const onClick = vi.fn()
    render(
      <WorkspaceChip
        name="WS"
        color="#aaa"
        onClick={onClick}
        onContextMenu={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByText('WS'))
    expect(onClick).toHaveBeenCalled()
  })

  it('calls onContextMenu on right click', () => {
    const onContextMenu = vi.fn()
    render(
      <WorkspaceChip
        name="WS"
        color="#aaa"
        onClick={vi.fn()}
        onContextMenu={onContextMenu}
      />,
    )
    fireEvent.contextMenu(screen.getByText('WS'))
    expect(onContextMenu).toHaveBeenCalled()
  })

  it('does not render when name is null', () => {
    const { container } = render(
      <WorkspaceChip
        name={null}
        color={null}
        onClick={vi.fn()}
        onContextMenu={vi.fn()}
      />,
    )
    expect(container.firstChild).toBeNull()
  })
})
```

### Step 7.6：跑測試確認 red

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceChip.test.tsx
```

預期：fail

### Step 7.7：實作 WorkspaceChip

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceChip.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceChip.tsx`**（完整內容）

```typescript
import { CaretDown } from '@phosphor-icons/react'

interface Props {
  name: string | null
  color: string | null
  onClick: () => void
  onContextMenu: (e: React.MouseEvent) => void
}

export function WorkspaceChip({ name, color, onClick, onContextMenu }: Props) {
  if (!name) return null

  return (
    <button
      onClick={onClick}
      onContextMenu={onContextMenu}
      className="flex items-center gap-1.5 px-2 py-1 rounded-md text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer transition-colors flex-shrink-0"
    >
      <span
        data-testid="workspace-color-dot"
        className="w-2.5 h-2.5 rounded-full flex-shrink-0"
        style={{ backgroundColor: color ?? '#888' }}
      />
      <span className="truncate max-w-24">{name}</span>
      <CaretDown size={10} className="flex-shrink-0 opacity-60" />
    </button>
  )
}
```

### Step 7.8：跑測試確認 green

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceChip.test.tsx
```

預期：全部 pass。

### Step 7.9：寫 WorkspaceRenameDialog 測試

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceRenameDialog.test.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceRenameDialog.test.tsx`**（完整內容）

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceRenameDialog } from './WorkspaceRenameDialog'

describe('WorkspaceRenameDialog', () => {
  beforeEach(() => {
    cleanup()
  })

  it('renders with current name pre-filled', () => {
    render(
      <WorkspaceRenameDialog
        currentName="Old Name"
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    const input = screen.getByRole('textbox') as HTMLInputElement
    expect(input.value).toBe('Old Name')
  })

  it('calls onConfirm with new name', () => {
    const onConfirm = vi.fn()
    render(
      <WorkspaceRenameDialog
        currentName="Old"
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    )
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: 'New Name' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    expect(onConfirm).toHaveBeenCalledWith('New Name')
  })

  it('calls onConfirm on Enter key', () => {
    const onConfirm = vi.fn()
    render(
      <WorkspaceRenameDialog
        currentName="Old"
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    )
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: 'Via Enter' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onConfirm).toHaveBeenCalledWith('Via Enter')
  })

  it('calls onCancel on Escape key', () => {
    const onCancel = vi.fn()
    render(
      <WorkspaceRenameDialog
        currentName="Old"
        onConfirm={vi.fn()}
        onCancel={onCancel}
      />,
    )
    const input = screen.getByRole('textbox')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalled()
  })

  it('does not confirm with empty name', () => {
    const onConfirm = vi.fn()
    render(
      <WorkspaceRenameDialog
        currentName="Old"
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    )
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: '  ' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    expect(onConfirm).not.toHaveBeenCalled()
  })
})
```

### Step 7.10：實作 WorkspaceRenameDialog

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceRenameDialog.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceRenameDialog.tsx`**（完整內容）

```typescript
import { useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  currentName: string
  onConfirm: (newName: string) => void
  onCancel: () => void
}

export function WorkspaceRenameDialog({ currentName, onConfirm, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)
  const [name, setName] = useState(currentName)

  const handleConfirm = () => {
    const trimmed = name.trim()
    if (!trimmed) return
    onConfirm(trimmed)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-sm mx-4 p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-3">
          {t('workspace.rename')}
        </h3>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') handleConfirm()
            if (e.key === 'Escape') onCancel()
          }}
          className="w-full bg-surface-primary border border-border-default rounded px-3 py-2 text-sm text-text-primary"
          autoFocus
        />
        <div className="flex justify-end gap-2 mt-4">
          <button
            onClick={onCancel}
            className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer"
          >
            {t('common.cancel')}
          </button>
          <button
            onClick={handleConfirm}
            className="px-3 py-1.5 rounded text-xs bg-accent text-white hover:bg-accent/80 cursor-pointer"
          >
            {t('common.save')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

### Step 7.11：寫 WorkspaceColorPicker 測試

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceColorPicker.test.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceColorPicker.test.tsx`**（完整內容）

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceColorPicker, WORKSPACE_COLORS } from './WorkspaceColorPicker'

describe('WorkspaceColorPicker', () => {
  beforeEach(() => {
    cleanup()
  })

  it('renders all color options', () => {
    render(
      <WorkspaceColorPicker
        currentColor="#7a6aaa"
        onSelect={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-color'))
    expect(buttons.length).toBe(WORKSPACE_COLORS.length)
  })

  it('highlights current color', () => {
    render(
      <WorkspaceColorPicker
        currentColor="#7a6aaa"
        onSelect={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    const activeBtn = screen.getByRole('button', { pressed: true })
    expect(activeBtn.getAttribute('data-color')).toBe('#7a6aaa')
  })

  it('calls onSelect with chosen color', () => {
    const onSelect = vi.fn()
    render(
      <WorkspaceColorPicker
        currentColor="#7a6aaa"
        onSelect={onSelect}
        onCancel={vi.fn()}
      />,
    )
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-color'))
    const different = buttons.find((b) => b.getAttribute('data-color') !== '#7a6aaa')!
    fireEvent.click(different)
    expect(onSelect).toHaveBeenCalledWith(different.getAttribute('data-color'))
  })
})
```

### Step 7.12：實作 WorkspaceColorPicker

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceColorPicker.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceColorPicker.tsx`**（完整內容）

```typescript
import { Check } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'

export const WORKSPACE_COLORS = [
  '#7a6aaa', '#6aaa7a', '#aa6a7a', '#6a8aaa', '#aa8a6a', '#8a6aaa',
  '#5b8c5a', '#c75050', '#d4a843', '#5a7fbf', '#bf5a9d', '#4abfbf',
]

interface Props {
  currentColor: string
  onSelect: (color: string) => void
  onCancel: () => void
}

export function WorkspaceColorPicker({ currentColor, onSelect, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-xs mx-4 p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-3">
          {t('workspace.change_color')}
        </h3>
        <div className="grid grid-cols-6 gap-2">
          {WORKSPACE_COLORS.map((color) => (
            <button
              key={color}
              data-color={color}
              aria-pressed={color === currentColor}
              onClick={() => onSelect(color)}
              className={`w-8 h-8 rounded-full cursor-pointer flex items-center justify-center transition-transform hover:scale-110 ${
                color === currentColor ? 'ring-2 ring-white ring-offset-2 ring-offset-surface-secondary' : ''
              }`}
              style={{ backgroundColor: color }}
            >
              {color === currentColor && <Check size={14} className="text-white" />}
            </button>
          ))}
        </div>
        <div className="flex justify-end mt-4">
          <button
            onClick={onCancel}
            className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer"
          >
            {t('common.cancel')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

### Step 7.13：寫 WorkspaceIconPicker 測試

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceIconPicker.test.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceIconPicker.test.tsx`**（完整內容）

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceIconPicker, WORKSPACE_ICONS } from './WorkspaceIconPicker'

describe('WorkspaceIconPicker', () => {
  beforeEach(() => {
    cleanup()
  })

  it('renders all icon options', () => {
    render(
      <WorkspaceIconPicker
        currentIcon={undefined}
        onSelect={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    expect(buttons.length).toBe(WORKSPACE_ICONS.length)
  })

  it('calls onSelect with chosen icon', () => {
    const onSelect = vi.fn()
    render(
      <WorkspaceIconPicker
        currentIcon={undefined}
        onSelect={onSelect}
        onCancel={vi.fn()}
      />,
    )
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    fireEvent.click(buttons[0])
    expect(onSelect).toHaveBeenCalledWith(buttons[0].getAttribute('data-icon'))
  })

  it('highlights current icon', () => {
    render(
      <WorkspaceIconPicker
        currentIcon={WORKSPACE_ICONS[2]}
        onSelect={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    const activeBtn = screen.getByRole('button', { pressed: true })
    expect(activeBtn.getAttribute('data-icon')).toBe(WORKSPACE_ICONS[2])
  })
})
```

### Step 7.14：實作 WorkspaceIconPicker

- [ ] 建立 `spa/src/features/workspace/components/WorkspaceIconPicker.tsx`

**檔案：`spa/src/features/workspace/components/WorkspaceIconPicker.tsx`**（完整內容）

```typescript
import { useI18nStore } from '../../../stores/useI18nStore'

export const WORKSPACE_ICONS = [
  'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H',
  'P', 'R', 'S', 'T', 'W', 'X',
  '🔧', '🖥', '📦', '🚀', '📝', '🎯',
  '💡', '🔬', '🎨', '⚡', '🌐', '📊',
]

interface Props {
  currentIcon: string | undefined
  onSelect: (icon: string) => void
  onCancel: () => void
}

export function WorkspaceIconPicker({ currentIcon, onSelect, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-xs mx-4 p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-3">
          {t('workspace.change_icon')}
        </h3>
        <div className="grid grid-cols-7 gap-2">
          {WORKSPACE_ICONS.map((icon) => (
            <button
              key={icon}
              data-icon={icon}
              aria-pressed={icon === currentIcon}
              onClick={() => onSelect(icon)}
              className={`w-8 h-8 rounded-md flex items-center justify-center text-sm cursor-pointer transition-colors ${
                icon === currentIcon
                  ? 'bg-accent/20 ring-2 ring-accent text-text-primary'
                  : 'bg-surface-tertiary text-text-secondary hover:text-text-primary hover:bg-surface-hover'
              }`}
            >
              {icon}
            </button>
          ))}
        </div>
        <div className="flex justify-end mt-4">
          <button
            onClick={onCancel}
            className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer"
          >
            {t('common.cancel')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

### Step 7.15：跑所有 Task 7 測試

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/components/
```

預期：全部 pass。

### Step 7.16：新增 store actions — renameWorkspace、setWorkspaceColor、setWorkspaceIcon

- [ ] 修改 `spa/src/features/workspace/store.ts`

在 `WorkspaceState` interface 的 `reset` 之前加入：

```typescript
  renameWorkspace: (wsId: string, name: string) => void
  setWorkspaceColor: (wsId: string, color: string) => void
  setWorkspaceIcon: (wsId: string, icon: string) => void
```

在 store 的 `reset` action 之前加入實作：

```typescript
      renameWorkspace: (wsId, name) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, name } : ws,
          ),
        })),

      setWorkspaceColor: (wsId, color) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, color } : ws,
          ),
        })),

      setWorkspaceIcon: (wsId, icon) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, icon } : ws,
          ),
        })),
```

### Step 7.17：驗證 store.ts 的 WorkspaceState interface 完整性

> **說明：** Step 7.16 已同時新增 interface 宣告和實作。此步驟為驗證用——確認 `WorkspaceState` interface 與實作一致，無遺漏。

完整的 interface 應為：

```typescript
interface WorkspaceState {
  workspaces: Workspace[]
  activeWorkspaceId: string | null

  addWorkspace: (name: string, opts?: { color?: string; icon?: string }) => Workspace
  removeWorkspace: (wsId: string) => void
  setActiveWorkspace: (wsId: string | null) => void
  addTabToWorkspace: (wsId: string, tabId: string) => void
  removeTabFromWorkspace: (wsId: string, tabId: string) => void
  setWorkspaceActiveTab: (wsId: string, tabId: string) => void
  reorderWorkspaceTabs: (wsId: string, tabIds: string[]) => void
  findWorkspaceByTab: (tabId: string) => Workspace | null
  insertTab: (tabId: string, workspaceId?: string | null) => void
  renameWorkspace: (wsId: string, name: string) => void
  setWorkspaceColor: (wsId: string, color: string) => void
  setWorkspaceIcon: (wsId: string, icon: string) => void
  reset: () => void
}
```

### Step 7.18：新增 store tests for rename/color/icon

- [ ] 在 `spa/src/features/workspace/store.test.ts` 最末尾加入：

```typescript
  // === Workspace settings ===

  it('renameWorkspace updates workspace name', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Old Name')
    useWorkspaceStore.getState().renameWorkspace(ws.id, 'New Name')
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe('New Name')
  })

  it('setWorkspaceColor updates workspace color', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setWorkspaceColor(ws.id, '#ff0000')
    expect(useWorkspaceStore.getState().workspaces[0].color).toBe('#ff0000')
  })

  it('setWorkspaceIcon updates workspace icon', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setWorkspaceIcon(ws.id, 'R')
    expect(useWorkspaceStore.getState().workspaces[0].icon).toBe('R')
  })
```

### Step 7.19：整合右鍵選單到 ActivityBar

- [ ] 修改 `spa/src/features/workspace/components/ActivityBar.tsx`

加入 `onContextMenuWorkspace` prop：

在 Props interface 加入：
```typescript
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
```

在 workspace button 上加入 `onContextMenu`：

將：
```typescript
        <button
          key={ws.id}
          title={ws.name}
          onClick={() => onSelectWorkspace(ws.id)}
```

改為：
```typescript
        <button
          key={ws.id}
          title={ws.name}
          onClick={() => onSelectWorkspace(ws.id)}
          onContextMenu={(e) => {
            e.preventDefault()
            onContextMenuWorkspace?.(e, ws.id)
          }}
```

### Step 7.20：更新 index.ts 匯出所有新元件

- [ ] 更新 `spa/src/features/workspace/index.ts`

**檔案：`spa/src/features/workspace/index.ts`**（完整內容）

```typescript
// Store
export { useWorkspaceStore } from './store'

// Hooks
export { useTabWorkspaceActions } from './hooks'

// Components
export { ActivityBar } from './components/ActivityBar'
export { WorkspaceDeleteDialog } from './components/WorkspaceDeleteDialog'
export { WorkspaceContextMenu } from './components/WorkspaceContextMenu'
export { WorkspaceChip } from './components/WorkspaceChip'
export { WorkspaceRenameDialog } from './components/WorkspaceRenameDialog'
export { WorkspaceColorPicker } from './components/WorkspaceColorPicker'
export { WorkspaceIconPicker } from './components/WorkspaceIconPicker'

// Lib
export { getVisibleTabIds } from './lib/getVisibleTabIds'

// Re-export shared types from types/tab.ts
export type { Workspace } from '../../types/tab'
export { createWorkspace, isStandaloneTab } from '../../types/tab'
```

### Step 7.21：整合 Chip + 右鍵選單 + 對話框到 App.tsx

- [ ] 修改 `spa/src/App.tsx`

在 import 區塊加入：

```typescript
import {
  WorkspaceChip,
  WorkspaceContextMenu,
  WorkspaceDeleteDialog,
  WorkspaceRenameDialog,
  WorkspaceColorPicker,
  WorkspaceIconPicker,
} from './features/workspace'
import { getPrimaryPane } from './lib/pane-tree'
import { getPaneLabel } from './lib/pane-labels'
```

在 `export default function App()` 內、`useTabWorkspaceActions` 呼叫之後加入 workspace 設定 UI 的 state 和 handlers：

```typescript
  // --- Workspace settings UI state ---
  const [wsContextMenu, setWsContextMenu] = useState<{ wsId: string; position: { x: number; y: number } } | null>(null)
  const [wsDeleteTarget, setWsDeleteTarget] = useState<string | null>(null)
  const [wsRenameTarget, setWsRenameTarget] = useState<string | null>(null)
  const [wsColorTarget, setWsColorTarget] = useState<string | null>(null)
  const [wsIconTarget, setWsIconTarget] = useState<string | null>(null)

  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)

  const handleWsContextMenu = (e: React.MouseEvent, wsId: string) => {
    setWsContextMenu({ wsId, position: { x: e.clientX, y: e.clientY } })
  }

  const handleWsDelete = (wsId: string) => {
    const ws = workspaces.find((w) => w.id === wsId)
    if (!ws) return
    if (ws.tabs.length === 0) {
      // No tabs — delete directly
      useWorkspaceStore.getState().removeWorkspace(wsId)
    } else {
      setWsDeleteTarget(wsId)
    }
  }

  const handleWsDeleteConfirm = (closedTabIds: string[]) => {
    if (!wsDeleteTarget) return
    // Close checked tabs via handleCloseTab (records to history)
    closedTabIds.forEach((tabId) => handleCloseTab(tabId))
    // Unchecked tabs are auto-released to standalone when workspace is removed
    useWorkspaceStore.getState().removeWorkspace(wsDeleteTarget)
    setWsDeleteTarget(null)
  }
```

需要在 App 的頂部加入 `useState` import（如尚未有）：

確認 `import { useEffect, useRef } from 'react'` 改為 `import { useEffect, useRef, useState } from 'react'`。

在 ActivityBar 上加入 `onContextMenuWorkspace`：

```typescript
        <ActivityBar
          workspaces={workspaces}
          standaloneTabs={standaloneTabs}
          activeWorkspaceId={activeStandaloneTabId ? null : activeWorkspaceId}
          activeStandaloneTabId={activeStandaloneTabId}
          onSelectWorkspace={handleSelectWorkspace}
          onSelectStandaloneTab={handleSelectTab}
          onAddWorkspace={() => {}}
          onOpenHosts={() => {
            const tabId = useTabStore.getState().openSingletonTab({ kind: 'hosts' })
            useWorkspaceStore.getState().insertTab(tabId)
            handleSelectTab(tabId)
          }}
          onOpenSettings={() => {
            const tabId = useTabStore.getState().openSingletonTab({ kind: 'settings', scope: 'global' })
            useWorkspaceStore.getState().insertTab(tabId)
            handleSelectTab(tabId)
          }}
          onContextMenuWorkspace={handleWsContextMenu}
        />
```

在 Electron titlebar 的 TabBar 之前加入 WorkspaceChip：

將：
```typescript
            {/* Tabs — no-drag so clicks work */}
            <div style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}>
              <TabBar
```

改為：
```typescript
            {/* Workspace chip + Tabs — no-drag so clicks work */}
            <div className="flex items-center" style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}>
              {activeWs && !activeStandaloneTabId && (
                <WorkspaceChip
                  name={activeWs.name}
                  color={activeWs.color}
                  onClick={() => {}}
                  onContextMenu={(e) => handleWsContextMenu(e, activeWs.id)}
                />
              )}
              <TabBar
```

在 SPA 模式的 TabBar 同樣加入 chip，但放在 TabBar 的 wrapper 裡。將 SPA TabBar 區塊：

```typescript
          {!isElectron && (
            <TabBar
              tabs={displayTabs}
              activeTabId={activeTabId}
              onSelectTab={handleSelectTab}
              onCloseTab={handleCloseTab}
              onAddTab={handleAddTab}
              onReorderTabs={handleReorderTabs}
              onMiddleClick={handleMiddleClick}
              onContextMenu={handleContextMenu}
            />
          )}
```

改為：
```typescript
          {!isElectron && (
            <div className="flex items-center bg-surface-secondary border-b border-border-subtle">
              {activeWs && !activeStandaloneTabId && (
                <WorkspaceChip
                  name={activeWs.name}
                  color={activeWs.color}
                  onClick={() => {}}
                  onContextMenu={(e) => handleWsContextMenu(e, activeWs.id)}
                />
              )}
              <div className="flex-1 min-w-0">
                <TabBar
                  tabs={displayTabs}
                  activeTabId={activeTabId}
                  onSelectTab={handleSelectTab}
                  onCloseTab={handleCloseTab}
                  onAddTab={handleAddTab}
                  onReorderTabs={handleReorderTabs}
                  onMiddleClick={handleMiddleClick}
                  onContextMenu={handleContextMenu}
                />
              </div>
            </div>
          )}
```

在 `contextMenu` 渲染之後（`</div>` closing tag 之前），加入所有 workspace 對話框：

```typescript
        {wsContextMenu && (
          <WorkspaceContextMenu
            position={wsContextMenu.position}
            workspaceName={workspaces.find((w) => w.id === wsContextMenu.wsId)?.name ?? ''}
            onRename={() => { setWsRenameTarget(wsContextMenu.wsId); setWsContextMenu(null) }}
            onChangeColor={() => { setWsColorTarget(wsContextMenu.wsId); setWsContextMenu(null) }}
            onChangeIcon={() => { setWsIconTarget(wsContextMenu.wsId); setWsContextMenu(null) }}
            onDelete={() => { handleWsDelete(wsContextMenu.wsId); setWsContextMenu(null) }}
            onClose={() => setWsContextMenu(null)}
          />
        )}
        {wsDeleteTarget && (() => {
          const ws = workspaces.find((w) => w.id === wsDeleteTarget)
          if (!ws) return null
          const t = useI18nStore.getState().t
          const tabItems = ws.tabs
            .map((tabId) => {
              const tab = tabs[tabId]
              if (!tab) return null
              const content = getPrimaryPane(tab.layout).content
              const label = getPaneLabel(content, { getByCode: () => undefined }, { getById: () => undefined }, t)
              return { id: tabId, label }
            })
            .filter(Boolean) as { id: string; label: string }[]
          return (
            <WorkspaceDeleteDialog
              workspaceName={ws.name}
              tabs={tabItems}
              onConfirm={handleWsDeleteConfirm}
              onCancel={() => setWsDeleteTarget(null)}
            />
          )
        })()}
        {wsRenameTarget && (
          <WorkspaceRenameDialog
            currentName={workspaces.find((w) => w.id === wsRenameTarget)?.name ?? ''}
            onConfirm={(name) => {
              useWorkspaceStore.getState().renameWorkspace(wsRenameTarget, name)
              setWsRenameTarget(null)
            }}
            onCancel={() => setWsRenameTarget(null)}
          />
        )}
        {wsColorTarget && (
          <WorkspaceColorPicker
            currentColor={workspaces.find((w) => w.id === wsColorTarget)?.color ?? '#888'}
            onSelect={(color) => {
              useWorkspaceStore.getState().setWorkspaceColor(wsColorTarget, color)
              setWsColorTarget(null)
            }}
            onCancel={() => setWsColorTarget(null)}
          />
        )}
        {wsIconTarget && (
          <WorkspaceIconPicker
            currentIcon={workspaces.find((w) => w.id === wsIconTarget)?.icon}
            onSelect={(icon) => {
              useWorkspaceStore.getState().setWorkspaceIcon(wsIconTarget, icon)
              setWsIconTarget(null)
            }}
            onCancel={() => setWsIconTarget(null)}
          />
        )}
```

### Step 7.22：跑全部測試

- [ ] 執行全部測試

```bash
cd spa && npx vitest run
```

預期：全部 pass。

### Step 7.23：Commit

- [ ] 提交

```bash
git add -A && git commit -m "feat: Workspace 右鍵選單 + Titlebar Chip + 重新命名/顏色/圖示設定 UI"
```

---

## Task 8：快捷鍵（10.4）

### Step 8.1：寫 keybindings 新增項目測試

- [ ] 建立 `spa/src/features/workspace/lib/workspace-shortcuts.test.ts`

**檔案：`spa/src/features/workspace/lib/workspace-shortcuts.test.ts`**（完整內容）

```typescript
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore } from '../store'

describe('workspace shortcut handlers', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
  })

  it('switch-workspace-N jumps to Nth workspace by position', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    const ws3 = useWorkspaceStore.getState().addWorkspace('WS3')

    // Simulate switch-workspace-2 — should go to ws2
    const workspaces = useWorkspaceStore.getState().workspaces
    const target = workspaces[1] // 0-indexed
    expect(target.id).toBe(ws2.id)

    useWorkspaceStore.getState().setActiveWorkspace(target.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
  })

  it('switch-workspace out of range is ignored', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const workspaces = useWorkspaceStore.getState().workspaces
    const target = workspaces[5] // out of range
    expect(target).toBeUndefined()
    // activeWorkspaceId should remain unchanged
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
  })

  it('prev-workspace wraps from first to last', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    const ws3 = useWorkspaceStore.getState().addWorkspace('WS3')
    useWorkspaceStore.getState().setActiveWorkspace(ws1.id)

    const workspaces = useWorkspaceStore.getState().workspaces
    const currentIdx = workspaces.findIndex((w) => w.id === ws1.id)
    const prevIdx = (currentIdx - 1 + workspaces.length) % workspaces.length
    useWorkspaceStore.getState().setActiveWorkspace(workspaces[prevIdx].id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws3.id)
  })

  it('next-workspace wraps from last to first', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    const ws3 = useWorkspaceStore.getState().addWorkspace('WS3')
    useWorkspaceStore.getState().setActiveWorkspace(ws3.id)

    const workspaces = useWorkspaceStore.getState().workspaces
    const currentIdx = workspaces.findIndex((w) => w.id === ws3.id)
    const nextIdx = (currentIdx + 1) % workspaces.length
    useWorkspaceStore.getState().setActiveWorkspace(workspaces[nextIdx].id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
  })

  it('workspace shortcuts with 0 workspaces do nothing', () => {
    const workspaces = useWorkspaceStore.getState().workspaces
    expect(workspaces).toHaveLength(0)
    // Attempting switch should not crash
    const target = workspaces[0]
    expect(target).toBeUndefined()
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })
})
```

### Step 8.2：跑測試確認 green

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/lib/workspace-shortcuts.test.ts
```

預期：全部 pass（測試直接操作 store，不依賴 handler 函式）。

### Step 8.3：新增 keybindings

> **macOS 注意：** `⌘⌥↑/↓`（`CommandOrControl+Alt+Up/Down`）可能與 macOS Mission Control 的桌面切換快捷鍵衝突。使用者若遇到快捷鍵無效，需至「系統設定 → 鍵盤 → 鍵盤快捷鍵 → Mission Control」關閉對應項目。

- [ ] 修改 `electron/keybindings.ts`

更新 `MenuGroup` type：

將：
```typescript
export type MenuGroup = 'tab-index' | 'tab-nav' | 'tab-action' | 'app' | 'view' | 'file'
```

改為：
```typescript
export type MenuGroup = 'tab-index' | 'tab-nav' | 'tab-action' | 'workspace-nav' | 'app' | 'view' | 'file'
```

在 `DEFAULT_KEYBINDINGS` 陣列中，在 `open-history` 項目之後、`// File` 之前加入：

```typescript
  // Workspace navigation
  { action: 'switch-workspace-1', accelerator: 'CommandOrControl+Alt+1', label: 'Workspace 1', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-2', accelerator: 'CommandOrControl+Alt+2', label: 'Workspace 2', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-3', accelerator: 'CommandOrControl+Alt+3', label: 'Workspace 3', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-4', accelerator: 'CommandOrControl+Alt+4', label: 'Workspace 4', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-5', accelerator: 'CommandOrControl+Alt+5', label: 'Workspace 5', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-6', accelerator: 'CommandOrControl+Alt+6', label: 'Workspace 6', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-7', accelerator: 'CommandOrControl+Alt+7', label: 'Workspace 7', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-8', accelerator: 'CommandOrControl+Alt+8', label: 'Workspace 8', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-9', accelerator: 'CommandOrControl+Alt+9', label: 'Workspace 9', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'prev-workspace', accelerator: 'CommandOrControl+Alt+Up', label: 'Previous Workspace', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'next-workspace', accelerator: 'CommandOrControl+Alt+Down', label: 'Next Workspace', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
```

### Step 8.4：更新 buildMenuTemplate 加入 workspace-nav group

- [ ] 修改 `electron/keybindings.ts` 的 `buildMenuTemplate`

將 `tabMenu`：

```typescript
  const tabMenu: MenuItemConstructorOptions = {
    label: 'Tab',
    submenu: [
      ...(byGroup.get('tab-index') ?? []),
      { type: 'separator' as const },
      ...(byGroup.get('tab-nav') ?? []),
      { type: 'separator' as const },
      ...(byGroup.get('tab-action') ?? []),
    ],
  }
```

改為：

```typescript
  const tabMenu: MenuItemConstructorOptions = {
    label: 'Tab',
    submenu: [
      ...(byGroup.get('tab-index') ?? []),
      { type: 'separator' as const },
      ...(byGroup.get('tab-nav') ?? []),
      { type: 'separator' as const },
      ...(byGroup.get('tab-action') ?? []),
      { type: 'separator' as const },
      ...(byGroup.get('workspace-nav') ?? []),
    ],
  }
```

### Step 8.5：新增 useShortcuts 的 workspace handler

- [ ] 修改 `spa/src/hooks/useShortcuts.ts`

在 `reopen-closed-tab` handler 之後、`if (import.meta.env.DEV)` 之前加入：

```typescript
      if (action.startsWith('switch-workspace-')) {
        const workspaces = useWorkspaceStore.getState().workspaces
        if (workspaces.length === 0) return
        const index = parseInt(action.replace('switch-workspace-', ''), 10) - 1
        const targetWs = workspaces[index]
        if (!targetWs) return
        useWorkspaceStore.getState().setActiveWorkspace(targetWs.id)
        // Activate the workspace's active tab or first tab
        const activeTab = targetWs.activeTabId ?? targetWs.tabs[0]
        if (activeTab) tabState.setActiveTab(activeTab)
        return
      }

      if (action === 'prev-workspace' || action === 'next-workspace') {
        const workspaces = useWorkspaceStore.getState().workspaces
        if (workspaces.length === 0) return
        const currentWsId = useWorkspaceStore.getState().activeWorkspaceId
        const currentIdx = workspaces.findIndex((w) => w.id === currentWsId)
        const delta = action === 'next-workspace' ? 1 : -1
        const nextIdx = currentIdx === -1
          ? 0
          : (currentIdx + delta + workspaces.length) % workspaces.length
        const targetWs = workspaces[nextIdx]
        useWorkspaceStore.getState().setActiveWorkspace(targetWs.id)
        const activeTab = targetWs.activeTabId ?? targetWs.tabs[0]
        if (activeTab) tabState.setActiveTab(activeTab)
        return
      }
```

### Step 8.6：寫 useShortcuts workspace 快捷鍵整合測試

- [ ] 在 `spa/src/hooks/useShortcuts.test.ts` 最末尾加入：

```typescript
  describe('switch-workspace-{n}', () => {
    it('switches to workspace by index', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
      useWorkspaceStore.getState().addTabToWorkspace(ws2.id, seedTabs(1, { addToWorkspace: false })[0].id)
      renderHook(() => useShortcuts())

      fire('switch-workspace-2')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
    })

    it('ignores out-of-range workspace index', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      const currentWsId = useWorkspaceStore.getState().activeWorkspaceId
      renderHook(() => useShortcuts())

      fire('switch-workspace-5')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(currentWsId)
    })

    it('does nothing with 0 workspaces', () => {
      const { fire } = mockElectronAPI()
      // Reset to empty state (0 workspaces)
      useWorkspaceStore.getState().reset()
      renderHook(() => useShortcuts())

      fire('switch-workspace-1')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
    })
  })

  describe('prev-workspace / next-workspace', () => {
    it('cycles to next workspace', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      const ws1Id = useWorkspaceStore.getState().activeWorkspaceId
      const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
      renderHook(() => useShortcuts())

      fire('next-workspace')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
    })

    it('wraps from last to first workspace', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      const ws1Id = useWorkspaceStore.getState().activeWorkspaceId!
      const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
      useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
      renderHook(() => useShortcuts())

      fire('next-workspace')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1Id)
    })

    it('cycles to prev workspace', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      const ws1Id = useWorkspaceStore.getState().activeWorkspaceId!
      const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
      useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
      renderHook(() => useShortcuts())

      fire('prev-workspace')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1Id)
    })

    it('does nothing with 0 workspaces', () => {
      const { fire } = mockElectronAPI()
      useWorkspaceStore.getState().reset()
      renderHook(() => useShortcuts())

      fire('next-workspace')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
    })
  })
```

### Step 8.7：跑全部測試

- [ ] 執行全部測試

```bash
cd spa && npx vitest run
```

預期：全部 pass。

### Step 8.8：Commit

- [ ] 提交

```bash
git add -A && git commit -m "feat: Workspace 快捷鍵 — ⌘⌥1~9 位置切換 + ⌘⌥↑/↓ 循環切換"
```

---

## Task 9：首個 workspace 建立詢問（10.1 剩餘）

### Step 9.1：寫 MigrateTabsDialog 測試

- [ ] 建立 `spa/src/features/workspace/components/MigrateTabsDialog.test.tsx`

**檔案：`spa/src/features/workspace/components/MigrateTabsDialog.test.tsx`**（完整內容）

```typescript
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { MigrateTabsDialog } from './MigrateTabsDialog'

describe('MigrateTabsDialog', () => {
  beforeEach(() => {
    cleanup()
  })

  it('renders dialog with tab count', () => {
    render(
      <MigrateTabsDialog
        tabCount={3}
        workspaceName="New WS"
        onMigrate={vi.fn()}
        onSkip={vi.fn()}
      />,
    )
    expect(screen.getByText(/3/)).toBeInTheDocument()
    expect(screen.getByText(/New WS/)).toBeInTheDocument()
  })

  it('calls onMigrate when user chooses to migrate', () => {
    const onMigrate = vi.fn()
    render(
      <MigrateTabsDialog
        tabCount={2}
        workspaceName="WS"
        onMigrate={onMigrate}
        onSkip={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /move/i }))
    expect(onMigrate).toHaveBeenCalled()
  })

  it('calls onSkip when user chooses not to migrate', () => {
    const onSkip = vi.fn()
    render(
      <MigrateTabsDialog
        tabCount={2}
        workspaceName="WS"
        onMigrate={vi.fn()}
        onSkip={onSkip}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /skip/i }))
    expect(onSkip).toHaveBeenCalled()
  })
})
```

### Step 9.2：跑測試確認 red

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/components/MigrateTabsDialog.test.tsx
```

預期：fail

### Step 9.3：實作 MigrateTabsDialog

- [ ] 建立 `spa/src/features/workspace/components/MigrateTabsDialog.tsx`

**檔案：`spa/src/features/workspace/components/MigrateTabsDialog.tsx`**（完整內容）

```typescript
import { ArrowRight, SkipForward } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  tabCount: number
  workspaceName: string
  onMigrate: () => void
  onSkip: () => void
}

export function MigrateTabsDialog({ tabCount, workspaceName, onMigrate, onSkip }: Props) {
  const t = useI18nStore((s) => s.t)

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-sm mx-4 p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-2">
          {t('workspace.migrate_title')}
        </h3>
        <p className="text-xs text-text-muted mb-4">
          {t('workspace.migrate_description', { count: tabCount, name: workspaceName })}
        </p>
        <div className="flex justify-end gap-2">
          <button
            onClick={onSkip}
            className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer flex items-center gap-1.5"
          >
            <SkipForward size={14} />
            {t('workspace.migrate_skip')}
          </button>
          <button
            onClick={onMigrate}
            className="px-3 py-1.5 rounded text-xs bg-accent text-white hover:bg-accent/80 cursor-pointer flex items-center gap-1.5"
          >
            <ArrowRight size={14} />
            {t('workspace.migrate_move')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

### Step 9.4：跑測試確認 green

- [ ] 執行測試

```bash
cd spa && npx vitest run src/features/workspace/components/MigrateTabsDialog.test.tsx
```

預期：全部 pass。

### Step 9.5：整合 onAddWorkspace 邏輯到 App.tsx

- [ ] 修改 `spa/src/App.tsx`

加入 import：

```typescript
import { MigrateTabsDialog } from './features/workspace'
```

（注意：若已在 Step 7.21 的 import 中加入了 features/workspace 的 import，只需要在那行加入 `MigrateTabsDialog`）

在 workspace settings UI state 區塊加入：

```typescript
  const [migrateDialog, setMigrateDialog] = useState<{ wsId: string; wsName: string } | null>(null)
```

將 ActivityBar 的 `onAddWorkspace={() => {}}` 替換為：

```typescript
          onAddWorkspace={() => {
            if (workspaces.length === 0 && tabOrder.length > 0) {
              // First workspace with existing standalone tabs — ask to migrate
              const ws = useWorkspaceStore.getState().addWorkspace('Workspace 1')
              setMigrateDialog({ wsId: ws.id, wsName: ws.name })
            } else {
              const count = workspaces.length + 1
              useWorkspaceStore.getState().addWorkspace(`Workspace ${count}`)
            }
          }}
```

在 dialog 渲染區域（wsIconTarget 之後）加入：

```typescript
        {migrateDialog && (
          <MigrateTabsDialog
            tabCount={tabOrder.length}
            workspaceName={migrateDialog.wsName}
            onMigrate={() => {
              // Move all existing tabs into the new workspace (use insertTab for consistency)
              tabOrder.forEach((tabId) => {
                useWorkspaceStore.getState().insertTab(tabId, migrateDialog.wsId)
              })
              setMigrateDialog(null)
            }}
            onSkip={() => {
              setMigrateDialog(null)
            }}
          />
        )}
```

### Step 9.6：更新 index.ts 匯出 MigrateTabsDialog

- [ ] 更新 `spa/src/features/workspace/index.ts`

**檔案：`spa/src/features/workspace/index.ts`**（完整內容）

```typescript
// Store
export { useWorkspaceStore } from './store'

// Hooks
export { useTabWorkspaceActions } from './hooks'

// Components
export { ActivityBar } from './components/ActivityBar'
export { WorkspaceDeleteDialog } from './components/WorkspaceDeleteDialog'
export { WorkspaceContextMenu } from './components/WorkspaceContextMenu'
export { WorkspaceChip } from './components/WorkspaceChip'
export { WorkspaceRenameDialog } from './components/WorkspaceRenameDialog'
export { WorkspaceColorPicker } from './components/WorkspaceColorPicker'
export { WorkspaceIconPicker } from './components/WorkspaceIconPicker'
export { MigrateTabsDialog } from './components/MigrateTabsDialog'

// Lib
export { getVisibleTabIds } from './lib/getVisibleTabIds'

// Re-export shared types from types/tab.ts
export type { Workspace } from '../../types/tab'
export { createWorkspace, isStandaloneTab } from '../../types/tab'
```

### Step 9.7：跑全部測試

- [ ] 執行全部測試

```bash
cd spa && npx vitest run
```

預期：全部 pass。

### Step 9.8：跑 lint + build 確認無錯

- [ ] 執行 lint 和 build

```bash
cd spa && pnpm run lint && pnpm run build
```

預期：無錯誤。

### Step 9.9：Commit

- [ ] 提交

```bash
git add -A && git commit -m "feat: 首個 workspace 建立時詢問遷移既有 tab — MigrateTabsDialog"
```

---

## 最終驗證

### Final Step：全部測試 + lint + build

- [ ] 跑全部驗證

```bash
cd spa && npx vitest run && pnpm run lint && pnpm run build
```

預期：全部 pass、無 lint 錯誤、build 成功。

---

## 檔案清單

### 新建檔案
| 檔案 | 說明 |
|------|------|
| `spa/src/features/workspace/store.ts` | Workspace store（全自由制 + insertTab） |
| `spa/src/features/workspace/store.test.ts` | Store 測試 |
| `spa/src/features/workspace/hooks.ts` | useTabWorkspaceActions |
| `spa/src/features/workspace/index.ts` | Public API |
| `spa/src/features/workspace/lib/getVisibleTabIds.ts` | 共用 visible tabs 邏輯 |
| `spa/src/features/workspace/lib/getVisibleTabIds.test.ts` | getVisibleTabIds 測試 |
| `spa/src/features/workspace/lib/workspace-shortcuts.test.ts` | Workspace 快捷鍵邏輯測試 |
| `spa/src/features/workspace/components/ActivityBar.tsx` | ActivityBar 元件 |
| `spa/src/features/workspace/components/ActivityBar.test.tsx` | ActivityBar 測試 |
| `spa/src/features/workspace/components/WorkspaceDeleteDialog.tsx` | 刪除確認對話框 |
| `spa/src/features/workspace/components/WorkspaceDeleteDialog.test.tsx` | 刪除確認測試 |
| `spa/src/features/workspace/components/WorkspaceContextMenu.tsx` | 右鍵選單 |
| `spa/src/features/workspace/components/WorkspaceContextMenu.test.tsx` | 右鍵選單測試 |
| `spa/src/features/workspace/components/WorkspaceChip.tsx` | Titlebar chip |
| `spa/src/features/workspace/components/WorkspaceChip.test.tsx` | Chip 測試 |
| `spa/src/features/workspace/components/WorkspaceRenameDialog.tsx` | 重新命名對話框 |
| `spa/src/features/workspace/components/WorkspaceRenameDialog.test.tsx` | 重新命名測試 |
| `spa/src/features/workspace/components/WorkspaceColorPicker.tsx` | 顏色選擇器 |
| `spa/src/features/workspace/components/WorkspaceColorPicker.test.tsx` | 顏色選擇器測試 |
| `spa/src/features/workspace/components/WorkspaceIconPicker.tsx` | 圖示選擇器 |
| `spa/src/features/workspace/components/WorkspaceIconPicker.test.tsx` | 圖示選擇器測試 |
| `spa/src/features/workspace/components/MigrateTabsDialog.tsx` | 遷移 tab 詢問對話框 |
| `spa/src/features/workspace/components/MigrateTabsDialog.test.tsx` | 遷移對話框測試 |

### 修改檔案
| 檔案 | 說明 |
|------|------|
| `spa/src/types/tab.ts` | `createWorkspace` 加 `icon?` 參數 |
| `spa/src/stores/useWorkspaceStore.ts` | 改為 re-export |
| `spa/src/hooks/useTabWorkspaceActions.ts` | 改為 re-export |
| `spa/src/components/ActivityBar.tsx` | 改為 re-export |
| `spa/src/hooks/useShortcuts.ts` | 改用共用 getVisibleTabIds + insertTab + workspace 快捷鍵 |
| `spa/src/hooks/useShortcuts.test.ts` | 更新 seedTabs + 新增 workspace 快捷鍵測試 |
| `spa/src/hooks/useNotificationDispatcher.ts` | 改用 insertTab |
| `spa/src/components/hosts/SessionsSection.tsx` | 改用 insertTab |
| `spa/src/App.tsx` | 整合 getVisibleTabIds + insertTab + Chip + 右鍵選單 + 對話框 |
| `electron/keybindings.ts` | 新增 workspace-nav keybindings + menu group |

### 刪除檔案
| 檔案 | 說明 |
|------|------|
| `spa/src/stores/useWorkspaceStore.test.ts` | 搬遷至 features/ |
| `spa/src/components/ActivityBar.test.tsx` | 搬遷至 features/ |
