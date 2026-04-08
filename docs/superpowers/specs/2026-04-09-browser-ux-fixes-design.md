# Browser UX Fixes — Mini Window + Tab Shortcuts

Date: 2026-04-09

## 問題描述

1. **Mini window toolbar 不可見**：Shift+Click 開啟的獨立瀏覽器視窗缺少 theme 初始化，CSS 變數 undefined 導致 toolbar 背景透明、文字不可見
2. **Mini window Cmd+W 無效**：menu accelerator 送 `shortcut:execute` 到 focused window，但 `MiniBrowserApp` 沒有 shortcut listener
3. **Browser tab 缺常用快捷鍵**：`keybindings.ts` 只有 tab 管理快捷鍵，沒有瀏覽器導航快捷鍵（back/forward/reload/focus URL/print）

## 設計

### 1. Tab Shortcut Handler Registry

引入 tab 等級的快捷鍵 dispatch 機制，讓不同 tab type 可以註冊各自的 handler。

**架構流程**：
```
Menu accelerator
  → action string (e.g. "reload")
  → useShortcuts dispatcher
    → 先查全域 handler (close-tab, new-tab, switch-tab-* 等)
    → 未命中 → 查 active tab 的 pane content kind
    → 從 registry 找 kind + action 對應的 handler
    → 執行
```

**新檔 `spa/src/lib/tab-shortcut-registry.ts`**：

- 型別：`TabShortcutHandler = (tab: Tab, pane: Pane) => void`
- 資料結構：`Map<PaneContent['kind'], Map<action, handler>>`
- 公開 API：
  - `registerTabShortcuts(kind, handlerMap)` — 註冊某 kind 的 handler map
  - `getTabShortcutHandler(kind, action)` — 查找 handler

**修改 `spa/src/hooks/useShortcuts.ts`**：

在現有全域 action handler（close-tab, new-tab, switch-tab-* 等）之後、`unknown action` fallback 之前，加入 registry dispatch：
1. 取得 active tab + primary pane
2. `getTabShortcutHandler(pane.content.kind, action)`
3. 有 handler → 呼叫；無 handler → 走原本的 unknown action log

### 2. Browser Tab Shortcuts 註冊

**新檔 `spa/src/lib/browser-shortcuts.ts`**：

呼叫 `registerTabShortcuts('browser', { ... })` 註冊以下 handler：

| action | handler |
|--------|---------|
| `go-back` | `electronAPI.browserViewGoBack(pane.id)` |
| `go-forward` | `electronAPI.browserViewGoForward(pane.id)` |
| `reload` | `electronAPI.browserViewReload(pane.id)` |
| `focus-url` | `document.dispatchEvent(new CustomEvent('browser:focus-url'))` |
| `print` | `electronAPI.browserViewPrint(pane.id)` |

此檔案需在 app 啟動時 import，確保 registry 有內容。

### 3. Keybindings 擴充

**修改 `electron/keybindings.ts`**：

`KeybindingDef` 新增 optional `platform` 欄位：

```ts
platform?: 'darwin' | 'win32' | 'linux'  // 省略 = 全平台
```

`MenuGroup` 新增 `'browser'`。

`menuCategory` 新增 `'Browser'`。

新增快捷鍵定義：

| action | accelerator | platform | label |
|--------|------------|----------|-------|
| `go-back` | `CommandOrControl+[` | — | Go Back |
| `go-forward` | `CommandOrControl+]` | — | Go Forward |
| `go-back` | `Command+Left` | darwin | Go Back |
| `go-forward` | `Command+Right` | darwin | Go Forward |
| `reload` | `CommandOrControl+R` | — | Reload |
| `focus-url` | `CommandOrControl+L` | — | Focus Address Bar |
| `print` | `CommandOrControl+P` | — | Print |

注意 `go-back` / `go-forward` 各有兩組 accelerator（`Cmd+[` 和 `Cmd+←`）。

**修改 `buildMenuTemplate()`**：

