// U3 mount: the right panel inside the deck and the chat (SessionPanelSplit). 「顯示全部」, a subagent line and a chat work row
// open it on the right step / chain; wide panes dock it, narrow ones lay it over the pane; only the focused pane's Esc closes it.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { emptyDoc } from '../../lib/conversations/model'
import { clearAllPanels, conversationBinding, readPanel } from '../../lib/conversations/panel-memory'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import type { ConversationEntry } from '../../stores/useConversationStore'
import type { PaneConversation } from '../../hooks/useConversationOfPane'
import { DeckPane } from './DeckPane'
import { ChatPane } from './ChatPane'

const exec = {
  type: 'step', id: 'x1', at: 1, index: 0, kind: 'execute', tool: 'Bash', status: 'done', summary: 'ls', started_at: 1, input: null,
  command: { text: 'ls' }, output: { text: Array.from({ length: 30 }, (_, i) => `r${i}`).join('\n'), total_lines: 30, total_bytes: 1, truncated: false },
} as ConversationItem
const task = {
  type: 'step', id: 'k1', at: 2, index: 1, kind: 'task', tool: 'Task', status: 'done', summary: 'look around', started_at: 2, input: null,
  subagent: { agent_id: 'ag1', description: 'look around', type: 'Explore' },
} as ConversationItem
const turn = (index: number, items: ConversationItem[]): Turn => ({ id: `t${index}`, index, started_at: index, outcome: 'done', items })
const entry = (turns: Turn[]): ConversationEntry => ({ doc: { ...emptyDoc(), turns }, status: 'live', reason: '', paging: false, subagents: {} })
const conv = (turns: Turn[], sessionId = 's'): PaneConversation => ({ state: 'ready', hostId: 'h', sessionId, entry: entry(turns) })

const PANE = 'p-split'
const BIND = conversationBinding('h', 's')
const WIDTH = { px: 1000 }
const base = { paneId: PANE, isActive: true, isFocusTarget: true, onSwitchToTerminal: vi.fn() }

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ width: WIDTH.px, height: 600, top: 0, left: 0, right: WIDTH.px, bottom: 600, x: 0, y: 0, toJSON: () => ({}) }))
  WIDTH.px = 1000
  cleanup(); clearAllPanels(); forgetFolds(`${PANE}\0s`)
})
afterEach(() => { vi.restoreAllMocks() })

