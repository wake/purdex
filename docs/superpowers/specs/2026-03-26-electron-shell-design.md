# Electron Shell 設計（Plan C+D+E）

## 概述

tmux-box Electron 桌面版：基於 electron-vite 建構，提供多視窗管理（tear-off/merge）、WebContentsView browser pane、system tray 常駐、記憶體監控。所有 SPA 側修改透過 `getPlatformCapabilities()` gate，確保 SPA 版本零影響。

## 決策摘要

| 決策項 | 選擇 | 理由 |
|--------|------|------|
| 工具鏈 | electron-vite | 與 Vite 技術棧一致，main/preload HMR 開箱即用 |
| 目標平台 | macOS only | 先做能測試的平台，架構不卡死跨平台 |
| Daemon 連線 | 沿用 SPA hostStore | 不重複連線邏輯，Electron 只是換殼 |
| Tear-off/merge 觸發 | 右鍵選單 | 簡單可靠，跨視窗拖曳座標計算留待後續 |
| WebContentsView 生命週期 | timeout + 記憶體監控 + max background | 三個可設定參數，自動 discard 閒置 view |
| 記憶體監控 | 新 PaneContent kind | 所有分頁的記憶體/CPU 用量 dashboard |
| 安全模型 | contextIsolation + sandbox | Electron 安全最佳實踐 |

## SPA/Electron 相容策略

所有 SPA 側修改必須同時在 SPA 和 Electron 模式下正常運作：

- **capability gate**：新 UI 元素（context menu tear-off、Electron settings、memory monitor）透過 `getPlatformCapabilities()` 條件渲染
- **NewTabProvider disabled**：memory-monitor provider 在 SPA 模式下 disabled + 說明文字（同 browser pane 模式）
- **Settings section gate**：Electron settings section 只在 `isElectron` 時註冊
- **BrowserPane useEffect**：`if (!window.electronAPI) return` 提前返回，SPA 行為不變
- **TabContextMenu**：tear-off/merge 選項只在 `canTearOffTab`/`canMergeWindow` 時顯示
- **electron.d.ts**：ambient 型別宣告，SPA 編譯不受影響（`electronAPI?` optional）
- **i18n keys**：所有新 key 加到 en.json + zh-TW.json，SPA 不使用但不影響 completeness test

## 檔案結構

```
tmux-box/
  spa/                              ← 現有，共用
    src/
      components/
        BrowserPane.tsx             ← 改：+useEffect IPC + ResizeObserver
        MemoryMonitorPage.tsx       ← 新：記憶體監控頁面
        ElectronSettingsSection.tsx  ← 新：Electron 設定區塊
      types/
        electron.d.ts               ← 改：+4 methods, +3 types
        tab.ts                      ← 改：+memory-monitor PaneContent kind
      lib/
        pane-labels.ts              ← 改：+memory-monitor case
        route-utils.ts              ← 改：+memory-monitor case
        register-panes.tsx          ← 改：+memory-monitor renderer/provider + electron settings section
        pane-utils.ts               ← 改：+memory-monitor contentMatches (singleton)
      locales/
        en.json                     ← 改：+electron/tray/memory-monitor keys
        zh-TW.json                  ← 改：同上

  electron/                         ← 全新
    main.ts                         ← app lifecycle + IPC handlers
    preload.ts                      ← contextBridge → window.electronAPI
    window-manager.ts               ← BaseWindow CRUD + tear-off/merge
    browser-view-manager.ts         ← WebContentsView pool + LRU + timeout + memory
    tray.ts                         ← system tray
    package.json                    ← electron + electron-vite deps
    tsconfig.json

  electron.vite.config.ts           ← 全新：electron-vite 設定
  pnpm-workspace.yaml               ← 全新：workspace 設定
  package.json                      ← 改：+electron scripts
```

## IPC API（window.electronAPI）

PR #75 已定義 6 個 method，本設計新增 4 個，共 10 個：

```typescript
interface Window {
  electronAPI?: {
    // === Window Management ===
    tearOffTab(tabJson: string): Promise<void>
    mergeTab(tabJson: string, targetWindowId: string): Promise<void>
    getWindows(): Promise<WindowInfo[]>            // 新增
    onTabReceived(cb: (tabJson: string) => void): () => void

    // === Browser View ===
    openBrowserView(url: string, paneId: string): Promise<void>
    closeBrowserView(paneId: string): Promise<void>
    navigateBrowserView(paneId: string, url: string): Promise<void>
    resizeBrowserView(paneId: string, bounds: Bounds): Promise<void>  // 新增

    // === Memory Monitor ===
    getProcessMetrics(): Promise<TabMetrics[]>     // 新增
    onMetricsUpdate(cb: (metrics: TabMetrics[]) => void): () => void  // 新增
  }
}

interface WindowInfo {
  id: string
  title: string
}

interface Bounds {
  x: number
  y: number
  width: number
  height: number
}

interface TabMetrics {
  paneId: string
  kind: string        // 'terminal' | 'stream' | 'browser' | 'shared-renderer'
  memoryKB: number
  cpuPercent: number
}
```

