# Phase 1 — Layout Modes: Activity Bar 寬窄切換

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓使用者可在 activity bar 切換窄版（現況 44px）與寬版（icon + workspace 名稱、預設 240px、可拖拉 resize）。

**Architecture:** 擴充 `useLayoutStore` 加入 layout-mode 狀態欄位與 actions，保留 `tabPosition` 欄位（無 setter，預設 `'top'`）供 Phase 2 使用；耦合規則先寫好（「tabPosition='left' 時禁止 narrow」），Phase 2 啟用 setter 後自動生效。`ActivityBar.tsx` 改為協調者，依 store 狀態分派到新抽出的 `ActivityBarNarrow` 或新建的 `ActivityBarWide`。Resize 直接複用既有 `RegionResize.tsx`。

**Tech Stack:** React 19, Zustand 5 (with persist middleware + `syncManager`), Phosphor Icons, Tailwind 4, Vitest + @testing-library/react.

**Prerequisite:** 本計畫應在獨立 worktree 執行（依 `CLAUDE.md` 規範使用 `EnterWorktree`）。

---

## 前置閱讀

- Spec：`docs/superpowers/specs/2026-04-17-layout-modes-design.md`
- 既有 store：`spa/src/stores/useLayoutStore.ts`（含 `clampWidth(120, 600)` 與 `syncManager.register`）
- 既有 activity bar：`spa/src/features/workspace/components/ActivityBar.tsx`
- 既有 resize：`spa/src/components/RegionResize.tsx`
- i18n 結構：扁平 key/value，一份 `en.json` + 一份 `zh-TW.json`，`locales/locale-completeness.test.ts` 會驗證兩邊 key 一致

## 檔案影響總覽

**Modify:**
- `spa/src/stores/useLayoutStore.ts`
- `spa/src/stores/useLayoutStore.test.ts`
- `spa/src/features/workspace/components/ActivityBar.tsx`
- `spa/src/features/workspace/components/ActivityBar.test.tsx`
- `spa/src/App.tsx`
- `spa/src/locales/en.json`
- `spa/src/locales/zh-TW.json`

**Create:**
- `spa/src/features/workspace/components/ActivityBarNarrow.tsx`
- `spa/src/features/workspace/components/ActivityBarNarrow.test.tsx`
- `spa/src/features/workspace/components/ActivityBarWide.tsx`
- `spa/src/features/workspace/components/ActivityBarWide.test.tsx`
- `spa/src/features/workspace/components/CollapseButton.tsx`
- `spa/src/features/workspace/components/CollapseButton.test.tsx`

---

## Task 1 — 擴充 `useLayoutStore` 型別與預設值

**Files:**
- Modify: `spa/src/stores/useLayoutStore.ts`
- Test: `spa/src/stores/useLayoutStore.test.ts`

- [ ] **Step 1: 寫失敗測試 — 預設值**

在 `spa/src/stores/useLayoutStore.test.ts` 的 `describe('initial state', ...)` 區塊內新增：

```ts
it('layout mode defaults: width=narrow, tabPosition=top, wideSize=240', () => {
  const state = useLayoutStore.getState()
  expect(state.activityBarWidth).toBe('narrow')
  expect(state.tabPosition).toBe('top')
  expect(state.activityBarWideSize).toBe(240)
  expect(state.workspaceExpanded).toEqual({})
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts -t "layout mode defaults"
```

預期：fail，原因 `activityBarWidth` 等屬性 undefined。

- [ ] **Step 3: 擴充 store 型別 + 初始值 + partialize**

在 `spa/src/stores/useLayoutStore.ts` 修改：

```ts
// 檔案頂部 imports 下方加入
export type ActivityBarWidth = 'narrow' | 'wide'
export type TabPosition = 'top' | 'left'

// LayoutState interface 新增四個欄位（放在 regions 後、action 宣告前）
interface LayoutState {
  regions: Record<SidebarRegion, RegionState>
  activityBarWidth: ActivityBarWidth
  tabPosition: TabPosition
  activityBarWideSize: number
  workspaceExpanded: Record<string, boolean>

  // ... existing actions
}

// create() 內部 state 初始化，在 `regions: createDefaultRegions(),` 後新增：
activityBarWidth: 'narrow',
tabPosition: 'top',
activityBarWideSize: 240,
workspaceExpanded: {},

// persist 的 partialize 改為：
partialize: (state) => ({
  regions: state.regions,
  activityBarWidth: state.activityBarWidth,
  tabPosition: state.tabPosition,
  activityBarWideSize: state.activityBarWideSize,
  workspaceExpanded: state.workspaceExpanded,
}),
```

