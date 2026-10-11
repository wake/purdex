// #2665: ⌘F searches the conversation of a deck / chat pane. The bar opens only for a ⌘F made inside the pane, finds the word in the person's
// words, the agent's prose and the steps' lines, marks every match (the CSS Custom Highlight API, faked here) and the current one, steps with
// Enter / Shift+Enter, opens a far-off turn that is drawn as plain text, and Esc takes it all away and gives the focus back to the input.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { emptyDoc } from '../../lib/conversations/model'
import { forgetFoldsOfPane } from '../../lib/conversations/fold-memory'
import { clearAllFinds, findKey, readFind } from '../../lib/conversations/find-memory'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import type { ConversationEntry } from '../../stores/useConversationStore'
import type { PaneConversation } from '../../hooks/useConversationOfPane'
import { DeckPane } from './DeckPane'
import { ChatPane } from './ChatPane'

class FakeHighlight {
  ranges: Range[] = []
  add(r: Range) { this.ranges.push(r); return this }
}
const g = globalThis as unknown as { CSS?: unknown; Highlight?: unknown }
let highlights: Map<string, FakeHighlight>
const marked = (name: string) => (highlights.get(name)?.ranges ?? []).map((r) => r.toString())

const user = (id: string, text: string): ConversationItem => ({ type: 'user', id, at: 1, index: 0, text, source: 'user' })
const agent = (id: string, markdown: string): ConversationItem => ({ type: 'agent_text', id, at: 1, index: 0, markdown })
const exec = (id: string, command: string): ConversationItem => ({
  type: 'step', id, at: 1, index: 0, kind: 'execute', tool: 'Bash', status: 'done', summary: command, started_at: 1, input: null, command: { text: command },
}) as ConversationItem
const turn = (index: number, items: ConversationItem[]): Turn => ({ id: `t${index}`, index, started_at: index, outcome: 'done', items })
const entry = (turns: Turn[]): ConversationEntry => ({ doc: { ...emptyDoc(), turns }, status: 'live', reason: '', paging: false, subagents: {} })
const conv = (turns: Turn[], sessionId = 's'): PaneConversation => ({ state: 'ready', hostId: 'h', sessionId, entry: entry(turns) })

const PANE = 'p-find'
const KEY = findKey(PANE, 's')
const base = { paneId: PANE, isActive: true, isFocusTarget: true, onSwitchToTerminal: vi.fn() }
const cmdF = { key: 'f', metaKey: true }

// IS_MAC decides which modifier counts: the test machine's navigator is jsdom's, so a Ctrl+F is the shortcut here.
const findKeys = () => (navigator.platform || navigator.userAgent).match(/Mac|iPhone|iPad|iPod/) ? cmdF : { key: 'f', ctrlKey: true }

const input = () => screen.getByTestId('find-input') as HTMLInputElement
const type = (value: string) => fireEvent.change(input(), { target: { value } })
const count = () => screen.getByTestId('find-count').textContent

beforeEach(() => {
  highlights = new Map()
  g.CSS = { highlights }
  g.Highlight = FakeHighlight
  cleanup(); clearAllFinds(); forgetFoldsOfPane(PANE)
})
afterEach(() => { g.CSS = undefined; g.Highlight = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals() })

const THREE = [
  turn(0, [user('u0', 'the needle is here'), agent('a0', 'a **needle** in the hay'), exec('e0', 'grep needle src')]),
  turn(1, [user('u1', 'nothing')]),
]

