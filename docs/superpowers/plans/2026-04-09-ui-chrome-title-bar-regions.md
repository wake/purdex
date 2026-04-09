# UI Chrome: Title Bar + Region Components Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure the App layout to add a dedicated Title Bar (Electron) and integrate the 4 sidebar/panel regions from the Layout Store.

**Architecture:** Title Bar replaces the current Electron title bar section in App.tsx. Four Region containers (primary-sidebar, primary-panel, secondary-panel, secondary-sidebar) wrap the content area. Regions read from `useLayoutStore` and render registered views from `module-registry`.

**Tech Stack:** React 19 / Zustand 5 / Tailwind 4 / Vitest

**Spec:** `docs/superpowers/specs/2026-04-09-module-layout-pane-split-design.md` (Sections 1, 5)

**Depends on:** Plan 1 (module-registry + useLayoutStore) — completed

**This is Plan 2 of 3:**
1. ~~Foundation~~ — done
2. **UI Chrome** (this plan) — Title Bar + Region components + App layout
3. **Pane Split** — Split renderer + pane operations + New Pane Page + Files Module

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `spa/src/components/TitleBar.tsx` | Electron title bar: traffic lights + title + layout buttons placeholder |
| Create | `spa/src/components/TitleBar.test.tsx` | TitleBar render tests |
| Create | `spa/src/components/SidebarRegion.tsx` | Region container: expanded/collapsed states, view tabs, content |
| Create | `spa/src/components/SidebarRegion.test.tsx` | Region rendering + toggle tests |
| Create | `spa/src/components/RegionResize.tsx` | Drag handle for region width adjustment |
| Create | `spa/src/components/RegionResize.test.tsx` | Resize interaction tests |
| Modify | `spa/src/App.tsx` | Restructure layout: TitleBar + 4 regions + tab bar repositioning |

**Task order:** Task 1 (TitleBar) → Task 2 (SidebarRegion + RegionResize) → Task 3 (App.tsx integration) → Task 4 (verification)

---

### Task 1: TitleBar Component

**Files:**
- Create: `spa/src/components/TitleBar.tsx`
- Create: `spa/src/components/TitleBar.test.tsx`

The TitleBar is Electron-only. It replaces the current inline title bar in App.tsx.

- [ ] **Step 1: Write failing tests**

```tsx
// spa/src/components/TitleBar.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TitleBar } from './TitleBar'

describe('TitleBar', () => {
  it('renders the title text', () => {
    render(<TitleBar title="tmux-box — tbox2" />)
    expect(screen.getByText('tmux-box — tbox2')).toBeDefined()
  })

  it('renders layout pattern buttons', () => {
    render(<TitleBar title="test" />)
    // Layout buttons should exist (even if placeholder for now)
    expect(screen.getByTestId('layout-buttons')).toBeDefined()
  })

  it('applies drag region styling', () => {
    const { container } = render(<TitleBar title="test" />)
    const bar = container.firstElementChild as HTMLElement
    expect(bar.style.getPropertyValue('-webkit-app-region') || bar.style.webkitAppRegion).toBeDefined()
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/components/TitleBar.test.tsx`

- [ ] **Step 3: Implement TitleBar**

