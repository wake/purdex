import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createElement, type ComponentProps } from 'react'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import type { DndContext as RealDndContext, DragEndEvent, Modifier } from '@dnd-kit/core'
import { ActivityBarWide } from './ActivityBarWide'
import { useLayoutStore, WORKER_LIST_DEFAULT } from '../../../stores/useLayoutStore'
import type { Workspace } from '../../../types/tab'

// The real DndContext still renders (so useSortable / useDroppable stay wired); the wrapper only records the props the
// bar hands it, so the tests can fire its drag-end handler and run its modifiers the way dnd-kit would.
const dnd = vi.hoisted(() => ({ props: null as null | ComponentProps<typeof RealDndContext> }))
vi.mock('@dnd-kit/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@dnd-kit/core')>()
  return {
    ...actual,
    DndContext: (props: ComponentProps<typeof RealDndContext>) => {
      dnd.props = props
      return createElement(actual.DndContext, props)
    },
  }
})

// The list itself is WorkerList's business (its own tests); here it only has to be there or not.
vi.mock('../../../components/executions/WorkerList', () => ({
  WorkerList: () => <div data-testid="worker-list" />,
}))

const ws = (id: string, name: string): Workspace => ({
  id, name, tabs: [], activeTabId: null,
})

function renderBar(over: Partial<ComponentProps<typeof ActivityBarWide>> = {}) {
  return render(
    <ActivityBarWide
      workspaces={[ws('w1', 'Purdex'), ws('w2', 'Client A')]}
      activeWorkspaceId="w1"
      onSelectWorkspace={() => {}}
      onSelectHome={() => {}}
      onAddWorkspace={() => {}}
      onOpenHosts={() => {}}
      onOpenSettings={() => {}}
      {...over}
    />,
  )
}

/** Every element under the bar root that scrolls vertically. */
const scrollers = () =>
  Array.from(screen.getByTestId('activity-bar-wide').querySelectorAll('*')).filter((el) =>
    el.classList.contains('overflow-y-auto'),
  )

