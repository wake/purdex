// spa/src/components/ConversationMessages.test.tsx
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import ConversationMessages from './ConversationMessages'
import type { StreamMessage } from '../lib/nex/message-types'
import type { PartialAssembly, PartialBlock } from '../lib/nex/partial'
import type { ToolActivity } from '../lib/nex/tool-activity'

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

  // ---- P-B2.2 spec §4.4 R1 / R2 / R4 ------------------------------------

  const pb = (index: number, over: Partial<PartialBlock> & { type: PartialBlock['type'] }): PartialBlock =>
    ({ index, text: '', thinking: '', partialJson: '', ...over })
  const assembly = (...blocks: PartialBlock[]): PartialAssembly =>
    ({ messageId: 'm', finalized: 0, blocks: Object.fromEntries(blocks.map((b) => [b.index, b])) })
  const durableTool: StreamMessage = { type: 'assistant', message: { id: 'm0', role: 'assistant', content: [{ type: 'tool_use', id: 'tu1', name: 'Bash', input: { command: 'ls' } }], stop_reason: null } } as StreamMessage
  const running: ToolActivity = { name: 'Bash', startedAt: 1_000, endedAt: null, status: 'running' }
  const follows = (a: Element, b: Element) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)

  describe('partial group (R1)', () => {
    it('R1: renders the partial group after the durable messages and before children', () => {
      render(
        <ConversationMessages messages={[assistantText]} keyPrefix="k" showThinking={false} showEmptyHint={false}
          partial={assembly(pb(0, { type: 'text', text: 'streaming…' }))}>
          <div data-testid="child">child</div>
        </ConversationMessages>,
      )
      const durable = screen.getByText('Hi there')
      const group = screen.getByTestId('partial-group')
      const child = screen.getByTestId('child')
      expect(within(group).getByText('streaming…')).toBeInTheDocument()
      expect(follows(durable, group)).toBe(true)
      expect(follows(group, child)).toBe(true)
    })

    it('R1: blocks inserted as 2,0,1 render in ascending index order 0,1,2', () => {
      const partial: PartialAssembly = { messageId: 'm', finalized: 0, blocks: {} }
      partial.blocks[2] = pb(2, { type: 'text', text: 'two' })
      partial.blocks[0] = pb(0, { type: 'text', text: 'zero' })
      partial.blocks[1] = pb(1, { type: 'text', text: 'one' })
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false} partial={partial} />)
      const group = screen.getByTestId('partial-group')
      const [zero, one, two] = ['zero', 'one', 'two'].map((x) => within(group).getByText(x))
      expect(follows(zero, one)).toBe(true)
      expect(follows(one, two)).toBe(true)
    })

    it('R1: a text block renders a MessageBubble with the stream cursor', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'text', text: 'hello' }))} />)
      const group = screen.getByTestId('partial-group')
      expect(within(group).getByText('hello')).toBeInTheDocument()
      expect(within(group).getByTestId('stream-cursor')).toBeInTheDocument()
      expect(within(group).getByTestId('assistant-text')).toBeInTheDocument()
      expect(within(group).queryByTestId('thinking-header')).not.toBeInTheDocument()
    })

    it('R1: a thinking block renders a ThinkingBlock with the stream cursor', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'thinking', thinking: 'pondering' }))} />)
      const group = screen.getByTestId('partial-group')
      expect(within(group).getByTestId('thinking-header')).toBeInTheDocument()
      expect(within(group).queryByTestId('assistant-text')).not.toBeInTheDocument()
      expect(within(group).getByTestId('stream-cursor')).toBeInTheDocument()
    })

    it('R1: an empty text block renders nothing', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'text', text: '' }))} />)
      expect(screen.getByTestId('partial-group')).toBeEmptyDOMElement()
      expect(screen.queryByTestId('stream-cursor')).not.toBeInTheDocument()
    })

    it('R1: whitespace-only text / thinking blocks render nothing (same predicate as partialHasVisibleContent)', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'text', text: ' \n\t ' }), pb(1, { type: 'thinking', thinking: '  ' }))} />)
      expect(screen.getByTestId('partial-group')).toBeEmptyDOMElement()
      expect(screen.queryByTestId('assistant-text')).not.toBeInTheDocument()
      expect(screen.queryByTestId('stream-cursor')).not.toBeInTheDocument()
    })

    it('R1: a started tool_use with empty partialJson renders the spinner row with only the tool name', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'tool_use', toolName: 'Bash' }))} />)
      const header = within(screen.getByTestId('partial-group')).getByTestId('tool-header')
      expect(within(header).getByTestId('tool-icon-spinner')).toBeInTheDocument()
      expect(header).toHaveTextContent(/^Bash$/)
    })

    it('R1: an unknown block renders nothing', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'unknown', text: 'x', thinking: 'y', partialJson: 'z' }))} />)
      expect(screen.getByTestId('partial-group')).toBeEmptyDOMElement()
    })

    it('R1: a snapshot-seeded tool_use block without toolName shows the execution.tool.unknown placeholder and the raw prefix', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'tool_use', partialJson: '{"command":"ls' }))} />)
      const header = within(screen.getByTestId('partial-group')).getByTestId('tool-header')
      expect(header).toHaveTextContent('tool')
      expect(header).toHaveTextContent('{"command":"ls')
      expect(within(header).getByTestId('tool-icon-spinner')).toBeInTheDocument()
    })

    it('R1: a tool_use block with toolName shows that name as streaming', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'tool_use', toolName: 'Read', partialJson: '{"file' }))} />)
      const header = within(screen.getByTestId('partial-group')).getByTestId('tool-header')
      expect(header).toHaveTextContent('Read')
      expect(within(header).getByTestId('tool-icon-spinner')).toBeInTheDocument()
    })

    it('no partial (null or no blocks) → no partial group', () => {
      const { rerender } = render(<ConversationMessages messages={[assistantText]} keyPrefix="k" showThinking={false} showEmptyHint={false} partial={null} />)
      expect(screen.queryByTestId('partial-group')).not.toBeInTheDocument()
      rerender(<ConversationMessages messages={[assistantText]} keyPrefix="k" showThinking={false} showEmptyHint={false} partial={assembly()} />)
      expect(screen.queryByTestId('partial-group')).not.toBeInTheDocument()
    })
  })

  describe('durable tool activity (R2)', () => {
    it('R2: a durable tool_use with a running tools entry shows the spinner and the elapsed badge', () => {
      render(<ConversationMessages messages={[durableTool]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        tools={{ tu1: running }} now={13_400} />)
      expect(screen.getByTestId('tool-icon-spinner')).toBeInTheDocument()
      expect(screen.getByTestId('tool-elapsed')).toHaveTextContent('12.4s')
      expect(screen.queryByTestId('tool-icon-wrench')).not.toBeInTheDocument()
    })

    it('R2: a durable tool_use with a done tools entry shows the wrench and the duration badge', () => {
      render(<ConversationMessages messages={[durableTool]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        tools={{ tu1: { ...running, endedAt: 7_200, status: 'done' } }} now={99_999} />)
      expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
      expect(screen.getByTestId('tool-duration')).toHaveTextContent('6.2s')
    })

    it("R2: a durable tool_use without a tools entry renders today's DOM (wrench, no badge)", () => {
      render(<ConversationMessages messages={[durableTool]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        tools={{ other: running }} now={13_400} />)
      expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
      expect(screen.queryByTestId('tool-icon-spinner')).not.toBeInTheDocument()
      expect(screen.queryByTestId('tool-elapsed')).not.toBeInTheDocument()
      expect(screen.queryByTestId('tool-duration')).not.toBeInTheDocument()
      expect(screen.queryByTestId('tool-aborted')).not.toBeInTheDocument()
    })
  })

  // ---- P-B3.2 spec §4.4 R4: raw user tool_result → ToolResultBlock.facts by tool_use_id ----
  describe('tool result facts (P-B3 R4)', () => {
    const resultFrame = (toolUseId: string): StreamMessage => ({
      type: 'user',
      message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: toolUseId, content: 'edited', is_error: false }], stop_reason: null },
    } as StreamMessage)
    const editDone: ToolActivity = {
      name: 'Edit', startedAt: 1, endedAt: 2, status: 'done',
      diff: { path: '/x', added: 1, removed: 1, hunks: [], truncated: false },
    }

    it('a tools entry for the tool_use_id with a diff → facts span "+1 −1"', () => {
      render(<ConversationMessages messages={[resultFrame('tu1')]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        tools={{ tu1: editDone }} />)
      expect(screen.getByTestId('tool-result-facts')).toHaveTextContent('+1 −1')
    })

    it('no entry for this tool_use_id → no facts span', () => {
      render(<ConversationMessages messages={[resultFrame('tu1')]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        tools={{ other: editDone }} />)
      expect(screen.getByTestId('tool-result-block')).toBeInTheDocument()
      expect(screen.queryByTestId('tool-result-facts')).not.toBeInTheDocument()
    })

    it('tools undefined (Stream mode) → no facts span', () => {
      render(<ConversationMessages messages={[resultFrame('tu1')]} keyPrefix="k" showThinking={false} showEmptyHint={false} />)
      expect(screen.getByTestId('tool-result-block')).toBeInTheDocument()
      expect(screen.queryByTestId('tool-result-facts')).not.toBeInTheDocument()
    })

    it('own-key lookup: tool_use_id "constructor" with an empty tools map → no facts, no crash', () => {
      render(<ConversationMessages messages={[resultFrame('constructor')]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        tools={{}} />)
      expect(screen.getByTestId('tool-result-block')).toBeInTheDocument()
      expect(screen.queryByTestId('tool-result-facts')).not.toBeInTheDocument()
      expect(screen.queryByTestId('tool-result-denied')).not.toBeInTheDocument()
    })

    it('own-key lookup: an entry reachable only through the prototype chain is ignored', () => {
      // `tools.tu1` resolves via the prototype but is not an own key — a
      // `tools?.[id]` lookup would pick it up; Object.hasOwn must not.
      const inherited = Object.create({ tu1: editDone }) as Record<string, ToolActivity>
      render(<ConversationMessages messages={[resultFrame('tu1')]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        tools={inherited} />)
      expect(screen.getByTestId('tool-result-block')).toBeInTheDocument()
      expect(screen.queryByTestId('tool-result-facts')).not.toBeInTheDocument()
    })
  })

  describe('auto-scroll (R4)', () => {
    const scrollTo = vi.fn()
    afterEach(() => {
      scrollTo.mockClear()
      delete (Element.prototype as { scrollTo?: unknown }).scrollTo
    })

    it('R4: partial text growth with the same messages reference scrolls again', () => {
      Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
      const messages = [assistantText]
      const { rerender } = render(<ConversationMessages messages={messages} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'text', text: 'he' }))} />)
      expect(scrollTo).toHaveBeenCalledTimes(1)
      rerender(<ConversationMessages messages={messages} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'text', text: 'hello' }))} />)
      expect(scrollTo).toHaveBeenCalledTimes(2)
      // Same content, new object identity → no extra scroll: the length
      // counter, not the assembly's identity, is the effect dep.
      rerender(<ConversationMessages messages={messages} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'text', text: 'hello' }))} />)
      expect(scrollTo).toHaveBeenCalledTimes(2)
    })

    it('R4: a started tool_use with empty input (no delta yet) scrolls when its row appears', () => {
      Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
      const messages = [assistantText]
      const { rerender } = render(<ConversationMessages messages={messages} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'text', text: 'hello' }))} />)
      expect(scrollTo).toHaveBeenCalledTimes(1)
      rerender(<ConversationMessages messages={messages} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'text', text: 'hello' }), pb(1, { type: 'tool_use', toolName: 'Bash' }))} />)
      expect(scrollTo).toHaveBeenCalledTimes(2)
    })
  })
})
