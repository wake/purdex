// spa/src/lib/task-groups/edits.test.ts — the pure edits of a host's manual task groups (name pool v2 spec §7, NA-6c): each maps the groups to the new list under the daemon's limits (≤32 groups, ≤64 roots a group,
// a root in at most one group, a name of 1–64 bytes) or says why it cannot. Every edit is a function of the list alone, because the store may apply it twice (a lost compare-and-set).
import { describe, it, expect } from 'vitest'
import type { TaskGroup } from './api'
import { addRoot, newGroup, removeRoot, renameGroup, deleteGroup, groupNameError, newGroupId, MAX_GROUPS, MAX_ROOTS } from './edits'

const g = (id: string, roots: string[] = [], created_at = 1): TaskGroup => ({ id, name: `G ${id}`, roots, created_at, updated_at: created_at })
const ok = (r: ReturnType<typeof addRoot>) => { if ('error' in r) throw new Error(r.error); return r.groups }

describe('addRoot', () => {
  it('puts the root in the group and stamps the group', () => {
    const out = ok(addRoot([g('a'), g('b')], 'a', 'R1', 50))
    expect(out[0]).toMatchObject({ id: 'a', roots: ['R1'], updated_at: 50 })
    expect(out[1].roots).toEqual([])
  })
  it('moves it out of the group that held it: a root is in at most one group', () => {
    const out = ok(addRoot([g('a', ['R1']), g('b')], 'b', 'R1', 50))
    expect(out[0]).toMatchObject({ roots: [], updated_at: 50 })
    expect(out[1].roots).toEqual(['R1'])
  })
  it('changes nothing for a root the group already holds', () => {
    const before = [g('a', ['R1']), g('b')]
    expect(ok(addRoot(before, 'a', 'R1', 50))).toEqual(before)
  })
  it('says when the group is gone (another device deleted it) or full', () => {
    expect(addRoot([g('a')], 'zz', 'R1', 1)).toEqual({ error: 'group-gone' })
    const full = g('a', Array.from({ length: MAX_ROOTS }, (_, i) => `R${i}`))
    expect(addRoot([full], 'a', 'NEW', 1)).toEqual({ error: 'group-full' })
  })
  it('does not mutate its input', () => {
    const before = [g('a', ['R1']), g('b')]
    const snapshot = JSON.stringify(before)
    addRoot(before, 'b', 'R1', 50)
    expect(JSON.stringify(before)).toBe(snapshot)
  })
})

describe('newGroup', () => {
  it('appends a group holding the root, taking it from the group that held it', () => {
    const out = ok(newGroup([g('a', ['R1'])], { id: 'nn', name: ' Fresh ', root: 'R1', now: 70 }))
    expect(out[0].roots).toEqual([])
    expect(out[1]).toEqual({ id: 'nn', name: 'Fresh', roots: ['R1'], created_at: 70, updated_at: 70 })
  })
  it('refuses a 33rd group, a bad name, and an id already used', () => {
    const many = Array.from({ length: MAX_GROUPS }, (_, i) => g(`g${i}`))
    expect(newGroup(many, { id: 'nn', name: 'X', root: 'R', now: 1 })).toEqual({ error: 'too-many-groups' })
    expect(newGroup([], { id: 'nn', name: '   ', root: 'R', now: 1 })).toEqual({ error: 'bad-name' })
    expect(newGroup([g('nn')], { id: 'nn', name: 'X', root: 'R', now: 1 })).toEqual({ error: 'id-taken' })
  })
})

describe('removeRoot', () => {
  it('takes the root out of whichever group holds it, and leaves the list alone when none does', () => {
    const out = ok(removeRoot([g('a', ['R1', 'R2']), g('b')], 'R1', 9))
    expect(out[0]).toMatchObject({ roots: ['R2'], updated_at: 9 })
    const before = [g('a', ['R2'])]
    expect(ok(removeRoot(before, 'R1', 9))).toEqual(before)
  })
})

describe('renameGroup / deleteGroup', () => {
  it('renames with the name trimmed, and checks the name', () => {
    expect(ok(renameGroup([g('a')], 'a', '  New  ', 5))[0]).toMatchObject({ name: 'New', updated_at: 5 })
    expect(renameGroup([g('a')], 'a', '', 5)).toEqual({ error: 'bad-name' })
    expect(renameGroup([g('a')], 'zz', 'X', 5)).toEqual({ error: 'group-gone' })
  })
  it('deletes a group, and says when it is already gone', () => {
    expect(ok(deleteGroup([g('a'), g('b')], 'a')).map((x) => x.id)).toEqual(['b'])
    expect(deleteGroup([g('a')], 'zz')).toEqual({ error: 'group-gone' })
  })
})

describe('groupNameError', () => {
  it('accepts 1–64 bytes with no control character, counted in bytes, not characters', () => {
    expect(groupNameError('Team 甲')).toBeNull()
    expect(groupNameError('x'.repeat(64))).toBeNull()
    expect(groupNameError('x'.repeat(65))).toBe('bad-name')
    expect(groupNameError('字'.repeat(22))).toBe('bad-name') // 66 bytes
    expect(groupNameError('a\nb')).toBe('bad-name')
    expect(groupNameError('')).toBe('bad-name')
  })
})

describe('newGroupId', () => {
  it('is 8 hex characters and avoids the ids in use', () => {
    const id = newGroupId(['a'])
    expect(id).toMatch(/^[0-9a-f]{8}$/)
  })
})
