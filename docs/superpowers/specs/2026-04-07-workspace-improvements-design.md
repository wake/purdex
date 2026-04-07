# Workspace 功能改善設計

**日期**：2026-04-07
**範圍**：SPA workspace 相關 UI 與行為改善

## 概述

五項 workspace 改善：WorkspaceChip 改造、Workspace 設定頁、空 workspace empty state、Phosphor Icons Picker、workspace tab 記憶修正。

---

## 1. WorkspaceChip 改造（Dropdown Header 風格）

### 現狀問題

WorkspaceChip 和 tabs 在同一列，膠囊樣式幾乎一樣，用戶無法快速區分。

### 設計

參考 Notion/Linear 的 dropdown header 模式，WorkspaceChip 改為：

- **小方塊 icon**（20×20px, border-radius 5px）：顯示 Phosphor Icon 或 `name.charAt(0)`，背景 `color + '66'`（40% opacity）
- **粗體名稱**（font-weight 600, 13px）：workspace 顏色
- **Chevron**（CaretDown 10px）：opacity 0.3
- **分隔線**：chip 右側 1px 垂直線（`border-border-default`），與 tabs 明確分隔
- **Hover**：整個 chip 顯示 `bg-surface-hover`
- **無背景/無 border**：靜態時不帶背景色，和膠囊 tabs 徹底區分

### 互動

| 操作 | 行為 |
|------|------|
| 左鍵點擊 | 開啟 workspace 設定頁（singleton tab） |
| 右鍵 | 保留現有 context menu |

### Home 模式

`activeWorkspaceId === null` 時 chip 不顯示（現有行為不變）。

### 影響檔案

- `spa/src/features/workspace/components/WorkspaceChip.tsx` — 重寫
- `spa/src/App.tsx` — WorkspaceChip `onClick` 改為開啟設定頁（**兩處**：Electron titlebar L226 + SPA tabbar L286，邏輯相同）

---

## 2. Workspace 設定頁

### 定位

前台式單頁設定，不是系統設定風格。內容居中（max-width ~520px），section 分區，類似 Notion workspace settings 的簡潔風格。

### PaneContent

沿用現有定義：`{ kind: 'settings', scope: { workspaceId: string } }`

路由：`/w/:wsId/settings`（已定義在 `route-utils.ts`）

### 頁面佈局

```
┌─────────────────────────────────┐
│                                 │
│    [ Icon (大) ]                │
│    Workspace Name (editable)    │
│    ─────────────────────        │
│                                 │
│    ■ Color                      │
│    [ 12-色 grid picker ]        │
│    ─────────────────────        │
│                                 │
│    ■ Icon                       │
│    [ Phosphor Icons Picker ]    │
│    ─────────────────────        │
│                                 │
│    ■ Danger Zone                │
│    [ Delete Workspace ]         │
│                                 │
└─────────────────────────────────┘
```

- **頂部**：workspace icon（大尺寸，如 48px）+ 名稱 inline editable（click-to-edit 或直接 input field）
- **Color section**：從現有 `WorkspaceColorPicker` 抽出 inline `ColorGrid` 子元件（現有元件是 modal overlay `fixed inset-0 z-50`，無法直接 inline 使用）。Context menu 繼續用 modal 版，設定頁用 inline 版。
- **Icon section**：新 Phosphor Icons Picker（見 Section 4），同樣需 inline 版本
- **Danger Zone**：紅色「刪除 Workspace」按鈕，觸發現有 `WorkspaceDeleteDialog`

### 進入方式

| 觸發 | 行為 |
|------|------|
| WorkspaceChip 左鍵 | 開啟該 workspace 的設定頁 |
| ActivityBar workspace 右鍵選單 | 新增「設定」項目 |
| 新增 workspace（+按鈕） | 自動開啟新 workspace 設定頁作為 creation flow |

**新增 workspace 與 MigrateTabsDialog 的衝突處理**：首次建立 workspace 時若有現有 tabs，現有流程會彈出 MigrateTabsDialog。順序為：先完成 MigrateDialog 流程（migrate 或 skip），再自動開啟設定頁。

### Singleton 行為

- 使用 `openSingletonTab({ kind: 'settings', scope: { workspaceId: wsId } })`
- `contentMatches` 已支援 scope 比對（`kind === 'settings'` + scope 相等性）
- 每個 workspace 一個 singleton，不同 workspace 的設定頁互不干擾

### 渲染整合

`SettingsPage` 已註冊為 `kind: 'settings'` 的 pane renderer，接收 `PaneRendererProps`（含 `pane.content`）。目前 `_props` 被丟棄。修改方式：

1. 從 `props.pane.content` 解構 `scope`
2. `scope === 'global'` → 現有 global settings 邏輯（sidebar + sections）
3. `scope` 是 `{ workspaceId }` → 渲染 `WorkspaceSettingsPage`
4. **注意**：module-level `lastSection` 變數僅用於 global settings，workspace settings 不受影響（兩者渲染路徑完全獨立）

