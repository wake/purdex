# Browser Tab Enhancement 設計規格

## 範圍

兩個功能：

1. **Browser tab 完整 toolbar** — 導航按鈕、可編輯 URL 欄位、更多選單
2. **Mini browser 獨立視窗** — Shift+click 彈出，共用同一套 toolbar component

**不在範圍內：**
- 完整 tab tear-off（跨視窗拖曳、狀態同步）
- WebContentsView 跨視窗搬移（V1 用重新載入取代）

**已知取捨：**
- 從 browser tab 彈出 mini browser 時，主視窗 tab 繼續存在（兩個 WebContentsView 並行，記憶體 double），V1 不處理此問題

## 連結點擊行為

統一規則，適用所有連結來源：

| 環境 | 來源 | Click | Shift+Click |
|------|------|-------|-------------|
| Electron | Terminal 連結 | 開新 browser tab | 彈出 mini browser 視窗 |
| Electron | Browser tab 內 `target=_blank` 連結 | 開新 browser tab | 彈出 mini browser 視窗 |
| Electron | Browser tab 內一般連結 | 正常導航（不攔截） | 正常導航（不攔截） |
| SPA | Terminal 連結 | `window.open(uri, '_blank')` | `window.open(uri, '_blank')` |

SPA 環境下 browser tab 不可用，所有連結一律開系統瀏覽器新分頁。

**Browser tab 內連結的攔截規則：** 只攔截 `target=_blank`（透過 Electron 的 `setWindowOpenHandler`）。一般頁面內導航走正常 `will-navigate` 流程，不攔截。

## BrowserToolbar Component

共用元件，同時用在 browser tab（inline）和 mini browser 視窗。純 props-driven，不直接呼叫 IPC。

### Props 介面

```typescript
interface BrowserToolbarProps {
  url: string
  title: string
  canGoBack: boolean
  canGoForward: boolean
  isLoading: boolean
  context: 'tab' | 'mini-window'     // 控制選單項目顯示
  onGoBack: () => void
  onGoForward: () => void
  onReload: () => void
  onStop: () => void
  onNavigate: (url: string) => void   // URL 欄位 Enter
  onOpenExternal: () => void          // 系統瀏覽器開啟（直接用 url prop）
  onCopyUrl: () => void
  onPopOut?: () => void               // 彈出成 mini browser（僅 context='tab'）
  onMoveToTab?: () => void            // 在主視窗重新開啟此網址（僅 context='mini-window'）
}
```

### 組成

| 區域 | 元素 | 功能 |
|------|------|------|
| 左側 | ← → ↻/✕ 按鈕 | goBack / goForward / reload（載入中切換為 stop） |
| 中間 | URL 欄位 | 顯示當前 URL，可編輯 + Enter 導航（見 URL 正規化規則） |
| 右側 | ⋯ 更多選單 | 下拉選單 |

所有 icon 統一使用 Phosphor Icons。Reload 與 Stop 按鈕依 `isLoading` 狀態交替顯示，比照瀏覽器行為。

### ⋯ 選單項目

- 在系統瀏覽器開啟
- 複製 URL
- 彈出成 mini browser 視窗（僅 `context='tab'` 顯示）
- 在主視窗重新開啟此網址（僅 `context='mini-window'` 顯示）

### URL 正規化

URL 欄位 Enter 觸發 `onNavigate(url)` 前，先做正規化：
- 無 scheme 的字串（如 `github.com`）→ 自動補 `https://`
- `javascript:` scheme → 拒絕（不觸發 onNavigate）
- 非 `http:` / `https:` scheme → 拒絕（不觸發 onNavigate）

此邏輯與現有 `BrowserNewTabSection.tsx` 的 URL 驗證一致，可抽為共用 utility。

### 導航按鈕狀態

- ← disabled 當 `canGoBack === false`
- → disabled 當 `canGoForward === false`

## BrowserPane 佈局變更

現有 `BrowserPane.tsx` 的 `ResizeObserver` 把整個 div 的 bounds 推給 Electron。加入 toolbar 後需要調整：

1. BrowserPane 頂部渲染 `BrowserToolbar`，下方為 `WebContentsView` 佔位區
2. `ResizeObserver` 觀察的是**佔位區**（不含 toolbar），推送的 bounds 是佔位區的位置和大小
3. toolbar 高度固定（不隨 WebContentsView 縮放），避免 WebContentsView 遮住 toolbar

## Electron 架構變更

### 新增 IPC 頻道

IPC 頻道名稱（snake-case）與 preload 函式名稱（camelCase）一對一對應。