describe.each([
  ['deck', 'session-view-deck', (turns: Turn[]) => <DeckPane {...base} conversation={conv(turns)} />],
  ['chat', 'session-view-chat', (turns: Turn[]) => <ChatPane {...base} title="t" conversation={conv(turns)} />],
] as const)('%s: ⌘F', (name, viewId, mount) => {
  it('opens for a ⌘F made inside the pane, with the input focused; one made outside it is left alone', () => {
    render(mount(THREE))
    const outside = fireEvent.keyDown(document.body, findKeys())
    expect(outside).toBe(true) // not prevented
    expect(screen.queryByTestId('find-bar')).toBeNull()
    const prevented = !fireEvent.keyDown(screen.getByTestId(viewId), findKeys())
    expect(prevented).toBe(true)
    expect(screen.getByTestId('find-bar')).toBeInTheDocument()
    expect(document.activeElement).toBe(input())
  })

  it('finds the word in what the view draws, marks every match and the current one; Enter / Shift+Enter step, round the ends', () => {
    render(mount(THREE))
    fireEvent.keyDown(screen.getByTestId(viewId), findKeys())
    type('needle')
    // the deck draws the step's line too (the chat shows a chain of work as a row that opens the panel)
    const total = name === 'deck' ? 3 : 2
    expect(count()).toBe(`1 / ${total}`)
    expect(marked('search-current')).toEqual(['needle'])
    expect(marked('search-match')).toHaveLength(total - 1)
    fireEvent.keyDown(input(), { key: 'Enter' })
    expect(count()).toBe(`2 / ${total}`)
    fireEvent.keyDown(input(), { key: 'Enter', shiftKey: true })
    expect(count()).toBe(`1 / ${total}`)
    fireEvent.keyDown(input(), { key: 'Enter', shiftKey: true })
    expect(count()).toBe(`${total} / ${total}`) // before the first: round to the last
    fireEvent.keyDown(input(), { key: 'Enter' })
    expect(count()).toBe(`1 / ${total}`)
  })

  it('a word that is not there says so; a single Latin letter does not search', () => {
    render(mount(THREE))
    fireEvent.keyDown(screen.getByTestId(viewId), findKeys())
    type('zzzz')
    expect(count()).toBe('No results')
    type('n')
    expect(count()).toBe('')
    expect(marked('search-match')).toEqual([])
  })

  it('Esc closes the bar, takes the marks away and forgets the search', () => {
    render(mount(THREE))
    fireEvent.keyDown(screen.getByTestId(viewId), findKeys())
    type('needle')
    fireEvent.keyDown(input(), { key: 'Escape' })
    expect(screen.queryByTestId('find-bar')).toBeNull()
    expect(marked('search-current')).toEqual([])
    expect(readFind(KEY).open).toBe(false)
    fireEvent.keyDown(screen.getByTestId(viewId), findKeys())
    expect(input().value).toBe('')
  })

  it('the pane keeps its search when it is shown again (the memory is outside the component)', () => {
    const first = render(mount(THREE))
    fireEvent.keyDown(screen.getByTestId(viewId), findKeys())
    type('needle')
    fireEvent.keyDown(input(), { key: 'Enter' })
    first.unmount()
    render(mount(THREE))
    expect(input().value).toBe('needle')
    expect(count()).toBe(`2 / ${name === 'deck' ? 3 : 2}`)
    expect(marked('search-current')).toEqual(['needle'])
  })

  it('a conversation that is not ready has nothing to search: the shortcut is not taken', () => {
    const view = name === 'deck'
      ? <DeckPane {...base} conversation={{ state: 'resolving' } as PaneConversation} />
      : <ChatPane {...base} title="t" conversation={{ state: 'resolving' } as PaneConversation} />
    render(view)
    expect(fireEvent.keyDown(screen.getByTestId(viewId), findKeys())).toBe(true)
    expect(screen.queryByTestId('find-bar')).toBeNull()
  })
})

