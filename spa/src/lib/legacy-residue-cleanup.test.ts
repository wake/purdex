// Boot-time removal of what the deleted old Sync module and device-state / workspace-snapshot features left in the
// browser (#1303). Driven through the real `localStorage` and fake-indexeddb (test-setup).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  cleanupLegacyResidue,
  LEGACY_IDB_NAME,
  LEGACY_LOCAL_STORAGE_KEYS,
  scheduleLegacyResidueCleanup,
} from './legacy-residue-cleanup'

const IDENTITY_KEY = 'purdex-client-identity'
const SYNC_STATE_KEY = 'purdex-sync-state'
const LEGACY_ENVELOPE = JSON.stringify({ state: { clientId: 'c_0123456789ab' }, version: 0 })

function openLegacyDb(): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(LEGACY_IDB_NAME, 1)
    req.onupgradeneeded = () => req.result.createObjectStore('snapshots')
    req.onsuccess = () => {
      req.result.close()
      resolve()
    }
    req.onerror = () => reject(req.error)
  })
}

async function databaseNames(): Promise<string[]> {
  return (await indexedDB.databases()).map((d) => d.name ?? '')
}

let warn: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  localStorage.clear()
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe('cleanupLegacyResidue', () => {
  it('names exactly the residue keys and database', () => {
    expect([...LEGACY_LOCAL_STORAGE_KEYS]).toEqual(['purdex-workspace-snapshot', 'purdex-workspace-snapshot-prev', 'purdex-profile-pull-unconfirmed'])
    expect(LEGACY_IDB_NAME).toBe('purdex-sync')
  })

  it('removes the workspace-snapshot keys and leaves every live key alone', async () => {
    localStorage.setItem('purdex-workspace-snapshot', '{}')
    localStorage.setItem('purdex-workspace-snapshot-prev', '{}')
    localStorage.setItem('purdex-device-state', '{"live":true}')
    localStorage.setItem('purdex-tabs', '{"live":true}')
    localStorage.setItem('purdex-workspace-snapshot-x', 'not ours')
    await cleanupLegacyResidue()
    expect(localStorage.getItem('purdex-workspace-snapshot')).toBeNull()
    expect(localStorage.getItem('purdex-workspace-snapshot-prev')).toBeNull()
    expect(localStorage.getItem('purdex-device-state')).toBe('{"live":true}')
    expect(localStorage.getItem('purdex-tabs')).toBe('{"live":true}')
    expect(localStorage.getItem('purdex-workspace-snapshot-x')).toBe('not ours')
    expect(warn).not.toHaveBeenCalled()
  })

  it('removes the stopped-pull notice the #1366 pull guard wrote (host ownership H3b: nothing writes or reads it any more)', async () => {
    localStorage.setItem('purdex-profile-pull-unconfirmed', JSON.stringify({ hostId: 'h1', profileId: 'p_000000000001', at: 1 }))
    localStorage.setItem('purdex-profile', '{"live":true}')
    await cleanupLegacyResidue()
    expect(localStorage.getItem('purdex-profile-pull-unconfirmed')).toBeNull()
    expect(localStorage.getItem('purdex-profile')).toBe('{"live":true}')
    expect(warn).not.toHaveBeenCalled()
  })

  it('deletes the purdex-sync IndexedDB database and no other', async () => {
    await openLegacyDb()
    await new Promise<void>((resolve) => {
      const req = indexedDB.open('pdx-keep-me', 1)
      req.onsuccess = () => {
        req.result.close()
        resolve()
      }
    })
    expect(await databaseNames()).toContain(LEGACY_IDB_NAME)
    await cleanupLegacyResidue()
    const names = await databaseNames()
    expect(names).not.toContain(LEGACY_IDB_NAME)
    expect(names).toContain('pdx-keep-me')
    expect(warn).not.toHaveBeenCalled()
  })

  describe('purdex-sync-state (the client-id adoption source)', () => {
    it('is removed once the client id has its own key', async () => {
      localStorage.setItem(IDENTITY_KEY, 'c_aaaaaaaaaaaa')
      localStorage.setItem(SYNC_STATE_KEY, LEGACY_ENVELOPE)
      await cleanupLegacyResidue()
      expect(localStorage.getItem(SYNC_STATE_KEY)).toBeNull()
      expect(localStorage.getItem(IDENTITY_KEY)).toBe('c_aaaaaaaaaaaa')
    })

    it('is kept while the client id has no key of its own (it is still the adoption source)', async () => {
      localStorage.setItem(SYNC_STATE_KEY, LEGACY_ENVELOPE)
      await cleanupLegacyResidue()
      expect(localStorage.getItem(SYNC_STATE_KEY)).toBe(LEGACY_ENVELOPE)
      // A pure check: the cleanup itself must not adopt / create the id.
      expect(localStorage.getItem(IDENTITY_KEY)).toBeNull()
    })

    it('is kept when the own key holds something that is not an id', async () => {
      localStorage.setItem(IDENTITY_KEY, 'garbage')
      localStorage.setItem(SYNC_STATE_KEY, LEGACY_ENVELOPE)
      await cleanupLegacyResidue()
      expect(localStorage.getItem(SYNC_STATE_KEY)).toBe(LEGACY_ENVELOPE)
    })
  })

  it('is idempotent: a second run over a clean browser changes nothing and logs nothing', async () => {
    localStorage.setItem('purdex-workspace-snapshot', '{}')
    localStorage.setItem(IDENTITY_KEY, 'c_aaaaaaaaaaaa')
    localStorage.setItem(SYNC_STATE_KEY, LEGACY_ENVELOPE)
    await openLegacyDb()
    await cleanupLegacyResidue()
    const after = { ...localStorage }
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    await cleanupLegacyResidue()
    await cleanupLegacyResidue()
    expect({ ...localStorage }).toEqual(after)
    expect(setItem).not.toHaveBeenCalled()
    expect(await databaseNames()).not.toContain(LEGACY_IDB_NAME)
    expect(warn).not.toHaveBeenCalled()
  })

  it('skips the database when there is no indexedDB, and still clears the keys', async () => {
    vi.stubGlobal('indexedDB', undefined)
    localStorage.setItem('purdex-workspace-snapshot', '{}')
    await expect(cleanupLegacyResidue()).resolves.toBeUndefined()
    expect(localStorage.getItem('purdex-workspace-snapshot')).toBeNull()
    expect(warn).not.toHaveBeenCalled()
  })

  it('never rejects: every failure is swallowed and logged exactly once', async () => {
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError')
    })
    vi.stubGlobal('indexedDB', {
      deleteDatabase: () => {
        throw new Error('idb unavailable')
      },
    })
    localStorage.setItem(IDENTITY_KEY, 'c_aaaaaaaaaaaa')
    await expect(cleanupLegacyResidue()).resolves.toBeUndefined()
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('logs once when the database delete fails asynchronously', async () => {
    vi.stubGlobal('indexedDB', {
      deleteDatabase: () => {
        const req: { onsuccess: null | (() => void); onerror: null | (() => void); onblocked: null | (() => void); error: Error } = {
          onsuccess: null,
          onerror: null,
          onblocked: null,
          error: new Error('delete failed'),
        }
        queueMicrotask(() => req.onerror?.())
        return req
      },
    })
    await expect(cleanupLegacyResidue()).resolves.toBeUndefined()
    expect(warn).toHaveBeenCalledTimes(1)
  })
})

describe('scheduleLegacyResidueCleanup', () => {
  it('does nothing synchronously (boot is not held up) and cleans up on a later task', async () => {
    vi.useFakeTimers()
    try {
      localStorage.setItem('purdex-workspace-snapshot', '{}')
      expect(scheduleLegacyResidueCleanup()).toBeUndefined()
      expect(localStorage.getItem('purdex-workspace-snapshot')).toBe('{}')
      await vi.runAllTimersAsync()
      expect(localStorage.getItem('purdex-workspace-snapshot')).toBeNull()
    } finally {
      vi.useRealTimers()
    }
  })
})