- [ ] **Step 4: 執行測試，確認 PASS（且不回退既有測試）**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts
```

預期：全綠。

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useLayoutStore.ts spa/src/stores/useLayoutStore.test.ts
git commit -m "feat(layout): add activityBarWidth/tabPosition/wideSize/workspaceExpanded state"
```

---

## Task 2 — `setActivityBarWidth` + 耦合規則

**Files:**
- Modify: `spa/src/stores/useLayoutStore.ts`
- Test: `spa/src/stores/useLayoutStore.test.ts`

- [ ] **Step 1: 寫失敗測試**

在 `useLayoutStore.test.ts` 新增：

```ts
describe('setActivityBarWidth', () => {
  it('narrow → wide', () => {
    useLayoutStore.getState().setActivityBarWidth('wide')
    expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
  })

  it('wide → narrow', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide' })
    useLayoutStore.getState().setActivityBarWidth('narrow')
    expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
  })

  it('refuses narrow when tabPosition=left', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
    useLayoutStore.getState().setActivityBarWidth('narrow')
    expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
  })

  it('allows wide when tabPosition=left', () => {
    useLayoutStore.setState({ activityBarWidth: 'narrow', tabPosition: 'left' })
    useLayoutStore.getState().setActivityBarWidth('wide')
    expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts -t "setActivityBarWidth"
```

- [ ] **Step 3: 實作 setter**

`useLayoutStore.ts` 的 `LayoutState` interface 新增宣告：

```ts
setActivityBarWidth: (width: ActivityBarWidth) => void
```

`create()` 內部實作（放在 `reconcileViews` 後）：

```ts
setActivityBarWidth: (width) =>
  set((state) => {
    if (width === 'narrow' && state.tabPosition === 'left') return state
    return { activityBarWidth: width }
  }),
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useLayoutStore.ts spa/src/stores/useLayoutStore.test.ts
git commit -m "feat(layout): setActivityBarWidth with tabPosition coupling rule"
```

---

## Task 3 — `toggleActivityBarWidth`

**Files:**
- Modify: `spa/src/stores/useLayoutStore.ts`
- Test: `spa/src/stores/useLayoutStore.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
describe('toggleActivityBarWidth', () => {
  it('toggles narrow ↔ wide', () => {
    useLayoutStore.getState().toggleActivityBarWidth()
    expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
    useLayoutStore.getState().toggleActivityBarWidth()
    expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
  })

  it('no-op when currently wide and tabPosition=left', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
    useLayoutStore.getState().toggleActivityBarWidth()
    expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts -t "toggleActivityBarWidth"
```

- [ ] **Step 3: 實作**

interface 新增：

```ts
toggleActivityBarWidth: () => void
```

action 實作（放在 `setActivityBarWidth` 後）：

```ts
toggleActivityBarWidth: () =>
  set((state) => {
    const next: ActivityBarWidth = state.activityBarWidth === 'narrow' ? 'wide' : 'narrow'
    if (next === 'narrow' && state.tabPosition === 'left') return state
    return { activityBarWidth: next }
  }),
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useLayoutStore.ts spa/src/stores/useLayoutStore.test.ts
git commit -m "feat(layout): toggleActivityBarWidth with coupling rule"
```

---

## Task 4 — `setActivityBarWideSize`（沿用 clampWidth）

**Files:**
- Modify: `spa/src/stores/useLayoutStore.ts`
- Test: `spa/src/stores/useLayoutStore.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
describe('setActivityBarWideSize', () => {
  it('updates value', () => {
    useLayoutStore.getState().setActivityBarWideSize(300)
    expect(useLayoutStore.getState().activityBarWideSize).toBe(300)
  })

  it('clamps below 120', () => {
    useLayoutStore.getState().setActivityBarWideSize(50)
    expect(useLayoutStore.getState().activityBarWideSize).toBe(120)
  })

  it('clamps above 600', () => {
    useLayoutStore.getState().setActivityBarWideSize(800)
    expect(useLayoutStore.getState().activityBarWideSize).toBe(600)
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts -t "setActivityBarWideSize"
```

- [ ] **Step 3: 實作**

interface 新增：

```ts
setActivityBarWideSize: (size: number) => void
```

action：

```ts
setActivityBarWideSize: (size) =>
  set(() => ({ activityBarWideSize: clampWidth(size) })),
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useLayoutStore.ts spa/src/stores/useLayoutStore.test.ts
git commit -m "feat(layout): setActivityBarWideSize with clampWidth reuse"
```

