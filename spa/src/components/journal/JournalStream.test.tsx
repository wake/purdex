// spa/src/components/journal/JournalStream.test.tsx — the stream's scrolling: open at the bottom, follow while there, the pill while
// away, the previous month keeps the reading position, and the memo that survives an unmount (J-2c, spec §11).
// jsdom has no layout: every divider / row is 20px tall, the viewport 100px, and scrollTop clamps like a browser's.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useImperativeHandle, useState } from 'react'
import { JournalStream } from './JournalStream'
import type { RowContext } from './JournalRow'
import { clearViewMemos, patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { useI18nStore } from '../../stores/useI18nStore'
import type { JournalEntry } from '../../lib/journal/types'

const H = 'h1'
const K = 'solo:a'
const ROW = 20
const VIEW = 100
const scrollTops = new WeakMap<Element, number>()
const heightOf = (el: Element) => el.querySelectorAll('[data-testid="journal-entry"], [data-testid="journal-day"]').length * ROW
const maxTop = (el: Element) => Math.max(0, heightOf(el) - VIEW)

const D = new Date(2026, 9, 14, 9, 0).getTime()
const entry = (n: number, over: Partial<JournalEntry> = {}): JournalEntry => ({
  id: `e${String(n).padStart(3, '0')}`, kind: 'exec', by: '', at: D + n * 60_000, actor: 'Adam', text: `event ${n}`, refs: [], items: [], detail: '', pinned: false, ...over,
})
const range = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, i) => entry(from + i))
const ctx: RowContext = { personName: 'Wake', remoteUrl: '', selectedId: null, onToggleDetail: () => {}, onTogglePin: () => {} }

/** The test pushes a new list (or switches the mounted stream to another unit) through this handle. */
interface Handle { set: (e: JournalEntry[]) => void; switchTo: (unit: string, e: JournalEntry[]) => void }
const handle: { current: Handle | null } = { current: null }
let loadOlder = vi.fn<() => Promise<unknown>>()
function Harness({ initial, more = false, filter = 'all' }: { initial: JournalEntry[]; more?: boolean; filter?: string }) {
  const [state, setState] = useState({ unit: K, entries: initial })
  useImperativeHandle(handle as React.Ref<Handle>, () => ({
    set: (entries) => setState((s) => ({ ...s, entries })),
    switchTo: (unit, entries) => setState({ unit, entries }),
  }), [])
  return <JournalStream host={H} unitKey={state.unit} entries={state.entries} filter={filter} more={more} onLoadOlder={loadOlder} ctx={ctx} />
}
const stream = () => screen.getByTestId('journal-stream')
const top = () => stream().scrollTop
const scrollTo = (n: number) => { stream().scrollTop = n; fireEvent.scroll(stream()) }
const push = (e: JournalEntry[]) => act(() => { handle.current?.set(e) })
const switchTo = (unit: string, e: JournalEntry[]) => act(() => { handle.current?.switchTo(unit, e) })

beforeEach(() => {
  cleanup()
  clearViewMemos()
  useI18nStore.getState().setLocale('en')
  loadOlder = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', { configurable: true, get() { return heightOf(this) } })
  // A row / divider starts below the rows and dividers before it.
  Object.defineProperty(HTMLElement.prototype, 'offsetTop', {
    configurable: true,
    get() {
      const all = Array.from(this.closest('[data-testid="journal-stream"]')?.querySelectorAll('[data-testid="journal-entry"], [data-testid="journal-day"]') ?? [])
      return Math.max(0, all.indexOf(this)) * ROW
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, get() { return VIEW } })
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get() { return scrollTops.get(this) ?? 0 },
    set(v: number) { scrollTops.set(this, Math.max(0, Math.min(maxTop(this), v))) },
  })
})
afterEach(() => {
  cleanup()
  delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollHeight
  delete (HTMLElement.prototype as unknown as Record<string, unknown>).clientHeight
  delete (HTMLElement.prototype as unknown as Record<string, unknown>).offsetTop
  delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollTop
})

