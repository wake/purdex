// Going up through a session's history, in the deck and in the chat: the next older page is asked for when the reader nears the
// top (even when they flung the box there while a page was out), the place on screen does not jump when it lands, the start of
// the conversation is said, and a failing page is not asked for in a loop. Fake layout (older-turns-layout): jsdom has none.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, renderHook, screen } from '@testing-library/react'
import { useOlderTurns } from '../../hooks/useOlderTurns'
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
const entryFrom = (first: number, paging = false, generation = 0): ConversationEntry => ({
  doc: { ...emptyDoc(), turns: Array.from({ length: LAST - first + 1 }, (_, i) => turn(first + i)), hasMoreBefore: first > 0, generation },
  status: 'live', reason: '', paging, subagents: {},
})

const load = vi.fn()
const deck = (e: ConversationEntry, pane = 'p-old') => <DeckView paneId={pane} hostId="h" sessionId="s" entry={e} onSwitchToTerminal={() => {}} />
const chat = (e: ConversationEntry, pane = 'p-old') => (
  <ChatPane paneId={pane} conversation={{ state: 'ready', hostId: 'h', sessionId: 's', entry: e } as never} title="t" isActive isFocusTarget={false} onSwitchToTerminal={() => {}} />
)

beforeEach(() => {
  cleanup(); clearAllPanels(); forgetFolds('p-old\0s'); forgetScrollMemosWithPrefix('p-old'); forgetScrollMemosWithPrefix('p-b'); forgetFolds('p-b\0s')
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
    // in the flow (no sticky / absolute / fixed between it and the box): it scrolls away with the content
    for (let el: HTMLElement | null = screen.getByTestId(startId); el && el !== box(); el = el.parentElement) {
      expect(el.classList.contains('sticky') || el.classList.contains('absolute') || el.classList.contains('fixed')).toBe(false)
    }
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

  it('keeps the place of a pane that did not ask: another pane of the same session paged', () => {
    const two = (e: ConversationEntry) => <>{view(e, 'p-old')}{view(e, 'p-b')}</>
    const r = render(two(entryFrom(12)))
    const [a, b] = screen.getAllByTestId(boxId)
    b.scrollTop = 1000; fireEvent.scroll(b) // this reader is mid-history: turn 15 at -100
    expect(turnTop(b, 15)).toBe(-100)
    a.scrollTop = 0; fireEvent.scroll(a)
    expect(load).toHaveBeenCalledTimes(1) // the other pane asked
    act(() => r.rerender(two(entryFrom(8))))
    expect(turnTop(b, 15)).toBe(-100)
  })

  it('keeps the place when the page lands after the view was unmounted and mounted again (a tab switch)', () => {
    const first = render(view(entryFrom(12)))
    const box = () => screen.getByTestId(boxId)
    box().scrollTop = 1000; fireEvent.scroll(box())
    first.unmount()
    const again = render(view(entryFrom(12)))
    expect(turnTop(box(), 15)).toBe(-100)
    act(() => again.rerender(view(entryFrom(8))))
    expect(turnTop(box(), 15)).toBe(-100)
  })

  it('a jump (another window of the conversation) is not older turns: nothing is put back', () => {
    const { box, show } = mount(12)
    box().scrollTop = 1000; fireEvent.scroll(box())
    act(() => show(entryFrom(2, false, 1)))
    expect(box().scrollTop).toBe(1000) // not shifted by the 10 turns that came in above
  })

  it('the start-of-conversation line arrives with the last page without moving the reader, even if paging ends a render later', () => {
    const { box, to, show } = mount(4)
    to(box(), 100)
    expect(turnTop(box(), 4)).toBe(-100)
    act(() => show(entryFrom(0, true))) // the page landed, paging not yet cleared
    expect(screen.getByTestId(startId)).toBeInTheDocument()
    expect(turnTop(box(), 4)).toBe(-100)
    act(() => show(entryFrom(0, false)))
    expect(turnTop(box(), 4)).toBe(-100)
  })

  it('a status line coming and going above the reader does not move the turns (it takes no room in the flow)', () => {
    const { box, show } = mount(12)
    box().scrollTop = 1000; fireEvent.scroll(box())
    expect(turnTop(box(), 15)).toBe(-100)
    act(() => show(entryFrom(12, true)))
    expect(screen.getByTestId(boxId.replace('scroll', 'paging'))).toBeInTheDocument()
    expect(turnTop(box(), 15)).toBe(-100)
    act(() => show(entryFrom(12, false)))
    expect(turnTop(box(), 15)).toBe(-100)
  })
})

describe('useOlderTurns before the first placement', () => {
  const short = () => {
    const el = document.createElement('div')
    Object.defineProperty(el, 'scrollHeight', { value: 100 })
    Object.defineProperty(el, 'clientHeight', { value: 600 })
    return el
  }
  it('does not ask on its own while the reader has not been placed, and does once they are', () => {
    const el = short()
    const ask = vi.fn()
    const opts = { box: el, hasMore: true, paging: false, firstTurn: 5, generation: 0, load: ask, shiftBy: () => {} }
    const { rerender } = renderHook((p: { placed: boolean }) => useOlderTurns({ ...opts, ...p }), { initialProps: { placed: false } })
    expect(ask).not.toHaveBeenCalled()
    rerender({ placed: true })
    expect(ask).toHaveBeenCalledTimes(1)
  })
})
