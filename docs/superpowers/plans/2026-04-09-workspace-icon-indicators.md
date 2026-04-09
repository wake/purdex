# Workspace Icon Indicators Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add unread badge and status pill indicators to workspace icons in the ActivityBar sidebar.

**Architecture:** Extract a `WorkspaceButton` component from ActivityBar that uses a `useWorkspaceIndicators` hook to aggregate per-workspace unread counts and status from existing stores. Pure utility functions handle the tabId→compositeKey bridging and status priority logic, keeping the hook thin.

**Tech Stack:** React 19, Zustand 5, Tailwind 4, Vitest, @testing-library/react

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `spa/src/features/workspace/workspace-indicators.ts` | Create | Pure utility functions: `getWorkspaceCompositeKeys`, `aggregateStatus` |
| `spa/src/features/workspace/workspace-indicators.test.ts` | Create | Unit tests for utility functions |
| `spa/src/features/workspace/useWorkspaceIndicators.ts` | Create | React hook: bridges stores → workspace-level indicators |
| `spa/src/features/workspace/useWorkspaceIndicators.test.ts` | Create | Hook integration tests using `renderHook` |
| `spa/src/features/workspace/components/ActivityBar.tsx` | Modify | Extract `WorkspaceButton` sub-component, add badge + pill rendering |
| `spa/src/features/workspace/components/ActivityBar.test.tsx` | Modify | Add test cases for badge display, count, and aria-label |

---

### Task 1: Pure Utility Functions

**Files:**
- Create: `spa/src/features/workspace/workspace-indicators.ts`
- Create: `spa/src/features/workspace/workspace-indicators.test.ts`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/features/workspace/workspace-indicators.test.ts
import { describe, it, expect } from 'vitest'
import type { Tab } from '../../types/tab'
import { getWorkspaceCompositeKeys, aggregateStatus } from './workspace-indicators'

function mockSessionTab(id: string, hostId: string, sessionCode: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: {
      type: 'leaf',
      pane: {
        id: `pane-${id}`,
        content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal' as const, cachedName: '', tmuxInstance: '' },
      },
    },
  }
}

function mockDashboardTab(id: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: `pane-${id}`, content: { kind: 'dashboard' } } },
  }
}

describe('getWorkspaceCompositeKeys', () => {
  it('returns compositeKeys for tmux-session tabs', () => {
    const tabs: Record<string, Tab> = {
      t1: mockSessionTab('t1', 'h1', 's1'),
      t2: mockSessionTab('t2', 'h1', 's2'),
    }
    expect(getWorkspaceCompositeKeys(['t1', 't2'], tabs)).toEqual(['h1:s1', 'h1:s2'])
  })

  it('skips non-session tabs', () => {
    const tabs: Record<string, Tab> = {
      t1: mockSessionTab('t1', 'h1', 's1'),
      t2: mockDashboardTab('t2'),
    }
    expect(getWorkspaceCompositeKeys(['t1', 't2'], tabs)).toEqual(['h1:s1'])
  })

  it('skips missing tab IDs', () => {
    const tabs: Record<string, Tab> = {
      t1: mockSessionTab('t1', 'h1', 's1'),
    }
    expect(getWorkspaceCompositeKeys(['t1', 't999'], tabs)).toEqual(['h1:s1'])
  })

  it('returns empty array for empty workspace', () => {
    expect(getWorkspaceCompositeKeys([], {})).toEqual([])
  })
})

describe('aggregateStatus', () => {
  it('returns undefined for empty array', () => {
    expect(aggregateStatus([])).toBeUndefined()
  })

  it('returns undefined for all idle', () => {
    expect(aggregateStatus(['idle', 'idle'])).toBeUndefined()
  })

  it('returns undefined for all undefined', () => {
    expect(aggregateStatus([undefined, undefined])).toBeUndefined()
  })

  it('returns running when highest is running', () => {
    expect(aggregateStatus(['running', 'idle'])).toBe('running')
  })

  it('returns waiting over running', () => {
    expect(aggregateStatus(['running', 'waiting'])).toBe('waiting')
  })

  it('returns error over everything', () => {
    expect(aggregateStatus(['running', 'waiting', 'error'])).toBe('error')
  })

  it('returns running when mixed with undefined', () => {
    expect(aggregateStatus([undefined, 'running', undefined])).toBe('running')
  })
})
```

- [ ] **Step 2: Verify tests fail**

Run: `cd spa && npx vitest run src/features/workspace/workspace-indicators.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Write implementation**

