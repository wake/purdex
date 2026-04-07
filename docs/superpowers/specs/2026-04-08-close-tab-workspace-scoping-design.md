# Close Tab Workspace Scoping 設計

日期：2026-04-08
觸發：PR #208 post-merge review 發現 6 個問題

## 背景

PR #208 在 `useShortcuts.ts` 的 close-tab handler 加了兩個 guard 防止 cmd+w 跨 workspace 關閉 tab。事後 review 發現多個問題：

1. **Stale snapshot**：post-close 邏輯使用 mutation 前的 `wsStore` 快照計算 `visibleIds`
2. **未呼叫 `setWorkspaceActiveTab`**：close-tab 後 workspace 的 `activeTabId` 留 null，切回時 fallback 到 `tabs[0]`
3. **`findWorkspaceByTab` 重複呼叫**：L58 與 L59 各呼叫一次
4. **`closeTab` 用全域 `tabOrder` 選 tab**：可能選到跨 workspace tab（根因）
5. **Post-close 邏輯應下沉 store 層**：hook 補丁 store 狀態，職責蔓延
6. **Close-last-tab 空 workspace 邊界未測試**

根因：`closeTab` store action 只認全域 `tabOrder`，不認 workspace 邊界。hook 層的事後補丁引入了新問題。

## 方案：Store 層 Composite Action

### 設計原則

- **mutation 前預算**：在任何狀態修改前，就決定好下一個 active tab，消除 stale snapshot 問題
- **單一 action 完成所有狀態同步**：recordClose → removeFromWorkspace → closeTab → sync activeTabId
- **Electron side effect 留 UI 層**：`destroyBrowserView` 不進 store

### 改動清單

#### 1. `useTabStore.closeTab(id)` — 簡化

移除自動選 tab 邏輯。改為只刪除 tab，**不動 `activeTabId`**。

```ts
closeTab: (id) => set((state) => {
  if (!state.tabs[id]) return state
  if (state.tabs[id].locked) return state
  const { [id]: _removed, ...remainingTabs } = state.tabs
  const newOrder = state.tabOrder.filter((tid) => tid !== id)
  return {
    tabs: remainingTabs,
    tabOrder: newOrder,
    // activeTabId 不動，由呼叫端（closeTabInWorkspace）負責
    activeTabId: state.activeTabId === id ? null : state.activeTabId,
  }
})
```

注意：當被關的 tab 正是 activeTabId 時，設為 null（避免指向已刪 tab）。選下一個 tab 的責任交給 `closeTabInWorkspace`。

#### 2. `useWorkspaceStore.closeTabInWorkspace(tabId)` — 新增 Composite Action

```ts
closeTabInWorkspace(tabId: string): void {
  const tabStore = useTabStore.getState()
  const tab = tabStore.tabs[tabId]
  if (!tab || tab.locked) return

  const ws = get().findWorkspaceByTab(tabId)

  // 1. 鄰近 tab 預算（mutation 前，使用 get() 當下狀態）
  let nextTabId: string | null = null
  if (ws) {
    // Workspace-scoped：在 ws.tabs 中找鄰近
    const idx = ws.tabs.indexOf(tabId)
    const remaining = ws.tabs.filter(id => id !== tabId)
    nextTabId = remaining[Math.min(idx, remaining.length - 1)] ?? null
  } else {
    // Standalone tab：在全域 tabOrder 中找鄰近
    const { tabOrder } = useTabStore.getState()
    const idx = tabOrder.indexOf(tabId)
    const remaining = tabOrder.filter(id => id !== tabId)
    nextTabId = remaining[Math.min(idx, remaining.length - 1)] ?? null
  }

  // 2. Record history（mutation 前，tab 物件尚存）
  useHistoryStore.getState().recordClose(tab, ws?.id)

  // 3. Remove from workspace
  if (ws) get().removeTabFromWorkspace(ws.id, tabId)

  // 4. Remove from tab store（不再自動選 tab）
  useTabStore.getState().closeTab(tabId)

  // 5. Sync active tab
  //    - tabStore.activeTabId → workspace-scoped next tab
  //    - ws.activeTabId → 同步
  const isActiveTab = tabStore.activeTabId === tabId
  if (isActiveTab) {
    useTabStore.getState().setActiveTab(nextTabId)
  }
  if (ws && nextTabId) {
    get().setWorkspaceActiveTab(ws.id, nextTabId)
  }
  // nextTabId 為 null 時，removeTabFromWorkspace 已將 ws.activeTabId 清為 null
}
```

