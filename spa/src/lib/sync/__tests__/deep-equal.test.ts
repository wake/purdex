import { describe, expect, it } from 'vitest'
import { deepEqual } from '../three-way-merge'

describe('deepEqual (exported)', () => {
  it('handles primitives', () => {
    expect(deepEqual(1, 1)).toBe(true)
    expect(deepEqual('a', 'b')).toBe(false)
  })

  it('handles nested objects', () => {
    expect(deepEqual({ a: { b: 1 } }, { a: { b: 1 } })).toBe(true)
    expect(deepEqual({ a: { b: 1 } }, { a: { b: 2 } })).toBe(false)
  })

  it('handles arrays', () => {
    expect(deepEqual([1, 2], [1, 2])).toBe(true)
    expect(deepEqual([1, 2], [2, 1])).toBe(false)
  })

  it('handles null / undefined asymmetry', () => {
    expect(deepEqual(null, undefined)).toBe(false)
    expect(deepEqual(null, null)).toBe(true)
  })
})
