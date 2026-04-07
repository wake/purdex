import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useBrowserViewState } from '../useBrowserViewState'
import type { BrowserViewState } from '../useBrowserViewState'

describe('useBrowserViewState', () => {
  let listeners: Array<(paneId: string, state: BrowserViewState) => void>
  let mockUnsubscribe: ReturnType<typeof vi.fn>

  beforeEach(() => {
    listeners = []
    mockUnsubscribe = vi.fn()
    window.electronAPI = {
      onBrowserViewStateUpdate: vi.fn((cb: (paneId: string, state: BrowserViewState) => void) => {
        listeners.push(cb)
        return mockUnsubscribe
      }),
    } as unknown as typeof window.electronAPI
  })

  afterEach(() => {
    window.electronAPI = undefined
  })

  it('returns initial empty state', () => {
    const { result } = renderHook(() => useBrowserViewState('pane-1'))
    expect(result.current).toEqual({
      url: '',
      title: '',
      canGoBack: false,
      canGoForward: false,
      isLoading: false,
    })
  })

  it('updates state when matching paneId received', () => {
    const { result } = renderHook(() => useBrowserViewState('pane-1'))

    act(() => {
      listeners[0]('pane-1', {
        url: 'https://github.com',
        title: 'GitHub',
        canGoBack: true,
        canGoForward: false,
        isLoading: false,
      })
    })

    expect(result.current.url).toBe('https://github.com')
    expect(result.current.title).toBe('GitHub')
    expect(result.current.canGoBack).toBe(true)
  })

  it('ignores state for different paneId', () => {
    const { result } = renderHook(() => useBrowserViewState('pane-1'))

    act(() => {
      listeners[0]('pane-OTHER', {
        url: 'https://other.com',
        title: 'Other',
        canGoBack: true,
        canGoForward: true,
        isLoading: true,
      })
    })

    expect(result.current.url).toBe('')
  })

  it('calls unsubscribe on unmount', () => {
    const { unmount } = renderHook(() => useBrowserViewState('pane-1'))
    unmount()
    expect(mockUnsubscribe).toHaveBeenCalledOnce()
  })

  it('returns empty state when electronAPI not available', () => {
    window.electronAPI = undefined
    const { result } = renderHook(() => useBrowserViewState('pane-1'))
    expect(result.current.url).toBe('')
  })
})
