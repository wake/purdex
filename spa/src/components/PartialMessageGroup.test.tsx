// spa/src/components/PartialMessageGroup.test.tsx — the trailing in-flight
// assistant group (P-B2 spec §4.4 R1). These guards lived in
// ConversationMessages.test's `partial group (R1)` describe and move here with
// that file's deletion (T4.4); the group's placement inside the transcript is
// asserted in room/RoomTranscript.test.
import type { ReactNode } from 'react'
import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import PartialMessageGroup from './PartialMessageGroup'
import { FoldContext, useFoldMemory } from './room/fold-context'
import type { PartialAssembly, PartialBlock } from '../lib/nex/partial'

/** The pane's fold memory: the room blocks read it, as they do in RoomTranscript. */
function Pane({ children }: { children: ReactNode }) {
  const store = useFoldMemory()
  return <FoldContext.Provider value={store}>{children}</FoldContext.Provider>
}

const pb = (index: number, over: Partial<PartialBlock> & { type: PartialBlock['type'] }): PartialBlock =>
  ({ index, text: '', thinking: '', partialJson: '', ...over })
const assembly = (...blocks: PartialBlock[]): PartialAssembly =>
  ({ messageId: 'm', finalized: 0, blocks: Object.fromEntries(blocks.map((b) => [b.index, b])) })
const follows = (a: Element, b: Element) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)

const renderGroup = (partial: PartialAssembly) =>
  render(<Pane><PartialMessageGroup partial={partial} /></Pane>)

describe('PartialMessageGroup (R1)', () => {
  it('renders a streaming text block with the cursor', () => {
    renderGroup(assembly(pb(0, { type: 'text', text: 'hello' })))
    const group = within(screen.getByTestId('partial-group'))
    const prose = group.getByTestId('room-prose')
    expect(prose).toHaveTextContent('hello')
    expect(within(prose).getByTestId('stream-cursor')).toBeInTheDocument()
    expect(group.queryByTestId('room-thinking')).not.toBeInTheDocument()
  })

  it('renders a streaming thinking block with the cursor', () => {
    renderGroup(assembly(pb(0, { type: 'thinking', thinking: 'pondering' })))
    const group = within(screen.getByTestId('partial-group'))
    const thought = group.getByTestId('room-thinking')
    expect(within(thought).getByTestId('thinking-header')).toBeInTheDocument()
    expect(within(within(thought).getByTestId('thinking-header')).getByTestId('stream-cursor')).toBeInTheDocument()
    expect(group.queryByTestId('room-prose')).not.toBeInTheDocument()
  })

  it('renders blocks in ascending index order', () => {
    // Inserted as 2,0,1.
    const partial: PartialAssembly = { messageId: 'm', finalized: 0, blocks: {} }
    partial.blocks[2] = pb(2, { type: 'text', text: 'two' })
    partial.blocks[0] = pb(0, { type: 'text', text: 'zero' })
    partial.blocks[1] = pb(1, { type: 'text', text: 'one' })
    renderGroup(partial)
    const group = within(screen.getByTestId('partial-group'))
    const [zero, one, two] = ['zero', 'one', 'two'].map((x) => group.getByText(x))
    expect(follows(zero, one)).toBe(true)
    expect(follows(one, two)).toBe(true)
  })

  it('renders nothing for an invisible block', () => {
    // Empty text, whitespace-only text and thinking: the same predicate as
    // partialHasVisibleContent, so the ThinkingIndicator and the group agree.
    renderGroup(assembly(
      pb(0, { type: 'text', text: '' }),
      pb(1, { type: 'text', text: ' \n\t ' }),
      pb(2, { type: 'thinking', thinking: '  ' }),
    ))
    expect(screen.getByTestId('partial-group')).toBeEmptyDOMElement()
    expect(screen.queryByTestId('stream-cursor')).not.toBeInTheDocument()
  })

  it('renders nothing for an unknown block', () => {
    renderGroup(assembly(pb(0, { type: 'unknown', text: 'x', thinking: 'y', partialJson: 'z' })))
    expect(screen.getByTestId('partial-group')).toBeEmptyDOMElement()
  })

  it('a started tool_use with empty partialJson renders the spinner row with only the tool name', () => {
    renderGroup(assembly(pb(0, { type: 'tool_use', toolName: 'Bash' })))
    const group = within(screen.getByTestId('partial-group'))
    expect(group.getByTestId('op-dot')).toHaveClass('animate-spin')
    expect(group.getByTestId('op-name')).toHaveTextContent(/^Bash$/)
    expect(group.getByTestId('op-arg-pending')).toBeInTheDocument()
    expect(group.queryByTestId('op-rail')).toBeNull()
  })

  it('a snapshot-seeded tool_use block without toolName shows the placeholder, never the half-assembled JSON', () => {
    renderGroup(assembly(pb(0, { type: 'tool_use', partialJson: '{"command":"ls' })))
    const group = screen.getByTestId('partial-group')
    expect(within(group).getByTestId('op-name')).toHaveTextContent('tool')
    // spec §3.1.1 #7: the placeholder, not the raw prefix.
    expect(group).not.toHaveTextContent('{"command":"ls')
    expect(within(group).getByTestId('op-arg-pending')).toBeInTheDocument()
    expect(within(group).getByTestId('op-dot')).toHaveClass('animate-spin')
  })

  it('a tool_use block with toolName shows that name as streaming', () => {
    renderGroup(assembly(pb(0, { type: 'tool_use', toolName: 'Read', partialJson: '{"file' })))
    const group = within(screen.getByTestId('partial-group'))
    expect(group.getByTestId('op-name')).toHaveTextContent('Read')
    expect(group.getByTestId('op-dot')).toHaveClass('animate-spin')
  })
})
