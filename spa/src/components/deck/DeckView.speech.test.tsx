// spa/src/components/deck/DeckView.speech.test.tsx — the deck's speech marks for a persona (name pool v2 spec §9.4, U15): the turn's first agent text carries `[avatar] Name · model · time` (the face replaces the model
// icon), and each LATER speech start (the agent text after a tool or a thought) carries a compact `[avatar] Name`. A conversation whose agent is no persona keeps today's header and draws nothing extra.
import { describe, it, expect, beforeEach } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import { forgetScrollMemosWithPrefix } from '../../lib/nex/transcript-scroll-memory'
import { emptyDoc } from '../../lib/conversations/model'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import type { ConversationEntry } from '../../stores/useConversationStore'
import type { Seat } from '../../lib/names/seat'
import { DeckView } from './DeckView'
import { DeckPane } from './DeckPane'

const text = (id: string, index: number): ConversationItem => ({ type: 'agent_text', id, at: new Date(2026, 9, 19, 4, 12).getTime(), index, markdown: `said ${id}` }) as ConversationItem
const think = (id: string, index: number): ConversationItem => ({ type: 'thinking', id, at: 1, index, text: 'hm' }) as ConversationItem
const system = (id: string, index: number): ConversationItem => ({ type: 'system', id, at: 1, index, kind: 'notice', detail: 'n' }) as ConversationItem
const step = (id: string, index: number): ConversationItem => ({
  type: 'step', id, at: 1, index, kind: 'execute', tool: 'Bash', status: 'done', summary: 'ls', started_at: 1, input: null,
  command: { text: 'ls' }, output: { text: 'a', total_lines: 1, total_bytes: 1, truncated: false },
}) as ConversationItem
const turn = (items: ConversationItem[]): Turn => ({ id: 't0', index: 0, started_at: 0, outcome: 'done', items })
const entry = (items: ConversationItem[]): ConversationEntry => ({
  doc: { ...emptyDoc(), turns: [turn(items)], header: { usage: { model: 'claude-opus-4' } } as never }, status: 'live', reason: '', paging: false, subagents: {},
})
const props = { paneId: 'p-speech', hostId: 'h', sessionId: 's', onSwitchToTerminal: () => {} }
const ann: Seat = { code: 'ann', name: 'Ann', avatar: '' }

HTMLElement.prototype.scrollTo = function () {} as never
beforeEach(() => { cleanup(); forgetFolds('p-speech\0s'); forgetScrollMemosWithPrefix('p-speech') })

describe('the deck of a persona', () => {
  it('the first speech carries the full head: face, name, model, time', () => {
    render(<DeckView {...props} seat={ann} entry={entry([text('a', 0)])} />)
    const head = screen.getByTestId('deck-agent-header')
    expect(head).toHaveAttribute('data-persona', 'ann')
    expect(within(head).getByRole('img', { name: 'Ann' })).toBeInTheDocument()
    expect(head).toHaveTextContent('Ann')
    expect(head).toHaveTextContent('claude-opus-4')
    expect(head).toHaveTextContent('04:12')
  })

  it('each later speech start gets the compact line — after a tool, after a thought — and a continuation does not', () => {
    render(<DeckView {...props} seat={ann} entry={entry([text('a', 0), step('s1', 1), text('b', 2), think('k', 3), text('c', 4), text('d', 5)])} />)
    expect(screen.getAllByTestId('deck-agent-header')).toHaveLength(1)
    const lines = screen.getAllByTestId('deck-speech-line')
    expect(lines).toHaveLength(2) // b (after the tool) and c (after the thought); d continues c
    for (const l of lines) { expect(l).toHaveTextContent('Ann'); expect(l).not.toHaveTextContent(/\d\d:\d\d/) }
  })

  it('a system row between two texts does not make the second a start', () => {
    render(<DeckView {...props} seat={ann} entry={entry([text('a', 0), system('n', 1), text('b', 2)])} />)
    expect(screen.queryAllByTestId('deck-speech-line')).toHaveLength(0)
  })

  it('a text that opens the turn after a tool is the turn\'s first: it carries the head, not the compact line', () => {
    render(<DeckView {...props} seat={ann} entry={entry([step('s1', 0), text('a', 1)])} />)
    expect(screen.getAllByTestId('deck-agent-header')).toHaveLength(1)
    expect(screen.queryAllByTestId('deck-speech-line')).toHaveLength(0)
  })
})

describe('the pane hands the persona to the deck', () => {
  it('DeckPane\'s `seat` reaches the head line', () => {
    const base = { paneId: 'p-speech', isActive: true, isFocusTarget: false, onSwitchToTerminal: () => {} }
    render(<DeckPane {...base} seat={ann} conversation={{ state: 'ready', hostId: 'h', sessionId: 's', entry: entry([text('a', 0)]) }} />)
    expect(screen.getByTestId('deck-agent-header')).toHaveAttribute('data-persona', 'ann')
  })
})

describe('the deck of a conversation that is no persona', () => {
  it('keeps today\'s header (the model icon and name) and draws no extra line', () => {
    render(<DeckView {...props} seat={null} entry={entry([text('a', 0), step('s1', 1), text('b', 2)])} />)
    const head = screen.getByTestId('deck-agent-header')
    expect(head).not.toHaveAttribute('data-persona')
    expect(head).toHaveTextContent('claude-opus-4')
    expect(screen.queryAllByTestId('deck-speech-line')).toHaveLength(0)
    expect(screen.queryByRole('img', { name: 'Ann' })).toBeNull()
  })
})
