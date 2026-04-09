import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

// Mock icon-loader to avoid CSR deep-import resolution failures in test env
vi.mock('../generated/icon-loader', () => ({
  iconLoaders: {},
}))

import { ActivityBar } from './ActivityBar'
import type { Workspace } from '../../../types/tab'
import { useTabStore } from '../../../stores/useTabStore'
import { useAgentStore } from '../../../stores/useAgentStore'
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
  standaloneTabCount: 0,
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
    expect(homeBtn.className).toContain('bg-accent')
  })

  it('calls onSelectHome on Home click', () => {
    const onSelectHome = vi.fn()
    render(<ActivityBar {...defaultProps} onSelectHome={onSelectHome} />)
    fireEvent.click(screen.getByTitle('Home'))
    expect(onSelectHome).toHaveBeenCalled()
  })

  it('shows badge on Home when standalone tabs exist and workspace is active', () => {
    const { container } = render(<ActivityBar {...defaultProps} standaloneTabCount={3} />)
    const badge = container.querySelector('.bg-red-500')
    expect(badge).toBeTruthy()
    expect(badge!.textContent).toBe('3')
  })

  it('hides badge on Home when in Home mode', () => {
    const { container } = render(<ActivityBar {...defaultProps} activeWorkspaceId={null} standaloneTabCount={3} />)
    const badge = container.querySelector('.bg-red-500')
    expect(badge).toBeNull()
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
})
