# Phase 10：Workspace 強化

**日期**: 2026-04-06
**狀態**: Review
**前置**: Phase 6（Hooks Unification）完成
**基於**: [tabbed-workspace-ui-design.md](2026-03-20-tabbed-workspace-ui-design.md) Section 4-5
**取代**: tabbed-workspace-ui-design.md Section 6（橫列式群組行為）改為 Titlebar Chip 方案

---

## 1. 目標

在既有 Workspace + Activity Bar 基礎上，強化工作區為獨立 feature module——完善 tab 資料連結、位置制快捷鍵切換、名稱/圖示/顏色設定。同時建立 `features/` 架構慣例。

---

## 2. 現況

已實作：
- `useWorkspaceStore`：基本 CRUD（建立/刪除/重新排序）
- `Workspace` type：`id/name/color/icon/tabs/activeTabId`
- `ActivityBar`：工作區圖示列 + 點擊切換（無右鍵選單）
- Tab 跨工作區搬移：基本支援

未實作：
- `features/` 架構慣例
- 全自由制（允許 0 workspace）
- 快捷鍵切換
- 工作區設定 UI（名稱/顏色/icon 編輯）
- Tab 與 workspace 的完整生命週期管理
- Workspace 刪除確認 UI

---

## 3. 設計決策

### 3.1 全自由制

- 允許 0 個 workspace，workspace 為可選的分組機制
- `activeWorkspaceId` 型別從 `string` 改為 `string | null`，0 workspace 時為 `null`
- 無 workspace 時：
  - Activity Bar workspace 區塊為空
  - Tab bar 顯示所有 tab（fallback 到 `tabOrder`）
  - Titlebar chip 隱藏
- 建立第一個 workspace 時：若已有 standalone tab，詢問使用者是否要把現有 tab 移入
- Workspace 刪除時：若有分頁，顯示確認對話框，列出所有分頁供勾選關閉，未勾選的回歸 standalone
- 刪除最後一個 workspace 後，`activeWorkspaceId` 設為 `null`

### 3.2 Feature Module 架構

新功能採用 `features/` 結構，既有程式碼不追溯重構：

```
spa/src/features/{name}/
  ├── store.ts           # Zustand store
  ├── types.ts           # Feature-specific type（非共用）
  ├── hooks.ts           # Feature-specific hooks
  ├── components/        # Feature-specific components
  ├── lib/               # Feature-specific utilities
  └── index.ts           # Public API — 外部只透過 index 引用
```

規則：
- Feature 之間透過 `index.ts` 互相引用，不深入 import 內部檔案
- **共用型別留在頂層** `types/`——`Workspace` interface 留在 `types/tab.ts`，feature 的 `index.ts` re-export
- 共用的東西留在頂層 `lib/`、`types/`、`stores/`
- 既有 `components/hosts/`、`components/settings/` 等不搬遷

### 3.3 與既有 UI Spec 的關係

本 spec 的 Titlebar Chip + 右鍵選單方案（10.3），**取代** `tabbed-workspace-ui-design.md` Section 6 的橫列式群組展開/收合設計。原因：Electron frameless window 的 titlebar 與 tab bar 同行，無法在 tab bar 上方加 workspace header。

---

## 4. 工作項目

### 10.0 建立 features/ 架構 + 搬遷 workspace

建立 `features/workspace/` 並搬遷既有 workspace 相關程式碼：

**搬遷對象：**
- `stores/useWorkspaceStore.ts` → `features/workspace/store.ts`
- `hooks/useTabWorkspaceActions.ts` → `features/workspace/hooks.ts`（workspace 邏輯）
- `components/ActivityBar.tsx` → `features/workspace/components/ActivityBar.tsx`

**共用型別——留在原位：**
- `types/tab.ts` 中的 `Workspace` interface、`createWorkspace`、`isStandaloneTab` 留在 `types/tab.ts`
- `features/workspace/index.ts` re-export 這些型別供消費方使用

**需更新 import path 的檔案：**
- `spa/src/App.tsx`
- `spa/src/hooks/useShortcuts.ts`
- `spa/src/hooks/useNotificationDispatcher.ts`
- `spa/src/components/hosts/SessionsSection.tsx`
- `spa/src/components/SortableTab.tsx`
- `spa/src/stores/useWorkspaceStore.test.ts`（隨 store 搬遷）
- `spa/src/hooks/useShortcuts.test.ts`
- `spa/src/components/hosts/SessionsSection.test.tsx`

**建立：**
- `features/workspace/index.ts`：匯出 public API（store、hooks、re-export 共用型別）

### 10.1 Workspace 模組化（全自由制）

**Store 型別變更：**
- `activeWorkspaceId: string` → `activeWorkspaceId: string | null`
- `createDefaultState()` 改為 `{ workspaces: [], activeWorkspaceId: null }`
- `removeWorkspace` 移除 `workspaces.length <= 1` 守衛，刪除最後一個時設 `activeWorkspaceId: null`
- `reset()` 對應修改

**Store Migration（version 1 → 2）：**
- 新增 `migrate` callback
- 既有使用者：保留原有 workspace 和 activeWorkspaceId 不變（向下相容）
- 新裝置：初始為 `workspaces: []`、`activeWorkspaceId: null`

