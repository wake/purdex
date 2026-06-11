# Plan — Editor 開檔/檢視體驗強化（close dirty-guard + image viewer zoom）

> Date: 2026-06-11
> Spec: `docs/specs/2026-06-09-editor-close-image-viewer-spec.md`（已過 codex round-1 review，4 finding 全修）
> Branch: `worktree-editor-close-image-viewer`
> 前提：實作已大致存在於 worktree（uncommitted，無 commit/PR）。本 plan 將既有 code 視為**實作草稿**，補齊 spec round-1 要求的 2 條 acceptance 測試，驗證後切 4 commit 開 PR。

## 0. 現況盤點

- 9 modified + 4 untracked（含本 plan / spec）。既有 4 測試檔 30 測全過、lint clean、build 通過。
- 完整套件另有 4 個 **pre-existing failures**（`sync/contributors/hosts.test.ts` ×3、`TabBar.test.tsx` ×1）— origin/main `alpha.294` 同樣 fail，**不在本 PR 範圍**（spec §N5）。
- 待補：AC11b（untitled dirty guard）、AC17b（image 同步量測）兩條測試 → 目標 32 測。

## 1. Task 切分（對齊 spec §6 的 4 commit）

### T1 — bufferKey helper 抽取（refactor / commit 1）
- 對應 **G1 / AC1-4**。
- 既有：`spa/src/lib/editor-buffer-key.ts` + `.test.ts`（4 測）已存在；`EditorPane.tsx`、`EditorBuffersPane.tsx` 已改 import 共用版本、刪私有 copy。
- 驗證：`vitest run editor-buffer-key`（4 綠）；`grep -rn "function bufferKey" spa/src` 確認無殘留私有定義（僅 `editor-buffer-key.ts` 一處）。
- **caller regression（codex plan-review #1）**：helper 抽取的真正風險在兩個 caller，必須跑既有 targeted 測試確認整合無回歸：
  - `spa/src/components/editor/__tests__/EditorPane.test.tsx`（untitled 開/掛 buffer、rename three-step sync）
  - `spa/src/components/editor/EditorBuffersPane.test.tsx`（rename / delete / smart-open 的 dirty 判斷皆依賴同一 key 公式）
- 無新增 production 工作（純驗證既有 refactor + caller regression）。

### T2 — dirty guard + untitled 測試（feat / commit 2）
- 對應 **G2 / G5 / AC5-11b**。
- 既有：`tab-lifecycle.ts` 的 `closeTab` dirty scan + `editor.close_dirty_confirm` i18n（en + zh-TW）+ 7 測。
- **補（TDD）**：在 `tab-lifecycle.test.ts` 的 `unsaved editor warning` describe 內新增 AC11b：
  - case A：`seedBuffer('untitled:draft', true)` + `editorContent('untitled:draft')` → `closeTab` → `confirm` ×1 + `closeTabInWorkspace(id, undefined)`。
  - case B（可併入）：`seedBuffer('untitled:draft', false)` → 不 `confirm` + 正常關閉（對應「空白/無變更不彈」）。
  - 預期既有實作**直接通過**（untitled 走相同 `bufferKey({type:'inapp'}, 'untitled:...')` → `inapp:untitled:...` 路徑），無 production 改動。
  - **範圍界定（codex plan-review #2）**：`seedBuffer` 是直接 **stub buffer 的 dirty 狀態**來測 `closeTab` 攔截邏輯，**不**重測「輸入內容會把 buffer 翻 dirty」那條 store 契約——後者已由 `spa/src/stores/useEditorStore.test.ts`（`updateContent` → `isDirty`）覆蓋。
- 驗證：`vitest run tab-lifecycle`（8 綠）。

