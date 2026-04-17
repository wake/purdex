import { describe, it, expect } from 'vitest'
import { objectDepth } from './object-depth'

describe('objectDepth', () => {
  it('returns 0 for primitives', () => {
    expect(objectDepth(null)).toBe(0)
    expect(objectDepth(undefined)).toBe(0)
    expect(objectDepth(42)).toBe(0)
    expect(objectDepth('hi')).toBe(0)
    expect(objectDepth(true)).toBe(0)
  })

  it('returns 1 for flat object', () => {
    expect(objectDepth({ a: 1, b: 'x' })).toBe(1)
  })

  it('returns 1 for flat array', () => {
    expect(objectDepth([1, 2, 3])).toBe(1)
  })

  it('counts nested depth', () => {
    expect(objectDepth({ a: { b: { c: 1 } } })).toBe(3)
  })

  it('counts arrays inside objects', () => {
    expect(objectDepth({ a: [1, [2, [3]]] })).toBe(4)
  })

  it('throws when depth exceeds max', () => {
    const deep: Record<string, unknown> = {}
    let cursor = deep
    for (let i = 0; i < 40; i++) {
      cursor['nested'] = {}
      cursor = cursor['nested'] as Record<string, unknown>
    }
    expect(() => objectDepth(deep, 32)).toThrow(/exceeds 32/)
  })

  it('default max is 32', () => {
    const deep: Record<string, unknown> = {}
    let cursor = deep
    for (let i = 0; i < 40; i++) {
      cursor['n'] = {}
      cursor = cursor['n'] as Record<string, unknown>
    }
    expect(() => objectDepth(deep)).toThrow(/exceeds 32/)
  })
})
