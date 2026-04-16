# 介面設定 — New Tab Layout 設計

- 日期：2026-04-17
- 作者：brainstorming session (wake + Claude)
- 狀態：Draft，待實作
- 範疇：SPA（React 19 / Zustand / Tailwind 4）

## 目標

1. 新增 top-level Settings section「介面設定 (Interface)」，作為未來各種視覺介面設定的容器。
2. 第一個子項目「New Tab」：讓使用者配置 New Tab 頁的 module 佈局，支援 3 欄 / 2 欄 / 1 欄三種獨立 profile，各可 enable/disable，欄內可拖曳排序。
3. 不再區分桌機 / 手機：執行期依視窗寬度（含 PWA 手機存取）自動選用最合適的 profile。
4. 鋪設可擴充骨架：未來 Pane、Sidebar 等子介面設定能以同樣模式掛上。

## 非目標

- 不處理 Pane / Sidebar 的實際配置（只預留 sub-nav 項，顯示 coming soon）。
- 不實作多裝置同步：本 spec 只做 localStorage 持久化；接入 sync 架構列入 follow-up。
- 不在本 spec 做自訂斷點；固定 ≥1024 / 640-1023 / <640。
- 不做鍵盤拖曳 a11y（列入 follow-up issue）。

## 設計決策（brainstorming + review 結論彙整）

| 問題 | 決策 |
|------|------|
| 介面設定 scope | 可擴充骨架，目前只有 New Tab |
| 手機預覽角色 | PWA 實際執行，不再分桌機/手機 |
| 欄數決定方式 | 3 個獨立 profile（3col/2col/1col），各自 enable/disable；跨 profile 可重複放同一 provider |
| 執行期選用 | 固定斷點 ≥1024 / ≥640 / <640，沿 fallback 鏈找第一個 enabled |
| Module 池 | 展示全部已註冊 provider，單一 profile 內每個 module 最多出現 1 次（可跨 profile 重複） |
| 首次預設 | `activeEditingProfile = '1col'`；3 profile 皆 enabled；`ensureDefaults` 把所有已註冊 provider（排除 `disabled=true`）依 `order` 升冪、分別加入**每個 enabled profile 的最短欄**。因此首次開啟：1col 列出全部、3col/2col 也被自動排入 provider（避免首次用 ≥1024 視窗看到空 3col） |
| 持久化 | Zustand persist → localStorage (`purdex-newtab-layout`)；sync 架構由 follow-up issue 接入 |
| 預覽呈現 | 主畫布 + 兩個縮圖 + profile switcher |
| 實作取向 | α+：保留 `interface-subsection-registry`（雙層）；畫布、palette、profile switcher 為 NewTab 專用具體元件（未來抽 generic 時再 rename） |
| DnD 函式庫 | 沿用專案已有的 `@dnd-kit/core` + `@dnd-kit/sortable`（支援 pointer sensor，桌/手機通吃） |
| 新 module 自動加入 | 以 `knownIds` 追蹤「已認過」的 provider；只對沒在 knownIds 的 id 跑 auto-place；使用者移除意圖自然持久 |

## 架構總覽

```
Settings
├─ Appearance
├─ Terminal
├─ Interface (new, order=2)               ← 新 top-level section
│   ├─ New Tab              ← 本 spec 實作
│   ├─ Pane (disabled)      ← 預留
│   └─ Sidebar (disabled)   ← 預留
├─ ... (其餘既有 section)
```

### 新增檔案

```
spa/src/lib/interface-subsection-registry.ts
spa/src/stores/useNewTabLayoutStore.ts
spa/src/hooks/useMediaQuery.ts                       # 新 hook（專案目前無）
spa/src/components/settings/InterfaceSection.tsx
spa/src/components/settings/InterfaceSubNav.tsx
spa/src/components/settings/new-tab/
  NewTabSubsection.tsx
  NewTabModulePalette.tsx
  NewTabProfileSwitcher.tsx
  NewTabCanvas.tsx
  NewTabThumbnail.tsx
```

