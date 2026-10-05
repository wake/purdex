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

/** Replaces ResizeObserver; `fire(h)` reports a content height for the observed element. */
function stubResizeObserver() {
  let callback: ResizeObserverCallback | null = null
  let observed: Element | null = null
  const disconnect = vi.fn()
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(cb: ResizeObserverCallback) {
        callback = cb
      }
      observe(el: Element) { observed = el }
      unobserve() {}
      disconnect = disconnect
    },
  )
  return {
    fire: (height: number) =>
      callback!([{ target: observed, contentRect: { height } } as unknown as ResizeObserverEntry], {} as never),
    observed: () => observed,
    disconnect,
  }
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

  // Shell polish spec §3: the section's header stays put, so the section no longer scrolls — its scroll area does.
  it('with the worker list open, the only scrollers are the workspace zone and the worker list scroll area', () => {
    useLayoutStore.setState({ workerListOpen: true })
    renderBar()
    expect(scrollers()).toEqual([
      screen.getByTestId('activity-bar-workspace-scroll'),
      screen.getByTestId('worker-list-scroll'),
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
    // The section is the sized column; the scroll area inside it is what scrolls (shell polish spec §3).
    const section = screen.getByTestId('worker-list-section')
    expect(section).toHaveClass('flex', 'flex-col', 'min-h-0', 'shrink-0')
    expect(section).not.toHaveClass('overflow-y-auto')
    expect(section).not.toHaveClass('overscroll-contain')
    expect(section).toContainElement(screen.getByTestId('worker-list'))
    expect(section.style.height).toBe(`${WORKER_LIST_DEFAULT}px`)
    const scroll = screen.getByTestId('worker-list-scroll')
    expect(scroll).toHaveClass('min-h-0', 'flex-1', 'overflow-y-auto', 'overscroll-contain')
    expect(scroll.style.height).toBe('')

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

  it('closing the list mid-drag commits nothing, and reopening shows the stored height', () => {
    const setWorkerListHeight = vi.fn(useLayoutStore.getState().setWorkerListHeight)
    useLayoutStore.setState({ workerListOpen: true, setWorkerListHeight })
    renderBar()
    fireEvent.mouseDown(screen.getByTestId('worker-list-divider'), { clientY: 500 })
    fireEvent.mouseMove(document, { clientY: 450 })
    expect(screen.getByTestId('worker-list-section').style.height).toBe('290px')

    act(() => useLayoutStore.setState({ workerListOpen: false }))
    fireEvent.mouseUp(document)
    expect(setWorkerListHeight).not.toHaveBeenCalled()

    act(() => useLayoutStore.setState({ workerListOpen: true }))
    expect(screen.getByTestId('worker-list-section').style.height).toBe(`${WORKER_LIST_DEFAULT}px`)
    fireEvent.mouseDown(screen.getByTestId('worker-list-divider'), { clientY: 500 })
    fireEvent.mouseUp(document)
    expect(setWorkerListHeight).not.toHaveBeenCalled()
  })

  it('unmounting the bar mid-drag writes nothing to the store', () => {
    const setWorkerListHeight = vi.fn(useLayoutStore.getState().setWorkerListHeight)
    useLayoutStore.setState({ workerListOpen: true, setWorkerListHeight })
    const { unmount } = renderBar()
    fireEvent.mouseDown(screen.getByTestId('worker-list-divider'), { clientY: 500 })
    fireEvent.mouseMove(document, { clientY: 450 })
    unmount()
    fireEvent.mouseMove(document, { clientY: 400 })
    fireEvent.mouseUp(document)
    expect(setWorkerListHeight).not.toHaveBeenCalled()
    expect(useLayoutStore.getState().workerListHeight).toBe(WORKER_LIST_DEFAULT)
  })

  it('caps the rendered height so the workspace zone keeps 96px, without shrinking the stored height', () => {
    const ro = stubResizeObserver()
    useLayoutStore.setState({ workerListOpen: true })
    renderBar()
    expect(ro.observed()).toBe(screen.getByTestId('worker-split-box'))
    const section = screen.getByTestId('worker-list-section')

    // available 300 → 300 − 96 (workspace zone) − 4 (divider) = 200 < 240
    act(() => ro.fire(300))
    expect(section.style.height).toBe('200px')
    expect(useLayoutStore.getState().workerListHeight).toBe(WORKER_LIST_DEFAULT)

    // Plenty of room → the stored height again.
    act(() => ro.fire(1000))
    expect(section.style.height).toBe(`${WORKER_LIST_DEFAULT}px`)
  })

  // Spec §4.2: a short window only caps what is rendered; a drag stores what the user sees at mouseup.
  describe('cap vs. stored height', () => {
    function setup() {
      const ro = stubResizeObserver()
      const setWorkerListHeight = vi.fn(useLayoutStore.getState().setWorkerListHeight)
      useLayoutStore.setState({ workerListOpen: true, workerListHeight: 800, setWorkerListHeight })
      renderBar()
      const section = screen.getByTestId('worker-list-section')
      // available 300 → cap 300 − 96 − 4 = 200
      act(() => ro.fire(300))
      expect(section.style.height).toBe('200px')
      return { ro, section, setWorkerListHeight }
    }

    it('a shrunk window alone never writes the stored height', () => {
      const { setWorkerListHeight } = setup()
      expect(setWorkerListHeight).not.toHaveBeenCalled()
      expect(useLayoutStore.getState().workerListHeight).toBe(800)
    })

    it('a drag starts from the capped height: down 50 then mouseup stores 150, exactly once', () => {
      const { section, setWorkerListHeight } = setup()
      const divider = screen.getByTestId('worker-list-divider')
      fireEvent.mouseDown(divider, { clientY: 500 })
      fireEvent.mouseMove(document, { clientY: 550 })
      expect(section.style.height).toBe('150px')
      expect(setWorkerListHeight).not.toHaveBeenCalled()

      fireEvent.mouseUp(document)
      expect(setWorkerListHeight).toHaveBeenCalledTimes(1)
      expect(setWorkerListHeight).toHaveBeenCalledWith(150)
      expect(useLayoutStore.getState().workerListHeight).toBe(150)
      expect(section.style.height).toBe('150px')
    })

    it('when the cap lifts without a drag, the rendered height returns to the stored value', () => {
      const { ro, section, setWorkerListHeight } = setup()
      act(() => ro.fire(1000))
      expect(section.style.height).toBe('800px')
      expect(setWorkerListHeight).not.toHaveBeenCalled()
    })

    it.each(['mouseup', 'blur'] as const)(
      'a cap that shrinks mid-drag (still ≥ the list minimum): a drag ending on %s stores the on-screen height',
      (end) => {
        const { ro, section, setWorkerListHeight } = setup()
        useLayoutStore.setState({ workerListHeight: 300 })
        // available 500 → cap 400 > stored 300
        act(() => ro.fire(500))
        expect(section.style.height).toBe('300px')

        fireEvent.mouseDown(screen.getByTestId('worker-list-divider'), { clientY: 500 })
        fireEvent.mouseMove(document, { clientY: 450 })
        expect(section.style.height).toBe('350px')
        // available 350 → cap 250, under the 350 draft
        act(() => ro.fire(350))
        expect(section.style.height).toBe('250px')

        if (end === 'mouseup') fireEvent.mouseUp(document)
        else fireEvent.blur(window)
        expect(setWorkerListHeight).toHaveBeenCalledTimes(1)
        expect(setWorkerListHeight).toHaveBeenCalledWith(250)
        expect(useLayoutStore.getState().workerListHeight).toBe(250)
        expect(section.style.height).toBe('250px')
      },
    )

    // Below the list minimum no storable height matches the screen, so the divider does nothing.
    it.each(['mouseup', 'blur'] as const)(
      'a split box too short for the list minimum: a drag ending on %s writes nothing and the list stays at the cap',
      (end) => {
        const { ro, section, setWorkerListHeight } = setup()
        // available 150 → cap 150 − 96 − 4 = 50 < WORKER_LIST_MIN
        act(() => ro.fire(150))
        expect(section.style.height).toBe('50px')

        fireEvent.mouseDown(screen.getByTestId('worker-list-divider'), { clientY: 500 })
        fireEvent.mouseMove(document, { clientY: 450 })
        expect(section.style.height).toBe('50px')
        if (end === 'mouseup') fireEvent.mouseUp(document)
        else fireEvent.blur(window)
        expect(setWorkerListHeight).not.toHaveBeenCalled()
        expect(useLayoutStore.getState().workerListHeight).toBe(800)
        expect(section.style.height).toBe('50px')

        act(() => ro.fire(1000))
        expect(section.style.height).toBe('800px')
      },
    )
  })
})

// Shell polish spec §3 (rule W): the docked list gets a header row — "Workers" and a × — that stays put while the
// list scrolls, like the narrow bar's floating panel.
describe('ActivityBarWide — worker list header', () => {
  beforeEach(() => {
    cleanup()
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })

  it('renders no header and no × while the list is closed', () => {
    renderBar()
    expect(screen.queryByTestId('worker-list-header')).toBeNull()
    expect(screen.queryByTestId('worker-list-close')).toBeNull()
  })

  it('open: the header holds the title and a × button labelled Close', () => {
    useLayoutStore.setState({ workerListOpen: true })
    renderBar()
    const header = screen.getByTestId('worker-list-header')
    expect(header).toHaveClass('flex', 'items-center', 'justify-between', 'shrink-0')
    const close = screen.getByTestId('worker-list-close')
    expect(header).toContainElement(close)
    expect(header.firstElementChild).toHaveTextContent('Workers')
    expect(header.lastElementChild).toBe(close)
    expect(close.tagName).toBe('BUTTON')
    expect(close).toHaveAttribute('type', 'button')
    expect(close).toHaveAttribute('aria-label', 'Close')
    expect(close).toHaveAttribute('title', 'Close')
    expect(screen.getByRole('button', { name: 'Close' })).toBe(close)
  })

  it('the header sits in the section above the scroll area, and the scroll area holds the list but not the header', () => {
    useLayoutStore.setState({ workerListOpen: true })
    renderBar()
    const section = screen.getByTestId('worker-list-section')
    const header = screen.getByTestId('worker-list-header')
    const scroll = screen.getByTestId('worker-list-scroll')
    const list = screen.getByTestId('worker-list')
    expect(Array.from(section.children)).toEqual([header, scroll])
    expect(section).toContainElement(header)
    expect(section).toContainElement(list)
    expect(scroll).toContainElement(list)
    expect(scroll).not.toContainElement(header)
    expect(header).not.toContainElement(list)
  })

  it('× closes the list: the store says closed, nothing of the list stays mounted, the stored height is untouched', () => {
    useLayoutStore.setState({ workerListOpen: true, workerListHeight: 300 })
    renderBar()
    expect(screen.getByTestId('worker-list-section').style.height).toBe('300px')

    fireEvent.click(screen.getByTestId('worker-list-close'))

    expect(useLayoutStore.getState().workerListOpen).toBe(false)
    expect(useLayoutStore.getState().workerListHeight).toBe(300)
    for (const id of ['worker-list', 'worker-list-section', 'worker-list-header', 'worker-list-divider']) {
      expect(screen.queryByTestId(id), id).toBeNull()
    }
    expect(screen.getByRole('button', { name: 'Workers' })).toHaveAttribute('aria-pressed', 'false')

    // Reopening with the Workers button shows the same height again.
    fireEvent.click(screen.getByRole('button', { name: 'Workers' }))
    expect(screen.getByTestId('worker-list-section').style.height).toBe('300px')
  })

  it('× keeps focus where it was on a mouse press, and stays in the tab order', () => {
    useLayoutStore.setState({ workerListOpen: true })
    renderBar()
    const close = screen.getByTestId('worker-list-close')
    expect(fireEvent.mouseDown(close)).toBe(false)
    expect(close.tabIndex).toBeGreaterThanOrEqual(0)
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
