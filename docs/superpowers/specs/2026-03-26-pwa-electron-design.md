# PWA + Electron 雙版本設計

## 概述

tmux-box 拆分為兩種部署版本：SPA（PWA installable）和 Electron 桌面版。共用同一份 React 程式碼，Electron 透過 `window.electronAPI`（preload 注入）提供進階能力。

## 決策摘要

| 決策項 | 選擇 | 理由 |
|--------|------|------|
| PWA 範圍 | Installable only（manifest + icons） | 遠端工具無 offline 意義 |
| PWA 離線 | 不做 service worker | 連線韌性由 WebSocket reconnect 處理 |
| Electron 載入方式 | A2（打包 SPA dist） | dev 用 loadURL，prod 用 loadFile |
| Daemon 打包 | 不包（暫時） | 先連遠端 daemon |
| 共用策略 | 同一份 SPA + window.electronAPI 偵測 | 不需 adapter 抽象層 |
| Browser pane | Electron 限定（WebContentsView IPC） | SPA 模式下 disabled + 提示 |
| 視窗管理 | Electron：tear-off + merge | SPA 模式下不可用 |
| System tray | Electron 限定 | 背景常駐、從 tray 開/切視窗 |

## 檔案結構

```
tmux-box/
  spa/                              ← 現有，共用
    public/
      manifest.json                 ← 新增：PWA manifest
      icons/                        ← 新增：icon-192, icon-512, icon-maskable-512
    src/
      lib/
        platform.ts                 ← 新增：capabilities 偵測
      components/
        BrowserPane.tsx             ← 新增：WebContentsView IPC placeholder
        BrowserNewTabSection.tsx    ← 新增：NewTab 的 Browser 區塊（URL 輸入）
    index.html                      ← 改：manifest link + PWA meta tags
    vite.config.ts                  ← 不動（不加 PWA plugin）

  electron/                         ← 全新
    main.ts                         ← Electron main process
    preload.ts                      ← contextBridge → window.electronAPI
    window-manager.ts               ← 多視窗 + tear-off/merge
    tray.ts                         ← system tray
    electron-builder.yml            ← 打包設定

  package.json                      ← 根：pnpm workspace + electron scripts
  pnpm-workspace.yaml               ← workspace 設定
```

## PWA

### manifest.json

```json
{
  "name": "tmux-box",
  "short_name": "tbox",
  "start_url": "/",
  "display": "standalone",
  "background_color": "#0a0a0a",
  "theme_color": "#0a0a0a",
  "icons": [
    { "src": "/icons/icon-192.png", "sizes": "192x192", "type": "image/png" },
    { "src": "/icons/icon-512.png", "sizes": "512x512", "type": "image/png" },
    { "src": "/icons/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable" }
  ]
}
```

### index.html 新增

```html
<link rel="manifest" href="/manifest.json">
<meta name="theme-color" content="#0a0a0a">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
<link rel="apple-touch-icon" href="/icons/icon-192.png">
```

不加 service worker。Chrome + iOS Safari 可安裝。

## Platform Capabilities

```typescript
// spa/src/lib/platform.ts
export interface PlatformCapabilities {
  canTearOffTab: boolean
  canMergeWindow: boolean
  canBrowserPane: boolean
  canSystemTray: boolean
}

export function getPlatformCapabilities(): PlatformCapabilities {
  const isElectron = !!window.electronAPI
  return {
    canTearOffTab: isElectron,
    canMergeWindow: isElectron,
    canBrowserPane: isElectron,
    canSystemTray: isElectron,
  }
}
```

### window.electronAPI 型別

```typescript
declare global {
  interface Window {
    electronAPI?: {
      tearOffTab: (tabJson: string) => Promise<void>
      mergeTab: (tabJson: string, targetWindowId: string) => Promise<void>
      openBrowserView: (url: string, paneId: string) => Promise<void>
      closeBrowserView: (paneId: string) => Promise<void>
      navigateBrowserView: (paneId: string, url: string) => Promise<void>
      onTabReceived: (callback: (tabJson: string) => void) => () => void
    }
  }
}
```

SPA 不 import 任何 electron 模組。所有跨 platform 通訊透過 `window.electronAPI` 這個唯一介面。

## PaneContent 新增 browser kind

```typescript
type PaneContent =
  | { kind: 'new-tab' }
  | { kind: 'session'; sessionCode: string; mode: 'terminal' | 'stream' }
  | { kind: 'dashboard' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' | { workspaceId: string } }
  | { kind: 'browser'; url: string }   // 新增
```

