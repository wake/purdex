# Theme System Design Spec

**日期**: 2026-03-25
**狀態**: Draft
**範圍**: SPA Theme 系統 — 多主題 + 自訂主題 + 匯入匯出

---

## 1. 目標

將 SPA 現有的硬編碼色彩抽取為語義化 CSS token，支援多主題切換與自訂主題。

### 不在範圍內

- Terminal ANSI 16 色 palette（歸 Host 管理，per-daemon 設定）
- System `prefers-color-scheme` 自動跟隨
- 過渡動畫（之後再考慮）

## 2. 技術方案

**Tailwind v4 `@theme` + CSS Variables + Registry 模式**

- CSS Variables（`--surface-primary` 等）定義色彩值
- Tailwind `@theme` 將 CSS variables 映射為 utility class（`bg-surface-primary`）
- `[data-theme="xxx"]` 區塊切換不同主題的 variable 值
- 自訂主題 runtime 注入 `<style>` 元素
- Theme Registry + 獨立 Theme Store 管理主題資料

### 為什麼選這個方案

- 遷移路徑自然：`bg-[#0a0a1a]` → `bg-surface-primary`，都是 Tailwind class
- 切換主題只改 `data-theme` attribute，零 React re-render
- xterm.js 可用 `getComputedStyle` 讀取 CSS variable
- 延續專案既有的 Registry 模式（pane-registry、new-tab-registry、settings-section-registry）

## 3. Token 結構

23 個語義 token，分 6 組。元件特化 token 在需要時才加，不預先定義。

### Surface（背景層級）

| Token | 用途 | Dark 參考值 |
|-------|------|------------|
| `--surface-primary` | 主背景（App、Terminal） | `#0a0a1a` |
| `--surface-secondary` | TabBar、StatusBar | `#12122a` |
| `--surface-tertiary` | ActivityBar | `#08081a` |
| `--surface-elevated` | Dialog、Dropdown、Picker | `#1e1e3e` |
| `--surface-hover` | Hover 狀態 | `#1a1a32` |
| `--surface-active` | 選中/活躍背景 | `#272444` |
| `--surface-input` | 輸入框背景 | `#2a2a2a` |

### Text

| Token | 用途 | Dark 參考值 |
|-------|------|------------|
| `--text-primary` | 主文字 | `#e0e0e0` |
| `--text-secondary` | 次要文字 | `#9ca3af`（gray-400） |
| `--text-muted` | 淡化文字 | `#6b7280`（gray-500） |
| `--text-inverse` | 反色文字（accent bg 上） | `#0a0a1a` |

### Border

| Token | 用途 | Dark 參考值 |
|-------|------|------------|
| `--border-default` | 預設邊框 | `#404040` |
| `--border-active` | 活躍/焦點邊框 | `#7a6aaa` |
| `--border-subtle` | 淡邊框 | `#2a2a2a` |

### Accent

| Token | 用途 | Dark 參考值 |
|-------|------|------------|
| `--accent` | 主互動色 | `#7a6aaa` |
| `--accent-hover` | Accent hover 狀態 | `#8a7aba` |
| `--accent-muted` | Accent 淡化（badge、indicator） | `rgba(122,106,170,0.2)` |

### Terminal（基本）

| Token | 用途 | Dark 參考值 |
|-------|------|------------|
| `--terminal-bg` | 終端背景 | `#0a0a1a` |
| `--terminal-fg` | 終端前景文字 | `#e0e0e0` |
| `--terminal-cursor` | 游標色 | `#e0e0e0` |

### Status（語義色）

| Token | 用途 | Dark 參考值 |
|-------|------|------------|
| `--status-error` | 錯誤背景 | `#4a3038` |
| `--status-warning` | 警告背景 | `#4a4028` |
| `--status-success` | 成功背景 | `#2a4a3a` |

## 4. CSS 架構

### 檔案結構

```
spa/src/
  styles/
    themes.css          ← CSS variable 定義（所有預設主題）
  index.css             ← 加 @import "./styles/themes.css"
```

### themes.css 結構

