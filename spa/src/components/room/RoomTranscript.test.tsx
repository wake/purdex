// spa/src/components/room/RoomTranscript.test.tsx — the room transcript
// (spec §4.1): one left edge, turns as containers, and everything
// ConversationMessages did (pairing, orphans, fold memory, the partial group,
// auto-scroll) carried over unchanged.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import RoomTranscript, { type RoomTranscriptProps } from './RoomTranscript'
import RoomUserLine from './RoomUserLine'
import type { ContentBlock, StreamMessage } from '../../lib/nex/message-types'
import type { PartialAssembly, PartialBlock } from '../../lib/nex/partial'
import type { ToolActivity } from '../../lib/nex/tool-activity'

const assistantText: StreamMessage = { type: 'assistant', message: { role: 'assistant', content: [{ type: 'text', text: 'Hi there' }], stop_reason: null } } as StreamMessage

const asst = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'assistant', message: { id: 'm', role: 'assistant', content: blocks, stop_reason: null } } as StreamMessage)
const usr = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'user', message: { role: 'user', content: blocks, stop_reason: null } } as StreamMessage)
const said = (text: string): StreamMessage => usr({ type: 'text', text })
const use = (id: string, name: string | undefined, input: Record<string, unknown> = {}): ContentBlock =>
  ({ type: 'tool_use', id, ...(name === undefined ? {} : { name }), input })
const res = (id: string, content: string, isError = false): ContentBlock =>
  ({ type: 'tool_result', tool_use_id: id, content, is_error: isError })
const longBody = Array.from({ length: 100 }, (_, i) => `line ${i + 1}`).join('\n')
const follows = (a: Element, b: Element) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)

/** The pane's defaults; a test states only what it is about. */
const T = (props: Partial<RoomTranscriptProps> & Pick<RoomTranscriptProps, 'messages'>) => (
  <RoomTranscript keyPrefix="k" showThinking={false} showEmptyHint={false} {...props} />
)

