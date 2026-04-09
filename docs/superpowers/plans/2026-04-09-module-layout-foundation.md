# Module + Layout Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Module Registry (unified pane + view registration) and Layout Store (4-region state management), then migrate all existing pane registrations to the new system.

**Architecture:** Module Registry replaces `pane-registry.ts` as the single source of truth for pane renderers and view definitions. Layout Store manages the 4 sidebar/panel regions. Existing `new-tab-registry` and `settings-section-registry` stay independent.

**Tech Stack:** React 19 / Zustand 5 / Vitest / TypeScript

**Spec:** `docs/superpowers/specs/2026-04-09-module-layout-pane-split-design.md`

**This is Plan 1 of 3:**
1. **Foundation** (this plan) — Module Registry + Layout Store + migration
2. **UI Chrome** — Title Bar + Region components + App layout restructure
3. **Pane Split** — Split renderer + pane operations + New Pane Page + Files Module

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `spa/src/lib/module-registry.ts` | Module/View/Pane registry + query functions |
| Create | `spa/src/lib/module-registry.test.ts` | Registry CRUD tests |
| Create | `spa/src/lib/register-modules.tsx` | Built-in module registrations (replaces register-panes.tsx) |
| Create | `spa/src/lib/register-modules.test.ts` | Smoke test: all modules registered |
| Create | `spa/src/stores/useLayoutStore.ts` | 4-region state + persist |
| Create | `spa/src/stores/useLayoutStore.test.ts` | Region mode/width/activeView tests |
| Modify | `spa/src/types/tab.ts` | Add `SidebarRegion` type + `Workspace.sidebarState` |
| Modify | `spa/src/lib/storage/keys.ts` | Add `LAYOUT` key |
| Modify | `spa/src/App.tsx` | Switch from `registerBuiltinPanes()` to `registerBuiltinModules()` |
| Modify | `spa/src/components/PaneLayoutRenderer.tsx` | Use module-registry instead of pane-registry |
| Delete | `spa/src/lib/pane-registry.ts` | Replaced by module-registry |
| Delete | `spa/src/lib/pane-registry.test.ts` | Replaced by module-registry tests |
| Delete | `spa/src/lib/register-panes.tsx` | Replaced by register-modules.tsx |
| Delete | `spa/src/lib/register-panes.test.ts` | Replaced by register-modules tests |

**Task order:** Task 1 (SidebarRegion types) → Task 2 (module registry) → Task 3 (pane migration) → Task 4 (layout store) → Task 5 (verification)

---

### Task 1: Add SidebarRegion + WorkspaceSidebarState types

**Files:**
- Modify: `spa/src/types/tab.ts`
- Modify: `spa/src/types/tab.test.ts`

> **Why first:** `SidebarRegion` is imported by `module-registry.ts` (Task 2), so the type must exist before the registry.

- [ ] **Step 1: Write test for new types**

Append to `spa/src/types/tab.test.ts`:

```typescript
import type { SidebarRegion, WorkspaceSidebarState } from './tab'

describe('SidebarRegion type', () => {
  it('accepts valid region values', () => {
    const regions: SidebarRegion[] = [
      'primary-sidebar',
      'primary-panel',
      'secondary-panel',
      'secondary-sidebar',
    ]
    expect(regions).toHaveLength(4)
  })
})

describe('Workspace.sidebarState', () => {
  it('is optional on Workspace', () => {
    const ws = createWorkspace('test')
    expect(ws.sidebarState).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/types/tab.test.ts`
Expected: FAIL — `SidebarRegion` not exported

- [ ] **Step 3: Add types to tab.ts**

Add after the `Workspace` interface and before `// === Factories ===`:

```typescript
// === Sidebar Region (layout system) ===
export type SidebarRegion =
  | 'primary-sidebar'
  | 'primary-panel'
  | 'secondary-panel'
  | 'secondary-sidebar'

export interface WorkspaceSidebarState {
  regions: Partial<Record<SidebarRegion, {
    activeViewId?: string
    width: number
    mode: 'pinned' | 'default' | 'collapsed'
  }>>
}
```

