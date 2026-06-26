# Spec — Fix #863: markdown buffer 切換的 transient `Loading editor…` 閃爍

- **Issue**: #863（cosmetic **medium**，來源 PR #862 R4 adversarial review）
- **Base**: alpha.297（`94478635`）
- **Scope**: `spa` only（`EditorPane.tsx` render 推導 + 回歸測試）
- **Status**: draft → codex review

## 1. 問題

同一 pane 從 markdown buffer A（`wysiwyg` 模式）切到另一個 markdown buffer B 時，UI 先 paint 一幀 `Loading editor…` fallback（閃爍 + 一瞬焦點中斷），才落到 B 的 editor。切 buffer 的觸發點包含 `EditorToolbar` 的 `onBufferSwitch` / `onNewBuffer`，以及 breadcrumb popover 切換 —— 它們都呼叫 `setPaneContent` 改 pane 的 `filePath`，並**刻意不**呼叫 `attachPane`（由 EditorPane 自身的 effect rebind）。

## 2. 根因（已查證）

`attachPane`（`EditorPane.tsx:113-115`）是 **post-commit `useEffect`**。切 buffer 後的「第一個 render」同時滿足：

1. `buffer` 已是 B、`isMarkdown === true`；
2. `paneState`（`paneStates[paneId]`）**仍是 A 的**：`editorMode === 'wysiwyg'`、`bufferKey === A`（attachPane effect 尚未跑）；
3. 因此 `effectiveEditorMode === 'wysiwyg'`，但 render 的 wysiwyg 分支被 PR #862 的 **gating render**（`paneState?.bufferKey === key` 才 mount Tiptap）擋下，落入 `else` 的 `Loading editor…` fallback（`EditorPane.tsx:445-452`）。

attachPane effect 跑完後 `paneState` 重建為 B（`createPaneState` 一律 `editorMode: 'raw'`）→ 第二個 render 走 raw Monaco。閃爍 = 第一幀用了 **stale A 的 `editorMode`** 卻被 gating 擋成 Loading。

> 非本 PR 引入的正確性回歸（attachPane 時序本就如此；PR #862 之前 transient 改閃 Suspense fallback / 一瞬 Tiptap content）。PR #862 的 gating render 正確修掉了「stale Tiptap mount 污染 `didRestoreRef`」，但把 transient 視窗變成可見的 `Loading editor…`。

### 2.1 stale 露出的不只 editorMode

第一個 render 中**整個 `paneState` 都是 stale A 的**。除 `editorMode` 外，若放任提前 mount editor，`monacoViewState` / `tiptapViewState` / `cursorPosition` / `showDiff` 也都會是 A 的值，套到 B 上。任何「讓 editor 在 stale 視窗提前 mount」的修法都必須一併處理，否則會把閃爍換成 scroll/cursor 錯位的新 bug。

## 3. 方案

### 方案 A — `attachPane` 改 `useLayoutEffect`（記憶原案，**不採用**）

讓 rebind 在 paint 前同步完成，stale 視窗不被瀏覽器 paint。

- 優點：一招覆蓋所有 stale 衍生（mode + 三種 viewState）。
- 缺點 1（可測性）：jsdom/RTL 下 `act()` 會把 layout effect 與 passive effect **一併同步 flush**，「不閃」無法確定性斷言；只能斷言「attachPane 註冊為 layout effect」這種脆弱的 implementation detail。
- 缺點 2（時序面）：改動 `attachPane`（它同時負責切走時刪除舊 buffer）的執行時機，碰觸 §對全 EditorPane 的影響面，風險高於必要。

### 方案 B — render 階段同步派生 aligned paneState（**採用**）

不依賴 effect 時序，於 render 推導一個 `alignedPaneState`：**僅當 `paneState.bufferKey === key` 時才信任 `paneState`，否則視為「尚未對齊」並一律採用全新對齊語義**（等同 `createPaneState` 的預設：`editorMode: 'raw'`、所有 viewState `null`、`cursorPosition {1,1}`、`showDiff false`）。

```ts
const alignedPaneState = paneState?.bufferKey === key ? paneState : undefined
const editorMode = alignedPaneState?.editorMode ?? 'raw'
const effectiveEditorMode = isMarkdown ? editorMode : 'raw'
const showDiff = alignedPaneState?.showDiff ?? false
// Monaco/Tiptap/StatusBar 皆改讀 alignedPaneState（viewState/cursor 同理）
```

