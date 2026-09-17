// spa/src/hooks/useElapsedTicker.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useElapsedTicker } from './useElapsedTicker'

const T0 = new Date('2026-09-18T00:00:00Z').getTime()

describe('useElapsedTicker', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(T0)
  })
  afterEach(() => { vi.useRealTimers() })

  it('returns Date.now() at mount', () => {
    const { result } = renderHook(() => useElapsedTicker(false))
    expect(result.current).toBe(T0)
  })

  it('inactive → value stays stable across 5 s and schedules no timer', () => {
    const { result } = renderHook(() => useElapsedTicker(false))
    expect(vi.getTimerCount()).toBe(0)
    act(() => { vi.advanceTimersByTime(5_000) })
    expect(result.current).toBe(T0)
  })

  it('active → re-samples about every 1000 ms', () => {
    const { result } = renderHook(() => useElapsedTicker(true))
    expect(result.current).toBe(T0)
    act(() => { vi.advanceTimersByTime(999) })
    expect(result.current).toBe(T0)
    act(() => { vi.advanceTimersByTime(1) })
    expect(result.current).toBe(T0 + 1_000)
    act(() => { vi.advanceTimersByTime(2_100) })
    expect(result.current).toBeGreaterThanOrEqual(T0 + 3_000)
    expect(result.current).toBe(T0 + 3_000)
  })

  it('toggling active → false stops updates and keeps the last value', () => {
    const { result, rerender } = renderHook(({ active }) => useElapsedTicker(active), {
      initialProps: { active: true },
    })
    act(() => { vi.advanceTimersByTime(2_000) })
    expect(result.current).toBe(T0 + 2_000)
    rerender({ active: false })
    expect(vi.getTimerCount()).toBe(0)
    act(() => { vi.advanceTimersByTime(5_000) })
    expect(result.current).toBe(T0 + 2_000)
  })

  it('toggling active → true again resumes ticking from a fresh sample', () => {
    const { result, rerender } = renderHook(({ active }) => useElapsedTicker(active), {
      initialProps: { active: false },
    })
    act(() => { vi.advanceTimersByTime(5_000) })
    expect(result.current).toBe(T0)
    rerender({ active: true })
    act(() => { vi.advanceTimersByTime(1_000) })
    expect(result.current).toBe(T0 + 6_000)
  })

  it('unmount clears the interval', () => {
    const { unmount } = renderHook(() => useElapsedTicker(true))
    expect(vi.getTimerCount()).toBe(1)
    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
