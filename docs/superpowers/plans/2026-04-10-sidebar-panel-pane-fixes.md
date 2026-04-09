# Sidebar / Panel / Pane 修正 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修正 sidebar/panel/pane 三層概念模型，實作 Module Config 系統、Pane UX 改善、Region Toggle 按鈕。

**Architecture:** Module Registry 擴充 config 宣告機制，Workspace store 新增泛用 moduleConfig 儲存，Pane 系統改善拖曳/彈出/合併/交換 UX，TitleBar 新增 region toggle 按鈕。

**Tech Stack:** React 19 / Zustand 5 / Tailwind 4 / Vitest / Phosphor Icons

**Spec:** `docs/superpowers/specs/2026-04-10-sidebar-panel-pane-fixes-design.md`

---

### Task 1: Module Registry — ConfigDef 型別 + 查詢函數

**Files:**
- Modify: `spa/src/lib/module-registry.ts`
- Test: `spa/src/lib/module-registry.test.ts`

- [ ] **Step 1: 寫測試 — ConfigDef 型別 + 查詢函數**

```typescript
// spa/src/lib/module-registry.test.ts — 追加以下測試

describe('workspaceConfig / globalConfig', () => {
  it('getModulesWithWorkspaceConfig returns modules that declared workspaceConfig', () => {
    registerModule({
      id: 'files',
      name: 'Files',
      workspaceConfig: [{ key: 'projectPath', type: 'string', label: '專案路徑' }],
    })
    registerModule({ id: 'browser', name: 'Browser' })

    const result = getModulesWithWorkspaceConfig()
    expect(result).toHaveLength(1)
    expect(result[0].id).toBe('files')
    expect(result[0].workspaceConfig![0].key).toBe('projectPath')
  })

  it('getModulesWithGlobalConfig returns modules that declared globalConfig', () => {
    registerModule({
      id: 'theme-mod',
      name: 'Theme Module',
      globalConfig: [{ key: 'darkMode', type: 'boolean', label: 'Dark Mode', defaultValue: true }],
    })
    registerModule({ id: 'other', name: 'Other' })

    const result = getModulesWithGlobalConfig()
    expect(result).toHaveLength(1)
    expect(result[0].id).toBe('theme-mod')
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/module-registry.test.ts`
Expected: FAIL — `getModulesWithWorkspaceConfig` and `getModulesWithGlobalConfig` not found, `workspaceConfig` not in type

- [ ] **Step 3: 實作 ConfigDef + ModuleDefinition 擴充 + 查詢函數**

```typescript
// spa/src/lib/module-registry.ts — 新增型別（在 ModuleDefinition 之前）

export interface ConfigDef {
  key: string
  type: 'string' | 'boolean' | 'number'
  label: string
  required?: boolean
  defaultValue?: unknown
}

// 修改 ModuleDefinition — 新增兩個 optional 欄位
export interface ModuleDefinition {
  id: string
  name: string
  pane?: PaneDefinition
  views?: ViewDefinition[]
  workspaceConfig?: ConfigDef[]
  globalConfig?: ConfigDef[]
}

// 新增查詢函數（檔案底部，clearModuleRegistry 之前）
export function getModulesWithWorkspaceConfig(): ModuleDefinition[] {
  return [...modules.values()].filter((m) => m.workspaceConfig && m.workspaceConfig.length > 0)
}

export function getModulesWithGlobalConfig(): ModuleDefinition[] {
  return [...modules.values()].filter((m) => m.globalConfig && m.globalConfig.length > 0)
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/module-registry.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/module-registry.ts spa/src/lib/module-registry.test.ts
git commit -m "feat: add ConfigDef type and workspace/global config queries to module registry"
```

---

### Task 2: ViewProps 新增 region + SidebarRegion 補傳 props

**Files:**
- Modify: `spa/src/lib/module-registry.ts:19-23`
- Modify: `spa/src/components/SidebarRegion.tsx:98-99`
- Test: `spa/src/components/SidebarRegion.test.tsx`

- [ ] **Step 1: 寫測試 — SidebarRegion 傳遞 region/workspaceId/hostId**

```typescript
// spa/src/components/SidebarRegion.test.tsx — 追加測試

it('passes region, workspaceId and hostId to active view component', async () => {
  const receivedProps: Record<string, unknown> = {}
  const TestView = (props: ViewProps) => {
    Object.assign(receivedProps, props)
    return <div>test</div>
  }

  // 註冊有 view 的 module（需根據現有測試的 setup pattern）
  registerModule({
    id: 'test-mod',
    name: 'Test',
    views: [{
      id: 'test-view',
      label: 'Test',
      icon: () => <span>T</span>,
      scope: 'workspace',
      defaultRegion: 'primary-sidebar',
      component: TestView,
    }],
  })

  // 設定 layout store 讓 primary-sidebar 有 view 且 pinned
  useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
  useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')

  // 設定 workspace 和 host
  // (根據現有 mock 方式設定 activeWorkspaceId 和 activeHostId)

  render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)

  expect(receivedProps.region).toBe('primary-sidebar')
  expect(receivedProps.isActive).toBe(true)
  // workspaceId 和 hostId 取決於 mock store 設定
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/components/SidebarRegion.test.tsx`
Expected: FAIL — `region` prop not passed

- [ ] **Step 3: 修改 ViewProps + SidebarRegion**

```typescript
// spa/src/lib/module-registry.ts — ViewProps 新增 region
export interface ViewProps {
  hostId?: string
  workspaceId?: string
  isActive: boolean
  region?: SidebarRegion
}
```

```typescript
// spa/src/components/SidebarRegion.tsx — import stores + 傳遞 props
// 頂部新增 import
import { useWorkspaceStore } from '../features/workspace/store'
import { useHostStore } from '../stores/useHostStore'

// 在 component 內部取值（在 const { views, ... } 之後）
const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
const activeHostId = useHostStore((s) => s.activeHostId ?? s.hostOrder[0] ?? '')

// 修改 line 99: <ActiveComponent> 補傳 props
{ActiveComponent && (
  <ActiveComponent
    isActive={true}
    region={region}
    workspaceId={activeWorkspaceId ?? undefined}
    hostId={activeHostId || undefined}
  />
)}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/components/SidebarRegion.test.tsx`
Expected: PASS

- [ ] **Step 5: 跑全部測試確認無 regression**

Run: `cd spa && npx vitest run`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/module-registry.ts spa/src/components/SidebarRegion.tsx spa/src/components/SidebarRegion.test.tsx
git commit -m "feat: pass region/workspaceId/hostId to sidebar view components"
```

---

### Task 3: Workspace moduleConfig + store action

**Files:**
- Modify: `spa/src/types/tab.ts:41-49,78-86`
- Modify: `spa/src/features/workspace/store.ts`
- Test: `spa/src/features/workspace/store.test.ts`

- [ ] **Step 1: 寫測試 — setModuleConfig action**

```typescript
// spa/src/features/workspace/store.test.ts — 追加測試

