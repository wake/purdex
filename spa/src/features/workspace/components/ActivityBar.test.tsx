import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

// Mock icon-loader to avoid CSR deep-import resolution failures in test env
vi.mock('../generated/icon-loader', () => ({
  iconLoaders: {},
}))

import { ActivityBar } from './ActivityBar'
import type { Workspace } from '../../../types/tab'

const mockWorkspaces: Workspace[] = [
  { id: 'ws-1', name: 'Project A', color: '#7a6aaa', icon: '🔧', tabs: ['t1', 't2'], activeTabId: 't1' },
  { id: 'ws-2', name: 'Server', color: '#6aaa7a', icon: '🖥', tabs: ['t3'], activeTabId: 't3' },
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
  })

  it('renders workspace icons', () => {
    render(<ActivityBar {...defaultProps} />)
    expect(screen.getByTitle('Project A')).toBeTruthy()
    expect(screen.getByTitle('Server')).toBeTruthy()
  })

  it('highlights active workspace', () => {
    render(<ActivityBar {...defaultProps} />)
    const activeBtn = screen.getByTitle('Project A')
    expect(activeBtn.className).toContain('ring')
  })

  it('calls onSelectWorkspace on click', () => {
    const onSelect = vi.fn()
    render(<ActivityBar {...defaultProps} onSelectWorkspace={onSelect} />)
    fireEvent.click(screen.getByTitle('Server'))
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
})