### IPC 通道映射

| electronAPI method | IPC channel | 方向 |
|--------------------|-------------|------|
| tearOffTab | `window:tear-off` | renderer → main |
| mergeTab | `window:merge` | renderer → main |
| getWindows | `window:get-all` | renderer → main (invoke) |
| onTabReceived | `tab:received` | main → renderer |
| openBrowserView | `browser-view:open` | renderer → main |
| closeBrowserView | `browser-view:close` | renderer → main |
| navigateBrowserView | `browser-view:navigate` | renderer → main |
| resizeBrowserView | `browser-view:resize` | renderer → main |
| getProcessMetrics | `metrics:get` | renderer → main (invoke) |
| onMetricsUpdate | `metrics:update` | main → renderer |

## Electron Main Process

### main.ts — 啟動流程

```
app.whenReady()
  → createTray(windowManager)
  → windowManager.createWindow()
    → dev:  loadURL('http://100.64.0.2:5174')
    → prod: loadFile(path.join(__dirname, '../spa/dist/index.html'))
  → 註冊 IPC handlers
  → 啟動 metrics polling (30s interval)
```

### 安全設定

```typescript
const webPreferences = {
  contextIsolation: true,
  sandbox: true,
  nodeIntegration: false,
  preload: path.join(__dirname, 'preload.js'),
}
```

### window-manager.ts

```typescript
class WindowManager {
  private windows = new Map<string, BaseWindow>()

  createWindow(opts?: { tabJson?: string }): BaseWindow
  closeWindow(windowId: string): void
  getAll(): WindowInfo[]
  showOrCreate(): void  // tray 點擊用

  // Tear-off：建新視窗，SPA ready 後 send tab data
  handleTearOff(tabJson: string): void

  // Merge：轉發 tab data 給目標視窗
  handleMerge(tabJson: string, targetWindowId: string): void
}
```

### browser-view-manager.ts

管理 WebContentsView 的生命週期，三種狀態：

```
ACTIVE ↔ BACKGROUND → DISCARDED
```

- **ACTIVE**：visible，`backgroundThrottling = false`，`setBounds` 在真實位置
- **BACKGROUND**：hidden，`backgroundThrottling = true`，`setBounds(-10000, -10000)`，idle timer 開始
- **DISCARDED**：`webContents.close()` 銷毀，URL 保存在 snapshot，切回時 `loadURL` 重建

#### Discard 觸發條件（任一觸發 → discard 最久沒用的 background view）

| 觸發 | 預設值 | 設定 key |
|------|--------|----------|
| Idle timeout | 5 分鐘 | `browserView.idleTimeout` |
| Memory threshold | 512 MB（全部 view 總和） | `browserView.memoryLimitMB` |
| Max background count | 3 | `browserView.maxBackground` |

#### 記憶體偵測

```typescript
// 每 30 秒執行
const metrics = app.getAppMetrics()
// 搭配 view.webContents.getOSProcessId() 比對
// 多個 view 可能共用 renderer process — 需處理 pid 重複
```

### tray.ts

```typescript
function createTray(windowManager: WindowManager): Tray {
  const tray = new Tray(iconPath)  // macOS Template image
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: t('tray.show_window'), click: () => windowManager.showOrCreate() },
    { type: 'separator' },
    { label: t('tray.quit'), click: () => app.quit() },
  ]))
  return tray
}
```

行為：
- 關閉視窗 ≠ 退出 app（`window-all-closed` 不呼叫 `app.quit()`）
- `activate` event → `windowManager.showOrCreate()`
- Quit 只能從 tray 選單或 Cmd+Q

## Tear-off 流程

1. User 右鍵 tab → **"Move to New Window"**
2. SPA 呼叫 `electronAPI.tearOffTab(JSON.stringify(tab))`
3. **Source SPA 立即 `removeTab(tabId)`**（fire-and-forget，不等 IPC）
4. Main process → `windowManager.handleTearOff(tabJson)`
5. 建新 BaseWindow，載入 SPA
6. 新視窗 SPA ready → `webContents.send('tab:received', tabJson)`
7. 新 SPA：`addTab(deserialize(tabJson))` → auto reconnect

