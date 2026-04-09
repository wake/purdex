import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useMiniWindowShortcuts } from './useMiniWindowShortcuts'

const PANE_ID = 'test-pane-123'

function mockElectronAPI() {
  let shortcutCallback: ((payload: { action: string }) => void) | null = null
  const cleanup = vi.fn()
  ;(window as unknown as Record<string, unknown>).electronAPI = {
    onShortcut: (cb: (payload: { action: string }) => void) => {
      shortcutCallback = cb
      return cleanup
    },
    browserViewGoBack: vi.fn(),
    browserViewGoForward: vi.fn(),
    browserViewReload: vi.fn(),
    browserViewPrint: vi.fn(),
    signalReady: vi.fn(),
  }
  return {
    fire: (action: string) => shortcutCallback?.({ action }),
    cleanup,
    api: (window as unknown as Record<string, unknown>).electronAPI as Record<string, ReturnType<typeof vi.fn>>,
  }
}

describe('useMiniWindowShortcuts', () => {
  beforeEach(() => {
    vi.spyOn(window, 'close').mockImplementation(() => {})
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    vi.restoreAllMocks()
  })

  it('does nothing when electronAPI is not available', () => {
    // No electronAPI set — hook should not throw
    const { unmount } = renderHook(() => useMiniWindowShortcuts(PANE_ID))
    unmount()
  })

  it('cleans up listener on unmount', () => {
    const { cleanup } = mockElectronAPI()
    const { unmount } = renderHook(() => useMiniWindowShortcuts(PANE_ID))
    unmount()
    expect(cleanup).toHaveBeenCalled()
  })

  describe('close-tab', () => {
    it('calls window.close()', () => {
      const { fire } = mockElectronAPI()
      renderHook(() => useMiniWindowShortcuts(PANE_ID))

      fire('close-tab')
      expect(window.close).toHaveBeenCalledOnce()
    })
  })

  describe('go-back', () => {
    it('calls browserViewGoBack with paneId', () => {
      const { fire, api } = mockElectronAPI()
      renderHook(() => useMiniWindowShortcuts(PANE_ID))

      fire('go-back')
      expect(api.browserViewGoBack).toHaveBeenCalledOnce()
      expect(api.browserViewGoBack).toHaveBeenCalledWith(PANE_ID)
    })
  })

  describe('go-forward', () => {
    it('calls browserViewGoForward with paneId', () => {
      const { fire, api } = mockElectronAPI()
      renderHook(() => useMiniWindowShortcuts(PANE_ID))

      fire('go-forward')
      expect(api.browserViewGoForward).toHaveBeenCalledOnce()
      expect(api.browserViewGoForward).toHaveBeenCalledWith(PANE_ID)
    })
  })

  describe('reload', () => {
    it('calls browserViewReload with paneId', () => {
      const { fire, api } = mockElectronAPI()
      renderHook(() => useMiniWindowShortcuts(PANE_ID))

      fire('reload')
      expect(api.browserViewReload).toHaveBeenCalledOnce()
      expect(api.browserViewReload).toHaveBeenCalledWith(PANE_ID)
    })
  })

  describe('focus-url', () => {
    it('dispatches browser:focus-url custom event on document', () => {
      const { fire } = mockElectronAPI()
      const handler = vi.fn()
      document.addEventListener('browser:focus-url', handler)
      renderHook(() => useMiniWindowShortcuts(PANE_ID))

      fire('focus-url')
      expect(handler).toHaveBeenCalledOnce()

      document.removeEventListener('browser:focus-url', handler)
    })
  })

  describe('print', () => {
    it('calls browserViewPrint with paneId', () => {
      const { fire, api } = mockElectronAPI()
      renderHook(() => useMiniWindowShortcuts(PANE_ID))

      fire('print')
      expect(api.browserViewPrint).toHaveBeenCalledOnce()
      expect(api.browserViewPrint).toHaveBeenCalledWith(PANE_ID)
    })
  })
})
