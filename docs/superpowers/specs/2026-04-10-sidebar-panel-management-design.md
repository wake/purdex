# Sidebar / Panel 管理機制設計

## 背景

目前 SidebarRegion 只有 pinned/collapsed/hidden 三態切換，view 的分配在 `main.tsx` 硬寫。用戶無法自行決定哪些 view 出現在哪個 region，也無法排序。

## 需求

1. **展開/收合**：region 自身控制 pinned ↔ collapsed
2. **顯示/隱藏**：TitleBar 控制 hidden ↔ 還原（記住隱藏前是 pinned 或 collapsed）
3. **管理**：用戶可在每個 region 啟用/停用 view、拖曳排序

## View Scope 三層模型

Module 註冊 view 時宣告 scope，決定 view 內容隨什麼切換：

| scope | 行為 | context 傳入 | 範例 |
|-------|------|-------------|------|
| `system` | 跨 workspace 一致 | 無 | 全域狀態面板 |
| `workspace` | 跟 workspace 走，跨 tab 一致 | workspaceId | Workspace 資訊面板 |
| `tab` | 跟 tab 走 | workspaceId + tabId | Files (Session) |

Pane 層級不在此系統管理，由 tab content 自行處理。

`tabId` 來源為全域 `useTabStore.activeTabId`。scope 為 `tab` 的 view 永遠顯示當前 active tab 的內容，不論 region layout 是全域的。

## ViewDefinition 變更

```typescript
// Before
interface ViewDefinition {
  id: string
  label: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  scope: 'system' | 'workspace'
  defaultRegion: SidebarRegion
  component: React.ComponentType<ViewProps>
}

// After
interface ViewDefinition {
  id: string
  label: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  scope: 'system' | 'workspace' | 'tab'
  component: React.ComponentType<ViewProps>
}
```

- 移除 `defaultRegion`：view 不再綁定特定 region
- `scope` 加入 `'tab'`

### ViewProps 變更

```typescript
// Before
interface ViewProps {
  hostId?: string
  workspaceId?: string
  isActive: boolean
  region?: SidebarRegion
}

// After
interface ViewProps {
  hostId?: string
  workspaceId?: string
  tabId?: string
  isActive: boolean
  region?: SidebarRegion
}
```

新增 `tabId`，scope 為 `tab` 的 view 需要知道當前 active tab。

注意：`FileTreeSessionView` 目前是 placeholder，不消費任何 prop。scope 改 `'tab'` 為語意標記，不需同步改元件。

## RegionState 變更

```typescript
// Before
interface RegionState {
  views: string[]
  activeViewId?: string
  width: number
  mode: 'pinned' | 'collapsed' | 'hidden'
}

// After
interface RegionState {
  views: string[]
  activeViewId?: string
  width: number
  mode: 'pinned' | 'collapsed' | 'hidden'
  previousMode?: 'pinned' | 'collapsed'
}
```

新增 `previousMode`：`toggleVisibility` 隱藏時記錄當前 mode，還原時恢復。

## toggleVisibility 行為修正

```typescript
// Before: hidden → pinned, else → hidden
toggleVisibility: (region) =>
  set((state) => {
    const current = state.regions[region].mode
    const next = current === 'hidden' ? 'pinned' : 'hidden'
    return updateRegion(state, region, { mode: next })
  })

// After: hidden → previousMode, else → hidden (記住當前 mode)
toggleVisibility: (region) =>
  set((state) => {
    const { mode, previousMode } = state.regions[region]
    if (mode === 'hidden') {
      return updateRegion(state, region, {
        mode: previousMode ?? 'pinned',
        previousMode: undefined,
      })
    }
    return updateRegion(state, region, {
      mode: 'hidden',
      previousMode: mode,
    })
  })
```

注意：`toggleRegion`（pinned ↔ collapsed）不需處理 `hidden` 狀態。hidden 時 SidebarRegion 不渲染任何 UI，collapsed bar 和 collapse 按鈕都不存在，`toggleRegion` 不可能被觸發。

## Module Registry 變更

### 移除的函式

- `getViewsByRegion(region, scope?)` — 不再需要，region 不綁定 view。同步刪除 `module-registry.test.ts` 中對應測試。

### 新增的函式

