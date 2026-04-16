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
- 不實作多裝置同步：本 spec 只做 localStorage 持久化，但資料結構標記為 syncable，交由未來 sync 架構接手。
- 不引入 `@dnd-kit` 等外部 DnD 依賴，用原生 HTML5 DnD。
- 不在本 spec 做自訂斷點；固定 ≥1024 / 640-1023 / <640。
- 不做鍵盤拖曳 a11y（列入 follow-up issue）。

## 設計決策（brainstorming 結論彙整）

| 問題 | 決策 |
|------|------|
| 介面設定 scope | 可擴充骨架，目前只有 New Tab |
| 手機預覽角色 | PWA 實際執行，不再分桌機/手機 |
| 欄數決定方式 | 3 個獨立 profile（3col/2col/1col），各自 enable/disable |
| 執行期選用 | 固定斷點 ≥1024 / ≥640 / <640，沿 fallback 鏈找第一個 enabled |
| Module 池 | 展示全部已註冊 provider，單一 profile 內每個 module 最多出現 1 次 |
| 首次預設 | 3 個 profile 皆 enabled；1col 依 provider `order` 堆入全部，3col/2col 空 |
| 持久化 | localStorage (`purdex-newtab-layout`)，標記為可 sync |
| 預覽呈現 | 主畫布 + 兩個縮圖 + profile switcher |
| 實作取向 | α+：共用「外殼 + palette + profile switcher + sub-section registry」，各子區自備 canvas 與 store |

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
spa/src/components/settings/interface/
  InterfaceSubNav.tsx
  ModulePalette.tsx
  ProfileSwitcher.tsx
  NewTabSubsection.tsx
  NewTabCanvas.tsx
  NewTabThumbnail.tsx
```

`useMediaQuery(query: string): boolean` 是新增的通用 hook，以 `window.matchMedia` 實作，SSR / JSDOM 時安全回傳 false。

### 變更既有檔案

- `spa/src/lib/register-modules.tsx`
  - 註冊新 settings section `interface`
  - 註冊 3 個 interface subsections（new-tab 啟用，pane/sidebar 預留）
  - 尾段呼叫 `ensureDefaults` 補齊 layout
- `spa/src/components/NewTabPage.tsx`
  - 改用 `useActiveProfile` + `useNewTabLayoutStore` 決定欄數與內容
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
  activeEditingProfile: ProfileKey

  setEnabled: (p: ProfileKey, enabled: boolean) => void
  setEditing: (p: ProfileKey) => void
  placeModule: (p: ProfileKey, providerId: string, colIdx: number, rowIdx: number) => void
  removeModule: (p: ProfileKey, providerId: string) => void
  moveModule: (p: ProfileKey, providerId: string, toCol: number, toRow: number) => void
  ensureDefaults: (registeredIds: string[]) => void
  reset: () => void
}
```

### 不變條件

- `profiles['3col'].columns.length === 3`
- `profiles['2col'].columns.length === 2`
- `profiles['1col'].columns.length === 1`
- `profiles['1col'].enabled === true`（setter 強制）
- 單一 profile 中每個 providerId 至多出現 1 次

### 預設 state

```ts
{
  profiles: {
    '3col': { enabled: true, columns: [[], [], []] },
    '2col': { enabled: true, columns: [[], []] },
    '1col': { enabled: true, columns: [/* 全部 providers 依 order 升冪 */] },
  },
  activeEditingProfile: '3col',
}
```

### ensureDefaults 行為

- 計算「已註冊但未出現在任何 profile 任何欄」的 id 集合 → 依 `order` 升冪加到 1col 底部。
- 計算「出現在某 profile 但已不在註冊表」的 id → 從所有 profile 清掉。
- 本 action 應在啟動後、provider 註冊完成時呼叫一次。

### Persist

- Middleware：Zustand `persist`
- key：`purdex-newtab-layout`
- `version: 1`
- `migrate(persisted, fromVersion)`：v0 或未知版本直接回傳初始 state
- 檔頭靜態旗標 `syncable: true`（供未來 sync 架構掃描）

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

