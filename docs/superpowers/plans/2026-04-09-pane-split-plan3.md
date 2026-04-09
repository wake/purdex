# Pane Split (Plan 3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add pane splitting, resizing, layout patterns, New Pane Page, and a Files Module with daemon API — completing the tab-internal split system.

**Architecture:** Extend the existing `PaneLayout` tree (`leaf | split`) with tree-manipulation utilities in `pane-tree.ts`, wire them through `useTabStore` actions, render splits in `PaneLayoutRenderer` with `PaneSplitter` drag handles, add layout pattern buttons to `TitleBar`, create a `NewPanePage` for content selection, and build a `files` daemon module + `FileTreeView` sidebar view.

**Tech Stack:** React 19, Zustand 5, Tailwind 4, Vitest, Phosphor Icons, Go net/http (daemon)

**Prerequisites:** Plan 1+2 completed (Module Registry, Layout Store, TitleBar, SidebarRegion, RegionResize)

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `spa/src/lib/pane-tree.ts` | Add splitAtPane, removePane, countLeaves, collectLeaves, applyLayoutPattern |
| Modify | `spa/src/lib/pane-tree.test.ts` | Tests for new tree utilities |
| Modify | `spa/src/types/tab.ts` | Add LayoutPattern type |
| Modify | `spa/src/stores/useTabStore.ts` | Implement splitPane, closePane, add resizePanes, applyLayout, detachPane |
| Modify | `spa/src/stores/useTabStore.test.ts` | Tests for new actions |
| Create | `spa/src/components/PaneSplitter.tsx` | Drag handle between split panes |
| Create | `spa/src/components/PaneSplitter.test.tsx` | Tests |
| Modify | `spa/src/components/PaneLayoutRenderer.tsx` | Render split layouts with PaneSplitter |
| Modify | `spa/src/components/PaneLayoutRenderer.test.tsx` | Tests for split rendering |
| Create | `spa/src/components/NewPanePage.tsx` | Module selection page for new split panes |
| Create | `spa/src/components/NewPanePage.test.tsx` | Tests |
| Modify | `spa/src/components/TitleBar.tsx` | Wire layout pattern buttons |
| Modify | `spa/src/components/TitleBar.test.tsx` | Tests |
| Create | `internal/module/files/module.go` | Files daemon module |
| Create | `internal/module/files/handler.go` | File listing API handler |
| Modify | `cmd/tbox/main.go` | Register files module |
| Create | `spa/src/components/FileTreeView.tsx` | File tree sidebar view |
| Create | `spa/src/components/FileTreeView.test.tsx` | Tests |
| Modify | `spa/src/lib/register-modules.tsx` | Register files module with FileTreeView |
| Create | `spa/src/components/PaneHeader.tsx` | Pane header bar with close/detach buttons |
| Create | `spa/src/components/PaneHeader.test.tsx` | Tests |
| Modify | `spa/src/components/TabContent.tsx` | Pass tabId to PaneLayoutRenderer |

**Not in scope (deferred):**
- `attachToTab` — cross-tab merge operation, needs target-selection UI (future)
- Per-workspace sidebar overrides — spec 3.4, no store wiring yet
- Pane drag-to-split (Phase 2) / Pane drag-swap (Phase 3)

---

### Task 1: Pane Tree Utilities

**Files:**
- Modify: `spa/src/types/tab.ts`
- Modify: `spa/src/lib/pane-tree.ts`
- Modify: `spa/src/lib/pane-tree.test.ts`

- [ ] **Step 1: Add LayoutPattern type**

In `spa/src/types/tab.ts`, add after the `PaneLayout` type:

```ts
export type LayoutPattern = 'single' | 'split-h' | 'split-v' | 'grid-4'
```

- [ ] **Step 2: Write failing tests for splitAtPane**

In `spa/src/lib/pane-tree.test.ts`, add:

```ts
import { splitAtPane, removePane, countLeaves, collectLeaves, applyLayoutPattern } from './pane-tree'
import type { PaneLayout, PaneContent, Pane } from '../types/tab'

const leaf = (id: string, kind: string = 'dashboard'): PaneLayout => ({
  type: 'leaf',
  pane: { id, content: { kind } as PaneContent },
})

const split = (id: string, dir: 'h' | 'v', children: PaneLayout[], sizes?: number[]): PaneLayout => ({
  type: 'split',
  id,
  direction: dir,
  children,
  sizes: sizes ?? children.map(() => 100 / children.length),
})

describe('splitAtPane', () => {
  it('splits a leaf pane into a horizontal split', () => {
    const layout = leaf('p1')
    const result = splitAtPane(layout, 'p1', 'h', { kind: 'new-tab' })
    expect(result.type).toBe('split')
    if (result.type !== 'split') throw new Error('expected split')
    expect(result.direction).toBe('h')
    expect(result.children).toHaveLength(2)
    expect(result.children[0]).toEqual(layout) // original preserved
    expect(result.sizes).toEqual([50, 50])
    if (result.children[1].type !== 'leaf') throw new Error('expected leaf')
    expect(result.children[1].pane.content.kind).toBe('new-tab')
  })

  it('splits a nested pane by traversing the tree', () => {
    const layout = split('s1', 'h', [leaf('p1'), leaf('p2')])
    const result = splitAtPane(layout, 'p2', 'v', { kind: 'new-tab' })
    expect(result.type).toBe('split')
    if (result.type !== 'split') throw new Error('expected split')
    expect(result.children[0]).toEqual(leaf('p1')) // untouched
    expect(result.children[1].type).toBe('split') // p2 is now a split
  })

  it('returns layout unchanged when paneId not found', () => {
    const layout = leaf('p1')
    const result = splitAtPane(layout, 'nonexistent', 'h', { kind: 'new-tab' })
    expect(result).toBe(layout) // reference equality
  })
})
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/pane-tree.test.ts`
Expected: FAIL — `splitAtPane` is not exported

- [ ] **Step 4: Implement splitAtPane**

In `spa/src/lib/pane-tree.ts`, add:

```ts
import { generateId } from './id'

export function splitAtPane(
  layout: PaneLayout,
  paneId: string,
  direction: 'h' | 'v',
  newContent: PaneContent,
): PaneLayout {
  if (layout.type === 'leaf') {
    if (layout.pane.id === paneId) {
      return {
        type: 'split',
        id: generateId(),
        direction,
        children: [
          layout,
          { type: 'leaf', pane: { id: generateId(), content: newContent } },
        ],
        sizes: [50, 50],
      }
    }
    return layout
  }
  const newChildren = layout.children.map((child) =>
    splitAtPane(child, paneId, direction, newContent),
  )
  return newChildren.some((c, i) => c !== layout.children[i])
    ? { ...layout, children: newChildren }
    : layout
}
```

