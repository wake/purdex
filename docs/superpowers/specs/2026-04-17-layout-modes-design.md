# Layout Modes — 寬窄 Activity Bar + Tab 位置切換

- **日期**：2026-04-17
- **狀態**：Spec（待實作）
- **分階段**：Phase 1 → Phase 2 → Phase 3（可獨立 PR）

## 背景與目標

目前 Purdex 的 activity bar 固定為窄版（44px、30×30 icon），tabs 固定在頂部 `TabBar`。隨著 workspace 數量成長，需要兩個新介面模式：

1. **Activity bar 寬窄切換** — 窄版保留現況；寬版顯示 icon + workspace 名稱，類似 VSCode/Slack。
2. **Tab 位置切換** — tabs 可維持在頂部 tab bar（現況），或改為顯示在寬版 activity bar 裡，每個 workspace 底下展開自己的 tab list。寬版才能選 `left`。

Cross-workspace drag 在 `tab 在左` 模式下是自然的 UX 延伸，本 spec 一併納入（Phase 3）。

## 範圍

### 包含

- Layout 狀態模型、持久化、既有 `syncManager` 多視窗同步
- Activity bar 窄/寬雙版本渲染
- 寬版右邊界 resize handle
- Settings → Appearance 延伸既有分頁放 tab position
- Activity bar 上寬窄切換按鈕
- 寬版下每個 workspace 可獨立展開/收合，顯示 inline tabs
- Home row 展開顯示 standalone tabs
- `tabPosition='left'` 時頂部 `TabBar` 隱藏
- 跨 workspace 拖曳 tab（含 standalone ↔ workspace 互轉）
- 拖到 collapsed workspace header 的 spring-load 自動展開

### 不包含

- 完整 keyboard navigation / `KeyboardSensor`（只補 `aria-expanded`；其餘開 GH issue 追蹤）
- 自動佈局 preset（「compact」、「spacious」等）
- Pinned tab 跨 workspace 拖曳（**明確禁止**，見 §DnD 規則）
- 拖曳 workspace 到另一個視窗（既有 `handleWsTearOff` 不變）
- Persist migration（alpha 階段依 `feedback_no_alpha_migration.md` 原則，舊資料由 zustand persist + partialize 預設值補齊）

## 架構

### 狀態模型

擴充 `useLayoutStore`（`spa/src/stores/useLayoutStore.ts`，現已持久化到 `purdex-layout`、由 `syncManager` 跨視窗同步）：

```ts
type ActivityBarWidth = 'narrow' | 'wide'
type TabPosition = 'top' | 'left'

interface LayoutState {
  // ... existing region state

  activityBarWidth: ActivityBarWidth         // default: 'narrow'
  tabPosition: TabPosition                    // default: 'top'
  activityBarWideSize: number                 // default: 240；沿用既有 clampWidth(120, 600)
  workspaceExpanded: Record<string, boolean>  // key: wsId，'home' 代表 Home row
}
```

**Persistence 變更**：`partialize` 必須擴充以包含新 4 個 key：
```ts
partialize: (state) => ({
  regions: state.regions,
  activityBarWidth: state.activityBarWidth,
  tabPosition: state.tabPosition,
  activityBarWideSize: state.activityBarWideSize,
  workspaceExpanded: state.workspaceExpanded,
})
```

**跨視窗同步**：已透過 `syncManager.register(STORAGE_KEYS.LAYOUT, useLayoutStore)` 生效，新 key 自動同步，無需額外設定。

### 耦合規則（store action 內強制）

- `setTabPosition('left')` → 同步將 `activityBarWidth` 設為 `'wide'`
- `setTabPosition('top')` → 保留 `activityBarWidth` 當前值
- `setActivityBarWidth('narrow')`：若 `tabPosition === 'left'`，動作 no-op；UI 層 `CollapseButton` 也會 disabled（雙重保險）
- `toggleActivityBarWidth()` → 同上規則

### `workspaceExpanded` 垃圾回收

沿用 `reconcileViews()` 模式，新增 `reconcileWorkspaceExpanded()`：訂閱 `useWorkspaceStore` 的 workspaces 變更，移除不存在於當前 workspaces 列表的 wsId（`'home'` 保留）。呼叫時機：
- store 初始化完成後一次
- `useWorkspaceStore.removeWorkspace` 後透過 Zustand subscribe 觸發