---

## Task 5 — `toggleWorkspaceExpanded`

**Files:**
- Modify: `spa/src/stores/useLayoutStore.ts`
- Test: `spa/src/stores/useLayoutStore.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
describe('toggleWorkspaceExpanded', () => {
  it('toggles from undefined → true', () => {
    useLayoutStore.getState().toggleWorkspaceExpanded('ws-1')
    expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBe(true)
  })

  it('toggles from true → false', () => {
    useLayoutStore.setState({ workspaceExpanded: { 'ws-1': true } })
    useLayoutStore.getState().toggleWorkspaceExpanded('ws-1')
    expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBe(false)
  })

  it('per-ws isolation', () => {
    useLayoutStore.getState().toggleWorkspaceExpanded('ws-1')
    useLayoutStore.getState().toggleWorkspaceExpanded('ws-2')
    expect(useLayoutStore.getState().workspaceExpanded).toEqual({
      'ws-1': true,
      'ws-2': true,
    })
  })

  it('supports "home" key', () => {
    useLayoutStore.getState().toggleWorkspaceExpanded('home')
    expect(useLayoutStore.getState().workspaceExpanded['home']).toBe(true)
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts -t "toggleWorkspaceExpanded"
```

- [ ] **Step 3: 實作**

interface 新增：

```ts
toggleWorkspaceExpanded: (wsId: string) => void
```

action：

```ts
toggleWorkspaceExpanded: (wsId) =>
  set((state) => ({
    workspaceExpanded: {
      ...state.workspaceExpanded,
      [wsId]: !state.workspaceExpanded[wsId],
    },
  })),
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useLayoutStore.ts spa/src/stores/useLayoutStore.test.ts
git commit -m "feat(layout): toggleWorkspaceExpanded per-ws state"
```

---

## Task 6 — `reconcileWorkspaceExpanded` GC

**Files:**
- Modify: `spa/src/stores/useLayoutStore.ts`
- Test: `spa/src/stores/useLayoutStore.test.ts`

- [ ] **Step 1: 寫失敗測試**

```ts
describe('reconcileWorkspaceExpanded', () => {
  it('prunes keys not in provided ws list, preserves "home"', () => {
    useLayoutStore.setState({
      workspaceExpanded: {
        'ws-alive': true,
        'ws-deleted': true,
        home: true,
      },
    })
    useLayoutStore.getState().reconcileWorkspaceExpanded(['ws-alive'])
    expect(useLayoutStore.getState().workspaceExpanded).toEqual({
      'ws-alive': true,
      home: true,
    })
  })

  it('is no-op when all keys are alive or "home"', () => {
    useLayoutStore.setState({
      workspaceExpanded: { 'ws-a': true, home: false },
    })
    const before = useLayoutStore.getState().workspaceExpanded
    useLayoutStore.getState().reconcileWorkspaceExpanded(['ws-a'])
    expect(useLayoutStore.getState().workspaceExpanded).toEqual(before)
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts -t "reconcileWorkspaceExpanded"
```

- [ ] **Step 3: 實作**

interface 新增：

```ts
reconcileWorkspaceExpanded: (liveWsIds: string[]) => void
```

action：

```ts
reconcileWorkspaceExpanded: (liveWsIds) =>
  set((state) => {
    const alive = new Set(liveWsIds)
    alive.add('home')
    const next: Record<string, boolean> = {}
    let changed = false
    for (const [key, value] of Object.entries(state.workspaceExpanded)) {
      if (alive.has(key)) next[key] = value
      else changed = true
    }
    if (!changed) return state
    return { workspaceExpanded: next }
  }),
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useLayoutStore.ts spa/src/stores/useLayoutStore.test.ts
git commit -m "feat(layout): reconcileWorkspaceExpanded GC action"
```

---

## Task 7 — App.tsx 訂閱 workspaces 變更觸發 reconcile

**Files:**
- Modify: `spa/src/App.tsx`

- [ ] **Step 1: 加入 useEffect 訂閱**

在 `App.tsx` 的 hooks 區塊（`useElectronIpc()` 那一段附近）加入：

```ts
// Reconcile workspaceExpanded when workspaces list changes
useEffect(() => {
  const wsIds = workspaces.map((w) => w.id)
  useLayoutStore.getState().reconcileWorkspaceExpanded(wsIds)
}, [workspaces])
```

並在檔案頂部加入 import：