### 共用元件 API

```tsx
<ModulePalette
  items={Array<{ id: string; label: string; icon: string; inUse: boolean; unavailable?: boolean }>}
  onDragStart={(id: string) => void}
  onClickAdd={(id: string) => void}  // 快速加入最短欄底部
/>

<ProfileSwitcher
  profiles={Array<{ key: ProfileKey; label: string; enabled: boolean }>}
  active={ProfileKey}
  lockedKeys={['1col']}
  onSelect={(k) => void}
  onToggleEnabled={(k, enabled) => void}
  renderMain={(k) => <NewTabCanvas profileKey={k} />}
  renderThumb={(k) => <NewTabThumbnail profileKey={k} />}
/>
```

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

### 拖曳（原生 HTML5 DnD）

- **Drop targets**
  - 每欄內每個 item 之間（insertion line）
  - 每欄末端（空欄 placeholder 或列末）
  - ModulePalette 本體（= 移除）
- **Source**
  - Palette chip（`draggable={!item.inUse}`；已放則不能從 palette 再拖，除非從 canvas 移除）
  - Canvas item（`draggable` 一律為 true）
- **Feedback**
  - Drop target 顯示藍色 insertion line（`data-dragover="true"`）
  - 被拖 chip 半透明
  - 空欄顯示 `Drop module here` 灰字
- **Edge case**
  - Drop 到自己原位：no-op
  - Drop 到非合法目標：React 不攔截 default，chip 回彈
  - 拖同一 provider 進已存在此 id 的 col：`placeModule` 先移除舊位再插入新位

### 快速互動

- Palette chip 點擊 → `onClickAdd` → store `placeModule` 到當前 profile 最短欄底部
- Canvas item 右上 `×` → `removeModule`
- 縮圖點擊 → `setEditing`
- 縮圖上小 toggle → `setEnabled`
- Profile switcher 上的「此 profile 空」警示（當該 profile 0 modules）

### 可用性標示

| 狀態 | palette chip | canvas item |
|------|-------------|-------------|
| `inUse=false` | 正常可拖 | — |
| `inUse=true` | 灰出 + "✓ 已放" | 正常 |
| `unavailable=true`（`NewTabProvider.disabled`）| 灰出但可拖可放 | 渲染時保留「requires desktop app」提示（沿用現況） |

## Runtime：NewTabPage 改寫

### useActiveProfile hook

```ts
function useActiveProfile(): ProfileKey {
  const isWide = useMediaQuery('(min-width: 1024px)')
  const isMid = useMediaQuery('(min-width: 640px)')
  const profiles = useNewTabLayoutStore((s) => s.profiles)

  const desired: ProfileKey = isWide ? '3col' : isMid ? '2col' : '1col'
  const chain: ProfileKey[] =
    desired === '3col' ? ['3col', '2col', '1col']
    : desired === '2col' ? ['2col', '1col']
    : ['1col']
  return chain.find((k) => profiles[k].enabled) ?? '1col'
}
```

純函式版（for unit test）：

```ts
export function resolveProfile(width: number, profiles: Record<ProfileKey, Profile>): ProfileKey
```

### NewTabPage

```tsx
export function NewTabPage({ onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  const profileKey = useActiveProfile()
  const profile = useNewTabLayoutStore((s) => s.profiles[profileKey])
  const providers = getNewTabProviders()
  const byId = useMemo(() => Object.fromEntries(providers.map((p) => [p.id, p])), [providers])

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
        <div key={i} className="flex flex-col gap-6 overflow-y-auto">
          {col.map((id) => {
            const p = byId[id]
            if (!p) return null
            return <ProviderSection key={id} provider={p} onSelect={onSelect} t={t} />
          })}
        </div>
      ))}
    </div>
  )
}
```

- `ProviderSection` 繼續使用現有元件。
- `byId[id]` miss 時 skip（provider 被移除），下一輪 `ensureDefaults` 會清。

### Bootstrap

