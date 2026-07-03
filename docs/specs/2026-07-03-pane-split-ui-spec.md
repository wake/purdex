# Spec — Pane Split UI（視窗分割入口層）

**Date**: 2026-07-03
**Branch**: `worktree-pane-split-ui`
**Author**: Claude (Opus 4.8) + Wake
**狀態**: Draft（待 codex 審）

---

## 1. 背景與現況

Purdex 的 tab 支援任意巢狀 pane 分割，**引擎已全齊，只缺 UI 入口層**。使用者目前只能用 TitleBar 的 4 顆固定版型鈕（single / split-h / split-v / grid-4）——這些是 destructive（壓平成該版型、不能自由巢狀）。本功能補上互動式分割入口，並支援把已開的 tab 拉進分割格做雙邊檢視。

### 引擎盤點（已存在，本次沿用，不改）

| 能力 | 位置 |
|------|------|
| 資料模型 `SplitLayout` / `PaneLayout` / `LayoutPattern` | `spa/src/types/tab.ts:14/15/19` |
| `splitPane(tabId, paneId, dir, content)` → `splitAtPane`（切 leaf 成 2-child split，sizes [50,50]） | `useTabStore.ts:281` → `pane-tree.ts:98` |
| `closePane` → `removePane`（塌陷單子 split、sizes 正規化） | `useTabStore.ts:290` → `pane-tree.ts:109` |
| `resizePanes` / `detachPane` / `setPaneContent` / `setTabLayout` | `useTabStore.ts:314/346/259/339` |
| `newTabPane()`（產 `new-tab` content leaf，**目前 private**） | `pane-tree.ts:141` |
| 通用遞迴 split 渲染 + `PaneSplitter` 拖動 | `PaneLayoutRenderer.tsx` / `PaneSplitter.tsx` |
| 空白 pane 內容選單頁 `NewPanePage`（現只列 dashboard/history/hosts） | `NewPanePage.tsx` |
| Tab 右鍵選單模式（本次 mirror） | `TabContextMenu.tsx`（fixed 定位 `{x,y}` + `onClose` + `MenuItem[]`） |
| 全域底部 `StatusBar`（僅 active tab 的 primary pane；editor 時回 null；tmux-session 顯示 terminal/stream viewMode 膠囊 :213-239） | `StatusBar.tsx` |

### 現況卡點（只在入口層）

1. `PaneHeader`（Close/Detach/Swap）**只在 split 內的 pane 顯示**（`showHeader` prop）；**單一 pane 完全無 header** → 無法從單 pane 直接分割。
2. `splitPane` 需呼叫端自備 content，缺「切出空白 new-tab pane」便利 action。
3. TitleBar 的 `grid-4` 是**硬編碼連動分隔線特例**（`PaneLayoutRenderer.tsx` grid 路徑 + `pane-layout-grid.ts` `isGrid4`），與自由巢狀打架。
4. **無 focus/active-pane 概念**（grep 確認為空）。

---

## 2. 目標與非目標

### 目標

1. 提供互動式分割入口，支援**任意巢狀**（沿用引擎）。
2. **兩個觸發入口**：pane 右鍵選單（主）+ StatusBar 分割鈕（次，終端機 pane）。
3. 分割新格預設為空白 New Tab 頁；並可**把已開的 tab（跨 workspace）拉進該格**做雙邊檢視。
4. 移除 `grid-4` 硬編碼特例，消除與自由巢狀的衝突。

### 非目標

- **不新增 active/focus-pane 概念**：右鍵知道目標 pane、StatusBar 用 primary pane，皆不需 focus 追蹤。
- **不動分割引擎**（`splitAtPane`/`removePane`/`resizePanes`/`detachPane`）與 `PaneSplitter` 渲染。
- **不改 PaneHeader 顯示條件**（不讓單 pane 長出 header）——入口改走右鍵 + status bar。
- **不做鍵盤快捷**分割（無 active-pane，v1 略）。
- 拉 tab 進來 **v1 只支援來源為單一 pane 的 tab**（來源本身是分割的多-pane tab 先不列/灰掉）。

---

## 3. 設計

### 3.1 觸發入口

