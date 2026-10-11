// #2665 (CLAUDE.md tab-hosted rule): the ⌘F search of a deck / chat pane outlives the tab — the pane unmounts on a switch (the real TabContent
// here) and the word, the open bar and the match the reader stood on come back with it.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { forgetFoldsOfPane } from '../../lib/conversations/fold-memory'
import { clearAllFinds } from '../../lib/conversations/find-memory'
import { forgetScrollMemosWithPrefix } from '../../lib/nex/transcript-scroll-memory'
import { emptyDoc } from '../../lib/conversations/model'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import type { PaneConversation } from '../../hooks/useConversationOfPane'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { DeckPane } from './DeckPane'
import { ChatPane } from './ChatPane'

const H = 'h'
const user = (id: string, text: string): ConversationItem => ({ type: 'user', id, at: 1, index: 0, text, source: 'user' })
const turns: Turn[] = Array.from({ length: 3 }, (_, i) => ({ id: `t${i}`, index: i, started_at: i, outcome: 'done' as const, items: [user(`u${i}`, `needle ${i}`)] }))
const conversation: PaneConversation = { state: 'ready', hostId: H, sessionId: 's', entry: { doc: { ...emptyDoc(), turns }, status: 'live', reason: '', paging: false, subagents: {} } }

const tab: Tab = { ...createTab({ kind: 'execution', executionId: 'x', host: H }), id: 't-find' }
const dash: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [tab, dash]
const pane = (tab.layout as { pane: { id: string } }).pane.id

function Deck({ pane: p }: PaneRendererProps) {
  return <DeckPane paneId={p.id} conversation={conversation} isActive isFocusTarget />
}
function Chat({ pane: p }: PaneRendererProps) {
  return <ChatPane paneId={p.id} title="t" conversation={conversation} isActive isFocusTarget />
}

class FakeHighlight { ranges: Range[] = []; add(r: Range) { this.ranges.push(r); return this } }
const g = globalThis as unknown as { CSS?: unknown; Highlight?: unknown }
let highlights: Map<string, FakeHighlight>

class NoRO { observe() {} unobserve() {} disconnect() {} }

beforeEach(() => {
  cleanup()
  highlights = new Map()
  g.CSS = { highlights }
  g.Highlight = FakeHighlight
  vi.stubGlobal('ResizeObserver', NoRO)
  clearModuleRegistry()
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => <div data-testid="other-tab" /> }] })
  clearAllFinds(); forgetFoldsOfPane(pane); forgetScrollMemosWithPrefix(pane)
})
afterEach(() => {
  g.CSS = undefined; g.Highlight = undefined
  vi.unstubAllGlobals(); forgetScrollMemosWithPrefix(pane)
})

const findKeys = () => ((navigator.platform || navigator.userAgent).match(/Mac|iPhone|iPad|iPod/) ? { key: 'f', metaKey: true } : { key: 'f', ctrlKey: true })

describe.each([
  ['deck', 'session-view-deck', Deck],
  ['chat', 'session-view-chat', Chat],
] as const)('%s: the search across a tab switch', (_name, viewId, Renderer) => {
  it('the bar, the word and the current match come back with the tab', () => {
    registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: Renderer }] })
    const r = render(<TabContent activeTab={tab} allTabs={all} />)
    fireEvent.keyDown(screen.getByTestId(viewId), findKeys())
    fireEvent.change(screen.getByTestId('find-input'), { target: { value: 'needle' } })
    fireEvent.keyDown(screen.getByTestId('find-input'), { key: 'Enter' })
    expect(screen.getByTestId('find-count').textContent).toBe('2 / 3')

    r.rerender(<TabContent activeTab={dash} allTabs={all} />)
    expect(screen.queryByTestId(viewId)).toBeNull()
    expect(screen.queryByTestId('find-bar')).toBeNull()
    expect(highlights.get('search-current')).toBeUndefined() // nothing of the hidden tab stays marked

    r.rerender(<TabContent activeTab={tab} allTabs={all} />)
    expect((screen.getByTestId('find-input') as HTMLInputElement).value).toBe('needle')
    expect(screen.getByTestId('find-count').textContent).toBe('2 / 3')
    expect(highlights.get('search-current')?.ranges.map((x) => x.toString())).toEqual(['needle'])
    // and it goes on from there
    fireEvent.keyDown(screen.getByTestId('find-input'), { key: 'Enter' })
    expect(screen.getByTestId('find-count').textContent).toBe('3 / 3')
  })
})