**Tab 生命週期 helper：**
- `insertTab(tabId, workspaceId?)` 封裝「加入 workspace + set active」邏輯
- `workspaceId` 為 `undefined/null` 時 tab 成為 standalone
- 替換以下散落的重複呼叫：
  - `App.tsx`（3 處：tearOff tab、onOpenHosts、onOpenSettings）
  - `useShortcuts.ts`（3 處：open-settings、open-history、reopen-closed-tab）
  - `useTabWorkspaceActions.ts:handleAddTab`
  - `hooks/useNotificationDispatcher.ts`
  - `components/hosts/SessionsSection.tsx`

**0 workspace 時的 displayTabs 邏輯：**
- 目前 `App.tsx` 在 `activeWs` 為 `undefined` 時 `visibleTabs` 為 `[]`，需修正
- 0 workspace 時 fallback 為 `tabOrder` 全部 tab

**首個 workspace 建立詢問：**
- 觸發位置：`addWorkspace` action 或 ActivityBar 的 `onAddWorkspace` callback
- 條件：`workspaces.length === 0 && tabOrder.length > 0` 時彈出
- 選「是」→ 批次 `addTabToWorkspace`，選「否」→ 建空 workspace

**既有 bug 修正：**
- `createWorkspace(name, color?)` 加入 `icon?` 參數（目前 `addWorkspace` 接收 `icon` 但未傳給工廠函式）

### 10.2 Workspace 刪除確認 UI

- `WorkspaceDeleteDialog` 元件
- Props：接收 `workspaceId`，內部讀取 `useWorkspaceStore` 取 tab ID 列表 + `useTabStore` 解析 tab 顯示名稱（透過 `getPaneLabel`）
- 顯示 workspace 內所有分頁清單，每個分頁前方有「關閉」checkbox（預設 checked）
- 確認後：checked 的 tab 關閉，unchecked 的回歸 standalone
- 無分頁時直接刪除，不彈對話框

### 10.3 Workspace 設定 UI

**Titlebar Chip + 右鍵選單（方案 3+5）：**

- **Electron 模式**：Titlebar 左側（traffic lights 右、tabs 左）顯示 workspace chip：色點 + 名稱 + 下拉箭頭
- **SPA 模式**（瀏覽器）：chip 放在 TabBar 最左側（同位置，但無 traffic lights）
- 無 workspace 時 chip 隱藏，tab 佔滿全寬
- 操作透過右鍵選單（Activity Bar icon 或 chip 皆可觸發）：
  - 重新命名
  - 變更顏色
  - 變更圖示
  - 分隔線
  - 刪除工作區（觸發 10.2 確認 UI）
- 詳細樣式（chip 尺寸、顏色、hover 效果等）留實作階段定案

### 10.4 快捷鍵切換（位置制）

與既有 tab 切換快捷鍵並存，不衝突：

| 快捷鍵 | 動作 | 備註 |
|--------|------|------|
| `⌘1`-`⌘8` | 切換 tab（既有，不動） | workspace 內的 tab |
| `⌘9` | 最後一個 tab（既有，不動） | |
| `⌘⌥1`-`⌘⌥9` | 跳至第 N 個 workspace | 位置制，依 Activity Bar 排序 |
| `⌘⌥↑` / `⌘⌥↓` | 前/後 workspace 循環切換 | |

實作要點：
- Electron 端：`keybindings.ts` 新增 `switch-workspace-1` ~ `switch-workspace-9` + `prev-workspace` / `next-workspace`
- `menuCategory: 'Tab'`、`menuGroup: 'workspace-nav'`（新增 group，需更新 `MenuGroup` type）
- SPA 端：workspace hooks 新增快捷鍵切換 handler
- 位置根據 `workspaces` 陣列順序（即 Activity Bar 的排列順序）
- 無 workspace 時快捷鍵靜默忽略

---

## 5. 依賴關係

```
10.0 features/ 架構 + 搬遷
 └→ 10.1 模組化（全自由制）
     ├→ 10.2 刪除確認 UI
     ├→ 10.3 設定 UI
     └→ 10.4 快捷鍵
```

10.2、10.3、10.4 彼此無依賴，可並行。

---

## 6. 測試策略

- 10.0：搬遷後全 test suite pass（純 refactor，無行為變更）
  - 既有 `useWorkspaceStore.test.ts` 隨 store 搬遷，更新 import path
- 10.1：store unit test
  - `activeWorkspaceId: string | null` 型別驗證
  - 0 workspace 狀態（初始化、刪除最後一個）
  - `insertTab` helper（有/無 workspaceId）
  - migration v1→v2（既有資料保留）
  - displayTabs fallback（0 workspace 時顯示 tabOrder）
  - 反轉既有「cannot remove last workspace」測試
- 10.2：WorkspaceDeleteDialog component test（checkbox 切換、確認/取消、tab 狀態）
- 10.3：chip 渲染 + 右鍵選單觸發 + 名稱/顏色/icon 儲存
- 10.4：快捷鍵 action dispatch + workspace 切換正確性 + 0 workspace 時靜默