```typescript
export function getAllViews(): ViewDefinition[] {
  return [...modules.values()].flatMap((m) => m.views ?? [])
}
```

### register-modules.tsx 變更

`files` module 的兩個 view 移除 `defaultRegion`，`file-tree-session` 的 scope 改為 `'tab'`：

```typescript
views: [
  {
    id: 'file-tree-workspace',
    label: 'Files (Workspace)',
    icon: FolderOpen,
    scope: 'workspace',
    component: FileTreeWorkspaceView,
  },
  {
    id: 'file-tree-session',
    label: 'Files (Session)',
    icon: FolderOpen,
    scope: 'tab',
    component: FileTreeSessionView,
  },
]
```

## 管理 UI

### 入口（三個）

1. **Pinned header ⚙ 按鈕**：region 展開時，header 右側（收合箭頭左邊）顯示齒輪 icon，點擊切換到管理畫面
2. **Collapsed bar + 按鈕**：region 收合時，icon bar 底部顯示 + 按鈕，點擊先展開 region 再切換到管理畫面
3. **右鍵 context menu**：header 和 collapsed bar 都支援右鍵，彈出 checkbox 清單可快速開關 view（不進管理畫面）

### 管理畫面（替換 Region 內容）

點 ⚙ 或 + 後，region 內容整個替換為管理畫面。⚙ 按鈕呈 active 狀態，再點一次或點「完成」回到正常 view。

管理模式由 SidebarRegion 內部 `useState<boolean>` 控制，不需持久化。切換 tab/workspace 或頁面重整會自動關閉管理模式，此為預期行為。

**管理畫面 layout**：

```
┌─────────────────────────┐
│ [tab1] [tab2]    [⚙] [◂]│  ← ⚙ active 狀態
├─────────────────────────┤
│ 已啟用                    │
│ ┌─ ☑ 📁 Files (Wks)  ⠿ ─┐│
│ │─ ☑ 📁 Files (Ses)  ⠿ ─││  ← 可拖曳排序
│ └────────────────────────┘│
│                           │
│ 可加入                    │
│ ┌─ 📜 History      [+] ─┐│
│ │─ 🔧 Debug        [+] ─││  ← 點 + 加入
│ └────────────────────────┘│
└─────────────────────────┘
```

- **已啟用區**：列出目前 region 的 views，每行顯示 icon + label + drag handle (⠿)，可拖曳排序，點 checkbox 可移除
- **可加入區**：列出所有已註冊但不在此 region 的 view，每行顯示 icon + label + [+] 按鈕
- 同一個 view 可同時出現在多個 region（加入不會從其他 region 移除）
- 所有 view 都移除後，管理畫面仍然顯示（已啟用區為空，可加入區列出全部 view）

### Region 空狀態

**views 為空時 SidebarRegion 不再 return null**（移除 `views.length === 0` 的 early return）。空 region 的行為：

- **Pinned**：顯示 header（只有 ⚙ 和收合按鈕，無 tab），內容區顯示空狀態提示（如「點擊 ⚙ 加入 views」）
- **Collapsed**：顯示 collapsed bar，只有底部 + 按鈕（無 icon 因為沒有 view）
- **Hidden**：仍然不渲染（TitleBar 按鈕仍可開啟）

TitleBar 的 `visibleToggles` filter 也要同步修改：移除 `views.length > 0` 的過濾條件，改為始終顯示所有 region 的 toggle 按鈕。

### Context Menu

右鍵 header 或 collapsed bar 時：

```
┌──────────────────────┐
│ Views                │
│ ☑ Files (Workspace)  │
│ ☑ Files (Session)    │
│ ☐ History            │
│ ☐ Debug              │
└──────────────────────┘
```

- Checkbox 清單，列出所有已註冊 view
- 已啟用的在前（依 region 中的啟用順序），未啟用的在後（依 registry 順序）
- 點擊切換啟用/停用
- 不支援排序（排序用管理畫面）

## Layout Store 新增 Actions

```typescript
interface LayoutState {
  // ... existing
  addView: (region: SidebarRegion, viewId: string) => void
  removeView: (region: SidebarRegion, viewId: string) => void
  reorderViews: (region: SidebarRegion, views: string[]) => void
}
```

