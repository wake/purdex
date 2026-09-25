// spa/src/components/room/RoomTurnGroup.test.tsx — a turn is a container with
// no edge of its own (spec Q1), and its hover strip reaches every foldable
// thing inside it through the registry, not through a key list (spec §3.2).
import type { ReactNode } from 'react'
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, within } from '@testing-library/react'
import RoomTurnGroup from './RoomTurnGroup'
import OperationBlock from './OperationBlock'
import RoomThinking from './RoomThinking'
import { FoldContext, useFoldMemory } from './fold-context'
import type { DiffHunk } from '../../lib/nex/tool-activity'
import type { ToolResultFacts } from '../../lib/nex/tool-result-facts'

beforeEach(() => { cleanup() })

/** The pane's fold memory, shared by every turn in it. */
function Pane({ children }: { children: ReactNode }) {
  const store = useFoldMemory()
  return <FoldContext.Provider value={store}>{children}</FoldContext.Provider>
}

const body = (n: number): string => Array.from({ length: n }, (_, i) => `line ${i + 1}`).join('\n')

// Ten rows: past the ladder's whole-block limit, so the diff folds.
const hunk: DiffHunk = {
  oldStart: 1, oldLines: 10, newStart: 1, newLines: 10,
  lines: Array.from({ length: 10 }, (_, i) => `+row ${i + 1}`),
}
const diffFacts: ToolResultFacts = { diff: { path: '/x', added: 10, removed: 0, hunks: [hunk], truncated: false } }

/** An operation with a folded output, a folded diff, and a thinking block. */
function TurnBody({ id }: { id: string }) {
  return (
    <>
      {/* Long enough to fold: a short thought is shown whole and has nothing to expand. */}
      <RoomThinking content={body(20)} foldKey={id} />
      <OperationBlock tool="Edit" input={{ file_path: '/x' }} foldKey={id}
        activity={{ status: 'done', startedAt: 0, endedAt: 0 }}
        facts={diffFacts} result={{ text: body(20), isError: false }} />
    </>
  )
}

function twoTurns() {
  render(
    <Pane>
      <RoomTurnGroup index={0}><TurnBody id="a" /></RoomTurnGroup>
      <RoomTurnGroup index={1}><TurnBody id="b" /></RoomTurnGroup>
    </Pane>,
  )
  const [first, second] = screen.getAllByTestId('room-turn')
  return { first, second }
}

/** All three of the turn's folds, read off what each one draws. */
function folds(turn: HTMLElement) {
  // The output and the thought draw the same affordance, so each is read
  // inside its own block.
  const op = within(within(turn).getByTestId('operation-block'))
  const thought = within(within(turn).getByTestId('room-thinking'))
  const state = (t: typeof op, less: string, more: string) =>
    t.queryByTestId(less) ? 'open' : t.queryByTestId(more) ? 'closed' : 'missing'
  return {
    output: state(op, 'fold-less', 'fold-more'),
    diff: state(op, 'diff-less', 'diff-more'),
    thinking: state(thought, 'fold-less', 'fold-more'),
  }
}

const ALL_OPEN = { output: 'open', diff: 'open', thinking: 'open' }
const ALL_CLOSED = { output: 'closed', diff: 'closed', thinking: 'closed' }

describe('RoomTurnGroup', () => {
  it('marks the section with its turn index', () => {
    const { first, second } = twoTurns()
    expect(first.tagName).toBe('SECTION')
    expect(first).toHaveAttribute('data-turn-index', '0')
    expect(second).toHaveAttribute('data-turn-index', '1')
  })

  it('draws no separator', () => {
    const { first } = twoTurns()
    expect(first.className).not.toContain('border')
    expect(first.className).not.toContain('divide')
    expect(first.querySelector('hr')).toBeNull()
  })

  it('shows no per-turn cost or duration', () => {
    render(<Pane><RoomTurnGroup index={0}><p>hi</p></RoomTurnGroup></Pane>)
    const turn = screen.getByTestId('room-turn')
    // Only the child and the two strip labels: no seconds, no dollars, no tokens.
    expect(turn.textContent).not.toMatch(/\$|\d+(\.\d+)?\s*(ms|s|m)\b|token/i)
    expect(within(turn).queryByTestId('turn-duration')).toBeNull()
    expect(within(turn).queryByTestId('turn-cost')).toBeNull()
  })

  it('hides the strip until the turn is hovered', () => {
    const { first } = twoTurns()
    expect(first.className).toContain('group')
    const strip = within(first).getByTestId('turn-fold-strip')
    expect(strip.className).toContain('opacity-0')
    expect(strip.className).toContain('group-hover:opacity-100')
  })

  it('expand all opens an operation, its diff and a thinking block inside the turn', () => {
    const { first } = twoTurns()
    expect(folds(first)).toEqual(ALL_CLOSED)
    fireEvent.click(within(first).getByTestId('turn-expand-all'))
    expect(folds(first)).toEqual(ALL_OPEN)
  })

  it('collapse all closes them', () => {
    const { first } = twoTurns()
    fireEvent.click(within(first).getByTestId('turn-expand-all'))
    expect(folds(first)).toEqual(ALL_OPEN)
    fireEvent.click(within(first).getByTestId('turn-collapse-all'))
    expect(folds(first)).toEqual(ALL_CLOSED)
  })

  it('leaves a neighbouring turn untouched', () => {
    const { first, second } = twoTurns()
    fireEvent.click(within(first).getByTestId('turn-expand-all'))
    expect(folds(second)).toEqual(ALL_CLOSED)

    fireEvent.click(within(second).getByTestId('turn-expand-all'))
    fireEvent.click(within(first).getByTestId('turn-collapse-all'))
    expect(folds(first)).toEqual(ALL_CLOSED)
    expect(folds(second)).toEqual(ALL_OPEN)
  })
})
