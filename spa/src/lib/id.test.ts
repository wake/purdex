import { describe, it, expect } from 'vitest'
import { generateId } from './id'

describe('generateId', () => {
  it('returns a 6-character string', () => {
    const id = generateId()
    expect(id).toHaveLength(6)
  })

  it('only contains base36 characters', () => {
    for (let i = 0; i < 100; i++) {
      const id = generateId()
      expect(id).toMatch(/^[0-9a-z]{6}$/)
    }
  })

  it('generates unique IDs', () => {
    const ids = new Set(Array.from({ length: 1000 }, () => generateId()))
    expect(ids.size).toBe(1000)
  })
})
