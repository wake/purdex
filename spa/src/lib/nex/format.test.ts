// spa/src/lib/nex/format.test.ts
import { describe, it, expect } from 'vitest'
import { firstLine, shortId } from './format'

describe('shortId', () => {
  it('keeps the first 12 chars of a 26-char ULID', () => {
    expect(shortId('exc_0123456789abcdefghijkl')).toBe('exc_01234567')
  })

  it('returns a shorter id unchanged', () => {
    expect(shortId('abc')).toBe('abc')
  })
})

describe('firstLine', () => {
  it('returns only the first line of a multi-line brief', () => {
    expect(firstLine('first line\nsecond line')).toBe('first line')
  })

  it('returns an empty string for an empty brief', () => {
    expect(firstLine('')).toBe('')
  })

  it('caps at the default 80 chars with a trailing ellipsis', () => {
    const out = firstLine('x'.repeat(100))
    expect(out).toHaveLength(80)
    expect(out.endsWith('…')).toBe(true)
    expect(out.startsWith('x'.repeat(79))).toBe(true)
  })

  it('leaves a line of exactly max chars untouched', () => {
    expect(firstLine('y'.repeat(80))).toBe('y'.repeat(80))
  })

  it('honours a custom max', () => {
    expect(firstLine('abcdefghij', 5)).toBe('abcd…')
  })
})
