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
    const o = createUrlOpener({ openBrowserTab: vi.fn(), openExternal: vi.fn() })
    expect(o.canOpen(token)).toBe(true)
    expect(o.canOpen({ ...token, type: 'file' })).toBe(false)
  })

  it('normal click: openBrowserTab, never window.open', () => {
    const openBrowserTab = vi.fn()
    const openExternal = vi.fn()
    const webSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
    const o = createUrlOpener({ openBrowserTab, openExternal })
    o.open(token, {}, new MouseEvent('click'))
    expect(openBrowserTab).toHaveBeenCalledWith('https://example.com')
    expect(openExternal).not.toHaveBeenCalled()
    expect(webSpy).not.toHaveBeenCalled()
  })

  it('shift+click: openExternal (OS default browser)', () => {
    const openBrowserTab = vi.fn()
    const openExternal = vi.fn()
    const o = createUrlOpener({ openBrowserTab, openExternal })
    o.open(token, {}, new MouseEvent('click', { shiftKey: true }))
    expect(openExternal).toHaveBeenCalledWith('https://example.com')
    expect(openBrowserTab).not.toHaveBeenCalled()
  })

  it('rejects non-http(s) schemes regardless of matcher output', () => {
    const openBrowserTab = vi.fn()
    const openExternal = vi.fn()
    const webSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
    const o = createUrlOpener({ openBrowserTab, openExternal })

    for (const uri of ['javascript:alert(1)', 'data:text/html,<script>x</script>', 'file:///etc/passwd']) {
      o.open({ ...token, text: uri }, {}, new MouseEvent('click'))
      o.open({ ...token, text: uri }, {}, new MouseEvent('click', { shiftKey: true }))
    }
    expect(openBrowserTab).not.toHaveBeenCalled()
    expect(openExternal).not.toHaveBeenCalled()
    expect(webSpy).not.toHaveBeenCalled()
  })
})
