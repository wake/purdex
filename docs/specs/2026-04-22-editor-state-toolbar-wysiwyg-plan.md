# Editor State / Toolbar / WYSIWYG 修正 Implementation Plan

> 日期：2026-04-22
> 狀態：Draft v2（post plan-review）
> 主 spec：`2026-04-22-editor-state-toolbar-wysiwyg-design.md`
> 範圍：純 SPA（editor module / tab store / tests）
> 參考：
> - Codex plan 審查：thread `019db16a-e715-72f1-b284-a5b5411b1126`（2026-04-22）

---

## 1. 範圍

本計畫只處理 editor module 的四個使用者問題：

1. tab 切換後 editor runtime state 遺失
2. top bar 缺 breadcrumbs
3. top bar 缺 inline rename 與撞名保護
4. WYSIWYG 容器與捲動異常

不改 daemon / Electron API，不改檔案 backend contract；rename 仍使用既有 `FsBackend.rename()`。

---

## 2. 檔案清單

### 修改

- `spa/src/stores/useEditorStore.ts`
  - 分離 shared buffer state 與 pane-local view state
  - 加入 shared buffer rename / conditional close / Monaco view state 保存 API
- `spa/src/stores/useEditorStore.test.ts`
  - 覆蓋 shared vs pane-local 邊界、rename migration、conditional close
- `spa/src/components/editor/EditorPane.tsx`
  - 移除 local `editorMode` / `showDiff`
  - 接上 pane-local state
  - unmount 時改為 conditional close
  - 接上 rename 流程與 warning
- `spa/src/components/editor/__tests__/EditorPane.test.tsx`
  - 補 tab 切換 state 保留、breadcrumbs、rename success / conflict
- `spa/src/components/editor/MonacoWrapper.tsx`
  - 對 shared buffer 使用穩定 `modelId`
  - 對 pane 儲存 / 還原 view state
- `spa/src/components/editor/MonacoWrapper.test.tsx`
  - 驗證 `modelId`、view-state save / restore wiring
- `spa/src/components/editor/EditorToolbar.tsx`
  - 顯示 breadcrumbs
  - 支援雙擊最後一段檔名進入 rename
  - 顯示 inline warning
- `spa/src/components/editor/TiptapEditor.tsx`
  - 調整 scroll 容器與 editable root 結構
- `spa/src/stores/useTabStore.ts`
  - 新增 editor path migration helper，將同一 `source + oldPath` 的所有 pane 一次更新到 `newPath`
- `spa/src/stores/useTabStore.test.ts`
  - 覆蓋多 tab / split pane 的 editor path migration

### 新增

- `spa/src/components/editor/TiptapEditor.test.tsx`
  - 驗證 WYSIWYG 容器 class / editable root 結構

---

## 3. 設計映射

### 3.1 Shared buffer state（key = `source + filePath`）

保留在 `buffers`：

- `content`
- `savedContent`
- `isDirty`
- `language`
- `lastStat`
- `modelId`

### 3.2 Pane-local view state（key = `pane.id`）

新增 `views`：

- `editorMode`
- `showDiff`
- `cursorPosition`
- `monacoViewState`
- `renameDraft` / `renameWarning` 不進 store，維持 `EditorPane` local state 即可

原則：view state 是 pane 自己的，不與其他同檔 pane 共用。

### 3.3 Rename migration 邊界

rename 成功後要同時更新兩層：

1. `useTabStore`：所有引用該 editor content 的 pane path 一次改成新 path
2. `useEditorStore`：shared buffer key 搬移到新 key，但保留 `modelId`

pane-local view state 維持以 `pane.id` 為 key，不搬動。

---

## 4. Test Case Matrix

### 4.1 `useEditorStore.test.ts`

