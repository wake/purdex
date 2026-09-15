import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { useAgentStore } from '../../../stores/useAgentStore'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useSessionStore } from '../../../stores/useSessionStore'
import type { Tab } from '../../../types/tab'

const mockOnPointerDown = vi.fn()

vi.mock('@dnd-kit/sortable', () => ({
  useSortable: () => ({
    attributes: {},
    listeners: { onPointerDown: mockOnPointerDown },
    setNodeRef: vi.fn(),
    transform: null,
    transition: null,
    isDragging: false,
  }),
}))

const { InlineTab } = await import('./InlineTab')

const baseTab: Tab = {
  id: 't1',
  kind: 'tmux-session',
  locked: false,
  layout: {
    type: 'leaf',
    pane: {
      id: 't1-pane',
      content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S1', terminated: false },
    },
  },
} as never

beforeEach(() => {
  cleanup()
  mockOnPointerDown.mockClear()
  useAgentStore.setState({
    statuses: {},
    unread: {},
    subagents: {},
    agentTypes: {},
    oscTitles: {},
  })
  useUISettingsStore.setState({
    dynamicTabName: false,
    tabNameTooltipMode: 'both',
    tabIndicatorStyle: 'badge',
    ccIconVariant: 'bot',
    codexIconVariant: 'openai',
    hostColorSidebarStyle: 'gradient',
    hostColorSidebarWidth: 2,
  })
  useSessionStore.setState({
    sessions: { h1: [{ code: 'S1', name: 'work' }] as never },
    activeHostId: null,
    activeCode: null,
  })
  useHostStore.setState({
    runtime: {},
    hosts: { h1: { id: 'h1', name: 'h1', ip: '127.0.0.1', port: 7860, order: 0 } },
  })
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

function setH1Color(color: string) {
  const h1 = useHostStore.getState().hosts.h1
  useHostStore.setState({ hosts: { h1: { ...h1, color } } })
}

function renderInline(tab: Tab = baseTab) {
  return render(
    <InlineTab
      tab={tab}
      isActive={false}
      onSelect={() => {}}
      onClose={() => {}}
      onMiddleClick={() => {}}
      onContextMenu={() => {}}
    />,
  )
}

describe('InlineTab — host color mark', () => {
  it('renders one gradient mark as first child when host has a color', () => {
    setH1Color('#3b82f6')
    renderInline()
    const marks = screen.getAllByTestId('host-color-mark')
    expect(marks).toHaveLength(1)
    expect(marks[0]).toHaveAttribute('data-style', 'gradient')
    expect(screen.getByTestId('inline-tab-row').firstElementChild).toBe(marks[0])
  })

  it('renders no mark when host has no color', () => {
    renderInline()
    expect(screen.queryByTestId('host-color-mark')).toBeNull()
  })

  it("renders no mark when sidebar style is 'none'", () => {
    setH1Color('#3b82f6')
    useUISettingsStore.setState({ hostColorSidebarStyle: 'none' })
    renderInline()
    expect(screen.queryByTestId('host-color-mark')).toBeNull()
  })

  it('reflects left-line width', () => {
    setH1Color('#3b82f6')
    useUISettingsStore.setState({ hostColorSidebarStyle: 'left-line', hostColorSidebarWidth: 4 })
    renderInline()
    const mark = screen.getByTestId('host-color-mark')
    expect(mark).toHaveAttribute('data-style', 'left-line')
    expect(mark.style.width).toBe('4px')
  })

  it('renders no mark for a tab without a tmux-session pane', () => {
    setH1Color('#3b82f6')
    const tab = {
      ...baseTab,
      id: 't2',
      kind: 'new-tab',
      layout: { type: 'leaf', pane: { id: 't2-pane', content: { kind: 'new-tab' } } },
    } as never
    renderInline(tab)
    expect(screen.queryByTestId('host-color-mark')).toBeNull()
  })
})

describe('InlineTab — indicator styles', () => {
  it("renders dot only when tabIndicatorStyle='dot'", () => {
    useUISettingsStore.setState({ tabIndicatorStyle: 'dot' })
    useAgentStore.setState({ statuses: { 'h1:S1': 'running' } })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByTestId('inline-tab-dot')).toBeInTheDocument()
  })

  it("renders icon + overlay dot when tabIndicatorStyle='badge'", () => {
    useUISettingsStore.setState({ tabIndicatorStyle: 'badge' })
    useAgentStore.setState({ statuses: { 'h1:S1': 'running' } })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByTestId('inline-tab-dot-overlay')).toBeInTheDocument()
  })

  it('renders close button visible (DOM present, opacity controlled by hover class)', () => {
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByLabelText(/close work/i)).toBeInTheDocument()
  })

  it('calls onClose when close button clicked', () => {
    const onClose = vi.fn()
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={onClose}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    fireEvent.click(screen.getByLabelText(/close work/i))
    expect(onClose).toHaveBeenCalledWith('t1')
  })
})

describe('InlineTab — close button visibility', () => {
  it('does NOT render close button when tab.locked is true', () => {
    const locked: Tab = { ...baseTab, locked: true } as never
    render(
      <InlineTab
        tab={locked}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.queryByLabelText(/close work/i)).not.toBeInTheDocument()
  })

  it('renders lock icon when tab.locked', () => {
    const locked: Tab = { ...baseTab, locked: true } as never
    render(
      <InlineTab
        tab={locked}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByTestId('inline-tab-lock')).toBeInTheDocument()
  })
})

