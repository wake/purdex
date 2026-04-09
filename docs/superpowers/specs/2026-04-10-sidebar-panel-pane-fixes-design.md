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

`ViewProps`（`module-registry.ts`）新增 `region: SidebarRegion` 欄位。`SidebarRegion.tsx` 渲染 view 時傳入 `region`、`workspaceId`、`hostId`（現有程式碼未傳 `workspaceId` 和 `hostId`，一併修正）。

### 介面變更

- `ViewProps` 新增 `region` 欄位
- `SidebarRegion.tsx` 呼叫 `<ActiveComponent>` 時補傳 `region` / `workspaceId` / `hostId`
  - `workspaceId` 取自 `useWorkspaceStore(s => s.activeWorkspaceId)`
  - `hostId` 取自 `useHostStore(s => s.activeHostId)`

### 不改動的部分

- `ViewDefinition` 介面不需修改
- `defaultRegion` 已足夠指定預設 placement

## 3. Module Config 系統（#244）

### 設計原則

Module 的設定參數不是 workspace 或 settings 本身的屬性，而是各 module 透過 registry 宣告的設定需求。系統提供泛用儲存介面，分為 workspace 層級和全域層級兩層。

### 型別定義

```typescript
// module-registry.ts — 共用 config 定義
interface ConfigDef {
  key: string                              // e.g. 'projectPath'
  type: 'string' | 'boolean' | 'number'   // 欄位型別
  label: string                            // UI 顯示名稱
  required?: boolean
  defaultValue?: unknown
}

interface ModuleDefinition {
  // ...existing fields
  workspaceConfig?: ConfigDef[]            // per-workspace 設定
  globalConfig?: ConfigDef[]               // 全域設定
}

// types/tab.ts — Workspace 泛用儲存
interface Workspace {
  // ...existing fields
  moduleConfig: Record<string, Record<string, unknown>>  // moduleId → config
}

// createWorkspace() 初始化
function createWorkspace(name: string): Workspace {
  return { ...existingFields, moduleConfig: {} }
}
```

### Workspace 層級

**儲存：** `Workspace.moduleConfig`，persist 到 workspace store。

**Store Action：** `setModuleConfig: (wsId: string, moduleId: string, key: string, value: unknown) => void`

**Safe spread（相容舊 persist 資料）：** `setModuleConfig` 實作須使用安全 spread，因為舊 persist 資料的 workspace 不含 `moduleConfig` 欄位：
```typescript
{ ...ws, moduleConfig: { ...(ws.moduleConfig ?? {}), [moduleId]: { ...(ws.moduleConfig?.[moduleId] ?? {}), [key]: value } } }
```
所有讀取也一律使用 optional chaining（如 `workspace.moduleConfig?.files?.projectPath`）。

**Settings UI：** Workspace settings 頁面根據 registry 的 `workspaceConfig` 定義**自動產生**表單欄位。

### 全域層級

**儲存：** 新建 `useModuleConfigStore.ts`，內含 `globalConfig: Record<string, Record<string, unknown>>`，persist 到 storage（新增 `STORAGE_KEYS.MODULE_CONFIG`）。

**Store Action：** `setGlobalModuleConfig: (moduleId: string, key: string, value: unknown) => void`

**Settings UI：** Settings 頁面（global scope）根據 registry 的 `globalConfig` 定義，自動在各 module 名稱下產生設定區塊。

### 存取方式

```typescript
// Workspace 層級 — 讀取 / 寫入
const path = workspace.moduleConfig?.files?.projectPath as string
setModuleConfig(wsId, 'files', 'projectPath', '/some/path')

// 全域層級 — 讀取 / 寫入
const val = getGlobalModuleConfig('files', 'someKey')
setGlobalModuleConfig('files', 'someKey', value)
```

### Registry 查詢

```typescript
// 取得所有有 workspaceConfig 的 module
getModulesWithWorkspaceConfig(): ModuleDefinition[]

// 取得所有有 globalConfig 的 module
getModulesWithGlobalConfig(): ModuleDefinition[]
```

### Files Module 註冊範例

```typescript
registerModule({
  id: 'files',
  workspaceConfig: [
    { key: 'projectPath', type: 'string', label: '專案路徑' }
  ],
  views: [...]
})
```

### 設定入口

