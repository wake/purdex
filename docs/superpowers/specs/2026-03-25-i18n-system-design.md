# i18n 系統設計

## 概述

tmux-box SPA 自建輕量 i18n 系統，對齊 Theme 系統架構模式。支援 builtin + 自訂語言包、即時切換、匯入匯出。

## 決策摘要

| 決策項 | 選擇 | 理由 |
|--------|------|------|
| 目標語系 | 多語系框架（zh-TW + en 優先） | 未來可擴充 |
| 預設語言 | 跟隨 navigator.languages，fallback en | 尊重使用者瀏覽器設定 |
| 翻譯檔案 | 單一 JSON per 語言 | ~90 key，YAGNI |
| Key 格式 | 扁平 dot notation | 好 grep、好搜尋 |
| Library | 自建（無第三方依賴） | 字串量小、跟現有 Registry/Store 風格一致 |
| React 接入 | Zustand Store | 跟 Theme/UISettings 7 個 store 統一模式 |
| 切換行為 | 即時生效，零 reload | React re-render 驅動 |
| 持久化 | Zustand persist → localStorage | 跟 Theme store 同策略 |
| 自訂語言包 | 開放使用者 fork/編輯/匯入匯出 | 跟 Theme 自訂體驗對齊 |
| Registry 標籤 | 存 i18n key，render 時 t() 翻譯 | 乾淨，改動集中 |
| 語言設定位置 | Settings > Appearance（跟 Theme 同區塊） | 外觀偏好的一部分 |

## 核心架構

### 資料流

```
Builtin Locales (靜態 import)  ─┐
                                 ├→ Locale Registry (Map) → useI18nStore.t() → Components
Custom Locales (Zustand persist) ─┘
```

### 型別

```typescript
interface LocaleDef {
  id: string           // 'en' | 'zh-TW' | 'custom-xxx'
  name: string         // 'English' | '繁體中文'
  translations: Record<string, string>
  builtin: boolean
}

type TFunction = (key: string, params?: Record<string, string | number>) => string
```

### t() 查詢順序

1. Active locale 的 translations[key]
2. en（fallback）的 translations[key]
3. key 本身（開發時立刻看出漏翻）

### Interpolation

```
"keepalive.desc": "Background tabs to keep connected ({{count}} = active only)"
t('keepalive.desc', { count: 0 })
// → "Background tabs to keep connected (0 = active only)"
```

實作：`value.replace(/\{\{(\w+)\}\}/g, (_, k) => String(params[k] ?? ''))`

## 與 Theme 系統的平行對照

| Theme 系統 | i18n 系統 |
|-----------|----------|
| register-themes.ts | register-locales.ts |
| theme-registry.ts | locale-registry.ts |
| useThemeStore (tbox-themes) | useI18nStore (tbox-i18n) |
| ThemeEditor → 色彩選擇器 | LocaleEditor → key-value 文字編輯 |
| ThemeImportModal | LocaleImportModal |
| ThemeInjector → `<style>` | 不需要（純資料 lookup） |
| themes.css（靜態 Tailwind） | locales/en.json + zh-TW.json |

## 語言偵測與切換

### 初次載入偵測（無 localStorage）

1. 檢查 localStorage (`tbox-i18n`) → 有值就用
2. 讀取 `navigator.languages` → 依序比對 Registry 已註冊的 locale id
   - 完全匹配優先（`zh-TW` → `zh-TW`）
   - 再試 prefix 匹配（`en-US` → `en`）
3. Fallback → `en`

### 即時切換

1. `setLocale('zh-TW')` → Store 更新 activeLocaleId
2. Store 重建 `t()` 函數（指向新 dictionary）
3. 所有 subscribe `s.t` 的元件 re-render
4. Zustand persist → localStorage 自動儲存
5. `document.documentElement.lang` 同步更新

## useI18nStore

```typescript
interface I18nState {
  activeLocaleId: string
  customLocales: Record<string, LocaleDef>  // keyed by id，同 Theme store 的 customThemes
  t: TFunction
  setLocale: (id: string) => void
  importLocale: (payload: LocaleImportPayload) => string  // 回傳新 id
  updateCustomLocale: (id: string, patch: Partial<LocaleDef>) => void
  deleteCustomLocale: (id: string) => void
}
```

