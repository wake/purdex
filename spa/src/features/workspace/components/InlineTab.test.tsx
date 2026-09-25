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

// The host badge renders a WorkspaceIcon, which only emits an <svg> once the
// Phosphor weight JSON is cached (a fetch that never resolves in jsdom).
vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: (name: string, weight: string) => `M ${name} ${weight}`,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
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
    hostBadgeSidebarEnabled: true,
    hostBadgeSidebarLineColor: 'host',
    hostBadgeSidebarBox: 16,
    hostBadgeSidebarInset: 2,
    hostBadgeSidebarRadius: 4,
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

function setH1Icon(icon: string) {
  const h1 = useHostStore.getState().hosts.h1
  useHostStore.setState({ hosts: { h1: { ...h1, icon } } })
}

function renderInline(tab: Tab = baseTab, opts: { isActive?: boolean } = {}) {
  return render(
    <InlineTab
      tab={tab}
      isActive={opts.isActive ?? false}
      onSelect={() => {}}
      onClose={() => {}}
      onMiddleClick={() => {}}
      onContextMenu={() => {}}
    />,
  )
}

function rowChildren(): Element[] {
  return Array.from(screen.getByTestId('inline-tab-row').children)
}

describe('InlineTab — host badge', () => {
  it('renders exactly one badge, between the icon slot and the title', () => {
    setH1Color('#3b82f6')
    renderInline()
    const badges = screen.getAllByTestId('host-badge')
    expect(badges).toHaveLength(1)

    const children = rowChildren()
    const title = screen.getByTestId('inline-tab-title')
    const badgeIdx = children.indexOf(badges[0])
    const titleIdx = children.indexOf(title)
    // Child 0 is the icon slot rendered by renderInlineTabIcon.
    expect(badgeIdx).toBe(1)
    expect(titleIdx).toBe(badgeIdx + 1)
    expect(children[0].contains(badges[0])).toBe(false)
    expect(
      badges[0].compareDocumentPosition(title) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('renders the badge as a real flex child, not an overlay', () => {
    setH1Color('#3b82f6')
    renderInline()
    const badge = screen.getByTestId('host-badge')
    expect(badge.style.position).not.toBe('absolute')
    expect(badge.style.display).toBe('inline-flex')
  })

  it('keeps the title flex-1 + truncate and the unread pip absolute', () => {
    setH1Color('#3b82f6')
    useAgentStore.setState({ unread: { 'h1:S1': true } })
    renderInline()
    const title = screen.getByTestId('inline-tab-title')
    expect(title.className).toContain('flex-1')
    expect(title.className).toContain('truncate')
    expect(screen.getByTestId('inline-tab-unread').className).toContain('absolute')
  })

  it('keeps the offline and lock icons after the title', () => {
    setH1Color('#3b82f6')
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never)
    renderInline({ ...baseTab, locked: true } as never)
    const children = rowChildren()
    const titleIdx = children.indexOf(screen.getByTestId('inline-tab-title'))
    expect(children.indexOf(screen.getByTestId('inline-tab-host-offline'))).toBeGreaterThan(titleIdx)
    expect(children.indexOf(screen.getByTestId('inline-tab-lock'))).toBeGreaterThan(titleIdx)
  })

  it('gives the lock the same 16x16 box as the close button', () => {
    setH1Color('#3b82f6')
    renderInline({ ...baseTab, locked: true } as never)
    const lock = screen.getByTestId('inline-tab-lock')
    expect(lock.className).toContain('h-4')
    expect(lock.className).toContain('w-4')
    expect(lock.className).toContain('justify-center')
  })

  it('still renders a neutral badge when the host has an icon but no color', () => {
    setH1Icon('Laptop')
    renderInline()
    const badge = screen.getByTestId('host-badge')
    expect(badge).toHaveAttribute('data-has-color', 'false')
  })

  it('renders the badge when the host has a color but no icon', () => {
    setH1Color('#3b82f6')
    renderInline()
    expect(screen.getByTestId('host-badge')).toHaveAttribute('data-has-color', 'true')
  })

  it('renders the badge when the host has both a color and an icon', () => {
    const h1 = useHostStore.getState().hosts.h1
    useHostStore.setState({ hosts: { h1: { ...h1, color: '#3b82f6', icon: 'Laptop' } } })
    renderInline()
    expect(screen.getByTestId('host-badge')).toHaveAttribute('data-has-color', 'true')
  })

  it('renders no badge — and reserves no space — when the host has neither color nor icon', () => {
    renderInline()
    expect(screen.queryByTestId('host-badge')).toBeNull()
    // The icon slot is immediately followed by the title: nothing sits in between.
    const children = rowChildren()
    expect(children.indexOf(screen.getByTestId('inline-tab-title'))).toBe(1)
  })

  it('renders no badge when the sidebar surface is disabled', () => {
    setH1Color('#3b82f6')
    useUISettingsStore.setState({ hostBadgeSidebarEnabled: false })
    renderInline()
    expect(screen.queryByTestId('host-badge')).toBeNull()
  })

  it('renders no badge for a tab without a tmux-session pane', () => {
    setH1Color('#3b82f6')
    const tab = {
      ...baseTab,
      id: 't2',
      kind: 'new-tab',
      layout: { type: 'leaf', pane: { id: 't2-pane', content: { kind: 'new-tab' } } },
    } as never
    renderInline(tab)
    expect(screen.queryByTestId('host-badge')).toBeNull()
  })

  it('passes the sidebar store settings through to the badge', () => {
    setH1Color('#3b82f6')
    useUISettingsStore.setState({
      hostBadgeSidebarBox: 20,
      hostBadgeSidebarInset: 1,
      hostBadgeSidebarRadius: 6,
    })
    renderInline()
    const badge = screen.getByTestId('host-badge')
    expect(badge.style.width).toBe('20px')
    expect(badge.style.height).toBe('20px')
    expect(badge.style.borderRadius).toBe('6px')
    expect(badge.style.background).toBe('rgba(59, 130, 246, 0.22)')
    // icon size = box − 2×inset
    expect(badge.querySelector('svg')).toHaveAttribute('width', '18')
  })

  it("uses the host's own icon and weight", () => {
    const h1 = useHostStore.getState().hosts.h1
    useHostStore.setState({
      hosts: { h1: { ...h1, color: '#3b82f6', icon: 'Laptop', iconWeight: 'duotone' } },
    })
    renderInline()
    const path = screen.getByTestId('host-badge').querySelector('path')
    expect(path).toHaveAttribute('d', 'M Laptop duotone')
  })

  it('marks the row data-active and lets the badge read main on the active row', () => {
    setH1Color('#3b82f6')
    renderInline(undefined, { isActive: true })
    const row = screen.getByTestId('inline-tab-row')
    expect(row).toHaveAttribute('data-active', 'true')
    const badge = screen.getByTestId('host-badge')
    expect(badge.style.getPropertyValue('--hb-main')).toBe('rgba(59, 130, 246, 1)')
    expect(badge.style.getPropertyValue('--hb-middle')).toBe('rgba(59, 130, 246, 0.6)')
  })

  it('marks an inactive row data-active=false', () => {
    setH1Color('#3b82f6')
    renderInline(undefined, { isActive: false })
    expect(screen.getByTestId('inline-tab-row')).toHaveAttribute('data-active', 'false')
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