```ts
import { useLayoutStore } from './stores/useLayoutStore'
```

- [ ] **Step 2: 手動驗證**

```bash
cd spa && pnpm run dev
```

在 UI 建立兩個 workspace、展開後刪除其中一個，打開 DevTools：
```js
JSON.parse(localStorage.getItem('purdex-layout')).state.workspaceExpanded
```
預期：已刪除 workspace 的 id 不再出現在 object 裡。

- [ ] **Step 3: Commit**

```bash
git add spa/src/App.tsx
git commit -m "feat(layout): reconcile workspaceExpanded on workspace deletion"
```

---

## Task 8 — 新增 i18n keys

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: 加入英文 key**

在 `spa/src/locales/en.json` 的 `"nav.toggle_view"` 那行下方加入：

```json
  "nav.collapse_activity_bar": "Collapse activity bar",
  "nav.expand_activity_bar": "Expand activity bar",
  "nav.collapse_locked_tooltip": "Activity bar stays wide while tabs are on the left",
```

- [ ] **Step 2: 加入繁中 key**

在 `spa/src/locales/zh-TW.json` 對應位置加入：

```json
  "nav.collapse_activity_bar": "收合側邊欄",
  "nav.expand_activity_bar": "展開側邊欄",
  "nav.collapse_locked_tooltip": "當分頁顯示在左側時，側邊欄會維持寬版",
```

- [ ] **Step 3: 執行 locale completeness 測試確認 PASS**

```bash
cd spa && npx vitest run src/locales/locale-completeness.test.ts
```

預期：綠燈。

- [ ] **Step 4: Commit**

```bash
git add spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "i18n: add collapse/expand activity bar strings"
```

---

## Task 9 — 抽出 `ActivityBarNarrow`（等同現況）

**Files:**
- Create: `spa/src/features/workspace/components/ActivityBarNarrow.tsx`
- Create: `spa/src/features/workspace/components/ActivityBarNarrow.test.tsx`
- Modify: `spa/src/features/workspace/components/ActivityBar.tsx`

**說明：** 這一步把現有 `ActivityBar.tsx` 內部的 JSX（除了最外層的導出 fn）搬到 `ActivityBarNarrow.tsx`，`ActivityBar.tsx` 暫時只是轉呼叫 `ActivityBarNarrow`。既有 `ActivityBar.test.tsx` 必須不用改就能繼續綠（因為對外行為等同）。

- [ ] **Step 1: 建立新檔，複製現有 JSX**

建立 `spa/src/features/workspace/components/ActivityBarNarrow.tsx`：

```tsx
// 內容完全複製自 spa/src/features/workspace/components/ActivityBar.tsx
// 把 export function ActivityBar(...) 改名為 export function ActivityBarNarrow(...)
// Props interface 也改名為 ActivityBarNarrowProps
// SortableWorkspaceButton 保留在同檔（只這裡用）
```

整個 render body 保持原樣。`Props` interface 改名：

```tsx
interface ActivityBarNarrowProps {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  // ... 其餘與原 Props 相同
}
```

函式簽名：

```tsx
export function ActivityBarNarrow(props: ActivityBarNarrowProps) {
  // ... 原本 ActivityBar 的 body
}
```

- [ ] **Step 2: 改寫 `ActivityBar.tsx` 為純轉呼叫**

整份檔案替換為：

```tsx
import { ActivityBarNarrow } from './ActivityBarNarrow'
import type { Workspace } from '../../../types/tab'

interface Props {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectHome: () => void
  standaloneTabIds: string[]
  onAddWorkspace: () => void
  onReorderWorkspaces?: (orderedIds: string[]) => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onOpenHosts: () => void
  onOpenSettings: () => void
}

export function ActivityBar(props: Props) {
  return <ActivityBarNarrow {...props} />
}
```

- [ ] **Step 3: 執行既有測試，確認全綠**

```bash
cd spa && npx vitest run src/features/workspace/components/ActivityBar.test.tsx
```

- [ ] **Step 4: 新增 ActivityBarNarrow 的 smoke 測試**

建立 `spa/src/features/workspace/components/ActivityBarNarrow.test.tsx`：

```tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ActivityBarNarrow } from './ActivityBarNarrow'

describe('ActivityBarNarrow', () => {
  it('renders Home button', () => {
    render(
      <ActivityBarNarrow
        workspaces={[]}
        activeWorkspaceId={null}
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(screen.getByRole('button', { name: /home/i })).toBeInTheDocument()
  })
})
```