### 渲染決策（純 derive）

| 條件 | 結果 |
|---|---|
| `tabPosition === 'top'` | 顯示頂部 `<TabBar />` |
| `tabPosition === 'left'` | 頂部 `<TabBar />` 不渲染；activity bar 內每 ws 可展開顯示 inline tabs |
| `activityBarWidth === 'narrow'` | Activity bar 寬 44px，只顯示 icon |
| `activityBarWidth === 'wide'` | Activity bar 寬 `activityBarWideSize`，顯示 icon + 名稱，可拖右邊界 resize |

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
└─ ActivityBarWide.tsx          寬版容器（Phase 3 持有頂層 DndContext）
    ├─ WorkspaceRow.tsx         單一 workspace 行（header + 可選展開區）
    │   └─ InlineTabList.tsx    展開後的 tab list；per-ws SortableContext
    │       └─ InlineTab.tsx    單一 tab 行（icon + title + close + unread + status dot）
    ├─ HomeRow.tsx              Home 行；tabs = standalone tabs
    └─ CollapseButton.tsx       寬/窄切換按鈕；tabPosition='left' 時 disabled + tooltip
```

### 其他

- `ActivityBarResize.tsx`（新）— 寬版右邊界 resize handle，複用 `SidebarRegion.tsx` 的 resize 實作
- `spa/src/components/settings/AppearanceSection.tsx`（**延伸既有**）— 新增 tab position radio group（Phase 2 才加）
- `App.tsx` — 依 `tabPosition` 條件渲染 `<TabBar />`；Phase 3 把 activity bar 的 DndContext 抽成頂層

### 共用取捨

- `SortableTab`（頂部水平）與 `InlineTab`（左側垂直）layout 差異大，**不共用元件**
- 只共用 presentational 子元件：`WorkspaceIcon`、`SubagentDots`、`TabStatusDot`、unread badge
- `useWorkspaceIndicators` hook 兩處都用

## 資料流

### A. 寬窄切換（Phase 1）
```
CollapseButton 點擊
  → useLayoutStore.toggleActivityBarWidth()
  → 若 tabPosition='left'，no-op（UI 已 disabled）
  → 否則 narrow ↔ wide
```

### B. Tab position 切換（Phase 2）
```
AppearanceSection radio → setTabPosition(pos)
  → pos='left' 時同步 setActivityBarWidth('wide')
App.tsx 依 tabPosition 決定是否渲染 <TabBar />
```

### C. Workspace 展開/收合（Phase 2）
```
WorkspaceRow header 點擊
  → useLayoutStore.toggleWorkspaceExpanded(wsId)
  → workspaceExpanded[wsId] 反轉
  → WorkspaceRow 條件渲染 InlineTabList
```
Home row 同邏輯，key 為 `'home'`。預設值：首次進入時 active workspace 展開、其餘收合（store 初始化時依 `activeWorkspaceId` 計算）。

### D. 同 workspace 內重排（Phase 2）
使用**既有** action `reorderWorkspaceTabs(wsId, tabIds)`，**不新增**。
```
InlineTab dragEnd
  → 計算新順序
  → useWorkspaceStore.reorderWorkspaceTabs(wsId, newOrder)
```

### E. 跨 workspace 拖曳（Phase 3）

#### DnD 結構（**Phase 2 就建立頂層 DndContext，Phase 3 只擴充 draggable 類型**）

```
ActivityBarWide
└─ <DndContext onDragStart onDragOver onDragEnd collisionDetection=customDetection>
    ├─ <SortableContext items={wsIds} strategy=vertical>
    │    每個 WorkspaceRow / HomeRow（type='workspace'）
    └─ 每個 Row 內的 InlineTabList
         └─ <SortableContext items={tabIds} strategy=vertical>
              InlineTab（type='tab'，data={tabId, sourceWsId}）
```

**Modifier 調整**：既有 `restrictToVertical`（clamp 到 `wsZoneRef`）不再適用跨 ws 拖曳；Phase 3 拆 ActivityBarWide 時移除此 modifier，改由 collision detection + `onDragEnd` 分派控制。

#### Draggable / Droppable data
```ts
type DraggableData =
  | { type: 'workspace'; wsId: string }
  | { type: 'tab'; tabId: string; sourceWsId: string | null; isPinned: boolean }

