// Bash mode in the chat (#2699): the command text is cleaned, and its output (system or user shaped) is kept, folded like the deck.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { ChatView, type ChatViewProps } from './ChatView'
import { UserBubble } from './ChatBubbles'
import { userText } from '../../lib/conversations/find-units'
import { clearAllPanels } from '../../lib/conversations/panel-memory'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import type { ConversationItem, UserItem } from '../../lib/conversations/types'
import type { PanelTurn } from '../../lib/conversations/panel-resolve'

const PANE = 'chat-bash-out'
const bashItem = (id: string, text = 'ls'): UserItem => ({ type: 'user', id, at: 1, index: 0, text, source: 'bash' }) as UserItem
const sysOut = (id: string, detail: string): ConversationItem => ({ type: 'system', id, at: 2, index: 0, kind: 'command_output', detail }) as unknown as ConversationItem
const userOut = (id: string, text: string): UserItem => ({ type: 'user', id, at: 2, index: 0, text, source: 'command_output' }) as UserItem
const agent = (id: string): ConversationItem => ({ type: 'agent_text', id, at: 3, index: 0, markdown: 'hello' }) as unknown as ConversationItem
const mount = (items: ConversationItem[]) => {
  const turns: PanelTurn[] = [{ id: 't1', index: 0, items: items.map((it, index) => ({ ...it, index }) as ConversationItem) }]
  const p: ChatViewProps = { paneKey: PANE, hostId: 'h', sessionId: 'session-1', title: 'x', status: 'idle', turns, onSwitchToTerminal: vi.fn(), active: true }
  return render(<ChatView {...p} />)
}

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ width: 1000, height: 600, top: 0, left: 0, right: 1000, bottom: 600, x: 0, y: 0, toJSON: () => ({}) }))
  cleanup(); clearAllPanels(); forgetFolds(PANE)
})
afterEach(() => { vi.restoreAllMocks() })

describe('bash output in the chat', () => {
  it('shows a system command_output (multi-line) as a foldable output after the command', () => {
    mount([bashItem('b1'), sysOut('s1', 'a\nb\nc')])
    expect(screen.getByTestId('chat-command')).toBeTruthy()
    fireEvent.click(screen.getByTestId('output-toggle'))
    expect(screen.getByTestId('output-toggle').getAttribute('aria-expanded')).toBe('true')
  })
  it('shows a user-source command_output as a fold too', () => {
    mount([bashItem('b1'), userOut('o1', 'a\nb')])
    expect(screen.getByTestId('output-toggle')).toBeTruthy()
  })
  it('keeps the data order with an agent text in between', () => {
    const { container } = mount([bashItem('b1'), agent('a1'), sysOut('s1', 'x\ny')])
    const order = Array.from(container.querySelectorAll('[data-testid="chat-command"],[data-testid="chat-agent"],[data-testid="deck-system"]')).map((e) => e.getAttribute('data-testid'))
    expect(order).toEqual(['chat-command', 'chat-agent', 'deck-system'])
  })
  it('shows a lone output with no command', () => {
    mount([sysOut('s2', 'p\nq')])
    expect(screen.getByTestId('output-toggle')).toBeTruthy()
  })
  it('does not bring other system kinds into the chat', () => {
    mount([bashItem('b1'), { type: 'system', id: 'n1', at: 2, index: 0, kind: 'notice', detail: 'zzz-note' } as unknown as ConversationItem])
    expect(screen.queryByText('zzz-note')).toBeNull()
  })
})

describe('bash command text is cleaned', () => {
  const dirty = 'echo \u001b[31mred\u001b[0m ‮x⁦y‎\u0007z\nline2\ttab'
  it('strips ANSI and invisible bidi / control characters, keeps newlines', () => {
    render(<UserBubble item={bashItem('b1', dirty)} />)
    const el = screen.getByTestId('chat-command').querySelector('[data-search-unit]')!
    expect(el.textContent).toBe('! echo red xyz\nline2\ttab')
  })
  it('the find index uses the same cleaned text', () => {
    expect(userText(bashItem('b1', dirty))).toBe('! echo red xyz\nline2\ttab')
  })
})
