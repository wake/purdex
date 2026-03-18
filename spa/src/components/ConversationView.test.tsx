// spa/src/components/ConversationView.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import ConversationView from './ConversationView'
import { useStreamStore } from '../stores/useStreamStore'

// No WS mock needed — ConversationView no longer manages WS connections

const SESSION = 'test-session'

const emptyState = {
  sessions: {},
  sessionStatus: {},
  relayStatus: {},
  handoffState: {},
  handoffProgress: {},
}

beforeEach(() => {
  cleanup()
  useStreamStore.setState(emptyState)
})

describe('ConversationView', () => {
  it('renders empty state when connected', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'connected')
    })
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
  })

  it('renders messages from per-session store', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'connected')
      useStreamStore.getState().addMessage(SESSION, {
        type: 'assistant',
        message: {
          role: 'assistant',
          content: [{ type: 'text', text: 'Hello from Claude' }],
          stop_reason: 'end_turn',
        },
      } as any)
    })
    expect(screen.getByText('Hello from Claude')).toBeInTheDocument()
  })

  it('shows ThinkingIndicator when streaming with no assistant messages', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'connected')
      useStreamStore.getState().setStreaming(SESSION, true)
    })
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('hides ThinkingIndicator when assistant message arrives', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'connected')
      useStreamStore.getState().setStreaming(SESSION, true)
      useStreamStore.getState().addMessage(SESSION, {
        type: 'assistant',
        message: {
          role: 'assistant',
          content: [{ type: 'text', text: 'Reply' }],
          stop_reason: null,
        },
      } as any)
    })
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })

  it('shows HandoffButton when handoffState is idle', () => {
    useStreamStore.getState().setSessionStatus(SESSION, 'cc-idle')
    render(<ConversationView sessionName={SESSION} />)
    expect(screen.getByText('Handoff')).toBeInTheDocument()
  })

  it('shows HandoffButton when handoffState is disconnected', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'disconnected')
    })
    expect(screen.getByText(/disconnected/i)).toBeInTheDocument()
  })

  it('hides HandoffButton when handoffState is connected', () => {
    render(<ConversationView sessionName={SESSION} />)
    act(() => {
      useStreamStore.getState().setHandoffState(SESSION, 'connected')
    })
    expect(screen.queryByText('Handoff')).not.toBeInTheDocument()
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
  })
})
