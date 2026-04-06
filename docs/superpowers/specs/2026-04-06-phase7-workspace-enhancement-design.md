# Phase 7：Workspace 強化

**日期**: 2026-04-06
**狀態**: Draft
**前置**: Phase 6（Hooks Unification）完成
**基於**: [tabbed-workspace-ui-design.md](2026-03-20-tabbed-workspace-ui-design.md) Section 4-5

---

## 1. 目標

在既有 Workspace + Activity Bar 基礎上，強化工作區為獨立模組——完善 tab 資料連結、位置制快捷鍵切換、名稱/圖示/顏色設定。

---

## 2. 現況

已實作：
- `useWorkspaceStore`：基本 CRUD（建立/刪除/重新排序）
- `Workspace` type：`id/name/color/icon/tabs/activeTabId`
- `ActivityBar`：工作區圖示列 + 點擊切換 + 右鍵選單
- Tab 跨工作區搬移：基本支援

未實作：
- 快捷鍵切換
- 工作區設定 UI（名稱/顏色/icon 編輯）
- Tab 與 workspace 的完整生命週期管理

---

## 3. 工作項目

### 7.1 Workspace 模組化

確保 workspace 作為獨立模組的完整性：

- 審視既有 `useWorkspaceStore` 的 API 是否完備
- Tab 生命週期：建立 tab 時自動加入當前 workspace、關閉 tab 時自動移除
- Workspace 刪除時的 tab 處理策略（tab 變為獨立 / 一併刪除 / 詢問）
- 確認 `Workspace` type 不需新增欄位（既有 `id/name/color/icon/tabs/activeTabId` 已足夠）

### 7.2 Workspace 設定 UI

進入方式：
- Activity Bar 工作區圖示右鍵 →「設定」

設定面板內容：
- 名稱編輯
- 顏色選擇器（既有色盤 + 自訂色碼）
- Icon 選擇（Phosphor Icons 列表或 emoji）

### 7.3 快捷鍵切換（位置制）

與既有 tab 切換快捷鍵並存，不衝突：

| 快捷鍵 | 動作 | 備註 |
|--------|------|------|
| `⌘1`-`⌘8` | 切換 tab（既有，不動） | workspace 內的 tab |
| `⌘9` | 最後一個 tab（既有，不動） | |
| `⌘⌥1`-`⌘⌥9` | 跳至第 N 個 workspace | 位置制，依 Activity Bar 排序 |
| `⌘⌥↑` / `⌘⌥↓` | 前/後 workspace 循環切換 | |

實作要點：
- Electron 端：`keybindings.ts` 新增 `switch-workspace-1` ~ `switch-workspace-9` + `prev-workspace` / `next-workspace`
- SPA 端：`useShortcuts.ts` 新增 workspace 切換 handler
- 位置根據 `workspaces` 陣列順序（即 Activity Bar 的排列順序）
- 無 workspace 時快捷鍵靜默忽略

---

## 4. 依賴關係

```
7.1 Workspace 模組化
 ├→ 7.2 設定 UI
 └→ 7.3 快捷鍵
```

7.2 和 7.3 彼此無依賴，可並行。

---

## 5. 測試策略

- 7.1：store unit test（tab 加入/移除/workspace 刪除 cascade）
- 7.2：component test（設定面板開關、名稱/顏色/icon 儲存）
- 7.3：快捷鍵 action dispatch + workspace 切換正確性
