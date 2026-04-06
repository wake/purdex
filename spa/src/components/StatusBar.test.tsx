import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act, waitFor } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import { createTab } from '../types/tab'
import type { Tab, PaneContent } from '../types/tab'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { compositeKey } from '../lib/composite-key'

const HOST_ID = 'test-host'

// Pre-populate stores for tests
function setupStores() {
  useSessionStore.setState({
    sessions: {
      [HOST_ID]: [
        { code: 'dev001', name: 'dev-server', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
      ],
    },
    activeHostId: HOST_ID,
    activeCode: null,
  })
  useHostStore.setState({
    hosts: {
      [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
    },
    hostOrder: [HOST_ID],
    runtime: {
      [HOST_ID]: { status: 'connected' as const },
    },
    activeHostId: HOST_ID,
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
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
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
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
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
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={onChange} />)
    fireEvent.click(screen.getByTitle('Toggle view mode'))
    // popup should show both options
    const streamOption = screen.getAllByText('stream')
    fireEvent.click(streamOption[streamOption.length - 1])
    // Should pass tabId, paneId, and mode
    expect(onChange).toHaveBeenCalledWith('t1', expect.any(String), 'stream')
  })

  it('falls back to sessionCode when session not in store', () => {
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'unknown999', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('unknown999')).toBeTruthy()
  })
})

describe('StatusBar upload progress', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
    useAgentStore.setState({ events: {}, statuses: {}, unread: {}, activeSubagents: {}, models: {} })
  })

  it('shows uploading progress', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useUploadStore.setState({
      sessions: { [ck]: { total: 5, completed: 1, failed: 0, currentFile: 'photo.png', status: 'uploading' } },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByTestId('upload-status')).toBeTruthy()
    expect(screen.getByText(/photo\.png/)).toBeTruthy()
    expect(screen.getByText(/2\/5/)).toBeTruthy()
  })

  it('shows upload done', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useUploadStore.setState({
      sessions: { [ck]: { total: 3, completed: 3, failed: 0, currentFile: '', status: 'done' } },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText(/3 files uploaded/)).toBeTruthy()
  })

  it('shows upload error', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useUploadStore.setState({
      sessions: { [ck]: { total: 1, completed: 0, failed: 1, currentFile: '', error: 'bad.mp4', status: 'error' } },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
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
    const ck = compositeKey(HOST_ID, 'dev001')
    useAgentStore.setState({
      events: { [ck]: { tmux_session: 'dev', event_name: 'SessionStart', raw_event: { modelName: 'Claude Opus 4' }, agent_type: 'cc', broadcast_ts: Date.now() } },
      statuses: { [ck]: 'idle' },
      unread: {},
      activeSubagents: {},
      models: { [ck]: 'Claude Opus 4' },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    const badge = screen.getByTestId('agent-label')
    expect(badge.textContent).toBe('Claude Opus 4')
    expect(badge.className).toContain('border')
  })

  it('reactively shows badge when models updates after mount', async () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useAgentStore.setState({ events: {}, statuses: {}, unread: {}, activeSubagents: {}, models: {} })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.queryByTestId('agent-label')).toBeNull()
    act(() => {
      useAgentStore.setState({ models: { [ck]: 'Claude Sonnet 4' } })
    })
    await waitFor(() => {
      const badge = screen.getByTestId('agent-label')
      expect(badge.textContent).toBe('Claude Sonnet 4')
    })
  })

  it('does not render badge when no model in models map', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useAgentStore.setState({
      events: { [ck]: { tmux_session: 'dev', event_name: 'UserPromptSubmit', raw_event: {}, agent_type: 'cc', broadcast_ts: Date.now() } },
      statuses: { [ck]: 'running' },
      unread: {},
      activeSubagents: {},
      models: {},
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.queryByTestId('agent-label')).toBeNull()
  })
})
