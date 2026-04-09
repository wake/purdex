# Sidebar / Panel / Pane 修正設計

> 日期：2026-04-10

## 1. 概念模型

三層 UI 容器各有明確的作用域：

| 層級 | 作用域 | 說明 |
|------|--------|------|
| **Sidebar** | Workspace | 一個 region 可放多個 view（tab 切換），內容以 workspace 為基礎 |
| **Panel** | 跨 Tab | 一個 region 可放多個 view（tab 切換），內容響應 active tab 的 context |
| **Pane** | Tab 內部 | Tab 內的內容分割（split-h / split-v / grid-4） |

4 個 region（`primary-sidebar`, `primary-panel`, `secondary-panel`, `secondary-sidebar`）維持不變。區別在於 sidebar region 的 view 使用 workspace 層級資料，panel region 的 view 使用 active tab context。

## 2. View Scope 模型（方案 B）

同一個 module 為不同 placement 註冊**獨立的 view**，而非一個 view 內部判斷 region。

### Files Module 範例

```
原本：1 view — file-tree (primary-panel)
改為：2 views
  - file-tree-workspace (primary-sidebar) → 以 workspace.projectPath 為根
  - file-tree-session   (primary-panel)   → 以 active terminal cwd 為根
```

### View Component Props

View component 新增 `region: SidebarRegion` prop（供未來用途，方案 B 下大部分 view 不需參考此值）。

### 不改動的部分

- `ModuleDefinition` / `ViewDefinition` 介面不需修改
- `defaultRegion` 已足夠指定預設 placement

## 3. Workspace 專案路徑

### 型別變更

```typescript
interface Workspace {
  // ...existing fields
  projectPath?: string  // 新增：workspace 專案根目錄
}
```

`projectPath` 存在 workspace store，persist 到 storage。

### 設定入口

- **Files sidebar view**：開啟時若 `projectPath` 為空 → 顯示引導畫面（路徑輸入 + 確認按鈕）
- **Workspace settings 頁面**：可編輯 `projectPath` 欄位

### View 行為

| View | 路徑來源 | 無路徑時 |
|------|----------|----------|
| `file-tree-workspace` | `workspace.projectPath` | 顯示「請設定專案路徑」引導 UI |
| `file-tree-session` | Active terminal 的 cwd（daemon API / tmux session） | 顯示「無可用路徑」提示 |

## 4. Pane UX 修正

### 4.1 分隔線與工具按鈕不明顯

**PaneSplitter：**
- Hover 時視覺寬度 3px → 6px
- 加深顏色對比
- Cursor 變更（`col-resize` / `row-resize`）

**PaneHeader：**
- 工具按鈕加大 hit area
- 增加 hover 背景色
- 加明確的邊框 / 色差來區分 pane 區域

### 4.2 四宮格垂直拖曳聯動

現狀：`grid-4` 佈局為 `split-h > [split-v, split-v]`，兩個垂直 split 各自獨立，無法統一調整行高。

修正：在 `PaneLayoutRenderer` 層級偵測 grid-4 結構，讓兩個垂直 split 的 splitter **聯動**。拖曳任一側的垂直 splitter 時，同步調整另一側的 `sizes`。

實作方式：
- `PaneLayoutRenderer` 偵測外層 `split-h` 包含兩個 `split-v` 子節點（grid pattern）
- 透過 callback prop 或 shared ref 讓兩個 `PaneSplitter` 同步 resize 事件
- 同步更新兩個 split-v 節點的 `sizes` 陣列

### 4.3 Pane 彈出位置

現狀：`detachPane` 建立新 tab 後 append 到 workspace 尾端。

修正：新 tab 插入到**當前 tab 的下一個位置**。

變更範圍：`useTabStore.ts` 的 `detachPane` 邏輯 + workspace store 的 tab 插入位置。

### 4.4 Tab 右鍵「加入 Pane」

`TabContextMenu` 新增選項：

- 條件：workspace 中有其他 tab 含 split layout（`layout.type === 'split'`）
- 顯示 submenu：列出所有含 split layout 的 tab（e.g.「加入 Tab A 成為 pane」）
- 點擊行為：
  1. 將被右鍵的 tab 的 primary pane content 作為新 pane
  2. Append 到目標 tab layout 的最後位置
  3. 關閉原 tab

### 4.5 Pane 內容交換（移動）

`PaneHeader` 工具列新增「移動」按鈕：

- 點擊後顯示同 tab 內其他 pane 的列表
- 選擇後 **swap** 兩個 pane 的 content
- 新增 `pane-tree.ts` 工具函數：`swapPaneContent(layout, paneIdA, paneIdB)`

## 5. TopBar Region Toggle 按鈕

### 位置

TabBar 右側，順序：`[region toggles] [分隔線] [pane layout buttons]`

### 按鈕配置（左到右）

| 按鈕 | Region | Icon | Mirror |
|------|--------|------|--------|
| Primary Sidebar | `primary-sidebar` | `SidebarSimple` | — |
| Primary Panel | `primary-panel` | `SquareHalfBottom` | — |
| Secondary Panel | `secondary-panel` | `SquareHalfBottom` | 水平翻轉 |
| Secondary Sidebar | `secondary-sidebar` | `SidebarSimple` | 水平翻轉 |

### 行為

- 點擊 = toggle `mode` between `'pinned'` / `'collapsed'`
- Active 狀態（pinned）時按鈕高亮
- 僅在 region 有註冊 view 時才顯示對應按鈕

### 視覺

- Region toggles 與 pane layout buttons 之間用細分隔線分隔

## 6. 改動範圍總表

| 區域 | 檔案 | 改動 |
|------|------|------|
| Module Registry | `register-modules.tsx` | files 拆成 2 個 view |
| Workspace Store | `workspace/store.ts`, `types/tab.ts` | 新增 `projectPath` 欄位 |
| FileTreeView | `FileTreeView.tsx` → 拆成兩個 component | `FileTreeWorkspaceView` + `FileTreeSessionView` |
| PaneSplitter | `PaneSplitter.tsx` | 加強視覺 + grid-4 垂直聯動 |
| PaneHeader | `PaneHeader.tsx` | 工具按鈕視覺加強 + swap 功能 |
| Pane detach | `useTabStore.ts` | 彈出 tab 插入到當前 tab 下一位 |
| TabContextMenu | `TabContextMenu.tsx` | 新增「加入 pane」submenu |
| TabBar | `TabBar.tsx` | 右側新增 4 個 region toggle 按鈕 |
| Pane tree utils | `pane-tree.ts` | 新增 `swapPaneContent` 工具函數 |

## 7. 不改動的部分

- `ModuleDefinition` / `ViewDefinition` 介面
- Sidebar / Panel region 架構（4-region layout）
- Pane layout pattern 系統（`LayoutPattern` type）

## 8. 風險點

- **Grid-4 垂直聯動**：需要讓兩個獨立 split node 共享 resize 事件，可能需在 `PaneLayoutRenderer` 層級協調（callback / shared ref）
- **Tab 右鍵 submenu**：需存取所有 workspace tabs 的 layout 資訊來判斷哪些 tab 有 split layout
