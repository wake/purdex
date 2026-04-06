# Phase 10：Workspace 強化

**日期**: 2026-04-06
**狀態**: Draft
**前置**: Phase 6（Hooks Unification）完成
**基於**: [tabbed-workspace-ui-design.md](2026-03-20-tabbed-workspace-ui-design.md) Section 4-5

---

## 1. 目標

在既有 Workspace + Activity Bar 基礎上，強化工作區為獨立 feature module——完善 tab 資料連結、位置制快捷鍵切換、名稱/圖示/顏色設定。同時建立 `features/` 架構慣例。

---

## 2. 現況

已實作：
- `useWorkspaceStore`：基本 CRUD（建立/刪除/重新排序）
- `Workspace` type：`id/name/color/icon/tabs/activeTabId`
- `ActivityBar`：工作區圖示列 + 點擊切換 + 右鍵選單
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
- 無 workspace 時：Activity Bar workspace 區塊為空，tab bar 顯示所有 tab
- 建立第一個 workspace 時：詢問使用者是否要把現有 tab 移入
- Workspace 刪除時：若有分頁，顯示確認對話框，列出所有分頁供勾選關閉，未勾選的回歸 standalone

### 3.2 Feature Module 架構

新功能採用 `features/` 結構，既有程式碼不追溯重構：

```
spa/src/features/{name}/
  ├── store.ts           # Zustand store
  ├── types.ts           # Feature type 定義
  ├── hooks.ts           # Feature-specific hooks
  ├── components/        # Feature-specific components
  ├── lib/               # Feature-specific utilities
  └── index.ts           # Public API — 外部只透過 index 引用
```

規則：
- Feature 之間透過 `index.ts` 互相引用，不深入 import 內部檔案
- 共用的東西留在頂層 `lib/`、`types/`、`stores/`
- 既有 `components/hosts/`、`components/settings/` 等不搬遷

---

## 4. 工作項目

### 10.0 建立 features/ 架構 + 搬遷 workspace

建立 `features/workspace/` 並搬遷既有 workspace 相關程式碼：

- `stores/useWorkspaceStore.ts` → `features/workspace/store.ts`
- `types/tab.ts` 中 `Workspace` / `createWorkspace` / `isStandaloneTab` → `features/workspace/types.ts`
- `hooks/useTabWorkspaceActions.ts` 中 workspace 邏輯 → `features/workspace/hooks.ts`
- `components/ActivityBar.tsx` → `features/workspace/components/ActivityBar.tsx`
- 更新所有 import path
- 建立 `features/workspace/index.ts` 匯出 public API

### 10.1 Workspace 模組化（全自由制）

- 移除 `workspaces.length <= 1` 刪除守衛，允許 0 workspace
- 移除 `createDefaultState()` 的預設 workspace（初始為空陣列）
- Tab 生命週期 helper：`insertTab(tabId, workspaceId?)` 封裝加入 workspace + set active 邏輯
- 收斂散落在 App.tsx、useShortcuts.ts 等處的重複 addTabToWorkspace + setWorkspaceActiveTab 呼叫
- 建立第一個 workspace 時的遷移對話框
- Store migration：version bump，既有使用者的 Default workspace 保留

### 10.2 Workspace 刪除確認 UI

- `WorkspaceDeleteDialog` 元件
- 顯示 workspace 內所有分頁清單
- 每個分頁前方有「關閉」checkbox（預設 checked）
- 確認後：checked 的 tab 關閉，unchecked 的回歸 standalone
- 無分頁時直接刪除，不彈對話框

### 10.3 Workspace 設定 UI

**Titlebar Chip + 右鍵選單（方案 3+5）：**

- Titlebar 左側（traffic lights 右、tabs 左）顯示 workspace chip：色點 + 名稱 + 下拉箭頭
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
- 10.1：store unit test（0 workspace 狀態、insertTab helper、建立首個 workspace 遷移）
- 10.2：WorkspaceDeleteDialog component test（checkbox 切換、確認/取消、tab 狀態）
- 10.3：設定面板 component test（名稱/顏色/icon 儲存）
- 10.4：快捷鍵 action dispatch + workspace 切換正確性