- **Files sidebar view**：開啟時若 `moduleConfig.files.projectPath` 為空 → 顯示引導畫面（路徑輸入 + 確認按鈕）
- **Workspace settings 頁面**：自動產生各 module 的 workspace config 表單
- **Global settings 頁面**：自動產生各 module 的 global config 表單

### View 行為

| View | 路徑來源 | 無路徑時 |
|------|----------|----------|
| `file-tree-workspace` | `workspace.moduleConfig.files.projectPath` | 顯示「請設定專案路徑」引導 UI |
| `file-tree-session` | Active terminal 的 cwd（透過 tmux `display-message -p '#{pane_current_path}'`，需 daemon 新增 API） | 顯示「無可用路徑」提示 |

**Daemon API 需求：** 需新增端點（如 `GET /api/sessions/:code/cwd`）回傳 tmux session 當前工作目錄。若 daemon 端尚未實作，`file-tree-session` 先 defer，優先完成其餘項目。

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

### 4.2 四宮格水平拖曳聯動

現狀：`grid-4` 佈局為 `split-v > [split-h, split-h]`（外層垂直分割，兩個子節點各自水平分割）。外層垂直 splitter 由 `split-v` 統一管理已可聯動，但左側與右側的 `split-h` 水平分割比例各自獨立，無法同步調整欄寬。

修正：在 `PaneLayoutRenderer` 層級偵測 grid-4 結構（外層 `split-v` 包含兩個 `split-h` 子節點），讓兩個 `split-h` 的 splitter **聯動**。拖曳任一側的水平 splitter 時，同步調整另一側的 `sizes`。

實作方式：
- `PaneLayoutRenderer` 在頂層偵測 grid pattern（外層 `split-v` 含兩個 `split-h` children）
- 偵測到 grid 時，不走遞迴渲染，改用專用 `GridRenderer` 邏輯：統一管理兩個 `split-h` 的水平 splitter
- 拖曳任一水平 splitter 時，透過 `useTabStore.resizePanes` 同時更新兩個 `split-h` 節點的 `sizes`（用各自的 splitId）
- 兩個 container 的 `offsetWidth` 各自計算百分比，但 sizes 值保持同步

### 4.3 Pane 彈出位置

現狀：`useTabStore.detachPane` 建立新 tab 後加入 `tabOrder`，但不會自動加入 workspace。`PaneLayoutRenderer.tsx` 的 `onDetach` handler 呼叫 detach 後需自行處理 workspace 整合。

修正：
1. `useTabStore.detachPane` 接受 optional `afterTabId` 參數，新 tab 插入到 `tabOrder` 中 `afterTabId` 的下一個位置（而非 append 到尾端）
2. `workspace/store.ts` 的 `insertTab` 新增 optional `afterTabId` 參數，在 `ws.tabs` 陣列中正確插入位置（而非 append）
3. `PaneLayoutRenderer.tsx` 的 `onDetach` handler 呼叫 detach 後，呼叫 `useWorkspaceStore.insertTab(newTabId, workspaceId, afterTabId)` 將新 tab 加入當前 workspace，插入到當前 tab 的下一位

**邊界情況：** 若當前 tab 為 pinned，detach 出的新 tab 預設為 unpinned，插入到 pinned group 之後的第一個位置。

### 4.4 Tab 右鍵「加入 Pane」

`TabContextMenu` 新增選項：

- 條件：workspace 中有其他 tab 含 split layout（`layout.type === 'split'`）
- 顯示 submenu：列出所有含 split layout 的 tab（e.g.「加入 Tab A 成為 pane」）
- 點擊行為：
  1. 將被右鍵的 tab 的 primary pane content 作為新 pane
  2. 使用 `splitAtPane(targetLayout, targetPrimaryPaneId, 'h', content)` 在目標 tab 的 primary pane 旁新增
  3. 關閉原 tab

**介面變更：**
- `ContextMenuAction` 新增 `'mergeToTab'`
- `onAction` callback 簽名改為 `(action: ContextMenuAction, payload?: string) => void`，`mergeToTab` 時 `payload` 為目標 tab ID
- `TabContextMenu` 新增 `targetTabs?: Tab[]` prop，由呼叫方從 workspace store 過濾含 split layout 的 tab（排除自身）後傳入
- 所有使用 `TabContextMenu` 的父元件（`TabBar.tsx` 等）需更新 `onAction` handler 簽名以接受 `payload` 參數，並處理 `mergeToTab` action