**命名理由**：`NewTabModulePalette` / `NewTabProfileSwitcher` 用具體名稱而非 generic `ModulePalette` / `ProfileSwitcher`，避免與現有 `ModuleConfigSection` 撞概念、也避免對未來 pane/sidebar 做出虛假的「通用性保證」。若 pane/sidebar 開工時形狀吻合，屆時再抽 generic 並 rename。

**useMediaQuery**：`useMediaQuery(query: string): boolean`，以 `window.matchMedia` 實作。初值以 `useState(() => matchMedia(query).matches)` 同步取得（避免第一幀 flash）；SSR / 無 `window` 時 fallback 回 false。

### 變更既有檔案

- `spa/src/lib/register-modules.tsx`
  - 註冊新 settings section `interface`
  - 註冊 3 個 interface subsections（new-tab 啟用，pane/sidebar 預留）
  - **不**直接呼叫 store（避免 hydration 時序問題；`ensureDefaults` 改由 React lifecycle 驅動）
- `spa/src/components/NewTabPage.tsx`
  - 改用 `useActiveProfile` + `useNewTabLayoutStore` 決定欄數與內容
  - 加上 `persist.hasHydrated()` gate 避免 hydration flash
- `spa/src/locales/en.json` / `zh-TW.json`：加入 i18n keys

## 資料模型

```ts
// spa/src/stores/useNewTabLayoutStore.ts
export type ProfileKey = '3col' | '2col' | '1col'

export interface Profile {
  enabled: boolean
  columns: string[][]   // columns[i] = provider id 陣列，由上到下
}

export interface NewTabLayoutState {
  profiles: Record<ProfileKey, Profile>
  knownIds: string[]                 // 曾被 ensureDefaults 處理過的 provider id
  activeEditingProfile: ProfileKey

  setEnabled: (p: ProfileKey, enabled: boolean) => void
  setEditing: (p: ProfileKey) => void
  placeModule: (p: ProfileKey, providerId: string, colIdx: number, rowIdx: number) => void
  removeModule: (p: ProfileKey, providerId: string) => void
  ensureDefaults: (providers: Array<{ id: string; order: number; disabled?: boolean }>) => void
  reset: () => void
}
```

### 不變條件

- `profiles['3col'].columns.length === 3`
- `profiles['2col'].columns.length === 2`
- `profiles['1col'].columns.length === 1`
- `profiles['1col'].enabled === true`（setter 強制忽略 false）
- 單一 profile 中每個 providerId 至多出現 1 次（跨 profile 可重複）

### 預設 state（未 hydrate / 未跑 ensureDefaults 前）

```ts
{
  profiles: {
    '3col': { enabled: true, columns: [[], [], []] },
    '2col': { enabled: true, columns: [[], []] },
    '1col': { enabled: true, columns: [[]] },
  },
  knownIds: [],
  activeEditingProfile: '1col',
}
```

首次 `ensureDefaults` 跑完後，每個 enabled profile 都會被填入 provider（詳見下方 ensureDefaults 行為）。

### placeModule 語意（單一寫入 API）

`placeModule(profileKey, id, colIdx, rowIdx)`：
1. 若 id 已存在於該 profile 的任一欄：先從原位移除。
2. 若原位與目標位於**同一欄**且 `fromRow < toRow`：`toRow--`（補償移除造成的 index shift）。
3. 在 `(colIdx, rowIdx)` 插入。

因此 drag/drop、palette 點擊加入、跨欄搬移都共用同一個 action；不再另有 `moveModule`。

### ensureDefaults 行為（以 knownIds 為中心）

```ts
ensureDefaults(providers) {
  const unknown = providers.filter(p => !knownIds.includes(p.id) && !p.disabled)
  unknown.sort((a, b) => a.order - b.order)

  for (const p of unknown) {
    // 對每個 enabled 的 profile：加到「當前最短欄」底部
    for (const key of ['3col', '2col', '1col'] as const) {
      if (!profiles[key].enabled) continue
      const shortest = shortestColIdx(profiles[key].columns)
      profiles[key].columns[shortest].push(p.id)
    }
    knownIds.push(p.id)
  }
}
```

