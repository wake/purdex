// spa/src/components/chat/ChatTranscript.test.tsx — the chat transcript
// (spec §5, R2 plan T1.3): the agent's bubbles on the left, yours on the
// right, no thinking, no ceremony. Tool operations are absent until R2-B.
import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import ChatTranscript, { type ChatTranscriptProps } from './ChatTranscript'
import type { ContentBlock, StreamMessage } from '../../lib/nex/message-types'
import type { PartialAssembly } from '../../lib/nex/partial'

const asst = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'assistant', message: { id: 'm', role: 'assistant', content: blocks, stop_reason: null } } as StreamMessage)
const usr = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'user', message: { role: 'user', content: blocks, stop_reason: null } } as StreamMessage)
const said = (text: string): StreamMessage => usr({ type: 'text', text })
const reply = (text: string): StreamMessage => asst({ type: 'text', text })
const use = (id: string, name: string, input: Record<string, unknown> = {}): ContentBlock =>
  ({ type: 'tool_use', id, name, input })
const res = (id: string, content: string): ContentBlock =>
  ({ type: 'tool_result', tool_use_id: id, content, is_error: false })
const child = (m: StreamMessage, parent: string): StreamMessage =>
  ({ ...m, parent_tool_use_id: parent }) as StreamMessage
const follows = (a: Element, b: Element) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)

const T = (props: Partial<ChatTranscriptProps> & Pick<ChatTranscriptProps, 'messages'>) => (
  <ChatTranscript keyPrefix="k" showThinking={false} showEmptyHint={false} {...props} />
)

