# Sidebar / Panel Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users manage which views appear in each sidebar/panel region, with expand/collapse/hide controls and drag-to-reorder.

**Architecture:** Extend the existing layout store with view management actions (add/remove/reorder). Add a RegionManager component that replaces region content for management mode. Add context menu for quick view toggling. Fix toggleVisibility to remember previous mode.

**Tech Stack:** React 19, Zustand 5, @dnd-kit (already in project for TabBar), Phosphor Icons, Vitest + React Testing Library

**Spec:** `docs/superpowers/specs/2026-04-10-sidebar-panel-management-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `spa/src/lib/module-registry.ts` | Remove `defaultRegion` from ViewDefinition, add `getAllViews()`, remove `getViewsByRegion()` |
| Modify | `spa/src/lib/module-registry.test.ts` | Update tests for API changes |
| Modify | `spa/src/lib/register-modules.tsx` | Remove `defaultRegion` from files module views, change scope |
| Modify | `spa/src/stores/useLayoutStore.ts` | Add `previousMode`, fix `toggleVisibility`, add `addView`/`removeView`/`reorderViews` |
| Modify | `spa/src/stores/useLayoutStore.test.ts` | Tests for new actions and fixed toggleVisibility |
| Modify | `spa/src/components/SidebarRegion.tsx` | Empty state rendering, management mode toggle, ⚙/+ buttons, context menu, pass tabId |
| Modify | `spa/src/components/SidebarRegion.test.tsx` | Tests for new UI states |
| Create | `spa/src/components/RegionManager.tsx` | Management panel with enabled/available lists and drag reorder |
| Create | `spa/src/components/RegionManager.test.tsx` | Tests for RegionManager |
| Create | `spa/src/components/RegionContextMenu.tsx` | Right-click context menu for quick view toggling |
| Create | `spa/src/components/RegionContextMenu.test.tsx` | Tests for context menu |
| Modify | `spa/src/components/TitleBar.tsx` | Remove `views.length > 0` filter from visibleToggles |
| Modify | `spa/src/components/TitleBar.test.tsx` | Update toggle visibility tests |
| Modify | `spa/src/main.tsx` | Change initialization to check all regions |
| Modify | `spa/src/types/tab.ts` | Remove `WorkspaceSidebarState`, remove `sidebarState` from Workspace |
| Modify | `spa/src/types/tab.test.ts` | Remove WorkspaceSidebarState tests |

---

### Task 1: ViewDefinition — remove defaultRegion, add tab scope

**Files:**
- Modify: `spa/src/lib/module-registry.ts:26-33` (ViewDefinition interface)
- Modify: `spa/src/lib/module-registry.ts:19-24` (ViewProps interface)
- Modify: `spa/src/lib/module-registry.ts:90-104` (remove getViewsByRegion)
- Modify: `spa/src/lib/module-registry.test.ts`
- Modify: `spa/src/lib/register-modules.tsx:101-118` (files module views)

- [ ] **Step 1: Update ViewDefinition and ViewProps**

In `spa/src/lib/module-registry.ts`, update the interfaces:

```typescript
export interface ViewProps {
  hostId?: string
  workspaceId?: string
  tabId?: string
  isActive: boolean
  region?: SidebarRegion
}

export interface ViewDefinition {
  id: string
  label: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  scope: 'system' | 'workspace' | 'tab'
  component: React.ComponentType<ViewProps>
}
```

Remove the `SidebarRegion` import from `'../types/tab'` if it's only used by `getViewsByRegion` and `ViewDefinition.defaultRegion`. Check: `ViewProps.region` still uses `SidebarRegion`, so keep the import.

- [ ] **Step 2: Replace getViewsByRegion with getAllViews**

In `spa/src/lib/module-registry.ts`, remove `getViewsByRegion` (lines 90-104) and add:

```typescript
export function getAllViews(): ViewDefinition[] {
  return [...modules.values()].flatMap((m) => m.views ?? [])
}
```

- [ ] **Step 3: Update register-modules.tsx**

In `spa/src/lib/register-modules.tsx`, update the files module (lines 101-118):

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
    ],
```

- [ ] **Step 4: Update module-registry tests**

In `spa/src/lib/module-registry.test.ts`:

1. Remove `defaultRegion` from all test module view registrations
2. Remove the `getViewsByRegion` describe block and its tests
3. Remove `getViewsByRegion` from the import
4. Add `getAllViews` to the import
5. Add test for `getAllViews`

Also update `spa/src/components/SidebarRegion.test.tsx`: remove `defaultRegion` from the test module helper (`registerTestModule` or inline `registerModule` calls) to match the new `ViewDefinition` interface.

