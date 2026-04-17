# Phase 2 — Layout Modes: Tab Position + Inline Tabs

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓使用者可在 Settings 切換 tab 位置到左側（`tabPosition='left'`），啟用寬版 activity bar 內 per-workspace 展開顯示 inline tabs，並支援同 workspace 內拖曳重排；`tabPosition='left'` 時頂部 `<TabBar />` 隱藏。

**Architecture:** 新增 `setTabPosition` store action（含 `left → wide` 耦合）；擴充 `AppearanceSection` 加 radio group。新建 `InlineTab` / `InlineTabList` / `WorkspaceRow` / `HomeRow` 元件；`ActivityBarWide` 改寫為協調者，建立**頂層 `DndContext`** 同時管 workspace reorder（外層 SortableContext）與同 ws tab reorder（per-ws 內層 SortableContext）。`onDragEnd` 依 `active.data.current.type` 分派到 `reorderWorkspaces` 或 `reorderWorkspaceTabs`，**僅允許同 ws 拖曳**（target 不在同 sourceWsId 則放棄，留待 Phase 3 擴充跨 ws）。`App.tsx` 依 `tabPosition` 條件渲染 `<TabBar />`、傳遞 tab handlers 給 `ActivityBar`；新增 `handleAddTabToWorkspace(wsId)` 與 `handleReorderWorkspaceTabs(wsId, ids)` handler。

**Tech Stack:** React 19, Zustand 5, `@dnd-kit/core` + `@dnd-kit/sortable` + `@dnd-kit/modifiers`, Phosphor Icons, Tailwind 4, Vitest + @testing-library/react.

**Prerequisite:** 本計畫應在獨立 worktree 執行（依 `CLAUDE.md` 規範使用 `EnterWorktree`）。已以 worktree `phase2-layout-modes` 開啟。

---

## 前置閱讀

- Spec：`docs/superpowers/specs/2026-04-17-layout-modes-design.md`
- Phase 1 Plan：`docs/superpowers/plans/2026-04-17-phase1-layout-modes.md`
- 既有 store：`spa/src/stores/useLayoutStore.ts`（已有 `activityBarWidth` / `tabPosition` / `activityBarWideSize` / `workspaceExpanded`，以及 `HOME_WS_KEY = 'home'` 常數）
- 既有 workspace store：`spa/src/features/workspace/store.ts`（`reorderWorkspaceTabs`、`insertTab`、`addTabToWorkspace` 等）
- 既有 DnD 使用：
  - `spa/src/features/workspace/components/ActivityBarNarrow.tsx`（workspace vertical DnD，**Phase 2 不動**）
  - `spa/src/components/TabBar.tsx` / `spa/src/components/SortableTab.tsx`（頂部水平 tab DnD）
- Activity bar props：`spa/src/features/workspace/components/activity-bar-props.ts`
- 既有 Wide：`spa/src/features/workspace/components/ActivityBarWide.tsx`（Phase 1 版，無 DnD、無 tab）
- `AppearanceSection.tsx` 模式（`SettingItem` + label/desc + input）
- 現有 tab handler：`spa/src/features/workspace/hooks.ts`（`handleAddTab` 建 standalone tab 模式）

## 檔案影響總覽

**Modify:**
- `spa/src/stores/useLayoutStore.ts`（加 `setTabPosition` action）
- `spa/src/stores/useLayoutStore.test.ts`
- `spa/src/features/workspace/components/activity-bar-props.ts`（擴充 ActivityBarProps）
- `spa/src/features/workspace/hooks.ts`（新增 `handleAddTabToWorkspace` + `handleReorderWorkspaceTabs`）
- `spa/src/features/workspace/hooks.test.ts`
- `spa/src/App.tsx`（傳新 props 到 `ActivityBar` + 條件渲染 `TabBar`）
- `spa/src/components/settings/AppearanceSection.tsx`（加 tab position radio）
- `spa/src/features/workspace/components/ActivityBarWide.tsx`（重構為協調 DndContext + 使用 HomeRow / WorkspaceRow）
- `spa/src/features/workspace/components/ActivityBarWide.test.tsx`
- `spa/src/features/workspace/components/ActivityBarNarrow.tsx`（接收新 optional props，但 Phase 2 可不使用）
- `spa/src/locales/en.json`
- `spa/src/locales/zh-TW.json`

**Create:**
- `spa/src/features/workspace/components/InlineTab.tsx`
- `spa/src/features/workspace/components/InlineTab.test.tsx`
- `spa/src/features/workspace/components/InlineTabList.tsx`
- `spa/src/features/workspace/components/InlineTabList.test.tsx`
- `spa/src/features/workspace/components/WorkspaceRow.tsx`
- `spa/src/features/workspace/components/WorkspaceRow.test.tsx`
- `spa/src/features/workspace/components/HomeRow.tsx`
- `spa/src/features/workspace/components/HomeRow.test.tsx`

---

## Task 1 — Store: `setTabPosition` setter（含 `left → wide` 耦合）

**Files:**
- Modify: `spa/src/stores/useLayoutStore.ts`
- Test: `spa/src/stores/useLayoutStore.test.ts`

- [ ] **Step 1: 寫失敗測試**

在 `spa/src/stores/useLayoutStore.test.ts` 的 `describe('useLayoutStore', ...)` 區塊新增：

```ts
describe('setTabPosition', () => {
  it('sets to left and forces activityBarWidth=wide', () => {
    useLayoutStore.setState({ activityBarWidth: 'narrow', tabPosition: 'top' })
    useLayoutStore.getState().setTabPosition('left')
    expect(useLayoutStore.getState().tabPosition).toBe('left')
    expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
  })

  it('sets to top without changing activityBarWidth (wide stays)', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
    useLayoutStore.getState().setTabPosition('top')
    expect(useLayoutStore.getState().tabPosition).toBe('top')
    expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
  })

  it('sets to top without changing activityBarWidth (narrow stays narrow)', () => {
    useLayoutStore.setState({ activityBarWidth: 'narrow', tabPosition: 'top' })
    useLayoutStore.getState().setTabPosition('top')
    expect(useLayoutStore.getState().tabPosition).toBe('top')
    expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts -t "setTabPosition"
```

預期：fail，原因 `setTabPosition is not a function`。

- [ ] **Step 3: 實作**

`spa/src/stores/useLayoutStore.ts` 的 `LayoutState` interface 新增（放在 `reconcileWorkspaceExpanded` 旁）：

```ts
setTabPosition: (position: TabPosition) => void
```

`create()` 內部實作（放在 `toggleActivityBarWidth` 之後）：

```ts
setTabPosition: (position) =>
  set((state) => {
    if (position === 'left') {
      return { tabPosition: 'left', activityBarWidth: 'wide' }
    }
    return { tabPosition: 'top' }
  }),
```

- [ ] **Step 4: 執行測試，確認 PASS（全綠不回退既有測試）**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useLayoutStore.ts spa/src/stores/useLayoutStore.test.ts
git commit -m "feat(layout): setTabPosition with left→wide coupling"
```

---

## Task 2 — i18n keys（tab position radio + empty workspace）

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: 加入英文 keys**

`spa/src/locales/en.json` 在現有 `"nav.expand_activity_bar"` 下方插入：

```json
  "nav.workspace_empty": "No tabs yet",
  "nav.add_tab_to_workspace": "New tab in {name}",
  "settings.appearance.tab_position.label": "Tab Position",
  "settings.appearance.tab_position.desc": "Where to display the open tabs",
  "settings.appearance.tab_position.top": "Top",
  "settings.appearance.tab_position.left": "Left (activity bar)",
  "settings.appearance.tab_position.left_hint": "Left position keeps the activity bar wide",
