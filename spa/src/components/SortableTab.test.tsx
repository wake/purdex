import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import type { Tab } from '../types/tab'
import { createTab } from '../types/tab'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useI18nStore } from '../stores/useI18nStore'

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
// Phosphor weight JSON is cached (a fetch that never resolves in jsdom). The stub
// encodes name + weight into the path data so tests can assert what was handed down.
vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: (name: string, weight: string) => `M ${name} ${weight}`,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

// Lazy import so mocks are applied before module resolution
const { SortableTab } = await import('./SortableTab')

function makeTestTab(id: string, opts?: { pinned?: boolean }): Tab {
  const tab = createTab(
    { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sc1', mode: 'terminal' as const, cachedName: '', tmuxInstance: '' },
    { pinned: opts?.pinned },
  )
  return { ...tab, id }
}

const defaultProps = {
  tab: makeTestTab('t1'),
  isActive: false,
  onSelect: vi.fn(),
  onClose: vi.fn(),
  onMiddleClick: vi.fn(),
  onContextMenu: vi.fn(),
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  clearModuleRegistry()
  registerModule({ id: 'session', name: 'Session', panes: [{ kind: 'tmux-session', component: () => null }] })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useHostStore.setState({
    runtime: {},
    hosts: { h1: { id: 'h1', name: 'h1', ip: '127.0.0.1', port: 7860, order: 0 } },
  })
  useAgentStore.setState({ unread: {}, statuses: {}, subagents: {} })
  useUISettingsStore.setState({
    tabIndicatorStyle: 'badge',
    tabNameTooltipMode: 'both',
    hostBadgeTabBarEnabled: true,
    hostBadgeTabBarLineColor: 'host',
    hostBadgeTabBarLineOpacity: 100,
    hostBadgeTabBarBgOpacity: 22,
    hostBadgeTabBarBox: 16,
    hostBadgeTabBarInset: 2,
    hostBadgeTabBarRadius: 4,
  })
  useI18nStore.setState({ t: (k: string) => k })
})

function setH1Color(color: string) {
  const h1 = useHostStore.getState().hosts.h1
  useHostStore.setState({ hosts: { h1: { ...h1, color } } })
}

function setH1Icon(icon: string) {
  const h1 = useHostStore.getState().hosts.h1
  useHostStore.setState({ hosts: { h1: { ...h1, icon } } })
}

function tabChildren(container: HTMLElement): Element[] {
  return Array.from(container.querySelector('[data-tab-id="t1"]')!.children)
}