Add `PaneContent` to the import at the top of the file:

```ts
import type { Pane, PaneContent, PaneLayout } from '../types/tab'
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/pane-tree.test.ts`
Expected: splitAtPane tests PASS

- [ ] **Step 6: Write failing tests for removePane, countLeaves, collectLeaves**

```ts
describe('removePane', () => {
  it('returns null when removing the only leaf', () => {
    expect(removePane(leaf('p1'), 'p1')).toBeNull()
  })

  it('promotes sibling when removing one child of a split', () => {
    const layout = split('s1', 'h', [leaf('p1'), leaf('p2')])
    const result = removePane(layout, 'p1')
    expect(result).toEqual(leaf('p2'))
  })

  it('redistributes sizes when removing from 3-child split', () => {
    const layout = split('s1', 'h', [leaf('p1'), leaf('p2'), leaf('p3')], [25, 50, 25])
    const result = removePane(layout, 'p2')
    expect(result?.type).toBe('split')
    if (result?.type !== 'split') throw new Error('expected split')
    expect(result.children).toHaveLength(2)
    // 25 + 25 = 50 total, normalized: [50, 50]
    expect(result.sizes[0]).toBeCloseTo(50)
    expect(result.sizes[1]).toBeCloseTo(50)
  })

  it('returns layout unchanged when paneId not found', () => {
    const layout = split('s1', 'h', [leaf('p1'), leaf('p2')])
    const result = removePane(layout, 'nonexistent')
    expect(result).toEqual(layout)
  })
})

describe('countLeaves', () => {
  it('returns 1 for a leaf', () => {
    expect(countLeaves(leaf('p1'))).toBe(1)
  })

  it('counts leaves in nested splits', () => {
    const layout = split('s1', 'h', [
      leaf('p1'),
      split('s2', 'v', [leaf('p2'), leaf('p3')]),
    ])
    expect(countLeaves(layout)).toBe(3)
  })
})

describe('collectLeaves', () => {
  it('collects all leaf panes in order', () => {
    const layout = split('s1', 'h', [
      leaf('p1'),
      split('s2', 'v', [leaf('p2'), leaf('p3')]),
    ])
    const leaves = collectLeaves(layout)
    expect(leaves.map((p) => p.id)).toEqual(['p1', 'p2', 'p3'])
  })
})
```

- [ ] **Step 7: Implement removePane, countLeaves, collectLeaves**

```ts
export function removePane(layout: PaneLayout, paneId: string): PaneLayout | null {
  if (layout.type === 'leaf') {
    return layout.pane.id === paneId ? null : layout
  }
  const newChildren = layout.children
    .map((child) => removePane(child, paneId))
    .filter((c): c is PaneLayout => c !== null)

  if (newChildren.length === layout.children.length) return layout // nothing removed
  if (newChildren.length === 0) return null
  if (newChildren.length === 1) return newChildren[0]

  const removedIndices = new Set(
    layout.children
      .map((child, i) => (removePane(child, paneId) === null ? i : -1))
      .filter((i) => i >= 0),
  )
  const keptSizes = layout.sizes.filter((_, i) => !removedIndices.has(i))
  const total = keptSizes.reduce((a, b) => a + b, 0)
  const normalizedSizes = keptSizes.map((s) => (s / total) * 100)

  return { ...layout, children: newChildren, sizes: normalizedSizes }
}

export function countLeaves(layout: PaneLayout): number {
  if (layout.type === 'leaf') return 1
  return layout.children.reduce((sum, child) => sum + countLeaves(child), 0)
}

export function collectLeaves(layout: PaneLayout): Pane[] {
  if (layout.type === 'leaf') return [layout.pane]
  return layout.children.flatMap((child) => collectLeaves(child))
}
```

- [ ] **Step 8: Run tests**

Run: `cd spa && npx vitest run src/lib/pane-tree.test.ts`
Expected: All PASS

- [ ] **Step 9: Write failing tests for applyLayoutPattern**

```ts
describe('applyLayoutPattern', () => {
  it('single: flattens split to primary pane', () => {
    const layout = split('s1', 'h', [leaf('p1'), leaf('p2')])
    const result = applyLayoutPattern(layout, 'single')
    expect(result.type).toBe('leaf')
    if (result.type !== 'leaf') throw new Error('expected leaf')
    expect(result.pane.id).toBe('p1')
  })

  it('split-h: creates horizontal split from single pane', () => {
    const layout = leaf('p1')
    const result = applyLayoutPattern(layout, 'split-h')
    expect(result.type).toBe('split')
    if (result.type !== 'split') throw new Error('expected split')
    expect(result.direction).toBe('h')
    expect(result.children).toHaveLength(2)
    expect(result.children[0].type).toBe('leaf')
    if (result.children[0].type === 'leaf') expect(result.children[0].pane.id).toBe('p1')
    if (result.children[1].type === 'leaf') expect(result.children[1].pane.content.kind).toBe('new-tab')
  })

  it('split-h: preserves second pane if it exists', () => {
    const layout = split('s1', 'v', [leaf('p1'), leaf('p2')])
    const result = applyLayoutPattern(layout, 'split-h')
    if (result.type !== 'split') throw new Error('expected split')
    expect(result.direction).toBe('h')
    if (result.children[1].type === 'leaf') expect(result.children[1].pane.id).toBe('p2')
  })

  it('grid-4: creates 2x2 grid', () => {
    const layout = leaf('p1')
    const result = applyLayoutPattern(layout, 'grid-4')
    expect(result.type).toBe('split')
    if (result.type !== 'split') throw new Error('expected split')
    expect(result.direction).toBe('v')
    expect(result.children).toHaveLength(2)
    for (const row of result.children) {
      expect(row.type).toBe('split')
      if (row.type === 'split') {
        expect(row.direction).toBe('h')
        expect(row.children).toHaveLength(2)
      }
    }
  })
})
```

- [ ] **Step 10: Implement applyLayoutPattern**