```

- [ ] **Step 2: 加入繁中 keys**

`spa/src/locales/zh-TW.json` 對應位置插入：

```json
  "nav.workspace_empty": "尚無分頁",
  "nav.add_tab_to_workspace": "在 {name} 開新分頁",
  "settings.appearance.tab_position.label": "分頁位置",
  "settings.appearance.tab_position.desc": "開啟的分頁顯示在哪裡",
  "settings.appearance.tab_position.top": "上方",
  "settings.appearance.tab_position.left": "左側（側邊欄內）",
  "settings.appearance.tab_position.left_hint": "選擇左側會讓側邊欄維持寬版",
```

- [ ] **Step 3: 執行 locale completeness**

```bash
cd spa && npx vitest run src/locales/locale-completeness.test.ts
```

預期：綠。

- [ ] **Step 4: Commit**

```bash
git add spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "i18n: add tab position + inline tabs strings"
```

---

## Task 3 — `AppearanceSection` 加 Tab Position radio

**Files:**
- Modify: `spa/src/components/settings/AppearanceSection.tsx`

- [ ] **Step 1: 頂部 imports 加入**

在現有 imports 區塊補上：

```ts
import { useLayoutStore } from '../../stores/useLayoutStore'
import type { TabPosition } from '../../stores/useLayoutStore'
```

- [ ] **Step 2: 函式內讀 store**

`AppearanceSection()` 頂部加入（在 `const t = useI18nStore((s) => s.t)` 附近）：

```ts
const tabPosition = useLayoutStore((s) => s.tabPosition)
const setTabPosition = useLayoutStore((s) => s.setTabPosition)
```

並加 handler：

```ts
const handleTabPositionChange = (e: React.ChangeEvent<HTMLInputElement>) => {
  setTabPosition(e.target.value as TabPosition)
}
```

- [ ] **Step 3: 加入 Tab Position SettingItem**

在 `{/* Language selector */}` 那個 `SettingItem` 結束 `</SettingItem>` **之後**、`{/* Locale customize + import */}` 之前，插入：

```tsx
<SettingItem
  label={t('settings.appearance.tab_position.label')}
  description={t('settings.appearance.tab_position.desc')}
>
  <div className="flex flex-col gap-1.5">
    <label className="flex items-center gap-2 text-xs text-text-primary cursor-pointer">
      <input
        type="radio"
        name="tab-position"
        value="top"
        checked={tabPosition === 'top'}
        onChange={handleTabPositionChange}
        className="accent-purple-500"
      />
      {t('settings.appearance.tab_position.top')}
    </label>
    <label className="flex items-center gap-2 text-xs text-text-primary cursor-pointer">
      <input
        type="radio"
        name="tab-position"
        value="left"
        checked={tabPosition === 'left'}
        onChange={handleTabPositionChange}
        className="accent-purple-500"
      />
      {t('settings.appearance.tab_position.left')}
    </label>
    <p className="text-[11px] text-text-muted mt-0.5">
      {t('settings.appearance.tab_position.left_hint')}
    </p>
  </div>
</SettingItem>
```

- [ ] **Step 4: 手動驗證**

```bash
cd spa && pnpm run dev
```

開 Settings → Appearance：
- 看到 Tab Position，預設選 Top
- 切 Left：activity bar 立刻變寬版（因 `setTabPosition('left')` 耦合 `activityBarWidth='wide'`）
- CollapseButton 變 disabled（Phase 1 已實作）
- 切回 Top：activity bar 保持 wide，CollapseButton 恢復可用

- [ ] **Step 5: 執行全測試確保不回退**

```bash
cd spa && npx vitest run
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/settings/AppearanceSection.tsx
git commit -m "feat(settings): add Tab Position radio in Appearance"
```

---

## Task 4 — 擴充 `ActivityBarProps` 與新增 App 層 handlers

**Files:**
- Modify: `spa/src/features/workspace/components/activity-bar-props.ts`
- Modify: `spa/src/features/workspace/hooks.ts`
- Test: `spa/src/features/workspace/hooks.test.ts`
- Modify: `spa/src/App.tsx`

**說明：** 新 props 全 optional，`ActivityBarNarrow` 不處理也不會壞（Narrow 模式 Phase 2 不用）。

- [ ] **Step 1: 擴充 `activity-bar-props.ts`**

完整替換檔案為：

```ts
import type { Workspace, Tab } from '../../../types/tab'

export interface ActivityBarProps {
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

  // Phase 2 additions — only used by ActivityBarWide when tabPosition='left'
  tabsById?: Record<string, Tab>
  activeTabId?: string | null
  onSelectTab?: (tabId: string) => void
  onCloseTab?: (tabId: string) => void
  onMiddleClickTab?: (tabId: string) => void
  onContextMenuTab?: (e: React.MouseEvent, tabId: string) => void
  onReorderWorkspaceTabs?: (wsId: string, tabIds: string[]) => void
  onReorderStandaloneTabs?: (tabIds: string[]) => void
  onAddTabToWorkspace?: (wsId: string) => void
}
```

- [ ] **Step 2: 寫失敗測試 — `handleAddTabToWorkspace`**

**注意：** 既有 `hooks.test.ts` 已 import `renderHook`, `act`, `useTabWorkspaceActions`, `useWorkspaceStore`（from `./store`）, `useTabStore`。不要重複 import。

在檔案末端加入新 describe：

```ts
describe('handleAddTabToWorkspace', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('creates a tab, adds to tab store, and inserts into given workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('A')

    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => {
      result.current.handleAddTabToWorkspace(ws.id)
    })

    const updated = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!
    expect(updated.tabs.length).toBe(1)
    const newTabId = updated.tabs[0]
    expect(updated.activeTabId).toBe(newTabId)
    expect(useTabStore.getState().tabs[newTabId]).toBeDefined()
    expect(useTabStore.getState().activeTabId).toBe(newTabId)
  })
})

describe('handleReorderWorkspaceTabs', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('delegates to workspace store reorderWorkspaceTabs', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('A')
    const t1 = createTab({ kind: 'new-tab' })
    const t2 = createTab({ kind: 'new-tab' })
    const t3 = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(t1)
    useTabStore.getState().addTab(t2)
    useTabStore.getState().addTab(t3)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t2.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t3.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => {
      result.current.handleReorderWorkspaceTabs(ws.id, [t2.id, t1.id, t3.id])
    })

    const updated = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!
    expect(updated.tabs).toEqual([t2.id, t1.id, t3.id])
  })
})
```

- [ ] **Step 3: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/features/workspace/hooks.test.ts -t "handleAddTabToWorkspace"
```

預期：fail，`result.current.handleAddTabToWorkspace is not a function`。

- [ ] **Step 4: 實作**

在 `spa/src/features/workspace/hooks.ts` 現有 `handleAddTab` 之後新增：

