// spa/src/stores/useHostLookStore.test.ts — the workbench's host looks (host ownership spec §4.1, H2c-1 T1).
import { beforeEach, describe, expect, it, vi } from 'vitest'

const registerSpy = vi.hoisted(() => vi.fn())

vi.mock('../lib/storage/sync', () => ({
  syncManager: { register: registerSpy, notify: vi.fn(), destroy: vi.fn() },
  createSyncManager: vi.fn(),
}))

import { useHostLookStore, type HostLookEntry } from './useHostLookStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

const RED = { console: { main: { color: '#ef4444', alpha: 100 } } }

/** Writes `state` as the persisted value and runs the store's own `merge` over it. */
async function rehydrateFrom(state: unknown): Promise<void> {
  localStorage.setItem(STORAGE_KEYS.HOST_LOOKS, JSON.stringify({ state, version: 1 }))
  await useHostLookStore.persist.rehydrate()
}

beforeEach(() => {
  localStorage.clear()
  useHostLookStore.setState({ looks: {} })
})

describe('useHostLookStore — shape', () => {
  it('defaults to { looks: {} }', () => {
    expect(useHostLookStore.getInitialState().looks).toEqual({})
  })

  it('persists under purdex-host-looks, looks only', () => {
    expect(STORAGE_KEYS.HOST_LOOKS).toBe('purdex-host-looks')
    useHostLookStore.getState().putLook('d1_a', { name: 'mlab' })
    const persisted = JSON.parse(localStorage.getItem(STORAGE_KEYS.HOST_LOOKS) ?? '{}') as { state: Record<string, unknown> }
    expect(persisted.state).toEqual({ looks: { d1_a: { name: 'mlab' } } })
  })

  it('is registered with syncManager under its storage key', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.HOST_LOOKS, useHostLookStore)
  })

  it('the migration marker is its own key, documented as device-local', () => {
    expect(STORAGE_KEYS.HOST_LOOKS_MIGRATED).toBe('purdex-host-looks-migrated')
    expect(registerSpy).not.toHaveBeenCalledWith(STORAGE_KEYS.HOST_LOOKS_MIGRATED, expect.anything())
  })
})

describe('useHostLookStore — merge sanitises every entry', () => {
  it('drops an invalid colour / legacy colour / icon / weight and every unknown field; keeps the valid ones', async () => {
    await rehydrateFrom({
      looks: {
        d1_a: { name: 'mlab', colors: RED, color: '#00ff00', icon: 'Laptop', iconWeight: 'bold' },
        d1_b: { name: 'air', colors: { console: { main: { alpha: 100 } } }, color: 'red', icon: 'NotAnIcon', iconWeight: 'heavy', token: 'secret', ip: '1.2.3.4' },
      },
    })
    const { looks } = useHostLookStore.getState()
    expect(looks.d1_a).toEqual({ name: 'mlab', colors: RED, color: '#00ff00', icon: 'Laptop', iconWeight: 'bold' })
    expect(looks.d1_b).toEqual({ name: 'air' })
  })

  it('drops a non-string name', async () => {
    await rehydrateFrom({ looks: { d1_a: { name: 42, icon: 'Laptop' }, d1_b: { name: null } } })
    expect(useHostLookStore.getState().looks).toEqual({ d1_a: { icon: 'Laptop' }, d1_b: {} })
  })

  it('drops null on every field — option A has no tombstone', async () => {
    await rehydrateFrom({ looks: { d1_a: { name: null, colors: null, color: null, icon: null, iconWeight: null } } })
    expect(useHostLookStore.getState().looks).toEqual({ d1_a: {} })
  })

  it('keeps unknown keys verbatim: a sync id no host here claims, a legacy local id', async () => {
    await rehydrateFrom({ looks: { d1_zzz: { name: 'far' }, 'k3Jq-local_9': { name: 'legacy' } } })
    expect(Object.keys(useHostLookStore.getState().looks).sort()).toEqual(['d1_zzz', 'k3Jq-local_9'])
  })

  it('an entry that is not a record is dropped; a looks value that is not a record reads as {}', async () => {
    await rehydrateFrom({ looks: { d1_a: 'x', d1_b: [1], d1_c: null, d1_d: { name: 'ok' } } })
    expect(useHostLookStore.getState().looks).toEqual({ d1_d: { name: 'ok' } })
    await rehydrateFrom({ looks: [] })
    expect(useHostLookStore.getState().looks).toEqual({})
    await rehydrateFrom({})
    expect(useHostLookStore.getState().looks).toEqual({})
  })

  it('never copies a __proto__ key', async () => {
    localStorage.setItem(STORAGE_KEYS.HOST_LOOKS, '{"state":{"looks":{"__proto__":{"name":"evil"},"d1_a":{"name":"ok"}}},"version":1}')
    await useHostLookStore.persist.rehydrate()
    const { looks } = useHostLookStore.getState()
    expect(Object.keys(looks)).toEqual(['d1_a'])
    expect(Object.getPrototypeOf(looks)).toBe(Object.prototype)
    expect(({} as Record<string, unknown>).name).toBeUndefined()
  })
})

