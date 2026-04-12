import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, cleanup } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useSessionWatch, __resetSessionWatch } from './useSessionWatch'

describe('useSessionWatch', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    __resetSessionWatch()
    useHostStore.setState({
      hosts: { h1: { id: 'h1', name: 'Host 1', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: ['h1'],
      runtime: { h1: { status: 'connected' } },
    })
    useSessionStore.setState({ sessions: {} })
  })

  afterEach(() => {
    cleanup()
    __resetSessionWatch()
    vi.useRealTimers()
  })

  it('starts polling on mount and stops on unmount', () => {
    const fetchHost = vi.spyOn(useSessionStore.getState(), 'fetchHost').mockResolvedValue(undefined)

    const { unmount } = renderHook(() => useSessionWatch())

    // Initial fetch on mount
    expect(fetchHost).toHaveBeenCalledTimes(1)

    // After 1s interval tick
    vi.advanceTimersByTime(1000)
    expect(fetchHost).toHaveBeenCalledTimes(2)

    // After unmount, no more polling
    unmount()
    vi.advanceTimersByTime(2000)
    expect(fetchHost).toHaveBeenCalledTimes(2)

    fetchHost.mockRestore()
  })

  it('ref-counts multiple consumers', () => {
    const fetchHost = vi.spyOn(useSessionStore.getState(), 'fetchHost').mockResolvedValue(undefined)

    const h1 = renderHook(() => useSessionWatch())
    const h2 = renderHook(() => useSessionWatch())

    // Both mounted, but only one interval — initial fetch fires for first mount
    expect(fetchHost).toHaveBeenCalledTimes(1)

    // Unmount first — polling continues
    h1.unmount()
    vi.advanceTimersByTime(1000)
    expect(fetchHost.mock.calls.length).toBeGreaterThan(1)

    // Unmount second — polling stops
    const countBefore = fetchHost.mock.calls.length
    h2.unmount()
    vi.advanceTimersByTime(2000)
    expect(fetchHost).toHaveBeenCalledTimes(countBefore)

    fetchHost.mockRestore()
  })

  it('skips disconnected hosts', () => {
    useHostStore.setState({
      hosts: {
        h1: { id: 'h1', name: 'Host 1', ip: '1.2.3.4', port: 7860, order: 0 },
        h2: { id: 'h2', name: 'Host 2', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: ['h1', 'h2'],
      runtime: {
        h1: { status: 'connected' },
        h2: { status: 'disconnected' },
      },
    })
    const fetchHost = vi.spyOn(useSessionStore.getState(), 'fetchHost').mockResolvedValue(undefined)

    renderHook(() => useSessionWatch())

    // Only h1 (connected) should be fetched, not h2 (disconnected)
    expect(fetchHost).toHaveBeenCalledWith('h1')
    expect(fetchHost).not.toHaveBeenCalledWith('h2')

    fetchHost.mockRestore()
  })
})
