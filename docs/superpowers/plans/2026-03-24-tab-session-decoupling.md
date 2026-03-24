# Tab / Session 解耦 + URL Routing 重構 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decouple Tab from Session, introduce Pane as content container, migrate to path-based routing with wouter, add dashboard/history/settings tab types.

**Architecture:** Tab contains a PaneLayout tree of Panes, each Pane holds a PaneContent discriminated union. Tab store remains SOT, wouter URL is a bidirectional projection. Singleton enforcement via openSingletonTab. History store tracks browse + closed records separately.

**Tech Stack:** React 19 / Vite 8 / Zustand 5 / wouter / Vitest / Tailwind 4 / Phosphor Icons

**Spec:** `docs/superpowers/specs/2026-03-24-tab-session-decoupling-design.md`

**Branch:** `v1` (破壞式開發)

**Test command:** `cd spa && npx vitest run`

**Lint command:** `cd spa && pnpm run lint`

---

## File Map

### New Files

| File | Responsibility |
|------|----------------|
| `spa/src/lib/id.ts` | 6-char base36 ID generation with rejection sampling |
| `spa/src/lib/id.test.ts` | ID uniqueness, length, charset tests |
| `spa/src/types/tab.ts` | Tab, Pane, PaneLayout, PaneContent type definitions (rewrite) |
| `spa/src/lib/pane-tree.ts` | PaneLayout tree traversal: findPane, updatePane, getPrimaryPane, getLayoutKey |
| `spa/src/lib/pane-tree.test.ts` | Tree traversal tests with nested layouts |
| `spa/src/lib/pane-labels.ts` | getPaneLabel, getPaneIcon derivation functions |
| `spa/src/lib/pane-labels.test.ts` | Label/icon derivation for all PaneContent kinds |
| `spa/src/lib/pane-registry.ts` | Pane renderer registry (replaces tab-registry) |
| `spa/src/lib/pane-registry.test.ts` | Registry CRUD tests |
| `spa/src/stores/useTabStore.ts` | Rewritten Tab store with Pane operations (rewrite) |
| `spa/src/stores/useTabStore.test.ts` | Full store test suite (rewrite) |
| `spa/src/stores/useHistoryStore.ts` | Browse history + closed tabs + reopen |
| `spa/src/stores/useHistoryStore.test.ts` | History record/reopen/limits tests |
| `spa/src/lib/route-utils.ts` | URL parse/generate, mode validation |
| `spa/src/lib/route-utils.test.ts` | Route parsing round-trip tests |
| `spa/src/hooks/useRouteSync.ts` | wouter bidirectional Tab ↔ URL sync |
| `spa/src/components/PaneLayoutRenderer.tsx` | Recursive pane tree renderer (leaf only for now) |
| `spa/src/components/PaneLayoutRenderer.test.tsx` | Renders correct component per PaneContent kind |
| `spa/src/components/DashboardPage.tsx` | Empty dashboard placeholder |
| `spa/src/components/HistoryPage.tsx` | Browse history list with click-to-navigate |
| `spa/src/components/HistoryPage.test.tsx` | Renders records, click behavior |
| `spa/src/components/SettingsPage.tsx` | Empty settings placeholder |
| `spa/src/components/SessionPaneContent.tsx` | Refactored from SessionTabContent for Pane model |
| `spa/src/lib/register-panes.tsx` | Register all built-in pane renderers + new-tab providers |
| `spa/src/lib/new-tab-registry.ts` | NewTab Provider Registry (register/get providers) |
| `spa/src/lib/new-tab-registry.test.ts` | Registry CRUD tests |
| `spa/src/components/NewTabPage.tsx` | Content picker page — renders registered providers |
| `spa/src/components/NewTabPage.test.tsx` | Renders providers, onSelect replaces content |
| `spa/src/components/SessionSection.tsx` | New-tab provider: session list from useSessionStore |

### Modified Files

| File | Changes |
|------|---------|
| `spa/src/stores/useWorkspaceStore.ts` | Workspace.id → 6-char, remove directories/sidebarState, update createWorkspace signature |
| `spa/src/stores/useWorkspaceStore.test.ts` | Update for new Workspace shape |
| `spa/src/components/TabContent.tsx` | Use PaneLayoutRenderer instead of tab-registry, keep useTabAlivePool |
| `spa/src/components/TabContent.test.tsx` | Update for new Tab shape + pane-registry |
| `spa/src/components/TabBar.tsx` | Derive label/icon from pane content |
| `spa/src/components/TabBar.test.tsx` | Update for new Tab shape + pane-registry |
| `spa/src/components/SortableTab.tsx` | Replace getTabIcon/isDirty with pane-based derivation |
| `spa/src/components/TabContextMenu.tsx` | Replace tab.type/tab.viewMode with pane content checks |
| `spa/src/components/StatusBar.tsx` | Read from active pane instead of tab props |
| `spa/src/components/StatusBar.test.tsx` | Update for new props |
| `spa/src/components/SessionPicker.tsx` | Adapt to create tabs via new createTab factory |
| `spa/src/components/App.tsx` | wouter Router, useRouteSync, remove legacy hooks |
| `spa/src/main.tsx` | Replace registerBuiltinRenderers with registerBuiltinPanes |
| `spa/src/types/tab.test.ts` | Rewrite for new Tab/Pane types |
| `spa/src/stores/useTabStore.pin-lock.test.ts` | Update for new Tab shape, remove dismissed tests |
| `spa/package.json` | Add wouter dependency |
| `spa/vite.config.ts` | Verify SPA fallback (likely no change needed) |

### Deleted Files

| File | Reason |
|------|--------|
| `spa/src/lib/hash-routing.ts` | Replaced by wouter |
| `spa/src/hooks/useHashRouting.ts` | Replaced by useRouteSync |
| `spa/src/lib/parseHash.test.ts` | Replaced by route-utils tests |
| `spa/src/hooks/useSessionTabSync.ts` | Auto-sync removed |
| `spa/src/hooks/useSessionTabSync.test.ts` | Auto-sync removed |
| `spa/src/lib/tab-helpers.ts` | Replaced by pane-labels |
| `spa/src/lib/tab-helpers.test.ts` | Replaced by pane-labels tests |
| `spa/src/lib/tab-registry.ts` | Replaced by pane-registry |
| `spa/src/lib/tab-registry.test.ts` | Replaced by pane-registry tests |
| `spa/src/lib/register-builtins.tsx` | Replaced by register-panes |
| `spa/src/components/SessionTabContent.tsx` | Replaced by SessionPaneContent |
| `spa/src/components/SessionTabContent.test.tsx` | Replaced by SessionPaneContent tests |