Add `sidebarState?: WorkspaceSidebarState` to the `Workspace` interface.

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/types/tab.test.ts`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/types/tab.ts spa/src/types/tab.test.ts
git commit -m "feat: add SidebarRegion type and Workspace.sidebarState"
```

---

### Task 2: Module Registry — Types + Core API

**Files:**
- Create: `spa/src/lib/module-registry.ts`
- Create: `spa/src/lib/module-registry.test.ts`

- [ ] **Step 1: Write failing tests for module registration**

```typescript
// spa/src/lib/module-registry.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerModule,
  unregisterModule,
  getModule,
  getModules,
  getPaneRenderer,
  getViewDefinition,
  getViewsByRegion,
  clearModuleRegistry,
} from './module-registry'
import type { ModuleDefinition } from './module-registry'

const DummyComponent = (() => null) as React.FC<any>

const sessionModule: ModuleDefinition = {
  id: 'session',
  name: 'Session',
  pane: { kind: 'tmux-session', component: DummyComponent },
  views: [{
    id: 'session-list',
    label: 'Sessions',
    icon: 'List',
    scope: 'system',
    defaultRegion: 'primary-sidebar',
    component: DummyComponent,
  }],
}

const filesModule: ModuleDefinition = {
  id: 'files',
  name: 'Files',
  views: [{
    id: 'file-tree',
    label: 'Files',
    icon: 'FolderOpen',
    scope: 'workspace',
    defaultRegion: 'primary-panel',
    component: DummyComponent,
  }],
}

beforeEach(() => {
  clearModuleRegistry()
})

describe('module-registry', () => {
  describe('registerModule / getModule', () => {
    it('registers and retrieves a module', () => {
      registerModule(sessionModule)
      expect(getModule('session')).toEqual(sessionModule)
    })

    it('returns undefined for unregistered module', () => {
      expect(getModule('nonexistent')).toBeUndefined()
    })

    it('overwrites existing module with same id', () => {
      registerModule(sessionModule)
      const updated = { ...sessionModule, name: 'Updated' }
      registerModule(updated)
      expect(getModule('session')?.name).toBe('Updated')
    })
  })

  describe('unregisterModule', () => {
    it('removes a registered module', () => {
      registerModule(sessionModule)
      unregisterModule('session')
      expect(getModule('session')).toBeUndefined()
    })

    it('is a no-op for unregistered module', () => {
      unregisterModule('nonexistent') // should not throw
    })
  })

  describe('getModules', () => {
    it('returns all registered modules', () => {
      registerModule(sessionModule)
      registerModule(filesModule)
      expect(getModules()).toHaveLength(2)
    })

    it('returns empty array when none registered', () => {
      expect(getModules()).toEqual([])
    })
  })

  describe('getPaneRenderer', () => {
    it('returns component for registered pane kind', () => {
      registerModule(sessionModule)
      const renderer = getPaneRenderer('tmux-session')
      expect(renderer).toBeDefined()
      expect(renderer?.component).toBe(DummyComponent)
    })

    it('returns undefined for module without pane', () => {
      registerModule(filesModule)
      expect(getPaneRenderer('files')).toBeUndefined()
    })

    it('returns undefined for unknown kind', () => {
      expect(getPaneRenderer('unknown')).toBeUndefined()
    })
  })

  describe('getViewDefinition', () => {
    it('returns view by id', () => {
      registerModule(sessionModule)
      const view = getViewDefinition('session-list')
      expect(view?.label).toBe('Sessions')
    })

    it('returns undefined for unknown view id', () => {
      expect(getViewDefinition('unknown')).toBeUndefined()
    })
  })

  describe('getViewsByRegion', () => {
    it('returns views matching region', () => {
      registerModule(sessionModule)
      registerModule(filesModule)
      const sidebarViews = getViewsByRegion('primary-sidebar')
      expect(sidebarViews).toHaveLength(1)
      expect(sidebarViews[0].id).toBe('session-list')
    })

    it('filters by scope when provided', () => {
      registerModule(sessionModule)
      registerModule(filesModule)
      const systemViews = getViewsByRegion('primary-sidebar', 'system')
      expect(systemViews).toHaveLength(1)
      const wsViews = getViewsByRegion('primary-sidebar', 'workspace')
      expect(wsViews).toHaveLength(0)
    })

    it('returns empty array for region with no views', () => {
      expect(getViewsByRegion('secondary-sidebar')).toEqual([])
    })
  })

  describe('clearModuleRegistry', () => {
    it('removes all modules', () => {
      registerModule(sessionModule)
      registerModule(filesModule)
      clearModuleRegistry()
      expect(getModules()).toEqual([])
      expect(getPaneRenderer('tmux-session')).toBeUndefined()
    })
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/lib/module-registry.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement module-registry.ts**

```typescript
// spa/src/lib/module-registry.ts
import type React from 'react'
import type { Pane, SidebarRegion } from '../types/tab'