```tsx
// spa/src/components/TitleBar.tsx
import { Columns, Rows, GridFour, Square } from '@phosphor-icons/react'

interface Props {
  title: string
}

export function TitleBar({ title }: Props) {
  return (
    <div
      className="shrink-0 flex items-center bg-surface-secondary border-b border-border-subtle px-2"
      style={{ height: 30, WebkitAppRegion: 'drag' } as React.CSSProperties}
    >
      {/* Traffic light safe zone */}
      <div className="shrink-0" style={{ width: 70 }} />

      {/* Title — centered */}
      <div className="flex-1 text-center text-xs text-text-muted truncate select-none">
        {title}
      </div>

      {/* Layout pattern buttons — placeholder, will be wired in Plan 3 */}
      <div
        data-testid="layout-buttons"
        className="shrink-0 flex items-center gap-0.5"
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        <button className="p-1 rounded hover:bg-surface-hover text-text-muted hover:text-text-primary" title="Single pane">
          <Square size={14} />
        </button>
        <button className="p-1 rounded hover:bg-surface-hover text-text-muted hover:text-text-primary" title="Split horizontal">
          <Columns size={14} />
        </button>
        <button className="p-1 rounded hover:bg-surface-hover text-text-muted hover:text-text-primary" title="Split vertical">
          <Rows size={14} />
        </button>
        <button className="p-1 rounded hover:bg-surface-hover text-text-muted hover:text-text-primary" title="Grid">
          <GridFour size={14} />
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/TitleBar.test.tsx`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/TitleBar.tsx spa/src/components/TitleBar.test.tsx
git commit -m "feat: add TitleBar component for Electron"
```

---

### Task 2: SidebarRegion + RegionResize Components

**Files:**
- Create: `spa/src/components/SidebarRegion.tsx`
- Create: `spa/src/components/SidebarRegion.test.tsx`
- Create: `spa/src/components/RegionResize.tsx`
- Create: `spa/src/components/RegionResize.test.tsx`

SidebarRegion is the container for each of the 4 regions. It reads from `useLayoutStore` and renders the active view from the module registry. RegionResize is the drag handle on the region edge.

- [ ] **Step 1: Write RegionResize tests**

```tsx
// spa/src/components/RegionResize.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import { RegionResize } from './RegionResize'

describe('RegionResize', () => {
  it('renders a drag handle', () => {
    const { container } = render(<RegionResize onResize={vi.fn()} side="right" />)
    expect(container.firstElementChild).toBeDefined()
  })

  it('calls onResize with delta on mouse drag', () => {
    const onResize = vi.fn()
    const { container } = render(<RegionResize onResize={onResize} side="right" />)
    const handle = container.firstElementChild as HTMLElement

    fireEvent.mouseDown(handle, { clientX: 100 })
    fireEvent.mouseMove(document, { clientX: 150 })
    fireEvent.mouseUp(document)

    expect(onResize).toHaveBeenCalledWith(50)
  })

  it('negates delta for left side', () => {
    const onResize = vi.fn()
    const { container } = render(<RegionResize onResize={onResize} side="left" />)
    const handle = container.firstElementChild as HTMLElement

    fireEvent.mouseDown(handle, { clientX: 200 })
    fireEvent.mouseMove(document, { clientX: 150 })
    fireEvent.mouseUp(document)

    // Moving left on a left-side handle means expanding (positive delta)
    expect(onResize).toHaveBeenCalledWith(50)
  })
})
```

- [ ] **Step 2: Implement RegionResize**

```tsx
// spa/src/components/RegionResize.tsx
import { useCallback, useRef } from 'react'

interface Props {
  onResize: (delta: number) => void
  side: 'left' | 'right'
}

export function RegionResize({ onResize, side }: Props) {
  const startX = useRef(0)

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    startX.current = e.clientX

    const handleMouseMove = (moveEvent: MouseEvent) => {
      const rawDelta = moveEvent.clientX - startX.current
      // Left-side handle: dragging left = expanding = positive delta
      const delta = side === 'left' ? -rawDelta : rawDelta
      onResize(delta)
      startX.current = moveEvent.clientX
    }

    const handleMouseUp = () => {
      document.removeEventListener('mousemove', handleMouseMove)
      document.removeEventListener('mouseup', handleMouseUp)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }

    document.addEventListener('mousemove', handleMouseMove)
    document.addEventListener('mouseup', handleMouseUp)
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
  }, [onResize, side])

  return (
    <div
      className="w-1 shrink-0 cursor-col-resize hover:bg-accent-base/30 active:bg-accent-base/50 transition-colors"
      onMouseDown={handleMouseDown}
    />
  )
}
```

- [ ] **Step 3: Run RegionResize tests**

Run: `cd spa && npx vitest run src/components/RegionResize.test.tsx`

- [ ] **Step 4: Write SidebarRegion tests**

```tsx
// spa/src/components/SidebarRegion.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SidebarRegion } from './SidebarRegion'
import { useLayoutStore } from '../stores/useLayoutStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

const DummyView = ({ isActive }: { isActive: boolean }) => (
  <div data-testid="dummy-view">{isActive ? 'active' : 'inactive'}</div>
)

beforeEach(() => {
  useLayoutStore.setState(useLayoutStore.getInitialState())
  clearModuleRegistry()
})

