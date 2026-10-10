// #2469: a turn far above the reader draws its agent text as plain text until it nears the viewport, then in full for good;
// the swap and the rendered markdown survive a tab switch (CLAUDE.md tab-hosted rule: the memory is outside the component, so
// this mounts the real TabContent). IntersectionObserver is stubbed: jsdom has none and there is no layout to ask.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import ReactMarkdown from 'react-markdown'
import { TabContent } from '../TabContent'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import { forgetReveals, isRevealed } from '../../lib/conversations/deck-reveal-memory'
import { clearMarkdownCache, markdownCacheSize } from '../../lib/conversations/markdown-cache'
import { forgetScrollMemosWithPrefix, readScrollMemo } from '../../lib/nex/transcript-scroll-memory'
import { emptyDoc } from '../../lib/conversations/model'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import { useConversationStore, type ConversationEntry } from '../../stores/useConversationStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { DeckView } from './DeckView'
import { EAGER_TURNS } from './deck-reveal'

// Counts every markdown render (react-markdown is called as a component and, by RoomProse's cache, as a function).
vi.mock('react-markdown', async (orig) => {
  const m = await orig<typeof import('react-markdown')>()
  return { ...m, default: vi.fn(m.default) }
})

interface FakeIO { cb: IntersectionObserverCallback; targets: Set<Element>; opts?: IntersectionObserverInit }
let observers: FakeIO[] = []
class StubIO {
  io: FakeIO
  constructor(cb: IntersectionObserverCallback, opts?: IntersectionObserverInit) {
    this.io = { cb, targets: new Set(), opts }
    observers.push(this.io)
  }
  observe(el: Element) { this.io.targets.add(el) }
  unobserve(el: Element) { this.io.targets.delete(el) }
  disconnect() { this.io.targets.clear() }
  takeRecords() { return [] }
}
/** The viewport reaches `el`. */
function approach(el: Element, isIntersecting = true) {
  const io = observers.find((o) => o.targets.has(el))
  if (!io) throw new Error('the turn is not observed')
  act(() => io.cb([{ target: el, isIntersecting } as IntersectionObserverEntry], io as unknown as IntersectionObserver))
}

const agent = (id: string, index: number, markdown: string, over: Partial<ConversationItem> = {}): ConversationItem =>
  ({ type: 'agent_text', id, at: 1, index, markdown, ...over }) as ConversationItem
const turn = (index: number, items: ConversationItem[]): Turn => ({ id: `t${index}`, index, started_at: index, outcome: 'done', items })
const turns = (n: number): Turn[] => Array.from({ length: n }, (_, i) => turn(i, [agent(`a${i}`, 0, `reply **${i}** with needle${i}`)]))
const entry = (ts: Turn[]): ConversationEntry => ({ doc: { ...emptyDoc(), turns: ts }, status: 'live', reason: '', paging: false, subagents: {} })

const KEY = 'p-deck\0s'
const props = { paneId: 'p-deck', hostId: 'h', sessionId: 's', onSwitchToTerminal: () => {} }
const turnEl = (i: number) => screen.getAllByTestId('deck-turn')[i]
const isLight = (i: number) => turnEl(i).querySelector('[data-testid="room-prose-light"]') !== null
const isFull = (i: number) => turnEl(i).querySelector('[data-testid="room-prose"]') !== null

beforeEach(() => {
  cleanup()
  observers = []
  vi.stubGlobal('IntersectionObserver', StubIO)
  forgetFolds(KEY)
  forgetReveals(KEY)
  clearMarkdownCache()
  forgetScrollMemosWithPrefix('p-deck')
  vi.mocked(ReactMarkdown).mockClear()
  useConversationStore.setState({ loadBefore: vi.fn(async () => {}) })
})
afterEach(() => { vi.unstubAllGlobals() })