describe('the deck: what hides behind a fold', () => {
  const output = { text: 'alpha\nneedle row\nomega', total_lines: 3, total_bytes: 1, truncated: false }
  const run = { ...(exec('e0', 'ls') as object), output } as ConversationItem

  it('a hit in a folded output opens the fold and marks the word in it', () => {
    render(<DeckPane {...base} conversation={conv([turn(0, [run])])} />)
    expect(screen.queryByTestId('output-body')).toBeNull()
    fireEvent.keyDown(screen.getByTestId('session-view-deck'), findKeys())
    type('needle')
    expect(count()).toBe('1 / 1')
    expect(screen.getByTestId('output-body')).toBeInTheDocument()
    expect(marked('search-current')).toEqual(['needle'])
  })

  it('a hit in a loaded subagent\'s folded steps opens the task line, then the output', () => {
    const task = { type: 'step', id: 'k1', at: 1, index: 1, kind: 'task', tool: 'Task', status: 'done', summary: 'dig', started_at: 1, input: null, subagent: { agent_id: 'ag', description: 'dig' } } as ConversationItem
    const e = entry([turn(0, [task])])
    e.subagents = { ag: { state: 'ready', answer: { items: [{ ...(run as object), id: 'c1' }] } } } as never
    render(<DeckPane {...base} conversation={{ state: 'ready', hostId: 'h', sessionId: 's', entry: e }} />)
    fireEvent.keyDown(screen.getByTestId('session-view-deck'), findKeys())
    type('needle')
    expect(count()).toBe('1 / 1')
    expect(screen.getByTestId('subagent-steps')).toBeInTheDocument()
    expect(screen.getByTestId('output-body')).toBeInTheDocument()
    expect(marked('search-current')).toEqual(['needle'])
  })

  it('a subagent\'s step that shares its id with a main-turn step is found in its own place, not in the main one', () => {
    const main = { ...(run as object), id: 'dup', output: { text: 'main row', total_lines: 1, total_bytes: 1, truncated: false } } as ConversationItem
    const task = { type: 'step', id: 'k1', at: 1, index: 1, kind: 'task', tool: 'Task', status: 'done', summary: 'dig', started_at: 1, input: null, subagent: { agent_id: 'ag', description: 'dig' } } as ConversationItem
    const e = entry([turn(0, [main, task])])
    e.subagents = { ag: { state: 'ready', answer: { items: [{ ...(run as object), id: 'dup', output: { text: 'inner needle', total_lines: 1, total_bytes: 1, truncated: false } }] } } } as never
    render(<DeckPane {...base} conversation={{ state: 'ready', hostId: 'h', sessionId: 's', entry: e }} />)
    fireEvent.keyDown(screen.getByTestId('session-view-deck'), findKeys())
    type('needle')
    expect(count()).toBe('1 / 1')
    const mark = highlights.get('search-current')!.ranges[0]
    expect(mark.startContainer.parentElement?.closest('[data-testid="subagent-steps"]')).not.toBeNull()
  })

  it('a long system command output is a fold that is searched', () => {
    const sys = { type: 'system', id: 'sy', at: 1, index: 0, kind: 'command_output', detail: 'first line\nthe needle line\nlast' } as ConversationItem
    render(<DeckPane {...base} conversation={conv([turn(0, [sys])])} />)
    fireEvent.keyDown(screen.getByTestId('session-view-deck'), findKeys())
    type('needle')
    expect(count()).toBe('1 / 1')
    expect(screen.getByTestId('output-body')).toBeInTheDocument()
  })

  it('the marks come back by themselves when React replaces the text under them (a late redraw)', async () => {
    render(<DeckPane {...base} conversation={conv(THREE)} />)
    fireEvent.keyDown(screen.getByTestId('session-view-deck'), findKeys())
    type('needle')
    const before = highlights.get('search-current')!.ranges[0]
    const unit = document.querySelector('[data-search-unit="u:u0"]') as HTMLElement
    await act(async () => {
      unit.textContent = 'the needle is here' // replaces the text node: the old Range collapses
      await new Promise((r) => setTimeout(r, 200))
    })
    const after = highlights.get('search-current')!.ranges[0]
    expect(after).not.toBe(before)
    expect(after.toString()).toBe('needle')
  })
})

describe('a ⌘F from the right panel is the panel\'s', () => {
  it('is not taken (the stream may be inert behind a modal panel)', () => {
    render(<DeckPane {...base} conversation={conv(THREE)} />)
    const panel = document.createElement('div')
    panel.setAttribute('data-testid', 'session-right-panel')
    screen.getByTestId('session-view-deck').appendChild(panel)
    expect(fireEvent.keyDown(panel, findKeys())).toBe(true)
    expect(screen.queryByTestId('find-bar')).toBeNull()
  })
})

describe('the deck: a turn far from the end', () => {
  it('draws its agent text as plain text until a search lands on it, then in full — the match is marked in the real markdown', () => {
    // an observer that never sees anything near: the far turns stay plain text
    vi.stubGlobal('IntersectionObserver', class { observe() {} unobserve() {} disconnect() {} })
    const turns = Array.from({ length: 25 }, (_, i) => turn(i, [agent(`a${i}`, i === 0 ? 'the **needle** far away' : `filler ${i}`)]))
    render(<DeckPane {...base} conversation={conv(turns)} />)
    expect(document.querySelectorAll('[data-testid="room-prose-light"]').length).toBeGreaterThan(0)
    fireEvent.keyDown(screen.getByTestId('session-view-deck'), findKeys())
    type('needle')
    expect(count()).toBe('1 / 1')
    expect(document.querySelector('[data-search-unit="a:a0"]')).not.toBeNull()
    expect(marked('search-current')).toEqual(['needle'])
  })
})

describe('Esc gives the focus back to the input', () => {
  it('the deck\'s input (when the pane has one)', async () => {
    const footer = () => <div data-testid="session-input"><textarea data-testid="ta" /></div>
    render(<DeckPane {...base} footer={footer} conversation={conv(THREE)} />)
    fireEvent.keyDown(screen.getByTestId('session-view-deck'), findKeys())
    await act(async () => {})
    fireEvent.keyDown(input(), { key: 'Escape' })
    expect(document.activeElement).toBe(screen.getByTestId('ta'))
  })
})
