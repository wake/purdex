# Tab / Session 解耦 + URL Routing 重構設計

**日期**: 2026-03-24
**狀態**: Draft
**範圍**: SPA 前端架構重構 — Tab/Pane 模型、path-based routing、history/settings tab types
**前置**: Phase 1.6b 完成（PR #68）
**分支**: v1（破壞式開發，不相容舊版 persist）

---

## 1. 設計目標

將 Tab 從 Session 的 1:1 容器解耦為獨立的通用容器，內部以 Pane 承載各種內容類型。同時將 hash routing 遷移至 path-based routing（wouter），建立未來支援 split view、多 tab types、Electron tear-off 的基礎架構。

### 核心原則

- Tab 是獨立容器，Session 只是 Pane 的一種內容類型
- 使用者手動管理 tabs，不自動同步 daemon session 列表
- URL 採用 path-based routing（wouter），帶 `/t/` `/w/` prefix 避免歧義
- URL 自足原則：任何合法 URL 都能獨立解析並渲染，不依賴預存 store 狀態
- 架構預留 tab 內 split view、sidebar 4 zones、Electron 多視窗
- 破壞式開發，不考慮向下相容

---

## 2. Tab + Pane 模型

### 概念對應

| tmux 概念 | SPA 概念 | 說明 |
|---|---|---|
| window | Tab | tab bar 上的單位 |
| pane | Pane | tab 內的內容槽 |
| session | PaneContent.session | pane 的一種內容類型 |

### ID 生成

Tab ID 和 Pane ID 統一使用 6 碼隨機 base36（`[0-9a-z]`），空間 36^6 ≈ 21 億。Workspace ID 同樣改為 6 碼。

```ts
function generateId(): string {
  const chars = '0123456789abcdefghijklmnopqrstuvwxyz'
  const bytes = crypto.getRandomValues(new Uint8Array(6))
  let id = ''
  for (const b of bytes) id += chars[b % 36]
  return id
}
```

### 型別定義

```ts
// === Tab（tab bar 的單位）===
interface Tab {
  id: string           // 6-char base36
  pinned: boolean
  locked: boolean
  createdAt: number
  layout: PaneLayout
}

// === Pane Layout（tab 內部分割樹）===
type PaneLayout =
  | { type: 'leaf'; pane: Pane }
  | { type: 'split'; direction: 'h' | 'v'
      children: PaneLayout[]; sizes: number[] }

// === Pane（內容槽）===
interface Pane {
  id: string           // 6-char base36
  content: PaneContent
}

// === Pane Content（內容類型，discriminated union）===
type PaneContent =
  | { kind: 'session'; sessionCode: string; mode: 'terminal' | 'stream' }
  | { kind: 'dashboard' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' | { workspaceId: string } }
  // 未來預留：
  // | { kind: 'tmux-pane'; sessionCode: string; paneIndex: number }
  // | { kind: 'editor'; hostId: string; filePath: string }
```

### Singleton 規則

| kind | 規則 | 判斷方式 |
|---|---|---|
| dashboard | 全域唯一 | 建立前掃所有 tab 的所有 pane |
| history | 全域唯一 | 同上 |
| settings (global) | 全域唯一 | 同上 |
| settings (workspace) | 每 workspace 唯一 | 掃該 workspace 的 tabs |
| session | 不限 | 同一 session 可開多個 pane |

### Pane Registry

取代現有 tab-registry，key 改為 `pane.content.kind`：

```ts
registerPaneRenderer('session', { component: SessionPaneContent })
registerPaneRenderer('dashboard', { component: DashboardPage })
registerPaneRenderer('history', { component: HistoryPage })
registerPaneRenderer('settings', { component: SettingsPage })
```

---

## 3. Store 架構

### useTabStore（重構）

```ts
interface TabState {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null

  createTab(layout: PaneLayout, opts?: { pinned?: boolean }): Tab
  closeTab(id: string): void
  setActiveTab(id: string): void
  setViewMode(tabId: string, paneId: string, mode: string): void
  splitPane(tabId: string, paneId: string, direction: 'h' | 'v', content: PaneContent): void
  closePane(tabId: string, paneId: string): void
  reorderTabs(newOrder: string[]): void
  togglePin(id: string): void
  toggleLock(id: string): void
}
```

