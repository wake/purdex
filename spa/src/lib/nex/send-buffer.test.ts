import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { holdSend, flushSend, cancelSend, hasHeldSend, clearAllHeldSends } from './send-buffer'

const entry = () => ({ fire: vi.fn(), undo: vi.fn() })

beforeEach(() => { vi.useFakeTimers(); clearAllHeldSends() })
afterEach(() => { clearAllHeldSends(); vi.useRealTimers() })

describe('send buffer registry', () => {
  it('fires after the window, and only then', () => {
    const e = entry()
    holdSend('k', e, 3000)
    vi.advanceTimersByTime(2999)
    expect(e.fire).not.toHaveBeenCalled()
    expect(hasHeldSend('k')).toBe(true)
    vi.advanceTimersByTime(1)
    expect(e.fire).toHaveBeenCalledTimes(1)
    expect(hasHeldSend('k')).toBe(false)
  })

  it('cancel undoes it, never fires it, and a second cancel finds nothing', () => {
    const e = entry()
    holdSend('k', e, 3000)
    expect(cancelSend('k')).toBe(true)
    expect(e.undo).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(10_000)
    expect(e.fire).not.toHaveBeenCalled()
    expect(cancelSend('k')).toBe(false)
  })

  it('cancel after the window ended is a no-op (the message is already out)', () => {
    const e = entry()
    holdSend('k', e, 1000)
    vi.advanceTimersByTime(1000)
    expect(cancelSend('k')).toBe(false)
    expect(e.undo).not.toHaveBeenCalled()
  })

  it('flush commits at once and the timer does not fire it twice', () => {
    const e = entry()
    holdSend('k', e, 3000)
    expect(flushSend('k')).toBe(true)
    expect(e.fire).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(10_000)
    expect(e.fire).toHaveBeenCalledTimes(1)
    expect(flushSend('k')).toBe(false)
  })

  it('holding a second message commits the first at once; cancel then takes back only the second', () => {
    const a = entry(), b = entry()
    holdSend('k', a, 3000)
    holdSend('k', b, 3000)
    expect(a.fire).toHaveBeenCalledTimes(1)
    expect(b.fire).not.toHaveBeenCalled()
    cancelSend('k')
    expect(b.undo).toHaveBeenCalledTimes(1)
    expect(a.undo).not.toHaveBeenCalled()
    vi.advanceTimersByTime(10_000)
    expect(b.fire).not.toHaveBeenCalled()
    expect(a.fire).toHaveBeenCalledTimes(1)
  })

  it('executions are independent', () => {
    const a = entry(), b = entry()
    holdSend('a', a, 3000)
    holdSend('b', b, 3000)
    cancelSend('a')
    vi.advanceTimersByTime(3000)
    expect(a.fire).not.toHaveBeenCalled()
    expect(b.fire).toHaveBeenCalledTimes(1)
  })
})
