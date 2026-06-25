# Spec — Editor 切換分頁 focus + markdown(wysiwyg) scroll/cursor 保留（#857）

> Date: 2026-06-26
> Status: Draft v2（codex round-1 6 finding 已修；待 round-2 / plan）
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
- **G1（focus 時序）**：切到 editor 分頁時，無論 editor 在 `isActive=true` 之前或之後才 ready，都要 focus。修法：editor ready 時若當前 isActive 則 focus —— Monaco `handleMount` 設 `editorRef` 後補 `focus()`；Tiptap 在 one-shot ready effect（`editor` 變 truthy、EditorContent DOM 已掛載後，見 §4.2）補 `focusEditable()`。以 ref 取最新 isActive 避免 closure stale。既有 `[isActive]` effect 保留（涵蓋「editor 已 ready、之後才 isActive 變 true」）。ready-path 補 focus 與既有 `[isActive]` effect 在某些時序可能都觸發，focus 兩次無害（不綁「只 focus 一次」契約，見 AC1/AC2）。
- **G2（Tiptap viewState）**：`TiptapEditor`（wysiwyg）保存/恢復 **scrollTop + 游標 selection**，比照 Monaco viewState 模式：
  - `EditorPaneState` 新增 `tiptapViewState: { scrollTop: number; selection: { from: number; to: number } | null } | null`（初始 null）。
  - 新增 `saveTiptapViewState(paneId, viewState)` action。
  - `EditorPane` 傳 `initialViewState={paneState?.tiptapViewState ?? null}` + `onViewStateChange` 給 `TiptapEditor`。
  - `TiptapEditor` unmount cleanup 時保存當前 scrollTop（scroll container）+ selection（`from`/`to`）。**editor instance 必須以 ref（`editorRef`）持有**：`[]` cleanup 的閉包抓的是初次 render 的 `editor`（=null），讀不到 live editor（與 Monaco `editorRef` 同理）；cleanup 讀 `editorRef.current?.state.selection` + `containerRef.current?.scrollTop`。
  - **initial restore 以 one-shot effect 執行**（editor 已建立 **且** editable DOM 已 render 後，見 §4.2 / I5）：restore selection（PM 合法 fallback，見 I2）→ restore scrollTop → 若 isActive 才 focus。順序確保 selection/scroll 在原位、focus 不把 scroll 拉回末尾。初次 mount 的 content sync（既有 `setContent` effect）不得重設已 restore 的 selection/scroll（I5）。

### Non-Goals
- **N1**：不改 Monaco（raw）既有 viewState 行為。
- **N2**：不做跨 session / persist 的 viewState（記憶體 paneState 即可，與 monacoViewState 同層；alpha 階段不 persist）。
- **N3**：不處理 content 被外部 reload 後的 selection 精準復原（reload 走既有 path；selection clamp 失敗時安全退回，不報錯）。
- **N4**：不改 raw↔wysiwyg 模式切換的內容轉換邏輯。

## 3. Invariants

