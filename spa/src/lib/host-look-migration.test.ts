import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createJSONStorage } from 'zustand/middleware'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useHostLookStore } from '../stores/useHostLookStore'
import { purdexStorage, STORAGE_KEYS, syncManager } from './storage'
import { PROJECTIONS } from './profile/projections'
import { syncIdOfSync } from './profile/host-identity'
import { bootHostLooks, migrateHostLooksOnce } from './host-look-migration'

const MARKER = STORAGE_KEYS.HOST_LOOKS_MIGRATED
const DAEMON = 'mini-lab:278cbm'
const WIRE = syncIdOfSync(DAEMON)

const withDaemon: HostConfig = {
  id: 'host-a',
  name: 'A',
  ip: '10.0.0.1',
  port: 7860,
  order: 0,
  daemonId: DAEMON,
  colors: { console: { main: { color: '#3b82f6', alpha: 100 } } },
  color: '#22c55e',
  icon: 'Laptop',
  iconWeight: 'duotone',
  token: 'secret',
}
const noDaemon: HostConfig = { id: 'host-b', name: 'B', ip: '10.0.0.2', port: 7860, order: 1 }

function snapshot(...hosts: HostConfig[]) {
  return { hosts: Object.fromEntries(hosts.map((h) => [h.id, h])), hostOrder: hosts.map((h) => h.id) }
}

beforeEach(() => {
  localStorage.removeItem(MARKER)
  useHostLookStore.setState({ looks: {} })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('migrateHostLooksOnce (spec §4.3)', () => {
  it('copies each host’s present look fields under its CURRENT wire id: d1_ with a daemonId, the local id without', () => {
    expect(migrateHostLooksOnce(snapshot(withDaemon, noDaemon))).toBe('migrated')
    expect(useHostLookStore.getState().looks).toEqual({
      [WIRE]: {
        name: 'A',
        colors: { console: { main: { color: '#3b82f6', alpha: 100 } } },
        color: '#22c55e',
        icon: 'Laptop',
        iconWeight: 'duotone',
      },
      'host-b': { name: 'B' },
    })
    expect(localStorage.getItem(MARKER)).toBe('1')
  })

  it('never overwrites an existing entry', () => {
    useHostLookStore.setState({ looks: { [WIRE]: { name: 'workbench' } } })
    migrateHostLooksOnce(snapshot(withDaemon, noDaemon))
    expect(useHostLookStore.getState().looks[WIRE]).toEqual({ name: 'workbench' })
    expect(useHostLookStore.getState().looks['host-b']).toEqual({ name: 'B' })
  })

  it('writes the look store once (one setState)', () => {
    const seen = vi.fn()
    const unsub = useHostLookStore.subscribe(seen)
    migrateHostLooksOnce(snapshot(withDaemon, noDaemon))
    unsub()
    expect(seen).toHaveBeenCalledTimes(1)
  })

  it('with the marker set nothing runs: a look the user reset stays reset', () => {
    localStorage.setItem(MARKER, '1')
    expect(migrateHostLooksOnce(snapshot(withDaemon, noDaemon))).toBe('already')
    expect(useHostLookStore.getState().looks).toEqual({})
  })

  it('a marker read that throws reads as "not migrated": the migration runs', () => {
    localStorage.setItem(MARKER, '1')
    const real = Storage.prototype.getItem
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(function (this: Storage, key: string) {
      if (key === MARKER) throw new Error('SecurityError')
      return real.call(this, key)
    })
    expect(migrateHostLooksOnce(snapshot(noDaemon))).toBe('migrated')
    expect(useHostLookStore.getState().looks).toEqual({ 'host-b': { name: 'B' } })
  })

  it('a marker write that throws is harmless: the looks are written, the next run skips every present key', () => {
    const real = Storage.prototype.setItem
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === MARKER) throw new Error('QuotaExceededError')
      return real.call(this, key, value)
    })
    expect(migrateHostLooksOnce(snapshot(noDaemon))).toBe('migrated')
    expect(useHostLookStore.getState().looks).toEqual({ 'host-b': { name: 'B' } })
  })

  it('two rows claiming one daemon (conflict) map to one key: one entry, the first in hostOrder', () => {
    const dup: HostConfig = { ...noDaemon, id: 'host-dup', name: 'Dup', daemonId: DAEMON }
    const snap = snapshot(withDaemon, dup)
    snap.hostOrder = ['host-dup', 'host-a']
    migrateHostLooksOnce(snap)
    expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: { name: 'Dup' } })
  })

  it('hosts outside hostOrder are migrated after the ordered ones', () => {
    const snap = snapshot(noDaemon)
    snap.hosts['host-c'] = { ...noDaemon, id: 'host-c', name: 'C' }
    migrateHostLooksOnce(snap)
    expect(Object.keys(useHostLookStore.getState().looks)).toEqual(['host-b', 'host-c'])
  })

  it('the marker is device-local: not projected, not registered with syncManager', async () => {
    for (const paths of Object.values(PROJECTIONS)) {
      for (const path of paths) expect(path.replace(/^!/, '').startsWith(MARKER)).toBe(false)
    }
    vi.resetModules()
    const storage = await import('./storage')
    const register = vi.spyOn(storage.syncManager, 'register')
    await import('./host-look-migration')
    expect(register).not.toHaveBeenCalledWith(MARKER, expect.anything())
    expect(syncManager).toBeDefined()
  })
})

