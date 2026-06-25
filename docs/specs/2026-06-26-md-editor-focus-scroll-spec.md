# Spec — Editor 切換分頁 focus + markdown(wysiwyg) scroll/cursor 保留（#857）

> Date: 2026-06-26
> Status: Draft（待 codex round-1 審閱）
> Repo: purdex / branch: `worktree-md-editor-focus-scroll`
> Issue: #857

## 1. Context

切換到 editor 分頁時兩個 UX 缺陷（使用者 alpha.295 回報）：

1. **不自動 focus**：focus 邏輯存在（Monaco `MonacoWrapper.tsx:78-81`、Tiptap `TiptapEditor.tsx:63-66`），但只依賴 `[isActive]`。editor 的 ready 時機（Monaco async `handleMount` / Tiptap lazy + Suspense）可能**晚於** `isActive` 變 true：focus effect 在 `isActive` false→true 觸發時 `editorRef` 還是 null → `focus()` no-op；editor mount 完後 `isActive` 不再變化 → focus **不重試** → 永遠沒 focus。Monaco `handleMount`（`:55-69`）設了 `editorRef` 卻沒在 ready 時 focus。

2. **markdown wysiwyg 切回跳到最後**：Monaco（raw 模式）有 `initialViewState` + `onViewStateChange`（`EditorPane.tsx:427/430`，存 `paneState.monacoViewState`），切回保留 scroll/cursor。**Tiptap（wysiwyg 模式）完全沒傳 viewState**（`EditorPane.tsx:435-440`）—— 零持久化。切回時 `setContent`（`TiptapEditor.tsx:54-61`）+ `focusEditable()`（`:63-66`）把游標 focus 進內容、scroll 跟到末尾。

> markdown 有兩種模式：`raw`（Monaco）/ `wysiwyg`（Tiptap），由 `effectiveEditorMode`（`EditorPane.tsx:97`）決定。本案的 scroll 問題只在 wysiwyg（Tiptap）；raw 走 Monaco 既有 viewState 已正常。

**使用者確認 scope**：viewState 存 **scroll 位置 + 游標位置**（不只 scroll）。

## 2. Goals / Non-Goals

### Goals
- **G1（focus 時序）**：切到 editor 分頁時，無論 editor 在 `isActive=true` 之前或之後才 ready，都要 focus。修法：editor ready 時若當前 isActive 則 focus —— Monaco `handleMount` 設 `editorRef` 後補 `focus()`；Tiptap 在 editor ready（`onCreate` 或等價）後補 `focusEditable()`。以 ref 取最新 isActive 避免 closure stale。既有 `[isActive]` effect 保留（涵蓋「editor 已 ready、之後才 isActive 變 true」）。
- **G2（Tiptap viewState）**：`TiptapEditor`（wysiwyg）保存/恢復 **scrollTop + 游標 selection**，比照 Monaco viewState 模式：
  - `EditorPaneState` 新增 `tiptapViewState: { scrollTop: number; selection: { from: number; to: number } | null } | null`（初始 null）。
  - 新增 `saveTiptapViewState(paneId, viewState)` action。
  - `EditorPane` 傳 `initialViewState={paneState?.tiptapViewState ?? null}` + `onViewStateChange` 給 `TiptapEditor`。
  - `TiptapEditor` unmount cleanup 時保存當前 scrollTop（scroll container）+ selection（`editor.state.selection` 的 `from`/`to`）。
  - mount/ready 時 restore：先 `setContent` → restore selection（**clamp 到 doc 範圍**，超界則退回末尾 caret 或不設）→ restore scrollTop → 若 isActive 才 focus。順序確保 selection/scroll 在原位、focus 不把 scroll 拉回末尾。

### Non-Goals
- **N1**：不改 Monaco（raw）既有 viewState 行為。
- **N2**：不做跨 session / persist 的 viewState（記憶體 paneState 即可，與 monacoViewState 同層；alpha 階段不 persist）。
- **N3**：不處理 content 被外部 reload 後的 selection 精準復原（reload 走既有 path；selection clamp 失敗時安全退回，不報錯）。
- **N4**：不改 raw↔wysiwyg 模式切換的內容轉換邏輯。

## 3. Invariants

- **I1**：focus 補強只在 editor ready **且** 當前 isActive=true 時觸發；isActive=false 時 editor ready 不得 focus（不搶別的 pane 焦點）。
- **I2**：Tiptap selection restore 必須對 doc 範圍 **clamp**：`from`/`to` 超過當前 doc size 時不可 throw（ProseMirror 對越界 position 會丟錯）；安全退回（clamp 到 docSize 或略過 selection、僅 restore scroll）。
- **I3**：restore 時序 = `setContent`（既有，emitUpdate:false）→ selection → scrollTop → focus(若 isActive)。focus 不得覆寫已 restore 的 scrollTop（focusEditable 後若瀏覽器 scrollIntoView caret，因 caret 已在 restore 的 selection 處、scroll 已對位，不會跳）。
- **I4**：`tiptapViewState` 與 `monacoViewState` 同為 paneState 欄位、同生命週期（pane 關閉即清）；wysiwyg/raw 各自獨立 viewState，互不干擾。