- `addView`：將 viewId 加到 region 的 views 尾端，若已存在則忽略
- `removeView`：從 region 的 views 移除 viewId，若移除的是 activeViewId 則重設為第一個（若為空則 undefined）
- `reorderViews`：設定排序後的 views 陣列。傳入陣列必須是原 views 的排列（相同元素集合），多餘的 id 丟棄、缺少的 id 補回尾端。

## SidebarRegion 元件變更

### 管理模式狀態

SidebarRegion 內部新增 `useState<boolean>` 控制是否顯示管理畫面。此狀態不需持久化。

### Props 傳入 tabId

scope 為 `tab` 的 view 需要 `tabId`。SidebarRegion 已經從 store 拿 `activeWorkspaceId`，同樣方式拿 `activeTabId`：

```typescript
const activeTabId = useTabStore((s) => s.activeTabId)
```

渲染 view component 時：

```typescript
<ActiveComponent
  isActive={true}
  region={region}
  workspaceId={activeWorkspaceId ?? undefined}
  hostId={activeHostId || undefined}
  tabId={activeTabId ?? undefined}
/>
```

### Header 新增 ⚙ 按鈕

在收合箭頭左邊加入 GearSix icon 按鈕（Phosphor Icons），點擊切換管理模式。

### Collapsed Bar 新增 + 按鈕

在 icon 列表下方（`flex-1` spacer 後）加入 Plus icon 按鈕，點擊後先設 mode 為 pinned 再開啟管理模式。

### 管理畫面元件

新建 `RegionManager` 元件，接收 `region` prop：

- 從 `getAllViews()` 取得所有已註冊 view
- 從 `useLayoutStore` 取得目前 region 的 views
- 分成「已啟用」和「可加入」兩組
- 已啟用：HTML5 drag & drop 排序，完成後呼叫 `reorderViews`
- 加入：點 + 呼叫 `addView`
- 移除：點 checkbox 呼叫 `removeView`

### Context Menu 元件

新建 `RegionContextMenu` 元件，使用現有的 context menu pattern（參考 `TabContextMenu`）：

- 右鍵 header 或 collapsed bar 開啟
- 列出所有已註冊 view 的 checkbox
- 點擊直接呼叫 `addView` / `removeView`

## main.tsx 初始化變更

移除硬寫的 view 分配邏輯：

```typescript
// 刪除：
const sidebarState = useLayoutStore.getState().regions['primary-sidebar']
if (sidebarState.views.length === 0) {
  useLayoutStore.getState().setRegionViews('primary-sidebar', ['file-tree-workspace'])
  useLayoutStore.getState().setActiveView('primary-sidebar', 'file-tree-workspace')
}
```

改為：如果所有 region 都沒有任何 view（全新安裝），才套用預設配置：

```typescript
const regions = useLayoutStore.getState().regions
const hasAnyView = Object.values(regions).some((r) => r.views.length > 0)
if (!hasAnyView) {
  useLayoutStore.getState().setRegionViews('primary-sidebar', ['file-tree-workspace'])
  useLayoutStore.getState().setActiveView('primary-sidebar', 'file-tree-workspace')
}
```

注意：此邏輯與現有程式碼有相同的 `purdexStorage` 非同步還原競態風險（若 storage 為 IndexedDB 且尚未載入完成，可能誤判為全新安裝）。此為既有問題，不在本次範圍內擴大或修復。

## 型別與測試清理

### WorkspaceSidebarState 移除

`types/tab.ts` 中的 `WorkspaceSidebarState` interface 和 `Workspace.sidebarState` 欄位未被 runtime 使用。移除時同步刪除 `types/tab.test.ts` 中的 `Workspace.sidebarState` describe block（第 61-77 行）。

### getViewsByRegion 測試移除

`module-registry.test.ts` 中對應 `getViewsByRegion` 的 import 和測試同步刪除。

## 不在範圍內

- View 的跨 region 拖曳（從一個 region 拖到另一個）
- Per-workspace region 配置（layout 維持全域）
- 動態建立新 region
- Module 的動態載入/卸載
- `FileTreeSessionView` 實際消費 `tabId`（目前為 placeholder）
- `purdexStorage` 非同步競態修復
