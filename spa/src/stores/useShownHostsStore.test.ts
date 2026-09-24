// spa/src/stores/useShownHostsStore.test.ts — the hosts shown in a workbench: a plain list of wire ids (host ownership
// spec §1.2 / §4.1, plan H2d-1 T1, §0.6 / §0.7).
import { beforeEach, describe, expect, it, vi } from 'vitest'

const registerSpy = vi.hoisted(() => vi.fn())

vi.mock('../lib/storage/sync', () => ({
  syncManager: { register: registerSpy, notify: vi.fn(), destroy: vi.fn() },
  createSyncManager: vi.fn(),
}))

import { useShownHostsStore } from './useShownHostsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

/** Writes `state` as the persisted value and runs the store's own `merge` over it. */
async function rehydrateFrom(state: unknown): Promise<void> {
  localStorage.setItem(STORAGE_KEYS.SHOWN_HOSTS, JSON.stringify({ state, version: 1 }))
  await useShownHostsStore.persist.rehydrate()
}

const ids = () => useShownHostsStore.getState().ids
const persisted = () => (JSON.parse(localStorage.getItem(STORAGE_KEYS.SHOWN_HOSTS) ?? '{}') as { state?: Record<string, unknown> }).state

beforeEach(() => {
  localStorage.clear()
  useShownHostsStore.setState({ ids: [], relabelStamp: 0 })
})

describe('useShownHostsStore — shape (§0.6: { ids }, no `all`)', () => {
  it('defaults to { ids: [] } — every host hidden (user rule 2)', () => {
    expect(useShownHostsStore.getInitialState().ids).toEqual([])
  })

  it('the state holds ids, relabelStamp and the four actions — no all / showAll / setShown / addShown', () => {
    expect(Object.keys(useShownHostsStore.getState()).sort()).toEqual(['hide', 'ids', 'rekey', 'relabelStamp', 'show', 'toggle'])
  })

  it('persists under purdex-shown-hosts, exactly { ids, relabelStamp }', () => {
    expect(STORAGE_KEYS.SHOWN_HOSTS).toBe('purdex-shown-hosts')
    useShownHostsStore.setState({ relabelStamp: 3 })
    useShownHostsStore.getState().show('d1_a')
    expect(persisted()).toEqual({ ids: ['d1_a'], relabelStamp: 3 })
  })

  it('is registered with syncManager under its storage key', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.SHOWN_HOSTS, useShownHostsStore)
  })
})

describe('useShownHostsStore — merge', () => {
  it('keeps strings only, dedupes keeping the first, keeps unknown ids and their order', async () => {
    await rehydrateFrom({ ids: ['d1_zzz', 'local-1', 3, null, 'd1_a', 'd1_zzz', { x: 1 }, 'local-1', 'd1_b'] })
    expect(ids()).toEqual(['d1_zzz', 'local-1', 'd1_a', 'd1_b'])
  })

  it('a non-array ids reads as []', async () => {
    useShownHostsStore.setState({ ids: ['d1_a'] })
    await rehydrateFrom({ ids: 'd1_a' })
    expect(ids()).toEqual([])
    useShownHostsStore.setState({ ids: ['d1_a'] })
    await rehydrateFrom({})
    expect(ids()).toEqual([])
  })

  it('a stored legacy `all` key (rev-6 dev builds) is ignored: not in memory, not in the next persisted value', async () => {
    await rehydrateFrom({ all: true, ids: ['d1_a'] })
    expect(Object.hasOwn(useShownHostsStore.getState(), 'all')).toBe(false)
    expect(ids()).toEqual(['d1_a'])
    useShownHostsStore.getState().show('d1_b')
    expect(persisted()).toEqual({ ids: ['d1_a', 'd1_b'], relabelStamp: 0 })
  })

  it('nothing stored keeps memory as it is', async () => {
    useShownHostsStore.setState({ ids: ['d1_a'] })
    await useShownHostsStore.persist.rehydrate()
    expect(ids()).toEqual(['d1_a'])
  })
})

