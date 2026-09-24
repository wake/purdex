// spa/src/lib/nex/mask-account.test.ts — #1264: the quota row must not print
// the host's login address verbatim (the worker pane ends up in screenshots).
import { describe, it, expect } from 'vitest'
import { maskAccount } from './mask-account'

describe('maskAccount', () => {
  it('masks the local part of an address', () => {
    expect(maskAccount('wake.gs@gmail.com')).toBe('wa…@gmail.com')
  })

  it('keeps a short local part whole', () => {
    expect(maskAccount('ab@x.io')).toBe('ab@x.io')
  })

  it('masks a bare handle', () => {
    expect(maskAccount('wakeliu')).toBe('wa…')
  })

  it('leaves a short bare handle', () => {
    expect(maskAccount('abcd')).toBe('abcd')
  })

  it('leaves an empty string', () => {
    expect(maskAccount('')).toBe('')
  })

  // The plan listed `a@b@c.io` -> `a@b@c.io` for this case, which is what a
  // split on the FIRST @ produces and contradicts both the case's name and the
  // implementation it prescribes. The split is on the last @, so the local part
  // is `a@b` (3 chars, over the keep-whole threshold) and the domain is `@c.io`.
  it('treats the last @ as the separator', () => {
    expect(maskAccount('a@b@c.io')).toBe('a@\u2026@c.io')
  })
})
