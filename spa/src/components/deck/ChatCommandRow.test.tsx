// A bash-mode command in the chat reads as a step row on the left (not a speech bubble); the deck keeps its block.
import { describe, it, expect, afterEach } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { UserBubble } from './ChatBubbles'
import { DeckItem } from './DeckItem'
import { formatClock } from '../../lib/conversations/deck-format'
import { userUnitId } from '../../lib/conversations/find-units'
import { FoldContext } from '../room/fold-context'
import { forgetFolds, usePaneFoldStore } from '../../lib/conversations/fold-memory'
import type { UserItem } from '../../lib/conversations/types'

const PANE = 'chat-cmd-test'
function Folds({ children }: { children: React.ReactNode }) {
  return <FoldContext.Provider value={usePaneFoldStore(PANE)}>{children}</FoldContext.Provider>
}
afterEach(() => { cleanup(); forgetFolds(PANE) })
const bash = { type: 'user', id: 'b1', at: 1_700_000_000_000, index: 0, source: 'bash', text: 'ls -la' } as unknown as UserItem
const out = { type: 'user', id: 'o1', at: 1_700_000_001_000, index: 1, source: 'command_output', text: 'total 0\nfoo' } as unknown as UserItem

describe('chat bash command row', () => {
  it('is an unframed, untinted, left row: icon, mono "! cmd", time', () => {
    render(<UserBubble item={bash} />)
    const row = screen.getByTestId('chat-command')
    expect(row.className).not.toMatch(/ring-|border|bg-|rounded/)
    expect(row.querySelector('svg')).not.toBeNull()
    const text = screen.getByText('! ls -la')
    expect(text.closest('[data-search-unit]')?.getAttribute('data-search-unit')).toBe(userUnitId('b1'))
    expect(text.closest('.font-mono')).not.toBeNull()
    expect(row.textContent).toContain(formatClock(bash.at))
    expect(screen.queryByTestId('deck-user')).toBeNull()
    expect(screen.queryByTestId('chat-user-bubble')).toBeNull()
  })

  it('keeps the output under the command as the folded output block', () => {
    render(<Folds><UserBubble item={out} /></Folds>)
    expect(screen.getByTestId('deck-user').getAttribute('data-source')).toBe('command_output')
    fireEvent.click(screen.getByTestId('output-toggle'))
    expect(screen.getByTestId('output-toggle').getAttribute('aria-expanded')).toBe('true')
  })

  it('deck mode still draws the UserBlock box', () => {
    render(<DeckItem item={bash} />)
    expect(screen.getByTestId('deck-user').getAttribute('data-tone')).toBe('you')
    expect(screen.queryByTestId('chat-command')).toBeNull()
  })
})
