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

  // #1816 — the Mac App loads the SPA from the dev server, so its Electron preload can be older than this
  // code: `window.electronAPI` is there but a method the hook calls is not. Each one is checked on its own.
  describe('with an older preload (#1816)', () => {
    it('an electronAPI without onBrowserViewStateUpdate: no throw, the initial state, no catch-up request', () => {
      const requestBrowserViewState = vi.fn()
      window.electronAPI = { requestBrowserViewState } as unknown as typeof window.electronAPI

      const { result, unmount } = renderHook(() => useBrowserViewState('pane-1'))

      expect(result.current.url).toBe('')
      expect(requestBrowserViewState).not.toHaveBeenCalled()
      expect(() => unmount()).not.toThrow()
    })

    it('an electronAPI without requestBrowserViewState: still subscribes, no throw', () => {
      // The beforeEach API has no requestBrowserViewState.
      const { result } = renderHook(() => useBrowserViewState('pane-1'))

      act(() => {
        listeners[0]('pane-1', { url: 'https://github.com', title: '', canGoBack: false, canGoForward: false, isLoading: false })
      })

      expect(result.current.url).toBe('https://github.com')
    })

    it('a full electronAPI asks for the current state once subscribed (unchanged)', () => {
      const requestBrowserViewState = vi.fn()
      window.electronAPI = { ...window.electronAPI, requestBrowserViewState } as unknown as typeof window.electronAPI

      renderHook(() => useBrowserViewState('pane-1'))

      expect(requestBrowserViewState).toHaveBeenCalledWith('pane-1')
    })
  })
})