Persist key: `tbox-i18n`

`t()` 是 derived——locale 變 → t 參照變 → Zustand selector 觸發 re-render。

### 語言偵測

`detectLocale()` 是純函數（非 store method），在 store 初始化時呼叫一次：

```typescript
// lib/detect-locale.ts
export function detectLocale(registeredIds: string[]): string
```

1. 讀取 `navigator.languages`
2. 依序比對 registeredIds（完全匹配 → prefix 匹配）
3. 無匹配回傳 `'en'`

Store persist `onRehydrateStorage` 中：若 localStorage 無值，呼叫 `detectLocale()` 設定初始 locale。

### setLocale 副作用

`setLocale()` 除了更新 `activeLocaleId` + 重建 `t()`，同步執行 `document.documentElement.lang = localeId`（對齊 Theme store 的 `applyThemeToDom()`）。

## Locale Registry

```typescript
// locale-registry.ts — 同 theme-registry.ts 結構
const registry = new Map<string, LocaleDef>()

export function registerLocale(def: LocaleDef): void     // 冪等
export function unregisterLocale(id: string): void        // builtin 不可 unregister
export function getLocale(id: string): LocaleDef | undefined
export function getAllLocales(): LocaleDef[]
export function clearLocaleRegistry(): void               // 測試用，清空 registry
```

## Settings UI

### Appearance Section 語言區塊

跟 Theme 完全相同的 UI pattern：

- **Language** dropdown（分組：Builtin / Custom）+ Export 按鈕 + Delete 按鈕
- **Customize** — fork 當前 locale → 開 LocaleEditor
- **Import** — 開 LocaleImportModal

### LocaleEditor（Modal）

- 頂部：Locale Name 輸入
- 搜尋欄位：搜尋 key 或 value
- 篩選 tabs：All / Modified / Missing
- 三欄式列表：
  - Key（唯讀，monospace）
  - 原文/en（唯讀，灰色背景）
  - 翻譯（可編輯 input）
- 按 dot notation 前綴分組，顯示 group header
- 已修改項綠色標記
- Footer：Reset / Cancel / Save

### LocaleImportModal

跟 ThemeImportModal 相同三種方式：Paste JSON / File / URL

### Export JSON 格式

```json
{
  "name": "我的繁中翻譯",
  "baseLocale": "zh-TW",
  "version": 1,
  "translations": {
    "settings.title": "設定",
    "tab.close": "關閉分頁"
  }
}
```

- `name`（必要）：顯示名稱
- `baseLocale`（選填）：基於哪個 builtin locale fork，用於 Editor 顯示原文欄
- `version`：export schema 版本（用於未來格式相容）
- `translations`（必要）：key-value 翻譯字典

Import 時由 `locale-import.ts` 的 `parseAndValidateLocale()` 驗證，自動產生唯一 id（`custom-${nanoid}`）並處理名稱去重。

## i18n Key 命名規範

扁平 dot notation，10 個分組前綴：

| 前綴 | 範圍 | 範例 |
|------|------|------|
| `common.*` | 共用按鈕/動作 | common.save, common.cancel, common.delete |
| `settings.*` | Settings 頁面 | settings.title, settings.appearance.theme.label |
| `tab.*` | Tab 操作 | tab.close, tab.close.others, tab.close.right |
| `nav.*` | ActivityBar / StatusBar | nav.new.workspace, nav.settings |
| `page.*` | 頁面標題/空狀態 | page.newtab.title, page.history.empty |
| `stream.*` | Stream / CC | stream.thinking, stream.starting |
| `session.*` | Session 列表 | session.title, session.empty, session.search |
| `theme.*` | Theme Editor | theme.editor.title, theme.group.surface |
| `locale.*` | Locale Editor | locale.editor.title, locale.filter.modified |
| `error.*` | 錯誤訊息 | error.json.invalid, error.fetch.failed |

## 檔案結構

### 新增檔案（9 個）

