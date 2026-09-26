// spa/src/components/chat/ChatToolsLine.test.tsx — a turn's tools as one
// quiet line (spec §5, R2 plan T2.1).
import type { ReactElement, ReactNode } from 'react'
import { describe, it, expect, beforeEach } from 'vitest'
import { render as rtlRender, screen, cleanup, fireEvent } from '@testing-library/react'
import ChatToolsLine from './ChatToolsLine'
import { FoldContext, TurnIndexContext, useFoldMemory } from '../room/fold-context'

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
const ops = () => <div data-testid="ops">the room blocks</div>

describe('ChatToolsLine', () => {
  it('says how many tools were used, with the wrench, folded', () => {
    render(<ChatToolsLine foldKey="k-turn-0:chat-tools" count={5} running={false} renderOperations={ops} />)
    const line = screen.getByTestId('chat-tools-line')
    expect(line).toHaveTextContent('Used 5 tools')
    expect(line).toHaveAttribute('aria-expanded', 'false')
    expect(line.querySelector('svg')).not.toBeNull()
    expect(line.className).toContain('text-text-muted')
    expect(screen.queryByTestId('ops')).toBeNull()
  })

  it('says one tool in the singular', () => {
    render(<ChatToolsLine foldKey="k" count={1} running={false} renderOperations={ops} />)
    expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Used 1 tool')
    expect(screen.getByTestId('chat-tools-line')).not.toHaveTextContent('tools')
  })

  it('says the tools are running while one is', () => {
    render(<ChatToolsLine foldKey="k" count={3} running renderOperations={ops} />)
    expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Using 3 tools…')
  })

  it('expands in place and registers with its turn', () => {
    render(<ChatToolsLine foldKey="k-turn-0:chat-tools" count={2} running={false} renderOperations={ops} />)
    fireEvent.click(screen.getByTestId('chat-tools-line'))
    expect(screen.getByTestId('ops')).toBeInTheDocument()
    expect(screen.getByTestId('chat-tools-line')).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(screen.getByTestId('chat-tools-line'))
    expect(screen.queryByTestId('ops')).toBeNull()
    // Expand-all of turn 0 reaches it.
    fireEvent.click(screen.getByTestId('expand-turn-0'))
    expect(screen.getByTestId('ops')).toBeInTheDocument()
  })
})
