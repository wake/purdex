import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { SessionPaneContent } from './SessionPaneContent'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTabStore } from '../stores/useTabStore'
import { useConfigStore } from '../stores/useConfigStore'
import type { Pane, Tab } from '../types/tab'
import type { ConfigData } from '../lib/host-api'

vi.mock('./TerminalView', () => ({
  default: () => <div data-testid="terminal-view" />,
}))

vi.mock('./ConversationView', () => ({
  default: () => <div data-testid="conversation-view" />,
}))

vi.mock('./TerminatedPane', () => ({
  TerminatedPane: ({ content }: { content: { terminated: string } }) => (
    <div data-testid="terminated-pane">Terminated: {content.terminated}</div>
  ),
}))

const HOST_ID = 'test-host'

const makePane = (overrides?: Partial<Pane>): Pane => ({
  id: 'pane-1',
  content: { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' },
  ...overrides,
})

const defaultConfig: ConfigData = {
  bind: '0.0.0.0',
  port: 7860,
  stream: { presets: [{ name: 'cc', command: 'claude -p' }] },
  detect: { cc_commands: [], poll_interval: 5 },
}

function setupTabStore(pane: Pane) {
  const tab: Tab = {
    id: 'tab-1',
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane },
  }
  useTabStore.setState({
    tabs: { 'tab-1': tab },
    tabOrder: ['tab-1'],
    activeTabId: 'tab-1',
  })
}

beforeEach(() => {
  cleanup()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    activeHostId: HOST_ID,
  })
  useSessionStore.setState({
    sessions: {
      [HOST_ID]: [{
        code: 'dev001', name: 'dev001', cwd: '/tmp', mode: 'terminal',
        cc_session_id: '', cc_model: '', has_relay: false,
      }],
    },
    activeHostId: HOST_ID,
    activeCode: null,
  })
  useConfigStore.setState({ config: defaultConfig })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

describe('SessionPaneContent', () => {
  it('returns null for non-session pane content', () => {
    const pane: Pane = { id: 'pane-1', content: { kind: 'dashboard' } }
    setupTabStore(pane)
    const { container } = render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(container.innerHTML).toBe('')
  })

  it('renders TerminalView for terminal mode', () => {
    const pane = makePane()
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('terminal-view')).toBeInTheDocument()
  })

  it('renders ConversationView for stream mode', () => {
    const pane = makePane({
      content: { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'stream', cachedName: '', tmuxInstance: '' },
    })
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('conversation-view')).toBeInTheDocument()
  })

  it('renders TerminatedPane when content.terminated is set', () => {
    const pane = makePane({
      content: {
        kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
        mode: 'terminal', cachedName: 'my-session', tmuxInstance: '',
        terminated: 'session-closed',
      },
    })
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('terminated-pane')).toBeInTheDocument()
    expect(screen.getByText('Terminated: session-closed')).toBeInTheDocument()
  })

  it('does not render TerminalView when terminated', () => {
    const pane = makePane({
      content: {
        kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
        mode: 'terminal', cachedName: '', tmuxInstance: '',
        terminated: 'tmux-restarted',
      },
    })
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.queryByTestId('terminal-view')).not.toBeInTheDocument()
    expect(screen.getByTestId('terminated-pane')).toBeInTheDocument()
  })
})
