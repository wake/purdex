# 介面設定 — New Tab Layout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 top-level Settings section「介面設定 (Interface)」；其下首個子項目「New Tab」讓使用者為 3col/2col/1col 三種 profile 配置 NewTab module 佈局；runtime 依視窗寬度自動選用 profile；不再區分桌機/手機。

**Architecture:** 雙層 registry（`settings-section-registry` + 新增 `interface-subsection-registry`）掛新 section；獨立 `useNewTabLayoutStore`（Zustand + persist）以 `knownIds` 為中心做 auto-place；`NewTabPage` 改為依 `useActiveProfile` 讀當前 profile，並以 `persist.hasHydrated()` 防 hydration flash。拖曳沿用專案既有 `@dnd-kit/core` + `@dnd-kit/sortable` 慣例（`PointerSensor` + `activationConstraint: { distance: 5 }` + DragOverlay via portal）。

**Tech Stack:** React 19 / Zustand 5 (`persist` middleware) / TypeScript / Tailwind 4 / `@dnd-kit/core` + `@dnd-kit/sortable` / Vitest + React Testing Library / Phosphor Icons

**Spec 權威版本:** `docs/superpowers/specs/2026-04-17-interface-settings-new-tab-design.md` @ commit `33e06122`（main）。

**實作分支起點:** main（33e06122）—— 請從 main 建立新 worktree，確保拿到最新 spec 與 alpha.148 程式碼。若 main 仍在 alpha.147 之前，先 `git pull`。

---

## 檔案結構概覽

### 新增

| 路徑 | 職責 |
|------|------|
| `spa/src/lib/interface-subsection-registry.ts` | 介面設定子項目註冊表（upsert 語意） |
| `spa/src/lib/interface-subsection-registry.test.ts` | 同上 unit test |
| `spa/src/lib/resolve-profile.ts` | 純函式 `resolveProfile(isWide, isMid, profiles) -> ProfileKey`；fallback 鏈 |
| `spa/src/lib/resolve-profile.test.ts` | `resolveProfile` unit test |
| `spa/src/hooks/useMediaQuery.ts` | 通用 `useMediaQuery(query): boolean`（由 `useIsMobile` 重構） |
| `spa/src/hooks/useMediaQuery.test.ts` | `useMediaQuery` unit test |
| `spa/src/hooks/useNewTabBootstrap.ts` | App root hook：hydration 完成後呼叫 `ensureDefaults` |
| `spa/src/stores/useNewTabLayoutStore.ts` | Zustand store（profiles / knownIds / placeModule / removeModule / setEnabled / ensureDefaults / reset） |
| `spa/src/stores/useNewTabLayoutStore.test.ts` | store unit test |
| `spa/src/components/settings/InterfaceSection.tsx` | 介面設定殼（controlled subsection） |
| `spa/src/components/settings/InterfaceSubNav.tsx` | 子項目側邊列 |
| `spa/src/components/settings/InterfaceSection.test.tsx` | section 殼 test |
| `spa/src/components/settings/new-tab/NewTabSubsection.tsx` | palette + switcher + canvas 組裝 |
| `spa/src/components/settings/new-tab/NewTabModulePalette.tsx` | palette chips |
| `spa/src/components/settings/new-tab/NewTabModulePalette.test.tsx` | palette test |
| `spa/src/components/settings/new-tab/NewTabProfileSwitcher.tsx` | profile 選單 + enable toggle + 縮圖 |
| `spa/src/components/settings/new-tab/NewTabProfileSwitcher.test.tsx` | switcher test |
| `spa/src/components/settings/new-tab/NewTabCanvas.tsx` | 主畫布（dnd-kit DndContext + DragOverlay via portal） |
| `spa/src/components/settings/new-tab/NewTabCanvas.test.tsx` | canvas test |
| `spa/src/components/settings/new-tab/NewTabThumbnail.tsx` | 縮圖 |

### 修改

| 路徑 | 變更 |
|------|------|
| `spa/src/test-setup.ts` | 加 `window.matchMedia` 全域 mock |
| `spa/src/hooks/useIsMobile.ts` | 改寫為 `useMediaQuery('(max-width: 767px)')` 薄包裝 |
| `spa/src/hooks/useIsMobile.test.ts` | 更新測試以反映重構（或保留原行為測試） |
| `spa/src/lib/storage/keys.ts` | 加入 `NEW_TAB_LAYOUT: 'purdex-newtab-layout'` |
| `spa/src/lib/register-modules.tsx` | 註冊 `interface` settings section + 3 interface subsections |
| `spa/src/lib/register-modules.test.ts` | 加入 section/subsection 註冊驗證 |
| `spa/src/App.tsx` | 呼叫 `useNewTabBootstrap()` |
| `spa/src/components/NewTabPage.tsx` | 改用 `useActiveProfile` + 多欄 grid + hydration gate |
| `spa/src/components/SettingsPage.tsx` | 若 `interface` section 選中，`activeSubsection` 由外層持有並傳入 |
| `spa/src/locales/en.json` | 新增 `settings.section.interface` 等 keys |
| `spa/src/locales/zh-TW.json` | 新增 `settings.section.interface` 等 keys |

---

## 命名與慣例速覽

- **i18n key**：遵循現有 `settings.section.*` / `settings.<section-id>.*`。Subsection 扁平 key 命名 `settings.interface.<short_name>`（避免四層巢狀，參考既有風格）。
- **Registry upsert**：see `settings-section-registry.ts`（先找 id，存在則 `sections[idx] = def`，否則 `push`，最後 `sort`）。
- **Persist hydration pattern**：see `useRouteSync.ts:18-23` —— `useState(store.persist.hasHydrated())` + `onFinishHydration` callback。
- **dnd-kit**：see `TabBar.tsx:50, 103` —— `useSensor(PointerSensor, { activationConstraint: { distance: 5 } })`。
- **Store persist 結構**：see `useLayoutStore.ts:87-241`（`create<State>()(persist(..., { name, storage: purdexStorage, version, partialize, onRehydrateStorage }))`）。

---

## Task 1: test-setup 加 matchMedia mock

**Files:**
- Modify: `spa/src/test-setup.ts`

`useMediaQuery` 在測試環境會呼叫 `window.matchMedia(q).matches`；JSDOM 不提供 `matchMedia`，直接呼叫會 throw。必須先打好全域 mock，之後所有測試都受益。

- [ ] **Step 1: 加入 matchMedia mock**

修改 `spa/src/test-setup.ts`，在 `ResizeObserver` polyfill 之後加入：

```ts
// jsdom 不提供 matchMedia
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  configurable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }),
})
```

- [ ] **Step 2: 執行整組測試確認不回歸**

Run: `cd spa && npx vitest run`
Expected: 所有既有測試仍通過；既有 `useIsMobile.test.ts` 本身已 `vi.stubGlobal` 覆寫 matchMedia，新全域 mock 只是作為預設 fallback，不相斥。

- [ ] **Step 3: Commit**

```bash
git add spa/src/test-setup.ts
git commit -m "test: add global matchMedia mock for jsdom"
```

---

## Task 2: `resolveProfile` 純函式 + test

**Files:**
- Create: `spa/src/lib/resolve-profile.ts`
- Test: `spa/src/lib/resolve-profile.test.ts`

無相依的純函式，最早可測；後續 `useActiveProfile` 會 wrap 它。

- [ ] **Step 1: 寫 failing test**

建 `spa/src/lib/resolve-profile.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { resolveProfile, type ProfileKey, type Profile } from './resolve-profile'

function p(enabled: boolean, cols: number): Profile {
  return { enabled, columns: Array.from({ length: cols }, () => []) }
}

function profiles(enabled: Record<ProfileKey, boolean>) {
  return {
    '3col': p(enabled['3col'], 3),
    '2col': p(enabled['2col'], 2),
    '1col': p(enabled['1col'], 1),
  }
}

describe('resolveProfile', () => {
  it('wide viewport picks 3col when enabled', () => {
    const r = resolveProfile(true, true, profiles({ '3col': true, '2col': true, '1col': true }))
    expect(r).toBe('3col')
  })

  it('wide viewport falls back to 2col when 3col disabled', () => {
    const r = resolveProfile(true, true, profiles({ '3col': false, '2col': true, '1col': true }))
    expect(r).toBe('2col')
  })

  it('wide viewport falls back to 1col when 3col and 2col disabled', () => {
    const r = resolveProfile(true, true, profiles({ '3col': false, '2col': false, '1col': true }))
    expect(r).toBe('1col')
  })

  it('mid viewport picks 2col when enabled', () => {
    const r = resolveProfile(false, true, profiles({ '3col': true, '2col': true, '1col': true }))
    expect(r).toBe('2col')
  })

  it('mid viewport falls back to 1col when 2col disabled', () => {
    const r = resolveProfile(false, true, profiles({ '3col': true, '2col': false, '1col': true }))
    expect(r).toBe('1col')
  })

  it('narrow viewport always picks 1col regardless of 3col/2col state', () => {
    const r = resolveProfile(false, false, profiles({ '3col': true, '2col': true, '1col': true }))
    expect(r).toBe('1col')
  })

  it('defends against all-disabled (should be prevented by setter but safe)', () => {
    const r = resolveProfile(true, true, profiles({ '3col': false, '2col': false, '1col': false }))
    expect(r).toBe('1col')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/resolve-profile.test.ts`
Expected: FAIL — `Cannot find module './resolve-profile'`

- [ ] **Step 3: 實作最小可過測試的版本**

建 `spa/src/lib/resolve-profile.ts`：

