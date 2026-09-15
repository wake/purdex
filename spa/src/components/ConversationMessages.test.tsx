// spa/src/components/ConversationMessages.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import ConversationMessages from './ConversationMessages'
import type { StreamMessage } from '../lib/stream-ws'

const assistantText: StreamMessage = { type: 'assistant', message: { role: 'assistant', content: [{ type: 'text', text: 'Hi there' }], stop_reason: null } } as StreamMessage

describe('ConversationMessages', () => {
  it('renders assistant text and shows nothing else when empty hint is off', () => {
    render(<ConversationMessages messages={[assistantText]} keyPrefix="k" showThinking={false} showEmptyHint={false} />)
    expect(screen.getByText('Hi there')).toBeInTheDocument()
    expect(screen.queryByText(/waiting/i)).not.toBeInTheDocument()
  })

  it('shows the default waiting hint, or the override, when asked', () => {
    const { rerender } = render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint />)
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
    rerender(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint emptyText="No history yet" />)
    expect(screen.getByText('No history yet')).toBeInTheDocument()
  })

  it('renders children before the thinking indicator and afterThinking after it', () => {
    render(
      <ConversationMessages messages={[assistantText]} keyPrefix="k" showThinking showEmptyHint={false}
        afterThinking={<div data-testid="after">after</div>}>
        <div data-testid="child">child</div>
      </ConversationMessages>,
    )
    const child = screen.getByTestId('child')
    const indicator = screen.getByTestId('thinking-indicator')
    const after = screen.getByTestId('after')
    // DOM order: list → children → ThinkingIndicator → afterThinking (Stream's prompts)
    expect(child.compareDocumentPosition(indicator) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(indicator.compareDocumentPosition(after) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('renders the four user block styles', () => {
    const msgs: StreamMessage[] = [
      { type: 'user', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 't', content: 'out', is_error: false }], stop_reason: null } },
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: '[Request interrupted by user]' }], stop_reason: null } },
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: '/compact' }], stop_reason: null } },
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'plain' }], stop_reason: null } },
    ] as StreamMessage[]
    render(<ConversationMessages messages={msgs} keyPrefix="k" showThinking={false} showEmptyHint={false} />)
    expect(screen.getByTestId('interrupted-msg')).toBeInTheDocument()
    expect(screen.getByTestId('command-bubble')).toHaveTextContent('/compact')
    expect(screen.getByText('plain')).toBeInTheDocument()
    expect(screen.getByText('out')).toBeInTheDocument()
  })
})
