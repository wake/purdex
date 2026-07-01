# Spec — Markdown Live Mode 閱讀樣式（Outline 風）＋ 內容寬度切換

**Date**: 2026-07-02
**Branch**: `worktree-md-preview-outline-style`
**Author**: Claude (Opus 4.8) + Wake
**狀態**: Draft（待 codex 審）

---

## 1. 背景與問題

Purdex 的 markdown「Live Mode」（Tiptap WYSIWYG，`editorMode === 'wysiwyg'`）目前**沒有任何自訂 prose 樣式**：

- `TiptapEditor.tsx:62` 的 editor 容器 class 為
  `tiptap-editor prose prose-invert prose-sm max-w-none px-4 py-4`。
- `prose-sm` 使字級偏小；`max-w-none` 讓內容**貼齊面板寬度**——面板一寬，單行字元數暴增到 100+，遠超過舒適閱讀的 50–75（CJK 更宜 ≤40 全形），閱讀體驗差。
- `spa/src/index.css` 只 `@plugin "@tailwindcss/typography"`，**無任何 `.tiptap-editor` / `.ProseMirror` / prose 覆寫**（grep 確認）。

使用者要求：markdown 預覽套用 **Outline（getoutline.com）** 的閱讀樣式，並提供**內容寬度切換**（限寬置中 ⇄ 滿寬）。

### 為何參考 Outline

Outline 的編輯器與 Purdex 同屬 **ProseMirror** 家族，樣式可近乎 1:1 移植。以下數值直接取自 Outline 原始碼（`outline/outline@main`）：

| 來源檔 | 取用內容 |
|--------|----------|
| `shared/editor/styles/EditorStyleHelper.ts` | `documentWidth = "52em"`、`padding = 32`、`blockRadius = "6px"` |
| `shared/editor/components/Styles.ts` | 字級變數、標題 margin/weight、區塊間距、複雜文字行高 |
| `shared/styles/theme.ts` | `fontFamily`、`fontWeight*` |

**權威數值表**：

- 內容欄寬 `52em`（≈832px @16px），置中。
- 字級：p `16px` / h1 `28px` / h2 `22px` / h3 `18px` / h4 `16px` / h5,h6 `15px`。
- 標題：`font-weight: 600`、**無底線**、`margin: 1em 0 0.25em`；標題緊接的 p `margin-top: 0.25em`。
- 頂層區塊間距：`margin: .5em 0`，首塊 `margin-top: 0`。
- 字體：`-apple-system, BlinkMacSystemFont, Inter, 'Segoe UI', Roboto, Oxygen, sans-serif`；等寬 `'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace`。
- block 圓角 `6px`。
- **CJK 行高洞察**：Outline 對「複雜文字系統」（泰、南亞、藏、阿拉伯、蒙古…）特別把 `p { line-height: 1.7 }`、段落 margin `0.8em`，因預設 Latin 行高對高字身文字太擠。**Outline 漏列了 CJK，但中文正屬此類**——故我們對正文採 `line-height: 1.7`（有原始碼依據，非臆測）。

---

## 2. 目標與非目標

### 目標

1. Markdown Live Mode 的渲染樣式對齊 Outline：字級階層、行高、標題（無底線、600 粗）、區塊間距、code block、清單、blockquote、表格。
2. 內容寬度可在 **`narrow`（限寬置中，預設）** 與 **`full`（滿寬，現狀 `max-w-none`）** 間切換。
3. 切換入口為 `EditorStatusBar` 右下角、Live Mode 鈕旁；**僅在 markdown Live Mode（wysiwyg）顯示**。
4. 寬度偏好為 **Purdex 全域**、持久化（一次設定，所有 md 檔一致）。

### 非目標

- **不改深色主題配色**：沿用 `prose-invert` / 既有 CSS 變數；只移植 Outline 的尺寸/間距/字重/無底線/欄寬，不套 Outline 的淺色。
- **不動 Monaco（raw mode）樣式**：寬度切換與 prose 樣式只作用於 Tiptap Live Mode。
- **不做每檔獨立寬度**（已與使用者確認採全域）。
- **不新增 Settings 頁面控制項**（狀態列 toggle 即入口；如需可列 follow-up issue）。
- **不改 markdown 解析、儲存、handoff、viewState 還原**等既有行為。
- **不引入自訂字型檔**（用系統字體堆疊）。

---

## 3. 現況程式碼盤點（探勘結果，實作免重探）

