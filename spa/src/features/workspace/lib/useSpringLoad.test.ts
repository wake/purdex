import { describe, it, expect, vi, afterEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useSpringLoad } from './useSpringLoad'

afterEach(() => {
  vi.useRealTimers()
})

describe('useSpringLoad', () => {
  it('fires onExpire after delay', () => {
    vi.useFakeTimers()
    const onExpire = vi.fn()
    const { result } = renderHook(() => useSpringLoad(500))
    act(() => result.current.schedule('w1', onExpire))
    expect(onExpire).not.toHaveBeenCalled()
    act(() => {
      vi.advanceTimersByTime(500)
    })
    expect(onExpire).toHaveBeenCalledTimes(1)
  })

  it('cancel prevents firing', () => {
    vi.useFakeTimers()
    const onExpire = vi.fn()
    const { result } = renderHook(() => useSpringLoad(500))
    act(() => result.current.schedule('w1', onExpire))
    act(() => result.current.cancel('w1'))
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(onExpire).not.toHaveBeenCalled()
  })

  it('schedule with different key cancels previous', () => {
    vi.useFakeTimers()
    const onA = vi.fn()
    const onB = vi.fn()
    const { result } = renderHook(() => useSpringLoad(500))
    act(() => result.current.schedule('a', onA))
    act(() => {
      vi.advanceTimersByTime(200)
    })
    act(() => result.current.schedule('b', onB))
    act(() => {
      vi.advanceTimersByTime(500)
    })
    expect(onA).not.toHaveBeenCalled()
    expect(onB).toHaveBeenCalledTimes(1)
  })

  it('schedule same key resets timer', () => {
    vi.useFakeTimers()
    const onExpire = vi.fn()
    const { result } = renderHook(() => useSpringLoad(500))
    act(() => result.current.schedule('a', onExpire))
    act(() => {
      vi.advanceTimersByTime(400)
    })
    act(() => result.current.schedule('a', onExpire))
    act(() => {
      vi.advanceTimersByTime(400)
    })
    expect(onExpire).not.toHaveBeenCalled()
    act(() => {
      vi.advanceTimersByTime(200)
    })
    expect(onExpire).toHaveBeenCalledTimes(1)
  })

  it('cancel() without key clears all', () => {
    vi.useFakeTimers()
    const onExpire = vi.fn()
    const { result } = renderHook(() => useSpringLoad(500))
    act(() => result.current.schedule('a', onExpire))
    act(() => result.current.cancel())
    act(() => {
      vi.advanceTimersByTime(500)
    })
    expect(onExpire).not.toHaveBeenCalled()
  })

  it('cancel(mismatched key) is a no-op (does not cancel active timer)', () => {
    vi.useFakeTimers()
    const onExpire = vi.fn()
    const { result } = renderHook(() => useSpringLoad(500))
    act(() => result.current.schedule('a', onExpire))
    act(() => result.current.cancel('other-key'))
    act(() => {
      vi.advanceTimersByTime(500)
    })
    expect(onExpire).toHaveBeenCalledTimes(1)
  })

  it('unmount cancels pending timer', () => {
    vi.useFakeTimers()
    const onExpire = vi.fn()
    const { result, unmount } = renderHook(() => useSpringLoad(500))
    act(() => result.current.schedule('a', onExpire))
    unmount()
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(onExpire).not.toHaveBeenCalled()
  })
})