// Re-export for convenience
export type { SidebarRegion } from '../types/tab'

// === Types ===

export interface PaneRendererProps {
  pane: Pane
  isActive: boolean
}

export interface PaneDefinition {
  kind: string
  component: React.ComponentType<PaneRendererProps>
}

export interface ViewProps {
  hostId?: string
  workspaceId?: string
  isActive: boolean
}

export interface ViewDefinition {
  id: string
  label: string
  icon: string
  scope: 'system' | 'workspace'
  defaultRegion: SidebarRegion
  component: React.ComponentType<ViewProps>
}

export interface ModuleDefinition {
  id: string
  name: string
  pane?: PaneDefinition
  views?: ViewDefinition[]
}

// === Registry ===

const modules = new Map<string, ModuleDefinition>()

export function registerModule(module: ModuleDefinition): void {
  modules.set(module.id, module)
}

export function unregisterModule(id: string): void {
  modules.delete(id)
}

export function getModule(id: string): ModuleDefinition | undefined {
  return modules.get(id)
}

export function getModules(): ModuleDefinition[] {
  return [...modules.values()]
}

// === Convenience queries ===

export function getPaneRenderer(kind: string): PaneDefinition | undefined {
  for (const m of modules.values()) {
    if (m.pane?.kind === kind) return m.pane
  }
  return undefined
}

export function getViewDefinition(viewId: string): ViewDefinition | undefined {
  for (const m of modules.values()) {
    if (!m.views) continue
    const view = m.views.find((v) => v.id === viewId)
    if (view) return view
  }
  return undefined
}

export function getViewsByRegion(
  region: SidebarRegion,
  scope?: 'system' | 'workspace',
): ViewDefinition[] {
  const result: ViewDefinition[] = []
  for (const m of modules.values()) {
    if (!m.views) continue
    for (const v of m.views) {
      if (v.defaultRegion === region && (!scope || v.scope === scope)) {
        result.push(v)
      }
    }
  }
  return result
}

