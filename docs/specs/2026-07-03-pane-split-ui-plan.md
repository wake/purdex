# Plan — Pane Split UI

**Spec**: `2026-07-03-pane-split-ui-spec.md`
**Branch 策略**: 依 spec §5 PR 分組拆 **三個獨立 PR**（各自 codex 兩輪 review）：
- **PR-A** = Phase 1 + 2（右鍵選單 + splitPaneBlank + StatusBar 入口）
- **PR-B** = Phase 3（跨 workspace 拉 tab）
- **PR-C** = Phase 4（移除 grid-4）

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
- **測試（先）**：
  - 非 editor leaf（如 dashboard/history/tmux-session mock）右鍵 → `preventDefault` 被呼叫、選單開（menu state set，座標 = 事件座標）。
  - editor leaf 右鍵 → **不** preventDefault、**不**開選單（放行 Monaco）。
  - 攔截型 leaf 的 **`Shift`+右鍵** → 不 preventDefault、不開選單（escape hatch）。
  - 選單 action 接線：`split-h/split-v` → `splitPaneBlank`；`close` → `closePane`；`detach` → `detachPane`(+ workspace insert，與既有 PaneHeader onDetach 一致)。
  - `canDetach` 傳入 = `countLeaves(tab.layout) > 1`。
- **實作**：leaf 渲染的最外層容器加 `onContextMenu`（判 `content.kind === 'editor'` 放行、`e.shiftKey` 放行、否則 `preventDefault()+stopPropagation()` 開選單）；本地 state 存 `{paneId, x, y} | null`；渲染 `PaneContextMenu`。detach 邏輯複用既有 onDetach（PaneLayoutRenderer 內已有範式 :84-90）。
- **commit**：`feat(panes): right-click context menu to split/close/detach panes`

### Task A4 — StatusBar 分割鈕（TDD）
- **檔案**：`spa/src/components/StatusBar.tsx`、`StatusBar.test.tsx`。
- **測試（先）**：
  - tmux-session active tab → 顯示 split-H / split-V 鈕（by title/role）。
  - 點擊 → `useTabStore.splitPaneBlank(activeTab.id, primaryPaneId, 'h'|'v')`（primary = `getPrimaryPane`）。
  - 無 active tab / editor pane（StatusBar 本就 null）→ 不顯示。
- **實作**：在 `terminal/stream` 膠囊所在的 `ml-auto` 群組加兩顆小鈕（Phosphor `Columns`/`Rows` + title），呼叫 `splitPaneBlank`。
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
  - active fallback：來源是其 ws 的 activeTabId → 搬後該 ws.activeTabId 與全域 activeTabId 皆非 null（選到 fallback）。
  - 無 dirty-confirm：來源是 dirty editor → 不呼叫 `window.confirm`、buffer 仍在 `useEditorStore`。
- **實作**：依 spec §3.2.1 三步；讀 `getPrimaryPane` 取來源 content；用 `useWorkspaceStore.closeTabInWorkspace`。
- **commit**：`feat(panes): moveTabContentIntoPane helper (pull tab into pane)`

### Task B2 — `NewPanePage`「Bring in an open tab」區塊（TDD）
- **檔案**：`spa/src/components/NewPanePage.tsx`、`NewPanePage.test.tsx`（新/延伸）。
- **測試（先）**：
  - 列舉：跨所有 workspace 的 tab，過濾 = 單-pane（`countLeaves===1`）且 primary kind ∈ allowlist 且 `!locked` 且非「本 pane 所在 tab」；每項顯示 tab 名 + 所屬 workspace 名。
  - browser tab、多-pane tab、locked tab、自身 tab → 不出現。
  - 點一項 → 呼叫 `moveTabContentIntoPane(sourceId, currentTabId, currentPaneId)`。
  - 無可搬 tab → 該區塊不顯示（或顯示空狀態）。
- **實作**：`NewPanePage` 需知道自己的 `tabId`/`paneId`（目前 props 只有 `onSelect`）→ 由呼叫端（pane 'new-tab' content 的 renderer）多傳 `tabId`/`paneId`；查 `useTabStore.tabs` + `useWorkspaceStore.findWorkspaceByTab`。
- **commit**：`feat(panes): pull an open tab into a split pane (cross-workspace)`

**PR-B 驗收**：AC4。vitest/lint/build 綠。**注意**：需確認 `new-tab` pane renderer 如何傳 `tabId/paneId` 給 `NewPanePage`（探勘 registry 綁定；若現無、Task B2 含此接線）。

---

## PR-C — 移除 grid-4

### Task C1 — 移除 grid-4（TDD/回歸）
- **檔案**：`types/tab.ts`、`lib/pane-tree.ts`、`components/PaneLayoutRenderer.tsx`、`components/pane-layout-grid.ts`（刪）、`components/TitleBar.tsx`、`PaneLayoutRenderer.test.tsx`、`pane-tree.test.ts`。
- **測試（先/更新）**：
  - `applyLayoutPattern` 型別只剩 `single|split-h|split-v`（grid-4 case 移除；既有 grid-4 測試案例刪除/改寫）。
  - **相容回歸**：手造一個 grid-4 形狀 layout（v-split of two h-splits）餵給 `PaneLayoutRenderer` → 正確渲染 4 個 leaf（通用遞迴路徑），不 crash、不走特例。
  - `TitleBar` 不再渲染 grid 鈕（只 3 顆）。
- **實作**：依 spec §3.3 全部移除點；刪 `pane-layout-grid.ts`；`TitleBar` 去 `grid-4` pattern + `GridFour` import；`PaneLayoutRenderer` 去 `isGrid4` 分支與硬編碼 grid 渲染 + import。
- **commit**：`refactor(panes): remove hardcoded grid-4 special case`

**PR-C 驗收**：AC6。vitest/lint/build 綠。

---

## 整體驗證（每個 PR 前）
- `cd spa && npx vitest run` 全綠、`pnpm run lint` 綠、`pnpm run build` 綠。
- 手動（SPA HMR）：右鍵分割 / StatusBar 分割 / 拉 tab 雙邊檢視 / 塌回單 pane / TitleBar 剩 3 鈕。

## 探勘待辦（實作時確認，非阻塞）
1. `new-tab` pane content 的 renderer 綁定（`module-registry`）如何把 `tabId/paneId` 傳給 `NewPanePage`（Task B2 需要）。
2. `PaneLayoutRenderer` 非 editor leaf 的最外層容器確切位置（Task A3 掛 `onContextMenu`）。

## 非目標（重申）
不新增 active-pane / 不動分割引擎與 PaneSplitter / 不做鍵盤快捷 / 拉 tab 不支援 browser 與分割-tab / 不改 PaneHeader 顯示條件。