**為何正確**：attachPane 跑完後 `paneState` 必為 `createPaneState(B)`（raw + null viewState）。`alignedPaneState` 在 stale 視窗回傳的預設值與「對齊後」**完全相同**，因此：

- stale 視窗直接 render **最終態的 raw Monaco**（而非 `Loading editor…`），消除閃爍；
- Monaco `key={buffer.modelId}`（B 的 modelId 在 stale/aligned 兩幀不變）→ 不 remount，`initialViewState` 兩幀皆 `null` → **無 scroll/cursor 錯位**；
- 進入 wysiwyg 分支的前提變為「`isMarkdown && alignedPaneState && editorMode==='wysiwyg'`」，亦即 `paneState.bufferKey === key` 恆成立 → §2 的 `Loading editor…` fallback 在 wysiwyg 路徑下不可達。

**與 R3 gating 的關係**：方案 B 提供 *更強* 的保護而非移除 —— stale 視窗根本不進 wysiwyg 分支（連 Tiptap 都不評估），`didRestoreRef` 不可能被 stale paneState 污染。據此移除 `EditorPane.tsx:445-452` 的 `else` fallback 分支，第三分支化簡為直接 mount Tiptap（此時 `alignedPaneState` 必非 undefined）。

> 取捨：方案 B 是純 render 派生，零 effect 時序依賴、可在 render 層確定性斷言、不動 attachPane 的 buffer 刪除時機。記憶原案（A）的可測性與風險面均劣於 B，故偏離原案。

## 4. 驗收條件（AC）

- **AC1**：freeze `attachPane`（mock noop）後，將一個先前處於 `wysiwyg`、`bufferKey=A` 的 pane render 成指向 markdown buffer B —— **不得** mount `TiptapEditor`，**不得**出現 `Loading editor…` fallback，**必須** mount raw Monaco（`monaco-wrapper`）。（回歸 #863）
- **AC2**：同 AC1 條件下，Monaco 收到的 `initialViewState` 為 `null`（不得是 A 的 `monacoViewState`）。**測試需透過 `monacoPropsSpy`（mock `MonacoWrapper` 時記錄 props）斷言 `initialViewState === null`** —— 否則只驗到「有 mount raw Monaco」，驗不到 viewState 未外洩。（防 §2.1 viewState 錯位；codex spec review F1）
- **AC3**：正常對齊路徑不回歸 —— pane 對齊 buffer B 且 `editorMode==='wysiwyg'` 時仍 mount Tiptap 並傳入該 pane 的 `tiptapViewState`（既有 AC9 test 續綠）。
- **AC4**：非 markdown buffer 一律 raw；既有 R3 gating test 改為驗證「stale 時 mount raw Monaco 而非 fallback」後續綠。
- **AC5**：覆蓋 §2.1 其餘 stale 露出面 —— 同 AC1 條件下，stale paneState 帶 `showDiff:true` 時**不得**先 mount `DiffView`（`diff-view` 不在場），stale `cursorPosition`（如 5,9）**不得**外洩到 status bar（`EditorStatusBar` 收到 `line:1, column:1`）。（codex spec review F2）
- **AC6**：`cd spa && npx vitest run` 全綠（既有 4 個 pre-existing 失敗 `TabBar` / `sync hosts` 與本變更無關，已單獨追蹤）、`pnpm run lint`、`pnpm run build` 通過。

## 5. 測試計畫

- 新增 AC1/AC2/AC5 回歸 test（單一 #863 test，沿用既有 `freeze attachPane` 手法：`vi.spyOn(store,'attachPane').mockImplementation(()=>{})`）：擴充 `MonacoWrapper` mock 以 `monacoPropsSpy` 記錄 props（AC2）；在 stale paneState 上預設 `showDiff:true` + `cursorPosition 5,9`，斷言 render B 後 `diff-view` 不在場、`EditorStatusBar` 收到 `line:1/column:1`（AC5）。
- 改寫既有 R3 gating test（`EditorPane.test.tsx:917`）斷言：stale 時 `monaco-wrapper` 在場、`tiptap-editor` 不在場、無 `Loading editor…`。
- 跑全 `EditorPane` / `TiptapEditor` / `useEditorStore` 套件確認 attachPane 影響面無回歸。

## 6. 非目標

- 不動 `attachPane` 的 effect 本體與 buffer 刪除語義。
- 不動 Tiptap/Monaco 內部還原邏輯（#857 已處理）。
- 不處理切 buffer 時 buffer B 尚未 load 的 `Loading...`（資料層 loading，屬正常狀態，非本 bug）。
```
