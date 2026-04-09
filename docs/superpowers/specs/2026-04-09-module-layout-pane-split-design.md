# Module 框架 + Layout 系統 + Pane Split

**日期**: 2026-04-09
**狀態**: Draft
**前置**: Phase 11 Side Panel spec（命名已更新）、原始 UI spec Section 7
**基於**: brainstorming session 2026-04-08 ~ 2026-04-09

---

## 術語定義

| 術語 | 說明 |
|------|------|
| **Module** | 最小功能單位，提供 Pane（最多一個）和/或 Views（多個） |
| **Pane** | Tab content 的分割格子，由 Module 提供的全螢幕形態 |
| **View** | Sidebar/Panel 中的 widget，由 Module 提供的面板形態 |
| **Region** | View 的放置區域（4 個） |
| **Sidebar** | outer region（全高，與 Tab Bar 並列） |
| **Panel** | inner region（Tab Bar 下方） |
| **Primary** | 左側 |
| **Secondary** | 右側 |

---

## 1. 整體佈局

### 1.1 佈局圖

```
┌─────────────────────────────────────────────────────┐
│ ● ● ●      Title Bar                [layout btns]  │
├──────┬──────┬──────────────────────────────┬──────┤
│      │ Pri  │ Tab Bar                       │ Sec  │
│ Act  │ Side ├──────┬───────────────┬──────┤ Side │
│ Bar  │ bar  │ Pri  │               │ Sec  │ bar  │
│      │      │ Pan  │   Content     │ Pan  │      │
│      │      │ el   │  (Pane Split) │ el   │      │
├──────┴──────┴──────┴───────────────┴──────┴──────┤
│ Status Bar                                        │
└───────────────────────────────────────────────────┘
```

### 1.2 佈局元素

| 元素 | 說明 | 可見性 |
|------|------|--------|
| Title Bar | 紅綠燈 + 視窗標題 + layout 按鈕（Electron 專用） | 常駐，最頂部 |
| Activity Bar | 工作區/獨立分頁切換 | 常駐，最左側 |
| Primary Sidebar (`primary-sidebar`) | 系統級 View（全高） | 可折疊/縮減/固定 |
| Tab Bar | 分頁列 | 常駐 |
| Primary Panel (`primary-panel`) | 工作區級 View（Tab Bar 下方） | 可折疊/縮減/固定 |
| Content Area | Pane Layout（可分割） | 常駐 |
| Secondary Panel (`secondary-panel`) | 工作區級 View（Tab Bar 下方） | 可折疊/縮減/固定 |
| Secondary Sidebar (`secondary-sidebar`) | 系統級 View（全高） | 可折疊/縮減/固定 |
| Status Bar | 連線狀態摘要 | 常駐 |

### 1.3 Title Bar

- **Electron 模式**：獨立一行，高度 ~30px
  - 左：紅綠燈（macOS traffic lights）
  - 中：視窗標題（當前 workspace 或 session 名稱）
  - 右：Layout pattern 按鈕（pane split 操作）
  - 整條為 `-webkit-app-region: drag`，按鈕為 `no-drag`
- **SPA 模式**：不顯示 Title Bar，layout 按鈕移至 Tab Bar 右側

---

## 2. Module 系統

### 2.1 概念

Module 是功能的最小封裝單位。一個 Module 可以提供：
- **Pane**（最多一個）：tab content 的全螢幕形態
- **Views**（零到多個）：sidebar/panel 中的 widget 形態

兩者皆為選填，Module 不一定要同時提供。

### 2.2 Module 介面

```typescript
interface ModuleDefinition {
  id: string                        // 唯一識別（如 'session', 'files', 'git'）
  name: string                      // 顯示名稱
  pane?: PaneDefinition             // tab content（最多一個）
  views?: ViewDefinition[]          // sidebar/panel widgets（零到多個）
}

interface PaneDefinition {
  kind: string                      // PaneContent.kind（如 'tmux-session', 'browser'）
  component: React.ComponentType<PaneRendererProps>
}

interface ViewDefinition {
  id: string                        // 唯一識別（如 'file-tree', 'git-changes'）
  label: string                     // 顯示名稱
  icon: string                      // Phosphor icon name
  scope: 'system' | 'workspace'    // 系統級或工作區級
  defaultRegion: SidebarRegion      // 預設放置區域
  component: React.ComponentType<ViewProps>
}

interface ViewProps {
  hostId?: string
  workspaceId?: string
  isActive: boolean                 // 當前是否為 region 中的選中 view
}
```

### 2.3 Module Registry

```typescript
// 統一的 registry，取代現有 pane-registry.ts
function registerModule(module: ModuleDefinition): void
function unregisterModule(id: string): void
function getModule(id: string): ModuleDefinition | undefined
function getModules(): ModuleDefinition[]

// 便捷查詢
function getPaneRenderer(kind: string): React.ComponentType<PaneRendererProps> | undefined
function getViewDefinition(viewId: string): ViewDefinition | undefined
function getViewsByRegion(region: SidebarRegion): ViewDefinition[]
```