type DroppableData =
  | { type: 'workspace-header'; wsId: string }
  | { type: 'tab-slot'; wsId: string | null; tabId: string }
  | { type: 'home-header' }
```

#### Pinned tab 規則（**明確禁止跨 ws**）

- `onDragStart`：若 draggable 是 pinned tab，設 flag
- `onDragOver`：若 flag 為 pinned，collision detection 只允許 **同 sourceWsId** 的 `tab-slot`；其他 target 不 highlight
- `onDragEnd`：若 drop target 不在允許範圍，放棄（視覺上回彈）

#### Spring-load 自動展開

- `onDragOver` 命中 **collapsed** workspace-header / home-header 時啟動 500ms 計時器
- 計時器到期呼叫 `toggleWorkspaceExpanded(wsId)` 自動展開該 row
- 游標離開或放開清除計時器

#### Collision detection（fallback chain）

MultipleContainers 標準 recipe：
```
pointerWithin(args)
  → rectIntersection(args)  (filter to droppables)
  → closestCenter(args)
```
若命中 workspace-header 且未命中任何 tab-slot → 視為「落到該 ws 末端」。若命中 tab-slot → 依游標相對於 slot 中線決定 before / after。

#### onDragEnd 分派

| 來源 | 目標 | 動作 |
|---|---|---|
| workspace | 另一 workspace | `reorderWorkspaces(newOrder)`（既有） |
| tab（非 pinned） | tab-slot 同 ws | `reorderWorkspaceTabs(wsId, newOrder)`（既有） |
| tab（非 pinned） | tab-slot 他 ws | `insertTab(tabId, targetWsId, afterTabId)`（既有，內建 cross-ws dedup） |
| tab（非 pinned） | workspace-header 他 ws | `insertTab(tabId, targetWsId, undefined)` ← append |
| tab（非 pinned） | home-header | 拆分：`findWorkspaceByTab(tabId)` → `removeTabFromWorkspace(wsId, tabId)`（既有兩步驟） |
| tab（pinned） | 任何他 ws 目標 | 禁止，拖曳無效 |
| standalone tab | workspace-header / tab-slot | `insertTab(tabId, targetWsId, ...)` |

**不新增** `moveTab`、`reorderTabsInWorkspace`、單參 `removeTabFromWorkspace`（皆與既有 action 衝突或重複）。

#### `insertTab` 擴充需求

既有 `insertTab(tabId, workspaceId?, afterTabId?)` 只支援 append 或「after 某 tab」；拖到目標 ws 第一個位置時無 afterTabId 可用。**Phase 3 需求**：擴充第三參數為 `{ afterTabId?: string; position?: 'start' | 'end' }` 或直接接受 `afterTabId: string | null`（`null` 代表 prepend）。實作時選一種風格即可，backwards compatible。

#### Active tab / active ws 處理

- 拖曳進行中不改 `activeTabId`
- 落下後：
  - 若被拖的 tab **不是 active tab** → `activeWorkspaceId` 不變
  - 若被拖的 tab **是 active tab** → 自動 `setActiveWorkspace(targetWsId)`

## Phase 拆分

### Phase 1 — 狀態與寬窄切換（僅 activity bar 外殼）

**工作量：~1–1.5 天**

- `useLayoutStore` 加入 `activityBarWidth`、`activityBarWideSize`、`workspaceExpanded`；**tab position 相關延到 Phase 2**
- setter 含 `activityBarWidth` 的耦合保護（即使 Phase 1 沒有 `setTabPosition`，也先寫好以 tabPosition 存在時阻擋 narrow 切換的邏輯）
- `ActivityBarNarrow`（等同現況，抽出）、`ActivityBarWide`（icon + 名稱，**沒有 tabs**，**沒有 DndContext 內部 tabs**）
- `CollapseButton`（寬/窄 toggle；Phase 1 因沒 tabPosition setter，disabled 條件是「預留」）
- `ActivityBarResize` 右邊界 resize handle
- `partialize` 擴充
- `reconcileWorkspaceExpanded` GC 邏輯
- i18n keys（新增）：
  - `nav.collapse_activity_bar` / `nav.expand_activity_bar`
  - 中/英/日各一份（依既有 i18n 覆蓋）
- 既有測試更新：`ActivityBar.test.tsx` 因拆成 Narrow/Wide 可能需改 selectors
- 新測試見「測試策略 Phase 1」

**Phase 1 ship 後使用者可體驗**：在 activity bar 切寬/窄，寬版顯示 workspace 名稱 + 可拖 resize。

### Phase 2 — Tab position + Inline tabs（同 ws 內拖曳）

**工作量：~2–3 天**

- `useLayoutStore.tabPosition` 及 setter（含耦合到 wide）
- `AppearanceSection.tsx` **延伸既有**，加入 tab position radio group（含「選 left 將鎖定寬版」註記）
- `WorkspaceRow`、`HomeRow`、`InlineTabList`、`InlineTab`
- `toggleWorkspaceExpanded(wsId)`
- **頂層 DndContext 先建立**（即使 Phase 2 只做同 ws 拖曳，collision detection 與 drag-types 也先設計為可擴充）
- per-ws `SortableContext` + 內部重排（使用既有 `reorderWorkspaceTabs`）
- `App.tsx`：`tabPosition='left'` 時條件隱藏 `<TabBar />`
- 空 workspace inline 空狀態、`+` 按鈕放該 ws 展開區末端（需 `handleAddTabToWorkspace(wsId)` 新 handler）
- i18n keys 新增：
  - `settings.appearance.tab_position` / `settings.appearance.tab_position.top` / `.left` / `.left_hint`
  - `nav.workspace_empty`（空 ws 空狀態文案）
- 既有測試更新：可能影響 `ActivityBar.test.tsx`、間接影響 `TabBar.test.tsx`（條件渲染）
- 新測試見「測試策略 Phase 2」

**Phase 2 ship 後使用者可體驗**：Settings 切 tab 到左側，每個 workspace 底下可展開 tab list，同 ws 內可拖曳重排。

### Phase 3 — 跨 workspace 拖曳

**工作量：~2.5–3.5 天**

- DnD draggable 類型擴充（加上 `'tab'` 類型；`'workspace'` 類型已於 Phase 2 存在）
- Custom collision detection fallback chain（pointerWithin → rectIntersection → closestCenter）
- Drop zone visual（workspace-header / home-header hover 時亮起 ring + bg；透過 `useDndContext.over` 傳入 row 元件）
- Spring-load 自動展開 collapsed row（500ms 計時器）
- Pinned tab 跨 ws 禁止（drag-type 篩選 + drag overlay 視覺提示）
- Active tab 在 cross-ws move 時的 `setActiveWorkspace` 邏輯
- `insertTab` 擴充支援 `afterTabId: string | null`（null 代表 prepend）
- 既有 `restrictToVertical` modifier 於 ActivityBarWide 中移除
- 回歸測試：workspace reorder 本身不壞
- 新測試見「測試策略 Phase 3」

**Phase 3 ship 後使用者可體驗**：tab 可在 workspace 間拖曳搬移、可拖到 Home 變 standalone、可從 Home 拖入 workspace。

**總工作量估算**：**5.5–8 天**

## 檔案影響範圍

### 新增
- `spa/src/features/workspace/components/ActivityBarNarrow.tsx`
- `spa/src/features/workspace/components/ActivityBarWide.tsx`
- `spa/src/features/workspace/components/WorkspaceRow.tsx`
- `spa/src/features/workspace/components/HomeRow.tsx`
- `spa/src/features/workspace/components/InlineTabList.tsx`
- `spa/src/features/workspace/components/InlineTab.tsx`
- `spa/src/features/workspace/components/CollapseButton.tsx`
- `spa/src/features/workspace/components/ActivityBarResize.tsx`
- 對應 `.test.tsx`

### 修改
- `spa/src/stores/useLayoutStore.ts`（新 state + action + partialize + reconcile）
- `spa/src/features/workspace/components/ActivityBar.tsx`（改為協調者）
- `spa/src/features/workspace/components/ActivityBar.test.tsx`（更新 selectors）
- `spa/src/components/settings/AppearanceSection.tsx`（加 tab position radio）
- `spa/src/App.tsx`（條件渲染 TabBar、活動區新 handler 傳入）
- `spa/src/features/workspace/store.ts`（`insertTab` 擴充支援 prepend）
- `spa/src/stores/useTabStore.ts` 或相關（若 `handleAddTabToWorkspace` 需要新 action）
- `spa/src/i18n/*`（新增 keys）

### 不變
- `spa/src/components/TabBar.tsx`（條件渲染由 App.tsx 控制，TabBar 本身不改）
- `spa/src/components/SortableTab.tsx`
- 其他 workspace 內元件邏輯

## 測試策略

### Phase 1

**Unit**：
- `useLayoutStore`：新 field 預設值、persistence via `partialize`、`syncManager` 跨視窗事件
- `useLayoutStore`：`setActivityBarWidth('narrow')` 在 `tabPosition='left'` 時 no-op（即使 Phase 1 預設 top，也要防禦測試）
- `useLayoutStore`：`reconcileWorkspaceExpanded` 刪 ws 後 GC 生效
- `CollapseButton`：點擊觸發 `toggleActivityBarWidth`
- `ActivityBarResize`：拖曳改 `activityBarWideSize`（clamp 生效）
- `ActivityBarNarrow` / `ActivityBarWide` 選擇式渲染

**Integration**：
- App 層：切 wide ↔ narrow 容器寬度切換、`<TabBar />` 照舊 render

### Phase 2

**Unit**：
- `setTabPosition('left')` 同步改 `activityBarWidth='wide'`
- `toggleWorkspaceExpanded(wsId)` per-ws 互不干擾
- `InlineTabList` 空狀態 + 正常 render 順序
- `InlineTab` 點擊 / close / 右鍵行為
- `HomeRow` 顯示 standalone tabs
- 使用 `reorderWorkspaceTabs` 更新順序

**Integration**：
- `tabPosition='left'` 時頂部 `<TabBar />` 不 render、`CollapseButton` disabled
- 頂層 DndContext 已就位、同 ws 拖曳生效
- `AppearanceSection` radio 選 left，activity bar 即刻變寬

### Phase 3

**Unit**：
- Custom collision detection：tab over workspace-header → 落 header；tab over tab-slot → 落 slot；曖昧命中取最近
- Spring-load 計時器：500ms 後自動 `toggleWorkspaceExpanded`，離開清除
- Pinned tab 拖曳限制：`onDragOver` 目標為他 ws 時不 highlight
- `insertTab` 擴充：`afterTabId = null` prepend 到 target ws 第一位
- Active tab 在跨 ws move 時 `setActiveWorkspace(targetWsId)`；非 active tab 不切

**Integration（抓架構 regression）**：
- 拖 workspace row：workspace 重排（既有行為不壞）
- 拖 tab 同 ws 另一個 tab-slot：順序變
- 拖 tab 到他 ws header：搬到該 ws 末端
- 拖 tab 到他 ws tab-slot：搬到指定位置
- 拖 tab 到 Home header：轉 standalone
- 拖 standalone tab 到 ws header：assign 到該 ws
- 拖到 collapsed workspace：spring-load 展開後可繼續拖入
- Hover workspace row 時有 drop-zone 視覺反饋
- Pinned tab 無法跨 ws（有嘗試落下視覺回彈）

### 回歸

- 既有 `ActivityBar.test.tsx`、`TabBar.test.tsx`、`SortableTab.test.tsx` 保持綠（可能需更新 selectors）
- Narrow + top 的 baseline 行為不受影響

## 開放議題

- **Settings 分層**：依 `project_settings_architecture.md` Settings 有三層，tab position 放在 global 層還是 workspace 層？當前假設 **global**（所有 workspace 共用），若使用者反映要 per-workspace 再調整
- **Drop zone 視覺 spec**：ring 顏色、背景淡化程度、動畫 duration 等細節留 plan 階段依現有 theme token 決定
- **Empty standalone 狀態**：Home 無 standalone tab 時是否顯示「None」或直接不渲染展開區？Phase 2 實作時決
- **Keyboard accessibility**：本 spec 不做完整 KeyboardSensor，僅 `aria-expanded` 標註。完整 keyboard nav 另開 GH issue

## 後續

Spec 審閱後進入 `writing-plans`，每個 phase 產一份 implementation plan。