**Tab 序列化**：Tab 是純資料（id, layout, pinned, locked, createdAt），不含 WS 連線。Session tab 在新視窗 mount 後由既有 reconnect 邏輯自動建立新連線。

## Merge 流程

1. User 右鍵 tab → **"Move to → Window 2 (Dashboard)"**
2. Submenu 列出所有視窗（`electronAPI.getWindows()` 取得，排除當前視窗）
3. SPA 呼叫 `electronAPI.mergeTab(tabJson, targetWindowId)`
4. **Source SPA 立即 `removeTab(tabId)`**
5. 若 source 視窗無剩餘 tab → 關閉視窗
6. Main process → `webContents.send('tab:received', tabJson)` 給目標視窗
7. Target SPA：`addTab(deserialize(tabJson))` → set active

## WebContentsView 整合（BrowserPane）

### BrowserPane.tsx 修改

```tsx
export function BrowserPane({ paneId, url }: BrowserPaneProps) {
  const ref = useRef<HTMLDivElement>(null)
  const t = useI18nStore((s) => s.t)

  // Open/close lifecycle
  useEffect(() => {
    if (!window.electronAPI) return
    window.electronAPI.openBrowserView(url, paneId)
    return () => { window.electronAPI?.closeBrowserView(paneId) }
  }, [url, paneId])

  // Bounds sync
  useEffect(() => {
    if (!window.electronAPI || !ref.current) return
    const observer = new ResizeObserver(() => {
      const el = ref.current
      if (!el) return
      const rect = el.getBoundingClientRect()
      window.electronAPI!.resizeBrowserView(paneId, {
        x: Math.round(rect.x),
        y: Math.round(rect.y),
        width: Math.round(rect.width),
        height: Math.round(rect.height),
      })
    })
    observer.observe(ref.current)
    return () => observer.disconnect()
  }, [paneId])

  // SPA fallback
  if (!window.electronAPI) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-muted">{t('browser.requires_app')}</p>
      </div>
    )
  }

  return <div ref={ref} className="w-full h-full" data-browser-pane={paneId} />
}
```

### Main process 側

- `openBrowserView(url, paneId)`：建立 `WebContentsView`，`loadURL(url)`，`addChildView` 到對應 `BaseWindow`
- `closeBrowserView(paneId)`：`removeChildView` + `webContents.close()`（或移入 background pool）
- `resizeBrowserView(paneId, bounds)`：`view.setBounds(bounds)`
- `navigateBrowserView(paneId, url)`：`view.webContents.loadURL(url)`

## Memory Monitor Page

### PaneContent

```typescript
type PaneContent =
  | ... // existing kinds
  | { kind: 'memory-monitor' }   // 新增，singleton
```

### contentMatches

```typescript
// memory-monitor 是 singleton（同 dashboard）
// 走 return true 的預設路徑即可
```

### MemoryMonitorPage.tsx

透過 `electronAPI.getProcessMetrics()` 取得初始資料，`electronAPI.onMetricsUpdate()` 訂閱即時更新。

顯示表格：
| 欄位 | 說明 |
|------|------|
| Tab name | 從 `getPaneLabel` 取得 |
| Kind | terminal / stream / browser |
| Memory | 個別 view 記憶體（browser），或 "shared"（terminal/stream） |
| CPU | 同上 |
| State | active / background / discarded |

底部 summary：renderer process total + browser views total + app total。

SPA 模式下：顯示 disabled 訊息（同 browser pane 模式）。

### NewTabProvider

```typescript
registerNewTabProvider({
  id: 'memory-monitor',
  label: 'monitor.provider_label',
  icon: 'ChartBar',
  order: 20,
  component: MemoryMonitorNewTabSection,
  disabled: !caps.canSystemTray,  // gate by isElectron
  disabledReason: 'monitor.requires_app',
})
```

## Settings

### Electron Settings Section

新增 `ElectronSettingsSection.tsx`，3 個設定項：

| 設定 | 型別 | 預設值 | 說明 |
|------|------|--------|------|
| `browserView.idleTimeout` | number | 5 | 背景 view 閒置幾分鐘後 discard |
| `browserView.memoryLimitMB` | number | 512 | 全部 view 記憶體總和上限（MB） |
| `browserView.maxBackground` | number | 3 | 最多保留幾個 background view |

這些設定存在 SPA 的 `useUISettingsStore`（persist），Electron main process 透過 IPC 讀取。

Settings section 註冊時透過 `getPlatformCapabilities()` gate：

```typescript
if (caps.canSystemTray) {
  registerSettingsSection({
    id: 'electron',
    label: 'settings.section.electron',
    order: 5,
    component: ElectronSettingsSection,
  })
}
```

## i18n Keys

新增到 en.json / zh-TW.json：

