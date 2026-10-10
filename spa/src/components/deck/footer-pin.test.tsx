// v3 point 3: a reader at the bottom stays at the bottom when the footer (input, attachments) grows or shrinks and the
// scrolling box gets shorter or taller; a reader scrolled up is not moved. jsdom has no layout: a mock ResizeObserver
// and a box whose geometry the test sets.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { forgetScrollMemosWithPrefix } from '../../lib/nex/transcript-scroll-memory'
import { emptyDoc } from '../../lib/conversations/model'
import type { ConversationItem } from '../../lib/conversations/types'
import type { PanelTurn } from '../../lib/conversations/panel-resolve'
import type { ConversationEntry } from '../../stores/useConversationStore'
import { DeckView } from './DeckView'
import { ChatView } from './ChatView'

const userItem = (id: string, index: number): ConversationItem => ({ type: 'user', id, at: 1, index, text: id, source: 'user' })

class MockRO {
  static all: MockRO[] = []
  els: Element[] = []
  constructor(readonly cb: () => void) { MockRO.all.push(this) }
  observe(el: Element) { this.els.push(el) }
  unobserve() {}
  disconnect() { this.els = [] }
}
const resized = (el: Element) => act(() => { for (const ro of MockRO.all) if (ro.els.includes(el)) ro.cb() })

function geometry(box: HTMLElement, scrollHeight: number, clientHeight: number, scrollTop: number) {
  Object.defineProperty(box, 'scrollHeight', { configurable: true, value: scrollHeight })
  Object.defineProperty(box, 'clientHeight', { configurable: true, writable: true, value: clientHeight })
  Object.defineProperty(box, 'scrollTop', { configurable: true, writable: true, value: scrollTop })
}

const deckEntry = (): ConversationEntry => ({
  doc: { ...emptyDoc(), turns: [{ id: 't0', index: 0, started_at: 0, outcome: 'done', items: [userItem('a', 0)] }] },
  status: 'live', reason: '', paging: false, subagents: {},
})
const chatTurns: PanelTurn[] = [{ id: 't0', index: 0, items: [userItem('a', 0)] }]

const views = [
  ['deck', 'deck-scroll', () => render(<DeckView paneId="pin-deck" hostId="h" sessionId="s" entry={deckEntry()} onSwitchToTerminal={() => {}} />)],
  ['chat', 'chat-scroll', () => render(<ChatView paneKey="pin-chat" hostId="h" sessionId="s" title="t" status="idle" turns={chatTurns} onSwitchToTerminal={() => {}} active />)],
] as const

beforeEach(() => {
  cleanup()
  MockRO.all = []
  vi.stubGlobal('ResizeObserver', MockRO)
  Element.prototype.scrollTo = vi.fn() as unknown as Element['scrollTo']
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ width: 1000, height: 600, top: 0, left: 0, right: 1000, bottom: 600, x: 0, y: 0, toJSON: () => ({}) }))
  forgetScrollMemosWithPrefix('pin-')
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  delete (Element.prototype as { scrollTo?: unknown }).scrollTo
})

describe.each(views)('%s: the footer growing under the box', (_n, testId, mount) => {
  /** A box 400 high over 1000 of content, scrolled to `top`, with the reader's position known to the hook. */
  function setup(top: number) {
    mount()
    const box = screen.getByTestId(testId)
    geometry(box, 1000, 400, top)
    resized(box) // the observer's first report: 400 is the height "before"
    box.scrollTop = top
    fireEvent.scroll(box)
    return box
  }
  const setHeight = (box: HTMLElement, h: number) => { Object.defineProperty(box, 'clientHeight', { configurable: true, writable: true, value: h }); resized(box) }

  it('at the bottom: scrollTop goes back to the end after the box gets shorter', () => {
    const box = setup(600)
    setHeight(box, 300)
    expect(box.scrollTop).toBe(700)
  })

  it('at the bottom within the near-bottom slack: also pinned', () => {
    const box = setup(590)
    setHeight(box, 330)
    expect(box.scrollTop).toBe(670)
  })

  it('scrolled up: scrollTop does not move', () => {
    const box = setup(200)
    setHeight(box, 300)
    expect(box.scrollTop).toBe(200)
  })

  it('the footer shrinking again: still at the bottom', () => {
    const box = setup(600)
    setHeight(box, 300)
    setHeight(box, 400)
    expect(box.scrollTop).toBe(600)
  })

  it('a reader who scrolled up stays up through repeated growth', () => {
    const box = setup(600)
    box.scrollTop = 100
    fireEvent.scroll(box)
    setHeight(box, 300)
    setHeight(box, 250)
    expect(box.scrollTop).toBe(100)
  })
})
