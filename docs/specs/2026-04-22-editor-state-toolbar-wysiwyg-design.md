# Editor State / Toolbar / WYSIWYG 修正 Spec

> 日期：2026-04-22
> 狀態：Draft v2（post spec-review）
> 範圍：`spa/src/components/editor/*`、`spa/src/stores/useEditorStore.ts`、editor 相關測試
> 參考：
> - 現行 editor 設計：`docs/superpowers/specs/2026-04-14-editor-module-design.md`
> - 相關實作：`spa/src/components/editor/EditorPane.tsx`、`EditorToolbar.tsx`、`MonacoWrapper.tsx`、`TiptapEditor.tsx`
> - Codex spec 審查：thread `019db160-4793-7310-bc6a-352bfc97b969`（2026-04-22）

---

## 1. 概述

這次修正聚焦 Editor module 的 4 個已知使用性缺陷：

1. 切換 tab 後回來，editor state 會遺失
2. top bar 只顯示檔名，沒有完整路徑 breadcrumbs
3. 無法直接在 top bar 雙擊檔名重新命名
4. Markdown WYSIWYG 模式的容器與捲動行為錯亂

本 spec 只處理 SPA editor runtime 與 UI，不改 daemon / Electron API contract；rename 仍沿用既有 `FsBackend.rename()`。

---

## 2. 問題定義

### 2.1 現況根因

根據目前程式碼檢查：

1. `EditorPane.tsx` 在 component unmount 時直接 `closeBuffer(key)`，而 tab keep-alive 預設是 `0`，所以切到別的 tab 時 editor pane 會被 unmount，buffer 跟著被刪掉。
2. `EditorPane.tsx` 的 `editorMode` / `showDiff` 是 local state，pane remount 後會回到初始值。
3. `EditorToolbar.tsx` 目前只 render basename，breadcrumbs UI 已不存在。
4. `MonacoWrapper.tsx` 沒有提供穩定 model identity；即使 buffer 內容保住，Monaco 的 view state / undo stack 也無法可靠保留。
5. `TiptapEditor.tsx` 把 `prose` 樣式掛在外層 scroll container，缺少明確的 `min-h-0` / `h-full` / editable root 佈局，導致編輯區外框與捲動異常。

### 2.2 使用者可見症狀

- 切 tab 回來後，游標位置、scroll、undo/redo 狀態消失
- Markdown tab 切回來後，raw/WYSIWYG 模式與 diff 開關重置
- top bar 無法辨識同名檔案所在目錄
- top bar 不能直接 rename 檔案
- WYSIWYG 模式會出現不合理的外框，且捲動不跟內容區正常配合

---

## 3. 目標 / 非目標

### 3.1 目標

1. editor tab 在一般 tab 切換後保留 runtime state
2. top bar 顯示完整路徑 breadcrumbs，而不是只有 basename
3. top bar 最後一段檔名可雙擊進入 rename 模式
4. rename 前先做同目錄同名衝突檢查，已存在則顯示警告，不覆蓋
5. 修正 WYSIWYG 模式的容器邊界與捲動行為
6. 以測試先行方式覆蓋上述行為

### 3.2 非目標

1. 不做跨資料夾 move UI；rename 仍限定在原資料夾改 basename
2. 不新增全域通知系統；rename 警告只需在 editor toolbar inline 顯示
3. 不重做 editor module 架構；以最小修改修正現有行為
4. 不處理 Markdown serializer / schema 額外功能

---

## 4. 設計決策

### 4.1 Shared buffer 與 pane view state 分離

這次修正要明確分成兩層狀態：

1. **shared buffer state**：同一個 `source + filePath` 共用
   - `content`
   - `savedContent`
   - `isDirty`
   - `language`
   - `lastStat`
   - 穩定 `modelId`
2. **pane-local view state**：同一檔案在不同 split / tab / detached pane 之間不可互相污染
   - `editorMode`
   - `showDiff`
   - cursor / selection / scroll / view state

`pane-local view state` 的 key 使用 `pane.id`，而不是 `source + filePath`。理由是系統已支援 split / detach，同一檔案可以同時出現在多個 pane；若直接把 view state 綁檔案 key，任一 pane 的 diff / scroll / cursor 會覆寫其他 pane。

### 4.2 Buffer lifetime 與 tab 切換解耦

`useEditorStore` 的 buffer 不再因 pane unmount 就直接刪除。清理條件改為：當前 worktree 內已沒有任何 pane 引用同一個 editor content（`source + filePath`）時，才允許關閉 buffer。

這樣可讓 keep-alive = 0 的情況下，tab 切換仍保留：

- buffer content / dirty 狀態
- pane-local cursor / scroll / mode / diff

### 4.3 editor UI state 進 store

`editorMode` 與 `showDiff` 從 `EditorPane` local state 提升到 `useEditorStore`，但必須以 `pane.id` 管理。原因是這兩者屬於 view state，不是檔案內容本身。

### 4.4 Monaco 使用穩定 model identity

`MonacoWrapper` 需要區分 model 與 view state：

1. **model identity**：綁 shared buffer 的穩定 `modelId`，不是直接綁 `filePath`
2. **view state**：以 `pane.id` 儲存 / 還原 Monaco view state

這樣 tab 切換後仍能恢復：

- selection / cursor
- scroll position
- undo/redo stack

rename 時 shared buffer 的 key 會變，但 `modelId` 必須維持穩定，避免 URI 切換直接丟失 undo stack。view state 則繼續綁各自的 `pane.id`。

### 4.5 Toolbar 改為 breadcrumbs + inline rename