```bash
cd spa && npx vitest run src/features/workspace/components/ActivityBarNarrow.test.tsx
```

預期：綠。

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/ActivityBarNarrow.tsx spa/src/features/workspace/components/ActivityBarNarrow.test.tsx spa/src/features/workspace/components/ActivityBar.tsx
git commit -m "refactor(activity-bar): extract ActivityBarNarrow (behavior unchanged)"
```

---

## Task 10 — 建立 `ActivityBarWide`

**Files:**
- Create: `spa/src/features/workspace/components/ActivityBarWide.tsx`
- Create: `spa/src/features/workspace/components/ActivityBarWide.test.tsx`

**說明：** Phase 1 的寬版**不含 tabs、不含 DndContext**，只顯示 icon + workspace 名稱、Home、Add / Hosts / Settings 按鈕。DnD + inline tabs 留到 Phase 2。

- [ ] **Step 1: 寫失敗測試**

建立 `ActivityBarWide.test.tsx`：

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ActivityBarWide } from './ActivityBarWide'
import type { Workspace } from '../../../types/tab'

const ws = (id: string, name: string): Workspace => ({
  id, name, tabs: [], activeTabId: null,
})

describe('ActivityBarWide', () => {
  it('renders Home label + workspace names', () => {
    render(
      <ActivityBarWide
        workspaces={[ws('w1', 'Purdex'), ws('w2', 'Client A')]}
        activeWorkspaceId="w1"
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(screen.getByText('Purdex')).toBeInTheDocument()
    expect(screen.getByText('Client A')).toBeInTheDocument()
  })

  it('clicking a workspace row calls onSelectWorkspace', () => {
    const onSelect = vi.fn()
    render(
      <ActivityBarWide
        workspaces={[ws('w1', 'Purdex')]}
        activeWorkspaceId={null}
        activeStandaloneTabId={null}
        onSelectWorkspace={onSelect}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    fireEvent.click(screen.getByText('Purdex'))
    expect(onSelect).toHaveBeenCalledWith('w1')
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL（元件不存在）**

```bash
cd spa && npx vitest run src/features/workspace/components/ActivityBarWide.test.tsx
```

- [ ] **Step 3: 實作 ActivityBarWide**

建立 `ActivityBarWide.tsx`：

```tsx
import { Plus, GearSix, HardDrives } from '@phosphor-icons/react'
import type { Workspace } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { WorkspaceIcon } from './WorkspaceIcon'

interface Props {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectHome: () => void
  standaloneTabIds: string[]
  onAddWorkspace: () => void
  onReorderWorkspaces?: (orderedIds: string[]) => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onOpenHosts: () => void
  onOpenSettings: () => void
}

