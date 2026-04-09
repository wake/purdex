# Workspace Icon 指示器設計

## 概述

在 ActivityBar 的 workspace icon 上新增兩種指示器，讓使用者不切換 workspace 就能掌握各 workspace 的狀態：

1. **未讀 badge** — 統計 workspace 內有幾個 unread tab，顯示紅色數字徽章
2. **狀態 pill** — 彙整 workspace 內 tab 的 agent 狀態，以 Discord 風格左側 pill 呈現

## 功能一：未讀 Badge（完整定義）

### 行為規則

- 計算方式：workspace 的 `tabs[]` 中，`useAgentStore.unread[compositeKey] === true` 的 tab 數量
- 顯示條件：`workspace 不是 active` **且** `unread count > 0`
- Active workspace 隱藏 badge，但不清除 unread 狀態；切走後若仍有未讀，badge 重新出現
- 每個 tab 的 unread 清除時機不變：tab 被 activate 時呼叫 `markRead()`

### 視覺規格

- 位置：workspace button 右上角，`top: -5px; right: -6px`
- 尺寸：`min-width: 15px; height: 15px; border-radius: 8px`
- 顏色：`background: #dc2626`（紅），`color: #fff`
- 字體：`font-size: 9px; font-weight: 700`
- 外框：`box-shadow: 0 0 0 2px` sidebar 背景色，確保深色背景下可辨識
- 數字 ≥ 10 時 badge 自動撐寬（`min-width` + `padding: 0 3px`）

### tabId → compositeKey 橋接

ActivityBar 只有 `workspace.tabs[]`（tab ID 陣列）。要取得 compositeKey 需要：

1. 從 `useTabStore` 取得 `Tab` 物件
2. 用 `getPrimaryPane(tab.layout)` 取得主要 pane
3. 檢查 `content.kind === 'tmux-session'`（非 session 的 tab 如 dashboard/settings/hosts 直接跳過）
4. 取 `content.hostId` + `content.sessionCode` → `compositeKey()`

Split-pane tab 只取 primary pane 的 compositeKey，與 `SortableTab` 的現有做法一致。

### 資料流

```
useTabStore.tabs (Tab[])
  ↓ 過濾 workspace.tabs[] 中的 tabId
  ↓ getPrimaryPane(tab.layout).content
  ↓ guard: kind === 'tmux-session'
  ↓ compositeKey(hostId, sessionCode)
  ↓
useAgentStore.unread[ck] === true → 計數
  ↓
條件渲染 badge（!isActive && count > 0）
```

## 功能二：狀態 Pill（方向定義，UI 實作階段迭代）

### 目標行為

- 彙整 workspace 內所有 tab 的 agent 狀態，取最高優先級：`error > waiting > running > idle`
- 優先級規則：任一 tab 為 `waiting` 或 `error` 即視為需關注；全部 `running` 則顯示 running；全部 `idle` 或無狀態則不顯示
- `idle` 不顯示 pill（與 tab 上 idle 不顯示 dot 的行為一致）

### UI 方向（待迭代）

經過 brainstorming 比較了 7 種方案（A-G），初步偏向 **Discord 風格左側 pill**（方案 F1）：

- Pill 定位在 sidebar container 的 `left: 0`，不佔 button 空間
- Pill 顏色對應狀態：綠（running）、黃（waiting）、紅（error）
- Running 狀態加 breathe 動畫

**待解決**：active workspace 已有 purple ring，pill 如何共存。可能方案：
1. Pill 統一負責 active + 狀態指示，移除 purple ring
2. Active workspace 不顯示 pill（狀態已可在 tab bar 看到）

此部分在實作階段邊做邊調整視覺呈現。

### 資料流

同未讀 badge 的 tabId → compositeKey 橋接流程，最後改為讀 `useAgentStore.statuses[ck]` 並取最高優先級。

## 涉及檔案

| 檔案 | 變更 |
|------|------|
| `spa/src/features/workspace/components/ActivityBar.tsx` | workspace button 內加入 badge + pill 渲染 |
| `spa/src/features/workspace/workspace-indicators.ts` | **新增** — 純函式：tabId→compositeKey 橋接、狀態優先級計算 |
| `spa/src/features/workspace/useWorkspaceIndicators.ts` | **新增** — React hook：封裝 store 訂閱、unread 聚合 |
| `spa/src/stores/useTabStore.ts` | 讀取依賴（hook 內訂閱） |
| `spa/src/stores/useAgentStore.ts` | 讀取依賴（hook 內訂閱） |
| `spa/src/features/workspace/components/ActivityBar.test.tsx` | 補測 badge 顯示/隱藏、數字計算 |

## 實作注意事項

- **效能**：`useWorkspaceIndicators` hook 須用精準 selector 訂閱 store，避免整個 `unread` 物件變動觸發全量重算。用 `useMemo` 將 tabIds 轉為 compositeKeys，再逐 key 訂閱。
- **元件職責**：聚合邏輯全部放在 `useWorkspaceIndicators` hook，ActivityBar 維持純渲染層。
- **Accessibility**：workspace button 的 `aria-label` 需包含未讀數（如 `"Workspace Foo, 3 unread"`），與 commit `4c5661f6` 的 aria-label 方向一致。

## 不做的事

- 不改變現有 tab 級別的 unread/status 行為
- 不改變 `markRead()` 的觸發時機
- Home 按鈕維持現有的 `standaloneTabCount` badge，不套用新的 unread 計算
- 不處理斷線 host 的特殊視覺狀態（`clearHost()` 已清除狀態，行為合理）
