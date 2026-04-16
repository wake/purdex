# Layout Modes — 寬窄 Activity Bar + Tab 位置切換

- **日期**：2026-04-17
- **狀態**：Spec（待實作）
- **分階段**：Phase 1 → Phase 2 → Phase 3（可獨立 PR）

## 背景與目標

目前 Purdex 的 activity bar 固定為窄版（44px、30×30 icon），tabs 固定在頂部 `TabBar`。隨著 workspace 數量成長，需要兩個新介面模式：

1. **Activity bar 寬窄切換** — 窄版保留目前現況；寬版顯示 icon + workspace 名稱，類似 VSCode/Slack。
2. **Tab 位置切換** — tabs 可維持在頂部 tab bar（現況），或改為顯示在寬版 activity bar 裡，每個 workspace 底下展開自己的 tab list。寬版才能選 `left`。

Cross-workspace drag 在 `tab 在左`模式下是自然的 UX 延伸，本 spec 一併納入（Phase 3）。

## 範圍

### 包含

- Layout 狀態模型與持久化
- Activity bar 窄/寬雙版本渲染
- 寬版右邊界 resize handle
- Settings → Appearance 新分頁放 tab position
- Activity bar 上寬窄切換按鈕
- 寬版下每個 workspace 可獨立展開/收合，顯示 inline tabs
- Home row 展開顯示 standalone tabs
- `tabPosition='left'` 時頂部 `TabBar` 隱藏
- 跨 workspace 拖曳 tab（含 standalone ↔ workspace 互轉）

### 不包含

- 多視窗同步 layout 設定（每個 window 獨立）
- 自動佈局 preset（「compact」、「spacious」之類的預設組合）
- 拖曳 workspace 到另一個視窗（既有 `handleWsTearOff` 不變）
- 窄版下 tab 的第二種排列方式

## 架構

### 狀態模型

擴充 `useLayoutStore`（現有 store，已持久化到 `purdex-layout`）：

```ts
type ActivityBarWidth = 'narrow' | 'wide'
type TabPosition = 'top' | 'left'

interface LayoutState {
  // ... existing region state

  activityBarWidth: ActivityBarWidth         // default: 'narrow'
  tabPosition: TabPosition                    // default: 'top'
  activityBarWideSize: number                 // default: 240，範圍 [180, 400]
  workspaceExpanded: Record<string, boolean>  // key: wsId, 'home' 代表 Home row
}
```

### 耦合規則（store action 內強制）

- `setTabPosition('left')` → 同步將 `activityBarWidth` 設為 `'wide'`
- `setTabPosition('top')` → 保留 `activityBarWidth` 當前值
- `setActivityBarWidth('narrow')`：若 `tabPosition === 'left'`，動作拒絕（no-op）；UI 層的 CollapseButton 也會 disabled，雙重保險
- `toggleActivityBarWidth()` → 同上規則

### 渲染決策（純 derive，不另存）

| 條件 | 結果 |
|---|---|
| `tabPosition === 'top'` | 顯示頂部 `<TabBar />` |
| `tabPosition === 'left'` | 頂部 `<TabBar />` 不渲染；activity bar 內每個 workspace 可展開顯示 inline tabs |
| `activityBarWidth === 'narrow'` | Activity bar 容器寬 44px，只顯示 icon |
| `activityBarWidth === 'wide'` | Activity bar 容器寬 `activityBarWideSize`，顯示 icon + 名稱，可拖右邊界 resize |

### 四種有效狀態

| width | position | 畫面 |
|---|---|---|
| narrow | top | 現況（baseline） |
| wide | top | 寬 activity bar，頂部 TabBar 照舊 |
| wide | left | 寬 activity bar，inline tabs per workspace，頂部 TabBar 隱藏 |
| narrow | left | **不允許**，被耦合規則擋掉 |

## 元件拆分

### 新元件（`spa/src/features/workspace/components/`）

```
ActivityBar.tsx                 協調者；依 activityBarWidth 選擇子元件
├─ ActivityBarNarrow.tsx        現有 narrow 渲染抽出
└─ ActivityBarWide.tsx          寬版容器
    ├─ WorkspaceRow.tsx         單一 workspace 行（header + 可選展開區）
    │   └─ InlineTabList.tsx    展開後的 tab list；per-ws SortableContext
    │       └─ InlineTab.tsx    單一 tab 行（icon + title + close + unread + status dot）
    ├─ HomeRow.tsx              Home 行；tabs = standalone tabs
    └─ CollapseButton.tsx       寬/窄切換按鈕；tabPosition='left' 時 disabled
```

### 其他

- `ActivityBarResize.tsx`（新）— 寬版右邊界 resize handle，複用 `SidebarRegion` 的 resize 模式
- `spa/src/components/settings/AppearanceSection.tsx`（新或延伸現有 settings 分頁）— tab position radio
- `App.tsx` — 依 `tabPosition` 條件渲染 `<TabBar />`；Phase 3 把 activity bar 的 `DndContext` 抽成頂層

