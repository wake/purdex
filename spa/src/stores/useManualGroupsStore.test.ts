// spa/src/stores/useManualGroupsStore.test.ts — the manual task groups (task groups spec §4.1, TG-1a-1 task 2).
import { beforeEach, describe, expect, it, vi } from 'vitest'

const registerSpy = vi.hoisted(() => vi.fn())

vi.mock('../lib/storage/sync', () => ({
  syncManager: { register: registerSpy, notify: vi.fn(), destroy: vi.fn() },
  createSyncManager: vi.fn(),
}))

import { MAX_GROUPS, MAX_MEMBERS, useManualGroupsStore } from './useManualGroupsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

async function rehydrateFrom(state: unknown): Promise<void> {
  localStorage.setItem(STORAGE_KEYS.TASK_GROUPS, JSON.stringify({ state, version: 1 }))
  await useManualGroupsStore.persist.rehydrate()
}

const st = () => useManualGroupsStore.getState()
const persisted = () => (JSON.parse(localStorage.getItem(STORAGE_KEYS.TASK_GROUPS) ?? '{}') as { state?: Record<string, unknown> }).state

const g = (over: Record<string, unknown> = {}) => ({ name: 'Alpha', members: [], createdAt: 1, ...over })

beforeEach(() => {
  localStorage.clear()
  useManualGroupsStore.setState({ order: [], groups: {} })
})

describe('useManualGroupsStore', () => {
  it('defaults to empty and persists exactly { order, groups }', () => {
    expect(STORAGE_KEYS.TASK_GROUPS).toBe('purdex-task-groups')
    expect(useManualGroupsStore.getInitialState().order).toEqual([])
    st().addGroup('a1b2c3d4', g())
    expect(persisted()).toEqual({ order: ['a1b2c3d4'], groups: { a1b2c3d4: g() } })
  })

  it('is registered with syncManager under its storage key', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.TASK_GROUPS, useManualGroupsStore)
  })

  it('addGroup appends to order; removeGroup drops both; setOrder reorders', () => {
    st().addGroup('aaaaaaaa', g())
    st().addGroup('bbbbbbbb', g({ name: 'B' }))
    expect(st().order).toEqual(['aaaaaaaa', 'bbbbbbbb'])
    st().setOrder(['bbbbbbbb', 'aaaaaaaa'])
    expect(st().order).toEqual(['bbbbbbbb', 'aaaaaaaa'])
    st().removeGroup('bbbbbbbb')
    expect(st().order).toEqual(['aaaaaaaa'])
    expect(Object.keys(st().groups)).toEqual(['aaaaaaaa'])
  })

  it('patchGroup merges fields into a group and ignores an unknown id', () => {
    st().addGroup('aaaaaaaa', g())
    st().patchGroup('aaaaaaaa', { name: 'Renamed', color: '#112233' })
    expect(st().groups.aaaaaaaa).toMatchObject({ name: 'Renamed', color: '#112233', createdAt: 1 })
    st().patchGroup('cccccccc', { name: 'x' })
    expect(st().groups.cccccccc).toBeUndefined()
  })
})

describe('useManualGroupsStore — merge (every arrival is sanitised)', () => {
  it('keeps a well-formed record as it is, in order', async () => {
    const groups = { aaaaaaaa: g({ color: '#aabbcc', members: [{ host: 'h1', root: 'r1' }, { host: 'h2', root: 'r2' }] }), bbbbbbbb: g({ name: 'B' }) }
    await rehydrateFrom({ order: ['bbbbbbbb', 'aaaaaaaa'], groups })
    expect(st().order).toEqual(['bbbbbbbb', 'aaaaaaaa'])
    expect(st().groups).toEqual(groups)
  })

  it('does not sort members (the builder does, not the store)', async () => {
    const members = [{ host: 'z', root: 'b' }, { host: 'a', root: 'a' }]
    await rehydrateFrom({ order: ['aaaaaaaa'], groups: { aaaaaaaa: g({ members }) } })
    expect(st().groups.aaaaaaaa.members).toEqual(members)
  })

  it('refuses a bad name or id, drops a bad colour, keeps the group', async () => {
    await rehydrateFrom({
      order: ['aaaaaaaa', 'bbbbbbbb', 'not-hex!!', 'dddddddd'],
      groups: {
        aaaaaaaa: g({ name: '   ' }), // empty after trim
        bbbbbbbb: g({ name: 'ok', color: 'red' }),
        'not-hex!!': g(),
        dddddddd: g({ name: 'x'.repeat(65) }),
      },
    })
    expect(Object.keys(st().groups)).toEqual(['bbbbbbbb'])
    expect(st().groups.bbbbbbbb.color).toBeUndefined()
    expect(st().order).toEqual(['bbbbbbbb'])
  })

  it('appends groups the order lacks and drops order ids with no group, and dedupes order', async () => {
    await rehydrateFrom({ order: ['bbbbbbbb', 'bbbbbbbb', 'ffffffff'], groups: { aaaaaaaa: g(), bbbbbbbb: g() } })
    expect(st().order).toEqual(['bbbbbbbb', 'aaaaaaaa'])
  })

  it('holds a (host, root) in at most one group — the first in order wins — and once within a group', async () => {
    await rehydrateFrom({
      order: ['aaaaaaaa', 'bbbbbbbb'],
      groups: {
        aaaaaaaa: g({ members: [{ host: 'h', root: 'r' }, { host: 'h', root: 'r' }, { host: 'h', root: 's' }] }),
        bbbbbbbb: g({ members: [{ host: 'h', root: 'r' }, { host: 'h', root: 't' }] }),
      },
    })
    expect(st().groups.aaaaaaaa.members).toEqual([{ host: 'h', root: 'r' }, { host: 'h', root: 's' }])
    expect(st().groups.bbbbbbbb.members).toEqual([{ host: 'h', root: 't' }])
  })

  it('drops malformed members', async () => {
    await rehydrateFrom({ order: ['aaaaaaaa'], groups: { aaaaaaaa: g({ members: [{ host: 1, root: 'r' }, { host: 'h' }, null, { host: '', root: 'r' }, { host: 'h', root: 'ok' }] }) } })
    expect(st().groups.aaaaaaaa.members).toEqual([{ host: 'h', root: 'ok' }])
  })

  it('enforces the limits: 32 groups, 64 members each', async () => {
    const groups: Record<string, unknown> = {}
    const order: string[] = []
    for (let i = 0; i < MAX_GROUPS + 3; i++) {
      const id = i.toString(16).padStart(8, '0')
      order.push(id)
      groups[id] = g({ members: Array.from({ length: MAX_MEMBERS + 5 }, (_, j) => ({ host: 'h', root: `r${i}-${j}` })) })
    }
    await rehydrateFrom({ order, groups })
    expect(st().order).toHaveLength(MAX_GROUPS)
    expect(st().groups[order[0]].members).toHaveLength(MAX_MEMBERS)
  })

  it('reads junk as empty', async () => {
    await rehydrateFrom({ order: 'x', groups: 5 })
    expect(st().order).toEqual([])
    expect(st().groups).toEqual({})
    await rehydrateFrom(null)
    expect(st().order).toEqual([])
  })

  it('a prototype-pollution key is not a group id', async () => {
    await rehydrateFrom(JSON.parse('{"order":["__proto__"],"groups":{"__proto__":{"name":"x","members":[],"createdAt":1}}}'))
    expect(st().order).toEqual([])
    expect(Object.keys(st().groups)).toEqual([])
  })
})
