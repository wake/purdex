import { describe, it, expect } from 'vitest'
import { orderStints, pickRecentStints, MAX_STINTS } from './entity-stints'
import type { ExecutionSummary } from './types'

const sum = (id: string, created_at: number) => ({ id, created_at }) as ExecutionSummary

describe('orderStints', () => {
  it('excludes the current stint, sorts by boundary then createdAt, and drops null boundaries', () => {
    const out = orderStints([
      { id: 'c', created_at: 5, boundary: 100 },
      { id: 'cur', created_at: 9, boundary: 300 },
      { id: 'a', created_at: 7, boundary: 100 },
      { id: 'n', created_at: 1, boundary: null },
      { id: 'z', created_at: 2, boundary: 0 },
    ], 'cur')
    expect(out).toEqual([
      { id: 'z', boundary: 0, createdAt: 2 },
      { id: 'c', boundary: 100, createdAt: 5 },
      { id: 'a', boundary: 100, createdAt: 7 },
    ])
  })
})

describe('pickRecentStints', () => {
  it('takes the newest 50 of 120, never the current one', () => {
    const rows = Array.from({ length: 120 }, (_, i) => sum(`e${String(i).padStart(3, '0')}`, i))
    const out = pickRecentStints(rows, 'e119')
    expect(out).toHaveLength(MAX_STINTS)
    expect(out[0].id).toBe('e118')
    expect(out[49].id).toBe('e069')
  })
  it('breaks created_at ties by id descending', () => {
    expect(pickRecentStints([sum('a', 1), sum('b', 1)], 'x').map((r) => r.id)).toEqual(['b', 'a'])
  })
})
