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
- 新元件 `PaneContextMenu`（mirror `TabContextMenu`：fixed `{x,y}` 定位 + `useClickOutside` + Esc 關閉 + `MenuItem[]` + 視窗邊界翻轉）。
- 選項（**條件顯示，回應 codex S1**）：
  - `Split Horizontal` / `Split Vertical`：**恆顯示**。
  - `Close pane` / `Detach to tab`：**僅當 `countLeaves(tab.layout) > 1`**（pane 在 split 內）才顯示——單一 pane 時 `detachPane` 回 `null`（`useTabStore.ts:346`）、`closePane` 亦無意義，故隱藏，避免無效項。
- 觸發與攔截策略（**回應 codex S3，取代純 kind 分流**）：pane 外層 wrapper（`PaneLayoutRenderer` leaf 渲染處）加 `onContextMenu`：
  - **editor(Monaco) pane**：**永不攔截**（保留 Monaco 原生選單）；editor 的分割改由「拉 tab 進來」或 TitleBar 達成。（使用者已確認 v1 如此。）
  - **其餘 pane（terminal/browser/preview/dashboard/history…）**：預設 `preventDefault()` + `stopPropagation()`（最內層 leaf 處理，防父層重複開）顯示 `PaneContextMenu`；**`Shift`+右鍵 = escape hatch，放行原生選單**（保留 xterm/browser 原生右鍵價值）。
- `Close pane` / `Detach to tab` 直接複用引擎（`closePane` / `detachPane` + workspace insert，與現有 `PaneHeader` 行為一致）。

**（B）StatusBar 分割鈕（次，終端機 pane）**
- 在 `StatusBar.tsx` 的 `terminal/stream` 膠囊旁（`ml-auto` 群組）加 split-H / split-V 小鈕（Phosphor `Columns` / `Rows`，附 title）。
- 作用於 active tab 的 **primary pane**（`getPrimaryPane`）。
- 因 StatusBar 對 editor pane 回 null，此入口自然只出現在 tmux-session pane（符合終端機為分割主場景）。

### 3.2 分割新格的內容

- 預設 = 空白 **New Tab 頁**：新增便利 action **`splitPaneBlank(tabId, paneId, dir)`**（新 action，**不改** 既有 `splitPane` 簽章與呼叫端，非破壞性）。內部直接把 content literal **`{ kind: 'new-tab' }`** 餵給既有 `splitAtPane`（後者自行產生新 leaf 的 pane id，`pane-tree.ts:98`）。**不 export** `newTabPane`（回應 codex S2：API 面不必放大；store 只需 content literal）。
- `NewPanePage` 加新區塊 **「Bring in an open tab」**（跨 workspace 拉 tab）。

#### 3.2.1 pull-in 流程 — `moveTabContentIntoPane` helper（回應 codex Blocker 1）

**不可**手拼 `setPaneContent → closeTab → removeTabFromWorkspace`（繞過 tab lifecycle → orphan / 漏 active fallback / 漏 dirty / 漏 BrowserView）。改定義單一 helper：

```
moveTabContentIntoPane(sourceTabId, targetTabId, targetPaneId)
```

1. **Guard**：來源存在、`!locked`、`countLeaves(source.layout) === 1`、`sourceTabId !== targetTabId`、來源 primary content.kind ∈ **allowlist**（見 3.2.2）。任一不符 → no-op（UI 端本就只列符合者）。
2. `setPaneContent(targetTabId, targetPaneId, sourcePrimaryContent)` — 把來源內容注入目標格。
3. 以 **`closeTabInWorkspace(sourceTabId, { skipHistory: true })`**（`workspace/store.ts:186`）移除來源 tab——此路徑**已內建** workspace 成員移除、`nextTab` 預算、**active fallback（全域 + workspace 兩者）**、全域 `closeTab`，一次到位（回應 codex active-fallback）。
   - **為何不走 `tab-lifecycle::closeTab`**：那是「銷毀」語意（dirty-confirm + `destroyBrowserViewIfNeeded`）。pull-in 是**搬移**，內容未銷毀：editor buffer 依 source/filePath 存於全域 `useEditorStore`、tmux-session 是 host+code 參照，搬到新格後狀態不滅 → dirty-confirm 會是**誤報**、應跳過；browser 已排除故無 BrowserView。用 `closeTabInWorkspace` 直達正是「搬移」該有的清理集合。
   - `skipHistory: true`：搬移不應在 history 留「已關閉 tab」（否則 reopen 會複製一份已在 pane 的內容）。

#### 3.2.2 v1 可搬移的 content kind allowlist（回應 codex Blocker 2）

**v1 allowlist**：`editor`、`tmux-session`、`image-preview`、`pdf-preview`（正是「開在別的 tab、想並排檢視」的文件/終端）。
**排除**：`browser`（`destroyBrowserViewIfNeeded` 的 BrowserView 生命週期綁 tab，搬移需 re-parent，v1 不做）、其餘 kind。allowlist 保守、可日後擴充。`NewPanePage` 只列「單-pane 且 primary kind ∈ allowlist」的 tab，並排除自身所在 tab；每項標明所屬 workspace。

