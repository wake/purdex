import { describe, it, expect } from 'vitest'
import { reorderStandaloneTabOrder } from './reorderStandaloneTabOrder'

describe('reorderStandaloneTabOrder', () => {
  it('all-standalone: returns newOrder', () => {
    expect(reorderStandaloneTabOrder(['a', 'b', 'c'], ['c', 'b', 'a'])).toEqual(['c', 'b', 'a'])
  })

  it('no-standalone: returns current unchanged', () => {
    expect(reorderStandaloneTabOrder(['x', 'y'], [])).toEqual(['x', 'y'])
  })

  it('mixed: inserts newOrder at first-standalone position, keeps non-standalone order', () => {
    // current: [ws1, sA, ws2, sB, ws3], newOrder: [sB, sA]
    // first standalone (sA) was at index 1 → in result (after removing standalones) should slot at index 1
    expect(
      reorderStandaloneTabOrder(
        ['ws1', 'sA', 'ws2', 'sB', 'ws3'],
        ['sB', 'sA'],
      ),
    ).toEqual(['ws1', 'sB', 'sA', 'ws2', 'ws3'])
  })

  it('standalone at start: inserts at head', () => {
    expect(
      reorderStandaloneTabOrder(['sA', 'ws1', 'sB'], ['sB', 'sA']),
    ).toEqual(['sB', 'sA', 'ws1'])
  })

  it('standalone at end: inserts at end', () => {
    expect(
      reorderStandaloneTabOrder(['ws1', 'ws2', 'sA', 'sB'], ['sB', 'sA']),
    ).toEqual(['ws1', 'ws2', 'sB', 'sA'])
  })

  it('empty current: returns empty', () => {
    expect(reorderStandaloneTabOrder([], [])).toEqual([])
  })

  it('empty newOrder with standalones in current: keeps current ordering', () => {
    // This is the no-op case — if caller passes [], no standalones are "touched"
    // so they stay where they are.
    expect(reorderStandaloneTabOrder(['sA', 'ws1', 'sB'], [])).toEqual(['sA', 'ws1', 'sB'])
  })

  describe('defensive filtering', () => {
    it('drops phantom ids from newOrder not present in current', () => {
      const current = ['a', 's1', 'b', 's2', 'c']
      const result = reorderStandaloneTabOrder(current, ['s2', 'phantom', 's1'])
      // phantom dropped; a/b/c non-standalone kept in place
      expect(result).toEqual(['a', 's2', 's1', 'b', 'c'])
    })

    it('handles newOrder entirely filtered to empty (no-op return)', () => {
      const current = ['a', 's1', 'b']
      const result = reorderStandaloneTabOrder(current, ['phantom'])
      // All ids phantom → effectively empty newOrder → return original slice
      expect(result).toEqual(['a', 's1', 'b'])
    })
  })
})