**特性**：
- 「已認過」的 provider（無論使用者之後有沒有移除）都不再被自動加回。
- 只處理 `!disabled` 的 provider；disabled 的 provider 被使用者手動放入才顯示（沿用既有 `NewTabProvider.disabled` 行為）。
- Provider 下架（從 registry 消失）**不**主動從 profiles 清除；改由 render 時 `byId[id]` miss 即 skip。重新註冊（例如 plugin 重灌）時 id 已在 knownIds，不會被重加——這是預期行為。

### Persist

- Middleware：Zustand `persist`
- key：`purdex-newtab-layout`
- `version: 1`
- **不寫 `migrate` callback**（alpha 階段慣例；版本不符時 Zustand 自動 reset 成 initial state）。版本號先保留 `1` 作為未來擴充錨點，進 beta 再視需要撰寫 migration。
- **Hydration 時序**：`ensureDefaults` 必須在 `persist.hasHydrated()` 為 true 之後才呼叫，否則會用 initial state 覆寫已儲存資料。
- Sync：本 spec 不實作；後續在 follow-up issue 中以 sync contributor 機制接入。

## 共用殼

### interface-subsection-registry

```ts
// spa/src/lib/interface-subsection-registry.ts
export interface InterfaceSubsection {
  id: string
  label: string          // i18n key
  order: number
  component: React.ComponentType
  disabled?: boolean
  disabledReason?: string
}

const subsections: InterfaceSubsection[] = []
export function registerInterfaceSubsection(s: InterfaceSubsection): void
export function getInterfaceSubsections(): InterfaceSubsection[]
export function clearInterfaceSubsectionRegistry(): void
```

與 `settings-section-registry` 同形，push 後依 `order` 排序。

### InterfaceSection

```tsx
export function InterfaceSection() {
  const subs = getInterfaceSubsections()
  const [active, setActive] = useState(subs[0]?.id)
  const selected = subs.find((s) => s.id === active)
  return (
    <div className="flex h-full">
      <InterfaceSubNav items={subs} active={active} onSelect={setActive} />
      <div className="flex-1 overflow-auto">
        {selected && !selected.disabled && <selected.component />}
      </div>
    </div>
  )
}
```

Registry 為 module-level 靜態 array；render 時 snapshot。不支援 runtime 動態增減 subsection（與既有 `settings-section-registry` 同設計）。

### NewTab 專用元件 API

```tsx
<NewTabModulePalette
  items={Array<{ id: string; label: string; icon: string; inUse: boolean; unavailable?: boolean }>}
  onClickAdd={(id: string) => void}   // 快速加入最短欄底部
/>

<NewTabProfileSwitcher
  profiles={Array<{ key: ProfileKey; label: string; enabled: boolean; isEmpty: boolean }>}
  active={ProfileKey}
  lockedKeys={['1col']}
  onSelect={(k) => void}
  onToggleEnabled={(k, enabled) => void}
  renderMain={(k) => <NewTabCanvas profileKey={k} />}
  renderThumb={(k) => <NewTabThumbnail profileKey={k} />}
/>
```

`isEmpty` 為 true 時在 switcher 顯示「此 profile 目前無 module」警示 badge。

## New Tab Subsection（畫布）

### 版面

```
┌───────────────────────────────────────────────┐
│ ModulePalette                                 │
│ [Sessions ✓] [Editor ✓] [Browser ⊘] [Hosts]  │
├───────────────────────────────────────────────┤
│ ProfileSwitcher                               │
│ ● 3col ☑   ○ 2col ☑   ○ 1col ☑(locked)     │
├───────────────────────────────────────────────┤
│ Main canvas（active profile）                 │
│ ┌─────┬─────┬─────┐                          │
│ │col 1│col 2│col 3│                          │
│ └─────┴─────┴─────┘                          │
│ Thumbnails: [2col ▸] [1col ▸]                │
└───────────────────────────────────────────────┘
```