describe('ActivityBarWide', () => {
  beforeEach(() => {
    cleanup()
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })

  it('renders Home label + workspace names', () => {
    render(
      <ActivityBarWide
        workspaces={[ws('w1', 'Purdex'), ws('w2', 'Client A')]}
        activeWorkspaceId="w1"
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(screen.getByText('Purdex')).toBeInTheDocument()
    expect(screen.getByText('Client A')).toBeInTheDocument()
  })

  it('clicking a workspace row calls onSelectWorkspace', () => {
    const onSelect = vi.fn()
    render(
      <ActivityBarWide
        workspaces={[ws('w1', 'Purdex')]}
        activeWorkspaceId={null}
        onSelectWorkspace={onSelect}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    fireEvent.click(screen.getByText('Purdex'))
    expect(onSelect).toHaveBeenCalledWith('w1')
  })

  it('renders a resize handle', () => {
    render(
      <ActivityBarWide
        workspaces={[]}
        activeWorkspaceId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    const handle = document.querySelector('[data-testid="activity-bar-resize"]')
    expect(handle).toBeInTheDocument()
  })

  it('scrolls only the workspace region and keeps the divider fixed below Home', () => {
    const { container } = render(
      <ActivityBarWide
        workspaces={[ws('w1', 'Purdex'), ws('w2', 'Client A')]}
        activeWorkspaceId="w1"
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )

    const workspaceScroll = screen.getByTestId('activity-bar-workspace-scroll')
    expect(workspaceScroll).toHaveClass('overflow-y-auto')
    expect(screen.getByTestId('home-header').closest('[data-testid="activity-bar-workspace-scroll"]')).toBeNull()
    expect(screen.getByTestId('ws-header-w1').closest('[data-testid="activity-bar-workspace-scroll"]')).toBe(workspaceScroll)
    expect(container.querySelector('[data-testid="activity-bar-workspace-separator"]')?.closest('[data-testid="activity-bar-workspace-scroll"]')).toBeNull()

    const activityBarRoot = workspaceScroll.closest('[data-testid="activity-bar-wide"]')
    expect(activityBarRoot).not.toHaveClass('overflow-y-auto')
    expect(activityBarRoot).toHaveClass('overflow-hidden')
    // With the worker list closed the workspace zone is the only scroller.
    expect(scrollers()).toEqual([workspaceScroll])
  })

  it('with the worker list open, the only scrollers are the workspace zone and the worker list section', () => {
    useLayoutStore.setState({ workerListOpen: true })
    renderBar()
    expect(scrollers()).toEqual([
      screen.getByTestId('activity-bar-workspace-scroll'),
      screen.getByTestId('worker-list-section'),
    ])
    expect(screen.getByTestId('activity-bar-wide')).toHaveClass('overflow-hidden')
  })
})

describe('ActivityBarWide — worker list section', () => {
  beforeEach(() => {
    cleanup()
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('mounts nothing of the list while it is closed', () => {
    renderBar()
    expect(screen.queryByTestId('worker-list-section')).toBeNull()
    expect(screen.queryByTestId('worker-list-divider')).toBeNull()
    expect(screen.queryByTestId('worker-list')).toBeNull()
    const box = screen.getByTestId('worker-split-box')
    expect(Array.from(box.children)).toEqual([screen.getByTestId('activity-bar-workspace-scroll')])
  })

  it('open: the split box holds zone → divider → section, Home and the separator sit above it, the bottom group below', () => {
    useLayoutStore.setState({ workerListOpen: true })
    renderBar()
    const box = screen.getByTestId('worker-split-box')
    expect(box).toHaveClass('flex', 'min-h-0', 'flex-1', 'flex-col')
    expect(Array.from(box.children).map((c) => c.getAttribute('data-testid'))).toEqual([
      'activity-bar-workspace-scroll',
      'worker-list-divider',
      'worker-list-section',
    ])
    const section = screen.getByTestId('worker-list-section')
    expect(section).toHaveClass('min-h-0', 'shrink-0', 'overflow-y-auto', 'overscroll-contain')
    expect(section).toContainElement(screen.getByTestId('worker-list'))
    expect(section.style.height).toBe(`${WORKER_LIST_DEFAULT}px`)

    const home = screen.getByTestId('home-header')
    const separator = screen.getByTestId('activity-bar-workspace-separator')
    const bottomNav = screen.getByTestId('bottom-nav')
    for (const el of [home, separator, bottomNav]) expect(box).not.toContainElement(el)
    expect(home.compareDocumentPosition(box) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(separator.compareDocumentPosition(box) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(box.compareDocumentPosition(bottomNav) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('dragging the divider up 50px grows the list and commits 290 exactly once, on mouseup', () => {
    const setWorkerListHeight = vi.fn(useLayoutStore.getState().setWorkerListHeight)
    useLayoutStore.setState({ workerListOpen: true, setWorkerListHeight })
    renderBar()
    const divider = screen.getByTestId('worker-list-divider')
    const section = screen.getByTestId('worker-list-section')

    fireEvent.mouseDown(divider, { clientY: 500 })
    fireEvent.mouseMove(document, { clientY: 480 })
    fireEvent.mouseMove(document, { clientY: 450 })
    expect(section.style.height).toBe('290px')
    expect(setWorkerListHeight).not.toHaveBeenCalled()

    fireEvent.mouseUp(document)
    expect(setWorkerListHeight).toHaveBeenCalledTimes(1)
    expect(setWorkerListHeight).toHaveBeenCalledWith(290)
    expect(useLayoutStore.getState().workerListHeight).toBe(290)
    expect(section.style.height).toBe('290px')
  })

  it('caps the rendered height so the workspace zone keeps 96px, without shrinking the stored height', () => {
    let fire: ((height: number) => void) | null = null
    let observed: Element | null = null
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(cb: ResizeObserverCallback) {
          fire = (height) =>
            cb([{ target: observed, contentRect: { height } } as unknown as ResizeObserverEntry], this as never)
        }
        observe(el: Element) { observed = el }
        unobserve() {}
        disconnect() {}
      },
    )
    useLayoutStore.setState({ workerListOpen: true })
    renderBar()
    expect(observed).toBe(screen.getByTestId('worker-split-box'))
    const section = screen.getByTestId('worker-list-section')

    // available 300 → 300 − 96 (workspace zone) − 4 (divider) = 200 < 240
    act(() => fire!(300))
    expect(section.style.height).toBe('200px')
    expect(useLayoutStore.getState().workerListHeight).toBe(WORKER_LIST_DEFAULT)

    // Plenty of room → the stored height again.
    act(() => fire!(1000))
    expect(section.style.height).toBe(`${WORKER_LIST_DEFAULT}px`)
  })
})

describe('ActivityBarWide — bottom group', () => {
  beforeEach(() => {
    cleanup()
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })

  it('renders the shared BottomNav in its wide variant', () => {
    renderBar()
    expect(screen.getByTestId('bottom-nav')).toHaveAttribute('data-compact', 'false')
    expect(screen.getByRole('button', { name: 'New workspace' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Hosts' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Settings' })).toBeInTheDocument()
  })

  it('the compact toggle flips bottomNavCompact and data-compact follows it', () => {
    renderBar()
    fireEvent.click(screen.getByTestId('bottom-nav-compact-toggle'))
    expect(useLayoutStore.getState().bottomNavCompact).toBe(true)
    expect(screen.getByTestId('bottom-nav')).toHaveAttribute('data-compact', 'true')
    fireEvent.click(screen.getByTestId('bottom-nav-compact-toggle'))
    expect(useLayoutStore.getState().bottomNavCompact).toBe(false)
    expect(screen.getByTestId('bottom-nav')).toHaveAttribute('data-compact', 'false')
  })

  it('Workers toggles workerListOpen and its aria-pressed follows the store', () => {
    renderBar()
    const workers = () => screen.getByRole('button', { name: 'Workers' })
    expect(workers()).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(workers())
    expect(useLayoutStore.getState().workerListOpen).toBe(true)
    expect(workers()).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('worker-list-section')).toBeInTheDocument()
    act(() => useLayoutStore.getState().setWorkerListOpen(false))
    expect(workers()).toHaveAttribute('aria-pressed', 'false')
    expect(screen.queryByTestId('worker-list-section')).toBeNull()
  })

  it('wires the other entries to the bar props', () => {
    const onAddWorkspace = vi.fn()
    const onOpenHosts = vi.fn()
    const onOpenSettings = vi.fn()
    renderBar({ onAddWorkspace, onOpenHosts, onOpenSettings })
    fireEvent.click(screen.getByRole('button', { name: 'New workspace' }))
    fireEvent.click(screen.getByRole('button', { name: 'Hosts' }))
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
    expect(onAddWorkspace).toHaveBeenCalledTimes(1)
    expect(onOpenHosts).toHaveBeenCalledTimes(1)
    expect(onOpenSettings).toHaveBeenCalledTimes(1)
  })
})

// jsdom gives every element a zero rect, so dnd-kit's own collision detection cannot pick a target here. These tests
// drive the drag-end event and the modifier the real DndContext received, with the worker list open.
describe('ActivityBarWide — drag and drop with the worker list open', () => {
  beforeEach(() => {
    cleanup()
    dnd.props = null
    useLayoutStore.setState({ ...useLayoutStore.getInitialState(), workerListOpen: true })
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  const dragEnd = (active: { id: string; data: unknown }, over: { id: string; data: unknown }) =>
    act(() =>
      dnd.props!.onDragEnd!({
        active: { id: active.id, data: { current: active.data } },
        over: { id: over.id, data: { current: over.data } },
      } as unknown as DragEndEvent),
    )

  it('the worker list is mounted inside the same DndContext as the workspace rows', () => {
    renderBar()
    expect(dnd.props).not.toBeNull()
    expect(screen.getByTestId('worker-list-section')).toBeInTheDocument()
    // The workspace rows are still real sortables.
    expect(screen.getByTestId('ws-header-w1').closest('[aria-roledescription="sortable"]')).not.toBeNull()
  })

  it('a workspace reorder still calls onReorderWorkspaces', () => {
    const onReorderWorkspaces = vi.fn()
    renderBar({ onReorderWorkspaces })
    dragEnd(
      { id: 'w1', data: { type: 'workspace', wsId: 'w1' } },
      { id: 'w2', data: { type: 'workspace', wsId: 'w2' } },
    )
    expect(onReorderWorkspaces).toHaveBeenCalledWith(['w2', 'w1'])
  })

  it('a tab dropped on another workspace header still calls onMoveTabToWorkspace', () => {
    const onMoveTabToWorkspace = vi.fn()
    renderBar({
      workspaces: [{ ...ws('w1', 'Purdex'), tabs: ['t1'] }, ws('w2', 'Client A')],
      onMoveTabToWorkspace,
    })
    dragEnd(
      { id: 't1', data: { type: 'tab', tabId: 't1', sourceWsId: 'w1' } },
      { id: 'ws-header-w2', data: { type: 'workspace-header', wsId: 'w2' } },
    )
    expect(onMoveTabToWorkspace).toHaveBeenCalledWith('t1', 'w2', null)
  })

  it('a workspace drag is still clamped to the workspace zone, not the split box', () => {
    renderBar()
    const rect = (top: number, bottom: number) =>
      ({ top, bottom, left: 0, right: 200, width: 200, height: bottom - top, x: 0, y: top, toJSON: () => ({}) }) as DOMRect
    vi.spyOn(screen.getByTestId('activity-bar-workspace-scroll'), 'getBoundingClientRect').mockReturnValue(rect(100, 300))
    vi.spyOn(screen.getByTestId('worker-split-box'), 'getBoundingClientRect').mockReturnValue(rect(100, 700))
    const [restrict] = dnd.props!.modifiers as Modifier[]
    const run = (y: number) =>
      restrict({
        transform: { x: 7, y, scaleX: 1, scaleY: 1 },
        activeNodeRect: rect(200, 230),
        active: { data: { current: { type: 'workspace', wsId: 'w1' } } },
      } as unknown as Parameters<Modifier>[0])
    // The row (200–230) may move between the zone's top (100) and bottom (300): y ∈ [−100, 70].
    expect(run(-500)).toMatchObject({ x: 0, y: -100 })
    expect(run(500)).toMatchObject({ x: 0, y: 70 })
  })
})

describe('ActivityBarWide Phase 2 — inline tabs', () => {
  beforeEach(() => {
    cleanup()
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })

  it('renders WorkspaceRow per workspace', () => {
    render(
      <ActivityBarWide
        workspaces={[
          { id: 'w1', name: 'Alpha', tabs: [], activeTabId: null },
          { id: 'w2', name: 'Beta', tabs: [], activeTabId: null },
        ]}
        activeWorkspaceId="w1"
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
        tabsById={{}}
        activeTabId={null}
        onSelectTab={() => {}}
        onCloseTab={() => {}}
        onMiddleClickTab={() => {}}
        onContextMenuTab={() => {}}
        onReorderWorkspaceTabs={() => {}}
        onAddTabToWorkspace={() => {}}
      />,
    )
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Beta')).toBeInTheDocument()
  })

  it('shows expanded inline tabs when workspaceExpanded set', () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide', workspaceExpanded: { w1: true } })
    render(
      <ActivityBarWide
        workspaces={[{ id: 'w1', name: 'Alpha', tabs: ['t1'], activeTabId: 't1' }]}
        activeWorkspaceId="w1"
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
        tabsById={{
          t1: {
            id: 't1',
            kind: 'new-tab',
            locked: false,
            layout: {
              type: 'leaf',
              pane: {
                id: 't1-pane',
                content: { kind: 'browser', url: 'https://example.test/' },
              },
            },
          } as never,
        }}
        activeTabId="t1"
        onSelectTab={() => {}}
        onCloseTab={() => {}}
        onMiddleClickTab={() => {}}
        onContextMenuTab={() => {}}
        onReorderWorkspaceTabs={() => {}}
        onAddTabToWorkspace={() => {}}
      />,
    )
    // getPaneLabel for browser returns hostname.
    // Label appears twice per row: visible title span + HoverTooltip.
    expect(screen.getAllByText('example.test').length).toBeGreaterThan(0)
  })

  it('renders home-header (the Home button) and ws-header-<id> (the droppable workspace headers)', () => {
    render(
      <ActivityBarWide
        workspaces={[ws('w1', 'Alpha'), ws('w2', 'Beta')]}
        activeWorkspaceId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(screen.getByTestId('home-header')).toBeInTheDocument()
    expect(screen.getByTestId('ws-header-w1')).toBeInTheDocument()
    expect(screen.getByTestId('ws-header-w2')).toBeInTheDocument()
  })
})

describe('ActivityBarWide — auto-expand active workspace', () => {
  beforeEach(() => {
    cleanup()
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })

  it("expands active workspace when tabPosition='left'", () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide' })
    render(
      <ActivityBarWide
        workspaces={[{ id: 'w1', name: 'Alpha', tabs: [], activeTabId: null }]}
        activeWorkspaceId="w1"
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(useLayoutStore.getState().workspaceExpanded['w1']).toBe(true)
  })

  // Home heads no tab list any more (Profile Sync spec §4.3), so there is nothing of it to expand.
  // (Was: "expands home when standalone tab is active".)
  it("expands nothing when there is no active workspace and tabPosition='both'", () => {
    useLayoutStore.setState({ tabPosition: 'both', activityBarWidth: 'wide' })
    render(
      <ActivityBarWide
        workspaces={[{ id: 'w1', name: 'Alpha', tabs: [], activeTabId: null }]}
        activeWorkspaceId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(useLayoutStore.getState().workspaceExpanded).toEqual({})
  })

  it("does NOT auto-expand when tabPosition='top'", () => {
    useLayoutStore.setState({ tabPosition: 'top' })
    render(
      <ActivityBarWide
        workspaces={[{ id: 'w1', name: 'Alpha', tabs: [], activeTabId: null }]}
        activeWorkspaceId="w1"
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(useLayoutStore.getState().workspaceExpanded['w1']).toBeFalsy()
  })
})
