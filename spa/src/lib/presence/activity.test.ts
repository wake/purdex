import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createActivityTracker } from './activity'

// PU-4 Task 1: whether the user is at this window — focused and with input in the last 120 s.

let focused = true
const hasFocus = vi.spyOn(document, 'hasFocus')

function key() { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' })) }
function down() { window.dispatchEvent(new Event('pointerdown')) }
function move() { window.dispatchEvent(new Event('pointermove')) }
function wheel() { window.dispatchEvent(new Event('wheel')) }

describe('activity tracker', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(1_000_000)
    focused = true
    hasFocus.mockImplementation(() => focused)
  })
  afterEach(() => { vi.useRealTimers() })

  it('is not active before any input, even when the window has focus', () => {
    const t = createActivityTracker()
    expect(t.isActive()).toBe(false)
    t.dispose()
  })

  it.each([['keydown', key], ['pointerdown', down], ['pointermove', move], ['wheel', wheel]])('%s makes it active', (_n, fire) => {
    const t = createActivityTracker()
    fire()
    expect(t.isActive()).toBe(true)
    t.dispose()
  })

  it('stays active for 120 s of silence and not a moment longer', () => {
    const t = createActivityTracker()
    key()
    vi.advanceTimersByTime(120_000)
    expect(t.isActive()).toBe(true)
    vi.advanceTimersByTime(1)
    expect(t.isActive()).toBe(false)
    t.dispose()
  })

  it('needs the window to have focus: input without focus is not presence', () => {
    const t = createActivityTracker()
    key()
    focused = false
    expect(t.isActive()).toBe(false)
    focused = true
    expect(t.isActive()).toBe(true)
    t.dispose()
  })

  it('pointermove is throttled but still counts as input once the throttle has passed', () => {
    const t = createActivityTracker()
    key()
    vi.advanceTimersByTime(119_000)
    move() // inside the throttle of nothing yet: counts, restarting the 120 s
    vi.advanceTimersByTime(100_000)
    expect(t.isActive()).toBe(true)
    t.dispose()
  })

  it('a flood of pointermove events is reduced: at most one counted per second', () => {
    const now = vi.fn(() => Date.now())
    const t = createActivityTracker({ now })
    move()
    const first = t.lastInputAt()
    vi.advanceTimersByTime(500)
    move()
    expect(t.lastInputAt()).toBe(first)
    vi.advanceTimersByTime(600)
    move()
    expect(t.lastInputAt()).toBeGreaterThan(first)
    t.dispose()
  })

  it('tells a subscriber when the answer changes: input after idle, focus gained, focus lost', () => {
    const t = createActivityTracker()
    const seen: boolean[] = []
    const off = t.subscribe((active) => seen.push(active))
    key() // idle -> active
    key() // already active: nothing new
    expect(seen).toEqual([true])
    focused = false
    window.dispatchEvent(new Event('blur'))
    expect(seen).toEqual([true, false])
    focused = true
    window.dispatchEvent(new Event('focus'))
    expect(seen).toEqual([true, false, true])
    off()
    focused = false
    window.dispatchEvent(new Event('blur'))
    expect(seen).toHaveLength(3)
    t.dispose()
  })

  it('tells a subscriber when the 120 s run out, without another event', () => {
    const t = createActivityTracker()
    const seen: boolean[] = []
    t.subscribe((active) => seen.push(active))
    key()
    expect(seen).toEqual([true])
    vi.advanceTimersByTime(120_000)
    expect(seen).toEqual([true])
    vi.advanceTimersByTime(2)
    expect(seen).toEqual([true, false])
    t.dispose()
  })

  it('later input pushes the expiry back', () => {
    const t = createActivityTracker()
    const seen: boolean[] = []
    t.subscribe((active) => seen.push(active))
    key()
    vi.advanceTimersByTime(100_000)
    key()
    vi.advanceTimersByTime(100_000)
    expect(seen).toEqual([true])
    vi.advanceTimersByTime(21_000)
    expect(seen).toEqual([true, false])
    t.dispose()
  })

  it('dispose cancels the expiry timer', () => {
    const t = createActivityTracker()
    key()
    t.dispose()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('stops listening on dispose', () => {
    const t = createActivityTracker()
    t.dispose()
    key()
    expect(t.isActive()).toBe(false)
  })
})
