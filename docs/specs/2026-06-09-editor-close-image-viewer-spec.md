# Spec — Editor 開檔/檢視體驗強化（close dirty-guard + image viewer zoom）

> Date: 2026-06-09
> Status: Draft（待 codex round-1 審閱）
> Repo: purdex / branch: `worktree-editor-close-image-viewer`
> Note: 本 spec 為 **reverse-engineered** — 實作已存在於 worktree（uncommitted，無 PR），spec 忠實描述既有行為作為 review 對照基準與 acceptance 契約。

## 1. Context

Editor 模組已支援以 editor pane（Monaco/Tiptap）與 image-preview pane 開啟檔案。本次累積了四個彼此耦合度不一的小改進，主題為「開檔 / 關閉 / 圖片檢視」的使用體驗強化：

1. **`bufferKey` DRY 抽取**（refactor）— `EditorPane.tsx` 與 `EditorBuffersPane.tsx` 各自帶一份 **私有** `bufferKey` / `bufferKeyFor` helper，公式相同。`EditorBuffersPane` 的版本上方原本就有註解預告「until a shared util is extracted, keep the formula in lock-step」。本次把公式抽到單一 `spa/src/lib/editor-buffer-key.ts`，兩個 call site 改 import 共用版本。是 §2 dirty-guard 的 enabler（`closeTab` 需以相同公式查 buffer dirty 狀態）。
2. **Tab 關閉 dirty guard**（feat）— `closeTab` 在執行關閉前掃 tab layout 的 pane tree，若任一 editor pane 對應的 buffer `isDirty`，跳 `window.confirm`；使用者取消則中止關閉。
3. **ImagePreviewPane fit/actual 縮放**（feat）— 圖片預覽從「永遠 object-contain 縮放至容器」升級為可在 fit ↔ actual（原始像素）間切換，僅當圖片 oversized（natural 尺寸 > 容器）時才可點擊切換並顯示對應 zoom cursor。
4. **MonacoWrapper `scrollBeyondLastLine`**（UX 微調）— `false → true`，讓編輯器底部保留可捲動緩衝。**使用者明確需求（編輯時捲到最後一行仍有一段緩衝空間）；與 1–3 功能正交，同 PR 一併納入**。

### 現況基準
- 改動檔（9 modified + 3 new source files；另含本 spec 檔，working tree 共 4 untracked），全 uncommitted
- 驗證：本任務 4 測試檔 30 測全過 / `pnpm run lint` clean / `pnpm run build` 通過。本 spec round-1 review 後再補 2 條 acceptance 測試（untitled dirty guard、image 同步量測），目標 32 測
- 完整套件另有 4 個 **pre-existing failures**（`sync/contributors/hosts.test.ts` ×3、`TabBar.test.tsx` ×1），origin/main `alpha.294` 同樣 fail，**非本任務造成**，不在本 spec 範圍

## 2. Goals / Non-Goals

### Goals
- **G1（bufferKey 抽取）**：新增 `editor-buffer-key.ts` 匯出純函式 `bufferKey(source, filePath)`，公式與原私有版本逐字一致：`daemon` source → `daemon:${hostId}:${filePath}`，其餘 → `${source.type}:${filePath}`。`EditorPane.tsx`、`EditorBuffersPane.tsx` 兩處改 import，刪除各自私有 copy；行為零變化。
- **G2（dirty guard）**：`closeTab(tabId)` 在實際關閉（`destroyBrowserViewIfNeeded` + `closeTabInWorkspace`）前，掃描該 tab layout 的所有 pane；若存在 `kind==='editor'` 且其 `bufferKey` 對應 buffer `isDirty===true`，跳一次 `window.confirm(t('editor.close_dirty_confirm'))`。confirm 回 true 才繼續關閉；回 false 則整個 `closeTab` 中止（不關、不寫 history）。
- **G3（image zoom）**：`ImagePreviewPane` 支援 fit / actual 兩種顯示模式：
  - 量測圖片 natural 尺寸與容器 box 尺寸，`oversized = natural.w > box.w || natural.h > box.h`。
  - 僅 `oversized` 時：圖片可點擊在 fit ↔ actual 間切換；cursor 顯示 `cursor-zoom-in`（fit 態）/ `cursor-zoom-out`（actual 態）。
  - 非 oversized：無 zoom cursor、點擊 no-op、維持 fit（`object-contain`）。
  - 容器永遠 `overflow-auto` + `min-h-0`（actual 態溢出可捲動）。
  - 切換檔案（`filePath` 變）時 reset 回 fit。