```ts
import type { Pane, PaneContent, PaneLayout, LayoutPattern } from '../types/tab'

function newTabPane(): Pane {
  return { id: generateId(), content: { kind: 'new-tab' } }
}

export function applyLayoutPattern(layout: PaneLayout, pattern: LayoutPattern): PaneLayout {
  const leaves = collectLeaves(layout)
  const p = (i: number): Pane => leaves[i] ?? newTabPane()

  switch (pattern) {
    case 'single':
      return { type: 'leaf', pane: leaves[0] }
    case 'split-h':
      return {
        type: 'split', id: generateId(), direction: 'h',
        children: [{ type: 'leaf', pane: p(0) }, { type: 'leaf', pane: p(1) }],
        sizes: [50, 50],
      }
    case 'split-v':
      return {
        type: 'split', id: generateId(), direction: 'v',
        children: [{ type: 'leaf', pane: p(0) }, { type: 'leaf', pane: p(1) }],
        sizes: [50, 50],
      }
    case 'grid-4':
      return {
        type: 'split', id: generateId(), direction: 'v',
        children: [
          {
            type: 'split', id: generateId(), direction: 'h',
            children: [{ type: 'leaf', pane: p(0) }, { type: 'leaf', pane: p(1) }],
            sizes: [50, 50],
          },
          {
            type: 'split', id: generateId(), direction: 'h',
            children: [{ type: 'leaf', pane: p(2) }, { type: 'leaf', pane: p(3) }],
            sizes: [50, 50],
          },
        ],
        sizes: [50, 50],
      }
  }
}
```

- [ ] **Step 11: Run all pane-tree tests**

Run: `cd spa && npx vitest run src/lib/pane-tree.test.ts`
Expected: All PASS

- [ ] **Step 12: Commit**

```bash
git add spa/src/types/tab.ts spa/src/lib/pane-tree.ts spa/src/lib/pane-tree.test.ts
git commit -m "feat: add pane-tree split/remove/pattern utilities"
```

---

### Task 2: Tab Store Split Actions

**Files:**
- Modify: `spa/src/stores/useTabStore.ts`
- Create: `spa/src/stores/useTabStore.split.test.ts` (separate test file for split actions)

- [ ] **Step 1: Write failing tests**

Create `spa/src/stores/useTabStore.split.test.ts`:

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import type { PaneLayout } from '../types/tab'

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
})

describe('splitPane', () => {
  it('splits a leaf pane into horizontal split with new-tab content', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.getState().addTab(tab)
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''

    useTabStore.getState().splitPane(tab.id, paneId, 'h', { kind: 'new-tab' })

    const updated = useTabStore.getState().tabs[tab.id]
    expect(updated.layout.type).toBe('split')
    if (updated.layout.type === 'split') {
      expect(updated.layout.direction).toBe('h')
      expect(updated.layout.children).toHaveLength(2)
    }
  })

  it('is a no-op for nonexistent tab', () => {
    useTabStore.getState().splitPane('no-tab', 'no-pane', 'h', { kind: 'new-tab' })
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(0)
  })
})

describe('closePane', () => {
  it('closes tab when closing the only pane', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.getState().addTab(tab)
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''

    useTabStore.getState().closePane(tab.id, paneId)

    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
  })

  it('promotes sibling when closing one pane of a split', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.getState().addTab(tab)
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
    useTabStore.getState().splitPane(tab.id, paneId, 'h', { kind: 'new-tab' })

    // Close the original pane
    useTabStore.getState().closePane(tab.id, paneId)

    const updated = useTabStore.getState().tabs[tab.id]
    expect(updated).toBeDefined()
    expect(updated.layout.type).toBe('leaf') // promoted
  })
})

describe('resizePanes', () => {
  it('updates sizes array on a split node', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.getState().addTab(tab)
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
    useTabStore.getState().splitPane(tab.id, paneId, 'h', { kind: 'new-tab' })

    const updated = useTabStore.getState().tabs[tab.id]
    if (updated.layout.type !== 'split') throw new Error('expected split')
    const splitId = updated.layout.id

    useTabStore.getState().resizePanes(tab.id, splitId, [30, 70])

    const final = useTabStore.getState().tabs[tab.id]
    if (final.layout.type !== 'split') throw new Error('expected split')
    expect(final.layout.sizes).toEqual([30, 70])
  })
})

describe('applyLayout', () => {
  it('applies grid-4 pattern', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.getState().addTab(tab)

    useTabStore.getState().applyLayout(tab.id, 'grid-4')

    const updated = useTabStore.getState().tabs[tab.id]
    expect(updated.layout.type).toBe('split')
    if (updated.layout.type === 'split') {
      expect(updated.layout.children).toHaveLength(2)
    }
  })

  it('applies single pattern to flatten split', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.getState().addTab(tab)
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
    useTabStore.getState().splitPane(tab.id, paneId, 'h', { kind: 'new-tab' })

    useTabStore.getState().applyLayout(tab.id, 'single')

    const updated = useTabStore.getState().tabs[tab.id]
    expect(updated.layout.type).toBe('leaf')
  })
})

describe('detachPane', () => {
  it('detaches a pane from split, creates new tab', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.getState().addTab(tab)
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
    useTabStore.getState().splitPane(tab.id, paneId, 'h', { kind: 'history' })

    const updated = useTabStore.getState().tabs[tab.id]
    if (updated.layout.type !== 'split') throw new Error('expected split')
    const secondPaneId = updated.layout.children[1].type === 'leaf'
      ? updated.layout.children[1].pane.id : ''

    const newTabId = useTabStore.getState().detachPane(tab.id, secondPaneId)

    expect(newTabId).toBeDefined()
    if (!newTabId) throw new Error('expected new tab id')
    // Original tab should be back to single pane
    expect(useTabStore.getState().tabs[tab.id].layout.type).toBe('leaf')
    // New tab should have the detached content
    const newTab = useTabStore.getState().tabs[newTabId]
    expect(newTab).toBeDefined()
    if (newTab.layout.type === 'leaf') {
      expect(newTab.layout.pane.content.kind).toBe('history')
    }
  })

  it('returns null when detaching from single-pane tab', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.getState().addTab(tab)
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''

    const result = useTabStore.getState().detachPane(tab.id, paneId)
    expect(result).toBeNull()
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/stores/useTabStore.split.test.ts`
Expected: FAIL — resizePanes, applyLayout, detachPane not defined

- [ ] **Step 3: Implement Tab Store actions**

In `spa/src/stores/useTabStore.ts`:

Add imports:

```ts
import { splitAtPane, removePane, collectLeaves, applyLayoutPattern } from '../lib/pane-tree'
import type { Tab, PaneContent, PaneLayout, TerminatedReason, LayoutPattern } from '../types/tab'
```

Add to `TabState` interface:

```ts
  resizePanes: (tabId: string, splitId: string, sizes: number[]) => void
  applyLayout: (tabId: string, pattern: LayoutPattern) => void
  detachPane: (tabId: string, paneId: string) => string | null
```

Replace the `splitPane` stub:

```ts
      splitPane: (tabId, paneId, direction, content) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const newLayout = splitAtPane(tab.layout, paneId, direction, content)
          if (newLayout === tab.layout) return state
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),
```

Replace the `closePane` stub:

```ts
      closePane: (tabId, paneId) => {
        const state = get()
        const tab = state.tabs[tabId]
        if (!tab) return
        const newLayout = removePane(tab.layout, paneId)
        if (newLayout === null) {
          // Only pane — close the entire tab
          get().closeTab(tabId)
          return
        }
        set({ tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } })
      },