```typescript
// spa/src/features/workspace/workspace-indicators.ts
import type { Tab } from '../../types/tab'
import type { AgentStatus } from '../../stores/useAgentStore'
import { getPrimaryPane } from '../../lib/pane-tree'
import { compositeKey } from '../../lib/composite-key'

/** Extract compositeKeys from a workspace's tab IDs. Skips non-session and missing tabs. */
export function getWorkspaceCompositeKeys(tabIds: string[], tabs: Record<string, Tab>): string[] {
  const keys: string[] = []
  for (const id of tabIds) {
    const tab = tabs[id]
    if (!tab) continue
    const { content } = getPrimaryPane(tab.layout)
    if (content.kind !== 'tmux-session') continue
    keys.push(compositeKey(content.hostId, content.sessionCode))
  }
  return keys
}

const STATUS_PRIORITY: Record<AgentStatus, number> = {
  error: 3,
  waiting: 2,
  running: 1,
  idle: 0,
}

/** Returns highest-priority status across tabs, or undefined if all idle/absent. */
export function aggregateStatus(statuses: (AgentStatus | undefined)[]): AgentStatus | undefined {
  let highest: AgentStatus | undefined
  let highestPri = -1
  for (const s of statuses) {
    if (s === undefined) continue
    const p = STATUS_PRIORITY[s]
    if (p > highestPri) {
      highest = s
      highestPri = p
    }
  }
  return highest === 'idle' ? undefined : highest
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd spa && npx vitest run src/features/workspace/workspace-indicators.test.ts`
Expected: All 9 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/workspace-indicators.ts spa/src/features/workspace/workspace-indicators.test.ts
git commit -m "feat: add workspace indicator utility functions

Pure functions for tabId→compositeKey bridging and status aggregation."
```

---

### Task 2: useWorkspaceIndicators Hook

**Files:**
- Create: `spa/src/features/workspace/useWorkspaceIndicators.ts`
- Create: `spa/src/features/workspace/useWorkspaceIndicators.test.ts`

- [ ] **Step 1: Write failing tests**

```typescript
// spa/src/features/workspace/useWorkspaceIndicators.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabStore } from '../../stores/useTabStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useWorkspaceIndicators } from './useWorkspaceIndicators'
import type { Tab } from '../../types/tab'

function mockSessionTab(id: string, hostId: string, sessionCode: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: {
      type: 'leaf',
      pane: {
        id: `pane-${id}`,
        content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal' as const, cachedName: '', tmuxInstance: '' },
      },
    },
  }
}

describe('useWorkspaceIndicators', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {} })
    useAgentStore.setState({ unread: {}, statuses: {} })
  })

  it('returns zero unread for empty workspace', () => {
    const { result } = renderHook(() => useWorkspaceIndicators([]))
    expect(result.current.unreadCount).toBe(0)
    expect(result.current.aggregatedStatus).toBeUndefined()
  })

  it('counts unread tabs', () => {
    useTabStore.setState({
      tabs: {
        t1: mockSessionTab('t1', 'h1', 's1'),
        t2: mockSessionTab('t2', 'h1', 's2'),
        t3: mockSessionTab('t3', 'h1', 's3'),
      },
    })
    useAgentStore.setState({ unread: { 'h1:s1': true, 'h1:s2': false, 'h1:s3': true } })

    const { result } = renderHook(() => useWorkspaceIndicators(['t1', 't2', 't3']))
    expect(result.current.unreadCount).toBe(2)
  })

  it('aggregates status with priority', () => {
    useTabStore.setState({
      tabs: {
        t1: mockSessionTab('t1', 'h1', 's1'),
        t2: mockSessionTab('t2', 'h1', 's2'),
      },
    })
    useAgentStore.setState({ statuses: { 'h1:s1': 'running', 'h1:s2': 'waiting' } })

    const { result } = renderHook(() => useWorkspaceIndicators(['t1', 't2']))
    expect(result.current.aggregatedStatus).toBe('waiting')
  })

  it('reacts to unread store updates', () => {
    useTabStore.setState({ tabs: { t1: mockSessionTab('t1', 'h1', 's1') } })
    const { result } = renderHook(() => useWorkspaceIndicators(['t1']))
    expect(result.current.unreadCount).toBe(0)

    act(() => {
      useAgentStore.setState({ unread: { 'h1:s1': true } })
    })
    expect(result.current.unreadCount).toBe(1)
  })

  it('returns undefined status for all-idle workspace', () => {
    useTabStore.setState({ tabs: { t1: mockSessionTab('t1', 'h1', 's1') } })
    useAgentStore.setState({ statuses: { 'h1:s1': 'idle' } })

    const { result } = renderHook(() => useWorkspaceIndicators(['t1']))
    expect(result.current.aggregatedStatus).toBeUndefined()
  })
})
```

- [ ] **Step 2: Verify tests fail**

Run: `cd spa && npx vitest run src/features/workspace/useWorkspaceIndicators.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Write implementation**