- **G4（monaco scroll）**：`MonacoWrapper` 傳給 Monaco 的 `options.scrollBeyondLastLine` 設為 `true`。
- **G5（i18n）**：新增 key `editor.close_dirty_confirm`（en + zh-TW），供 G2 confirm 文案。

### Non-Goals
- N1：不改 buffer 的 dirty 追蹤機制本身（沿用 `useEditorStore.buffers[key].isDirty`）。
- N2：dirty guard 不做「逐 pane 分別 confirm」或「列出哪些檔 dirty」；一個 tab 內多 dirty editor 也只 confirm 一次（首個 dirty 命中即足夠）。
- N3：dirty guard 不接管 `EditorBuffersPane` 既有的 close-all / close-pane dirty 判斷邏輯（那是另一條既有 path，本次只共用 `bufferKey`，不改其行為）。
- N4：image zoom 不做多級縮放 / 拖曳平移 / 滾輪縮放；只有 fit ↔ actual 二態 toggle。
- N5：不修 §1 提到的 4 個 pre-existing test failures。

## 3. Invariants

- I1：`bufferKey` 為純函式，無副作用、不讀 store；輸出對 `(source, filePath)` 決定性。抽取後三個 call site（EditorPane / EditorBuffersPane / tab-lifecycle）產生的 key 完全一致 — 同一檔在 editor store 中只有一個 buffer 條目。
- I2：`closeTab` 對 **locked tab** 維持既有行為：最前面 early return（`!tab || tab.locked`），**不掃 pane、不 confirm、不關閉**。dirty guard 位於 lock 檢查之後、實際關閉之前。
- I3：dirty guard 只看 `kind==='editor'` 的 pane；`image-preview`、`new-tab`、terminal 等其他 kind 不觸發 confirm。
- I4：buffer 不存在（editor pane 開著但 store 無對應 key）視為「非 dirty」，不 confirm。
- I5：image natural 尺寸量測對「已 cached / HMR 已 `complete` 的圖」也必須成立 — 同步量測 + native `load` listener 雙保險（React `onLoad` 對已 complete 的 img 不可靠）。

## 4. 實作要點（對照既有 code）

### 4.1 `spa/src/lib/editor-buffer-key.ts`（新）
```ts
import type { FileSource } from '../types/fs'
export function bufferKey(source: FileSource, filePath: string): string {
  if (source.type === 'daemon') return `daemon:${source.hostId}:${filePath}`
  return `${source.type}:${filePath}`
}
```
- `EditorPane.tsx`：刪私有 `bufferKey`，改 `import { bufferKey } from '../../lib/editor-buffer-key'`。
- `EditorBuffersPane.tsx`：刪私有 `bufferKeyFor` + 上方註解，4 個 call site 改用 `bufferKey`。

### 4.2 `spa/src/lib/tab-lifecycle.ts`（`closeTab`）
- lock early-return 後，以 `scanPaneTree(tab.layout, ...)` 走訪；命中首個 dirty editor 即設 `dirty` 旗標，**後續 pane 的 callback 早退為 no-op**（`scanPaneTree` 本身無 short-circuit，仍走完整棵樹，但不再做額外判斷）。
- `if (dirty && !window.confirm(t('editor.close_dirty_confirm'))) return`。
- 其後沿用既有 `destroyBrowserViewIfNeeded(tab)` + `closeTabInWorkspace(tabId, opts)`。