```json
{
  "tray.show_window": "Show Window",
  "tray.quit": "Quit tmux-box",

  "settings.section.electron": "Electron",
  "settings.electron.title": "Desktop App",
  "settings.electron.desc": "Desktop app specific settings",
  "settings.electron.idle_timeout.label": "Browser View Idle Timeout",
  "settings.electron.idle_timeout.desc": "Minutes before background browser view is discarded",
  "settings.electron.idle_timeout.aria": "Idle timeout",
  "settings.electron.memory_limit.label": "Browser View Memory Limit",
  "settings.electron.memory_limit.desc": "Discard oldest view when total memory exceeds this (MB)",
  "settings.electron.memory_limit.aria": "Memory limit",
  "settings.electron.max_bg.label": "Max Background Views",
  "settings.electron.max_bg.desc": "Maximum browser views kept alive in background",
  "settings.electron.max_bg.aria": "Max background views",

  "monitor.provider_label": "Memory Monitor",
  "monitor.requires_app": "Requires desktop app",
  "monitor.title": "Memory Monitor",
  "monitor.col.tab": "Tab",
  "monitor.col.kind": "Kind",
  "monitor.col.memory": "Memory",
  "monitor.col.cpu": "CPU",
  "monitor.col.state": "State",
  "monitor.state.active": "Active",
  "monitor.state.background": "Background",
  "monitor.state.discarded": "Discarded",
  "monitor.shared": "shared",
  "monitor.summary.renderer": "Renderer process",
  "monitor.summary.views": "Browser views",
  "monitor.summary.total": "Total app",

  "tab.move_new_window": "Move to New Window",
  "tab.move_to": "Move to"
}
```

## Build Pipeline

```bash
# SPA 開發（不變）
cd spa && pnpm dev

# Electron 開發
pnpm electron:dev    → electron-vite dev（啟動 Electron + 連 SPA dev server）

# SPA 打包（不變）
cd spa && pnpm build

# Electron 打包
pnpm electron:build  → build SPA → electron-vite build → electron-builder → .app
```

### pnpm workspace

```yaml
# pnpm-workspace.yaml
packages:
  - 'spa'
  - 'electron'
```

### electron-vite 設定

```typescript
// electron.vite.config.ts
import { defineConfig } from 'electron-vite'

export default defineConfig({
  main: {
    build: {
      rollupOptions: {
        input: { index: 'electron/main.ts' },
      },
    },
  },
  preload: {
    build: {
      rollupOptions: {
        input: { index: 'electron/preload.ts' },
      },
    },
  },
  renderer: {
    // 使用 spa/ 的 Vite 設定
    root: 'spa',
    build: {
      rollupOptions: {
        input: 'spa/index.html',
      },
    },
  },
})
```

## 測試策略

### SPA 側（Vitest）

- `electron.d.ts` 型別擴展 — tsc 編譯驗證
- `pane-labels.test.ts` — +memory-monitor case
- `pane-utils.test.ts` — +memory-monitor contentMatches（singleton）
- `register-panes.test.ts` — +memory-monitor provider disabled/enabled
- BrowserPane useEffect — 不測（依賴 Electron IPC）
- MemoryMonitorPage — 不測（依賴 Electron IPC）
- locale completeness test — 守門新增 keys

### Electron 側

手動 smoke test checklist：
- [ ] `pnpm electron:dev` 啟動成功，載入 SPA
- [ ] 連線 daemon（100.64.0.2:7860）正常
- [ ] System tray 出現，Show Window / Quit 正常
- [ ] 關閉視窗後 tray 仍在，點擊 tray → 重新開視窗
- [ ] 右鍵 tab → "Move to New Window" → 新視窗出現 + tab 轉移
- [ ] 右鍵 tab → "Move to → Window X" → tab 合併到目標
- [ ] 最後一個 tab merge 走 → source 視窗自動關閉
- [ ] Browser pane：New Tab → Browser → 輸入 URL → WebContentsView 載入
- [ ] Browser pane 切走 → background → idle timeout 後 discarded
- [ ] Memory Monitor page 顯示所有 tab 記憶體
- [ ] Settings → Electron section 可調整 3 個參數
- [ ] `pnpm electron:build` 打包 .app 成功

## YAGNI — 不做的事

- 拖曳 tear-off/merge（跨視窗座標計算，留待後續）
- 全域快捷鍵
- Native menu bar 自訂（用預設）
- Auto-update（electron-updater）
- Daemon 打包進 Electron
- Webview 歷史紀錄 / 書籤管理
- Service worker / offline cache
- 多 tab webview 的 session 隔離（partition）
- 跨平台（Linux / Windows）
