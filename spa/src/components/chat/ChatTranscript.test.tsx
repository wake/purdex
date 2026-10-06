// spa/src/components/chat/ChatTranscript.test.tsx — the chat transcript
// (spec §5, R2 plan T1.3): the agent's bubbles on the left, yours on the
// right, no thinking, no ceremony; a turn's tools fold into quiet lines (R2-B).
import type { ReactNode } from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import ChatTranscript, { type ChatTranscriptProps } from './ChatTranscript'
import { ChatUserBubble } from './ChatBubble'
import { FoldContext, useFoldMemory } from '../room/fold-context'
import type { ContentBlock, StreamMessage } from '../../lib/nex/message-types'
import type { PartialAssembly } from '../../lib/nex/partial'
import type { DiffHunk, ToolActivity } from '../../lib/nex/tool-activity'
import type { TurnMeta } from '../../lib/nex/event-reducer'

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

  it('a Read result that carries an image shows no raw JSON (#1629)', () => {
    render(T({ messages: [
      said('q'),
      asst(use('r1', 'Read', { file_path: '/a.png' })),
      usr({ type: 'tool_result', tool_use_id: 'r1', content: [{ type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 80 } }] } as unknown as ContentBlock),
      reply('done'),
    ] }))
    fireEvent.click(screen.getByTestId('chat-tools-line'))
    expect(screen.getByTestId('operation-block')).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('omitted')
    expect(document.body.textContent).not.toContain('"type"')
    expect(screen.getByTestId('prelude-media')).toHaveTextContent('[image · png · 80 B]')
    expect(screen.queryByTestId('op-non-text')).toBeNull()
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
        <ChatUserBubble text="second" pending><span data-testid="pending">queued</span></ChatUserBubble>
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

  // R2 plan T2.1: a turn's operations as quiet lines.
  describe('operation lines', () => {
    const errRes = (id: string, content: string): ContentBlock =>
      ({ type: 'tool_result', tool_use_id: id, content, is_error: true })
    const entry = (status: ToolActivity['status'], over: Partial<ToolActivity> = {}): ToolActivity =>
      ({ name: 'x', startedAt: 1, endedAt: status === 'running' ? null : 2, status, ...over })
    const hunk: DiffHunk = { oldStart: 1, oldLines: 1, newStart: 1, newLines: 4, lines: [' a', '+b', '+c', '+d'] }
    const diff = { path: '/w/notes.md', added: 3, removed: 0, hunks: [hunk], truncated: false }
    const toolUse = (i: number, id: string, name: string): PartialAssembly['blocks'][number] =>
      ({ index: i, type: 'tool_use', text: '', thinking: '', partialJson: '{"com', toolId: id, toolName: name })

    it("folds a turn's tools into one line", () => {
      render(T({ messages: [
        said('q'),
        asst(use('a', 'Read', { file_path: '/a' })), usr(res('a', 'AAA')),
        asst(use('b', 'Grep', { pattern: 'x' })), usr(res('b', 'BBB')),
        reply('done'),
      ] }))
      expect(screen.getAllByTestId('chat-tools-line')).toHaveLength(1)
      expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Used 2 tools')
      expect(screen.queryByTestId('operation-block')).toBeNull()
      expect(screen.queryByText('AAA')).toBeNull()
    })

    it('expands the line into the room blocks in place', () => {
      render(T({ messages: [
        said('q'),
        asst(use('a', 'Read', { file_path: '/a' })), usr(res('a', 'AAA')),
        asst(use('b', 'Grep', { pattern: 'x' })), usr(res('b', 'BBB')),
        reply('done'),
      ] }))
      fireEvent.click(screen.getByTestId('chat-tools-line'))
      const ops = screen.getByTestId('chat-tools-ops')
      const blocks = within(ops).getAllByTestId('operation-block')
      expect(blocks.map((b) => within(b).getByTestId('op-name').textContent)).toEqual(['Read', 'Grep'])
      expect(within(ops).getByText('AAA')).toBeInTheDocument()
      // In place: right under the line, before the reply that followed the tools.
      expect(follows(screen.getByTestId('chat-tools-line'), ops)).toBe(true)
      expect(follows(ops, screen.getByText('done'))).toBe(true)
    })

    it('gives an edit its own Edited line with the stat', () => {
      render(T({
        messages: [
          said('q'),
          asst(use('r', 'Read', { file_path: '/w/notes.md' }), use('e', 'Edit', { file_path: '/w/notes.md' })),
          usr(res('r', 'x'), res('e', 'ok')),
        ],
        tools: { e: entry('done', { diff }) },
      }))
      expect(screen.getByTestId('chat-edited-line')).toHaveTextContent('Edited notes.md (+3 −0)')
      // Not counted in the tools line.
      expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Used 1 tool')
      fireEvent.click(screen.getByTestId('chat-edited-line'))
      expect(screen.getByTestId('tool-diff')).toBeInTheDocument()
    })

    it('never hides a failed tool', () => {
      render(T({ messages: [
        said('q'),
        asst(use('r', 'Read', { file_path: '/a' }), use('x', 'Bash', { command: 'false' })),
        usr(res('r', 'x'), errRes('x', 'boom: exit 1\nmore')),
      ] }))
      const failed = screen.getByTestId('chat-failed-line')
      expect(failed).toHaveTextContent('Bash · boom: exit 1')
      expect(failed.className).toContain('text-status-error')
      expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Used 1 tool')
      fireEvent.click(failed)
      const block = screen.getByTestId('operation-block')
      expect(within(block).getByTestId('op-name')).toHaveTextContent('Bash')
    })

    it('a denied tool is a failed line', () => {
      render(T({
        messages: [said('q'), asst(use('w', 'Write', { file_path: '/etc/x' })), usr(res('w', 'Permission denied by user'))],
        tools: { w: entry('denied') },
      }))
      expect(screen.getByTestId('chat-failed-line')).toHaveTextContent('Write · Permission denied by user')
      expect(screen.queryByTestId('chat-tools-line')).toBeNull()
    })

    it("places the tools line at the turn's first operation", () => {
      render(T({ messages: [
        said('q'),
        asst({ type: 'text', text: 'before' }, use('a', 'Read', { file_path: '/a' })), usr(res('a', 'x')),
        asst({ type: 'text', text: 'between' }, use('b', 'Grep', { pattern: 'y' })), usr(res('b', 'y')),
        reply('after'),
      ] }))
      const line = screen.getByTestId('chat-tools-line')
      expect(follows(screen.getByText('before'), line)).toBe(true)
      expect(follows(line, screen.getByText('between'))).toBe(true)
      expect(screen.getAllByTestId('chat-tools-line')).toHaveLength(1)
      expect(line).toHaveTextContent('Used 2 tools')
    })

    it('draws no tools line for a turn with none', () => {
      render(T({
        messages: [
          said('one'), asst(use('a', 'Read', { file_path: '/a' })), usr(res('a', 'x')), reply('a'),
          said('two'), asst(use('e', 'Edit', { file_path: '/w/notes.md' })), usr(res('e', 'ok')), reply('b'),
          said('three'), reply('c'),
        ],
        turnStarts: [0, 4, 8],
        tools: { e: entry('done', { diff }) },
      }))
      const turns = screen.getAllByTestId('room-turn')
      expect(within(turns[0]).getByTestId('chat-tools-line')).toBeInTheDocument()
      expect(within(turns[1]).queryByTestId('chat-tools-line')).toBeNull()
      expect(within(turns[1]).getByTestId('chat-edited-line')).toBeInTheDocument()
      expect(within(turns[2]).queryByTestId('chat-tools-line')).toBeNull()
    })

    it('says the tools are running while one is', () => {
      const messages = [said('q'), asst(use('a', 'Read', { file_path: '/a' })), usr(res('a', 'x')), asst(use('r', 'Bash', { command: 'sleep 9' }))]
      const { rerender } = render(T({ messages, tools: { a: entry('done'), r: entry('running') } }))
      expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Using 2 tools…')
      rerender(T({ messages: [...messages, usr(res('r', 'slept'))], tools: { a: entry('done'), r: entry('done') } }))
      expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Used 2 tools')
    })

    it("counts a tool_use still streaming its input in the turn's running line", () => {
      const partial: PartialAssembly = { messageId: 'm2', finalized: 0, blocks: { 0: toolUse(0, 's', 'Bash') } }
      // A turn with no durable tool yet: the line appears for the streaming call alone.
      const { rerender } = render(T({ messages: [said('q')], turnStarts: [0], partial }))
      expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Using 1 tool…')
      fireEvent.click(screen.getByTestId('chat-tools-line'))
      expect(within(screen.getByTestId('chat-tools-ops')).getByTestId('op-arg-pending')).toBeInTheDocument()
      // With durable tools in the turn, it joins their line.
      rerender(T({ messages: [said('q'), asst(use('a', 'Read', { file_path: '/a' })), usr(res('a', 'x'))], turnStarts: [0], partial }))
      expect(screen.getAllByTestId('chat-tools-line')).toHaveLength(1)
      expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Using 2 tools…')
    })

    // PR #1471 R1 (P3): with no durable tool yet, a partial holding text
    // then a tool_use put the line above the text still being typed, and it
    // jumped below once the text was finalised. The line follows the stream.
    it('puts a streaming-only tools line after the text streaming before it', () => {
      const partial: PartialAssembly = {
        messageId: 'm2', finalized: 0,
        blocks: {
          0: { index: 0, type: 'text', text: 'let me check', thinking: '', partialJson: '' },
          1: toolUse(1, 's', 'Bash'),
        },
      }
      render(T({ messages: [said('q')], turnStarts: [0], partial }))
      const text = screen.getByTestId('chat-partial-group')
      const line = screen.getByTestId('chat-tools-line')
      expect(text.compareDocumentPosition(line) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    })

    it('a Task counts as a tool and expands into its subagent', () => {
      render(T({ messages: [
        said('go'),
        asst(use('T', 'Task', { subagent_type: 'general-purpose', description: 'look around' })),
        child(said('SUBAGENT PROMPT'), 'T'),
        child(asst(use('c', 'Bash', { command: 'ls' })), 'T'),
        child(usr(errRes('c', 'child boom')), 'T'),
        child(reply('SUBAGENT REPLY'), 'T'),
        usr(res('T', 'handed back')),
        reply('done'),
      ] }))
      // One tool: the Task. Its subagent's failed call is inside it, not a red line of this turn.
      expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Used 1 tool')
      expect(screen.queryByTestId('chat-failed-line')).toBeNull()
      fireEvent.click(screen.getByTestId('chat-tools-line'))
      const ops = screen.getByTestId('chat-tools-ops')
      expect(within(ops).getAllByTestId('operation-block')[0]).toHaveTextContent('Task')
      fireEvent.click(within(ops).getByTestId('subagent-toggle'))
      expect(within(ops).getByText('SUBAGENT REPLY')).toBeInTheDocument()
    })

    it('expand-all opens the tools line', () => {
      // The pane's memory, with a stand-in for turn 0's expand-all (chat draws no strip).
      function Pane({ children }: { children: ReactNode }) {
        const s = useFoldMemory()
        return (
          <FoldContext.Provider value={s}>
            <button type="button" data-testid="expand-turn-0" onClick={() => s.setTurn(0, true)} />
            {children}
          </FoldContext.Provider>
        )
      }
      render(<Pane>{T({ messages: [said('q'), asst(use('a', 'Read', { file_path: '/a' })), usr(res('a', 'AAA'))] })}</Pane>)
      expect(screen.queryByTestId('chat-tools-ops')).toBeNull()
      fireEvent.click(screen.getByTestId('expand-turn-0'))
      expect(within(screen.getByTestId('chat-tools-ops')).getByText('AAA')).toBeInTheDocument()
    })
  })

  describe('auto-scroll', () => {
    const scrollTo = vi.fn()
    afterEach(() => {
      scrollTo.mockClear()
      delete (Element.prototype as { scrollTo?: unknown }).scrollTo
    })

    // F3: a (re)mount — the pane opening, or a view switch — jumps to the
    // bottom at once; only later growth animates.
    it('F3: the first scroll is instant, later ones smooth', () => {
      Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
      const { rerender } = render(T({ messages: [said('q')] }))
      expect(scrollTo).toHaveBeenCalledTimes(1)
      expect(scrollTo.mock.calls[0][0]).toMatchObject({ behavior: 'auto' })
      rerender(T({ messages: [said('q'), reply('a')] }))
      expect(scrollTo).toHaveBeenCalledTimes(2)
      expect(scrollTo.mock.calls[1][0]).toMatchObject({ behavior: 'smooth' })
    })

    // F10: chat draws no thought, so a streaming thought is no reason to scroll.
    it('F10: a streaming thought does not scroll; streaming text does', () => {
      Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
      const messages = [said('q')]
      const block = (i: number, type: 'text' | 'thinking', s: string) =>
        ({ index: i, type, text: type === 'text' ? s : '', thinking: type === 'thinking' ? s : '', partialJson: '' })
      const partial = (...blocks: ReturnType<typeof block>[]): PartialAssembly =>
        ({ messageId: 'm1', finalized: 0, blocks: Object.fromEntries(blocks.map((b) => [b.index, b])) })
      const { rerender } = render(T({ messages, partial: partial(block(0, 'thinking', 'a')) }))
      expect(scrollTo).toHaveBeenCalledTimes(1)
      rerender(T({ messages, partial: partial(block(0, 'thinking', 'a longer thought')) }))
      rerender(T({ messages, partial: partial(block(0, 'thinking', 'a longer thought, still going')) }))
      expect(scrollTo).toHaveBeenCalledTimes(1)
      rerender(T({ messages, partial: partial(block(0, 'thinking', 'done'), block(1, 'text', 'he')) }))
      expect(scrollTo).toHaveBeenCalledTimes(2)
      rerender(T({ messages, partial: partial(block(0, 'thinking', 'done'), block(1, 'text', 'hello')) }))
      expect(scrollTo).toHaveBeenCalledTimes(3)
    })

    // R2-B: the tool lines grow the transcript without a new message — a
    // streaming tool_use draws the "using…" line, and an N2 status that turns
    // a call into a failure draws a red line. Both must scroll; a tool's input
    // streaming (which chat does not show) must not.
    it('follows the tool lines chat draws, not a tool input it hides', () => {
      Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
      const messages = [said('q'), asst(use('a', 'Bash', { command: 'x' }))]
      const tu = (json: string): PartialAssembly => ({
        messageId: 'm2', finalized: 0,
        blocks: { 0: { index: 0, type: 'tool_use', text: '', thinking: '', partialJson: json, toolId: 's', toolName: 'Read' } },
      })
      const running = { a: { name: 'Bash', startedAt: 1, endedAt: null, status: 'running' } as ToolActivity }
      const { rerender } = render(T({ messages, tools: running }))
      expect(scrollTo).toHaveBeenCalledTimes(1)
      // A streaming call joins the line.
      rerender(T({ messages, tools: running, partial: tu('{') }))
      expect(scrollTo).toHaveBeenCalledTimes(2)
      // Its input streams: nothing chat shows moved.
      rerender(T({ messages, tools: running, partial: tu('{"file_path":"/a"') }))
      expect(scrollTo).toHaveBeenCalledTimes(2)
      // N2 says the running call failed: a red line appears with no new message.
      rerender(T({ messages, tools: { a: { ...running.a, endedAt: 2, status: 'error' } }, partial: tu('{"file_path":"/a"') }))
      expect(scrollTo).toHaveBeenCalledTimes(3)
      expect(screen.getByTestId('chat-failed-line')).toBeInTheDocument()
    })
  })

  // ---- spec §7.2: turn footer ------------------------------------------------

  describe('turn footer (spec §7.2)', () => {
    const meta = (patch: Partial<TurnMeta>): TurnMeta => ({ startAt: 0, endAt: null, outcome: null, durationMs: null, ...patch })
    const twoTurns: StreamMessage[] = [said('hello'), reply('Hi there'), said('second'), reply('again')]

    it('shows a footer after a completed turn', () => {
      render(T({
        messages: twoTurns, turnStarts: [0, 2],
        turnMeta: [meta({ endAt: 1000, outcome: 'ok', durationMs: 500 }), meta({ endAt: 2000, outcome: 'failed', durationMs: 900 })],
      }))
      const turns = screen.getAllByTestId('room-turn')
      expect(within(turns[0]).getByTestId('turn-footer')).toHaveTextContent('Worked for')
      expect(within(turns[1]).getByTestId('turn-footer')).toHaveTextContent('Failed after')
    })

    it('shows no footer after a turn the user interrupted', () => {
      render(T({
        messages: [said('hello'), reply('Hi there')], turnStarts: [0],
        turnMeta: [meta({ endAt: 1000, outcome: 'interrupted', durationMs: 500 })],
      }))
      expect(screen.queryByTestId('turn-footer')).toBeNull()
    })

    it('shows no footer without turnMeta (absent prop)', () => {
      render(T({ messages: twoTurns, turnStarts: [0, 2] }))
      expect(screen.queryByTestId('turn-footer')).toBeNull()
    })
  })
})