describe('DeckView defers far-off markdown', () => {
  it('draws the newest turns in full and the older ones as plain text holding the whole source', () => {
    const n = EAGER_TURNS + 10
    render(<DeckView {...props} entry={entry(turns(n))} />)
    for (let i = 0; i < 10; i++) expect(isLight(i)).toBe(true)
    for (let i = 10; i < n; i++) expect(isFull(i)).toBe(true)
    // plain text keeps the markdown source, so the browser's find still reaches it
    expect(turnEl(3).textContent).toContain('reply **3** with needle3')
    // asked to be told within about one screen of the box
    expect(observers[0].opts?.rootMargin).toBe('100% 0px')
    expect(observers[0].opts?.root).toBe(screen.getByTestId('deck-scroll'))
  })

  it('swaps a turn to the real markdown when it nears, and only that turn', () => {
    render(<DeckView {...props} entry={entry(turns(EAGER_TURNS + 10))} />)
    approach(turnEl(4))
    expect(isFull(4)).toBe(true)
    expect(turnEl(4).querySelector('strong')?.textContent).toBe('4')
    expect(isLight(3)).toBe(true)
    expect(isLight(5)).toBe(true)
    expect(isRevealed(KEY, 't4')).toBe(true)
  })

  it('never goes back to plain text: not on a later report that it left, nor when a live turn lands', () => {
    const base = turns(EAGER_TURNS + 10)
    const { rerender } = render(<DeckView {...props} entry={entry(base)} />)
    const el = turnEl(4)
    approach(el)
    // a late report that it left the viewport (the observer no longer watches it, but a queued entry may still arrive)
    act(() => observers[0].cb([{ target: el, isIntersecting: false } as unknown as IntersectionObserverEntry], observers[0] as unknown as IntersectionObserver))
    expect(isFull(4)).toBe(true)
    rerender(<DeckView {...props} entry={entry([...base, turn(base.length, [agent('new', 0, 'fresh')])])} />)
    expect(isFull(4)).toBe(true)
    // the turn that fell out of the newest ones was drawn in full before, and stays so
    expect(isFull(10)).toBe(true)
    // a turn that never neared is still plain
    expect(isLight(5)).toBe(true)
  })

  it('moves the box by however far the item at its top moved when a swap lands, so the reader keeps their place', () => {
    render(<DeckView {...props} entry={entry(turns(EAGER_TURNS + 10))} />)
    const box = screen.getByTestId('deck-scroll')
    // turn 2 is the one at the top of the box: its first item shows, and the swap above it pushes that item down 55 px
    const section = turnEl(2)
    const item = section.firstElementChild as HTMLElement
    vi.spyOn(section, 'getBoundingClientRect').mockReturnValue({ top: -50, bottom: 500 } as DOMRect)
    let calls = 0
    vi.spyOn(item, 'getBoundingClientRect').mockImplementation(() => ({ top: calls++ === 0 ? 100 : 155, bottom: 300 } as DOMRect))
    expect(box.scrollTop).toBe(0)
    approach(section)
    expect(isFull(2)).toBe(true)
    expect(box.scrollTop).toBe(55)
  })

  it('holds the line under the reader when the message that is swapped is the one cut by the top of the box', () => {
    // jsdom has no layout: the hit test and the range rects are stubbed. The item keeps its top (-200) while the words move 55 px.
    const ts = turns(EAGER_TURNS + 10)
    ts[2] = turn(2, [agent('a2', 0, 'zebra quartz mango and more words')])
    render(<DeckView {...props} entry={entry(ts)} />)
    const box = screen.getByTestId('deck-scroll')
    const section = turnEl(2)
    const item = section.firstElementChild as HTMLElement
    vi.spyOn(section, 'getBoundingClientRect').mockReturnValue({ top: -250, bottom: 500 } as DOMRect)
    vi.spyOn(item, 'getBoundingClientRect').mockReturnValue({ top: -200, bottom: 300 } as DOMRect)
    const light = item.querySelector('[data-testid="room-prose-light"]')!.textContent!
    expect(light).toContain('zebra')
    const text = [...(function* () { const w = document.createTreeWalker(item, NodeFilter.SHOW_TEXT); for (let n = w.nextNode(); n; n = w.nextNode()) yield n })()][0]
    const range = document.createRange()
    range.setStart(text, 0)
    Object.defineProperty(document, 'caretRangeFromPoint', { configurable: true, value: () => range })
    Object.defineProperty(Range.prototype, 'getBoundingClientRect', {
      configurable: true,
      value(this: Range) { return { top: item.querySelector('[data-testid="room-prose-light"]') ? 100 : 155 } as DOMRect },
    })
    try {
      approach(section)
      expect(isFull(2)).toBe(true)
      expect(box.scrollTop).toBe(55)
    } finally {
      Reflect.deleteProperty(document, 'caretRangeFromPoint')
      Reflect.deleteProperty(Range.prototype, 'getBoundingClientRect')
    }
  })

  it('leaves the box alone when nothing under the reader moved', () => {
    render(<DeckView {...props} entry={entry(turns(EAGER_TURNS + 10))} />)
    const box = screen.getByTestId('deck-scroll')
    const section = turnEl(2)
    vi.spyOn(section, 'getBoundingClientRect').mockReturnValue({ top: -50, bottom: 500 } as DOMRect)
    vi.spyOn(section.firstElementChild as HTMLElement, 'getBoundingClientRect').mockReturnValue({ top: 100, bottom: 300 } as DOMRect)
    approach(section)
    expect(isFull(2)).toBe(true)
    expect(box.scrollTop).toBe(0)
  })

  it('draws streaming text in full whatever the turn', () => {
    const ts = turns(EAGER_TURNS + 3)
    ts[1] = turn(1, [agent('s1', 0, 'typing', { streaming: true })])
    render(<DeckView {...props} entry={entry(ts)} />)
    expect(isFull(1)).toBe(true)
    expect(isLight(0)).toBe(true)
  })

  it('does not put streaming text in the markdown cache, and does once it finished', () => {
    const live = (streaming: boolean) => entry([turn(0, [agent('s1', 0, 'typing', { streaming })])])
    const { rerender } = render(<DeckView {...props} entry={live(true)} />)
    expect(markdownCacheSize()).toBe(0)
    rerender(<DeckView {...props} entry={live(false)} />)
    expect(markdownCacheSize()).toBe(1)
  })
})