export function clearModuleRegistry(): void {
  modules.clear()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/lib/module-registry.test.ts`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/module-registry.ts spa/src/lib/module-registry.test.ts
git commit -m "feat: add module-registry with unified pane + view registration"
```

---

### Task 3: Migrate existing panes to Module registration

**Files:**
- Create: `spa/src/lib/register-modules.tsx`
- Create: `spa/src/lib/register-modules.test.ts`
- Modify: `spa/src/components/PaneLayoutRenderer.tsx`
- Modify: `spa/src/App.tsx`
- Delete: `spa/src/lib/pane-registry.ts`
- Delete: `spa/src/lib/pane-registry.test.ts`
- Delete: `spa/src/lib/register-panes.tsx`
- Delete: `spa/src/lib/register-panes.test.ts`

- [ ] **Step 1: Create register-modules.tsx**

Migrate all `registerPaneRenderer` calls from `register-panes.tsx` to `registerModule` calls. Keep `registerSettingsSection` and `registerNewTabProvider` calls — they stay independent.

```tsx
// spa/src/lib/register-modules.tsx
import { registerModule } from './module-registry'
import { registerNewTabProvider } from './new-tab-registry'
import { registerSettingsSection } from './settings-section-registry'
import { findPane } from './pane-tree'
import { getPlatformCapabilities } from './platform'
import { SessionPaneContent } from '../components/SessionPaneContent'
import { NewTabPage } from '../components/NewTabPage'
import { DashboardPage } from '../components/DashboardPage'
import { HistoryPage } from '../components/HistoryPage'
import { SettingsPage } from '../components/SettingsPage'
import { SessionSection } from '../components/SessionSection'
import { BrowserPane } from '../components/BrowserPane'
import { BrowserNewTabSection } from '../components/BrowserNewTabSection'
import { MemoryMonitorPage } from '../components/MemoryMonitorPage'
import { HostPage } from '../components/HostPage'
import { AppearanceSection } from '../components/settings/AppearanceSection'
import { AgentSection } from '../components/settings/AgentSection'
import { TerminalSection } from '../components/settings/TerminalSection'
import { ElectronSection } from '../components/settings/ElectronSection'
import { DevEnvironmentSection } from '../components/settings/DevEnvironmentSection'
import { useTabStore } from '../stores/useTabStore'
import type { PaneContent } from '../types/tab'

export function registerBuiltinModules(): void {
  // --- Modules (pane renderers) ---

  registerModule({
    id: 'new-tab',
    name: 'New Tab',
    pane: {
      kind: 'new-tab',
      component: ({ pane }) => {
        const handleSelect = (content: PaneContent) => {
          const { tabs } = useTabStore.getState()
          const tabId = Object.keys(tabs).find((id) =>
            findPane(tabs[id].layout, pane.id) !== undefined,
          )
          if (!tabId) return
          useTabStore.getState().setPaneContent(tabId, pane.id, content)
          useTabStore.getState().setActiveTab(tabId)
        }
        return <NewTabPage onSelect={handleSelect} />
      },
    },
  })

  registerModule({
    id: 'session',
    name: 'Session',
    pane: { kind: 'tmux-session', component: SessionPaneContent },
  })

  registerModule({
    id: 'dashboard',
    name: 'Dashboard',
    pane: { kind: 'dashboard', component: DashboardPage },
  })

  registerModule({
    id: 'history',
    name: 'History',
    pane: { kind: 'history', component: HistoryPage },
  })

  registerModule({
    id: 'settings',
    name: 'Settings',
    pane: { kind: 'settings', component: SettingsPage },
  })

  registerModule({
    id: 'browser',
    name: 'Browser',
    pane: {
      kind: 'browser',
      component: ({ pane }) => {
        const content = pane.content
        if (content.kind !== 'browser') return null
        return <BrowserPane paneId={pane.id} url={content.url} />
      },
    },
  })

  registerModule({
    id: 'memory-monitor',
    name: 'Memory Monitor',
    pane: { kind: 'memory-monitor', component: () => <MemoryMonitorPage /> },
  })

  registerModule({
    id: 'hosts',
    name: 'Hosts',
    pane: { kind: 'hosts', component: HostPage },
  })

  // --- Settings sections (independent registry) ---
  registerSettingsSection({ id: 'appearance', label: 'settings.section.appearance', order: 0, component: AppearanceSection })
  registerSettingsSection({ id: 'terminal', label: 'settings.section.terminal', order: 1, component: TerminalSection })
  registerSettingsSection({ id: 'agent', label: 'settings.section.agent', order: 2, component: AgentSection })
  registerSettingsSection({ id: 'workspace', label: 'settings.section.workspace', order: 10 }) // reserved
  registerSettingsSection({ id: 'sync', label: 'settings.section.sync', order: 11 }) // reserved

  // --- New-tab providers (independent registry) ---
  registerNewTabProvider({
    id: 'sessions',
    label: 'session.provider_label',
    icon: 'List',
    order: 0,
    component: SessionSection,
  })

  const caps = getPlatformCapabilities()

  registerNewTabProvider({
    id: 'browser',
    label: 'browser.provider_label',
    icon: 'Globe',
    order: -10,
    component: BrowserNewTabSection,
    disabled: !caps.canBrowserPane,
    disabledReason: 'browser.requires_app',
  })

  if (caps.canSystemTray) {
    registerSettingsSection({
      id: 'electron',
      label: 'settings.section.electron',
      order: 5,
      component: ElectronSection,
    })
  }

  if (caps.devUpdateEnabled) {
    registerSettingsSection({
      id: 'dev-environment',
      label: 'settings.section.dev_environment',
      order: 20,
      component: DevEnvironmentSection,
    })
  }
}
```

- [ ] **Step 2: Update PaneLayoutRenderer to use module-registry**

```typescript
// spa/src/components/PaneLayoutRenderer.tsx
import { getPaneRenderer } from '../lib/module-registry'
import { getLayoutKey } from '../lib/pane-tree'
import type { PaneLayout } from '../types/tab'

interface Props {
  layout: PaneLayout
  isActive: boolean
}

export function PaneLayoutRenderer({ layout, isActive }: Props) {
  if (layout.type === 'leaf') {
    const pane = getPaneRenderer(layout.pane.content.kind)
    if (!pane) {
      return (
        <div className="flex-1 flex items-center justify-center text-text-muted">
          No renderer for &quot;{layout.pane.content.kind}&quot;
        </div>
      )
    }
    const Component = pane.component
    return <Component pane={layout.pane} isActive={isActive} />
  }

  // Split — future: render split container. For now, render first child.
  return (
    <PaneLayoutRenderer
      key={getLayoutKey(layout.children[0])}
      layout={layout.children[0]}
      isActive={isActive}
    />
  )
}
```

- [ ] **Step 3: Update App.tsx import**

In `spa/src/App.tsx`, find all references to `registerBuiltinPanes` and replace with `registerBuiltinModules`. The import line needs to change from:

```typescript
// Old — find and remove this import wherever it exists
import { registerBuiltinPanes } from './lib/register-panes'
```

to:

```typescript
import { registerBuiltinModules } from './lib/register-modules'
```

Then find `registerBuiltinPanes()` call and replace with `registerBuiltinModules()`.

> **Note:** The call may be in `App.tsx` or in `main.tsx`. Search for `registerBuiltinPanes` across the codebase to find all call sites.

- [ ] **Step 4: Update all other imports of pane-registry**

Search for all files importing from `./pane-registry` or `../lib/pane-registry` and update them to import from `./module-registry` or `../lib/module-registry`. Key files to check:

- `spa/src/components/PaneLayoutRenderer.tsx` (already done in Step 2)
- Any other file importing `getPaneRenderer` or `PaneRendererProps`

The `PaneRendererProps` type is now exported from `module-registry.ts` instead of `pane-registry.ts`.

- [ ] **Step 5: Delete old files**

Delete:
- `spa/src/lib/pane-registry.ts`
- `spa/src/lib/pane-registry.test.ts`
- `spa/src/lib/register-panes.tsx`
- `spa/src/lib/register-panes.test.ts`

- [ ] **Step 6: Write smoke test for register-modules**

```typescript
// spa/src/lib/register-modules.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { clearModuleRegistry, getModules, getPaneRenderer } from './module-registry'
import { clearNewTabRegistry } from './new-tab-registry'
import { clearSettingsSectionRegistry } from './settings-section-registry'
import { registerBuiltinModules } from './register-modules'

beforeEach(() => {
  clearModuleRegistry()
  clearNewTabRegistry()
  clearSettingsSectionRegistry()
})

describe('registerBuiltinModules', () => {
  it('registers all built-in modules', () => {
    registerBuiltinModules()
    const modules = getModules()
    expect(modules.length).toBeGreaterThanOrEqual(8)
    // Spot check key modules
    expect(getPaneRenderer('tmux-session')).toBeDefined()
    expect(getPaneRenderer('new-tab')).toBeDefined()
    expect(getPaneRenderer('browser')).toBeDefined()
    expect(getPaneRenderer('hosts')).toBeDefined()
  })
})
```

- [ ] **Step 7: Run all tests**

Run: `cd spa && npx vitest run`
Expected: All PASS. No test references old pane-registry.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor: migrate pane registration to module-registry"
```

---

### Task 4: Layout Store

**Files:**
- Modify: `spa/src/lib/storage/keys.ts`
- Create: `spa/src/stores/useLayoutStore.ts`
- Create: `spa/src/stores/useLayoutStore.test.ts`

- [ ] **Step 1: Add storage key**

In `spa/src/lib/storage/keys.ts`, add `LAYOUT: 'purdex-layout'` to `STORAGE_KEYS`.

- [ ] **Step 2: Write failing tests**

```typescript
// spa/src/stores/useLayoutStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useLayoutStore } from './useLayoutStore'
import type { SidebarRegion } from '../types/tab'

beforeEach(() => {
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

describe('useLayoutStore', () => {
  describe('initial state', () => {
    it('has 4 regions with default values', () => {
      const { regions } = useLayoutStore.getState()
      const regionIds: SidebarRegion[] = [
        'primary-sidebar',
        'primary-panel',
        'secondary-panel',
        'secondary-sidebar',
      ]
      for (const id of regionIds) {
        expect(regions[id]).toBeDefined()
        expect(regions[id].views).toEqual([])
        expect(regions[id].activeViewId).toBeUndefined()
        expect(regions[id].mode).toBe('collapsed')
      }
    })

    it('sidebars default to 240px, panels to 200px', () => {
      const { regions } = useLayoutStore.getState()
      expect(regions['primary-sidebar'].width).toBe(240)
      expect(regions['secondary-sidebar'].width).toBe(240)
      expect(regions['primary-panel'].width).toBe(200)
      expect(regions['secondary-panel'].width).toBe(200)
    })
  })

  describe('setRegionMode', () => {
    it('changes mode for a region', () => {
      useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
    })
  })

  describe('setRegionWidth', () => {
    it('changes width for a region', () => {
      useLayoutStore.getState().setRegionWidth('primary-sidebar', 300)
      expect(useLayoutStore.getState().regions['primary-sidebar'].width).toBe(300)
    })

    it('enforces minimum width of 120', () => {
      useLayoutStore.getState().setRegionWidth('primary-sidebar', 50)
      expect(useLayoutStore.getState().regions['primary-sidebar'].width).toBe(120)
    })

    it('enforces maximum width of 600', () => {
      useLayoutStore.getState().setRegionWidth('primary-sidebar', 800)
      expect(useLayoutStore.getState().regions['primary-sidebar'].width).toBe(600)
    })
  })

  describe('setActiveView', () => {
    it('sets the active view for a region', () => {
      useLayoutStore.getState().setActiveView('primary-sidebar', 'session-list')
      expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBe('session-list')
    })

    it('clears active view with undefined', () => {
      useLayoutStore.getState().setActiveView('primary-sidebar', 'session-list')
      useLayoutStore.getState().setActiveView('primary-sidebar', undefined)
      expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBeUndefined()
    })
  })

  describe('setRegionViews', () => {
    it('sets the view list for a region', () => {
      useLayoutStore.getState().setRegionViews('primary-sidebar', ['session-list', 'prompts'])
      expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['session-list', 'prompts'])
    })
  })

  describe('toggleRegion', () => {
    it('cycles collapsed → pinned → collapsed', () => {
      const store = useLayoutStore.getState()
      expect(store.regions['primary-sidebar'].mode).toBe('collapsed')

      store.toggleRegion('primary-sidebar')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')

      useLayoutStore.getState().toggleRegion('primary-sidebar')
      expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('collapsed')
    })
  })
})
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/stores/useLayoutStore.test.ts`
Expected: FAIL — module not found

- [ ] **Step 4: Implement useLayoutStore**

```typescript
// spa/src/stores/useLayoutStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { SidebarRegion } from '../types/tab'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'

const MIN_WIDTH = 120
const MAX_WIDTH = 600

interface RegionState {
  views: string[]
  activeViewId?: string
  width: number
  mode: 'pinned' | 'default' | 'collapsed'
}

interface LayoutState {
  regions: Record<SidebarRegion, RegionState>

  setRegionMode: (region: SidebarRegion, mode: RegionState['mode']) => void
  setRegionWidth: (region: SidebarRegion, width: number) => void
  setActiveView: (region: SidebarRegion, viewId: string | undefined) => void
  setRegionViews: (region: SidebarRegion, views: string[]) => void
  toggleRegion: (region: SidebarRegion) => void
}

function createDefaultRegions(): Record<SidebarRegion, RegionState> {
  return {
    'primary-sidebar': { views: [], width: 240, mode: 'collapsed' },
    'primary-panel': { views: [], width: 200, mode: 'collapsed' },
    'secondary-panel': { views: [], width: 200, mode: 'collapsed' },
    'secondary-sidebar': { views: [], width: 240, mode: 'collapsed' },
  }
}

function clampWidth(w: number): number {
  return Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, w))
}