`register-modules.tsx` 結尾：

```ts
const ids = getNewTabProviders().map((p) => p.id)
useNewTabLayoutStore.getState().ensureDefaults(ids)
```

註冊時機：`registerBuiltinModules()` 已在 entry 被呼叫一次；provider 列表此時完整。

## 邊界情況與錯誤處理

| 情況 | 處理 |
|------|------|
| Provider 註冊後又被移除 | `ensureDefaults` 清掉；render 時 `byId[id]` 為 undefined 時 skip |
| 3col/2col 皆 disabled，視窗 ≥1024 | fallback 到 1col |
| 使用者試圖 disable 1col | setter 忽略；UI toggle locked + tooltip |
| 同一 provider 重複放 | `placeModule` 先移除舊位置再插入 |
| Persist schema 變更 | `version: 1` + `migrate` → 未知版本回預設 |
| localStorage 失敗 | Zustand persist 內建 try/catch，console.warn，不影響 runtime |
| 斷點附近抖動 | `matchMedia` 只在跨斷點觸發 |
| 空 profile（cols 全空） | runtime 顯示 `page.newtab.empty`；設定頁顯示「此 profile 目前無 module」警示 |
| Drop 到非合法目標 | default ignore，chip 回彈 |

## 測試策略（TDD）

### 單元（Vitest）

`useNewTabLayoutStore.test.ts`
- `placeModule` 唯一性（同 id 重複 = move）
- `removeModule`
- `setEnabled('1col', false)` 被忽略
- `ensureDefaults`：補新、清除已下架
- `moveModule` 跨欄、同欄上下

`interface-subsection-registry.test.ts`
- 註冊、order 排序、clear

`resolveProfile.test.ts`
- 各寬度 × 各 enabled 組合的 fallback 正確

### 元件（Vitest + RTL）

- `ModulePalette`：`inUse`、`unavailable` class；點擊發事件
- `ProfileSwitcher`：切換、toggle、locked 鎖定
- `NewTabCanvas`：
  - 渲染當前 profile 欄位
  - 模擬 drop handler 呼叫 store action
  - 空欄顯示 placeholder
- `NewTabPage` 整合：給不同 profiles state + mock matchMedia → 渲染對應欄數與內容

### 不測

- JSDOM 的 DragEvent 不完整 → 以「直接呼叫 handler」模擬，不 dispatch 真 drag event。E2E 之後補。

## i18n keys（新增）

```
settings.section.interface          介面設定
settings.interface.new_tab          分頁首頁
settings.interface.pane             分割
settings.interface.sidebar          側邊欄
settings.interface.profile.3col     三欄
settings.interface.profile.2col     兩欄
settings.interface.profile.1col     單欄
settings.interface.profile.locked   保底配置（無法停用）
settings.interface.profile.empty    此配置尚未加入任何 module
settings.interface.palette.in_use   已放
settings.interface.palette.unavailable  此環境不可用
settings.interface.canvas.drop_here     拖曳 module 到此
```

## 實作順序建議

1. `interface-subsection-registry` + test
2. `useNewTabLayoutStore` + test（含 `resolveProfile` 純函式）
3. 共用元件 `ModulePalette` / `ProfileSwitcher` + test
4. `NewTabCanvas` / `NewTabThumbnail` + test
5. `NewTabSubsection` 組裝
6. `InterfaceSection` + `InterfaceSubNav` 殼
7. 註冊到 `register-modules.tsx`（含 pane/sidebar 預留項、`ensureDefaults` 呼叫）
8. `NewTabPage` 改寫 + 整合測試
9. i18n 補字
10. 手動驗證：SPA 桌面 / 窄視窗 / PWA 手機實機

## Follow-up（不在本 spec）

- 鍵盤 DnD a11y（issue）
- 自訂斷點（issue）
- 同一 module 可多位（目前禁止，issue 討論）
- Pane / Sidebar subsection 實作（各自開 spec）
- 實際接上 sync 架構（等 sync 架構 phase 完成）
