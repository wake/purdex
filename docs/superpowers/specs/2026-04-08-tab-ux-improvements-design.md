# Tab UX 改善設計規格

**日期**: 2026-04-08
**範圍**: SPA Tab 系統的 8 項 UX 改善

## 概述

針對 tab 系統的操作體驗進行一系列改善，涵蓋右鍵選單、new tab 頁面、鍵盤導航、瀏覽紀錄等面向。

---

## 1. Tab 右鍵 → Rename Session

**目標**: 直接從 tab 右鍵選單重新命名 tmux session。

### 設計

- `ContextMenuAction` 新增 `'rename'` 類型
- 僅 `tmux-session` 且非 `terminated` 的 tab 才顯示此選項
- 選擇 rename 後，在 **tab 正下方** 彈出 inline input popover（非 modal）
  - 錨定右鍵所點的 tab 元素，水平置中對齊
  - 左右不超出 viewport（同 `TabContextMenu` 的 boundary correction）
  - 預填現有 session name
- Enter 確認 → 呼叫 `renameSession(hostId, code, newName)`
- Escape / click outside → 取消
- 成功後 daemon session watcher 透過 WS 推送更新，`updateSessionCache` 自動同步所有 tab
- **錯誤處理**: API 失敗時在 popover 內顯示紅色錯誤訊息，不關閉 popover，使用者可重試或 Escape 取消

### 元件

- `RenamePopover`: 新元件，props 為 `anchorRect`, `currentName`, `onConfirm`, `onCancel`
- 定位邏輯: `left = anchorRect.left + anchorRect.width/2 - popoverWidth/2`，clamp 到 `[4, viewportWidth - popoverWidth - 4]`
- `top = anchorRect.bottom + 4`

### 觸發流程

1. 右鍵 tab → context menu 顯示 "Rename Session"
2. 點擊 → context menu 關閉，RenamePopover 出現在 tab 下方
3. 使用者編輯 → Enter → API call → popover 關閉
4. WS event → session name 全域更新

---

## 2. Browser 區塊移到 New Tab 最上方

**目標**: URL 輸入欄在 new tab 頁面最優先。

### 設計

- `register-panes.tsx` 中 browser provider 的 `order` 從 `10` 改為 `-10`
- 排序後 browser 在 sessions 之前

---

## 3. 切換到 New Tab 時自動 Focus URL Input

**目標**: 開新 tab 或切換到 new-tab 頁面時，自動聚焦 URL 輸入欄。

### 設計

- `BrowserNewTabSection` 的 `<input>` 使用 `ref` + `useEffect` 聚焦（不用 `autoFocus`，因為 `autoFocus` 只在首次 mount 生效，tab 切換回來時不會重新觸發）
- 監聯方式：`NewTabPage` 每次 render 時觸發子元件重新 mount（因 `setPaneContent` 切換 content kind 會卸載/掛載），所以 `useEffect(() => ref.current?.focus(), [])` 即可
- 配合 item 2（browser 在最上方），視覺上開新 tab 就直接可以打字

---

## 4. 移除 Memory Monitor New Tab 區段

**目標**: new tab 頁面不再顯示 Memory Monitor 入口。

### 設計

- 移除 `register-panes.tsx` 中 `memory-monitor` 的 `registerNewTabProvider` 呼叫及其 import
- 刪除 `MemoryMonitorNewTabSection.tsx`（移除 provider 後已無引用處）
- `MemoryMonitorPage.tsx` 保留（pane renderer 仍使用）

---

## 5. URL 欄位歷史下拉 + Auto Filter

**目標**: URL 輸入欄帶出歷史瀏覽紀錄，輸入時即時過濾。

### Store

- 新增 `useBrowserHistoryStore`（Zustand + persist）
- State: `{ urls: string[] }`
- Actions:
  - `addUrl(url: string)`: 去重後加到陣列頭部，上限 100 筆（FIFO）
