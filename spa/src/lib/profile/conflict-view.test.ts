// spa/src/lib/profile/conflict-view.test.ts — the sidebar's reading of the master's locks (sidebar conflict icons
// spec §2): which locks there are, in the section list's order, and the one lock of a workspace's tabs.
import { describe, expect, it } from 'vitest'
import type { ExecutorStatus, SectionLock } from './executor'
import type { ProfileSyncSnapshot } from './sync-status'
import { locksOf, tabsLockOf } from './conflict-view'

const H = (c: string) => c.repeat(64)
const CONFLICT: SectionLock = { status: 'locked:conflict', currentHash: H('a'), sot: { rev: 7, hash: H('b') }, conflict: { localHash: H('a'), sot: { rev: 7, hash: H('b') } } }
const RESET: SectionLock = { status: 'locked:reset', currentHash: H('c'), sot: { rev: 2, hash: H('d') }, conflict: null }
const INVALID: SectionLock = { status: 'locked:invalid', currentHash: H('e'), sot: { rev: 9, hash: H('f') }, conflict: null }

function statusOf(locks: Record<string, SectionLock>, over: Partial<ExecutorStatus> = {}): ExecutorStatus {
  return {
    profile: 'locked:conflict',
    schemaLock: null,
    sections: Object.fromEntries(Object.entries(locks).map(([k, l]) => [k, l.status])),
    locks,
    profileGone: false,
    detail: {},
    indexFailures: 0,
    lastSuccessAt: null,
    ...over,
  }
}

function snap(status: ExecutorStatus | null, over: Partial<ProfileSyncSnapshot> = {}): ProfileSyncSnapshot {
  return { master: { hostId: 'h1', profileId: 'p1' }, leader: true, blocked: null, status, problems: [], remote: false, stale: false, ...over }
}

describe('locksOf', () => {
  it('no master / no status / the profile gone (either source) → nothing', () => {
    expect(locksOf(snap(statusOf({ workspaces: CONFLICT }), { master: null }), null)).toEqual([])
    expect(locksOf(snap(null), null)).toEqual([])
    expect(locksOf(snap(statusOf({ workspaces: CONFLICT }, { profileGone: true })), null)).toEqual([])
    expect(locksOf(snap(statusOf({ workspaces: CONFLICT }), { blocked: 'profile-gone' }), null)).toEqual([])
  })

  it('no locks → nothing', () => {
    expect(locksOf(snap(statusOf({})), null)).toEqual([])
  })

  it('every lock, in the section list\'s order; tabs follow the master\'s workspaces and carry their id', () => {
    const status = statusOf({ 'tabs.w2': INVALID, workspaces: CONFLICT, 'tabs.w1': RESET, settings: RESET, 'future.x': CONFLICT })
    const master = [{ id: 'w1', name: 'One' }, { id: 'w2', name: 'Two' }]
    const got = locksOf(snap(status), master)
    expect(got.map((l) => l.key)).toEqual(['settings', 'workspaces', 'tabs.w1', 'tabs.w2', 'future.x'])
    expect(got.map((l) => l.status)).toEqual(['locked:reset', 'locked:conflict', 'locked:reset', 'locked:invalid', 'locked:conflict'])
    expect(got.map((l) => l.workspaceId)).toEqual([null, null, 'w1', 'w2', null])
    expect(got.map((l) => l.kind)).toEqual(['settings', 'workspaces', 'tabs', 'tabs', 'other'])
    expect(got[2].view).toEqual({ key: 'tabs.w1', kind: 'tabs', workspace: 'One' })
  })

  it('a stale follower still reads its locks: a lock is a fact, not a promise', () => {
    expect(locksOf(snap(statusOf({ workspaces: CONFLICT }), { remote: true, stale: true, leader: false }), null)).toHaveLength(1)
  })
})

describe('tabsLockOf', () => {
  const status = statusOf({ 'tabs.w1': CONFLICT, workspaces: RESET })

  it('the lock of that workspace\'s tabs; none for another workspace', () => {
    expect(tabsLockOf(snap(status), 'w1')).toBe(CONFLICT)
    expect(tabsLockOf(snap(status), 'w2')).toBeNull()
  })

  it('an id that cannot form a section key → null, and no throw', () => {
    expect(() => tabsLockOf(snap(status), 'ws.bad!')).not.toThrow()
    expect(tabsLockOf(snap(status), 'ws.bad!')).toBeNull()
  })

  it('same guards as locksOf: no master, no status, the profile gone → null', () => {
    expect(tabsLockOf(snap(status, { master: null }), 'w1')).toBeNull()
    expect(tabsLockOf(snap(null), 'w1')).toBeNull()
    expect(tabsLockOf(snap(statusOf({ 'tabs.w1': CONFLICT }, { profileGone: true })), 'w1')).toBeNull()
  })
})