```css
/* === Tailwind token 映射 === */
@theme {
  --color-surface-primary: var(--surface-primary);
  --color-surface-secondary: var(--surface-secondary);
  --color-surface-tertiary: var(--surface-tertiary);
  --color-surface-elevated: var(--surface-elevated);
  --color-surface-hover: var(--surface-hover);
  --color-surface-active: var(--surface-active);
  --color-surface-input: var(--surface-input);
  --color-text-primary: var(--text-primary);
  --color-text-secondary: var(--text-secondary);
  --color-text-muted: var(--text-muted);
  --color-text-inverse: var(--text-inverse);
  --color-border-default: var(--border-default);
  --color-border-active: var(--border-active);
  --color-border-subtle: var(--border-subtle);
  --color-accent: var(--accent);
  --color-accent-hover: var(--accent-hover);
  --color-accent-muted: var(--accent-muted);
  --color-terminal-bg: var(--terminal-bg);
  --color-terminal-fg: var(--terminal-fg);
  --color-terminal-cursor: var(--terminal-cursor);
  --color-status-error: var(--status-error);
  --color-status-warning: var(--status-warning);
  --color-status-success: var(--status-success);
}

/* === Dark（預設） === */
[data-theme="dark"] {
  --surface-primary: #0a0a1a;
  /* ... 全部 token */
}

/* === Light === */
[data-theme="light"] { /* ... */ }

/* === Nord === */
[data-theme="nord"] { /* ... */ }

/* === Dracula === */
[data-theme="dracula"] { /* ... */ }
```

### 主題套用

`data-theme` 設定在 `document.documentElement`（`<html>` 元素），確保 CSS variables 從根元素向下繼承，`body` 背景、xterm `getComputedStyle(document.documentElement)` 都能正確讀取。

```tsx
// useThemeStore 的 side effect
document.documentElement.dataset.theme = activeThemeId;
```

`index.css` 中 `body { background-color: #191919 }` 改為 `body { background-color: var(--surface-primary) }`，確保 React hydrate 前和未覆蓋區域也使用主題色。

### 元件遷移

```tsx
// 前
<div className="bg-[#0a0a1a] text-[#e0e0e0]">
// 後
<div className="bg-surface-primary text-text-primary">
```

### 自訂主題注入

自訂主題無對應的靜態 CSS 區塊。由 `ThemeInjector` 元件在 runtime 注入 `<style>` 元素。

**只注入當前 active 的自訂主題**（不是全部自訂主題），避免 `<head>` 內堆積無用 `<style>`。Theme selector 的色塊預覽從 Registry 的 `tokens` 物件直接讀取，不需要 CSS 注入。

```tsx
function ThemeInjector() {
  const { activeThemeId, customThemes } = useThemeStore();
  const custom = customThemes[activeThemeId];

  useEffect(() => {
    if (!custom) return;
    const style = document.createElement('style');
    style.dataset.themeId = custom.id;
    style.textContent = `[data-theme="${custom.id}"] { ${tokensToCss(custom.tokens)} }`;
    document.head.appendChild(style);
    return () => style.remove();
  }, [custom]);

  return null;
}
```

## 5. Theme Registry

延續專案的 Registry 模式。

### ThemeDefinition

```ts
interface ThemeDefinition {
  id: string;           // 'dark' | 'light' | 'nord' | 'dracula' | custom-id
  name: string;         // 顯示名稱
  tokens: ThemeTokens;  // 全部 ~25 個 token 鍵值對
  builtin: boolean;     // 預設主題不可刪除/重命名
}

type ThemeTokens = Record<ThemeTokenKey, string>;
// ThemeTokenKey = 'surface-primary' | 'surface-secondary' | ... 全部 token key 的 union
```

### Registry API（`theme-registry.ts`）

```ts
registerTheme(def: ThemeDefinition): void      // 冪等註冊
getTheme(id: string): ThemeDefinition | undefined
getAllThemes(): ThemeDefinition[]
unregisterTheme(id: string): void              // 只能移除 !builtin
```

- Map-based，與 pane-registry 同結構
- 4 個預設主題在獨立的 `register-themes.ts` 中 `registerTheme()`（不塞進 `register-panes.tsx`，職責分離）
- App 啟動時 `register-themes.ts` 與 `register-panes.tsx` 同層級呼叫
- 自訂主題在 store hydrate 後也註冊進 Registry

## 6. Theme Store

### 介面（`useThemeStore.ts`）

