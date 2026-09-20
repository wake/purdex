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
  saveConflict,
  saveSection,
  type PersistedSection,
} from './section-store'

const PREFIX = STORAGE_KEYS.PROFILE_SECTIONS
const P1 = 'p_0123456789ab'
const P2 = 'p_ba9876543210'
const H = (c: string): string => c.repeat(64)

/** The key format is part of the contract: these are built by hand, not by the module. */
const sKey = (profileId: string, section: string): string => `${PREFIX}:${profileId}:s:${section}`
const pKey = (profileId: string, hash: string): string => `${PREFIX}:${profileId}:p:${hash}`

const SETTINGS: PersistedSection = { base: { rev: 3, hash: H('a') }, currentHash: H('b') }
const HOSTS: PersistedSection = { base: { rev: 0, hash: null }, currentHash: null }
/** Locked on a put: needs the snapshot that was sent (b) and the SOT's payload (d). */
const LOCKED = {
  base: { rev: 3, hash: H('a') },
  currentHash: H('c'),
  conflict: { localHash: H('b'), sot: { rev: 5, hash: H('d') } },
} satisfies PersistedSection
const UNLOCKED: PersistedSection = { base: LOCKED.base, currentHash: LOCKED.currentHash }
/** Locked on a delete over a tombstone: needs no payload at all. */
const DELETE_LOCKED = {
  base: { rev: 3, hash: H('a') },
  currentHash: null,
  conflict: { localHash: null, sot: { rev: 6, hash: null } },
} satisfies PersistedSection
const LOCKED_PAYLOADS = { [H('b')]: { mine: 1 }, [H('d')]: { theirs: 1 } }

const utf8Bytes = (v: unknown): number => new TextEncoder().encode(JSON.stringify(v)).length
/** `{ s: 'xxx…' }` that serialises to exactly `bytes` — one UTF-16 code unit per byte. */
const asciiPayloadOfBytes = (bytes: number): { s: string } => ({ s: 'x'.repeat(bytes - JSON.stringify({ s: '' }).length) })
/** The same size in UTF-8, in a third of the code units: CJK is 3 bytes and 1 code unit a character.
 *  jsdom's quota counts code units, so this is how a payload AT the 5 MiB limit fits in it. */
const cjkPayloadOfBytes = (bytes: number): { s: string } => {
  const room = bytes - JSON.stringify({ s: '' }).length
  return { s: '繁'.repeat(Math.floor(room / 3)) + 'x'.repeat(room % 3) }
}

const storedKeys = (): string[] => {
  const keys: string[] = []
  for (let i = 0; i < localStorage.length; i += 1) keys.push(localStorage.key(i)!)
  return keys.sort()
}
/** Everything in storage, to assert "nothing was written". */
const snapshot = (): string => JSON.stringify(storedKeys().map((k) => [k, localStorage.getItem(k)]))
const writeRawSection = (profileId: string, section: string, value: unknown): void =>
  localStorage.setItem(sKey(profileId, section), typeof value === 'string' ? value : JSON.stringify(value))
const writeRawPayload = (profileId: string, hash: string, value: unknown): void =>
  localStorage.setItem(pKey(profileId, hash), typeof value === 'string' ? value : JSON.stringify(value))

const EMPTY = (profileId: string) => ({ profileId, sections: {} })

