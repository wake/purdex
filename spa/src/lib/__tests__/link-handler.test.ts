import { describe, it, expect, vi } from 'vitest'
import { createLinkHandler } from '../link-handler'

function makeMouseEvent(overrides: Partial<MouseEvent> = {}): MouseEvent {
  return { shiftKey: false, ...overrides } as MouseEvent
}

describe('createLinkHandler', () => {
  describe('Electron mode', () => {
    it('calls openBrowserTab on normal click', () => {
      const openBrowserTab = vi.fn()
      const openMiniWindow = vi.fn()
      const handler = createLinkHandler({
        isElectron: true,
        openBrowserTab,
        openMiniWindow,
      })

      handler(makeMouseEvent(), 'https://github.com')

      expect(openBrowserTab).toHaveBeenCalledWith('https://github.com')
      expect(openMiniWindow).not.toHaveBeenCalled()
    })

    it('calls openMiniWindow on shift+click', () => {
      const openBrowserTab = vi.fn()
      const openMiniWindow = vi.fn()
      const handler = createLinkHandler({
        isElectron: true,
        openBrowserTab,
        openMiniWindow,
      })

      handler(makeMouseEvent({ shiftKey: true }), 'https://github.com')

      expect(openMiniWindow).toHaveBeenCalledWith('https://github.com')
      expect(openBrowserTab).not.toHaveBeenCalled()
    })
  })

  describe('SPA mode', () => {
    it('calls window.open on any click', () => {
      const openSpy = vi.fn()
      vi.stubGlobal('open', openSpy)

      const handler = createLinkHandler({
        isElectron: false,
        openBrowserTab: vi.fn(),
        openMiniWindow: vi.fn(),
      })

      handler(makeMouseEvent(), 'https://github.com')

      expect(openSpy).toHaveBeenCalledWith('https://github.com', '_blank')

      vi.unstubAllGlobals()
    })

    it('calls window.open on shift+click too', () => {
      const openSpy = vi.fn()
      vi.stubGlobal('open', openSpy)

      const handler = createLinkHandler({
        isElectron: false,
        openBrowserTab: vi.fn(),
        openMiniWindow: vi.fn(),
      })

      handler(makeMouseEvent({ shiftKey: true }), 'https://github.com')

      expect(openSpy).toHaveBeenCalledWith('https://github.com', '_blank')

      vi.unstubAllGlobals()
    })
  })
})
