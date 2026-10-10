import { describe, it, expect } from 'vitest'
import { entryTime, groupEntries, refreshCounts } from './view-model'
import { entry } from '../team/__tests__/workbook-fixture'

describe('groupEntries', () => {
  it('groups by thing, the thing of the newest entry first, entries newest first inside', () => {
    const g = groupEntries([entry(5, { thing: 'B' }), entry(4, { thing: 'A' }), entry(3, { thing: 'B' }), entry(2, { thing: 'A' })])
    expect(g.map((x) => [x.thing, x.entries.map((e) => e.id)])).toEqual([['B', [5, 3]], ['A', [4, 2]]])
  })
  it('drops skipped entries, and a group left empty disappears', () => {
    const g = groupEntries([entry(3, { thing: 'A', state: 'skipped' }), entry(2, { thing: 'B' })])
    expect(g.map((x) => x.thing)).toEqual(['B'])
  })
  it('an entry with no thing is its own group (a pending one is never filed under a name)', () => {
    const g = groupEntries([entry(3, { thing: '', state: 'pending' }), entry(2, { thing: '', state: 'failed' })])
    expect(g).toHaveLength(2)
    expect(g.map((x) => x.key)).toEqual(['e:3', 'e:2'])
  })
  it('done follows the newest settled entry: a pending newer one does not reopen a finished thing', () => {
    const fin = groupEntries([entry(3, { thing: 'A', state: 'pending' }), entry(2, { thing: 'A', thingDone: true })])
    expect(fin[0].done).toBe(true)
    const open = groupEntries([entry(3, { thing: 'A' }), entry(2, { thing: 'A', thingDone: true })])
    expect(open[0].done).toBe(false)
  })
})

describe('refreshCounts / entryTime', () => {
  it('counts the three kinds of change', () => {
    const t = { id: 1, title: 'x' }
    expect(refreshCounts({ todoChanges: { added: [t, t, t], done: [t], dropped: [t, t] } })).toEqual({ done: 1, dropped: 2, added: 3 })
  })
  it('a refresh has no turn time: its creation time stands in', () => {
    expect(entryTime({ turnAt: 0, createdAt: 9 })).toBe(9)
    expect(entryTime({ turnAt: 4, createdAt: 9 })).toBe(4)
  })
})