describe('the stream', () => {
  it('draws oldest to newest with one divider per local day', () => {
    const next = new Date(2026, 9, 15, 8, 0).getTime()
    render(<Harness initial={[entry(1), entry(2), entry(3, { at: next })]} />)
    const rows = screen.getAllByTestId(/^journal-(entry|day)$/).map((n) => n.getAttribute('data-entry-id') ?? n.textContent)
    expect(rows).toEqual(['2026-10-14', 'e001', 'e002', '2026-10-15', 'e003'])
  })
  it('opens scrolled to the bottom', () => {
    render(<Harness initial={range(1, 12)} />)
    expect(top()).toBe(maxTop(stream()))
    expect(top()).toBeGreaterThan(0)
  })
  it('at the bottom a new event is followed, with no pill', () => {
    render(<Harness initial={range(1, 12)} />)
    push(range(1, 13))
    expect(top()).toBe(maxTop(stream()))
    expect(screen.queryByTestId('journal-new-pill')).toBeNull()
  })
  it('away from the bottom a new event shows a pill with the count; a click goes to the bottom and clears it (M: always follow)', () => {
    render(<Harness initial={range(1, 12)} />)
    scrollTo(40)
    expect(readViewMemo(H, K).pinnedToBottom).toBe(false)
    push(range(1, 13))
    expect(screen.getByTestId('journal-new-pill').textContent).toContain('1')
    expect(top()).toBe(40)
    push(range(1, 15))
    expect(screen.getByTestId('journal-new-pill').textContent).toContain('3')
    fireEvent.click(screen.getByTestId('journal-new-pill'))
    expect(top()).toBe(maxTop(stream()))
    expect(screen.queryByTestId('journal-new-pill')).toBeNull()
    expect(readViewMemo(H, K).pinnedToBottom).toBe(true)
    push(range(1, 16))
    expect(top()).toBe(maxTop(stream()))
  })
  it('scrolling back to the bottom by hand clears the pill', () => {
    render(<Harness initial={range(1, 12)} />)
    scrollTo(40)
    push(range(1, 13))
    expect(screen.getByTestId('journal-new-pill')).toBeTruthy()
    scrollTo(maxTop(stream()))
    expect(screen.queryByTestId('journal-new-pill')).toBeNull()
  })
})

describe('switching the mounted stream to another unit (a tab switch)', () => {
  const B = 'solo:b'
  const other = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, i) => entry(from + i, { id: `b${String(from + i).padStart(3, '0')}` }))
  it('the other unit\'s whole list is not "new events"; it opens at its own place (M: no unit-change detection)', () => {
    render(<Harness initial={range(1, 12)} />)
    switchTo(B, other(1, 14))
    scrollTo(40) // B: the reader leaves the bottom
    switchTo(K, range(1, 12))
    expect(screen.queryByTestId('journal-new-pill')).toBeNull()
    expect(top()).toBe(maxTop(stream())) // A was left at its bottom
    switchTo(B, other(1, 14)) // B was left away from the bottom
    expect(screen.queryByTestId('journal-new-pill')).toBeNull()
    expect(top()).toBe(40)
    push(other(1, 15)) // a real new event in B still counts
    expect(screen.getByTestId('journal-new-pill').textContent).toContain('1')
  })
  it('a unit not seen before opens at the bottom with no pill', () => {
    render(<Harness initial={range(1, 12)} />)
    scrollTo(40)
    switchTo(B, other(1, 18))
    expect(screen.queryByTestId('journal-new-pill')).toBeNull()
    expect(top()).toBe(maxTop(stream()))
  })
  it('a pill of the unit that was left does not follow to the other one', () => {
    render(<Harness initial={range(1, 12)} />)
    scrollTo(40)
    push(range(1, 13))
    expect(screen.getByTestId('journal-new-pill')).toBeTruthy()
    switchTo(B, other(1, 18))
    expect(screen.queryByTestId('journal-new-pill')).toBeNull()
  })
})

describe('saving the scroll position', () => {
  afterEach(() => { vi.useRealTimers() })
  it('a burst of scrolls saves the last position after the debounce, and the next burst saves again', () => {
    vi.useFakeTimers()
    render(<Harness initial={range(1, 12)} />)
    scrollTo(40) // leaves the bottom: saved at once
    scrollTo(45)
    scrollTo(50)
    expect(readViewMemo(H, K).scroll).toBe(40)
    act(() => { vi.advanceTimersByTime(200) })
    expect(readViewMemo(H, K).scroll).toBe(50)
    scrollTo(55)
    act(() => { vi.advanceTimersByTime(200) })
    expect(readViewMemo(H, K).scroll).toBe(55)
  })
  it('a pending save that a unit switch cancelled does not stop the other unit\'s positions from being saved (M: the timer is not cleared out of its ref)', () => {
    vi.useFakeTimers()
    render(<Harness initial={range(1, 12)} />)
    scrollTo(40)
    scrollTo(45) // a save is pending
    act(() => { handle.current?.switchTo('solo:b', range(1, 12)) }) // the switch cancels it (and saves what A had)
    expect(readViewMemo(H, K).scroll).toBe(45)
    scrollTo(30) // leaves the bottom of B: saved at once
    scrollTo(35) // a plain scroll: saved after the debounce
    act(() => { vi.advanceTimersByTime(200) })
    expect(readViewMemo(H, 'solo:b').scroll).toBe(35)
  })
})