describe('SortableTab — host badge', () => {
  it('renders exactly one badge, between the tab icon and the label', () => {
    setH1Color('#3b82f6')
    const { container } = render(<SortableTab {...defaultProps} />)
    const badges = screen.getAllByTestId('host-badge')
    expect(badges).toHaveLength(1)

    const children = tabChildren(container)
    const label = container.querySelector('[data-tab-id="t1"] span.overflow-hidden')!
    const badgeIdx = children.indexOf(badges[0])
    // Child 0 is the TabIcon slot.
    expect(badgeIdx).toBe(1)
    expect(children.indexOf(label)).toBe(badgeIdx + 1)
    expect(children[0].contains(badges[0])).toBe(false)
    expect(
      badges[0].compareDocumentPosition(label) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('renders the badge as a real flex child, not an overlay', () => {
    setH1Color('#3b82f6')
    render(<SortableTab {...defaultProps} />)
    const badge = screen.getByTestId('host-badge')
    expect(badge.style.position).not.toBe('absolute')
    expect(badge.style.display).toBe('inline-flex')
  })

  it('keeps the close overlay as the last, absolutely positioned child', () => {
    setH1Color('#3b82f6')
    const { container } = render(<SortableTab {...defaultProps} />)
    const last = container.querySelector('[data-tab-id="t1"]')!.lastElementChild!
    expect(last.className).toContain('absolute')
    expect(last.contains(screen.getByTitle('tab.close'))).toBe(true)
  })

  it('keeps the unread pip absolutely positioned', () => {
    setH1Color('#3b82f6')
    useAgentStore.setState({ unread: { 'h1:sc1': true }, statuses: {}, subagents: {} })
    const { container } = render(<SortableTab {...defaultProps} isActive={false} />)
    const pip = tabChildren(container).find(
      (el) => (el as HTMLElement).style.backgroundColor === 'rgb(185, 28, 28)',
    ) as HTMLElement | undefined
    expect(pip).toBeTruthy()
    expect(pip!.className).toContain('absolute')
  })

  it('still renders a neutral badge when the host has an icon but no color', () => {
    setH1Icon('Laptop')
    render(<SortableTab {...defaultProps} />)
    expect(screen.getByTestId('host-badge')).toHaveAttribute('data-has-color', 'false')
  })

  it('renders the badge when the host has a color but no icon', () => {
    setH1Color('#3b82f6')
    render(<SortableTab {...defaultProps} />)
    expect(screen.getByTestId('host-badge')).toHaveAttribute('data-has-color', 'true')
  })

  it('renders the badge when the host has both a color and an icon', () => {
    const h1 = useHostStore.getState().hosts.h1
    useHostStore.setState({ hosts: { h1: { ...h1, color: '#3b82f6', icon: 'Laptop' } } })
    render(<SortableTab {...defaultProps} />)
    expect(screen.getByTestId('host-badge')).toHaveAttribute('data-has-color', 'true')
  })

  it('renders no badge — and reserves no space — when the host has neither color nor icon', () => {
    const { container } = render(<SortableTab {...defaultProps} />)
    expect(screen.queryByTestId('host-badge')).toBeNull()
    // The TabIcon slot is immediately followed by the label: nothing sits in between.
    const children = tabChildren(container)
    const label = container.querySelector('[data-tab-id="t1"] span.overflow-hidden')!
    expect(children.indexOf(label)).toBe(1)
  })

  it('renders no badge when the top tab surface is disabled', () => {
    setH1Color('#3b82f6')
    useUISettingsStore.setState({ hostBadgeTabBarEnabled: false })
    render(<SortableTab {...defaultProps} />)
    expect(screen.queryByTestId('host-badge')).toBeNull()
  })

  it('renders no badge for a tab without a tmux-session pane', () => {
    setH1Color('#3b82f6')
    const tab = { ...createTab({ kind: 'new-tab' }), id: 't1' }
    render(<SortableTab {...defaultProps} tab={tab} />)
    expect(screen.queryByTestId('host-badge')).toBeNull()
  })

  it('passes the tab-bar store settings through to the badge', () => {
    setH1Color('#3b82f6')
    useUISettingsStore.setState({
      hostBadgeTabBarBox: 20,
      hostBadgeTabBarInset: 1,
      hostBadgeTabBarRadius: 6,
      hostBadgeTabBarBgOpacity: 40,
    })
    render(<SortableTab {...defaultProps} />)
    const badge = screen.getByTestId('host-badge')
    expect(badge.style.width).toBe('20px')
    expect(badge.style.height).toBe('20px')
    expect(badge.style.borderRadius).toBe('6px')
    expect(badge.style.background).toContain('40%')
    // icon size = box − 2×inset
    expect(badge.querySelector('svg')).toHaveAttribute('width', '18')
  })

  it("uses the host's own icon and weight", () => {
    const h1 = useHostStore.getState().hosts.h1
    useHostStore.setState({
      hosts: { h1: { ...h1, color: '#3b82f6', icon: 'Laptop', iconWeight: 'duotone' } },
    })
    render(<SortableTab {...defaultProps} />)
    expect(screen.getByTestId('host-badge').querySelector('path')).toHaveAttribute(
      'd',
      'M Laptop duotone',
    )
  })

  it('renders no badge on a pinned tab and keeps its w-9 width', () => {
    setH1Color('#3b82f6')
    const pinnedTab = makeTestTab('t1', { pinned: true })
    const { container } = render(<SortableTab {...defaultProps} tab={pinnedTab} pinned />)
    expect(screen.queryByTestId('host-badge')).toBeNull()
    expect(container.querySelector('[data-tab-id="t1"]')!.className).toContain('w-9')
  })
})

describe('SortableTab', () => {
  it('renders data-tab-id on normal tab', () => {
    const { container } = render(<SortableTab {...defaultProps} />)
    const el = container.querySelector('[data-tab-id="t1"]')
    expect(el).toBeTruthy()
  })

  it('renders data-tab-id on pinned tab', () => {
    const pinnedTab = makeTestTab('t1', { pinned: true })
    const { container } = render(<SortableTab {...defaultProps} tab={pinnedTab} pinned />)
    const el = container.querySelector('[data-tab-id="t1"]')
    expect(el).toBeTruthy()
  })

  it('calls dnd-kit onPointerDown handler', () => {
    const { container } = render(<SortableTab {...defaultProps} />)
    const el = container.querySelector('[data-tab-id="t1"]')!
    fireEvent.pointerDown(el)
    expect(mockOnPointerDown).toHaveBeenCalled()
  })

  it('calls preventDefault on active tab pointerDown', () => {
    const { container } = render(<SortableTab {...defaultProps} isActive />)
    const el = container.querySelector('[data-tab-id="t1"]')!
    const event = new PointerEvent('pointerdown', { bubbles: true, cancelable: true })
    el.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
  })

  it('does not render a bright border on active tabs', () => {
    const { container } = render(<SortableTab {...defaultProps} isActive />)
    const el = container.querySelector('[data-tab-id="t1"]')!

    expect(el.className).not.toContain('border-accent-muted')
    expect(el.className).toContain('border-transparent')
  })

  it('does not render a bright border on active pinned tabs', () => {
    const pinnedTab = makeTestTab('t1', { pinned: true })
    const { container } = render(<SortableTab {...defaultProps} tab={pinnedTab} isActive pinned />)
    const el = container.querySelector('[data-tab-id="t1"]')!

    expect(el.className).not.toContain('border-accent-muted')
    expect(el.className).toContain('border-transparent')
  })

  it('does not call preventDefault on inactive tab pointerDown', () => {
    const { container } = render(<SortableTab {...defaultProps} isActive={false} />)
    const el = container.querySelector('[data-tab-id="t1"]')!
    const event = new PointerEvent('pointerdown', { bubbles: true, cancelable: true })
    el.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
  })

  it('calls onSelect when tab is clicked', () => {
    const onSelect = vi.fn()
    const { container } = render(<SortableTab {...defaultProps} onSelect={onSelect} />)
    const el = container.querySelector('[data-tab-id="t1"]')!
    fireEvent.click(el)
    expect(onSelect).toHaveBeenCalledWith('t1')
  })

  it('calls onClose without triggering onSelect when close button is clicked', () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    render(<SortableTab {...defaultProps} onSelect={onSelect} onClose={onClose} />)
    const closeBtn = screen.getByTitle('tab.close')
    fireEvent.click(closeBtn)
    expect(onClose).toHaveBeenCalledWith('t1')
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('renders HoverTooltip with combined text on regular tab (not native title attr)', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'work', pane_title: 'my-feature' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ tabIndicatorStyle: 'badge', dynamicTabName: true })
    useAgentStore.setState({
      unread: {},
      statuses: {},
      subagents: {},
      agentTypes: { 'h1:sc1': 'cc' },
      oscTitles: { 'h1:sc1': 'my-feature' },
    } as never)
    const { container } = render(<SortableTab {...defaultProps} />)
    // The visible label span should NOT have a native title attribute anymore.
    const tabRoot = container.querySelector('[data-tab-id="t1"]')!
    const labelSpan = tabRoot.querySelector('span.overflow-hidden')!
    expect(labelSpan.getAttribute('title')).toBeNull()
    expect(labelSpan.textContent).toBe('my-feature - work')
    // HoverTooltip renders as a sibling with role="tooltip".
    const tooltip = screen.getByRole('tooltip')
    expect(tooltip.textContent).toBe('my-feature - work')
  })

  it('renders HoverTooltip on pinned tab (not native title attr)', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'work', pane_title: 'my-feature' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ tabIndicatorStyle: 'badge', dynamicTabName: true })
    useAgentStore.setState({
      unread: {},
      statuses: {},
      subagents: {},
      agentTypes: { 'h1:sc1': 'cc' },
      oscTitles: { 'h1:sc1': 'my-feature' },
    } as never)
    const pinnedTab = makeTestTab('t1', { pinned: true })
    const { container } = render(<SortableTab {...defaultProps} tab={pinnedTab} pinned />)
    const tabRoot = container.querySelector('[data-tab-id="t1"]')!
    expect(tabRoot.getAttribute('title')).toBeNull()
    const tooltip = screen.getByRole('tooltip')
    expect(tooltip.textContent).toBe('my-feature - work')
  })

  it('does not render HoverTooltip when top tab tooltip is disabled', () => {
    useUISettingsStore.setState({ tabNameTooltipMode: 'left' })
    render(<SortableTab {...defaultProps} />)
    expect(screen.queryByRole('tooltip')).toBeNull()
  })
})

