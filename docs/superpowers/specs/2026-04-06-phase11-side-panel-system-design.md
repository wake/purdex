# Phase 11：Side Panel 系統

**日期**: 2026-04-06（命名更新 2026-04-09）
**狀態**: Draft
**前置**: Phase 10（Workspace 強化）完成
**基於**: [tabbed-workspace-ui-design.md](2026-03-20-tabbed-workspace-ui-design.md) Section 7

---

## 術語定義

| 術語 | 說明 |
|------|------|
| **Sidebar** | outer 區域（全高，與 Tab Bar 並列），放系統級 View |
| **Panel** | inner 區域（Tab Bar 下方），放工作區級 View |
| **Primary** | 左側 |
| **Secondary** | 右側 |
| **View** | 區域內的個別內容（如 SessionsView、FilesView） |
| **Pane** | 內容區的分割格子（PaneLayout，不屬於本 Phase） |

---

## 1. 目標

實作側欄框架——4 Region 配置、View Registry、三種顯示模式（固定/預設/縮減）。這是 file tree、grep view 等後續功能的基礎設施。

---

## 2. 現況

已實作：
- `ActivityBar`（最左側，固定）
- `SettingsSidebar`、`HostSidebar`、`SessionPanel`（各自獨立的硬編碼側欄）

未實作：
- 統一的側欄框架（4 Region）
- View Registry（可插拔 View）
- 顯示模式切換（固定/預設/縮減）
- 同側智慧切換
- Per-workspace 側欄狀態記憶

---

## 3. 架構概覽

```
┌─────────────────────────────────────────────────────┐
│ ● ● ●  Title Bar                                    │
├──────┬──────┬──────────────────────────────┬──────┤
│      │ Pri  │ Tab Bar                       │ Sec  │
│ Act  │ Side ├──────┬───────────────┬──────┤ Side │
│ Bar  │ bar  │ Pri  │               │ Sec  │ bar  │
│      │      │ Pan  │   Content     │ Pan  │      │
│      │      │ el   │               │ el   │      │
├──────┴──────┴──────┴───────────────┴──────┴──────┤
│ Status Bar                                        │
└───────────────────────────────────────────────────┘
```

### 3.1 四個 Region

| Region | ID | 位置 | 垂直範圍 | 預設用途 |
|--------|-----|------|----------|----------|
| Primary Sidebar | `primary-sidebar` | Activity Bar 右側 | 全高 | 系統級 View（Sessions） |
| Primary Panel | `primary-panel` | Content 左側 | Tab Bar 下方 | 工作區級 View（File Tree、Git） |
| Secondary Panel | `secondary-panel` | Content 右側 | Tab Bar 下方 | 工作區級 View（Info） |
| Secondary Sidebar | `secondary-sidebar` | 最右側 | 全高 | 系統級 View（Prompts） |

### 3.2 View Registry

```typescript
interface ViewDefinition {
  id: string
  label: string
  icon: string                      // Phosphor icon name
  scope: 'system' | 'workspace'    // 系統級或工作區級
  defaultRegion: SidebarRegion      // 預設放置區域
  component: React.ComponentType<ViewProps>
}

interface ViewProps {
  hostId?: string
  workspaceId?: string
}

function registerView(view: ViewDefinition): void
function unregisterView(id: string): void
function getViews(): ViewDefinition[]
```

### 3.3 三種模式

| 模式 | 行為 |
|------|------|
| **固定（pinned）** | 始終展開，不受 context 切換影響 |
| **預設（default）** | 依 context 智慧切換（工作區 tab → Panel 展開/Sidebar 縮減；獨立 tab → 反之） |
| **縮減（collapsed）** | 收窄為按鈕條（~24px），hover 或快捷鍵浮動展開 |

### 3.4 Layout Store

```typescript
interface LayoutState {
  // 全域配置
  regions: Record<SidebarRegion, RegionState>
}

interface RegionState {
  views: string[]           // view IDs（排序）
  activeViewId?: string     // 當前選中
  width: number             // 寬度 px
  mode: 'pinned' | 'default' | 'collapsed'
}

type SidebarRegion =
  | 'primary-sidebar'
  | 'primary-panel'
  | 'secondary-panel'
  | 'secondary-sidebar'
```

Per-workspace 覆寫存在 `Workspace.sidebarState`（既有 type）。

---

## 4. 工作項目

### 11.1 Region 框架元件

- `SidebarRegion` 容器元件：渲染指定 region 的 views
- `RegionTabs`：region 內的 view 圖示切換列
- `RegionResize`：拖曳邊緣調整寬度
- `CollapsedBar`：縮減模式的窄條 + hover 浮動展開

### 11.2 View Registry

- `registerView` / `unregisterView` / `getViews`
- 內建 view 註冊（Sessions、Prompts 先行，File Tree 在後續 Phase）

### 11.3 Layout Store

- `useLayoutStore`：全域 region 配置
- Per-workspace sidebar state 整合（切換工作區時套用）
- 持久化（`purdex-layout`）

### 11.4 智慧切換邏輯

- 工作區 tab → Panel 展開、Sidebar 縮減
- 獨立 tab → Sidebar 展開、Panel 縮減
- 固定模式區域不參與切換

### 11.5 Sessions View 升級

- 從獨立 `SessionPanel` 元件遷移為 registered view
- 保留既有功能（按 host 分組、狀態 badge）

### 11.6 既有側欄遷移

- `SettingsSidebar` → Settings 頁面自帶側欄，不走 view registry
- `HostSidebar` → Host 頁面自帶側欄，不走 view registry
- `SessionPanel` → 遷移為 registered view（11.5）

---

## 5. 依賴關係

```
11.1 Region 框架 ─→ 11.4 智慧切換
11.2 View Registry ─→ 11.5 Sessions View
11.3 Layout Store ──→ 11.4 智慧切換
11.1 + 11.2 + 11.3 → 11.6 遷移
```

---

## 6. 測試策略

- 11.1：Region 框架元件渲染 + resize 互動
- 11.2：View Registry CRUD + 查詢
- 11.3：Layout Store 持久化 + workspace 切換套用
- 11.4：context 切換時 region 模式變化
- 11.5：Sessions View 功能回歸
