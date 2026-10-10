import { describe, it, expect } from 'vitest'
import { emptyTodos, MAX_DONE_TODOS, MAX_TODO_TOUCHES, mergeEntries, snapshotTodos, touched, upsertTodos } from './merge'
import type { WorkbookEntry, WorkbookTodo } from './types'

const todo = (id: number, state: WorkbookTodo['state'] = 'open'): WorkbookTodo =>
  ({ id, title: `t${id}`, detail: '', state, closedBy: '', createdAt: id, closedAt: 0, addedEntryId: 0, closedEntryId: 0 })
const entry = (id: number, updatedAt: number): WorkbookEntry => ({
  id, convKey: 'c', sessionId: 's', turnId: '', turnAt: 0, state: 'ok', reason: '', thing: '', push: '', entry: '', thingDone: false,
  createdAt: 0, updatedAt, kind: 'turn', usage: { in: 0, out: 0, cacheRead: 0 }, todoChanges: { added: [], done: [], dropped: [] },
})
const ids = (xs: WorkbookTodo[]) => xs.map((x) => x.id)

describe('mergeEntries', () => {
  it('newest id first, one per id, a stale copy never replaces a newer one', () => {
    const out = mergeEntries([entry(2, 20), entry(1, 5)], [entry(2, 10), entry(3, 1), entry(1, 6)])
    expect(out.map((e) => [e.id, e.updatedAt])).toEqual([[3, 1], [2, 20], [1, 6]])
  })
})

describe('upsertTodos', () => {
  it('open → done moves; a done todo is not reopened; a dropped one leaves open', () => {
    let t = upsertTodos(emptyTodos(), [todo(1), todo(2), todo(3)])
    t = upsertTodos(t, [todo(1, 'done'), todo(2, 'dropped')])
    t = upsertTodos(t, [todo(1)])
    expect(ids(t.open)).toEqual([3])
    expect(ids(t.done)).toEqual([1])
  })
  it('caps done at MAX_DONE_TODOS and says so', () => {
    const t = upsertTodos(emptyTodos(), Array.from({ length: MAX_DONE_TODOS + 1 }, (_, i) => todo(i + 1, 'done')))
    expect(t.done).toHaveLength(MAX_DONE_TODOS)
    expect(t.doneCapped).toBe(true)
    expect(t.doneOldestId).toBe(2)
  })
})

describe('touched / snapshotTodos', () => {
  it('keeps the bounded touch history and the stamp it discarded up to', () => {
    let t = emptyTodos()
    for (let i = 1; i <= MAX_TODO_TOUCHES + 3; i++) t = touched(t, [todo(i)], i)
    expect(t.touches).toHaveLength(MAX_TODO_TOUCHES)
    expect(t.touchFloor).toBe(3)
  })
  it('refuses the ids touched after the request started', () => {
    const t = touched(upsertTodos(emptyTodos(), [todo(1, 'dropped')]), [todo(1, 'dropped')], 10)
    expect(ids(snapshotTodos(t, { open: [todo(1), todo(2)], done: [] }, 5).open)).toEqual([2])
    expect(ids(snapshotTodos(t, { open: [todo(1), todo(2)], done: [] }, 11).open)).toEqual([1, 2]) // started after: the answer is current
  })
  it('ignores the open list of an answer older than the discarded history', () => {
    let t = upsertTodos(emptyTodos(), [todo(9)])
    for (let i = 1; i <= MAX_TODO_TOUCHES + 1; i++) t = touched(t, [todo(1000 + i, 'dropped')], 100 + i)
    expect(ids(snapshotTodos(t, { open: [todo(1), todo(9)], done: [todo(4, 'done')] }, 50).open)).toEqual([9])
    expect(ids(snapshotTodos(t, { open: [todo(1)], done: [todo(4, 'done')] }, 50).done)).toEqual([4]) // done is terminal: still merged
  })
})