路由同步（`useRouteSync.ts`）已處理 `workspace-settings` case，但**缺少 `setActiveWorkspace` 呼叫**。需補修：開啟 workspace settings 時同步激活該 workspace，確保 ActivityBar 顯示正確的 active state。

### 影響檔案

- `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx` — **新增**
- `spa/src/features/workspace/components/ActivityBar.tsx` — 右鍵選單加入「設定」
- `spa/src/features/workspace/components/WorkspaceContextMenu.tsx` — 加入「設定」項目（新增 `onSettings` callback prop）
- `spa/src/components/SettingsPage.tsx` — scope 分流：global → 現有, workspace → WorkspaceSettingsPage
- `spa/src/App.tsx` — 新增 workspace + WorkspaceChip onClick 開啟設定頁 + WorkspaceContextMenu `onSettings` 接線
- `spa/src/hooks/useRouteSync.ts` — `workspace-settings` case 補 `setActiveWorkspace`

---

## 3. 空 Workspace Empty State

### 行為

切換到沒有 tab 的 workspace 時，content area 顯示居中 empty state，不自動建立任何 tab。

### 設計

- 居中容器，簡潔文案（如「No tabs in this workspace」）
- 引導操作提示（如「Press + to create a tab」）
- 風格與現有 new-tab 頁或 dashboard 的空狀態一致

### 實作位置

在 `App.tsx` 的 content area，當 `visibleTabIds.length === 0 && activeWorkspaceId !== null` 時渲染 empty state 元件。不需要新的 PaneContent kind。

**注意**：若 workspace 內有 singleton tab（如 settings 頁），`visibleTabIds` 不為空，empty state 不顯示。這是預期行為 — 有 tab 就不是空 workspace。

### 影響檔案

- `spa/src/features/workspace/components/WorkspaceEmptyState.tsx` — **新增**
- `spa/src/App.tsx` — 條件渲染

---

## 4. Phosphor Icons Picker

### 設計

取代現有 `WorkspaceIconPicker`（26 個 letter/emoji），改用 Phosphor Icons。

### 結構

```
┌─────────────────────────────────────┐
│ 🔍 Search / type icon name...      │
├─────────────────────────────────────┤
│ ▸ General     ▸ Development         │
│ ▸ Objects     ▸ Communication       │
│ ▸ Media       ▸ Arrows              │
│ ▸ Nature      ▸ Business            │
├─────────────────────────────────────┤
│ ┌──┬──┬──┬──┬──┬──┬──┬──┐          │
│ │🏠│⭐│❤ │🔔│📁│🔧│⚡│🎯│ ...     │
│ └──┴──┴──┴──┴──┴──┴──┴──┘          │
│ (grid, scrollable)                  │
├─────────────────────────────────────┤
│ [✕ Clear icon]                      │
└─────────────────────────────────────┘
```

- **搜尋欄**：輸入關鍵字過濾精選列表，或輸入完整 icon 名稱（如 `Rocket`）使用完整 Phosphor 庫的任意 icon
- **分類 tabs**：5-8 個類別，每類 ~100 個精選 icon
- **Icon grid**：6-8 欄，每個 icon 32×32px，hover 顯示名稱 tooltip
- **選中態**：accent 背景 + ring
- **清除**：移除 icon，回到預設（顯示 `name.charAt(0)`）

### 分類規劃（精選 ~600 icons）

| 分類 | 範例 |
|------|------|
| General | House, Star, Heart, Bell, BookmarkSimple, Flag, Lightning |
| Development | Terminal, Code, GitBranch, Bug, Database, CloudArrowUp |
| Objects | Folder, File, Clipboard, Book, Key, Lock, Wrench |
| Communication | ChatCircle, Envelope, Phone, Megaphone, Bell |
| Media | Play, Camera, MusicNote, Image, VideoCamera |
| Arrows & Navigation | ArrowRight, CaretDown, Compass, MapPin, Signpost |
| Nature & Weather | Sun, Moon, Tree, Leaf, Cloud, Drop, Snowflake |
| Business | ChartBar, Calendar, Money, Briefcase, Buildings |

### 兩層架構：精選（靜態 import）+ 完整庫（Vite glob import）

**第一層：精選 icon（~600 個）**
- 全部靜態 import 並 re-export 為 `Record<string, Icon>` map
- 打包進主 bundle，無 lazy loading，確保 picker grid 即時渲染
- 分類 tabs 僅顯示精選 icon

**第二層：完整 Phosphor 庫（~1500 個 icon name）**
- 使用 Vite glob import 建立 lazy map：
  ```typescript
  const allIcons = import.meta.glob(
    '/node_modules/@phosphor-icons/react/dist/csr/*.mjs',
    { import: 'default', eager: false }
  )
  ```