describe('the deck opens the right panel', () => {
  it('「顯示全部」 opens the output of THAT step in THAT turn, docked beside the deck in a wide pane', () => {
    render(<DeckPane {...base} conversation={conv([turn(0, [exec])])} />)
    fireEvent.click(screen.getByTestId('output-toggle'))
    fireEvent.click(screen.getByTestId('output-show-all'))
    expect(readPanel(PANE)).toMatchObject({ binding: BIND, content: { kind: 'output', turnId: 't0', stepId: 'x1' } })
    expect(screen.getByTestId('session-split')).toHaveAttribute('data-mode', 'docked')
    expect(screen.getByTestId('session-right-panel')).toBeInTheDocument()
    expect(screen.getByTestId('deck-scroll')).toBeInTheDocument() // the deck stays
  })

  it('a subagent line opens that subagent in the panel', () => {
    render(<DeckPane {...base} conversation={conv([turn(3, [exec, task])])} />)
    fireEvent.click(screen.getByTestId('deck-step-task'))
    expect(readPanel(PANE)).toMatchObject({ binding: BIND, content: { kind: 'subagent', turnId: 't3', stepId: 'k1' } })
    expect(screen.getByTestId('session-right-panel')).toBeInTheDocument()
  })

  it('a narrow pane lays the panel over the deck, with a scrim that closes it', () => {
    WIDTH.px = 500
    render(<DeckPane {...base} conversation={conv([turn(0, [exec, task])])} />)
    fireEvent.click(screen.getByTestId('deck-step-task'))
    expect(screen.getByTestId('session-split')).toHaveAttribute('data-mode', 'overlay')
    expect(screen.getByTestId('split-overlay')).toContainElement(screen.getByTestId('session-right-panel'))
    fireEvent.click(screen.getByTestId('split-scrim'))
    expect(readPanel(PANE)).toBeUndefined()
    expect(screen.queryByTestId('session-right-panel')).toBeNull()
  })

  it('Esc closes the focused pane\'s panel only: a pane that is not the focus target keeps its own', () => {
    const { unmount } = render(<DeckPane {...base} isFocusTarget={false} conversation={conv([turn(0, [exec, task])])} />)
    fireEvent.click(screen.getByTestId('deck-step-task'))
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(readPanel(PANE)).toBeDefined()
    unmount()
    render(<DeckPane {...base} conversation={conv([turn(0, [exec, task])])} />)
    expect(readPanel(PANE)).toBeDefined() // opened before
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(readPanel(PANE)).toBeUndefined()
  })

  it('in a narrow pane Esc also leaves only the focused pane\'s overlay closed (the overlay\'s own Esc is gated the same way)', () => {
    WIDTH.px = 500
    render(<DeckPane {...base} isFocusTarget={false} conversation={conv([turn(0, [exec, task])])} />)
    fireEvent.click(screen.getByTestId('deck-step-task'))
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(readPanel(PANE)).toBeDefined()
    cleanup()
    render(<DeckPane {...base} conversation={conv([turn(0, [exec, task])])} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(readPanel(PANE)).toBeUndefined()
  })

  it('Esc with both the overlay and the panel listening closes once, without trouble', () => {
    WIDTH.px = 500
    render(<DeckPane {...base} conversation={conv([turn(0, [exec, task])])} />)
    fireEvent.click(screen.getByTestId('deck-step-task'))
    expect(() => fireEvent.keyDown(document, { key: 'Escape' })).not.toThrow()
    expect(screen.queryByTestId('session-right-panel')).toBeNull()
    expect(screen.getByTestId('session-split')).toHaveAttribute('data-mode', 'closed')
  })

  it('a panel left from another session (/clear, relay) is dropped, not shown when the old one would come back', () => {
    const { rerender } = render(<DeckPane {...base} conversation={conv([turn(0, [exec, task])])} />)
    fireEvent.click(screen.getByTestId('deck-step-task'))
    rerender(<DeckPane {...base} conversation={conv([turn(0, [exec, task])], 's2')} />)
    expect(screen.queryByTestId('session-right-panel')).toBeNull()
    expect(readPanel(PANE)).toBeUndefined()
    rerender(<DeckPane {...base} conversation={conv([turn(0, [exec, task])])} />)
    expect(screen.queryByTestId('session-right-panel')).toBeNull()
  })
})

describe('the chat opens the right panel through the same split', () => {
  it('a work row opens its chain; narrow panes overlay it; Esc of the focused pane closes it', () => {
    render(<ChatPane {...base} title="t" conversation={conv([turn(0, [exec, task])])} />)
    fireEvent.click(screen.getAllByTestId('chat-work')[0])
    expect(readPanel(PANE)?.content).toMatchObject({ kind: 'chain', turnId: 't0' })
    expect(screen.getByTestId('session-split')).toHaveAttribute('data-mode', 'docked')
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(readPanel(PANE)).toBeUndefined()
    WIDTH.px = 500
    cleanup()
    render(<ChatPane {...base} title="t" conversation={conv([turn(0, [exec, task])])} />)
    fireEvent.click(screen.getAllByTestId('chat-work')[0])
    expect(screen.getByTestId('session-split')).toHaveAttribute('data-mode', 'overlay')
  })

  it('the chat has ONE layout: the panel is a SessionSplit child, not a sibling the chat lays out itself', () => {
    render(<ChatPane {...base} title="t" conversation={conv([turn(0, [exec, task])])} />)
    fireEvent.click(screen.getAllByTestId('chat-work')[0])
    expect(screen.getByTestId('session-split')).toContainElement(screen.getByTestId('session-right-panel'))
    expect(screen.getByTestId('split-chat')).toContainElement(screen.getByTestId('chat-scroll'))
  })

  it('an unreadable chat draws no panel even if the memory holds one', () => {
    render(<ChatPane {...base} title="t" conversation={conv([turn(0, [])])} />)
    expect(screen.getByTestId('unreadable')).toBeInTheDocument()
    expect(screen.queryByTestId('session-split')).toBeNull()
  })
})
