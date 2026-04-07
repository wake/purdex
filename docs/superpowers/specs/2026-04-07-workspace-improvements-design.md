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
- `spa/src/App.tsx` — WorkspaceChip `onClick` 改為開啟設定頁

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
- **Color section**：沿用現有 `WorkspaceColorPicker`（12 色 grid）
- **Icon section**：新 Phosphor Icons Picker（見 Section 4）
- **Danger Zone**：紅色「刪除 Workspace」按鈕，觸發現有 `WorkspaceDeleteDialog`

### 進入方式

| 觸發 | 行為 |
|------|------|
| WorkspaceChip 左鍵 | 開啟該 workspace 的設定頁 |
| ActivityBar workspace 右鍵選單 | 新增「設定」項目 |
| 新增 workspace（+按鈕） | 自動開啟新 workspace 設定頁作為 creation flow |

### Singleton 行為

- 使用 `openSingletonTab({ kind: 'settings', scope: { workspaceId: wsId } })`
- `contentMatches` 已支援 scope 比對（`kind === 'settings'` + scope 相等性）
- 每個 workspace 一個 singleton，不同 workspace 的設定頁互不干擾

### 渲染整合

`SettingsPage` 已註冊為 `kind: 'settings'` 的 pane renderer，接收 `PaneRendererProps`（含 `pane.content`）。修改 `SettingsPage`：當 `content.scope` 是 `{ workspaceId }` 時，渲染 `WorkspaceSettingsPage`；`scope === 'global'` 時走現有邏輯。

路由同步（`useRouteSync.ts`）已處理 `workspace-settings` case，無需修改。

### 影響檔案

- `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx` — **新增**
- `spa/src/features/workspace/components/ActivityBar.tsx` — 右鍵選單加入「設定」
- `spa/src/features/workspace/components/WorkspaceContextMenu.tsx` — 加入「設定」項目
- `spa/src/components/SettingsPage.tsx` — scope 分流：global → 現有, workspace → WorkspaceSettingsPage
- `spa/src/App.tsx` — 新增 workspace + WorkspaceChip onClick 開啟設定頁

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

### 手動輸入

搜尋欄輸入 icon 名稱時：
1. 先從精選列表中 filter（名稱模糊匹配）
2. 若精選列表無匹配，對完整 Phosphor 匯出名稱列表進行匹配（建立靜態名稱陣列，不 dynamic import）
3. 匹配成功 → grid 顯示匹配結果，可點選
4. 確認選擇 → 存入 workspace `icon` 欄位（存 icon 名稱字串，如 `"Rocket"`）

完整名稱列表從 `@phosphor-icons/react` 的 CSR 匯出中靜態提取（build time 或手動維護），用於搜尋匹配。渲染時使用 `React.lazy(() => import(...))` 按需載入非精選 icon 元件。

### Icon 儲存格式

`Workspace.icon` 欄位從目前的 letter/emoji 字串改為 Phosphor icon 名稱字串。

- `icon: undefined` → 預設顯示 `name.charAt(0)`
- `icon: "Rocket"` → 渲染 `<Rocket />` 元件
- 向下相容：舊的 letter/emoji 值視為 fallback（單字元 → 直接顯示文字）

### 渲染輔助

新增 `renderWorkspaceIcon(icon: string | undefined, name: string)` 工具函式：
- 若 `icon` 是 Phosphor icon 名稱 → lazy render 對應元件
- 若 `icon` 是單字元 → 直接顯示文字
- 若 `icon` 是 undefined → `name.charAt(0)`

用於 ActivityBar、WorkspaceChip、WorkspaceSettingsPage 等所有顯示 workspace icon 的位置。

### 影響檔案

- `spa/src/features/workspace/components/WorkspaceIconPicker.tsx` — **重寫**
- `spa/src/features/workspace/constants.ts` — 精選 icon 列表取代舊 `WORKSPACE_ICONS`
- `spa/src/features/workspace/lib/renderWorkspaceIcon.tsx` — **新增** icon 渲染工具
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

改用 `useWorkspaceStore.getState()` 直接讀取最新 store state：

```typescript
const handleSelectWorkspace = useCallback((wsId: string) => {
  setActiveWorkspace(wsId)
  const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)
  if (ws?.activeTabId && tabs[ws.activeTabId]) setActiveTab(ws.activeTabId)
  else if (ws?.tabs[0]) setActiveTab(ws.tabs[0])
}, [setActiveWorkspace, setActiveTab, tabs])
```

額外加入 `tabs[ws.activeTabId]` 存在性檢查，防止 `activeTabId` 指向已關閉的 tab。

### 影響檔案

- `spa/src/features/workspace/hooks.ts` — 修正 `handleSelectWorkspace`

---

## 不在範圍內

- Workspace 層級偏好設定（預設 session mode、通知行為等）— 未來擴充
- WorkspaceChip 色彩渲染 tab bar — 可後續迭代
- Phosphor Icons Picker 的 weight 選擇（固定 regular weight）