### 拖曳（`@dnd-kit`）

沿用專案已有的 `@dnd-kit/core` + `@dnd-kit/sortable`（`TabBar` / `ActivityBar` / `RegionManager` 已在用）。

- **Sensors**：`PointerSensor`（桌/手機觸控通吃）+ `KeyboardSensor`（基本鍵盤拖曳由 dnd-kit 內建）。
- **DndContext** 包住整個 NewTabSubsection。
- **Draggables**
  - Palette chip：`useDraggable({ id: 'palette:' + providerId })`；`inUse=true` 時 `disabled` 避免重複拖
  - Canvas item：用 `useSortable`（同一欄內 reorder 順暢）
- **Droppables**
  - 每欄（`useDroppable({ id: 'col:' + profile + ':' + colIdx })`），欄內 item 再交給 `useSortable` 處理 insertion index
  - Palette 區整塊為 droppable（drop 到此 = 從 canvas 移除）
- **onDragEnd** dispatch 到 store 的 `placeModule` / `removeModule`。
- **Feedback**
  - Drop target 顯示 insertion line（sortable 自帶動畫 + 自訂 style）
  - `DragOverlay` 顯示拖曳中的 chip
  - 空欄顯示 `Drop module here` 灰字

### 快速互動

- Palette chip 點擊 → `onClickAdd` → store `placeModule` 到當前 profile 最短欄底部
- Canvas item 右上 `×` → `removeModule`
- 縮圖點擊 → `setEditing`
- 縮圖上小 toggle → `setEnabled`（1col 的 toggle `disabled`）

### 可用性標示

| 狀態 | palette chip | canvas item |
|------|-------------|-------------|
| `inUse=false` | 正常可拖 / 點擊加入 | — |
| `inUse=true` | 灰出 + "✓ 已放" + 不可拖 | 正常 |
| `unavailable=true`（`NewTabProvider.disabled`）| 灰出但可拖可放（使用者顯式選擇） | 渲染時保留「requires desktop app」提示（沿用現況） |

注：`ensureDefaults` **不**會自動把 `disabled=true` 的 provider 加入任何 profile，避免預設佈局頂端出現「requires desktop app」的糟糕第一印象。

## Runtime：NewTabPage 改寫

### useActiveProfile hook

```ts
function useActiveProfile(): ProfileKey {
  const isWide = useMediaQuery('(min-width: 1024px)')   // useState 初值用 matchMedia 同步取
  const isMid = useMediaQuery('(min-width: 640px)')
  const profiles = useNewTabLayoutStore((s) => s.profiles)
  return resolveProfile(isWide, isMid, profiles)
}

// 純函式，方便單元測試
export function resolveProfile(
  isWide: boolean,
  isMid: boolean,
  profiles: Record<ProfileKey, Profile>,
): ProfileKey {
  const desired: ProfileKey = isWide ? '3col' : isMid ? '2col' : '1col'
  const chain: ProfileKey[] =
    desired === '3col' ? ['3col', '2col', '1col']
    : desired === '2col' ? ['2col', '1col']
    : ['1col']
  return chain.find((k) => profiles[k].enabled) ?? '1col'
}
```

### NewTabPage

```tsx
export function NewTabPage({ onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  const hydrated = useNewTabLayoutStore.persist.hasHydrated()
  const profileKey = useActiveProfile()
  const profile = useNewTabLayoutStore((s) => s.profiles[profileKey])
  const providers = getNewTabProviders()
  const byId = useMemo(() => Object.fromEntries(providers.map((p) => [p.id, p])), [providers])

  if (!hydrated) {
    return <div className="flex-1" />  // 佔位，避免 hydration flash
  }

  if (providers.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-secondary">{t('page.newtab.empty')}</p>
      </div>
    )
  }

  const gridCols = profile.columns.length === 3 ? 'grid-cols-3'
                 : profile.columns.length === 2 ? 'grid-cols-2'
                 : 'grid-cols-1'

  return (
    <div className={`flex-1 grid overflow-hidden gap-6 px-6 pt-8 ${gridCols}`}>
      {profile.columns.map((col, i) => (
        <div key={`${profileKey}-${i}`} className="flex flex-col gap-6 overflow-y-auto">
          {col.map((id) => {
            const p = byId[id]
            if (!p) return null   // provider 已下架：靜默 skip，不回寫 store
            return <ProviderSection key={id} provider={p} onSelect={onSelect} t={t} />
          })}
        </div>
      ))}
    </div>
  )
}
```

