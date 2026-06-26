# Spec — Editor 切換分頁 focus + markdown(wysiwyg) scroll/cursor 保留（#857）

> Date: 2026-06-26
> Status: Draft v5（spec 2 輪 + plan-review 2 輪收斂；inlineContent 前置檢查；AC7/AC8 強化；I6 transient reuse 加 key + known-limitation）
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
- **I2（selection 合法 restore，inlineContent 前置檢查）**：Tiptap selection restore 必須走**單一路徑**、優先保留原 range，不可 throw。**關鍵事實（本地 `prosemirror-state` 實證）：`TextSelection.create` 對非 textblock 位置不會 throw —— 只 `console.warn` 並回傳一個非法 selection**（`create(doc,0,0)` 回 `TextSelection 0..0`，`resolve(0).parent.inlineContent === false`）。因此**不可**用 `try/catch` 判斷合法性，必須**前置檢查 `inlineContent`**：
  1. `from`/`to` **各自** clamp 到 `[0, doc.content.size]`（`doc.resolve(pos)` 越界會 `throw RangeError`，故必須先 clamp 再 resolve）。
  2. `const $from = doc.resolve(from), $to = doc.resolve(to)`；**僅當** `$from.parent.inlineContent && $to.parent.inlineContent` 才 `TextSelection.create(doc, from, to)` —— 保留原 range（含非 collapsed，對齊 AC6）。
  3. 否則退化 `Selection.near($from)`（回最近合法 `Selection`，不 throw；本地實證 `near(resolve(0))` 回 `1..1`）。
  `Selection.near` 只接受單一 `ResolvedPos`、回傳最近合法 selection（可能收斂成 caret），**不是** range restore 的等價方案 —— 只作 inlineContent 檢查未過時的退化路徑。任何情況下 scroll 仍 restore。
- **I3（restore→focus 順序）**：restore 時序 = selection → scrollTop → focus(若 isActive)。focus 不得覆寫已 restore 的 scrollTop（focusEditable 後若瀏覽器 scrollIntoView caret，因 caret 已在 restore 的 selection 處、scroll 已對位，不會跳）。本契約以「restore 的呼叫順序早於 focus」為可單元測形式驗證（見 AC8）。
- **I4**：`tiptapViewState` 與 `monacoViewState` 同為 paneState 欄位、同生命週期（pane 關閉即清）；wysiwyg/raw 各自獨立 viewState，互不干擾。
- **I5（one-shot initial restore 時序）**：initial restore/focus 必須在 editor 已建立 **且** editable DOM 已 render 之後，以 **one-shot effect**（`didRestoreRef` 守門，只跑一次）執行；不可只掛 `onCreate`（可能早於 EditorContent 掛 DOM）。初次 mount 既有的 content sync effect（`setContent`）會把 selection 重設到 doc 開頭 —— 實作必須確保初次 content sync **不重設** 已 restore 的 selection/scroll（例如以 `hasInitializedRef` 跳過初次 sync，或讓 one-shot restore 在初次 sync 之後執行）。
- **I6（Tiptap key — 堵 transient 跨 buffer reuse）**：`EditorPane` 對 `TiptapEditor` 加 `key={buffer.modelId}`（對齊既有 Monaco 分支 `EditorPane.tsx:422`），buffer 身份變即 remount → 重置 `didRestoreRef`/`hasInitializedRef`。**背景（plan-review round-1 Finding 2 → round-2 修正）**：`attachPane`（`:113-115`）是 commit 後 effect，切到另一已載入 markdown buffer 的**第一個 render** 仍讀到舊 `paneState.editorMode='wysiwyg'` → 進入 Tiptap 分支；無 key 時 React reconcile **重用同一 TiptapEditor 實例一個 render**（`didRestoreRef` stale），effect 跑完才 `createPaneState` reset `editorMode→raw`、切 Monaco。故 transient reuse **可達（一個 render）**，但**最終穩定態為 Monaco**（raw）。`key` 使 buffer 身份變即 remount，堵此 transient。回歸測試策略見 plan（最終穩定態 Monaco 使 RTL 難以觀測 transient；需 unmock 整合測試）。

## 4. 實作要點

### 4.1 BUG1 focus（`MonacoWrapper.tsx` / `TiptapEditor.tsx`）
- Monaco：`handleMount`（`:55`）末尾，以 `isActiveRef.current` 判斷補 `ed.focus()`。新增 `isActiveRef` + effect 同步 `isActiveRef.current = isActive`（避免 handleMount useCallback closure 抓到舊 isActive）。保留既有 `[isActive]` focus effect。
- Tiptap：新增 `isActiveRef` + effect 同步 `isActiveRef.current = isActive`。focus 補強整合進 §4.2 的 **one-shot initial restore effect**（restore selection/scroll 後若 `isActiveRef.current` 才 `focusEditable()`，確保 I3 順序）。保留既有 `[isActive]` effect（涵蓋 editor 已 ready 後 isActive 才變 true）。

