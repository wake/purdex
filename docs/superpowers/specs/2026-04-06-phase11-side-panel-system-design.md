# Phase 11：Side Panel 系統

**日期**: 2026-04-06
**狀態**: Draft
**前置**: Phase 10（Workspace 強化）完成
**基於**: [tabbed-workspace-ui-design.md](2026-03-20-tabbed-workspace-ui-design.md) Section 7

---

## 1. 目標

實作側欄面板框架——4 區域配置、panel registry、三種顯示模式（固定/預設/縮減）。這是 file tree、grep panel 等後續功能的基礎設施。

---

## 2. 現況

已實作：
- `ActivityBar`（最左側，固定）
- `SettingsSidebar`、`HostSidebar`、`SessionPanel`（各自獨立的硬編碼側欄）

未實作：
- 統一的側欄框架（4 區域）
- Panel registry（可插拔面板）
- 面板模式切換（固定/預設/縮減）
- 同側智慧切換
- Per-workspace 側欄狀態記憶

---

## 3. 架構概覽

```
┌──────┬──────┬──────────────────────────────┬──────┐
│      │ 左   │ Tab Bar                       │ 右   │
│ Act  │ 外   ├──────┬───────────────┬──────┤ 外   │
│ Bar  │      │ 左   │               │ 右   │      │
│      │      │ 內   │   Content     │ 內   │      │
│      │      │      │               │      │      │
├──────┴──────┴──────┴───────────────┴──────┴──────┤
│ Status Bar                                        │
└───────────────────────────────────────────────────┘
```

### 3.1 四個 Zone

| Zone | 位置 | 垂直範圍 | 預設用途 |
|------|------|----------|----------|
| `left-outer` | Activity Bar 右側 | 全高 | 系統級面板（Sessions） |
| `left-inner` | Content 左側 | Tab Bar 下方 | 工作區級面板（File Tree、Git） |
| `right-inner` | Content 右側 | Tab Bar 下方 | 工作區級面板（Info） |
| `right-outer` | 最右側 | 全高 | 系統級面板（Prompts） |

### 3.2 Panel Registry

```typescript
interface PanelDefinition {
  id: string
  label: string
  icon: string                    // Phosphor icon name
  scope: 'system' | 'workspace'  // 系統級或工作區級
  defaultZone: SidebarZone        // 預設放置區域
  component: React.ComponentType<PanelProps>
}

interface PanelProps {
  hostId?: string
  workspaceId?: string
}

function registerPanel(panel: PanelDefinition): void
function unregisterPanel(id: string): void
function getPanels(): PanelDefinition[]
```

### 3.3 三種模式

| 模式 | 行為 |
|------|------|
| **固定（pinned）** | 始終展開，不受 context 切換影響 |
| **預設（default）** | 依 context 智慧切換（工作區 tab → 內展開/外縮減；獨立 tab → 反之） |
| **縮減（collapsed）** | 收窄為按鈕條（~24px），hover 或快捷鍵浮動展開 |

### 3.4 Sidebar Store

```typescript
interface SidebarState {
  // 全域配置
  zones: Record<SidebarZone, ZoneState>
}

interface ZoneState {
  panels: string[]          // panel IDs（排序）
  activePanelId?: string    // 當前選中
  width: number             // 面板寬度 px
  mode: 'pinned' | 'default' | 'collapsed'
}
```

Per-workspace 覆寫存在 `Workspace.sidebarState`（既有 type）。

---

## 4. 工作項目

### 11.1 Sidebar 框架元件

- `SidebarZone` 容器元件：渲染指定 zone 的 panels
- `SidebarTabs`：zone 內的 panel 圖示切換列
- `SidebarResize`：拖曳邊緣調整寬度
- `CollapsedBar`：縮減模式的窄條 + hover 浮動展開

### 11.2 Panel Registry

- `registerPanel` / `unregisterPanel` / `getPanels`
- 內建 panel 註冊（Sessions、Prompts 先行，File Tree 在 Phase 9）

### 11.3 Sidebar Store

- `useSidebarStore`：全域 zone 配置
- Per-workspace sidebar state 整合（切換工作區時套用）
- 持久化（`purdex-sidebar`）

### 11.4 智慧切換邏輯

- 工作區 tab → 內面板展開、外面板縮減
- 獨立 tab → 外面板展開、內面板縮減
- 固定模式面板不參與切換

### 11.5 Sessions Panel 升級

- 從獨立 `SessionPanel` 元件遷移為 registered panel
- 保留既有功能（按 host 分組、狀態 badge）

### 11.6 既有側欄遷移

- `SettingsSidebar` → Settings 頁面自帶側欄，不走 panel system
- `HostSidebar` → Host 頁面自帶側欄，不走 panel system
- `SessionPanel` → 遷移為 registered panel（11.5）

---

## 5. 依賴關係

```
11.1 框架元件 ─→ 11.4 智慧切換
11.2 Registry ─→ 11.5 Sessions Panel
11.3 Store ───→ 11.4 智慧切換
11.1 + 11.2 + 11.3 → 11.6 遷移
```

---

## 6. 測試策略

- 11.1：框架元件渲染 + resize 互動
- 11.2：registry CRUD + panel 查詢
- 11.3：store 持久化 + workspace 切換套用
- 11.4：context 切換時 zone 模式變化
- 11.5：Sessions panel 功能回歸