- **I1**：focus 補強只在 editor ready **且** 當前 isActive=true 時觸發；isActive=false 時 editor ready 不得 focus（不搶別的 pane 焦點）。
- **I2（selection 合法 fallback）**：Tiptap selection restore 不可 throw 也不可只靠 `[0, doc.content.size]` clamp + `setTextSelection`：ProseMirror 的合法 `TextSelection` 受 textblock / 節點邊界限制，`0` 或 block boundary 等位置即使不越界也可能非法而丟錯。實作必須走 PM 合法 fallback —— 以 `Selection.near(doc.resolve(clampedPos))` 找最近合法位置，或 `try { TextSelection.create(...) } catch { Selection.atEnd(doc) }` 退回末尾 caret；任何情況下 scroll 仍 restore。
- **I3（restore→focus 順序）**：restore 時序 = selection → scrollTop → focus(若 isActive)。focus 不得覆寫已 restore 的 scrollTop（focusEditable 後若瀏覽器 scrollIntoView caret，因 caret 已在 restore 的 selection 處、scroll 已對位，不會跳）。本契約以「restore 的呼叫順序早於 focus」為可單元測形式驗證（見 AC8）。
- **I4**：`tiptapViewState` 與 `monacoViewState` 同為 paneState 欄位、同生命週期（pane 關閉即清）；wysiwyg/raw 各自獨立 viewState，互不干擾。
- **I5（one-shot initial restore 時序）**：initial restore/focus 必須在 editor 已建立 **且** editable DOM 已 render 之後，以 **one-shot effect**（`didRestoreRef` 守門，只跑一次）執行；不可只掛 `onCreate`（可能早於 EditorContent 掛 DOM）。初次 mount 既有的 content sync effect（`setContent`）會把 selection 重設到 doc 開頭 —— 實作必須確保初次 content sync **不重設** 已 restore 的 selection/scroll（例如以 `hasInitializedRef` 跳過初次 sync，或讓 one-shot restore 在初次 sync 之後執行）。

## 4. 實作要點

### 4.1 BUG1 focus（`MonacoWrapper.tsx` / `TiptapEditor.tsx`）
- Monaco：`handleMount`（`:55`）末尾，以 `isActiveRef.current` 判斷補 `ed.focus()`。新增 `isActiveRef` + effect 同步 `isActiveRef.current = isActive`（避免 handleMount useCallback closure 抓到舊 isActive）。保留既有 `[isActive]` focus effect。
- Tiptap：新增 `isActiveRef` + effect 同步 `isActiveRef.current = isActive`。focus 補強整合進 §4.2 的 **one-shot initial restore effect**（restore selection/scroll 後若 `isActiveRef.current` 才 `focusEditable()`，確保 I3 順序）。保留既有 `[isActive]` effect（涵蓋 editor 已 ready 後 isActive 才變 true）。

### 4.2 BUG2 Tiptap viewState
- `useEditorStore`：`EditorPaneState` 加 `tiptapViewState`；`createPaneState` 初始 `null`；新增 `saveTiptapViewState(paneId, vs)` action（mirror `saveMonacoViewState`）。
- `EditorPane.tsx`：wysiwyg 分支（`:435`）傳 `initialViewState={paneState?.tiptapViewState ?? null}` + `onViewStateChange={(vs) => useEditorStore.getState().saveTiptapViewState(paneId, vs)}`。
- `TiptapEditor.tsx`：
  - 新 props：`initialViewState`、`onViewStateChange`。`onViewStateChangeRef` 同步（mirror MonacoWrapper `:47-49,71-76`）。
  - **`editorRef`**：新增 `editorRef`，effect 同步 `editorRef.current = editor`（editor 變化時更新）。`[]` cleanup 必須讀 `editorRef.current`，**不可**讀 render 閉包的 `editor`（初次為 null，I3-cleanup 對應 finding #3）。
  - scroll container = `containerRef`（既有 `:72` `tiptap-scroll-root`）。
  - unmount cleanup effect（`[]`）：`onViewStateChangeRef.current({ scrollTop: containerRef.current?.scrollTop ?? 0, selection: editorRef.current ? { from: editorRef.current.state.selection.from, to: editorRef.current.state.selection.to } : null })`。
  - **one-shot initial restore**（I5）：`didRestoreRef`（初 false）守門的 effect，依賴 `[editor]`；當 `editor` truthy 且 `!didRestoreRef.current` 時執行一次並設 `didRestoreRef.current = true`：
    1. selection restore（若 `initialViewState?.selection`）：以 PM 合法 fallback 設定（I2）—— `const { doc } = editor.state; const pos = clamp(sel.from/to, 0, doc.content.size);` 用 `Selection.near(doc.resolve(pos))` 或 `try TextSelection.create catch Selection.atEnd`，再 `editor.view.dispatch(tr.setSelection(...))`。
    2. scroll restore：`containerRef.current.scrollTop = initialViewState.scrollTop`。
    3. 若 `isActiveRef.current` 才 `focusEditable()`（順序晚於 1、2，I3）。
  - 既有 `setContent` content-sync effect（`:54-61`）需確保**初次 mount 不重設** restore 後的 selection/scroll（I5）—— 以 `hasInitializedRef` 跳過初次 sync（首次只標記、不 `setContent`，content 已由 `useEditor({ content })` 初始化），之後外部 content 變化照舊 sync。
  - imports：`Selection`（必要時 `TextSelection`）from `@tiptap/pm/state`。