### 共用取捨

- `SortableTab`（頂部水平）與 `InlineTab`（左側垂直）layout 差異大，**不共用元件**，避免 premature abstraction
- 只共用 presentational 子元件：`WorkspaceIcon`、`SubagentDots`、`TabStatusDot`、unread badge
- `useWorkspaceIndicators` hook 兩處都用

## 資料流

### A. 寬窄切換（Phase 1）

```
User 按 CollapseButton
  → useLayoutStore.toggleActivityBarWidth()
  → 若 tabPosition='left'，no-op
  → 否則 narrow ↔ wide 反轉
  → ActivityBar 依新 width render
```

### B. Tab position 切換（Phase 1）

```
Settings radio onChange → setTabPosition(pos)
  → pos='left' 時，同時 setActivityBarWidth('wide')
  → pos='top' 時，width 保留
App.tsx 依 tabPosition 決定是否渲染 <TabBar />
```

### C. Workspace 展開/收合（Phase 2）

```
User 按 WorkspaceRow header（或 chevron icon）
  → useLayoutStore.toggleWorkspaceExpanded(wsId)
  → workspaceExpanded[wsId] 反轉
  → WorkspaceRow 條件渲染 InlineTabList
```

Home row 同邏輯，key 為 `'home'`。預設值：首次進入時 active workspace 展開、其餘收合。

### D. 同 workspace 內重排（Phase 2）

每個 workspace 有自己的 `SortableContext`（`items = workspace.tabs`）。

```
InlineTab dragEnd
  → 計算 oldIdx / newIdx
  → useWorkspaceStore.reorderTabsInWorkspace(wsId, newOrder)
```

需新增 store action `reorderTabsInWorkspace(wsId: string, orderedTabIds: string[])`。

### E. 跨 workspace 拖曳（Phase 3）

#### DnD 結構重構

```
ActivityBarWide
└─ <DndContext onDragStart onDragOver onDragEnd collisionDetection=customDetection>
    ├─ <SortableContext items={wsIds} strategy=vertical>
    │    每個 WorkspaceRow / HomeRow（type='workspace'）
    └─ 每個 (Workspace|Home)Row 內的 InlineTabList
         └─ <SortableContext items={tabIds} strategy=vertical>
              InlineTab（type='tab'，data={tabId, sourceWsId}）
```

#### Draggable / Droppable data

```ts
type DraggableData =
  | { type: 'workspace'; wsId: string }
  | { type: 'tab'; tabId: string; sourceWsId: string | null }  // null = standalone

type DroppableData =
  | { type: 'workspace-header'; wsId: string }
  | { type: 'tab-slot'; wsId: string | null; tabId: string }
  | { type: 'home-header' }
```

#### onDragEnd 分派

| 來源 | 目標 | 動作 |
|---|---|---|
| workspace | 另一 workspace | 沿用既有 workspace reorder |
| tab（有 wsId） | tab-slot 同 ws | `reorderTabsInWorkspace(wsId, newOrder)` |
| tab（有 wsId） | tab-slot 他 ws | `moveTab(tabId, targetWsId, beforeTabId)` |
| tab（有 wsId） | workspace-header 他 ws | `moveTab(tabId, targetWsId, null)` ← append |
| tab（有 wsId） | home-header | `removeTabFromWorkspace(tabId)` ← 轉 standalone |
| tab（無 wsId / standalone） | workspace-header/tab-slot | `insertTab(tabId, targetWsId, ...)` |

#### Collision detection

- 先用 `pointerWithin`
- 若命中 workspace-header 且不在任何 tab-slot 上 → 視為「落到該 ws 末端」
- 若命中 tab-slot → 依游標相對於 slot 中線決定 insert before / after

#### Active tab 處理

- 拖曳過程不改變 `activeTabId`
- 落下後 `activeTabId` 不變；若 tab 被搬到別 workspace，自動切換 active workspace 到目標 ws

#### `useWorkspaceStore` 需新增

- `moveTab(tabId: string, targetWsId: string, beforeTabId?: string | null)` — 從任何來源（workspace / standalone）移到目標 ws，位置由 beforeTabId 決定；`null` = append
- `reorderTabsInWorkspace(wsId: string, orderedTabIds: string[])` — 內部重排
- `removeTabFromWorkspace(tabId: string)` — 從所屬 workspace 移除，tab 變 standalone

## Phase 拆分

### Phase 1 — 狀態與寬窄切換

- `useLayoutStore` 加入 `activityBarWidth`、`tabPosition`、`activityBarWideSize`
- setter 耦合規則
- `ActivityBarNarrow`（等同現況）、`ActivityBarWide`（只有 icon + 名稱，沒 tabs）
- `CollapseButton`（寬/窄 toggle；`tabPosition='left'` 時 disabled）
- `ActivityBarResize` 右邊界 resize handle
- Settings → Appearance：tab position radio（標註「left 將鎖定寬版」）
- 測試：state 轉移、耦合規則、button toggle、Settings UI、resize