describe('ChatTranscript', () => {
  it('puts the agent on the left and you on the right', () => {
    render(T({ messages: [said('hello'), reply('Hi there')] }))
    const mine = screen.getByTestId('chat-bubble-user')
    const agents = screen.getByTestId('chat-bubble-agent')
    expect(mine).toHaveTextContent('hello')
    expect(agents).toHaveTextContent('Hi there')
    expect(mine.parentElement!.className).toContain('justify-end')
    expect(agents.parentElement!.className).toContain('justify-start')
    // The agent speaks markdown through the room's prose renderer.
    expect(within(agents).getByTestId('room-prose')).toBeInTheDocument()
  })

  it('renders your line as text, not markdown', () => {
    render(T({ messages: [said('**bold** and `code`')] }))
    const mine = screen.getByTestId('chat-bubble-user')
    expect(mine).toHaveTextContent('**bold** and `code`')
    expect(mine.querySelector('strong')).toBeNull()
    expect(mine.querySelector('code')).toBeNull()
    expect(within(mine).queryByTestId('room-prose')).toBeNull()
  })

  it('hides thinking entirely', () => {
    render(T({ messages: [said('q'), asst({ type: 'thinking', thinking: 'deep thought' }, { type: 'text', text: 'Answer' })] }))
    expect(screen.queryByText(/deep thought/)).toBeNull()
    expect(screen.queryByTestId('room-thinking')).toBeNull()
    expect(screen.getAllByTestId('chat-bubble-agent')).toHaveLength(1)
    expect(screen.getByTestId('chat-bubble-agent')).toHaveTextContent('Answer')
  })

  it('shows the typewriter while a thought streams, then nothing', () => {
    const streaming: PartialAssembly = {
      messageId: 'm1', finalized: 0,
      blocks: { 0: { index: 0, type: 'thinking', text: '', thinking: 'musing out loud', partialJson: '' } },
    }
    // ExecutionView's chat rule keeps the dots on while only a thought streams.
    const { rerender } = render(T({ messages: [said('q')], partial: streaming, showThinking: true }))
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
    expect(screen.queryByText(/musing/)).toBeNull()
    expect(screen.queryByTestId('chat-bubble-agent')).toBeNull()

    // Finalised as a thought-only message: nothing at all is left of it.
    rerender(T({ messages: [said('q'), asst({ type: 'thinking', thinking: 'musing out loud' })], partial: null }))
    expect(screen.queryByTestId('thinking-indicator')).toBeNull()
    expect(screen.queryByText(/musing/)).toBeNull()
    expect(screen.queryByTestId('chat-bubble-agent')).toBeNull()
  })

  it("keeps a subagent's frames out of the top level", () => {
    render(T({
      messages: [
        said('go'),
        asst(use('T', 'Task', { subagent_type: 'general-purpose', prompt: 'read it' })),
        child(said('SUBAGENT PROMPT'), 'T'),
        child(reply('SUBAGENT REPLY'), 'T'),
        reply('done'),
      ],
    }))
    expect(screen.queryByText('SUBAGENT PROMPT')).toBeNull()
    expect(screen.queryByText('SUBAGENT REPLY')).toBeNull()
    expect(screen.getAllByTestId('chat-bubble-user')).toHaveLength(1)
    expect(screen.getAllByTestId('chat-bubble-agent')).toHaveLength(1)
    expect(screen.getByTestId('chat-bubble-agent')).toHaveTextContent('done')
  })

  it('renders the interrupt sentinel as a system line', () => {
    render(T({ messages: [said('q'), said('[Request interrupted by user]')] }))
    const line = screen.getByTestId('chat-interrupted')
    expect(line.className).toContain('justify-center')
    expect(line.className).toContain('text-status-error')
    expect(line.className).toContain('italic')
    expect(line.className).toContain('text-xs')
    expect(line.querySelector('svg')).not.toBeNull()
    expect(line.closest('[data-testid^="chat-bubble"]')).toBeNull()
    // Only the real line is a bubble.
    expect(screen.getAllByTestId('chat-bubble-user')).toHaveLength(1)
  })

  it('renders a slash command in a user bubble', () => {
    render(T({ messages: [said('/compact')] }))
    const bubble = screen.getByTestId('chat-bubble-user')
    expect(bubble).toHaveTextContent('/compact')
    expect(bubble.className).toContain('font-mono')
  })

  it('draws the pending line as a dimmed user bubble', () => {
    render(
      <ChatTranscript messages={[said('first'), reply('ok')]} turnStarts={[0]} keyPrefix="k" showThinking showEmptyHint={false}>
        <span data-testid="pending">second</span>
      </ChatTranscript>,
    )
    const pending = screen.getByTestId('pending')
    const bubble = pending.closest('[data-testid="chat-bubble-user"]') as HTMLElement
    expect(bubble).not.toBeNull()
    expect(bubble.className).toContain('opacity-60')
    // The provisional turn one past the last real one, as in the room.
    const turns = screen.getAllByTestId('room-turn')
    expect(turns).toHaveLength(2)
    expect(turns[1]).toHaveAttribute('data-turn-index', '1')
    expect(turns[1]).toContainElement(pending)
    expect(follows(pending, screen.getByTestId('thinking-indicator'))).toBe(true)
  })

  it('caps bubble width', () => {
    const { container } = render(T({ messages: [said('hello'), reply('Hi')] }))
    for (const bubble of [screen.getByTestId('chat-bubble-user'), screen.getByTestId('chat-bubble-agent')]) {
      expect(bubble.className).toContain('w-fit')
      expect(bubble.className).toContain('max-w-[85%]')
      expect(bubble.className).toContain('@md:max-w-[70ch]')
      expect(bubble.className).toContain('rounded-lg')
    }
    expect(screen.getByTestId('chat-bubble-agent').className).toContain('bg-surface-secondary')
    expect(screen.getByTestId('chat-bubble-agent').className).toContain('border-border-subtle')
    expect(screen.getByTestId('chat-bubble-user').className).toContain('bg-accent-muted')
    // The transcript root is the container the @md cap reads.
    expect((container.firstElementChild as HTMLElement).className).toContain('@container')
  })

  it('wraps each turn in a turn group', () => {
    render(T({ messages: [said('one'), reply('a'), said('two'), reply('b')], turnStarts: [0, 2] }))
    const turns = screen.getAllByTestId('room-turn')
    expect(turns).toHaveLength(2)
    expect(within(turns[0]).getByText('one')).toBeInTheDocument()
    expect(within(turns[1]).getByText('two')).toBeInTheDocument()
    expect(screen.queryByTestId('turn-fold-strip')).toBeNull()
  })

  it('draws no empty bubble for a message that only carries a tool result', () => {
    render(T({ messages: [said('q'), asst(use('t1', 'Bash', { command: 'ls' })), usr(res('t1', 'OUTPUT')), reply('done')] }))
    expect(screen.getAllByTestId('chat-bubble-user')).toHaveLength(1)
    expect(screen.getByTestId('chat-bubble-user')).toHaveTextContent('q')
    expect(screen.getAllByTestId('chat-bubble-agent')).toHaveLength(1)
    expect(screen.queryByText('OUTPUT')).toBeNull()
    for (const b of screen.queryAllByTestId(/^chat-bubble/)) expect(b.textContent?.trim()).not.toBe('')
  })

  it('streams the partial text into an agent bubble inside the last turn', () => {
    const partial: PartialAssembly = {
      messageId: 'm2', finalized: 0,
      blocks: { 0: { index: 0, type: 'text', text: 'typing', thinking: '', partialJson: '' } },
    }
    render(T({ messages: [said('q')], turnStarts: [0], partial }))
    const turn = screen.getByTestId('room-turn')
    expect(within(turn).getByTestId('chat-partial-group')).toHaveTextContent('typing')
  })
})