與現有差異：
- 移除 `dismissedSessions`
- 移除 `createSessionTab()` → 通用 `createTab(layout)`
- 新增 `splitPane()` / `closePane()`
- `setViewMode` 需要 `paneId`

### useWorkspaceStore（調整）

```ts
interface Workspace {
  id: string            // 6-char base36
  name: string
  color: string
  icon?: string
  tabs: string[]        // tab IDs（有序）
  activeTabId: string
}
```

Workspace 管 tab IDs，不管 pane 細節。

### useHistoryStore（新增）

```ts
interface BrowseRecord {
  tabId: string
  paneContent: PaneContent
  visitedAt: number
}

interface ClosedTabRecord {
  tab: Tab                    // 完整快照（含 layout）
  closedAt: number
  fromWorkspaceId?: string
  reopened: boolean
}

interface HistoryState {
  browseHistory: BrowseRecord[]       // 上限 500 筆
  closedTabs: ClosedTabRecord[]       // 上限 100 筆

  recordVisit(tabId: string, content: PaneContent): void
  recordClose(tab: Tab, workspaceId?: string): void
  reopenTab(index: number): Tab       // 沿用原 tab id + pane ids
  clearBrowseHistory(): void
  clearClosedTabs(): void
}
```

瀏覽紀錄與關閉紀錄是分開的兩件事：
- **瀏覽紀錄**：顯示在 History 頁面，記錄每次 tab 訪問
- **關閉紀錄**：不顯示在 UI，只供 `⌘+Shift+T` reopen 使用

Reopen 時沿用原 tab ID 和 pane IDs，讓書籤持續有效。

### 不動的 stores

- `useSessionStore` — daemon session 列表 cache，維持原樣
- `useStreamStore` — per-session stream 狀態，維持原樣
- `useHostStore` — 連線資訊，維持原樣
- `useConfigStore` — daemon 配置，維持原樣
- `useUISettingsStore` — UI 設定，維持原樣

### 移除項目

- `useSessionTabSync` hook — 自動建 tab 邏輯刪除
- `dismissedSessions` — 不再需要
- `tab.data` bag — 被 `Pane.content` 取代
- `tab-helpers.ts`（`getSessionName`, `getSessionCode`）— 不再需要

---

## 4. 路由系統

### 依賴

```bash
pnpm add wouter
```

### URL 結構

| 路徑 | 對應 | Singleton |
|---|---|---|
| `/` | Dashboard | 是 |
| `/history` | History 頁面 | 是 |
| `/settings` | 全域設定 | 是 |
| `/t/:tabId/:mode` | Session tab（standalone） | 否 |
| `/w/:workspaceId` | Workspace 入口 → active tab | — |
| `/w/:workspaceId/settings` | 工作區設定 | 每 workspace |
| `/w/:workspaceId/t/:tabId/:mode` | Workspace 內 session tab | 否 |

### 無歧義保證

- `/t/` 和 `/w/` prefix 立刻區分 tab 和 workspace
- `/history`、`/settings` 是保留字，不與 prefix 衝突
- 每個路徑的 segment 數量和 prefix 都不同

### wouter Routes

```tsx
<Route path="/history" />
<Route path="/settings" />
<Route path="/t/:tabId/:mode" />
<Route path="/w/:workspaceId" />
<Route path="/w/:workspaceId/settings" />
<Route path="/w/:workspaceId/t/:tabId/:mode" />
<Route path="/" />
```

### 雙向同步（Tab-driven）

Tab store 是 source of truth，URL 是 active tab 的投影。

```
Tab activate → URL update（replace）
URL change  → Tab 查找/建立 → activate
```

**URL 自足原則**：直接訪問任何合法 URL 時，即使 tab store 是空的：
1. 解析 URL 得到 PaneContent
2. 搜尋現有 tabs 有沒有匹配的
3. 有 → activate
4. 沒有 → 建立新 tab → activate

**Split 狀態不編碼在 URL 中**，URL 只反映 active tab 的 primary pane（第一個 leaf）。Split 靠 persist 恢復。

### Vite 設定

Vite dev server 預設啟用 SPA fallback。部署時需設定 `try_files $uri /index.html`。

---

## 5. 介面佈局架構

### 完整佈局