describe('InlineTab — subagent dots (prop wiring)', () => {
  it('renders outlined proxy dot when subagents contains an is_proxy ref', () => {
    useUISettingsStore.setState({ tabIndicatorStyle: 'dot' })
    useAgentStore.setState({
      statuses: { 'h1:S1': 'running' },
      subagents: {
        'h1:S1': [
          {
            id: 'proxy:codex:200:t200',
            type: 'codex',
            started_at: 0,
            source_pid: 200,
            source_start_time: 't200',
            is_proxy: true,
          },
        ],
      },
    } as never)
    const { container } = render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')
    expect(dot).toBeTruthy()
    expect(dot!.getAttribute('data-is-proxy')).toBe('true')
    expect(dot!.getAttribute('data-subagent-type')).toBe('codex')
  })
})

describe('InlineTab — unread dot', () => {
  it('shows unread dot when unread and not active', () => {
    useAgentStore.setState({ unread: { 'h1:S1': true } })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByTestId('inline-tab-unread')).toBeInTheDocument()
  })

  it('hides unread dot when active', () => {
    useAgentStore.setState({ unread: { 'h1:S1': true } })
    render(
      <InlineTab
        tab={baseTab}
        isActive={true}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.queryByTestId('inline-tab-unread')).not.toBeInTheDocument()
  })
})

describe('InlineTab — host offline', () => {
  it('renders WifiSlash when host is disconnected and tab is not terminated', () => {
    useHostStore.setState({
      runtime: { h1: { status: 'disconnected' } },
    } as never)
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByTestId('inline-tab-host-offline')).toBeInTheDocument()
  })

  it('does NOT render WifiSlash when tab is terminated (session tombstoned)', () => {
    useHostStore.setState({
      runtime: { h1: { status: 'disconnected' } },
    } as never)
    const terminated: Tab = {
      ...baseTab,
      layout: {
        type: 'leaf',
        pane: {
          id: 't1-pane',
          content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S1', terminated: true },
        },
      },
    } as never
    render(
      <InlineTab
        tab={terminated}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.queryByTestId('inline-tab-host-offline')).not.toBeInTheDocument()
  })
})

describe('InlineTab — statusline display', () => {
  it('shows "{paneTitle} - {baseLabel}" when dynamicTabName + pane_title + agentType all set', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'S1', name: 'work', pane_title: 'my-feature' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true })
    useAgentStore.setState({
      agentTypes: { 'h1:S1': 'cc' },
      oscTitles: { 'h1:S1': 'my-feature' },
    })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const title = screen.getByTestId('inline-tab-title')
    expect(title.textContent).toBe('my-feature - work')
  })

  it('shows only baseLabel when pane_title absent', () => {
    useUISettingsStore.setState({ dynamicTabName: true })
    useAgentStore.setState({
      agentTypes: { 'h1:S1': 'cc' },
      oscTitles: {},
    })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const title = screen.getByTestId('inline-tab-title')
    expect(title.textContent).toBe('work')
    expect(title.textContent).not.toContain(' - ')
  })

  it('shows only baseLabel when dynamicTabName is disabled', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'S1', name: 'work', pane_title: 'my-feature' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: false })
    useAgentStore.setState({
      agentTypes: { 'h1:S1': 'cc' },
      oscTitles: { 'h1:S1': 'my-feature' },
    })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const title = screen.getByTestId('inline-tab-title')
    expect(title.textContent).toBe('work')
  })

  it('renders HoverTooltip with combined text (not native title attr)', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'S1', name: 'work', pane_title: 'my-feature' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true })
    useAgentStore.setState({
      agentTypes: { 'h1:S1': 'cc' },
      oscTitles: { 'h1:S1': 'my-feature' },
    })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const title = screen.getByTestId('inline-tab-title')
    // Native title attribute must be gone
    expect(title.getAttribute('title')).toBeNull()
    // HoverTooltip renders combined text as a sibling
    const tooltip = screen.getByTestId('inline-tab-tooltip')
    expect(tooltip.textContent).toBe('my-feature - work')
  })

  it('uses placement=right for tooltip (escapes sidebar overflow-y-auto clipping)', () => {
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const tooltip = screen.getByTestId('inline-tab-tooltip')
    expect(tooltip).toHaveAttribute('data-placement', 'right')
  })

  it('does not render tooltip when left tab tooltip is disabled', () => {
    useUISettingsStore.setState({ tabNameTooltipMode: 'top' })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.queryByTestId('inline-tab-tooltip')).toBeNull()
  })
})

describe('InlineTab — pointer down (dnd-kit integration)', () => {
  it('forwards pointerdown to dnd-kit handler', () => {
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    fireEvent.pointerDown(screen.getByTestId('inline-tab-row'))
    expect(mockOnPointerDown).toHaveBeenCalled()
  })

  it('calls preventDefault on active tab pointerdown', () => {
    render(
      <InlineTab
        tab={baseTab}
        isActive
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const el = screen.getByTestId('inline-tab-row')
    const event = new PointerEvent('pointerdown', { bubbles: true, cancelable: true })
    el.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
  })

  it('does not call preventDefault on inactive tab pointerdown', () => {
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const el = screen.getByTestId('inline-tab-row')
    const event = new PointerEvent('pointerdown', { bubbles: true, cancelable: true })
    el.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
  })
})
