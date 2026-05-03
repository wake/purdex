# Settings Modules Sidebar Alignment Plan

- **Date**: 2026-05-03
- **Spec**: `docs/specs/2026-05-03-settings-modules-order-spec.md`
- **Worktree**: `.claude/worktrees/settings-modules-order` / branch `worktree-settings-modules-order`
- **Base**: `origin/main` @ `05c4790d` (alpha.291)

## 0. 排版總覽

從 spec §3.1 / §4.1 落到具體 commit 切分：

| Commit | 主題 | 檔案 (高層) | 預估 |
|---|---|---|---|
| C1 | 新 i18n keys + PlaceholderSettingsSection + 失敗測試 | locales / new component / test scaffolding | 紅燈 |
| C2 | SETTINGS_ORDER 重排 + register-modules Browser/Files purdex contribution + memory-monitor/quick-commands labelKey 切換 | settings-order.ts / register-modules/index.tsx | 過 T3-T7 |
| C3 | ModulesSwitchboardSection 排序改 + 失敗測試先 → 實作 | switchboard component + test | 過 T1-T2 |
| C4 | 既有 test 對齊（labelKey 變動） | register-modules.test.ts / register-modules.quick-commands.test.tsx | 全綠 |

預估 net diff ~150 行（新增 component + i18n + 測試 - 舊 labelKey 替換）。

## 1. 失敗測試先（C1 – TDD red）

### 1.1 新增 `spa/src/components/settings/PlaceholderSettingsSection.test.tsx`

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PlaceholderSettingsSection } from './PlaceholderSettingsSection'

// I5: component must render only the i18n string. The store-subscription
// guard prevents future drift where someone wires in store reads.
const useI18nStoreMock = vi.fn()
vi.mock('../../stores/useI18nStore', () => ({
  useI18nStore: (selector: (s: unknown) => unknown) => useI18nStoreMock(selector),
}))

describe('PlaceholderSettingsSection', () => {
  it('renders the no-purdex-settings i18n string', () => {
    useI18nStoreMock.mockImplementation((sel) => sel({ t: (k: string) => k }))
    render(<PlaceholderSettingsSection />)
    expect(screen.getByText('settings.module.no_purdex_settings')).toBeTruthy()
  })

  it('subscribes only to t (no other store reads)', () => {
    useI18nStoreMock.mockClear()
    useI18nStoreMock.mockImplementation((sel) => sel({ t: (k: string) => k }))
    render(<PlaceholderSettingsSection />)
    // Exactly one selector call — for `t`.
    expect(useI18nStoreMock).toHaveBeenCalledTimes(1)
  })
})
```

### 1.2 新增 i18n keys（先紅燈：keys 還沒存在 → render 出 i18n key 而不是 translated 字串，但 test 直接 assert key 文字所以會 pass — OK）

實際 i18n string 在 C1 一起加；test 不依賴翻譯結果。

### 1.3 擴充 `ModulesSwitchboardSection.test.tsx`

加 T1 / T2 / T2b 測試（會紅 — 排序還沒實作）：

```tsx
it('T1: renders disableable modules sorted by purdex contribution order', () => {
  registerModule({
    id: 'aaa', name: 'AAA', disableable: true,
    settings: [{ localId: 'aaa', scope: 'purdex', order: 30, labelKey: 'x', component: () => null }],
  })
  registerModule({
    id: 'bbb', name: 'BBB', disableable: true,
    settings: [{ localId: 'bbb', scope: 'purdex', order: 10, labelKey: 'y', component: () => null }],
  })
  registerModule({
    id: 'ccc', name: 'CCC', disableable: true,
    settings: [{ localId: 'ccc', scope: 'purdex', order: 20, labelKey: 'z', component: () => null }],
  })
  render(<ModulesSwitchboardSection ctx={purdexCtx} />)
  const rows = Array.from(document.querySelectorAll('[data-module-id]'))
  expect(rows.map((r) => r.getAttribute('data-module-id'))).toEqual(['bbb', 'ccc', 'aaa'])
})

