# Electron Shell Implementation Plan (C+D+E)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Electron desktop shell for tmux-box — multi-window management, WebContentsView browser panes, system tray, memory monitoring.

**Architecture:** electron-vite project alongside existing SPA (pnpm workspace). Main process manages windows, browser views, and tray. SPA communicates via `window.electronAPI` (contextBridge). All SPA changes gated by `getPlatformCapabilities()` for SPA/Electron compatibility.

**Tech Stack:** Electron 33+ / electron-vite / TypeScript / React 19 / Vitest / Phosphor Icons

**Spec:** `docs/superpowers/specs/2026-03-26-electron-shell-design.md`

---

## File Structure

### New Files (13)

| File | Responsibility |
|------|---------------|
| `package.json` (root) | pnpm workspace root + electron scripts |
| `pnpm-workspace.yaml` | workspace packages definition |
| `electron.vite.config.ts` | electron-vite build config |
| `electron/package.json` | Electron deps (electron, electron-builder) |
| `electron/tsconfig.json` | TypeScript config for Electron main/preload |
| `electron/main.ts` | App lifecycle, IPC handlers, metrics polling |
| `electron/preload.ts` | contextBridge → window.electronAPI |
| `electron/window-manager.ts` | BaseWindow CRUD, tear-off/merge |
| `electron/browser-view-manager.ts` | WebContentsView pool, LRU, timeout, memory |
| `electron/tray.ts` | System tray + context menu |
| `spa/src/components/MemoryMonitorPage.tsx` | Memory monitor dashboard |
| `spa/src/components/MemoryMonitorNewTabSection.tsx` | NewTab provider for memory monitor |
| `spa/src/components/settings/ElectronSection.tsx` | Electron settings (3 browser view params) |

### Modified Files (12)

| File | Change |
|------|--------|
| `spa/src/types/electron.d.ts` | +4 methods, +3 types (WindowInfo, Bounds, TabMetrics) |
| `spa/src/types/tab.ts` | +`memory-monitor` PaneContent kind |
| `spa/src/lib/pane-labels.ts` | +`memory-monitor` case in getPaneLabel + getPaneIcon |
| `spa/src/lib/route-utils.ts` | +`memory-monitor` case in tabToUrl |
| `spa/src/lib/pane-utils.ts` | `memory-monitor` walks to `return true` (singleton, no change needed) |
| `spa/src/lib/register-panes.tsx` | +memory-monitor renderer/provider + electron settings section |
| `spa/src/components/BrowserPane.tsx` | +useEffect IPC (open/close) + ResizeObserver (bounds sync) |
| `spa/src/components/TabContextMenu.tsx` | +tear-off/merge actions + submenu |
| `spa/src/components/TabContextMenu.test.tsx` | +tear-off/merge test cases |
| `spa/src/stores/useHistoryStore.test.ts` | +`memory-monitor` case in makeContent |
| `spa/src/locales/en.json` | +electron/tray/monitor/context-menu i18n keys |
| `spa/src/locales/zh-TW.json` | +same keys in Chinese |

---

## Task 1: Project Scaffolding

**Files:**
- Create: `package.json` (root)
- Create: `pnpm-workspace.yaml`
- Create: `electron/package.json`
- Create: `electron/tsconfig.json`
- Create: `electron.vite.config.ts`

- [ ] **Step 1: Create pnpm-workspace.yaml**

```yaml
packages:
  - 'spa'
  - 'electron'
```

- [ ] **Step 2: Create root package.json**

```json
{
  "private": true,
  "scripts": {
    "electron:dev": "electron-vite dev",
    "electron:build": "electron-vite build && electron-builder",
    "electron:preview": "electron-vite preview"
  }
}
```

- [ ] **Step 3: Create electron/package.json**

```json
{
  "name": "tmux-box-electron",
  "version": "1.0.0-alpha.1",
  "private": true,
  "main": "../out/main/index.js",
  "type": "module"
}
```

- [ ] **Step 4: Install electron dependencies**

```bash
cd /Users/wake/Workspace/wake/tmux-box
pnpm add -D -w electron electron-vite electron-builder @electron-toolkit/utils
```

- [ ] **Step 5: Create electron/tsconfig.json**

```json
{
  "compilerOptions": {
    "target": "ESNext",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "outDir": "../out",
    "rootDir": ".",
    "declaration": false,
    "sourceMap": true
  },
  "include": ["*.ts"]
}
```

- [ ] **Step 6: Create electron.vite.config.ts**

```typescript
import { defineConfig } from 'electron-vite'
import { resolve } from 'path'

export default defineConfig({
  main: {
    build: {
      outDir: 'out/main',
      rollupOptions: {
        input: { index: resolve(__dirname, 'electron/main.ts') },
      },
    },
  },
  preload: {
    build: {
      outDir: 'out/preload',
      rollupOptions: {
        input: { index: resolve(__dirname, 'electron/preload.ts') },
      },
    },
  },
  renderer: {
    root: 'spa',
    build: {
      outDir: resolve(__dirname, 'out/renderer'),
      rollupOptions: {
        input: resolve(__dirname, 'spa/index.html'),
      },
    },
  },
})
```

- [ ] **Step 7: Verify workspace setup**

Run: `pnpm install`
Expected: Dependencies installed, workspace recognized.

- [ ] **Step 8: Commit**

```bash
git add package.json pnpm-workspace.yaml electron/ electron.vite.config.ts
git commit -m "chore: Electron project scaffolding (electron-vite + pnpm workspace)"
```

---

## Task 2: SPA Types + PaneContent + i18n

