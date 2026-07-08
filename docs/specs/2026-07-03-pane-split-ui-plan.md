# Plan — Pane Split UI

**Spec**: `2026-07-03-pane-split-ui-spec.md`
**Branch 策略**（**更新**：原規劃 3 PR，PR-C 因 codex Round-2 揭露的邏輯相依併入 PR-A）：
- **PR-A** = Phase 1 + 2 + **4**（右鍵選單 + splitPaneBlank + StatusBar 入口 + **移除 grid-4**）。
  - grid-4 移除本規劃為獨立 PR-C，但 Round-2 對抗 review 發現：PR-A 開放任意 leaf split 後，在 grid-4 形狀內 split 仍命中 `isGrid4` 硬編碼連動 resize（bug）。此為**邏輯相依**（非僅 merge conflict），故必須與 PR-A 同時移除 → 併入 PR-A（PR #901）。
- **PR-B** = Phase 3（跨 workspace 拉 tab）。

紀律：TDD（先失敗測試再實作）、每 task 獨立 commit、subagent 開發、主 session 整合/review。三 PR 各自從 origin/main 開分支（B、C 不依賴 A 的程式碼，可平行；但為 review 清晰按序 A→B→C 出）。

---

## PR-A — Pane 右鍵選單 + StatusBar 入口

### Task A1 — `splitPaneBlank` action（TDD）
- **檔案**：`spa/src/stores/useTabStore.ts`（+ 型別）、`useTabStore.test.ts`。
- **測試（先）**：
  - `splitPaneBlank(tabId, paneId, 'h')` → 該 pane 變成 2-child `split`（direction 'h'）、`sizes [50,50]`、新 child 的 leaf `content.kind === 'new-tab'`、原 pane 內容保留在另一 child。
  - `'v'` 同理。
  - 不影響既有 `splitPane`（簽章/行為不變）。
- **實作**：加 `splitPaneBlank: (tabId, paneId, dir) => ...` → 呼叫既有 `splitAtPane(layout, paneId, dir, { kind: 'new-tab' })` 更新 tab layout。**不** export `newTabPane`。
- **commit**：`feat(tabs): add splitPaneBlank action (blank new-tab pane)`

### Task A2 — `PaneContextMenu` 元件（TDD）
- **檔案**：`spa/src/components/PaneContextMenu.tsx`（新）、`PaneContextMenu.test.tsx`（新）。
- **測試（先）**：
  - 渲染 `Split Horizontal` / `Split Vertical`（恆有）。
  - `canDetach=false` → 無 `Close pane` / `Detach to tab`；`canDetach=true` → 有。
  - 點各項 → `onAction(action)` 帶正確 action、並 `onClose`。
  - Esc / click-outside → `onClose`（沿用 `useClickOutside` + keydown，mirror TabContextMenu）。
  - 座標 `{x,y}` 定位（fixed）；視窗右/下邊界翻轉（mirror TabContextMenu 的 useLayoutEffect 邏輯）。
- **實作**：mirror `TabContextMenu.tsx` 結構；props `{ position, canDetach, onClose, onAction }`；action 型別 `'split-h' | 'split-v' | 'close' | 'detach'`。
- **commit**：`feat(panes): add PaneContextMenu component`

### Task A3 — `PaneLayoutRenderer` 右鍵接線（TDD）
- **檔案**：`spa/src/components/PaneLayoutRenderer.tsx`、對應 test。
- **測試（先，回應 codex 測法建議）**：
  - **每條測試先 seed store**：`useTabStore.setState` 放入含目標 layout 的 `tabs[t1]`，render 時傳 `tabId="t1"`——`canDetach`/`detachPane`/`closePane` 都讀 `useTabStore.getState().tabs[tabId]`（`PaneLayoutRenderer.tsx:68/83`），不 seed 會測成假陽性。
  - 非 editor leaf 右鍵 → 選單開（斷言 `PaneContextMenu` 出現 / 座標 = 事件座標）；攔截驗證用 **`fireEvent.contextMenu` 後 `event.defaultPrevented === true`**（不 spy method）。
  - editor leaf 右鍵 → 不開選單、`defaultPrevented === false`（放行 Monaco）。
  - 攔截型 leaf **`Shift`+右鍵** → 不開選單、`defaultPrevented === false`（escape hatch）。
  - **`stopPropagation`**：在 leaf wrapper 外層再包一個帶 `onContextMenu` spy 的父節點，非 editor 右鍵後父 spy **未被呼叫**（冒泡被止）。
  - `canDetach`：單-pane layout → 選單無 Close/Detach；split layout（countLeaves>1）→ 有。
  - action 接線：`split-h/split-v` → `splitPaneBlank(t1, paneId, dir)`；`close` → `closePane(t1, paneId)`；`detach` → **與既有 PaneHeader onDetach 完全一致**：`detachPane(t1, paneId, t1)` 回 newTabId 後 `insertTab(newTabId, ws.id, t1)`（插在原 tab 之後）+ `setActiveTab(newTabId)`（斷言三者，非只斷言「有 detach」）。