const quotaError = (): DOMException => new DOMException('exceeded', 'QuotaExceededError')
/** `setItem` refuses the keys `refuse` picks and really writes the rest; returns the keys it was called with. */
const refuseSetItem = (refuse: (key: string) => boolean): string[] => {
  const real = Storage.prototype.setItem
  const calls: string[] = []
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
    calls.push(k)
    if (refuse(k)) throw quotaError()
    real.call(this, k, v)
  })
  return calls
}

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('section-store', () => {
  it('STORAGE_KEYS.PROFILE_SECTIONS is the prefix of every key', () => {
    expect(PREFIX).toBe('purdex-profile-sections')
  })

  it('nothing stored → empty, and the load does not write', () => {
    expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
    expect(storedKeys()).toEqual([])
  })

  describe('layout — one thing, one key', () => {
    it('a section is one key, holding only that section', () => {
      expect(saveSection(P1, 'settings', SETTINGS)).toBe('ok')
      expect(saveSection(P1, 'tabs.ws1', HOSTS)).toBe('ok')

      expect(storedKeys()).toEqual([sKey(P1, 'settings'), sKey(P1, 'tabs.ws1')].sort())
      expect(JSON.parse(localStorage.getItem(sKey(P1, 'settings'))!)).toEqual(SETTINGS)
      expect(JSON.parse(localStorage.getItem(sKey(P1, 'tabs.ws1'))!)).toEqual(HOSTS)
    })

    it('a payload is one key, addressed by its hash', () => {
      expect(putStash(P1, H('b'), { a: 1 })).toBe('ok')
      expect(storedKeys()).toEqual([pKey(P1, H('b'))])
      expect(JSON.parse(localStorage.getItem(pKey(P1, H('b')))!)).toEqual({ a: 1 })
    })

    it('a conflict is its section key plus one key per payload', () => {
      expect(saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)).toBe('ok')
      expect(storedKeys()).toEqual([pKey(P1, H('b')), pKey(P1, H('d')), sKey(P1, 'settings')].sort())
      expect(JSON.parse(localStorage.getItem(sKey(P1, 'settings'))!)).toEqual(LOCKED)
    })
  })

  it('round-trips sections', () => {
    expect(saveSection(P1, 'settings', SETTINGS)).toBe('ok')
    expect(saveSection(P1, 'hosts', HOSTS)).toBe('ok')

    expect(loadSectionStore(P1)).toEqual({ profileId: P1, sections: { settings: SETTINGS, hosts: HOSTS } })
  })

  it('load does not carry the payloads — they are read on demand', () => {
    saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)
    expect(Object.keys(loadSectionStore(P1)).sort()).toEqual(['profileId', 'sections'])
  })

  it('saveSection replaces the section of the same key', () => {
    saveSection(P1, 'settings', SETTINGS)
    saveSection(P1, 'settings', HOSTS)
    expect(loadSectionStore(P1).sections).toEqual({ settings: HOSTS })
  })

  it('round-trips a conflict — the lock survives a restart with the snapshot that was sent', () => {
    expect(saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)).toBe('ok')
    expect(saveConflict(P1, 'tabs.ws1', DELETE_LOCKED, {})).toBe('ok')

    const loaded = loadSectionStore(P1)
    expect(loaded.sections.settings).toEqual(LOCKED)
    expect(loaded.sections['tabs.ws1']).toEqual(DELETE_LOCKED)
    expect(getStash(P1, H('b'))).toEqual({ mine: 1 })
    expect(getStash(P1, H('d'))).toEqual({ theirs: 1 })
  })

  it('a section saved without a conflict comes back without the key', () => {
    saveConflict(P1, 'settings', DELETE_LOCKED, {})
    saveSection(P1, 'settings', SETTINGS)
    expect('conflict' in loadSectionStore(P1).sections.settings).toBe(false)
  })

  describe('two leaders interleaving — a write cannot touch what it does not name', () => {
    it('the reviewed finding is gone: A loads, B saves hosts, A saves settings → both are there', () => {
      saveSection(P1, 'settings', UNLOCKED) // what both leaders start from
      const seenByA = loadSectionStore(P1) // A's stale view: no hosts, the old settings

      expect(saveSection(P1, 'hosts', HOSTS)).toBe('ok') // B
      expect(putStash(P1, H('e'), { b: 1 })).toBe('ok') // B
      expect(saveSection(P1, 'settings', SETTINGS)).toBe('ok') // A, knowing nothing of B's writes

      expect(seenByA.sections).toEqual({ settings: UNLOCKED })
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS, hosts: HOSTS })
      expect(getStash(P1, H('e'))).toEqual({ b: 1 })
    })

    it('a stash write cannot revert a section, nor a section write a stash', () => {
      saveSection(P1, 'settings', SETTINGS)
      const before = localStorage.getItem(sKey(P1, 'settings'))

      putStash(P1, H('a'), { a: 1 })
      expect(localStorage.getItem(sKey(P1, 'settings'))).toBe(before)

      const payloadBefore = localStorage.getItem(pKey(P1, H('a')))
      saveSection(P1, 'hosts', HOSTS)
      dropSection(P1, 'settings')
      expect(localStorage.getItem(pKey(P1, H('a')))).toBe(payloadBefore)
    })

    it('KNOWN RESIDUAL: the same section key written by two leaders is last-writer-wins — and only that key', () => {
      saveSection(P1, 'hosts', HOSTS) // A's other section
      expect(saveSection(P1, 'settings', SETTINGS)).toBe('ok') // A
      expect(saveSection(P1, 'settings', UNLOCKED)).toBe('ok') // B, later

      expect(loadSectionStore(P1).sections).toEqual({ settings: UNLOCKED, hosts: HOSTS })
    })

    it('the same payload written twice is idempotent: the second write does not touch storage', () => {
      expect(putStash(P1, H('a'), { a: 1 })).toBe('ok')
      const setItem = vi.spyOn(Storage.prototype, 'setItem')
      expect(putStash(P1, H('a'), { a: 1 })).toBe('ok')
      expect(setItem).not.toHaveBeenCalled()
      expect(getStash(P1, H('a'))).toEqual({ a: 1 })
    })
  })

  describe('profiles are separated by the key itself', () => {
    beforeEach(() => {
      saveSection(P1, 'settings', SETTINGS)
      saveConflict(P1, 'hosts', LOCKED, LOCKED_PAYLOADS)
      saveSection(P2, 'settings', HOSTS)
      putStash(P2, H('e'), { p2: 1 })
    })

    it('a load sees its own profile only', () => {
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS, hosts: LOCKED })
      expect(loadSectionStore(P2).sections).toEqual({ settings: HOSTS })
    })

    it('a payload of one profile is not readable as another', () => {
      expect(getStash(P1, H('e'))).toBeUndefined()
      expect(getStash(P2, H('b'))).toBeUndefined()
      expect(getStash(P2, H('e'))).toEqual({ p2: 1 })
    })

    it('a conflict cannot borrow the other profile\'s payloads', () => {
      const before = snapshot()
      expect(saveConflict(P2, 'hosts', LOCKED, {})).toBe('failed') // b and d exist — under P1
      expect(snapshot()).toBe(before)

      writeRawSection(P2, 'hosts', LOCKED)
      expect(loadSectionStore(P2).sections.hosts).toEqual(UNLOCKED)
    })

    it('writes, drops and prunes as one profile leave the other alone', () => {
      const p1Before = storedKeys().filter((k) => k.includes(P1)).map((k) => [k, localStorage.getItem(k)])

      saveSection(P2, 'hosts', HOSTS)
      dropSection(P2, 'settings')
      pruneStash(P2, new Set())

      expect(storedKeys().filter((k) => k.includes(P1)).map((k) => [k, localStorage.getItem(k)])).toEqual(p1Before)
      expect(getStash(P2, H('e'))).toBeUndefined()
    })

    it('clearSectionStore(profile) removes that profile and nothing else', () => {
      localStorage.setItem('purdex-profile', 'not ours')
      expect(clearSectionStore(P1)).toBe('ok')

      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
      expect(getStash(P1, H('b'))).toBeUndefined()
      expect(storedKeys()).toEqual(['purdex-profile', pKey(P2, H('e')), sKey(P2, 'settings')].sort())
    })

    it('clearSectionStore() removes every profile — and only keys under the prefix', () => {
      localStorage.setItem('purdex-profile', 'not ours')
      localStorage.setItem(`${PREFIX}-neighbour`, 'not ours either')
      expect(clearSectionStore()).toBe('ok')
      expect(storedKeys()).toEqual(['purdex-profile', `${PREFIX}-neighbour`].sort())
    })

    it('clearSectionStore with a malformed profile id removes nothing', () => {
      const before = snapshot()
      expect(clearSectionStore('nope')).toBe('failed')
      expect(clearSectionStore('')).toBe('failed')
      expect(snapshot()).toBe(before)
    })
  })

  describe('validation — what storage holds is checked, not cast', () => {
    const bad: Array<[string, unknown]> = [
      ['not JSON', '{nope'],
      ['JSON null', 'null'],
      ['an array', '[]'],
      ['not an object', '"x"'],
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

    it.each(bad)('a section that is %s is dropped — and ONLY that section; the load does not write', (_label, section) => {
      saveSection(P1, 'settings', SETTINGS)
      saveSection(P1, 'hosts', HOSTS)
      writeRawSection(P1, 'workspaces', section)
      const before = snapshot()

      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS, hosts: HOSTS })
      expect(snapshot()).toBe(before)
    })

    it.each([
      ['an unknown section name', 'bogus'],
      ['an empty section name', ''],
      ['__proto__', '__proto__'],
      ['a tabs key without a workspace id', 'tabs.'],
      ['a tabs key with a colon in it', 'tabs.a:b'],
      ['a tabs key with a 65-character id', `tabs.${'w'.repeat(65)}`],
    ])('a key whose section is %s is not a section', (_label, name) => {
      saveSection(P1, 'settings', SETTINGS)
      writeRawSection(P1, name, HOSTS)

      const loaded = loadSectionStore(P1)
      expect(loaded.sections).toEqual({ settings: SETTINGS })
      expect(Object.getPrototypeOf(loaded.sections)).toBe(Object.prototype)
    })

    it('strips fields it does not know from a section', () => {
      writeRawSection(P1, 'settings', { ...SETTINGS, inFlight: { kind: 'put' }, conflict: null, profileId: P2, generation: 4 })
      expect(loadSectionStore(P1).sections.settings).toEqual(SETTINGS)
      expect(Object.keys(loadSectionStore(P1).sections.settings).sort()).toEqual(['base', 'currentHash'])
    })

    it.each([
      ['a string', '"x"'],
      ['an array', '[1]'],
      ['null', 'null'],
      ['not JSON', '{nope'],
    ])('a stored payload that is %s reads as missing', (_label, stored) => {
      writeRawPayload(P1, H('a'), stored)
      expect(getStash(P1, H('a'))).toBeUndefined()
    })

    it.each([
      ['a malformed profile id', 'nope'],
      ['a profile id with a separator in it', `${P1}:s`],
      ['an upper-case profile id', 'p_0123456789AB'],
      ['an empty profile id', ''],
    ])('%s: nothing is written, nothing is read', (_label, profileId) => {
      saveSection(P1, 'settings', SETTINGS)
      putStash(P1, H('a'), { a: 1 })
      const before = snapshot()

      expect(saveSection(profileId, 'settings', SETTINGS)).toBe('failed')
      expect(saveConflict(profileId, 'settings', LOCKED, LOCKED_PAYLOADS)).toBe('failed')
      expect(putStash(profileId, H('b'), { b: 1 })).toBe('failed')
      expect(dropSection(profileId, 'settings')).toBe('failed')
      expect(pruneStash(profileId, new Set())).toBe('failed')
      expect(loadSectionStore(profileId)).toEqual(EMPTY(profileId))
      expect(getStash(profileId, H('a'))).toBeUndefined()

      expect(snapshot()).toBe(before)
    })

    it.each([
      ['a bad section', () => saveSection(P1, 'settings', { base: { rev: -1, hash: null }, currentHash: null })],
      ['a section with a conflict through saveSection — that is saveConflict\'s job', () => saveSection(P1, 'settings', DELETE_LOCKED)],
      ['a bad conflict', () => saveConflict(P1, 'settings', { ...SETTINGS, conflict: { localHash: 'x', sot: { rev: 1, hash: null } } }, {})],
      ['a conflict-less section through saveConflict', () => saveConflict(P1, 'settings', SETTINGS as never, {})],
      ['a conflict whose payloads is not an object', () => saveConflict(P1, 'settings', DELETE_LOCKED, [] as never)],
      ['a conflict payload under a key that is not a hash', () => saveConflict(P1, 'settings', LOCKED, { ...LOCKED_PAYLOADS, short: { x: 1 } })],
      ['a conflict payload that is an array', () => saveConflict(P1, 'settings', LOCKED, { ...LOCKED_PAYLOADS, [H('d')]: [1] })],
      ['a conflict payload that cannot be serialised', () => { const o: Record<string, unknown> = {}; o.self = o; return saveConflict(P1, 'settings', LOCKED, { ...LOCKED_PAYLOADS, [H('b')]: o }) }],
      ['a conflict payload over the size limit', () => saveConflict(P1, 'settings', LOCKED, { ...LOCKED_PAYLOADS, [H('b')]: asciiPayloadOfBytes(MAX_STASH_PAYLOAD_BYTES + 1) })],
      ['a conflict without its localHash payload', () => saveConflict(P1, 'settings', LOCKED, { [H('d')]: { theirs: 1 } })],
      ['a conflict with no payloads at all', () => saveConflict(P1, 'settings', LOCKED, {})],
      ['an empty section key', () => saveSection(P1, '', SETTINGS)],
      ['a conflict with an empty section key', () => saveConflict(P1, '', DELETE_LOCKED, {})],
      ['a section key the daemon does not know', () => saveSection(P1, 'bogus', SETTINGS)],
      ['a section key that would break the separator', () => saveSection(P1, 'tabs.a:b', SETTINGS)],
      ['a conflict under a section key the daemon does not know', () => saveConflict(P1, 'settings:s', DELETE_LOCKED, {})],
      ['a stash key that is not a hash', () => putStash(P1, 'abc', { a: 1 })],
      ['a stash key that is an upper-case hash', () => putStash(P1, H('A'), { a: 1 })],
      ['a stash payload that is an array', () => putStash(P1, H('a'), [1])],
      ['a stash payload that is null', () => putStash(P1, H('a'), null)],
      ['a stash payload that is a class instance', () => putStash(P1, H('a'), new Date(0))],
      ['a stash payload that cannot be serialised', () => { const o: Record<string, unknown> = {}; o.self = o; return putStash(P1, H('a'), o) }],
    ])('refuses to write %s', (_label, write) => {
      saveSection(P1, 'hosts', HOSTS)
      const before = snapshot()
      expect(write()).toBe('failed')
      expect(snapshot()).toBe(before)
    })

    it('saveSection refuses a conflict even when its payloads are already stored', () => {
      putStash(P1, H('b'), { mine: 1 })
      putStash(P1, H('d'), { theirs: 1 })
      const before = snapshot()
      expect(saveSection(P1, 'settings', LOCKED)).toBe('failed')
      expect(snapshot()).toBe(before)
    })

    it('reads with a bad section key or hash find nothing', () => {
      putStash(P1, H('a'), { a: 1 })
      expect(getStash(P1, 'abc')).toBeUndefined()
      expect(getStash(P1, H('A'))).toBeUndefined()
      expect(dropSection(P1, 'bogus')).toBe('failed')
    })
  })

  describe('stash', () => {
    it('put / get round trip, alongside sections', () => {
      saveSection(P1, 'settings', SETTINGS)
      expect(putStash(P1, H('b'), { tabs: [{ id: 't1' }], n: null })).toBe('ok')

      expect(getStash(P1, H('b'))).toEqual({ tabs: [{ id: 't1' }], n: null })
      expect(getStash(P1, H('c'))).toBeUndefined()
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
    })

    it('pruneStash keeps only the hashes in `keep`', () => {
      putStash(P1, H('a'), { a: 1 })
      putStash(P1, H('b'), { b: 1 })
      putStash(P1, H('c'), { c: 1 })
      saveSection(P1, 'settings', SETTINGS)

      expect(pruneStash(P1, new Set([H('b'), H('f')]))).toBe('ok')

      expect(storedKeys()).toEqual([pKey(P1, H('b')), sKey(P1, 'settings')].sort())
    })

    it('pruneStash also removes a payload key it cannot read', () => {
      writeRawPayload(P1, H('a'), '{nope')
      expect(pruneStash(P1, new Set())).toBe('ok')
      expect(storedKeys()).toEqual([])
    })

    it('the limit is the daemon\'s 5 MiB', () => {
      expect(MAX_STASH_PAYLOAD_BYTES).toBe(5 * 1024 * 1024)
    })

    it('a payload over 5 MiB is refused before storage is asked', () => {
      const setItem = vi.spyOn(Storage.prototype, 'setItem') // a spy only: the real backend would do the write
      const over = cjkPayloadOfBytes(MAX_STASH_PAYLOAD_BYTES + 1)
      expect(utf8Bytes(over)).toBe(MAX_STASH_PAYLOAD_BYTES + 1)
      // 1.75 M code units: jsdom's quota would take it, so 'failed' here can only be the limit.
      expect(JSON.stringify(over).length).toBeLessThan(2_000_000)

      expect(putStash(P1, H('a'), over)).toBe('failed')
      expect(saveConflict(P1, 'settings', LOCKED, { ...LOCKED_PAYLOADS, [H('d')]: over })).toBe('failed')
      expect(putStash(P1, H('a'), asciiPayloadOfBytes(MAX_STASH_PAYLOAD_BYTES + 1))).toBe('failed')

      expect(setItem).not.toHaveBeenCalled()
      expect(storedKeys()).toEqual([])
    })

    it('a payload of exactly 5 MiB is really written and read back (no mock)', () => {
      const atLimit = cjkPayloadOfBytes(MAX_STASH_PAYLOAD_BYTES)
      expect(utf8Bytes(atLimit)).toBe(MAX_STASH_PAYLOAD_BYTES)

      expect(putStash(P1, H('a'), atLimit)).toBe('ok')
      expect(getStash(P1, H('a'))).toEqual(atLimit)
    })

    it('the limit counts UTF-8 bytes, not UTF-16 code units', () => {
      const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {})
      // 2 M CJK characters: 2 M code units (under the limit), 6 M bytes (over it).
      expect(putStash(P1, H('a'), { s: '繁'.repeat(2_000_000) })).toBe('failed')
      expect(setItem).not.toHaveBeenCalled()
    })

    it('dropSection / pruneStash with nothing to remove report ok without touching storage', () => {
      const setItem = vi.spyOn(Storage.prototype, 'setItem')
      const removeItem = vi.spyOn(Storage.prototype, 'removeItem')
      expect(dropSection(P1, 'settings')).toBe('ok')
      expect(pruneStash(P1, new Set())).toBe('ok')
      expect(setItem).not.toHaveBeenCalled()
      expect(removeItem).not.toHaveBeenCalled()
    })

    it('dropSection removes that one key and keeps the rest', () => {
      saveSection(P1, 'settings', SETTINGS)
      saveSection(P1, 'hosts', HOSTS)
      putStash(P1, H('a'), { a: 1 })

      expect(dropSection(P1, 'settings')).toBe('ok')

      expect(storedKeys()).toEqual([pKey(P1, H('a')), sKey(P1, 'hosts')].sort())
      expect(loadSectionStore(P1).sections).toEqual({ hosts: HOSTS })
    })
  })

  describe('saveConflict — payloads first, the section last, so a stored conflict always has its payloads', () => {
    it('writes every payload key before the section key', () => {
      const calls = refuseSetItem(() => false)

      expect(saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)).toBe('ok')

      expect(calls).toEqual([pKey(P1, H('b')), pKey(P1, H('d')), sKey(P1, 'settings')])
    })

    it('the LAST payload is refused → failed, and the section key is never written', () => {
      saveSection(P1, 'settings', SETTINGS)
      const calls = refuseSetItem((k) => k === pKey(P1, H('d')))

      expect(saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)).toBe('failed')

      expect(calls).toEqual([pKey(P1, H('b')), pKey(P1, H('d'))]) // it stopped there
      vi.restoreAllMocks()
      expect(loadSectionStore(P1).sections.settings).toEqual(SETTINGS) // the previous, unlocked section
      expect(localStorage.getItem(pKey(P1, H('d')))).toBeNull()
      // An unreferenced payload may be left behind; pruneStash is what clears it.
      expect(getStash(P1, H('b'))).toEqual({ mine: 1 })
      expect(pruneStash(P1, new Set())).toBe('ok')
      expect(storedKeys()).toEqual([sKey(P1, 'settings')])
    })

    it('the FIRST payload is refused → nothing at all is written', () => {
      const calls = refuseSetItem((k) => k === pKey(P1, H('b')))
      expect(saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)).toBe('failed')
      expect(calls).toEqual([pKey(P1, H('b'))])
      expect(storedKeys()).toEqual([])
    })

    it('the SECTION is refused → failed; the payloads stay, the section does not exist', () => {
      refuseSetItem((k) => k === sKey(P1, 'settings'))

      expect(saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)).toBe('failed')

      vi.restoreAllMocks()
      expect(storedKeys()).toEqual([pKey(P1, H('b')), pKey(P1, H('d'))].sort())
      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
    })

    it('a payload that is already stored with the same content is not written again', () => {
      putStash(P1, H('b'), { mine: 1 })
      const calls = refuseSetItem(() => false)

      expect(saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)).toBe('ok')

      expect(calls).toEqual([pKey(P1, H('d')), sKey(P1, 'settings')])
    })

    it('a payload that is stored but unreadable is written over', () => {
      writeRawPayload(P1, H('b'), '{nope')
      expect(saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)).toBe('ok')
      expect(getStash(P1, H('b'))).toEqual({ mine: 1 })
    })

    it('a payload the stash already holds need not be passed again', () => {
      putStash(P1, H('b'), { mine: 1 })
      expect(saveConflict(P1, 'settings', LOCKED, { [H('d')]: { theirs: 1 } })).toBe('ok')
      expect(loadSectionStore(P1).sections.settings).toEqual(LOCKED)
    })

    it('a payload the stash holds DAMAGED does not count as held', () => {
      writeRawPayload(P1, H('b'), '[1]')
      const before = snapshot()
      expect(saveConflict(P1, 'settings', LOCKED, { [H('d')]: { theirs: 1 } })).toBe('failed')
      expect(snapshot()).toBe(before)
    })

    it('the SOT moving on while locked: the new payload joins, the section is replaced', () => {
      saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)
      const moved = { ...LOCKED, conflict: { localHash: H('b'), sot: { rev: 7, hash: H('e') } } }

      expect(saveConflict(P1, 'settings', moved, { [H('e')]: { theirs: 2 } })).toBe('ok')

      expect(loadSectionStore(P1).sections.settings).toEqual(moved)
      expect(getStash(P1, H('e'))).toEqual({ theirs: 2 })
    })

    it('the LOCAL payload missing: failed, and NOTHING is written — not the section, not the payloads that were passed', () => {
      saveSection(P1, 'settings', SETTINGS)
      const before = snapshot()

      expect(saveConflict(P1, 'settings', LOCKED, { [H('d')]: { theirs: 1 } })).toBe('failed')

      expect(snapshot()).toBe(before)
    })

    it('ASYMMETRIC: only the local payload (the snapshot that was sent) → ok, and the lock survives a load', () => {
      expect(saveConflict(P1, 'settings', LOCKED, { [H('b')]: { mine: 1 } })).toBe('ok')
      expect(loadSectionStore(P1).sections.settings).toEqual(LOCKED)
      expect(getStash(P1, H('b'))).toEqual({ mine: 1 })
      expect(getStash(P1, H('d'))).toBeUndefined() // the SOT side can be fetched again; it was never held
    })

    it('ASYMMETRIC: the SOT moving on while locked, announced by hash only → re-saved without its payload', () => {
      saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)
      const moved = { ...LOCKED, conflict: { localHash: H('b'), sot: { rev: 7, hash: H('e') } } }
      expect(saveConflict(P1, 'settings', moved, {})).toBe('ok') // b is already stored; e is not held by anyone
      expect(loadSectionStore(P1).sections.settings).toEqual(moved)
    })
  })

  describe('the real quota (jsdom: 5,000,000 UTF-16 code units for the whole origin, keys included — no mock)', () => {
    it('a payload the quota has room for is stored and read back', () => {
      const big = asciiPayloadOfBytes(4_900_000)
      expect(saveConflict(P1, 'settings', { ...LOCKED, conflict: { localHash: H('b'), sot: { rev: 5, hash: null } } }, { [H('b')]: big })).toBe('ok')
      expect(getStash(P1, H('b'))).toEqual(big)
      expect(loadSectionStore(P1).sections.settings.conflict).toEqual({ localHash: H('b'), sot: { rev: 5, hash: null } })
    })

    it('a payload under the 5 MiB limit but over the quota: failed — decided by storage — and no section key', () => {
      saveSection(P1, 'settings', SETTINGS)
      const setItem = vi.spyOn(Storage.prototype, 'setItem')
      const tooBigForStorage = asciiPayloadOfBytes(MAX_STASH_PAYLOAD_BYTES) // 5,242,880 code units > 5,000,000

      expect(saveConflict(P1, 'settings', LOCKED, { [H('b')]: tooBigForStorage, [H('d')]: { theirs: 1 } })).toBe('failed')

      expect(setItem).toHaveBeenCalledTimes(1) // storage was asked, and said no
      expect(storedKeys()).toEqual([sKey(P1, 'settings')])
      expect(loadSectionStore(P1).sections.settings).toEqual(SETTINGS)
    })

    it('two payloads that each fit but not together: failed, no locked section; the orphan is prunable', () => {
      const mine = asciiPayloadOfBytes(3_000_000)
      const theirs = { s: 'y'.repeat(mine.s.length) }

      expect(saveConflict(P1, 'settings', LOCKED, { [H('b')]: mine, [H('d')]: theirs })).toBe('failed')

      expect(storedKeys()).toEqual([pKey(P1, H('b'))])
      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
      expect(pruneStash(P1, new Set())).toBe('ok')
      expect(storedKeys()).toEqual([])
    })
  })

  describe('referential integrity — a conflict is only restored with the payloads it needs', () => {
    const stored = (payloads: Record<string, unknown>, settings: unknown = LOCKED): void => {
      localStorage.clear()
      writeRawSection(P1, 'settings', settings)
      writeRawSection(P1, 'hosts', HOSTS)
      for (const [hash, payload] of Object.entries(payloads)) writeRawPayload(P1, hash, payload)
    }

    it('both payloads present → the conflict is kept', () => {
      stored(LOCKED_PAYLOADS)
      expect(loadSectionStore(P1).sections.settings).toEqual(LOCKED)
    })

    it.each([
      ['the localHash payload is missing', { [H('d')]: { theirs: 1 } }],
      ['the localHash payload is damaged', { [H('b')]: 'not an object', [H('d')]: { theirs: 1 } }],
      ['there are no payloads', {}],
    ])('%s → the conflict is dropped, base and currentHash stay, the other sections too', (_label, payloads) => {
      stored(payloads)

      const loaded = loadSectionStore(P1).sections

      expect(loaded.settings).toEqual(UNLOCKED)
      expect('conflict' in loaded.settings).toBe(false)
      expect(loaded.hosts).toEqual(HOSTS)
    })

    // The two sides are not alike: the local one is the snapshot that was SENT and
    // exists nowhere else; the SOT's can be fetched again (and keep-sot does just that).
    it.each([
      ['the sot payload is missing', { [H('b')]: { mine: 1 } }],
      ['the sot payload is damaged', { [H('b')]: { mine: 1 }, [H('d')]: [1] }],
      ['the sot payload is not JSON', { [H('b')]: { mine: 1 }, [H('d')]: '{nope' }],
    ])('%s → the conflict is KEPT: only the local side is required', (_label, payloads) => {
      stored(payloads)
      expect(loadSectionStore(P1).sections.settings).toEqual(LOCKED)
    })

    it('a null hash needs no payload: a delete over a tombstone is kept with no payload keys', () => {
      stored({}, DELETE_LOCKED)
      expect(loadSectionStore(P1).sections.settings).toEqual(DELETE_LOCKED)
    })

    it('a sent DELETE (localHash null) needs nothing at all, whatever the SOT holds', () => {
      const sentDelete = { ...LOCKED, conflict: { localHash: null, sot: { rev: 5, hash: H('d') } } }
      stored({ [H('d')]: { theirs: 1 } }, sentDelete)
      expect(loadSectionStore(P1).sections.settings).toEqual(sentDelete)

      stored({}, sentDelete)
      expect(loadSectionStore(P1).sections.settings).toEqual(sentDelete)
    })

    it('the load does not write', () => {
      stored({ [H('d')]: { theirs: 1 } })
      const before = snapshot()
      loadSectionStore(P1)
      expect(snapshot()).toBe(before)
    })

    it('pruneStash never removes a payload a stored conflict still refers to, whatever `keep` says', () => {
      saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)
      putStash(P1, H('e'), { loose: 1 })
      putStash(P1, H('f'), { kept: 1 })

      expect(pruneStash(P1, new Set([H('f')]))).toBe('ok')

      expect(storedKeys()).toEqual([pKey(P1, H('b')), pKey(P1, H('d')), pKey(P1, H('f')), sKey(P1, 'settings')].sort())
      expect(loadSectionStore(P1).sections.settings).toEqual(LOCKED)
    })

    it('pruneStash lets them go once the lock is lifted', () => {
      saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)
      saveSection(P1, 'settings', SETTINGS)

      pruneStash(P1, new Set())

      expect(storedKeys()).toEqual([sKey(P1, 'settings')])
    })

    it('pruneStash with only referenced payloads outside `keep` removes nothing', () => {
      saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)
      const removeItem = vi.spyOn(Storage.prototype, 'removeItem')
      expect(pruneStash(P1, new Set())).toBe('ok')
      expect(removeItem).not.toHaveBeenCalled()
    })
  })

  describe('a storage that refuses', () => {
    const blockWrites = (): void => {
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw quotaError()
      })
      vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
        throw new Error('blocked')
      })
    }

    it('no write throws; each reports failed, and the old data is intact', () => {
      saveSection(P1, 'settings', SETTINGS)
      putStash(P1, H('a'), { a: 1 })
      const before = snapshot()
      blockWrites()

      expect(saveSection(P1, 'hosts', HOSTS)).toBe('failed')
      expect(saveSection(P1, 'settings', HOSTS)).toBe('failed')
      expect(saveConflict(P1, 'hosts', DELETE_LOCKED, {})).toBe('failed')
      expect(putStash(P1, H('b'), { b: 1 })).toBe('failed')
      expect(dropSection(P1, 'settings')).toBe('failed')
      expect(pruneStash(P1, new Set())).toBe('failed')
      expect(clearSectionStore(P1)).toBe('failed')
      expect(clearSectionStore()).toBe('failed')

      // setItem is atomic per key: a refused write leaves the previous value, never half of the new one.
      expect(snapshot()).toBe(before)
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS })
    })

    it('a storage that throws on read → empty / undefined / failed, no throw', () => {
      saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('blocked')
      })

      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
      expect(getStash(P1, H('b'))).toBeUndefined()
      expect(dropSection(P1, 'settings')).toBe('failed')
      // The payloads could not be checked, so a conflict that relies on stored ones is not written.
      expect(saveConflict(P1, 'hosts', LOCKED, {})).toBe('failed')
    })

    it('a storage that cannot be enumerated → load is empty, prune and clear are failed, nothing is removed', () => {
      saveSection(P1, 'settings', SETTINGS)
      putStash(P1, H('a'), { a: 1 })
      const before = snapshot()
      const removeItem = vi.spyOn(Storage.prototype, 'removeItem')
      vi.spyOn(Storage.prototype, 'key').mockImplementation(() => {
        throw new Error('blocked')
      })

      expect(loadSectionStore(P1)).toEqual(EMPTY(P1))
      expect(pruneStash(P1, new Set())).toBe('failed')
      expect(clearSectionStore(P1)).toBe('failed')
      expect(clearSectionStore()).toBe('failed')
      // What does not enumerate still works.
      expect(getStash(P1, H('a'))).toEqual({ a: 1 })
      expect(saveSection(P1, 'hosts', HOSTS)).toBe('ok')

      expect(removeItem).not.toHaveBeenCalled()
      vi.restoreAllMocks()
      expect(snapshot()).not.toBe(before) // hosts was added …
      expect(loadSectionStore(P1).sections).toEqual({ settings: SETTINGS, hosts: HOSTS }) // … and nothing was lost
    })

    it('pruneStash removes nothing when a section cannot be read — it might hold a reference', () => {
      saveConflict(P1, 'settings', LOCKED, LOCKED_PAYLOADS)
      const removeItem = vi.spyOn(Storage.prototype, 'removeItem')
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('blocked')
      })

      expect(pruneStash(P1, new Set())).toBe('failed')
      expect(removeItem).not.toHaveBeenCalled()
    })
  })

  it('there is no in-memory cache: a write made behind its back is what the next read sees', () => {
    saveSection(P1, 'settings', SETTINGS)
    writeRawSection(P1, 'settings', HOSTS) // another window
    writeRawPayload(P1, H('a'), { a: 2 })
    expect(loadSectionStore(P1).sections).toEqual({ settings: HOSTS })
    expect(getStash(P1, H('a'))).toEqual({ a: 2 })
  })

  it('is not a synced store, and does not pretend to fence', () => {
    expect(moduleSource).toContain('export function loadSectionStore') // the raw source, not a transformed stub
    const code = moduleSource.replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, '')
    expect(code).not.toMatch(/syncManager/)
    expect(code).not.toMatch(/zustand/)
    expect(code).not.toMatch(/generation|fenced|claimSectionStore/)
  })
})