### Bootstrap（ensureDefaults 時機）

**不**在 `register-modules.tsx` 模組頂層呼叫 store。改在應用根元件（例如 `App.tsx` 或專屬 provider）以 `useEffect` + hydration gate 觸發：

```tsx
// 在 App root 或類似位置
useEffect(() => {
  const unsubscribe = useNewTabLayoutStore.persist.onFinishHydration(() => {
    const providers = getNewTabProviders().map(p => ({ id: p.id, order: p.order, disabled: p.disabled }))
    useNewTabLayoutStore.getState().ensureDefaults(providers)
  })
  // 若已 hydrated（HMR 等場景）也跑一次
  if (useNewTabLayoutStore.persist.hasHydrated()) {
    const providers = getNewTabProviders().map(p => ({ id: p.id, order: p.order, disabled: p.disabled }))
    useNewTabLayoutStore.getState().ensureDefaults(providers)
  }
  return unsubscribe
}, [])
```

此時：
- `registerBuiltinModules()` 已在 entry 執行完，provider 列表完整。
- Hydration 完成後才讀 / 寫 store，不會覆蓋 localStorage。
- `ensureDefaults` 只處理 knownIds 以外的 provider，重複呼叫無副作用。

## 邊界情況與錯誤處理

| 情況 | 處理 |
|------|------|
| Provider 註冊後又被移除 | render 時 `byId[id]` miss 即 skip；**不**回寫 store（保留 knownIds 紀錄，plugin 重灌時不再自動加回，視為預期） |
| 3col/2col 皆 disabled，視窗 ≥1024 | fallback 鏈走到 1col |
| 使用者試圖 disable 1col | setter 忽略；UI toggle 為 locked 狀態 + tooltip |
| 同一 provider 在同 profile 重複放入 | `placeModule` 先移除舊位再插入；同欄內向下移動時 `toRow` 補償 1（避免 off-by-one） |
| 跨 profile 重複放入同一 provider | 合法；各 profile 獨立 |
| Persist schema 變更 | `version: 1`，不寫 migrate，版本不符 Zustand 自動 reset（alpha 慣例） |
| Hydration 未完成時 render | `persist.hasHydrated()` gate 顯示空白佔位，避免閃爍 |
| `ensureDefaults` 在 hydration 前被呼叫 | 禁止；僅在 `onFinishHydration` callback 或 `hasHydrated()` 為 true 時才執行 |
| localStorage 寫入失敗 | Zustand persist 內建 try/catch + console.warn，不影響 runtime 渲染 |
| 斷點附近視窗抖動 | `matchMedia` 只在跨斷點時觸發，不抖動 |
| 空 profile（cols 全空） | runtime 顯示 `page.newtab.empty`；設定頁 switcher 顯示「此 profile 目前無 module」警示 |
| Touch 裝置（iOS / Android PWA） | `@dnd-kit` `PointerSensor` 原生支援，無需 fallback |
| Drop 到非合法目標 | dnd-kit 內建處理，chip 回彈原位 |

## 測試策略（TDD）

### 單元（Vitest）