**（A）Pane 右鍵選單（主）**
- 新元件 `PaneContextMenu`（mirror `TabContextMenu`：fixed `{x,y}` 定位 + `useClickOutside` + Esc 關閉 + `MenuItem[]`）。
- 選項：`Split Horizontal`、`Split Vertical`、`Close pane`、`Detach to tab`。
- 觸發：pane 外層 wrapper（`PaneLayoutRenderer` leaf 渲染處）加 `onContextMenu`，`preventDefault()` 攔截瀏覽器/元件自帶選單，記錄 `{x,y}` + 目標 `paneId` 開選單。
- **⚠️ 右鍵攔截與 Monaco/xterm 自帶選單的取捨**：見 §7 open question。v1 預設：**editor(Monaco) pane 不攔截右鍵**（保留 Monaco 原生選單），editor 的分割改由「拉 tab 進來」或 TitleBar 達成；**其餘 pane 類型（terminal / browser / dashboard / history / preview 等）攔截右鍵**顯示 `PaneContextMenu`。
- `Close pane` / `Detach to tab` 直接複用引擎（`closePane` / `detachPane` + workspace insert，與現有 `PaneHeader` 行為一致）。

**（B）StatusBar 分割鈕（次，終端機 pane）**
- 在 `StatusBar.tsx` 的 `terminal/stream` 膠囊旁（`ml-auto` 群組）加 split-H / split-V 小鈕（Phosphor `Columns` / `Rows`，附 title）。
- 作用於 active tab 的 **primary pane**（`getPrimaryPane`）。
- 因 StatusBar 對 editor pane 回 null，此入口自然只出現在 tmux-session pane（符合終端機為分割主場景）。

### 3.2 分割新格的內容

- 預設 = 空白 **New Tab 頁**：新增便利 action **`splitPaneBlank(tabId, paneId, dir)`**（新 action，**不改** 既有 `splitPane` 簽章與呼叫端，非破壞性）。內部把 `newTabPane()` 產出的 leaf content 餵給既有 `splitAtPane`。`newTabPane()` 由 private **改為 export**（`pane-tree.ts`）供 store 使用。
- `NewPanePage` 加新區塊 **「Bring in an open tab」**：
  - 列出**所有 workspace** 的已開 tab（`useTabStore.tabs` 全域 map），**每項標明所屬 workspace**（`useWorkspaceStore.findWorkspaceByTab`）。
  - **v1 只列單一 pane 的 tab**（`countLeaves(tab.layout) === 1`）；排除自己所在的 tab。
  - 選一個 → `setPaneContent(當前tab, 當前pane, 來源tab.primaryContent)` → `closeTab(來源tab)` **+ `removeTabFromWorkspace(來源ws, 來源tabId)`**（關鍵：`closeTab` 只清全域，不清 workspace 成員，漏掉會留孤兒 ID）。

### 3.3 移除 grid-4

- `tab.ts`：`LayoutPattern` 移除 `'grid-4'`。
- `pane-tree.ts`：`applyLayoutPattern` 移除 `grid-4` case。
- `PaneLayoutRenderer.tsx`：移除 `isGrid4` 分支與整段硬編碼連動分隔線 grid 渲染路徑（回歸為通用遞迴 split）。
- `pane-layout-grid.ts`：移除（僅 export `isGrid4`，無其他用途 → 刪檔）。
- `TitleBar.tsx`：`patterns` 移除 `grid-4` 項（留 `single`/`split-h`/`split-v`）。
- 既有已存成 grid-4 形狀的 layout **不會壞**——它就是「v-split of two h-splits」的普通巢狀，通用遞迴 renderer 照畫，只是分隔線不再連動。

---

## 4. 元件 / 資料流

| 元件 | 變更 |
|------|------|
| `PaneContextMenu.tsx`（新） | mirror TabContextMenu；props `{ position, paneId, tabId, paneKind, onClose, onAction }` |
| `PaneLayoutRenderer.tsx` | leaf wrapper 加 `onContextMenu`（依 paneKind 決定是否攔截）；管理 context-menu open state + 座標；移除 grid-4 路徑 |
| `useTabStore.ts` | 加 `splitPaneBlank`（新 action，不改既有 `splitPane`）；`applyLayoutPattern` 去 grid-4 |
| `pane-tree.ts` | `newTabPane` export；`applyLayoutPattern` 去 grid-4 |
| `NewPanePage.tsx` | 加「Bring in an open tab」區塊（跨 workspace 列舉 + pull-in action） |
| `StatusBar.tsx` | terminal pane split-H/V 鈕 |
| `TitleBar.tsx` | patterns 去 grid-4 |
| `tab.ts` | `LayoutPattern` 去 grid-4 |
| `pane-layout-grid.ts` | 刪檔 |

---

## 5. Phase 切分（依 review 大小）

