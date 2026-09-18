import { describe, it, expect, vi } from 'vitest'
import { loadPairings, type PairingApi, type PairingAppHost, type PairingSnapshot } from './peer-pairing-load'
import type { PeerHostRow, PeerHostVerify } from './host-api'
import { pairStatus } from './peer-pairing'

const X: PairingAppHost = { hostId: 'hM', name: 'mlab', url: 'http://100.64.0.2:7860', status: 'connected' }
const AIR: PairingAppHost = { hostId: 'hA', name: 'Air 2026', url: 'http://100.64.0.4:7860', status: 'connected' }
const OTHER: PairingAppHost = { hostId: 'hO', name: 'Other', url: 'http://100.64.0.9:7860', status: 'connected' }

const row = (p: Partial<PeerHostRow>): PeerHostRow => ({
  alias: 'air', url: 'http://100.64.0.4:7860', host_id: 'wakes-air-2026:oa6drb',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true, ...p,
})
const ok = (alias: string, self_alias: string, host_id: string): PeerHostVerify =>
  ({ alias, host_id, ok: true, self_alias, daemon_version: '1.0.0-alpha.378' })

/** A fake API keyed by hostId; every method is a vi.fn so call counts are assertable. */
function fakeApi(spec: {
  info?: Record<string, string | Error>
  settings?: Record<string, string | Error>
  list?: Record<string, PeerHostRow[] | Error>
  verify?: Record<string, PeerHostVerify | Error>     // key `${hostId}/${alias}`
}): PairingApi {
  const pick = <T,>(m: Record<string, T | Error> | undefined, k: string): Promise<T> => {
    const v = m?.[k]
    if (v === undefined) return Promise.reject(new Error(`unexpected ${k}`))
    return v instanceof Error ? Promise.reject(v) : Promise.resolve(v)
  }
  return {
    info: vi.fn((h) => pick(spec.info, h).then((host_id) => ({ host_id }))),
    settings: vi.fn((h) => pick(spec.settings, h).then((alias) => ({ deliver: true, alias }))),
    list: vi.fn((h) => pick(spec.list, h)),
    verify: vi.fn((h, a) => pick(spec.verify, `${h}/${a}`)),
  }
}

const collect = () => { const snaps: PairingSnapshot[] = []; return { snaps, emit: (s: PairingSnapshot) => snaps.push(s) } }

describe('loadPairings — the §2.1 fixture (mlab ↔ air, drift)', () => {
  const api = () => fakeApi({
    info: { hM: 'mini-lab:278cbm', hA: 'wakes-air-2026:oa6drb' },
    settings: { hM: 'mini-lab', hA: 'air26' },
    list: {
      hM: [row({})],
      hA: [row({ alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm' })],
    },
    verify: {
      'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb'),
      'hA/mini-lab': ok('mini-lab', 'mini-lab', 'mini-lab:278cbm'),
    },
  })

  it('joins by host_id, finds the return entry, verifies both directions, ends bidirectional', async () => {
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], api(), emit)
    expect(final.error).toBeNull()
    expect(final.self).toEqual({ host_id: 'mini-lab:278cbm', self_alias: 'mini-lab' })
    expect(final.rows).toHaveLength(1)
    const r = final.rows[0]
    expect(r.counterpart).toEqual({ hostId: 'hA', name: 'Air 2026' })
    expect(r.counterpartCause).toBe('')
    expect(r.returnEntry?.alias).toBe('mini-lab')
    expect(r.outbound).toEqual({ ok: true, self_alias: 'air26', daemon_version: '1.0.0-alpha.378', host_id: 'wakes-air-2026:oa6drb' })
    expect(r.inbound).toEqual({ ok: true, self_alias: 'mini-lab', daemon_version: '1.0.0-alpha.378', host_id: 'mini-lab:278cbm' })
    expect(pairStatus(r.outbound, r.inbound)).toBe('bidirectional')
    // first emit: rows present, both sides pending → 'checking'
    expect(snaps[0].rows[0].outbound).toBe('pending')
    expect(snaps[0].rows[0].inbound).toBe('pending')
    expect(pairStatus(snaps[0].rows[0].outbound, snaps[0].rows[0].inbound)).toBe('checking')
    // one emit per settled verify after the first
    expect(snaps).toHaveLength(3)
    // every emitted snapshot is an independently stable object — no consumer
    // (e.g. React setState) should ever see one it stored change under it.
    expect(snaps[0]).not.toBe(snaps[1])
    expect(snaps[0].rows).not.toBe(snaps[2].rows)
  })

  it('calls verify exactly once per direction and list exactly once per host', async () => {
    const a = api()
    await loadPairings(X, [AIR], a, () => {})
    expect(a.verify).toHaveBeenCalledTimes(2)
    expect(a.verify).toHaveBeenCalledWith('hM', 'air')
    expect(a.verify).toHaveBeenCalledWith('hA', 'mini-lab')
    expect(a.list).toHaveBeenCalledTimes(2)
    expect(a.info).toHaveBeenCalledTimes(2)
    expect(a.settings).toHaveBeenCalledTimes(2)
  })
})