| 類別 | 測試項 | 預期 |
|---|---|---|
| Shared buffer | openBuffer 會建立 shared buffer，含穩定 `modelId` | 通過 |
| Shared buffer | updateContent 只改 shared buffer，不影響 pane-local state | 通過 |
| Pane-local | 同一檔案兩個 pane 的 `editorMode` / `showDiff` 互不污染 | 通過 |
| Pane-local | `updateCursor` 僅更新指定 pane | 通過 |
| Close | 仍有 pane 引用時不 close buffer | 通過 |
| Close | 最後一個引用移除時才 close buffer | 通過 |

### 4.1b `MonacoWrapper.test.tsx`

| 類別 | 測試項 | 預期 |
|---|---|---|
| Model identity | 以 shared buffer 的 `modelId` 當作 path / identity 傳給 Monaco | 通過 |
| View state | mount 時會 restore 指定 `pane.id` 的 view state | 通過 |
| View state | unmount 或切換前會保存目前 pane 的 view state | 通過 |

### 4.2 `useTabStore.test.ts`

| 類別 | 測試項 | 預期 |
|---|---|---|
| Migration | 單 tab 單 pane editor path 會更新 | 通過 |
| Migration | split pane 中所有 matching editor path 都更新 | 通過 |
| Migration | 不同 source 或不同 path 的 editor pane 不受影響 | 通過 |

### 4.3 `EditorPane.test.tsx`

| 類別 | 測試項 | 預期 |
|---|---|---|
| State retention | tab 切換造成 unmount/remount 後，shared buffer 保留 | 通過 |
| State retention | remount 後仍使用既有 pane-local `editorMode` / `showDiff` | 通過 |
| Isolation | 同一檔案兩個 pane 同時開啟時，pane-local state 不互相污染 | 通過 |
| Toolbar | 顯示完整 breadcrumbs | 通過 |
| Rename | 雙擊檔名進入 rename mode | 通過 |
| Rename | 空字串、`.`、`..`、含 path separator 的 basename 會顯示 warning，且不送出 rename | 通過 |
| Rename | rename 成功後呼叫 backend.rename，更新所有 matching pane path，shared buffer 保留 | 通過 |
| Rename | target 已存在時顯示 warning，且不覆蓋 | 通過 |
| Rename | backend.rename 失敗時顯示 warning，且保留舊 path | 通過 |

### 4.4 `TiptapEditor.test.tsx`

| 類別 | 測試項 | 預期 |
|---|---|---|
| Layout | scroll 容器具有 `h-full min-h-0 overflow-auto` | 通過 |
| Layout | editable root 承接 typography class，不是外層容器 | 通過 |
| Integration | render 結構存在，不因 class 調整破壞 `EditorContent` 掛載 | 通過 |

---

## 5. 實作順序（TDD）

### Phase 1: Runtime state foundation

**目標**：先修 tab 切換 state 遺失的根因。

1. 寫 `useEditorStore.test.ts` failing cases：shared vs pane-local state、conditional close
2. 寫 `EditorPane.test.tsx` failing case：模擬 tab 切換 unmount/remount 後 state 保留
3. 寫 `MonacoWrapper.test.tsx` failing cases：`modelId` 與 view-state save / restore wiring
4. 實作 `useEditorStore` 新 state shape 與 API
5. 實作 `EditorPane` 改讀 pane-local state，移除 local `editorMode` / `showDiff`
6. 實作 `MonacoWrapper` 的 `modelId` + view-state save/restore
7. 跑：
   - `pnpm --prefix spa exec vitest run src/stores/useEditorStore.test.ts`
   - `pnpm --prefix spa exec vitest run src/components/editor/MonacoWrapper.test.tsx`
   - `pnpm --prefix spa exec vitest run src/components/editor/__tests__/EditorPane.test.tsx`

### Phase 2: Breadcrumbs

**目標**：先把純 UI 的完整路徑顯示獨立完成。

1. 在 `EditorPane.test.tsx` 新增 breadcrumbs failing test
2. 修改 `EditorToolbar.tsx` render 完整路徑 breadcrumbs
3. 跑：
   - `pnpm --prefix spa exec vitest run src/components/editor/__tests__/EditorPane.test.tsx`