| 位置 | 現況 | 本次動作 |
|------|------|----------|
| `spa/src/index.css` | 只 `@plugin typography`，無 prose 覆寫 | **Phase 1**：加 `.tiptap-editor` 靜態樣式 |
| `TiptapEditor.tsx:62` | class 含 `prose-sm max-w-none px-4 py-4`（寫死） | **Phase 1/3**：去 `prose-sm`；寬度改由 prop 控制的內層 wrapper |
| `TiptapEditor.tsx:150-163` | 外層 scroll 容器 + `min-h-full` 內層 div 包 `<EditorContent>` | **Phase 3**：內層改為「置中欄 wrapper」，寬度依 prop |
| `useEditorSettingsStore.ts` | persist + sanitize + merge 成熟模式；doc 註「僅 Monaco 讀」 | **Phase 2**：加 `contentWidth`，更新 doc 註 |
| `EditorStatusBar.tsx` | 右下角有 Live Mode / language 下拉 | **Phase 3**：加寬度 toggle |
| `EditorPane.tsx:484-492` | 渲染 `<TiptapEditor>`；status bar 在 496 | **Phase 3**：讀 store 寬度傳入 + wire toggle |

---

## 4. 設計

### 4.1 樣式套用策略（靜態 vs 動態分離）

- **靜態（Phase 1）**：字級、行高、標題、間距、code、清單、blockquote、表格——這些不隨狀態改變，寫進 `index.css` 針對 `.tiptap-editor`（ProseMirror 容器已帶此 class）。用 `.tiptap-editor` 前綴確保只作用於本編輯器，不污染其他 prose 使用處。
- **動態（Phase 3）**：只有「內容寬度」隨偏好改變。**不**在 `useEditor` 初始化的 class 上改（那需 `setOptions` 重設、易與 viewState 打架），改在 `<EditorContent>` 外層包一個**置中欄 wrapper**，其 class 依 `contentWidth` prop：
  - `narrow` → `max-width: 52em; margin-inline: auto;`（＋水平 padding）
  - `full` → 無 max-width（等同現狀）。
  - ProseMirror 節點填滿 wrapper；水平 padding 移到 wrapper，垂直 padding 保留。

> 為何不用 `prose` 內建 `max-w-*`：Tailwind prose 的 `max-w-none` 目前寫死在 editor class；用獨立 wrapper 控制寬度可保 `useEditor` 設定不變（避免 remount / viewState 風險），且 `narrow/full` 切換是純 class 替換、可反應式。

### 4.2 寬度偏好狀態

擴充 `useEditorSettingsStore`（既有 persist/sanitize/merge 模式）：

```ts
export type ContentWidthOption = 'narrow' | 'full'

interface EditorSettingsState {
  // ...existing
  contentWidth: ContentWidthOption      // default 'narrow'
  setContentWidth: (v: ContentWidthOption) => void
}
```

- `DEFAULT_EDITOR_SETTINGS.contentWidth = 'narrow'`。
- `sanitize`：加 `isContentWidth` guard，非法值 → 落回 default。
- `partialize` / `merge` 一併納入。
- 更新檔頭 doc 註解：此 store 現同時服務 Monaco 與 **Tiptap 的 `contentWidth`**（原註「Only MonacoWrapper reads」需修正）。
- **不註冊 syncManager**（與既有 editor 偏好一致，device-local）。

### 4.3 Toggle UI（EditorStatusBar）

- 新增 optional props：`contentWidth?: ContentWidthOption`、`onContentWidthChange?: (v) => void`。
- **僅當 `editorMode === 'wysiwyg'` 且 `onContentWidthChange` 存在**時渲染（raw mode 不顯示——寬度只對 Live Mode 有意義）。
- 位置：Live Mode 鈕**左側**（同一 `ml-auto` 群組內）。
- 型式：與現有 language/mode 鈕一致的小按鈕，點擊在 `narrow ⇄ full` 間切換（兩態直接 toggle，不必下拉選單）。
- 標籤/圖示：用 Phosphor icon 表達寬窄（如 `ArrowsInLineHorizontal` / `ArrowsOutLineHorizontal`），附 `title`。i18n 走既有 `useI18nStore`（若狀態列其他文字已 i18n；否則沿用現有硬字串風格保持一致）。

### 4.4 EditorPane 接線

- 讀 `const contentWidth = useEditorSettingsStore((s) => s.contentWidth)`。
- 傳給 `<TiptapEditor contentWidth={contentWidth} />`。
- `<EditorStatusBar>` 加 `contentWidth={contentWidth}`、`onContentWidthChange={(v) => useEditorSettingsStore.getState().setContentWidth(v)}`。

### 4.5 TiptapEditor 接線

- 新增 prop `contentWidth: ContentWidthOption`（或帶 default `'narrow'` 以相容測試）。
- 內層 wrapper（現 `TiptapEditor.tsx:160` 的 div）依 `contentWidth` 套寬度 class。
- 保留既有 scroll 容器、viewState 還原、focus 邏輯**完全不動**。