### T3 — image fit/actual + 同步量測測試（feat / commit 3）
- 對應 **G3 / AC12-17b**。
- 既有：`ImagePreviewPane.tsx`（fit/actual、oversized、ResizeObserver、render-time reset）+ 6 測。
- **補（TDD）**：在 `ImagePreviewPane.test.tsx` 新增 AC17b 同步量測 case：
  - `imgComplete=true` + `naturalW/H=2000` + `box=500`；`render(...)` 後**先 `await screen.findByRole('img')` 等 img mount**（`backend.read(filePath)` resolve → `objectUrl` 設入 state → `img` 才 mount，是非同步鏈），**不**呼叫 `fireEvent.load(img)`，再直接 `waitFor` `img.className` 含 `cursor-zoom-in`（僅靠 mount effect 同步 `measureNatural()` 即判定 oversized）。
  - **風險與分流（codex plan-review #3）**：jsdom 下 effect 同步階段讀 prototype-stubbed `complete/naturalWidth` 是否穩定。既有測試靠 `fireEvent.load` 補觸發，未驗證同步路徑。若紅，**依序排查**：(a) 先確認 `img` 是否已 mount（findByRole 是否拿到）——沒拿到是非同步鏈未完成，非 mount effect 問題；(b) img 在但 cursor 沒出 → 才是「測試對 jsdom 同步時序假設不當」（調整測試，如顯式 `act()` flush effect）或「production 同步量測真有缺陷」（修 `ImagePreviewPane.tsx`，spec I5 要求同步量測必須成立）。
- 驗證：`vitest run ImagePreviewPane`（7 綠）。

### T4 — Monaco scrollBeyondLastLine（feat / commit 4）
- 對應 **G4 / AC18**。
- 既有：`MonacoWrapper.tsx` `scrollBeyondLastLine: false→true` + 1 測（AC18）。
- 無新增工作（純驗證）。

## 2. 整合驗證（全 task 完成後）

```
cd spa
npx vitest run src/lib/editor-buffer-key.test.ts src/lib/tab-lifecycle.test.ts \
  src/components/editor/ImagePreviewPane.test.tsx src/components/editor/MonacoWrapper.test.tsx \
  src/components/editor/__tests__/EditorPane.test.tsx src/components/editor/EditorBuffersPane.test.tsx
pnpm run lint
pnpm run build
```
- 通過標準：本任務 4 檔 **32 測全綠** + 2 個 caller regression 檔（EditorPane / EditorBuffersPane）不回歸 / lint clean / build 通過。
- 完整套件 4 個 pre-existing failures 維持原狀（不修、不回歸新增）。

## 3. Commit 順序與訊息

1. `refactor(editor): extract shared bufferKey helper`（commit 1，必先於 2）
2. `feat(editor): warn before closing tab with unsaved changes`（commit 2，含 untitled AC11b）
3. `feat(editor): fit/actual zoom toggle in image preview`（commit 3，含同步量測 AC17b）
4. `feat(editor): enable scrollBeyondLastLine in Monaco`（commit 4）

> commit 1 必須在 2 之前（dirty guard 依賴共用 key）；3、4 彼此獨立。**docs 歸屬講死（codex plan-review #4）**：`spec` + `plan` 兩份文件**併入 commit 1（refactor）**一起提交，維持總數**恰 4 commit**。

## 4. 開發方式

- 依 `feedback_subagent_tdd_priority`：T2 / T3 的「寫測試 → 驗證 →（必要時修 production）」派 subagent 跑 TDD；主 session 負責整合、跑全套驗證、切 commit、開 PR。
- 每個 task 完成即在 worktree 內驗證對應測試綠燈，再進下一個。

## 5. PR 後流程（對齊 CLAUDE.md）

- PR → codex 兩輪 review（R1 標準 + R2 三平行 adversarial）→ 問題彙整表（信心/關聯/複雜度）→ 修 → merge。
- 獨立 bump PR 更新 `VERSION` + `CHANGELOG.md`。
- 清理 worktree、對齊 origin/main。

## 6. 風險彙整

| 風險 | 等級 | 緩解 |
|------|------|------|
| AC17b 同步量測在 jsdom 時序下不穩定 | 中 | T3 先判定 test-vs-prod，分別處置（見 T3） |
| untitled AC11b 揭露 bufferKey 路徑不一致 | 低 | 預期直接綠；若紅代表 untitled 走不同 source/key，需對齊 |
| 既有 4 pre-existing failures 干擾判讀 | 低 | 只跑本任務 4 檔 + 2 caller regression 檔，不跑全套；spec §N5 已界定 |
| helper 抽取後 caller 整合回歸 | 中 | T1 納入 EditorPane / EditorBuffersPane targeted 測試（codex plan-review #1） |