```
spa/src/
  locales/
    en.json                          — English 翻譯（fallback，完整 key set）
    zh-TW.json                       — 繁體中文翻譯
  lib/
    locale-registry.ts               — Map-based registry
    locale-import.ts                 — parseAndValidateLocale() + LocaleImportPayload 型別
    detect-locale.ts                 — detectLocale() 純函數
    register-locales.ts              — registerBuiltinLocales()
  stores/
    i18n-store.ts                    — useI18nStore
  components/settings/
    LocaleEditor.tsx                 — key-value 翻譯編輯器 modal
    LocaleImportModal.tsx            — JSON 匯入 modal
```

### 修改檔案（~25 個）

**啟動 & 基礎：**
- `main.tsx` — 加 `registerBuiltinLocales()`
- `register-panes.tsx` — section label 改 i18n key
- `pane-labels.ts` — 標籤改 i18n key

**Settings：**
- `AppearanceSection.tsx` — 語言選擇器 enable + locale CRUD 按鈕
- `TerminalSection.tsx` — 所有 label/desc
- `SettingsSidebar.tsx` — "Settings" / "coming soon" + registry label 通過 t() 渲染
- `ThemeEditor.tsx` — GROUP_LABELS + 按鈕
- `ThemeImportModal.tsx` — 標題/錯誤訊息

**Tab & 導覽：**
- `TabContextMenu.tsx` — 右鍵選單
- `TabBar.tsx` — aria-label
- `ActivityBar.tsx` — title
- `StatusBar.tsx` — title

**頁面 & Stream：**
- `NewTabPage.tsx` — 標題/空狀態 + provider label 通過 t() 渲染
- `HistoryPage.tsx` — 標題/狀態標籤
- `DashboardPage.tsx` — 標題/內容
- `SessionPanel.tsx` — 標題/空狀態
- `SessionPaneContent.tsx` — 狀態文字
- `HandoffButton.tsx` — 進度文字
- `ConversationView.tsx` — 狀態訊息
- `PermissionPrompt.tsx` — 按鈕
- `ThinkingBlock.tsx` — 標籤
- `StreamInput.tsx` — placeholder
- `AskUserQuestion.tsx` — placeholder / 按鈕
- `ToolCallBlock.tsx` — 標籤
- `ToolResultBlock.tsx` — 標籤

### 啟動順序

```
registerBuiltinLocales() → registerBuiltinThemes() → registerBuiltinPanes() → createRoot
```

## 測試策略

### locale-registry.test.ts
- registerLocale / unregisterLocale / getLocale / getAllLocales
- builtin 不可 unregister
- 冪等註冊

### i18n-store.test.ts
- t(key) 正常查詢
- t(key, params) interpolation
- fallback 順序：active locale → en → key itself
- setLocale 切換 → t() 參照變化
- detectLocale — navigator.languages 比對
- custom locale CRUD（add / update / delete）
- 刪除 active custom locale → fallback to en
- persist rehydrate 後 t() 正確

### locale-validation.test.ts
- Import JSON 驗證（必要欄位、型別檢查）
- 空名稱 / 重複名稱處理
- translations 值型別必須是 string

### locale-completeness.test.ts
- en.json 與 zh-TW.json key 集合相同（無遺漏、無多餘）
- 所有 t() 呼叫使用的 key 都存在於 en.json（可選，靜態掃描）

## 邊界情況

| 情況 | 處理 |
|------|------|
| Key 不存在 | 回傳 key 本身 |
| Active locale 被刪除 | 自動 fallback to 'en' |
| Custom locale 部分翻譯 | 缺的 key fallback to en |
| Interpolation param 缺失 | `{{var}}` 替換為空字串 |
| Persist 損壞 / schema 變更 | version guard — 不匹配就 reset |
| 新版本新增 key | builtin 跟隨更新；custom locale 用 Missing filter 顯示 |

## YAGNI — 不做的事

- Plurals / ICU MessageFormat — 目前無複數需求
- RTL 支援 — 目標語系都是 LTR
- 日期/數字格式化 — 無日期/貨幣顯示
- Lazy loading 語言包 — 字串量小，靜態打包
- Server-side locale — 純 SPA，無 SSR
- Namespace / 多檔案 — 單檔足夠