### Phase 3: Inline rename + migration

**目標**：完成 rename UX 與跨引用 migration。

1. 在 `useTabStore.test.ts` 新增 editor path migration failing tests
2. 在 `useEditorStore.test.ts` 新增 `renameBuffer()` failing tests
3. 在 `EditorPane.test.tsx` 新增非法 basename / rename success / conflict / backend failure tests
4. 實作 `useTabStore` editor path migration helper
5. 實作 `EditorToolbar` rename UI
6. 實作 `EditorPane` rename flow：basename 驗證、preflight stat、backend.rename、error mapping
7. 實作 `useEditorStore.renameBuffer()` 與成功 rename 後的 shared buffer 搬移
8. 跑：
   - `pnpm --prefix spa exec vitest run src/stores/useEditorStore.test.ts`
   - `pnpm --prefix spa exec vitest run src/stores/useTabStore.test.ts src/stores/useTabStore.migration.test.ts`
   - `pnpm --prefix spa exec vitest run src/components/editor/__tests__/EditorPane.test.tsx`

### Phase 4: WYSIWYG container / scroll

**目標**：修正 Tiptap 容器與可捲動區域。

1. 新增 `TiptapEditor.test.tsx` failing tests
2. 調整 `TiptapEditor.tsx` 容器結構與 class 掛載位置
3. 跑：
   - `pnpm --prefix spa exec vitest run src/components/editor/TiptapEditor.test.tsx`

### Final verification

1. `pnpm --prefix spa exec vitest run src/stores/useEditorStore.test.ts src/stores/useTabStore.test.ts src/stores/useTabStore.migration.test.ts src/components/editor/MonacoWrapper.test.tsx src/components/editor/__tests__/EditorPane.test.tsx src/components/editor/TiptapEditor.test.tsx`
2. `pnpm --prefix spa run lint`
3. `pnpm --prefix spa run build`

---

## 6. 驗收條件

- [ ] keepAlive = 0 時，切換 editor tab 後再回來，shared buffer 與 pane-local runtime state 保留
- [ ] 同一檔案在不同 pane 的 view state 不互相污染
- [ ] toolbar 顯示完整 breadcrumbs
- [ ] 雙擊最後一段檔名可 rename
- [ ] rename target 已存在或 backend 拒絕時，只顯示 warning，不覆蓋目標檔
- [ ] rename 成功後，所有 matching pane 轉到新 path，且 shared buffer 不丟失
- [ ] WYSIWYG 容器不再外溢，pane 內可正常捲動
- [ ] `vitest`、`lint`、`build` 全綠

---

## 7. 風險

| 風險 | 發生可能 | 緩解 |
|---|---|---|
| `useEditorStore` state shape 變更導致既有測試或呼叫點壞掉 | 中 | 先寫 store tests，再逐步替換 `EditorPane` / `MonacoWrapper` 呼叫 |
| Monaco view-state 在 jsdom 難完整驗證 | 中 | 單元測試以 store API 與 prop wiring 為主；最後 build 前做實機 smoke |
| rename migration 若只更新 active pane，會留下 stale path | 高 | `useTabStore` 提供全域 migration helper，禁止在 component 內逐一手動 patch |
| WYSIWYG 問題若包含 Tiptap selection/undo persistence，可能超出本輪最小修復範圍 | 中 | 本輪先明確驗收「容器與捲動修正」；若 selection history 仍不穩，另開 follow-up |

---

## 8. Commit 規劃

每個 phase 一個 commit，維持可獨立 review：

1. `docs: add editor state toolbar wysiwyg spec and plan`
2. `fix(spa): preserve editor runtime state across tab switches`
3. `fix(spa): restore editor breadcrumbs in toolbar`
4. `fix(spa): add inline editor rename with conflict guard`
5. `fix(spa): fix wysiwyg editor container scrolling`
