// spa/src/components/room/RoomThinking.test.tsx — spec §4.3: thinking is an
// optional element. With no text it draws nothing; with text it folds by the
// same rule as every other block (codex plan review #4), and its expansion
// lives in the pane's fold memory, not in the block.
import type { ReactNode } from 'react'
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, within } from '@testing-library/react'
import RoomThinking from './RoomThinking'
import OperationBlock from './OperationBlock'
import { FoldContext, useFoldMemory } from './fold-context'

beforeEach(() => { cleanup() })

function Pane({ children }: { children: ReactNode }) {
  const store = useFoldMemory()
  return <FoldContext.Provider value={store}>{children}</FoldContext.Provider>
}

const lines = (n: number): string => Array.from({ length: n }, (_, i) => `thought ${i + 1}`).join('\n')

describe('RoomThinking', () => {
  it('renders nothing for an empty thought', () => {
    const { container } = render(<Pane><RoomThinking content="" foldKey="k" /></Pane>)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders nothing for a whitespace-only thought', () => {
    const { container } = render(<Pane><RoomThinking content={' \n\t '} foldKey="k" /></Pane>)
    expect(container).toBeEmptyDOMElement()
  })

  it('heads the block with its word count', () => {
    render(<Pane><RoomThinking content="Let me analyze   this problem." foldKey="k" /></Pane>)
    expect(screen.getByTestId('thinking-header')).toHaveTextContent('Thought · 5 words')
  })

  it('shows a short thought whole, with no affordance', () => {
    render(<Pane><RoomThinking content={'first line\nsecond line'} foldKey="k" /></Pane>)
    const block = screen.getByTestId('room-thinking')
    expect(within(block).getByTestId('fold-body')).toHaveTextContent('first line')
    expect(within(block).getByTestId('fold-body')).toHaveTextContent('second line')
    expect(within(block).queryByTestId('fold-more')).toBeNull()
    expect(within(block).queryByTestId('fold-less')).toBeNull()
  })

  it('folds a medium thought to six lines', () => {
    render(<Pane><RoomThinking content={lines(20)} foldKey="k" /></Pane>)
    const body = screen.getByTestId('fold-body')
    expect(body.textContent?.split('\n')).toHaveLength(6)
    expect(screen.getByTestId('fold-more')).toHaveTextContent('+14 lines')
  })

  it('folds a long thought to three lines', () => {
    render(<Pane><RoomThinking content={lines(100)} foldKey="k" /></Pane>)
    const body = screen.getByTestId('fold-body')
    expect(body.textContent?.split('\n')).toEqual(['thought 1', 'thought 2', 'thought 3'])
    expect(screen.getByTestId('fold-more')).toHaveTextContent('+97 lines')
  })

  it('expands and collapses through the affordance', () => {
    render(<Pane><RoomThinking content={lines(100)} foldKey="k" /></Pane>)
    fireEvent.click(screen.getByTestId('fold-more'))
    expect(screen.getByTestId('fold-body')).toHaveTextContent('thought 100')
    fireEvent.click(screen.getByTestId('fold-less'))
    expect(screen.getByTestId('fold-body')).not.toHaveTextContent('thought 100')
  })

  it('keeps its expansion across a remount (pane memory, not local state)', () => {
    const { rerender } = render(<Pane><RoomThinking key="a" content={lines(100)} foldKey="k" /></Pane>)
    fireEvent.click(screen.getByTestId('fold-more'))
    // A new React key unmounts and remounts the block; a useState would reset.
    rerender(<Pane><RoomThinking key="b" content={lines(100)} foldKey="k" /></Pane>)
    expect(screen.getByTestId('fold-less')).toBeInTheDocument()
  })

  it('does not share its expansion with an operation under the same fold key', () => {
    // The thinking fold is `${foldKey}:thinking`; the bare key belongs to an
    // operation's output and must not open the thought, nor the other way round.
    render(
      <Pane>
        <RoomThinking content={lines(100)} foldKey="k" />
        <OperationBlock tool="Bash" input={{ command: 'ls' }} foldKey="k"
          activity={{ status: 'done', startedAt: 0, endedAt: 0 }}
          result={{ text: lines(100), isError: false }} />
      </Pane>,
    )
    const thought = screen.getByTestId('room-thinking')
    const op = screen.getByTestId('operation-block')
    fireEvent.click(within(thought).getByTestId('fold-more'))
    expect(within(thought).getByTestId('fold-less')).toBeInTheDocument()
    expect(within(op).getByTestId('fold-more')).toBeInTheDocument()
  })
})

// P-B2.2 task 8 (spec §4.4 R1): the cursor sits in the header, so it is seen
// even while the body is folded, and again at the end of the body whenever
// the body's end is on screen.
describe('RoomThinking streaming cursor (R1)', () => {
  it('a short streaming thought shows the cursor after the label and at the end of the body', () => {
    render(<Pane><RoomThinking content="thinking..." foldKey="k" streaming /></Pane>)
    const cursors = screen.getAllByTestId('stream-cursor')
    expect(cursors).toHaveLength(2)
    const header = screen.getByTestId('thinking-header')
    expect(header.contains(cursors[0])).toBe(true)
    expect(screen.getByTestId('thinking-label').nextElementSibling).toBe(cursors[0])
    // Inline at the end of the text, as the typewriter has always drawn it —
    // not on a line of its own under the body.
    const body = screen.getByTestId('fold-body')
    expect(body.lastElementChild).toBe(cursors[1])
    expect(body.textContent).toBe('thinking...▌')
  })

  it('a folded streaming thought shows only the header cursor', () => {
    render(<Pane><RoomThinking content={lines(100)} foldKey="k" streaming /></Pane>)
    const cursors = screen.getAllByTestId('stream-cursor')
    expect(cursors).toHaveLength(1)
    expect(screen.getByTestId('thinking-header').contains(cursors[0])).toBe(true)
  })

  it('an expanded streaming thought shows the cursor at the end of the body again', () => {
    render(<Pane><RoomThinking content={lines(100)} foldKey="k" streaming /></Pane>)
    fireEvent.click(screen.getByTestId('fold-more'))
    const cursors = screen.getAllByTestId('stream-cursor')
    expect(cursors).toHaveLength(2)
    expect(screen.getByTestId('fold-body').lastElementChild).toBe(cursors[1])
  })

  it('without streaming renders no cursor', () => {
    render(<Pane><RoomThinking content="thinking..." foldKey="k" /></Pane>)
    expect(screen.queryByTestId('stream-cursor')).toBeNull()
  })
})