```ts
interface ThemeState {
  activeThemeId: string;                          // 預設 'dark'
  customThemes: Record<string, ThemeDefinition>;  // 自訂主題持久化

  // Actions
  setActiveTheme(id: string): void;
  createCustomTheme(name: string, baseId: string, overrides: Partial<ThemeTokens>): string;
  updateCustomTheme(id: string, patch: Partial<ThemeDefinition>): void;
  deleteCustomTheme(id: string): void;
  importTheme(json: ThemeImportPayload): string;
}
```

- Persist key: `tbox-themes`
- `setActiveTheme` → 更新 `activeThemeId` + side effect 設定 `document.documentElement.dataset.theme`
- `createCustomTheme` → 複製 base tokens，套用 overrides，產生 6-char base36 ID（rejection sampling 排除與 builtin ID 及現有 custom ID 碰撞），同步 `registerTheme()`
- `deleteCustomTheme` → 刪除後若為 active，自動切回 `'dark'`
- Store hydrate 完成後，把 `customThemes` 全部 `registerTheme()` 進 Registry

### 與 UISettingsStore 的關係

完全獨立。UISettingsStore 不碰主題。

## 7. 預設主題

4 個預設主題：Dark（現有色調）、Light、Nord、Dracula。

全部標記 `builtin: true`，不可刪除/重命名。

各主題的完整 token 值在實作階段定義（參考各主題的官方配色規範）。

## 8. 主題編輯器 UI

### 進入方式

Settings > Appearance > Theme selector 旁的「自訂」按鈕 → 展開 ThemeEditor 面板（Settings 內部）。

### ThemeEditor 元件結構

```
ThemeEditor
├── 頂部：主題名稱（可編輯 input）+ 基底主題標示
├── Token 分組列表（6 組，可收合）
│   ├── Surface（7 tokens）
│   ├── Text（4 tokens）
│   ├── Border（3 tokens）
│   ├── Accent（3 tokens）
│   ├── Terminal（3 tokens）
│   └── Status（3 tokens）
├── 每個 token：label + 色塊預覽 + <input type="color"> + hex 輸入
└── 底部：儲存 / 取消 / 重置為基底
```

### 操作流程

1. Theme selector 選一個主題
2. 點「自訂」→ Fork 當前主題，進入 ThemeEditor
3. 調整色彩 → **即時預覽**：建立臨時 `<style>` 元素搭配臨時 `data-theme` 值，直接套用編輯中的 token。不修改 store state。
4. 輸入名稱 → 「儲存」→ `createCustomTheme()` 存入 store，移除臨時 `<style>`，切換到正式 theme ID
5. 「取消」→ 移除臨時 `<style>`，還原 `data-theme` 回原本的主題。頁面刷新等同取消（臨時 style 不持久化）

### Theme Selector 設計

- Dropdown 分兩區：預設主題（4）/ 自訂主題（0+）
- 自訂主題附刪除按鈕
- 預設主題不可刪除

### Color Picker

瀏覽器原生 `<input type="color">`，旁邊顯示 hex 值可手動輸入。不引入第三方套件。

## 9. 匯入/匯出

### Theme JSON 格式

```json
{
  "name": "My Theme",
  "tokens": {
    "surface-primary": "#0a0a1a",
    "surface-secondary": "#12122a",
    "accent": "#7a6aaa"
  }
}
```

### 匯入方式

ThemeEditor 區域或 Theme selector 旁的「匯入」按鈕 → 開啟 modal，支援：

1. **貼上 JSON**：textarea 貼入 JSON 文字
2. **選擇檔案**：拖入或選擇 `.json` 檔案
3. **URL 匯入**：貼入 URL，SPA `fetch` 取得 JSON。注意：受瀏覽器 CORS 政策限制，僅能 fetch 允許跨域的 URL（如 GitHub raw、CORS-friendly API）。fetch 失敗時顯示明確的錯誤訊息提示使用者改用複製貼上方式

### 匯入驗證

- 必須有 `name`（string）+ `tokens`（object）
- `tokens` 必須包含至少一個有效 token key
- 缺少的 token → 用 Dark 主題補齊（fallback merge）
- 重複名稱 → 自動加後綴（`My Theme (2)`）
- 驗證失敗 → 顯示錯誤訊息，不匯入

### 匯出

自訂主題可點「匯出」下載為 `.json` 檔案（`{name}.json`）。

匯入後自動 `createCustomTheme()` 存入 store 並 `registerTheme()`。

## 10. xterm.js 整合