### 4.3 `spa/src/components/editor/ImagePreviewPane.tsx`
- state：`zoom: 'fit'|'actual'`、`natural`、`box`；refs：`containerRef`、`imgRef`。
- `seenFilePath` render-time adjust-state-on-prop-change：`filePath` 變 → reset `zoom='fit'` + `natural=null`（避免 setState-in-effect cascade）。
- `measureNatural`（useCallback）：img complete 且 naturalWidth>0 才量；值未變則回 prev（穩定 ref）。
- effect：mount/objectUrl 變時同步 `measureNatural()` + 掛 native `load` listener。
- `useLayoutEffect` + `ResizeObserver` 量容器 box。
- `oversized` useMemo；`isActual = zoom==='actual' && oversized`。
- class：fit → `max-w-full max-h-full object-contain` + 置中；actual → `max-w-none max-h-none` + 左上對齊；cursor 依 oversized/isActual。

### 4.4 `spa/src/components/editor/MonacoWrapper.tsx`
- `options.scrollBeyondLastLine: true`（原 `false`）。

## 5. Acceptance Criteria（= 測試契約）

### bufferKey（`editor-buffer-key.test.ts`, 4）
- AC1 `daemon`+hostId → `daemon:h1:/a.md`
- AC2 `inapp` → `inapp:/a.md`；AC3 `local` → `local:/a.md`
- AC4 同 path 不同 hostId → 不同 key

### closeTab dirty guard（`tab-lifecycle.test.ts`, +8）
- AC5 dirty + 接受 → confirm ×1 + `closeTabInWorkspace(id, undefined)`
- AC6 dirty + 取消 → confirm ×1 + **不**呼叫 closeTabInWorkspace
- AC7 clean → 不 confirm + 正常關閉
- AC8 無 buffer → 不 confirm + 正常關閉
- AC9 非 editor tab → 不 confirm + 正常關閉
- AC10 split（一 dirty 一 clean）→ confirm **恰一次** + 正常關閉
- AC11 locked tab → 不 confirm + **不**關閉
- **AC11b（新建 untitled 文件）** filePath=`untitled:<name>` 的 editor pane，其 buffer（key=`inapp:untitled:<name>`）`isDirty===true` → 關 tab confirm ×1；`isDirty===false`（或無 buffer）→ 不 confirm。涵蓋使用者主訴「新建文件依是否有內容彈警告」的核心場景。

### ImagePreviewPane（`ImagePreviewPane.test.tsx`, 7）
- AC12 oversized → fit 態 `cursor-zoom-in` + `object-contain` + `max-w-full`
- AC13 click → actual 態 `cursor-zoom-out` + `max-w-none` + 無 `object-contain`
- AC14 再 click → 切回 fit（`cursor-zoom-in` + `object-contain`）
- AC15 small image → 無 zoom cursor + click no-op（維持 `object-contain`）
- AC16 容器含 `overflow-auto` + `min-h-0`
- AC17 `filePath` 變 → reset 回 fit
- **AC17b（cached/HMR 同步量測，對應 I5）** 圖片已 `complete`（`naturalWidth>0`）但**不**派發後續 `load` event 時，僅靠 mount effect 的同步 `measureNatural()` 也必須正確判定 oversized 並顯示 `cursor-zoom-in`。測試 render 後**不**呼叫 `fireEvent.load`，直接 `waitFor` zoom cursor 出現。

### MonacoWrapper（`MonacoWrapper.test.tsx`, +1）
- AC18 傳入 options 含 `scrollBeyondLastLine: true`

## 6. Commit 切分（單一 PR，4 commit）
1. `refactor(editor): extract shared bufferKey helper`（G1 / AC1-4）
2. `feat(editor): warn before closing tab with unsaved changes`（G2/G5 / AC5-11b，含 untitled）
3. `feat(editor): fit/actual zoom toggle in image preview`（G3 / AC12-17b，含同步量測）
4. `feat(editor): enable scrollBeyondLastLine in Monaco`（G4 / AC18）

> commit 1 必須在 commit 2 之前（dirty guard 依賴共用 key）。3、4 彼此獨立。

## 7. Out of Scope / Known Limitations
- pre-existing test failures（§1 / §N5）— 另案；本 PR 不碰。
- image zoom 僅二態 toggle，無連續縮放 / 平移（§N4）。
- dirty guard 不列舉 dirty 檔名、不分 pane confirm（§N2）。
