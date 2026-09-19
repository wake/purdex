import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../storage/keys'
import moduleSource from './section-store.ts?raw'
import {
  MAX_STASH_PAYLOAD_BYTES,
  clearSectionStore,
  dropSection,
  getStash,
  loadSectionStore,
  pruneStash,
  putStash,
  saveSection,
  type PersistedSection,
} from './section-store'

const KEY = STORAGE_KEYS.PROFILE_SECTIONS
const P1 = 'p_0123456789ab'
const P2 = 'p_ba9876543210'
const H = (c: string): string => c.repeat(64)

const SETTINGS: PersistedSection = { base: { rev: 3, hash: H('a') }, currentHash: H('b') }
const HOSTS: PersistedSection = { base: { rev: 0, hash: null }, currentHash: null }

const raw = (): string | null => localStorage.getItem(KEY)
const writeRaw = (value: unknown): void =>
  localStorage.setItem(KEY, typeof value === 'string' ? value : JSON.stringify(value))

const EMPTY = (profileId: string) => ({ profileId, sections: {}, stash: {} })

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('section-store', () => {
  it('uses its own storage key', () => {
    expect(KEY).toBe('purdex-profile-sections')
  })

  it('nothing stored → empty, and the load does not write', () => {
    expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
    expect(raw()).toBeNull()
  })

  it('round-trips sections', () => {
    expect(saveSection(P1, 'settings', SETTINGS)).toBe(true)
    expect(saveSection(P1, 'hosts', HOSTS)).toBe(true)

    expect(loadSectionStore(P1)).toEqual({ profileId: P1, sections: { settings: SETTINGS, hosts: HOSTS }, stash: {} })
  })

  it('saveSection replaces the section of the same key', () => {
    saveSection(P1, 'settings', SETTINGS)
    saveSection(P1, 'settings', HOSTS)
    expect(loadSectionStore(P1).sections).toEqual({ settings: HOSTS })
  })

  it('round-trips a conflict — the lock survives a restart with the snapshot that was sent', () => {
    const locked: PersistedSection = {
      base: { rev: 3, hash: H('a') },
      currentHash: H('c'),
      conflict: { localHash: H('b'), sot: { rev: 5, hash: H('d') } },
    }
    const deleteLocked: PersistedSection = {
      base: { rev: 3, hash: H('a') },
      currentHash: null,
      conflict: { localHash: null, sot: { rev: 6, hash: null } },
    }
    saveSection(P1, 'settings', locked)
    saveSection(P1, 'tabs.ws1', deleteLocked)

    const loaded = loadSectionStore(P1).sections
    expect(loaded.settings).toEqual(locked)
    expect(loaded['tabs.ws1']).toEqual(deleteLocked)
  })

  it('a section saved without a conflict comes back without the key', () => {
    saveSection(P1, 'settings', { ...SETTINGS, conflict: { localHash: null, sot: { rev: 4, hash: null } } })
    saveSection(P1, 'settings', SETTINGS)
    expect('conflict' in loadSectionStore(P1).sections.settings).toBe(false)
  })

  it('dropSection removes one section and keeps the rest', () => {
    saveSection(P1, 'settings', SETTINGS)
    saveSection(P1, 'hosts', HOSTS)

    dropSection(P1, 'settings')

    expect(loadSectionStore(P1).sections).toEqual({ hosts: HOSTS })
  })

  describe('the data belongs to one profile', () => {
    beforeEach(() => {
      saveSection(P1, 'settings', SETTINGS)
      putStash(P1, H('b'), { a: 1 })
    })

    it('another profile reads empty — a different master never sees these bases', () => {
      expect(loadSectionStore(P2)).toEqual(EMPTY(P2))
      expect(getStash(P2, H('b'))).toBeUndefined()
    })

    it('reading as another profile does not destroy what is there', () => {
      const before = raw()
      loadSectionStore(P2)
      getStash(P2, H('b'))
      expect(raw()).toBe(before)
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
    })

    it('dropSection / pruneStash as another profile leave it alone too', () => {
      const before = raw()
      dropSection(P2, 'settings')
      pruneStash(P2, new Set())
      expect(raw()).toBe(before)
    })

    it('saveSection as another profile discards the old profile wholesale', () => {
      saveSection(P2, 'hosts', HOSTS)

      expect(loadSectionStore(P2)).toEqual({ profileId: P2, sections: { hosts: HOSTS }, stash: {} })
      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
      expect(raw()).not.toContain(H('b'))
    })

    it('putStash as another profile discards the old profile wholesale', () => {
      putStash(P2, H('e'), { z: 1 })

      expect(loadSectionStore(P2)).toEqual({ profileId: P2, sections: {}, stash: { [H('e')]: { z: 1 } } })
      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
    })
  })

  describe('validation — what storage holds is checked, not cast', () => {
    it.each([
      ['not JSON', '{nope'],
      ['JSON null', 'null'],
      ['a string', '"x"'],
      ['an array', '[]'],
      ['no profileId', { sections: {}, stash: {} }],
      ['a malformed profileId', { profileId: 'P1', sections: {}, stash: {} }],
      ['sections that is an array', { profileId: P1, sections: [], stash: {} }],
      ['sections that is null', { profileId: P1, sections: null, stash: {} }],
      ['a stash that is a string', { profileId: P1, sections: {}, stash: 'x' }],
    ])('top level is %s → empty, and not written back', (_label, stored) => {
      writeRaw(stored)
      const before = raw()
      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
      expect(raw()).toBe(before)
    })

    const bad: Array<[string, unknown]> = [
      ['not an object', 'x'],
      ['null', null],
      ['no base', { currentHash: null }],
      ['a negative rev', { base: { rev: -1, hash: null }, currentHash: null }],
      ['a fractional rev', { base: { rev: 1.5, hash: null }, currentHash: null }],
      ['an unsafe rev', { base: { rev: 2 ** 53, hash: null }, currentHash: null }],
      ['a string rev', { base: { rev: '3', hash: null }, currentHash: null }],
      ['a short base hash', { base: { rev: 1, hash: 'abc' }, currentHash: null }],
      ['an upper-case base hash', { base: { rev: 1, hash: H('A') }, currentHash: null }],
      ['a missing base hash', { base: { rev: 1 }, currentHash: null }],
      ['a numeric currentHash', { base: { rev: 1, hash: null }, currentHash: 5 }],
      ['a missing currentHash', { base: { rev: 1, hash: null } }],
      ['a conflict that is a string', { base: { rev: 1, hash: null }, currentHash: null, conflict: 'x' }],
      ['a conflict without sot', { base: { rev: 1, hash: null }, currentHash: null, conflict: { localHash: null } }],
      ['a conflict with a bad localHash', { base: { rev: 1, hash: null }, currentHash: null, conflict: { localHash: 'zz', sot: { rev: 1, hash: null } } }],
      ['a conflict with a bad sot rev', { base: { rev: 1, hash: null }, currentHash: null, conflict: { localHash: null, sot: { rev: -2, hash: null } } }],
    ]

    it.each(bad)('a section that is %s is dropped — and ONLY that section', (_label, section) => {
      writeRaw({ profileId: P1, sections: { settings: SETTINGS, broken: section, hosts: HOSTS }, stash: { [H('b')]: { a: 1 } } })

      const loaded = loadSectionStore(P1)

      expect(loaded.sections).toEqual({ settings: SETTINGS, hosts: HOSTS })
      expect(loaded.stash).toEqual({ [H('b')]: { a: 1 } })
    })

    it('strips fields it does not know from a section', () => {
      writeRaw({ profileId: P1, sections: { settings: { ...SETTINGS, inFlight: { kind: 'put' }, conflict: null } }, stash: {} })
      expect(loadSectionStore(P1).sections.settings).toEqual(SETTINGS)
      expect(Object.keys(loadSectionStore(P1).sections.settings).sort()).toEqual(['base', 'currentHash'])
    })

    it('a stash entry with a bad key or a non-object payload is dropped, the rest kept', () => {
      writeRaw({
        profileId: P1,
        sections: {},
        stash: { [H('a')]: { ok: true }, short: { x: 1 }, [H('b')]: 'string', [H('c')]: [1], [H('d')]: null },
      })
      expect(loadSectionStore(P1).stash).toEqual({ [H('a')]: { ok: true } })
    })

    it('a section named __proto__ is an ordinary key, not a prototype', () => {
      writeRaw(`{"profileId":"${P1}","sections":{"__proto__":${JSON.stringify(SETTINGS)}},"stash":{}}`)
      const loaded = loadSectionStore(P1)
      expect(Object.keys(loaded.sections)).toEqual(['__proto__'])
      expect(Object.getPrototypeOf(loaded.sections)).toBe(Object.prototype)
    })

    it('the next write drops the bad section from storage as well', () => {
      writeRaw({ profileId: P1, sections: { settings: SETTINGS, broken: { base: 1 } }, stash: {} })
      saveSection(P1, 'hosts', HOSTS)
      expect(JSON.parse(raw()!).sections).toEqual({ settings: SETTINGS, hosts: HOSTS })
    })

    it.each([
      ['a bad section', () => saveSection(P1, 'settings', { base: { rev: -1, hash: null }, currentHash: null })],
      ['a bad conflict', () => saveSection(P1, 'settings', { ...SETTINGS, conflict: { localHash: 'x', sot: { rev: 1, hash: null } } })],
      ['an empty section key', () => saveSection(P1, '', SETTINGS)],
      ['a malformed profile id', () => saveSection('nope', 'settings', SETTINGS)],
      ['a stash key that is not a hash', () => putStash(P1, 'abc', { a: 1 })],
      ['a stash payload that is an array', () => putStash(P1, H('a'), [1])],
      ['a stash payload that is null', () => putStash(P1, H('a'), null)],
      ['a stash payload that is a class instance', () => putStash(P1, H('a'), new Date(0))],
      ['a stash payload that cannot be serialised', () => { const o: Record<string, unknown> = {}; o.self = o; return putStash(P1, H('a'), o) }],
    ])('refuses to write %s', (_label, write) => {
      expect(write()).toBe(false)
      expect(raw()).toBeNull()
    })
  })

  describe('stash', () => {
    it('put / get round trip, alongside sections', () => {
      saveSection(P1, 'settings', SETTINGS)
      expect(putStash(P1, H('b'), { tabs: [{ id: 't1' }], n: null })).toBe(true)

      expect(getStash(P1, H('b'))).toEqual({ tabs: [{ id: 't1' }], n: null })
      expect(getStash(P1, H('c'))).toBeUndefined()
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
    })

    it('pruneStash keeps only the hashes in `keep`', () => {
      putStash(P1, H('a'), { a: 1 })
      putStash(P1, H('b'), { b: 1 })
      putStash(P1, H('c'), { c: 1 })
      saveSection(P1, 'settings', SETTINGS)

      pruneStash(P1, new Set([H('b'), H('f')]))

      expect(loadSectionStore(P1).stash).toEqual({ [H('b')]: { b: 1 } })
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
    })

    it('a payload over 5 MiB is not stored; one at the limit is', () => {
      expect(MAX_STASH_PAYLOAD_BYTES).toBe(5 * 1024 * 1024)
      putStash(P1, H('a'), { a: 1 })
      const before = raw()
      const wrapper = JSON.stringify({ s: '' }).length
      // jsdom's localStorage has its own quota; the size rule is ours, so keep
      // the real backend out of it.
      const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {})

      expect(putStash(P1, H('b'), { s: 'x'.repeat(MAX_STASH_PAYLOAD_BYTES - wrapper + 1) })).toBe(false)
      expect(setItem).not.toHaveBeenCalled()
      expect(raw()).toBe(before)

      expect(putStash(P1, H('b'), { s: 'x'.repeat(MAX_STASH_PAYLOAD_BYTES - wrapper) })).toBe(true)
      expect(setItem).toHaveBeenCalledTimes(1)
    })

    it('the limit counts UTF-8 bytes, not UTF-16 code units', () => {
      const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {})
      // 2 M CJK characters: 2 M code units, 6 M bytes.
      expect(putStash(P1, H('a'), { s: '繁'.repeat(2_000_000) })).toBe(false)
      expect(setItem).not.toHaveBeenCalled()
    })
  })

  describe('a storage that refuses the write (quota)', () => {
    const quota = (): void => {
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw new DOMException('exceeded', 'QuotaExceededError')
      })
    }

    it('saveSection does not throw, reports false, and the old data is intact', () => {
      saveSection(P1, 'settings', SETTINGS)
      const before = raw()
      quota()

      expect(saveSection(P1, 'hosts', HOSTS)).toBe(false)

      // setItem is atomic: a failed write leaves the previous value, never half of the new one.
      expect(raw()).toBe(before)
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
    })

    it('putStash / dropSection / pruneStash do not throw either', () => {
      saveSection(P1, 'settings', SETTINGS)
      putStash(P1, H('a'), { a: 1 })
      const before = raw()
      quota()

      expect(putStash(P1, H('b'), { b: 1 })).toBe(false)
      expect(dropSection(P1, 'settings')).toBe(false)
      expect(pruneStash(P1, new Set())).toBe(false)
      expect(raw()).toBe(before)
    })

    it('a storage that throws on read → empty, no throw', () => {
      saveSection(P1, 'settings', SETTINGS)
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('blocked')
      })
      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
      expect(getStash(P1, H('a'))).toBeUndefined()
    })

    it('clearSectionStore does not throw when removeItem does', () => {
      vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
        throw new Error('blocked')
      })
      expect(() => clearSectionStore()).not.toThrow()
    })
  })

  it('clearSectionStore removes everything', () => {
    saveSection(P1, 'settings', SETTINGS)
    putStash(P1, H('a'), { a: 1 })

    clearSectionStore()

    expect(raw()).toBeNull()
    expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
  })

  it('there is no in-memory cache: a write made behind its back is what the next read sees', () => {
    saveSection(P1, 'settings', SETTINGS)
    writeRaw({ profileId: P1, sections: { hosts: HOSTS }, stash: {} }) // another window, the new leader
    expect(loadSectionStore(P1).sections).toEqual({ hosts: HOSTS })
  })

  it('is not a synced store: the module never registers with syncManager', () => {
    expect(moduleSource).toContain('export function loadSectionStore') // the raw source, not a transformed stub
    const code = moduleSource.replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, '')
    expect(code).not.toMatch(/syncManager/)
    expect(code).not.toMatch(/zustand/)
  })
})