```ts
export type ProfileKey = '3col' | '2col' | '1col'

export interface Profile {
  enabled: boolean
  columns: string[][]
}

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

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/resolve-profile.test.ts`
Expected: PASS（7 tests）

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/resolve-profile.ts spa/src/lib/resolve-profile.test.ts
git commit -m "feat(new-tab): add resolveProfile pure function + tests"
```

---

## Task 3: `useMediaQuery` hook（重構 `useIsMobile`）

**Files:**
- Create: `spa/src/hooks/useMediaQuery.ts`
- Test: `spa/src/hooks/useMediaQuery.test.ts`
- Modify: `spa/src/hooks/useIsMobile.ts`
- Modify: `spa/src/hooks/useIsMobile.test.ts`（保留行為測試）

專案已有 `useIsMobile`（hard-coded `max-width: 767px`）。重構為通用 `useMediaQuery(query)`，原 `useIsMobile` 薄包裝沿用不改呼叫點。

- [ ] **Step 1: 寫 `useMediaQuery` failing test**

建 `spa/src/hooks/useMediaQuery.test.ts`：

```ts
import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useMediaQuery } from './useMediaQuery'

describe('useMediaQuery', () => {
  it('returns false when matchMedia reports false', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((q: string) => ({
      matches: false, media: q,
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
    })))
    const { result } = renderHook(() => useMediaQuery('(min-width: 1024px)'))
    expect(result.current).toBe(false)
    vi.unstubAllGlobals()
  })

  it('returns true when matchMedia reports true', () => {
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((q: string) => ({
      matches: true, media: q,
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
    })))
    const { result } = renderHook(() => useMediaQuery('(min-width: 1024px)'))
    expect(result.current).toBe(true)
    vi.unstubAllGlobals()
  })

  it('registers and cleans up change listener', () => {
    const add = vi.fn()
    const remove = vi.fn()
    vi.stubGlobal('matchMedia', vi.fn().mockImplementation((q: string) => ({
      matches: false, media: q,
      addEventListener: add, removeEventListener: remove,
    })))
    const { unmount } = renderHook(() => useMediaQuery('(min-width: 640px)'))
    expect(add).toHaveBeenCalledWith('change', expect.any(Function))
    unmount()
    expect(remove).toHaveBeenCalledWith('change', expect.any(Function))
    vi.unstubAllGlobals()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/hooks/useMediaQuery.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: 實作 `useMediaQuery`**

建 `spa/src/hooks/useMediaQuery.ts`：

```ts
import { useState, useEffect } from 'react'

export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => {
    if (typeof window === 'undefined') return false
    return window.matchMedia(query).matches
  })

  useEffect(() => {
    const mql = window.matchMedia(query)
    const handler = (e: MediaQueryListEvent) => setMatches(e.matches)
    mql.addEventListener('change', handler)
    // 同步一次，避免 render 之間的 race
    setMatches(mql.matches)
    return () => mql.removeEventListener('change', handler)
  }, [query])

  return matches
}
```

- [ ] **Step 4: 驗證測試通過**

Run: `cd spa && npx vitest run src/hooks/useMediaQuery.test.ts`
Expected: PASS

- [ ] **Step 5: 重構 `useIsMobile` 成薄包裝**

改寫 `spa/src/hooks/useIsMobile.ts` 全檔：

```ts
import { useMediaQuery } from './useMediaQuery'

const MOBILE_BREAKPOINT = 768

export function useIsMobile(): boolean {
  return useMediaQuery(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
}
```

- [ ] **Step 6: 確認既有 `useIsMobile.test.ts` 仍過**

既有測試用 `vi.stubGlobal('matchMedia', ...)`；重構後仍會呼叫 `window.matchMedia`，行為一致。

Run: `cd spa && npx vitest run src/hooks/useIsMobile.test.ts`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add spa/src/hooks/useMediaQuery.ts spa/src/hooks/useMediaQuery.test.ts spa/src/hooks/useIsMobile.ts
git commit -m "refactor(hooks): extract useMediaQuery from useIsMobile"
```

---

## Task 4: `interface-subsection-registry` + test

**Files:**
- Create: `spa/src/lib/interface-subsection-registry.ts`
- Test: `spa/src/lib/interface-subsection-registry.test.ts`

與 `settings-section-registry` 同形，但 `register` 必須是 upsert（避免 HMR 重跑 `registerBuiltinModules` 產生重複）。

- [ ] **Step 1: 寫 failing test**

建 `spa/src/lib/interface-subsection-registry.test.ts`：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerInterfaceSubsection,
  getInterfaceSubsections,
  clearInterfaceSubsectionRegistry,
  type InterfaceSubsection,
} from './interface-subsection-registry'

const Fake = () => null

function make(overrides: Partial<InterfaceSubsection> = {}): InterfaceSubsection {
  return { id: 'test', label: 'Test', order: 0, component: Fake, ...overrides }
}

describe('interface-subsection-registry', () => {
  beforeEach(() => clearInterfaceSubsectionRegistry())

  it('registers and sorts by order', () => {
    registerInterfaceSubsection(make({ id: 'a', order: 2 }))
    registerInterfaceSubsection(make({ id: 'b', order: 0 }))
    registerInterfaceSubsection(make({ id: 'c', order: 1 }))
    expect(getInterfaceSubsections().map((s) => s.id)).toEqual(['b', 'c', 'a'])
  })

  it('re-registering same id updates in place (upsert)', () => {
    registerInterfaceSubsection(make({ id: 'x', label: 'Old' }))
    registerInterfaceSubsection(make({ id: 'x', label: 'New' }))
    const items = getInterfaceSubsections()
    expect(items).toHaveLength(1)
    expect(items[0].label).toBe('New')
  })

  it('returns a copy, not the internal array', () => {
    registerInterfaceSubsection(make())
    expect(getInterfaceSubsections()).not.toBe(getInterfaceSubsections())
  })

  it('supports disabled subsections', () => {
    registerInterfaceSubsection(make({ id: 'pane', disabled: true, disabledReason: 'settings.coming_soon' }))
    const [only] = getInterfaceSubsections()
    expect(only.disabled).toBe(true)
    expect(only.disabledReason).toBe('settings.coming_soon')
  })

  it('clear removes all', () => {
    registerInterfaceSubsection(make({ id: 'a' }))
    registerInterfaceSubsection(make({ id: 'b' }))
    clearInterfaceSubsectionRegistry()
    expect(getInterfaceSubsections()).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run failing test**

Run: `cd spa && npx vitest run src/lib/interface-subsection-registry.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: 實作**

建 `spa/src/lib/interface-subsection-registry.ts`：

```ts
import type { ComponentType } from 'react'

export interface InterfaceSubsection {
  id: string
  label: string          // i18n key
  order: number
  component: ComponentType
  disabled?: boolean
  disabledReason?: string // i18n key
}

const subsections: InterfaceSubsection[] = []

export function registerInterfaceSubsection(def: InterfaceSubsection): void {
  const idx = subsections.findIndex((s) => s.id === def.id)
  if (idx >= 0) {
    subsections[idx] = def
  } else {
    subsections.push(def)
  }
  subsections.sort((a, b) => a.order - b.order)
}

export function getInterfaceSubsections(): InterfaceSubsection[] {
  return [...subsections]
}

export function clearInterfaceSubsectionRegistry(): void {
  subsections.length = 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/interface-subsection-registry.test.ts`
Expected: PASS（5 tests）

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/interface-subsection-registry.ts spa/src/lib/interface-subsection-registry.test.ts
git commit -m "feat(interface): add interface-subsection registry (upsert)"
```

---

## Task 5: `NEW_TAB_LAYOUT` storage key

**Files:**
- Modify: `spa/src/lib/storage/keys.ts`

給 store persist 用的 key，加在 `STORAGE_KEYS` single source of truth。

- [ ] **Step 1: 加入 key**

修改 `spa/src/lib/storage/keys.ts`：在 `LAYOUT: 'purdex-layout'` 下一行加：

```ts
  NEW_TAB_LAYOUT: 'purdex-newtab-layout',
```

- [ ] **Step 2: Typecheck**

Run: `cd spa && pnpm run build` (或 `npx tsc -b --noEmit`)
Expected: 通過（純常數新增）

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/storage/keys.ts
git commit -m "feat(storage): add NEW_TAB_LAYOUT key"
```

---

## Task 6: `useNewTabLayoutStore` + test

**Files:**
- Create: `spa/src/stores/useNewTabLayoutStore.ts`
- Test: `spa/src/stores/useNewTabLayoutStore.test.ts`

Zustand store：persist 到 `purdex-newtab-layout`，以 `knownIds` 為中心的 `ensureDefaults`，單一寫入 API `placeModule` 處理 add / reorder / cross-column。

- [ ] **Step 1: 寫 failing tests（store 全套）**

建 `spa/src/stores/useNewTabLayoutStore.test.ts`：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useNewTabLayoutStore } from './useNewTabLayoutStore'

beforeEach(() => {
  // 使用 getInitialState 以避開 persist 寫入的殘留
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
})

describe('useNewTabLayoutStore', () => {
  describe('initial state', () => {
    it('profiles have correct column counts', () => {
      const { profiles } = useNewTabLayoutStore.getState()
      expect(profiles['3col'].columns).toHaveLength(3)
      expect(profiles['2col'].columns).toHaveLength(2)
      expect(profiles['1col'].columns).toHaveLength(1)
    })

    it('only 1col enabled by default', () => {
      const { profiles } = useNewTabLayoutStore.getState()
      expect(profiles['1col'].enabled).toBe(true)
      expect(profiles['2col'].enabled).toBe(false)
      expect(profiles['3col'].enabled).toBe(false)
    })

    it('activeEditingProfile default 1col; knownIds empty', () => {
      const s = useNewTabLayoutStore.getState()
      expect(s.activeEditingProfile).toBe('1col')
      expect(s.knownIds).toEqual([])
    })
  })

  describe('setEnabled', () => {
    it('toggles 3col and 2col', () => {
      useNewTabLayoutStore.getState().setEnabled('3col', true)
      expect(useNewTabLayoutStore.getState().profiles['3col'].enabled).toBe(true)
      useNewTabLayoutStore.getState().setEnabled('2col', true)
      expect(useNewTabLayoutStore.getState().profiles['2col'].enabled).toBe(true)
    })

    it('ignores disable on 1col', () => {
      useNewTabLayoutStore.getState().setEnabled('1col', false)
      expect(useNewTabLayoutStore.getState().profiles['1col'].enabled).toBe(true)
    })
  })

  describe('setEditing', () => {
    it('switches active editing profile', () => {
      useNewTabLayoutStore.getState().setEditing('3col')
      expect(useNewTabLayoutStore.getState().activeEditingProfile).toBe('3col')
    })
  })

  describe('placeModule', () => {
    it('inserts into empty column', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['a'])
    })

    it('appends to non-empty column (rowIdx beyond length clamps to end)', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
      useNewTabLayoutStore.getState().placeModule('1col', 'b', 0, 99)
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['a', 'b'])
    })

    it('moving same-column downward compensates for index shift', () => {
      // 初始: col0 = [a, b, c, d]
      const s = useNewTabLayoutStore.getState()
      s.placeModule('1col', 'a', 0, 0)
      s.placeModule('1col', 'b', 0, 1)
      s.placeModule('1col', 'c', 0, 2)
      s.placeModule('1col', 'd', 0, 3)
      // 把 a 移到 index 2（b 之後、c 之前）：期望 [b, a, c, d]
      s.placeModule('1col', 'a', 0, 2)
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['b', 'a', 'c', 'd'])
    })

    it('moving same-column to end places at true end (no compensation needed)', () => {
      const s = useNewTabLayoutStore.getState()
      s.placeModule('1col', 'a', 0, 0)
      s.placeModule('1col', 'b', 0, 1)
      s.placeModule('1col', 'c', 0, 2)
      // 移 a 到末尾（toRow = 3 原值）
      s.placeModule('1col', 'a', 0, 3)
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['b', 'c', 'a'])
    })

    it('cross-column move removes from source and inserts at target', () => {
      useNewTabLayoutStore.setState((state) => ({
        profiles: {
          ...state.profiles,
          '3col': { enabled: false, columns: [['a', 'b'], ['c'], []] },
        },
      }))
      useNewTabLayoutStore.getState().placeModule('3col', 'a', 2, 0)
      const cols = useNewTabLayoutStore.getState().profiles['3col'].columns
      expect(cols[0]).toEqual(['b'])
      expect(cols[2]).toEqual(['a'])
    })

    it('cross-profile placement is independent', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'x', 0, 0)
      useNewTabLayoutStore.getState().placeModule('2col', 'x', 0, 0)
      const s = useNewTabLayoutStore.getState()
      expect(s.profiles['1col'].columns[0]).toContain('x')
      expect(s.profiles['2col'].columns[0]).toContain('x')
    })

    it('negative rowIdx clamps to 0', () => {
      useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, -5)
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['a'])
    })
  })

  describe('removeModule', () => {
    it('removes from all occurrences in a profile', () => {
      useNewTabLayoutStore.setState((state) => ({
        profiles: {
          ...state.profiles,
          '3col': { enabled: false, columns: [['a'], ['b'], ['c']] },
        },
      }))
      useNewTabLayoutStore.getState().removeModule('3col', 'b')
      expect(useNewTabLayoutStore.getState().profiles['3col'].columns[1]).toEqual([])
    })

    it('is a no-op when id not present', () => {
      const before = useNewTabLayoutStore.getState().profiles
      useNewTabLayoutStore.getState().removeModule('1col', 'nope')
      expect(useNewTabLayoutStore.getState().profiles).toEqual(before)
    })
  })

  describe('ensureDefaults', () => {
    it('populates shortest column of EVERY profile on first call', () => {
      useNewTabLayoutStore.getState().ensureDefaults([
        { id: 'a', order: 0 },
        { id: 'b', order: 1 },
        { id: 'c', order: 2 },
      ])
      const { profiles, knownIds } = useNewTabLayoutStore.getState()
      // 1col: 所有都落單一欄
      expect(profiles['1col'].columns[0]).toEqual(['a', 'b', 'c'])
      // 2col: shortest 依序切 ['a','c'] / ['b']
      expect(profiles['2col'].columns[0]).toEqual(['a', 'c'])
      expect(profiles['2col'].columns[1]).toEqual(['b'])
      // 3col: shortest 依序切 ['a'] / ['b'] / ['c']
      expect(profiles['3col'].columns[0]).toEqual(['a'])
      expect(profiles['3col'].columns[1]).toEqual(['b'])
      expect(profiles['3col'].columns[2]).toEqual(['c'])
      expect(knownIds).toEqual(['a', 'b', 'c'])
    })

    it('skips providers with disabled=true', () => {
      useNewTabLayoutStore.getState().ensureDefaults([
        { id: 'a', order: 0 },
        { id: 'b', order: 1, disabled: true },
      ])
      const { profiles, knownIds } = useNewTabLayoutStore.getState()
      expect(knownIds).toEqual(['a'])
      expect(profiles['1col'].columns[0]).toEqual(['a'])
    })

    it('does not re-add ids already in knownIds (user removal persists)', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      useNewTabLayoutStore.getState().removeModule('1col', 'a')
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual([])
    })

    it('does not prune ids whose provider disappeared (render-time skip)', () => {
      useNewTabLayoutStore.getState().ensureDefaults([{ id: 'a', order: 0 }])
      useNewTabLayoutStore.getState().ensureDefaults([]) // a removed from registry
      expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).toEqual(['a'])
    })

    it('respects order ascending', () => {
      useNewTabLayoutStore.getState().ensureDefaults([
        { id: 'b', order: 5 },
        { id: 'a', order: -10 },
      ])
      expect(useNewTabLayoutStore.getState().knownIds).toEqual(['a', 'b'])
    })
  })

  describe('reset', () => {
    it('restores initial state', () => {
      useNewTabLayoutStore.getState().setEnabled('3col', true)
      useNewTabLayoutStore.getState().placeModule('1col', 'x', 0, 0)
      useNewTabLayoutStore.getState().reset()
      const s = useNewTabLayoutStore.getState()
      expect(s.profiles['3col'].enabled).toBe(false)
      expect(s.profiles['1col'].columns[0]).toEqual([])
      expect(s.knownIds).toEqual([])
    })
  })
})
```

- [ ] **Step 2: Run failing test**

Run: `cd spa && npx vitest run src/stores/useNewTabLayoutStore.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: 實作 store**