- **實作**：leaf 最外層容器加 `onContextMenu`（`content.kind === 'editor'` 或 `e.shiftKey` → return 放行；否則 `preventDefault()+stopPropagation()` 開選單）；本地 state `{paneId,x,y}|null`；渲染 `PaneContextMenu`；detach 複用既有 onDetach 範式（`PaneLayoutRenderer.tsx:84-90`）。
- **commit**：`feat(panes): right-click context menu to split/close/detach panes`

### Task A4 — StatusBar 分割鈕（TDD）
- **檔案**：`spa/src/components/StatusBar.tsx`、`StatusBar.test.tsx`。
- **測試（先）**：
  - tmux-session active tab（單 leaf）→ 顯示 split-H / split-V 鈕（by title/role）。
  - **split-layout tab（回應 codex）**：primary pane 為 tmux-session 的巢狀 layout → 鈕仍顯示，點擊呼叫 `splitPaneBlank(activeTab.id, getPrimaryPane(layout).id, dir)`（守住 primary 解析非單純 leaf 的情況）。
  - 點擊 → `splitPaneBlank(activeTab.id, primaryPaneId, 'h'|'v')`。
  - 無 active tab / editor pane（StatusBar 本就 null）→ 不顯示。
- **實作**：在 `terminal/stream` 膠囊所在的 `ml-auto` 群組加兩顆小鈕（Phosphor `Columns`/`Rows` + title），呼叫 `splitPaneBlank`（paneId = `getPrimaryPane(activeTab.layout).id`）。
- **commit**：`feat(statusbar): split-H/V buttons for terminal panes`

**PR-A 驗收**：AC1/2/3、AC5（塌陷沿用引擎）、AC7/8。vitest/lint/build 綠。

---

## PR-B — Bring in an open tab（跨 workspace 拉 tab）

### Task B1 — `moveTabContentIntoPane` helper（TDD）
- **檔案**：`spa/src/lib/pane-move.ts`（新）、`pane-move.test.ts`（新）。
- **allowlist 常數**：`MOVABLE_KINDS = ['editor','tmux-session','image-preview','pdf-preview']`（同檔 export 供 UI 用）。
- **測試（先）**：
  - guard：來源不存在 / locked / `countLeaves>1` / kind 不在 allowlist / source===target → no-op（回傳 false 或不變）。
  - happy：`setPaneContent(target, targetPane, sourcePrimaryContent)` 被呼叫且內容正確；來源 tab 經 `closeTabInWorkspace(source,{skipHistory:true})` 移除 → 全域 tabs 無該 id、來源 workspace.tabs 無該 id。
  - **active fallback（回應 codex，拆 2 條、斷定 fallback id 非只 non-null）**：
    (a) 來源是其 ws activeTabId **但非全域 activeTabId** → 搬後全域 active **保持原值**、ws.activeTabId 切到預期 fallback id（`visitHistory → adjacent`）。
    (b) 來源**同時是全域 activeTabId** → 全域 active 切到**預期 fallback id**（明確斷言）。
  - **無 dirty-confirm（回應 codex，斷 2 點）**：來源 dirty editor → (1) `window.confirm` spy **未被呼叫**（證明沒走 `tab-lifecycle::closeTab`），(2) `useEditorStore.buffers[bufferKey(...)]` **仍存在**。
- **實作**：依 spec §3.2.1 三步；讀 `getPrimaryPane` 取來源 content；用 `useWorkspaceStore.closeTabInWorkspace`。
- **commit**：`feat(panes): moveTabContentIntoPane helper (pull tab into pane)`