```ts
const handleAddTabToWorkspace = useCallback((wsId: string) => {
  const tab = createTab({ kind: 'new-tab' })
  addTab(tab)
  setActiveTab(tab.id)
  useWorkspaceStore.getState().insertTab(tab.id, wsId)
}, [addTab, setActiveTab])

const handleReorderWorkspaceTabs = useCallback((wsId: string, tabIds: string[]) => {
  useWorkspaceStore.getState().reorderWorkspaceTabs(wsId, tabIds)
}, [])
```

並在 return object 末端（`handleAddTab` 旁）加入：

```ts
handleAddTabToWorkspace,
handleReorderWorkspaceTabs,
```

（注意 TypeScript return type 推斷：若該 hook 有顯式回傳型別，需同步擴充。此 hook 是物件 return，型別推斷會自動涵蓋。）

- [ ] **Step 5: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/features/workspace/hooks.test.ts
```

- [ ] **Step 6: 在 `App.tsx` 解構 + 傳給 `ActivityBar`**

`App.tsx` 把 `useTabWorkspaceActions(displayTabs)` 的解構擴充（約 line 107-125）：

```ts
const {
  contextMenu,
  setContextMenu,
  contextMenuHasRightUnlocked,
  handleSelectWorkspace,
  handleSelectTab,
  handleCloseTab,
  handleAddTab,
  handleAddTabToWorkspace,
  handleReorderTabs,
  handleReorderWorkspaceTabs,
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

在現有 `handleReorderTabs` 用來 reorder 全部 tabs；但 Phase 2 `HomeRow` 的 standalone tab reorder 需要**只重排 standalone 子集合**。新增在 `App.tsx`（`handleReorderWorkspaces` 附近）：

```ts
const handleReorderStandaloneTabs = useCallback((newOrder: string[]) => {
  // newOrder 是 standalone tab id 新順序；以 newOrder 取代原本 tabOrder 中的 standalone 區段
  const current = useTabStore.getState().tabOrder
  const standaloneSet = new Set(newOrder)
  const result: string[] = []
  let insertIndex = -1
  for (let i = 0; i < current.length; i++) {
    const id = current[i]
    if (standaloneSet.has(id)) {
      if (insertIndex === -1) insertIndex = result.length
      // skip; handled below
    } else {
      result.push(id)
    }
  }
  if (insertIndex === -1) insertIndex = result.length
  result.splice(insertIndex, 0, ...newOrder)
  useTabStore.getState().reorderTabs(result)
}, [])
```

在 `<ActivityBar>` 的 props 加入：

```tsx
<ActivityBar
  workspaces={workspaces}
  activeWorkspaceId={activeStandaloneTabId ? null : activeWorkspaceId}
  activeStandaloneTabId={activeStandaloneTabId}
  onSelectWorkspace={handleSelectWorkspace}
  onSelectHome={handleSelectHome}
  standaloneTabIds={standaloneTabIds}
  onAddWorkspace={handleAddWorkspace}
  onReorderWorkspaces={handleReorderWorkspaces}
  onContextMenuWorkspace={handleWsContextMenu}
  onOpenHosts={handleOpenHosts}
  onOpenSettings={handleOpenSettings}
  // Phase 2
  tabsById={tabs}
  activeTabId={activeTabId}
  onSelectTab={handleSelectTab}
  onCloseTab={handleCloseTab}
  onMiddleClickTab={handleMiddleClick}
  onContextMenuTab={handleContextMenu}
  onReorderWorkspaceTabs={handleReorderWorkspaceTabs}
  onReorderStandaloneTabs={handleReorderStandaloneTabs}
  onAddTabToWorkspace={handleAddTabToWorkspace}
/>
```

- [ ] **Step 7: 型別檢查**

```bash
cd spa && pnpm run build
```

預期：build 綠。

- [ ] **Step 8: Commit**

```bash
git add spa/src/features/workspace/components/activity-bar-props.ts spa/src/features/workspace/hooks.ts spa/src/features/workspace/hooks.test.ts spa/src/App.tsx
git commit -m "feat(activity-bar): extend props + app handlers for inline tabs"
```

---

## Task 5 — `InlineTab` 元件

**Files:**
- Create: `spa/src/features/workspace/components/InlineTab.tsx`
- Create: `spa/src/features/workspace/components/InlineTab.test.tsx`

**說明：** 垂直列版本的 tab 行：icon + title（左對齊 truncate）+ close 按鈕。active 時高亮。使用 `useSortable` 讓外層 `SortableContext` 可重排。

- [ ] **Step 1: 寫失敗測試**

建立 `spa/src/features/workspace/components/InlineTab.test.tsx`：

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { SortableContext } from '@dnd-kit/sortable'
import { InlineTab } from './InlineTab'
import type { Tab } from '../../../types/tab'

function renderWith(tab: Tab, overrides: Partial<React.ComponentProps<typeof InlineTab>> = {}) {
  return render(
    <DndContext>
      <SortableContext items={[tab.id]}>
        <InlineTab
          tab={tab}
          isActive={false}
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
          {...overrides}
        />
      </SortableContext>
    </DndContext>,
  )
}

const mkTab = (overrides: Partial<Tab> = {}): Tab =>
  ({
    id: 't1',
    title: 'Untitled',
    kind: 'new-tab',
    locked: false,
    layout: { type: 'single' } as Tab['layout'],
    ...overrides,
  }) as Tab

describe('InlineTab', () => {
  it('renders tab title', () => {
    renderWith(mkTab({ title: 'My Tab' }))
    expect(screen.getByText('My Tab')).toBeInTheDocument()
  })

  it('click triggers onSelect', () => {
    const onSelect = vi.fn()
    renderWith(mkTab(), { onSelect })
    fireEvent.click(screen.getByText('Untitled'))
    expect(onSelect).toHaveBeenCalledWith('t1')
  })

  it('close button triggers onClose and stops propagation', () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    renderWith(mkTab(), { onSelect, onClose })
    fireEvent.click(screen.getByRole('button', { name: /close/i }))
    expect(onClose).toHaveBeenCalledWith('t1')
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('active state adds a purple ring class', () => {
    const { container } = renderWith(mkTab(), { isActive: true })
    const row = container.querySelector('[data-testid="inline-tab-row"]')!
    expect(row.className).toMatch(/ring/)
  })

  it('middle click triggers onMiddleClick', () => {
    const onMiddleClick = vi.fn()
    renderWith(mkTab(), { onMiddleClick })
    const row = screen.getByText('Untitled').closest('[data-testid="inline-tab-row"]')!
    fireEvent.mouseDown(row, { button: 1 })
    expect(onMiddleClick).toHaveBeenCalledWith('t1')
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/features/workspace/components/InlineTab.test.tsx
```

- [ ] **Step 3: 實作**

建立 `spa/src/features/workspace/components/InlineTab.tsx`：

```tsx
import { X } from '@phosphor-icons/react'
import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { Tab } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  tab: Tab
  isActive: boolean
  sourceWsId?: string | null
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
}

export function InlineTab({
  tab,
  isActive,
  sourceWsId = null,
  onSelect,
  onClose,
  onMiddleClick,
  onContextMenu,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: tab.id,
    data: { type: 'tab', tabId: tab.id, sourceWsId },
  })

  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  const handleCloseClick = (e: React.MouseEvent) => {
    e.stopPropagation()
    onClose(tab.id)
  }

  const handleMouseDown = (e: React.MouseEvent) => {
    if (e.button === 1) {
      e.preventDefault()
      onMiddleClick(tab.id)
    }
  }

  return (
    <div
      ref={setNodeRef}
      style={style}
      data-testid="inline-tab-row"
      {...attributes}
      {...listeners}
      role="button"
      tabIndex={0}
      onClick={() => onSelect(tab.id)}
      onMouseDown={handleMouseDown}
      onContextMenu={(e) => onContextMenu(e, tab.id)}
      className={`group flex items-center gap-2 mx-2 pl-5 pr-1.5 py-1 rounded-md text-xs cursor-pointer transition-colors ${
        isActive
          ? 'bg-surface-hover text-text-primary ring-1 ring-purple-400/60'
          : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
      }`}
    >
      <span className="flex-1 truncate" title={tab.title}>
        {tab.title || t('nav.new_tab')}
      </span>
      <button
        type="button"
        aria-label={`Close ${tab.title}`}
        title={t('common.close')}
        onClick={handleCloseClick}
        onMouseDown={(e) => e.stopPropagation()}
        className="opacity-0 group-hover:opacity-100 rounded p-0.5 hover:bg-surface-secondary hover:text-text-primary"
      >
        <X size={12} />
      </button>
    </div>
  )
}
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/features/workspace/components/InlineTab.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/InlineTab.tsx spa/src/features/workspace/components/InlineTab.test.tsx
git commit -m "feat(activity-bar): add InlineTab vertical tab row"
```

---

## Task 6 — `InlineTabList` 元件

**Files:**
- Create: `spa/src/features/workspace/components/InlineTabList.tsx`
- Create: `spa/src/features/workspace/components/InlineTabList.test.tsx`

**說明：** 接收 `tabIds: string[]` + `tabsById` lookup，輸出 per-ws `SortableContext` 包住 InlineTab 清單；空陣列顯示「尚無分頁」。本元件**不含 DndContext**（由 ActivityBarWide 頂層提供），只有內層 SortableContext。

- [ ] **Step 1: 寫失敗測試**

建立 `spa/src/features/workspace/components/InlineTabList.test.tsx`：

```tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { InlineTabList } from './InlineTabList'
import type { Tab } from '../../../types/tab'

const mkTab = (id: string, title: string): Tab =>
  ({ id, title, kind: 'new-tab', locked: false, layout: { type: 'single' } }) as Tab

describe('InlineTabList', () => {
  it('renders empty state when tabIds is empty', () => {
    render(
      <DndContext>
        <InlineTabList
          tabIds={[]}
          tabsById={{}}
          activeTabId={null}
          sourceWsId="ws-1"
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
        />
      </DndContext>,
    )
    expect(screen.getByText(/no tabs yet/i)).toBeInTheDocument()
  })

  it('renders tabs in the given order', () => {
    render(
      <DndContext>
        <InlineTabList
          tabIds={['a', 'b']}
          tabsById={{ a: mkTab('a', 'Alpha'), b: mkTab('b', 'Beta') }}
          activeTabId="a"
          sourceWsId="ws-1"
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
        />
      </DndContext>,
    )
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Beta')).toBeInTheDocument()
  })

  it('skips ids with no matching tab entry', () => {
    render(
      <DndContext>
        <InlineTabList
          tabIds={['a', 'missing']}
          tabsById={{ a: mkTab('a', 'Alpha') }}
          activeTabId={null}
          sourceWsId="ws-1"
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
        />
      </DndContext>,
    )
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.queryByText('missing')).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/features/workspace/components/InlineTabList.test.tsx
```

- [ ] **Step 3: 實作**

建立 `spa/src/features/workspace/components/InlineTabList.tsx`：

```tsx
import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable'
import type { Tab } from '../../../types/tab'
import { InlineTab } from './InlineTab'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  tabIds: string[]
  tabsById: Record<string, Tab>
  activeTabId: string | null
  sourceWsId: string | null
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
}

export function InlineTabList({
  tabIds,
  tabsById,
  activeTabId,
  sourceWsId,
  onSelect,
  onClose,
  onMiddleClick,
  onContextMenu,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const validIds = tabIds.filter((id) => !!tabsById[id])

  if (validIds.length === 0) {
    return (
      <div className="pl-7 pr-3 py-1 text-[11px] text-text-muted italic">
        {t('nav.workspace_empty')}
      </div>
    )
  }

  return (
    <SortableContext items={validIds} strategy={verticalListSortingStrategy}>
      <div className="flex flex-col gap-0.5 py-0.5">
        {validIds.map((id) => (
          <InlineTab
            key={id}
            tab={tabsById[id]}
            isActive={activeTabId === id}
            sourceWsId={sourceWsId}
            onSelect={onSelect}
            onClose={onClose}
            onMiddleClick={onMiddleClick}
            onContextMenu={onContextMenu}
          />
        ))}
      </div>
    </SortableContext>
  )
}
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/features/workspace/components/InlineTabList.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/InlineTabList.tsx spa/src/features/workspace/components/InlineTabList.test.tsx
git commit -m "feat(activity-bar): add InlineTabList per-ws SortableContext"
```

---

## Task 7 — `WorkspaceRow` 元件

**Files:**
- Create: `spa/src/features/workspace/components/WorkspaceRow.tsx`
- Create: `spa/src/features/workspace/components/WorkspaceRow.test.tsx`

**說明：** Header（icon + name + 展開 chevron + context menu）+ 可選展開區（`InlineTabList` + `+` 按鈕）。Header 本身透過 `useSortable({id: workspaceId, data: {type:'workspace'}})` 供頂層 SortableContext 重排。

- [ ] **Step 1: 寫失敗測試**

建立 `spa/src/features/workspace/components/WorkspaceRow.test.tsx`：

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { SortableContext } from '@dnd-kit/sortable'
import { WorkspaceRow } from './WorkspaceRow'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import type { Workspace, Tab } from '../../../types/tab'

const mkWs = (id: string, name: string, tabs: string[] = []): Workspace => ({
  id,
  name,
  tabs,
  activeTabId: null,
})

const mkTab = (id: string, title: string): Tab =>
  ({ id, title, kind: 'new-tab', locked: false, layout: { type: 'single' } }) as Tab

beforeEach(() => {
  cleanup()
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

function renderRow(ws: Workspace, overrides: Partial<React.ComponentProps<typeof WorkspaceRow>> = {}) {
  return render(
    <DndContext>
      <SortableContext items={[ws.id]}>
        <WorkspaceRow
          workspace={ws}
          isActive={false}
          tabsById={{}}
          activeTabId={null}
          onSelectWorkspace={() => {}}
          onContextMenuWorkspace={() => {}}
          onSelectTab={() => {}}
          onCloseTab={() => {}}
          onMiddleClickTab={() => {}}
          onContextMenuTab={() => {}}
          onAddTabToWorkspace={() => {}}
          {...overrides}
        />
      </SortableContext>
    </DndContext>,
  )
}

describe('WorkspaceRow', () => {
  it('renders workspace name', () => {
    renderRow(mkWs('ws-1', 'Purdex'))
    expect(screen.getByText('Purdex')).toBeInTheDocument()
  })

  it('header click selects workspace', () => {
    const onSelect = vi.fn()
    renderRow(mkWs('ws-1', 'Purdex'), { onSelectWorkspace: onSelect })
    fireEvent.click(screen.getByText('Purdex'))
    expect(onSelect).toHaveBeenCalledWith('ws-1')
  })

  it('tabs hidden when workspaceExpanded[id] is false/undefined', () => {
    renderRow(mkWs('ws-1', 'W', ['t1']), { tabsById: { t1: mkTab('t1', 'Tab One') } })
    expect(screen.queryByText('Tab One')).not.toBeInTheDocument()
  })

  it('tabs shown when workspaceExpanded[id]=true', () => {
    useLayoutStore.setState({ workspaceExpanded: { 'ws-1': true } })
    renderRow(mkWs('ws-1', 'W', ['t1']), { tabsById: { t1: mkTab('t1', 'Tab One') } })
    expect(screen.getByText('Tab One')).toBeInTheDocument()
  })

  it('chevron toggles expand state', () => {
    renderRow(mkWs('ws-1', 'W', ['t1']), { tabsById: { t1: mkTab('t1', 'Tab One') } })
    const chevron = screen.getByRole('button', { name: /expand|collapse/i })
    fireEvent.click(chevron)
    expect(useLayoutStore.getState().workspaceExpanded['ws-1']).toBe(true)
  })

  it('+ button visible when expanded, calls onAddTabToWorkspace', () => {
    useLayoutStore.setState({ workspaceExpanded: { 'ws-1': true } })
    const onAdd = vi.fn()
    renderRow(mkWs('ws-1', 'W', []), { onAddTabToWorkspace: onAdd })
    const addBtn = screen.getByRole('button', { name: /new tab in W/i })
    fireEvent.click(addBtn)
    expect(onAdd).toHaveBeenCalledWith('ws-1')
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceRow.test.tsx
```

- [ ] **Step 3: 實作**

建立 `spa/src/features/workspace/components/WorkspaceRow.tsx`：

```tsx
import { CaretRight, CaretDown, Plus } from '@phosphor-icons/react'
import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { Workspace, Tab } from '../../../types/tab'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { WorkspaceIcon } from './WorkspaceIcon'
import { InlineTabList } from './InlineTabList'

interface Props {
  workspace: Workspace
  isActive: boolean
  tabsById: Record<string, Tab>
  activeTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onSelectTab: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onMiddleClickTab: (tabId: string) => void
  onContextMenuTab: (e: React.MouseEvent, tabId: string) => void
  onAddTabToWorkspace: (wsId: string) => void
}

export function WorkspaceRow(props: Props) {
  const {
    workspace,
    isActive,
    tabsById,
    activeTabId,
    onSelectWorkspace,
    onContextMenuWorkspace,
    onSelectTab,
    onCloseTab,
    onMiddleClickTab,
    onContextMenuTab,
    onAddTabToWorkspace,
  } = props
  const t = useI18nStore((s) => s.t)
  const expanded = useLayoutStore((s) => !!s.workspaceExpanded[workspace.id])
  const toggleExpanded = useLayoutStore((s) => s.toggleWorkspaceExpanded)

  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: workspace.id,
    data: { type: 'workspace', wsId: workspace.id },
  })

  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  const Chevron = expanded ? CaretDown : CaretRight
  const chevronLabel = expanded
    ? `Collapse ${workspace.name}`
    : `Expand ${workspace.name}`

  return (
    <div ref={setNodeRef} style={style} className="flex flex-col">
      <div
        {...attributes}
        {...listeners}
        className={`mx-2 flex items-center gap-1 pr-1.5 rounded-md text-sm transition-colors ${
          isActive
            ? 'bg-[#8b5cf6]/25 text-text-primary ring-1 ring-purple-400'
            : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        }`}
      >
        <button
          type="button"
          aria-label={chevronLabel}
          aria-expanded={expanded}
          onClick={(e) => {
            e.stopPropagation()
            toggleExpanded(workspace.id)
          }}
          onMouseDown={(e) => e.stopPropagation()}
          className="p-1 rounded hover:bg-surface-secondary text-text-muted cursor-pointer"
        >
          <Chevron size={12} />
        </button>
        <button
          type="button"
          onClick={() => onSelectWorkspace(workspace.id)}
          onContextMenu={(e) => {
            e.preventDefault()
            onContextMenuWorkspace?.(e, workspace.id)
          }}
          className="flex-1 flex items-center gap-2 py-1.5 text-left cursor-pointer"
        >
          <WorkspaceIcon
            icon={workspace.icon}
            name={workspace.name}
            size={16}
            weight={workspace.iconWeight}
          />
          <span className="truncate" title={workspace.name}>
            {workspace.name}
          </span>
        </button>
      </div>

      {expanded && (
        <div className="flex flex-col">
          <InlineTabList
            tabIds={workspace.tabs}
            tabsById={tabsById}
            activeTabId={activeTabId}
            sourceWsId={workspace.id}
            onSelect={onSelectTab}
            onClose={onCloseTab}
            onMiddleClick={onMiddleClickTab}
            onContextMenu={onContextMenuTab}
          />
          <button
            type="button"
            aria-label={t('nav.add_tab_to_workspace').replace('{name}', workspace.name)}
            title={t('nav.add_tab_to_workspace').replace('{name}', workspace.name)}
            onClick={() => onAddTabToWorkspace(workspace.id)}
            className="mx-2 pl-5 pr-1.5 py-1 rounded-md text-xs text-text-muted hover:bg-surface-hover hover:text-text-primary flex items-center gap-1.5 cursor-pointer"
          >
            <Plus size={12} />
            <span>{t('nav.new_tab')}</span>
          </button>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/features/workspace/components/WorkspaceRow.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/WorkspaceRow.tsx spa/src/features/workspace/components/WorkspaceRow.test.tsx
git commit -m "feat(activity-bar): add WorkspaceRow with expand/collapse + inline tabs"
```

---

## Task 8 — `HomeRow` 元件

**Files:**
- Create: `spa/src/features/workspace/components/HomeRow.tsx`
- Create: `spa/src/features/workspace/components/HomeRow.test.tsx`

**說明：** 類似 WorkspaceRow 但不 sortable（Home 永遠在最上方），tabs = standalone tabs，key `'home'`。不含 `+` 按鈕（新 standalone tab 從別處建）。使用 `useLayoutStore` 讀 `workspaceExpanded['home']`。

- [ ] **Step 1: 寫失敗測試**

建立 `spa/src/features/workspace/components/HomeRow.test.tsx`：

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { HomeRow } from './HomeRow'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import type { Tab } from '../../../types/tab'

const mkTab = (id: string, title: string): Tab =>
  ({ id, title, kind: 'new-tab', locked: false, layout: { type: 'single' } }) as Tab

beforeEach(() => {
  cleanup()
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

function renderRow(overrides: Partial<React.ComponentProps<typeof HomeRow>> = {}) {
  return render(
    <DndContext>
      <HomeRow
        isActive={false}
        standaloneTabIds={[]}
        tabsById={{}}
        activeTabId={null}
        onSelectHome={() => {}}
        onSelectTab={() => {}}
        onCloseTab={() => {}}
        onMiddleClickTab={() => {}}
        onContextMenuTab={() => {}}
        {...overrides}
      />
    </DndContext>,
  )
}

describe('HomeRow', () => {
  it('renders Home label', () => {
    renderRow()
    expect(screen.getByText(/home/i)).toBeInTheDocument()
  })

  it('header click calls onSelectHome', () => {
    const onSelectHome = vi.fn()
    renderRow({ onSelectHome })
    fireEvent.click(screen.getByText(/home/i))
    expect(onSelectHome).toHaveBeenCalled()
  })

  it('tabs hidden when home not expanded', () => {
    renderRow({ standaloneTabIds: ['t1'], tabsById: { t1: mkTab('t1', 'Solo') } })
    expect(screen.queryByText('Solo')).not.toBeInTheDocument()
  })

  it('tabs shown when workspaceExpanded["home"]=true', () => {
    useLayoutStore.setState({ workspaceExpanded: { home: true } })
    renderRow({ standaloneTabIds: ['t1'], tabsById: { t1: mkTab('t1', 'Solo') } })
    expect(screen.getByText('Solo')).toBeInTheDocument()
  })

  it('chevron toggles home expand state', () => {
    renderRow({ standaloneTabIds: ['t1'], tabsById: { t1: mkTab('t1', 'Solo') } })
    const chevron = screen.getByRole('button', { name: /expand|collapse/i })
    fireEvent.click(chevron)
    expect(useLayoutStore.getState().workspaceExpanded['home']).toBe(true)
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/features/workspace/components/HomeRow.test.tsx
```

- [ ] **Step 3: 實作**

建立 `spa/src/features/workspace/components/HomeRow.tsx`：

```tsx
import { CaretRight, CaretDown } from '@phosphor-icons/react'
import type { Tab } from '../../../types/tab'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { InlineTabList } from './InlineTabList'

const HOME_KEY = 'home'

interface Props {
  isActive: boolean
  standaloneTabIds: string[]
  tabsById: Record<string, Tab>
  activeTabId: string | null
  onSelectHome: () => void
  onSelectTab: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onMiddleClickTab: (tabId: string) => void
  onContextMenuTab: (e: React.MouseEvent, tabId: string) => void
}

export function HomeRow(props: Props) {
  const {
    isActive,
    standaloneTabIds,
    tabsById,
    activeTabId,
    onSelectHome,
    onSelectTab,
    onCloseTab,
    onMiddleClickTab,
    onContextMenuTab,
  } = props
  const t = useI18nStore((s) => s.t)
  const expanded = useLayoutStore((s) => !!s.workspaceExpanded[HOME_KEY])
  const toggleExpanded = useLayoutStore((s) => s.toggleWorkspaceExpanded)

  const Chevron = expanded ? CaretDown : CaretRight
  const chevronLabel = expanded ? 'Collapse Home' : 'Expand Home'

  return (
    <div className="flex flex-col">
      <div
        className={`mx-2 flex items-center gap-1 pr-1.5 rounded-md text-sm transition-colors ${
          isActive
            ? 'bg-surface-hover text-text-primary ring-1 ring-purple-400'
            : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        }`}
      >
        <button
          type="button"
          aria-label={chevronLabel}
          aria-expanded={expanded}
          onClick={(e) => {
            e.stopPropagation()
            toggleExpanded(HOME_KEY)
          }}
          className="p-1 rounded hover:bg-surface-secondary text-text-muted cursor-pointer"
        >
          <Chevron size={12} />
        </button>
        <button
          type="button"
          onClick={onSelectHome}
          className="flex-1 flex items-center gap-2 py-1.5 text-left cursor-pointer"
        >
          <img
            src="/icons/logo-transparent.png"
            alt=""
            width={16}
            height={16}
            className="rounded-sm"
          />
          <span className="truncate">{t('nav.home')}</span>
        </button>
      </div>

      {expanded && (
        <InlineTabList
          tabIds={standaloneTabIds}
          tabsById={tabsById}
          activeTabId={activeTabId}
          sourceWsId={null}
          onSelect={onSelectTab}
          onClose={onCloseTab}
          onMiddleClick={onMiddleClickTab}
          onContextMenu={onContextMenuTab}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: 執行測試，確認 PASS**

```bash
cd spa && npx vitest run src/features/workspace/components/HomeRow.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/HomeRow.tsx spa/src/features/workspace/components/HomeRow.test.tsx
git commit -m "feat(activity-bar): add HomeRow for standalone inline tabs"
```

---

## Task 9 — 重寫 `ActivityBarWide`：頂層 DndContext + WorkspaceRow/HomeRow

**Files:**
- Modify: `spa/src/features/workspace/components/ActivityBarWide.tsx`
- Modify: `spa/src/features/workspace/components/ActivityBarWide.test.tsx`

**說明：** Phase 1 的 Wide 是純 flex 列表、無 DnD；Phase 2 要建**頂層 `DndContext`** 同時支援 workspace reorder（外層 vertical `SortableContext`）與同 ws tab reorder（內層 per-ws `SortableContext` 已在 `WorkspaceRow` / `HomeRow` 提供）。`onDragEnd` 依 `active.data.current.type` 分派：
- `'workspace'` → `onReorderWorkspaces(newOrder)`
- `'tab'` → 若 `over.data.current?.type === 'tab'` 且同 `sourceWsId` → `onReorderWorkspaceTabs(wsId, newOrder)`（或 home → `onReorderStandaloneTabs`）；**跨 ws drop 直接放棄**（留給 Phase 3）
- Pinned tab 不特別處理（Phase 3 專管）

Resize handle 保留於寬版底部右邊（與 Phase 1 一致）。Bottom 控制區（Add / Hosts / Settings / CollapseButton）保留。

- [ ] **Step 1: 寫失敗測試（新增到既有檔案）**

在 `spa/src/features/workspace/components/ActivityBarWide.test.tsx` 新增：

```tsx
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'

describe('ActivityBarWide Phase 2 — inline tabs', () => {
  beforeEach(() => {
    cleanup()
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })

  it('renders WorkspaceRow per workspace', () => {
    render(
      <ActivityBarWide
        workspaces={[
          { id: 'w1', name: 'Alpha', tabs: [], activeTabId: null },
          { id: 'w2', name: 'Beta', tabs: [], activeTabId: null },
        ]}
        activeWorkspaceId="w1"
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
        tabsById={{}}
        activeTabId={null}
        onSelectTab={() => {}}
        onCloseTab={() => {}}
        onMiddleClickTab={() => {}}
        onContextMenuTab={() => {}}
        onReorderWorkspaceTabs={() => {}}
        onReorderStandaloneTabs={() => {}}
        onAddTabToWorkspace={() => {}}
      />,
    )
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Beta')).toBeInTheDocument()
  })

  it('shows expanded inline tabs when workspaceExpanded set', () => {
    useLayoutStore.setState({ workspaceExpanded: { w1: true } })
    render(
      <ActivityBarWide
        workspaces={[{ id: 'w1', name: 'Alpha', tabs: ['t1'], activeTabId: 't1' }]}
        activeWorkspaceId="w1"
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
        tabsById={{
          t1: { id: 't1', title: 'Tab-1', kind: 'new-tab', locked: false, layout: { type: 'single' } } as never,
        }}
        activeTabId="t1"
        onSelectTab={() => {}}
        onCloseTab={() => {}}
        onMiddleClickTab={() => {}}
        onContextMenuTab={() => {}}
        onReorderWorkspaceTabs={() => {}}
        onReorderStandaloneTabs={() => {}}
        onAddTabToWorkspace={() => {}}
      />,
    )
    expect(screen.getByText('Tab-1')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 執行測試，確認 FAIL**

```bash
cd spa && npx vitest run src/features/workspace/components/ActivityBarWide.test.tsx -t "Phase 2"
```

- [ ] **Step 3: 實作**

完整替換 `spa/src/features/workspace/components/ActivityBarWide.tsx`：

```tsx
import { useCallback } from 'react'
import { Plus, GearSix, HardDrives } from '@phosphor-icons/react'
import {
  DndContext,
  PointerSensor,
  useSensor,
  useSensors,
  closestCenter,
  type DragEndEvent,
} from '@dnd-kit/core'
import { SortableContext, verticalListSortingStrategy, arrayMove } from '@dnd-kit/sortable'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { RegionResize } from '../../../components/RegionResize'
import { CollapseButton } from './CollapseButton'
import { WorkspaceRow } from './WorkspaceRow'
import { HomeRow } from './HomeRow'
import type { ActivityBarProps } from './activity-bar-props'

const HOME_KEY = 'home'

export function ActivityBarWide(props: ActivityBarProps) {
  const {
    workspaces,
    activeWorkspaceId,
    activeStandaloneTabId,
    onSelectWorkspace,
    onSelectHome,
    standaloneTabIds,
    onAddWorkspace,
    onReorderWorkspaces,
    onContextMenuWorkspace,
    onOpenHosts,
    onOpenSettings,
    tabsById = {},
    activeTabId = null,
    onSelectTab,
    onCloseTab,
    onMiddleClickTab,
    onContextMenuTab,
    onReorderWorkspaceTabs,
    onReorderStandaloneTabs,
    onAddTabToWorkspace,
  } = props

  const t = useI18nStore((s) => s.t)
  const wideSize = useLayoutStore((s) => s.activityBarWideSize)
  const setWideSize = useLayoutStore((s) => s.setActivityBarWideSize)

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))
  const wsIds = workspaces.map((ws) => ws.id)
  const isHomeActive = !activeWorkspaceId && !activeStandaloneTabId ? true : !activeWorkspaceId

  const handleDragEnd = useCallback(
    (e: DragEndEvent) => {
      const { active, over } = e
      if (!over || active.id === over.id) return

      const activeData = active.data.current as
        | { type: 'workspace'; wsId: string }
        | { type: 'tab'; tabId: string; sourceWsId: string | null }
        | undefined
      const overData = over.data.current as
        | { type: 'workspace'; wsId: string }
        | { type: 'tab'; tabId: string; sourceWsId: string | null }
        | undefined

      if (!activeData) return

      if (activeData.type === 'workspace') {
        const oldIndex = wsIds.indexOf(String(active.id))
        const newIndex = wsIds.indexOf(String(over.id))
        if (oldIndex === -1 || newIndex === -1) return
        const newOrder = arrayMove(wsIds, oldIndex, newIndex)
        onReorderWorkspaces?.(newOrder)
        return
      }

      if (activeData.type === 'tab') {
        if (!overData || overData.type !== 'tab') return // Phase 3 will handle cross-zone
        if (activeData.sourceWsId !== overData.sourceWsId) return // Phase 3 cross-ws
        const sourceWsId = activeData.sourceWsId
        if (sourceWsId === null) {
          // Standalone reorder (home row)
          const oldIdx = standaloneTabIds.indexOf(activeData.tabId)
          const newIdx = standaloneTabIds.indexOf(overData.tabId)
          if (oldIdx === -1 || newIdx === -1) return
          onReorderStandaloneTabs?.(arrayMove(standaloneTabIds, oldIdx, newIdx))
          return
        }
        const ws = workspaces.find((w) => w.id === sourceWsId)
        if (!ws) return
        const oldIdx = ws.tabs.indexOf(activeData.tabId)
        const newIdx = ws.tabs.indexOf(overData.tabId)
        if (oldIdx === -1 || newIdx === -1) return
        onReorderWorkspaceTabs?.(sourceWsId, arrayMove(ws.tabs, oldIdx, newIdx))
      }
    },
    [wsIds, workspaces, standaloneTabIds, onReorderWorkspaces, onReorderWorkspaceTabs, onReorderStandaloneTabs],
  )

  const handleSelectHomeWithFallback = () => {
    onSelectHome()
  }

  return (
    <>
      <div
        className="hidden lg:flex flex-col bg-surface-tertiary border-r border-border-subtle py-2 gap-0.5 flex-shrink-0 overflow-y-auto"
        style={{ width: wideSize }}
      >
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
          {/* Home row (not sortable) */}
          <HomeRow
            isActive={isHomeActive && !activeStandaloneTabId}
            standaloneTabIds={standaloneTabIds}
            tabsById={tabsById}
            activeTabId={activeTabId}
            onSelectHome={handleSelectHomeWithFallback}
            onSelectTab={onSelectTab ?? (() => {})}
            onCloseTab={onCloseTab ?? (() => {})}
            onMiddleClickTab={onMiddleClickTab ?? (() => {})}
            onContextMenuTab={onContextMenuTab ?? (() => {})}
          />

          {workspaces.length > 0 && <div className="mx-3 my-1 h-px bg-border-default" />}

          {/* Workspace rows (sortable) */}
          <SortableContext items={wsIds} strategy={verticalListSortingStrategy}>
            <div className="flex flex-col gap-0.5">
              {workspaces.map((ws) => (
                <WorkspaceRow
                  key={ws.id}
                  workspace={ws}
                  isActive={activeWorkspaceId === ws.id && !activeStandaloneTabId}
                  tabsById={tabsById}
                  activeTabId={activeTabId}
                  onSelectWorkspace={onSelectWorkspace}
                  onContextMenuWorkspace={onContextMenuWorkspace}
                  onSelectTab={onSelectTab ?? (() => {})}
                  onCloseTab={onCloseTab ?? (() => {})}
                  onMiddleClickTab={onMiddleClickTab ?? (() => {})}
                  onContextMenuTab={onContextMenuTab ?? (() => {})}
                  onAddTabToWorkspace={onAddTabToWorkspace ?? (() => {})}
                />
              ))}
            </div>
          </SortableContext>
        </DndContext>

        {/* Bottom controls */}
        <div className="mt-auto flex flex-col gap-1 px-2 pb-1 pt-2">
          <CollapseButton />
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
      <div data-testid="activity-bar-resize" className="hidden lg:block">
        <RegionResize
          resizeEdge="right"
          onResize={(delta) => setWideSize(wideSize + delta)}
        />
      </div>
    </>
  )
}
```

（若既有 `ActivityBarWide.tsx` 的 Home 按鈕位置與上不同，以此新版為準；Phase 1 的 Home `<button>` 被 `HomeRow` 取代。）

- [ ] **Step 4: 執行測試，確認 PASS（包含既有 resize + Phase 1 測試）**

```bash
cd spa && npx vitest run src/features/workspace/components/ActivityBarWide.test.tsx
```

若 Phase 1 測試因為 Home 改為 `HomeRow`（原本是 `<span>Home</span>`，新版 `<span>Home</span>` 仍在 `HomeRow` 裡）而 selector 變化，調整 test selector 但不改行為。若測試找不到 `Home` 文字，確認 `HomeRow` 內 `{t('nav.home')}` 在測試環境能正常翻譯（若翻譯未載入則顯示 key；預期 i18n store default locale 載入 en）。

- [ ] **Step 5: 執行整個 workspace components 測試套件**

```bash
cd spa && npx vitest run src/features/workspace/components
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/features/workspace/components/ActivityBarWide.tsx spa/src/features/workspace/components/ActivityBarWide.test.tsx
git commit -m "feat(activity-bar): ActivityBarWide with top-level DndContext + inline tabs"
```

---

## Task 10 — `App.tsx` 條件隱藏頂部 `<TabBar />`

**Files:**
- Modify: `spa/src/App.tsx`

- [ ] **Step 1: 讀 `tabPosition`**

`App.tsx` 既有 import 區塊下方加入（若尚未存在）：

```ts
import { useLayoutStore } from './stores/useLayoutStore'
```

（Phase 1 已加過。）

在 `App()` 內的 hooks 區塊（`const workspaces = ...` 附近）加入：

```ts
const tabPosition = useLayoutStore((s) => s.tabPosition)
```

- [ ] **Step 2: 條件渲染 `<TabBar />`**

找到現有：

```tsx
<TabBar
  tabs={displayTabs}
  activeTabId={activeTabId}
  ...
/>
```

包裹：

```tsx
{tabPosition === 'top' && (
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

- [ ] **Step 3: 手動驗證**

```bash
cd spa && pnpm run dev
```

1. 預設狀態：頂部 `TabBar` 可見，activity bar narrow
2. Settings → Appearance → Tab Position：Left
   - 頂部 `TabBar` 消失
   - activity bar 變 wide（耦合）
   - Home row、workspace rows 顯示；點 chevron 可展開 inline tabs
   - 同 ws 內 drag 某 tab 重排生效
   - 切回 Top：頂部 `TabBar` 回來

- [ ] **Step 4: Commit**

```bash
git add spa/src/App.tsx
git commit -m "feat(layout): hide top TabBar when tabPosition=left"
```

---

## Task 11 — 最終整合測試 + 回歸驗證

**Files:**
- Test only

- [ ] **Step 1: 執行全 workspace 元件測試**

```bash
cd spa && npx vitest run src/features/workspace/components
```

- [ ] **Step 2: 執行 store 測試**

```bash
cd spa && npx vitest run src/stores/useLayoutStore.test.ts
```

- [ ] **Step 3: 執行 hooks 測試**

```bash
cd spa && npx vitest run src/features/workspace/hooks.test.ts
```

- [ ] **Step 4: 執行 locale completeness**

```bash
cd spa && npx vitest run src/locales/locale-completeness.test.ts
```

- [ ] **Step 5: 全測試**

```bash
cd spa && npx vitest run
```

預期：全綠。

- [ ] **Step 6: Lint**

```bash
cd spa && pnpm run lint
```

- [ ] **Step 7: Build**

```bash
cd spa && pnpm run build
```

- [ ] **Step 8: 手動 smoke test 清單**

```bash
cd spa && pnpm run dev
```

在瀏覽器驗證：

1. **Top baseline**：預設 `tabPosition='top'`、narrow activity bar；既有行為不變（頂部 TabBar、workspace 圖示列）
2. **切 Tab Position = Left**：
   - Settings → Appearance 看到 radio group
   - 選 Left：activity bar 變 wide，頂部 TabBar 消失
   - CollapseButton 變 disabled（Phase 1 邏輯）
3. **Wide mode 展開/收合**：
   - 點 Home chevron：展開 → 顯示 standalone tabs；收合 → 隱藏
   - 點 workspace chevron：同上，workspaceExpanded[wsId] 獨立切換
   - reload 瀏覽器：展開狀態保留（persist 生效）
4. **切換 active tab 從 inline**：點 inline tab 切到該 tab，TabContent 同步
5. **Close tab from inline**：點 inline tab 的 X 按鈕關閉該 tab
6. **+ 按鈕**：展開某 workspace，按末端 +：新 tab 出現在該 workspace 末端，自動 active
7. **同 ws 內拖曳**：展開 workspace，拖某 inline tab 到同 workspace 另一個 tab 上方/下方：順序改變並立即持久化
8. **同 home 拖曳 standalone**：類似上述但 scope 為 standalone tab 清單
9. **跨 ws 拖曳（Phase 2 不支援）**：拖 tab 到他 ws 的 tab slot 上 → 放下後無反應（預期，Phase 3 才處理）
10. **Workspace reorder**：拖 workspace row：workspace 順序改變（回歸既有行為）
11. **切回 Tab Position = Top**：頂部 TabBar 回來，activity bar 維持 wide；可手動 CollapseButton 切 narrow
12. **刪除展開中的 workspace**：workspace 被刪、expanded state 清掉（`reconcileWorkspaceExpanded` Phase 1 邏輯仍生效）
13. **跨視窗同步**（若能開兩個 Electron window）：一邊切 left、另一邊 5 秒內自動跟上（`syncManager`）

- [ ] **Step 9: 確認 PR merge 前不動 `VERSION` / `CHANGELOG.md`**

依 `CLAUDE.md`，Phase 2 PR 合併後才更新 `VERSION` + `CHANGELOG.md`。此 plan 執行階段不動這兩個檔案。

---

## 最終檢查清單

- [ ] 所有 vitest 測試綠
- [ ] `pnpm run lint` 綠
- [ ] `pnpm run build` 綠
- [ ] Smoke test 13 項全過
- [ ] 既有 `ActivityBarNarrow` / `ActivityBar` / `CollapseButton` / `TabBar` 測試不回歸
- [ ] Settings → Appearance radio 切換即時生效
- [ ] `localStorage.purdex-layout.workspaceExpanded` 持久化正確
- [ ] `syncManager` 跨視窗同步正常
- [ ] 跨 ws 拖曳被放棄（非 bug，Phase 3 處理）
- [ ] Pinned tab 在 inline list 中目前無特別處理（Phase 3 才禁止跨 ws 拖曳；Phase 2 同 ws 內 reorder 無差別）

---

## 未涵蓋項目（明確交給 Phase 3）

- 跨 workspace 拖曳 tab、拖到 Home 變 standalone、從 Home 拖入 workspace
- Collision detection fallback chain（`pointerWithin → rectIntersection → closestCenter`）
- Spring-load 500ms 自動展開 collapsed row
- Drop-zone ring/bg 視覺反饋
- Pinned tab 跨 ws 禁止（UI 視覺 + drag overlay）
- `insertTab` 擴充支援 `afterTabId: string | null`（prepend）
- Active tab 在跨 ws move 時自動 `setActiveWorkspace(targetWsId)`
- 移除 Phase 1 的 `restrictToVertical` modifier（實際上 Phase 2 的 `ActivityBarWide` 已不用此 modifier，若還在舊 `ActivityBarNarrow` 請保留）

---

## 依賴澄清

- `@dnd-kit/core`、`@dnd-kit/sortable`、`@dnd-kit/utilities`、`@dnd-kit/modifiers` 皆已於專案內（Phase 1 / TabBar 使用）；本 phase 不新增套件
- `useSortable` + `SortableContext` + `DndContext` 用法與 `ActivityBarNarrow` / `TabBar` 一致
- `PointerSensor` 使用 `activationConstraint: { distance: 5 }`，與專案既有慣例（brainstorming 期間確認）一致
