import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createUrlOpener } from './url'
import type { LinkToken } from '../types'

const token: LinkToken = {
  type: 'url',
  text: 'https://example.com',
  range: { startCol: 0, endCol: 19 },
}

describe('url opener', () => {
  beforeEach(() => { vi.restoreAllMocks() })

  it('canOpen true only for type url', () => {
    const o = createUrlOpener({ isElectron: false, openBrowserTab: vi.fn(), openMiniWindow: vi.fn() })
    expect(o.canOpen(token)).toBe(true)
    expect(o.canOpen({ ...token, type: 'file' })).toBe(false)
  })

  it('web: uses window.open with _blank', () => {
    const spy = vi.spyOn(window, 'open').mockImplementation(() => null)
    const o = createUrlOpener({ isElectron: false, openBrowserTab: vi.fn(), openMiniWindow: vi.fn() })
    o.open(token, {}, new MouseEvent('click'))
    expect(spy).toHaveBeenCalledWith('https://example.com', '_blank')
  })

  it('electron normal click: openBrowserTab', () => {
    const openBrowserTab = vi.fn()
    const openMiniWindow = vi.fn()
    const o = createUrlOpener({ isElectron: true, openBrowserTab, openMiniWindow })
    o.open(token, {}, new MouseEvent('click'))
    expect(openBrowserTab).toHaveBeenCalledWith('https://example.com')
    expect(openMiniWindow).not.toHaveBeenCalled()
  })

  it('electron shift+click: openMiniWindow', () => {
    const openBrowserTab = vi.fn()
    const openMiniWindow = vi.fn()
    const o = createUrlOpener({ isElectron: true, openBrowserTab, openMiniWindow })
    o.open(token, {}, new MouseEvent('click', { shiftKey: true }))
    expect(openMiniWindow).toHaveBeenCalledWith('https://example.com')
    expect(openBrowserTab).not.toHaveBeenCalled()
  })

  it('rejects non-http(s) schemes regardless of matcher output', () => {
    const openBrowserTab = vi.fn()
    const openMiniWindow = vi.fn()
    const webSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
    const electron = createUrlOpener({ isElectron: true, openBrowserTab, openMiniWindow })
    const web = createUrlOpener({ isElectron: false, openBrowserTab, openMiniWindow })

    for (const uri of ['javascript:alert(1)', 'data:text/html,<script>x</script>', 'file:///etc/passwd']) {
      electron.open({ ...token, text: uri }, {}, new MouseEvent('click'))
      web.open({ ...token, text: uri }, {}, new MouseEvent('click'))
    }
    expect(openBrowserTab).not.toHaveBeenCalled()
    expect(openMiniWindow).not.toHaveBeenCalled()
    expect(webSpy).not.toHaveBeenCalled()
  })
})