```typescript
// spa/src/features/workspace/useWorkspaceIndicators.ts
import { useCallback, useMemo } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useAgentStore } from '../../stores/useAgentStore'
import type { AgentStatus } from '../../stores/useAgentStore'
import { getWorkspaceCompositeKeys, aggregateStatus } from './workspace-indicators'

interface WorkspaceIndicators {
  unreadCount: number
  aggregatedStatus: AgentStatus | undefined
}

export function useWorkspaceIndicators(tabIds: string[]): WorkspaceIndicators {
  const tabs = useTabStore((s) => s.tabs)

  const compositeKeys = useMemo(
    () => getWorkspaceCompositeKeys(tabIds, tabs),
    [tabIds, tabs],
  )

  const unreadCount = useAgentStore(
    useCallback(
      (s: { unread: Record<string, boolean> }) =>
        compositeKeys.reduce((n, k) => n + (s.unread[k] ? 1 : 0), 0),
      [compositeKeys],
    ),
  )

  const aggregatedStatus = useAgentStore(
    useCallback(
      (s: { statuses: Record<string, AgentStatus> }) =>
        aggregateStatus(compositeKeys.map((k) => s.statuses[k])),
      [compositeKeys],
    ),
  )

  return { unreadCount, aggregatedStatus }
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd spa && npx vitest run src/features/workspace/useWorkspaceIndicators.test.ts`
Expected: All 5 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/useWorkspaceIndicators.ts spa/src/features/workspace/useWorkspaceIndicators.test.ts
git commit -m "feat: add useWorkspaceIndicators hook

Zustand-based hook bridging tab/agent stores to workspace-level
unread count and aggregated status."
```

---

### Task 3: Extract WorkspaceButton + Unread Badge

**Files:**
- Modify: `spa/src/features/workspace/components/ActivityBar.tsx`
- Modify: `spa/src/features/workspace/components/ActivityBar.test.tsx`

- [ ] **Step 1: Extract WorkspaceButton and add badge in ActivityBar.tsx**

Add imports at top of file:

```typescript
import { useWorkspaceIndicators } from '../useWorkspaceIndicators'
```

Add `WorkspaceButton` component before the `ActivityBar` export. This replaces the inline workspace button JSX in the `.map()`:

```typescript
interface WorkspaceButtonProps {
  workspace: Workspace
  isActive: boolean
  onSelect: (wsId: string) => void
  onContextMenu?: (e: React.MouseEvent, wsId: string) => void
}

function WorkspaceButton({ workspace: ws, isActive, onSelect, onContextMenu }: WorkspaceButtonProps) {
  const { unreadCount } = useWorkspaceIndicators(ws.tabs)
  const showBadge = !isActive && unreadCount > 0

  return (
    <button
      title={ws.name}
      aria-label={showBadge ? `${ws.name}, ${unreadCount} unread` : ws.name}
      onClick={() => onSelect(ws.id)}
      onContextMenu={(e) => {
        e.preventDefault()
        onContextMenu?.(e, ws.id)
      }}
      className={`relative w-[30px] h-[30px] rounded-md flex items-center justify-center text-sm cursor-pointer transition-all ${
        isActive
          ? 'bg-[#8b5cf6]/35 text-text-primary ring-2 ring-purple-400'
          : 'bg-surface-secondary text-text-secondary hover:bg-surface-hover hover:text-text-primary'
      }`}
    >
      <WorkspaceIcon icon={ws.icon} name={ws.name} size={16} weight={ws.iconWeight} />
      {showBadge && (
        <span
          data-testid="ws-unread-badge"
          className="absolute -top-[5px] -right-[6px] min-w-[15px] h-[15px] rounded-full flex items-center justify-center text-white text-[9px] font-bold px-[3px] leading-none"
          style={{ backgroundColor: '#dc2626', boxShadow: '0 0 0 2px var(--surface-tertiary)' }}
        >
          {unreadCount}
        </span>
      )}
    </button>
  )
}
```

Replace the workspace `.map()` block (lines 55-75) in ActivityBar with:

```typescript
{workspaces.map((ws) => (
  <WorkspaceButton
    key={ws.id}
    workspace={ws}
    isActive={activeWorkspaceId === ws.id && !activeStandaloneTabId}
    onSelect={onSelectWorkspace}
    onContextMenu={onContextMenuWorkspace}
  />
))}
```

- [ ] **Step 2: Run existing tests to verify no regression**

Run: `cd spa && npx vitest run src/features/workspace/components/ActivityBar.test.tsx`
Expected: All 8 existing tests PASS (stores default to empty → no badge renders → no interference)

- [ ] **Step 3: Add badge test cases to ActivityBar.test.tsx**

Add imports at top:

```typescript
import { useTabStore } from '../../../stores/useTabStore'
import { useAgentStore } from '../../../stores/useAgentStore'
import type { Tab } from '../../../types/tab'
```

Add helper inside test file:

```typescript
function mockSessionTab(id: string, hostId: string, sessionCode: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: {
      type: 'leaf',
      pane: {
        id: `pane-${id}`,
        content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal' as const, cachedName: '', tmuxInstance: '' },
      },
    },
  }
}
```

Add to `beforeEach`:

```typescript
useTabStore.setState({ tabs: {} })
useAgentStore.setState({ unread: {}, statuses: {} })
```

Add new test cases inside the `describe` block:

```typescript
it('shows unread badge on inactive workspace', () => {
  useTabStore.setState({
    tabs: {
      t1: mockSessionTab('t1', 'h1', 's1'),
      t2: mockSessionTab('t2', 'h1', 's2'),
      t3: mockSessionTab('t3', 'h1', 's3'),
    },
  })
  useAgentStore.setState({ unread: { 'h1:s3': true } })

  render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

  const badges = screen.getAllByTestId('ws-unread-badge')
  expect(badges).toHaveLength(1)
  expect(badges[0].textContent).toBe('1')
})