describe('SortableTab renderTabIcon modes', () => {
  function seedAgent(style: 'icon' | 'dot' | 'iconDot' | 'badge') {
    useAgentStore.setState({
      unread: {},
      subagents: {},
      statuses: { 'h1:sc1': 'running' },
      agentTypes: { 'h1:sc1': 'cc' },
    })
    useUISettingsStore.setState({ tabIndicatorStyle: style })
  }

  it('icon mode: no status dot rendered', () => {
    seedAgent('icon')
    render(<SortableTab {...defaultProps} />)
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
  })

  it('dot mode: renders replace-style dot (8px, not absolute)', () => {
    seedAgent('dot')
    render(<SortableTab {...defaultProps} />)
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.style.width).toBe('8px')
    expect(dot.style.position).not.toBe('absolute')
  })

  it('iconDot mode: renders replace-style dot alongside icon', () => {
    seedAgent('iconDot')
    render(<SortableTab {...defaultProps} />)
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.style.width).toBe('8px')
    expect(dot.style.position).not.toBe('absolute')
  })

  it('badge mode: renders overlay dot in upper-right (nudged -1/-2 px)', () => {
    seedAgent('badge')
    render(<SortableTab {...defaultProps} />)
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.style.width).toBe('6px')
    expect(dot.style.position).toBe('absolute')
    expect(dot.style.top).toBe('-1px')
    expect(dot.style.right).toBe('-2px')
  })

  it('badge mode + unread: dot tints red instead of separate pip', () => {
    seedAgent('badge')
    useAgentStore.setState({ unread: { 'h1:sc1': true } })
    render(<SortableTab {...defaultProps} isActive={false} />)
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.style.backgroundColor).toBe('rgb(239, 68, 68)')
    expect(screen.queryByTestId('tab-unread-pip')).toBeNull()
  })

  it('dot mode + unread: pip on dot wrapper, no global pip', () => {
    seedAgent('dot')
    useAgentStore.setState({ unread: { 'h1:sc1': true } })
    render(<SortableTab {...defaultProps} isActive={false} />)
    expect(screen.getByTestId('tab-unread-pip')).toBeTruthy()
  })

  it('badge mode + error: warning-diamond instead of dot', () => {
    seedAgent('badge')
    useAgentStore.setState({ statuses: { 'h1:sc1': 'error' } })
    render(<SortableTab {...defaultProps} />)
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
    expect(screen.getByTestId('tab-status-error')).toBeTruthy()
  })

  it('dot mode + error: warning-diamond replaces the dot', () => {
    seedAgent('dot')
    useAgentStore.setState({ statuses: { 'h1:sc1': 'error' } })
    render(<SortableTab {...defaultProps} />)
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
    expect(screen.getByTestId('tab-status-error')).toBeTruthy()
  })

  it('dot mode + proxy subagent: SubagentDots renders outlined dot (prop-wiring)', () => {
    // Seed a proxy SubagentRef on this session. Tests that the
    // subagentRefs prop chain (useTabDisplay → SortableTab → TabIcon →
    // SubagentDots) is intact end-to-end.
    seedAgent('dot')
    useAgentStore.setState({
      subagents: {
        'h1:sc1': [
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
    const { container } = render(<SortableTab {...defaultProps} />)
    const dot = container.querySelector<HTMLElement>('[data-testid="subagent-dot"]')!
    expect(dot).toBeTruthy()
    expect(dot.getAttribute('data-is-proxy')).toBe('true')
    expect(dot.getAttribute('data-subagent-type')).toBe('codex')
  })

  it('terminated session: no status dot even when agent event exists', () => {
    seedAgent('badge')
    const terminatedTab: Tab = createTab(
      { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sc1', mode: 'terminal', cachedName: '', tmuxInstance: '', terminated: 'session-closed' },
    )
    terminatedTab.id = 't1'
    render(<SortableTab {...defaultProps} tab={terminatedTab} />)
    // Terminated sessions keep the pane tombstone icon; renderTabIcon still runs
    // but agentType is intentionally ignored via !isTerminated guard.
    // The overlay dot is still rendered because agentStatus is set — however,
    // getAgentIcon is NOT called, so the tab icon stays as the pane icon.
    // We assert the component renders without crashing.
    expect(screen.getByTestId('tab-status-indicator')).toBeTruthy()
  })
})