describe('bootHostLooks (plan §0.16): after both stores hydrated, before anything else reads the looks', () => {
  /** A storage whose reads wait until `release()` — an async hydration. */
  function heldStorage() {
    let release!: () => void
    const gate = new Promise<void>((r) => {
      release = r
    })
    const storage = createJSONStorage(() => ({
      getItem: async (key: string) => {
        await gate
        return localStorage.getItem(key)
      },
      setItem: (key: string, value: string) => localStorage.setItem(key, value),
      removeItem: (key: string) => localStorage.removeItem(key),
    }))
    return { storage, release }
  }

  const flush = async () => {
    for (let i = 0; i < 10; i++) await Promise.resolve()
  }

  afterEach(() => {
    useHostStore.persist.setOptions({ storage: purdexStorage })
    useHostLookStore.persist.setOptions({ storage: purdexStorage })
    useHostStore.getState().reset()
  })

  it('does not migrate before BOTH stores hydrated, then migrates the hydrated hosts (not the defaults)', async () => {
    useHostStore.getState().reset()
    // What storage holds (the hydration will read it): one host, and no look.
    localStorage.setItem(STORAGE_KEYS.HOSTS, JSON.stringify({ state: { ...snapshot(noDaemon), activeHostId: 'host-b', devHostId: null }, version: 1 }))
    localStorage.setItem(STORAGE_KEYS.HOST_LOOKS, JSON.stringify({ state: { looks: {} }, version: 1 }))
    const hostHeld = heldStorage()
    const lookHeld = heldStorage()
    useHostStore.persist.setOptions({ storage: hostHeld.storage })
    useHostLookStore.persist.setOptions({ storage: lookHeld.storage })
    const hostHydration = useHostStore.persist.rehydrate()
    const lookHydration = useHostLookStore.persist.rehydrate()
    expect(useHostStore.persist.hasHydrated()).toBe(false)
    expect(useHostLookStore.persist.hasHydrated()).toBe(false)

    let outcome: string | null = null
    void bootHostLooks().then((o) => {
      outcome = o
    })
    await flush()
    expect(outcome).toBeNull()
    expect(localStorage.getItem(MARKER)).toBeNull()

    hostHeld.release()
    await hostHydration
    await flush()
    expect(Object.keys(useHostStore.getState().hosts)).toEqual(['host-b'])
    expect(outcome).toBeNull() // one store is not enough
    expect(localStorage.getItem(MARKER)).toBeNull()
    expect(useHostLookStore.getState().looks).toEqual({})

    lookHeld.release()
    await lookHydration
    await flush()
    expect(outcome).toBe('migrated')
    expect(useHostLookStore.getState().looks).toEqual({ 'host-b': { name: 'B' } }) // the hydrated host, not `mlab`
    expect(localStorage.getItem(MARKER)).toBe('1')
  })

  it('both already hydrated → migrates at once (a microtask)', async () => {
    useHostStore.getState().reset()
    const defaultId = useHostStore.getState().hostOrder[0]
    await expect(bootHostLooks()).resolves.toBe('migrated')
    expect(useHostLookStore.getState().looks).toEqual({ [defaultId]: { name: 'mlab' } })
  })

  it('never rejects: a migration that throws is reported and boot goes on', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const write = vi.spyOn(useHostLookStore, 'setState').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })
    await expect(bootHostLooks()).resolves.toBe('failed')
    write.mockRestore()
    expect(error).toHaveBeenCalled()
    expect(localStorage.getItem(MARKER)).toBeNull()
  })
})
