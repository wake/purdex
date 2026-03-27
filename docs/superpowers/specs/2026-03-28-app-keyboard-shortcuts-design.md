# App 快捷鍵系統設計

## 概述

為 Electron shell 加入 Menu accelerator 快捷鍵，讓使用者快速切換 tab、開啟 Settings/History。架構上採用 keybinding registry pattern，為未來自定義快捷鍵預留擴充點。

**範圍**：Electron 專屬功能（瀏覽器中 Cmd+1 等按鍵被瀏覽器攔截，無法使用）。

**不含**：使用者自定義快捷鍵 UI、keybindings.json 設定檔、衝突偵測、按鍵錄製元件。

## 快捷鍵清單

| 快捷鍵 (macOS) | Windows/Linux | Action Name | 說明 |
|---|---|---|---|
| `Cmd+1` ~ `Cmd+8` | `Ctrl+1` ~ `Ctrl+8` | `switch-tab-{n}` | 切換到第 N 個 tab |
| `Cmd+9` | `Ctrl+9` | `switch-tab-last` | 切換到最後一個 tab |
| `Cmd+Option+←` | `Ctrl+Alt+←` | `prev-tab` | 前一個 tab |
| `Cmd+Option+→` | `Ctrl+Alt+→` | `next-tab` | 下一個 tab |
| `Cmd+,` | `Ctrl+,` | `open-settings` | 開啟 Settings（singleton tab） |
| `Cmd+Y` | `Ctrl+Y` | `open-history` | 開啟歷史紀錄（singleton tab） |
| `Cmd+Shift+T` | `Ctrl+Shift+T` | `reopen-closed-tab` | 重開最近關閉的 tab |

### Tab 索引規則

- `Cmd+1` ~ `Cmd+8`：切換到 tabOrder 中的第 1~8 個 tab（0-indexed: tabOrder[0] ~ tabOrder[7]）
- `Cmd+9`：固定切換到最後一個 tab（tabOrder[tabOrder.length - 1]），不論 tab 總數
- 索引超出範圍時靜默忽略（不做任何事）

## 架構

### 層級

```
┌─────────────────────────────────────────────────┐
│ Keybinding Registry (electron/keybindings.ts)   │
│  action name → { accelerator, label }           │
│  getMenuTemplate() → Electron MenuItemOptions[] │
└────────────────────┬────────────────────────────┘
                     │ Menu accelerator 觸發
                     ▼
┌─────────────────────────────────────────────────┐
│ Electron Main (main.ts)                         │
│  Menu.setApplicationMenu(menu)                  │
│  click → webContents.send('shortcut:execute',   │
│          { action, payload })                    │
└────────────────────┬────────────────────────────┘
                     │ IPC
                     ▼
┌─────────────────────────────────────────────────┐
│ Preload (preload.ts)                            │
│  expose onShortcut(callback) via electronAPI    │
└────────────────────┬────────────────────────────┘
                     │ contextBridge
                     ▼
┌─────────────────────────────────────────────────┐
│ SPA (useShortcuts.ts hook)                      │
│  listener → dispatch to store actions           │
│  - switch-tab → useTabStore.setActiveTab()      │
│  - open-settings → openSingletonTab(settings)   │
│  - open-history → openSingletonTab(history)     │
│  - reopen-closed-tab → useHistoryStore          │
└─────────────────────────────────────────────────┘
```

### 新增 / 修改檔案

| 檔案 | 動作 | 說明 |
|------|------|------|
| `electron/keybindings.ts` | 新增 | Keybinding registry：定義 default bindings，產生 Menu template |
| `electron/main.ts` | 修改 | 呼叫 registry 建立 Menu，設定 click handler 發送 IPC |
| `electron/preload.ts` | 修改 | 加入 `onShortcut(callback)` API |
| `spa/src/hooks/useShortcuts.ts` | 新增 | 統一 shortcut listener，dispatch 到 stores |
| `spa/src/App.tsx` | 修改 | 掛載 `useShortcuts` hook，移除現有硬編碼 `Cmd+Shift+T` |
| `spa/src/types/electron.d.ts` | 修改 | 擴充 `ElectronAPI` type |

### Keybinding Registry（electron/keybindings.ts）

