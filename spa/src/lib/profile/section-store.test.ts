import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../storage/keys'
import moduleSource from './section-store.ts?raw'
import {
  MAX_STASH_PAYLOAD_BYTES,
  claimSectionStore,
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

/** Take the lease's fencing token; a claim that cannot be stored fails the test. */
const claim = (profileId: string = P1): number => {
  const generation = claimSectionStore(profileId)
  if (generation === null) throw new Error('claim failed')
  return generation
}
const storedGeneration = (): unknown => (JSON.parse(raw()!) as { generation?: unknown }).generation

/** The generation the current test's leader holds (claimed for P1 in `beforeEach`). */
let g: number

beforeEach(() => {
  localStorage.clear()
  g = claim()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('section-store', () => {
  it('uses its own storage key', () => {
    expect(KEY).toBe('purdex-profile-sections')
  })

  it('nothing stored → empty, and the load does not write', () => {
    localStorage.clear()
    expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
    expect(raw()).toBeNull()
  })

  it('round-trips sections', () => {
    expect(saveSection(P1, g, 'settings', SETTINGS)).toBe('ok')
    expect(saveSection(P1, g, 'hosts', HOSTS)).toBe('ok')

    expect(loadSectionStore(P1)).toEqual({ profileId: P1, sections: { settings: SETTINGS, hosts: HOSTS }, stash: {} })
  })

  it('saveSection replaces the section of the same key', () => {
    saveSection(P1, g, 'settings', SETTINGS)
    saveSection(P1, g, 'settings', HOSTS)
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
    saveSection(P1, g, 'settings', locked)
    saveSection(P1, g, 'tabs.ws1', deleteLocked)

    const loaded = loadSectionStore(P1).sections
    expect(loaded.settings).toEqual(locked)
    expect(loaded['tabs.ws1']).toEqual(deleteLocked)
  })

  it('a section saved without a conflict comes back without the key', () => {
    saveSection(P1, g, 'settings', { ...SETTINGS, conflict: { localHash: null, sot: { rev: 4, hash: null } } })
    saveSection(P1, g, 'settings', SETTINGS)
    expect('conflict' in loadSectionStore(P1).sections.settings).toBe(false)
  })

  it('dropSection removes one section and keeps the rest', () => {
    saveSection(P1, g, 'settings', SETTINGS)
    saveSection(P1, g, 'hosts', HOSTS)

    dropSection(P1, g, 'settings')

    expect(loadSectionStore(P1).sections).toEqual({ hosts: HOSTS })
  })

  describe('the data belongs to one profile', () => {
    beforeEach(() => {
      saveSection(P1, g, 'settings', SETTINGS)
      putStash(P1, g, H('b'), { a: 1 })
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

    it('no write as another profile gets through, even with the current generation — only a claim changes profile', () => {
      const before = raw()

      expect(saveSection(P2, g, 'hosts', HOSTS)).toBe('fenced')
      expect(putStash(P2, g, H('e'), { z: 1 })).toBe('fenced')
      expect(dropSection(P2, g, 'settings')).toBe('fenced')
      expect(pruneStash(P2, g, new Set())).toBe('fenced')

      expect(raw()).toBe(before)
    })

    it('claiming as another profile discards the old profile wholesale', () => {
      const g2 = claim(P2)
      expect(saveSection(P2, g2, 'hosts', HOSTS)).toBe('ok')

      expect(loadSectionStore(P2)).toEqual({ profileId: P2, sections: { hosts: HOSTS }, stash: {} })
      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
      expect(raw()).not.toContain(H('b'))
    })

    it('claiming as the same profile keeps everything', () => {
      claim(P1)
      expect(loadSectionStore(P1)).toEqual({ profileId: P1, sections: { settings: SETTINGS }, stash: { [H('b')]: { a: 1 } } })
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
      ['a negative generation', { profileId: P1, generation: -1, sections: { settings: SETTINGS }, stash: {} }],
      ['a fractional generation', { profileId: P1, generation: 1.5, sections: { settings: SETTINGS }, stash: {} }],
      ['an unsafe generation', { profileId: P1, generation: 2 ** 53, sections: { settings: SETTINGS }, stash: {} }],
      ['a string generation', { profileId: P1, generation: '1', sections: { settings: SETTINGS }, stash: {} }],
      ['a null generation', { profileId: P1, generation: null, sections: { settings: SETTINGS }, stash: {} }],
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
      writeRaw({ profileId: P1, generation: g, sections: { settings: SETTINGS, broken: { base: 1 } }, stash: {} })
      saveSection(P1, g, 'hosts', HOSTS)
      expect(JSON.parse(raw()!).sections).toEqual({ settings: SETTINGS, hosts: HOSTS })
    })

    it.each([
      ['a bad section', () => saveSection(P1, g, 'settings', { base: { rev: -1, hash: null }, currentHash: null })],
      ['a bad conflict', () => saveSection(P1, g, 'settings', { ...SETTINGS, conflict: { localHash: 'x', sot: { rev: 1, hash: null } } })],
      ['an empty section key', () => saveSection(P1, g, '', SETTINGS)],
      ['a malformed profile id', () => saveSection('nope', g, 'settings', SETTINGS)],
      ['generation 0 — nobody ever holds it', () => saveSection(P1, 0, 'settings', SETTINGS)],
      ['a fractional generation', () => putStash(P1, 1.5, H('a'), { a: 1 })],
      ['a NaN generation', () => dropSection(P1, Number.NaN, 'settings')],
      ['a negative generation', () => pruneStash(P1, -1, new Set())],
      ['a stash key that is not a hash', () => putStash(P1, g, 'abc', { a: 1 })],
      ['a stash payload that is an array', () => putStash(P1, g, H('a'), [1])],
      ['a stash payload that is null', () => putStash(P1, g, H('a'), null)],
      ['a stash payload that is a class instance', () => putStash(P1, g, H('a'), new Date(0))],
      ['a stash payload that cannot be serialised', () => { const o: Record<string, unknown> = {}; o.self = o; return putStash(P1, g, H('a'), o) }],
    ])('refuses to write %s', (_label, write) => {
      const before = raw()
      expect(write()).toBe('failed')
      expect(raw()).toBe(before)
    })
  })

  describe('stash', () => {
    it('put / get round trip, alongside sections', () => {
      saveSection(P1, g, 'settings', SETTINGS)
      expect(putStash(P1, g, H('b'), { tabs: [{ id: 't1' }], n: null })).toBe('ok')

      expect(getStash(P1, H('b'))).toEqual({ tabs: [{ id: 't1' }], n: null })
      expect(getStash(P1, H('c'))).toBeUndefined()
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
    })

    it('pruneStash keeps only the hashes in `keep`', () => {
      putStash(P1, g, H('a'), { a: 1 })
      putStash(P1, g, H('b'), { b: 1 })
      putStash(P1, g, H('c'), { c: 1 })
      saveSection(P1, g, 'settings', SETTINGS)

      pruneStash(P1, g, new Set([H('b'), H('f')]))

      expect(loadSectionStore(P1).stash).toEqual({ [H('b')]: { b: 1 } })
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
    })

    it('a payload over 5 MiB is not stored; one at the limit is', () => {
      expect(MAX_STASH_PAYLOAD_BYTES).toBe(5 * 1024 * 1024)
      putStash(P1, g, H('a'), { a: 1 })
      const before = raw()
      const wrapper = JSON.stringify({ s: '' }).length
      // jsdom's localStorage has its own quota; the size rule is ours, so keep
      // the real backend out of it.
      const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {})

      expect(putStash(P1, g, H('b'), { s: 'x'.repeat(MAX_STASH_PAYLOAD_BYTES - wrapper + 1) })).toBe('failed')
      expect(setItem).not.toHaveBeenCalled()
      expect(raw()).toBe(before)

      expect(putStash(P1, g, H('b'), { s: 'x'.repeat(MAX_STASH_PAYLOAD_BYTES - wrapper) })).toBe('ok')
      expect(setItem).toHaveBeenCalledTimes(1)
    })

    it('dropSection / pruneStash with nothing to remove report ok without writing', () => {
      const setItem = vi.spyOn(Storage.prototype, 'setItem')
      expect(dropSection(P1, g, 'settings')).toBe('ok')
      expect(pruneStash(P1, g, new Set())).toBe('ok')
      expect(setItem).not.toHaveBeenCalled()
    })

    it('the limit counts UTF-8 bytes, not UTF-16 code units', () => {
      const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {})
      // 2 M CJK characters: 2 M code units, 6 M bytes.
      expect(putStash(P1, g, H('a'), { s: '繁'.repeat(2_000_000) })).toBe('failed')
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
      saveSection(P1, g, 'settings', SETTINGS)
      const before = raw()
      quota()

      expect(saveSection(P1, g, 'hosts', HOSTS)).toBe('failed')

      // setItem is atomic: a failed write leaves the previous value, never half of the new one.
      expect(raw()).toBe(before)
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
    })

    it('putStash / dropSection / pruneStash do not throw either', () => {
      saveSection(P1, g, 'settings', SETTINGS)
      putStash(P1, g, H('a'), { a: 1 })
      const before = raw()
      quota()

      expect(putStash(P1, g, H('b'), { b: 1 })).toBe('failed')
      expect(dropSection(P1, g, 'settings')).toBe('failed')
      expect(pruneStash(P1, g, new Set())).toBe('failed')
      expect(raw()).toBe(before)
    })

    it('a storage that throws on read → empty, no throw', () => {
      saveSection(P1, g, 'settings', SETTINGS)
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('blocked')
      })
      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
      expect(getStash(P1, H('a'))).toBeUndefined()
    })

    it('a storage that throws on read: a write is failed, not fenced — nobody is known to have claimed', () => {
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('blocked')
      })
      expect(saveSection(P1, g, 'settings', SETTINGS)).toBe('failed')
      expect(putStash(P1, g, H('a'), { a: 1 })).toBe('failed')
      expect(dropSection(P1, g, 'settings')).toBe('failed')
      expect(pruneStash(P1, g, new Set())).toBe('failed')
      expect(claimSectionStore(P1)).toBeNull()
    })

    it('claimSectionStore reports null and leaves the old generation in force', () => {
      saveSection(P1, g, 'settings', SETTINGS)
      const before = raw()
      quota()

      expect(claimSectionStore(P1)).toBeNull()

      expect(raw()).toBe(before)
      vi.restoreAllMocks()
      expect(saveSection(P1, g, 'hosts', HOSTS)).toBe('ok')
    })

    it('clearSectionStore does not throw when the storage does', () => {
      quota()
      vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
        throw new Error('blocked')
      })
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('blocked')
      })
      expect(() => clearSectionStore()).not.toThrow()
    })
  })

  it('clearSectionStore removes every section and payload, whichever profile it was', () => {
    saveSection(P1, g, 'settings', SETTINGS)
    putStash(P1, g, H('a'), { a: 1 })

    clearSectionStore()

    expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
    expect(getStash(P1, H('a'))).toBeUndefined()
    expect(raw()).not.toContain(H('a'))
    expect(JSON.parse(raw()!)).toEqual({ profileId: null, generation: g, sections: {}, stash: {} })
  })

  describe('fencing — an old leader cannot overwrite a newer one', () => {
    it('a claim hands out 1, 2, 3 … and stores it', () => {
      localStorage.clear()
      expect(claimSectionStore(P1)).toBe(1)
      expect(storedGeneration()).toBe(1)
      expect(claimSectionStore(P1)).toBe(2)
      expect(claimSectionStore(P1)).toBe(3)
      expect(storedGeneration()).toBe(3)
    })

    it('a malformed profile id cannot claim', () => {
      const before = raw()
      expect(claimSectionStore('nope')).toBeNull()
      expect(raw()).toBe(before)
    })

    it('A claims, B claims: every write of A is fenced and changes nothing; B writes', () => {
      const a = g
      saveSection(P1, a, 'settings', SETTINGS)
      putStash(P1, a, H('a'), { a: 1 })
      const b = claim()
      expect(b).toBe(a + 1)
      const before = raw()

      expect(saveSection(P1, a, 'hosts', HOSTS)).toBe('fenced')
      expect(dropSection(P1, a, 'settings')).toBe('fenced')
      expect(putStash(P1, a, H('b'), { b: 1 })).toBe('fenced')
      expect(pruneStash(P1, a, new Set())).toBe('fenced')
      // Fenced even when there is nothing to remove: the caller must learn it lost.
      expect(dropSection(P1, a, 'absent')).toBe('fenced')
      expect(pruneStash(P1, a, new Set([H('a')]))).toBe('fenced')
      expect(raw()).toBe(before)

      expect(saveSection(P1, b, 'hosts', HOSTS)).toBe('ok')
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS, hosts: HOSTS })
    })

    it('the reviewed interleaving: B, writing from a stale document, no longer reverts A', () => {
      // Both read D; the newer leader B writes S1; the old leader A then writes hosts.
      const a = g
      const b = claim()
      expect(saveSection(P1, b, 'settings', SETTINGS)).toBe('ok')
      expect(saveSection(P1, a, 'hosts', HOSTS)).toBe('fenced')
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
    })

    it('a generation from the future is fenced too', () => {
      expect(saveSection(P1, g + 1, 'settings', SETTINGS)).toBe('fenced')
    })

    it('a write never changes the generation', () => {
      saveSection(P1, g, 'settings', SETTINGS)
      putStash(P1, g, H('a'), { a: 1 })
      dropSection(P1, g, 'settings')
      pruneStash(P1, g, new Set())
      expect(storedGeneration()).toBe(g)
    })

    it('changing profile continues the count — it never starts over', () => {
      claim(P1) // 2
      const g2 = claim(P2)
      expect(g2).toBe(3)
      // The P1 leader that held 1 must not become valid again if P1 comes back.
      const g3 = claim(P1)
      expect(g3).toBe(4)
      expect(saveSection(P1, 1, 'settings', SETTINGS)).toBe('fenced')
    })

    it('clearing keeps the count: the generation a detached leader held is never handed out again', () => {
      const old = g
      saveSection(P1, old, 'settings', SETTINGS)

      clearSectionStore()
      expect(storedGeneration()).toBe(old)
      // Nobody owns a cleared store — not even the holder of its generation.
      expect(saveSection(P1, old, 'hosts', HOSTS)).toBe('fenced')

      const next = claim()
      expect(next).toBe(old + 1)
      expect(saveSection(P1, old, 'hosts', HOSTS)).toBe('fenced')
      expect(loadSectionStore(P1).sections).toEqual({})
    })

    it('clearing twice, or clearing nothing, still never goes backwards', () => {
      claim()
      claim() // 3
      clearSectionStore()
      clearSectionStore()
      expect(claim()).toBe(4)
    })

    it('a document from before fencing (no generation) is readable, and the first claim gets 1', () => {
      writeRaw({ profileId: P1, sections: { settings: SETTINGS }, stash: { [H('b')]: { a: 1 } } })

      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
      // Generation 0 is what such a document reads as, and nobody may write with it.
      expect(saveSection(P1, 0, 'hosts', HOSTS)).toBe('failed')

      expect(claimSectionStore(P1)).toBe(1)
      expect(loadSectionStore(P1)).toEqual({ profileId: P1, sections: { settings: SETTINGS }, stash: { [H('b')]: { a: 1 } } })
    })

    it('a document that cannot be read is claimed from 0, and fences whoever wrote before', () => {
      writeRaw('{nope')
      expect(saveSection(P1, g, 'settings', SETTINGS)).toBe('fenced')
      expect(claimSectionStore(P1)).toBe(1)
    })

    it('nothing stored: a write is fenced — a claim comes first', () => {
      localStorage.clear()
      expect(saveSection(P1, 1, 'settings', SETTINGS)).toBe('fenced')
      expect(raw()).toBeNull()
    })
  })

  it('there is no in-memory cache: a write made behind its back is what the next read sees', () => {
    saveSection(P1, g, 'settings', SETTINGS)
    writeRaw({ profileId: P1, generation: g + 1, sections: { hosts: HOSTS }, stash: {} }) // another window, the new leader
    expect(loadSectionStore(P1).sections).toEqual({ hosts: HOSTS })
  })

  it('is not a synced store: the module never registers with syncManager', () => {
    expect(moduleSource).toContain('export function loadSectionStore') // the raw source, not a transformed stub
    const code = moduleSource.replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, '')
    expect(code).not.toMatch(/syncManager/)
    expect(code).not.toMatch(/zustand/)
  })
})
