import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act, waitFor } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import { createTab } from '../types/tab'
import type { Tab, PaneContent } from '../types/tab'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useTabStore } from '../stores/useTabStore'
import { compositeKey } from '../lib/composite-key'
import type { PaneLayout } from '../types/tab'

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
  useAgentStore.setState({ agentTypes: {}, oscTitles: {}, models: {}, lastEvents: {}, statuses: {}, unread: {}, subagents: {} })
  useUISettingsStore.setState({ showAgentTitleInStatusBar: false })
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

  it('does not render the global fallback bar for editor tabs', () => {
    const tab = makeTab('t1', { kind: 'editor', source: { type: 'inapp' }, filePath: '/notes/a.md' })
    const { container } = render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(container).toBeEmptyDOMElement()
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

  it('shows split-H / split-V buttons for a tmux-session tab', () => {
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByTitle('Split Horizontal')).toBeInTheDocument()
    expect(screen.getByTitle('Split Vertical')).toBeInTheDocument()
  })

  it('clicking split-H calls splitPaneBlank with the primary pane id', () => {
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    const paneId = (tab.layout as { pane: { id: string } }).pane.id
    const spy = vi.spyOn(useTabStore.getState(), 'splitPaneBlank').mockImplementation(() => {})
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    fireEvent.click(screen.getByTitle('Split Horizontal'))
    expect(spy).toHaveBeenCalledWith('t1', paneId, 'h')
    fireEvent.click(screen.getByTitle('Split Vertical'))
    expect(spy).toHaveBeenCalledWith('t1', paneId, 'v')
  })

  it('shows split buttons for a split-layout tab whose primary pane is a tmux-session, using the primary pane id', () => {
    const primaryPane = { id: 'primary-pane', content: { kind: 'tmux-session' as const, hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal' as const, cachedName: '', tmuxInstance: '' } }
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'h',
      children: [
        { type: 'leaf', pane: primaryPane },
        { type: 'leaf', pane: { id: 'other', content: { kind: 'new-tab' } } },
      ],
      sizes: [50, 50],
    }
    const tab = { ...makeTab('t1', { kind: 'new-tab' }), layout }
    const spy = vi.spyOn(useTabStore.getState(), 'splitPaneBlank').mockImplementation(() => {})
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByTitle('Split Horizontal')).toBeInTheDocument()
    fireEvent.click(screen.getByTitle('Split Horizontal'))
    expect(spy).toHaveBeenCalledWith('t1', 'primary-pane', 'h')
  })

  it('does not show split buttons for an editor tab (bar is null)', () => {
    const tab = makeTab('t1', { kind: 'editor', source: { type: 'inapp' }, filePath: '/notes/a.md' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.queryByTitle('Split Horizontal')).toBeNull()
  })

  it('does not show split buttons when there is no active tab', () => {
    render(<StatusBar activeTab={null} onViewModeChange={vi.fn()} />)
    expect(screen.queryByTitle('Split Horizontal')).toBeNull()
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
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {} })
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

  it('shows typing status after upload completes', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useUploadStore.setState({
      sessions: { [ck]: { total: 2, completed: 2, failed: 0, currentFile: '', status: 'typing' } },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByTestId('upload-status')).toBeTruthy()
    expect(screen.getByText('Typing into session...')).toBeTruthy()
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
      lastEvents: { [ck]: { raw_event_name: 'PdxSessionStart', status: 'idle', agent_type: 'cc', broadcast_ts: Date.now(), model: 'Claude Opus 4' } },
      statuses: { [ck]: 'idle' },
      unread: {},
      subagents: {},
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
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {} })
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
      lastEvents: { [ck]: { raw_event_name: 'PdxUserPromptSubmit', status: 'running', agent_type: 'cc', broadcast_ts: Date.now() } },
      statuses: { [ck]: 'running' },
      unread: {},
      subagents: {},
      models: {},
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.queryByTestId('agent-label')).toBeNull()
  })
})

describe('StatusBar agent pane title', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
  })

  it('shows pane_title left of the terminal/stream switch when showAgentTitleInStatusBar=true', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'dev001', name: 'dev-server', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false, pane_title: 'plan review' },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    useAgentStore.setState({ agentTypes: { [ck]: 'cc' }, oscTitles: { [ck]: 'osc fallback' } })
    useUISettingsStore.setState({ showAgentTitleInStatusBar: true })

    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)

    const title = screen.getByTestId('agent-pane-title')
    expect(title.textContent).toBe('plan review')
    expect(title.compareDocumentPosition(screen.getByTitle('Toggle view mode')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('hides title when showAgentTitleInStatusBar=false', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'dev001', name: 'dev-server', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false, pane_title: 'plan review' },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    useAgentStore.setState({ agentTypes: { [ck]: 'cc' } })
    useUISettingsStore.setState({ showAgentTitleInStatusBar: false })

    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)

    expect(screen.queryByTestId('agent-pane-title')).toBeNull()
  })
})