// The master's list and a promote (per-workbench shown hosts plan §2, fail closed 2): the reader trusts `ids` only while
// `relabelStamp` equals `useLocalProfilesStore.relabelCount`. Device-local, never projected.
describe('useShownHostsStore — relabelStamp', () => {
  const writeLocalProfiles = (state: unknown) => localStorage.setItem(STORAGE_KEYS.LOCAL_PROFILES, JSON.stringify({ state, version: 1 }))

  it('a stored stamp is kept as it is', async () => {
    writeLocalProfiles({ relabelCount: 9 })
    await rehydrateFrom({ ids: ['d1_a'], relabelStamp: 4 })
    expect(useShownHostsStore.getState()).toMatchObject({ ids: ['d1_a'], relabelStamp: 4 })
  })

  it("a record without one (pre-upgrade data) is stamped once with the local profiles' stored relabelCount", async () => {
    writeLocalProfiles({ relabelCount: 7 })
    await rehydrateFrom({ ids: ['d1_a'] })
    expect(useShownHostsStore.getState()).toMatchObject({ ids: ['d1_a'], relabelStamp: 7 })
  })

  it.each([['a string', '3'], ['negative', -1], ['a fraction', 1.5], ['null', null]])('a junk stamp (%s) is stamped the same way', async (_label, junk) => {
    writeLocalProfiles({ relabelCount: 2 })
    await rehydrateFrom({ ids: [], relabelStamp: junk })
    expect(useShownHostsStore.getState().relabelStamp).toBe(2)
  })

  it('empty storage (nothing of this store stored): memory keeps its ids and is stamped with the stored relabelCount', async () => {
    writeLocalProfiles({ relabelCount: 5 })
    useShownHostsStore.setState({ ids: ['d1_a'], relabelStamp: 0 })
    localStorage.removeItem(STORAGE_KEYS.SHOWN_HOSTS) // setState persisted it
    await useShownHostsStore.persist.rehydrate()
    expect(useShownHostsStore.getState()).toMatchObject({ ids: ['d1_a'], relabelStamp: 5 })
  })

  it('nothing stored anywhere, or local profiles unreadable / junk → 0 (the relabelCount such storage reads as)', async () => {
    useShownHostsStore.setState({ relabelStamp: 9 })
    localStorage.clear()
    await useShownHostsStore.persist.rehydrate()
    expect(useShownHostsStore.getState().relabelStamp).toBe(0)
    localStorage.setItem(STORAGE_KEYS.LOCAL_PROFILES, '{not json')
    await rehydrateFrom({ ids: [] })
    expect(useShownHostsStore.getState().relabelStamp).toBe(0)
    writeLocalProfiles({ relabelCount: 'x' })
    await rehydrateFrom({ ids: [] })
    expect(useShownHostsStore.getState().relabelStamp).toBe(0)
  })

  it('an action carries the stamp through untouched', () => {
    useShownHostsStore.setState({ relabelStamp: 4 })
    useShownHostsStore.getState().show('d1_a')
    useShownHostsStore.getState().rekey([['d1_a', 'd1_b']])
    expect(useShownHostsStore.getState().relabelStamp).toBe(4)
  })
})

describe('useShownHostsStore — actions: each touches exactly one id', () => {
  it('show appends once; the second call is a no-op (same state object)', () => {
    useShownHostsStore.setState({ ids: ['d1_unknown'] })
    useShownHostsStore.getState().show('d1_a')
    const after = useShownHostsStore.getState()
    useShownHostsStore.getState().show('d1_a')
    expect(useShownHostsStore.getState()).toBe(after)
    expect(ids()).toEqual(['d1_unknown', 'd1_a'])
  })

  it('hide removes only that id — the others, unknown ones included, keep their order; absent → no-op', () => {
    useShownHostsStore.setState({ ids: ['d1_unknown', 'd1_a', 'local-x', 'd1_b'] })
    useShownHostsStore.getState().hide('d1_a')
    expect(ids()).toEqual(['d1_unknown', 'local-x', 'd1_b'])
    const before = useShownHostsStore.getState()
    useShownHostsStore.getState().hide('d1_a')
    expect(useShownHostsStore.getState()).toBe(before)
  })

  it('toggle both ways; nothing else in the list changes', () => {
    useShownHostsStore.setState({ ids: ['d1_unknown', 'd1_a', 'd1_b'] })
    useShownHostsStore.getState().toggle('d1_a')
    expect(ids()).toEqual(['d1_unknown', 'd1_b'])
    useShownHostsStore.getState().toggle('d1_c')
    expect(ids()).toEqual(['d1_unknown', 'd1_b', 'd1_c'])
  })

  it('toggle on an empty list shows exactly that id (never "every host but one")', () => {
    useShownHostsStore.getState().toggle('d1_a')
    expect(ids()).toEqual(['d1_a'])
  })

  it('rekey replaces a local id in place; an already-present d1_ drops the local id; a missing source is a no-op', () => {
    useShownHostsStore.setState({ ids: ['d1_unknown', 'localA', 'd1_b', 'localB', 'tail'] })
    useShownHostsStore.getState().rekey([['localA', 'd1_a'], ['localB', 'd1_b']])
    expect(ids()).toEqual(['d1_unknown', 'd1_a', 'd1_b', 'tail'])
    const before = useShownHostsStore.getState()
    useShownHostsStore.getState().rekey([['localZ', 'd1_z']])
    expect(useShownHostsStore.getState()).toBe(before)
  })
})

describe('useShownHostsStore — module boundary', () => {
  it('importing the module does not load useHostStore (ids are opaque wire ids; the store imports no store)', async () => {
    vi.resetModules()
    const loaded = vi.fn()
    vi.doMock('./useHostStore', () => {
      loaded()
      return {}
    })
    await import('./useShownHostsStore')
    expect(loaded).not.toHaveBeenCalled()
    vi.doUnmock('./useHostStore')
  })
})