```typescript
describe('getAllViews', () => {
  it('returns all views from all modules', () => {
    registerModule({
      id: 'mod-a',
      name: 'A',
      views: [
        { id: 'view-1', label: 'V1', icon: DummyIcon, scope: 'system', component: DummyView },
      ],
    })
    registerModule({
      id: 'mod-b',
      name: 'B',
      views: [
        { id: 'view-2', label: 'V2', icon: DummyIcon, scope: 'workspace', component: DummyView },
        { id: 'view-3', label: 'V3', icon: DummyIcon, scope: 'tab', component: DummyView },
      ],
    })
    registerModule({ id: 'mod-c', name: 'C' }) // no views
    const views = getAllViews()
    expect(views).toHaveLength(3)
    expect(views.map((v) => v.id)).toEqual(['view-1', 'view-2', 'view-3'])
  })

  it('returns empty array when no modules have views', () => {
    registerModule({ id: 'mod-x', name: 'X' })
    expect(getAllViews()).toEqual([])
  })
})
```

- [ ] **Step 5: Run tests and verify**

Run: `cd spa && npx vitest run src/lib/module-registry.test.ts`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/module-registry.ts spa/src/lib/module-registry.test.ts spa/src/lib/register-modules.tsx
git commit -m "refactor: remove defaultRegion from ViewDefinition, add getAllViews and tab scope"
```

---

### Task 2: Layout Store — previousMode + toggleVisibility fix

**Files:**
- Modify: `spa/src/stores/useLayoutStore.ts:9-14,77-82`
- Modify: `spa/src/stores/useLayoutStore.test.ts`

- [ ] **Step 1: Write failing tests for previousMode behavior**

In `spa/src/stores/useLayoutStore.test.ts`, **delete the entire existing `toggleVisibility` describe block** (lines 93-113, including "toggles between hidden and pinned" and "hides a pinned region" tests). Replace with:

```typescript
describe('toggleVisibility', () => {
  it('hides a pinned region and remembers previousMode', () => {
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
    useLayoutStore.getState().toggleVisibility('primary-sidebar')
    const region = useLayoutStore.getState().regions['primary-sidebar']
    expect(region.mode).toBe('hidden')
    expect(region.previousMode).toBe('pinned')
  })

  it('hides a collapsed region and remembers previousMode', () => {
    // default is collapsed
    useLayoutStore.getState().toggleVisibility('primary-sidebar')
    const region = useLayoutStore.getState().regions['primary-sidebar']
    expect(region.mode).toBe('hidden')
    expect(region.previousMode).toBe('collapsed')
  })

  it('restores to previousMode when unhiding', () => {
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'collapsed')
    useLayoutStore.getState().toggleVisibility('primary-sidebar') // hide
    useLayoutStore.getState().toggleVisibility('primary-sidebar') // restore
    const region = useLayoutStore.getState().regions['primary-sidebar']
    expect(region.mode).toBe('collapsed')
    expect(region.previousMode).toBeUndefined()
  })

  it('defaults to pinned when no previousMode', () => {
    // Directly set to hidden without going through toggleVisibility
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'hidden')
    useLayoutStore.getState().toggleVisibility('primary-sidebar')
    expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/stores/useLayoutStore.test.ts`
Expected: FAIL — `previousMode` property doesn't exist, restore doesn't work

- [ ] **Step 3: Implement previousMode and fix toggleVisibility**

In `spa/src/stores/useLayoutStore.ts`:

Update `RegionState`:
```typescript
interface RegionState {
  views: string[]
  activeViewId?: string
  width: number
  mode: 'pinned' | 'collapsed' | 'hidden'
  previousMode?: 'pinned' | 'collapsed'
}
```

Replace `toggleVisibility`:
```typescript
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
        }),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/stores/useLayoutStore.test.ts`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useLayoutStore.ts spa/src/stores/useLayoutStore.test.ts
git commit -m "fix: toggleVisibility remembers previousMode on hide/restore"
```

---

### Task 3: Layout Store — addView, removeView, reorderViews

**Files:**
- Modify: `spa/src/stores/useLayoutStore.ts`
- Modify: `spa/src/stores/useLayoutStore.test.ts`

- [ ] **Step 1: Write failing tests**

Append to `spa/src/stores/useLayoutStore.test.ts`:

```typescript
describe('addView', () => {
  it('appends a view to the region', () => {
    useLayoutStore.getState().addView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['view-a'])
  })

  it('appends to existing views', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    useLayoutStore.getState().addView('primary-sidebar', 'view-b')
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['view-a', 'view-b'])
  })

  it('ignores duplicate view', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    useLayoutStore.getState().addView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['view-a'])
  })
})

describe('removeView', () => {
  it('removes a view from the region', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    useLayoutStore.getState().removeView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['view-b'])
  })

  it('resets activeViewId to first when active view is removed', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'view-a')
    useLayoutStore.getState().removeView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBe('view-b')
  })

  it('sets activeViewId to undefined when last view removed', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'view-a')
    useLayoutStore.getState().removeView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBeUndefined()
  })

  it('does not change activeViewId when non-active view is removed', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'view-b')
    useLayoutStore.getState().removeView('primary-sidebar', 'view-a')
    expect(useLayoutStore.getState().regions['primary-sidebar'].activeViewId).toBe('view-b')
  })
})

describe('reorderViews', () => {
  it('reorders views', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['a', 'b', 'c'])
    useLayoutStore.getState().reorderViews('primary-sidebar', ['c', 'a', 'b'])
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['c', 'a', 'b'])
  })

  it('discards extra ids and appends missing ids', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['a', 'b', 'c'])
    useLayoutStore.getState().reorderViews('primary-sidebar', ['b', 'x'])
    // 'x' discarded (not in original), 'a' and 'c' appended
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toEqual(['b', 'a', 'c'])
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/stores/useLayoutStore.test.ts`
Expected: FAIL — `addView`, `removeView`, `reorderViews` not defined

- [ ] **Step 3: Implement the three actions**

In `spa/src/stores/useLayoutStore.ts`, add to the `LayoutState` interface:

```typescript
  addView: (region: SidebarRegion, viewId: string) => void
  removeView: (region: SidebarRegion, viewId: string) => void
  reorderViews: (region: SidebarRegion, views: string[]) => void
```

Add implementations inside the `create` call, after `toggleVisibility`:

```typescript
      addView: (region, viewId) =>
        set((state) => {
          const current = state.regions[region].views
          if (current.includes(viewId)) return state
          return updateRegion(state, region, { views: [...current, viewId] })
        }),

      removeView: (region, viewId) =>
        set((state) => {
          const { views, activeViewId } = state.regions[region]
          const next = views.filter((id) => id !== viewId)
          const patch: Partial<RegionState> = { views: next }
          if (activeViewId === viewId) {
            patch.activeViewId = next[0]
          }
          return updateRegion(state, region, patch)
        }),

      reorderViews: (region, newOrder) =>
        set((state) => {
          const current = state.regions[region].views
          const currentSet = new Set(current)
          // Keep only ids that exist in current views, in the new order
          const reordered = newOrder.filter((id) => currentSet.has(id))
          // Append any current ids missing from newOrder
          const reorderedSet = new Set(reordered)
          for (const id of current) {
            if (!reorderedSet.has(id)) reordered.push(id)
          }
          return updateRegion(state, region, { views: reordered })
        }),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/stores/useLayoutStore.test.ts`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useLayoutStore.ts spa/src/stores/useLayoutStore.test.ts
git commit -m "feat: add addView, removeView, reorderViews to layout store"
```

---

### Task 4: TitleBar — always show region toggles

**Files:**
- Modify: `spa/src/components/TitleBar.tsx:34`
- Modify: `spa/src/components/TitleBar.test.tsx` (if exists, otherwise skip test update)

- [ ] **Step 1: Remove views.length filter and simplify separator**

In `spa/src/components/TitleBar.tsx`, change line 34 from:

```typescript
  const visibleToggles = regionToggles.filter((t) => regions[t.region].views.length > 0)
```

to:

```typescript
  const visibleToggles = regionToggles
```

Also update the separator condition (around line 68) — since `visibleToggles` is now always the full array, the condition is always true. Simplify to unconditional:

```typescript
        <div className="w-px h-3.5 bg-border-subtle mx-0.5" />
```

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/TitleBar.tsx
git commit -m "fix: TitleBar always shows all region toggle buttons"
```

---

### Task 5: SidebarRegion — empty state, management mode, ⚙/+ buttons

**Files:**
- Modify: `spa/src/components/SidebarRegion.tsx`
- Modify: `spa/src/components/SidebarRegion.test.tsx`

- [ ] **Step 1: Write failing tests for empty region and management toggle**

Add to `spa/src/components/SidebarRegion.test.tsx`:

```typescript
it('renders empty pinned region with gear button', () => {
  useLayoutStore.getState().setRegionViews('primary-sidebar', [])
  useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
  render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
  expect(screen.getByTestId('manage-button')).toBeInTheDocument()
  expect(screen.getByText(/加入 views/i)).toBeInTheDocument()
})

it('renders collapsed empty region with plus button', () => {
  useLayoutStore.getState().setRegionViews('primary-sidebar', [])
  useLayoutStore.getState().setRegionMode('primary-sidebar', 'collapsed')
  render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
  expect(screen.getByTestId('add-view-button')).toBeInTheDocument()
})

it('clicking plus on collapsed bar expands and opens manage mode', () => {
  useLayoutStore.getState().setRegionViews('primary-sidebar', ['session-list'])
  useLayoutStore.getState().setRegionMode('primary-sidebar', 'collapsed')
  render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
  fireEvent.click(screen.getByTestId('add-view-button'))
  expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
  expect(screen.getByTestId('region-manager')).toBeInTheDocument()
})

it('returns null when hidden regardless of views', () => {
  useLayoutStore.getState().setRegionViews('primary-sidebar', ['session-list'])
  useLayoutStore.getState().setRegionMode('primary-sidebar', 'hidden')
  const { container } = render(<SidebarRegion region="primary-sidebar" resizeEdge="right" />)
  expect(container.firstChild).toBeNull()
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/components/SidebarRegion.test.tsx`
Expected: FAIL — empty region returns null, no manage-button

- [ ] **Step 3: Implement SidebarRegion changes**

Rewrite `spa/src/components/SidebarRegion.tsx`:

```typescript
import { useState } from 'react'
import { CaretLeft, CaretRight, GearSix, Plus } from '@phosphor-icons/react'
import { useLayoutStore } from '../stores/useLayoutStore'
import { useTabStore } from '../stores/useTabStore'
import { getViewDefinition } from '../lib/module-registry'
import { RegionResize } from './RegionResize'
import { RegionManager } from './RegionManager'
import { RegionContextMenu } from './RegionContextMenu'
import type { SidebarRegion as SidebarRegionType } from '../types/tab'
import { useWorkspaceStore } from '../features/workspace/store'
import { useHostStore } from '../stores/useHostStore'

interface Props {
  region: SidebarRegionType
  resizeEdge: 'left' | 'right'
}

export function SidebarRegion({ region, resizeEdge }: Props) {
  const regionState = useLayoutStore((s) => s.regions[region])
  const setRegionWidth = useLayoutStore((s) => s.setRegionWidth)
  const toggleRegion = useLayoutStore((s) => s.toggleRegion)
  const setRegionMode = useLayoutStore((s) => s.setRegionMode)
  const setActiveView = useLayoutStore((s) => s.setActiveView)

  const { views, activeViewId, width, mode } = regionState
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
  const activeHostId = useHostStore((s) => s.activeHostId ?? s.hostOrder[0] ?? '')
  const activeTabId = useTabStore((s) => s.activeTabId)

  const [managing, setManaging] = useState(false)
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number } | null>(null)

  if (mode === 'hidden') return null

  const resolvedActiveViewId = activeViewId ?? views[0]
  const activeView = resolvedActiveViewId ? getViewDefinition(resolvedActiveViewId) : undefined

  const handleContextMenu = (e: React.MouseEvent) => {
    e.preventDefault()
    setContextMenu({ x: e.clientX, y: e.clientY })
  }

  const openManageMode = () => {
    if (mode === 'collapsed') setRegionMode(region, 'pinned')
    setManaging(true)
  }

  if (mode === 'collapsed') {
    return (
      <>
        <div
          data-testid="collapsed-bar"
          className="shrink-0 w-6 bg-surface-tertiary border-border-subtle flex flex-col items-center pt-2 gap-1 cursor-pointer hover:bg-surface-hover transition-colors"
          style={{ borderLeftWidth: resizeEdge === 'left' ? 1 : 0, borderRightWidth: resizeEdge === 'right' ? 1 : 0 }}
          onClick={() => toggleRegion(region)}
          onContextMenu={handleContextMenu}
        >
          {views.map((viewId) => {
            const viewDef = getViewDefinition(viewId)
            if (!viewDef) return null
            const Icon = viewDef.icon
            return (
              <div
                key={viewId}
                className={`w-5 h-5 flex items-center justify-center rounded ${
                  viewId === resolvedActiveViewId ? 'text-text-primary' : 'text-text-muted'
                }`}
                title={viewDef.label}
              >
                <Icon size={14} />
              </div>
            )
          })}
          <div className="flex-1" />
          <button
            data-testid="add-view-button"
            className="w-5 h-5 flex items-center justify-center rounded text-text-muted hover:text-text-primary mb-2"
            onClick={(e) => {
              e.stopPropagation()
              openManageMode()
            }}
            title="Manage views"
          >
            <Plus size={12} />
          </button>
        </div>
        {contextMenu && (
          <RegionContextMenu
            region={region}
            position={contextMenu}
            onClose={() => setContextMenu(null)}
          />
        )}
      </>
    )
  }

  // Pinned mode
  const ActiveComponent = activeView?.component
  const CollapseIcon = resizeEdge === 'right' ? CaretLeft : CaretRight

  const resizeHandle = (
    <RegionResize
      resizeEdge={resizeEdge}
      onResize={(delta) => setRegionWidth(region, width + delta)}
    />
  )

  return (
    <div className="shrink-0 flex" style={{ width }}>
      {resizeEdge === 'left' && resizeHandle}
      <div className="flex-1 flex flex-col min-w-0 bg-surface-tertiary border-border-subtle"
        style={{ borderLeftWidth: resizeEdge === 'right' ? 1 : 0, borderRightWidth: resizeEdge === 'left' ? 1 : 0 }}
        onContextMenu={handleContextMenu}
      >
        <div className="shrink-0 flex items-center gap-0.5 px-1 py-1 border-b border-border-subtle">
          {views.length > 1 && views.map((viewId) => {
            const viewDef = getViewDefinition(viewId)
            if (!viewDef) return null
            return (
              <button
                key={viewId}
                className={`px-2 py-0.5 rounded text-xs transition-colors ${
                  viewId === resolvedActiveViewId
                    ? 'bg-surface-active text-text-primary'
                    : 'text-text-muted hover:text-text-primary hover:bg-surface-hover'
                }`}
                onClick={() => setActiveView(region, viewId)}
              >
                {viewDef.label}
              </button>
            )
          })}
          <div className="flex-1" />
          <button
            data-testid="manage-button"
            className={`p-0.5 rounded transition-colors ${
              managing
                ? 'text-accent-base bg-accent-base/10'
                : 'text-text-muted hover:text-text-primary hover:bg-surface-hover'
            }`}
            onClick={() => setManaging(!managing)}
            title="Manage views"
          >
            <GearSix size={12} />
          </button>
          <button
            data-testid="collapse-button"
            className="p-0.5 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
            onClick={() => {
              setManaging(false)
              toggleRegion(region)
            }}
            title="Collapse"
          >
            <CollapseIcon size={12} />
          </button>
        </div>
        <div className="flex-1 overflow-hidden">
          {managing ? (
            <RegionManager region={region} />
          ) : views.length === 0 ? (
            <button
              className="flex-1 flex flex-col items-center justify-center gap-2 p-4 text-text-muted hover:text-text-primary cursor-pointer w-full h-full"
              onClick={openManageMode}
            >
              <Plus size={20} />
              <span className="text-xs">加入 views</span>
            </button>
          ) : ActiveComponent ? (
            <ActiveComponent
              isActive={true}
              region={region}
              workspaceId={activeWorkspaceId ?? undefined}
              hostId={activeHostId || undefined}
              tabId={activeTabId ?? undefined}
            />
          ) : null}
        </div>
      </div>
      {resizeEdge === 'right' && resizeHandle}
      {contextMenu && (
        <RegionContextMenu
          region={region}
          position={contextMenu}
          onClose={() => setContextMenu(null)}
        />
      )}
    </div>
  )
}
```