## 4. 實作要點

### 4.1 BUG1 focus（`MonacoWrapper.tsx` / `TiptapEditor.tsx`）
- Monaco：`handleMount`（`:55`）末尾，以 `isActiveRef.current` 判斷補 `ed.focus()`。新增 `isActiveRef` + effect 同步 `isActiveRef.current = isActive`（避免 handleMount useCallback closure 抓到舊 isActive）。保留既有 `[isActive]` focus effect。
- Tiptap：`useEditor` 的 `onCreate`（或 editor 變 truthy 的 effect）內，若 `isActiveRef.current` 則 `focusEditable()`。保留既有 `[isActive]` effect。

### 4.2 BUG2 Tiptap viewState
- `useEditorStore`：`EditorPaneState` 加 `tiptapViewState`；`createPaneState` 初始 `null`；新增 `saveTiptapViewState(paneId, vs)` action（mirror `saveMonacoViewState`）。
- `EditorPane.tsx`：wysiwyg 分支（`:435`）傳 `initialViewState={paneState?.tiptapViewState ?? null}` + `onViewStateChange={(vs) => useEditorStore.getState().saveTiptapViewState(paneId, vs)}`。
- `TiptapEditor.tsx`：
  - 新 props：`initialViewState`、`onViewStateChange`。`onViewStateChangeRef` 同步（mirror MonacoWrapper `:47-49,71-76`）。
  - scroll container = `containerRef`（既有 `:72` `tiptap-scroll-root`）。
  - unmount cleanup effect：`onViewStateChangeRef.current({ scrollTop: containerRef.current?.scrollTop ?? 0, selection: editor ? { from: editor.state.selection.from, to: editor.state.selection.to } : null })`。
  - restore：editor ready 後（`onCreate` 或 mount effect，在既有 setContent 之後）：若 `initialViewState`，`editor.commands.setTextSelection(clampRange(initialViewState.selection, docSize))`（越界 clamp / null 略過）+ `containerRef.current.scrollTop = initialViewState.scrollTop`。再依 I3 focus。
  - `clampRange` helper：`from/to` clamp 到 `[0, doc.content.size]`。

## 5. Acceptance Criteria（= 測試契約）

### BUG1 focus（`MonacoWrapper.test.tsx` / `TiptapEditor.test.tsx`）
- **AC1**：Monaco `handleMount` 時 `isActive=true` → `editor.focus()` 被呼叫（模擬 editor ready 晚於 isActive）。
- **AC2**：Tiptap editor ready 時 `isActive=true` → 進入 focus（contentEditable focus）。
- **AC3**：editor ready 時 `isActive=false` → **不** focus（I1）。

### BUG2 Tiptap viewState（`useEditorStore` + `TiptapEditor.test.tsx`）
- **AC4**：`EditorPaneState.tiptapViewState` 初始 `null`；`saveTiptapViewState` 寫入 `{ scrollTop, selection }`。
- **AC5**：`TiptapEditor` unmount → `onViewStateChange` 收到當前 `scrollTop` + selection `{ from, to }`。
- **AC6**：mount with `initialViewState` → scrollTop 套到 scroll container；selection restore 到對應 from/to。
- **AC7（clamp，I2）**：`initialViewState.selection` 的 from/to 超過 doc size → restore **不 throw**，selection 安全 clamp（scroll 仍 restore）。
- **AC8（focus 不搶 scroll，I3）**：restore scrollTop 後 focus，最終 `containerRef.scrollTop` 維持 restore 值（非 0 / 非末尾）。

## 6. Commit 切分（單一 PR，2 commit）

1. `fix(editor): focus editor on activation even when it mounts after isActive (#857)` — G1 / AC1-3（Monaco + Tiptap focus 時序）。
2. `feat(editor): persist scroll + cursor for markdown wysiwyg editor (#857)` — G2 / AC4-8（Tiptap viewState）。

> 兩 commit 獨立（focus 與 viewState 正交）；可分別 review。

## 7. Out of Scope / Known Limitations

- 不碰 Monaco（raw）viewState（N1）；不做跨 session persist（N2）。
- content 外部 reload 後 selection 精準復原不保證（N3，clamp 安全退回）。
- BUG1 的 focus 補強針對「切換分頁/mode 後 editor 才 ready」；若有其他 focus 競爭來源（如同時彈 dialog）不在此處理。