| 方向 | IPC 頻道 | Preload 函式 | 用途 |
|------|----------|-------------|------|
| SPA → Electron | `browser-view:go-back` | `browserViewGoBack` | 上一頁 |
| SPA → Electron | `browser-view:go-forward` | `browserViewGoForward` | 下一頁 |
| SPA → Electron | `browser-view:reload` | `browserViewReload` | 重新整理 |
| SPA → Electron | `browser-view:stop` | `browserViewStop` | 停止載入 |
| SPA → Electron | `browser-view:open-mini-window` | `browserViewOpenMiniWindow` | 彈出 mini browser 視窗 |
| SPA → Electron | `browser-view:destroy` | `destroyBrowserView` | 主動關閉 tab 時銷毀 view |
| SPA → Electron | `browser-view:move-to-tab` | `browserViewMoveToTab` | mini browser「在主視窗重新開啟此網址」 |
| Electron → SPA | `browser-view:state-update` | `onBrowserViewStateUpdate` | 回報導航狀態 |
| Electron → SPA | `browser-view:open-in-tab` | `onBrowserViewOpenInTab` | 主視窗收到：開新 browser tab |
| WebContentsView → Electron | `browser-view:link-click` | （preload 內部） | 頁面內連結點擊回報 shiftKey + URL |

**不需要 IPC 的操作：**
- **在系統瀏覽器開啟** — toolbar 已持有 URL，SPA 端直接 `window.open(url, '_blank')` 即可

**`browser-view:close` vs `browser-view:destroy` 語意區分：**
- `browser-view:close`（現有）→ `background()` — 用於 tab 切換時 BrowserPane unmount，view 保留在背景
- `browser-view:destroy`（新增）→ `destroy()` — 用於使用者主動關閉 tab，真正銷毀 view

### State Update 機制

Electron 監聽 `WebContentsView` 事件（`did-navigate`、`did-start-loading`、`did-stop-loading`、`page-title-updated` 等），彙整為 `BrowserViewState` 透過 `viewEntry.window.webContents.send()` 推給**該 view 所屬 window** 的 SPA。

```typescript
interface BrowserViewState {
  url: string
  title: string
  canGoBack: boolean
  canGoForward: boolean
  isLoading: boolean
}
```

推送時帶 `paneId`，SPA 端的 `useBrowserViewState(paneId)` hook 過濾只處理自己的 paneId。

### Browser Tab 內連結攔截（shiftKey 問題）

Electron 的 `will-navigate` 和 `setWindowOpenHandler` 事件不包含 modifier keys。解決方案：

為 `WebContentsView` 設定專用 preload script，在頁面層攔截 `click` 事件：

```javascript
// browser-view-preload.ts（注入到 WebContentsView，需在 electron-vite 打包為 CJS）
const { ipcRenderer } = require('electron')

document.addEventListener('click', (e) => {
  const link = e.target.closest('a[href]')
  if (!link) return
  const href = link.href
  if (!href || href.startsWith('javascript:')) return

  if (e.shiftKey || link.target === '_blank') {
    e.preventDefault()
    ipcRenderer.send('browser-view:link-click', {
      url: href,
      shiftKey: e.shiftKey,
      targetBlank: link.target === '_blank',
    })
  }
  // 一般連結不攔截，走正常導航
}, true)
```

**打包設定：** `electron.vite.config.ts` 的 preload section 需新增第二個 entry：

```typescript
preload: {
  build: {
    rollupOptions: {
      input: {
        index: resolve(__dirname, 'electron/preload.ts'),
        browserViewPreload: resolve(__dirname, 'electron/browser-view-preload.ts'),
      },
    },
  },
},
```

`BrowserViewManager` 建立 `WebContentsView` 時，透過 `join(__dirname, '../preload/browserViewPreload.js')` 引用打包後的路徑設定 `webPreferences.preload`。

**sender 反查機制：** Main process 收到 `browser-view:link-click` 時，`event.sender` 是 WebContentsView 的 webContents（不是 BrowserWindow 的）。`BrowserViewManager` 需新增反查方法：

```typescript
getEntryByWebContents(wc: WebContents): ViewEntry | undefined
```

透過遍歷 `views` Map 比對 `entry.view.webContents === wc` 實現。取得 entry 後即可知道 paneId 和所屬 window，進而通知正確的主視窗 SPA 開新 tab。

### Mini Browser 視窗

**建立方式：** 新建 Electron `BrowserWindow`，載入獨立的 SPA entry point。

**SPA Entry Point：** 新增 `spa/src/mini-browser.tsx` 作為 mini browser 的獨立 entry（Vite multi-entry），搭配 `spa/mini-browser.html` 作為 HTML 入口。這個 entry 只渲染 `BrowserToolbar` + `WebContentsView` 佔位 div，不載入完整 App（無 tab bar、sidebar 等）。透過 URL query parameter 傳入 `paneId`（如 `mini-browser.html?paneId=xxx`）。