### 影響的 6 處

1. `types/tab.ts` — 加 browser kind
2. `pane-labels.ts`（getPaneLabel）— `case 'browser': try { return new URL(url).hostname } catch { return url }`
3. `pane-labels.ts`（getPaneIcon）— `case 'browser': return 'Globe'`（getPaneIcon 在同一個檔案，不存在獨立 pane-icons.ts）
4. `route-utils.ts`（tabToUrl）— 加 `case 'browser': return '/'`（browser pane 不映射到獨立 URL）
5. `register-panes.tsx` — 註冊 browser pane renderer + NewTab provider（永遠註冊，disabled 由 capabilities 決定）
6. `BrowserPane.tsx` — 新元件

### Browser NewTab Provider

```typescript
registerNewTabProvider({
  id: 'browser',
  label: 'browser.provider_label',
  icon: 'Globe',
  order: 10,
  component: BrowserNewTabSection,
  disabled: !caps.canBrowserPane,
  disabledReason: 'browser.requires_app',  // i18n key："需要安裝桌面版本"
})
```

### NewTabProvider interface 修改

```typescript
// new-tab-registry.ts
export interface NewTabProvider {
  id: string
  label: string
  icon: string
  order: number
  component: React.ComponentType<NewTabProviderProps>
  disabled?: boolean          // 新增
  disabledReason?: string     // 新增：i18n key，disabled 時顯示的說明
}
```

### NewTabPage disabled 渲染

`NewTabPage.tsx` 渲染 disabled provider 時：section 標題旁加 `(disabledReason)` 文字，component 不渲染，改為顯示一行灰色說明。

```tsx
{providers.map((p) => (
  <section key={p.id} className="w-full max-w-md">
    <h3 className="text-sm font-medium text-text-secondary mb-2 px-2">
      {t(p.label)}
      {p.disabled && p.disabledReason && (
        <span className="text-text-muted text-xs ml-2">— {t(p.disabledReason)}</span>
      )}
    </h3>
    {!p.disabled && <p.component onSelect={onSelect} />}
  </section>
))}
```

在 SPA 模式下：顯示 Browser 區塊標題 + disabled 說明「需要安裝桌面版本」，不顯示 URL 輸入。

### BrowserPane

使用 IPC 驅動的 `WebContentsView`（Electron 30+ 推薦方案），而非 deprecated 的 `<webview>` tag。

SPA 端 `BrowserPane.tsx` 只渲染一個 placeholder `<div>`，透過 `ResizeObserver` 回報尺寸和位置給 main process。main process 建立 `WebContentsView` 並疊在對應位置上。

```tsx
export function BrowserPane({ paneId, url }: { paneId: string; url: string }) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!window.electronAPI) return
    window.electronAPI.openBrowserView(url, paneId)
    return () => { window.electronAPI?.closeBrowserView(paneId) }
  }, [url, paneId])

  useEffect(() => {
    // ResizeObserver → IPC 回報 bounds 給 main process 定位 WebContentsView
  }, [paneId])

  if (!window.electronAPI) {
    return <p className="text-text-muted text-sm">{t('browser.requires_app')}</p>
  }

  return <div ref={ref} className="w-full h-full" data-browser-pane={paneId} />
}
```

Main process 側：
- `openBrowserView` → 建立 `WebContentsView`，`loadURL(url)`，attach 到 `BrowserWindow`
- `closeBrowserView` → detach + destroy
- bounds 更新：SPA `ResizeObserver` → IPC → `view.setBounds()`

此方案與 `window.electronAPI` 的 IPC 設計一致（`openBrowserView` / `closeBrowserView` / `navigateBrowserView`）。

## Electron Main Process

### 啟動流程

```
app.whenReady()
  → createTray()
  → createMainWindow()
    → dev:  loadURL('http://100.64.0.2:5174')
    → prod: loadFile(path.join(__dirname, '../spa/dist/index.html'))
  → 註冊 IPC handlers
```

### window-manager.ts

```typescript
class WindowManager {
  private windows = new Map<string, BrowserWindow>()

  createWindow(opts?: { tabJson?: string }): BrowserWindow
  closeWindow(windowId: string): void
  getAll(): BrowserWindow[]
  showOrCreate(): void  // tray 點擊用

  // Tear-off
  handleTearOff(tabJson: string): void
  // Merge
  handleMerge(tabJson: string, targetWindowId: string): void
}
```

