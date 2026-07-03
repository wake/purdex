# Plan — Markdown Live Mode Outline 風樣式 + 內容寬度切換

**Spec**: `2026-07-02-md-preview-outline-style-spec.md`
**Branch**: `worktree-md-preview-outline-style`
**Date**: 2026-07-02

實作紀律：TDD（先寫失敗測試再實作）、每 task 獨立 commit、subagent 開發、主 session 整合/review。三 phase → 三個 PR-sized 提交群（可同 PR 分 commit）。

---

## Phase 1 — Outline 靜態 prose 樣式（CSS）

**檔案**：`spa/src/index.css`、`spa/src/components/editor/TiptapEditor.tsx`（僅去 `prose-sm`）、`TiptapEditor.test.tsx`（斷言）。

### Task 1.1 — 去 `prose-sm`（TDD）
- **測試（先）**，`TiptapEditor.test.tsx`：
  - **必須更新既有測試**：現有 `applies typography classes to the editable root`（約 :62-67）斷言 `data-editor-class` 含 `'prose prose-invert prose-sm max-w-none'`——去 `prose-sm` 後**這條會紅**，改斷言 `'prose prose-invert max-w-none'`（去掉 prose-sm 後仍為連續子字串）＋顯式 `expect(...).not.stringContaining('prose-sm')`。
  - **加便宜 smoke test（回應 codex）**：editable root 的 `data-editor-class` 仍含 `tiptap-editor`——整個 `.tiptap-editor` CSS scope 建立在此 class 上，class 名被改則 CI 現在抓不到。
- **實作**：移除 `TiptapEditor.tsx:62` class 字串中的 `prose-sm`（其餘保留，含 `max-w-none`；`max-w-none` 由 Task 3.1 才移到 wrapper）。
- **commit**：`refactor(editor): drop prose-sm from Tiptap; explicit CSS owns type scale`

### Task 1.2 — Outline `.tiptap-editor` 樣式塊（CSS，無單元測試）
- **實作**：`index.css` 加樣式塊，全部以 `.tiptap-editor` 前綴限定（scope 已驗證：只有 TiptapEditor 用此 class；MessageBubble 用泛用 prose 不受影響）。依 spec §5 Phase 1 selector 表：
  - `.tiptap-editor { line-height: 1.7; }`
  - `.tiptap-editor :is(h1,h2,h3,h4,h5,h6) { font-weight: 600; margin: 1em 0 0.25em; border-bottom: 0; }`
  - h1 28 / h2 22 / h3 18 / h4 16 / h5,h6 15（px 或 rem 對應）
  - `.tiptap-editor :is(h1,h2,h3,h4,h5,h6) + p { margin-top: 0.25em; }`
  - `.tiptap-editor p { font-size: 16px; }`；頂層區塊 `> * { margin: .5em 0 }`、`> :first-child { margin-top: 0 }`
  - `.tiptap-editor :is(ul,ol)` 縮排 + `li` 間距
  - `.tiptap-editor pre { border-radius: 6px; overflow-x: auto; }` + mono + 淡底
  - `.tiptap-editor :not(pre) > code`（inline）mono、`font-size: 90%`、淡底、小圓角
  - `.tiptap-editor blockquote` 左邊框 + 灰字 + `padding-inline`
  - `.tiptap-editor table` cell padding/border/隔行底；外層（`.tableWrapper` 或 `table` 容器）`overflow-x: auto`
  - **顏色沿用深色變數**（不覆寫 prose-invert 的色）。
  - **不含 padding / max-width**（留 Phase 3）。
- **驗證**：`pnpm run build` + `pnpm run lint` 綠；手動截圖 7 樣本（spec §5 Phase 1）對照 Outline。
- **commit**：`feat(editor): Outline-style markdown typography for Live Mode`

**Phase 1 完成準則**：AC1（樣式，除寬度置中留 Phase 3）、AC10 部分（build/lint/vitest 綠）。

---

## Phase 2 — `contentWidth` 全域偏好（store，強 TDD）

**檔案**：`spa/src/stores/useEditorSettingsStore.ts`、`useEditorSettingsStore.test.ts`（既有或新建）。