it('T2: disableable module without purdex contribution falls back to last alphabetically', () => {
  registerModule({
    id: 'ordered', name: 'Ordered', disableable: true,
    settings: [{ localId: 'ordered', scope: 'purdex', order: 5, labelKey: 'x', component: () => null }],
  })
  registerModule({ id: 'no-settings', name: 'NoSettings', disableable: true })
  render(<ModulesSwitchboardSection ctx={purdexCtx} />)
  const rows = Array.from(document.querySelectorAll('[data-module-id]'))
  expect(rows.map((r) => r.getAttribute('data-module-id'))).toEqual(['ordered', 'no-settings'])
})

it('T2b: integration — switchboard order matches sidebar disableable subset', () => {
  // After registerBuiltinModules, the switchboard's row order must match
  // the order of disableable purdex contributions in listContributions('purdex').
  registerBuiltinModules()
  const purdexEntries = listContributions('purdex').filter((c) => {
    const m = getModuleById(c.moduleId)
    return m?.disableable === true
  })
  const expectedOrder = purdexEntries.map((c) => c.moduleId)
  render(<ModulesSwitchboardSection ctx={purdexCtx} />)
  const rows = Array.from(document.querySelectorAll('[data-module-id]'))
  expect(rows.map((r) => r.getAttribute('data-module-id'))).toEqual(expectedOrder)
})
```

### 1.4 擴充 `register-modules.test.ts`（T3-T7 + I1 invariant）

```ts
describe('Settings sidebar alignment (spec §3 I1)', () => {
  beforeEach(() => { /* reset registries */ })

  it('T3 / I1: every disableable module declares at least one purdex-scope settings contribution', () => {
    registerBuiltinModules()
    const violations = getModules()
      .filter((m) => m.disableable === true)
      .filter((m) => !(m.settings ?? []).some((s) => s.scope === 'purdex'))
      .map((m) => m.id)
    expect(violations).toEqual([])
  })

  it('T4: browser registers a purdex placeholder with settings.section.browser label', () => {
    registerBuiltinModules()
    const browser = getModuleById('browser')
    const purdex = browser?.settings?.find((s) => s.scope === 'purdex')
    expect(purdex?.labelKey).toBe('settings.section.browser')
    expect(purdex?.component).toBe(PlaceholderSettingsSection)
  })

  it('T5: files registers a purdex placeholder with settings.section.files label', () => {
    registerBuiltinModules()
    const files = getModuleById('files')
    const purdex = files?.settings?.find((s) => s.scope === 'purdex')
    expect(purdex?.labelKey).toBe('settings.section.files')
    expect(purdex?.component).toBe(PlaceholderSettingsSection)
  })

  it('T6: memory-monitor purdex labelKey switched to settings.section.monitor', () => {
    registerBuiltinModules()
    const m = getModuleById('memory-monitor')
    const purdex = m?.settings?.find((s) => s.scope === 'purdex')
    expect(purdex?.labelKey).toBe('settings.section.monitor')
  })

  it('T7: quick-commands purdex labelKey switched to settings.section.commands', () => {
    registerBuiltinModules()
    const m = getModuleById('quick-commands')
    const purdex = m?.settings?.find((s) => s.scope === 'purdex')
    expect(purdex?.labelKey).toBe('settings.section.commands')
  })
})
```

> 注意：實際 `getModuleById` helper 若不存在，改用 `getModules().find((m) => m.id === '...')`。確認 `module-registry.ts` 出口。

### 1.5 修改既有 assertion

**已存在的 test 必須在 C2 之後跟著改**（不修會紅）：
- `spa/src/lib/register-modules.test.ts:83` `'performance_monitor.title'` → `'settings.section.monitor'`
- `spa/src/lib/register-modules.quick-commands.test.tsx:26` `'settings.section.quick_commands'` → `'settings.section.commands'`

C1 不動這兩個 — 等 C2 改 source 後同 commit 修。

## 2. 實作（C2 – 主結構）

### 2.1 `spa/src/lib/settings-order.ts`

更新註解 + 重排：

```ts
/**
 * ...（保留前段 doc comment）
 *
 * **purdex scope** — sidebar at `/settings`:
 *
 *   | Band                        | Range  | Examples                                  |
 *   |-----------------------------|--------|-------------------------------------------|
 *   | Top built-in (core)         | 0 – 4  | Appearance / Terminal / Interface         |
 *   | Top conditional built-in    | 5 – 9  | Electron (gated by canSystemTray)         |
 *   | Modules switchboard         | 10     | `module-config` (single header row)       |
 *   | Module-owned (alphabetical) | 11–19  | Browser / Commands / Editor / Files /     |
 *   |                             |        | Monitor / Sync                            |
 *   | Tail built-in               | 20–29  | Dev Environment / Tmux Agent Monitor      |
 *
 * The "Module-owned" band sorts by **English (default) sidebar short label**,
 * not runtime locale. Constants are named after the **module identity**
 * (e.g. `MODULE_QUICK_COMMANDS` even though the sidebar shows "Commands"),
 * because the underlying module ID is the stable identifier; sidebar
 * labels can change without breaking the constants. (Spec §I3)
 *
 * ...
 */