Note: This imports `RegionManager` and `RegionContextMenu` which don't exist yet. Create stubs so tests can run:

Create `spa/src/components/RegionManager.tsx`:
```typescript
import type { SidebarRegion } from '../types/tab'

interface Props {
  region: SidebarRegion
}

export function RegionManager({ region }: Props) {
  void region
  return <div data-testid="region-manager">Manager placeholder</div>
}
```

Create `spa/src/components/RegionContextMenu.tsx`:
```typescript
import type { SidebarRegion } from '../types/tab'

interface Props {
  region: SidebarRegion
  position: { x: number; y: number }
  onClose: () => void
}

export function RegionContextMenu({ region, position, onClose }: Props) {
  void region; void position; void onClose
  return null
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/SidebarRegion.test.tsx`
Expected: All pass (existing + new)

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/SidebarRegion.tsx spa/src/components/SidebarRegion.test.tsx spa/src/components/RegionManager.tsx spa/src/components/RegionContextMenu.tsx
git commit -m "feat: SidebarRegion empty state, management mode, gear/plus buttons"
```

---

### Task 6: RegionManager — full implementation

**Files:**
- Modify: `spa/src/components/RegionManager.tsx` (replace stub)
- Create: `spa/src/components/RegionManager.test.tsx`

- [ ] **Step 1: Write tests**

Create `spa/src/components/RegionManager.test.tsx`:

```typescript
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { RegionManager } from './RegionManager'
import { useLayoutStore } from '../stores/useLayoutStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

const DummyIcon = ({ size }: { size?: number }) => <span data-testid="icon">{size}</span>
const DummyView = () => <div>view</div>