**Files:**
- Modify: `spa/src/types/electron.d.ts`
- Modify: `spa/src/types/tab.ts`
- Modify: `spa/src/lib/pane-labels.ts`
- Modify: `spa/src/lib/pane-labels.test.ts`
- Modify: `spa/src/lib/route-utils.ts`
- Modify: `spa/src/stores/useHistoryStore.test.ts`
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: Write failing tests for memory-monitor pane label + icon**

Add to `spa/src/lib/pane-labels.test.ts`:

In `describe('getPaneLabel')`:
```typescript
it('returns i18n key for memory-monitor', () => {
  expect(getPaneLabel({ kind: 'memory-monitor' }, mockSessionStore, mockWorkspaceStore, mockT)).toBe('monitor.title')
})
```

In `describe('getPaneIcon')`:
```typescript
it('returns ChartBar for memory-monitor', () => {
  expect(getPaneIcon({ kind: 'memory-monitor' })).toBe('ChartBar')
})
```

- [ ] **Step 2: Run tests to verify fail**

Run: `cd spa && npx vitest run src/lib/pane-labels.test.ts`
Expected: FAIL — `memory-monitor` not in PaneContent union.

- [ ] **Step 3: Add memory-monitor to PaneContent union**

In `spa/src/types/tab.ts`, add after the browser kind:

```typescript
  | { kind: 'memory-monitor' }
```

- [ ] **Step 4: Add memory-monitor cases to pane-labels.ts**

In `getPaneLabel`, add case:
```typescript
    case 'memory-monitor':
      return t('monitor.title')
```

In `getPaneIcon`, add case:
```typescript
    case 'memory-monitor':
      return 'ChartBar'
```

- [ ] **Step 5: Add memory-monitor case to route-utils.ts tabToUrl**

```typescript
    case 'memory-monitor':
      return '/'
```

- [ ] **Step 6: Add memory-monitor case to useHistoryStore.test.ts makeContent**

```typescript
    case 'memory-monitor': return { kind: 'memory-monitor' }
```

- [ ] **Step 7: Run tests to verify pass**

Run: `cd spa && npx vitest run`
Expected: All tests pass.

- [ ] **Step 8: Expand electron.d.ts**

Replace `spa/src/types/electron.d.ts` with:

```typescript
interface ElectronWindowInfo {
  id: string
  title: string
}

interface ElectronBounds {
  x: number
  y: number
  width: number
  height: number
}

interface ElectronTabMetrics {
  paneId: string
  kind: string
  memoryKB: number
  cpuPercent: number
}

interface Window {
  electronAPI?: {
    // Window Management
    tearOffTab: (tabJson: string) => Promise<void>
    mergeTab: (tabJson: string, targetWindowId: string) => Promise<void>
    getWindows: () => Promise<ElectronWindowInfo[]>
    onTabReceived: (callback: (tabJson: string) => void) => () => void

    // Browser View
    openBrowserView: (url: string, paneId: string) => Promise<void>
    closeBrowserView: (paneId: string) => Promise<void>
    navigateBrowserView: (paneId: string, url: string) => Promise<void>
    resizeBrowserView: (paneId: string, bounds: ElectronBounds) => Promise<void>

    // Memory Monitor
    getProcessMetrics: () => Promise<ElectronTabMetrics[]>
    onMetricsUpdate: (callback: (metrics: ElectronTabMetrics[]) => void) => () => void
  }
}
```

Note: Types prefixed with `Electron` to avoid global namespace collisions. File remains ambient (no import/export).

- [ ] **Step 9: Add i18n keys to en.json**

Add after the last `browser.*` key block:

```json
  "tray.show_window": "Show Window",
  "tray.quit": "Quit tmux-box",

  "settings.section.electron": "Desktop App",
  "settings.electron.title": "Desktop App",
  "settings.electron.desc": "Desktop app specific settings",
  "settings.electron.idle_timeout.label": "Browser View Idle Timeout",
  "settings.electron.idle_timeout.desc": "Minutes before background browser view is discarded",
  "settings.electron.idle_timeout.aria": "Idle timeout",
  "settings.electron.memory_limit.label": "Browser View Memory Limit",
  "settings.electron.memory_limit.desc": "Discard oldest view when total memory exceeds this (MB)",
  "settings.electron.memory_limit.aria": "Memory limit",
  "settings.electron.max_bg.label": "Max Background Views",
  "settings.electron.max_bg.desc": "Maximum browser views kept alive in background",
  "settings.electron.max_bg.aria": "Max background views",

  "monitor.provider_label": "Memory Monitor",
  "monitor.requires_app": "Requires desktop app",
  "monitor.title": "Memory Monitor",
  "monitor.col.tab": "Tab",
  "monitor.col.kind": "Kind",
  "monitor.col.memory": "Memory",
  "monitor.col.cpu": "CPU",
  "monitor.col.state": "State",
  "monitor.state.active": "Active",
  "monitor.state.background": "Background",
  "monitor.state.discarded": "Discarded",
  "monitor.shared": "shared",
  "monitor.summary.renderer": "Renderer process",
  "monitor.summary.views": "Browser views",
  "monitor.summary.total": "Total app",

  "tab.move_new_window": "Move to New Window",
  "tab.move_to": "Move to",
```

- [ ] **Step 10: Add i18n keys to zh-TW.json**