export function ActivityBarWide(props: Props) {
  const {
    workspaces,
    activeWorkspaceId,
    activeStandaloneTabId,
    onSelectWorkspace,
    onSelectHome,
    onAddWorkspace,
    onContextMenuWorkspace,
    onOpenHosts,
    onOpenSettings,
  } = props
  const t = useI18nStore((s) => s.t)
  const wideSize = useLayoutStore((s) => s.activityBarWideSize)
  const isHomeActive = !activeWorkspaceId

  return (
    <div
      className="hidden lg:flex flex-col bg-surface-tertiary border-r border-border-subtle py-2 gap-1 flex-shrink-0"
      style={{ width: wideSize }}
    >
      {/* Home row */}
      <button
        onClick={onSelectHome}
        className={`mx-2 flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-left cursor-pointer transition-all ${
          isHomeActive && !activeStandaloneTabId
            ? 'bg-surface-hover text-text-primary ring-1 ring-purple-400'
            : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        }`}
      >
        <img src="/icons/logo-transparent.png" alt="" width={18} height={18} className="rounded-sm" />
        <span className="truncate">{t('nav.home')}</span>
      </button>

      {workspaces.length > 0 && <div className="mx-3 my-1 h-px bg-border-default" />}

      {/* Workspace rows */}
      <div className="flex flex-col gap-0.5">
        {workspaces.map((ws) => {
          const isActive = activeWorkspaceId === ws.id && !activeStandaloneTabId
          return (
            <button
              key={ws.id}
              onClick={() => onSelectWorkspace(ws.id)}
              onContextMenu={(e) => {
                e.preventDefault()
                onContextMenuWorkspace?.(e, ws.id)
              }}
              className={`mx-2 flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-left cursor-pointer transition-all ${
                isActive
                  ? 'bg-[#8b5cf6]/25 text-text-primary ring-1 ring-purple-400'
                  : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
              }`}
            >
              <WorkspaceIcon icon={ws.icon} name={ws.name} size={16} weight={ws.iconWeight} />
              <span className="truncate" title={ws.name}>{ws.name}</span>
            </button>
          )
        })}
      </div>

      {/* Bottom controls */}
      <div className="mt-auto flex flex-col gap-1 px-2 pb-1">
        <button
          title={t('nav.new_workspace')}
          onClick={onAddWorkspace}
          className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <Plus size={16} />
          <span className="truncate">{t('nav.new_workspace')}</span>
        </button>
        <button
          title={t('nav.hosts')}
          onClick={onOpenHosts}
          className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <HardDrives size={16} />
          <span className="truncate">{t('nav.hosts')}</span>
        </button>
        <button
          title={t('nav.settings')}
          onClick={onOpenSettings}
          className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <GearSix size={16} />
          <span className="truncate">{t('nav.settings')}</span>
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/features/workspace/components/ActivityBarWide.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/ActivityBarWide.tsx spa/src/features/workspace/components/ActivityBarWide.test.tsx
git commit -m "feat(activity-bar): add ActivityBarWide with icon+name layout"
```

---

## Task 11 — 建立 `CollapseButton`

**Files:**
- Create: `spa/src/features/workspace/components/CollapseButton.tsx`
- Create: `spa/src/features/workspace/components/CollapseButton.test.tsx`

- [ ] **Step 1: 寫失敗測試**

建立 `CollapseButton.test.tsx`：

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { CollapseButton } from './CollapseButton'
import { useLayoutStore } from '../../../stores/useLayoutStore'

beforeEach(() => {
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

describe('CollapseButton', () => {
  it('shows expand tooltip when narrow', () => {
    render(<CollapseButton />)
    const btn = screen.getByRole('button')
    expect(btn).toHaveAttribute('title', expect.stringMatching(/expand/i))
  })

  it('shows collapse tooltip when wide', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide' })
    render(<CollapseButton />)
    const btn = screen.getByRole('button')
    expect(btn).toHaveAttribute('title', expect.stringMatching(/collapse/i))
  })

  it('click toggles width when tabPosition=top', () => {
    render(<CollapseButton />)
    fireEvent.click(screen.getByRole('button'))
    expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
    fireEvent.click(screen.getByRole('button'))
    expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
  })

  it('is disabled when tabPosition=left', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
    render(<CollapseButton />)
    const btn = screen.getByRole('button')
    expect(btn).toBeDisabled()
    expect(btn.getAttribute('title')).toMatch(/locked|left/i)
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/features/workspace/components/CollapseButton.test.tsx
```

- [ ] **Step 3: 實作**

建立 `CollapseButton.tsx`：

```tsx
import { CaretDoubleLeft, CaretDoubleRight } from '@phosphor-icons/react'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useI18nStore } from '../../../stores/useI18nStore'

export function CollapseButton() {
  const width = useLayoutStore((s) => s.activityBarWidth)
  const tabPosition = useLayoutStore((s) => s.tabPosition)
  const toggle = useLayoutStore((s) => s.toggleActivityBarWidth)
  const t = useI18nStore((s) => s.t)

  const locked = tabPosition === 'left'
  const isWide = width === 'wide'
  const Icon = isWide ? CaretDoubleLeft : CaretDoubleRight
  const label = locked
    ? t('nav.collapse_locked_tooltip')
    : isWide
      ? t('nav.collapse_activity_bar')
      : t('nav.expand_activity_bar')

  return (
    <button
      type="button"
      disabled={locked}
      title={label}
      aria-label={label}
      onClick={toggle}
      className={`w-[30px] h-[30px] rounded-md flex items-center justify-center cursor-pointer ${
        locked
          ? 'text-text-muted/50 cursor-not-allowed'
          : 'text-text-secondary hover:text-text-primary hover:bg-surface-secondary'
      }`}
    >
      <Icon size={14} />
    </button>
  )
}
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/features/workspace/components/CollapseButton.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/CollapseButton.tsx spa/src/features/workspace/components/CollapseButton.test.tsx
git commit -m "feat(activity-bar): add CollapseButton with tabPosition lock"
```

---

## Task 12 — 把 `CollapseButton` 嵌入 Narrow 與 Wide

**Files:**
- Modify: `spa/src/features/workspace/components/ActivityBarNarrow.tsx`
- Modify: `spa/src/features/workspace/components/ActivityBarWide.tsx`

- [ ] **Step 1: Narrow 版加入 CollapseButton**

在 `ActivityBarNarrow.tsx` 的 bottom 按鈕群（現有 `Plus` / `HardDrives` / `GearSix` 那個 `div.mt-auto`）的 **最上方** 插入：

```tsx
<CollapseButton />
```

檔案頂部加入 import：

```tsx
import { CollapseButton } from './CollapseButton'
```

- [ ] **Step 2: Wide 版加入 CollapseButton**

在 `ActivityBarWide.tsx` 的 bottom 按鈕群（`div.mt-auto`）的**第一個子元素**插入：

```tsx
<CollapseButton />
```

檔案頂部加入 import：

```tsx
import { CollapseButton } from './CollapseButton'
```

- [ ] **Step 3: 執行所有相關測試**

```bash
cd spa && npx vitest run src/features/workspace/components
```

預期：全綠。若 `ActivityBar.test.tsx` 因多一個按鈕而影響 selectors，用更具體的 `role`/`name` 調整測試（但不改行為）。

- [ ] **Step 4: Commit**

```bash
git add spa/src/features/workspace/components/ActivityBarNarrow.tsx spa/src/features/workspace/components/ActivityBarWide.tsx
git commit -m "feat(activity-bar): mount CollapseButton in narrow and wide"
```

---

## Task 13 — `ActivityBar` 協調者：依 store 分派

**Files:**
- Modify: `spa/src/features/workspace/components/ActivityBar.tsx`
- Modify: `spa/src/features/workspace/components/ActivityBar.test.tsx`

- [ ] **Step 1: 寫失敗測試**

在 `ActivityBar.test.tsx` 新增（既有測試保留不動）：

```tsx
import { useLayoutStore } from '../../../stores/useLayoutStore'

describe('ActivityBar coordinator', () => {
  beforeEach(() => {
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })

  it('renders Narrow by default', () => {
    render(
      <ActivityBar
        workspaces={[]}
        activeWorkspaceId={null}
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    // Narrow 的 Home 用 <img alt="Purdex">；Wide 用 <span>Home</span>
    expect(screen.getByAltText('Purdex')).toBeInTheDocument()
    expect(screen.queryByText('Home')).not.toBeInTheDocument()
  })

  it('renders Wide when activityBarWidth=wide', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide' })
    render(
      <ActivityBar
        workspaces={[]}
        activeWorkspaceId={null}
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(screen.getByText('Home')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/features/workspace/components/ActivityBar.test.tsx -t "coordinator"
```

- [ ] **Step 3: 改寫 ActivityBar 為協調者**

`ActivityBar.tsx` 完整替換為：

```tsx
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { ActivityBarNarrow } from './ActivityBarNarrow'
import { ActivityBarWide } from './ActivityBarWide'
import type { Workspace } from '../../../types/tab'

interface Props {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectHome: () => void
  standaloneTabIds: string[]
  onAddWorkspace: () => void
  onReorderWorkspaces?: (orderedIds: string[]) => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onOpenHosts: () => void
  onOpenSettings: () => void
}

export function ActivityBar(props: Props) {
  const width = useLayoutStore((s) => s.activityBarWidth)
  if (width === 'wide') return <ActivityBarWide {...props} />
  return <ActivityBarNarrow {...props} />
}
```

- [ ] **Step 4: 執行測試，確認 PASS（包含既有測試）**

```bash
cd spa && npx vitest run src/features/workspace/components/ActivityBar.test.tsx
```

若既有測試因 default state 變動而壞（應該不會，因為 default 是 narrow），用 `beforeEach` 明確重設 store。

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/ActivityBar.tsx spa/src/features/workspace/components/ActivityBar.test.tsx
git commit -m "feat(activity-bar): ActivityBar dispatches Narrow/Wide from store"
```

---

## Task 14 — `ActivityBarWide` 加入 resize 邊界

**Files:**
- Modify: `spa/src/features/workspace/components/ActivityBarWide.tsx`
- Modify: `spa/src/features/workspace/components/ActivityBarWide.test.tsx`

- [ ] **Step 1: 寫失敗測試**

在 `ActivityBarWide.test.tsx` 新增：

```tsx
import { useLayoutStore } from '../../../stores/useLayoutStore'

it('renders a resize handle that updates activityBarWideSize', async () => {
  useLayoutStore.setState({ activityBarWideSize: 240 })
  render(
    <ActivityBarWide
      workspaces={[]}
      activeWorkspaceId={null}
      activeStandaloneTabId={null}
      onSelectWorkspace={() => {}}
      onSelectHome={() => {}}
      standaloneTabIds={[]}
      onAddWorkspace={() => {}}
      onOpenHosts={() => {}}
      onOpenSettings={() => {}}
    />,
  )
  const handle = document.querySelector('[data-testid="activity-bar-resize"]')
  expect(handle).toBeInTheDocument()
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/features/workspace/components/ActivityBarWide.test.tsx -t "resize handle"
```

- [ ] **Step 3: 實作 — 包裝 RegionResize 成右邊界 handle**

`ActivityBarWide.tsx` 修改：

1. 頂部加 import：
```tsx
import { RegionResize } from '../../../components/RegionResize'
```

2. 從 store 拿 setter：
```tsx
const setWideSize = useLayoutStore((s) => s.setActivityBarWideSize)
```

3. 最外層容器改為 Fragment 包兩個元素（activity bar 本體 + resize handle）：

```tsx
return (
  <>
    <div
      className="hidden lg:flex flex-col bg-surface-tertiary border-r border-border-subtle py-2 gap-1 flex-shrink-0"
      style={{ width: wideSize }}
    >
      {/* 現有內容 */}
    </div>
    <div data-testid="activity-bar-resize" className="hidden lg:block">
      <RegionResize
        resizeEdge="right"
        onResize={(delta) => setWideSize(wideSize + delta)}
      />
    </div>
  </>
)
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/features/workspace/components/ActivityBarWide.test.tsx
```

- [ ] **Step 5: 手動驗證**

```bash
cd spa && pnpm run dev
```

UI 上切 wide（點 CollapseButton），拖右邊界應可改寬度並持久化（reload 後保留）。

- [ ] **Step 6: Commit**

```bash
git add spa/src/features/workspace/components/ActivityBarWide.tsx spa/src/features/workspace/components/ActivityBarWide.test.tsx
git commit -m "feat(activity-bar): add drag-resize handle on wide variant"
```

---

## Task 15 — 最終整合測試 + 回歸驗證

**Files:**
- Test only（不改產品碼）

- [ ] **Step 1: 跑所有 workspace 相關測試**

```bash
cd spa && npx vitest run src/features/workspace/components
```

- [ ] **Step 2: 跑 store 測試**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts
```

- [ ] **Step 3: 跑 i18n completeness**

```bash
cd spa && npx vitest run src/locales/locale-completeness.test.ts
```

- [ ] **Step 4: 全專案 lint**

```bash
cd spa && pnpm run lint
```

- [ ] **Step 5: 全專案 build**

```bash
cd spa && pnpm run build
```

- [ ] **Step 6: 手動 smoke test**

```bash
cd spa && pnpm run dev
```

驗證：
1. 預設窄版、既有行為不變
2. 點右下 CollapseButton：容器變寬、顯示 workspace 名稱、reload 後保留
3. 拖右邊界：寬度改變、reload 後保留
4. 再按 CollapseButton：回到窄版
5. 建立 workspace → 展開 chevron（此時因 Phase 2 還沒做所以沒展開 UI，跳過）→ 刪除該 workspace → `localStorage.getItem('purdex-layout')` 內 `workspaceExpanded` 無殘留
6. 多視窗情境（若可開兩個 Electron window）：一邊切 wide，另一邊 5 秒內自動同步（`syncManager` 機制）

- [ ] **Step 7: 標記 Phase 1 完成（在 CHANGELOG 或 VERSION bump，依專案規範）**

依 `CLAUDE.md`：PR merge 後更新 `VERSION` + `CHANGELOG.md`。Phase 1 PR 合併時才更新，此 plan 執行階段不動。

---

## 最終檢查清單

- [ ] 所有 vitest 測試綠
- [ ] `pnpm run lint` 綠
- [ ] `pnpm run build` 綠
- [ ] 手動 smoke test 六項全數過
- [ ] 既有 `ActivityBar.test.tsx` 不回歸
- [ ] `localStorage.purdex-layout` 新 key 正確持久化
- [ ] `syncManager` 跨視窗同步正常

---

## 未涵蓋項目（明確交給 Phase 2 / 3）

- `setTabPosition` setter（Phase 2）
- Settings → Appearance 的 tab position radio（Phase 2）
- `WorkspaceRow` / `InlineTabList` / `InlineTab`（Phase 2）
- 頂層 `DndContext`（Phase 2 建立）
- 頂部 `<TabBar />` 條件隱藏（Phase 2）
- 跨 workspace 拖曳 + spring-load + pinned 禁止（Phase 3）