### 2.4 現有 Module 盤點

| Module ID | Pane | Views | 備註 |
|-----------|------|-------|------|
| `session` | SessionPane (`tmux-session`) | SessionListView | 現有，需遷移 |
| `files` | — | FileTreeView | **新增（本次實作）** |
| `editor` | EditorPane (`editor`) | — | 未來 |
| `browser` | BrowserPane (`browser`) | — | 現有，需遷移 |
| `dashboard` | DashboardPane (`dashboard`) | — | 現有，需遷移 |
| `hosts` | HostsPane (`hosts`) | — | 現有，需遷移 |
| `settings` | SettingsPane (`settings`) | — | 現有，需遷移 |
| `history` | HistoryPane (`history`) | — | 現有，需遷移 |
| `new-tab` | NewTabPane (`new-tab`) | — | 現有，需遷移 |
| `memory-monitor` | MemoryMonitorPane (`memory-monitor`) | — | 現有，需遷移 |

### 2.5 Module 註冊範例

```typescript
// files module
registerModule({
  id: 'files',
  name: 'Files',
  views: [{
    id: 'file-tree',
    label: 'Files',
    icon: 'FolderOpen',
    scope: 'workspace',
    defaultRegion: 'primary-panel',
    component: FileTreeView,
  }],
})

// session module
registerModule({
  id: 'session',
  name: 'Session',
  pane: {
    kind: 'tmux-session',
    component: SessionPaneContent,
  },
  views: [{
    id: 'session-list',
    label: 'Sessions',
    icon: 'List',
    scope: 'system',
    defaultRegion: 'primary-sidebar',
    component: SessionListView,
  }],
})
```

---

## 3. Layout Store（Region 管理）

### 3.1 狀態結構

```typescript
interface LayoutState {
  regions: Record<SidebarRegion, RegionState>
}

interface RegionState {
  views: string[]           // view IDs（排序）
  activeViewId?: string     // 當前選中的 view
  width: number             // 寬度 px
  mode: 'pinned' | 'default' | 'collapsed'
}

type SidebarRegion =
  | 'primary-sidebar'
  | 'primary-panel'
  | 'secondary-panel'
  | 'secondary-sidebar'
```

### 3.2 三種顯示模式

| 模式 | 行為 |
|------|------|
| **pinned** | 始終展開，不受 context 切換影響 |
| **default** | 同側 Sidebar + Panel 智慧切換 |
| **collapsed** | 收窄為按鈕條（~24px），hover/快捷鍵浮動展開 |

### 3.3 智慧切換（default 模式）

**在工作區 tab 時：**
- Panel（workspace scope）→ 展開
- Sidebar（system scope）→ 縮減

**在獨立 tab 時：**
- Sidebar（system scope）→ 展開
- Panel（workspace scope）→ 縮減

pinned 模式的 region 不參與切換。

### 3.4 Per-workspace 覆寫

```typescript
// Workspace 介面擴充
interface Workspace {
  // ...existing fields
  sidebarState?: WorkspaceSidebarState
}

interface WorkspaceSidebarState {
  regions: Record<SidebarRegion, {
    activeViewId?: string
    width: number
    mode: 'pinned' | 'default' | 'collapsed'
  }>
}
```

切換 workspace 時套用該 workspace 的 sidebarState。

### 3.5 持久化

- Key: `purdex-layout`
- Per-workspace 狀態存在 workspace store 內

---

## 4. Pane Split

### 4.1 資料結構

沿用現有 `PaneLayout` 定義（已在 `types/tab.ts`）：

```typescript
type PaneLayout =
  | { type: 'leaf'; pane: Pane }
  | { type: 'split'; id: string; direction: 'h' | 'v'; children: PaneLayout[]; sizes: number[] }
```

每個 Tab 持有一個 `PaneLayout` 樹。

### 4.2 操作方式（Phase 1 = 選單操作）

| 操作 | 觸發位置 | 行為 |
|------|----------|------|
| 選擇 layout pattern | Title Bar 右側按鈕 | 套用預設分割（左右、上下、三欄等） |
| Attach to pane | Tab 右鍵選單 | 將 tab 的 content 併入另一個 tab 的 split |
| Detach to tab | Pane 右上角選單 | 將 pane 從 split 中移出，成為獨立 tab |
| Close pane | Pane 右上角選單 | 關閉 pane，相鄰 pane 填滿空間 |
| 調整大小 | 分隔線拖曳 | 拖曳改變 `sizes` 比例 |

### 4.3 Layout Pattern 按鈕

提供常用的分割預設，點擊即套用：

```
[⊞]  — 取消分割（回到單 pane）
[◫]  — 左右分割
[⬒]  — 上下分割
[⊞₄] — 四宮格
```