```
┌──────┬──────┬──────────────────────────────┬──────┬──────┐
│      │ 左   │         TabBar               │      │ 右   │
│ Act  │ tab  ├──────┬───────────────┬──────┤      │ tab  │
│ Bar  │ 外   │ 左   │  Tab Content   │ 右   │      │ 外   │
│      │      │ tab  │ ┌──────┬─────┐│ tab  │      │      │
│      │      │ 內   │ │Pane A│Pane B││ 內   │      │      │
│      │      │      │ │      │     ││      │      │      │
│      │      │      │ └──────┴─────┘│      │      │      │
├──────┴──────┴──────┴───────────────┴──────┴──────┴──────┤
│  StatusBar                                                │
└───────────────────────────────────────────────────────────┘
```

### 兩種面板概念（正交）

| 概念 | 所在層級 | 行為 | 命名 |
|---|---|---|---|
| **Pane** | Tab 內部 | 內容分割，隨 tab 切換出現/消失 | `Pane` |
| **Sidebar Zone** | App 層級 | 輔助面板，4 區域固定位置 | `SidebarZone` |

它們完全獨立：
- 切換 tab → Pane 跟著換，Sidebar 不動（除非 workspace context 變）
- 開關 sidebar → 內容區域伸縮，Pane 內的 split 比例不變

### Sidebar Zone 架構（預留，此次不實作）

```ts
type SidebarZoneId = 'left-outer' | 'left-inner' | 'right-inner' | 'right-outer'

interface SidebarZoneState {
  activePanelId?: string
  width: number
  mode: 'fixed' | 'default' | 'collapsed'
}
```

| Zone | 垂直範圍 | 層級 | 預設面板 |
|---|---|---|---|
| left-outer | 全高（與 TabBar 並列） | 系統級 | Sessions |
| left-inner | TabBar 下方 | 工作區級 | 目錄 |
| right-inner | TabBar 下方 | 工作區級 | — |
| right-outer | 全高（與 TabBar 並列） | 系統級 | — |

### Pane 分割渲染

```tsx
function PaneLayoutRenderer({ layout }: { layout: PaneLayout }) {
  if (layout.type === 'leaf') {
    const renderer = getPaneRenderer(layout.pane.content.kind)
    return <renderer.component pane={layout.pane} />
  }

  return (
    <SplitContainer direction={layout.direction} sizes={layout.sizes}>
      {layout.children.map((child, i) => (
        <PaneLayoutRenderer key={i} layout={child} />
      ))}
    </SplitContainer>
  )
}
```

### 元件層級

```
App
├─ ActivityBar
├─ SidebarZone (left-outer)          ← 未來
├─ MainArea
│  ├─ TabBar
│  ├─ ContentArea
│  │  ├─ SidebarZone (left-inner)    ← 未來
│  │  ├─ TabContent
│  │  │  └─ PaneLayoutRenderer
│  │  │     ├─ SessionPaneContent
│  │  │     ├─ DashboardPage
│  │  │     ├─ HistoryPage
│  │  │     └─ SettingsPage
│  │  └─ SidebarZone (right-inner)   ← 未來
│  └─ StatusBar
└─ SidebarZone (right-outer)         ← 未來
```

---

## 6. 關閉與 Reopen

### 關閉流程

```
使用者關閉 tab
  → historyStore.recordClose(tab, workspaceId?)
  → tabStore.closeTab(id)
  → workspaceStore.removeTab(workspaceId, id)
  → activate 相鄰 tab 或 dashboard
```

### Reopen 流程

```
使用者 reopen（⌘+Shift+T 或程式呼叫）
  → historyStore 取出最近的未重開記錄
  → 直接把快照的 tab（含原 id + 原 pane ids）放回 tabStore
  → 加回原 workspace（如果還存在）或 standalone
  → activate
  → 標記記錄為 reopened
```

沿用原 ID，讓書籤持續有效。

### 快捷鍵

| 動作 | 快捷鍵 |
|---|---|
| Reopen last closed tab | `⌘+Shift+T` |

### 持久化

`useHistoryStore` persist 到 localStorage。瀏覽紀錄上限 500 筆、關閉紀錄上限 100 筆，超過從最舊丟棄。

---

## 7. Electron 相容性

### 基本相容

- SPA 獨立於 daemon → BrowserWindow 直接載入
- wouter 用 `window.location`，每個 BrowserWindow 各自獨立
- Zustand persist 用 localStorage，Electron 支援

