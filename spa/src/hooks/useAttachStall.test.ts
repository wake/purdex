// spa/src/hooks/useAttachStall.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useAttachStall } from './useAttachStall'
import { useHostStore, type HostRuntime } from '../stores/useHostStore'

const HOST = 'h1'

function setRuntime(rt: HostRuntime) {
  useHostStore.setState({ runtime: { [HOST]: rt } })
}

// Gate closed, daemon reachable: the stall condition.
const stalling: HostRuntime = { status: 'connected', attachReady: false, daemonState: 'connected' }

describe('useAttachStall', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setRuntime(stalling)
  })
  afterEach(() => { vi.useRealTimers() })

  it('stays false at 9.9 s and turns true at 10 s', () => {
    const { result } = renderHook(() => useAttachStall(HOST))
    expect(result.current).toBe(false)
    act(() => { vi.advanceTimersByTime(9_900) })
    expect(result.current).toBe(false)
    act(() => { vi.advanceTimersByTime(100) })
    expect(result.current).toBe(true)
  })

  it('goes false again when the gate opens', () => {
    const { result } = renderHook(() => useAttachStall(HOST))
    act(() => { vi.advanceTimersByTime(10_000) })
    expect(result.current).toBe(true)
    act(() => { setRuntime({ ...stalling, attachReady: true }) })
    expect(result.current).toBe(false)
  })

  it('an unreachable daemon is not a stall, and the timer restarts when it comes back', () => {
    const { result } = renderHook(() => useAttachStall(HOST))
    act(() => { vi.advanceTimersByTime(6_000) })
    act(() => { setRuntime({ ...stalling, daemonState: 'unreachable' }) })
    act(() => { vi.advanceTimersByTime(10_000) })
    expect(result.current).toBe(false)
    act(() => { setRuntime(stalling) })
    act(() => { vi.advanceTimersByTime(9_900) })
    expect(result.current).toBe(false)
    act(() => { vi.advanceTimersByTime(100) })
    expect(result.current).toBe(true)
  })

  it('status flapping during the subscribe-retry loop does not reset the timer', () => {
    const { result } = renderHook(() => useAttachStall(HOST))
    for (let i = 0; i < 10; i++) {
      act(() => { setRuntime({ ...stalling, status: i % 2 === 0 ? 'reconnecting' : 'connected' }) })
      act(() => { vi.advanceTimersByTime(1_000) })
    }
    expect(result.current).toBe(true)
  })

  // A pane re-bound to another host starts a fresh run: host A's accumulated
  // time (or its stall) says nothing about host B.
  it('rebinding to another waiting host restarts the timer', () => {
    useHostStore.setState({ runtime: { A: stalling, B: stalling } })
    const { result, rerender } = renderHook(({ id }) => useAttachStall(id), { initialProps: { id: 'A' } })
    act(() => { vi.advanceTimersByTime(9_000) })
    rerender({ id: 'B' })
    act(() => { vi.advanceTimersByTime(1_000) })
    expect(result.current).toBe(false)
    act(() => { vi.advanceTimersByTime(8_900) })
    expect(result.current).toBe(false)
    act(() => { vi.advanceTimersByTime(100) })
    expect(result.current).toBe(true)
  })

  it('a stalled pane re-bound to another waiting host is not stalled, even for one render', () => {
    useHostStore.setState({ runtime: { A: stalling, B: stalling } })
    const seen: boolean[] = []
    const { result, rerender } = renderHook(({ id }) => {
      const v = useAttachStall(id)
      seen.push(v)
      return v
    }, { initialProps: { id: 'A' } })
    act(() => { vi.advanceTimersByTime(10_000) })
    expect(result.current).toBe(true)
    seen.length = 0
    rerender({ id: 'B' })
    expect(result.current).toBe(false)
    expect(seen).not.toContain(true)
  })

  it('an empty hostId never stalls', () => {
    const { result } = renderHook(() => useAttachStall(''))
    act(() => { vi.advanceTimersByTime(20_000) })
    expect(result.current).toBe(false)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('honours a custom duration', () => {
    const { result } = renderHook(() => useAttachStall(HOST, 500))
    act(() => { vi.advanceTimersByTime(500) })
    expect(result.current).toBe(true)
  })

  it('clears its timer on unmount', () => {
    const { unmount } = renderHook(() => useAttachStall(HOST))
    expect(vi.getTimerCount()).toBe(1)
    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
