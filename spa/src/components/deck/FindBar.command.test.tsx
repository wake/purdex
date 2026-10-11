// #2699: ⌘F and bash commands / command output — the deck draws the cleaned command (so match offsets agree with the index), and the chat
// searches both shapes of a command output (system and user) and opens their fold.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { emptyDoc } from '../../lib/conversations/model'
import { forgetFoldsOfPane } from '../../lib/conversations/fold-memory'
import { clearAllFinds } from '../../lib/conversations/find-memory'
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

const turn = (index: number, items: ConversationItem[]): Turn => ({ id: `t${index}`, index, started_at: index, outcome: 'done', items })
const entry = (turns: Turn[]): ConversationEntry => ({ doc: { ...emptyDoc(), turns }, status: 'live', reason: '', paging: false, subagents: {} })
const conv = (turns: Turn[]): PaneConversation => ({ state: 'ready', hostId: 'h', sessionId: 's', entry: entry(turns) })

const PANE = 'p-find-cmd'
const base = { paneId: PANE, isActive: true, isFocusTarget: true, onSwitchToTerminal: vi.fn() }
const findKeys = () => (navigator.platform || navigator.userAgent).match(/Mac|iPhone|iPad|iPod/) ? { key: 'f', metaKey: true } : { key: 'f', ctrlKey: true }
const input = () => screen.getByTestId('find-input') as HTMLInputElement
const type = (value: string) => fireEvent.change(input(), { target: { value } })
const count = () => screen.getByTestId('find-count').textContent

beforeEach(() => {
  highlights = new Map()
  g.CSS = { highlights }
  g.Highlight = FakeHighlight
  cleanup(); clearAllFinds(); forgetFoldsOfPane(PANE)
})
afterEach(() => { g.CSS = undefined; g.Highlight = undefined; vi.restoreAllMocks() })

const lines = '1 one\n2 two\n3 three\n4 four\nfifthneedle five\n6 six'

describe('bash command and its output (#2699)', () => {
  it('the deck draws the cleaned command, so a match after the stripped characters is marked right', () => {
    const bash = { type: 'user', id: 'b1', at: 1, index: 0, text: 'echo \u001b[31m‮hello world', source: 'bash' } as ConversationItem
    render(<DeckPane {...base} conversation={conv([turn(0, [bash])])} />)
    expect(document.body.textContent).not.toContain('\u001b')
    expect(document.body.textContent).not.toContain('‮')
    fireEvent.keyDown(screen.getByTestId('session-view-deck'), findKeys())
    type('world')
    expect(count()).toBe('1 / 1')
    expect(marked('search-current')).toEqual(['world'])
  })

  it.each([
    ['system-shaped', { type: 'system', id: 'sy', at: 1, index: 0, kind: 'command_output', detail: lines } as ConversationItem],
    ['user-shaped', { type: 'user', id: 'co', at: 1, index: 0, text: lines, source: 'command_output' } as ConversationItem],
  ])('the chat searches a %s command output and opens its fold', (_n, item) => {
    render(<ChatPane {...base} title="t" conversation={conv([turn(0, [item])])} />)
    expect(screen.queryByTestId('output-body')).toBeNull()
    fireEvent.keyDown(screen.getByTestId('session-view-chat'), findKeys())
    type('fifthneedle')
    expect(count()).toBe('1 / 1')
    expect(screen.getByTestId('output-body')).toBeInTheDocument()
    expect(marked('search-current')).toEqual(['fifthneedle'])
  })
})
