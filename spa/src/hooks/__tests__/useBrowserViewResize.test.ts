import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useBrowserViewResize } from '../useBrowserViewResize'

// #1816 — the Mac App loads the SPA from the dev server, so its Electron preload can be older than this code:
// `window.electronAPI` is there but `resizeBrowserView` is not. Pushing bounds then must be a no-op, never a
// TypeError out of the ResizeObserver / animation-frame callback.
describe('useBrowserViewResize', () => {
  let observerCallbacks: Array<() => void>
  let observed: Element[]
  const OriginalResizeObserver = globalThis.ResizeObserver

  function element() {
    const el = document.createElement('div')
    el.getBoundingClientRect = () => ({ x: 1.4, y: 2.6, width: 100.2, height: 50.7 }) as DOMRect
    return el
  }

  /** Fires every observer the hook created, as a layout change would. */
  function resize() {
    for (const cb of observerCallbacks) cb()
  }

  beforeEach(() => {
    observerCallbacks = []
    observed = []
    globalThis.ResizeObserver = class {
      constructor(cb: () => void) { observerCallbacks.push(cb) }
      observe(el: Element) { observed.push(el) }
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { cb(0); return 1 })
    vi.stubGlobal('cancelAnimationFrame', () => {})
  })

  afterEach(() => {
    globalThis.ResizeObserver = OriginalResizeObserver
    vi.unstubAllGlobals()
    delete window.electronAPI
  })

  it('a full electronAPI: pushes the rounded bounds (unchanged)', () => {
    const resizeBrowserView = vi.fn()
    window.electronAPI = { resizeBrowserView } as unknown as Window['electronAPI']
    const ref = { current: element() }

    renderHook(() => useBrowserViewResize('p1', ref))
    resize()

    expect(observed).toEqual([ref.current])
    expect(resizeBrowserView).toHaveBeenCalledWith('p1', { x: 1, y: 3, width: 100, height: 51 })
  })

  it('an electronAPI without resizeBrowserView: a resize does not throw, and nothing is observed', () => {
    window.electronAPI = { openBrowserView: vi.fn() } as unknown as Window['electronAPI']
    const ref = { current: element() }

    renderHook(() => useBrowserViewResize('p1', ref))

    expect(() => resize()).not.toThrow()
    expect(observed).toEqual([])
  })

  it('no electronAPI: nothing is observed (unchanged)', () => {
    const ref = { current: element() }

    renderHook(() => useBrowserViewResize('p1', ref))

    expect(observed).toEqual([])
  })
})