---

## Task 1: ID Generation

**Files:**
- Create: `spa/src/lib/id.ts`
- Create: `spa/src/lib/id.test.ts`

- [ ] **Step 1: Write failing tests**

```ts
// spa/src/lib/id.test.ts
import { describe, it, expect } from 'vitest'
import { generateId } from './id'

describe('generateId', () => {
  it('returns a 6-character string', () => {
    const id = generateId()
    expect(id).toHaveLength(6)
  })

  it('only contains base36 characters', () => {
    for (let i = 0; i < 100; i++) {
      const id = generateId()
      expect(id).toMatch(/^[0-9a-z]{6}$/)
    }
  })

  it('generates unique IDs', () => {
    const ids = new Set(Array.from({ length: 1000 }, () => generateId()))
    expect(ids.size).toBe(1000)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/id.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement generateId**

```ts
// spa/src/lib/id.ts
export function generateId(): string {
  const chars = '0123456789abcdefghijklmnopqrstuvwxyz'
  let id = ''
  while (id.length < 6) {
    const [b] = crypto.getRandomValues(new Uint8Array(1))
    if (b < 252) id += chars[b % 36] // rejection sampling: 252 = 36*7
  }
  return id
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/id.test.ts`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/id.ts spa/src/lib/id.test.ts
git commit -m "feat: add 6-char base36 ID generator with rejection sampling"
```

---

## Task 2: Type Definitions

**Files:**
- Rewrite: `spa/src/types/tab.ts`

- [ ] **Step 1: Rewrite types/tab.ts**

```ts
// spa/src/types/tab.ts
import { generateId } from '../lib/id'

// === Tab (tab bar unit) ===
export interface Tab {
  id: string
  pinned: boolean
  locked: boolean
  createdAt: number
  layout: PaneLayout
}

// === Pane Layout (tab-internal split tree) ===
export type PaneLayout =
  | { type: 'leaf'; pane: Pane }
  | { type: 'split'; id: string; direction: 'h' | 'v'; children: PaneLayout[]; sizes: number[] }

// === Pane (content slot) ===
export interface Pane {
  id: string
  content: PaneContent
}

// === Pane Content (discriminated union) ===
export type PaneContent =
  | { kind: 'new-tab' }
  | { kind: 'session'; sessionCode: string; mode: 'terminal' | 'stream' }
  | { kind: 'dashboard' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' | { workspaceId: string } }

// === Workspace ===
export interface Workspace {
  id: string
  name: string
  color: string
  icon?: string
  tabs: string[]
  activeTabId: string | null
}

// === Factories ===
export function createTab(content: PaneContent, opts?: { pinned?: boolean }): Tab {
  return {
    id: generateId(),
    pinned: opts?.pinned ?? false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: generateId(), content } },
  }
}

export function createWorkspace(name: string, color: string): Workspace {
  return {
    id: generateId(),
    name,
    color,
    tabs: [],
    activeTabId: null,
  }
}

export function isStandaloneTab(tabId: string, workspaces: Workspace[]): boolean {
  return !workspaces.some((ws) => ws.tabs.includes(tabId))
}
```

- [ ] **Step 2: Rewrite types/tab.test.ts for new shape**

Update test file to use new `createTab(content)` factory and verify Tab/Pane/Workspace structures. Remove old tests for `createSessionTab`, `createEditorTab`.

- [ ] **Step 3: Run type tests**

Run: `cd spa && npx vitest run src/types/tab.test.ts`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/types/tab.ts spa/src/types/tab.test.ts
git commit -m "feat: rewrite Tab/Pane/PaneContent type definitions"
```

---

## Task 3: Pane Tree Utilities

**Files:**
- Create: `spa/src/lib/pane-tree.ts`
- Create: `spa/src/lib/pane-tree.test.ts`

- [ ] **Step 1: Write failing tests**

```ts
// spa/src/lib/pane-tree.test.ts
import { describe, it, expect } from 'vitest'
import { getPrimaryPane, findPane, updatePaneInLayout, getLayoutKey } from './pane-tree'
import type { PaneLayout, Pane } from '../types/tab'

const paneA: Pane = { id: 'aaaaaa', content: { kind: 'session', sessionCode: 'abc123', mode: 'terminal' } }
const paneB: Pane = { id: 'bbbbbb', content: { kind: 'dashboard' } }

const leaf: PaneLayout = { type: 'leaf', pane: paneA }
const split: PaneLayout = {
  type: 'split', id: 'ssssss', direction: 'h',
  children: [{ type: 'leaf', pane: paneA }, { type: 'leaf', pane: paneB }],
  sizes: [50, 50],
}

describe('getPrimaryPane', () => {
  it('returns pane from leaf layout', () => {
    expect(getPrimaryPane(leaf)).toBe(paneA)
  })

  it('returns first leaf pane from split layout', () => {
    expect(getPrimaryPane(split)).toBe(paneA)
  })
})

describe('findPane', () => {
  it('finds pane by id in leaf', () => {
    expect(findPane(leaf, 'aaaaaa')).toBe(paneA)
  })

  it('finds pane by id in split', () => {
    expect(findPane(split, 'bbbbbb')).toBe(paneB)
  })

  it('returns undefined for unknown id', () => {
    expect(findPane(leaf, 'zzzzzz')).toBeUndefined()
  })
})

describe('updatePaneInLayout', () => {
  it('updates pane content in leaf', () => {
    const updated = updatePaneInLayout(leaf, 'aaaaaa', { kind: 'history' })
    expect(updated.type).toBe('leaf')
    if (updated.type === 'leaf') {
      expect(updated.pane.content).toEqual({ kind: 'history' })
      expect(updated.pane.id).toBe('aaaaaa')
    }
  })

  it('updates pane content in nested split', () => {
    const updated = updatePaneInLayout(split, 'bbbbbb', { kind: 'history' })
    if (updated.type === 'split') {
      const secondChild = updated.children[1]
      if (secondChild.type === 'leaf') {
        expect(secondChild.pane.content).toEqual({ kind: 'history' })
      }
    }
  })
})

describe('getLayoutKey', () => {
  it('returns pane id for leaf', () => {
    expect(getLayoutKey(leaf)).toBe('aaaaaa')
  })

  it('returns split id for split', () => {
    expect(getLayoutKey(split)).toBe('ssssss')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/pane-tree.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement pane-tree utilities**

```ts
// spa/src/lib/pane-tree.ts
import type { Pane, PaneContent, PaneLayout } from '../types/tab'

export function getPrimaryPane(layout: PaneLayout): Pane {
  if (layout.type === 'leaf') return layout.pane
  return getPrimaryPane(layout.children[0])
}

export function findPane(layout: PaneLayout, paneId: string): Pane | undefined {
  if (layout.type === 'leaf') {
    return layout.pane.id === paneId ? layout.pane : undefined
  }
  for (const child of layout.children) {
    const found = findPane(child, paneId)
    if (found) return found
  }
  return undefined
}

export function updatePaneInLayout(
  layout: PaneLayout,
  paneId: string,
  content: PaneContent,
): PaneLayout {
  if (layout.type === 'leaf') {
    if (layout.pane.id === paneId) {
      return { type: 'leaf', pane: { ...layout.pane, content } }
    }
    return layout
  }
  return {
    ...layout,
    children: layout.children.map((child) => updatePaneInLayout(child, paneId, content)),
  }
}

export function getLayoutKey(layout: PaneLayout): string {
  return layout.type === 'leaf' ? layout.pane.id : layout.id
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/pane-tree.test.ts`
Expected: PASS (7 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/pane-tree.ts spa/src/lib/pane-tree.test.ts
git commit -m "feat: add PaneLayout tree traversal utilities"
```

---

## Task 4: Pane Labels & Icons

**Files:**
- Create: `spa/src/lib/pane-labels.ts`
- Create: `spa/src/lib/pane-labels.test.ts`

- [ ] **Step 1: Write failing tests**

```ts
// spa/src/lib/pane-labels.test.ts
import { describe, it, expect } from 'vitest'
import { getPaneLabel, getPaneIcon } from './pane-labels'
import type { PaneContent } from '../types/tab'

describe('getPaneLabel', () => {
  const mockSessionStore = {
    getByCode: (code: string) =>
      code === 'abc123' ? { name: 'dev-server' } : undefined,
  }
  const mockWorkspaceStore = {
    getById: (id: string) =>
      id === 'ws0001' ? { name: 'My Project' } : undefined,
  }

  it('returns session name for session content', () => {
    const c: PaneContent = { kind: 'session', sessionCode: 'abc123', mode: 'terminal' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore)).toBe('dev-server')
  })

  it('falls back to sessionCode if session not found', () => {
    const c: PaneContent = { kind: 'session', sessionCode: 'zzz999', mode: 'terminal' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore)).toBe('zzz999')
  })

  it('returns Dashboard for dashboard', () => {
    expect(getPaneLabel({ kind: 'dashboard' }, mockSessionStore, mockWorkspaceStore)).toBe('Dashboard')
  })

  it('returns History for history', () => {
    expect(getPaneLabel({ kind: 'history' }, mockSessionStore, mockWorkspaceStore)).toBe('History')
  })

  it('returns Settings for global settings', () => {
    const c: PaneContent = { kind: 'settings', scope: 'global' }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore)).toBe('Settings')
  })

  it('returns workspace name for workspace settings', () => {
    const c: PaneContent = { kind: 'settings', scope: { workspaceId: 'ws0001' } }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore)).toBe('Settings — My Project')
  })

  it('falls back to workspace id if not found', () => {
    const c: PaneContent = { kind: 'settings', scope: { workspaceId: 'zzzzzz' } }
    expect(getPaneLabel(c, mockSessionStore, mockWorkspaceStore)).toBe('Settings — zzzzzz')
  })
})

