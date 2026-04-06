# Phase 7：Workspace 強化

**日期**: 2026-04-06
**狀態**: Draft
**前置**: Phase 6（Hooks Unification）完成
**基於**: [tabbed-workspace-ui-design.md](2026-03-20-tabbed-workspace-ui-design.md) Section 4-5
**補充**: [workspace-and-editor-module-design.md](2026-04-06-workspace-and-editor-module-design.md) Section 2

---

## 1. 目標

在既有 Workspace + Activity Bar 基礎上，強化工作區為「帶有操作 context 的分頁群組」。新增預設 host/path、快捷鍵切換、quick actions，讓工作區不只是分組而是工作流程的起點。

---

## 2. 現況

已實作：
- `useWorkspaceStore`：基本 CRUD（建立/刪除/重新排序）
- `Workspace` type：`id/name/color/icon/tabs/activeTabId`
- `ActivityBar`：工作區圖示列 + 點擊切換 + 右鍵選單
- Tab 跨工作區搬移：基本支援

未實作：
- 預設 host/path
- 快捷鍵切換
- Quick actions
- Workspace dashboard
- 工作區設定 UI

---

## 3. 工作項目

### 7.1 擴充 Workspace Type

```typescript
interface Workspace {
  // 既有
  id: string
  name: string
  color: string
  icon?: string
  tabs: string[]
  activeTabId: string | null

  // 新增
  defaultHostId?: string   // 快速操作預設 host
  defaultPath?: string     // 快速操作預設工作目錄
  hotkey?: string          // 切換快捷鍵
}
```

- Store migration：version bump + 新欄位預設值
- 不影響既有持久化資料（新欄位皆 optional）

### 7.2 Workspace 設定 UI

進入方式：
- Activity Bar 工作區圖示右鍵 →「設定」
- 工作區 dashboard 內

設定面板內容：
- 名稱編輯
- 顏色選擇器（既有色盤 + 自訂）
- Icon 選擇（Phosphor Icons 列表 或 emoji）
- 預設 Host 下拉（從 hostStore 取 host 清單）
- 預設 Path 輸入（搭配 daemon fs API 做路徑補全，若 fs API 尚未實作則先用純文字輸入）
- 快捷鍵綁定

### 7.3 快捷鍵切換

- 每個工作區可綁定一組快捷鍵
- 按下 → 切換到該工作區（等同點擊 Activity Bar）
- 衝突偵測：設定時比對系統快捷鍵 + 其他工作區已綁定的快捷鍵
- 預設不綁定，由使用者自行設定
- 儲存在 workspace store，隨工作區持久化

### 7.4 Quick Actions

基於 `defaultHostId` + `defaultPath` 提供的快速操作：

| Action | 條件 | 行為 |
|--------|------|------|
| 新增 Terminal Session | defaultHostId 已設 | 在預設 host + path 建立 tmux session，自動加入工作區 |
| 啟動 Claude Code | defaultHostId + defaultPath 已設 | 在預設 path 啟動 `claude -p` stream session |
| 啟動 Codex | defaultHostId + defaultPath 已設 | 在預設 path 啟動 codex CLI stream session |
| 開啟目錄 | defaultHostId + defaultPath 已設 | 在 file tree panel 開啟預設 path（Phase 9 才有） |

觸發入口：
- Activity Bar 右鍵選單
- Workspace dashboard（若啟用）

若 defaultHostId/Path 未設定，quick action 按鈕 disabled 或不顯示。

### 7.5 Workspace Dashboard

工作區專屬的 dashboard tab，選用功能：

- 工作區 session 狀態總覽
- 預設 host 連線狀態
- Quick actions 按鈕列
- 最近操作的檔案（Phase 9 才有完整支援）

新增 pane type：`{ kind: 'workspace-dashboard'; workspaceId: string }`

---

## 4. 依賴關係

```
7.1 Workspace Type 擴充
 ├→ 7.2 設定 UI
 ├→ 7.3 快捷鍵
 └→ 7.4 Quick Actions
      └→ 7.5 Dashboard
```

7.1 是地基，其餘可並行但 7.5 依賴 7.4。

---

## 5. 測試策略

- 7.1：store unit test（migration、新欄位 CRUD）
- 7.2：component test（設定面板開關、欄位儲存）
- 7.3：快捷鍵註冊/衝突偵測 unit test + 整合測試
- 7.4：quick action 建立 session 的 flow test
- 7.5：dashboard 渲染 + 資料正確性
