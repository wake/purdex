// #2694: a ⌘F hit in the cut-off lines of a folded peer message opens its fold (the user unit carries the `peerKey` reveal key). Goes through the
// real FindBar in a DeckPane; only layout is stubbed (jsdom lays nothing out, so scrollHeight says "taller than three lines").
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { clearAllFinds } from '../../lib/conversations/find-memory'
import { forgetFoldsOfPane } from '../../lib/conversations/fold-memory'
import { emptyDoc } from '../../lib/conversations/model'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import type { PaneConversation } from '../../hooks/useConversationOfPane'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { DeckPane } from './DeckPane'

const H = 'h'
const PANE = 'pane-peer-reveal'
const peer: ConversationItem = { type: 'user', id: 'p1', at: 1, index: 0, source: 'peer', from: { kind: 'peer', name: 'host/a' }, text: 'line one\nline two\nline three\nline four\nzebrafive tail' }
const turns: Turn[] = [{ id: 't0', index: 0, started_at: 0, outcome: 'done', items: [peer] }]
const conversation: PaneConversation = { state: 'ready', hostId: H, sessionId: 's', entry: { doc: { ...emptyDoc(), turns }, status: 'live', reason: '', paging: false, subagents: {} } }

class FakeHighlight { ranges: Range[] = []; add(r: Range) { this.ranges.push(r); return this } }
class NoRO { observe() {} unobserve() {} disconnect() {} }
const g = globalThis as unknown as { CSS?: unknown; Highlight?: unknown }
let highlights: Map<string, FakeHighlight>

beforeEach(() => {
  cleanup()
  highlights = new Map()
  g.CSS = { highlights }
  g.Highlight = FakeHighlight
  vi.stubGlobal('ResizeObserver', NoRO)
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(() => 200)
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  clearAllFinds(); forgetFoldsOfPane(PANE)
})
afterEach(() => { g.CSS = undefined; g.Highlight = undefined; vi.unstubAllGlobals(); vi.restoreAllMocks() })

const findKeys = () => ((navigator.platform || navigator.userAgent).match(/Mac|iPhone|iPad|iPod/) ? { key: 'f', metaKey: true } : { key: 'f', ctrlKey: true })

describe('deck: a find hit inside a folded peer message', () => {
  it('opens the fold and marks the hit when the word sits past the third line', async () => {
    render(<DeckPane paneId={PANE} conversation={conversation} isActive isFocusTarget />)
    const box = screen.getByTestId('deck-user')
    const toggle = within(box).getByRole('button')
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(box.querySelector('.line-clamp-3')).not.toBeNull()

    fireEvent.keyDown(screen.getByTestId('session-view-deck'), findKeys())
    fireEvent.change(screen.getByTestId('find-input'), { target: { value: 'zebrafive' } })
    await act(async () => { await Promise.resolve() })

    expect(screen.getByTestId('find-count').textContent).toBe('1 / 1')
    expect(within(screen.getByTestId('deck-user')).getByRole('button').getAttribute('aria-expanded')).toBe('true')
    expect(screen.getByTestId('deck-user').querySelector('.line-clamp-3')).toBeNull()
    expect(highlights.get('search-current')?.ranges.map((x) => x.toString())).toEqual(['zebrafive'])
  })
})