#### 3.2.3 locked / active 語意

- **來源 locked**：不列（`closeTabInWorkspace` 對 locked 亦 no-op）。
- **目標 tab locked**：v1 **仍允許** split / pull-in（既有 lock 語意 = 僅防「關閉」，見 `closeTab`/`closeTabInWorkspace` 的 `tab.locked` guard；不擋內容變更）。spec 明訂此語意，不新增 guard。
- **來源為其 workspace 的 activeTab**：由 `closeTabInWorkspace` 的 active fallback 處理（不會留 `null` 空選）。

### 3.3 移除 grid-4

- `tab.ts`：`LayoutPattern` 移除 `'grid-4'`。
- `pane-tree.ts`：`applyLayoutPattern` 移除 `grid-4` case。
- `PaneLayoutRenderer.tsx`：移除 `isGrid4` 分支與整段硬編碼連動分隔線 grid 渲染路徑（回歸為通用遞迴 split）；移除 `pane-layout-grid` import。
- `pane-layout-grid.ts`：刪檔（僅 export `isGrid4`，無其他用途）。
- `TitleBar.tsx`：`patterns` 移除 `grid-4` 項（留 `single`/`split-h`/`split-v`）；**移除已無用的 `GridFour` icon import**（回應 codex S4）。
- **測試（回應 codex S4，屬 Phase 4 範圍，不可漏）**：`PaneLayoutRenderer.test.tsx`、`pane-tree.test.ts` 中涉及 grid-4 的案例一併更新/移除；補一條「既有 grid-4 形狀 layout（v-split of two h-splits）仍由通用 renderer 正確渲染」的結構/快照回歸測試。
- 既有已存成 grid-4 形狀的 layout **不會壞**——它就是普通巢狀，通用遞迴 renderer 照畫（persist 的是 layout tree 非 pattern enum），只是分隔線不再連動。

---

## 4. 元件 / 資料流

| 元件 | 變更 |
|------|------|
| `PaneContextMenu.tsx`（新） | mirror TabContextMenu；props `{ position, tabId, paneId, canDetach, onClose, onAction }`（`canDetach` = `countLeaves>1`，控制 Close/Detach 顯示） |
| `PaneLayoutRenderer.tsx` | leaf wrapper 加 `onContextMenu`（editor 不攔、其餘攔 + Shift escape hatch）；管理 menu open state + 座標；移除 grid-4 路徑與 import |
| `useTabStore.ts` | 加 `splitPaneBlank`（傳 `{kind:'new-tab'}`，不改既有 `splitPane`）；`applyLayoutPattern` 去 grid-4 |
| `pane-tree.ts` | `applyLayoutPattern` 去 grid-4（**不** export `newTabPane`） |
| `lib/pane-move.ts`（新，或置於 tab-lifecycle） | `moveTabContentIntoPane` helper（§3.2.1，經 `closeTabInWorkspace`） |
| `NewPanePage.tsx` | 加「Bring in an open tab」區塊（跨 workspace 列 allowlist 單-pane tab + 呼叫 `moveTabContentIntoPane`） |
| `StatusBar.tsx` | terminal pane split-H/V 鈕（作用 primary pane） |
| `TitleBar.tsx` | patterns 去 grid-4；去 `GridFour` import |
| `tab.ts` | `LayoutPattern` 去 grid-4 |
| `pane-layout-grid.ts` | 刪檔 |

---

## 5. Phase 切分（依 review 大小）

### Phase 1 — `splitPaneBlank` + Pane 右鍵選單（核心入口）
- `splitPaneBlank(tabId, paneId, dir)` action，傳 `{kind:'new-tab'}`（TDD：新格 content.kind === 'new-tab'、sizes [50,50]、不改既有 splitPane）。
- `PaneContextMenu` 元件（TDD：Split H/V 恆顯示、Close/Detach 僅 `canDetach` 時顯示、點擊觸發 onAction、Esc/click-outside 關閉、視窗邊界翻轉沿用 TabContextMenu 模式）。
- `PaneLayoutRenderer` leaf wrapper `onContextMenu`：editor 不攔、其餘攔（+ `Shift` escape hatch 放行原生）+ `stopPropagation` + 開選單 + 接 splitPaneBlank/closePane/detachPane（TDD：非 editor 右鍵開選單、editor 右鍵不開、Shift+右鍵不開、單 pane 選單無 Close/Detach）。
- **Review 大小**：中。

### Phase 2 — StatusBar 分割鈕（終端機 pane）
- `StatusBar.tsx` 加 split-H/V 鈕，作用於 primary pane，呼叫 `splitPaneBlank`（TDD：tmux-session 顯示、點擊呼叫、editor/無 tab 不顯示）。
- **Review 大小**：小。

