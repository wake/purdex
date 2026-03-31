import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import { createTab } from '../types/tab'
import type { Tab, PaneContent } from '../types/tab'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'

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
    const tab = makeTab('t1', { kind: 'session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal' })
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
    const tab = makeTab('t1', { kind: 'session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('terminal')).toBeTruthy()
  })

  it('shows simplified status for non-session tabs', () => {
    const tab = makeTab('t1', { kind: 'dashboard' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('dashboard')).toBeTruthy()
    expect(screen.queryByTitle('Toggle view mode')).toBeNull()
  })

  it('opens popup on badge click and calls onViewModeChange', () => {
    const onChange = vi.fn()
    const tab = makeTab('t1', { kind: 'session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={onChange} />)
    fireEvent.click(screen.getByTitle('Toggle view mode'))
    // popup should show both options
    const streamOption = screen.getAllByText('stream')
    fireEvent.click(streamOption[streamOption.length - 1])
    // Should pass tabId, paneId, and mode
    expect(onChange).toHaveBeenCalledWith('t1', expect.any(String), 'stream')
  })

  it('falls back to sessionCode when session not in store', () => {
    useSessionStore.setState({ sessions: [], activeId: null })
    const tab = makeTab('t1', { kind: 'session', hostId: 'test-host', sessionCode: 'unknown999', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('unknown999')).toBeTruthy()
  })
})

describe('StatusBar upload progress', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
    useAgentStore.setState({ events: {}, statuses: {}, unread: {}, activeSubagents: {}, hooksInstalled: false })
  })

  it('shows uploading progress', () => {
    useUploadStore.setState({
      sessions: { dev001: { total: 5, completed: 1, failed: 0, currentFile: 'photo.png', status: 'uploading' } },
    })
    const tab = makeTab('t1', { kind: 'session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByTestId('upload-status')).toBeTruthy()
    expect(screen.getByText(/photo\.png/)).toBeTruthy()
    expect(screen.getByText(/2\/5/)).toBeTruthy()
  })

  it('shows upload done', () => {
    useUploadStore.setState({
      sessions: { dev001: { total: 3, completed: 3, failed: 0, currentFile: '', status: 'done' } },
    })
    const tab = makeTab('t1', { kind: 'session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText(/3 files uploaded/)).toBeTruthy()
  })

  it('shows upload error', () => {
    useUploadStore.setState({
      sessions: { dev001: { total: 1, completed: 0, failed: 1, currentFile: '', error: 'bad.mp4', status: 'error' } },
    })
    const tab = makeTab('t1', { kind: 'session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText(/bad\.mp4/)).toBeTruthy()
  })
})

describe('StatusBar agent label badge', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
  })

  it('renders agent label as badge with model name', () => {
    useAgentStore.setState({
      events: { dev001: { tmux_session: 'dev', event_name: 'SessionStart', raw_event: { modelName: 'Claude Opus 4' }, agent_type: 'cc', broadcast_ts: Date.now() } },
      statuses: { dev001: 'idle' },
      unread: {},
      activeSubagents: {},
      hooksInstalled: true,
    })
    const tab = makeTab('t1', { kind: 'session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    const badge = screen.getByTestId('agent-label')
    expect(badge.textContent).toBe('Claude Opus 4')
    expect(badge.className).toContain('border')
  })

  it('renders fallback Agent badge with white styling', () => {
    useAgentStore.setState({
      events: { dev001: { tmux_session: 'dev', event_name: 'UserPromptSubmit', raw_event: {}, agent_type: 'cc', broadcast_ts: Date.now() } },
      statuses: { dev001: 'running' },
      unread: {},
      activeSubagents: {},
      hooksInstalled: true,
    })
    const tab = makeTab('t1', { kind: 'session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    const badge = screen.getByTestId('agent-label')
    expect(badge.textContent).toBe('Agent')
    expect(badge.className).toContain('border')
  })
})
