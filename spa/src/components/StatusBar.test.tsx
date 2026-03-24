import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import { createTab } from '../types/tab'
import type { Tab, PaneContent } from '../types/tab'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'

// Pre-populate stores for tests
function setupStores() {
  useSessionStore.setState({
    sessions: [
      { code: 'dev001', name: 'dev-server', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
    ],
    activeId: null,
  })
  useHostStore.setState({
    hosts: {
      local: { id: 'local', name: 'mlab', address: '100.64.0.2', port: 7860, status: 'connected' as const },
    },
    defaultHost: { id: 'local', name: 'mlab', address: '100.64.0.2', port: 7860, status: 'connected' as const },
  })
}

function makeTab(id: string, content: PaneContent): Tab {
  const tab = createTab(content)
  return { ...tab, id }
}

beforeEach(() => {
  cleanup()
  setupStores()
})

describe('StatusBar', () => {
  it('renders host and session info', () => {
    const tab = makeTab('t1', { kind: 'session', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('mlab')).toBeTruthy()
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('connected')).toBeTruthy()
  })

  it('renders empty state when no active tab', () => {
    render(<StatusBar activeTab={null} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('No active session')).toBeTruthy()
  })

  it('shows viewMode badge for session tabs', () => {
    const tab = makeTab('t1', { kind: 'session', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('terminal')).toBeTruthy()
  })

  it('shows simplified status for non-session tabs', () => {
    const tab = makeTab('t1', { kind: 'dashboard' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('dashboard')).toBeTruthy()
    expect(screen.queryByTitle('切換檢視模式')).toBeNull()
  })

  it('opens popup on badge click and calls onViewModeChange', () => {
    const onChange = vi.fn()
    const tab = makeTab('t1', { kind: 'session', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={onChange} />)
    fireEvent.click(screen.getByTitle('切換檢視模式'))
    // popup should show both options
    const streamOption = screen.getAllByText('stream')
    fireEvent.click(streamOption[streamOption.length - 1])
    // Should pass tabId, paneId, and mode
    expect(onChange).toHaveBeenCalledWith('t1', expect.any(String), 'stream')
  })

  it('falls back to sessionCode when session not in store', () => {
    useSessionStore.setState({ sessions: [], activeId: null })
    const tab = makeTab('t1', { kind: 'session', sessionCode: 'unknown999', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('unknown999')).toBeTruthy()
  })
})