export const SETTINGS_ORDER = {
  APPEARANCE: 0,
  TERMINAL: 1,
  INTERFACE: 2,
  ELECTRON: 5,
  MODULE_CONFIG: 10,
  // Module-owned (alphabetical by English sidebar short label).
  MODULE_BROWSER: 11,            // sidebar: "Browser"
  MODULE_QUICK_COMMANDS: 12,     // sidebar: "Commands"
  MODULE_EDITOR: 13,             // sidebar: "Editor"
  MODULE_FILES: 14,              // sidebar: "Files"
  MODULE_PERFORMANCE_MONITOR: 15, // sidebar: "Monitor"
  MODULE_SYNC: 16,               // sidebar: "Sync"
  DEV_ENVIRONMENT: 20,
  TMUX_AGENT_MONITOR: 21,
  WORKSPACE_FILES: 10,
} as const
```

### 2.2 `spa/src/components/settings/PlaceholderSettingsSection.tsx`

```tsx
import { useI18nStore } from '../../stores/useI18nStore'

// Spec §I5 — view-only placeholder for disableable modules that have no
// global (purdex-scope) settings to expose. Browser and Files use this so
// every disableable module carries an entry in the Settings sidebar
// (spec §I1), keeping the Modules Switchboard ↔ sidebar mental model
// consistent. Subscribes only to `t`; no store writes.
export function PlaceholderSettingsSection() {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="space-y-4">
      <p className="text-sm text-text-secondary">
        {t('settings.module.no_purdex_settings')}
      </p>
    </div>
  )
}
```

### 2.3 `spa/src/lib/register-modules/index.tsx`

**Browser** — 加 `settings: [...]`：

```tsx
import { PlaceholderSettingsSection } from '../../components/settings/PlaceholderSettingsSection'
// ...