### Tear-off 流程

1. SPA `@dnd-kit` 偵測 tab 被拖出視窗邊界
2. SPA 呼叫 `window.electronAPI.tearOffTab(JSON.stringify(tab))`
3. **來源視窗的 SPA 立即執行 `removeTab(tabId)`**（不等 IPC 回應，避免閃爍）
4. main process → `windowManager.handleTearOff(tabJson)`
5. 建新 BrowserWindow，載入 SPA
6. 新視窗收到 `webContents.send('receive-tab', tabJson)` → `useTabStore.addTab(deserializedTab)` 並設為 active

**Tab 序列化範圍：** Tab 是純資料（id, layout, pinned, locked, createdAt），不含 WebSocket 連線狀態。Session tab 在新視窗載入後，由 `SessionPaneContent` 的正常 mount 邏輯自動建立新 WebSocket 連線（reconnect 策略已有）。

### Merge 流程

1. 小視窗 SPA 偵測到 tab 被拖出
2. main process 偵測滑鼠位置在另一個 BrowserWindow 範圍內
3. **來源視窗的 SPA 執行 `removeTab(tabId)`** + 序列化 tab data
4. tab data 透過 IPC 傳給目標視窗 → `useTabStore.addTab()`
5. 關閉來源視窗（若無剩餘 tab）

### tray.ts

```typescript
function createTray(): Tray {
  const tray = new Tray(iconPath)  // macOS: Template image
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: 'Show Window', click: () => windowManager.showOrCreate() },
    { type: 'separator' },
    { label: 'Quit', click: () => app.quit() }
  ]))
}
```

- 關閉視窗 ≠ 退出 app（`window-all-closed` 不呼叫 `app.quit()`）
- 從 tray 點擊 → show 或 create

## Build Pipeline

```bash
# SPA / PWA（現有 + manifest）
pnpm --filter spa build         → spa/dist/

# Electron 開發
pnpm electron:dev               → Vite dev server + Electron 同時啟動

# Electron 打包
pnpm electron:build             → build SPA → electron-builder → .app
```

### pnpm workspace 設定

```yaml
# pnpm-workspace.yaml（根目錄，新增）
packages:
  - 'spa'
  - 'electron'
```

```jsonc
// package.json（根目錄，新增）
{
  "private": true,
  "scripts": {
    "electron:dev": "pnpm --filter spa dev & pnpm --filter electron dev",
    "electron:build": "pnpm --filter spa build && pnpm --filter electron build"
  }
}
```

`electron/package.json` 放 Electron 專屬依賴（`electron`、`electron-builder`），SPA 的 `package.json` 不變。

## i18n Keys

新增到 en.json / zh-TW.json：

```json
{
  "browser.provider_label": "Browser",
  "browser.requires_app": "Requires desktop app",
  "browser.url_placeholder": "Enter URL...",
  "tray.show_window": "Show Window",
  "tray.quit": "Quit"
}
```

## 測試策略

### SPA 側（Vitest）

- `platform.test.ts` — 預設全 false；mock `window.electronAPI` 後全 true
- `pane-labels.test.ts` — 補 browser case
- `register-panes.test.ts` — browser provider 永遠註冊、disabled 狀態正確
- BrowserPane.tsx — 不測（依賴 Electron IPC / WebContentsView）

### Electron 側

- 第一版手動驗證（Electron 測試工具鏈成本高）
- Smoke test checklist：啟動、連線、tray、tear-off、merge、webview

## 分期

| Phase | 範圍 | PR 策略 |
|-------|------|---------|
| A | PWA manifest + icons + meta | 合併 A+B 一個 PR |
| B | platform.ts + browser PaneContent kind + BrowserPane + i18n keys | 同上 |
| C | electron/ 目錄 + main + preload + tray | 獨立 PR |
| D | window-manager + tear-off/merge IPC | 獨立 PR |
| E | webview 整合 + BrowserPane 接上 IPC | 獨立 PR |

## YAGNI — 不做的事

- 全域快捷鍵（Phase 2）
- Native menu bar 自訂（用預設）
- Auto-update（electron-updater）
- Daemon 打包進 Electron
- Webview 歷史紀錄 / 書籤管理
- Service worker / offline cache
- 多 tab webview 的 session 隔離（partition）