- 這讓 Vite 在 build time 靜態分析所有 entry，自動 code-split 為獨立 chunks
- 搜尋欄輸入 icon 名稱時，從 glob map 的 key 提取名稱進行模糊匹配
- 選中後 lazy load 該 icon 的 chunk

**注意**：精選列表需手動維護，這是有意的取捨 — 確保精選品質且不引入自動化複雜度。

### 搜尋行為

搜尋欄輸入 icon 名稱時：
1. 先從精選列表中 filter（名稱模糊匹配）
2. 同時從完整 glob map key 列表中匹配
3. 精選結果優先顯示，後接完整庫結果（去重）
4. Grid 顯示匹配結果，精選 icon 即時渲染，非精選 icon 顯示 loading placeholder 後 lazy 載入
5. 確認選擇 → 存入 workspace `icon` 欄位（存 icon 名稱字串，如 `"Rocket"`）

### Icon 儲存格式

`Workspace.icon` 欄位從目前的 letter/emoji 字串改為 Phosphor icon 名稱字串。

- `icon: undefined` → 預設顯示 `name.charAt(0)`
- `icon: "Rocket"` → 渲染 `<Rocket />` 元件
- 向下相容：舊的 letter/emoji 值視為 fallback（單字元 → 直接顯示文字）

### 渲染輔助

新增 `WorkspaceIcon` React 元件（非純函式，因需管理 Suspense）：

```tsx
<WorkspaceIcon icon={ws.icon} name={ws.name} size={18} />
```

邏輯：
- `icon` 是 undefined → 顯示 `name.charAt(0)` 文字
- `icon` 是單字元（legacy letter/emoji） → 直接顯示文字
- `icon` 是 Phosphor icon 名稱 → 先查精選 map（同步），miss 則從 glob map lazy load
- **Suspense fallback**：顯示 `name.charAt(0)` 文字（與 undefined 一致），載入完成後自動替換為 icon。不使用 spinner，避免 ActivityBar 閃爍。
- 每個使用處自帶 `<Suspense>` boundary（ActivityBar button、WorkspaceChip、SettingsPage header）

用於 ActivityBar、WorkspaceChip、WorkspaceSettingsPage 等所有顯示 workspace icon 的位置。

### 影響檔案

- `spa/src/features/workspace/components/WorkspaceIconPicker.tsx` — **重寫**
- `spa/src/features/workspace/constants.ts` — 精選 icon 列表取代舊 `WORKSPACE_ICONS`
- `spa/src/features/workspace/components/WorkspaceIcon.tsx` — **新增** icon 元件（含 Suspense）
- `spa/src/features/workspace/lib/icon-map.ts` — **新增** 精選 static map + glob lazy map
- `spa/src/features/workspace/components/ActivityBar.tsx` — 使用 `renderWorkspaceIcon`
- `spa/src/features/workspace/components/WorkspaceChip.tsx` — 使用 `renderWorkspaceIcon`

---

## 5. Workspace Tab 記憶修正

### 現狀問題

切換 workspace 時，不一定回到該 workspace 上次瀏覽的 tab，可能跳錯。

### 根因分析

`handleSelectWorkspace`（`hooks.ts:29-34`）從 React hook closure 的 `workspaces` 讀取 `activeTabId`：

```typescript
const handleSelectWorkspace = useCallback((wsId: string) => {
  setActiveWorkspace(wsId)
  const ws = workspaces.find((w) => w.id === wsId)  // ← closure 可能 stale
  if (ws?.activeTabId) setActiveTab(ws.activeTabId)
  else if (ws?.tabs[0]) setActiveTab(ws.tabs[0])
}, [workspaces, setActiveWorkspace, setActiveTab])
```

`workspaces` 來自 `useWorkspaceStore((s) => s.workspaces)` 的 subscription，在同一 render cycle 內可能是上一次的值。

### 修正

改用 `getState()` 直接讀取最新 store state（workspace 和 tabs 皆從 store 即時讀取，不依賴 closure）：

```typescript
const handleSelectWorkspace = useCallback((wsId: string) => {
  setActiveWorkspace(wsId)
  const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)
  const allTabs = useTabStore.getState().tabs
  if (ws?.activeTabId && allTabs[ws.activeTabId]) setActiveTab(ws.activeTabId)
  else if (ws?.tabs[0]) setActiveTab(ws.tabs[0])
}, [setActiveWorkspace, setActiveTab])
```

重點：
- `workspaces` 和 `tabs` 都從 `getState()` 讀取，deps 僅保留 stable setter
- `allTabs[ws.activeTabId]` 存在性檢查，防止 `activeTabId` 指向已關閉的 tab

### 影響檔案

- `spa/src/features/workspace/hooks.ts` — 修正 `handleSelectWorkspace`

---

## 不在範圍內

- Workspace 層級偏好設定（預設 session mode、通知行為等）— 未來擴充
- WorkspaceChip 色彩渲染 tab bar — 可後續迭代
- Phosphor Icons Picker 的 weight 選擇（固定 regular weight）
