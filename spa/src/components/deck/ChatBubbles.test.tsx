// The agent bubble's markdown (#2461): RoomProse's list indent, code face and table border read the pane's --wt-* vars, which
// only the execution pane root sets — the chat bubble supplies the worker theme itself, and drops prose's backtick pseudo
// content around inline code. jsdom has no layout, so this pins the contract (vars on the bubble, scoping class), and the
// real-Chromium numbers are in the PR.
import { describe, it, expect, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { AgentBubble } from './ChatBubbles'
import { DeckItem } from './DeckItem'
import { getWorkerTheme, workerThemeStyle } from '../../lib/worker-theme/registry'
import type { AgentTextItem } from '../../lib/conversations/types'

afterEach(cleanup)
const md = '看 `fold-memory.ts`：\n\n- 第一項\n- 第二項\n'
const item = { type: 'agent_text', id: 'a1', at: 1, index: 0, markdown: md } as unknown as AgentTextItem

describe('agent bubble markdown', () => {
  it('renders the list and inline code as elements (no backtick characters in the text)', () => {
    render(<AgentBubble item={item} />)
    const bubble = screen.getByTestId('chat-agent')
    expect(bubble.querySelectorAll('ul > li')).toHaveLength(2)
    const code = bubble.querySelector('p code')!
    expect(code.textContent).toBe('fold-memory.ts')
    expect(bubble.textContent).not.toContain('`')
  })

  it('carries the worker theme vars (list indent, code font) and the bubble-markdown scope class', () => {
    render(<AgentBubble item={item} />)
    const scope = screen.getByTestId('chat-agent-md')
    expect(scope).toHaveClass('chat-md')
    // #2463: the vars now come from RoomProse itself, not from this wrapper.
    expect(scope.getAttribute('style')).toBeNull()
    const vars = workerThemeStyle(getWorkerTheme(undefined))
    const prose = scope.querySelector<HTMLElement>('[data-testid="room-prose"]')!
    expect(prose.style.getPropertyValue('--wt-list-indent')).toBe(vars['--wt-list-indent'])
    expect(prose.style.getPropertyValue('--wt-code-font')).toBe(vars['--wt-code-font'])
  })

  it('leaves the deck (another RoomProse caller) without the bubble scope, yet with the theme vars on its RoomProse (#2463)', () => {
    render(<DeckItem item={item} />)
    const deck = screen.getByTestId('deck-agent-text')
    expect(deck.querySelector('.chat-md')).toBeNull()
    expect(deck.getAttribute('style')).toBeNull()
    const prose = deck.querySelector<HTMLElement>('[data-testid="room-prose"]')!
    expect(prose.style.getPropertyValue('--wt-list-indent')).toBe('1.5em')
    expect(prose.style.getPropertyValue('--wt-code-font')).toBe('Menlo, Monaco, monospace')
  })
})