### 4.2 BUG2 Tiptap viewState
- `useEditorStore`：`EditorPaneState` 加 `tiptapViewState`；`createPaneState` 初始 `null`；新增 `saveTiptapViewState(paneId, vs)` action（mirror `saveMonacoViewState`）。
- `EditorPane.tsx`：wysiwyg 分支（`:435`）加 `key={buffer.modelId}`（I6，對齊 Monaco 分支 `:422`）+ 傳 `initialViewState={paneState?.tiptapViewState ?? null}` + `onViewStateChange={(vs) => useEditorStore.getState().saveTiptapViewState(paneId, vs)}`。
- `TiptapEditor.tsx`：
  - 新 props：`initialViewState`、`onViewStateChange`。`onViewStateChangeRef` 同步（mirror MonacoWrapper `:47-49,71-76`）。
  - **`editorRef`**：新增 `editorRef`，effect 同步 `editorRef.current = editor`（editor 變化時更新）。`[]` cleanup 必須讀 `editorRef.current`，**不可**讀 render 閉包的 `editor`（初次為 null，I3-cleanup 對應 finding #3）。
  - scroll container = `containerRef`（既有 `:72` `tiptap-scroll-root`）。
  - unmount cleanup effect（`[]`）：`onViewStateChangeRef.current({ scrollTop: containerRef.current?.scrollTop ?? 0, selection: editorRef.current ? { from: editorRef.current.state.selection.from, to: editorRef.current.state.selection.to } : null })`。
  - **one-shot initial restore**（I5）：`didRestoreRef`（初 false）守門的 effect，依賴 `[editor]`；當 `editor` truthy 且 `!didRestoreRef.current` 時執行一次並設 `didRestoreRef.current = true`：
    1. selection restore（若 `initialViewState?.selection`，依 I2，**抽純函式 `resolveRestoreSelection(doc, saved)`** 以便用真實 PM doc 單元測）：clamp from/to → `doc.resolve` 兩端 → 檢查 `$from.parent.inlineContent && $to.parent.inlineContent`，是則 `TextSelection.create(doc, from, to)`、否則 `Selection.near($from)`；再 `editor.view.dispatch(editor.state.tr.setSelection(sel))`。
    2. scroll restore：`containerRef.current.scrollTop = initialViewState.scrollTop`。
    3. 若 `isActiveRef.current` 才 `focusEditable()`（順序晚於 1、2，I3）。
  - 既有 `setContent` content-sync effect（`:54-61`）需確保**初次 mount 不重設** restore 後的 selection/scroll（I5）—— 以 `hasInitializedRef` 跳過初次 sync（首次只標記、不 `setContent`，content 已由 `useEditor({ content })` 初始化），之後外部 content 變化照舊 sync。
  - imports：`TextSelection`（inlineContent 通過時）+ `Selection`（退化 `near`）from `@tiptap/pm/state`；`resolveRestoreSelection` 在 `tiptapSelection.ts`。

## 5. Acceptance Criteria（= 測試契約）

### 5.0 測試前提（mock lifecycle 契約，防假綠燈）

既有 `TiptapEditor.test.tsx` 的 `useEditor` mock **一開始就回傳 editor 物件**、`EditorPane.test.tsx` 的 `TiptapEditor` mock **不接 props** —— 這種形狀下 AC5/AC8/AC9 會**假綠燈**（`[]` cleanup 錯抓初次 render 閉包、one-shot restore 從未經 `null→editor` ready transition、`EditorPane` 沒把 `initialViewState/onViewStateChange` 傳下去，全都能測綠）。本案測試必須強化 mock：

- **M1**：`TiptapEditor.test.tsx` 的 `useEditor` mock 必須**先回 `null`**，再於 rerender/flush 後提供 live editor，模擬真實 `null→editor` ready transition（AC2/AC5/AC8 的辨識力前提）。
- **M2**：editor mock 必須含**可變** `state.selection`（`from`/`to`）、`state.tr` + `view.dispatch`（讓 selection restore 與 unmount cleanup 讀到真實 live 值，而非常數 stub）。
- **M3**：`EditorPane.test.tsx` 的 `TiptapEditor` mock 必須 **capture `initialViewState` 與 `onViewStateChange`** 並能主動觸發回呼（AC9 驗 wiring 的前提）。
- **M4**：AC7（非法退化）建議直接 import 本地真實 `@tiptap/pm/state`（`TextSelection`/`Selection`）對真 doc 驗證，比純 spy 更能避免假綠燈。

### BUG1 focus（`MonacoWrapper.test.tsx` / `TiptapEditor.test.tsx`）
- **AC1**：Monaco `handleMount` 時 `isActive=true` → `editor.focus()` **至少被呼叫一次**（模擬 editor ready 晚於 isActive）。只驗「有 focus」，不要求「只 focus 一次」（finding #6：ready-path 與 `[isActive]` effect 可能都觸發，無害）。
- **AC2**：Tiptap one-shot ready effect 時 `isActive=true` → contentEditable **至少被 focus 一次**（同 AC1，不綁次數）。
- **AC3**：editor ready 時 `isActive=false` → ready-path **不** focus（I1）。

