import { describe, expect, it } from 'vitest'
import type { ManualGroups } from '../../stores/useManualGroupsStore'
import { MAX_GROUPS, MAX_MEMBERS } from '../../stores/useManualGroupsStore'
import {
  addMember, deleteGroup, groupNameError, memberGroupOf, moveMember, newGroup, newManualGroupId, removeMember, renameGroup, setGroupColor,
} from './manual-edits'

const empty = (): ManualGroups => ({ order: [], groups: {} })
const ok = (r: ReturnType<typeof newGroup>): ManualGroups => {
  if ('error' in r) throw new Error(r.error)
  return r.state
}
const two = (): ManualGroups => {
  let s = ok(newGroup(empty(), { id: 'aaaaaaaa', name: 'A', now: 1 }))
  s = ok(newGroup(s, { id: 'bbbbbbbb', name: 'B', now: 2 }))
  return s
}

describe('groupNameError', () => {
  it('1–64 bytes, printable', () => {
    expect(groupNameError('Alpha')).toBeNull()
    expect(groupNameError('  ')).toBe('bad-name')
    expect(groupNameError('x'.repeat(65))).toBe('bad-name')
    expect(groupNameError('a\u0000b')).toBe('bad-name')
    expect(groupNameError('團'.repeat(21))).toBeNull() // bytes, not characters: 63
    expect(groupNameError('團'.repeat(22))).toBe('bad-name') // 66
  })
})

describe('newGroup', () => {
  it('an empty group is allowed, appended to order, with the colour and creation time', () => {
    const s = ok(newGroup(empty(), { id: 'aaaaaaaa', name: ' A ', color: '#112233', now: 5 }))
    expect(s.order).toEqual(['aaaaaaaa'])
    expect(s.groups.aaaaaaaa).toEqual({ name: 'A', color: '#112233', members: [], createdAt: 5 })
  })

  it('refuses a bad name, a bad colour, a taken id, and a 33rd group', () => {
    expect(newGroup(empty(), { id: 'aaaaaaaa', name: '', now: 1 })).toEqual({ error: 'bad-name' })
    expect(newGroup(empty(), { id: 'aaaaaaaa', name: 'A', color: 'red', now: 1 })).toEqual({ error: 'bad-color' })
    expect(newGroup(two(), { id: 'aaaaaaaa', name: 'X', now: 1 })).toEqual({ error: 'id-taken' })
    expect(newGroup(empty(), { id: 'not-hex!', name: 'X', now: 1 })).toEqual({ error: 'bad-id' })
    let s = empty()
    for (let i = 0; i < MAX_GROUPS; i += 1) s = ok(newGroup(s, { id: i.toString(16).padStart(8, '0'), name: `G${i}`, now: i }))
    expect(newGroup(s, { id: 'ffffffff', name: 'one more', now: 1 })).toEqual({ error: 'too-many-groups' })
  })

  it('can start with its first member', () => {
    const s = ok(newGroup(empty(), { id: 'aaaaaaaa', name: 'A', now: 1, member: { host: 'h1', root: 'r1' } }))
    expect(s.groups.aaaaaaaa.members).toEqual([{ host: 'h1', root: 'r1' }])
  })

  it('a first member already in another group is moved, not copied', () => {
    let s = ok(addMember(two(), 'aaaaaaaa', { host: 'h1', root: 'r1' }))
    s = ok(newGroup(s, { id: 'cccccccc', name: 'C', now: 3, member: { host: 'h1', root: 'r1' } }))
    expect(s.groups.aaaaaaaa.members).toEqual([])
    expect(s.groups.cccccccc.members).toEqual([{ host: 'h1', root: 'r1' }])
  })
})

