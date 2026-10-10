// v3 resume scroll: coming back to a deck / chat tab (the pane unmounts on a switch, the real TabContent here) puts the reader
//  (A) at the bottom at once when they left following the bottom — never sliding down from an old position, whether the
//      conversation was already loaded when the tab came back or arrived (and grew) after the box was drawn;
//  (B) where they were when they left scrolled up — the turn they were on keeps its place, however much landed meanwhile.
// jsdom has no layout: a fake one (every turn 100 high, a swapped-out light turn 60, the box 400 high) behind the prototype's
// geometry, with every scrollTop write and scrollTo call recorded so that "no middle position" can be asserted.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { useSyncExternalStore } from 'react'
import { TabContent } from '../TabContent'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import { forgetRevealsOfPane } from '../../lib/conversations/deck-reveal-memory'
import { forgetScrollMemosWithPrefix } from '../../lib/nex/transcript-scroll-memory'
import { emptyDoc } from '../../lib/conversations/model'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import type { PanelTurn } from '../../lib/conversations/panel-resolve'
import type { ConversationEntry } from '../../stores/useConversationStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { DeckView } from './DeckView'
import { ChatView } from './ChatView'

const H = 'h'
const BOX = 400
const TURN = 100
const LIGHT = 60

// ---- a fake layout ----
const tops = new WeakMap<Element, number>()
let sets: number[] = []
let calls: Array<ScrollToOptions> = []
const isBox = (el: Element) => (el.getAttribute('data-testid') ?? '').endsWith('-scroll')
const turnsOf = (box: Element) => [...box.querySelectorAll<HTMLElement>('[data-turn-index]')]
const heightOf = (turn: Element) => (turn.querySelector('[data-testid="room-prose-light"]') ? LIGHT : TURN)
const scrollHeightOf = (box: Element) => turnsOf(box).reduce((s, t) => s + heightOf(t), 0)
const maxTop = (box: Element) => Math.max(0, scrollHeightOf(box) - BOX)
function setTop(el: Element, v: number) {
  tops.set(el, isBox(el) ? Math.min(Math.max(v, 0), maxTop(el)) : v)
}
/** The box's top is 0; a turn sits after the heights of the turns before it. */
function rectOf(el: Element): DOMRect {
  let top = 0
  let bottom = 0
  if (isBox(el)) { top = 0; bottom = BOX } else {
    const turn = el.closest('[data-turn-index]')
    const box = el.closest('[data-testid$="-scroll"]')
    if (turn && box) {
      let y = 0
      for (const t of turnsOf(box)) { if (t === turn) break; y += heightOf(t) }
      top = y - (tops.get(box) ?? 0)
      bottom = top + heightOf(turn)
    }
  }
  return { top, bottom, left: 0, right: 700, width: 700, height: bottom - top, x: 0, y: top, toJSON: () => ({}) } as DOMRect
}
function installLayout() {
  sets = []
  calls = []
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', { configurable: true, get(this: HTMLElement) { return isBox(this) ? scrollHeightOf(this) : 0 } })
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, get(this: HTMLElement) { return isBox(this) ? BOX : 0 } })
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get(this: HTMLElement) { return tops.get(this) ?? 0 },
    set(this: HTMLElement, v: number) { setTop(this, v); sets.push(tops.get(this) ?? 0) },
  })
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
    configurable: true,
    value(this: HTMLElement, o: ScrollToOptions) { calls.push(o); setTop(this, o.top ?? 0); sets.push(tops.get(this) ?? 0) },
  })
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) { return rectOf(this) })
}
function removeLayout() {
  for (const k of ['scrollHeight', 'clientHeight', 'scrollTop', 'scrollTo']) Reflect.deleteProperty(HTMLElement.prototype, k)
  vi.restoreAllMocks()
}
/** The reader scrolls the box to `top` (a scroll event, as the browser sends it). */
function scrollBoxTo(box: HTMLElement, top: number) {
  tops.set(box, Math.min(top, maxTop(box)))
  fireEvent.scroll(box)
}
const turnTop = (box: HTMLElement, i: number) => {
  const t = box.querySelector(`[data-turn-index="${i}"]`) as HTMLElement
  return t.getBoundingClientRect().top
}

// ---- the conversation behind the pane: a store the renderer subscribes to ----
type Shown = { entry: ConversationEntry; turns: PanelTurn[] }
let shown: Shown
const subs = new Set<() => void>()
const show = (s: Shown) => act(() => { shown = s; subs.forEach((f) => f()) })
const subscribe = (f: () => void) => { subs.add(f); return () => { subs.delete(f) } }

const user = (id: string, index: number): ConversationItem => ({ type: 'user', id, at: 1, index, text: id, source: 'user' })
const mkTurns = (n: number): Turn[] => Array.from({ length: n }, (_, i) => ({ id: `t${i}`, index: i, started_at: i, outcome: 'done' as const, items: [user(`u${i}`, 0)] }))
const loaded = (n: number): Shown => ({
  entry: { doc: { ...emptyDoc(), turns: mkTurns(n) }, status: 'live', reason: '', paging: false, subagents: {} },
  turns: mkTurns(n) as PanelTurn[],
})
/** The moment after a re-open: the conversation is acquired but nothing has arrived. */
const loading = (): Shown => ({
  entry: { doc: emptyDoc(), status: 'loading', reason: '', paging: false, subagents: {} },
  turns: [],
})

