// spa/src/lib/team/fnv1a.test.ts — the published FNV-1a 32 test vectors.
import { describe, it, expect } from 'vitest'
import { fnv1a32, FNV_OFFSET_32 } from './fnv1a'

describe('fnv1a32', () => {
  it('matches the published vectors', () => {
    expect(fnv1a32('')).toBe(0x811c9dc5)
    expect(fnv1a32('a')).toBe(0xe40c292c)
    expect(fnv1a32('foobar')).toBe(0xbf9cf968)
  })

  it('takes an explicit basis, defaulting to the standard one', () => {
    expect(fnv1a32('foobar', FNV_OFFSET_32)).toBe(fnv1a32('foobar'))
    expect(fnv1a32('foobar', 1)).not.toBe(fnv1a32('foobar'))
  })
})