describe('rename / colour / delete', () => {
  it('renames and recolours, and clears the colour with undefined', () => {
    let s = ok(renameGroup(two(), 'aaaaaaaa', 'Renamed'))
    expect(s.groups.aaaaaaaa.name).toBe('Renamed')
    s = ok(setGroupColor(s, 'aaaaaaaa', '#abcdef'))
    expect(s.groups.aaaaaaaa.color).toBe('#abcdef')
    s = ok(setGroupColor(s, 'aaaaaaaa', undefined))
    expect(Object.hasOwn(s.groups.aaaaaaaa, 'color')).toBe(false)
  })

  it('refuses a bad name or colour and an unknown group; a no-op returns the same state', () => {
    const s = two()
    expect(renameGroup(s, 'aaaaaaaa', ' ')).toEqual({ error: 'bad-name' })
    expect(renameGroup(s, 'zzzzzzzz', 'X')).toEqual({ error: 'group-gone' })
    expect(setGroupColor(s, 'aaaaaaaa', '#12')).toEqual({ error: 'bad-color' })
    expect(setGroupColor(s, 'zzzzzzzz', '#112233')).toEqual({ error: 'group-gone' })
    expect(ok(renameGroup(s, 'aaaaaaaa', 'A'))).toBe(s)
  })

  it('delete removes the group and its place in order, members included', () => {
    const s = ok(deleteGroup(ok(addMember(two(), 'aaaaaaaa', { host: 'h1', root: 'r1' })), 'aaaaaaaa'))
    expect(s.order).toEqual(['bbbbbbbb'])
    expect(Object.keys(s.groups)).toEqual(['bbbbbb' + 'bb'])
    expect(deleteGroup(s, 'aaaaaaaa')).toEqual({ error: 'group-gone' })
  })
})

describe('members', () => {
  it('adds a member; two hosts can sit in one group; the same member twice changes nothing', () => {
    let s = ok(addMember(two(), 'aaaaaaaa', { host: 'h1', root: 'r1' }))
    s = ok(addMember(s, 'aaaaaaaa', { host: 'h2', root: 'r1' }))
    expect(s.groups.aaaaaaaa.members).toEqual([{ host: 'h1', root: 'r1' }, { host: 'h2', root: 'r1' }])
    expect(ok(addMember(s, 'aaaaaaaa', { host: 'h1', root: 'r1' }))).toBe(s)
  })

  it('a member is in at most one group: adding it elsewhere takes it out of the first', () => {
    let s = ok(addMember(two(), 'aaaaaaaa', { host: 'h1', root: 'r1' }))
    s = ok(addMember(s, 'bbbbbbbb', { host: 'h1', root: 'r1' }))
    expect(s.groups.aaaaaaaa.members).toEqual([])
    expect(s.groups.bbbbbbbb.members).toEqual([{ host: 'h1', root: 'r1' }])
    expect(memberGroupOf(s, { host: 'h1', root: 'r1' })).toBe('bbbbbbbb')
    expect(memberGroupOf(s, { host: 'h1', root: 'nope' })).toBeNull()
  })

  it('refuses a missing group and a 65th member; a full group still takes a member it already holds', () => {
    expect(addMember(two(), 'zzzzzzzz', { host: 'h1', root: 'r1' })).toEqual({ error: 'group-gone' })
    let s = two()
    for (let i = 0; i < MAX_MEMBERS; i += 1) s = ok(addMember(s, 'aaaaaaaa', { host: 'h1', root: `r${i}` }))
    expect(addMember(s, 'aaaaaaaa', { host: 'h1', root: 'extra' })).toEqual({ error: 'group-full' })
    expect(ok(addMember(s, 'aaaaaaaa', { host: 'h1', root: 'r0' }))).toBe(s)
    // moving one in from another group is refused while the target is full, and the member stays where it was
    const other = ok(addMember(s, 'bbbbbbbb', { host: 'h2', root: 'x' }))
    expect(addMember(other, 'aaaaaaaa', { host: 'h2', root: 'x' })).toEqual({ error: 'group-full' })
  })

  it('removes a member from whichever group holds it; unknown member = no change', () => {
    const s = ok(addMember(two(), 'bbbbbbbb', { host: 'h1', root: 'r1' }))
    const gone = ok(removeMember(s, { host: 'h1', root: 'r1' }))
    expect(gone.groups.bbbbbbbb.members).toEqual([])
    expect(ok(removeMember(gone, { host: 'h1', root: 'r1' }))).toBe(gone)
  })

  it('moves a member to another group (same as adding it there)', () => {
    const s = ok(addMember(two(), 'aaaaaaaa', { host: 'h1', root: 'r1' }))
    const moved = ok(moveMember(s, { host: 'h1', root: 'r1' }, 'bbbbbbbb'))
    expect(moved.groups.aaaaaaaa.members).toEqual([])
    expect(moved.groups.bbbbbbbb.members).toEqual([{ host: 'h1', root: 'r1' }])
  })
})

describe('newManualGroupId', () => {
  it('8 hex characters, not among the used ones', () => {
    const id = newManualGroupId(['aaaaaaaa'])
    expect(id).toMatch(/^[0-9a-f]{8}$/)
    expect(id).not.toBe('aaaaaaaa')
  })
})
