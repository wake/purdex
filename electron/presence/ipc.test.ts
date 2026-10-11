import { describe, expect, it, vi } from 'vitest'
import {
  createAwayMinutesStore,
  registerPresenceIpc,
  validateHostsPayload,
  MAX_HOSTS,
  MAX_PAYLOAD_BYTES,
  type IpcMainLike,
} from './ipc'

const TOKEN = 'tok-SECRET-9999'
const good = () => ({ rev: 7, hosts: [{ hostId: 'h1', baseUrl: 'http://100.64.0.2:7860', token: TOKEN }] })
const reasonOf = (raw: unknown) => {
  const r = validateHostsPayload(raw)
  return r.ok ? null : r.reason
}

describe('validateHostsPayload: accepted', () => {
  it('accepts a good payload and canonicalises baseUrl to its origin', () => {
    const r = validateHostsPayload(good())
    expect(r).toEqual({ ok: true, value: good() })
  })
  it.each([
    ['http://100.64.0.2:7860/', 'http://100.64.0.2:7860'],
    ['http://100.64.0.2:7860', 'http://100.64.0.2:7860'],
    ['HTTPS://Example.COM:443', 'https://example.com'],
    ['http://host.mlab.host:80/', 'http://host.mlab.host'],
    ['https://[::1]:8443/', 'https://[::1]:8443'],
  ])('canonicalises %s', (input, origin) => {
    const p = good()
    p.hosts[0].baseUrl = input
    const r = validateHostsPayload(p)
    expect(r.ok && r.value.hosts[0].baseUrl).toBe(origin)
  })
  it('accepts a null token, an empty token and a 4096-char token', () => {
    for (const token of [null, '', 'x'.repeat(4096)]) {
      const p = good()
      ;(p.hosts[0] as { token: string | null }).token = token
      expect(validateHostsPayload(p).ok).toBe(true)
    }
  })
  it('accepts an empty host list and rev 0 and the largest safe integer', () => {
    expect(validateHostsPayload({ rev: 0, hosts: [] }).ok).toBe(true)
    expect(validateHostsPayload({ rev: Number.MAX_SAFE_INTEGER, hosts: [] }).ok).toBe(true)
  })
  it('accepts exactly the maximum number of hosts', () => {
    const hosts = Array.from({ length: MAX_HOSTS }, (_, i) => ({ hostId: `h${i}`, baseUrl: 'http://h:1', token: null }))
    expect(validateHostsPayload({ rev: 1, hosts }).ok).toBe(true)
  })
  it('accepts a 128-char hostId', () => {
    const p = good()
    p.hosts[0].hostId = 'a'.repeat(128)
    expect(validateHostsPayload(p).ok).toBe(true)
  })
  it('strips unknown keys', () => {
    const r = validateHostsPayload({ rev: 1, extra: 1, hosts: [{ hostId: 'h', baseUrl: 'http://h:1', token: null, more: 2 }] })
    expect(r).toEqual({ ok: true, value: { rev: 1, hosts: [{ hostId: 'h', baseUrl: 'http://h:1', token: null }] } })
  })
})

