# Settings UI 重構設計

> Date: 2026-03-25
> Scope: Settings UI 重構（Phase 1 of 3: Settings → Theme → i18n）

## 概述

將現有的 SettingsPanel overlay 替換為 VSCode 式的 Settings pane：左側固定分類選單 + 右側內容區，以 singleton tab 方式開啟。Daemon config 設定不在此處，歸到未來的 Host 管理頁面。

## 設計決定記錄

| 項目 | 決定 | 理由 |
|------|------|------|
| 佈局 | VSCode 式固定 sidebar + 內容區 | 擴充性好，分類清晰 |
| 分類 | Appearance、Terminal（實作）；Workspace、Sync（預留） | 先做 SPA 本地設定 |
| 路由 | `/settings/:section`，section 為頁面內 URL 同步 | 不影響 PaneContent singleton 邏輯 |
| PaneContent | `{ kind: 'settings'; scope }` 不加 section | Section 是頁面內導航 |
| 搜尋框 | 不做 | YAGNI |
| SettingsPanel | 刪除，直接替換 | Daemon config 歸 Host 管理 |
| Per-host 設定 | 不在 Settings | 歸 Host 管理頁面 |
| Cross-daemon 統一設定 | 預留 Sync 分類，不實作 | 架構預留 |

## 功能退化說明

刪除 SettingsPanel 後，以下 daemon config 設定項將暫時無 UI 可編輯，直到 Host 管理頁面完成：

- Terminal sizing mode（`terminal.sizing_mode`）
- Stream presets（`stream.presets`）
- JSONL presets（`jsonl.presets`）
- CC detect commands（`detect.cc_commands`）
- Detect poll interval（`detect.poll_interval`）

這些設定仍可透過直接編輯 `~/.config/tbox/config.toml` 或 `PUT /api/config` API 修改。此為預期行為，不是 bug。

## 設定三層架構

| 層級 | 儲存位置 | Phase 1 狀態 |
|------|---------|-------------|
| SPA 本地 | localStorage（useUISettingsStore） | 實作 |
| Workspace 作用域 | localStorage（per-workspace） | 預留分類 |
| Cross-daemon 統一 | SPA → 各 daemon 推送 | 預留分類 |

## 路由設計

### URL 結構

- `/settings` → 預設導向 `appearance`
- `/settings/appearance` → Appearance 設定
- `/settings/terminal` → Terminal 設定

### 與 useRouteSync 的整合

`tabToUrl` 對 settings 回傳 `/settings`，但實際瀏覽器 URL 可能是 `/settings/terminal`。需要避免 Tab→URL sync 把 section 路徑覆蓋掉：

1. **`parseRoute`**：擴充 `/settings` 匹配邏輯，`/settings` 和 `/settings/:section` 都回傳 `{ kind: 'settings', scope: 'global' }`。Section 資訊不進入 ParsedRoute（頁面自己 parse）。
2. **`tabToUrl`**：不改，繼續回傳 `/settings`。
3. **Tab→URL sync**：修改比對邏輯，當 `location` 以 `activeUrl` 為前綴時（如 `/settings/terminal` starts with `/settings`），不執行 replace。
4. **SettingsPage 內部**：用 `useLocation()` 讀取完整路徑，parse `/settings/` 之後的 segment 作為 active section。Section 切換呼叫 `setLocation('/settings/terminal')`。

### 路由同步流程

```
使用者點擊 sidebar "Terminal"
  → setLocation('/settings/terminal')
  → URL 更新
  → SettingsPage 偵測到 location 變更 → 切換到 TerminalSection
  → Tab→URL sync: activeUrl='/settings', location='/settings/terminal'
    → location.startsWith(activeUrl) → skip replace ✓
```

## 元件架構

### 新增

```
spa/src/components/
  SettingsPage.tsx              — 主頁面（重寫 stub，接收 PaneRendererProps）
  settings/
    SettingsSidebar.tsx         — 左側分類選單（w-48, 192px）
    AppearanceSection.tsx       — Appearance 內容
    TerminalSection.tsx         — Terminal 內容
    SettingItem.tsx             — 通用設定項 wrapper
```

### 刪除

```
spa/src/components/SettingsPanel.tsx   — 整個移除
```

### 修改

```
spa/src/App.tsx
  - 刪除 settingsOpen state
  - 刪除 SettingsPanel 渲染和 import
  - ActivityBar onOpenSettings 改為 openSingletonTab

spa/src/lib/route-utils.ts
  - parseRoute: /settings 和 /settings/:section 都回傳 settings

spa/src/hooks/useRouteSync.ts
  - Tab→URL sync: startsWith 前綴比對避免覆蓋 section
```

### SettingsPage 結構

```
┌─────────────────────────────────────────┐
│  SettingsPage (flex row, full height)   │
│  ┌──────────┬──────────────────────────┐│
│  │ Settings │                          ││
│  │ Sidebar  │   Active Section         ││
│  │ (w-48)   │   (scrollable)           ││
│  │          │                          ││
│  │ ● Appear │                          ││
│  │   Term   │                          ││
│  │          │                          ││
│  │ ─────── │                          ││
│  │   Worksp │                          ││
│  │   Sync   │                          ││
│  └──────────┴──────────────────────────┘│
└─────────────────────────────────────────┘
```