```

Add `resizePanes`:

```ts
      resizePanes: (tabId, splitId, sizes) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const update = (layout: PaneLayout): PaneLayout => {
            if (layout.type === 'leaf') return layout
            if (layout.id === splitId) return { ...layout, sizes }
            const newChildren = layout.children.map(update)
            return newChildren.some((c, i) => c !== layout.children[i])
              ? { ...layout, children: newChildren }
              : layout
          }
          const newLayout = update(tab.layout)
          if (newLayout === tab.layout) return state
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),
```

Add `applyLayout`:

```ts
      applyLayout: (tabId, pattern) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const newLayout = applyLayoutPattern(tab.layout, pattern)
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),
```

Add `detachPane`:

```ts
      detachPane: (tabId, paneId) => {
        const state = get()
        const tab = state.tabs[tabId]
        if (!tab) return null
        const pane = findPane(tab.layout, paneId)
        if (!pane) return null
        // Don't detach from single-pane tab
        if (tab.layout.type === 'leaf') return null
        const newLayout = removePane(tab.layout, paneId)
        if (!newLayout) return null
        // Create new tab with the detached pane's content
        const newTab = createTab(pane.content)
        set({
          tabs: {
            ...state.tabs,
            [tabId]: { ...tab, layout: newLayout },
            [newTab.id]: newTab,
          },
          tabOrder: [...state.tabOrder, newTab.id],
        })
        return newTab.id
      },
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/stores/useTabStore.split.test.ts`
Expected: All PASS

- [ ] **Step 5: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.split.test.ts
git commit -m "feat: implement splitPane, closePane, resizePanes, applyLayout, detachPane"
```

---

### Task 3: PaneSplitter Component

**Files:**
- Create: `spa/src/components/PaneSplitter.tsx`
- Create: `spa/src/components/PaneSplitter.test.tsx`

- [ ] **Step 1: Write failing tests**

Create `spa/src/components/PaneSplitter.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import { PaneSplitter } from './PaneSplitter'

describe('PaneSplitter', () => {
  it('renders a horizontal drag handle', () => {
    const { container } = render(<PaneSplitter direction="h" onResize={vi.fn()} />)
    expect(container.firstElementChild).toBeDefined()
    expect(container.firstElementChild?.className).toContain('cursor-col-resize')
  })

  it('renders a vertical drag handle', () => {
    const { container } = render(<PaneSplitter direction="v" onResize={vi.fn()} />)
    expect(container.firstElementChild).toBeDefined()
    expect(container.firstElementChild?.className).toContain('cursor-row-resize')
  })

  it('calls onResize with pixel delta during drag', () => {
    const onResize = vi.fn()
    const { container } = render(<PaneSplitter direction="h" onResize={onResize} />)
    const handle = container.firstElementChild as HTMLElement

    fireEvent.mouseDown(handle, { clientX: 100, clientY: 100 })
    fireEvent.mouseMove(document, { clientX: 150, clientY: 100 })
    fireEvent.mouseUp(document)

    expect(onResize).toHaveBeenCalledWith(50)
  })

  it('uses clientY delta for vertical direction', () => {
    const onResize = vi.fn()
    const { container } = render(<PaneSplitter direction="v" onResize={onResize} />)
    const handle = container.firstElementChild as HTMLElement

    fireEvent.mouseDown(handle, { clientX: 100, clientY: 200 })
    fireEvent.mouseMove(document, { clientX: 100, clientY: 250 })
    fireEvent.mouseUp(document)

    expect(onResize).toHaveBeenCalledWith(50)
  })
})
```

- [ ] **Step 2: Implement PaneSplitter**

Create `spa/src/components/PaneSplitter.tsx`:

```tsx
import { useCallback, useEffect, useRef } from 'react'

interface Props {
  direction: 'h' | 'v'
  onResize: (deltaPx: number) => void
}

export function PaneSplitter({ direction, onResize }: Props) {
  const startPos = useRef(0)
  const onResizeRef = useRef(onResize)
  useEffect(() => { onResizeRef.current = onResize })

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    startPos.current = direction === 'h' ? e.clientX : e.clientY

    const handleMouseMove = (moveEvent: MouseEvent) => {
      const current = direction === 'h' ? moveEvent.clientX : moveEvent.clientY
      const delta = current - startPos.current
      onResizeRef.current(delta)
      startPos.current = current
    }

    const handleMouseUp = () => {
      document.removeEventListener('mousemove', handleMouseMove)
      document.removeEventListener('mouseup', handleMouseUp)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }

    document.addEventListener('mousemove', handleMouseMove)
    document.addEventListener('mouseup', handleMouseUp)
    document.body.style.cursor = direction === 'h' ? 'col-resize' : 'row-resize'
    document.body.style.userSelect = 'none'
  }, [direction])

  return (
    <div
      className={`shrink-0 ${
        direction === 'h'
          ? 'w-1 cursor-col-resize'
          : 'h-1 cursor-row-resize'
      } hover:bg-accent-base/30 active:bg-accent-base/50 transition-colors`}
      onMouseDown={handleMouseDown}
    />
  )
}
```

- [ ] **Step 3: Run tests**