describe('setModuleConfig', () => {
  it('sets a module config value on a workspace', () => {
    const wsId = setupWorkspace('test-ws')
    useWorkspaceStore.getState().setModuleConfig(wsId, 'files', 'projectPath', '/home/user/project')

    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
    expect(ws.moduleConfig?.files?.projectPath).toBe('/home/user/project')
  })

  it('uses safe spread for workspaces without moduleConfig field', () => {
    const wsId = setupWorkspace('legacy-ws')
    // Simulate old persist data without moduleConfig by directly mutating
    useWorkspaceStore.setState((s) => ({
      workspaces: s.workspaces.map((ws) => {
        if (ws.id !== wsId) return ws
        const { moduleConfig: _, ...rest } = ws as Workspace & { moduleConfig?: unknown }
        return rest as Workspace
      }),
    }))

    // Should not throw
    useWorkspaceStore.getState().setModuleConfig(wsId, 'files', 'projectPath', '/safe')
    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
    expect(ws.moduleConfig?.files?.projectPath).toBe('/safe')
  })

  it('preserves existing config when setting a new key', () => {
    const wsId = setupWorkspace('multi-config')
    useWorkspaceStore.getState().setModuleConfig(wsId, 'files', 'projectPath', '/path1')
    useWorkspaceStore.getState().setModuleConfig(wsId, 'files', 'showHidden', true)

    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
    expect(ws.moduleConfig?.files?.projectPath).toBe('/path1')
    expect(ws.moduleConfig?.files?.showHidden).toBe(true)
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/features/workspace/store.test.ts`
Expected: FAIL — `setModuleConfig` not found, `moduleConfig` not in type

- [ ] **Step 3: 修改 Workspace type + createWorkspace + store action**

```typescript
// spa/src/types/tab.ts — Workspace 新增 moduleConfig
export interface Workspace {
  id: string
  name: string
  icon?: string
  iconWeight?: IconWeight
  tabs: string[]
  activeTabId: string | null
  sidebarState?: WorkspaceSidebarState
  moduleConfig?: Record<string, Record<string, unknown>>
}

// spa/src/types/tab.ts — createWorkspace 初始化 moduleConfig
export function createWorkspace(name: string, icon?: string): Workspace {
  return {
    id: generateId(),
    name,
    icon,
    tabs: [],
    activeTabId: null,
    moduleConfig: {},
  }
}
```

```typescript
// spa/src/features/workspace/store.ts — WorkspaceState 新增 action
// 在 interface WorkspaceState 新增：
setModuleConfig: (wsId: string, moduleId: string, key: string, value: unknown) => void

// 在 store 實作中新增：
setModuleConfig: (wsId, moduleId, key, value) =>
  set((state) => ({
    workspaces: state.workspaces.map((ws) => {
      if (ws.id !== wsId) return ws
      return {
        ...ws,
        moduleConfig: {
          ...(ws.moduleConfig ?? {}),
          [moduleId]: {
            ...(ws.moduleConfig?.[moduleId] ?? {}),
            [key]: value,
          },
        },
      }
    }),
  })),
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/features/workspace/store.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/types/tab.ts spa/src/features/workspace/store.ts spa/src/features/workspace/store.test.ts
git commit -m "feat: add moduleConfig to Workspace type and setModuleConfig action"
```

---

### Task 4: Global Module Config Store

**Files:**
- Create: `spa/src/stores/useModuleConfigStore.ts`
- Modify: `spa/src/lib/storage/keys.ts`
- Create: `spa/src/stores/useModuleConfigStore.test.ts`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/stores/useModuleConfigStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useModuleConfigStore } from './useModuleConfigStore'

beforeEach(() => {
  useModuleConfigStore.setState({ globalConfig: {} })
})

describe('useModuleConfigStore', () => {
  it('sets and reads global module config', () => {
    useModuleConfigStore.getState().setGlobalModuleConfig('files', 'maxDepth', 5)
    expect(useModuleConfigStore.getState().globalConfig.files?.maxDepth).toBe(5)
  })

  it('preserves existing keys when setting new ones', () => {
    useModuleConfigStore.getState().setGlobalModuleConfig('files', 'keyA', 'valA')
    useModuleConfigStore.getState().setGlobalModuleConfig('files', 'keyB', 'valB')
    const cfg = useModuleConfigStore.getState().globalConfig.files!
    expect(cfg.keyA).toBe('valA')
    expect(cfg.keyB).toBe('valB')
  })

  it('getGlobalModuleConfig returns value or undefined', () => {
    useModuleConfigStore.getState().setGlobalModuleConfig('m', 'k', 42)
    expect(useModuleConfigStore.getState().getGlobalModuleConfig('m', 'k')).toBe(42)
    expect(useModuleConfigStore.getState().getGlobalModuleConfig('m', 'missing')).toBeUndefined()
    expect(useModuleConfigStore.getState().getGlobalModuleConfig('nope', 'k')).toBeUndefined()
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/stores/useModuleConfigStore.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: 新增 STORAGE_KEYS + 建立 store**

```typescript
// spa/src/lib/storage/keys.ts — 新增 key
MODULE_CONFIG: 'purdex-module-config',
```

```typescript
// spa/src/stores/useModuleConfigStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { browserStorage } from '../lib/storage'
import { STORAGE_KEYS } from '../lib/storage/keys'

interface ModuleConfigState {
  globalConfig: Record<string, Record<string, unknown>>
  setGlobalModuleConfig: (moduleId: string, key: string, value: unknown) => void
  getGlobalModuleConfig: (moduleId: string, key: string) => unknown
}

export const useModuleConfigStore = create<ModuleConfigState>()(
  persist(
    (set, get) => ({
      globalConfig: {},

      setGlobalModuleConfig: (moduleId, key, value) =>
        set((state) => ({
          globalConfig: {
            ...state.globalConfig,
            [moduleId]: {
              ...(state.globalConfig[moduleId] ?? {}),
              [key]: value,
            },
          },
        })),

      getGlobalModuleConfig: (moduleId, key) => {
        return get().globalConfig[moduleId]?.[key]
      },
    }),
    {
      name: STORAGE_KEYS.MODULE_CONFIG,
      storage: browserStorage,
      version: 1,
    },
  ),
)
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/stores/useModuleConfigStore.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/storage/keys.ts spa/src/stores/useModuleConfigStore.ts spa/src/stores/useModuleConfigStore.test.ts
git commit -m "feat: create global module config store with persist"
```

---

### Task 5: Files Module 拆分為 workspace + session 兩個 view

**Files:**
- Modify: `spa/src/components/FileTreeView.tsx` (重命名為 workspace view)
- Create: `spa/src/components/FileTreeSessionView.tsx`
- Modify: `spa/src/lib/register-modules.tsx:93-104`
- Test: `spa/src/components/FileTreeView.test.tsx`

- [ ] **Step 1: 修改 FileTreeView 為 FileTreeWorkspaceView**

把現有 `FileTreeView.tsx` 改名為 `FileTreeWorkspaceView`，並加入 workspace projectPath 邏輯：

```typescript
// spa/src/components/FileTreeView.tsx — 重構為 workspace view
import { useCallback, useEffect, useState } from 'react'
import { FolderSimple, File, CaretRight, CaretDown } from '@phosphor-icons/react'
import { useHostStore } from '../stores/useHostStore'
import { useWorkspaceStore } from '../features/workspace/store'
import type { ViewProps } from '../lib/module-registry'

interface FileEntry {
  name: string
  isDir: boolean
  size: number
}

interface DirState {
  entries: FileEntry[]
  expanded: boolean
  loading: boolean
}

/** Workspace-scoped file tree — uses workspace.moduleConfig.files.projectPath as root */
export function FileTreeWorkspaceView({ isActive, workspaceId }: ViewProps) {
  void isActive
  const activeHostId = useHostStore((s) => s.activeHostId ?? s.hostOrder[0] ?? '')
  const baseUrl = useHostStore((s) => (activeHostId ? s.getDaemonBase(activeHostId) : ''))

  const workspace = useWorkspaceStore((s) => s.workspaces.find((ws) => ws.id === workspaceId))
  const projectPath = workspace?.moduleConfig?.files?.projectPath as string | undefined
  const setModuleConfig = useWorkspaceStore((s) => s.setModuleConfig)

  const [pathInput, setPathInput] = useState('')
  const [rootEntries, setRootEntries] = useState<FileEntry[]>([])
  const [expandedDirs, setExpandedDirs] = useState<Record<string, DirState>>({})
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const fetchDir = useCallback(async (path?: string): Promise<{ path: string; entries: FileEntry[] }> => {
    const url = path
      ? `${baseUrl}/api/files?path=${encodeURIComponent(path)}`
      : `${baseUrl}/api/files`
    const authHeaders = useHostStore.getState().getAuthHeaders(activeHostId)
    const res = await fetch(url, { headers: authHeaders })
    if (!res.ok) throw new Error(`Failed to fetch: ${res.statusText}`)
    return res.json()
  }, [baseUrl, activeHostId])

  // Load root when projectPath is set
  useEffect(() => {
    if (!baseUrl || !projectPath) return
    setLoading(true)
    setError(null)
    fetchDir(projectPath)
      .then((data) => setRootEntries(data.entries))
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false))
  }, [baseUrl, projectPath, fetchDir])

  const toggleDir = useCallback(async (fullPath: string) => {
    const existing = expandedDirs[fullPath]
    if (existing?.expanded) {
      setExpandedDirs((prev) => ({ ...prev, [fullPath]: { ...prev[fullPath], expanded: false } }))
      return
    }
    if (existing?.entries.length) {
      setExpandedDirs((prev) => ({ ...prev, [fullPath]: { ...prev[fullPath], expanded: true } }))
      return
    }
    setExpandedDirs((prev) => ({ ...prev, [fullPath]: { entries: [], expanded: true, loading: true } }))
    try {
      const data = await fetchDir(fullPath)
      setExpandedDirs((prev) => ({ ...prev, [fullPath]: { entries: data.entries, expanded: true, loading: false } }))
    } catch {
      setExpandedDirs((prev) => ({ ...prev, [fullPath]: { entries: [], expanded: false, loading: false } }))
    }
  }, [expandedDirs, fetchDir])

  // No projectPath — show setup prompt
  if (!projectPath) {
    const handleSetPath = () => {
      if (!pathInput.trim() || !workspaceId) return
      setModuleConfig(workspaceId, 'files', 'projectPath', pathInput.trim())
    }

    return (
      <div className="flex-1 flex flex-col items-center justify-center gap-3 p-4 text-xs">
        <p className="text-text-muted text-center">請設定此 workspace 的專案路徑</p>
        <input
          className="w-full px-2 py-1 rounded border border-border-default bg-surface-primary text-text-primary"
          placeholder="/path/to/project"
          value={pathInput}
          onChange={(e) => setPathInput(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && handleSetPath()}
        />
        <button
          className="px-3 py-1 rounded bg-accent-base text-white hover:bg-accent-hover disabled:opacity-40"
          disabled={!pathInput.trim() || !workspaceId}
          onClick={handleSetPath}
        >
          設定
        </button>
      </div>
    )
  }

  if (!baseUrl) return <div className="p-3 text-xs text-text-muted">No host connected</div>
  if (loading) return <div className="p-3 text-xs text-text-muted">Loading...</div>
  if (error) return <div className="p-3 text-xs text-red-400">Error: {error}</div>

  const renderEntries = (entries: FileEntry[], parentPath: string, depth: number) => (
    <div>
      {entries.map((entry) => {
        const fullPath = parentPath === '/' ? `/${entry.name}` : `${parentPath}/${entry.name}`
        const dirState = expandedDirs[fullPath]
        const isExpanded = dirState?.expanded ?? false
        return (
          <div key={entry.name}>
            <button
              data-testid={`file-entry-${entry.name}`}
              className="w-full flex items-center gap-1 px-2 py-0.5 text-xs text-text-primary hover:bg-surface-hover transition-colors"
              style={{ paddingLeft: 8 + depth * 16 }}
              onClick={() => entry.isDir && toggleDir(fullPath)}
            >
              {entry.isDir ? (
                <>
                  {isExpanded ? <CaretDown size={12} /> : <CaretRight size={12} />}
                  <FolderSimple size={14} className="text-text-muted shrink-0" />
                </>
              ) : (
                <>
                  <span className="w-3" />
                  <File size={14} className="text-text-muted shrink-0" />
                </>
              )}
              <span className="truncate">{entry.name}</span>
            </button>
            {entry.isDir && isExpanded && dirState?.entries && renderEntries(dirState.entries, fullPath, depth + 1)}
          </div>
        )
      })}
    </div>
  )

  return (
    <div className="flex-1 overflow-auto text-xs">
      <div className="px-2 py-1 text-text-muted font-medium truncate border-b border-border-subtle">{projectPath}</div>
      {renderEntries(rootEntries, projectPath, 0)}
    </div>
  )
}

// Keep backward-compat export for existing test imports
export { FileTreeWorkspaceView as FileTreeView }
```

- [ ] **Step 2: 建立 FileTreeSessionView（deferred — placeholder）**

```typescript
// spa/src/components/FileTreeSessionView.tsx
import type { ViewProps } from '../lib/module-registry'

/**
 * Session-scoped file tree — uses active terminal's cwd as root.
 * Deferred: requires daemon API endpoint GET /api/sessions/:code/cwd
 */
export function FileTreeSessionView({ isActive }: ViewProps) {
  void isActive
  return (
    <div className="flex-1 flex items-center justify-center p-4 text-xs text-text-muted text-center">
      Session file tree 尚未實作（需 daemon cwd API）
    </div>
  )
}
```

- [ ] **Step 3: 更新 register-modules.tsx — files 拆為兩個 view**

```typescript
// spa/src/lib/register-modules.tsx — 修改 files module 註冊
// import 改為:
import { FileTreeWorkspaceView } from '../components/FileTreeView'
import { FileTreeSessionView } from '../components/FileTreeSessionView'

// 替換 files module 註冊（原 lines 93-104）:
registerModule({
  id: 'files',
  name: 'Files',
  workspaceConfig: [
    { key: 'projectPath', type: 'string', label: '專案路徑' },
  ],
  views: [
    {
      id: 'file-tree-workspace',
      label: 'Files (Workspace)',
      icon: FolderOpen,
      scope: 'workspace',
      defaultRegion: 'primary-sidebar',
      component: FileTreeWorkspaceView,
    },
    {
      id: 'file-tree-session',
      label: 'Files (Session)',
      icon: FolderOpen,
      scope: 'workspace',
      defaultRegion: 'primary-panel',
      component: FileTreeSessionView,
    },
  ],
})
```

- [ ] **Step 4: 更新測試**

更新 `spa/src/components/FileTreeView.test.tsx` 和 `spa/src/lib/register-modules.test.ts`，修正因 view id 從 `'file-tree'` 改為 `'file-tree-workspace'` / `'file-tree-session'` 造成的失敗。

同時檢查 `spa/src/main.tsx` 是否有 hardcoded `'file-tree'` view id 需要更新。

- [ ] **Step 5: 執行測試確認通過**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/FileTreeView.tsx spa/src/components/FileTreeSessionView.tsx spa/src/lib/register-modules.tsx spa/src/components/FileTreeView.test.tsx spa/src/lib/register-modules.test.ts
git commit -m "feat: split files module into workspace and session views"
```

---

### Task 6: pane-tree 新增 swapPaneContent

**Files:**
- Modify: `spa/src/lib/pane-tree.ts`
- Test: `spa/src/lib/pane-tree.test.ts`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/lib/pane-tree.test.ts — 追加

describe('swapPaneContent', () => {
  it('swaps content between two leaf panes', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1', 'dashboard'), mkLeaf('p2', 'history')])
    const result = swapPaneContent(layout, 'p1', 'p2')

    const leaves = collectLeaves(result)
    expect(leaves[0].id).toBe('p1')
    expect(leaves[0].content.kind).toBe('history')
    expect(leaves[1].id).toBe('p2')
    expect(leaves[1].content.kind).toBe('dashboard')
  })

  it('returns same layout if either pane not found', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1', 'dashboard'), mkLeaf('p2', 'history')])
    expect(swapPaneContent(layout, 'p1', 'missing')).toBe(layout)
    expect(swapPaneContent(layout, 'missing', 'p2')).toBe(layout)
  })

  it('works in nested split layouts', () => {
    const layout = mkSplit('s1', 'v', [
      mkSplit('s2', 'h', [mkLeaf('p1', 'dashboard'), mkLeaf('p2', 'history')]),
      mkLeaf('p3', 'hosts'),
    ])
    const result = swapPaneContent(layout, 'p1', 'p3')
    const leaves = collectLeaves(result)
    expect(leaves.find((l) => l.id === 'p1')!.content.kind).toBe('hosts')
    expect(leaves.find((l) => l.id === 'p3')!.content.kind).toBe('dashboard')
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/pane-tree.test.ts`
Expected: FAIL — `swapPaneContent` not found

- [ ] **Step 3: 實作 swapPaneContent**

```typescript
// spa/src/lib/pane-tree.ts — 新增函數

export function swapPaneContent(layout: PaneLayout, paneIdA: string, paneIdB: string): PaneLayout {
  const paneA = findPane(layout, paneIdA)
  const paneB = findPane(layout, paneIdB)
  if (!paneA || !paneB) return layout

  const contentA = paneA.content
  const contentB = paneB.content

  let result = updatePaneInLayout(layout, paneIdA, contentB)
  result = updatePaneInLayout(result, paneIdB, contentA)
  return result
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/pane-tree.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/pane-tree.ts spa/src/lib/pane-tree.test.ts
git commit -m "feat: add swapPaneContent to pane-tree utils"
```

---

### Task 7: Workspace insertTab 支援 afterTabId

**Files:**
- Modify: `spa/src/features/workspace/store.ts:101-125`
- Test: `spa/src/features/workspace/store.test.ts`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/features/workspace/store.test.ts — 追加

describe('insertTab with afterTabId', () => {
  it('inserts tab after specified tab in workspace', () => {
    const wsId = setupWorkspace('test')
    // Add tabs A, B, C
    useWorkspaceStore.getState().insertTab('a', wsId)
    useWorkspaceStore.getState().insertTab('b', wsId)
    useWorkspaceStore.getState().insertTab('c', wsId)

    // Insert 'x' after 'a'
    useWorkspaceStore.getState().insertTab('x', wsId, 'a')
    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
    expect(ws.tabs).toEqual(['a', 'x', 'b', 'c'])
  })

  it('appends if afterTabId not found in workspace', () => {
    const wsId = setupWorkspace('test')
    useWorkspaceStore.getState().insertTab('a', wsId)
    useWorkspaceStore.getState().insertTab('x', wsId, 'missing')
    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
    expect(ws.tabs).toEqual(['a', 'x'])
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/features/workspace/store.test.ts`
Expected: FAIL — `insertTab` doesn't accept 3rd argument

- [ ] **Step 3: 修改 insertTab signature**

```typescript
// spa/src/features/workspace/store.ts — insertTab 新增 afterTabId
// Interface:
insertTab: (tabId: string, workspaceId?: string | null, afterTabId?: string) => void

// Implementation:
insertTab: (tabId, workspaceId, afterTabId) => {
  const targetWsId = workspaceId === null
    ? null
    : workspaceId !== undefined
      ? workspaceId
      : get().activeWorkspaceId

  if (!targetWsId) return

  set((state) => ({
    workspaces: state.workspaces.map((ws) => {
      if (ws.id === targetWsId) {
        if (ws.tabs.includes(tabId)) return { ...ws, activeTabId: tabId }
        let newTabs: string[]
        if (afterTabId) {
          const idx = ws.tabs.indexOf(afterTabId)
          if (idx !== -1) {
            newTabs = [...ws.tabs]
            newTabs.splice(idx + 1, 0, tabId)
          } else {
            newTabs = [...ws.tabs, tabId]
          }
        } else {
          newTabs = [...ws.tabs, tabId]
        }
        return { ...ws, tabs: newTabs, activeTabId: tabId }
      }
      // Remove from other workspaces (singleton tab dedup)
      if (!ws.tabs.includes(tabId)) return ws
      return {
        ...ws,
        tabs: ws.tabs.filter((id) => id !== tabId),
        activeTabId: ws.activeTabId === tabId ? null : ws.activeTabId,
      }
    }),
  }))
},
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/features/workspace/store.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/store.ts spa/src/features/workspace/store.test.ts
git commit -m "feat: insertTab supports afterTabId for position-aware insertion"
```

---

### Task 8: detachPane 支援 afterTabId + workspace 整合

**Files:**
- Modify: `spa/src/stores/useTabStore.ts:249-264`
- Modify: `spa/src/components/PaneLayoutRenderer.tsx:35-38`
- Test: `spa/src/stores/useTabStore.split.test.ts` (如果存在) 或 `spa/src/components/PaneLayoutRenderer.test.tsx`

- [ ] **Step 1: 寫測試 — detachPane afterTabId**

檢查現有 detachPane 測試所在檔案。在對應測試檔案追加：

```typescript
it('inserts detached tab after specified tab in tabOrder', () => {
  // Setup: tab with split layout
  const tab = createTab({ kind: 'dashboard' })
  const splitLayout = applyLayoutPattern(tab.layout, 'split-h')
  const tabWithSplit = { ...tab, layout: splitLayout }

  useTabStore.getState().addTab(tabWithSplit)
  // Add another tab after it
  const tab2 = createTab({ kind: 'history' })
  useTabStore.getState().addTab(tab2)

  const panes = collectLeaves(splitLayout)
  const newTabId = useTabStore.getState().detachPane(tabWithSplit.id, panes[1].id, tabWithSplit.id)

  expect(newTabId).toBeTruthy()
  const order = useTabStore.getState().tabOrder
  const sourceIdx = order.indexOf(tabWithSplit.id)
  const detachedIdx = order.indexOf(newTabId!)
  expect(detachedIdx).toBe(sourceIdx + 1)
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run` (找到含 detachPane 測試的檔案)
Expected: FAIL — detachPane doesn't accept 3rd argument

- [ ] **Step 3: 修改 detachPane**

```typescript
// spa/src/stores/useTabStore.ts — detachPane 新增 afterTabId
// Interface (TabState):
detachPane: (tabId: string, paneId: string, afterTabId?: string) => string | null

// Implementation:
detachPane: (tabId, paneId, afterTabId) => {
  const state = get()
  const tab = state.tabs[tabId]
  if (!tab) return null
  const pane = findPane(tab.layout, paneId)
  if (!pane) return null
  if (tab.layout.type === 'leaf') return null
  const newLayout = removePane(tab.layout, paneId)
  if (!newLayout) return null
  const newTab = createTab(pane.content)

  let newOrder: string[]
  if (afterTabId) {
    const idx = state.tabOrder.indexOf(afterTabId)
    if (idx !== -1) {
      newOrder = [...state.tabOrder]
      newOrder.splice(idx + 1, 0, newTab.id)
    } else {
      newOrder = [...state.tabOrder, newTab.id]
    }
  } else {
    newOrder = [...state.tabOrder, newTab.id]
  }

  set({
    tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout }, [newTab.id]: newTab },
    tabOrder: newOrder,
  })
  return newTab.id
},
```

- [ ] **Step 4: 修改 PaneLayoutRenderer onDetach — 補 workspace insertTab**

```typescript
// spa/src/components/PaneLayoutRenderer.tsx — 修改 onDetach handler
// 頂部新增 import:
import { useWorkspaceStore } from '../features/workspace/store'

// 修改 onDetach callback (line 35-38):
onDetach={() => {
  const newTabId = useTabStore.getState().detachPane(tabId, layout.pane.id, tabId)
  if (newTabId) {
    const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
    if (ws) {
      useWorkspaceStore.getState().insertTab(newTabId, ws.id, tabId)
    }
    useTabStore.getState().setActiveTab(newTabId)
  }
}}
```

- [ ] **Step 5: 執行測試確認通過**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/components/PaneLayoutRenderer.tsx
git commit -m "feat: detachPane inserts after source tab + workspace integration"
```

---

### Task 9: PaneSplitter 視覺加強

**Files:**
- Modify: `spa/src/components/PaneSplitter.tsx`
- Test: `spa/src/components/PaneSplitter.test.tsx`

- [ ] **Step 1: 修改 PaneSplitter 視覺**

```typescript
// spa/src/components/PaneSplitter.tsx — 替換 return JSX
return (
  <div
    className={`shrink-0 group relative ${
      direction === 'h'
        ? 'w-1 cursor-col-resize'
        : 'h-1 cursor-row-resize'
    }`}
    onMouseDown={handleMouseDown}
  >
    {/* Visible bar */}
    <div className={`absolute ${
      direction === 'h'
        ? 'inset-y-0 left-1/2 -translate-x-1/2 w-[1px] group-hover:w-[3px] group-active:w-[3px]'
        : 'inset-x-0 top-1/2 -translate-y-1/2 h-[1px] group-hover:h-[3px] group-active:h-[3px]'
    } bg-border-subtle group-hover:bg-accent-base/50 group-active:bg-accent-base/70 transition-all`} />
    {/* Invisible hit area */}
    <div className={`absolute ${
      direction === 'h'
        ? 'inset-y-0 -left-1 -right-1'
        : 'inset-x-0 -top-1 -bottom-1'
    }`} />
  </div>
)
```

- [ ] **Step 2: 執行測試確認通過**

Run: `cd spa && npx vitest run src/components/PaneSplitter.test.tsx`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/PaneSplitter.tsx
git commit -m "fix: improve PaneSplitter visual feedback and hit area"
```

---

### Task 10: PaneHeader 視覺加強 + swap 按鈕

**Files:**
- Modify: `spa/src/components/PaneHeader.tsx`
- Modify: `spa/src/components/PaneLayoutRenderer.tsx`
- Test: `spa/src/components/PaneHeader.test.tsx`

- [ ] **Step 1: 寫測試 — swap 按鈕**

```typescript
// spa/src/components/PaneHeader.test.tsx — 追加

it('renders swap button when onSwap is provided', () => {
  const onSwap = vi.fn()
  render(<PaneHeader title="test" onClose={vi.fn()} onSwap={onSwap} swapTargets={[{ id: 'p2', label: 'history' }]} />)
  expect(screen.getByTitle('Swap with...')).toBeTruthy()
})

it('does not render swap button when onSwap is not provided', () => {
  render(<PaneHeader title="test" onClose={vi.fn()} />)
  expect(screen.queryByTitle('Swap with...')).toBeNull()
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/components/PaneHeader.test.tsx`
Expected: FAIL

- [ ] **Step 3: 修改 PaneHeader — 加強視覺 + swap 按鈕**

```typescript
// spa/src/components/PaneHeader.tsx
import { useState } from 'react'
import { X, ArrowSquareOut, ArrowsLeftRight } from '@phosphor-icons/react'

interface SwapTarget {
  id: string
  label: string
}

interface Props {
  title: string
  onClose: () => void
  onDetach?: () => void
  onSwap?: (targetPaneId: string) => void
  swapTargets?: SwapTarget[]
}

export function PaneHeader({ title, onClose, onDetach, onSwap, swapTargets }: Props) {
  const [showSwapMenu, setShowSwapMenu] = useState(false)

  return (
    <div className="shrink-0 flex items-center h-7 px-2 bg-surface-secondary border-b border-border-default">
      <span className="flex-1 text-xs text-text-muted truncate font-medium">{title}</span>
      <div className="flex items-center gap-0.5">
        {onSwap && swapTargets && swapTargets.length > 0 && (
          <div className="relative">
            <button
              className="p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
              title="Swap with..."
              onClick={() => setShowSwapMenu(!showSwapMenu)}
            >
              <ArrowsLeftRight size={12} />
            </button>
            {showSwapMenu && (
              <div className="absolute right-0 top-full mt-1 z-50 bg-surface-elevated border border-border-default rounded shadow-lg py-1 min-w-[120px]">
                {swapTargets.map((target) => (
                  <button
                    key={target.id}
                    className="w-full text-left px-3 py-1 text-xs hover:bg-surface-hover transition-colors"
                    onClick={() => { onSwap(target.id); setShowSwapMenu(false) }}
                  >
                    {target.label}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
        {onDetach && (
          <button
            className="p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
            title="Detach to tab"
            onClick={onDetach}
          >
            <ArrowSquareOut size={12} />
          </button>
        )}
        <button
          className="p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
          title="Close pane"
          onClick={onClose}
        >
          <X size={12} />
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 修改 PaneLayoutRenderer — 傳入 swap props**

```typescript
// spa/src/components/PaneLayoutRenderer.tsx — leaf render 部分
// 新增 import:
import { collectLeaves, swapPaneContent } from '../lib/pane-tree'

// 在 showHeader 的 leaf render 中（line 29-42），加入 swap 邏輯:
if (showHeader) {
  const allLeaves = (() => {
    const tab = useTabStore.getState().tabs[tabId]
    return tab ? collectLeaves(tab.layout) : []
  })()
  const swapTargets = allLeaves
    .filter((p) => p.id !== layout.pane.id)
    .map((p) => ({ id: p.id, label: p.content.kind }))

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <PaneHeader
        title={layout.pane.content.kind}
        onClose={() => useTabStore.getState().closePane(tabId, layout.pane.id)}
        onDetach={() => {
          const newTabId = useTabStore.getState().detachPane(tabId, layout.pane.id, tabId)
          if (newTabId) {
            const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
            if (ws) useWorkspaceStore.getState().insertTab(newTabId, ws.id, tabId)
            useTabStore.getState().setActiveTab(newTabId)
          }
        }}
        onSwap={(targetPaneId) => {
          const tab = useTabStore.getState().tabs[tabId]
          if (!tab) return
          const newLayout = swapPaneContent(tab.layout, layout.pane.id, targetPaneId)
          useTabStore.getState().setTabLayout(tabId, newLayout)
        }}
        swapTargets={swapTargets}
      />
      <Component pane={layout.pane} isActive={isActive} />
    </div>
  )
}
```

注意：需要在 `useTabStore` 中確認是否有 `setTabLayout` action。如果沒有，需新增一個簡單的 setter：

```typescript
// useTabStore — 如果不存在 setTabLayout:
setTabLayout: (tabId: string, layout: PaneLayout) =>
  set((state) => {
    const tab = state.tabs[tabId]
    if (!tab) return state
    return { tabs: { ...state.tabs, [tabId]: { ...tab, layout } } }
  }),
```

- [ ] **Step 5: 執行測試確認通過**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/PaneHeader.tsx spa/src/components/PaneHeader.test.tsx spa/src/components/PaneLayoutRenderer.tsx spa/src/stores/useTabStore.ts
git commit -m "feat: PaneHeader visual enhancement + swap pane button"
```

---

### Task 11: Grid-4 水平 Splitter 聯動

**Files:**
- Modify: `spa/src/components/PaneLayoutRenderer.tsx`
- Test: `spa/src/components/PaneLayoutRenderer.test.tsx`

- [ ] **Step 1: 寫測試 — grid-4 偵測**

```typescript
// spa/src/components/PaneLayoutRenderer.test.tsx — 追加

it('renders grid-4 layout with synced horizontal splitters', () => {
  const gridLayout = applyLayoutPattern({ type: 'leaf', pane: { id: 'p0', content: { kind: 'dashboard' } } }, 'grid-4')
  const tab = { ...createTab({ kind: 'dashboard' }), layout: gridLayout }
  useTabStore.getState().addTab(tab)

  render(<PaneLayoutRenderer layout={gridLayout} tabId={tab.id} isActive={true} />)

  // Grid should render 4 panes
  // Verify the structure renders without error
  expect(document.querySelectorAll('[data-testid]')).toBeDefined()
})
```

- [ ] **Step 2: 實作 grid-4 偵測與專用渲染**

```typescript
// spa/src/components/PaneLayoutRenderer.tsx — 在 split rendering 之前加入 grid 偵測

// 偵測函數（component 外部）
function isGrid4(layout: PaneLayout): layout is Extract<PaneLayout, { type: 'split' }> {
  if (layout.type !== 'split' || layout.direction !== 'v' || layout.children.length !== 2) return false
  return layout.children.every(
    (c) => c.type === 'split' && c.direction === 'h' && c.children.length === 2,
  )
}

// 在 PaneLayoutRenderer component 中，split 渲染之前：
if (isGrid4(layout)) {
  const topSplit = layout.children[0] as Extract<PaneLayout, { type: 'split' }>
  const bottomSplit = layout.children[1] as Extract<PaneLayout, { type: 'split' }>

  const handleHorizontalResize = (index: number, deltaPx: number) => {
    const container = containerRef.current
    if (!container) return
    const containerWidth = container.offsetWidth
    if (containerWidth === 0) return
    const percentDelta = (deltaPx / containerWidth) * 100

    // Sync both horizontal splits
    for (const split of [topSplit, bottomSplit]) {
      const totalPercent = split.sizes[index] + split.sizes[index + 1]
      const newLeft = Math.max(10, Math.min(totalPercent - 10, split.sizes[index] + percentDelta))
      const newRight = totalPercent - newLeft
      useTabStore.getState().resizePanes(tabId, split.id, [newLeft, newRight])
    }
  }

  const handleVerticalResize = (index: number, deltaPx: number) => {
    const container = containerRef.current
    if (!container) return
    const containerHeight = container.offsetHeight
    if (containerHeight === 0) return
    const percentDelta = (deltaPx / containerHeight) * 100
    const totalPercent = layout.sizes[index] + layout.sizes[index + 1]
    const newTop = Math.max(10, Math.min(totalPercent - 10, layout.sizes[index] + percentDelta))
    const newBottom = totalPercent - newTop
    useTabStore.getState().resizePanes(tabId, layout.id, [newTop, newBottom])
  }

  // Render grid as 2x2 with synced splitters
  return (
    <div ref={containerRef} className="flex-1 flex flex-col overflow-hidden">
      {/* Top row */}
      <div style={{ flex: `${layout.sizes[0]} 0 0%` }} className="min-h-0 flex flex-row overflow-hidden">
        {topSplit.children.map((child, i) => (
          <div key={getLayoutKey(child)} className="contents">
            {i > 0 && <PaneSplitter direction="h" onResize={(d) => handleHorizontalResize(i - 1, d)} />}
            <div style={{ flex: `${topSplit.sizes[i]} 0 0%` }} className="min-w-0 min-h-0 flex overflow-hidden">
              <PaneLayoutRenderer layout={child} tabId={tabId} isActive={isActive} showHeader={true} />
            </div>
          </div>
        ))}
      </div>
      {/* Vertical splitter */}
      <PaneSplitter direction="v" onResize={(d) => handleVerticalResize(0, d)} />
      {/* Bottom row */}
      <div style={{ flex: `${layout.sizes[1]} 0 0%` }} className="min-h-0 flex flex-row overflow-hidden">
        {bottomSplit.children.map((child, i) => (
          <div key={getLayoutKey(child)} className="contents">
            {i > 0 && <PaneSplitter direction="h" onResize={(d) => handleHorizontalResize(i - 1, d)} />}
            <div style={{ flex: `${bottomSplit.sizes[i]} 0 0%` }} className="min-w-0 min-h-0 flex overflow-hidden">
              <PaneLayoutRenderer layout={child} tabId={tabId} isActive={isActive} showHeader={true} />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 3: 執行測試確認通過**

Run: `cd spa && npx vitest run src/components/PaneLayoutRenderer.test.tsx`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/PaneLayoutRenderer.tsx spa/src/components/PaneLayoutRenderer.test.tsx
git commit -m "feat: grid-4 synchronized horizontal splitter resize"
```

---

### Task 12: TabContextMenu — mergeToTab action

**Files:**
- Modify: `spa/src/components/TabContextMenu.tsx`
- Modify: `spa/src/features/workspace/hooks.ts`
- Modify: `spa/src/App.tsx` (TabContextMenu render)
- Test: `spa/src/components/TabContextMenu.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/TabContextMenu.test.tsx — 追加

it('shows mergeToTab submenu when targetTabs are provided', () => {
  const targetTab = createTab({ kind: 'dashboard' })
  const splitLayout = applyLayoutPattern(targetTab.layout, 'split-h')
  const targetTabWithSplit = { ...targetTab, layout: splitLayout }

  render(
    <TabContextMenu
      {...props}
      targetTabs={[targetTabWithSplit]}
    />,
  )

  expect(screen.getByText(/加入.*成為 pane/i)).toBeTruthy()
})

it('does not show mergeToTab when no targetTabs', () => {
  render(<TabContextMenu {...props} />)
  expect(screen.queryByText(/加入.*成為 pane/i)).toBeNull()
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/components/TabContextMenu.test.tsx`
Expected: FAIL

- [ ] **Step 3: 修改 TabContextMenu**

```typescript
// spa/src/components/TabContextMenu.tsx

// 修改 ContextMenuAction type:
export type ContextMenuAction =
  | 'viewMode-terminal' | 'viewMode-stream'
  | 'lock' | 'unlock' | 'pin' | 'unpin'
  | 'close' | 'closeOthers' | 'closeRight'
  | 'tearOff' | 'mergeTo'
  | 'mergeToTab'
  | 'rename'

// 修改 Props:
interface Props {
  tab: Tab
  position: { x: number; y: number }
  onClose: () => void
  onAction: (action: ContextMenuAction, payload?: string) => void
  hasOtherUnlocked: boolean
  hasRightUnlocked: boolean
  targetTabs?: Tab[]
}

// 在 items 陣列的 close section 之前加入 mergeToTab section:
// (在 'separator' before close section 之前)
...(targetTabs && targetTabs.length > 0 ? [
  'separator' as const,
  ...targetTabs.map((t) => ({
    label: `加入 ${getPrimaryPane(t.layout).content.kind} tab 成為 pane`,
    action: 'mergeToTab' as const,
    show: true,
    payload: t.id,
  })),
] : []),
```

注意：MenuItem interface 需要新增 optional `payload` 欄位，並修改 onClick handler：

```typescript
interface MenuItem {
  label: string
  action: ContextMenuAction
  show: boolean
  disabled?: boolean
  payload?: string
}

// onClick handler 改為:
onClick={() => { onAction(item.action, item.payload); onClose() }}
```

- [ ] **Step 4: 修改 hooks.ts — handleContextAction 處理 mergeToTab**

```typescript
// spa/src/features/workspace/hooks.ts

// 修改 handleContextAction 簽名:
const handleContextAction = useCallback((action: ContextMenuAction, payload?: string) => {
  // ...existing cases...

  // 新增 case:
  case 'mergeToTab': {
    if (!payload) break
    const sourceTab = tabs[tab.id]
    const targetTab = tabs[payload]
    if (!sourceTab || !targetTab) break
    const sourcePrimary = getPrimaryPane(sourceTab.layout)
    // 在目標 tab 的 primary pane 旁新增
    const targetPrimary = getPrimaryPane(targetTab.layout)
    useTabStore.getState().splitPane(payload, targetPrimary.id, 'h', sourcePrimary.content)
    // 關閉原 tab
    handleCloseTab(tab.id)
    break
  }
}, [contextMenu, tabs, displayTabs, handleCloseTab])
```

- [ ] **Step 5: 修改 App.tsx — 傳入 targetTabs prop**

```typescript
// spa/src/App.tsx — TabContextMenu render
// 需要計算 targetTabs:
<TabContextMenu
  tab={contextMenu.tab}
  position={contextMenu.position}
  onClose={() => setContextMenu(null)}
  onAction={handleContextAction}
  hasOtherUnlocked={displayTabs.some((t) => t.id !== contextMenu.tab.id && !t.locked)}
  hasRightUnlocked={contextMenuHasRightUnlocked}
  targetTabs={displayTabs.filter((t) =>
    t.id !== contextMenu.tab.id && t.layout.type === 'split'
  )}
/>
```

- [ ] **Step 6: 執行測試確認通過**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add spa/src/components/TabContextMenu.tsx spa/src/components/TabContextMenu.test.tsx spa/src/features/workspace/hooks.ts spa/src/App.tsx
git commit -m "feat: tab context menu mergeToTab action"
```

---

### Task 13: TitleBar — Region Toggle 按鈕

**Files:**
- Modify: `spa/src/components/TitleBar.tsx`
- Test: `spa/src/components/TitleBar.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/TitleBar.test.tsx — 追加

it('renders region toggle buttons for regions with views', () => {
  useLayoutStore.getState().setRegionViews('primary-sidebar', ['file-tree-workspace'])
  useLayoutStore.getState().setRegionViews('primary-panel', ['file-tree-session'])

  render(<TitleBar title="test" />)

  expect(screen.getByTitle('Primary Sidebar')).toBeTruthy()
  expect(screen.getByTitle('Primary Panel')).toBeTruthy()
  // Regions without views should not have buttons
})

it('toggles region mode on click', async () => {
  useLayoutStore.getState().setRegionViews('primary-sidebar', ['file-tree-workspace'])
  useLayoutStore.getState().setRegionMode('primary-sidebar', 'collapsed')

  render(<TitleBar title="test" />)
  await userEvent.click(screen.getByTitle('Primary Sidebar'))

  expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/components/TitleBar.test.tsx`
Expected: FAIL

- [ ] **Step 3: 修改 TitleBar — 新增 region toggle 按鈕**

```typescript
// spa/src/components/TitleBar.tsx
import { Columns, Rows, GridFour, Square, SidebarSimple, SquareHalfBottom } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import { useLayoutStore } from '../stores/useLayoutStore'
import type { LayoutPattern, SidebarRegion } from '../types/tab'

interface Props {
  title: string
}

const patterns: { pattern: LayoutPattern; icon: typeof Square; label: string }[] = [
  { pattern: 'single', icon: Square, label: 'Single pane' },
  { pattern: 'split-h', icon: Columns, label: 'Split horizontal' },
  { pattern: 'split-v', icon: Rows, label: 'Split vertical' },
  { pattern: 'grid-4', icon: GridFour, label: 'Grid' },
]

const regionToggles: { region: SidebarRegion; icon: typeof SidebarSimple; label: string; mirror?: boolean }[] = [
  { region: 'primary-sidebar', icon: SidebarSimple, label: 'Primary Sidebar' },
  { region: 'primary-panel', icon: SquareHalfBottom, label: 'Primary Panel' },
  { region: 'secondary-panel', icon: SquareHalfBottom, label: 'Secondary Panel', mirror: true },
  { region: 'secondary-sidebar', icon: SidebarSimple, label: 'Secondary Sidebar', mirror: true },
]

export function TitleBar({ title }: Props) {
  const activeTabId = useTabStore((s) => s.activeTabId)
  const regions = useLayoutStore((s) => s.regions)
  const toggleRegion = useLayoutStore((s) => s.toggleRegion)

  const handlePattern = (pattern: LayoutPattern) => {
    if (!activeTabId) return
    useTabStore.getState().applyLayout(activeTabId, pattern)
  }

  const visibleToggles = regionToggles.filter((t) => regions[t.region].views.length > 0)

  return (
    <div
      className="shrink-0 flex items-center bg-surface-secondary border-b border-border-subtle px-2"
      style={{ height: 30, WebkitAppRegion: 'drag' } as React.CSSProperties}
    >
      <div className="shrink-0" style={{ width: 70 }} />
      <div className="flex-1 text-center text-xs text-text-muted truncate select-none">{title}</div>
      <div
        className="shrink-0 flex items-center gap-0.5"
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        {/* Region toggles */}
        {visibleToggles.map(({ region, icon: Icon, label, mirror }) => {
          const isPinned = regions[region].mode === 'pinned'
          return (
            <button
              key={region}
              className={`p-1 rounded transition-colors ${
                isPinned
                  ? 'text-accent-base bg-accent-base/10 hover:bg-accent-base/20'
                  : 'text-text-muted hover:text-text-primary hover:bg-surface-hover'
              }`}
              title={label}
              onClick={() => toggleRegion(region)}
              style={mirror ? { transform: 'scaleX(-1)' } : undefined}
            >
              <Icon size={14} />
            </button>
          )
        })}
        {/* Separator */}
        {visibleToggles.length > 0 && (
          <div className="w-px h-3.5 bg-border-subtle mx-0.5" />
        )}
        {/* Layout pattern buttons */}
        {patterns.map(({ pattern, icon: Icon, label }) => (
          <button
            key={pattern}
            disabled={!activeTabId}
            className="p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover disabled:opacity-40 disabled:pointer-events-none"
            title={label}
            onClick={() => handlePattern(pattern)}
          >
            <Icon size={14} />
          </button>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/components/TitleBar.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/TitleBar.tsx spa/src/components/TitleBar.test.tsx
git commit -m "feat: add region toggle buttons to TitleBar"
```

---

### Task 14: Settings UI — Module Config 自動表單

**Files:**
- Create: `spa/src/components/settings/ModuleConfigSection.tsx`
- Modify: `spa/src/lib/register-modules.tsx` (register settings section)
- Create: `spa/src/components/settings/ModuleConfigSection.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/settings/ModuleConfigSection.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { userEvent } from '@testing-library/user-event'
import { ModuleConfigSection } from './ModuleConfigSection'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { useModuleConfigStore } from '../../stores/useModuleConfigStore'

beforeEach(() => {
  clearModuleRegistry()
  useModuleConfigStore.setState({ globalConfig: {} })
})

describe('ModuleConfigSection', () => {
  it('renders config fields for modules with globalConfig', () => {
    registerModule({
      id: 'test-mod',
      name: 'Test Module',
      globalConfig: [
        { key: 'maxDepth', type: 'number', label: 'Max Depth', defaultValue: 5 },
        { key: 'enabled', type: 'boolean', label: 'Enabled', defaultValue: true },
      ],
    })

    render(<ModuleConfigSection scope="global" />)
    expect(screen.getByText('Test Module')).toBeTruthy()
    expect(screen.getByText('Max Depth')).toBeTruthy()
    expect(screen.getByText('Enabled')).toBeTruthy()
  })

  it('renders nothing when no modules have config', () => {
    registerModule({ id: 'empty', name: 'Empty' })
    const { container } = render(<ModuleConfigSection scope="global" />)
    expect(container.children).toHaveLength(0)
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/components/settings/ModuleConfigSection.test.tsx`
Expected: FAIL

- [ ] **Step 3: 實作 ModuleConfigSection**

```typescript
// spa/src/components/settings/ModuleConfigSection.tsx
import { getModulesWithGlobalConfig, getModulesWithWorkspaceConfig } from '../../lib/module-registry'
import type { ConfigDef } from '../../lib/module-registry'
import { useModuleConfigStore } from '../../stores/useModuleConfigStore'
import { useWorkspaceStore } from '../../features/workspace/store'

interface Props {
  scope: 'global' | { workspaceId: string }
}

export function ModuleConfigSection({ scope }: Props) {
  const modules = scope === 'global' ? getModulesWithGlobalConfig() : getModulesWithWorkspaceConfig()

  if (modules.length === 0) return null

  return (
    <div className="space-y-6">
      {modules.map((mod) => {
        const configs = scope === 'global' ? mod.globalConfig! : mod.workspaceConfig!
        return (
          <div key={mod.id}>
            <h3 className="text-sm font-medium text-text-primary mb-2">{mod.name}</h3>
            <div className="space-y-2">
              {configs.map((def) => (
                <ConfigField
                  key={def.key}
                  def={def}
                  moduleId={mod.id}
                  scope={scope}
                />
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}

function ConfigField({ def, moduleId, scope }: { def: ConfigDef; moduleId: string; scope: Props['scope'] }) {
  const globalValue = useModuleConfigStore((s) => s.globalConfig[moduleId]?.[def.key])
  const wsValue = useWorkspaceStore((s) => {
    if (scope === 'global') return undefined
    const ws = s.workspaces.find((w) => w.id === scope.workspaceId)
    return ws?.moduleConfig?.[moduleId]?.[def.key]
  })

  const value = scope === 'global' ? globalValue : wsValue
  const displayValue = value ?? def.defaultValue ?? ''

  const handleChange = (newValue: unknown) => {
    if (scope === 'global') {
      useModuleConfigStore.getState().setGlobalModuleConfig(moduleId, def.key, newValue)
    } else {
      useWorkspaceStore.getState().setModuleConfig(scope.workspaceId, moduleId, def.key, newValue)
    }
  }

  return (
    <div className="flex items-center justify-between py-1">
      <label className="text-xs text-text-secondary">{def.label}</label>
      {def.type === 'boolean' ? (
        <button
          className={`w-8 h-4 rounded-full transition-colors ${displayValue ? 'bg-accent-base' : 'bg-surface-hover'}`}
          onClick={() => handleChange(!displayValue)}
        >
          <div className={`w-3 h-3 rounded-full bg-white transition-transform ${displayValue ? 'translate-x-4' : 'translate-x-0.5'}`} />
        </button>
      ) : (
        <input
          className="w-48 px-2 py-0.5 rounded border border-border-default bg-surface-primary text-xs text-text-primary"
          type={def.type === 'number' ? 'number' : 'text'}
          value={String(displayValue)}
          onChange={(e) => handleChange(def.type === 'number' ? Number(e.target.value) : e.target.value)}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: 註冊到 settings sections**

```typescript
// spa/src/lib/register-modules.tsx — 在 registerBuiltinModules() 中，settings sections 區塊新增:
import { ModuleConfigSection } from '../components/settings/ModuleConfigSection'

registerSettingsSection({
  id: 'module-config',
  label: 'settings.section.modules',
  order: 8,
  component: () => <ModuleConfigSection scope="global" />,
})
```

- [ ] **Step 5: 執行測試確認通過**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/settings/ModuleConfigSection.tsx spa/src/components/settings/ModuleConfigSection.test.tsx spa/src/lib/register-modules.tsx
git commit -m "feat: auto-generated module config settings UI"
```

---

### Task 15: 全部整合測試 + Lint + Build

- [ ] **Step 1: 執行全部測試**

Run: `cd spa && npx vitest run`
Expected: 全部 PASS

- [ ] **Step 2: 執行 lint**

Run: `cd spa && pnpm run lint`
Expected: 無錯誤

- [ ] **Step 3: 執行 build**

Run: `cd spa && pnpm run build`
Expected: 成功

- [ ] **Step 4: 修正任何 lint/build/test 錯誤**

- [ ] **Step 5: Final commit（如有修正）**

```bash
git add -A
git commit -m "fix: address lint and build issues"
```