describe('validateHostsPayload: rejected', () => {
  const badRoots: Array<[string, unknown]> = [
    ['null', null],
    ['undefined', undefined],
    ['string', 'x'],
    ['number', 5],
    ['array', []],
    ['class instance', new (class X { rev = 1; hosts = [] })()],
    ['map', new Map()],
  ]
  it.each(badRoots)('root: %s', (_n, raw) => {
    expect(validateHostsPayload(raw).ok).toBe(false)
  })
  it.each([
    ['missing', undefined],
    ['string', '5'],
    ['negative', -1],
    ['fraction', 1.5],
    ['NaN', NaN],
    ['Infinity', Infinity],
    ['unsafe', Number.MAX_SAFE_INTEGER + 2],
    ['null', null],
    ['bigint', BigInt(1)],
  ])('rev: %s', (_n, rev) => {
    expect(validateHostsPayload({ rev, hosts: [] }).ok).toBe(false)
  })
  it('hosts must be an array', () => {
    expect(validateHostsPayload({ rev: 1 }).ok).toBe(false)
    expect(validateHostsPayload({ rev: 1, hosts: {} }).ok).toBe(false)
    expect(validateHostsPayload({ rev: 1, hosts: 'x' }).ok).toBe(false)
  })
  it('more than the maximum number of hosts', () => {
    const hosts = Array.from({ length: MAX_HOSTS + 1 }, (_, i) => ({ hostId: `h${i}`, baseUrl: 'http://h:1', token: null }))
    expect(validateHostsPayload({ rev: 1, hosts }).ok).toBe(false)
  })
  it.each([null, 'x', 5, [], undefined])('host entry: %s', (entry) => {
    expect(validateHostsPayload({ rev: 1, hosts: [entry] }).ok).toBe(false)
  })
  it.each([
    ['missing', undefined],
    ['empty', ''],
    ['number', 5],
    ['129 chars', 'a'.repeat(129)],
    ['newline', 'a\nb'],
    ['carriage return', 'a\rb'],
    ['NUL', 'a\u0000b'],
    ['space', 'a b'],
    ['unicode', 'h\u00e9'],
    ['cjk', '\u4e3b\u6a5f'],
    ['slash', 'a/b'],
  ])('hostId: %s', (_n, hostId) => {
    const p = good()
    ;(p.hosts[0] as { hostId: unknown }).hostId = hostId
    expect(validateHostsPayload(p).ok).toBe(false)
  })
  it.each(['abc123', 'local', 'h1', 'Host_1-b', 'a'.repeat(128)])('a generated or legacy host id is accepted: %s', (hostId) => {
    const p = good()
    p.hosts[0].hostId = hostId
    expect(validateHostsPayload(p).ok).toBe(true)
  })
  it('duplicate hostId', () => {
    const e = { hostId: 'dup', baseUrl: 'http://h:1', token: null }
    expect(validateHostsPayload({ rev: 1, hosts: [e, { ...e, baseUrl: 'http://other:1' }] }).ok).toBe(false)
  })
  it.each([
    ['missing', undefined],
    ['number', 5],
    ['empty', ''],
    ['no scheme', '100.64.0.2:7860'],
    ['not a url', 'not a url'],
    ['ftp', 'ftp://h:1'],
    ['file', 'file:///etc/passwd'],
    ['javascript', 'javascript:alert(1)'],
    ['ws', 'ws://h:1'],
    ['userinfo', 'http://user@h:1'],
    ['userinfo + password', 'http://user:pw@h:1'],
    ['empty userinfo password', 'http://:pw@h:1'],
    ['path', 'http://h:1/api'],
    ['double slash path', 'http://h:1//'],
    ['query', 'http://h:1/?a=1'],
    ['bare ?', 'http://h:1?'],
    ['bare ? after slash', 'http://h:1/?'],
    ['hash', 'http://h:1/#x'],
    ['bare #', 'http://h:1#'],
    ['too long', 'http://' + 'a'.repeat(2100) + ':1'],
  ])('baseUrl: %s', (_n, baseUrl) => {
    const p = good()
    ;(p.hosts[0] as { baseUrl: unknown }).baseUrl = baseUrl
    expect(validateHostsPayload(p).ok).toBe(false)
  })
  it.each([
    ['missing', undefined],
    ['number', 5],
    ['object', {}],
    ['4097 chars', 'x'.repeat(4097)],
  ])('token: %s', (_n, token) => {
    const p = good()
    ;(p.hosts[0] as { token: unknown }).token = token
    expect(validateHostsPayload(p).ok).toBe(false)
  })
  it('a payload over the size cap', () => {
    const hosts = Array.from({ length: MAX_HOSTS }, (_, i) => ({ hostId: `h${i}`, baseUrl: 'http://h:1', token: 'x'.repeat(4096) }))
    expect(JSON.stringify({ rev: 1, hosts }).length).toBeGreaterThan(MAX_PAYLOAD_BYTES)
    expect(validateHostsPayload({ rev: 1, hosts }).ok).toBe(false)
  })
  it('the cap counts UTF-8 bytes, not UTF-16 units', () => {
    const hosts = Array.from({ length: MAX_HOSTS }, (_, i) => ({ hostId: `h${i}`, baseUrl: 'http://h:1', token: '\u20ac'.repeat(1400) }))
    const text = JSON.stringify({ rev: 1, hosts })
    expect(text.length).toBeLessThan(MAX_PAYLOAD_BYTES)
    expect(Buffer.byteLength(text, 'utf8')).toBeGreaterThan(MAX_PAYLOAD_BYTES)
    expect(validateHostsPayload({ rev: 1, hosts }).ok).toBe(false)
  })
  it('a payload that cannot be serialised', () => {
    const p: { rev: number; hosts: unknown[]; self?: unknown } = { rev: 1, hosts: [] }
    p.self = p
    expect(validateHostsPayload(p).ok).toBe(false)
  })
  it('reasons never carry a value from the payload', () => {
    const secret = 'SECRET-VALUE-123'
    const probes: unknown[] = [
      { rev: secret, hosts: [] },
      { rev: 1, hosts: [{ hostId: secret + 'x'.repeat(200), baseUrl: 'http://h:1', token: null }] },
      { rev: 1, hosts: [{ hostId: 'a', baseUrl: `http://${secret}@h:1`, token: null }] },
      { rev: 1, hosts: [{ hostId: 'a', baseUrl: `http://h:1/${secret}`, token: null }] },
      { rev: 1, hosts: [{ hostId: 'a', baseUrl: `javascript:${secret}`, token: null }] },
      { rev: 1, hosts: [{ hostId: secret, baseUrl: 'http://h:1', token: null }, { hostId: secret, baseUrl: 'http://h:1', token: null }] },
      { rev: 1, hosts: [{ hostId: 'a', baseUrl: 'http://h:1', token: { secret } }] },
    ]
    for (const raw of probes) {
      const reason = reasonOf(raw)
      expect(reason).not.toBeNull()
      expect(reason).not.toContain(secret)
    }
  })
})