function updateRegion(
  state: LayoutState,
  region: SidebarRegion,
  patch: Partial<RegionState>,
): Partial<LayoutState> {
  return {
    regions: {
      ...state.regions,
      [region]: { ...state.regions[region], ...patch },
    },
  }
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      regions: createDefaultRegions(),

      setRegionMode: (region, mode) =>
        set((state) => updateRegion(state, region, { mode })),

      setRegionWidth: (region, width) =>
        set((state) => updateRegion(state, region, { width: clampWidth(width) })),

      setActiveView: (region, viewId) =>
        set((state) => updateRegion(state, region, { activeViewId: viewId })),

      setRegionViews: (region, views) =>
        set((state) => updateRegion(state, region, { views })),

      toggleRegion: (region) =>
        set((state) => {
          const current = state.regions[region].mode
          const next = current === 'collapsed' ? 'pinned' : 'collapsed'
          return updateRegion(state, region, { mode: next })
        }),
    }),
    {
      name: STORAGE_KEYS.LAYOUT,
      storage: purdexStorage,
      version: 1,
    },
  ),
)
```

- [ ] **Step 5: Run tests**

Run: `cd spa && npx vitest run src/stores/useLayoutStore.test.ts`
Expected: All PASS

- [ ] **Step 6: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add spa/src/lib/storage/keys.ts spa/src/stores/useLayoutStore.ts spa/src/stores/useLayoutStore.test.ts
git commit -m "feat: add useLayoutStore for 4-region sidebar/panel state"
```

---

### Task 5: Lint + full verification

- [ ] **Step 1: Run lint**

Run: `cd spa && pnpm run lint`
Expected: No errors

- [ ] **Step 2: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All PASS

- [ ] **Step 3: Run build**

Run: `cd spa && pnpm run build`
Expected: Build succeeds

- [ ] **Step 4: Fix any issues found, commit**

If lint/test/build issues found, fix them and commit:

```bash
git add -A
git commit -m "fix: resolve lint/test/build issues from module-registry migration"
```