describe('SidebarRegion', () => {
  it('renders nothing when collapsed and no views', () => {
    const { container } = render(<SidebarRegion region="primary-sidebar" side="right" />)
    // Collapsed with no views = nothing rendered
    expect(container.innerHTML).toBe('')
  })

  it('renders collapsed bar when collapsed with views', () => {
    registerModule({
      id: 'test',
      name: 'Test',
      views: [{
        id: 'test-view',
        label: 'Test View',
        icon: 'List',
        scope: 'system',
        defaultRegion: 'primary-sidebar',
        component: DummyView,
      }],
    })
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')

    render(<SidebarRegion region="primary-sidebar" side="right" />)
    // Should render collapsed indicator
    expect(screen.getByTestId('collapsed-bar')).toBeDefined()
  })

  it('renders expanded view when pinned', () => {
    registerModule({
      id: 'test',
      name: 'Test',
      views: [{
        id: 'test-view',
        label: 'Test View',
        icon: 'List',
        scope: 'system',
        defaultRegion: 'primary-sidebar',
        component: DummyView,
      }],
    })
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')

    render(<SidebarRegion region="primary-sidebar" side="right" />)
    expect(screen.getByTestId('dummy-view')).toBeDefined()
    expect(screen.getByText('active')).toBeDefined()
  })

  it('toggles region mode on collapsed bar click', () => {
    registerModule({
      id: 'test',
      name: 'Test',
      views: [{
        id: 'test-view',
        label: 'Test View',
        icon: 'List',
        scope: 'system',
        defaultRegion: 'primary-sidebar',
        component: DummyView,
      }],
    })
    useLayoutStore.getState().setRegionViews('primary-sidebar', ['test-view'])
    useLayoutStore.getState().setActiveView('primary-sidebar', 'test-view')

    render(<SidebarRegion region="primary-sidebar" side="right" />)
    fireEvent.click(screen.getByTestId('collapsed-bar'))

    expect(useLayoutStore.getState().regions['primary-sidebar'].mode).toBe('pinned')
  })
})
```

- [ ] **Step 5: Implement SidebarRegion**

```tsx
// spa/src/components/SidebarRegion.tsx
import { useLayoutStore } from '../stores/useLayoutStore'
import { getViewDefinition } from '../lib/module-registry'
import { RegionResize } from './RegionResize'
import type { SidebarRegion as SidebarRegionType } from '../types/tab'

interface Props {
  region: SidebarRegionType
  /** Which side the resize handle sits on */
  side: 'left' | 'right'
}