建 `spa/src/stores/useNewTabLayoutStore.ts`：

```ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'
import type { Profile, ProfileKey } from '../lib/resolve-profile'

export type { Profile, ProfileKey }

interface ProviderInfo {
  id: string
  order: number
  disabled?: boolean
}

interface State {
  profiles: Record<ProfileKey, Profile>
  knownIds: string[]
  activeEditingProfile: ProfileKey

  setEnabled: (p: ProfileKey, enabled: boolean) => void
  setEditing: (p: ProfileKey) => void
  placeModule: (p: ProfileKey, providerId: string, colIdx: number, rowIdx: number) => void
  removeModule: (p: ProfileKey, providerId: string) => void
  ensureDefaults: (providers: ProviderInfo[]) => void
  reset: () => void
}

function initialState(): Pick<State, 'profiles' | 'knownIds' | 'activeEditingProfile'> {
  return {
    profiles: {
      '3col': { enabled: false, columns: [[], [], []] },
      '2col': { enabled: false, columns: [[], []] },
      '1col': { enabled: true, columns: [[]] },
    },
    knownIds: [],
    activeEditingProfile: '1col',
  }
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, n))
}

function shortestColIdx(cols: string[][]): number {
  let best = 0
  for (let i = 1; i < cols.length; i++) {
    if (cols[i].length < cols[best].length) best = i
  }
  return best
}

function cloneProfile(p: Profile): Profile {
  return { enabled: p.enabled, columns: p.columns.map((c) => [...c]) }
}

function placeIn(profile: Profile, id: string, colIdx: number, rowIdx: number): Profile {
  const next = cloneProfile(profile)
  // find and remove existing
  let fromCol = -1
  let fromRow = -1
  for (let c = 0; c < next.columns.length; c++) {
    const i = next.columns[c].indexOf(id)
    if (i >= 0) {
      fromCol = c
      fromRow = i
      next.columns[c].splice(i, 1)
      break
    }
  }
  const target = next.columns[colIdx]
  if (!target) return profile // unknown colIdx — defensive no-op
  let toRow = clamp(rowIdx, 0, target.length)
  // same-column downward move: compensate for removed slot
  // off-by-one only when insertion point is BEFORE the end AND original was earlier in the same column
  if (fromCol === colIdx && fromRow >= 0 && fromRow < toRow && toRow < target.length + 1) {
    // After splice, indices >= fromRow shifted down by 1.
    // If caller supplied an index into the PRE-removal array AND that index is strictly
    // past fromRow (i.e. user wanted to drop AFTER the old slot), we should NOT compensate
    // — because splice already shifted. So actually: if caller passed the post-removal
    // insertion index, no compensation needed; if caller passed pre-removal, decrement.
    // We follow spec §placeModule: decrement when fromRow < toRow AND toRow !== post-removal length.
    const postLen = target.length
    if (toRow > postLen) toRow = postLen // clamp past-end already handled above
    else if (toRow === postLen) { /* appending — no decrement */ }
    else toRow = toRow - 1 < fromRow ? toRow - 1 : toRow - 1 // decrement by 1
  }
  target.splice(toRow, 0, id)
  return next
}

export const useNewTabLayoutStore = create<State>()(
  persist(
    (set) => ({
      ...initialState(),

      setEnabled: (p, enabled) =>
        set((state) => {
          if (p === '1col' && !enabled) return state
          return {
            profiles: {
              ...state.profiles,
              [p]: { ...state.profiles[p], enabled },
            },
          }
        }),

      setEditing: (p) => set({ activeEditingProfile: p }),

      placeModule: (p, providerId, colIdx, rowIdx) =>
        set((state) => ({
          profiles: {
            ...state.profiles,
            [p]: placeIn(state.profiles[p], providerId, colIdx, rowIdx),
          },
        })),

      removeModule: (p, providerId) =>
        set((state) => {
          const next = cloneProfile(state.profiles[p])
          let changed = false
          for (const col of next.columns) {
            const i = col.indexOf(providerId)
            if (i >= 0) {
              col.splice(i, 1)
              changed = true
            }
          }
          if (!changed) return state
          return { profiles: { ...state.profiles, [p]: next } }
        }),

      ensureDefaults: (providers) =>
        set((state) => {
          const known = new Set(state.knownIds)
          const newcomers = providers
            .filter((p) => !known.has(p.id) && !p.disabled)
            .sort((a, b) => a.order - b.order)
          if (newcomers.length === 0) return state

          const profiles = { ...state.profiles }
          for (const key of ['3col', '2col', '1col'] as const) {
            profiles[key] = cloneProfile(profiles[key])
          }
          const knownIds = [...state.knownIds]

          for (const p of newcomers) {
            for (const key of ['3col', '2col', '1col'] as const) {
              const cols = profiles[key].columns
              cols[shortestColIdx(cols)].push(p.id)
            }
            knownIds.push(p.id)
          }

          return { profiles, knownIds }
        }),

      reset: () => set({ ...initialState() }),
    }),
    {
      name: STORAGE_KEYS.NEW_TAB_LAYOUT,
      storage: purdexStorage,
      version: 1,
      // alpha 慣例：不寫 migrate；版本不符 Zustand 自動 reset
      partialize: (state) => ({
        profiles: state.profiles,
        knownIds: state.knownIds,
        activeEditingProfile: state.activeEditingProfile,
      }),
    },
  ),
)
```