describe('createAwayMinutesStore', () => {
  it('loads once, serves from memory, saves on set and returns the saved value', () => {
    const load = vi.fn(() => 10)
    const save = vi.fn()
    const s = createAwayMinutesStore({ load, save })
    expect(s.get()).toBe(10)
    expect(s.get()).toBe(10)
    expect(load).toHaveBeenCalledTimes(1)
    expect(s.set(30)).toBe(30)
    expect(save).toHaveBeenCalledWith(30)
    expect(s.get()).toBe(30)
  })
  it.each([0, 4, 61, -1, 5.5, NaN, '5', null, undefined, {}, [5]])('rejects %s without saving', (v) => {
    const save = vi.fn()
    const s = createAwayMinutesStore({ load: () => 5, save })
    expect(() => s.set(v)).toThrow()
    expect(save).not.toHaveBeenCalled()
    expect(s.get()).toBe(5)
  })
  it('the error message carries no value', () => {
    const s = createAwayMinutesStore({ load: () => 5, save: () => {} })
    try {
      s.set('SECRET-VALUE')
    } catch (e) {
      expect(String((e as Error).message)).not.toContain('SECRET-VALUE')
    }
    expect.assertions(1)
  })
  it('a failed save keeps the old value and rethrows', () => {
    const s = createAwayMinutesStore({ load: () => 5, save: () => { throw new Error('disk full') } })
    expect(() => s.set(10)).toThrow()
    expect(s.get()).toBe(5)
  })
})

