// `getClientId()` — the id daemon-side records (storage backups, profile
// writes) hang off. Storage is the truth: every call reads the key, so
// these tests drive it through `localStorage` rather than through module state.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from './storage'

const KEY = 'purdex-client-identity'
const LEGACY_KEY = 'purdex-sync-state'
const ID_PATTERN = /^c_[0-9a-f]{12}$/
const LEGACY_ID = 'c_0123456789ab'

type Identity = typeof import('./client-identity')

/** A fresh module graph = a fresh realm (its own module-level state) over the SAME `localStorage`. */
async function openRealm(): Promise<Identity> {
  vi.resetModules()
  return import('./client-identity')
}

/** What zustand `persist` actually writes for `useSyncStore`: `{state, version}`. */
function legacyEnvelope(clientId: unknown): string {
  return JSON.stringify({ state: { clientId, enabledModules: [] }, version: 0 })
}

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
})

describe('getClientId', () => {
  it('registers its own storage key', () => {
    expect(STORAGE_KEYS.CLIENT_IDENTITY).toBe(KEY)
  })

  it('generates a well-formed id and writes it to its own key when nothing exists', async () => {
    const { getClientId } = await openRealm()
    const id = getClientId()
    expect(id).toMatch(ID_PATTERN)
    expect(localStorage.getItem(KEY)).toBe(id)
  })

  it('is stable across calls and across realms', async () => {
    const a = await openRealm()
    const id = a.getClientId()
    expect(a.getClientId()).toBe(id)
    const b = await openRealm()
    expect(b.getClientId()).toBe(id)
  })

  it('returns the stored id without rewriting it', async () => {
    localStorage.setItem(KEY, 'c_aaaaaaaaaaaa')
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    const { getClientId } = await openRealm()
    expect(getClientId()).toBe('c_aaaaaaaaaaaa')
    expect(setItem).not.toHaveBeenCalled()
  })

  it('adopts the legacy sync-store id so daemon-side records keep their owner', async () => {
    localStorage.setItem(LEGACY_KEY, legacyEnvelope(LEGACY_ID))
    const { getClientId } = await openRealm()
    expect(getClientId()).toBe(LEGACY_ID)
    expect(localStorage.getItem(KEY)).toBe(LEGACY_ID)
  })

  it('prefers its own key over the legacy one once it holds a valid id', async () => {
    localStorage.setItem(KEY, 'c_aaaaaaaaaaaa')
    localStorage.setItem(LEGACY_KEY, legacyEnvelope(LEGACY_ID))
    const { getClientId } = await openRealm()
    expect(getClientId()).toBe('c_aaaaaaaaaaaa')
  })

  it.each([
    ['garbage', 'not-an-id'],
    ['an empty string', ''],
    ['uppercase hex', 'c_AAAAAAAAAAAA'],
    ['too short', 'c_abc'],
    ['a JSON wrapper', JSON.stringify({ clientId: 'c_aaaaaaaaaaaa' })],
  ])('replaces a malformed stored value (%s)', async (_label, bad) => {
    localStorage.setItem(KEY, bad)
    const { getClientId } = await openRealm()
    const id = getClientId()
    expect(id).toMatch(ID_PATTERN)
    expect(localStorage.getItem(KEY)).toBe(id)
  })

  it('a malformed own key still falls back to adopting the legacy id', async () => {
    localStorage.setItem(KEY, 'not-an-id')
    localStorage.setItem(LEGACY_KEY, legacyEnvelope(LEGACY_ID))
    const { getClientId } = await openRealm()
    expect(getClientId()).toBe(LEGACY_ID)
  })

  it.each([
    ['broken JSON', '{not json'],
    ['a malformed id', legacyEnvelope('c_local')],
    ['a null id', legacyEnvelope(null)],
    ['a non-string id', legacyEnvelope(42)],
    ['no state', JSON.stringify({ version: 0 })],
    ['a JSON null', 'null'],
    ['a bare string', JSON.stringify(LEGACY_ID)],
  ])('ignores a legacy entry holding %s and generates instead', async (_label, legacy) => {
    localStorage.setItem(LEGACY_KEY, legacy)
    const { getClientId } = await openRealm()
    const id = getClientId()
    expect(id).toMatch(ID_PATTERN)
    expect(id).not.toBe(LEGACY_ID)
    expect(localStorage.getItem(KEY)).toBe(id)
    expect(localStorage.getItem(LEGACY_KEY)).toBe(legacy)
  })

  it('two realms racing the first call converge on the last writer', async () => {
    const a = await openRealm()
    const b = await openRealm()

    // Interleave: A reads the key (empty) → B runs its whole first call
    // (reads empty, generates, writes) → A carries on with its stale "empty"
    // read, generates its own and overwrites B's.
    const realGetItem = Storage.prototype.getItem
    let idFromB: string | null = null
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(function (
      this: Storage,
      key: string,
    ) {
      const value = realGetItem.call(this, key)
      if (key === KEY && idFromB === null) {
        idFromB = '' // re-entrancy guard: only the very first read yields to B
        idFromB = b.getClientId()
      }
      return value
    })
    const idFromA = a.getClientId()
    spy.mockRestore()

    expect(idFromB).toMatch(ID_PATTERN)
    expect(idFromA).toMatch(ID_PATTERN)
    expect(idFromA).not.toBe(idFromB) // they really did each generate one
    expect(localStorage.getItem(KEY)).toBe(idFromA) // A wrote last

    // The point: B must not keep answering with the id it generated.
    expect(b.getClientId()).toBe(idFromA)
    expect(a.getClientId()).toBe(idFromA)
  })

  it('does not throw when localStorage is unavailable, and stays stable within the realm', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError')
    })
    const { getClientId } = await openRealm()
    const id = getClientId()
    expect(id).toMatch(ID_PATTERN)
    expect(getClientId()).toBe(id)
  })

  it('does not throw when only writes fail (quota), and stays stable within the realm', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError')
    })
    const { getClientId } = await openRealm()
    const id = getClientId()
    expect(id).toMatch(ID_PATTERN)
    expect(getClientId()).toBe(id)
  })

  it('still adopts the legacy id when the write fails', async () => {
    localStorage.setItem(LEGACY_KEY, legacyEnvelope(LEGACY_ID))
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError')
    })
    const { getClientId } = await openRealm()
    expect(getClientId()).toBe(LEGACY_ID)
  })
})