## 5. Acceptance Criteria（= 測試契約）

### BUG1 focus（`MonacoWrapper.test.tsx` / `TiptapEditor.test.tsx`）
- **AC1**：Monaco `handleMount` 時 `isActive=true` → `editor.focus()` **至少被呼叫一次**（模擬 editor ready 晚於 isActive）。只驗「有 focus」，不要求「只 focus 一次」（finding #6：ready-path 與 `[isActive]` effect 可能都觸發，無害）。
- **AC2**：Tiptap one-shot ready effect 時 `isActive=true` → contentEditable **至少被 focus 一次**（同 AC1，不綁次數）。
- **AC3**：editor ready 時 `isActive=false` → ready-path **不** focus（I1）。

### BUG2 Tiptap viewState（`useEditorStore` + `TiptapEditor.test.tsx`）
- **AC4**：`EditorPaneState.tiptapViewState` 初始 `null`；`saveTiptapViewState` 寫入 `{ scrollTop, selection }`。
- **AC5**：`TiptapEditor` unmount → `onViewStateChange` 收到當前 `scrollTop` + selection `{ from, to }`，且 selection 取自 **`editorRef.current.state.selection`**（live editor，非初次 render 閉包的 null editor，finding #3）。
- **AC6**：mount with `initialViewState` → scrollTop 套到 scroll container；selection restore 到對應 from/to。
- **AC7（合法 fallback，I2）**：`initialViewState.selection` 的 from/to 超過 doc size 或落在非法（非 textblock）位置 → restore **不 throw**，selection 經 PM 合法 fallback（`Selection.near` / `atEnd`）安全落點（scroll 仍 restore）。
- **AC8（restore 早於 focus，I3，順序契約）**：以 spy 記錄呼叫順序，驗證 selection restore + scrollTop 設定 **皆早於** `focusEditable()`。不依賴真實 caret scrollIntoView（jsdom 不支援；既有 test 將 `@tiptap/react` mock，focus 為 spy）—— 改驗可單元測的順序契約而非最終 scrollTop 數值。
- **AC9（EditorPane wiring 整合，finding #4）**：`EditorPane` wysiwyg 分支確實把 `paneState.tiptapViewState` 以 `initialViewState` 傳入 `TiptapEditor`，且 `onViewStateChange` 回呼確實呼叫 `saveTiptapViewState(paneId, vs)` 寫回 store（驗 prop 傳遞 + store 回寫，補 AC4-8 只各驗單側之缺口）。

## 6. Commit 切分（單一 PR，2 commit）

1. `fix(editor): focus editor on activation even when it mounts after isActive (#857)` — G1 / AC1-3（Monaco + Tiptap focus 時序）。
2. `feat(editor): persist scroll + cursor for markdown wysiwyg editor (#857)` — G2 / AC4-9（Tiptap viewState + EditorPane wiring）。

> 兩 commit 獨立（focus 與 viewState 正交）；可分別 review。

## 7. Out of Scope / Known Limitations

- 不碰 Monaco（raw）viewState（N1）；不做跨 session persist（N2）。
- content 外部 reload 後 selection 精準復原不保證（N3，clamp 安全退回）。
- BUG1 的 focus 補強針對「切換分頁/mode 後 editor 才 ready」；若有其他 focus 競爭來源（如同時彈 dialog）不在此處理。