Toolbar 左側改成：

- 完整 path breadcrumbs
- 最後一段檔名為主要焦點
- 雙擊最後一段進入 rename input

rename 流程：

1. 使用者雙擊檔名
2. 只允許編輯 basename，不允許改父路徑
3. basename 經過 trim 後若為空、`.`、`..`、含 path separator，視為非法名稱，顯示 inline warning
4. 若新名稱與原名稱相同，結束 edit mode，不做 rename
5. 可先做目標 path 存在性檢查，提早提供 UX warning；但這不是最終保證
6. `backend.rename(from, to)` 的成功 / 失敗才是最終權威，不能只依賴前置檢查
7. 若 rename 失敗（包含衝突、權限、case-only rename 正規化差異、backend 拒絕），將 backend error 映射為 inline warning
8. 成功後要做**跨所有引用**的 migration：
   - 更新所有引用同一 `source + oldFilePath` 的 pane content
   - 搬移 shared buffer key（保留 `modelId`）
   - 保留既有 pane-local view state

rename 不得只更新單一 pane，否則 split / duplicate view 會殘留舊路徑。

### 4.6 WYSIWYG 佈局修正

`TiptapEditor` 改為明確的兩層結構：

- 外層：`h-full min-h-0 overflow-auto`
- 內層 editable root：承接 `prose` 樣式、padding、focus outline 控制、最小高度

重點是把 typography 樣式掛到 editable root，而不是 scroll 容器本身，避免容器尺寸與內容尺寸彼此污染。

另外，WYSIWYG 也有自己的 pane-local runtime state 問題。這次至少要保證：

- tab 切換回來後，不會因 remount 直接回到 raw 模式
- WYSIWYG 模式本身仍可繼續編輯與捲動

Tiptap 的 selection / undo history 若無法在本輪以低風險方式完整保留，需在 plan 與驗收條件中明講，避免規格承諾過頭。

---

## 5. Phase / PR 切分

| Phase | 範圍 | 尺寸 | 依賴 |
|---|---|---|---|
| Phase 1 | shared buffer lifetime、pane-local UI state、Monaco model/view-state foundation、相關測試 | 中 | 無 |
| Phase 2 | toolbar breadcrumbs | 小 | Phase 1 |
| Phase 3 | inline rename、衝突 / 非法名稱 warning、跨引用 migration、相關測試 | 中 | Phase 1 |
| Phase 4 | Tiptap/WYSIWYG container 與 scroll 修正、相關測試 | 小 | Phase 1 |

說明：breadcrumbs 與 rename 拆開，避免單一 PR 同時混入純 UI 與 state migration 邏輯。

---

## 6. 測試策略

### 6.1 `EditorPane.test.tsx`

至少新增以下案例：

1. tab 切換造成 unmount/remount 後，buffer 與 pane-local UI state 不丟失
2. 同一檔案在兩個 pane 同時開啟時，mode / diff / cursor / scroll 不互相污染
3. 切回 tab 後仍保留 cursor / mode / diff state
4. toolbar 顯示完整 breadcrumbs
5. rename 成功後會更新所有相關 pane path，並保留 shared buffer
6. rename 目標已存在或 backend 拒絕時顯示 warning，且不覆蓋目標檔

### 6.2 `useEditorStore.test.ts`

至少新增：

1. shared buffer 與 pane-local state 的讀寫邊界
2. buffer rename / key migration
3. conditional close 行為
4. 多 pane 引用同一檔案時的 migration 正確性

### 6.3 WYSIWYG 測試

若 jsdom 無法可靠驗證真實 scroll 行為，至少測：

1. `TiptapEditor` render 出正確的容器 class / 結構
2. editable root 帶有預期 class
3. 不再把 `prose` 直接掛在 scroll 容器
4. tab remount 後仍停留在 WYSIWYG 模式

---

## 7. 驗收條件

- [ ] keepAlive = 0 時，切換 editor tab 再切回，不會丟失 shared buffer 與 pane-local runtime state
- [ ] 同一檔案在不同 pane 的 mode / diff / cursor / scroll 互不污染
- [ ] top bar 顯示完整路徑 breadcrumbs
- [ ] 雙擊檔名可 rename，撞名時只警告不覆蓋
- [ ] WYSIWYG 模式容器不再外溢，且可在 pane 內正常捲動
- [ ] `pnpm --prefix spa exec vitest run` 相關測試綠
- [ ] `pnpm --prefix spa run lint` 綠
- [ ] `pnpm --prefix spa run build` 綠

---

## 8. 風險

| 風險 | 發生可能 | 緩解 |
|---|---|---|
| buffer 改成延後清理後，可能殘留無引用 editor state | 中 | close 時掃描 `useTabStore` 實際引用；測試覆蓋「仍有 tab 引用」與「最後一個引用移除」 |
| 把 pane-local state 錯綁到 shared buffer，導致 split 視圖互相污染 | 高 | `pane.id` 與 buffer key 分層；加入同檔多 pane 測試 |
| Monaco model 保留若 key 設計錯誤，可能造成不同檔案共用 state | 中 | model identity 必須綁定 shared buffer 的穩定 `modelId`；加入不同 filePath 不共享的測試 |
| rename 後 buffer / pane path 不同步，造成 save 寫回舊路徑 | 高 | rename 成功後以單一 migration 更新所有引用 pane 與 shared buffer key；測試覆蓋多 pane 引用 |
| jsdom 難完整重現 Tiptap scroll 問題 | 高 | 自動化測試只驗證 DOM 結構與 class，最終以手動 smoke 驗證補足 |