function harness() {
  const handlers = new Map<string, (e: unknown, ...a: unknown[]) => unknown>()
  const ipcMain: IpcMainLike = { handle: (ch, fn) => { handlers.set(ch, fn) } }
  const reporter = {
    setHosts: vi.fn(() => 'applied' as const),
    force: vi.fn(),
    getOwned: vi.fn(() => ['h1']),
  }
  const logs: string[] = []
  const minutes = createAwayMinutesStore({ load: () => 5, save: () => {} })
  registerPresenceIpc({ ipcMain, reporter, minutes, logger: { warn: (m) => logs.push(m) } })
  const call = (ch: string, ...args: unknown[]) => handlers.get(ch)!({}, ...args)
  return { handlers, reporter, logs, call, minutes }
}

describe('registerPresenceIpc', () => {
  it('registers exactly the four channels', () => {
    const h = harness()
    expect([...h.handlers.keys()].sort()).toEqual([
      'presence:get-away-minutes',
      'presence:get-owned',
      'presence:set-away-minutes',
      'presence:set-hosts',
    ])
  })
  it('set-hosts validates, hands the canonical payload to the reporter, returns true', () => {
    const h = harness()
    const p = good()
    p.hosts[0].baseUrl = 'http://100.64.0.2:7860/'
    expect(h.call('presence:set-hosts', p)).toBe(true)
    expect(h.reporter.setHosts).toHaveBeenCalledWith({ rev: 7, hosts: [{ hostId: 'h1', baseUrl: 'http://100.64.0.2:7860', token: TOKEN }] })
  })
  it('set-hosts returns true for a stale rev the reporter ignored (valid, just old)', () => {
    const h = harness()
    h.reporter.setHosts.mockReturnValueOnce('ignored' as never)
    expect(h.call('presence:set-hosts', good())).toBe(true)
  })
  it('set-hosts returns false for an invalid payload, does not call the reporter, and logs a reason with no values', () => {
    const h = harness()
    const p = good()
    p.hosts[0].baseUrl = `http://user:${TOKEN}@h:1`
    expect(h.call('presence:set-hosts', p)).toBe(false)
    expect(h.reporter.setHosts).not.toHaveBeenCalled()
    expect(h.logs).toHaveLength(1)
    expect(h.logs[0]).not.toContain(TOKEN)
    expect(h.logs[0]).not.toContain('user:')
    expect(h.logs[0]).not.toContain('h1')
  })
  it('set-hosts never throws for hostile arguments', () => {
    const h = harness()
    for (const arg of [undefined, null, 1, 'x', [], () => 1, Symbol('s')]) {
      expect(() => h.call('presence:set-hosts', arg)).not.toThrow()
    }
    expect(() => h.handlers.get('presence:set-hosts')!({})).not.toThrow()
  })
  it('get-owned returns host ids only', () => {
    const h = harness()
    expect(h.call('presence:get-owned')).toEqual(['h1'])
  })
  it('get-away-minutes returns the stored value', () => {
    const h = harness()
    expect(h.call('presence:get-away-minutes')).toBe(5)
  })
  it('set-away-minutes saves, forces a send with reason n, returns the saved value', () => {
    const h = harness()
    expect(h.call('presence:set-away-minutes', 15)).toBe(15)
    expect(h.call('presence:get-away-minutes')).toBe(15)
    expect(h.reporter.force).toHaveBeenCalledWith('n')
  })
  it('set-away-minutes outside the set rejects and forces nothing', () => {
    const h = harness()
    expect(() => h.call('presence:set-away-minutes', 7)).toThrow()
    expect(() => h.call('presence:set-away-minutes', '5')).toThrow()
    expect(() => h.call('presence:set-away-minutes')).toThrow()
    expect(h.reporter.force).not.toHaveBeenCalled()
    expect(h.call('presence:get-away-minutes')).toBe(5)
  })
})