describe('isClientIdPersisted', () => {
  it('true once the id is in storage — and asking is enough to create it', async () => {
    const { isClientIdPersisted, getClientId } = await openRealm()
    expect(localStorage.getItem(KEY)).toBeNull()
    expect(isClientIdPersisted()).toBe(true)
    expect(localStorage.getItem(KEY)).toMatch(ID_PATTERN)
    expect(getClientId()).toBe(localStorage.getItem(KEY))
  })

  it('true for an id that was already stored, without rewriting it', async () => {
    localStorage.setItem(KEY, 'c_aaaaaaaaaaaa')
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    const { isClientIdPersisted } = await openRealm()
    expect(isClientIdPersisted()).toBe(true)
    expect(setItem).not.toHaveBeenCalled()
  })

  it('false when writes throw (quota): the id lives in this realm only', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError')
    })
    const { isClientIdPersisted, getClientId } = await openRealm()
    expect(getClientId()).toMatch(ID_PATTERN)
    expect(isClientIdPersisted()).toBe(false)
  })

  it('false when writes are silently dropped', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {})
    const { isClientIdPersisted } = await openRealm()
    expect(isClientIdPersisted()).toBe(false)
  })

  it('false when reads throw', async () => {
    localStorage.setItem(KEY, 'c_aaaaaaaaaaaa')
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError')
    })
    const { isClientIdPersisted } = await openRealm()
    expect(isClientIdPersisted()).toBe(false)
  })

  it('false for an adopted legacy id that could not be written to its own key', async () => {
    localStorage.setItem(LEGACY_KEY, legacyEnvelope(LEGACY_ID))
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError')
    })
    const { isClientIdPersisted, getClientId } = await openRealm()
    expect(getClientId()).toBe(LEGACY_ID)
    expect(isClientIdPersisted()).toBe(false)
  })

  it('still true after the stored id changes under it — storage is the truth', async () => {
    const { isClientIdPersisted, getClientId } = await openRealm()
    expect(isClientIdPersisted()).toBe(true)
    localStorage.setItem(KEY, 'c_bbbbbbbbbbbb')
    expect(isClientIdPersisted()).toBe(true)
    expect(getClientId()).toBe('c_bbbbbbbbbbbb')
  })

  it('re-creates the id after storage is cleared, and is true again', async () => {
    const { isClientIdPersisted } = await openRealm()
    expect(isClientIdPersisted()).toBe(true)
    localStorage.clear()
    expect(isClientIdPersisted()).toBe(true)
    expect(localStorage.getItem(KEY)).toMatch(ID_PATTERN)
  })

  it('turns false when storage stops accepting writes after it was cleared', async () => {
    const { isClientIdPersisted } = await openRealm()
    expect(isClientIdPersisted()).toBe(true)
    localStorage.clear()
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError')
    })
    expect(isClientIdPersisted()).toBe(false)
  })
})

// A pure read for the residue cleanup: the legacy `purdex-sync-state` may be dropped only once the id has its own
// key. Unlike `isClientIdPersisted()` it must never create or adopt an id.
describe('hasPersistedClientId', () => {
  it('is false when nothing is stored, and stays a pure read (no id is created)', async () => {
    localStorage.setItem(LEGACY_KEY, legacyEnvelope(LEGACY_ID))
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    const { hasPersistedClientId } = await openRealm()
    expect(hasPersistedClientId()).toBe(false)
    expect(setItem).not.toHaveBeenCalled()
    expect(localStorage.getItem(KEY)).toBeNull()
  })

  it('is true when the key holds a well-formed id', async () => {
    localStorage.setItem(KEY, 'c_aaaaaaaaaaaa')
    const { hasPersistedClientId } = await openRealm()
    expect(hasPersistedClientId()).toBe(true)
  })

  it('is false when the key holds something that is not an id', async () => {
    localStorage.setItem(KEY, 'not-an-id')
    const { hasPersistedClientId } = await openRealm()
    expect(hasPersistedClientId()).toBe(false)
  })

  it('is false when storage cannot be read', async () => {
    localStorage.setItem(KEY, 'c_aaaaaaaaaaaa')
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError')
    })
    const { hasPersistedClientId } = await openRealm()
    expect(hasPersistedClientId()).toBe(false)
  })
})