- 過濾不符合當前 `process.platform` 的項目
- 同一 action 多個 accelerator 時，menu 只顯示一個項目，但所有 accelerator 都要註冊。做法：第一個 entry 正常放入 menu item，後續同 action 的 entry 設為 `visible: false` 的隱藏 menu item（仍註冊 accelerator）
- 新增 Browser 子選單

### 4. Mini Window Theme 修復

**修改 `spa/src/mini-browser.tsx`**：

- Import `useThemeStore`（模組載入觸發 Zustand persist rehydrate → `applyThemeToDom(themeId)`）
- 渲染 `<ThemeInjector />` 支援自訂主題 CSS 注入

效果：mini window 的 `<html>` 取得 `data-theme` 屬性，CSS 變數生效，toolbar 可見。

### 5. Mini Window 快捷鍵

Mini window 不走 `useShortcuts`（那是主視窗的 tab/workspace 管理邏輯），改用獨立的精簡 hook。

**新檔 `spa/src/hooks/useMiniWindowShortcuts.ts`**：

監聽 `shortcut:execute` IPC，處理以下 action：

| action | 行為 |
|--------|------|
| `close-tab` | `window.close()` |
| `go-back` | `electronAPI.browserViewGoBack(paneId)` |
| `go-forward` | `electronAPI.browserViewGoForward(paneId)` |
| `reload` | `electronAPI.browserViewReload(paneId)` |
| `focus-url` | `document.dispatchEvent(new CustomEvent('browser:focus-url'))` |
| `print` | `electronAPI.browserViewPrint(paneId)` |

**修改 `spa/src/components/MiniBrowserApp.tsx`**：呼叫 `useMiniWindowShortcuts(paneId)`。

### 6. `focus-url` DOM 通訊

Shortcut handler 無法直接 reference toolbar 的 `<input>`，改用 custom DOM event：

- Handler 端：`document.dispatchEvent(new CustomEvent('browser:focus-url'))`
- **修改 `spa/src/components/BrowserToolbar.tsx`**：加 `useEffect` 監聽 `browser:focus-url` event → `inputRef.current?.focus()` + `inputRef.current?.select()`

主視窗和 mini window 共用同一個 `BrowserToolbar` component，自動受益。

### 7. 新增 IPC — Print

- **修改 `electron/preload.ts`**：新增 `browserViewPrint(paneId: string)` bridge
- **修改 `electron/browser-view-ipc.ts`**：新增 `browser-view:print` handler → `entry.view.webContents.print()`

Electron 的 `webContents.print()` 呼叫系統列印對話框。

## 變更清單

| 檔案 | 變更類型 | 說明 |
|------|---------|------|
| `electron/keybindings.ts` | 修改 | 加 `platform` 欄位、`browser` menuGroup/menuCategory、7 個 keybinding |
| `electron/keybindings.ts` | 修改 | `buildMenuTemplate` 加 platform 過濾、同 action 多 accelerator、Browser 子選單 |
| `electron/browser-view-ipc.ts` | 修改 | 新增 `browser-view:print` handler |
| `electron/preload.ts` | 修改 | 新增 `browserViewPrint` bridge |
| `spa/src/lib/tab-shortcut-registry.ts` | 新檔 | Tab shortcut handler registry |
| `spa/src/lib/browser-shortcuts.ts` | 新檔 | 註冊 browser tab 的 5 個 shortcut handler |
| `spa/src/hooks/useShortcuts.ts` | 修改 | unknown action 前加 registry dispatch |
| `spa/src/hooks/useMiniWindowShortcuts.ts` | 新檔 | Mini window 精簡 shortcut handler |
| `spa/src/components/BrowserToolbar.tsx` | 修改 | 加 `browser:focus-url` event listener |
| `spa/src/components/MiniBrowserApp.tsx` | 修改 | 用 `useMiniWindowShortcuts` |
| `spa/src/mini-browser.tsx` | 修改 | 引入 `useThemeStore` + `<ThemeInjector />` |

## 不在範圍內

- `Cmd+F` 頁內搜尋 — 需要獨立 UI（搜尋列 + 上下筆 + match 計數），另開 issue 追蹤
- Windows 平台 `Alt+←/→` — 目前 build target 只有 macOS，日後再加