### Task 2.1 — store 欄位 + setter + sanitize + persist（TDD）
- **測試（先）**，`useEditorSettingsStore.test.ts`，**併入既有 rehydrate 信任邊界測法（回應 codex；真正該守的是 `persist.rehydrate()` 行為，非 helper-level partialize）**：
  1. 初始 `contentWidth === 'narrow'`。
  2. `setContentWidth('full')` → state 為 `'full'`。
  3. `setContentWidth('full')` 後，`localStorage[EDITOR_SETTINGS]` envelope 的 `state.contentWidth === 'full'`（比照 S1-6）。
  4. **rehydrate happy-path（比照 S1-9）**：直接寫 envelope `state.contentWidth='full'` → `await persist.rehydrate()` → state 為 `'full'`。
  5. **rehydrate 非法值（比照 S1-7）**：envelope `state.contentWidth='wide'`（或 `123`/`null`/缺欄）→ rehydrate 後落回 `'narrow'`。
  6. `reset()` → 回 `'narrow'`。
- **實作**（對照既有 `fontSize` 等欄位模式）：
  - `export type ContentWidthOption = 'narrow' | 'full'`
  - interface 加 `contentWidth: ContentWidthOption` + `setContentWidth`
  - `DEFAULT_EDITOR_SETTINGS.contentWidth = 'narrow'`
  - `isContentWidth` guard；`sanitize` 加一行；`partialize` 加 `contentWidth`
  - setter：`setContentWidth: (contentWidth) => set({ contentWidth })`
  - **改寫檔頭 doc 註解（5-16）** 為 global editor preferences（Monaco + Tiptap）。
- **commit**：`feat(editor-settings): add global contentWidth preference (narrow/full)`

**Phase 2 完成準則**：AC3（持久化 + 預設 narrow）。

---

## Phase 3 — 寬度套用 + 狀態列 toggle（元件 TDD）

**檔案**：`TiptapEditor.tsx`、`EditorStatusBar.tsx`、`EditorPane.tsx` + 各測試。

### Task 3.1 — TiptapEditor 寬度 wrapper（TDD）
- **測試（先）**，`TiptapEditor.test.tsx`：
  1. `contentWidth="narrow"` → 內層 wrapper 帶限寬 class（斷言確切字串 `max-w-[52em] mx-auto box-border px-4`，或 `data-content-width="narrow"`）。
  2. `contentWidth="full"` → wrapper 為滿寬（`max-w-none`，無 52em）。
  3. **切 prop `narrow`↔`full` 不重跑 restore/focus/viewState、scrollTop 不歸零（回應 codex Blocker 1，改用行為斷言為主證據）**：既有 harness 已把 `@tiptap/react` 全 mock、且已有 `resolveRestoreSelection`（mock spy）、`editor.view.dispatch`（vi.fn）、`focusSpy`。作法——render `narrow`（觸發一次 restore/focus）記下各 spy call count 與 scroll root `scrollTop`（先設一非零值），`rerender` 成 `full`：
     - `resolveRestoreSelection` **call count 不變**（restore 不重跑）
     - `editor.view.dispatch` **不再被呼叫**（selection 不重設）
     - `focusSpy` **不再被呼叫**（focus 不重打）
     - `onViewStateChange` **在 rerender 過程未被觸發**（未誤判為 unmount 寫回）
     - `tiptap-scroll-root.scrollTop` **維持原值**（不歸零）
     - DOM node reference 相同僅作**輔助**佐證，**不**當「editor instance 未重建」的主要證據（mock 下 instance identity 由 mock 決定，不反映真實生命週期）。
- **實作**：
  - Props 加 `contentWidth?: ContentWidthOption`（default `'narrow'`）。
  - 現 `TiptapEditor.tsx:160` 內層 div 依 `contentWidth` 套 class：
    - `narrow` → **`max-w-[52em] mx-auto box-border px-4`**（`box-border` = `box-sizing: border-box`，**使 padding 計入 52em 之內**，落實 spec §4.1；回應 codex Blocker 2——無 `box-border` 則 padding 加在 52em 外、量測偏掉）
    - `full` → `max-w-none px-4`
  - **水平 padding 從 editorProps class（`:62` 的 `px-4`）移到此 wrapper**；editor root class 去 `px-4`、**保留 `max-w-none`**（讓 prose 不自我限縮到 65ch，改由 wrapper 統一控寬）、`py-4` 保留（垂直）。
  - scroll 容器（151-154）與 viewState 還原/focus 邏輯**完全不動**。