describe('a tab switch', () => {
  const H = 'h'
  function DeckRenderer({ pane }: PaneRendererProps) {
    if (pane.content.kind !== 'execution') return null
    return <DeckView paneId={pane.id} hostId={H} sessionId="s" entry={entry(turns(EAGER_TURNS + 10))} onSwitchToTerminal={() => {}} />
  }
  const deckTab: Tab = { ...createTab({ kind: 'execution', executionId: 'x', host: H }), id: 't-deck' }
  const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
  const all = [deckTab, dashTab]
  const pane = (deckTab.layout as { pane: { id: string } }).pane.id

  beforeEach(() => {
    clearModuleRegistry()
    useUISettingsStore.setState({ keepAliveCount: 0 })
    useShownHostsStore.setState({ ids: [H] })
    useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
    registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: DeckRenderer }] })
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => <div data-testid="other-tab" /> }] })
    forgetFolds(`${pane}\0s`)
    forgetReveals(`${pane}\0s`)
    forgetScrollMemosWithPrefix(pane)
  })
  afterEach(() => {
    forgetReveals(`${pane}\0s`)
    forgetScrollMemosWithPrefix(pane)
  })

  it('brings back a swapped turn in full at once and parses no markdown again', () => {
    const { rerender } = render(<TabContent activeTab={deckTab} allTabs={all} />)
    approach(turnEl(4))
    expect(isFull(4)).toBe(true)
    const parsed = vi.mocked(ReactMarkdown).mock.calls.length
    expect(parsed).toBeGreaterThan(0)

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('deck-scroll')).toBeNull()
    observers = []
    vi.mocked(ReactMarkdown).mockClear()
    rerender(<TabContent activeTab={deckTab} allTabs={all} />)

    expect(isFull(4)).toBe(true) // swapped before the switch: in full from the first paint, without a new report
    expect(isLight(5)).toBe(true) // never neared: still plain
    for (let i = 10; i < EAGER_TURNS + 10; i++) expect(isFull(i)).toBe(true)
    // every full turn came out of the cache: react-markdown ran for none of them
    expect(ReactMarkdown).not.toHaveBeenCalled()
    // and the far-off turn can still be swapped later
    approach(turnEl(5))
    expect(isFull(5)).toBe(true)
  })

  it('keeps where the reader was scrolled', () => {
    const { rerender } = render(<TabContent activeTab={deckTab} allTabs={all} />)
    const box = screen.getByTestId('deck-scroll')
    Object.defineProperty(box, 'scrollHeight', { configurable: true, value: 5000 })
    Object.defineProperty(box, 'clientHeight', { configurable: true, value: 500 })
    Object.defineProperty(box, 'scrollTop', { configurable: true, writable: true, value: 1200 })
    fireEvent.scroll(box)
    expect(readScrollMemo(`${pane}\0s`)).toMatchObject({ scrollTop: 1200, atBottom: false, view: 'deck' })
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(readScrollMemo(`${pane}\0s`)).toMatchObject({ scrollTop: 1200, atBottom: false })
    // the memory is read back: the new box is placed at it (jsdom has no layout, so the stored scrollTop is what is used)
    const scrollTo = vi.fn()
    Object.defineProperty(HTMLElement.prototype, 'scrollTo', { configurable: true, value: scrollTo })
    try {
      rerender(<TabContent activeTab={deckTab} allTabs={all} />)
      expect(scrollTo).toHaveBeenCalledWith({ top: 1200, behavior: 'auto' })
    } finally {
      Reflect.deleteProperty(HTMLElement.prototype, 'scrollTo')
    }
  })
})