describe('RoomTranscript', () => {
  it('renders assistant text and shows nothing else when empty hint is off', () => {
    render(T({ messages: [assistantText] }))
    expect(screen.getByText('Hi there')).toBeInTheDocument()
    expect(screen.queryByText(/waiting/i)).not.toBeInTheDocument()
  })

  it('shows the default waiting hint, or the override, when asked', () => {
    const { rerender } = render(T({ messages: [], showEmptyHint: true }))
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
    rerender(T({ messages: [], showEmptyHint: true, emptyText: 'No history yet' }))
    expect(screen.getByText('No history yet')).toBeInTheDocument()
  })

  it('renders children before the thinking indicator and afterThinking after it', () => {
    render(
      <RoomTranscript messages={[assistantText]} keyPrefix="k" showThinking showEmptyHint={false}
        afterThinking={<div data-testid="after">after</div>}>
        <div data-testid="child">child</div>
      </RoomTranscript>,
    )
    const child = screen.getByTestId('child')
    const indicator = screen.getByTestId('thinking-indicator')
    const after = screen.getByTestId('after')
    expect(follows(child, indicator)).toBe(true)
    expect(follows(indicator, after)).toBe(true)
  })

  it('renders the four user block kinds', () => {
    render(T({
      messages: [
        usr(res('t', 'out')),
        said('[Request interrupted by user]'),
        said('/compact'),
        said('plain'),
      ],
    }))
    expect(screen.getByTestId('interrupted-msg')).toBeInTheDocument()
    expect(screen.getByTestId('room-command')).toHaveTextContent('/compact')
    expect(screen.getByTestId('room-user-line')).toHaveTextContent('plain')
    expect(screen.getByText('out')).toBeInTheDocument()
  })

  // ---- spec §4.1: one left edge ------------------------------------------

  describe('one left edge (T4.3)', () => {
    const everyKind: StreamMessage[] = [
      said('please list'),
      asst({ type: 'thinking', thinking: 'short thought' }, { type: 'text', text: 'Sure.' }, use('tu1', 'Bash', { command: 'ls' })),
      usr(res('tu1', 'a\nb')),
      said('[Request interrupted by user]'),
      said('/compact'),
    ]

    it('renders every message at one left edge', () => {
      const { container } = render(T({ messages: everyKind, turnStarts: [0] }))
      expect(container.querySelector('.justify-end')).toBeNull()
      expect(container.querySelector('[class*="max-w-["][class*="%]"]')).toBeNull()
    })

    it('renders a user line with a gutter mark, not a bubble', () => {
      render(T({ messages: [said('hello')], turnStarts: [0] }))
      const line = screen.getByTestId('room-user-line')
      expect(line).toHaveTextContent('hello')
      expect(line.className).toContain('text-text-primary')
      expect(line.className).toContain('font-medium')
      expect(within(line).getByTestId('room-user-mark').className).toContain('bg-accent')
      expect(screen.queryByTestId('user-bubble')).toBeNull()
      // No hard-coded bubble colours left.
      expect(line.outerHTML).not.toMatch(/#[0-9a-f]{3,6}/i)
    })

    it('keeps a user line as plain text, not markdown', () => {
      render(T({ messages: [said('**not bold**')] }))
      expect(screen.getByTestId('room-user-line')).toHaveTextContent('**not bold**')
      expect(document.querySelector('strong')).toBeNull()
    })

    it('caps prose at a reading measure but not output', () => {
      render(T({ messages: everyKind, turnStarts: [0] }))
      expect(screen.getByTestId('room-prose').className).toContain('max-w-[90ch]')
      expect(screen.getByTestId('operation-block').className).not.toMatch(/max-w-/)
    })

    it('renders the interrupt sentinel without a bubble', () => {
      render(T({ messages: [said('[Request interrupted by user]')] }))
      const el = screen.getByTestId('interrupted-msg')
      expect(el).toHaveTextContent(/interrupted/i)
      expect(el.className).toContain('text-status-error')
      expect(el.className).toContain('italic')
      expect(el.className).not.toMatch(/rounded|bg-/)
      expect(el.outerHTML).not.toMatch(/#[0-9a-f]{3,6}/i)
      expect(screen.queryByTestId('room-user-line')).toBeNull()
    })

    it('renders a slash command at the left edge', () => {
      render(T({ messages: [said('/compact')] }))
      const el = screen.getByTestId('room-command')
      expect(el).toHaveTextContent('/compact')
      expect(el.className).toContain('text-status-warning')
      expect(el.className).toContain('font-mono')
      expect(el.className).not.toMatch(/rounded|bg-/)
      expect(el.outerHTML).not.toMatch(/#[0-9a-f]{3,6}/i)
      expect(el.parentElement?.className ?? '').not.toContain('justify-end')
    })
  })

  // ---- spec §4.3: thinking -------------------------------------------------

  describe('thinking (T4.3)', () => {
    it('renders nothing for an empty thinking block', () => {
      render(T({ messages: [asst({ type: 'thinking', thinking: '' }, { type: 'text', text: 'answer' })] }))
      expect(screen.queryByTestId('room-thinking')).toBeNull()
      expect(screen.queryByTestId('thinking-header')).toBeNull()
      expect(screen.getByText('answer')).toBeInTheDocument()
    })

    it('renders a word count for a thinking block with text', () => {
      render(T({ messages: [asst({ type: 'thinking', thinking: 'one two three' })] }))
      expect(screen.getByTestId('thinking-header')).toHaveTextContent('Thought · 3 words')
    })

    it('shows a short thought whole and folds a long one', () => {
      const long = Array.from({ length: 100 }, (_, i) => `t${i + 1}`).join('\n')
      render(T({ messages: [asst({ type: 'thinking', thinking: 'a\nb' }), asst({ type: 'thinking', thinking: long })] }))
      const [short, folded] = screen.getAllByTestId('room-thinking')
      expect(within(short).queryByTestId('fold-more')).toBeNull()
      expect(within(short).getByTestId('fold-body').textContent).toBe('a\nb')
      expect(within(folded).getByTestId('fold-body').textContent).toBe('t1\nt2\nt3')
      expect(within(folded).getByTestId('fold-more')).toHaveTextContent('+97 lines')
    })
  })

  // ---- spec §4.1: turns ------------------------------------------------------

  describe('turns (T4.3)', () => {
    const twoTurns: StreamMessage[] = [said('first'), assistantText, said('second'), asst({ type: 'text', text: 'again' })]

    it('groups messages into turn containers', () => {
      render(T({ messages: twoTurns, turnStarts: [0, 2] }))
      const turns = screen.getAllByTestId('room-turn')
      expect(turns).toHaveLength(2)
      expect(turns.map((t) => t.getAttribute('data-turn-index'))).toEqual(['0', '1'])
      expect(within(turns[0]).getByText('first')).toBeInTheDocument()
      expect(within(turns[0]).getByText('Hi there')).toBeInTheDocument()
      expect(within(turns[1]).getByText('second')).toBeInTheDocument()
      expect(within(turns[1]).getByText('again')).toBeInTheDocument()
    })

    it('takes the boundaries from turnStarts, not from where user lines fall', () => {
      // Two user lines, one recorded boundary: one turn (spec §4.1 — explicit, not inferred).
      render(T({ messages: twoTurns, turnStarts: [0] }))
      expect(screen.getAllByTestId('room-turn')).toHaveLength(1)
    })

    it('puts everything in one turn when no boundary was recorded', () => {
      render(T({ messages: twoTurns }))
      expect(screen.getAllByTestId('room-turn')).toHaveLength(1)
    })

    it('draws no separator and no per-turn facts between turns', () => {
      const { container } = render(T({ messages: twoTurns, turnStarts: [0, 2] }))
      expect(container.querySelector('hr')).toBeNull()
      for (const turn of screen.getAllByTestId('room-turn')) {
        expect(turn.className).not.toMatch(/border|divide/)
      }
    })

    it('expand all reaches the operations and thoughts of its own turn only', () => {
      const long = Array.from({ length: 100 }, (_, i) => `t${i + 1}`).join('\n')
      render(T({
        messages: [
          said('one'), asst({ type: 'thinking', thinking: long }, use('a', 'Bash', { command: 'x' })), usr(res('a', longBody)),
          said('two'), asst(use('b', 'Bash', { command: 'y' })), usr(res('b', longBody)),
        ],
        turnStarts: [0, 3],
      }))
      const [first, second] = screen.getAllByTestId('room-turn')
      fireEvent.click(within(first).getByTestId('turn-expand-all'))
      expect(within(first).queryByTestId('fold-more')).toBeNull()
      expect(within(first).getAllByTestId('fold-less')).toHaveLength(2)
      expect(within(second).getByTestId('fold-more')).toBeInTheDocument()
    })

    it('keeps the optimistic pending line inside a turn container', () => {
      render(
        <RoomTranscript messages={twoTurns} turnStarts={[0, 2]} keyPrefix="k" showThinking={false} showEmptyHint={false}>
          <RoomUserLine text="third" pending />
        </RoomTranscript>,
      )
      const turns = screen.getAllByTestId('room-turn')
      expect(turns).toHaveLength(3)
      expect(turns[2]).toHaveAttribute('data-turn-index', '2')
      expect(within(turns[2]).getByTestId('room-user-line')).toHaveTextContent('third')
    })

    it('swaps the provisional turn for the real one when the accepted message arrives', () => {
      const { rerender } = render(
        <RoomTranscript messages={[]} turnStarts={[]} keyPrefix="k" showThinking={false} showEmptyHint={false}>
          <RoomUserLine text="hello" pending />
        </RoomTranscript>,
      )
      expect(screen.getAllByTestId('room-turn')).toHaveLength(1)
      expect(screen.getByTestId('room-turn')).toHaveAttribute('data-turn-index', '0')
      // message_accepted: the durable line lands, the boundary is recorded,
      // pendingLocal clears.
      rerender(
        <RoomTranscript messages={[said('hello')]} turnStarts={[0]} keyPrefix="k" showThinking={false} showEmptyHint={false}>
          {null}
        </RoomTranscript>,
      )
      const turns = screen.getAllByTestId('room-turn')
      expect(turns).toHaveLength(1)
      expect(turns[0]).toHaveAttribute('data-turn-index', '0')
      expect(screen.getAllByTestId('room-user-line')).toHaveLength(1)
    })

    it('opens no provisional turn for an empty children slot', () => {
      render(
        <RoomTranscript messages={twoTurns} turnStarts={[0, 2]} keyPrefix="k" showThinking={false} showEmptyHint={false}>
          {false}
        </RoomTranscript>,
      )
      expect(screen.getAllByTestId('room-turn')).toHaveLength(2)
    })

    it('keeps an empty recorded turn as its own container', () => {
      // A boundary whose payload had no text appends nothing: the daemon still
      // declared a turn there (event-reducer markTurnStart).
      render(T({ messages: [said('one'), assistantText], turnStarts: [0, 2, 2] }))
      expect(screen.getAllByTestId('room-turn')).toHaveLength(3)
    })
  })

  describe('RoomUserLine', () => {
    it('a pending line has the same gutter mark, dimmed', () => {
      render(<RoomUserLine text="sending" pending><span data-testid="aside">queued</span></RoomUserLine>)
      const line = screen.getByTestId('room-user-line')
      expect(line.className).toContain('opacity-60')
      expect(within(line).getByTestId('room-user-mark').className).toContain('bg-accent')
      expect(within(line).getByTestId('aside')).toBeInTheDocument()
    })

    it('a durable line is not dimmed', () => {
      render(<RoomUserLine text="sent" />)
      expect(screen.getByTestId('room-user-line').className).not.toContain('opacity-60')
    })
  })

  // ---- P-B2.2 spec §4.4 R1 / R2 / R4 ------------------------------------

  const pb = (index: number, over: Partial<PartialBlock> & { type: PartialBlock['type'] }): PartialBlock =>
    ({ index, text: '', thinking: '', partialJson: '', ...over })
  const assembly = (...blocks: PartialBlock[]): PartialAssembly =>
    ({ messageId: 'm', finalized: 0, blocks: Object.fromEntries(blocks.map((b) => [b.index, b])) })
  const durableTool: StreamMessage = { type: 'assistant', message: { id: 'm0', role: 'assistant', content: [{ type: 'tool_use', id: 'tu1', name: 'Bash', input: { command: 'ls' } }], stop_reason: null } } as StreamMessage
  const running: ToolActivity = { name: 'Bash', startedAt: 1_000, endedAt: null, status: 'running' }

  describe('partial group placement (R1)', () => {
    it('R1: renders the partial group after the durable messages and before children', () => {
      render(
        <RoomTranscript messages={[assistantText]} keyPrefix="k" showThinking={false} showEmptyHint={false}
          partial={assembly(pb(0, { type: 'text', text: 'streaming…' }))}>
          <div data-testid="child">child</div>
        </RoomTranscript>,
      )
      const durable = screen.getByText('Hi there')
      const group = screen.getByTestId('partial-group')
      const child = screen.getByTestId('child')
      expect(within(group).getByText('streaming…')).toBeInTheDocument()
      expect(follows(durable, group)).toBe(true)
      expect(follows(group, child)).toBe(true)
    })

    it('R1: the partial group sits inside the last recorded turn', () => {
      render(T({ messages: [said('one'), assistantText, said('two')], turnStarts: [0, 2],
        partial: assembly(pb(0, { type: 'text', text: 'streaming…' })) }))
      const turns = screen.getAllByTestId('room-turn')
      expect(turns).toHaveLength(2)
      expect(within(turns[1]).getByTestId('partial-group')).toBeInTheDocument()
    })

    it('R1: a partial with no message yet still sits inside a turn', () => {
      render(T({ messages: [], partial: assembly(pb(0, { type: 'text', text: 'streaming…' })) }))
      expect(within(screen.getByTestId('room-turn')).getByTestId('partial-group')).toBeInTheDocument()
    })

    it('no partial (null or no blocks) → no partial group', () => {
      const { rerender } = render(T({ messages: [assistantText], partial: null }))
      expect(screen.queryByTestId('partial-group')).not.toBeInTheDocument()
      rerender(T({ messages: [assistantText], partial: assembly() }))
      expect(screen.queryByTestId('partial-group')).not.toBeInTheDocument()
    })
  })

  describe('durable tool activity (R2)', () => {
    it('R2: a durable tool_use with a running tools entry shows the spinner and the elapsed badge', () => {
      render(T({ messages: [durableTool], tools: { tu1: running }, now: 13_400 }))
      expect(screen.getByTestId('op-dot')).toHaveClass('animate-spin')
      expect(screen.getByTestId('op-elapsed')).toHaveTextContent('12.4s')
    })

    it('R2: a durable tool_use with a done tools entry shows the success dot and the duration badge', () => {
      render(T({ messages: [durableTool], tools: { tu1: { ...running, endedAt: 7_200, status: 'done' } }, now: 99_999 }))
      expect(screen.getByTestId('op-dot')).not.toHaveClass('animate-spin')
      expect(screen.getByTestId('op-dot').className).toContain('bg-status-success')
      expect(screen.getByTestId('op-duration')).toHaveTextContent('6.2s')
    })

    it('R2: a durable tool_use without a tools entry renders the plain pending block (no badge)', () => {
      render(T({ messages: [durableTool], tools: { other: running }, now: 13_400 }))
      expect(screen.getByTestId('operation-block')).toBeInTheDocument()
      expect(screen.getByTestId('op-dot')).not.toHaveClass('animate-spin')
      expect(screen.getByTestId('op-dot').className).toContain('bg-text-muted')
      expect(screen.queryByTestId('op-elapsed')).not.toBeInTheDocument()
      expect(screen.queryByTestId('op-duration')).not.toBeInTheDocument()
      expect(screen.queryByTestId('op-aborted')).not.toBeInTheDocument()
    })
  })

  // ---- T3.3: operations (spec §4.2) — one call ⊕ its result is one block ----

  describe('operations (T3.3)', () => {
    it('pairs a call with its result into one block', () => {
      render(T({ messages: [asst(use('tu1', 'Bash', { command: 'ls -la' })), usr(res('tu1', 'total 8'))] }))
      expect(screen.getAllByTestId('operation-block')).toHaveLength(1)
      const one = screen.getByTestId('operation-block')
      expect(within(one).getByTestId('op-name')).toHaveTextContent('Bash')
      expect(within(one).getByTestId('op-arg')).toHaveTextContent('ls -la')
      expect(within(one).getByTestId('fold-body')).toHaveTextContent('total 8')
      expect(screen.queryByTestId('tool-result-block')).toBeNull()
    })

    it('pairs a call with its result across a turn boundary', () => {
      // The pairing runs over the whole list, not per turn: a result that
      // lands after a boundary still belongs to the call that asked for it.
      render(T({ messages: [asst(use('tu1', 'Bash', { command: 'ls' })), said('next'), usr(res('tu1', 'late'))], turnStarts: [0, 1] }))
      expect(screen.getAllByTestId('operation-block')).toHaveLength(1)
      expect(within(screen.getByTestId('operation-block')).getByTestId('fold-body')).toHaveTextContent('late')
    })

    it('renders an orphan result on its own', () => {
      render(T({ messages: [usr(res('tu9', 'stray output'))] }))
      expect(screen.getAllByTestId('operation-block')).toHaveLength(1)
      expect(screen.getByTestId('op-name')).toHaveTextContent('tool')
      expect(screen.getByTestId('fold-body')).toHaveTextContent('stray output')
    })

    it('renders no argument for an orphan result', () => {
      // An orphan has no call, so there is no input to summarise: the
      // argument slot stays empty rather than printing a literal `{}`.
      render(T({ messages: [usr(res('tu9', 'stray output'))] }))
      expect(screen.queryByTestId('op-arg')).toBeNull()
      expect(screen.getByTestId('operation-block').textContent).not.toContain('{}')
    })

    it("remembers a block's expansion across a re-render", () => {
      const messages = [asst(use('tu1', 'Bash', { command: 'ls' })), usr(res('tu1', longBody))]
      const { rerender } = render(T({ messages }))
      fireEvent.click(screen.getByTestId('fold-more'))
      expect(screen.getByTestId('fold-less')).toBeInTheDocument()
      // A new keyPrefix re-keys every row (and every turn), so the blocks
      // unmount and remount: a useState inside the block would lose it here.
      rerender(T({ messages, keyPrefix: 'k2' }))
      expect(screen.getByTestId('fold-less')).toBeInTheDocument()
      expect(screen.queryByTestId('fold-more')).toBeNull()
    })

    it("remembers a thought's expansion across a re-render", () => {
      const long = Array.from({ length: 100 }, (_, i) => `t${i + 1}`).join('\n')
      const messages = [asst({ type: 'thinking', thinking: long })]
      const { rerender } = render(T({ messages }))
      fireEvent.click(screen.getByTestId('fold-more'))
      rerender(T({ messages, keyPrefix: 'k2' }))
      expect(screen.getByTestId('fold-less')).toBeInTheDocument()
    })

    it('renders each of two same-id calls with its own result', () => {
      render(T({
        messages: [
          asst(use('tu1', 'Bash', { command: 'first' }), use('tu1', 'Bash', { command: 'second' })),
          usr(res('tu1', 'ANSWER A'), res('tu1', 'ANSWER B')),
        ],
      }))
      const blocks = screen.getAllByTestId('operation-block')
      expect(blocks).toHaveLength(2)
      expect(within(blocks[0]).getByTestId('op-arg')).toHaveTextContent('first')
      expect(within(blocks[0]).getByTestId('fold-body')).toHaveTextContent('ANSWER A')
      expect(within(blocks[1]).getByTestId('op-arg')).toHaveTextContent('second')
      expect(within(blocks[1]).getByTestId('fold-body')).toHaveTextContent('ANSWER B')
      expect(blocks[0]).not.toHaveTextContent('ANSWER B')
      expect(blocks[1]).not.toHaveTextContent('ANSWER A')
    })

    it('two calls sharing a tool_use id expand independently', () => {
      // The fold key is the call's position (`blockKey(i, j)`), not its
      // tool_use id: ids repeat, and two calls under one id would then share
      // one expansion. Position is safe because `event-reducer.ts` only appends.
      render(T({
        messages: [
          asst(use('tu1', 'Bash', { command: 'first' }), use('tu1', 'Bash', { command: 'second' })),
          usr(res('tu1', `A\n${longBody}`), res('tu1', `B\n${longBody}`)),
        ],
      }))
      const blocks = screen.getAllByTestId('operation-block')
      expect(blocks).toHaveLength(2)
      expect(screen.getAllByTestId('fold-more')).toHaveLength(2)
      fireEvent.click(within(blocks[0]).getByTestId('fold-more'))
      expect(within(blocks[0]).getByTestId('fold-less')).toBeInTheDocument()
      expect(within(blocks[0]).queryByTestId('fold-more')).toBeNull()
      expect(within(blocks[1]).getByTestId('fold-more')).toBeInTheDocument()
      expect(within(blocks[1]).queryByTestId('fold-less')).toBeNull()
    })

    it('renders every result exactly once', () => {
      render(T({
        messages: [
          asst(use('tu1', 'Bash', { command: 'a' }), use('tu2', 'Read', { file_path: '/x' }), use('tu4', undefined, {})),
          // out of order, plus one result nothing called for
          usr(res('tu2', 'ANSWER 2'), res('tu1', 'ANSWER 1'), res('tu3', 'ORPHAN'), res('tu4', 'ANSWER 4')),
        ],
      }))
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
  // `tools[id]` reads `Object.prototype.constructor` for the id `constructor`.
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
      render(T({ messages: [asst(use('constructor', 'Bash', { command: 'ls' }))], tools: {}, now: 13_400 }))
      expect(screen.getByTestId('op-name')).toHaveTextContent('Bash')
      expect(screen.queryByTestId('tool-diff')).toBeNull()
      expect(screen.queryByTestId('op-elapsed')).toBeNull()
      expect(screen.queryByTestId('op-duration')).toBeNull()
      expect(screen.queryByTestId('op-aborted')).toBeNull()
    })

    it('a `constructor` entry on the prototype is ignored; the same entry as an own key is found', () => {
      const msgs = [asst(use('constructor', 'Edit', { file_path: '/x' })), usr(res('constructor', 'edited'))]
      const inherited = Object.create({ constructor: diffEntry }) as Record<string, ToolActivity>
      const { unmount } = render(T({ messages: msgs, tools: inherited, now: 13_400 }))
      expect(screen.getByTestId('operation-block')).toBeInTheDocument()
      expect(screen.queryByTestId('tool-diff')).toBeNull()
      unmount()
      // The positive control: without it "no diff" would pass against anything.
      render(T({ messages: msgs, tools: { constructor: diffEntry }, now: 13_400 }))
      expect(screen.getByTestId('tool-diff')).toBeInTheDocument()
    })

    it('an entry reachable only through the prototype chain is ignored', () => {
      const inherited = Object.create({ tu1: running }) as Record<string, ToolActivity>
      const { unmount } = render(T({ messages: [durableTool], tools: inherited, now: 13_400 }))
      expect(screen.getByTestId('op-dot')).not.toHaveClass('animate-spin')
      expect(screen.queryByTestId('op-elapsed')).toBeNull()
      unmount()
      render(T({ messages: [durableTool], tools: { tu1: running }, now: 13_400 }))
      expect(screen.getByTestId('op-elapsed')).toHaveTextContent('12.4s')
    })

    it('an orphan result does not take its name from a prototype entry', () => {
      const inherited = Object.create({ tu9: fileEntry }) as Record<string, ToolActivity>
      const { unmount } = render(T({ messages: [resultFrame('tu9')], tools: inherited }))
      expect(screen.getByTestId('op-name')).toHaveTextContent('tool')
      unmount()
      render(T({ messages: [resultFrame('tu9')], tools: { tu9: fileEntry } }))
      expect(screen.getByTestId('op-name')).toHaveTextContent('/srv/x.ts')
    })

    it('tools undefined → the orphan result still renders, with no facts', () => {
      render(T({ messages: [resultFrame('tu1')] }))
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
      const { rerender } = render(T({ messages, partial: assembly(pb(0, { type: 'text', text: 'he' })) }))
      expect(scrollTo).toHaveBeenCalledTimes(1)
      rerender(T({ messages, partial: assembly(pb(0, { type: 'text', text: 'hello' })) }))
      expect(scrollTo).toHaveBeenCalledTimes(2)
      // Same content, new object identity → no extra scroll: the length
      // counter, not the assembly's identity, is the effect dep.
      rerender(T({ messages, partial: assembly(pb(0, { type: 'text', text: 'hello' })) }))
      expect(scrollTo).toHaveBeenCalledTimes(2)
    })

    it('R4: a started tool_use with empty input (no delta yet) scrolls when its row appears', () => {
      Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
      const messages = [assistantText]
      const { rerender } = render(T({ messages, partial: assembly(pb(0, { type: 'text', text: 'hello' })) }))
      expect(scrollTo).toHaveBeenCalledTimes(1)
      rerender(T({ messages, partial: assembly(pb(0, { type: 'text', text: 'hello' }), pb(1, { type: 'tool_use', toolName: 'Bash' })) }))
      expect(scrollTo).toHaveBeenCalledTimes(2)
    })

    it('R4: the pending line scrolls via scrollKey', () => {
      Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
      const messages = [assistantText]
      const { rerender } = render(T({ messages, scrollKey: 0 }))
      expect(scrollTo).toHaveBeenCalledTimes(1)
      rerender(T({ messages, scrollKey: 1 }))
      expect(scrollTo).toHaveBeenCalledTimes(2)
    })
  })
})