---

## 5. Phase 切分（依 review 大小）

### Phase 1 — Outline 靜態 prose 樣式（CSS-only）

- **範圍**：`spa/src/index.css` 加 `.tiptap-editor` 樣式塊；移除 editor class 的 `prose-sm`（改由 CSS 明確定字級）。
- **交付**：字級階層（28/22/18/16/15/16）、`line-height: 1.7`、標題 600＋無底線＋margin、區塊間距、code（mono＋`6px`＋淡底）、行內 code、清單、blockquote、表格；全部深色。
- **驗證**：`pnpm run build` + lint 綠；手動截圖對照 Outline 參考。純 CSS 難 TDD——測試僅斷言 editor class **不再含 `prose-sm`**（TiptapEditor 既有測試延伸）。
- **Review 大小**：小（單檔 CSS + 一處 class 字串）。

### Phase 2 — `contentWidth` 全域偏好（store，強 TDD）

- **範圍**：`useEditorSettingsStore.ts` 加 `contentWidth` 全套（type、default、setter、sanitize guard、partialize、merge、doc 註）。
- **驗證（TDD 先行）**：
  - default 為 `'narrow'`。
  - `setContentWidth('full')` 生效。
  - persist round-trip（rehydrate 保留）。
  - `sanitize` 對非法值（`'wide'`、數字、null）落回 default。
  - `reset` 回 default。
- **Review 大小**：小（單 store + 測試）。

### Phase 3 — 寬度套用 + 狀態列 toggle（元件 TDD）

- **範圍**：`TiptapEditor.tsx`（prop + wrapper class）、`EditorStatusBar.tsx`（toggle）、`EditorPane.tsx`（接線）。
- **驗證（TDD）**：
  - `EditorStatusBar`：wysiwyg 時渲染寬度 toggle；raw 時不渲染；點擊呼叫 `onContentWidthChange` 傳另一態；顯示反映當前 `contentWidth`。
  - `EditorPane`：從 store 讀 `contentWidth` 並傳入 TiptapEditor 與 StatusBar（可用既有 EditorPane 測試延伸 / mock store）。
  - `TiptapEditor`：`narrow` 套限寬 class、`full` 不套（斷言 wrapper class）。
- **Review 大小**：中（3 檔，含 UI）。

**Phase 相依**：1 獨立可先；2 獨立；3 依賴 2（需 store）。可 1→2→3 順序，或 1、2 並行後 3。

---

## 6. 驗收準則（整體）

1. Markdown Live Mode 預設呈現：限寬置中 `52em`、行高 1.7、標題無底線、字級階層如上，深色。
2. 狀態列（Live Mode 時）出現寬度 toggle；切 `full` 立即滿寬、切 `narrow` 立即限寬置中。
3. 偏好持久化：重載後維持上次選擇；預設 `narrow`。
4. Raw（Monaco）模式不受影響、無寬度 toggle。
5. 非 markdown 檔不顯示寬度 toggle。
6. `cd spa && npx vitest run` 全綠、`pnpm run lint` 綠、`pnpm run build` 綠。
7. 既有 viewState 還原 / focus / handoff 行為不回歸。

---

## 7. 風險與緩解

| 風險 | 緩解 |
|------|------|
| `.tiptap-editor` 樣式外溢到其他 prose 使用處 | 一律以 `.tiptap-editor` 前綴限定；grep 確認無其他元件複用此 class |
| 去 `prose-sm` 後 Tailwind prose 預設值回大，與自訂衝突 | CSS 明確覆寫關鍵屬性；build 後截圖驗證 |
| wrapper 改結構影響 scroll/viewState 還原 | 只在既有內層 div 加寬度 class，不動 scroll 容器與還原邏輯；Phase 3 測試守住 |
| store doc 註「僅 Monaco」失真 | Phase 2 一併修正註解，避免誤導後續開發 |
| CJK 行高 1.7 對純英文檔略鬆 | 可接受；Outline 亦對多語系統一放鬆；如反彈再 follow-up 分語系 |

---

## 8. 未決 / 待 codex 檢視

1. Toggle 用「兩態直接切」還是「下拉（跟 Live Mode 一致）」？本 spec 採兩態直切（更快）；請 codex 評 UX 一致性。
2. 寬度只作用 Live Mode，raw/diff 不顯示 toggle——是否有使用者會期待 raw 也限寬？（本 spec 判定否。）
3. `52em` 是否直接沿用，或因 Purdex 深色 + 中文再微調？（本 spec 先沿用權威值，實測後可調。）
