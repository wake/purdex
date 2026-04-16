import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

// Mock icon-path-cache to avoid CSR deep-import resolution failures in test env
vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { ActivityBar } from './ActivityBar'
import type { Workspace } from '../../../types/tab'
import { useTabStore } from '../../../stores/useTabStore'
import { useAgentStore } from '../../../stores/useAgentStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import type { Tab } from '../../../types/tab'

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

const mockWorkspaces: Workspace[] = [
  { id: 'ws-1', name: 'Project A', icon: '🔧', tabs: ['t1', 't2'], activeTabId: 't1' },
  { id: 'ws-2', name: 'Server', icon: '🖥', tabs: ['t3'], activeTabId: 't3' },
]

const defaultProps = {
  workspaces: mockWorkspaces,
  activeWorkspaceId: 'ws-1' as string | null,
  activeStandaloneTabId: null as string | null,
  onSelectWorkspace: vi.fn(),
  onSelectHome: vi.fn(),
  standaloneTabIds: [] as string[],
  onAddWorkspace: vi.fn(),
  onOpenHosts: vi.fn(),
  onOpenSettings: vi.fn(),
}

describe('ActivityBar', () => {
  beforeEach(() => {
    cleanup()
    vi.clearAllMocks()
    useTabStore.setState({ tabs: {} })
    useAgentStore.setState({ unread: {}, statuses: {} })
  })

  it('renders workspace icons', () => {
    render(<ActivityBar {...defaultProps} />)
    expect(screen.getByRole('button', { name: 'Project A' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Server' })).toBeTruthy()
  })

  it('highlights active workspace', () => {
    render(<ActivityBar {...defaultProps} />)
    const activeBtn = screen.getByRole('button', { name: 'Project A' })
    expect(activeBtn.className).toContain('ring')
  })

  it('calls onSelectWorkspace on click', () => {
    const onSelect = vi.fn()
    render(<ActivityBar {...defaultProps} onSelectWorkspace={onSelect} />)
    fireEvent.click(screen.getByRole('button', { name: 'Server' }))
    expect(onSelect).toHaveBeenCalledWith('ws-2')
  })

  it('renders Home button', () => {
    render(<ActivityBar {...defaultProps} />)
    expect(screen.getByTitle('Home')).toBeTruthy()
  })

  it('highlights Home when no active workspace', () => {
    render(<ActivityBar {...defaultProps} activeWorkspaceId={null} />)
    const homeBtn = screen.getByTitle('Home')
    expect(homeBtn.className).toContain('ring-2')
  })

  it('calls onSelectHome on Home click', () => {
    const onSelectHome = vi.fn()
    render(<ActivityBar {...defaultProps} onSelectHome={onSelectHome} />)
    fireEvent.click(screen.getByTitle('Home'))
    expect(onSelectHome).toHaveBeenCalled()
  })

  it('shows unread badge on Home when standalone tabs have unreads and workspace is active', () => {
    useTabStore.setState({
      tabs: {
        s1: mockSessionTab('s1', 'h1', 'sa'),
        s2: mockSessionTab('s2', 'h1', 'sb'),
        s3: mockSessionTab('s3', 'h1', 'sc'),
      },
    })
    useAgentStore.setState({ unread: { 'h1:sa': true, 'h1:sb': true } })

    render(<ActivityBar {...defaultProps} standaloneTabIds={['s1', 's2', 's3']} />)
    const badge = screen.getByTestId('home-unread-badge')
    expect(badge.textContent).toBe('2')
  })

  it('hides Home badge when standalone tabs have no unreads', () => {
    useTabStore.setState({
      tabs: { s1: mockSessionTab('s1', 'h1', 'sa') },
    })
    useAgentStore.setState({ unread: {} })

    const { container } = render(<ActivityBar {...defaultProps} standaloneTabIds={['s1']} />)
    expect(container.querySelector('[data-testid="home-unread-badge"]')).toBeNull()
  })

  it('hides Home unread badge when in Home mode', () => {
    useTabStore.setState({
      tabs: { s1: mockSessionTab('s1', 'h1', 'sa') },
    })
    useAgentStore.setState({ unread: { 'h1:sa': true } })

    const { container } = render(<ActivityBar {...defaultProps} activeWorkspaceId={null} standaloneTabIds={['s1']} />)
    expect(container.querySelector('[data-testid="home-unread-badge"]')).toBeNull()
  })

  it('shows Home status dot when standalone tab has running agent', () => {
    useTabStore.setState({
      tabs: { s1: mockSessionTab('s1', 'h1', 'sa') },
    })
    useAgentStore.setState({ statuses: { 'h1:sa': 'running' } })

    const { container } = render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" standaloneTabIds={['s1']} />)

    const dots = container.querySelectorAll('.animate-breathe')
    // One for Home button — workspace dots won't render since ws-1 has no matching tab status
    expect(dots.length).toBeGreaterThanOrEqual(1)
  })

  it('hides Home status dot when in Home mode', () => {
    useTabStore.setState({
      tabs: { s1: mockSessionTab('s1', 'h1', 'sa') },
    })
    useAgentStore.setState({ statuses: { 'h1:sa': 'running' } })

    const { container } = render(<ActivityBar {...defaultProps} activeWorkspaceId={null} standaloneTabIds={['s1']} />)

    // Home is active — dot should not render for Home
    const dots = container.querySelectorAll('.animate-breathe')
    expect(dots).toHaveLength(0)
  })

  it('shows Home unread badge when standalone tab is focused and other standalone tabs have unreads', () => {
    useTabStore.setState({
      tabs: {
        s1: mockSessionTab('s1', 'h1', 'sa'),
        s2: mockSessionTab('s2', 'h1', 'sb'),
      },
    })
    useAgentStore.setState({ unread: { 'h1:sb': true } })

    render(<ActivityBar {...defaultProps} activeWorkspaceId={null} activeStandaloneTabId="s1" standaloneTabIds={['s1', 's2']} />)
    const badge = screen.getByTestId('home-unread-badge')
    expect(badge.textContent).toBe('1')
  })

  it('shows Home status dot when standalone tab is focused and other standalone tabs have running agent', () => {
    useTabStore.setState({
      tabs: {
        s1: mockSessionTab('s1', 'h1', 'sa'),
        s2: mockSessionTab('s2', 'h1', 'sb'),
      },
    })
    useAgentStore.setState({ statuses: { 'h1:sb': 'running' } })

    const { container } = render(<ActivityBar {...defaultProps} activeWorkspaceId={null} activeStandaloneTabId="s1" standaloneTabIds={['s1', 's2']} />)
    const dots = container.querySelectorAll('.animate-breathe')
    expect(dots.length).toBeGreaterThanOrEqual(1)
  })

  it('excludes focused standalone tab from Home badge unread count', () => {
    useTabStore.setState({
      tabs: {
        s1: mockSessionTab('s1', 'h1', 'sa'),
        s2: mockSessionTab('s2', 'h1', 'sb'),
      },
    })
    // s1 (focused) has unread, s2 does not — badge should NOT show
    useAgentStore.setState({ unread: { 'h1:sa': true } })

    const { container } = render(<ActivityBar {...defaultProps} activeWorkspaceId={null} activeStandaloneTabId="s1" standaloneTabIds={['s1', 's2']} />)
    expect(container.querySelector('[data-testid="home-unread-badge"]')).toBeNull()
  })

  it('shows Home static dot for waiting status', () => {
    useTabStore.setState({
      tabs: { s1: mockSessionTab('s1', 'h1', 'sa') },
    })
    useAgentStore.setState({ statuses: { 'h1:sa': 'waiting' } })

    const { container } = render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" standaloneTabIds={['s1']} />)

    const dots = container.querySelectorAll('.rounded-full[style]')
    const waitingDot = Array.from(dots).find(d =>
      (d as HTMLElement).style.backgroundColor === 'rgb(250, 204, 21)'
    )
    expect(waitingDot).toBeTruthy()
    expect(waitingDot!.className).not.toContain('animate-breathe')
  })

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

  it('hides unread badge on active workspace even when it has unread tabs', () => {
    useTabStore.setState({
      tabs: {
        t1: mockSessionTab('t1', 'h1', 's1'),
        t2: mockSessionTab('t2', 'h1', 's2'),
        t3: mockSessionTab('t3', 'h1', 's3'),
      },
    })
    // ws-1 (active) has t1 unread, ws-2 (inactive) has t3 unread
    useAgentStore.setState({ unread: { 'h1:s1': true, 'h1:s3': true } })

    render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

    // Only ws-2's badge should show — ws-1 is active so its badge is suppressed
    const badges = screen.getAllByTestId('ws-unread-badge')
    expect(badges).toHaveLength(1)
    expect(badges[0].textContent).toBe('1')
  })

  it('includes unread count in aria-label', () => {
    useTabStore.setState({
      tabs: { t3: mockSessionTab('t3', 'h1', 's3') },
    })
    useAgentStore.setState({ unread: { 'h1:s3': true } })

    render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

    expect(screen.getByLabelText('Server, 1 unread')).toBeTruthy()
  })

  it('shows status dot on inactive workspace with running agent', () => {
    useTabStore.setState({
      tabs: { t3: mockSessionTab('t3', 'h1', 's3') },
    })
    useAgentStore.setState({ statuses: { 'h1:s3': 'running' } })

    const { container } = render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

    const dot = container.querySelector('.animate-breathe')
    expect(dot).toBeTruthy()
    expect(dot!.className).toContain('rounded-full')
  })

  it('hides status dot on active workspace', () => {
    useTabStore.setState({
      tabs: { t1: mockSessionTab('t1', 'h1', 's1') },
    })
    useAgentStore.setState({ statuses: { 'h1:s1': 'running' } })

    const { container } = render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

    // ws-1 is active — dot should not render
    const dots = container.querySelectorAll('.animate-breathe')
    expect(dots).toHaveLength(0)
  })

  it('shows static dot (no breathe) for waiting status', () => {
    useTabStore.setState({
      tabs: { t3: mockSessionTab('t3', 'h1', 's3') },
    })
    useAgentStore.setState({ statuses: { 'h1:s3': 'waiting' } })

    const { container } = render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

    const dots = container.querySelectorAll('.rounded-full[style]')
    // Should have a dot but without animate-breathe
    const statusDot = Array.from(dots).find(d =>
      (d as HTMLElement).style.backgroundColor === 'rgb(250, 204, 21)'
    )
    expect(statusDot).toBeTruthy()
    expect(statusDot!.className).not.toContain('animate-breathe')
  })

  it('includes status in aria-label for inactive workspace', () => {
    useTabStore.setState({
      tabs: { t3: mockSessionTab('t3', 'h1', 's3') },
    })
    useAgentStore.setState({ statuses: { 'h1:s3': 'running' } })

    render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

    expect(screen.getByLabelText('Server, running')).toBeTruthy()
  })

  it('excludes status from aria-label for active workspace', () => {
    useTabStore.setState({
      tabs: { t1: mockSessionTab('t1', 'h1', 's1') },
    })
    useAgentStore.setState({ statuses: { 'h1:s1': 'running' } })

    render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

    expect(screen.getByLabelText('Project A')).toBeTruthy()
  })

  it('shows unread count in tooltip on inactive workspace', () => {
    useTabStore.setState({
      tabs: { t3: mockSessionTab('t3', 'h1', 's3') },
    })
    useAgentStore.setState({ unread: { 'h1:s3': true } })

    render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

    expect(screen.getByText('Server (1 unread)')).toBeTruthy()
  })

  it('truncates workspace badge to 99+ when unread count exceeds 99', () => {
    // Create 100 tabs in ws-2 with unique sessions, all unread
    const tabIds = Array.from({ length: 100 }, (_, i) => `t-${i}`)
    const tabs: Record<string, Tab> = {}
    const unread: Record<string, boolean> = {}
    tabIds.forEach((id, i) => {
      tabs[id] = mockSessionTab(id, 'h1', `s${i}`)
      unread[`h1:s${i}`] = true
    })
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'Active', icon: '🔧', tabs: ['t1'], activeTabId: 't1' },
      { id: 'ws-2', name: 'Big', icon: '🖥', tabs: tabIds, activeTabId: tabIds[0] },
    ]
    tabs.t1 = mockSessionTab('t1', 'h1', 'active-s')
    useTabStore.setState({ tabs })
    useAgentStore.setState({ unread })

    render(<ActivityBar {...defaultProps} workspaces={workspaces} activeWorkspaceId="ws-1" />)
    const badge = screen.getByTestId('ws-unread-badge')
    expect(badge.textContent).toBe('99+')
  })

  it('truncates Home badge to 99+ when unread count exceeds 99', () => {
    const tabIds = Array.from({ length: 100 }, (_, i) => `sh-${i}`)
    const tabs: Record<string, Tab> = {}
    const unread: Record<string, boolean> = {}
    tabIds.forEach((id, i) => {
      tabs[id] = mockSessionTab(id, 'h1', `hs${i}`)
      unread[`h1:hs${i}`] = true
    })
    useTabStore.setState({ tabs })
    useAgentStore.setState({ unread })

    render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" standaloneTabIds={tabIds} />)
    const badge = screen.getByTestId('home-unread-badge')
    expect(badge.textContent).toBe('99+')
  })

  it('tooltip shows only name when no unread and no status', () => {
    useTabStore.setState({
      tabs: { t3: mockSessionTab('t3', 'h1', 's3') },
    })
    useAgentStore.setState({ unread: {}, statuses: {} })

    render(<ActivityBar {...defaultProps} activeWorkspaceId="ws-1" />)

    const tooltips = screen.getAllByTestId('ws-tooltip')
    const serverTooltip = tooltips.find(el => el.textContent === 'Server')
    expect(serverTooltip).toBeTruthy()
  })
})

describe('ActivityBar coordinator', () => {
  beforeEach(() => {
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })

  it('renders Narrow by default', () => {
    render(
      <ActivityBar
        workspaces={[]}
        activeWorkspaceId={null}
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    // Narrow Home uses <img alt="Purdex">; Wide uses <span>Home</span>
    expect(screen.getByAltText('Purdex')).toBeInTheDocument()
    expect(screen.queryByText('Home')).not.toBeInTheDocument()
  })

  it('renders Wide when activityBarWidth=wide', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide' })
    render(
      <ActivityBar
        workspaces={[]}
        activeWorkspaceId={null}
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(screen.getByText('Home')).toBeInTheDocument()
  })
})