```typescript
interface KeybindingDef {
  action: string
  accelerator: string          // Electron accelerator 格式
  label: string                // Menu 顯示名稱
  menuCategory?: string        // Menu 分類（Tab、View 等）
}

// Default bindings
const DEFAULT_KEYBINDINGS: KeybindingDef[] = [
  { action: 'switch-tab-1', accelerator: 'CommandOrControl+1', label: 'Tab 1', menuCategory: 'Tab' },
  { action: 'switch-tab-2', accelerator: 'CommandOrControl+2', label: 'Tab 2', menuCategory: 'Tab' },
  // ... 3~8
  { action: 'switch-tab-last', accelerator: 'CommandOrControl+9', label: 'Last Tab', menuCategory: 'Tab' },
  { action: 'prev-tab', accelerator: 'CommandOrControl+Alt+Left', label: 'Previous Tab', menuCategory: 'Tab' },
  { action: 'next-tab', accelerator: 'CommandOrControl+Alt+Right', label: 'Next Tab', menuCategory: 'Tab' },
  { action: 'open-settings', accelerator: 'CommandOrControl+,', label: 'Settings', menuCategory: 'View' },
  { action: 'open-history', accelerator: 'CommandOrControl+Y', label: 'History', menuCategory: 'View' },
  { action: 'reopen-closed-tab', accelerator: 'CommandOrControl+Shift+T', label: 'Reopen Closed Tab', menuCategory: 'Tab' },
]

function buildMenu(bindings: KeybindingDef[], send: (action: string) => void): MenuItemConstructorOptions[]
```

**未來自定義擴充點**：在 `buildMenu` 前插入一層 merge（user JSON overrides → defaults），registry 與 Menu 建構邏輯不需改動。

### IPC 協議

**Channel**：`shortcut:execute`

**Payload**：
```typescript
interface ShortcutPayload {
  action: string              // e.g. 'switch-tab-3', 'open-settings'
}
```

- `switch-tab-{n}` 的 tab index 從 action name 解析（`switch-tab-1` → index 0）
- `switch-tab-last` 為特殊 action，SPA 側讀取 tabOrder.length - 1

### SPA Hook（useShortcuts.ts）

```typescript
function useShortcuts() {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return

    const cleanup = window.electronAPI.onShortcut(({ action }) => {
      // dispatch based on action name
    })

    return cleanup
  }, [])
}
```

**Action dispatch 映射**：
- `switch-tab-{n}` → 讀取當前 workspace 的 tabOrder，`setActiveTab(tabOrder[n-1])`
- `switch-tab-last` → `setActiveTab(tabOrder[tabOrder.length - 1])`
- `prev-tab` / `next-tab` → 找到 activeTabId 在 tabOrder 的位置，±1（循環）
- `open-settings` → `openSingletonTab({ kind: 'settings', scope: 'global' })`
- `open-history` → `openSingletonTab({ kind: 'history' })`
- `reopen-closed-tab` → `useHistoryStore.getState().reopenLast()` + addTab + setActiveTab

### Menu 結構

```
tmux-box（App menu，macOS 自動產生）
├─ About tmux-box
├─ Settings          Cmd+,
├─ ─────────────────
├─ Hide / Quit（macOS 標準）

Tab
├─ Tab 1             Cmd+1
├─ Tab 2             Cmd+2
├─ ...
├─ Tab 8             Cmd+8
├─ Last Tab          Cmd+9
├─ ─────────────────
├─ Previous Tab      Cmd+Option+←
├─ Next Tab          Cmd+Option+→
├─ ─────────────────
├─ Reopen Closed Tab Cmd+Shift+T

View
├─ History           Cmd+Y

Edit（macOS 標準，讓 Cmd+C/V/X/A 正常運作）
├─ Cut / Copy / Paste / Select All
```

macOS 的 Edit menu 必須存在，否則 `Cmd+C/V/X/A` 在 input 元素中無法使用。

### 非 Electron 環境

SPA 在瀏覽器中運行時，`window.electronAPI` 不存在，`useShortcuts` hook 直接 return。不需 fallback —— 這些快捷鍵在瀏覽器中無意義。

現有的 `App.tsx` 中 `Cmd+Shift+T` 硬編碼保留為 SPA fallback？不保留 —— 瀏覽器中此快捷鍵被瀏覽器自己的「重開分頁」功能攔截。

## 測試策略

### 單元測試（Vitest）

- **useShortcuts hook**：模擬 `window.electronAPI.onShortcut` 發送各種 action，驗證 store 狀態變更
  - switch-tab-1 ~ 8：正確切換到對應 tab
  - switch-tab-last：切換到最後一個 tab
  - 索引超出範圍：不做任何事
  - prev-tab / next-tab：循環切換
  - open-settings / open-history：開啟 singleton tab
  - reopen-closed-tab：reopenLast 整合

### 手動測試（Electron）

- 在 Electron .app 中驗證所有快捷鍵觸發正確
- 確認 macOS Edit menu 讓 Cmd+C/V/X/A 在 input 中正常運作
- 確認 Menu 顯示正確的快捷鍵提示