**Vite 打包設定：** `electron.vite.config.ts` 的 renderer section 需擴充 `rollupOptions.input`：

```typescript
renderer: {
  build: {
    rollupOptions: {
      input: {
        index: resolve(__dirname, 'spa/index.html'),
        miniBrowser: resolve(__dirname, 'spa/mini-browser.html'),
      },
    },
  },
},
```

**載入路徑：**
- Dev 模式：`http://100.64.0.2:5174/mini-browser.html?paneId=xxx`
- Bundled 模式：`app://./mini-browser.html?paneId=xxx`

**生命週期：**
- `mini-browser-window.ts` 建立 `BrowserWindow` + `WebContentsView`，記錄 parent window reference
- 視窗關閉時 destroy `WebContentsView`，從 `BrowserViewManager` 移除
- 「在主視窗重新開啟此網址」：見下方 IPC 路徑說明

**Mini window SPA 如何知道自己的 paneId：** 從 `window.location.search` 的 `paneId` query parameter 讀取，由 `mini-browser-window.ts` 建立視窗時帶入 URL。

**「在主視窗重新開啟此網址」IPC 路徑：**

Mini window SPA 無法直接存取主視窗的 tab store，需要完整的 IPC 鏈：

1. Mini window SPA → main process：`browserViewMoveToTab(paneId)` IPC（preload 暴露）
2. Main process 從 `MiniWindowManager` 查詢 paneId 對應的 parent window 和當前 URL
3. Main process → 主視窗 SPA：`parentWindow.webContents.send('browser-view:open-in-tab', url)`
4. 主視窗 SPA 監聽 `browser-view:open-in-tab` 事件，呼叫 `openBrowserTab(url)`
5. Main process 關閉 mini window + destroy WebContentsView

## xterm.js 連結攔截

### 變更

`useTerminal.ts` 的 `WebLinksAddon` 改為傳入自訂 handler：

```typescript
new WebLinksAddon((event, uri) => {
  linkHandler(event, uri)
})
```

`linkHandler` 由上層傳入（`useTerminal` 接受 `linkHandler` 參數），不在 hook 內部處理。

### link-handler.ts

建立連結處理函式的 factory，接受 callback 參數，保持 `lib/` 模組不直接依賴 store：

```typescript
interface LinkHandlerDeps {
  isElectron: boolean
  openBrowserTab: (url: string) => void
  openMiniWindow: (url: string) => void
}

function createLinkHandler(deps: LinkHandlerDeps) {
  return (event: MouseEvent, uri: string) => {
    if (deps.isElectron) {
      if (event.shiftKey) deps.openMiniWindow(uri)
      else deps.openBrowserTab(uri)
    } else {
      window.open(uri, '_blank')
    }
  }
}
```

呼叫端（TerminalView 等）負責接線：將 `openBrowserTab` 綁到 tab store action，`openMiniWindow` 綁到 Electron IPC。

## 模組劃分

```
spa/src/
├── components/
│   └── BrowserToolbar.tsx        # 共用 toolbar（純 UI，props-driven）
│
├── hooks/
│   ├── useTerminal.ts            # 現有，改為接受 linkHandler 參數
│   └── useBrowserViewState.ts    # 訂閱 Electron state-update，以 paneId 過濾
│
├── lib/
│   └── link-handler.ts           # createLinkHandler factory（純函式，不依賴 store）
│
├── mini-browser.tsx              # Mini browser 獨立 entry point（Vite multi-entry）
│
electron/
├── browser-view-manager.ts       # 現有，擴充導航 API + state 回報 + preload script 注入
├── browser-view-ipc.ts           # 新增，集中註冊 browser-view IPC handler
├── browser-view-preload.ts       # 新增，注入 WebContentsView 攔截連結 click（electron-vite 打包為 CJS）
└── mini-browser-window.ts        # 新增，mini browser 視窗建立與生命週期
```

### 各模組職責

| 模組 | 職責 | 依賴 |
|------|------|------|
| `BrowserToolbar` | 渲染 ← → ↻/✕ URL ⋯，觸發 callback | 無外部依賴，純 props |
| `useBrowserViewState(paneId)` | 監聯 IPC state-update，以 paneId 過濾，回傳 `BrowserViewState`。IPC listener 必須在 `useEffect` cleanup 中透過 unsubscribe function 移除，避免 Strict Mode double-mount 殭屍 listener | Electron preload API |
| `link-handler.ts` | `createLinkHandler(deps)` factory — 根據 platform + shiftKey 分派 | 無（deps 由呼叫端注入） |
| `mini-browser.tsx` | Mini browser SPA entry，只渲染 toolbar + 佔位 div | BrowserToolbar、useBrowserViewState |
| `browser-view-ipc.ts` | export `registerBrowserViewIpc(manager, miniWindowManager)` 由 `main.ts` 呼叫 | browser-view-manager、mini-browser-window |
| `browser-view-preload.ts` | 注入 WebContentsView，攔截 click 事件回報 shiftKey + URL。需作為 electron-vite preload entry 打包（CJS） | Electron ipcRenderer |
| `mini-browser-window.ts` | 建立/管理 mini browser BrowserWindow，記錄 parent window reference，URL 帶入 paneId | Electron BrowserWindow |
| `browser-view-manager.ts` | 現有 + goBack/goForward/reload/stop + destroy() + 監聽 webContents 推 state + 注入 preload + `getEntryByWebContents()` 反查 | Electron WebContentsView |

