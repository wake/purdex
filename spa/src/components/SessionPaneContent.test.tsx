import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { SessionPaneContent } from './SessionPaneContent'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useConfigStore } from '../stores/useConfigStore'
import type { Pane } from '../types/tab'
import type { ConfigData } from '../lib/api'

vi.mock('./TerminalView', () => ({
  default: () => <div data-testid="terminal-view" />,
}))

vi.mock('./ConversationView', () => ({
  default: () => <div data-testid="conversation-view" />,
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
})

describe('SessionPaneContent', () => {
  it('returns null for non-session pane content', () => {
    const pane: Pane = { id: 'pane-1', content: { kind: 'dashboard' } }
    const { container } = render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(container.innerHTML).toBe('')
  })

  it('renders TerminalView for terminal mode', () => {
    const pane = makePane()
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('terminal-view')).toBeInTheDocument()
  })

  it('renders ConversationView for stream mode', () => {
    const pane = makePane({
      content: { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'stream', cachedName: '', tmuxInstance: '' },
    })
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('conversation-view')).toBeInTheDocument()
  })
})
