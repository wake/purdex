// spa/src/lib/journal/refs.test.ts — entry refs as links (J-2c, spec §5.1): `#N` / `PR #N` -> <remote>/issues/<N> only with a remote
// URL; task numbers and anything without `#digits` stay plain text; only http(s) is ever opened.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { openExternal, refUrl } from './refs'

const REMOTE = 'https://github.com/wake/purdex'

describe('refUrl', () => {
  it('links #N and PR #N to the issue URL of the remote', () => {
    expect(refUrl('#2829', REMOTE)).toBe(`${REMOTE}/issues/2829`)
    expect(refUrl('PR #2829', REMOTE)).toBe(`${REMOTE}/issues/2829`)
    expect(refUrl('pr #7', REMOTE)).toBe(`${REMOTE}/issues/7`)
  })
  it('tidies the remote: trailing slash and .git', () => {
    expect(refUrl('#1', `${REMOTE}/`)).toBe(`${REMOTE}/issues/1`)
    expect(refUrl('#1', `${REMOTE}.git`)).toBe(`${REMOTE}/issues/1`)
  })
  it('no remote URL: plain text (M: link anyway)', () => {
    expect(refUrl('#2829', '')).toBeNull()
    expect(refUrl('PR #2829', '   ')).toBeNull()
  })
  it('a task number or a ref without #digits is never a link', () => {
    expect(refUrl('7bcf08-12', REMOTE)).toBeNull()
    expect(refUrl('12', REMOTE)).toBeNull()
    expect(refUrl('#abc', REMOTE)).toBeNull()
    expect(refUrl('see #12 and more', REMOTE)).toBeNull()
    expect(refUrl('', REMOTE)).toBeNull()
  })
  it('only an http(s) remote is accepted', () => {
    expect(refUrl('#1', 'http://git.local/o/r')).toBe('http://git.local/o/r/issues/1')
    expect(refUrl('#1', 'javascript:alert(1)')).toBeNull()
    expect(refUrl('#1', 'git@github.com:wake/purdex.git')).toBeNull()
    expect(refUrl('#1', 'file:///etc')).toBeNull()
  })
})

describe('openExternal', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    delete (window as { electronAPI?: unknown }).electronAPI
  })
  it('goes through the shell when there is one', () => {
    const openExternalUrl = vi.fn().mockResolvedValue(undefined)
    ;(window as unknown as { electronAPI: unknown }).electronAPI = { openExternalUrl }
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    openExternal('https://github.com/wake/purdex/issues/1')
    expect(openExternalUrl).toHaveBeenCalledWith('https://github.com/wake/purdex/issues/1')
    expect(open).not.toHaveBeenCalled()
  })
  it('without the shell it opens a new window with noopener', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    openExternal('https://github.com/wake/purdex/issues/1')
    expect(open).toHaveBeenCalledWith('https://github.com/wake/purdex/issues/1', '_blank', 'noopener')
  })
  it('never opens a non-http(s) URL', () => {
    const openExternalUrl = vi.fn()
    ;(window as unknown as { electronAPI: unknown }).electronAPI = { openExternalUrl }
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    openExternal('javascript:alert(1)')
    openExternal('file:///etc/passwd')
    expect(openExternalUrl).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalled()
  })
})