```json
  "tray.show_window": "顯示視窗",
  "tray.quit": "結束 tmux-box",

  "settings.section.electron": "桌面應用程式",
  "settings.electron.title": "桌面應用程式",
  "settings.electron.desc": "桌面版專屬設定",
  "settings.electron.idle_timeout.label": "瀏覽器分頁閒置逾時",
  "settings.electron.idle_timeout.desc": "背景瀏覽器分頁閒置幾分鐘後自動釋放",
  "settings.electron.idle_timeout.aria": "閒置逾時",
  "settings.electron.memory_limit.label": "瀏覽器分頁記憶體上限",
  "settings.electron.memory_limit.desc": "所有瀏覽器分頁記憶體總和超過此值時自動釋放最舊的（MB）",
  "settings.electron.memory_limit.aria": "記憶體上限",
  "settings.electron.max_bg.label": "背景分頁上限",
  "settings.electron.max_bg.desc": "最多保留幾個背景瀏覽器分頁",
  "settings.electron.max_bg.aria": "背景分頁上限",

  "monitor.provider_label": "記憶體監控",
  "monitor.requires_app": "需要安裝桌面版本",
  "monitor.title": "記憶體監控",
  "monitor.col.tab": "分頁",
  "monitor.col.kind": "類型",
  "monitor.col.memory": "記憶體",
  "monitor.col.cpu": "CPU",
  "monitor.col.state": "狀態",
  "monitor.state.active": "使用中",
  "monitor.state.background": "背景",
  "monitor.state.discarded": "已釋放",
  "monitor.shared": "共用",
  "monitor.summary.renderer": "渲染程序",
  "monitor.summary.views": "瀏覽器分頁",
  "monitor.summary.total": "應用程式總計",

  "tab.move_new_window": "移到新視窗",
  "tab.move_to": "移到",
```

- [ ] **Step 11: Run locale completeness test**

Run: `cd spa && npx vitest run src/locales/locale-completeness.test.ts`
Expected: PASS

- [ ] **Step 12: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All tests pass.

- [ ] **Step 13: Commit**

```bash
git add spa/src/types/ spa/src/lib/pane-labels.ts spa/src/lib/pane-labels.test.ts \
  spa/src/lib/route-utils.ts spa/src/stores/useHistoryStore.test.ts \
  spa/src/locales/
git commit -m "feat: memory-monitor PaneContent + electron.d.ts expansion + i18n keys"
```

---

## Task 3: MemoryMonitorPage + ElectronSection + Registration

**Files:**
- Create: `spa/src/components/MemoryMonitorPage.tsx`
- Create: `spa/src/components/MemoryMonitorNewTabSection.tsx`
- Create: `spa/src/components/settings/ElectronSection.tsx`
- Modify: `spa/src/lib/register-panes.tsx`
- Modify: `spa/src/lib/register-panes.test.ts`

- [ ] **Step 1: Write failing registration test**

Add to `spa/src/lib/register-panes.test.ts`:

```typescript
describe('memory-monitor provider registration', () => {
  beforeEach(() => {
    clearNewTabRegistry()
    clearPaneRegistry()
    clearSettingsSectionRegistry()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearNewTabRegistry()
    clearPaneRegistry()
    clearSettingsSectionRegistry()
  })

  it('registers memory-monitor provider as disabled when no electronAPI', () => {
    registerBuiltinPanes()
    const monitor = getNewTabProviders().find((p) => p.id === 'memory-monitor')
    expect(monitor).toBeDefined()
    expect(monitor?.disabled).toBe(true)
    expect(monitor?.disabledReason).toBe('monitor.requires_app')
  })

  it('registers memory-monitor provider as enabled when electronAPI present', () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = { tearOffTab: async () => {} }
    registerBuiltinPanes()
    const monitor = getNewTabProviders().find((p) => p.id === 'memory-monitor')
    expect(monitor).toBeDefined()
    expect(monitor?.disabled).toBe(false)
  })
})
```

Also add settings section test:

```typescript
import { getSettingsSections } from './settings-section-registry'

describe('electron settings section registration', () => {
  beforeEach(() => {
    clearNewTabRegistry()
    clearPaneRegistry()
    clearSettingsSectionRegistry()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearNewTabRegistry()
    clearPaneRegistry()
    clearSettingsSectionRegistry()
  })

  it('does not register electron section when no electronAPI', () => {
    registerBuiltinPanes()
    const electron = getSettingsSections().find((s) => s.id === 'electron')
    expect(electron).toBeUndefined()
  })

  it('registers electron section when electronAPI present', () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = { tearOffTab: async () => {} }
    registerBuiltinPanes()
    const electron = getSettingsSections().find((s) => s.id === 'electron')
    expect(electron).toBeDefined()
  })
})
```

Check if `getSettingsSections` is exported from `settings-section-registry.ts`. If not, add the export.

- [ ] **Step 2: Run tests to verify fail**

Run: `cd spa && npx vitest run src/lib/register-panes.test.ts`
Expected: FAIL — memory-monitor provider not registered, ElectronSection not found.

- [ ] **Step 3: Create MemoryMonitorPage.tsx**