beforeEach(() => {
  clearModuleRegistry()
  useLayoutStore.setState(useLayoutStore.getInitialState())
  registerModule({
    id: 'mod-a',
    name: 'Module A',
    views: [
      { id: 'view-a', label: 'View A', icon: DummyIcon, scope: 'system', component: DummyView },
      { id: 'view-b', label: 'View B', icon: DummyIcon, scope: 'workspace', component: DummyView },
    ],
  })
  registerModule({
    id: 'mod-b',
    name: 'Module B',
    views: [
      { id: 'view-c', label: 'View C', icon: DummyIcon, scope: 'tab', component: DummyView },
    ],
  })
})

describe('RegionManager', () => {
  it('shows enabled views and available views', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    render(<RegionManager region="primary-sidebar" />)
    // view-a is enabled
    expect(screen.getByText('View A')).toBeInTheDocument()
    // view-b, view-c are available
    expect(screen.getByText('View B')).toBeInTheDocument()
    expect(screen.getByText('View C')).toBeInTheDocument()
  })

  it('adds a view when clicking add button', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    render(<RegionManager region="primary-sidebar" />)
    const addButtons = screen.getAllByTestId('add-view-btn')
    fireEvent.click(addButtons[0]) // Add first available (view-b)
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toContain('view-b')
  })

  it('removes a view when clicking remove button', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    render(<RegionManager region="primary-sidebar" />)
    const removeButtons = screen.getAllByTestId('remove-view-btn')
    fireEvent.click(removeButtons[0]) // Remove first enabled (view-a)
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).not.toContain('view-a')
  })

  it('shows all views as available when region is empty', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', [])
    render(<RegionManager region="primary-sidebar" />)
    const addButtons = screen.getAllByTestId('add-view-btn')
    expect(addButtons).toHaveLength(3)
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/components/RegionManager.test.tsx`
Expected: FAIL — stub doesn't render view lists

- [ ] **Step 3: Implement RegionManager**

Replace `spa/src/components/RegionManager.tsx`:

```typescript
import { GripVertical, Plus, X } from '@phosphor-icons/react'
import {
  DndContext,
  closestCenter,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core'
import {
  SortableContext,
  verticalListSortingStrategy,
  useSortable,
  arrayMove,
} from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { useLayoutStore } from '../stores/useLayoutStore'
import { getAllViews, getViewDefinition } from '../lib/module-registry'
import type { SidebarRegion } from '../types/tab'

interface Props {
  region: SidebarRegion
}

export function RegionManager({ region }: Props) {
  const views = useLayoutStore((s) => s.regions[region].views)
  const addView = useLayoutStore((s) => s.addView)
  const removeView = useLayoutStore((s) => s.removeView)
  const reorderViews = useLayoutStore((s) => s.reorderViews)

  const allViews = getAllViews()
  const enabledSet = new Set(views)
  const available = allViews.filter((v) => !enabledSet.has(v.id))

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 4 } }))

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const oldIndex = views.indexOf(String(active.id))
    const newIndex = views.indexOf(String(over.id))
    if (oldIndex === -1 || newIndex === -1) return
    reorderViews(region, arrayMove(views, oldIndex, newIndex))
  }

  return (
    <div data-testid="region-manager" className="flex-1 overflow-y-auto p-2">
      {/* Enabled views */}
      <div className="text-[10px] font-semibold text-text-muted uppercase tracking-wider mb-1.5 px-1">
        已啟用
      </div>
      {views.length === 0 ? (
        <div className="text-xs text-text-muted px-1 mb-3">無</div>
      ) : (
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
          <SortableContext items={views} strategy={verticalListSortingStrategy}>
            <div className="flex flex-col gap-0.5 mb-3">
              {views.map((viewId) => (
                <SortableViewItem
                  key={viewId}
                  viewId={viewId}
                  onRemove={() => removeView(region, viewId)}
                />
              ))}
            </div>
          </SortableContext>
        </DndContext>
      )}

      {/* Available views */}
      {available.length > 0 && (
        <>
          <div className="text-[10px] font-semibold text-text-muted uppercase tracking-wider mb-1.5 px-1">
            可加入
          </div>
          <div className="flex flex-col gap-0.5">
            {available.map((view) => (
              <div
                key={view.id}
                className="flex items-center gap-2 px-2 py-1.5 rounded text-xs text-text-muted hover:bg-surface-hover transition-colors"
              >
                <view.icon size={14} />
                <span className="flex-1 truncate">{view.label}</span>
                <button
                  data-testid="add-view-btn"
                  className="p-0.5 rounded hover:bg-surface-active hover:text-text-primary transition-colors"
                  onClick={() => addView(region, view.id)}
                >
                  <Plus size={12} />
                </button>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

function SortableViewItem({ viewId, onRemove }: { viewId: string; onRemove: () => void }) {
  const viewDef = getViewDefinition(viewId)
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: viewId })

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  if (!viewDef) return null
  const Icon = viewDef.icon

  return (
    <div
      ref={setNodeRef}
      style={style}
      className="flex items-center gap-2 px-2 py-1.5 rounded text-xs bg-surface-active/50 text-text-primary"
    >
      <button
        className="p-0.5 cursor-grab text-text-muted hover:text-text-primary"
        {...attributes}
        {...listeners}
      >
        <GripVertical size={12} />
      </button>
      <Icon size={14} />
      <span className="flex-1 truncate">{viewDef.label}</span>
      <button
        data-testid="remove-view-btn"
        className="p-0.5 rounded text-text-muted hover:text-red-400 hover:bg-red-400/10 transition-colors"
        onClick={onRemove}
      >
        <X size={12} />
      </button>
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/RegionManager.test.tsx`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/RegionManager.tsx spa/src/components/RegionManager.test.tsx
git commit -m "feat: RegionManager with drag-to-reorder and add/remove views"
```

---

### Task 7: RegionContextMenu — full implementation

**Files:**
- Modify: `spa/src/components/RegionContextMenu.tsx` (replace stub)
- Create: `spa/src/components/RegionContextMenu.test.tsx`

- [ ] **Step 1: Write tests**

Create `spa/src/components/RegionContextMenu.test.tsx`:

```typescript
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { RegionContextMenu } from './RegionContextMenu'
import { useLayoutStore } from '../stores/useLayoutStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

const DummyIcon = ({ size }: { size?: number }) => <span>{size}</span>
const DummyView = () => <div>view</div>

beforeEach(() => {
  clearModuleRegistry()
  useLayoutStore.setState(useLayoutStore.getInitialState())
  registerModule({
    id: 'mod-a',
    name: 'A',
    views: [
      { id: 'view-a', label: 'View A', icon: DummyIcon, scope: 'system', component: DummyView },
      { id: 'view-b', label: 'View B', icon: DummyIcon, scope: 'workspace', component: DummyView },
    ],
  })
})

describe('RegionContextMenu', () => {
  it('shows all views with enabled ones checked', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    render(
      <RegionContextMenu region="primary-sidebar" position={{ x: 100, y: 100 }} onClose={() => {}} />,
    )
    expect(screen.getByText('View A')).toBeInTheDocument()
    expect(screen.getByText('View B')).toBeInTheDocument()
  })

  it('adds view when clicking unchecked item', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a'])
    render(
      <RegionContextMenu region="primary-sidebar" position={{ x: 100, y: 100 }} onClose={() => {}} />,
    )
    fireEvent.click(screen.getByText('View B'))
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).toContain('view-b')
  })

  it('removes view when clicking checked item', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-a', 'view-b'])
    render(
      <RegionContextMenu region="primary-sidebar" position={{ x: 100, y: 100 }} onClose={() => {}} />,
    )
    fireEvent.click(screen.getByText('View A'))
    expect(useLayoutStore.getState().regions['primary-sidebar'].views).not.toContain('view-a')
  })

  it('shows enabled views first in region order, then available in registry order', () => {
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['view-b']) // view-b enabled
    render(
      <RegionContextMenu region="primary-sidebar" position={{ x: 100, y: 100 }} onClose={() => {}} />,
    )
    const items = screen.getAllByRole('button')
    // First: enabled (view-b), then available in registry order (view-a)
    const labels = items.map((el) => el.textContent).filter(Boolean)
    expect(labels.indexOf('View B')).toBeLessThan(labels.indexOf('View A'))
  })

  it('calls onClose when clicking outside', () => {
    const onClose = vi.fn()
    render(
      <RegionContextMenu region="primary-sidebar" position={{ x: 100, y: 100 }} onClose={onClose} />,
    )
    fireEvent.mouseDown(document.body)
    expect(onClose).toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/components/RegionContextMenu.test.tsx`
Expected: FAIL — stub returns null

- [ ] **Step 3: Implement RegionContextMenu**

Replace `spa/src/components/RegionContextMenu.tsx`:

```typescript
import { useEffect, useRef, useLayoutEffect } from 'react'
import { CheckSquare, Square } from '@phosphor-icons/react'
import { useLayoutStore } from '../stores/useLayoutStore'
import { getAllViews } from '../lib/module-registry'
import type { SidebarRegion } from '../types/tab'

interface Props {
  region: SidebarRegion
  position: { x: number; y: number }
  onClose: () => void
}

export function RegionContextMenu({ region, position, onClose }: Props) {
  const views = useLayoutStore((s) => s.regions[region].views)
  const addView = useLayoutStore((s) => s.addView)
  const removeView = useLayoutStore((s) => s.removeView)
  const menuRef = useRef<HTMLDivElement>(null)

  const enabledSet = new Set(views)
  const allViews = getAllViews()

  // Enabled views first (in region order), then available (in registry order)
  const enabledViews = views
    .map((id) => allViews.find((v) => v.id === id))
    .filter(Boolean) as typeof allViews
  const availableViews = allViews.filter((v) => !enabledSet.has(v.id))
  const orderedViews = [...enabledViews, ...availableViews]

  // Viewport correction
  useLayoutEffect(() => {
    const el = menuRef.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    if (rect.right > window.innerWidth) el.style.left = `${position.x - rect.width}px`
    if (rect.bottom > window.innerHeight) el.style.top = `${position.y - rect.height}px`
  }, [position])

  // Close on outside click
  useEffect(() => {
    const handle = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) onClose()
    }
    document.addEventListener('mousedown', handle)
    return () => document.removeEventListener('mousedown', handle)
  }, [onClose])

  // Close on escape
  useEffect(() => {
    const handle = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', handle)
    return () => document.removeEventListener('keydown', handle)
  }, [onClose])

  const handleToggle = (viewId: string) => {
    if (enabledSet.has(viewId)) {
      removeView(region, viewId)
    } else {
      addView(region, viewId)
    }
  }

  return (
    <div
      ref={menuRef}
      className="fixed z-50 bg-surface-secondary border border-border-subtle rounded-md shadow-lg py-1 min-w-[180px]"
      style={{ left: position.x, top: position.y }}
    >
      <div className="px-3 py-1 text-[10px] font-semibold text-text-muted uppercase tracking-wider">
        Views
      </div>
      {orderedViews.map((view) => {
        const enabled = enabledSet.has(view.id)
        const Icon = enabled ? CheckSquare : Square
        return (
          <button
            key={view.id}
            className="flex items-center gap-2 w-full px-3 py-1.5 text-xs text-left hover:bg-surface-hover transition-colors"
            onClick={() => handleToggle(view.id)}
          >
            <Icon size={14} className={enabled ? 'text-accent-base' : 'text-text-muted'} />
            <span className={enabled ? 'text-text-primary' : 'text-text-muted'}>{view.label}</span>
          </button>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/RegionContextMenu.test.tsx`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/RegionContextMenu.tsx spa/src/components/RegionContextMenu.test.tsx
git commit -m "feat: RegionContextMenu for quick view toggle via right-click"
```

---

### Task 8: main.tsx initialization + type cleanup

**Files:**
- Modify: `spa/src/main.tsx:17-23`
- Modify: `spa/src/types/tab.ts:48,59-65`
- Modify: `spa/src/types/tab.test.ts:61-77`

- [ ] **Step 1: Update main.tsx initialization**

In `spa/src/main.tsx`, replace lines 17-23:

```typescript
// Only set defaults if not already persisted (first install)
const regions = useLayoutStore.getState().regions
const hasAnyView = Object.values(regions).some((r) => r.views.length > 0)
if (!hasAnyView) {
  useLayoutStore.getState().setRegionViews('primary-sidebar', ['file-tree-workspace'])
  useLayoutStore.getState().setActiveView('primary-sidebar', 'file-tree-workspace')
}
```

Remove the comment `// Note: file-tree-session is a placeholder...`.

- [ ] **Step 2: Remove WorkspaceSidebarState**

In `spa/src/types/tab.ts`:

1. Remove `sidebarState?: WorkspaceSidebarState` from `Workspace` interface (line 48)
2. Remove the `WorkspaceSidebarState` interface (lines 59-65)

In `spa/src/types/tab.test.ts`:

1. Remove `WorkspaceSidebarState` from the import
2. Remove the entire `describe('Workspace.sidebarState', ...)` block (lines 61-77)

- [ ] **Step 3: Run all tests**

Run: `cd spa && npx vitest run`
Expected: All pass

- [ ] **Step 4: Run lint**

Run: `cd spa && pnpm run lint`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add spa/src/main.tsx spa/src/types/tab.ts spa/src/types/tab.test.ts
git commit -m "chore: update init logic, remove unused WorkspaceSidebarState"
```

---

### Task 9: Final integration test

**Files:** None to create — verify everything works together

- [ ] **Step 1: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All tests pass

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Expected: No errors

- [ ] **Step 3: Build check**

Run: `cd spa && pnpm run build`
Expected: Build succeeds with no type errors

- [ ] **Step 4: Final commit if any fixups needed**

If lint/build revealed issues, fix and commit:
```bash
git add -A
git commit -m "fix: address lint/build issues from sidebar management feature"
```
