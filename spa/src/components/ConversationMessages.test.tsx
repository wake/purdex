// spa/src/components/ConversationMessages.test.tsx
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import ConversationMessages from './ConversationMessages'
import type { ContentBlock, StreamMessage } from '../lib/nex/message-types'
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
      const group = within(screen.getByTestId('partial-group'))
      expect(group.getByTestId('op-dot')).toHaveClass('animate-spin')
      expect(group.getByTestId('op-name')).toHaveTextContent(/^Bash$/)
      expect(group.getByTestId('op-arg-pending')).toBeInTheDocument()
      expect(group.queryByTestId('op-rail')).toBeNull()
    })

    it('R1: an unknown block renders nothing', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'unknown', text: 'x', thinking: 'y', partialJson: 'z' }))} />)
      expect(screen.getByTestId('partial-group')).toBeEmptyDOMElement()
    })

    it('R1: a snapshot-seeded tool_use block without toolName shows the execution.tool.unknown placeholder, never the half-assembled JSON', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'tool_use', partialJson: '{"command":"ls' }))} />)
      const group = screen.getByTestId('partial-group')
      expect(within(group).getByTestId('op-name')).toHaveTextContent('tool')
      // spec §3.1.1 #7: the placeholder, not the raw prefix.
      expect(group).not.toHaveTextContent('{"command":"ls')
      expect(within(group).getByTestId('op-arg-pending')).toBeInTheDocument()
      expect(within(group).getByTestId('op-dot')).toHaveClass('animate-spin')
    })

    it('R1: a tool_use block with toolName shows that name as streaming', () => {
      render(<ConversationMessages messages={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        partial={assembly(pb(0, { type: 'tool_use', toolName: 'Read', partialJson: '{"file' }))} />)
      const group = within(screen.getByTestId('partial-group'))
      expect(group.getByTestId('op-name')).toHaveTextContent('Read')
      expect(group.getByTestId('op-dot')).toHaveClass('animate-spin')
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
      expect(screen.getByTestId('op-dot')).toHaveClass('animate-spin')
      expect(screen.getByTestId('op-elapsed')).toHaveTextContent('12.4s')
    })

    it('R2: a durable tool_use with a done tools entry shows the success dot and the duration badge', () => {
      render(<ConversationMessages messages={[durableTool]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        tools={{ tu1: { ...running, endedAt: 7_200, status: 'done' } }} now={99_999} />)
      expect(screen.getByTestId('op-dot')).not.toHaveClass('animate-spin')
      expect(screen.getByTestId('op-dot').className).toContain('bg-status-success')
      expect(screen.getByTestId('op-duration')).toHaveTextContent('6.2s')
    })

    it('R2: a durable tool_use without a tools entry renders the plain pending block (no badge)', () => {
      render(<ConversationMessages messages={[durableTool]} keyPrefix="k" showThinking={false} showEmptyHint={false}
        tools={{ other: running }} now={13_400} />)
      expect(screen.getByTestId('operation-block')).toBeInTheDocument()
      expect(screen.getByTestId('op-dot')).not.toHaveClass('animate-spin')
      expect(screen.getByTestId('op-dot').className).toContain('bg-text-muted')
      expect(screen.queryByTestId('op-elapsed')).not.toBeInTheDocument()
      expect(screen.queryByTestId('op-duration')).not.toBeInTheDocument()
      expect(screen.queryByTestId('op-aborted')).not.toBeInTheDocument()
    })
  })

  // ---- T3.3: operations (spec §4.2) — one call ⊕ its result is one block ----
  const asst = (...blocks: ContentBlock[]): StreamMessage =>
    ({ type: 'assistant', message: { id: 'm', role: 'assistant', content: blocks, stop_reason: null } } as StreamMessage)
  const usr = (...blocks: ContentBlock[]): StreamMessage =>
    ({ type: 'user', message: { role: 'user', content: blocks, stop_reason: null } } as StreamMessage)
  const use = (id: string, name: string | undefined, input: Record<string, unknown> = {}): ContentBlock =>
    ({ type: 'tool_use', id, ...(name === undefined ? {} : { name }), input })
  const res = (id: string, content: string, isError = false): ContentBlock =>
    ({ type: 'tool_result', tool_use_id: id, content, is_error: isError })
  const longBody = Array.from({ length: 100 }, (_, i) => `line ${i + 1}`).join('\n')

  describe('operations (T3.3)', () => {
    it('pairs a call with its result into one block', () => {
      render(<ConversationMessages messages={[asst(use('tu1', 'Bash', { command: 'ls -la' })), usr(res('tu1', 'total 8'))]}
        keyPrefix="k" showThinking={false} showEmptyHint={false} />)
      expect(screen.getAllByTestId('operation-block')).toHaveLength(1)
      const one = screen.getByTestId('operation-block')
      expect(within(one).getByTestId('op-name')).toHaveTextContent('Bash')
      expect(within(one).getByTestId('op-arg')).toHaveTextContent('ls -la')
      expect(within(one).getByTestId('fold-body')).toHaveTextContent('total 8')
      expect(screen.queryByTestId('tool-result-block')).toBeNull()
    })

    it('renders an orphan result on its own', () => {
      render(<ConversationMessages messages={[usr(res('tu9', 'stray output'))]}
        keyPrefix="k" showThinking={false} showEmptyHint={false} />)
      expect(screen.getAllByTestId('operation-block')).toHaveLength(1)
      expect(screen.getByTestId('op-name')).toHaveTextContent('tool')
      expect(screen.getByTestId('fold-body')).toHaveTextContent('stray output')
    })

    it("remembers a block's expansion across a re-render", () => {
      const messages = [asst(use('tu1', 'Bash', { command: 'ls' })), usr(res('tu1', longBody))]
      const { rerender } = render(<ConversationMessages messages={messages}
        keyPrefix="k" showThinking={false} showEmptyHint={false} />)
      expect(screen.getByTestId('fold-more')).toBeInTheDocument()
      fireEvent.click(screen.getByTestId('fold-more'))
      expect(screen.getByTestId('fold-less')).toBeInTheDocument()
      // A new keyPrefix re-keys every row, so the blocks unmount and remount:
      // a useState inside the block would lose the expansion here.
      rerender(<ConversationMessages messages={messages}
        keyPrefix="k2" showThinking={false} showEmptyHint={false} />)
      expect(screen.getByTestId('fold-less')).toBeInTheDocument()
      expect(screen.queryByTestId('fold-more')).toBeNull()
    })

    it('renders each of two same-id calls with its own result', () => {
      render(<ConversationMessages
        messages={[
          asst(use('tu1', 'Bash', { command: 'first' }), use('tu1', 'Bash', { command: 'second' })),
          usr(res('tu1', 'ANSWER A'), res('tu1', 'ANSWER B')),
        ]}
        keyPrefix="k" showThinking={false} showEmptyHint={false} />)
      const blocks = screen.getAllByTestId('operation-block')
      // Two calls, two blocks — and no third block for a result shown on its own.
      expect(blocks).toHaveLength(2)
      expect(within(blocks[0]).getByTestId('op-arg')).toHaveTextContent('first')
      expect(within(blocks[0]).getByTestId('fold-body')).toHaveTextContent('ANSWER A')
      expect(within(blocks[1]).getByTestId('op-arg')).toHaveTextContent('second')
      expect(within(blocks[1]).getByTestId('fold-body')).toHaveTextContent('ANSWER B')
      // Neither body leaked into the other block.
      expect(blocks[0]).not.toHaveTextContent('ANSWER B')
      expect(blocks[1]).not.toHaveTextContent('ANSWER A')
    })

    it('renders every result exactly once', () => {
      render(<ConversationMessages
        messages={[
          asst(use('tu1', 'Bash', { command: 'a' }), use('tu2', 'Read', { file_path: '/x' }), use('tu4', undefined, {})),
          // out of order, plus one result nothing called for
          usr(res('tu2', 'ANSWER 2'), res('tu1', 'ANSWER 1'), res('tu3', 'ORPHAN'), res('tu4', 'ANSWER 4')),
        ]}
        keyPrefix="k" showThinking={false} showEmptyHint={false} />)
      // three calls + one orphan result
      expect(screen.getAllByTestId('operation-block')).toHaveLength(4)
      expect(screen.getAllByTestId('fold-body')).toHaveLength(4)
      for (const text of ['ANSWER 1', 'ANSWER 2', 'ANSWER 4', 'ORPHAN']) {
        expect(screen.getAllByText(text)).toHaveLength(1)
      }
      // A call with no name still renders — otherwise its result is consumed
      // by the pairing and then shown by nobody.
      const blocks = screen.getAllByTestId('operation-block')
      expect(within(blocks[2]).getByTestId('op-name')).toHaveTextContent('tool')
      expect(within(blocks[2]).getByTestId('fold-body')).toHaveTextContent('ANSWER 4')
    })
  })

  // ---- own-key lookup into `tools` (inherited from ToolUseBlock, T3.3) -------
  // `tools` is keyed by tool_use ids, which are foreign strings: a plain
  // `tools[id]` reads `Object.prototype.constructor` for the id `constructor`
  // and picks up any entry someone hung on the prototype. ToolUseBlock held
  // these two guards; T3.3 deleted it, so the lookup — and the guards — live
  // here now.
  describe('own-key lookup into tools', () => {
    const hunk = { oldStart: 1, oldLines: 3, newStart: 1, newLines: 3, lines: [' hello', '-world', '+nexen', ' three'] }
    const diffEntry: ToolActivity = {
      name: 'Edit', startedAt: 1, endedAt: 2, status: 'done',
      diff: { path: '/x', added: 1, removed: 1, hunks: [hunk], truncated: false },
    }
    const fileEntry: ToolActivity = {
      name: 'Read', startedAt: 1, endedAt: 2, status: 'done', file: { path: '/srv/x.ts', lines: 4 },
    }
    const resultFrame = (toolUseId: string): StreamMessage => usr(res(toolUseId, 'edited'))

    it('a `constructor` id with an empty tools map does not reach Object.prototype', () => {
      render(<ConversationMessages messages={[asst(use('constructor', 'Bash', { command: 'ls' }))]}
        keyPrefix="k" showThinking={false} showEmptyHint={false} tools={{}} now={13_400} />)
      expect(screen.getByTestId('op-name')).toHaveTextContent('Bash')
      expect(screen.queryByTestId('tool-diff')).toBeNull()
      expect(screen.queryByTestId('op-elapsed')).toBeNull()
      expect(screen.queryByTestId('op-duration')).toBeNull()
      expect(screen.queryByTestId('op-aborted')).toBeNull()
    })

    it('a `constructor` entry on the prototype is ignored; the same entry as an own key is found', () => {
      const msgs = [asst(use('constructor', 'Edit', { file_path: '/x' })), usr(res('constructor', 'edited'))]
      const inherited = Object.create({ constructor: diffEntry }) as Record<string, ToolActivity>
      const { unmount } = render(<ConversationMessages messages={msgs}
        keyPrefix="k" showThinking={false} showEmptyHint={false} tools={inherited} now={13_400} />)
      expect(screen.getByTestId('operation-block')).toBeInTheDocument()
      expect(screen.queryByTestId('tool-diff')).toBeNull()
      unmount()
      // The positive control: without it "no diff" would pass against anything.
      render(<ConversationMessages messages={msgs}
        keyPrefix="k" showThinking={false} showEmptyHint={false} tools={{ constructor: diffEntry }} now={13_400} />)
      expect(screen.getByTestId('tool-diff')).toBeInTheDocument()
    })

    it('an entry reachable only through the prototype chain is ignored', () => {
      const inherited = Object.create({ tu1: running }) as Record<string, ToolActivity>
      const { unmount } = render(<ConversationMessages messages={[durableTool]}
        keyPrefix="k" showThinking={false} showEmptyHint={false} tools={inherited} now={13_400} />)
      expect(screen.getByTestId('op-dot')).not.toHaveClass('animate-spin')
      expect(screen.queryByTestId('op-elapsed')).toBeNull()
      unmount()
      render(<ConversationMessages messages={[durableTool]}
        keyPrefix="k" showThinking={false} showEmptyHint={false} tools={{ tu1: running }} now={13_400} />)
      expect(screen.getByTestId('op-elapsed')).toHaveTextContent('12.4s')
    })

    it('an orphan result does not take its name from a prototype entry', () => {
      const inherited = Object.create({ tu9: fileEntry }) as Record<string, ToolActivity>
      const { unmount } = render(<ConversationMessages messages={[resultFrame('tu9')]}
        keyPrefix="k" showThinking={false} showEmptyHint={false} tools={inherited} />)
      expect(screen.getByTestId('op-name')).toHaveTextContent('tool')
      unmount()
      render(<ConversationMessages messages={[resultFrame('tu9')]}
        keyPrefix="k" showThinking={false} showEmptyHint={false} tools={{ tu9: fileEntry }} />)
      expect(screen.getByTestId('op-name')).toHaveTextContent('/srv/x.ts')
    })

    it('tools undefined (Stream mode) → the orphan result still renders, with no facts', () => {
      render(<ConversationMessages messages={[resultFrame('tu1')]}
        keyPrefix="k" showThinking={false} showEmptyHint={false} />)
      expect(screen.getByTestId('operation-block')).toBeInTheDocument()
      expect(screen.getByTestId('op-name')).toHaveTextContent('tool')
      expect(screen.queryByTestId('tool-diff')).toBeNull()
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
