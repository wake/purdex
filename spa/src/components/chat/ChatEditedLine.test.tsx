// spa/src/components/chat/ChatEditedLine.test.tsx — an edit as one line with
// its stat, expanding into the room's diff view (spec §5, R2 plan T2.1).
import type { ReactElement, ReactNode } from 'react'
import { describe, it, expect, beforeEach } from 'vitest'
import { render as rtlRender, screen, cleanup, fireEvent } from '@testing-library/react'
import ChatEditedLine from './ChatEditedLine'
import { FoldContext, TurnIndexContext, useFoldMemory } from '../room/fold-context'
import type { DiffHunk } from '../../lib/nex/tool-activity'

beforeEach(() => { cleanup() })

/** The pane's fold memory, with a stand-in for turn 0's expand-all (chat draws no strip). */
function Harness({ children }: { children: ReactNode }) {
  const store = useFoldMemory()
  return (
    <FoldContext.Provider value={store}>
      <button type="button" data-testid="expand-turn-0" onClick={() => store.setTurn(0, true)} />
      <TurnIndexContext.Provider value={0}>{children}</TurnIndexContext.Provider>
    </FoldContext.Provider>
  )
}
const render = (ui: ReactElement) => rtlRender(<Harness>{ui}</Harness>)

const hunk: DiffHunk = { oldStart: 1, oldLines: 1, newStart: 1, newLines: 4, lines: [' a', '+b', '+c', '+d'] }
const diff = { path: '/work/docs/notes.md', added: 3, removed: 0, hunks: [hunk], truncated: false }

describe('ChatEditedLine', () => {
  it('names the file by its basename, with the stat and a U+2212 minus', () => {
    render(<ChatEditedLine foldKey="3:1" diff={diff} />)
    const line = screen.getByTestId('chat-edited-line')
    expect(line).toHaveTextContent('Edited notes.md (+3 −0)')
    expect(line).not.toHaveTextContent('/work/docs')
    expect(line.querySelector('svg')).not.toBeNull()
    expect(line).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByTestId('tool-diff')).toBeNull()
  })

  it('expands into the same diff view the room draws, and folds with its turn', () => {
    render(<ChatEditedLine foldKey="3:1" diff={diff} />)
    fireEvent.click(screen.getByTestId('chat-edited-line'))
    expect(screen.getByTestId('tool-diff')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('chat-edited-line'))
    expect(screen.queryByTestId('tool-diff')).toBeNull()
    fireEvent.click(screen.getByTestId('expand-turn-0'))
    expect(screen.getByTestId('tool-diff')).toBeInTheDocument()
  })
})