it('hides unread badge on active workspace', () => {
  useTabStore.setState({
    tabs: {
      t1: mockSessionTab('t1', 'h1', 's1'),
      t2: mockSessionTab('t2', 'h1', 's2'),
    },
  })
  useAgentStore.setState({ unread: { 'h1:s1': true } })

  render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

  // ws-1 is active → no badge even though t1 is unread
  expect(screen.queryAllByTestId('ws-unread-badge')).toHaveLength(0)
})

it('includes unread count in aria-label', () => {
  useTabStore.setState({
    tabs: { t3: mockSessionTab('t3', 'h1', 's3') },
  })
  useAgentStore.setState({ unread: { 'h1:s3': true } })

  render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

  expect(screen.getByLabelText('Server, 1 unread')).toBeTruthy()
})
```

- [ ] **Step 4: Run all tests**

Run: `cd spa && npx vitest run src/features/workspace/`
Expected: All tests PASS (utilities + hook + ActivityBar)

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/components/ActivityBar.tsx spa/src/features/workspace/components/ActivityBar.test.tsx
git commit -m "feat: add unread badge to workspace icons

Extract WorkspaceButton component, integrate useWorkspaceIndicators
hook, render red badge with count on inactive workspaces."
```

---

### Task 4: Status Pill (Initial Implementation)

**Files:**
- Modify: `spa/src/features/workspace/components/ActivityBar.tsx`

> **Note:** This task's visual output is subject to iteration. The initial implementation provides a working pill that can be adjusted visually.

- [ ] **Step 1: Add status pill to WorkspaceButton**

Add status color constant before `WorkspaceButton`:

```typescript
const PILL_COLORS: Record<string, string> = {
  running: '#4ade80',
  waiting: '#facc15',
  error: '#ef4444',
}
```

In `WorkspaceButton`, destructure `aggregatedStatus` from the hook (already called but not used):

```typescript
const { unreadCount, aggregatedStatus } = useWorkspaceIndicators(ws.tabs)
```

Add pill JSX inside the `<button>`, after the `WorkspaceIcon`:

```typescript
{aggregatedStatus && (
  <span
    className={`absolute rounded-r-sm ${aggregatedStatus === 'running' ? 'animate-breathe' : ''}`}
    style={{
      left: '-7px',
      top: '50%',
      transform: 'translateY(-50%)',
      width: '3px',
      height: aggregatedStatus === 'error' || aggregatedStatus === 'waiting' ? '60%' : '40%',
      backgroundColor: PILL_COLORS[aggregatedStatus],
      '--breathe-color': PILL_COLORS[aggregatedStatus],
      '--breathe-bg': 'transparent',
    } as React.CSSProperties}
  />
)}
```

- [ ] **Step 2: Run all tests to verify no regression**

Run: `cd spa && npx vitest run src/features/workspace/`
Expected: All tests PASS

- [ ] **Step 3: Visual verification**

Run: `cd spa && pnpm run dev`
Open `http://100.64.0.2:5174` — verify:
- Workspace with running agent shows green pill on left edge
- Workspace with waiting agent shows yellow pill (taller)
- Idle workspaces show no pill
- Badge and pill coexist without overlap

> Adjust pill position (`left`), height percentages, and active-workspace visibility based on visual feedback. This is expected to require iteration.

- [ ] **Step 4: Commit**

```bash
git add spa/src/features/workspace/components/ActivityBar.tsx
git commit -m "feat: add status pill to workspace icons (initial)

Discord-style left pill showing aggregated agent status.
Position and active-workspace behavior subject to iteration."
```

---

## Run All Tests

After all tasks, verify the full test suite:

```bash
cd spa && npx vitest run
```

Expected: All tests PASS, no regressions.
