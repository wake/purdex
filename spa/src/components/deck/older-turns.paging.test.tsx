// Going up through a session's history, in the deck and in the chat: the next older page is asked for when the reader nears the
// top (even when they flung the box there while a page was out), the place on screen does not jump when it lands, the start of
// the conversation is said, and a failing page is not asked for in a loop. Fake layout (older-turns-layout): jsdom has none.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { emptyDoc } from '../../lib/conversations/model'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import { forgetScrollMemosWithPrefix } from '../../lib/nex/transcript-scroll-memory'
import { clearAllPanels } from '../../lib/conversations/panel-memory'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import { useConversationStore, type ConversationEntry } from '../../stores/useConversationStore'
import { DeckView } from './DeckView'
import { ChatPane } from './ChatPane'
import { installFakeLayout, uninstallFakeLayout, layout, turnTop } from './older-turns-layout'

const LAST = 29
const user = (n: number): ConversationItem => ({ type: 'user', id: `u${n}`, at: 1, index: 0, text: `said ${n}`, source: 'user' })
const turn = (n: number): Turn => ({ id: `t${n}`, index: n, started_at: n, outcome: 'done', items: [user(n)] })
const entryFrom = (first: number, paging = false): ConversationEntry => ({
  doc: { ...emptyDoc(), turns: Array.from({ length: LAST - first + 1 }, (_, i) => turn(first + i)), hasMoreBefore: first > 0 },
  status: 'live', reason: '', paging, subagents: {},
})

const load = vi.fn()
const deck = (e: ConversationEntry) => <DeckView paneId="p-old" hostId="h" sessionId="s" entry={e} onSwitchToTerminal={() => {}} />
const chat = (e: ConversationEntry) => (
  <ChatPane paneId="p-old" conversation={{ state: 'ready', hostId: 'h', sessionId: 's', entry: e } as never} title="t" isActive isFocusTarget={false} onSwitchToTerminal={() => {}} />
)

beforeEach(() => {
  cleanup(); clearAllPanels(); forgetFolds('p-old\0s'); forgetScrollMemosWithPrefix('p-old')
  installFakeLayout()
  layout.turnH = 300; layout.client = 600
  load.mockReset()
  useConversationStore.setState({ loadBefore: (async (...a: unknown[]) => { load(...a) }) as never })
})
afterEach(() => { vi.useRealTimers(); uninstallFakeLayout() })

describe.each([
  ['deck', deck, 'deck-scroll', 'deck-history-start'],
  ['chat', chat, 'chat-scroll', 'chat-history-start'],
] as const)('older turns in the %s', (_name, view, boxId, startId) => {
  const mount = (first: number) => {
    const r = render(view(entryFrom(first)))
    const box = () => screen.getByTestId(boxId)
    const to = (el: HTMLElement, top: number) => { el.scrollTop = top; fireEvent.scroll(el) }
    return { ...r, box, to, show: (e: ConversationEntry) => r.rerender(view(e)) }
  }
  // 300 px per turn, a 600 px box: 18 turns held, 5400 px, the reader at the bottom.

  it('asks for the page before the first turn held when the reader reaches the top', () => {
    const { box, to } = mount(12)
    expect(box().scrollTop).toBe(18 * 300 - 600) // at the end
    expect(load).not.toHaveBeenCalled()
    to(box(), 0)
    expect(load).toHaveBeenCalledTimes(1)
    expect(load).toHaveBeenCalledWith('h', 's')
  })

  it('keeps the turn on screen where it was when the page lands, page after page, up to the first turn', () => {
    const { box, to, show } = mount(12)
    let first = 12
    while (first > 0) {
      to(box(), 0)
      expect(load).toHaveBeenCalled()
      load.mockClear()
      expect(turnTop(box(), first)).toBe(0)
      const next = Math.max(0, first - 4)
      act(() => show(entryFrom(next)))
      expect(turnTop(box(), first)).toBe(0) // the same turn at the same place: no jump past the reader
      first = next
    }
    expect(screen.getByTestId(startId)).toHaveTextContent(/Start of the conversation|已到對話開頭/)
    to(box(), 0)
    expect(load).not.toHaveBeenCalled()
  })

  it('keeps the place of a reader part-way down a turn (a non-zero offset)', () => {
    const { box, to, show } = mount(12)
    to(box(), 100)
    expect(load).toHaveBeenCalledTimes(1)
    const before = turnTop(box(), 12)
    expect(before).toBe(-100)
    act(() => show(entryFrom(8)))
    expect(turnTop(box(), 12)).toBe(-100)
  })

  it('asks again when the reader flung the box to the very top while the page was out', () => {
    layout.turnH = 60
    const { box, to, show } = mount(15) // 15 turns of 60 px: 900 px held, 300 px of travel
    to(box(), 100)
    expect(load).toHaveBeenCalledTimes(1)
    act(() => show(entryFrom(15, true)))
    box().scrollTop = 0 // no scroll event for the reader's last bit of travel
    act(() => show(entryFrom(14))) // a page of one turn (60 px) lands
    expect(turnTop(box(), 15)).toBe(0) // not jumped
    expect(load).toHaveBeenCalledTimes(2) // and the reader, who is still within reach of the top, gets the next page
  })

  it('does not hammer a page that fails: the scroll path waits 1 s, then 2 s', () => {
    vi.useFakeTimers()
    const { box, to, show } = mount(12)
    const fail = () => { act(() => show(entryFrom(12, true))); act(() => show(entryFrom(12, false))) }
    to(box(), 0)
    expect(load).toHaveBeenCalledTimes(1)
    fail()
    for (let i = 0; i < 20; i++) to(box(), 0)
    expect(load).toHaveBeenCalledTimes(1) // 20 scroll events right after the failure ask for nothing
    act(() => { vi.advanceTimersByTime(1000) })
    expect(load).toHaveBeenCalledTimes(2) // the retry
    fail()
    for (let i = 0; i < 20; i++) to(box(), 0)
    act(() => { vi.advanceTimersByTime(1999) })
    expect(load).toHaveBeenCalledTimes(2)
    act(() => { vi.advanceTimersByTime(1) })
    expect(load).toHaveBeenCalledTimes(3)
  })

  it('does not ask while nothing older exists, and does not ask without a reason when the reader is far from the top', () => {
    const { box, to } = mount(0)
    to(box(), 0)
    expect(load).not.toHaveBeenCalled()
    cleanup(); forgetScrollMemosWithPrefix('p-old')
    const m = mount(12)
    m.to(m.box(), 2000)
    expect(load).not.toHaveBeenCalled()
  })

  it('a reader at the bottom is not pulled when a page lands (the box is long enough to scroll)', () => {
    const { box, show } = mount(12)
    const bottom = box().scrollTop
    act(() => show(entryFrom(8)))
    expect(box().scrollTop).toBe(bottom + 4 * 300) // still at the end of the longer box
  })
})