**關於 off-by-one 的實作備註**：`placeIn` 處理同欄向下移動的「caller passed 原陣列 index」情況——先 splice-remove 出原 id，再 clamp 到 post-removal target 長度，如果 `fromRow < toRow` 且 `toRow` 不是末尾，就 `toRow--`。末尾（`toRow === target.length`）無需補償。測試 `moving same-column downward compensates for index shift` 與 `moving same-column to end places at true end` 驗證兩路徑。

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/stores/useNewTabLayoutStore.test.ts`
Expected: PASS（全套）

如果「same-column downward」一題 fail，簡化實作為：

```ts
if (fromCol === colIdx && fromRow !== -1 && fromRow < toRow && toRow < target.length + 1) {
  toRow = Math.max(0, toRow - 1)
}
```

再 clamp：`toRow = clamp(toRow, 0, target.length)`。

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useNewTabLayoutStore.ts spa/src/stores/useNewTabLayoutStore.test.ts
git commit -m "feat(new-tab): add useNewTabLayoutStore with knownIds-based ensureDefaults"
```

---

## Task 7: `NewTabModulePalette` component + test

**Files:**
- Create: `spa/src/components/settings/new-tab/NewTabModulePalette.tsx`
- Test: `spa/src/components/settings/new-tab/NewTabModulePalette.test.tsx`

顯示全部 provider chip：`inUse=true` 灰出 + 不可拖；`unavailable=true` 灰出但可放。點擊 → `onClickAdd`。Palette 整塊是 droppable（drop 到此 = 移除）。拖曳以 dnd-kit `useDraggable` 實作 chip 本身（canvas 的 `useSortable` 另於 NewTabCanvas 任務處理）。

- [ ] **Step 1: 寫 component test**

建 `spa/src/components/settings/new-tab/NewTabModulePalette.test.tsx`：

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { NewTabModulePalette } from './NewTabModulePalette'

function renderWithDnd(ui: React.ReactElement) {
  return render(<DndContext>{ui}</DndContext>)
}

describe('NewTabModulePalette', () => {
  const items = [
    { id: 'a', label: 'provider.a', icon: 'List', inUse: false },
    { id: 'b', label: 'provider.b', icon: 'List', inUse: true },
    { id: 'c', label: 'provider.c', icon: 'List', inUse: false, unavailable: true },
  ]

  it('renders a chip per item', () => {
    renderWithDnd(<NewTabModulePalette items={items} onClickAdd={() => {}} />)
    expect(screen.getByTestId('palette-chip-a')).toBeInTheDocument()
    expect(screen.getByTestId('palette-chip-b')).toBeInTheDocument()
    expect(screen.getByTestId('palette-chip-c')).toBeInTheDocument()
  })

  it('marks inUse chips as disabled-looking and non-clickable for add', () => {
    const onClickAdd = vi.fn()
    renderWithDnd(<NewTabModulePalette items={items} onClickAdd={onClickAdd} />)
    fireEvent.click(screen.getByTestId('palette-chip-b'))
    expect(onClickAdd).not.toHaveBeenCalled()
  })

  it('fires onClickAdd for available, not-in-use chips', () => {
    const onClickAdd = vi.fn()
    renderWithDnd(<NewTabModulePalette items={items} onClickAdd={onClickAdd} />)
    fireEvent.click(screen.getByTestId('palette-chip-a'))
    expect(onClickAdd).toHaveBeenCalledWith('a')
  })

  it('marks unavailable chips with an "unavailable" data attribute', () => {
    renderWithDnd(<NewTabModulePalette items={items} onClickAdd={() => {}} />)
    expect(screen.getByTestId('palette-chip-c')).toHaveAttribute('data-unavailable', 'true')
  })
})
```

- [ ] **Step 2: Run failing test**

Run: `cd spa && npx vitest run src/components/settings/new-tab/NewTabModulePalette.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: 實作**

建 `spa/src/components/settings/new-tab/NewTabModulePalette.tsx`：

```tsx
import { useDraggable, useDroppable } from '@dnd-kit/core'
import { useI18nStore } from '../../../stores/useI18nStore'

export interface PaletteItem {
  id: string
  label: string       // i18n key
  icon: string
  inUse: boolean
  unavailable?: boolean
}

interface Props {
  items: PaletteItem[]
  onClickAdd: (id: string) => void
}

function Chip({ item, onClickAdd }: { item: PaletteItem; onClickAdd: (id: string) => void }) {
  const t = useI18nStore((s) => s.t)
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: `palette:${item.id}`,
    disabled: item.inUse,
    data: { type: 'palette', providerId: item.id },
  })

  const disabled = item.inUse
  const className = [
    'inline-flex items-center gap-1 px-2 py-1 rounded-md text-xs border select-none',
    disabled ? 'text-text-muted border-border-subtle bg-transparent cursor-not-allowed'
             : 'text-text-primary border-border-default bg-surface-elevated cursor-grab hover:bg-white/5',
    isDragging ? 'opacity-40' : '',
    item.unavailable ? 'italic' : '',
  ].filter(Boolean).join(' ')

  return (
    <button
      ref={setNodeRef}
      {...listeners}
      {...attributes}
      data-testid={`palette-chip-${item.id}`}
      data-unavailable={item.unavailable ? 'true' : undefined}
      data-in-use={item.inUse ? 'true' : undefined}
      disabled={disabled}
      onClick={() => { if (!disabled) onClickAdd(item.id) }}
      className={className}
      type="button"
    >
      <span>{t(item.label)}</span>
      {item.inUse && <span className="ml-1 text-[10px]">{t('settings.interface.palette_in_use')}</span>}
      {item.unavailable && <span className="ml-1 text-[10px]">{t('settings.interface.palette_unavailable')}</span>}
    </button>
  )
}

export function NewTabModulePalette({ items, onClickAdd }: Props) {
  // Palette itself is a droppable zone (drop here = remove from canvas)
  const { setNodeRef, isOver } = useDroppable({ id: 'palette-zone', data: { type: 'palette-zone' } })
  return (
    <div
      ref={setNodeRef}
      data-testid="new-tab-palette"
      data-over={isOver ? 'true' : undefined}
      className={`flex flex-wrap gap-2 p-3 border-b border-border-subtle ${isOver ? 'bg-white/5' : ''}`}
    >
      {items.map((it) => <Chip key={it.id} item={it} onClickAdd={onClickAdd} />)}
    </div>
  )
}
```

- [ ] **Step 4: Run test**

Run: `cd spa && npx vitest run src/components/settings/new-tab/NewTabModulePalette.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/new-tab/NewTabModulePalette.tsx spa/src/components/settings/new-tab/NewTabModulePalette.test.tsx
git commit -m "feat(new-tab): add NewTabModulePalette with draggable chips"
```

---

## Task 8: `NewTabProfileSwitcher` + test

**Files:**
- Create: `spa/src/components/settings/new-tab/NewTabProfileSwitcher.tsx`
- Test: `spa/src/components/settings/new-tab/NewTabProfileSwitcher.test.tsx`

自己以 Zustand selector 訂閱 store 算 `isEmpty`。`1col` toggle 為 locked。顯示預填但 disabled 提示文案。

- [ ] **Step 1: 寫 component test**

建 `spa/src/components/settings/new-tab/NewTabProfileSwitcher.test.tsx`：

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { NewTabProfileSwitcher } from './NewTabProfileSwitcher'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'

beforeEach(() => {
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
})

