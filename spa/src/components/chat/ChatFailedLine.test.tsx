// spa/src/components/chat/ChatFailedLine.test.tsx — a failed tool is never
// hidden: one red line, expanding into the room's block (spec §5, R2 plan T2.1).
import type { ReactElement, ReactNode } from 'react'
import { describe, it, expect, beforeEach } from 'vitest'
import { render as rtlRender, screen, cleanup, fireEvent } from '@testing-library/react'
import ChatFailedLine from './ChatFailedLine'
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
const block = () => <div data-testid="op">the room block</div>

describe('ChatFailedLine', () => {
  it('is one red line: the name and the first line of the error', () => {
    render(<ChatFailedLine foldKey="2:0" name="Bash" message={'\n  exit 1: no such file\nstack…'} renderOperation={block} />)
    const line = screen.getByTestId('chat-failed-line')
    expect(line).toHaveTextContent('Bash · exit 1: no such file')
    expect(line).not.toHaveTextContent('stack')
    expect(line.className).toContain('text-status-error')
    expect(line.querySelector('svg')).not.toBeNull()
    expect(line).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByTestId('op')).toBeNull()
  })

  it('says only the name when there is no message', () => {
    render(<ChatFailedLine foldKey="2:0" name="Write" message="" renderOperation={block} />)
    expect(screen.getByTestId('chat-failed-line').textContent?.trim()).toBe('Write')
  })

  it('expands into the room block, and folds with its turn', () => {
    render(<ChatFailedLine foldKey="2:0" name="Bash" message="boom" renderOperation={block} />)
    fireEvent.click(screen.getByTestId('chat-failed-line'))
    expect(screen.getByTestId('op')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('chat-failed-line'))
    expect(screen.queryByTestId('op')).toBeNull()
    fireEvent.click(screen.getByTestId('expand-turn-0'))
    expect(screen.getByTestId('op')).toBeInTheDocument()
  })
})