Run: `cd spa && npx vitest run src/components/PaneSplitter.test.tsx`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/PaneSplitter.tsx spa/src/components/PaneSplitter.test.tsx
git commit -m "feat: add PaneSplitter drag handle component"
```

---

### Task 4: PaneLayoutRenderer Split Rendering

**Files:**
- Modify: `spa/src/components/PaneLayoutRenderer.tsx`
- Modify: `spa/src/components/PaneLayoutRenderer.test.tsx`

- [ ] **Step 1: Write failing tests for split rendering**

Add to `spa/src/components/PaneLayoutRenderer.test.tsx`:

```tsx
  it('renders all children of a split layout with splitters', () => {
    registerModule({
      id: 'dashboard-multi',
      name: 'Dashboard',
      pane: {
        kind: 'dashboard',
        component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div>,
      },
    })
    const layout: PaneLayout = {
      type: 'split',
      id: 's1',
      direction: 'h',
      children: [
        { type: 'leaf', pane: { id: 'left', content: { kind: 'dashboard' } } },
        { type: 'leaf', pane: { id: 'right', content: { kind: 'dashboard' } } },
      ],
      sizes: [50, 50],
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    expect(screen.getByTestId('dash-left')).toBeTruthy()
    expect(screen.getByTestId('dash-right')).toBeTruthy()
  })

  it('renders nested splits', () => {
    registerModule({
      id: 'dashboard-nested',
      name: 'Dashboard',
      pane: {
        kind: 'dashboard',
        component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div>,
      },
    })
    const layout: PaneLayout = {
      type: 'split',
      id: 's1',
      direction: 'v',
      children: [
        { type: 'leaf', pane: { id: 'top', content: { kind: 'dashboard' } } },
        {
          type: 'split',
          id: 's2',
          direction: 'h',
          children: [
            { type: 'leaf', pane: { id: 'bl', content: { kind: 'dashboard' } } },
            { type: 'leaf', pane: { id: 'br', content: { kind: 'dashboard' } } },
          ],
          sizes: [50, 50],
        },
      ],
      sizes: [50, 50],
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    expect(screen.getByTestId('dash-top')).toBeTruthy()
    expect(screen.getByTestId('dash-bl')).toBeTruthy()
    expect(screen.getByTestId('dash-br')).toBeTruthy()
  })
```

- [ ] **Step 2: Update existing tests and TabContent.tsx to pass tabId**

**Before** implementing, update all existing `PaneLayoutRenderer` call sites:

In `spa/src/components/TabContent.tsx` line 40, change:
```tsx
<PaneLayoutRenderer layout={tab.layout} isActive={isActive} />
```
to:
```tsx
<PaneLayoutRenderer layout={tab.layout} tabId={id} isActive={isActive} />
```

In `spa/src/components/PaneLayoutRenderer.test.tsx`, update ALL existing test renders to include `tabId="t1"`:
```tsx
// Every existing <PaneLayoutRenderer layout={layout} isActive={...} />
// becomes:
<PaneLayoutRenderer layout={layout} tabId="t1" isActive={...} />
```

- [ ] **Step 3: Run tests to verify new split tests fail, existing tests still pass**

Run: `cd spa && npx vitest run src/components/PaneLayoutRenderer.test.tsx`
Expected: New split tests FAIL (both children not rendered), existing tests PASS with tabId

- [ ] **Step 4: Implement split rendering**

Rewrite `spa/src/components/PaneLayoutRenderer.tsx`:

```tsx
import { getPaneRenderer } from '../lib/module-registry'
import { getLayoutKey } from '../lib/pane-tree'
import { PaneSplitter } from './PaneSplitter'
import { useTabStore } from '../stores/useTabStore'
import type { PaneLayout } from '../types/tab'

interface Props {
  layout: PaneLayout
  tabId: string
  isActive: boolean
}

export function PaneLayoutRenderer({ layout, tabId, isActive }: Props) {
  if (layout.type === 'leaf') {
    const config = getPaneRenderer(layout.pane.content.kind)
    if (!config) {
      return (
        <div className="flex-1 flex items-center justify-center text-text-muted">
          No renderer for &quot;{layout.pane.content.kind}&quot;
        </div>
      )
    }
    const Component = config.component
    return <Component pane={layout.pane} isActive={isActive} />
  }

  if (layout.children.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-text-muted">
        Empty split layout
      </div>
    )
  }

  const handleResize = (index: number, deltaPx: number) => {
    const totalPercent = layout.sizes[index] + layout.sizes[index + 1]
    // Convert pixel delta to percent delta (approximate using container size)
    // For simplicity, assume 1px ≈ 0.1% (adjustable with actual container ref)
    const percentDelta = deltaPx * 0.1
    const newLeft = Math.max(10, Math.min(totalPercent - 10, layout.sizes[index] + percentDelta))
    const newRight = totalPercent - newLeft
    const newSizes = [...layout.sizes]
    newSizes[index] = newLeft
    newSizes[index + 1] = newRight
    useTabStore.getState().resizePanes(tabId, layout.id, newSizes)
  }

  return (
    <div className={`flex-1 flex ${layout.direction === 'h' ? 'flex-row' : 'flex-col'} overflow-hidden`}>
      {layout.children.map((child, i) => (
        <div key={getLayoutKey(child)} className="contents">
          {i > 0 && (
            <PaneSplitter
              direction={layout.direction}
              onResize={(delta) => handleResize(i - 1, delta)}
            />
          )}
          <div style={{ flex: `${layout.sizes[i]} 0 0%` }} className="min-w-0 min-h-0 flex overflow-hidden">
            <PaneLayoutRenderer
              layout={child}
              tabId={tabId}
              isActive={isActive}
            />
          </div>
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 5: Run all tests**

Run: `cd spa && npx vitest run`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/PaneLayoutRenderer.tsx spa/src/components/PaneLayoutRenderer.test.tsx spa/src/components/TabContent.tsx
git commit -m "feat: render split layouts with PaneSplitter in PaneLayoutRenderer"
```

---

### Task 5: TitleBar Layout Buttons Wiring

**Files:**
- Modify: `spa/src/components/TitleBar.tsx`
- Modify: `spa/src/components/TitleBar.test.tsx`

- [ ] **Step 1: Write failing test**

Add to `TitleBar.test.tsx`:

```tsx
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'

  it('calls applyLayout when layout button is clicked', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id, visitHistory: [] })

    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    // Buttons should be enabled when there's an active tab
    expect(buttons[0]).toHaveProperty('disabled', false)
    
    // Click "Split horizontal"
    fireEvent.click(buttons[1])
    const updated = useTabStore.getState().tabs[tab.id]
    expect(updated.layout.type).toBe('split')
  })

  it('keeps buttons disabled when no active tab', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    for (const btn of buttons) {
      expect(btn).toHaveProperty('disabled', true)
    }
  })
```

- [ ] **Step 2: Implement TitleBar button wiring**

Update `spa/src/components/TitleBar.tsx`:

```tsx
import { Columns, Rows, GridFour, Square } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import type { LayoutPattern } from '../types/tab'

interface Props {
  title: string
}

const patterns: { pattern: LayoutPattern; icon: typeof Square; label: string }[] = [
  { pattern: 'single', icon: Square, label: 'Single pane' },
  { pattern: 'split-h', icon: Columns, label: 'Split horizontal' },
  { pattern: 'split-v', icon: Rows, label: 'Split vertical' },
  { pattern: 'grid-4', icon: GridFour, label: 'Grid' },
]

export function TitleBar({ title }: Props) {
  const activeTabId = useTabStore((s) => s.activeTabId)

  const handlePattern = (pattern: LayoutPattern) => {
    if (!activeTabId) return
    useTabStore.getState().applyLayout(activeTabId, pattern)
  }

  return (
    <div
      className="shrink-0 flex items-center bg-surface-secondary border-b border-border-subtle px-2"
      style={{ height: 30, WebkitAppRegion: 'drag' } as React.CSSProperties}
    >
      <div className="shrink-0" style={{ width: 70 }} />

      <div className="flex-1 text-center text-xs text-text-muted truncate select-none">
        {title}
      </div>

      <div
        data-testid="layout-buttons"
        className="shrink-0 flex items-center gap-0.5"
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        {patterns.map(({ pattern, icon: Icon, label }) => (
          <button
            key={pattern}
            disabled={!activeTabId}
            className="p-1 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover disabled:opacity-40 disabled:pointer-events-none"
            title={label}
            onClick={() => handlePattern(pattern)}
          >
            <Icon size={14} />
          </button>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Run tests**

Run: `cd spa && npx vitest run src/components/TitleBar.test.tsx`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/TitleBar.tsx spa/src/components/TitleBar.test.tsx
git commit -m "feat: wire TitleBar layout buttons to applyLayout"
```

---

### Task 6: New Pane Page

**Files:**
- Create: `spa/src/components/NewPanePage.tsx`
- Create: `spa/src/components/NewPanePage.test.tsx`

- [ ] **Step 1: Write failing test**

Create `spa/src/components/NewPanePage.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { NewPanePage } from './NewPanePage'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

beforeEach(() => {
  clearModuleRegistry()
})

describe('NewPanePage', () => {
  it('renders a list of available pane modules', () => {
    registerModule({
      id: 'dashboard',
      name: 'Dashboard',
      pane: { kind: 'dashboard', component: () => null },
    })
    registerModule({
      id: 'history',
      name: 'History',
      pane: { kind: 'history', component: () => null },
    })

    render(<NewPanePage onSelect={vi.fn()} />)
    expect(screen.getByText('Dashboard')).toBeTruthy()
    expect(screen.getByText('History')).toBeTruthy()
  })

  it('calls onSelect with correct content when module is clicked', () => {
    registerModule({
      id: 'dashboard',
      name: 'Dashboard',
      pane: { kind: 'dashboard', component: () => null },
    })

    const onSelect = vi.fn()
    render(<NewPanePage onSelect={onSelect} />)
    fireEvent.click(screen.getByText('Dashboard'))

    expect(onSelect).toHaveBeenCalledWith({ kind: 'dashboard' })
  })

  it('skips modules without pane', () => {
    registerModule({ id: 'files', name: 'Files' })
    registerModule({
      id: 'dashboard',
      name: 'Dashboard',
      pane: { kind: 'dashboard', component: () => null },
    })

    render(<NewPanePage onSelect={vi.fn()} />)
    expect(screen.queryByText('Files')).toBeNull()
    expect(screen.getByText('Dashboard')).toBeTruthy()
  })
})
```

- [ ] **Step 2: Implement NewPanePage**

Create `spa/src/components/NewPanePage.tsx`:

```tsx
import { getModules } from '../lib/module-registry'
import type { PaneContent } from '../types/tab'

interface Props {
  onSelect: (content: PaneContent) => void
}

// Only kinds that need no extra parameters (no hostId, url, etc.)
const SIMPLE_KINDS = new Set(['dashboard', 'history', 'hosts', 'memory-monitor'])

export function NewPanePage({ onSelect }: Props) {
  const paneModules = getModules().filter((m) => m.pane && SIMPLE_KINDS.has(m.pane.kind))

  return (
    <div className="flex-1 flex flex-col items-center justify-center p-8">
      <h2 className="text-sm text-text-muted mb-4">Select content for this pane</h2>
      <div className="flex flex-wrap gap-2 max-w-md">
        {paneModules.map((m) => (
          <button
            key={m.id}
            className="px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
            onClick={() => onSelect({ kind: m.pane!.kind } as PaneContent)}
          >
            {m.name}
          </button>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Register NewPanePage as the new-tab pane renderer for split panes**

No registration change needed — the existing `new-tab` module already renders `NewTabPage` for `{ kind: 'new-tab' }` content. When a pane is split, the new pane gets `{ kind: 'new-tab' }` content, which renders the existing `NewTabPage`. The `NewPanePage` is an alternative for a more focused split-pane UX — decide at implementation time whether to replace or supplement.

**Alternative approach (simpler):** Keep using the existing `NewTabPage` for new split panes. The `NewPanePage` component is available as a focused alternative that can be swapped in later.

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/NewPanePage.test.tsx`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/NewPanePage.tsx spa/src/components/NewPanePage.test.tsx
git commit -m "feat: add NewPanePage for split pane content selection"
```

---

### Task 7: Daemon Files Module

**Files:**
- Create: `internal/module/files/module.go`
- Create: `internal/module/files/handler.go`
- Modify: `cmd/tbox/main.go`

- [ ] **Step 1: Create files module**

Create `internal/module/files/module.go`:

```go
package files

import (
	"context"
	"net/http"

	"github.com/wake/tmux-box/internal/core"
)

type FilesModule struct{}

func New() *FilesModule {
	return &FilesModule{}
}

func (m *FilesModule) Name() string            { return "files" }
func (m *FilesModule) Dependencies() []string   { return nil }
func (m *FilesModule) Init(_ *core.Core) error  { return nil }
func (m *FilesModule) Start(_ context.Context) error { return nil }
func (m *FilesModule) Stop(_ context.Context) error  { return nil }

func (m *FilesModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/files", m.handleList)
}
```

- [ ] **Step 2: Create handler**

Create `internal/module/files/handler.go`:

```go
package files

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FileEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

func (m *FilesModule) handleList(w http.ResponseWriter, r *http.Request) {
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			http.Error(w, "cannot determine home directory", http.StatusInternalServerError)
			return
		}
		dirPath = home
	}

	// Resolve to absolute and clean
	dirPath = filepath.Clean(dirPath)

	// Prevent path traversal: must be absolute
	if !filepath.IsAbs(dirPath) {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	result := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		// Skip hidden files by default (can be toggled later)
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, FileEntry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Size:  info.Size(),
		})
	}

	// Directories first, then files, both alphabetical
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return result[i].Name < result[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"path":    dirPath,
		"entries": result,
	})
}
```

- [ ] **Step 3: Register in main.go**

In `cmd/tbox/main.go`, add import:

```go
"github.com/wake/tmux-box/internal/module/files"
```

Add module registration (near other `c.AddModule` calls):

```go
c.AddModule(files.New())
```

- [ ] **Step 4: Build and test daemon**

Run: `go build ./cmd/tbox && echo "build OK"`
Expected: Build succeeds

Run daemon and test:
```bash
curl -s "http://100.64.0.2:7860/api/files?path=$HOME" | jq '.entries[:3]'
```
Expected: JSON array with file entries

- [ ] **Step 5: Commit**

```bash
git add internal/module/files/ cmd/tbox/main.go
git commit -m "feat(daemon): add files module with directory listing API"
```

---

### Task 8: FileTreeView + Files Module Registration

**Files:**
- Create: `spa/src/components/FileTreeView.tsx`
- Create: `spa/src/components/FileTreeView.test.tsx`
- Modify: `spa/src/lib/register-modules.tsx`

- [ ] **Step 1: Write failing tests**

Create `spa/src/components/FileTreeView.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { FileTreeView } from './FileTreeView'

const mockEntries = [
  { name: 'docs', isDir: true, size: 0 },
  { name: 'src', isDir: true, size: 0 },
  { name: 'README.md', isDir: false, size: 1024 },
]

beforeEach(() => {
  vi.restoreAllMocks()
  // Must set host state — FileTreeView reads baseUrl from useHostStore
  const { useHostStore } = await import('../stores/useHostStore')
  useHostStore.setState({
    hostOrder: ['test-host'],
    hosts: { 'test-host': { id: 'test-host', name: 'Test', url: 'http://localhost:7860', status: 'connected' } } as any,
  })
})

describe('FileTreeView', () => {
  it('renders file entries after fetch', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ path: '/home/user', entries: mockEntries }),
    } as Response)

    render(<FileTreeView isActive={true} />)

    await waitFor(() => {
      expect(screen.getByText('docs')).toBeTruthy()
      expect(screen.getByText('src')).toBeTruthy()
      expect(screen.getByText('README.md')).toBeTruthy()
    })
  })

  it('shows directories with folder icon', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({ path: '/home/user', entries: mockEntries }),
    } as Response)

    render(<FileTreeView isActive={true} />)

    await waitFor(() => {
      // Directories should have SVG icons (Phosphor FolderSimple)
      const docs = screen.getByText('docs')
      expect(docs.closest('[data-testid]')?.querySelector('svg')).toBeTruthy()
    })
  })

  it('shows error state on fetch failure', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new Error('Network error'))

    render(<FileTreeView isActive={true} />)

    await waitFor(() => {
      expect(screen.getByText(/error/i)).toBeTruthy()
    })
  })
})
```

- [ ] **Step 2: Implement FileTreeView**

Create `spa/src/components/FileTreeView.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import { FolderSimple, File, CaretRight, CaretDown } from '@phosphor-icons/react'
import { useHostStore } from '../stores/useHostStore'
import type { ViewProps } from '../lib/module-registry'

interface FileEntry {
  name: string
  isDir: boolean
  size: number
}

interface DirState {
  entries: FileEntry[]
  expanded: boolean
  loading: boolean
}

export function FileTreeView({ isActive }: ViewProps) {
  const hostOrder = useHostStore((s) => s.hostOrder)
  const hosts = useHostStore((s) => s.hosts)
  const firstHost = hostOrder[0] ? hosts[hostOrder[0]] : undefined
  const baseUrl = firstHost?.url ?? ''

  const [rootPath, setRootPath] = useState<string>('')
  const [rootEntries, setRootEntries] = useState<FileEntry[]>([])
  const [expandedDirs, setExpandedDirs] = useState<Record<string, DirState>>({})
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const fetchDir = useCallback(async (path?: string): Promise<{ path: string; entries: FileEntry[] }> => {
    const url = path
      ? `${baseUrl}/api/files?path=${encodeURIComponent(path)}`
      : `${baseUrl}/api/files`
    const res = await fetch(url)
    if (!res.ok) throw new Error(`Failed to fetch: ${res.statusText}`)
    return res.json()
  }, [baseUrl])

  useEffect(() => {
    if (!baseUrl) return
    setLoading(true)
    setError(null)
    fetchDir()
      .then((data) => {
        setRootPath(data.path)
        setRootEntries(data.entries)
      })
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false))
  }, [baseUrl, fetchDir])

  const toggleDir = useCallback(async (fullPath: string) => {
    const existing = expandedDirs[fullPath]
    if (existing?.expanded) {
      setExpandedDirs((prev) => ({
        ...prev,
        [fullPath]: { ...prev[fullPath], expanded: false },
      }))
      return
    }
    if (existing?.entries.length) {
      setExpandedDirs((prev) => ({
        ...prev,
        [fullPath]: { ...prev[fullPath], expanded: true },
      }))
      return
    }

    setExpandedDirs((prev) => ({
      ...prev,
      [fullPath]: { entries: [], expanded: true, loading: true },
    }))

    try {
      const data = await fetchDir(fullPath)
      setExpandedDirs((prev) => ({
        ...prev,
        [fullPath]: { entries: data.entries, expanded: true, loading: false },
      }))
    } catch {
      setExpandedDirs((prev) => ({
        ...prev,
        [fullPath]: { entries: [], expanded: false, loading: false },
      }))
    }
  }, [expandedDirs, fetchDir])

  if (!baseUrl) {
    return <div className="p-3 text-xs text-text-muted">No host connected</div>
  }

  if (loading) {
    return <div className="p-3 text-xs text-text-muted">Loading...</div>
  }

  if (error) {
    return <div className="p-3 text-xs text-red-400">Error: {error}</div>
  }

  const renderEntries = (entries: FileEntry[], parentPath: string, depth: number) => (
    <div>
      {entries.map((entry) => {
        const fullPath = `${parentPath}/${entry.name}`
        const dirState = expandedDirs[fullPath]
        const isExpanded = dirState?.expanded ?? false

        return (
          <div key={entry.name}>
            <button
              data-testid={`file-entry-${entry.name}`}
              className="w-full flex items-center gap-1 px-2 py-0.5 text-xs text-text-primary hover:bg-surface-hover transition-colors"
              style={{ paddingLeft: 8 + depth * 16 }}
              onClick={() => entry.isDir && toggleDir(fullPath)}
            >
              {entry.isDir ? (
                <>
                  {isExpanded ? <CaretDown size={12} /> : <CaretRight size={12} />}
                  <FolderSimple size={14} className="text-text-muted shrink-0" />
                </>
              ) : (
                <>
                  <span className="w-3" />
                  <File size={14} className="text-text-muted shrink-0" />
                </>
              )}
              <span className="truncate">{entry.name}</span>
            </button>
            {entry.isDir && isExpanded && dirState?.entries && (
              renderEntries(dirState.entries, fullPath, depth + 1)
            )}
          </div>
        )
      })}
    </div>
  )

  return (
    <div className="flex-1 overflow-auto text-xs">
      <div className="px-2 py-1 text-text-muted font-medium truncate border-b border-border-subtle">
        {rootPath}
      </div>
      {renderEntries(rootEntries, rootPath, 0)}
    </div>
  )
}
```

- [ ] **Step 3: Register Files Module**

In `spa/src/lib/register-modules.tsx`, add import:

```tsx
import { FileTreeView } from '../components/FileTreeView'
import { FolderOpen } from '@phosphor-icons/react'
```

Add inside `registerBuiltinModules()`, after the existing module registrations:

```tsx
  registerModule({
    id: 'files',
    name: 'Files',
    views: [{
      id: 'file-tree',
      label: 'Files',
      icon: FolderOpen,
      scope: 'workspace',
      defaultRegion: 'primary-panel',
      component: FileTreeView,
    }],
  })
```

- [ ] **Step 4: Wire FileTreeView to Layout Store on startup**

In `spa/src/main.tsx`, after `registerBuiltinModules()`, set default region views **only if empty** (to not overwrite persisted state):

```tsx
import { useLayoutStore } from './stores/useLayoutStore'

// After registerBuiltinModules():
const panelState = useLayoutStore.getState().regions['primary-panel']
if (panelState.views.length === 0) {
  useLayoutStore.getState().setRegionViews('primary-panel', ['file-tree'])
  useLayoutStore.getState().setActiveView('primary-panel', 'file-tree')
}
```

- [ ] **Step 5: Run tests**

Run: `cd spa && npx vitest run src/components/FileTreeView.test.tsx`
Expected: All PASS

Run: `cd spa && npx vitest run`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/FileTreeView.tsx spa/src/components/FileTreeView.test.tsx spa/src/lib/register-modules.tsx spa/src/main.tsx
git commit -m "feat: add Files Module with FileTreeView sidebar view"
```

---

### Task 9: PaneHeader (Close / Detach UI)

**Files:**
- Create: `spa/src/components/PaneHeader.tsx`
- Create: `spa/src/components/PaneHeader.test.tsx`
- Modify: `spa/src/components/PaneLayoutRenderer.tsx`

- [ ] **Step 1: Write failing tests**

Create `spa/src/components/PaneHeader.test.tsx`:

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { PaneHeader } from './PaneHeader'

describe('PaneHeader', () => {
  it('renders close button', () => {
    render(<PaneHeader title="Dashboard" onClose={vi.fn()} />)
    expect(screen.getByTitle('Close pane')).toBeTruthy()
  })

  it('calls onClose when close button clicked', () => {
    const onClose = vi.fn()
    render(<PaneHeader title="Dashboard" onClose={onClose} />)
    fireEvent.click(screen.getByTitle('Close pane'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('renders detach button when onDetach is provided', () => {
    render(<PaneHeader title="Dashboard" onClose={vi.fn()} onDetach={vi.fn()} />)
    expect(screen.getByTitle('Detach to tab')).toBeTruthy()
  })

  it('does not render detach button when onDetach is not provided', () => {
    render(<PaneHeader title="Dashboard" onClose={vi.fn()} />)
    expect(screen.queryByTitle('Detach to tab')).toBeNull()
  })

  it('hides header when isSinglePane is true', () => {
    const { container } = render(
      <PaneHeader title="Dashboard" onClose={vi.fn()} isSinglePane={true} />
    )
    expect(container.innerHTML).toBe('')
  })
})
```

- [ ] **Step 2: Implement PaneHeader**

Create `spa/src/components/PaneHeader.tsx`:

```tsx
import { X, ArrowSquareOut } from '@phosphor-icons/react'

interface Props {
  title: string
  onClose: () => void
  onDetach?: () => void
  isSinglePane?: boolean
}

export function PaneHeader({ title, onClose, onDetach, isSinglePane }: Props) {
  if (isSinglePane) return null

  return (
    <div className="shrink-0 flex items-center h-6 px-2 bg-surface-secondary border-b border-border-subtle">
      <span className="flex-1 text-xs text-text-muted truncate">{title}</span>
      <div className="flex items-center gap-0.5">
        {onDetach && (
          <button
            className="p-0.5 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
            title="Detach to tab"
            onClick={onDetach}
          >
            <ArrowSquareOut size={12} />
          </button>
        )}
        <button
          className="p-0.5 rounded text-text-muted hover:text-text-primary hover:bg-surface-hover transition-colors"
          title="Close pane"
          onClick={onClose}
        >
          <X size={12} />
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Integrate PaneHeader into PaneLayoutRenderer**

In `PaneLayoutRenderer.tsx`, wrap each leaf pane render with `PaneHeader`:

```tsx
import { PaneHeader } from './PaneHeader'
import { countLeaves } from '../lib/pane-tree'

// Inside the leaf branch, after getting Component:
const isSinglePane = /* pass from parent or check layout context */
const paneTitle = config.kind // or a better display name

return (
  <div className="flex-1 flex flex-col overflow-hidden">
    <PaneHeader
      title={layout.pane.content.kind}
      isSinglePane={isSinglePane}
      onClose={() => useTabStore.getState().closePane(tabId, layout.pane.id)}
      onDetach={() => useTabStore.getState().detachPane(tabId, layout.pane.id)}
    />
    <Component pane={layout.pane} isActive={isActive} />
  </div>
)
```

Pass `isSinglePane` by checking `layout === rootLayout` or adding a prop. Simplest: pass a `showPaneHeader` boolean prop from the split branch (true when there are siblings).

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/components/PaneHeader.test.tsx`
Expected: All PASS

Run: `cd spa && npx vitest run`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/PaneHeader.tsx spa/src/components/PaneHeader.test.tsx spa/src/components/PaneLayoutRenderer.tsx
git commit -m "feat: add PaneHeader with close/detach buttons for split panes"
```

---

## Execution Order

```
Task 1 (pane-tree utils)
  └─► Task 2 (Tab Store actions)
        ├─► Task 3 (PaneSplitter) ─► Task 4 (PaneLayoutRenderer) ─► Task 9 (PaneHeader)
        └─► Task 5 (TitleBar wiring)
Task 6 (New Pane Page) — independent
Task 7 (Daemon files) → Task 8 (FileTreeView)
```

**Parallelizable groups:**
- Group A: Tasks 1 → 2 → 3 → 4 → 9 → 5 (critical path)
- Group B: Task 6 (independent)
- Group C: Tasks 7 → 8 (daemon + SPA)

Groups A, B, C can run in parallel. Within each group, tasks are sequential.
