// spa/src/lib/nex/permission-card-memory.test.ts — the request card's memory outside the component (P-2c).
import { describe, it, expect, afterEach } from 'vitest'
import {
  permissionCardKey, readPermissionCard, writePermissionCard, prunePermissionCards, permissionCardCount, clearAllPermissionCards,
  closePermissionCard, isPermissionCardClosed, closedPermissionRequests,
} from './permission-card-memory'

afterEach(() => clearAllPermissionCards())

describe('permission-card-memory', () => {
  const A = permissionCardKey('h', 'exc_1', 'req_a')

  it('keys by host, execution and request', () => {
    expect(A).toBe('h:exc_1:req_a')
  })

  it('reads the defaults for a request never touched', () => {
    expect(readPermissionCard(A)).toEqual({ note: '', noteOpen: false, expanded: false })
  })

  it('merges each change and keeps the others', () => {
    writePermissionCard(A, { expanded: true })
    writePermissionCard(A, { noteOpen: true })
    writePermissionCard(A, { note: 'no' })
    expect(readPermissionCard(A)).toEqual({ note: 'no', noteOpen: true, expanded: true })
    writePermissionCard(A, { noteOpen: false })
    expect(readPermissionCard(A)).toEqual({ note: 'no', noteOpen: false, expanded: true })
  })

  it('an entry back at the defaults is dropped, not kept empty', () => {
    writePermissionCard(A, { expanded: true })
    expect(permissionCardCount()).toBe(1)
    writePermissionCard(A, { expanded: false })
    expect(permissionCardCount()).toBe(0)
  })

  it('another request, or the same request id of another execution or host, does not share it', () => {
    writePermissionCard(A, { note: 'a' })
    expect(readPermissionCard(permissionCardKey('h', 'exc_1', 'req_b')).note).toBe('')
    expect(readPermissionCard(permissionCardKey('h', 'exc_2', 'req_a')).note).toBe('')
    expect(readPermissionCard(permissionCardKey('h2', 'exc_1', 'req_a')).note).toBe('')
  })

  it('prune drops the requests of one execution that `keep` rejects, and only that execution\'s', () => {
    writePermissionCard(A, { note: 'a' })
    writePermissionCard(permissionCardKey('h', 'exc_1', 'req_b'), { note: 'b' })
    writePermissionCard(permissionCardKey('h', 'exc_2', 'req_a'), { note: 'other execution' })
    writePermissionCard(permissionCardKey('h', 'exc_10', 'req_a'), { note: 'a longer execution id' })
    prunePermissionCards('h', 'exc_1', (id) => id === 'req_b')
    expect(readPermissionCard(A).note).toBe('')
    expect(readPermissionCard(permissionCardKey('h', 'exc_1', 'req_b')).note).toBe('b')
    expect(readPermissionCard(permissionCardKey('h', 'exc_2', 'req_a')).note).toBe('other execution')
    expect(readPermissionCard(permissionCardKey('h', 'exc_10', 'req_a')).note).toBe('a longer execution id')
  })

  it('prune without `keep` drops every request of that execution (the worker ended)', () => {
    writePermissionCard(A, { note: 'a' })
    writePermissionCard(permissionCardKey('h', 'exc_1', 'req_b'), { expanded: true })
    writePermissionCard(permissionCardKey('h', 'exc_2', 'req_a'), { note: 'stays' })
    prunePermissionCards('h', 'exc_1')
    expect(permissionCardCount()).toBe(1)
    expect(readPermissionCard(permissionCardKey('h', 'exc_2', 'req_a')).note).toBe('stays')
  })
})

// A2: "this pane closed the request" outlives the pane too, until the store stops listing it as pending.
describe('permission-card-memory — requests closed by the pane', () => {
  const A = permissionCardKey('h', 'exc_1', 'req_a')

  it('closing marks the request closed and drops its draft at once', () => {
    writePermissionCard(A, { note: 'no prod', noteOpen: true, expanded: true })
    closePermissionCard(A)
    expect(isPermissionCardClosed(A)).toBe(true)
    expect(readPermissionCard(A)).toEqual({ note: '', noteOpen: false, expanded: false })
    expect(permissionCardCount()).toBe(0)
  })

  it('lists the closed request ids of one execution only — not another request, execution or host', () => {
    closePermissionCard(A)
    closePermissionCard(permissionCardKey('h', 'exc_1', 'req_c'))
    closePermissionCard(permissionCardKey('h', 'exc_2', 'req_b'))
    closePermissionCard(permissionCardKey('h', 'exc_10', 'req_x'))
    closePermissionCard(permissionCardKey('h2', 'exc_1', 'req_y'))
    expect([...closedPermissionRequests('h', 'exc_1')].sort()).toEqual(['req_a', 'req_c'])
    expect(isPermissionCardClosed(permissionCardKey('h', 'exc_1', 'req_b'))).toBe(false)
    expect(isPermissionCardClosed(permissionCardKey('h', 'exc_2', 'req_a'))).toBe(false)
    expect(isPermissionCardClosed(permissionCardKey('h2', 'exc_1', 'req_a'))).toBe(false)
  })

  it('prune drops a closed mark by the same rule as a draft: `keep` rejects it, or there is no `keep`', () => {
    closePermissionCard(A)
    closePermissionCard(permissionCardKey('h', 'exc_1', 'req_b'))
    closePermissionCard(permissionCardKey('h', 'exc_2', 'req_a'))
    prunePermissionCards('h', 'exc_1', (id) => id === 'req_b')
    expect(isPermissionCardClosed(A)).toBe(false)
    expect(isPermissionCardClosed(permissionCardKey('h', 'exc_1', 'req_b'))).toBe(true)
    prunePermissionCards('h', 'exc_1')
    expect(isPermissionCardClosed(permissionCardKey('h', 'exc_1', 'req_b'))).toBe(false)
    expect(isPermissionCardClosed(permissionCardKey('h', 'exc_2', 'req_a'))).toBe(true)
  })

  it('clearAll forgets the closed marks too', () => {
    closePermissionCard(A)
    clearAllPermissionCards()
    expect(isPermissionCardClosed(A)).toBe(false)
  })
})