SettingsPage 接收 `PaneRendererProps`（`{ pane, isActive }`），從 `pane.content` 讀取 `scope` 判斷是 global 或 workspace settings。

### SettingItem 元件

通用設定項 wrapper，統一每個設定項的佈局。

```tsx
interface SettingItemProps {
  label: string
  description?: string
  disabled?: boolean
  children: React.ReactNode  // 控制元件 slot
}
```

佈局：左側 label + description，右側控制元件。disabled 時整體 `opacity-50` + `pointer-events-none`。

## 控制元件規格

### Segment Buttons

用於二選一或多選一的切換（Theme、Renderer）。

```
┌──────────┬──────────┐
│  Active  │ Inactive │
└──────────┴──────────┘
```

- Active：`bg-[#1e1e3e] border border-[#7a6aaa] text-gray-200`
- Inactive：`bg-transparent border border-[#404040] text-gray-500 hover:text-gray-300 hover:border-gray-600`
- 圓角：`rounded-md`，相鄰邊共用（左圓右圓）
- Cursor：`cursor-pointer`

### Toggle Switch

用於 boolean 值切換（Keep-alive Pinned）。

- Off：`bg-gray-700` track + `bg-gray-400` knob（靠左）
- On：`bg-[#7a6aaa]` track + `bg-white` knob（靠右）
- 尺寸：track `w-9 h-5`，knob `w-4 h-4`
- Transition：`transition-all duration-150`
- Cursor：`cursor-pointer`

### Number Input

- 外觀：`bg-[#2a2a2a] border border-[#404040] rounded-md text-gray-200 w-20`
- Hover：`border-gray-500`
- Focus：`border-[#7a6aaa] outline-none`

## 設定項目明細

### Appearance Section

| 設定項 | 控制元件 | 儲存 | 驗證 | 備註 |
|--------|---------|------|------|------|
| Theme | segment buttons（Dark / Light） | localStorage | — | Phase 2 實作，Phase 1 顯示 disabled UI |
| Language | select dropdown | localStorage | — | Phase 3 實作，Phase 1 顯示 disabled UI |

### Terminal Section

| 設定項 | 控制元件 | 儲存 | 驗證 | 備註 |
|--------|---------|------|------|------|
| Renderer | segment buttons（WebGL / DOM） | useUISettingsStore | — | 切換立即 bump `terminalSettingsVersion`，觸發已開啟 terminal 重連 |
| Keep-alive Count | number input | useUISettingsStore | min=0, max=10, step=1 | 0 表示不保活 |
| Keep-alive Pinned | toggle switch | useUISettingsStore | — | 僅在 keepAliveCount > 0 時有意義 |
| Reveal Delay | number input (ms) | useUISettingsStore | min=0, max=2000, step=50 | overlay 遮罩延遲 |

### Reserved Sections

Sidebar 中灰色顯示，不可點擊。Item 右側顯示 `coming soon` 小字標籤：

```
  Workspace    coming soon
  Sync         coming soon
```

- Item：`text-gray-600 cursor-not-allowed`
- Badge：`text-[10px] text-gray-600 ml-auto`

## ActivityBar 整合

```
齒輪按鈕:
  Before: onClick={() => setSettingsOpen(true)}     // SettingsPanel overlay
  After:  onClick={() => openSingletonTab(content)} // SettingsPage tab
```

ActivityBar `onOpenSettings` prop 介面不變（`() => void`），呼叫端邏輯改變。

## 樣式規範

| 元素 | 樣式 |
|------|------|
| Sidebar 背景 | `bg-[#0a0a1a]` |
| Sidebar 寬度 | `w-48`（192px） |
| Sidebar 分隔線 | `border-r border-gray-800` |
| Active item | `bg-[#1e1e3e]` + `border-l-2 border-[#7a6aaa]` + `text-gray-200` |
| Inactive item | `text-gray-400` + `cursor-pointer` + `hover:bg-white/5` |
| Reserved item | `text-gray-600` + `cursor-not-allowed` |
| Content 區背景 | 透明（繼承 App 背景） |
| Content 區 padding | `p-6` |
| Section title | `text-gray-200 text-lg` |
| Section description | `text-gray-400 text-xs mb-6` |
| SettingItem label | `text-gray-300 text-sm` |
| SettingItem description | `text-gray-400 text-xs` |
| 控制元件 | 見「控制元件規格」 |
| Disabled / Coming soon | `opacity-50` + `pointer-events-none` |

## 測試策略

- **SettingsPage**：renders sidebar + default section、section 切換、接收 PaneRendererProps
- **SettingsSidebar**：active state highlight、reserved items not clickable、click callback
- **AppearanceSection**：renders disabled theme/language items with coming soon indicator
- **TerminalSection**：renderer toggle bumps version、keepAlive 數值更新 min/max、與 useUISettingsStore 雙向同步
- **SettingItem**：renders label + description + children slot、disabled state
- **App.tsx**：齒輪按鈕開啟 settings tab（非 overlay）、SettingsPanel 不再渲染
- **路由**：`/settings` 和 `/settings/:section` 正確解析、Tab→URL 不覆蓋 section

## 不在範圍

- Theme 切換邏輯（Phase 2）
- i18n 框架（Phase 3）
- Daemon config 設定 UI（歸 Host 管理）
- Host 管理頁面和 ActivityBar 按鈕
- Workspace / Sync 設定實作
- 搜尋框