`useNewTabLayoutStore.test.ts`
- `placeModule` 唯一性（同 id 重複放入同 profile = move）
- `placeModule` 同欄向下 move 的 off-by-one 正確處理
- `placeModule` 跨欄移動
- `placeModule` 跨 profile 可獨立放相同 id
- `removeModule`
- `setEnabled('1col', false)` 被忽略
- `ensureDefaults`：
  - 首次呼叫把未知 provider 補進每個 enabled profile 最短欄，並記入 `knownIds`
  - 已在 `knownIds` 的 id 不會被再加（即使使用者已移除）
  - 跳過 `disabled=true` 的 provider
  - 已下架的 provider **不**被自動從 profiles 清除

`interface-subsection-registry.test.ts`
- 註冊、order 排序、clear

`resolveProfile.test.ts`
- 各斷點 × 各 enabled 組合的 fallback 正確

### 元件（Vitest + RTL）

- `NewTabModulePalette`：`inUse`、`unavailable` 的 class 與 `disabled` 狀態；點擊發事件
- `NewTabProfileSwitcher`：切換、toggle、`lockedKeys` 鎖住 1col、`isEmpty` badge
- `NewTabCanvas`：
  - 渲染當前 profile 欄位
  - dnd-kit `onDragEnd` 呼叫 store action（以 fake DndContext / 直接呼叫 handler 驗證）
  - 空欄顯示 placeholder
- `NewTabPage` 整合：
  - `hasHydrated=false` 時顯示佔位
  - 不同 profiles state + mock matchMedia → 渲染對應欄數與內容
  - `byId[id]` miss 時 silent skip（不 throw、不回寫）

### 不測（或之後補 E2E）

- dnd-kit 實際 drag gesture 在 JSDOM 不穩定 → 測試以直接呼叫 `onDragEnd` handler 或使用 `@dnd-kit` 官方的 testing utilities；真 drag E2E 留給後續 Playwright / Cypress。
- Touch 裝置行為以 `PointerSensor` 為黑盒假設（dnd-kit 維護）。

## i18n keys（新增）

遵循現有 `settings.section.*` 慣例；subsection 層級的 key 用 `settings.interface.<sub-id>_*` 命名，避免深層 key（既有慣例是兩到三層）。

```
settings.section.interface                介面設定
settings.interface.new_tab                分頁首頁
settings.interface.pane                   分割（coming soon 項目）
settings.interface.sidebar                側邊欄（coming soon 項目）
settings.interface.profile_3col           三欄
settings.interface.profile_2col           兩欄
settings.interface.profile_1col           單欄
settings.interface.profile_locked         保底配置（無法停用）
settings.interface.profile_empty          此配置尚未加入任何 module
settings.interface.palette_in_use         已放
settings.interface.palette_unavailable    此環境不可用
settings.interface.canvas_drop_here       拖曳 module 到此
```

## 實作順序建議

1. `useMediaQuery` hook + test
2. `interface-subsection-registry` + test
3. `useNewTabLayoutStore` + test（含 `placeModule` off-by-one / knownIds / `resolveProfile` 純函式）
4. `NewTabModulePalette` / `NewTabProfileSwitcher` + test
5. `NewTabCanvas` / `NewTabThumbnail`（含 dnd-kit DndContext 整合）+ test
6. `NewTabSubsection` 組裝
7. `InterfaceSection` + `InterfaceSubNav` 殼
8. 註冊到 `register-modules.tsx`（含 pane/sidebar 預留項；**不**呼叫 store）
9. App root `useEffect` 掛 `onFinishHydration` → `ensureDefaults`
10. `NewTabPage` 改寫 + 整合測試（hydration gate、matchMedia mock）
11. i18n 補字
12. 手動驗證：SPA 桌面 / 窄視窗 / PWA 手機實機拖曳

## Follow-up（不在本 spec）

- 自訂斷點（issue）
- 使用者明確「永久隱藏」某 module 的 UI（進階；目前透過「不加入任何 profile」近似）
- Pane / Sidebar subsection 實作（各自開 spec）
- 接入 sync contributor 架構（等 sync 架構 phase 完成）
- 鍵盤 DnD a11y 精修（dnd-kit `KeyboardSensor` 基本可用，細節 polish）
- 真 drag 的 E2E 測試（Playwright）