export function SidebarRegion({ region, side }: Props) {
  const regionState = useLayoutStore((s) => s.regions[region])
  const setRegionWidth = useLayoutStore((s) => s.setRegionWidth)
  const toggleRegion = useLayoutStore((s) => s.toggleRegion)
  const setActiveView = useLayoutStore((s) => s.setActiveView)

  const { views, activeViewId, width, mode } = regionState

  // No views registered for this region — don't render anything
  if (views.length === 0) return null

  const activeView = activeViewId ? getViewDefinition(activeViewId) : undefined

  // Collapsed mode — render narrow indicator bar
  if (mode === 'collapsed') {
    return (
      <div
        data-testid="collapsed-bar"
        className="shrink-0 w-6 bg-surface-tertiary border-border-subtle flex flex-col items-center pt-2 gap-1 cursor-pointer hover:bg-surface-hover transition-colors"
        style={{ borderLeftWidth: side === 'left' ? 1 : 0, borderRightWidth: side === 'right' ? 1 : 0 }}
        onClick={() => toggleRegion(region)}
      >
        {views.map((viewId) => {
          const viewDef = getViewDefinition(viewId)
          if (!viewDef) return null
          return (
            <div
              key={viewId}
              className={`w-5 h-5 flex items-center justify-center rounded text-xs ${
                viewId === activeViewId ? 'text-text-primary' : 'text-text-muted'
              }`}
              title={viewDef.label}
            >
              {viewDef.label.charAt(0)}
            </div>
          )
        })}
      </div>
    )
  }

  // Expanded (pinned or default) — render full panel
  const ActiveComponent = activeView?.component

  const resizeHandle = (
    <RegionResize
      side={side}
      onResize={(delta) => setRegionWidth(region, width + delta)}
    />
  )

  return (
    <div className="shrink-0 flex" style={{ width }}>
      {side === 'left' && resizeHandle}
      <div className="flex-1 flex flex-col min-w-0 bg-surface-tertiary border-border-subtle"
        style={{ borderLeftWidth: side === 'right' ? 1 : 0, borderRightWidth: side === 'left' ? 1 : 0 }}
      >
        {/* View tabs — only show if multiple views */}
        {views.length > 1 && (
          <div className="shrink-0 flex items-center gap-0.5 px-1 py-1 border-b border-border-subtle">
            {views.map((viewId) => {
              const viewDef = getViewDefinition(viewId)
              if (!viewDef) return null
              return (
                <button
                  key={viewId}
                  className={`px-2 py-0.5 rounded text-xs transition-colors ${
                    viewId === activeViewId
                      ? 'bg-surface-active text-text-primary'
                      : 'text-text-muted hover:text-text-primary hover:bg-surface-hover'
                  }`}
                  onClick={() => setActiveView(region, viewId)}
                >
                  {viewDef.label}
                </button>
              )
            })}
          </div>
        )}

        {/* View content */}
        <div className="flex-1 overflow-hidden">
          {ActiveComponent && <ActiveComponent isActive={true} />}
        </div>
      </div>
      {side === 'right' && resizeHandle}
    </div>
  )
}
```

- [ ] **Step 6: Run SidebarRegion tests**

Run: `cd spa && npx vitest run src/components/SidebarRegion.test.tsx`

- [ ] **Step 7: Commit**

```bash
git add spa/src/components/SidebarRegion.tsx spa/src/components/SidebarRegion.test.tsx spa/src/components/RegionResize.tsx spa/src/components/RegionResize.test.tsx
git commit -m "feat: add SidebarRegion and RegionResize components"
```

---

### Task 3: App.tsx Layout Restructure

**Files:**
- Modify: `spa/src/App.tsx`

Restructure the App layout to:
1. Replace the inline Electron title bar with `<TitleBar />`
2. Move TabBar below TitleBar (not embedded in it)
3. Add 4 SidebarRegion slots around the content area

- [ ] **Step 1: Understand the target layout**

The new flex structure should be:

```
<div h-screen flex flex-col>
  {isElectron && <TitleBar />}           ← NEW: dedicated title bar
  <div flex-1 flex min-h-0>             ← main row
    <ActivityBar />                      ← unchanged
    <SidebarRegion primary-sidebar />    ← NEW: left outer (full height)
    <div flex-1 flex flex-col min-w-0>   ← inner column
      <TabBar />                         ← MOVED: always here, not in title bar
      <div flex-1 flex overflow-hidden>  ← content row
        <SidebarRegion primary-panel />  ← NEW: left inner
        <TabContent />                   ← unchanged
        <SidebarRegion secondary-panel />← NEW: right inner
      </div>
      <StatusBar />                      ← unchanged
    </div>
    <SidebarRegion secondary-sidebar />  ← NEW: right outer (full height)
  </div>
</div>
```

- [ ] **Step 2: Modify App.tsx**

Key changes:
1. Import `TitleBar` and `SidebarRegion`
2. Replace the Electron title bar `<div>` block with `<TitleBar title={...} />`
3. Remove the `embedded` prop from TabBar — it's no longer inside the title bar
4. Both Electron and SPA modes now have TabBar in the same position (inside the inner column)
5. Add 4 `<SidebarRegion>` components around the content area
6. Derive the title string from active workspace name or active tab

The TabBar is no longer embedded in the Electron title bar. It sits below the title bar, inside the inner column, just like SPA mode. This means we can remove the conditional Electron/SPA TabBar rendering and have a single TabBar.

- [ ] **Step 3: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All PASS

- [ ] **Step 4: Run lint**

Run: `cd spa && pnpm run lint`

- [ ] **Step 5: Commit**

```bash
git add spa/src/App.tsx
git commit -m "refactor: restructure App layout with TitleBar and 4 SidebarRegions"
```

---

### Task 4: Lint + Full Verification

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

```bash
git add -A
git commit -m "fix: resolve issues from UI chrome restructure"
```