### Phase 2 — 左側 tabs（同 workspace 內拖曳）

- `workspaceExpanded` state + `toggleWorkspaceExpanded`
- `WorkspaceRow`、`HomeRow`、`InlineTabList`、`InlineTab`
- per-ws `SortableContext`，內部重排
- `useWorkspaceStore.reorderTabsInWorkspace`
- `App.tsx`：`tabPosition='left'` 時條件隱藏 `<TabBar />`
- 空 workspace inline 空狀態、+ 按鈕放該 ws 展開區末端
- 測試：展開/收合 per-ws、內部重排、Home 展開、TabBar 隱藏、active 樣式

### Phase 3 — 跨 workspace 拖曳

- Activity bar DnD 結構重構（單一頂層 DndContext）
- 兩種 draggable 類型 + 三種 droppable 類型
- Custom collision detection
- Drop zone visual（hover workspace-header / home-header 時亮起）
- `useWorkspaceStore.moveTab`、`removeTabFromWorkspace`
- 邊界：拖 active tab、拖 pinned tab、從 Home 拖到 workspace、拖到 Home 變 standalone、跨 ws 拖到空 workspace
- 回歸：workspace 本身的 reorder 不能壞
- 測試：每個邊界至少一支 unit，跨 ws 流程至少一支 integration

**總工作量估算**：6–8 天

## 測試策略

### Phase 1

**Unit**：
- `useLayoutStore`：`setActivityBarWidth('narrow')` 在 `tabPosition='left'` 時被拒絕
- `useLayoutStore`：`setTabPosition('left')` 順帶將 width 設為 wide
- `useLayoutStore`：持久化到 `purdex-layout`、reload 後還原
- `CollapseButton`：`tabPosition='left'` 時 disabled
- `AppearanceSection`：選 left 即刻反映 wide
- `ActivityBarResize`：拖曳改 `activityBarWideSize`，放開寫 store

**Integration**：
- App 層：切 wide ↔ narrow 時 `<TabBar />` 仍在、容器寬度切換
- App 層：切 `tabPosition='left'`，activity bar 寬、TabBar 消失、CollapseButton disabled

### Phase 2

**Unit**：
- `toggleWorkspaceExpanded(wsId)` per-ws 互不干擾
- `InlineTabList` 空狀態 + 正常 render 順序
- `InlineTab` 點擊呼叫 `onSelectTab`、close 呼叫 `onCloseTab`
- `HomeRow` 顯示 standalone tabs
- `reorderTabsInWorkspace` 正確更新 workspace.tabs 順序

**Integration**：
- `tabPosition='left'` 時頂部 `<TabBar />` 不 render
- 展開多個 workspace 同時可見；內部 reorder 不影響別的 workspace
- active tab 在 inline list 有正確 active 樣式

### Phase 3

**Unit**：
- `moveTab`、`reorderTabsInWorkspace`、`removeTabFromWorkspace` 邊界：
  - 目標 ws 不存在、source = target、tab 不存在、pinned tab 跨 ws
  - 拖走 active tab 後 `activeTabId` 保持原值
- Custom collision detection：tab over workspace-header → 落 header；tab over tab-slot → 落 slot；曖昧命中取最近

**Integration**（抓架構 regression）：
- 拖 workspace row：workspace 重排（既有行為不壞）
- 拖 tab 同 ws 另一個 tab-slot：順序變
- 拖 tab 到另一 ws 的 header：搬到該 ws 末端
- 拖 tab 到另一 ws 的 tab-slot：搬到該 ws 指定位置
- 拖 tab 到 Home header：轉 standalone
- 拖 standalone tab 到 workspace header：assign 到該 ws
- Hover workspace row 時有 drop-zone 視覺反饋

### 回歸

- 既有 `ActivityBar.test.tsx`、`TabBar.test.tsx`、`SortableTab.test.tsx` 保持綠
- Narrow + top 的 baseline 行為不受影響

## 視覺細節

- 寬版 workspace 名稱：單行、超出以 `truncate`（ellipsis）處理；tooltip 顯示全名
- Chevron（▸/▾）：寬版 workspace row 左側 14px 寬保留區，點擊 row 任一處均展開/收合
- Drop zone 視覺：hover workspace-header 時整列加 ring/背景；hover home-header 同
- CollapseButton 位置：寬版 activity bar 的 Settings 按鈕旁（底部區），narrow 時仍在該位置但 icon 旋轉 180°
- Standalone tab 識別：沿用既有 `isStandaloneTab(id, workspaces)` 判定（不屬於任何 workspace.tabs 即 standalone）

## 開放議題

無（所有需求皆於 brainstorming 階段對齊）。

## 後續

Spec 審閱後進入 `writing-plans`，每個 phase 產一份 implementation plan。