describe('NewTabProfileSwitcher', () => {
  it('highlights active profile', () => {
    const onSelect = vi.fn()
    render(
      <NewTabProfileSwitcher
        active="1col"
        onSelect={onSelect}
        onToggleEnabled={() => {}}
        renderMain={() => <div>main</div>}
        renderThumb={(k) => <div>{`thumb-${k}`}</div>}
      />
    )
    expect(screen.getByTestId('profile-tab-1col')).toHaveAttribute('data-active', 'true')
    expect(screen.getByTestId('profile-tab-3col')).not.toHaveAttribute('data-active')
  })

  it('calls onSelect for each tab', () => {
    const onSelect = vi.fn()
    render(
      <NewTabProfileSwitcher
        active="1col"
        onSelect={onSelect}
        onToggleEnabled={() => {}}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    fireEvent.click(screen.getByTestId('profile-tab-3col'))
    expect(onSelect).toHaveBeenCalledWith('3col')
  })

  it('calls onToggleEnabled for 3col/2col but not 1col (locked)', () => {
    const onToggle = vi.fn()
    render(
      <NewTabProfileSwitcher
        active="1col"
        onSelect={() => {}}
        onToggleEnabled={onToggle}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    fireEvent.click(screen.getByTestId('profile-toggle-3col'))
    expect(onToggle).toHaveBeenCalledWith('3col', true)

    fireEvent.click(screen.getByTestId('profile-toggle-1col'))
    expect(onToggle).not.toHaveBeenCalledWith('1col', expect.anything())
  })

  it('shows prefilled hint when profile has content but is disabled', () => {
    // Store 預設：3col/2col disabled 且空 → 無提示
    // 放一個 module 到 3col → 3col enabled=false && !isEmpty → 顯示提示
    useNewTabLayoutStore.getState().placeModule('3col', 'a', 0, 0)
    render(
      <NewTabProfileSwitcher
        active="1col"
        onSelect={() => {}}
        onToggleEnabled={() => {}}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    expect(screen.getByTestId('profile-hint-3col')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-hint-1col')).not.toBeInTheDocument()
  })

  it('shows empty badge when profile has no content', () => {
    // 預設 1col enabled 且空
    render(
      <NewTabProfileSwitcher
        active="1col"
        onSelect={() => {}}
        onToggleEnabled={() => {}}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    expect(screen.getByTestId('profile-empty-1col')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run failing test**

Run: `cd spa && npx vitest run src/components/settings/new-tab/NewTabProfileSwitcher.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: 實作**

建 `spa/src/components/settings/new-tab/NewTabProfileSwitcher.tsx`：

```tsx
import { useI18nStore } from '../../../stores/useI18nStore'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import type { ProfileKey } from '../../../lib/resolve-profile'

interface Props {
  active: ProfileKey
  onSelect: (k: ProfileKey) => void
  onToggleEnabled: (k: ProfileKey, enabled: boolean) => void
  renderMain: (k: ProfileKey) => React.ReactNode
  renderThumb: (k: ProfileKey) => React.ReactNode
}

const KEYS: ProfileKey[] = ['3col', '2col', '1col']
const LABEL_KEY: Record<ProfileKey, string> = {
  '3col': 'settings.interface.profile_3col',
  '2col': 'settings.interface.profile_2col',
  '1col': 'settings.interface.profile_1col',
}

export function NewTabProfileSwitcher({ active, onSelect, onToggleEnabled, renderMain, renderThumb }: Props) {
  const t = useI18nStore((s) => s.t)
  const profiles = useNewTabLayoutStore((s) => s.profiles)

  const meta = (k: ProfileKey) => {
    const p = profiles[k]
    return {
      enabled: p.enabled,
      isEmpty: p.columns.flat().length === 0,
      locked: k === '1col',
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-1 px-3 pt-2">
        {KEYS.map((k) => {
          const m = meta(k)
          return (
            <div key={k} className="flex items-center gap-1">
              <button
                type="button"
                data-testid={`profile-tab-${k}`}
                data-active={k === active ? 'true' : undefined}
                onClick={() => onSelect(k)}
                className={[
                  'px-2 py-1 text-xs rounded-md transition-colors cursor-pointer',
                  k === active
                    ? 'bg-surface-elevated text-text-primary border border-border-active'
                    : 'text-text-secondary hover:bg-white/5 border border-transparent',
                ].join(' ')}
              >
                {t(LABEL_KEY[k])}
                {m.isEmpty && (
                  <span data-testid={`profile-empty-${k}`} className="ml-1 text-[10px] text-text-muted">
                    {t('settings.interface.profile_empty')}
                  </span>
                )}
              </button>
              <label className="inline-flex items-center gap-1 text-[10px] text-text-secondary select-none">
                <input
                  type="checkbox"
                  data-testid={`profile-toggle-${k}`}
                  checked={m.enabled}
                  disabled={m.locked}
                  onChange={(e) => { if (!m.locked) onToggleEnabled(k, e.target.checked) }}
                  title={m.locked ? t('settings.interface.profile_locked') : undefined}
                />
                <span>{t('settings.interface.enabled')}</span>
              </label>
              {!m.enabled && !m.isEmpty && !m.locked && (
                <span data-testid={`profile-hint-${k}`} className="text-[10px] text-text-muted">
                  {t('settings.interface.profile_prefilled')}
                </span>
              )}
            </div>
          )
        })}
      </div>
      <div className="flex-1 px-3">{renderMain(active)}</div>
      <div className="flex gap-2 px-3 pb-3">
        {KEYS.filter((k) => k !== active).map((k) => (
          <button
            key={k}
            type="button"
            className="border border-border-subtle rounded-md p-1 cursor-pointer hover:bg-white/5"
            onClick={() => onSelect(k)}
            data-testid={`profile-thumb-${k}`}
            title={t(LABEL_KEY[k])}
          >
            {renderThumb(k)}
          </button>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run test**

Run: `cd spa && npx vitest run src/components/settings/new-tab/NewTabProfileSwitcher.test.tsx`
Expected: PASS（加入 `settings.interface.enabled` 到 locales，參見 Task 13）—— 測試 render 時 `t()` 回傳 key 本身即可通過。

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/new-tab/NewTabProfileSwitcher.tsx spa/src/components/settings/new-tab/NewTabProfileSwitcher.test.tsx
git commit -m "feat(new-tab): add NewTabProfileSwitcher with prefilled hints"
```

---

## Task 9: `NewTabCanvas` + `NewTabThumbnail` + test

**Files:**
- Create: `spa/src/components/settings/new-tab/NewTabCanvas.tsx`
- Create: `spa/src/components/settings/new-tab/NewTabThumbnail.tsx`
- Test: `spa/src/components/settings/new-tab/NewTabCanvas.test.tsx`

畫布渲染當前 profile 欄位；每欄內用 `useSortable` 以 id 清單排序；`×` 按鈕移除。`NewTabThumbnail` 只是縮圖示意（不拖曳），可直接從 store 讀 profile 繪簡單方塊。**DndContext 由上層 NewTabSubsection 提供**（Task 10），本 task 只實作 canvas 內部。

- [ ] **Step 1: 寫 canvas test**

建 `spa/src/components/settings/new-tab/NewTabCanvas.test.tsx`：

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { NewTabCanvas } from './NewTabCanvas'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import { clearNewTabRegistry, registerNewTabProvider } from '../../../lib/new-tab-registry'

beforeEach(() => {
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
  clearNewTabRegistry()
  const Dummy = () => null
  registerNewTabProvider({ id: 'a', label: 'a.label', icon: 'List', order: 0, component: Dummy })
  registerNewTabProvider({ id: 'b', label: 'b.label', icon: 'List', order: 1, component: Dummy })
})

function wrap(ui: React.ReactElement) {
  return <DndContext>{ui}</DndContext>
}

describe('NewTabCanvas', () => {
  it('renders the correct number of columns for each profile', () => {
    const { rerender } = render(wrap(<NewTabCanvas profileKey="3col" />))
    expect(screen.getAllByTestId(/^canvas-column-3col-\d+$/)).toHaveLength(3)
    rerender(wrap(<NewTabCanvas profileKey="2col" />))
    expect(screen.getAllByTestId(/^canvas-column-2col-\d+$/)).toHaveLength(2)
    rerender(wrap(<NewTabCanvas profileKey="1col" />))
    expect(screen.getAllByTestId(/^canvas-column-1col-\d+$/)).toHaveLength(1)
  })

  it('renders items placed in profile', () => {
    useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
    useNewTabLayoutStore.getState().placeModule('1col', 'b', 0, 1)
    render(wrap(<NewTabCanvas profileKey="1col" />))
    expect(screen.getByTestId('canvas-item-1col-a')).toBeInTheDocument()
    expect(screen.getByTestId('canvas-item-1col-b')).toBeInTheDocument()
  })

  it('shows empty placeholder for columns with no items', () => {
    render(wrap(<NewTabCanvas profileKey="3col" />))
    expect(screen.getAllByTestId(/^canvas-column-empty-3col-\d+$/)).toHaveLength(3)
  })

  it('remove button calls store.removeModule', () => {
    useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
    render(wrap(<NewTabCanvas profileKey="1col" />))
    fireEvent.click(screen.getByTestId('canvas-remove-1col-a'))
    expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).not.toContain('a')
  })

  it('skips unknown provider ids silently (does not throw)', () => {
    useNewTabLayoutStore.getState().placeModule('1col', 'ghost', 0, 0)
    expect(() => render(wrap(<NewTabCanvas profileKey="1col" />))).not.toThrow()
    expect(screen.queryByTestId('canvas-item-1col-ghost')).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run failing test**

Run: `cd spa && npx vitest run src/components/settings/new-tab/NewTabCanvas.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 3: 實作 `NewTabCanvas`**

建 `spa/src/components/settings/new-tab/NewTabCanvas.tsx`：

```tsx
import { useMemo } from 'react'
import { useDroppable } from '@dnd-kit/core'
import { SortableContext, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { X } from '@phosphor-icons/react'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import { getNewTabProviders } from '../../../lib/new-tab-registry'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { ProfileKey } from '../../../lib/resolve-profile'

interface Props { profileKey: ProfileKey }

function SortableItem({ profileKey, id, label, onRemove }: {
  profileKey: ProfileKey; id: string; label: string; onRemove: () => void
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: `item:${profileKey}:${id}`,
    data: { type: 'canvas-item', providerId: id, profileKey },
  })
  const style = {
    transform: CSS.Translate.toString(transform),
    transition,
    opacity: isDragging ? 0.4 : 1,
  }
  return (
    <div
      ref={setNodeRef}
      style={style}
      data-testid={`canvas-item-${profileKey}-${id}`}
      className="flex items-center justify-between px-3 py-2 rounded-md bg-surface-elevated border border-border-subtle text-xs"
    >
      <button {...listeners} {...attributes} className="flex-1 text-left cursor-grab select-none" type="button">
        {label}
      </button>
      <button
        type="button"
        onClick={onRemove}
        data-testid={`canvas-remove-${profileKey}-${id}`}
        className="text-text-muted hover:text-text-primary cursor-pointer p-1"
        aria-label="remove"
      >
        <X size={12} />
      </button>
    </div>
  )
}

function Column({ profileKey, colIdx, ids }: { profileKey: ProfileKey; colIdx: number; ids: string[] }) {
  const t = useI18nStore((s) => s.t)
  const removeModule = useNewTabLayoutStore((s) => s.removeModule)
  const providers = useMemo(() => getNewTabProviders(), [])
  const byId = useMemo(() => Object.fromEntries(providers.map((p) => [p.id, p])), [providers])
  const { setNodeRef, isOver } = useDroppable({
    id: `col:${profileKey}:${colIdx}`,
    data: { type: 'column', profileKey, colIdx },
  })
  const sortableIds = ids.map((id) => `item:${profileKey}:${id}`)
  return (
    <div
      ref={setNodeRef}
      data-testid={`canvas-column-${profileKey}-${colIdx}`}
      data-over={isOver ? 'true' : undefined}
      className={[
        'flex flex-col gap-2 p-2 rounded-md min-h-32 border',
        isOver ? 'border-border-active bg-white/5' : 'border-border-subtle',
      ].join(' ')}
    >
      <SortableContext items={sortableIds} strategy={verticalListSortingStrategy}>
        {ids.map((id) => {
          const p = byId[id]
          if (!p) return null
          return (
            <SortableItem
              key={id}
              profileKey={profileKey}
              id={id}
              label={t(p.label)}
              onRemove={() => removeModule(profileKey, id)}
            />
          )
        })}
      </SortableContext>
      {ids.length === 0 && (
        <div
          data-testid={`canvas-column-empty-${profileKey}-${colIdx}`}
          className="flex-1 flex items-center justify-center text-[11px] text-text-muted"
        >
          {t('settings.interface.canvas_drop_here')}
        </div>
      )}
    </div>
  )
}

export function NewTabCanvas({ profileKey }: Props) {
  const profile = useNewTabLayoutStore((s) => s.profiles[profileKey])
  const gridCols = profile.columns.length === 3 ? 'grid-cols-3'
                 : profile.columns.length === 2 ? 'grid-cols-2'
                 : 'grid-cols-1'
  return (
    <div className={`grid gap-3 ${gridCols}`} data-testid={`canvas-${profileKey}`}>
      {profile.columns.map((ids, i) => (
        <Column key={i} profileKey={profileKey} colIdx={i} ids={ids} />
      ))}
    </div>
  )
}
```

- [ ] **Step 4: 實作 `NewTabThumbnail`**

建 `spa/src/components/settings/new-tab/NewTabThumbnail.tsx`：

```tsx
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import type { ProfileKey } from '../../../lib/resolve-profile'

interface Props { profileKey: ProfileKey }

export function NewTabThumbnail({ profileKey }: Props) {
  const profile = useNewTabLayoutStore((s) => s.profiles[profileKey])
  const gridCols = profile.columns.length === 3 ? 'grid-cols-3'
                 : profile.columns.length === 2 ? 'grid-cols-2'
                 : 'grid-cols-1'
  return (
    <div className={`grid gap-[2px] w-16 h-12 ${gridCols}`} aria-hidden="true">
      {profile.columns.map((ids, i) => (
        <div key={i} className="flex flex-col gap-[2px] rounded-sm bg-surface-elevated p-[2px]">
          {ids.slice(0, 6).map((id) => (
            <div key={id} className="h-[3px] rounded-[1px] bg-border-default" />
          ))}
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 5: Run canvas test**

Run: `cd spa && npx vitest run src/components/settings/new-tab/NewTabCanvas.test.tsx`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/settings/new-tab/NewTabCanvas.tsx \
        spa/src/components/settings/new-tab/NewTabCanvas.test.tsx \
        spa/src/components/settings/new-tab/NewTabThumbnail.tsx
git commit -m "feat(new-tab): add NewTabCanvas (sortable cols) + NewTabThumbnail"
```

---

## Task 10: `NewTabSubsection` 組裝（DndContext 層）

**Files:**
- Create: `spa/src/components/settings/new-tab/NewTabSubsection.tsx`

把 Palette / Switcher / Canvas 組起來。`DndContext` 在此掛。`DragOverlay` 用 `createPortal` 到 `document.body` 規避 Settings 容器 `overflow-auto` 截斷。`onDragEnd` 派遣到 store。

- [ ] **Step 1: 實作**

建 `spa/src/components/settings/new-tab/NewTabSubsection.tsx`：

```tsx
import { useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import {
  DndContext, DragOverlay, PointerSensor, useSensor, useSensors,
  type DragEndEvent, type DragStartEvent, pointerWithin,
} from '@dnd-kit/core'
import { getNewTabProviders } from '../../../lib/new-tab-registry'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import type { ProfileKey } from '../../../lib/resolve-profile'
import { useI18nStore } from '../../../stores/useI18nStore'
import { NewTabModulePalette, type PaletteItem } from './NewTabModulePalette'
import { NewTabProfileSwitcher } from './NewTabProfileSwitcher'
import { NewTabCanvas } from './NewTabCanvas'
import { NewTabThumbnail } from './NewTabThumbnail'

function shortestColIdx(cols: string[][]): number {
  let best = 0
  for (let i = 1; i < cols.length; i++) if (cols[i].length < cols[best].length) best = i
  return best
}

export function NewTabSubsection() {
  const t = useI18nStore((s) => s.t)
  const providers = useMemo(() => getNewTabProviders(), [])
  const profiles = useNewTabLayoutStore((s) => s.profiles)
  const active = useNewTabLayoutStore((s) => s.activeEditingProfile)
  const setEditing = useNewTabLayoutStore((s) => s.setEditing)
  const setEnabled = useNewTabLayoutStore((s) => s.setEnabled)
  const placeModule = useNewTabLayoutStore((s) => s.placeModule)
  const removeModule = useNewTabLayoutStore((s) => s.removeModule)

  const [dragging, setDragging] = useState<null | { providerId: string; source: 'palette' | 'canvas' }>(null)

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  const paletteItems: PaletteItem[] = useMemo(() => {
    const activeIds = new Set(profiles[active].columns.flat())
    return providers.map((p) => ({
      id: p.id,
      label: p.label,
      icon: p.icon,
      inUse: activeIds.has(p.id),
      unavailable: p.disabled,
    }))
  }, [providers, profiles, active])

  const handleClickAdd = (id: string) => {
    const cols = useNewTabLayoutStore.getState().profiles[active].columns
    const col = shortestColIdx(cols)
    placeModule(active, id, col, cols[col].length)
  }

  const handleDragStart = (e: DragStartEvent) => {
    const id = String(e.active.id)
    if (id.startsWith('palette:')) {
      setDragging({ providerId: id.slice('palette:'.length), source: 'palette' })
    } else if (id.startsWith('item:')) {
      const parts = id.split(':')
      setDragging({ providerId: parts[2], source: 'canvas' })
    }
  }

  const handleDragEnd = (e: DragEndEvent) => {
    const active = e.active
    const over = e.over
    setDragging(null)
    if (!over) return

    const src = active.data.current as { type?: string; providerId?: string; profileKey?: ProfileKey } | undefined
    const dst = over.data.current as { type?: string; profileKey?: ProfileKey; colIdx?: number } | undefined
    if (!src?.providerId) return

    // Drop into palette zone = remove
    if (dst?.type === 'palette-zone') {
      if (src.type === 'canvas-item' && src.profileKey) {
        removeModule(src.profileKey, src.providerId)
      }
      return
    }

    // Drop onto another canvas item = insert before it (within its column)
    if (over.id.toString().startsWith('item:')) {
      const [, overProfile, overId] = over.id.toString().split(':')
      const store = useNewTabLayoutStore.getState()
      const profileKey = overProfile as ProfileKey
      const cols = store.profiles[profileKey].columns
      const colIdx = cols.findIndex((c) => c.includes(overId))
      if (colIdx < 0) return
      const rowIdx = cols[colIdx].indexOf(overId)
      placeModule(profileKey, src.providerId, colIdx, rowIdx)
      return
    }

    // Drop onto column empty area
    if (dst?.type === 'column' && dst.profileKey && typeof dst.colIdx === 'number') {
      const cols = useNewTabLayoutStore.getState().profiles[dst.profileKey].columns
      placeModule(dst.profileKey, src.providerId, dst.colIdx, cols[dst.colIdx].length)
    }
  }

  const overlayLabel = dragging
    ? (providers.find((p) => p.id === dragging.providerId)?.label ?? dragging.providerId)
    : null

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={pointerWithin}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      onDragCancel={() => setDragging(null)}
    >
      <div className="flex flex-col h-full">
        <NewTabModulePalette items={paletteItems} onClickAdd={handleClickAdd} />
        <NewTabProfileSwitcher
          active={active}
          onSelect={setEditing}
          onToggleEnabled={setEnabled}
          renderMain={(k) => <NewTabCanvas profileKey={k} />}
          renderThumb={(k) => <NewTabThumbnail profileKey={k} />}
        />
      </div>
      {typeof document !== 'undefined' && createPortal(
        <DragOverlay>
          {overlayLabel && (
            <div className="px-2 py-1 rounded-md bg-surface-elevated border border-border-active text-xs shadow-lg">
              {t(overlayLabel)}
            </div>
          )}
        </DragOverlay>,
        document.body,
      )}
    </DndContext>
  )
}
```

- [ ] **Step 2: Smoke test — render 不 throw**

暫不為 Subsection 寫完整 dnd 測試（JSDOM 對 DragEvent 支援有限，風險/價值比低；單元覆蓋在 Task 6–9 已足）。確認組裝能 build + 型別通過即可。

Run: `cd spa && pnpm run lint && pnpm run build`
Expected: 通過

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/settings/new-tab/NewTabSubsection.tsx
git commit -m "feat(new-tab): assemble NewTabSubsection with DndContext + DragOverlay portal"
```

---

## Task 11: `InterfaceSection` + `InterfaceSubNav` (controlled)

**Files:**
- Create: `spa/src/components/settings/InterfaceSection.tsx`
- Create: `spa/src/components/settings/InterfaceSubNav.tsx`
- Test: `spa/src/components/settings/InterfaceSection.test.tsx`

Controlled（`activeSubsection` + `onSelectSubsection` props），比照 `SettingsSidebar` / `GlobalSettingsPage` 的外層持 state 慣例。

- [ ] **Step 1: 寫 test**

建 `spa/src/components/settings/InterfaceSection.test.tsx`：

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { InterfaceSection } from './InterfaceSection'
import {
  clearInterfaceSubsectionRegistry,
  registerInterfaceSubsection,
} from '../../lib/interface-subsection-registry'

const NewTab = () => <div data-testid="nt-body">new-tab-body</div>
const Pane = () => <div data-testid="pane-body">pane-body</div>

beforeEach(() => {
  clearInterfaceSubsectionRegistry()
  registerInterfaceSubsection({ id: 'new-tab', label: 'settings.interface.new_tab', order: 0, component: NewTab })
  registerInterfaceSubsection({ id: 'pane', label: 'settings.interface.pane', order: 1, component: Pane, disabled: true, disabledReason: 'settings.coming_soon' })
})

describe('InterfaceSection', () => {
  it('renders active subsection body', () => {
    render(<InterfaceSection activeSubsection="new-tab" onSelectSubsection={() => {}} />)
    expect(screen.getByTestId('nt-body')).toBeInTheDocument()
  })

  it('shows disabled subsection in nav with "coming soon" hint but does not render body', () => {
    render(<InterfaceSection activeSubsection="new-tab" onSelectSubsection={() => {}} />)
    const paneBtn = screen.getByTestId('interface-subnav-pane')
    expect(paneBtn).toBeInTheDocument()
    expect(screen.queryByTestId('pane-body')).not.toBeInTheDocument()
  })

  it('calls onSelectSubsection on nav click (enabled only)', () => {
    const onSel = vi.fn()
    render(<InterfaceSection activeSubsection="new-tab" onSelectSubsection={onSel} />)
    fireEvent.click(screen.getByTestId('interface-subnav-pane'))
    expect(onSel).not.toHaveBeenCalled() // disabled

    fireEvent.click(screen.getByTestId('interface-subnav-new-tab'))
    expect(onSel).toHaveBeenCalledWith('new-tab')
  })

  it('renders nothing when active id refers to a disabled subsection', () => {
    render(<InterfaceSection activeSubsection="pane" onSelectSubsection={() => {}} />)
    expect(screen.queryByTestId('nt-body')).not.toBeInTheDocument()
    expect(screen.queryByTestId('pane-body')).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run failing test**

Run: `cd spa && npx vitest run src/components/settings/InterfaceSection.test.tsx`
Expected: FAIL — modules not found

- [ ] **Step 3: 實作 `InterfaceSubNav`**

建 `spa/src/components/settings/InterfaceSubNav.tsx`：

```tsx
import { useI18nStore } from '../../stores/useI18nStore'
import type { InterfaceSubsection } from '../../lib/interface-subsection-registry'

interface Props {
  items: InterfaceSubsection[]
  active: string
  onSelect: (id: string) => void
}

export function InterfaceSubNav({ items, active, onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="w-40 border-r border-border-subtle py-3 pl-2 flex-shrink-0">
      {items.map((item) => {
        const enabled = !item.disabled
        const isActive = enabled && item.id === active
        return (
          <button
            key={item.id}
            type="button"
            data-testid={`interface-subnav-${item.id}`}
            data-active={isActive ? 'true' : undefined}
            onClick={() => { if (enabled) onSelect(item.id) }}
            className={[
              'w-full text-left px-3 py-2 text-sm flex items-center transition-colors',
              !enabled ? 'text-text-muted cursor-not-allowed'
                      : isActive ? 'bg-surface-elevated text-text-primary border-l-2 border-border-active'
                                 : 'text-text-secondary cursor-pointer hover:bg-white/5',
            ].join(' ')}
          >
            <span>{t(item.label)}</span>
            {!enabled && (
              <span className="text-[10px] text-text-muted ml-auto">
                {t(item.disabledReason ?? 'settings.coming_soon')}
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: 實作 `InterfaceSection`**

建 `spa/src/components/settings/InterfaceSection.tsx`：

```tsx
import { getInterfaceSubsections } from '../../lib/interface-subsection-registry'
import { InterfaceSubNav } from './InterfaceSubNav'

interface Props {
  activeSubsection: string
  onSelectSubsection: (id: string) => void
}

export function InterfaceSection({ activeSubsection, onSelectSubsection }: Props) {
  const subs = getInterfaceSubsections()
  const selected = subs.find((s) => s.id === activeSubsection)
  return (
    <div className="flex h-full">
      <InterfaceSubNav items={subs} active={activeSubsection} onSelect={onSelectSubsection} />
      <div className="flex-1 overflow-auto">
        {selected && !selected.disabled && <selected.component />}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Run test**

Run: `cd spa && npx vitest run src/components/settings/InterfaceSection.test.tsx`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/settings/InterfaceSection.tsx \
        spa/src/components/settings/InterfaceSubNav.tsx \
        spa/src/components/settings/InterfaceSection.test.tsx
git commit -m "feat(settings): add InterfaceSection shell with controlled subsection"
```

---

## Task 12: 註冊 `interface` section + 3 interface subsections

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`
- Modify: `spa/src/lib/register-modules.test.ts`

**不**在 register 內呼叫 store（hydration 時序由 `useNewTabBootstrap` 接手，Task 13）。

- [ ] **Step 1: 更新 `register-modules.tsx` imports 與註冊**

修改 `spa/src/lib/register-modules.tsx`：

1. 在現有 imports 區加入：

```tsx
import { registerInterfaceSubsection } from './interface-subsection-registry'
import { InterfaceSection } from '../components/settings/InterfaceSection'
import { NewTabSubsection } from '../components/settings/new-tab/NewTabSubsection'
```

2. 移除現有 `NewTabPage` 由 settings 直接引用的路徑（若無，略過）。

3. 在 `// Settings sections` 區的 `registerSettingsSection({ id: 'appearance', ... })` 之後，`terminal` 之前（或依 `order` 穿插），加入：

```tsx
// Interface section holds its own subsection state via controlled props;
// SettingsPage will wrap it in a stateful container (see SettingsPage.tsx).
registerSettingsSection({
  id: 'interface',
  label: 'settings.section.interface',
  order: 2,
  component: () => <InterfaceSectionHost />,
})
```

4. 在檔案最下方（outside `registerBuiltinModules`）新增 local `InterfaceSectionHost` 元件——其負責 `useState('new-tab')` 並把 props 傳進 `InterfaceSection`：

```tsx
function InterfaceSectionHost() {
  // Controlled wrapper so SettingsPage doesn't need to know about interface-specific state.
  const subs = getInterfaceSubsections()
  const [active, setActive] = useState<string>(() => subs[0]?.id ?? '')
  return <InterfaceSection activeSubsection={active} onSelectSubsection={setActive} />
}
```

**Import 調整**：在檔案頂端加 `import { useState } from 'react'` 與 `import { getInterfaceSubsections } from './interface-subsection-registry'`。

5. 在 `registerBuiltinModules()` 內最末段（`// New-tab providers` 之前或其後；任何地方皆可，因其 idempotent），加入：

```tsx
// Interface subsections
registerInterfaceSubsection({
  id: 'new-tab',
  label: 'settings.interface.new_tab',
  order: 0,
  component: NewTabSubsection,
})
registerInterfaceSubsection({
  id: 'pane',
  label: 'settings.interface.pane',
  order: 1,
  component: () => null,
  disabled: true,
  disabledReason: 'settings.coming_soon',
})
registerInterfaceSubsection({
  id: 'sidebar',
  label: 'settings.interface.sidebar',
  order: 2,
  component: () => null,
  disabled: true,
  disabledReason: 'settings.coming_soon',
})
```

- [ ] **Step 2: 更新 register-modules 測試**

修改 `spa/src/lib/register-modules.test.ts`：

在 import 區加：

```ts
import { clearInterfaceSubsectionRegistry, getInterfaceSubsections } from './interface-subsection-registry'
```

在 `clearAll()` 加一行：

```ts
clearInterfaceSubsectionRegistry()
```

在 describe 內加測試：

```ts
it('registers interface section with order=2', () => {
  registerBuiltinModules()
  const sections = getSettingsSections()
  const iface = sections.find((s) => s.id === 'interface')
  expect(iface).toBeDefined()
  expect(iface?.order).toBe(2)
  expect(iface?.component).toBeDefined()
})

it('registers interface subsections: new-tab enabled, pane/sidebar disabled', () => {
  registerBuiltinModules()
  const subs = getInterfaceSubsections()
  expect(subs.map((s) => s.id)).toEqual(['new-tab', 'pane', 'sidebar'])
  expect(subs[0].disabled).toBeFalsy()
  expect(subs[1].disabled).toBe(true)
  expect(subs[2].disabled).toBe(true)
})
```

- [ ] **Step 3: Run tests**

Run: `cd spa && npx vitest run src/lib/register-modules.test.ts`
Expected: 全部 PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/lib/register-modules.tsx spa/src/lib/register-modules.test.ts
git commit -m "feat(settings): register Interface section + 3 subsections"
```

---

## Task 13: i18n keys（en.json + zh-TW.json）

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: en.json 加入 keys**

在 `"settings.section.sync": ...` 同區塊加：

```json
"settings.section.interface": "Interface",
"settings.interface.new_tab": "New Tab",
"settings.interface.pane": "Pane",
"settings.interface.sidebar": "Sidebar",
"settings.interface.enabled": "enabled",
"settings.interface.profile_3col": "3 Columns",
"settings.interface.profile_2col": "2 Columns",
"settings.interface.profile_1col": "1 Column",
"settings.interface.profile_locked": "Fallback profile (cannot be disabled)",
"settings.interface.profile_empty": "(empty)",
"settings.interface.profile_prefilled": "Prefilled — enable to use",
"settings.interface.palette_in_use": "✓ placed",
"settings.interface.palette_unavailable": "unavailable here",
"settings.interface.canvas_drop_here": "Drop module here",
```

- [ ] **Step 2: zh-TW.json 加入 keys**

在對應位置加：

```json
"settings.section.interface": "介面設定",
"settings.interface.new_tab": "分頁首頁",
"settings.interface.pane": "分割區",
"settings.interface.sidebar": "側邊欄",
"settings.interface.enabled": "啟用",
"settings.interface.profile_3col": "三欄",
"settings.interface.profile_2col": "兩欄",
"settings.interface.profile_1col": "單欄",
"settings.interface.profile_locked": "保底配置（無法停用）",
"settings.interface.profile_empty": "(空)",
"settings.interface.profile_prefilled": "已預填預設佈局，啟用後即可使用",
"settings.interface.palette_in_use": "✓ 已放",
"settings.interface.palette_unavailable": "此環境不可用",
"settings.interface.canvas_drop_here": "拖曳 module 到此",
```

- [ ] **Step 3: 跑 locale completeness 測試**

Run: `cd spa && npx vitest run src/locales/locale-completeness.test.ts`
Expected: PASS（兩 locale 同步）

- [ ] **Step 4: Commit**

```bash
git add spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "i18n: add settings.interface.* keys"
```

---

## Task 14: `useNewTabBootstrap` hook

**Files:**
- Create: `spa/src/hooks/useNewTabBootstrap.ts`
- Modify: `spa/src/App.tsx`

Hydration 完成後呼叫 `ensureDefaults(providers)`；`hasHydrated()` 為 true 時直接跑並 early-return（不雙呼叫）。

- [ ] **Step 1: 實作 hook**

建 `spa/src/hooks/useNewTabBootstrap.ts`：

```ts
import { useEffect } from 'react'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { getNewTabProviders } from '../lib/new-tab-registry'

export function useNewTabBootstrap(): void {
  useEffect(() => {
    const runDefaults = () => {
      const providers = getNewTabProviders().map((p) => ({
        id: p.id,
        order: p.order,
        disabled: p.disabled,
      }))
      useNewTabLayoutStore.getState().ensureDefaults(providers)
    }

    if (useNewTabLayoutStore.persist.hasHydrated()) {
      runDefaults()
      return
    }
    return useNewTabLayoutStore.persist.onFinishHydration(runDefaults)
  }, [])
}
```

- [ ] **Step 2: 在 `App.tsx` 呼叫**

修改 `spa/src/App.tsx`：

1. 在 imports 加：

```tsx
import { useNewTabBootstrap } from './hooks/useNewTabBootstrap'
```

2. 在 `--- Extracted hooks ---` 區（既有 `useRouteSync()` / `useShortcuts()` 同一段）加：

```tsx
useNewTabBootstrap()
```

- [ ] **Step 3: 基本 smoke**

Run: `cd spa && pnpm run lint && pnpm run build`
Expected: 通過

- [ ] **Step 4: Commit**

```bash
git add spa/src/hooks/useNewTabBootstrap.ts spa/src/App.tsx
git commit -m "feat(new-tab): wire useNewTabBootstrap in App"
```

---

## Task 15: 改寫 `NewTabPage`（多欄 grid + hydration gate）

**Files:**
- Modify: `spa/src/components/NewTabPage.tsx`

- [ ] **Step 1: 全檔改寫**

`spa/src/components/NewTabPage.tsx`：

```tsx
import { useMemo, useState, useEffect } from 'react'
import { getNewTabProviders } from '../lib/new-tab-registry'
import { useI18nStore } from '../stores/useI18nStore'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useMediaQuery } from '../hooks/useMediaQuery'
import { resolveProfile } from '../lib/resolve-profile'
import type { PaneContent } from '../types/tab'

interface Props {
  onSelect: (content: PaneContent) => void
}

export function NewTabPage({ onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  const [hydrated, setHydrated] = useState(useNewTabLayoutStore.persist.hasHydrated())
  useEffect(() => {
    if (hydrated) return
    return useNewTabLayoutStore.persist.onFinishHydration(() => setHydrated(true))
  }, [hydrated])

  const isWide = useMediaQuery('(min-width: 1024px)')
  const isMid = useMediaQuery('(min-width: 640px)')
  const profiles = useNewTabLayoutStore((s) => s.profiles)
  const profileKey = resolveProfile(isWide, isMid, profiles)
  const profile = profiles[profileKey]

  const providers = getNewTabProviders()
  const byId = useMemo(() => Object.fromEntries(providers.map((p) => [p.id, p])), [providers])

  if (!hydrated) {
    return <div className="flex-1" />
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
            if (!p) return null
            return (
              <section key={id} className="w-full">
                <h3 className="text-sm font-medium text-text-secondary mb-2 px-2">
                  {t(p.label)}
                  {p.disabled && p.disabledReason && (
                    <span className="text-text-muted text-xs ml-2">— {t(p.disabledReason)}</span>
                  )}
                </h3>
                {!p.disabled && <p.component onSelect={onSelect} />}
              </section>
            )
          })}
        </div>
      ))}
    </div>
  )
}
```

**變動摘要**：
- `h2` 標題刪除（原 `page.newtab.title`）——多欄版不需要頂端 heading；若要保留再加。
- 每欄獨立 `overflow-y-auto`（滾動行為 per-column）。

- [ ] **Step 2: Lint + build**

Run: `cd spa && pnpm run lint && pnpm run build`
Expected: 通過

- [ ] **Step 3: 若既有 snapshot/單元測試 cover `NewTabPage`，執行並修正**

Run: `cd spa && npx vitest run` (全組)
Expected: 全 PASS。若某測試假設單列/h2 存在而失敗，依新結構調整。

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/NewTabPage.tsx
git commit -m "feat(new-tab): multi-column NewTabPage with hydration gate + useActiveProfile"
```

---

## Task 16: 手動驗證（桌面 / 窄視窗 / PWA 手機）

**Files:** —（無 code change；純 smoke test）

- [ ] **Step 1: 啟動 dev**

Run: `cd spa && pnpm run dev`

確認 daemon 跑著（`bin/pdx` 在 `100.64.0.2:7860`）。

- [ ] **Step 2: 桌面 ≥1024 測試**

1. 開 `http://100.64.0.2:5174/`（或本機 `localhost:5174`）。
2. Settings → Interface → New Tab 出現。
3. 首次 store 應為：1col enabled、2col/3col disabled 但縮圖已填預設。
4. 展開 3col toggle → runtime 立刻切三欄。
5. Palette chip click → 加到最短欄底部。
6. Drag palette chip → 放到不同欄、移除（drop 到 palette 區）。
7. 同欄向下 drag → 順序正確（無 off-by-one）。
8. Refresh → 佈局保留（localStorage persist）。

- [ ] **Step 3: 窄視窗 640–1023 測試**

縮視窗至 800px：runtime 應切 2col。若 disable 2col → fallback 1col。

- [ ] **Step 4: PWA 手機實機**

用 iPhone Safari 開 tailnet URL（如 `https://purdex.mlab.host`）→ 應顯示 1col。Touch 拖曳可運作（dnd-kit `PointerSensor`）。

- [ ] **Step 5: 清理驗證**

- [ ] 觀察 console 無 warning/error。
- [ ] Settings → Appearance / Terminal 等既有 section 仍正常。
- [ ] 切換 profile edit 與 toggle enable 不觸發 hydration flash。

**No commit for this task.**

---

## Plan review notes

**Spec coverage 自查**（對照 spec 33e06122）：
- §目標 1–4 → Tasks 10–12（InterfaceSection / register / subsections）
- §新增檔案清單 → Tasks 2–11 全部覆蓋
- §變更既有檔案 → Tasks 12–15
- §資料模型（profiles / knownIds / placeModule / ensureDefaults / setEnabled 1col-lock）→ Task 6
- §placeModule off-by-one + clamp → Task 6 step 3
- §ensureDefaults 填所有 profile、跳過 disabled、已下架不主動清 → Task 6 tests
- §Persist（version 1、無 migrate、hasHydrated gate）→ Task 6 + Task 15
- §interface-subsection-registry upsert → Task 4
- §InterfaceSection controlled props → Task 11 + Task 12 `InterfaceSectionHost`
- §NewTabModulePalette / NewTabProfileSwitcher / NewTabCanvas / NewTabThumbnail → Tasks 7–9
- §dnd-kit + PointerSensor distance 5 + DragOverlay portal → Task 10
- §useActiveProfile / resolveProfile / NewTabPage hydration gate → Tasks 2, 15
- §useNewTabBootstrap → Task 14
- §i18n keys → Task 13
- §matchMedia mock in test-setup → Task 1

**已知未盡事項（列入 follow-up，不在本 plan）**：
- 真 drag gesture 的 Playwright E2E（spec §不測）
- 鍵盤 DnD a11y polish
- Sync contributor 接入
- `knownIds` 裁剪策略（alpha YAGNI）
- 自訂斷點 UI
- 「永久隱藏某 module」的顯式 UI

---

## 實作指引

**建議切入：** 從 main（commit 33e06122 或其後含 spec 的提交）開新 worktree 執行本 plan。由於 main 目前有兩條並存 lineage（spec lineage 33e06122 與 alpha.148 lineage），實作前先確認 branch 實際狀態：

```bash
git checkout main
git pull
git log --oneline -5
# 若看不到 spec 相關 commit，切到保有 spec 的分支或 cherry-pick 3919926b..33e06122
```

**PR review**：完成後走專案規定的兩輪 review（`code-review:code-review` skill + 3 parallel attacker / defender / size agents），依信心/關聯/複雜度表格分類處理。

**Commit 節奏**：每個 Task 結尾 commit 一次（plan 已示範）；TDD：先 test、再實作、全測過再 commit。