describe('getPaneIcon', () => {
  it('returns TerminalWindow for terminal session', () => {
    expect(getPaneIcon({ kind: 'session', sessionCode: 'x', mode: 'terminal' })).toBe('TerminalWindow')
  })

  it('returns ChatCircleDots for stream session', () => {
    expect(getPaneIcon({ kind: 'session', sessionCode: 'x', mode: 'stream' })).toBe('ChatCircleDots')
  })

  it('returns Plus for new-tab', () => {
    expect(getPaneIcon({ kind: 'new-tab' })).toBe('Plus')
  })

  it('returns House for dashboard', () => {
    expect(getPaneIcon({ kind: 'dashboard' })).toBe('House')
  })

  it('returns ClockCounterClockwise for history', () => {
    expect(getPaneIcon({ kind: 'history' })).toBe('ClockCounterClockwise')
  })

  it('returns GearSix for settings', () => {
    expect(getPaneIcon({ kind: 'settings', scope: 'global' })).toBe('GearSix')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/pane-labels.test.ts`
Expected: FAIL

- [ ] **Step 3: Implement**

```ts
// spa/src/lib/pane-labels.ts
import type { PaneContent } from '../types/tab'

interface SessionLookup {
  getByCode(code: string): { name: string } | undefined
}

interface WorkspaceLookup {
  getById(id: string): { name: string } | undefined
}

export function getPaneLabel(
  content: PaneContent,
  sessionStore: SessionLookup,
  workspaceStore: WorkspaceLookup,
): string {
  switch (content.kind) {
    case 'session': {
      const session = sessionStore.getByCode(content.sessionCode)
      return session?.name ?? content.sessionCode
    }
    case 'dashboard':
      return 'Dashboard'
    case 'history':
      return 'History'
    case 'settings':
      if (content.scope === 'global') return 'Settings'
      const ws = workspaceStore.getById(content.scope.workspaceId)
      return `Settings — ${ws?.name ?? content.scope.workspaceId}`
  }
}

export function getPaneIcon(content: PaneContent): string {
  switch (content.kind) {
    case 'session':
      return content.mode === 'terminal' ? 'TerminalWindow' : 'ChatCircleDots'
    case 'dashboard':
      return 'House'
    case 'history':
      return 'ClockCounterClockwise'
    case 'settings':
      return 'GearSix'
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/pane-labels.test.ts`
Expected: PASS (12 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/pane-labels.ts spa/src/lib/pane-labels.test.ts
git commit -m "feat: add getPaneLabel and getPaneIcon derivation functions"
```

---

## Task 5: Pane Registry

**Files:**
- Create: `spa/src/lib/pane-registry.ts`
- Create: `spa/src/lib/pane-registry.test.ts`

- [ ] **Step 1: Write failing tests**

```ts
// spa/src/lib/pane-registry.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { registerPaneRenderer, getPaneRenderer, clearPaneRegistry } from './pane-registry'

beforeEach(() => {
  clearPaneRegistry()
})

describe('pane-registry', () => {
  it('registers and retrieves a renderer', () => {
    const component = (() => null) as React.FC<any>
    registerPaneRenderer('session', { component })
    expect(getPaneRenderer('session')).toEqual({ component })
  })

  it('returns undefined for unregistered kind', () => {
    expect(getPaneRenderer('unknown')).toBeUndefined()
  })

  it('clearPaneRegistry removes all entries', () => {
    registerPaneRenderer('session', { component: (() => null) as React.FC<any> })
    clearPaneRegistry()
    expect(getPaneRenderer('session')).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/pane-registry.test.ts`
Expected: FAIL

- [ ] **Step 3: Implement**

```ts
// spa/src/lib/pane-registry.ts
import type { Pane } from '../types/tab'

export interface PaneRendererProps {
  pane: Pane
  isActive: boolean
}

export interface PaneRendererConfig {
  component: React.ComponentType<PaneRendererProps>
}

const registry = new Map<string, PaneRendererConfig>()

export function registerPaneRenderer(kind: string, config: PaneRendererConfig): void {
  registry.set(kind, config)
}

export function getPaneRenderer(kind: string): PaneRendererConfig | undefined {
  return registry.get(kind)
}

export function clearPaneRegistry(): void {
  registry.clear()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/pane-registry.test.ts`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/pane-registry.ts spa/src/lib/pane-registry.test.ts
git commit -m "feat: add pane renderer registry"
```

---

## Task 6: useTabStore Rewrite

**Files:**
- Rewrite: `spa/src/stores/useTabStore.ts`
- Rewrite: `spa/src/stores/useTabStore.test.ts`

- [ ] **Step 1: Write failing tests for core tab operations**

Test file: `spa/src/stores/useTabStore.test.ts`

Cover: addTab, closeTab (with locked guard), setActiveTab, reorderTabs, togglePin, toggleLock, openSingletonTab, setViewMode (with paneId).

Key test cases:
- `createTab` adds tab to tabs + tabOrder
- `closeTab` removes from tabs + tabOrder
- `closeTab` on locked tab does nothing
- `setActiveTab` updates activeTabId
- `openSingletonTab` returns existing tab if content matches
- `openSingletonTab` creates new tab if no match
- `setViewMode` updates mode on correct pane
- `togglePin` / `toggleLock` toggle booleans
- `reorderTabs` updates tabOrder

Reference existing test patterns: `beforeEach` reset state, `useTabStore.getState()` for actions.

- [ ] **Step 2: Run test to verify they fail**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`
Expected: FAIL — old store shape doesn't match new tests

- [ ] **Step 3: Implement useTabStore**

Rewrite `spa/src/stores/useTabStore.ts` with:
- New `TabState` interface per spec (section 3)
- `addTab(tab: Tab)` — accepts a pre-built Tab object (from `createTab()` factory in types/tab.ts), adds to tabs + tabOrder. **Note:** factory `createTab(content)` in types/tab.ts builds the Tab object; store `addTab(tab)` inserts it. No naming conflict.
- `openSingletonTab(content)` — scan all tabs' pane trees for matching content (use `getPrimaryPane` + deep-compare by `kind` + relevant fields). Found → `setActiveTab`. Not found → `addTab(createTab(content))` + `setActiveTab`.
- `closeTab(id)` — if `tab.locked` return early. Remove from tabs + tabOrder. If it was activeTabId, activate adjacent tab (next in tabOrder, or previous, or null).
- `setActiveTab(id)` — set activeTabId
- `setViewMode(tabId, paneId, mode)` — use `updatePaneInLayout` from pane-tree to update the pane's content mode
- `splitPane` / `closePane` — stub (throw 'not implemented' or no-op)
- `reorderTabs(newOrder)` — replace tabOrder
- `togglePin(id)` / `toggleLock(id)` — toggle booleans
- Persist with key `tbox-v2-tabs`, version 1, no migration needed (破壞式)

Also update `spa/src/stores/useTabStore.pin-lock.test.ts` — remove `dismissedSessions`/`dismissTab` tests, use new `createTab()` factory for test fixtures.

- [ ] **Step 4: Run test to verify they pass**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.test.ts
git commit -m "feat: rewrite useTabStore for Tab+Pane model"
```

---

## Task 7: useWorkspaceStore Adjustment

**Files:**
- Modify: `spa/src/stores/useWorkspaceStore.ts`
- Modify: `spa/src/stores/useWorkspaceStore.test.ts`

- [ ] **Step 1: Update tests for new Workspace shape**

Update test file to:
- Use `createWorkspace()` from types/tab (new factory)
- Remove references to `directories`, `sidebarState`
- Use 6-char IDs
- Update `beforeEach` reset to match new shape

- [ ] **Step 2: Run test to verify they fail**

Run: `cd spa && npx vitest run src/stores/useWorkspaceStore.test.ts`
Expected: FAIL

- [ ] **Step 3: Update useWorkspaceStore**

- Remove `directories`, `sidebarState` from Workspace usage
- Use `generateId()` for workspace IDs
- Change persist key to `tbox-v2-workspaces`, version 1
- Keep all existing actions (addWorkspace, removeWorkspace, addTabToWorkspace, etc.)

- [ ] **Step 4: Run test to verify they pass**

Run: `cd spa && npx vitest run src/stores/useWorkspaceStore.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useWorkspaceStore.ts spa/src/stores/useWorkspaceStore.test.ts
git commit -m "feat: update useWorkspaceStore for new Workspace shape"
```

---

## Task 8: useHistoryStore

**Files:**
- Create: `spa/src/stores/useHistoryStore.ts`
- Create: `spa/src/stores/useHistoryStore.test.ts`

- [ ] **Step 1: Write failing tests**

Cover:
- `recordVisit` adds BrowseRecord, respects 500 limit
- `recordClose` adds ClosedTabRecord
- `reopenLast` returns most recent unreopened tab, sets reopenedAt
- `reopenLast` returns null when no unreopened records
- `clearBrowseHistory` / `clearClosedTabs`
- 100 limit on closedTabs (oldest dropped)

- [ ] **Step 2: Run test to verify they fail**

Run: `cd spa && npx vitest run src/stores/useHistoryStore.test.ts`
Expected: FAIL

- [ ] **Step 3: Implement useHistoryStore**

Per spec section 3: BrowseRecord, ClosedTabRecord interfaces. Persist with key `tbox-v2-history`.

- [ ] **Step 4: Run test to verify they pass**

Run: `cd spa && npx vitest run src/stores/useHistoryStore.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useHistoryStore.ts spa/src/stores/useHistoryStore.test.ts
git commit -m "feat: add useHistoryStore for browse + closed tab records"
```

---

## Task 9: Route Utilities

**Files:**
- Create: `spa/src/lib/route-utils.ts`
- Create: `spa/src/lib/route-utils.test.ts`

- [ ] **Step 1: Write failing tests**

```ts
// spa/src/lib/route-utils.test.ts
import { describe, it, expect } from 'vitest'
import { parseRoute, tabToUrl } from './route-utils'

describe('parseRoute', () => {
  it('parses / as dashboard', () => {
    expect(parseRoute('/')).toEqual({ kind: 'dashboard' })
  })

  it('parses /history', () => {
    expect(parseRoute('/history')).toEqual({ kind: 'history' })
  })

  it('parses /settings', () => {
    expect(parseRoute('/settings')).toEqual({ kind: 'settings', scope: 'global' })
  })

  it('parses /t/:tabId/:mode', () => {
    expect(parseRoute('/t/abc123/terminal')).toEqual({
      kind: 'session-tab', tabId: 'abc123', mode: 'terminal',
    })
  })

  it('invalid mode falls back to terminal', () => {
    expect(parseRoute('/t/abc123/invalid')).toEqual({
      kind: 'session-tab', tabId: 'abc123', mode: 'terminal',
    })
  })

  it('parses /w/:workspaceId', () => {
    expect(parseRoute('/w/ws0001')).toEqual({
      kind: 'workspace', workspaceId: 'ws0001',
    })
  })

  it('parses /w/:workspaceId/settings', () => {
    expect(parseRoute('/w/ws0001/settings')).toEqual({
      kind: 'workspace-settings', workspaceId: 'ws0001',
    })
  })

  it('parses /w/:workspaceId/t/:tabId/:mode', () => {
    expect(parseRoute('/w/ws0001/t/abc123/stream')).toEqual({
      kind: 'workspace-session-tab', workspaceId: 'ws0001', tabId: 'abc123', mode: 'stream',
    })
  })

  it('returns null for unknown routes', () => {
    expect(parseRoute('/unknown/path')).toBeNull()
  })
})

describe('tabToUrl', () => {
  it('generates session tab URL', () => {
    expect(tabToUrl('abc123', { kind: 'session', sessionCode: 'x', mode: 'terminal' }))
      .toBe('/t/abc123/terminal')
  })

  it('generates dashboard URL', () => {
    expect(tabToUrl('abc123', { kind: 'dashboard' })).toBe('/')
  })

  it('generates history URL', () => {
    expect(tabToUrl('abc123', { kind: 'history' })).toBe('/history')
  })

  it('generates global settings URL', () => {
    expect(tabToUrl('abc123', { kind: 'settings', scope: 'global' })).toBe('/settings')
  })

  it('generates workspace settings URL', () => {
    expect(tabToUrl('abc123', { kind: 'settings', scope: { workspaceId: 'ws0001' } }))
      .toBe('/w/ws0001/settings')
  })

  it('generates session tab URL within workspace', () => {
    expect(tabToUrl('abc123', { kind: 'session', sessionCode: 'x', mode: 'terminal' }, 'ws0001'))
      .toBe('/w/ws0001/t/abc123/terminal')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/route-utils.test.ts`
Expected: FAIL

- [ ] **Step 3: Implement**

```ts
// spa/src/lib/route-utils.ts
import type { PaneContent } from '../types/tab'

export type ParsedRoute =
  | { kind: 'dashboard' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' }
  | { kind: 'session-tab'; tabId: string; mode: 'terminal' | 'stream' }
  | { kind: 'workspace'; workspaceId: string }
  | { kind: 'workspace-settings'; workspaceId: string }
  | { kind: 'workspace-session-tab'; workspaceId: string; tabId: string; mode: 'terminal' | 'stream' }

function validateMode(mode: string): 'terminal' | 'stream' {
  return mode === 'stream' ? 'stream' : 'terminal'
}

export function parseRoute(path: string): ParsedRoute | null {
  if (path === '/') return { kind: 'dashboard' }
  if (path === '/history') return { kind: 'history' }
  if (path === '/settings') return { kind: 'settings', scope: 'global' }

  const segments = path.split('/').filter(Boolean)

  if (segments[0] === 't' && segments.length === 3) {
    return { kind: 'session-tab', tabId: segments[1], mode: validateMode(segments[2]) }
  }

  if (segments[0] === 'w' && segments.length === 2) {
    return { kind: 'workspace', workspaceId: segments[1] }
  }

  if (segments[0] === 'w' && segments[1] && segments[2] === 'settings' && segments.length === 3) {
    return { kind: 'workspace-settings', workspaceId: segments[1] }
  }

  if (segments[0] === 'w' && segments[2] === 't' && segments.length === 5) {
    return {
      kind: 'workspace-session-tab',
      workspaceId: segments[1],
      tabId: segments[3],
      mode: validateMode(segments[4]),
    }
  }

  return null
}

export function tabToUrl(tabId: string, content: PaneContent, workspaceId?: string): string {
  switch (content.kind) {
    case 'dashboard': return '/'
    case 'history': return '/history'
    case 'settings':
      if (content.scope === 'global') return '/settings'
      return `/w/${content.scope.workspaceId}/settings`
    case 'session':
      if (workspaceId) return `/w/${workspaceId}/t/${tabId}/${content.mode}`
      return `/t/${tabId}/${content.mode}`
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/route-utils.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/route-utils.ts spa/src/lib/route-utils.test.ts
git commit -m "feat: add route-utils for URL parsing and generation"
```

---

## Task 10: Install wouter + Route Sync Hook

**Files:**
- Modify: `spa/package.json` (pnpm add wouter)
- Create: `spa/src/hooks/useRouteSync.ts`

- [ ] **Step 1: Install wouter**

```bash
cd spa && pnpm add wouter
```

- [ ] **Step 2: Implement useRouteSync**

```ts
// spa/src/hooks/useRouteSync.ts
import { useEffect, useRef } from 'react'
import { useLocation } from 'wouter'
import { useTabStore } from '../stores/useTabStore'
import { parseRoute, tabToUrl } from '../lib/route-utils'
import { getPrimaryPane } from '../lib/pane-tree'

export function useRouteSync() {
  const [location, setLocation] = useLocation()
  const tabs = useTabStore((s) => s.tabs)
  const activeTabId = useTabStore((s) => s.activeTabId)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const openSingletonTab = useTabStore((s) => s.openSingletonTab)
  const suppressSync = useRef(false)

  // Tab → URL: when activeTab changes, update URL
  useEffect(() => {
    if (suppressSync.current) {
      suppressSync.current = false
      return
    }
    if (!activeTabId) return // stay on current URL, don't redirect
    const tab = tabs[activeTabId]
    if (!tab) return
    const content = getPrimaryPane(tab.layout).content
    const url = tabToUrl(activeTabId, content)
    if (location !== url) setLocation(url, { replace: true })
  }, [activeTabId])

  // URL → Tab: when URL changes (back/forward/direct), find or create tab
  useEffect(() => {
    const parsed = parseRoute(location)
    if (!parsed) return

    suppressSync.current = true

    switch (parsed.kind) {
      case 'dashboard':
        openSingletonTab({ kind: 'dashboard' })
        break
      case 'history':
        openSingletonTab({ kind: 'history' })
        break
      case 'settings':
        openSingletonTab({ kind: 'settings', scope: 'global' })
        break
      case 'session-tab': {
        // tabId from URL → lookup in tab store
        const tab = tabs[parsed.tabId]
        if (tab) {
          setActiveTab(parsed.tabId)
        }
        // else: tab not found → stay on URL, content area shows new-tab page
        break
      }
      case 'workspace':
        // Activate workspace → its activeTab (handled by App)
        break
      case 'workspace-settings':
        openSingletonTab({ kind: 'settings', scope: { workspaceId: parsed.workspaceId } })
        break
      case 'workspace-session-tab': {
        const tab = tabs[parsed.tabId]
        if (tab) {
          setActiveTab(parsed.tabId)
        }
        // else: tab not found → stay on URL, content area shows new-tab page
        break
      }
    }
  }, [location])
}
```

**Key design decisions:**
- Session tab URL (`/t/:tabId/:mode`) uses tabId, which requires persist. If tab not found → stay on URL, content area shows empty state (no redirect).
- Singleton routes are fully self-sufficient — always create or activate.
- `SessionPaneContent` gets `wsBase`/`daemonBase` from `useHostStore()` internally (not via props). `PaneRendererProps` only passes `{ pane, isActive }`.
- No `JSON.stringify` for content matching — session tabs use direct tabId lookup.

- [ ] **Step 3: Commit**

```bash
git add spa/package.json spa/pnpm-lock.yaml spa/src/hooks/useRouteSync.ts
git commit -m "feat: install wouter and add useRouteSync hook"
```

---

## Task 11: PaneLayoutRenderer + Stub Pages + Register Panes

**Files:**
- Create: `spa/src/components/PaneLayoutRenderer.tsx`
- Create: `spa/src/components/PaneLayoutRenderer.test.tsx`
- Create: `spa/src/components/DashboardPage.tsx`
- Create: `spa/src/components/HistoryPage.tsx`
- Create: `spa/src/components/HistoryPage.test.tsx`
- Create: `spa/src/components/SettingsPage.tsx`
- Create: `spa/src/components/SessionPaneContent.tsx`
- Create: `spa/src/lib/register-panes.tsx`

- [ ] **Step 1: Write PaneLayoutRenderer test**

```tsx
// spa/src/components/PaneLayoutRenderer.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { PaneLayoutRenderer } from './PaneLayoutRenderer'
import { registerPaneRenderer, clearPaneRegistry } from '../lib/pane-registry'
import type { PaneLayout } from '../types/tab'

beforeEach(() => {
  cleanup()
  clearPaneRegistry()
  registerPaneRenderer('dashboard', {
    component: () => <div>Dashboard Content</div>,
  })
  registerPaneRenderer('session', {
    component: ({ pane }) => <div>Session {pane.content.kind === 'session' ? pane.content.sessionCode : ''}</div>,
  })
})

describe('PaneLayoutRenderer', () => {
  it('renders leaf pane with correct renderer', () => {
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'aaaaaa', content: { kind: 'dashboard' } },
    }
    render(<PaneLayoutRenderer layout={layout} isActive={true} />)
    expect(screen.getByText('Dashboard Content')).toBeTruthy()
  })

  it('renders unknown kind gracefully', () => {
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'aaaaaa', content: { kind: 'history' } },
    }
    render(<PaneLayoutRenderer layout={layout} isActive={true} />)
    // No renderer registered for 'history' → should show fallback
    expect(screen.getByText(/no renderer/i)).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/PaneLayoutRenderer.test.tsx`
Expected: FAIL

- [ ] **Step 3: Implement PaneLayoutRenderer**

```tsx
// spa/src/components/PaneLayoutRenderer.tsx
import { getPaneRenderer } from '../lib/pane-registry'
import { getLayoutKey } from '../lib/pane-tree'
import type { PaneLayout } from '../types/tab'

interface Props {
  layout: PaneLayout
  isActive: boolean
}

export function PaneLayoutRenderer({ layout, isActive }: Props) {
  if (layout.type === 'leaf') {
    const config = getPaneRenderer(layout.pane.content.kind)
    if (!config) return <div className="flex-1 flex items-center justify-center text-gray-500">No renderer for "{layout.pane.content.kind}"</div>
    const Component = config.component
    return <Component pane={layout.pane} isActive={isActive} />
  }

  // Split — reserved for future, render first child only for now
  return <PaneLayoutRenderer layout={layout.children[0]} isActive={isActive} />
}
```

- [ ] **Step 4: Create NewTab Provider Registry**

```ts
// spa/src/lib/new-tab-registry.ts
export interface NewTabProviderProps {
  onSelect: (content: PaneContent) => void
}

export interface NewTabProvider {
  id: string
  label: string
  icon: string
  order: number
  component: React.ComponentType<NewTabProviderProps>
}

const providers: NewTabProvider[] = []

export function registerNewTabProvider(provider: NewTabProvider): void {
  providers.push(provider)
  providers.sort((a, b) => a.order - b.order)
}

export function getNewTabProviders(): NewTabProvider[] {
  return [...providers]
}

export function clearNewTabRegistry(): void {
  providers.length = 0
}
```

- [ ] **Step 5: Create NewTabPage + SessionSection**

NewTabPage.tsx — renders all registered providers. Each provider gets `onSelect` callback that replaces the current pane's content via `useTabStore.getState().setPaneContent(tabId, paneId, content)`.

SessionSection.tsx — reads `useSessionStore` and renders session list. Click → `onSelect({ kind: 'session', sessionCode, mode: 'terminal' })`.

- [ ] **Step 6: Create stub pages + SessionPaneContent**

DashboardPage.tsx — empty placeholder with centered text.
SettingsPage.tsx — empty placeholder with centered text.
HistoryPage.tsx — reads useHistoryStore.browseHistory and renders list. Click on entry → if tab still open, setActiveTab; if closed, create new tab from paneContent.
SessionPaneContent.tsx — refactor from existing SessionTabContent, adapting props from `{ tab }` to `{ pane }`. Keep TerminalView and ConversationView rendering logic. Gets `wsBase`/`daemonBase` from `useHostStore()` internally. Reference existing `spa/src/components/SessionTabContent.tsx`.
register-panes.tsx — register all 5 pane renderers (new-tab, session, dashboard, history, settings) + register sessions new-tab provider.

- [ ] **Step 5: Run PaneLayoutRenderer test**

Run: `cd spa && npx vitest run src/components/PaneLayoutRenderer.test.tsx`
Expected: PASS

- [ ] **Step 6: Write and run HistoryPage test**

Test: render with mock browse records, verify list renders, verify click behavior.

Run: `cd spa && npx vitest run src/components/HistoryPage.test.tsx`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add spa/src/components/PaneLayoutRenderer.tsx spa/src/components/PaneLayoutRenderer.test.tsx \
  spa/src/components/DashboardPage.tsx spa/src/components/HistoryPage.tsx spa/src/components/HistoryPage.test.tsx \
  spa/src/components/SettingsPage.tsx spa/src/components/SessionPaneContent.tsx spa/src/lib/register-panes.tsx
git commit -m "feat: add PaneLayoutRenderer, stub pages, SessionPaneContent, register-panes"
```

---

## Task 12: TabContent Refactor

**Files:**
- Modify: `spa/src/components/TabContent.tsx`

- [ ] **Step 1: Refactor TabContent**

Replace tab-registry lookup with PaneLayoutRenderer. Keep useTabAlivePool pattern. The key change: instead of `getTabRenderer(tab.type)`, render `<PaneLayoutRenderer layout={tab.layout} />`.

- [ ] **Step 2: Run tests**

Run: `cd spa && npx vitest run`
Check which tests still fail.

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/TabContent.tsx
git commit -m "refactor: TabContent uses PaneLayoutRenderer"
```

---

## Task 13: TabBar + SortableTab + TabContextMenu Adaptation

**Files:**
- Modify: `spa/src/components/TabBar.tsx`
- Modify: `spa/src/components/SortableTab.tsx`
- Modify: `spa/src/components/TabContextMenu.tsx`
- Update: `spa/src/components/TabBar.test.tsx`

- [ ] **Step 1: Update SortableTab**

SortableTab currently uses `getTabIcon(tab)` from tab-registry and `isDirty(tab)` from tab-helpers. Change to:
- Import `getPaneIcon` from pane-labels, `getPrimaryPane` from pane-tree
- Derive icon: `getPaneIcon(getPrimaryPane(tab.layout).content)`
- Remove `isDirty` (editor tab not implemented yet)

- [ ] **Step 2: Update TabContextMenu**

Currently checks `tab.type === 'session'` and reads `tab.viewMode`. Change to:
- Get primary pane: `const primary = getPrimaryPane(tab.layout)`
- Check kind: `primary.content.kind === 'session'`
- Read mode: `primary.content.kind === 'session' ? primary.content.mode : undefined`

- [ ] **Step 3: Update TabBar**

TabBar currently reads `tab.label`. Change to derive label from pane content using `getPaneLabel`. Pass new Tab shape to SortableTab.

- [ ] **Step 4: Update TabBar tests**

Use new Tab shape (with layout) in test fixtures. Update mock tabs to use `createTab()` factory. Replace `registerTabRenderer`/`clearRegistry` with `registerPaneRenderer`/`clearPaneRegistry`.

- [ ] **Step 5: Run tests**

Run: `cd spa && npx vitest run src/components/TabBar.test.tsx`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/TabBar.tsx spa/src/components/SortableTab.tsx \
  spa/src/components/TabContextMenu.tsx spa/src/components/TabBar.test.tsx
git commit -m "refactor: TabBar/SortableTab/TabContextMenu use pane content"
```

---

## Task 14: StatusBar Adaptation

**Files:**
- Modify: `spa/src/components/StatusBar.tsx`
- Update: `spa/src/components/StatusBar.test.tsx`

- [ ] **Step 1: Update StatusBar**

StatusBar now receives the active tab's primary pane content instead of individual props. Derive host, session name, status, viewMode from PaneContent. For non-session content (dashboard, history, settings), show simplified status.

- [ ] **Step 2: Update StatusBar tests**

- [ ] **Step 3: Run tests**

Run: `cd spa && npx vitest run src/components/StatusBar.test.tsx`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/StatusBar.tsx spa/src/components/StatusBar.test.tsx
git commit -m "refactor: StatusBar reads from active pane content"
```

---

## Task 15: App.tsx Rewrite

**Files:**
- Modify: `spa/src/components/App.tsx`

- [ ] **Step 1: Rewrite App.tsx**

Major changes:
1. Wrap with wouter `<Router>` (uses browser history by default)
2. Call `registerBuiltinPanes()` on mount (from register-panes.tsx)
3. Call `useRouteSync()` for bidirectional URL sync
4. Remove `useHashRouting()` call
5. Remove `useSessionTabSync()` call
6. Remove `SessionPicker` modal — users add sessions from Sessions sidebar panel (future) or direct URL
7. ActivityBar settings button → navigate to `/settings`
8. Add tab button → `addTab(createTab({ kind: 'new-tab' }))` — opens new-tab content picker page
9. Keep workspace switching logic
10. Keep tab event handlers (close, reorder, pin, lock, context menu)
11. Update all handlers to work with new Tab shape

- [ ] **Step 2: Run full test suite**

Run: `cd spa && npx vitest run`
Fix remaining failures.

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/App.tsx
git commit -m "refactor: App.tsx with wouter Router, useRouteSync, remove legacy sync"
```

---

## Task 16: Delete Old Files + Update main.tsx

**Files:**
- Delete: `spa/src/lib/hash-routing.ts`, `spa/src/hooks/useHashRouting.ts`, `spa/src/lib/parseHash.test.ts`
- Delete: `spa/src/hooks/useSessionTabSync.ts`, `spa/src/hooks/useSessionTabSync.test.ts`
- Delete: `spa/src/lib/tab-helpers.ts`, `spa/src/lib/tab-helpers.test.ts`
- Delete: `spa/src/lib/tab-registry.ts`, `spa/src/lib/tab-registry.test.ts`
- Delete: `spa/src/lib/register-builtins.tsx`
- Delete: `spa/src/components/SessionTabContent.tsx`, `spa/src/components/SessionTabContent.test.tsx`
- Modify: `spa/src/main.tsx`

- [ ] **Step 1: Delete all old files**

```bash
cd spa && rm -f \
  src/lib/hash-routing.ts src/hooks/useHashRouting.ts src/lib/parseHash.test.ts \
  src/hooks/useSessionTabSync.ts src/hooks/useSessionTabSync.test.ts \
  src/lib/tab-helpers.ts src/lib/tab-helpers.test.ts \
  src/lib/tab-registry.ts src/lib/tab-registry.test.ts \
  src/lib/register-builtins.tsx \
  src/components/SessionTabContent.tsx src/components/SessionTabContent.test.tsx
```

- [ ] **Step 2: Update main.tsx**

Replace `import { registerBuiltinRenderers } from './lib/register-builtins'` with `import { registerBuiltinPanes } from './lib/register-panes'`. Update the call.

- [ ] **Step 3: Commit**

```bash
git add -A spa/src/
git commit -m "chore: remove legacy files, update main.tsx to register-panes"
```

---

## Task 17: Keybinding ⌘+Shift+T

- [ ] **Step 1: Add keydown listener in App.tsx**

In App.tsx, add `useEffect` that listens for `keydown`:
- `(e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'T'` → call `useHistoryStore.getState().reopenLast()`
- If `reopenLast()` returns a Tab, call `tabStore.addTab(tab)` + `tabStore.setActiveTab(tab.id)`

- [ ] **Step 2: Commit**

```bash
git add spa/src/components/App.tsx
git commit -m "feat: add Cmd+Shift+T keybinding for reopen last closed tab"
```

---

## Task 18: Final Cleanup + Full Test Pass

- [ ] **Step 1: Run full test suite**

Run: `cd spa && npx vitest run`
Fix any remaining failures. Update any test files that reference old Tab shape or deleted imports.

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Fix any lint errors.

- [ ] **Step 3: Run build**

Run: `cd spa && pnpm run build`
Verify no TypeScript errors.

- [ ] **Step 4: Manual smoke test**

1. Start daemon: `bin/tbox`
2. Start SPA: `cd spa && pnpm dev`
3. Open `http://100.64.0.2:5174/`
4. Verify dashboard page loads
5. Navigate to `/history` → history page
6. Navigate to `/settings` → settings page
7. Open a session tab from the add-tab UI
8. Verify terminal renders in the tab
9. Switch viewMode to stream
10. Verify URL updates to `/t/{tabId}/stream`
11. Close tab → reopen with ⌘+Shift+T
12. Verify tab bar shows correct labels and icons
13. Browser back/forward navigation works

- [ ] **Step 5: Commit any final fixes**

```bash
git add -A spa/
git commit -m "fix: final cleanup and test fixes for tab-session decoupling"
```

---

## Summary

| Task | Description | Steps |
|------|-------------|-------|
| 1 | ID Generation | 5 |
| 2 | Type Definitions | 4 |
| 3 | Pane Tree Utilities | 5 |
| 4 | Pane Labels & Icons | 5 |
| 5 | Pane Registry | 5 |
| 6 | useTabStore Rewrite | 5 |
| 7 | useWorkspaceStore Adjustment | 5 |
| 8 | useHistoryStore | 5 |
| 9 | Route Utilities | 5 |
| 10 | Install wouter + Route Sync | 3 |
| 11 | PaneLayoutRenderer + Pages | 7 |
| 12 | TabContent Refactor | 3 |
| 13 | TabBar + SortableTab + TabContextMenu | 6 |
| 14 | StatusBar Adaptation | 4 |
| 15 | App.tsx Rewrite | 3 |
| 16 | Delete Old Files + main.tsx | 3 |
| 17 | Keybinding ⌘+Shift+T | 2 |
| 18 | Final Cleanup + Full Test Pass | 5 |
| **Total** | | **85 steps** |

## Key Design Notes

- **Factory vs Store action naming**: `createTab(content)` in `types/tab.ts` builds a Tab object. `addTab(tab)` in store inserts it. No naming conflict.
- **SessionPaneContent** gets `wsBase`/`daemonBase` from `useHostStore()` internally, not via props.
- **useTabAlivePool** is preserved in TabContent — keeps pinned + active tabs mounted.
- **New tab flow**: `[+]` button creates a tab with `{ kind: 'new-tab' }`, which renders NewTabPage (content picker). User selects from registered providers → pane content replaced. No pre-connected session.
- **NewTab Provider Registry**: content types register sections into the new-tab page via `registerNewTabProvider()`. Built-in: sessions list. Future: recent files, recent closed, etc.
- **Unresolvable URLs**: session tab URLs that can't find their tabId in store → stay on URL, content area shows NewTabPage as fallback.
