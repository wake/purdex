// spa/src/lib/nex/permission-card-memory.test.ts — the request card's memory outside the component (P-2c).
import { describe, it, expect, afterEach } from 'vitest'
import {
  permissionCardKey, readPermissionCard, writePermissionCard, prunePermissionCards, permissionCardCount, clearAllPermissionCards,
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