registerModule({
  id: 'browser',
  name: 'Browser',
  disableable: true,
  descriptionKey: 'modules.browser.description',
  panes: [{ kind: 'browser', component: BrowserPaneWrapper }],
  settings: [
    {
      localId: 'browser',
      scope: 'purdex',
      order: SETTINGS_ORDER.MODULE_BROWSER,
      labelKey: 'settings.section.browser',
      component: PlaceholderSettingsSection,
    },
  ],
})
```

**memory-monitor** — labelKey 切換：

```tsx
registerModule({
  id: 'memory-monitor',
  // ...
  settings: [{
    localId: 'performance-monitor',
    scope: 'purdex',
    order: SETTINGS_ORDER.MODULE_PERFORMANCE_MONITOR,  // value 13 → 15
    labelKey: 'settings.section.monitor',  // was 'performance_monitor.title'
    component: PerformanceMonitorSettingsSection,
  }],
})
```

> **重要**：`PerformanceMonitorSettingsSection` 內部 H2 仍呼 `t('performance_monitor.title')`（`MemoryMonitorPage.tsx:284`），不動。

**quick-commands** — labelKey 切換：

```tsx
registerModule({
  id: 'quick-commands',
  // ...
  settings: [{
    localId: 'quick-commands',
    scope: 'purdex',
    order: SETTINGS_ORDER.MODULE_QUICK_COMMANDS,  // value 12 → 12（不變但 label 改）
    labelKey: 'settings.section.commands',  // was 'settings.section.quick_commands'
    component: QuickCommandsSettingsSection,
  }],
})
```

**Files** — 加第二個 purdex-scope contribution：

```tsx
registerModule({
  id: 'files',
  name: 'Files',
  disableable: true,
  descriptionKey: 'modules.files.description',
  settings: [
    {
      localId: 'workspace-files',
      scope: 'workspace',
      order: SETTINGS_ORDER.WORKSPACE_FILES,
      labelKey: 'settings.section.files_workspace',
      component: FilesWorkspaceSettingsSection,
    },
    {
      localId: 'files',
      scope: 'purdex',
      order: SETTINGS_ORDER.MODULE_FILES,
      labelKey: 'settings.section.files',
      component: PlaceholderSettingsSection,
    },
  ],
  views: [ /* unchanged */ ],
})
```

### 2.4 `spa/src/locales/en.json` / `zh-TW.json`

新增 keys（在現有 `settings.section.*` 群附近，保持字典字母序）：

| Key | en | zh-TW |
|---|---|---|
| `settings.section.browser` | Browser | 瀏覽器 |
| `settings.section.commands` | Commands | 指令 |
| `settings.section.files` | Files | 檔案 |
| `settings.section.monitor` | Monitor | 監控 |
| `settings.module.no_purdex_settings` | This module has no global settings. | 此模組沒有全域設定。 |

舊 keys **保留不刪**：
- `performance_monitor.title` — `pane-labels.ts:45`、`MemoryMonitorPage.tsx:284` 仍用
- `settings.section.quick_commands` — 改完後僅有一個歷史 test reference；保留以保 backward compat（可選後續 issue 清理）

### 2.5 `spa/src/components/settings/ModulesSwitchboardSection.tsx`

```tsx
export function ModulesSwitchboardSection({ ctx: _ctx }: Props = {}) {
  const t = useI18nStore((s) => s.t)
  const hasPending = useModuleEnabledStore((s) => s.hasPendingChanges())

  // Spec §I2 — sort by the module's first purdex-scope settings.order so
  // Switchboard rows match the sidebar's relative position for the same
  // disableable subset. Fallback (no purdex contribution) goes to the end
  // by module.name; spec §I1 means this fallback should never trigger in
  // production but stays as a safety net for local-test edge cases.
  const modules = getModules()
    .filter((m) => m.disableable === true)
    .map((m) => ({
      module: m,
      order: m.settings?.find((s) => s.scope === 'purdex')?.order
        ?? Number.POSITIVE_INFINITY,
    }))
    .sort((a, b) => {
      if (a.order !== b.order) return a.order - b.order
      return a.module.name.localeCompare(b.module.name)
    })
    .map(({ module }) => module)

  return (
    <div className="space-y-6">
      {hasPending && <ReloadBanner t={t} />}
      <div className="space-y-4">
        {modules.map((m) => (
          <ModuleRow key={m.id} module={m} t={t} />
        ))}
      </div>
    </div>
  )
}
```

> 移除 AR-3 註解的 `useMemo` 警告 — 排序邏輯不需要 memo（同樣每 render 重算），現有註解仍有效。

## 3. 對齊既有 test（C4）

- `register-modules.test.ts:83`：`expect(...).toBe('performance_monitor.title')` → `'settings.section.monitor'`
- `register-modules.quick-commands.test.tsx:26`：`labelKey: 'settings.section.quick_commands'` → `'settings.section.commands'`

不需動的：
- `pane-labels.test.ts:80` — 仍 `performance_monitor.title`（pane label 路徑未變）
- `MemoryMonitorPage` 的內部測試（如有）— 內頁標題仍是 `performance_monitor.title`

## 4. Acceptance / verification

### 自動

```sh
cd spa
npx vitest run src/components/settings/PlaceholderSettingsSection.test.tsx
npx vitest run src/components/settings/ModulesSwitchboardSection.test.tsx
npx vitest run src/lib/register-modules.test.ts
npx vitest run src/lib/register-modules.quick-commands.test.tsx
npx vitest run                # 全套
pnpm run lint
pnpm run build
```

全綠 = pass。

### 手動

1. 啟動 SPA dev server，進 `/settings`
2. Sidebar 從上至下顯示：
   `Appearance / Terminal / Interface / Electron / Modules / Browser / Commands / Editor / Files / Monitor / Sync / Dev Environment / Tmux Agent Monitor`
3. 點 Browser → 右側 placeholder「This module has no global settings.」
4. 點 Files → 右側 placeholder（workspace 設定不在這裡，仍在 workspace settings page）
5. 點 Monitor → 右側 `MemoryMonitorPage` 內頁仍顯示「Performance Monitor」標題 + 完整 dashboard
6. 點 Commands → 右側 Quick Commands 完整設定面板（內頁標題仍可顯示 "Quick Commands"）
7. 進 Modules Switchboard：清單從上到下 = Browser / Quick Commands / Editor / Files / Performance Monitor（**全名**，相對順序對齊 sidebar）
8. 切 zh-TW：sidebar 短名 `瀏覽器 / 指令 / 編輯器 / 檔案 / 監控 / 同步`
9. 隨機 disable 一個 module → reload → 該 module 從 sidebar 與 Switchboard 都消失（既有 disable filter 行為，未動）

## 5. 風險 / Edge cases

| 風險 | 緩解 |
|---|---|
| 既有 dispatch 路徑對 Files 多一個 contribution 的影響 | dispatch logic 已支援多 contribution；測試 T2b + 全套 vitest 守住 |
| `performance_monitor.title` 同時被 sidebar 與 inner page 用，切 sidebar labelKey 後是否有殘留？ | grep 確認只有 `register-modules/index.tsx:184` 使用作 labelKey；切後 inner page 仍呼 i18n key 取「Performance Monitor」 |
| Browser placeholder 與 Switchboard 介面落差（user 期待 Browser tab 設定但看到空頁） | i18n 文案 "no global settings" 顯式說明；可選後續 issue 加「Browser 設定請至 pane 內右鍵」連結 |
| HMR + getModules() 排序時序 | 既有 AR-3 註解保留 — 不 memoize，每 render 重算，HMR 不會殘留 |
| Files settings array 順序敏感性 | 排序 `.find((s) => s.scope === 'purdex')` 取第一個 — Files 兩個 contribution 不同 scope，無歧義 |

## 6. 開發步驟

按 commit 順序：

1. **C1**：寫 PlaceholderSettingsSection + 加 i18n keys + Switchboard 測試 + register-modules.test.ts T3-T7
2. **C2**：改 settings-order.ts + register-modules/index.tsx (Browser/Files/memory-monitor/quick-commands) → T3-T7 過
3. **C3**：改 ModulesSwitchboardSection 排序 → T1/T2/T2b 過
4. **C4**：對齊既有 test（labelKey 變動）→ 全綠
5. **手動驗證**：cd spa && pnpm run dev → 跑 §4 manual checklist
6. PR 開出 → 兩輪 codex review

## 7. PR / commit message 草稿

PR title:
```
fix(settings): align modules switchboard with sidebar order
```

Commit summaries (squash 後 PR 顯示):
- `feat(settings): introduce shared PlaceholderSettingsSection for disableable modules without purdex settings`
- `refactor(settings): reorder SETTINGS_ORDER alphabetically by sidebar short label (B/C/E/F/M/Sync)`
- `feat(settings): switchboard rows sort by purdex contribution order`
- `chore(test): align labelKey assertions to new sidebar short labels`