### 設計原則

- `BrowserToolbar` 完全 props-driven，不直接呼叫 IPC，由使用端接線
- `link-handler.ts` 是 factory，不依賴 store，由呼叫端注入 dependencies
- Electron 端 IPC 集中在 `browser-view-ipc.ts`，`main.ts` 只呼叫 `registerBrowserViewIpc()`
- `state-update` 推送到 view 所屬 window，hook 以 paneId 過濾

## Preload API 擴充

### 現有

```typescript
openBrowserView(url: string, paneId: string): void
closeBrowserView(paneId: string): void
navigateBrowserView(paneId: string, url: string): void
resizeBrowserView(paneId: string, bounds: Bounds): void
```

### 新增

```typescript
// 導航控制
browserViewGoBack(paneId: string): void
browserViewGoForward(paneId: string): void
browserViewReload(paneId: string): void
browserViewStop(paneId: string): void

// 視窗操作
browserViewOpenMiniWindow(url: string): void
destroyBrowserView(paneId: string): void          // 主動關閉 tab 時銷毀
browserViewMoveToTab(paneId: string): void         // mini browser → 在主視窗重新開啟

// 狀態訂閱（Electron → SPA）
onBrowserViewStateUpdate(
  callback: (paneId: string, state: BrowserViewState) => void
): () => void   // 回傳 unsubscribe function

// 主視窗接收（Electron → SPA）
onBrowserViewOpenInTab(
  callback: (url: string) => void
): () => void   // 回傳 unsubscribe function
```

**「在系統瀏覽器開啟」** 不需要新 IPC — toolbar 已持有 URL，SPA 端直接 `window.open(url, '_blank')` 即可。

## PaneContent 型別

不需變更。現有 `{ kind: 'browser'; url: string }` 已足夠，`url` 為初始 URL。

### Tab Store 新增 Action

```typescript
openBrowserTab(url: string): void
```

此 action 放在 hook 層（`useTabWorkspaceActions`），不放在 `useTabStore` store 層。原因：workspace 整合邏輯（`addTabToWorkspace`、`setWorkspaceActiveTab`）在現有架構中由 hook 層完成（參考 `handleAddTab`），store 層不直接跨 store 呼叫。

`openBrowserTab` 內部流程：建立新 tab（content `{ kind: 'browser', url }`）→ 加入當前 workspace → 設為 active。

### Tab Title 顯示

- 來源：`BrowserViewState.title`（Electron 回報的頁面 title）
- Fallback：URL 的 hostname（如 `github.com`）
- Icon：Phosphor `Globe`（需確認 `TabBar.tsx` 的 ICON_MAP 包含 `Globe`）

## Browser Tab 關閉復原

### 關閉時 Snapshot

Browser tab 關閉前，從 Electron 取得當前 URL 和 title：

```typescript
interface BrowserTabSnapshot {
  url: string    // 當前 URL
  title: string  // 頁面 title
}
```

V1 不存導航歷史（`navigationHistory` API 可用性有版本風險，且目前無法注入歷史復原）。

### 關閉路徑

使用者主動關閉 browser tab 時：
1. 從 `BrowserViewManager` 取得當前 `BrowserViewState`（url + title）
2. 存入 recently-closed store
3. 透過 `destroyBrowserView(paneId)` IPC 呼叫 `BrowserViewManager.destroy(paneId)`（真正銷毀 view）

**與 tab 切換路徑的區分：**
- Tab 切換（BrowserPane unmount）→ `closeBrowserView(paneId)` → `background()`（view 保留在背景）
- 使用者主動關閉 tab → `destroyBrowserView(paneId)` → `destroy()`（view 銷毀）
- Idle discard → `background()` → 超時 → `discard()`（快照 URL 後銷毀，可復原）

### 儲存

存在記憶體中的 recently-closed store。App 重啟後不保留（與瀏覽器行為一致）。

### 復原

建立新 browser tab，`url` 設為 snapshot 的 URL。頁面重新載入（不保留滾動位置等狀態）。