describe('the previous month', () => {
  it('scrolling to the top asks for it once, and the reading position holds when it arrives (M: scrollTop not compensated)', async () => {
    let finish: () => void = () => {}
    loadOlder.mockImplementation(() => new Promise<void>((res) => { finish = res }))
    render(<Harness initial={range(10, 21)} more />)
    scrollTo(60)
    scrollTo(0)
    scrollTo(0)
    expect(loadOlder).toHaveBeenCalledTimes(1)
    const before = heightOf(stream())
    push(range(1, 21)) // nine older rows come in above
    expect(heightOf(stream()) - before).toBe(9 * ROW)
    expect(top()).toBe(9 * ROW) // the row that was at the top of the reader's view is still there
    await act(async () => { finish() })
    scrollTo(0)
    expect(loadOlder).toHaveBeenCalledTimes(2)
  })
  it('a month arriving together with a new event: the reading position holds and the new event is still counted (M: the early return skipped the count)', () => {
    loadOlder.mockImplementation(() => new Promise<void>(() => {}))
    render(<Harness initial={range(10, 21)} more />)
    scrollTo(60)
    scrollTo(0)
    expect(loadOlder).toHaveBeenCalledTimes(1)
    push(range(1, 22)) // nine older rows above, one new row below
    expect(top()).toBe(9 * ROW)
    expect(screen.getByTestId('journal-new-pill').textContent).toContain('1')
  })
  it('a month arriving together with a new event, the reader at the bottom: follows the new event', () => {
    loadOlder.mockImplementation(() => new Promise<void>(() => {}))
    render(<Harness initial={range(10, 12)} more />) // short: the top is the bottom
    scrollTo(0)
    expect(readViewMemo(H, K).pinnedToBottom).toBe(true)
    push(range(1, 16))
    expect(top()).toBe(maxTop(stream()))
    expect(screen.queryByTestId('journal-new-pill')).toBeNull()
  })
  it('a month with nothing the filter shows does not strand the reader at the top: the next month is asked for, and the position holds when a row arrives (M: no second ask)', async () => {
    let n = 0
    loadOlder.mockImplementation(async () => { n++; await Promise.resolve(); if (n === 3) await push(range(1, 21)) }) // months 1 and 2 add no row under this filter
    render(<Harness initial={range(10, 21)} more filter="exec" />)
    scrollTo(60)
    scrollTo(0)
    await waitFor(() => expect(loadOlder).toHaveBeenCalledTimes(3))
    await waitFor(() => expect(top()).toBe(9 * ROW))
    expect(loadOlder).toHaveBeenCalledTimes(3)
  })
  it('keeps asking only so many times in a row when no month has a matching row', async () => {
    render(<Harness initial={range(10, 21)} more filter="exec" />)
    scrollTo(60)
    scrollTo(0)
    await waitFor(() => expect(loadOlder).toHaveBeenCalledTimes(5))
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(loadOlder).toHaveBeenCalledTimes(5)
    scrollTo(0) // the reader asks again by hand: a new run
    await waitFor(() => expect(loadOlder).toHaveBeenCalledTimes(10))
  })
  it('with nothing older, the top asks for nothing', () => {
    render(<Harness initial={range(10, 21)} more={false} />)
    scrollTo(0)
    expect(loadOlder).not.toHaveBeenCalled()
    expect(screen.queryByTestId('journal-load-older')).toBeNull()
  })
  it('the button loads it too', () => {
    render(<Harness initial={range(10, 21)} more />)
    fireEvent.click(screen.getByTestId('journal-load-older'))
    expect(loadOlder).toHaveBeenCalledTimes(1)
  })
})

describe('what survives an unmount (the memo)', () => {
  it('a reader away from the bottom comes back to the same place', () => {
    const { unmount } = render(<Harness initial={range(1, 12)} />)
    scrollTo(40)
    unmount()
    expect(readViewMemo(H, K)).toMatchObject({ scroll: 40, pinnedToBottom: false })
    render(<Harness initial={range(1, 12)} />)
    expect(top()).toBe(40)
  })
  it('a reader at the bottom comes back to the bottom, even when entries arrived meanwhile', () => {
    const { unmount } = render(<Harness initial={range(1, 12)} />)
    unmount()
    render(<Harness initial={range(1, 20)} />)
    expect(top()).toBe(maxTop(stream()))
  })
  it('another filter starts at its own bottom', () => {
    const { rerender } = render(<Harness initial={range(1, 12)} filter="all" />)
    scrollTo(40)
    rerender(<Harness initial={range(1, 12)} filter="exec" />)
    expect(top()).toBe(maxTop(stream()))
    expect(readViewMemo(H, K).pinnedToBottom).toBe(true)
  })
  it('shows an empty note, and says nothing matches under a filter', () => {
    patchViewMemo(H, K, { filter: 'confirm' })
    const { rerender } = render(<Harness initial={[]} filter="all" />)
    expect(screen.getByTestId('journal-empty').textContent).toBe('Nothing in the journal yet')
    rerender(<Harness initial={[]} filter="confirm" />)
    expect(screen.getByTestId('journal-empty').textContent).toBe('No matching events')
  })
})