### 4.5 Pane 內容交換（移動）

`PaneHeader` 工具列新增「移動」按鈕：

- 點擊後顯示同 tab 內其他 pane 的列表
- 選擇後 **swap** 兩個 pane 的 content
- 新增 `pane-tree.ts` 工具函數：`swapPaneContent(layout, paneIdA, paneIdB)`

## 5. TopBar Region Toggle 按鈕

### 位置

`TitleBar.tsx` 右側（與現有 pane layout buttons 同一區域），順序：`[region toggles] [分隔線] [pane layout buttons]`

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
- 僅在 region 有註冊 view 時才顯示對應按鈕（`TitleBar` 訂閱 `useLayoutStore`，根據 `regions[x].views.length > 0` 決定）

### 視覺

- Region toggles 與 pane layout buttons 之間用細分隔線分隔

## 6. 改動範圍總表

| 區域 | 檔案 | 改動 |
|------|------|------|
| Module Registry | `module-registry.ts` | `ViewProps` 新增 `region`；新增 `ConfigDef` 型別 + `ModuleDefinition.workspaceConfig` / `globalConfig`；新增查詢函數 |
| Module Registry | `register-modules.tsx` | files 拆成 2 個 view + 宣告 `workspaceConfig` |
| Workspace Store | `workspace/store.ts`, `types/tab.ts` | `Workspace` 新增 `moduleConfig` 欄位 + `setModuleConfig` action + `createWorkspace` 初始化 + `insertTab` 新增 `afterTabId` 參數 |
| Module Config Store | 新建 `useModuleConfigStore.ts` | 全域 module config 儲存 + `setGlobalModuleConfig` action |
| Storage Keys | `storage.ts` | 新增 `MODULE_CONFIG` key |
| Settings UI | settings 相關元件 | Workspace / Global settings 頁面自動產生 module config 表單 |
| FileTreeView | `FileTreeView.tsx` → 拆成兩個 component | `FileTreeWorkspaceView` + `FileTreeSessionView` |
| SidebarRegion | `SidebarRegion.tsx` | 補傳 `region` / `workspaceId` / `hostId` 給 view component |
| PaneSplitter | `PaneSplitter.tsx` | 加強視覺 |
| PaneLayoutRenderer | `PaneLayoutRenderer.tsx` | grid-4 偵測 + 專用 GridRenderer + detach handler 補 workspace insertTab |
| PaneHeader | `PaneHeader.tsx` | 工具按鈕視覺加強 + swap 功能 |
| Pane detach | `useTabStore.ts` | `detachPane` 新增 `afterTabId` 參數 |
| TabContextMenu | `TabContextMenu.tsx` | 新增 `mergeToTab` action + `targetTabs` prop + `onAction` 簽名變更 |
| TabContextMenu 父元件 | `TabBar.tsx` 等 | 更新 `onAction` handler 簽名 + 處理 `mergeToTab` + 傳入 `targetTabs` |
| TitleBar | `TitleBar.tsx` | 新增 4 個 region toggle 按鈕 |
| Pane tree utils | `pane-tree.ts` | 新增 `swapPaneContent` 工具函數 |
| Tests | `useTabStore.split.test.ts`, `pane-tree.test.ts` | 更新 detachPane 測試 + 新增 swapPaneContent 測試 |

## 7. 不改動的部分

- `ViewDefinition` 介面
- Sidebar / Panel region 架構（4-region layout）
- Pane layout pattern 系統（`LayoutPattern` type）

## 8. 風險點

- **Grid-4 水平聯動**：頂層偵測 grid pattern 後需跳過遞迴渲染，改用專用 GridRenderer 統一管理兩個 `split-h` 的 splitter
- **Tab 右鍵 submenu**：`TabContextMenu` 需透過 `targetTabs` prop 存取其他 tab 的 layout 資訊
- **Daemon cwd API**：`file-tree-session` 依賴尚未實作的 daemon 端 API，若 daemon 未準備好則此 view 需 defer
- **Pinned tab detach**：detach 出的新 tab 預設 unpinned，插入位置需跳過 pinned group