describe('useHostLookStore — actions', () => {
  it('putLook writes a sanitised entry under the key as given', () => {
    useHostLookStore.getState().putLook('d1_a', { name: 'mlab', icon: 'NotAnIcon', colors: RED } as HostLookEntry)
    expect(useHostLookStore.getState().looks).toEqual({ d1_a: { name: 'mlab', colors: RED } })
  })

  it('putLook ignores the __proto__ key', () => {
    const before = useHostLookStore.getState()
    useHostLookStore.getState().putLook('__proto__', { name: 'evil' })
    expect(useHostLookStore.getState()).toBe(before)
  })

  it('patchLook hands the current entry (or undefined) to fn; undefined deletes; the same object writes nothing', () => {
    const { patchLook } = useHostLookStore.getState()
    patchLook('d1_a', (cur) => ({ ...cur, name: 'mlab' }))
    patchLook('d1_a', (cur) => ({ ...cur, icon: 'Laptop' }))
    expect(useHostLookStore.getState().looks.d1_a).toEqual({ name: 'mlab', icon: 'Laptop' })
    const before = useHostLookStore.getState()
    patchLook('d1_a', (cur) => cur)
    expect(useHostLookStore.getState()).toBe(before)
    patchLook('d1_a', () => undefined)
    expect(useHostLookStore.getState().looks).toEqual({})
    const empty = useHostLookStore.getState()
    patchLook('d1_nope', () => undefined) // absent → absent: nothing to write
    expect(useHostLookStore.getState()).toBe(empty)
  })

  it('patchLook sanitises what fn returns', () => {
    useHostLookStore.getState().patchLook('d1_a', () => ({ name: 'x', iconWeight: 'heavy' }) as unknown as HostLookEntry)
    expect(useHostLookStore.getState().looks.d1_a).toEqual({ name: 'x' })
  })

  it('putLooksIfAbsent writes only the keys with no entry; an existing entry is untouched (same object)', () => {
    useHostLookStore.getState().putLook('d1_a', { name: 'mine' })
    const mine = useHostLookStore.getState().looks.d1_a
    useHostLookStore.getState().putLooksIfAbsent({ d1_a: { name: 'theirs' }, d1_b: { name: 'new', icon: 'Laptop' } })
    const { looks } = useHostLookStore.getState()
    expect(looks.d1_a).toBe(mine)
    expect(looks.d1_b).toEqual({ name: 'new', icon: 'Laptop' })
  })

  it('putLooksIfAbsent with nothing absent writes nothing', () => {
    useHostLookStore.getState().putLook('d1_a', { name: 'mine' })
    const before = useHostLookStore.getState()
    useHostLookStore.getState().putLooksIfAbsent({ d1_a: { name: 'theirs' } })
    expect(useHostLookStore.getState()).toBe(before)
  })

  it('rekey moves an entry to its new key; an existing target wins and the source is dropped; a missing source is a no-op', () => {
    const { putLook } = useHostLookStore.getState()
    putLook('localA', { name: 'a' })
    putLook('localB', { name: 'b-local' })
    putLook('d1_b', { name: 'b-wire' })
    useHostLookStore.getState().rekey([['localA', 'd1_a'], ['localB', 'd1_b']])
    expect(useHostLookStore.getState().looks).toEqual({ d1_a: { name: 'a' }, d1_b: { name: 'b-wire' } })
    const before = useHostLookStore.getState()
    useHostLookStore.getState().rekey([['localZ', 'd1_z']])
    expect(useHostLookStore.getState()).toBe(before)
  })
})

describe('useHostLookStore — module boundary', () => {
  it('importing the module does not load useHostStore (keys are opaque; the store imports no store)', async () => {
    vi.resetModules()
    const loaded = vi.fn()
    vi.doMock('./useHostStore', () => {
      loaded()
      return {}
    })
    await import('./useHostLookStore')
    expect(loaded).not.toHaveBeenCalled()
    vi.doUnmock('./useHostStore')
  })
})