### Tab tear-off（拖曳成新視窗）

架構天然支援，因為：

1. **Tab 是純資料物件** — 直接 `JSON.stringify` 跨視窗傳遞
2. **WS 連線不在 store 裡** — 由 component mount 時建立，新視窗自動建新連線
3. **每個視窗獨立連 daemon** — 不需跨視窗共享連線狀態

未來需補：Electron main process 的 WindowManager 協調層，避免兩個視窗同時操作同一個 tab。不影響現在的資料模型。

---

## 8. 此次實作範圍

### 實作

| 項目 | 說明 |
|---|---|
| Tab + Pane 模型 | 新型別定義、ID 生成 |
| useTabStore 重構 | 新 Tab 模型 + createTab/closeTab/splitPane/closePane |
| useHistoryStore | 瀏覽紀錄 + 關閉紀錄 + reopen |
| wouter 路由 | path-based routing + useRouteSync 雙向同步 |
| Pane Registry | 取代 tab-registry |
| PaneLayoutRenderer | leaf only（單 pane 渲染） |
| DashboardPage | 暫空 |
| HistoryPage | 瀏覽紀錄列表 |
| SettingsPage | 暫空 |
| SessionPaneContent | 從 SessionTabContent 重構 |
| TabBar / StatusBar | 適配新模型 |
| 移除 useSessionTabSync | 不再自動建 tab |

### 架構預留（不實作）

| 項目 | 說明 |
|---|---|
| SplitContainer | tab 內分割渲染容器 |
| Sidebar 4 zones | 側欄面板框架 |
| Electron WindowManager | 多視窗協調 |
| tmux-pane content type | 個別 tmux pane 連線 |

---

## 9. 檔案異動

### 刪除

| 檔案 | 原因 |
|---|---|
| `spa/src/lib/hash-routing.ts` | 被 wouter 取代 |
| `spa/src/hooks/useHashRouting.ts` | 被 useRouteSync 取代 |
| `spa/src/lib/parseHash.test.ts` | 被新路由測試取代 |
| `spa/src/hooks/useSessionTabSync.ts` | 自動建 tab 移除 |
| `spa/src/lib/tab-helpers.ts` | 不再需要 |
| `spa/src/lib/tab-registry.ts` | 被 pane-registry 取代 |
| `spa/src/lib/register-builtins.tsx` | 被新的 pane 註冊取代 |

### 新增

| 檔案 | 用途 |
|---|---|
| `spa/src/hooks/useRouteSync.ts` | wouter 路由雙向同步 |
| `spa/src/stores/useHistoryStore.ts` | 瀏覽紀錄 + 關閉紀錄 |
| `spa/src/components/PaneLayoutRenderer.tsx` | Pane tree 遞迴渲染 |
| `spa/src/components/DashboardPage.tsx` | Dashboard（暫空） |
| `spa/src/components/HistoryPage.tsx` | 瀏覽紀錄頁面 |
| `spa/src/components/SettingsPage.tsx` | 設定頁面（暫空） |
| `spa/src/components/SessionPaneContent.tsx` | Session pane（重構自 SessionTabContent） |
| `spa/src/lib/pane-registry.ts` | Pane renderer 註冊 |
| `spa/src/lib/route-utils.ts` | URL 解析 / 生成工具 |
| `spa/src/lib/id.ts` | 6-char base36 ID 生成 |

### 修改

| 檔案 | 改動 |
|---|---|
| `spa/src/types/tab.ts` | Tab / Pane / PaneLayout / PaneContent 新型別 |
| `spa/src/stores/useTabStore.ts` | 新 Tab 模型 + pane 操作 |
| `spa/src/stores/useWorkspaceStore.ts` | workspace.id 改 6-char |
| `spa/src/components/TabContent.tsx` | 改用 PaneLayoutRenderer |
| `spa/src/components/StatusBar.tsx` | 從 active pane 取資訊 |
| `spa/src/components/App.tsx` | wouter Router + 移除 SessionPicker |
| `spa/src/components/TabBar.tsx` | tab label 從 pane content 推導 |
| `vite.config.ts` | 確認 SPA fallback |
| `package.json` | 新增 wouter 依賴 |

### 依賴變更

```bash
pnpm add wouter
```