重點：
- `findWorkspaceByTab` 只呼叫一次（修 #3）
- 鄰近 tab 在 mutation 前計算（修 #1）
- `setWorkspaceActiveTab` 明確同步（修 #2）
- workspace-scoped 選 tab 取代全域 tabOrder（修 #4）
- 邏輯在 store 層，非 hook 補丁（修 #5）

#### 3. `useShortcuts.ts` close-tab handler — 簡化

```ts
if (action === 'close-tab') {
  const { activeTabId, tabs } = tabState
  if (!activeTabId || !visibleIds.includes(activeTabId)) return
  const tab = tabs[activeTabId]
  if (!tab || tab.locked) return
  destroyBrowserViewIfNeeded(tab)
  useWorkspaceStore.getState().closeTabInWorkspace(activeTabId)
  return
}
```

Pre-close guard（`visibleIds.includes`）留在 hook 層，因為 `visibleIds` 在 handler 頂端統一計算。

新增小 helper：`destroyBrowserViewIfNeeded(tab)` 抽出 browser view 清理邏輯，useShortcuts 和 hooks.ts 共用。

#### 4. `hooks.ts` handleCloseTab — 簡化

```ts
const handleCloseTab = useCallback((tabId: string) => {
  const tab = tabs[tabId]
  if (!tab || tab.locked) return
  destroyBrowserViewIfNeeded(tab)
  useWorkspaceStore.getState().closeTabInWorkspace(tabId)
}, [tabs])
```

#### 5. `destroyBrowserViewIfNeeded` — 共用 helper

```ts
// spa/src/lib/browser-cleanup.ts（或放在 features/workspace/lib/）
export function destroyBrowserViewIfNeeded(tab: Tab): void {
  const primary = getPrimaryPane(tab.layout)
  if (primary.content.kind === 'browser') {
    window.electronAPI?.destroyBrowserView(primary.id)
  }
}
```

#### 6. 測試

新增 / 修改的測試案例：

**workspace/store.test.ts** — `closeTabInWorkspace`：
- 關閉中間 tab → activeTabId 選右側鄰近 tab
- 關閉最後一個 tab（idx = last）→ activeTabId 選左側
- 關閉 workspace 唯一 tab → activeTabId = null，workspace.activeTabId = null
- workspace.activeTabId 正確同步
- locked tab 不可關閉
- tab 不存在時 no-op
- history store 正確記錄

**useShortcuts.test.ts** — 現有測試更新：
- 保留 "does not close tabs from another workspace"
- 保留 "reopens tab into current workspace"
- 修改 "selects next tab within workspace after closing"：移除 `if` guard，改為硬性 assertion
- 新增 "closes last tab in workspace → activeTabId null"

#### 7. 其他 `closeTab` 呼叫端遷移

以下 3 處直接呼叫 `useTabStore.closeTab()`，需改為 `closeTabInWorkspace`：

| 檔案 | 用途 | 改法 |
|------|------|------|
| `TerminatedPane.tsx` | 點「關閉」按鈕 | 改呼叫 `closeTabInWorkspace`，加 `destroyBrowserViewIfNeeded` |
| `WorkspaceSettingsPage.tsx` | 刪除 workspace 時批次關閉 | 改呼叫 `closeTabInWorkspace`（loop 中逐一關閉） |
| `host-lifecycle.ts` | cascade delete 批次關閉 | 改呼叫 `closeTabInWorkspace`（loop 中逐一關閉） |

這些都是用戶可觸發的路徑，改為 composite action 確保 workspace activeTabId 同步、history 記錄一致。

#### 8. `useTabStore.test.ts` 既有測試更新

`closeTab` 簡化後不再自動選鄰近 tab，以下測試需更新：
- "closeTab activates adjacent tab when removing active" → 改為斷言 `activeTabId === null`
- "closeTab sets null when removing last tab" → 保留（行為不變）

### 不改的部分

- `getVisibleTabIds` — 不變
- `insertTab` — 不變
- `reopen-closed-tab` handler — 不變（`insertTab` 已正確處理）
- pre-close guard 位置 — 留 hook 層

### 移除的程式碼

- `useShortcuts.ts` L62-75：整段 post-close 邏輯刪除
- `useTabStore.closeTab` 中的自動選 tab 邏輯（L133-135）：刪除
