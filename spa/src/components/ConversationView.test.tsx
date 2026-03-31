// spa/src/components/ConversationView.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import ConversationView from './ConversationView'
import { useStreamStore } from '../stores/useStreamStore'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'
import type { StreamMessage } from '../lib/stream-ws'

// No WS mock needed — ConversationView no longer manages WS connections

const HOST = 'test-host'
const SESSION = 'test-session'
const CK = compositeKey(HOST, SESSION)

const emptyState = {
  sessions: {},
  relayStatus: {},
  handoffProgress: {},
}

beforeEach(() => {
  cleanup()
  useStreamStore.setState(emptyState)
})

describe('ConversationView', () => {
  it('shows conversation UI when relay is connected', () => {
    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(HOST, SESSION, true)
    })
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
    expect(screen.queryByText('Handoff')).not.toBeInTheDocument()
  })

  it('shows HandoffButton when relay is not connected', () => {
    useAgentStore.setState({ statuses: { [CK]: 'idle' } })
    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    expect(screen.getByText('Handoff')).toBeInTheDocument()
  })

  it('shows progress when handoff is in progress', () => {
    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffProgress(HOST, SESSION, 'detecting')
    })
    expect(screen.getByText(/Detecting/i)).toBeInTheDocument()
  })

  it('renders messages when relay connected', () => {
    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(HOST, SESSION, true)
      useStreamStore.getState().addMessage(HOST, SESSION, {
        type: 'assistant',
        message: {
          role: 'assistant',
          content: [{ type: 'text', text: 'Hello from Claude' }],
          stop_reason: 'end_turn',
        },
      } as StreamMessage)
    })
    expect(screen.getByText('Hello from Claude')).toBeInTheDocument()
  })

  it('shows ThinkingIndicator when streaming with no assistant messages', () => {
    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(HOST, SESSION, true)
      useStreamStore.getState().setStreaming(HOST, SESSION, true)
    })
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('transitions from HandoffButton to conversation when relay connects', () => {
    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    // Initially: no relay → show Handoff
    expect(screen.getByText('Handoff')).toBeInTheDocument()
    // Relay connects → show conversation
    act(() => {
      useStreamStore.getState().setRelayStatus(HOST, SESSION, true)
    })
    expect(screen.queryByText('Handoff')).not.toBeInTheDocument()
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
  })
})

describe('ConversationView message rendering', () => {
  it('renders thinking block for assistant thinking content', () => {
    useStreamStore.setState({
      sessions: {
        [CK]: {
          messages: [{
            type: 'assistant',
            message: {
              role: 'assistant',
              content: [
                { type: 'thinking', thinking: 'Let me analyze...' },
                { type: 'text', text: 'Here is my answer.' },
              ],
              stop_reason: 'end_turn',
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      relayStatus: { [CK]: true },
    })

    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)

    expect(screen.getByTestId('thinking-header')).toBeInTheDocument()
    expect(screen.getByText('Here is my answer.')).toBeInTheDocument()
  })

  it('renders tool_result block for user tool results', () => {
    useStreamStore.setState({
      sessions: {
        [CK]: {
          messages: [{
            type: 'user',
            message: {
              role: 'user',
              content: [
                { type: 'tool_result', tool_use_id: 'toolu_01', content: 'file contents here', is_error: false },
              ],
              stop_reason: null,
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      relayStatus: { [CK]: true },
    })

    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)

    expect(screen.getByTestId('tool-result-header')).toBeInTheDocument()
  })

  it('renders interrupted message with prohibit style', () => {
    useStreamStore.setState({
      sessions: {
        [CK]: {
          messages: [{
            type: 'user',
            message: {
              role: 'user',
              content: [{ type: 'text', text: '[Request interrupted by user]' }],
              stop_reason: null,
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      relayStatus: { [CK]: true },
    })

    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)

    expect(screen.getByTestId('interrupted-msg')).toBeInTheDocument()
  })

  it('renders slash command with command bubble style', () => {
    useStreamStore.setState({
      sessions: {
        [CK]: {
          messages: [{
            type: 'user',
            message: {
              role: 'user',
              content: [{ type: 'text', text: '/exit' }],
              stop_reason: null,
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      relayStatus: { [CK]: true },
    })

    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)

    expect(screen.getByTestId('command-bubble')).toBeInTheDocument()
    expect(screen.getByTestId('command-bubble')).toHaveTextContent('/exit')
  })

  it('renders mixed assistant content blocks correctly', () => {
    useStreamStore.setState({
      sessions: {
        [CK]: {
          messages: [{
            type: 'assistant',
            message: {
              role: 'assistant',
              content: [
                { type: 'thinking', thinking: 'Deep thought' },
                { type: 'text', text: 'My response' },
                { type: 'tool_use', id: 'toolu_01', name: 'Bash', input: { command: 'ls' } },
              ],
              stop_reason: 'end_turn',
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      relayStatus: { [CK]: true },
    })

    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)

    expect(screen.getByTestId('thinking-header')).toBeInTheDocument()
    expect(screen.getByText('My response')).toBeInTheDocument()
    expect(screen.getByText('Bash')).toBeInTheDocument()
    expect(screen.getByText('ls')).toBeInTheDocument()
  })

  it('renders user tool_result with error state', () => {
    useStreamStore.setState({
      sessions: {
        [CK]: {
          messages: [{
            type: 'user',
            message: {
              role: 'user',
              content: [
                { type: 'tool_result', tool_use_id: 'toolu_02', content: 'ENOENT: no such file', is_error: true },
              ],
              stop_reason: null,
            },
          }],
          pendingControlRequests: [],
          isStreaming: false,
          conn: null,
          sessionInfo: { ccSessionId: '', model: '' },
          cost: 0,
        },
      },
      relayStatus: { [CK]: true },
    })

    render(<ConversationView hostId={HOST} sessionCode={SESSION} />)

    const block = screen.getByTestId('tool-result-block')
    expect(block.className).toContain('border-[#302a2a]')
  })
})