### Phase 3 — 「Bring in an open tab」（跨 workspace 拉 tab）
- `moveTabContentIntoPane` helper（§3.2.1，經 `closeTabInWorkspace({skipHistory:true})`）+ `NewPanePage` 區塊（跨 workspace 列 **allowlist 單-pane** tab、標 workspace、排除自身、排除 locked）。
- TDD：
  - 列舉：跨 ws 正確、只 allowlist 單-pane、排除自身/locked。
  - pull-in：目標格顯示來源內容；來源 tab 從全域 **與來源 workspace 皆移除**（孤兒防護）。
  - **active fallback（回應 codex S5）**：來源是其 workspace 的 `activeTabId` 時，搬走後該 workspace + 全域 active 都選出 fallback（非 `null`）——由 `closeTabInWorkspace` 保證，測試守住。
  - **不觸發 dirty-confirm**：搬移 dirty editor tab 不彈確認框、buffer 存活（走 `closeTabInWorkspace` 而非 lifecycle）。
  - **browser 排除**：browser pane 的 tab 不在清單。
- **Review 大小**：中。

### Phase 4 — 移除 grid-4
- 依 §3.3 全部移除點（含 `pane-layout-grid.ts` 刪檔、`GridFour` import、`PaneLayoutRenderer.test.tsx`/`pane-tree.test.ts` 更新）。TDD/回歸：`applyLayoutPattern` 只剩 3 pattern；既有 grid-4 形狀 layout 仍由通用 renderer 正確渲染（結構/快照測試）；TitleBar 不再有 grid 鈕。
- **Review 大小**：小-中。

**相依**：Phase 1 為地基（splitPaneBlank）。Phase 2 依賴 Phase 1。Phase 3 獨立（可與 1/2 並行）。Phase 4 獨立清理。

**PR 分組建議（回應 codex S6）**：**PR-A = Phase 1 + 2**（UI 入口）；**PR-B = Phase 3**（跨 store lifecycle，獨立審）；**PR-C = Phase 4**（機械式 cleanup，獨立審）。各自 codex 兩輪 review。

---

## 6. 驗收準則

1. 非 editor pane 右鍵 → `PaneContextMenu`；單 pane 只有 Split H/V，split 內的 pane 另有 Close/Detach。點 Split → 該 pane 一分為二、新格為空白 New Tab 頁。
2. editor(Monaco) pane 右鍵 → 保留 Monaco 原生選單（v1 不攔截）。攔截型 pane 的 `Shift`+右鍵 → 放行原生選單（escape hatch）。
3. 終端機 pane 的 StatusBar 有 split-H/V 鈕；點擊分割 primary pane。
4. New Tab 頁可「Bring in an open tab」：列跨 workspace 的 **allowlist 單-pane** tab（標 ws、排除自身/locked）；選一個 → 內容進本格、來源 tab 從全域 + 來源 workspace 皆消失、active 有 fallback、不彈 dirty 框、buffer 存活。browser tab 不在清單。
5. 分割到剩一個時自動塌回單 pane（引擎 `removePane`）。
6. TitleBar 只剩 single/split-h/split-v；grid-4 完全移除（含 icon import、測試）；既有 grid-4 形狀 layout 不回歸壞掉。
7. `cd spa && npx vitest run` 全綠、`pnpm run lint` 綠、`pnpm run build` 綠。
8. 不新增 active-pane 概念；不動分割引擎與 PaneSplitter。

---

## 7. 風險與已決（codex 審後更新）

| 項目 | 決議 |
|------|------|
| 右鍵攔截 vs 原生選單（原 open question，**已決**） | **editor 恆不攔截**（保留 Monaco，使用者確認）；其餘 pane 攔截顯示 `PaneContextMenu`，但 **`Shift`+右鍵放行原生**（保留 xterm/browser 原生右鍵，回應 codex S3）。 |
| 右鍵在巢狀 pane 的事件冒泡 | 最內層 leaf wrapper 處理 + `stopPropagation`。 |
| 拉 tab 繞過 tab lifecycle（codex B1） | **已決**：`moveTabContentIntoPane` 經 `closeTabInWorkspace({skipHistory:true})`，非手拼 3 action；搬移語意跳過 dirty-confirm/browser-destroy（§3.2.1）。 |
| 拉 tab 可搬 kind（codex B2） | **已決**：v1 allowlist = editor/tmux-session/image-preview/pdf-preview；排除 browser（BrowserView 生命週期）。 |
| 拉 tab：來源為分割 tab | v1 排除（只列單-pane）。日後擴充需拉整棵子樹（新 primitive，非本次）。 |
| 拉 tab：active fallback（codex S5） | 由 `closeTabInWorkspace` 內建處理（全域 + workspace active 都選 fallback）。 |
| 拉 tab：locked | 來源 locked 不列；目標 locked 仍允許 split/pull-in（lock = 僅防關閉，§3.2.3）。 |
| grid-4 移除的既有資料 | 合法巢狀、通用 renderer 相容；僅分隔線不再連動（可接受）。 |
| StatusBar 分割鈕只作用 primary pane | 精準分割靠右鍵；已於設計說明。 |