```tsx
// spa/src/components/MemoryMonitorPage.tsx
import { useEffect, useState } from 'react'
import { useI18nStore } from '../stores/useI18nStore'

interface TabMetrics {
  paneId: string
  kind: string
  memoryKB: number
  cpuPercent: number
}

export function MemoryMonitorPage() {
  const t = useI18nStore((s) => s.t)
  const [metrics, setMetrics] = useState<TabMetrics[]>([])

  useEffect(() => {
    if (!window.electronAPI) return

    window.electronAPI.getProcessMetrics().then(setMetrics)
    const unsub = window.electronAPI.onMetricsUpdate(setMetrics)
    return unsub
  }, [])

  if (!window.electronAPI) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-muted">{t('monitor.requires_app')}</p>
      </div>
    )
  }

  return (
    <div className="flex-1 flex flex-col p-4 overflow-y-auto">
      <h2 className="text-lg text-text-secondary mb-4">{t('monitor.title')}</h2>
      <div className="text-xs font-mono">
        {/* Header */}
        <div className="grid grid-cols-[1fr_100px_80px_80px_80px] gap-2 px-3 py-2 text-text-muted border-b border-border-default">
          <div>{t('monitor.col.tab')}</div>
          <div>{t('monitor.col.kind')}</div>
          <div>{t('monitor.col.memory')}</div>
          <div>{t('monitor.col.cpu')}</div>
          <div>{t('monitor.col.state')}</div>
        </div>
        {/* Rows */}
        {metrics.map((m) => (
          <div key={m.paneId} className="grid grid-cols-[1fr_100px_80px_80px_80px] gap-2 px-3 py-2 border-b border-border-subtle">
            <div className="text-text-primary truncate">{m.paneId}</div>
            <div className="text-text-muted">{m.kind}</div>
            <div className="text-text-primary">
              {m.memoryKB > 0 ? `${Math.round(m.memoryKB / 1024)} MB` : t('monitor.shared')}
            </div>
            <div className="text-text-primary">
              {m.cpuPercent > 0 ? `${m.cpuPercent.toFixed(1)}%` : t('monitor.shared')}
            </div>
            <div className="text-text-muted">{t('monitor.state.active')}</div>
          </div>
        ))}
        {metrics.length === 0 && (
          <div className="px-3 py-4 text-text-muted text-center">{t('monitor.shared')}</div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Create MemoryMonitorNewTabSection.tsx**

```tsx
// spa/src/components/MemoryMonitorNewTabSection.tsx
import { ChartBar } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import type { NewTabProviderProps } from '../lib/new-tab-registry'

export function MemoryMonitorNewTabSection({ onSelect }: NewTabProviderProps) {
  const t = useI18nStore((s) => s.t)

  return (
    <button
      onClick={() => onSelect({ kind: 'memory-monitor' })}
      className="flex items-center gap-2 px-2 py-1.5 w-full text-left text-xs text-text-secondary hover:bg-surface-hover rounded-md transition-colors"
    >
      <ChartBar size={16} className="text-text-muted flex-shrink-0" />
      {t('monitor.title')}
    </button>
  )
}
```

- [ ] **Step 5: Create ElectronSection.tsx**

```tsx
// spa/src/components/settings/ElectronSection.tsx
import { useI18nStore } from '../../stores/useI18nStore'