### Task B2 — `NewTabPage`「Bring in an open tab」區塊（TDD）
> **⚠️ 回應 codex Blocker**：`new-tab` pane 真正渲染的是 `NewTabPaneWrapper → <NewTabPage>`（`register-modules/index.tsx:66`），**不是** `NewPanePage.tsx`（後者是**未接線孤兒**，勿改）。B2 必須改 `NewTabPage`，否則測過也碰不到真實路徑、無法滿足 AC4。
- **檔案**：`spa/src/components/NewTabPage.tsx`、`spa/src/lib/register-modules/index.tsx`（`NewTabPaneWrapper` 傳 context）、`NewTabPage.test.tsx`（新/延伸）。
- **拿到 tabId/paneId（已解，非探勘）**：`NewTabPaneWrapper` 已用 `pane.id` + `findPane` 反查 `tabId`（`index.tsx:66-72`）。在 wrapper 內把 `currentTabId`（已算）+ `currentPaneId = pane.id` 傳給 `NewTabPage` 的新區塊（或新 card），**不改** `module-registry`/`PaneRendererProps` 型別。
- **測試（先）**：
  - 列舉：跨所有 workspace 的 tab，過濾 = 單-pane（`countLeaves===1`）且 primary kind ∈ allowlist 且 `!locked` 且非「本 pane 所在 tab」；每項顯示 tab 名 + 所屬 workspace 名。
  - browser tab、多-pane tab、locked tab、自身 tab → 不出現。
  - 點一項 → `moveTabContentIntoPane(sourceId, currentTabId, currentPaneId)`。
  - 無可搬 tab → 該區塊不顯示（或空狀態）。
- **實作**：`NewTabPage` 加「Bring in an open tab」區塊（接受 optional `currentTabId`/`currentPaneId`；缺省時不渲染該區塊，兼容其他呼叫路徑）；查 `useTabStore.tabs` + `useWorkspaceStore.findWorkspaceByTab` + `MOVABLE_KINDS`。
- **commit**：`feat(panes): pull an open tab into a split pane (cross-workspace)`

**PR-B 驗收**：AC4。vitest/lint/build 綠。

---

## PR-C — 移除 grid-4

### Task C1 — 移除 grid-4（TDD/回歸）
- **檔案**：`types/tab.ts`、`lib/pane-tree.ts`、`components/PaneLayoutRenderer.tsx`、`components/pane-layout-grid.ts`（刪）、`components/TitleBar.tsx`、`PaneLayoutRenderer.test.tsx`、`pane-tree.test.ts`。
- **測試（先/更新）**：
  - `applyLayoutPattern` 型別只剩 `single|split-h|split-v`（grid-4 case 移除；既有 grid-4 測試案例刪除/改寫）。
  - **相容回歸（結構斷言，不用 snapshot；回應 codex C1）**：手造 grid-4 形狀 layout（v-split of two h-splits）餵給 `PaneLayoutRenderer` → 4 個 leaf 都被通用遞迴路徑渲染出（by testid/內容）、不 crash。
  - `TitleBar` 不再渲染 grid 鈕（只 3 顆）；`LayoutPattern` 型別不再接受 `'grid-4'`。
- **實作**：依 spec §3.3 全部移除點；刪 `pane-layout-grid.ts`；`TitleBar` 去 `grid-4` pattern + `GridFour` import；`PaneLayoutRenderer` 去 `isGrid4` 分支與硬編碼 grid 渲染 + import。
- **commit**：`refactor(panes): remove hardcoded grid-4 special case`

**PR-C 驗收**：AC6。vitest/lint/build 綠。

---

## 整體驗證（每個 PR 前）
- `cd spa && npx vitest run` 全綠、`pnpm run lint` 綠、`pnpm run build` 綠。
- 手動（SPA HMR）：右鍵分割 / StatusBar 分割 / 拉 tab 雙邊檢視 / 塌回單 pane / TitleBar 剩 3 鈕。

## 分支順序（回應 codex：A3 與 C1 都改 `PaneLayoutRenderer.tsx`）
- PR-B 與 PR-A **程式碼獨立**（`new-tab` pane 本就存在，B 不吃 A 的 `splitPaneBlank`），可先後獨立出。
- **PR-C 在 PR-A 之後做**（非平行）：A3 與 C1 都改 `PaneLayoutRenderer.tsx` 與其 test，平行會 merge conflict。序列 A→（B）→C，C 從已含 A 的 main rebase。

## 探勘待辦（實作時確認，非阻塞）
1. `PaneLayoutRenderer` 非 editor leaf 的最外層容器確切位置（Task A3 掛 `onContextMenu` 的節點）。
（B2 的 tabId/paneId 取得已解決，見 Task B2。）

## 非目標（重申）
不新增 active-pane / 不動分割引擎與 PaneSplitter / 不做鍵盤快捷 / 拉 tab 不支援 browser 與分割-tab / 不改 PaneHeader 顯示條件。
