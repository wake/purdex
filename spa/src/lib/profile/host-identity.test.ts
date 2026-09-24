import { afterEach, describe, expect, it, vi } from 'vitest'
import { identityOfSync, isSyncId, SYNC_ID_PREFIX, syncIdOf, syncIdOfSync, wireIdOfHost } from './host-identity'

// Golden vectors — computed ONCE with Python's hashlib (independent of the code
// under test and of any JS SHA-256), then pinned:
//   h = hashlib.sha256(x.encode('utf-8')).digest()[:10]
//   'd1_' + base36(int.from_bytes(h, 'big')).rjust(16, '0')
// Changing any of these changes every wire key of every profile: never "update" them.
const GOLDEN: ReadonlyArray<readonly [string, string]> = [
  ['mini-lab:278cbm', 'd1_2u8ajsho6ji7nk6h'],
  ['My Mac Book:abc123', 'd1_2cxzknyi2nlagua4'],
  ['東京-サーバー:x9z0k1 🚀', 'd1_2jzgknv1g0lj9a7j'],
  ['a'.repeat(512), 'd1_1ioknbyfwhi9eaj8'],
  // base36 of this digest prefix has 15 digits: the left pad is load-bearing.
  ['pad:0', 'd1_0w9g43l4ltrh27g0'],
]

describe('syncIdOf / syncIdOfSync (spec §3)', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it.each(GOLDEN)('sync path: %s', (daemonId, expected) => {
    expect(syncIdOfSync(daemonId)).toBe(expected)
  })

  it.each(GOLDEN)('async path via crypto.subtle: %s', async (daemonId, expected) => {
    expect(typeof globalThis.crypto?.subtle?.digest).toBe('function')
    expect(await syncIdOf(daemonId)).toBe(expected)
  })

  it.each(GOLDEN)('async path via the pure-JS fallback (no crypto.subtle): %s', async (daemonId, expected) => {
    vi.stubGlobal('crypto', { subtle: undefined })
    expect(globalThis.crypto.subtle).toBeUndefined()
    expect(await syncIdOf(daemonId)).toBe(expected)
  })

  it('is always the prefix plus 16 lower-case base36 characters', () => {
    for (const [daemonId] of GOLDEN) {
      expect(syncIdOfSync(daemonId)).toMatch(/^d1_[0-9a-z]{16}$/)
    }
    expect(SYNC_ID_PREFIX).toBe('d1_')
  })
})

describe('isSyncId', () => {
  it('recognises any d<digits>_ prefix as a sync id', () => {
    expect(isSyncId('d1_2u8ajsho6ji7nk6h')).toBe(true)
    expect(isSyncId('d2_whatever')).toBe(true)
    expect(isSyncId('d10_x')).toBe(true)
  })

  it('treats everything else as not a sync id', () => {
    for (const s of ['abc123', 'd_1', 'dx_1', 'D1_abc', 'xd1_abc', '', 'd1']) {
      expect(isSyncId(s), s).toBe(false)
    }
    expect(isSyncId(undefined)).toBe(false)
    expect(isSyncId(42)).toBe(false)
  })
})

describe('wireIdOfHost (H2 plan §0.4)', () => {
  it('a host with a valid daemonId travels under syncIdOfSync(daemonId)', () => {
    expect(wireIdOfHost({ id: 'abc123', daemonId: 'mini-lab:278cbm' })).toBe('d1_2u8ajsho6ji7nk6h')
  })

  it('a host with no / an empty / an invalid daemonId travels under its local id', () => {
    expect(wireIdOfHost({ id: 'abc123' })).toBe('abc123')
    expect(wireIdOfHost({ id: 'abc123', daemonId: '' })).toBe('abc123')
    expect(wireIdOfHost({ id: 'abc123', daemonId: 'bad\u0000id' })).toBe('abc123')
    expect(wireIdOfHost({ id: 'abc123', daemonId: 'x'.repeat(513) })).toBe('abc123')
  })

  it('equals identityOfSync(hosts).toWire for every host of a conflict-free snapshot', () => {
    const hosts = {
      aaa111: { id: 'aaa111', daemonId: 'mini-lab:278cbm' },
      bbb222: { id: 'bbb222' },
      ccc333: { id: 'ccc333', daemonId: 'My Mac Book:abc123' },
      ddd444: { id: 'ddd444', daemonId: '' },
    }
    const identity = identityOfSync(hosts)
    expect(identity.conflict).toBeNull()
    for (const [id, host] of Object.entries(hosts)) {
      expect(wireIdOfHost(host), id).toBe(identity.toWire.get(id))
    }
  })

  it('under a conflict (two rows, one daemon) both rows still get the same d1_ id', () => {
    const hosts = {
      aaa111: { id: 'aaa111', daemonId: 'mini-lab:278cbm' },
      bbb222: { id: 'bbb222', daemonId: 'mini-lab:278cbm' },
    }
    // toWire leaves both conflicting hosts out; the per-host rule does not.
    expect(identityOfSync(hosts).conflict).toEqual(['aaa111', 'bbb222'])
    expect(wireIdOfHost(hosts.aaa111)).toBe('d1_2u8ajsho6ji7nk6h')
    expect(wireIdOfHost(hosts.bbb222)).toBe('d1_2u8ajsho6ji7nk6h')
  })

  it('memoises per daemonId: the hash runs once per daemonId', () => {
    const hash = vi.fn((daemonId: string) => `d1_${daemonId.length}`)
    expect(wireIdOfHost({ id: 'aaa111', daemonId: 'memo-a:1' }, { hash })).toBe('d1_8')
    expect(wireIdOfHost({ id: 'bbb222', daemonId: 'memo-a:1' }, { hash })).toBe('d1_8')
    expect(wireIdOfHost({ id: 'aaa111', daemonId: 'memo-a:1' }, { hash })).toBe('d1_8')
    expect(hash).toHaveBeenCalledTimes(1)
    expect(wireIdOfHost({ id: 'ccc333', daemonId: 'memo-b:22' }, { hash })).toBe('d1_9')
    expect(hash).toHaveBeenCalledTimes(2)
    // a host without a claim never hashes
    wireIdOfHost({ id: 'ddd444' }, { hash })
    expect(hash).toHaveBeenCalledTimes(2)
  })
})