### Phase 1 — `splitPaneBlank` + Pane 右鍵選單（核心入口）
- `newTabPane` export；`splitPaneBlank` action（TDD：切出的新格 content.kind === 'new-tab'、sizes [50,50]）。
- `PaneContextMenu` 元件（TDD：渲染選項、點擊觸發 onAction、Esc/click-outside 關閉、視窗邊界翻轉沿用 TabContextMenu 模式）。
- `PaneLayoutRenderer` leaf wrapper `onContextMenu` 攔截（依 paneKind：editor 不攔、其餘攔）+ 開選單 + 接 splitPaneBlank/closePane/detachPane（TDD：非 editor pane 右鍵開選單、editor pane 不開）。
- **Review 大小**：中。

### Phase 2 — StatusBar 分割鈕（終端機 pane）
- `StatusBar.tsx` 加 split-H/V 鈕，作用於 primary pane，呼叫 `splitPaneBlank`（TDD：tmux-session 顯示、點擊呼叫、editor/無 tab 不顯示）。
- **Review 大小**：小。

### Phase 3 — 「Bring in an open tab」（跨 workspace 拉 tab）
- `NewPanePage` 加區塊：跨 workspace 列單一-pane tab（標 workspace、排除自身）+ pull-in（`setPaneContent` + `closeTab` + `removeTabFromWorkspace`）。
- TDD：列舉正確（跨 ws、只單-pane、排除自身）；pull-in 後目標格顯示來源內容、來源 tab 從全域 **與來源 workspace 皆移除**（孤兒防護）。
- **Review 大小**：中。

### Phase 4 — 移除 grid-4
- 依 §3.3 全部移除點；TDD/回歸：`applyLayoutPattern` 只剩 3 pattern；既有 grid-4 形狀 layout 仍能被通用 renderer 正確渲染（快照/結構測試）；TitleBar 不再有 grid 鈕。
- **Review 大小**：小-中。

**相依**：Phase 1 為地基（splitPaneBlank）。Phase 2 依賴 Phase 1。Phase 3 獨立（可與 1/2 並行）。Phase 4 獨立清理。

---

## 6. 驗收準則

1. 非 editor pane 右鍵 → `PaneContextMenu`（Split H/V/Close/Detach）；點 Split → 該 pane 一分為二、新格為空白 New Tab 頁。
2. editor(Monaco) pane 右鍵 → 保留 Monaco 原生選單（v1 不攔截）。
3. 終端機 pane 的 StatusBar 有 split-H/V 鈕；點擊分割 primary pane。
4. New Tab 頁可「Bring in an open tab」：列跨 workspace 單-pane tab（標 ws、排除自身）；選一個 → 內容進本格、來源 tab 從全域 + 來源 workspace 皆消失。
5. 分割到剩一個時自動塌回單 pane（引擎 `removePane`）。
6. TitleBar 只剩 single/split-h/split-v；grid-4 完全移除；既有 grid-4 形狀 layout 不回歸壞掉。
7. `cd spa && npx vitest run` 全綠、`pnpm run lint` 綠、`pnpm run build` 綠。
8. 不新增 active-pane 概念；不動分割引擎與 PaneSplitter。

---

## 7. 風險與未決（待 codex 檢視）

| 項目 | 說明 / 傾向 |
|------|-------------|
| **右鍵攔截 vs Monaco/xterm 原生選單**（open question） | Monaco 右鍵選單（複製/命令面板/跳定義）有價值。v1 傾向 **editor 不攔截右鍵**（保留 Monaco），editor 靠拉-tab / TitleBar 分割；terminal(xterm) 的原生右鍵較少用，攔截可接受。請 codex 評此分流是否合理，或建議統一策略（如全攔但加修飾鍵）。 |
| 右鍵在巢狀 pane 的事件冒泡 | `onContextMenu` 在最內層 leaf wrapper 處理 + `stopPropagation`，避免父層重複開選單。 |
| 拉 tab：來源為分割 tab | v1 排除（只列單-pane）。之後可擴充為拉整棵子樹（需新 primitive，非本次）。 |
| 拉 tab：孤兒 workspace 成員 | 必須 `closeTab` + `removeTabFromWorkspace` 成對；測試守住。 |
| 拉 tab：來源 tab 是 active/locked | locked tab `closeTab` 會被拒（現有 guard）→ pull-in 需先擋 locked tab（不列或提示）。 |
| grid-4 移除的既有資料 | 既有 grid-4 形狀是合法巢狀，通用 renderer 相容；僅分隔線不再連動（可接受）。 |
| StatusBar 分割鈕只作用 primary pane | 多 pane 時 status bar 分割的是 primary；精準分割靠右鍵。已於設計說明。 |