套用 pattern 時：
- 若當前是單 pane → 分割，新 pane 顯示 New Pane Page
- 若已有分割 → 保留現有 pane content，調整佈局方向和比例；多出的格子顯示 New Pane Page，多餘的格子合併到最後一格
- 選擇 `single` → 保留當前 active pane，其餘 pane 關閉（需確認）

### 4.4 New Pane Page

新 pane 分割出來後顯示的選擇頁面：
- 基於 New Tab Page 調整
- 顯示可用的 module pane 類型供選擇
- 選擇後載入對應的 module content

### 4.5 PaneLayoutRenderer 擴充

現有 renderer 只處理 leaf，需擴充：
- 遞迴渲染 split 節點
- 每個 split 之間插入可拖曳的分隔線
- 分隔線拖曳時更新 `sizes` 陣列
- 最小 pane 尺寸限制

### 4.6 Pane 操作（資料層）

```typescript
// Tab store 擴充
interface TabStoreActions {
  // Split 操作
  splitPane(tabId: string, paneId: string, direction: 'h' | 'v'): void
  closePane(tabId: string, paneId: string): void
  
  // 跨 tab 操作
  attachToTab(sourceTabId: string, targetTabId: string, position: 'left' | 'right' | 'top' | 'bottom'): void
  detachPane(tabId: string, paneId: string): string  // returns new tab ID
  
  // 大小調整
  resizePanes(tabId: string, splitId: string, sizes: number[]): void
  
  // Layout pattern
  applyLayout(tabId: string, pattern: LayoutPattern): void
}

type LayoutPattern = 'single' | 'split-h' | 'split-v' | 'grid-4'
```

### 4.7 延後項目

以下功能不在本次範圍，但資料結構預留支援：

- **Phase 2**：Tab Bar 拖曳 tab 到 content area 自動 split
- **Phase 3**：Pane 之間拖曳交換、拖出成 tab
- Pane 之間的 swap 操作

---

## 5. Region 框架元件

### 5.1 元件清單

| 元件 | 職責 |
|------|------|
| `SidebarRegion` | Region 容器，渲染 view tabs + active view content |
| `RegionTabs` | Region 頂部/側邊的 view 圖示切換列 |
| `RegionResize` | Region 邊緣的拖曳調整寬度 handle |
| `CollapsedBar` | 縮減模式的窄條，hover 浮動展開 |
| `PaneSplitter` | Pane 之間的分隔線，拖曳調整大小 |

### 5.2 Region 渲染邏輯

```
SidebarRegion
├── collapsed?
│   └── CollapsedBar（窄條 + hover 展開）
└── expanded
    ├── RegionTabs（view 圖示列）
    ├── Active View Content（目前選中的 view component）
    └── RegionResize（拖曳邊緣）
```

---

## 6. 初期實作範圍

本次只做以下項目：

1. **Module Registry**：統一的 registry 取代 `pane-registry.ts`
2. **既有 pane 遷移**：將現有 `register-panes.tsx` 中的註冊改為 module 註冊
3. **Title Bar**：獨立一行（Electron），含 layout pattern 按鈕
4. **Layout Store**：`useLayoutStore`，region 狀態管理 + 持久化
5. **Region 框架元件**：4 個 region 的容器 + resize + collapsed
6. **Files Module**：FileTreeView（Primary Panel 的第一個 view）
7. **PaneLayoutRenderer 擴充**：支援 split 渲染 + 分隔線拖曳
8. **Pane 操作**：split / close / attach / detach（選單操作）
9. **New Pane Page**：新 pane 的 module 選擇頁面

### 不做的：

- 拖曳 tab 到 content area（Phase 2）
- Pane 之間拖曳（Phase 3）
- 資料源共享 / Agent bridge
- 智慧切換邏輯（先做手動切換，智慧切換後續加）
- 其他 View（Git、Prompts、Info、AI History）

---

## 7. 與既有 spec 的關係

- **Phase 11 Side Panel spec**：命名已更新。本 spec 擴大範圍，涵蓋 Module 系統 + Pane Split。Phase 11 spec 的工作項目（11.1~11.6）被本 spec 包含並擴充。
- **原始 UI spec Section 7**：命名已更新。四區域配置、三種模式、智慧切換的設計不變，本 spec 加上了 Module 抽象和 Pane Split。

---

## 8. 測試策略

- Module Registry：CRUD + 查詢 + 重複註冊防護
- Layout Store：持久化 + workspace 切換套用 + region mode 切換
- Region 元件：渲染 + resize 互動 + collapsed ↔ expanded
- PaneLayoutRenderer：split 渲染 + 分隔線拖曳 + sizes 更新
- Pane 操作：split / close / attach / detach 的資料正確性
- Files Module（FileTreeView）：基本渲染 + 目錄展開/收合