export function ElectronSection() {
  const t = useI18nStore((s) => s.t)

  // Settings values are stored in useUISettingsStore — extend it when Electron
  // main process reads these via IPC. For now, render the UI controls.
  return (
    <div className="space-y-6">
      <div>
        <h3 className="text-sm font-medium text-text-primary">{t('settings.electron.title')}</h3>
        <p className="text-xs text-text-muted mt-1">{t('settings.electron.desc')}</p>
      </div>

      <div className="space-y-4">
        {/* Idle Timeout */}
        <div className="flex items-center justify-between">
          <div>
            <div className="text-sm text-text-primary">{t('settings.electron.idle_timeout.label')}</div>
            <div className="text-xs text-text-muted">{t('settings.electron.idle_timeout.desc')}</div>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="number"
              defaultValue={5}
              min={1}
              max={60}
              aria-label={t('settings.electron.idle_timeout.aria')}
              className="w-16 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-2 py-1 text-center focus:border-border-active focus:outline-none"
            />
            <span className="text-xs text-text-muted">min</span>
          </div>
        </div>

        {/* Memory Limit */}
        <div className="flex items-center justify-between">
          <div>
            <div className="text-sm text-text-primary">{t('settings.electron.memory_limit.label')}</div>
            <div className="text-xs text-text-muted">{t('settings.electron.memory_limit.desc')}</div>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="number"
              defaultValue={512}
              min={128}
              max={4096}
              step={128}
              aria-label={t('settings.electron.memory_limit.aria')}
              className="w-16 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-2 py-1 text-center focus:border-border-active focus:outline-none"
            />
            <span className="text-xs text-text-muted">MB</span>
          </div>
        </div>

        {/* Max Background */}
        <div className="flex items-center justify-between">
          <div>
            <div className="text-sm text-text-primary">{t('settings.electron.max_bg.label')}</div>
            <div className="text-xs text-text-muted">{t('settings.electron.max_bg.desc')}</div>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="number"
              defaultValue={3}
              min={0}
              max={20}
              aria-label={t('settings.electron.max_bg.aria')}
              className="w-16 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-2 py-1 text-center focus:border-border-active focus:outline-none"
            />
            <span className="text-xs text-text-muted">views</span>
          </div>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 6: Update register-panes.tsx**

Add imports:
```typescript
import { MemoryMonitorPage } from '../components/MemoryMonitorPage'
import { MemoryMonitorNewTabSection } from '../components/MemoryMonitorNewTabSection'
import { ElectronSection } from '../components/settings/ElectronSection'
```

Add memory-monitor pane renderer after browser renderer:
```typescript
  registerPaneRenderer('memory-monitor', {
    component: () => <MemoryMonitorPage />,
  })
```

Add memory-monitor provider after browser provider:
```typescript
  registerNewTabProvider({
    id: 'memory-monitor',
    label: 'monitor.provider_label',
    icon: 'ChartBar',
    order: 20,
    component: MemoryMonitorNewTabSection,
    disabled: !caps.canSystemTray,
    disabledReason: 'monitor.requires_app',
  })
```

Add electron settings section (conditionally):
```typescript
  if (caps.canSystemTray) {
    registerSettingsSection({
      id: 'electron',
      label: 'settings.section.electron',
      order: 5,
      component: ElectronSection,
    })
  }
```

- [ ] **Step 7: Run tests to verify pass**

Run: `cd spa && npx vitest run`
Expected: All tests pass.

- [ ] **Step 8: Run build**

Run: `cd spa && pnpm run build`
Expected: Build succeeds.

- [ ] **Step 9: Commit**

```bash
git add spa/src/components/MemoryMonitorPage.tsx spa/src/components/MemoryMonitorNewTabSection.tsx \
  spa/src/components/settings/ElectronSection.tsx spa/src/lib/register-panes.tsx \
  spa/src/lib/register-panes.test.ts
git commit -m "feat: MemoryMonitorPage + ElectronSection + registration (disabled in SPA)"
```

---

## Task 4: BrowserPane IPC + ResizeObserver

**Files:**
- Modify: `spa/src/components/BrowserPane.tsx`

- [ ] **Step 1: Update BrowserPane.tsx**

Replace the full component:

```tsx
import { useEffect, useRef } from 'react'
import { useI18nStore } from '../stores/useI18nStore'

interface BrowserPaneProps {
  paneId: string
  url: string
}

export function BrowserPane({ paneId, url }: BrowserPaneProps) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)

  // Open/close lifecycle
  useEffect(() => {
    if (!window.electronAPI) return
    window.electronAPI.openBrowserView(url, paneId)
    return () => { window.electronAPI?.closeBrowserView(paneId) }
  }, [url, paneId])

  // Bounds sync via ResizeObserver
  useEffect(() => {
    if (!window.electronAPI || !ref.current) return
    const observer = new ResizeObserver(() => {
      const el = ref.current
      if (!el) return
      const rect = el.getBoundingClientRect()
      window.electronAPI!.resizeBrowserView(paneId, {
        x: Math.round(rect.x),
        y: Math.round(rect.y),
        width: Math.round(rect.width),
        height: Math.round(rect.height),
      })
    })
    observer.observe(ref.current)
    return () => observer.disconnect()
  }, [paneId])

  // SPA fallback
  if (!window.electronAPI) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-muted">{t('browser.requires_app')}</p>
      </div>
    )
  }

  return <div ref={ref} className="w-full h-full" data-browser-pane={paneId} />
}
```

- [ ] **Step 2: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All tests pass (BrowserPane has no unit tests — SPA fallback path is unchanged).

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/BrowserPane.tsx
git commit -m "feat: BrowserPane useEffect IPC + ResizeObserver bounds sync"
```

---

## Task 5: TabContextMenu Tear-off/Merge

**Files:**
- Modify: `spa/src/components/TabContextMenu.tsx`
- Modify: `spa/src/components/TabContextMenu.test.tsx`

- [ ] **Step 1: Read TabContextMenu.test.tsx to understand test patterns**

Read the existing test file to understand how tests are structured before adding new ones.

- [ ] **Step 2: Expand ContextMenuAction union**

In `spa/src/components/TabContextMenu.tsx`, update the union type:

```typescript
export type ContextMenuAction =
  | 'viewMode-terminal' | 'viewMode-stream'
  | 'lock' | 'unlock' | 'pin' | 'unpin'
  | 'close' | 'closeOthers' | 'closeRight'
  | 'tearOff' | 'mergeTo'
```

- [ ] **Step 3: Add tear-off/merge items to menu**

In the `items` array, add before the Close section separator:

```typescript
    // Tear-off/Merge section (Electron only)
    ...(caps.canTearOffTab ? [
      'separator' as const,
      { label: t('tab.move_new_window'), action: 'tearOff' as const, show: true },
    ] : []),
```

This requires `caps` inside the component. Add at the top of the component function:

```typescript
import { getPlatformCapabilities } from '../lib/platform'

// Inside the component:
const caps = getPlatformCapabilities()
```

Note: The "Move to → Window X" submenu requires `getWindows()` async call + submenu rendering. For the first iteration, implement only "Move to New Window" (tearOff). The merge submenu can be added when Electron is running and `getWindows()` returns real data.

- [ ] **Step 4: Handle tearOff action in useTabWorkspaceActions**

Read `spa/src/hooks/useTabWorkspaceActions.ts` to understand the action handler pattern. Add the `tearOff` case:

```typescript
case 'tearOff': {
  if (!window.electronAPI) break
  const tab = tabs[tabId]
  if (!tab) break
  window.electronAPI.tearOffTab(JSON.stringify(tab))
  // Source removes tab immediately (fire-and-forget)
  removeTab(tabId)
  break
}
```

- [ ] **Step 5: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All tests pass. Existing TabContextMenu tests should not break (tearOff items hidden in SPA mode via caps gate).

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/TabContextMenu.tsx spa/src/hooks/useTabWorkspaceActions.ts
git commit -m "feat: TabContextMenu tear-off action (Electron only, gated by capabilities)"
```

---

## Task 6: Electron preload.ts

**Files:**
- Create: `electron/preload.ts`

- [ ] **Step 1: Create preload.ts**

```typescript
import { contextBridge, ipcRenderer } from 'electron'

contextBridge.exposeInMainWorld('electronAPI', {
  // Window Management
  tearOffTab: (tabJson: string) => ipcRenderer.invoke('window:tear-off', tabJson),
  mergeTab: (tabJson: string, targetWindowId: string) =>
    ipcRenderer.invoke('window:merge', tabJson, targetWindowId),
  getWindows: () => ipcRenderer.invoke('window:get-all'),
  onTabReceived: (callback: (tabJson: string) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, tabJson: string) => callback(tabJson)
    ipcRenderer.on('tab:received', handler)
    return () => ipcRenderer.removeListener('tab:received', handler)
  },

  // Browser View
  openBrowserView: (url: string, paneId: string) =>
    ipcRenderer.invoke('browser-view:open', url, paneId),
  closeBrowserView: (paneId: string) =>
    ipcRenderer.invoke('browser-view:close', paneId),
  navigateBrowserView: (paneId: string, url: string) =>
    ipcRenderer.invoke('browser-view:navigate', paneId, url),
  resizeBrowserView: (paneId: string, bounds: { x: number; y: number; width: number; height: number }) =>
    ipcRenderer.invoke('browser-view:resize', paneId, JSON.stringify(bounds)),

  // Memory Monitor
  getProcessMetrics: () => ipcRenderer.invoke('metrics:get'),
  onMetricsUpdate: (callback: (metrics: unknown[]) => void) => {
    const handler = (_event: Electron.IpcRendererEvent, metrics: unknown[]) => callback(metrics)
    ipcRenderer.on('metrics:update', handler)
    return () => ipcRenderer.removeListener('metrics:update', handler)
  },
})
```

- [ ] **Step 2: Commit**

```bash
git add electron/preload.ts
git commit -m "feat: Electron preload.ts — contextBridge with 10 IPC methods"
```

---

## Task 7: Electron main.ts + tray.ts

**Files:**
- Create: `electron/tray.ts`
- Create: `electron/main.ts`

- [ ] **Step 1: Create tray.ts**

```typescript
import { Tray, Menu, nativeImage } from 'electron'
import { join } from 'path'
import type { WindowManager } from './window-manager'

let tray: Tray | null = null

export function createTray(windowManager: WindowManager): Tray {
  // macOS Template image (auto dark/light)
  const iconPath = join(__dirname, '../../spa/public/favicon.svg')
  const icon = nativeImage.createFromPath(iconPath).resize({ width: 18, height: 18 })
  icon.setTemplateImage(true)

  tray = new Tray(icon)
  tray.setToolTip('tmux-box')
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: 'Show Window', click: () => windowManager.showOrCreate() },
      { type: 'separator' },
      { label: 'Quit', click: () => { require('electron').app.quit() } },
    ]),
  )

  tray.on('click', () => windowManager.showOrCreate())

  return tray
}

export function destroyTray(): void {
  tray?.destroy()
  tray = null
}
```

- [ ] **Step 2: Create main.ts**

```typescript
import { app, BrowserWindow, ipcMain } from 'electron'
import { join } from 'path'
import { WindowManager } from './window-manager'
import { BrowserViewManager } from './browser-view-manager'
import { createTray } from './tray'

const windowManager = new WindowManager()
const browserViewManager = new BrowserViewManager()
let metricsInterval: ReturnType<typeof setInterval> | null = null

function registerIpcHandlers(): void {
  // Window Management
  ipcMain.handle('window:tear-off', (_event, tabJson: string) => {
    windowManager.handleTearOff(tabJson)
  })
  ipcMain.handle('window:merge', (_event, tabJson: string, targetWindowId: string) => {
    windowManager.handleMerge(tabJson, targetWindowId)
  })
  ipcMain.handle('window:get-all', () => {
    return windowManager.getAll()
  })

  // Browser View
  ipcMain.handle('browser-view:open', (event, url: string, paneId: string) => {
    const win = BrowserWindow.fromWebContents(event.sender)
    if (win) browserViewManager.open(win, url, paneId)
  })
  ipcMain.handle('browser-view:close', (_event, paneId: string) => {
    browserViewManager.close(paneId)
  })
  ipcMain.handle('browser-view:navigate', (_event, paneId: string, url: string) => {
    browserViewManager.navigate(paneId, url)
  })
  ipcMain.handle('browser-view:resize', (_event, paneId: string, boundsJson: string) => {
    browserViewManager.resize(paneId, JSON.parse(boundsJson))
  })

  // Memory Monitor
  ipcMain.handle('metrics:get', () => {
    return browserViewManager.getMetrics()
  })
}

function startMetricsPolling(): void {
  metricsInterval = setInterval(() => {
    const metrics = browserViewManager.getMetrics()
    for (const win of windowManager.getAllWindows()) {
      win.webContents.send('metrics:update', metrics)
    }
  }, 30_000) // 30 seconds
}

app.whenReady().then(() => {
  registerIpcHandlers()
  createTray(windowManager)

  windowManager.createWindow()

  startMetricsPolling()

  app.on('activate', () => {
    windowManager.showOrCreate()
  })
})

// macOS: close window ≠ quit app
app.on('window-all-closed', () => {
  // no-op on macOS — tray keeps running
})

app.on('before-quit', () => {
  if (metricsInterval) clearInterval(metricsInterval)
  browserViewManager.destroyAll()
})
```

- [ ] **Step 3: Commit**

```bash
git add electron/main.ts electron/tray.ts
git commit -m "feat: Electron main.ts + tray.ts — app lifecycle, IPC, system tray"
```

---

## Task 8: Window Manager

**Files:**
- Create: `electron/window-manager.ts`

- [ ] **Step 1: Create window-manager.ts**

```typescript
import { BaseWindow, WebContentsView, BrowserWindow } from 'electron'
import { join } from 'path'
import { is } from '@electron-toolkit/utils'

interface WindowInfo {
  id: string
  title: string
}

export class WindowManager {
  private windows = new Map<string, BrowserWindow>()
  private nextId = 1

  createWindow(opts?: { tabJson?: string }): BrowserWindow {
    const id = String(this.nextId++)
    const win = new BrowserWindow({
      width: 1200,
      height: 800,
      titleBarStyle: 'hiddenInset',
      trafficLightPosition: { x: 12, y: 12 },
      webPreferences: {
        contextIsolation: true,
        sandbox: true,
        nodeIntegration: false,
        preload: join(__dirname, '../preload/index.js'),
      },
    })

    this.windows.set(id, win)

    // Load SPA
    if (is.dev) {
      win.loadURL('http://100.64.0.2:5174')
    } else {
      win.loadFile(join(__dirname, '../renderer/index.html'))
    }

    // If tab data provided, send after SPA is ready
    if (opts?.tabJson) {
      win.webContents.once('did-finish-load', () => {
        // Small delay to let React hydrate
        setTimeout(() => {
          win.webContents.send('tab:received', opts.tabJson)
        }, 500)
      })
    }

    win.on('closed', () => {
      this.windows.delete(id)
    })

    // Store window ID for IPC identification
    ;(win as unknown as Record<string, unknown>).__windowId = id

    return win
  }

  closeWindow(windowId: string): void {
    const win = this.windows.get(windowId)
    if (win && !win.isDestroyed()) {
      win.close()
    }
  }

  getAll(): WindowInfo[] {
    return Array.from(this.windows.entries()).map(([id, win]) => ({
      id,
      title: win.isDestroyed() ? '' : win.getTitle(),
    }))
  }

  getAllWindows(): BrowserWindow[] {
    return Array.from(this.windows.values()).filter((w) => !w.isDestroyed())
  }

  showOrCreate(): void {
    const wins = this.getAllWindows()
    if (wins.length > 0) {
      const win = wins[0]
      if (win.isMinimized()) win.restore()
      win.show()
      win.focus()
    } else {
      this.createWindow()
    }
  }

  handleTearOff(tabJson: string): void {
    this.createWindow({ tabJson })
  }

  handleMerge(tabJson: string, targetWindowId: string): void {
    const target = this.windows.get(targetWindowId)
    if (target && !target.isDestroyed()) {
      target.webContents.send('tab:received', tabJson)
      target.show()
      target.focus()
    }
  }
}
```

- [ ] **Step 2: Commit**

```bash
git add electron/window-manager.ts
git commit -m "feat: WindowManager — BaseWindow CRUD, tear-off/merge"
```

---

## Task 9: Browser View Manager

**Files:**
- Create: `electron/browser-view-manager.ts`

- [ ] **Step 1: Create browser-view-manager.ts**

```typescript
import { WebContentsView, BrowserWindow, app } from 'electron'

interface Bounds {
  x: number
  y: number
  width: number
  height: number
}

interface ViewEntry {
  view: WebContentsView
  paneId: string
  url: string
  window: BrowserWindow
  state: 'active' | 'background'
  lastActiveAt: number
}

interface Snapshot {
  url: string
  paneId: string
}

interface TabMetrics {
  paneId: string
  kind: string
  memoryKB: number
  cpuPercent: number
}

// Default settings — can be overridden via IPC from SPA settings
const DEFAULTS = {
  idleTimeoutMs: 5 * 60 * 1000,   // 5 minutes
  memoryLimitMB: 512,
  maxBackground: 3,
}

export class BrowserViewManager {
  private views = new Map<string, ViewEntry>()
  private snapshots = new Map<string, Snapshot>()
  private timers = new Map<string, ReturnType<typeof setTimeout>>()
  private checkInterval: ReturnType<typeof setInterval> | null = null

  constructor() {
    // Periodic memory check
    this.checkInterval = setInterval(() => this.checkMemoryLimit(), 30_000)
  }

  open(win: BrowserWindow, url: string, paneId: string): void {
    // If already exists, just activate
    if (this.views.has(paneId)) {
      this.activate(paneId)
      return
    }

    const view = new WebContentsView({
      webPreferences: {
        contextIsolation: true,
        sandbox: true,
        backgroundThrottling: false, // Active — no throttling
      },
    })

    win.contentView.addChildView(view)
    view.webContents.loadURL(url)

    this.views.set(paneId, {
      view,
      paneId,
      url,
      window: win,
      state: 'active',
      lastActiveAt: Date.now(),
    })

    // Clear any existing snapshot
    this.snapshots.delete(paneId)
    this.clearTimer(paneId)
  }

  close(paneId: string): void {
    this.deactivate(paneId)
  }

  navigate(paneId: string, url: string): void {
    const entry = this.views.get(paneId)
    if (entry) {
      entry.url = url
      entry.view.webContents.loadURL(url)
    }
  }

  resize(paneId: string, bounds: Bounds): void {
    const entry = this.views.get(paneId)
    if (entry) {
      entry.view.setBounds(bounds)
    }
  }

  private activate(paneId: string): void {
    const entry = this.views.get(paneId)
    if (!entry) {
      // Restore from snapshot
      const snapshot = this.snapshots.get(paneId)
      if (snapshot) {
        // Need a window — caller should provide. For now, use first available.
        return
      }
      return
    }

    entry.state = 'active'
    entry.lastActiveAt = Date.now()
    entry.view.webContents.backgroundThrottling = false
    this.clearTimer(paneId)
    // Bounds will be set by SPA ResizeObserver via resize()
  }

  private deactivate(paneId: string): void {
    const entry = this.views.get(paneId)
    if (!entry) return

    entry.state = 'background'
    entry.view.webContents.backgroundThrottling = true
    // Move off-screen
    entry.view.setBounds({ x: -10000, y: -10000, width: 1, height: 1 })

    // Start idle timer
    this.startIdleTimer(paneId)

    // Check max background count
    this.enforceMaxBackground()
  }

  private startIdleTimer(paneId: string): void {
    this.clearTimer(paneId)
    this.timers.set(paneId, setTimeout(() => {
      this.discard(paneId)
    }, DEFAULTS.idleTimeoutMs))
  }

  private clearTimer(paneId: string): void {
    const timer = this.timers.get(paneId)
    if (timer) {
      clearTimeout(timer)
      this.timers.delete(paneId)
    }
  }

  private discard(paneId: string): void {
    const entry = this.views.get(paneId)
    if (!entry) return

    // Save snapshot
    this.snapshots.set(paneId, {
      url: entry.view.webContents.getURL() || entry.url,
      paneId,
    })

    // Destroy
    entry.window.contentView.removeChildView(entry.view)
    entry.view.webContents.close()
    this.views.delete(paneId)
    this.clearTimer(paneId)
  }

  private enforceMaxBackground(): void {
    const bgEntries = Array.from(this.views.values())
      .filter((e) => e.state === 'background')
      .sort((a, b) => a.lastActiveAt - b.lastActiveAt)

    while (bgEntries.length > DEFAULTS.maxBackground) {
      const oldest = bgEntries.shift()!
      this.discard(oldest.paneId)
    }
  }

  private checkMemoryLimit(): void {
    const metrics = app.getAppMetrics()
    let totalViewMemoryKB = 0

    for (const entry of this.views.values()) {
      const pid = entry.view.webContents.getOSProcessId()
      const metric = metrics.find((m) => m.pid === pid)
      if (metric?.memory) {
        totalViewMemoryKB += metric.memory.privateBytes ?? 0
      }
    }

    const totalMB = totalViewMemoryKB / 1024
    if (totalMB > DEFAULTS.memoryLimitMB) {
      // Discard oldest background view
      const bgEntries = Array.from(this.views.values())
        .filter((e) => e.state === 'background')
        .sort((a, b) => a.lastActiveAt - b.lastActiveAt)

      if (bgEntries.length > 0) {
        this.discard(bgEntries[0].paneId)
      }
    }
  }

  getMetrics(): TabMetrics[] {
    const appMetrics = app.getAppMetrics()
    const result: TabMetrics[] = []

    for (const entry of this.views.values()) {
      const pid = entry.view.webContents.getOSProcessId()
      const metric = appMetrics.find((m) => m.pid === pid)
      result.push({
        paneId: entry.paneId,
        kind: 'browser',
        memoryKB: metric?.memory?.privateBytes ?? 0,
        cpuPercent: metric?.cpu?.percentCPUUsage ?? 0,
      })
    }

    return result
  }

  destroyAll(): void {
    if (this.checkInterval) clearInterval(this.checkInterval)
    for (const paneId of [...this.views.keys()]) {
      const entry = this.views.get(paneId)!
      entry.window.contentView.removeChildView(entry.view)
      entry.view.webContents.close()
    }
    this.views.clear()
    this.snapshots.clear()
    for (const timer of this.timers.values()) clearTimeout(timer)
    this.timers.clear()
  }
}
```

- [ ] **Step 2: Commit**

```bash
git add electron/browser-view-manager.ts
git commit -m "feat: BrowserViewManager — WebContentsView pool, LRU, timeout, memory monitoring"
```

---

## Task 10: Final Verification

- [ ] **Step 1: Run SPA test suite**

Run: `cd spa && npx vitest run`
Expected: All tests pass (520+ tests).

- [ ] **Step 2: Run SPA lint**

Run: `cd spa && pnpm run lint`
Expected: No new lint errors from this PR.

- [ ] **Step 3: Run SPA build**

Run: `cd spa && pnpm run build`
Expected: Build succeeds, tsc clean.

- [ ] **Step 4: Test electron-vite dev**

Run: `pnpm electron:dev`
Expected: Electron window opens, loads SPA from dev server (http://100.64.0.2:5174).

- [ ] **Step 5: Smoke test checklist**

- [ ] App launches, SPA loads in Electron window
- [ ] System tray icon appears (macOS menu bar)
- [ ] Tray → "Show Window" works
- [ ] Tray → "Quit" exits app
- [ ] Close window → tray persists, app not quit
- [ ] Tray click → window re-opens
- [ ] Right-click tab → "Move to New Window" appears (Electron only)
- [ ] Tear-off → new window opens with tab
- [ ] Browser pane: New Tab → Browser → enter URL → WebContentsView loads
- [ ] Browser pane tab switch → view moves to background
- [ ] Settings → "Desktop App" section visible with 3 controls
- [ ] Memory Monitor: New Tab → Memory Monitor → shows table
- [ ] SPA mode (browser): all Electron features hidden/disabled

- [ ] **Step 6: Build Electron app**

Run: `pnpm electron:build`
Expected: Produces `.app` bundle in `dist/`.

- [ ] **Step 7: Commit any fixes**

```bash
git add -A && git commit -m "fix: address final verification issues"
```