### BUG2 Tiptap viewState（`useEditorStore` + `TiptapEditor.test.tsx`）
- **AC4**：`EditorPaneState.tiptapViewState` 初始 `null`；`saveTiptapViewState` 寫入 `{ scrollTop, selection }`。
- **AC5**：`TiptapEditor` unmount → `onViewStateChange` 收到當前 `scrollTop` + selection `{ from, to }`，且 selection 取自 **`editorRef.current.state.selection`**（live editor，非初次 render 閉包的 null editor，finding #3）。**前提 M1/M2**：mock 須走 `null→editor` transition 且暴露可變 `state.selection`，否則「錯抓初次 null 閉包」測不出。
- **AC6（range 完整保留）**：mount with `initialViewState`（合法、`from≠to` 的 range）→ scrollTop 套到 scroll container；selection **完整 restore 成原 range**（`from`/`to` 與輸入一致，**不**收斂成 caret）—— 走 `TextSelection.create` 主路徑（前提 M1/M2）。
- **AC7（非法位置退化，I2）**：`initialViewState.selection` 超過 doc size 或落在非 inlineContent 位置（如 `pos 0`，parent=doc）→ restore **不 throw**；經 inlineContent 前置檢查退化到 `Selection.near` 的合法落點。**斷言必須驗退化結果落在 inlineContent 內**（`doc.resolve(sel.from).parent.inlineContent === true`），**不可**只驗 `sel.to <= doc.content.size` —— 本地實證 `TextSelection.create(doc, size, size)` 回非法的 `size..size` 也滿足 `<= size`，弱斷言會讓舊 try/catch 錯路徑假綠（plan-review round-2 Finding 1）。合法 range（AC6）不得走退化路徑。直接用真實 `@tiptap/pm/state` 對真 doc 驗證（前提 M4）。
- **AC8（restore 早於 focus，I3，順序契約）**：驗證 selection restore **與** scrollTop 設定 **皆早於** `focusEditable()`，兩者**都要獨立可觀測驗證**：(1) `view.dispatch`（selection）的 `invocationCallOrder` < focus；(2) **focus 時 scroll container 的 `scrollTop` 已 = restore 值**（focus spy 內讀 DOM scrollTop）—— 不可只驗 `dispatch < focus`，否則實作把 `focusEditable()` 放在 scrollTop 設定之前仍會假綠、scroll-jump 未被堵（plan-review round-2 Finding 3）。另驗 `tr.setSelection` 收到 `resolveRestoreSelection` 的回傳值（非隨意 transaction）。不依賴真實 caret scrollIntoView（jsdom 不支援；`@tiptap/react` 被 mock、focus 為 spy）。**前提 M1/M2**：mock 暴露可變 `state.selection` / `view.dispatch`。
- **AC9（EditorPane wiring 整合，finding #4）**：`EditorPane` wysiwyg 分支確實把 `paneState.tiptapViewState` 以 `initialViewState` 傳入 `TiptapEditor`，且 `onViewStateChange` 回呼確實呼叫 `saveTiptapViewState(paneId, vs)` 寫回 store（驗 prop 傳遞 + store 回寫，補 AC4-8 只各驗單側之缺口）。**前提 M3**：`EditorPane` 的 `TiptapEditor` mock 須 capture 兩 props 並能觸發 `onViewStateChange`。

## 6. Commit 切分（單一 PR，2 commit）

1. `fix(editor): focus editor on activation even when it mounts after isActive (#857)` — G1 / AC1-3（Monaco + Tiptap focus 時序）。
2. `feat(editor): persist scroll + cursor for markdown wysiwyg editor (#857)` — G2 / AC4-9（Tiptap viewState + EditorPane wiring）。

> 兩 commit 獨立（focus 與 viewState 正交）；可分別 review。

## 7. Out of Scope / Known Limitations

- 不碰 Monaco（raw）viewState（N1）；不做跨 session persist（N2）。
- content 外部 reload 後 selection 精準復原不保證（N3，clamp 安全退回）。
- BUG1 的 focus 補強針對「切換分頁/mode 後 editor 才 ready」；若有其他 focus 競爭來源（如同時彈 dialog）不在此處理。
- **I6 transient（known-limitation）**：切到另一已載入 markdown buffer 的第一個 render 會有 transient TiptapEditor reuse，已以 `key={buffer.modelId}` 堵之；其殘留副作用（一個 render、最終穩定態為 Monaco、最壞使 B 的 `tiptapViewState` 退化為 `scrollTop:0`）極小。觀測 transient 需 unmock TiptapEditor 的整合測試（jsdom 跑 ProseMirror，成本高），**不寫**（使用者決定，YAGNI）；`key` 的正確性由「對齊 Monaco 既有模式 + buffer identity 一致」保證。