Theme 只管 terminal-bg、terminal-fg、terminal-cursor 三個基本 token。

切換主題時，useTerminal hook 從 CSS variables 讀取值並更新 xterm instance：

```ts
const style = getComputedStyle(document.documentElement);
term.options.theme = {
  background: style.getPropertyValue('--terminal-bg').trim(),
  foreground: style.getPropertyValue('--terminal-fg').trim(),
  cursor: style.getPropertyValue('--terminal-cursor').trim(),
};
```

ANSI 16 色 palette 不在 Theme 範圍內，歸 Host 管理（per-daemon 設定）。

## 11. 遷移策略

所有現有元件的硬編碼色彩需逐一遷移為語義 token class。遷移對象包含：

- **Arbitrary values**：`bg-[#xxx]`、`text-[#xxx]`、`border-[#xxx]`、inline style
- **Tailwind 預設色彩 class**：`text-gray-*`、`bg-gray-*`、`border-gray-*` 等（約 24 檔 67 處）。這些在 Light/Nord/Dracula 主題下語義不正確（例如 dark 的 `text-gray-200` 是淺色文字，在 light 主題上會看不見），必須遷移為 `text-text-secondary` 等語義 token
- **測試檔案**：含硬編碼色值斷言的測試（如 `toContain('bg-[#7a6aaa]')`）需同步更新為新 token class

遷移原則：
- 先建立 token 系統 + Dark 主題（值與現有完全一致，視覺零變化）
- 再逐元件替換硬編碼為 token class
- 最後加入其他 3 個預設主題 + 編輯器 + 匯入匯出

### 影響範圍（依探索結果）

| 元件 | 主要色彩 | 遷移對象 |
|------|---------|---------|
| App.tsx | `#0a0a1a` bg | `bg-surface-primary` |
| TabBar | `#12122a` bg | `bg-surface-secondary` |
| SortableTab | `#12122a`/`#272444`/`#1a1a32` | secondary/active/hover |
| StatusBar | `#12122a` bg + dropdown | secondary + elevated |
| ActivityBar | `#08081a` bg | `bg-surface-tertiary` |
| TerminalView | `#0a0a1a` bg | `bg-terminal-bg` |
| Settings 元件 | sidebar/input/toggle | elevated/input/accent |
| SessionPicker | `#1e1e3e` bg | `bg-surface-elevated` |
| TabContextMenu | `#1e1e2e` bg | `bg-surface-elevated` |
| ConversationView | status 色彩 | status tokens |
| ToolCallBlock | `#1e1e1e`/`#2a2a2a` | elevated/border-subtle |
| Stream UI 元件 | 各種 hex | 對應 token |

## 12. 新增/異動檔案摘要

### 新增

| 檔案 | 用途 |
|------|------|
| `spa/src/styles/themes.css` | CSS variable 定義 + `@theme` 映射 |
| `spa/src/lib/theme-registry.ts` | Theme Registry |
| `spa/src/lib/theme-tokens.ts` | ThemeTokenKey type + token metadata（label、group） |
| `spa/src/stores/useThemeStore.ts` | Theme Store（activeTheme + customThemes + CRUD） |
| `spa/src/components/settings/ThemeEditor.tsx` | 色彩編輯器元件 |
| `spa/src/components/settings/ThemeImportModal.tsx` | 匯入 modal（JSON/檔案/URL） |
| `spa/src/components/ThemeInjector.tsx` | Active 自訂主題 runtime `<style>` 注入 |
| `spa/src/register-themes.ts` | 預設主題註冊（與 register-panes.tsx 同層級） |

### 異動

| 檔案 | 變更 |
|------|------|
| `spa/src/index.css` | 加 `@import "./styles/themes.css"` + `body` 背景改用 `var(--surface-primary)` |
| `spa/src/App.tsx` | 根元素加 `data-theme` + 掛載 ThemeInjector |
| `spa/src/components/settings/AppearanceSection.tsx` | 啟用 Theme selector + 自訂按鈕 + 匯入按鈕 |
| `spa/src/hooks/useTerminal.ts` | 從 CSS variables 讀取 terminal theme |
| 所有含硬編碼色彩的元件 | `bg-[#xxx]` / `text-gray-*` → 語義 token class |
| 含色值斷言的測試檔案 | 同步更新斷言為新 token class |