const deckTab: Tab = { ...createTab({ kind: 'execution', executionId: 'x', host: H }), id: 't-deck' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [deckTab, dashTab]
const pane = (deckTab.layout as { pane: { id: string } }).pane.id

function Deck({ pane: p }: PaneRendererProps) {
  const s = useSyncExternalStore(subscribe, () => shown)
  return <DeckView paneId={p.id} hostId={H} sessionId="s" entry={s.entry} onSwitchToTerminal={() => {}} />
}
function Chat({ pane: p }: PaneRendererProps) {
  const s = useSyncExternalStore(subscribe, () => shown)
  return <ChatView paneKey={p.id} hostId={H} sessionId="s" title="t" status="idle" turns={s.turns} onSwitchToTerminal={() => {}} active />
}

const views = [
  ['deck', 'deck-scroll', Deck],
  ['chat', 'chat-scroll', Chat],
] as const

class NoRO { observe() {} unobserve() {} disconnect() {} }

beforeEach(() => {
  cleanup()
  installLayout()
  vi.stubGlobal('ResizeObserver', NoRO)
  clearModuleRegistry()
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => <div data-testid="other-tab" /> }] })
  forgetFolds(`${pane}\0s`)
  forgetRevealsOfPane(pane)
  forgetScrollMemosWithPrefix(pane)
})
afterEach(() => {
  vi.unstubAllGlobals()
  removeLayout()
  forgetRevealsOfPane(pane)
  forgetScrollMemosWithPrefix(pane)
})

describe.each(views)('%s: coming back to the tab', (_name, testId, Renderer) => {
  const mounted = () => screen.getByTestId(testId)
  function open() {
    registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: Renderer }] })
    show(loaded(40))
    return render(<TabContent activeTab={deckTab} allTabs={all} />)
  }
  const leave = (r: ReturnType<typeof render>) => {
    r.rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId(testId)).toBeNull()
  }
  const back = (r: ReturnType<typeof render>) => r.rerender(<TabContent activeTab={deckTab} allTabs={all} />)
  /** What the log of writes since the last `mark()` may say: at the end of 60 turns, and only there. */
  const END = 60 * TURN - BOX
  const mark = () => { sets = []; calls = [] }
  const expectOnlyEnd = () => {
    expect(calls.filter((c) => c.behavior === 'smooth')).toEqual([])
    expect([...new Set(sets)].filter((v) => v !== END)).toEqual([])
  }

  describe('(A) left following the bottom', () => {
    it('already loaded when the tab comes back, 20 turns added meanwhile: at the end at once', () => {
      const r = open()
      scrollBoxTo(mounted(), 40 * TURN - BOX)
      leave(r)
      show(loaded(60))
      mark()
      back(r)
      expect(mounted().scrollTop).toBe(END)
      expectOnlyEnd()
    })

    it('the conversation arrives after the box is drawn (re-opened, loading first): no slide down from the top', () => {
      const r = open()
      scrollBoxTo(mounted(), 40 * TURN - BOX)
      leave(r)
      show(loading())
      mark()
      back(r)
      show(loaded(60))
      expect(mounted().scrollTop).toBe(END)
      expectOnlyEnd()
    })

    it('20 turns landing in one go after the box is placed: jumps, does not slide', () => {
      const r = open()
      scrollBoxTo(mounted(), 40 * TURN - BOX)
      leave(r)
      show(loaded(40))
      back(r)
      fireEvent.scroll(mounted()) // the browser's own report of the placement
      mark()
      show(loaded(60))
      expect(mounted().scrollTop).toBe(END)
      expectOnlyEnd()
    })

    it('one more turn landing under a reader at the bottom still follows smoothly', () => {
      const r = open()
      scrollBoxTo(mounted(), 40 * TURN - BOX)
      leave(r)
      back(r)
      fireEvent.scroll(mounted()) // the browser's own report of the placement
      mark()
      show(loaded(41))
      expect(calls.some((c) => c.behavior === 'smooth')).toBe(true)
    })
  })

  describe('(B) left scrolled up on turn 10', () => {
    const OFFSET = -30 // turn 10's top relative to the box when the reader left
    const scrollUp = () => scrollBoxTo(mounted(), 10 * TURN - OFFSET)

    it('already loaded when the tab comes back, 20 turns added below: turn 10 has not moved', () => {
      const r = open()
      scrollUp()
      expect(turnTop(mounted(), 10)).toBe(OFFSET)
      leave(r)
      show(loaded(60))
      back(r)
      expect(turnTop(mounted(), 10)).toBe(OFFSET)
    })

    it('the conversation arrives after the box is drawn: turn 10 lands where it was, nothing slides to the end', () => {
      const r = open()
      scrollUp()
      leave(r)
      show(loading())
      mark()
      back(r)
      show(loaded(60))
      expect(turnTop(mounted(), 10)).toBe(OFFSET)
      expect(calls.filter((c) => c.behavior === 'smooth')).toEqual([])
      expect(sets.includes(END)).toBe(false)
    })

    it('content landing below afterwards does not drag the reader down', () => {
      const r = open()
      scrollUp()
      leave(r)
      back(r)
      show(loaded(60))
      expect(turnTop(mounted(), 10)).toBe(OFFSET)
    })
  })
})
