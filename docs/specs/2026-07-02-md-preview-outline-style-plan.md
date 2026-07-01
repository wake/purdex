# Plan — Markdown Live Mode Outline 風樣式 + 內容寬度切換

**Spec**: `2026-07-02-md-preview-outline-style-spec.md`
**Branch**: `worktree-md-preview-outline-style`
**Date**: 2026-07-02

實作紀律：TDD（先寫失敗測試再實作）、每 task 獨立 commit、subagent 開發、主 session 整合/review。三 phase → 三個 PR-sized 提交群（可同 PR 分 commit）。

---

## Phase 1 — Outline 靜態 prose 樣式（CSS）

**檔案**：`spa/src/index.css`、`spa/src/components/editor/TiptapEditor.tsx`（僅去 `prose-sm`）、`TiptapEditor.test.tsx`（斷言）。

### Task 1.1 — 去 `prose-sm`（TDD）
- **測試（先）**：`TiptapEditor.test.tsx` 加一條：渲染後 editor 容器 class **不含 `prose-sm`**、仍含 `tiptap-editor prose prose-invert`。
  - 註：class 設在 `editorProps.attributes`（`TiptapEditor.tsx:62`），測試取 `[contenteditable]` 或 `.tiptap-editor` node 的 className 斷言。
- **實作**：移除 `TiptapEditor.tsx:62` class 字串中的 `prose-sm`。
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
- **測試（先）**，於 store 測試檔：
  1. 初始 `contentWidth === 'narrow'`。
  2. `setContentWidth('full')` → state 為 `'full'`。
  3. `partialize` 產出含 `contentWidth`；rehydrate（merge）後保留 persisted 值。
  4. `sanitize`/`merge` 對非法 persisted（`'wide'` / `123` / `null` / 缺欄）→ 落回 `'narrow'`。
  5. `reset()` → 回 `'narrow'`。
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
  1. `contentWidth="narrow"` → 內層 wrapper 帶限寬 class（如 `max-w-[52em] mx-auto` 或 `data-content-width="narrow"`）。
  2. `contentWidth="full"` → wrapper 無限寬（滿寬）。
  3. **切 prop `narrow`→`full` 不 remount**：rerender 前後 `[contenteditable]` DOM node 為同一 reference（editor instance 未重建）；scroll root `scrollTop` 不被歸零（設一個 scrollTop 再切 prop，斷言不變）。
- **實作**：
  - Props 加 `contentWidth?: ContentWidthOption`（default `'narrow'`）。
  - 現 `TiptapEditor.tsx:160` 內層 div 依 `contentWidth` 套 class：`narrow` → `max-w-[52em] mx-auto px-4`（`box-sizing: border-box`，padding 在 52em 內）；`full` → `max-w-none px-4`。**水平 padding 從 editorProps class（62 行）移到此 wrapper**；editor class 的 `px-4` 拿掉、`py-4` 保留（垂直）。
  - scroll 容器（151-154）與 viewState 還原/focus 邏輯**完全不動**。
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

## 相依與順序

- Phase 1、Phase 2 互相獨立，可先做任一或並行。
- Phase 3 依賴 Phase 2（store）與 Phase 1（wrapper 樣式協調）。
- 建議：subagent A 做 Phase 1、subagent B 做 Phase 2（並行）→ 主 session 整合 → Phase 3（依序 3.1→3.2→3.3，或 3.1/3.2 並行後 3.3）。

## 非目標（重申）

不改配色 / 不動 Monaco / 不做每檔寬度 / 不加 Settings 頁控制項 / 不新增 i18n key / 不動 markdown 解析儲存 handoff。