describe('loadPairings — page-level preconditions (§5.2 step 0)', () => {
  it('a failing settings(X) yields the banner, no rows, and no calls to any other host', async () => {
    const a = fakeApi({ info: { hM: 'mini-lab:278cbm' }, settings: { hM: new Error('HTTP 500') }, list: { hM: [row({})] } })
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], a, emit)
    expect(final).toEqual({ self: null, error: { call: 'settings', message: 'HTTP 500' }, rows: [] })
    expect(snaps).toEqual([final])
    expect(a.info).not.toHaveBeenCalledWith('hA')
    expect(a.verify).not.toHaveBeenCalled()
  })
  it('a failing list(X) names "list"', async () => {
    const a = fakeApi({ info: { hM: 'm:1' }, settings: { hM: 'm' }, list: { hM: new Error('403 admin required') } })
    const final = await loadPairings(X, [], a, () => {})
    expect(final.error).toEqual({ call: 'list', message: '403 admin required' })
  })
  it('an empty entry list is not an error', async () => {
    const a = fakeApi({ info: { hM: 'm:1' }, settings: { hM: 'm' }, list: { hM: [] } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.error).toBeNull()
    expect(final.rows).toEqual([])
    expect(a.verify).not.toHaveBeenCalled()
  })
})

describe('loadPairings — the return side (§5.1 inbound states)', () => {
  const base = { info: { hM: 'mini-lab:278cbm', hA: 'wakes-air-2026:oa6drb' }, settings: { hM: 'mini-lab', hA: 'air26' } }

  it('no App host is the peer → not-app-host, and only the outbound verify runs', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({ host_id: 'stranger:aaaaaa', url: 'http://10.0.0.1:7860' })] },
      verify: { 'hM/air': ok('air', 'stranger', 'stranger:aaaaaa') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpart).toBeNull()
    expect(final.rows[0].inbound).toBe('not-app-host')
    expect(a.verify).toHaveBeenCalledTimes(1)
    expect(pairStatus(final.rows[0].outbound, final.rows[0].inbound)).toBe('outbound-only')
  })

  it('the counterpart has no entry for X → no-entry', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: [] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpart?.hostId).toBe('hA')
    expect(final.rows[0].returnEntry).toBeNull()
    expect(final.rows[0].inbound).toBe('no-entry')
    expect(a.verify).toHaveBeenCalledTimes(1)
  })

  it('a disconnected App host still joins by URL and is counterpart-unavailable with its status as cause', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [{ ...AIR, status: 'disconnected' }], a, () => {})
    const r = final.rows[0]
    expect(r.counterpart).toEqual({ hostId: 'hA', name: 'Air 2026' })
    expect(r.counterpartCause).toBe('disconnected')
    expect(r.inbound).toBe('counterpart-unavailable')
    expect(a.info).not.toHaveBeenCalledWith('hA')
    expect(pairStatus(r.outbound, r.inbound)).toBe('return-unknown')
  })

  it('an auth-error host is unavailable with cause auth-error, never not-app-host', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [{ ...AIR, status: 'auth-error' }], a, () => {})
    expect(final.rows[0].counterpartCause).toBe('auth-error')
    expect(final.rows[0].inbound).toBe('counterpart-unavailable')
  })

  it('a host with status undefined is unavailable with cause "unknown"', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [{ ...AIR, status: undefined }], a, () => {})
    expect(final.rows[0].counterpartCause).toBe('unknown')
    expect(final.rows[0].inbound).toBe('counterpart-unavailable')
    expect(a.info).not.toHaveBeenCalledWith('hA')
  })

  it('a reconnecting host is unavailable with cause reconnecting', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [{ ...AIR, status: 'reconnecting' }], a, () => {})
    expect(final.rows[0].counterpartCause).toBe('reconnecting')
    expect(final.rows[0].inbound).toBe('counterpart-unavailable')
  })

  it('a connected host whose info() fails is unavailable with "info: <message>"', async () => {
    const a = fakeApi({ info: { hM: 'mini-lab:278cbm', hA: new Error('HTTP 502') }, settings: { hM: 'mini-lab', hA: 'air26' },
      list: { hM: [row({})] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpartCause).toBe('info: HTTP 502')
    expect(final.rows[0].inbound).toBe('counterpart-unavailable')
  })

  it('a failing list(Y) makes every entry joined to Y unavailable with "list: <message>"', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: new Error('HTTP 500') }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpart?.hostId).toBe('hA')
    expect(final.rows[0].counterpartCause).toBe('list: HTTP 500')
    expect(final.rows[0].inbound).toBe('counterpart-unavailable')
  })

  it('two entries pointing at the same daemon share ONE list(Y) call', async () => {
    const a = fakeApi({ ...base,
      list: { hM: [row({}), row({ alias: 'air-again', url: 'http://air.local:7860' })],
              hA: [row({ alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm' })] },
      verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb'), 'hM/air-again': ok('air-again', 'air26', 'wakes-air-2026:oa6drb'),
                'hA/mini-lab': ok('mini-lab', 'mini-lab', 'mini-lab:278cbm') } })
    const final = await loadPairings(X, [AIR, OTHER], a, () => {})
    expect(a.list).toHaveBeenCalledTimes(2)              // hM once, hA once; OTHER is nobody's counterpart → never listed
    expect(a.list).not.toHaveBeenCalledWith('hO')
    expect(final.rows.map((r) => r.returnEntry?.alias)).toEqual(['mini-lab', 'mini-lab'])
  })

  it('a rejected verify (e.g. 404 because the entry vanished) is that direction failing, not a page error', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: [row({ alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm' })] },
      verify: { 'hM/air': new Error('404 unknown alias'), 'hA/mini-lab': ok('mini-lab', 'mini-lab', 'mini-lab:278cbm') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.error).toBeNull()
    expect(final.rows[0].outbound).toEqual({ ok: false, error: '404 unknown alias' })
    expect(pairStatus(final.rows[0].outbound, final.rows[0].inbound)).toBe('one-way')
  })

  it('the return entry is found by URL when the counterpart row has no host_id', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: [row({ alias: 'mlab-by-url', url: 'http://100.64.0.2:7860/', host_id: '' })] },
      verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb'), 'hA/mlab-by-url': ok('mlab-by-url', 'mini-lab', 'mini-lab:278cbm') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].returnEntry?.alias).toBe('mlab-by-url')
    expect(a.verify).toHaveBeenCalledWith('hA', 'mlab-by-url')
  })
})