- **已知風險（明寫，回應 codex）**：padding 換位置不影響 scroll root 的 scrollTop 讀寫（仍是同一 container），但 `narrow` 文字 measure 變窄後 reflow，**同一 scrollTop 數值可能對應不同視覺位置**——本 task 只保證「scrollTop 不歸零」，**不保證視覺 anchor 完全不位移**（spec §7 已列為接受風險）。
- **commit**：`feat(editor): apply content-width wrapper in TiptapEditor`

### Task 3.2 — EditorStatusBar 寬度 toggle（TDD）
- **測試（先）**，`EditorStatusBar.test.tsx`：
  1. `editorMode="wysiwyg"` + `onContentWidthChange` → 渲染寬度 toggle 按鈕（by title/role）。
  2. `editorMode="raw"` → 不渲染 toggle。
  3. 無 `onContentWidthChange` → 不渲染。
  4. 點擊：`contentWidth="narrow"` 時呼叫 `onContentWidthChange('full')`；反之。
  5. icon/title 反映當前值（narrow 顯示 `Narrow width` title、full 顯示 `Full width`）。
- **實作**：
  - Props 加 `contentWidth?: ContentWidthOption`、`onContentWidthChange?: (v) => void`。
  - 在 `ml-auto` 群組、Live Mode 鈕左側加按鈕；顯示條件 `editorMode === 'wysiwyg' && onContentWidthChange`（**唯一條件，不加 isMarkdown 二判**，spec Blocker 定案）。
  - Phosphor `ArrowsInLineHorizontal`(narrow) / `ArrowsOutLineHorizontal`(full) + `title`（硬字串，不 i18n）。
- **commit**：`feat(editor): add content-width toggle to status bar`

### Task 3.3 — EditorPane 接線（TDD）
- **測試（先）**，`EditorPane.test.tsx`（延伸既有）：
  1. store `contentWidth` 值傳入 `<TiptapEditor>`（wysiwyg 路徑）。
  2. `<EditorStatusBar>` 收到 `contentWidth` + `onContentWidthChange`。
  3. **raw ↔ live 往返**：切回 raw 再回 wysiwyg，傳入的 `contentWidth` 仍為 store 值（AC8）。
- **實作**：
  - `const contentWidth = useEditorSettingsStore((s) => s.contentWidth)`。
  - `<TiptapEditor ... contentWidth={contentWidth} />`（`EditorPane.tsx:484`）。
  - `<EditorStatusBar ... contentWidth={contentWidth} onContentWidthChange={(v) => useEditorSettingsStore.getState().setContentWidth(v)} />`（496）。
- **commit**：`feat(editor): wire content-width preference through EditorPane`

**Phase 3 完成準則**：AC2、AC4-AC9、AC11。

---

## 整體驗證（PR 前）

- `cd spa && npx vitest run` 全綠。
- `cd spa && pnpm run lint` 綠。
- `cd spa && pnpm run build` 綠。
- 手動（SPA HMR，純前端）：
  - md 檔開 Live Mode → 預設 narrow 置中、Outline 樣式。
  - toggle 切 full/narrow 即時生效、不跳頂、不失 selection。
  - 寬表 / 長 code 橫捲可讀。
  - reload 後維持選擇。
  - raw/diff 無 toggle。

## 相依與順序（回應 codex：區分硬依賴 vs 視覺整合依賴）

- Phase 1、Phase 2 互相獨立，可並行。
- **硬依賴**：Phase 3 → Phase 2（`contentWidth` store 需先存在，`3.2`/`3.3` 讀它）。
- **僅視覺整合依賴**（非技術依賴）：Phase 3 → Phase 1（Task `3.1` 的 wrapper 只要測試不把 typography class 綁死，其實 Phase 2 完成即可先做，不必等 Phase 1）。
- 建議：subagent A 做 Phase 1、subagent B 做 Phase 2（並行）→ 主 session 整合 → Phase 3（`3.1`/`3.2` 可並行、`3.3` 最後接線）。

## 非目標（重申）

不改配色 / 不動 Monaco / 不做每檔寬度 / 不加 Settings 頁控制項 / 不新增 i18n key / 不動 markdown 解析儲存 handoff。