- 持久化 key: `STORAGE_KEYS.BROWSER_HISTORY`（需在 `storage/keys.ts` 新增）
- 不需 `syncManager.register`（browser history 不需跨視窗同步）

### 記錄時機

- 在 `BrowserNewTabSection` 的 `onSelect` 回調中記錄（URL 已通過 `new URL()` 驗證，使用者意圖明確即可記錄）

### UI

- `BrowserNewTabSection` 改為 controlled dropdown：
  - 輸入時顯示 dropdown，以 `includes` 過濾歷史 URL
  - 空輸入時顯示全部歷史（最近 N 筆）
  - 點選項目 → 直接開啟 browser tab
  - 鍵盤：`↑`/`↓` 在 dropdown 中移動 highlight，`Enter` 選擇 highlight 項目或提交當前輸入
  - `Escape` 關閉 dropdown（不關閉 new tab）
  - Click outside dropdown → 關閉 dropdown

---

## 6. Tab 鍵切換到 Session List + kj/上下選擇

**目標**: 從 URL 輸入欄按 Tab 可跳到 session list，用鍵盤選擇 session。

### 設計

- `NewTabPage` 統一管理 focus 區域：URL input → session list
- URL input 按 `Tab` → 阻止預設行為，focus 移到 session list 第一項
- Session list 的 session button 支援鍵盤：
  - `↑` / `k`: 上移 focus
  - `↓` / `j`: 下移 focus
  - `Enter`: 選擇該 session（觸發 `onSelect`）
  - `Shift+Tab`: 回到 URL input
- 實作方式：session buttons 設定 `tabIndex={0}`，用 `onKeyDown` + DOM focus management
- 導航方式：用 `parentElement.querySelectorAll('button')` 取得所有可互動按鈕（避免 `previousElementSibling` 在多 host 場景下 focus 到 header div）

---

## 7. Tab Close → 按瀏覽紀錄回到上一個 Tab

**目標**: 關閉 tab 時回到上一次檢視的 tab，而非相鄰 tab。

### 設計

- `useTabStore` 新增 state: `visitHistory: string[]`
- **不持久化**（`partialize` 排除），僅 runtime 追蹤

### 記錄邏輯（在 `setActiveTab` 中）

```
if (currentActiveTabId !== null && currentActiveTabId !== newId) {
  // 移除 history 中已有的 newId（避免重複）
  // push currentActiveTabId 到 visitHistory 末端
}
```

### 回退邏輯（在 `closeTab` 中）

```
if (closedTabId === activeTabId) {
  // 從 visitHistory 末端往前找第一個仍存在於 tabs 中的 ID
  // 找到 → 設為 activeTabId，清理 history 中已失效的 ID
  // 找不到 → fallback 到相鄰 tab（現有邏輯）
}
// 清理 visitHistory 中等於 closedTabId 的項目
```

### 清理

- `closeTab` 時從 `visitHistory` 中移除被關閉的 tab ID
- 不做主動全量清理，保持簡單

---

## 8. 點擊已 Active 的 Tab 不搶 Focus

**目標**: 點擊已經 active 的 tab 時，focus 保持在內容區域。

### 設計

- `SortableTab` 的 `onPointerDown`（配合 dnd-kit 的事件模型，不用 `onMouseDown`）：若該 tab 已是 active 且非拖曳中，呼叫 `e.preventDefault()` 阻止 focus 移動到 tab button
- 這樣 focus 自然留在 content area（terminal、browser 等）
- 點擊非 active tab 則正常切換（focus 會在切換後由 content 元件接管）
- **兩個 render path 都要處理**：pinned tab（`<button>`）和 normal tab 都需加上相同邏輯

---

## 不在範圍內

- 獨立的 tab 命名功能（rename 只改 tmux session name）
- Memory Monitor pane renderer 移除（仍可從其他入口開啟）
- URL 歷史的匯出/匯入
