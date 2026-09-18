import { describe, it, expect, vi } from 'vitest'
import { loadPairings, type PairingApi, type PairingAppHost, type PairingSnapshot } from './peer-pairing-load'
import { HostApiError, type PeerHostRow, type PeerHostVerify } from './host-api'
import { pairStatus } from './peer-pairing'

const X: PairingAppHost = { hostId: 'hM', name: 'mlab', url: 'http://100.64.0.2:7860', status: 'connected' }
const AIR: PairingAppHost = { hostId: 'hA', name: 'Air 2026', url: 'http://100.64.0.4:7860', status: 'connected' }
const OTHER: PairingAppHost = { hostId: 'hO', name: 'Other', url: 'http://100.64.0.9:7860', status: 'connected' }

const row = (p: Partial<PeerHostRow>): PeerHostRow => ({
  alias: 'air', url: 'http://100.64.0.4:7860', host_id: 'wakes-air-2026:oa6drb',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true,
  rotation_pending: false, last_inbound_auth: '', ...p,
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

  it("carries X's alias_source from the step-0 settings call into self (self alias #1196) — no second call", async () => {
    const a = api()
    a.settings = vi.fn(async (h: string) => ({ deliver: true, alias: h === 'hM' ? 'mlab' : 'air26', alias_source: 'config' as const }))
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.self).toEqual({ host_id: 'mini-lab:278cbm', self_alias: 'mlab', self_alias_source: 'config' })
    expect(a.settings).toHaveBeenCalledTimes(2)
  })

  it('an old daemon (settings without alias_source) leaves self.self_alias_source undefined', async () => {
    const final = await loadPairings(X, [AIR], api(), () => {})
    expect(final.self?.self_alias).toBe('mini-lab')
    expect(final.self?.self_alias_source).toBeUndefined()
  })
})

describe('loadPairings — page-level preconditions (§5.2 step 0)', () => {
  it('a failing settings(X) yields the banner, no rows, and no calls to any other host', async () => {
    const a = fakeApi({ info: { hM: 'mini-lab:278cbm' }, settings: { hM: new Error('HTTP 500') }, list: { hM: [row({})] } })
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], a, emit)
    expect(final).toEqual({ self: null, error: { call: 'settings', message: 'HTTP 500' }, rows: [], candidates: [] })
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
  it('a HostApiError on list(X) surfaces the daemon detail, not the status line', async () => {
    const a = fakeApi({ info: { hM: 'm:1' }, settings: { hM: 'm' },
      list: { hM: new HostApiError(403, 'Forbidden', 'admin required') } })
    const final = await loadPairings(X, [], a, () => {})
    expect(final.error).toEqual({ call: 'list', message: 'admin required' })
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

  it('a connected host whose info() rejects with a HostApiError is unavailable with "info: <detail>"', async () => {
    const a = fakeApi({ info: { hM: 'mini-lab:278cbm', hA: new HostApiError(502, 'Bad Gateway', 'upstream down') },
      settings: { hM: 'mini-lab', hA: 'air26' }, list: { hM: [row({})] }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpartCause).toBe('info: upstream down')
    expect(final.rows[0].inbound).toBe('counterpart-unavailable')
  })

  it('a failing list(Y) makes every entry joined to Y unavailable with "list: <message>"', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: new Error('HTTP 500') }, verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpart?.hostId).toBe('hA')
    expect(final.rows[0].counterpartCause).toBe('list: HTTP 500')
    expect(final.rows[0].inbound).toBe('counterpart-unavailable')
  })

  it('a failing list(Y) that is a HostApiError keeps the daemon detail, not the status line (codex F3)', async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: new HostApiError(403, 'Forbidden', 'admin required') },
      verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.rows[0].counterpartCause).toBe('list: admin required')
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

  it('a URL-only entry joins the known App host over an unavailable one at the same URL, order-invariant (codex F2)', async () => {
    const entryByUrl = row({ host_id: '', url: 'http://100.64.0.4:7860' })
    const air2 = { hostId: 'hA2', name: 'Air again', url: 'http://100.64.0.4:7860', status: 'connected' as const }
    const spec = {
      info: { hM: 'mini-lab:278cbm', hA2: 'wakes-air-2026:oa6drb' },
      settings: { hM: 'mini-lab', hA2: 'air26' },
      list: { hM: [entryByUrl], hA2: [row({ alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm' })] },
      verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb'), 'hA2/mini-lab': ok('mini-lab', 'mini-lab', 'mini-lab:278cbm') },
    }
    const a1 = fakeApi(spec)
    const final1 = await loadPairings(X, [{ ...AIR, status: 'disconnected' }, air2], a1, () => {})
    expect(final1.rows[0].counterpart?.hostId).toBe('hA2')
    expect(a1.list).not.toHaveBeenCalledWith('hA')

    const a2 = fakeApi(spec)
    const final2 = await loadPairings(X, [air2, { ...AIR, status: 'disconnected' }], a2, () => {})
    expect(final2.rows[0].counterpart?.hostId).toBe('hA2')
    expect(a2.list).not.toHaveBeenCalledWith('hA')
  })
})

describe('loadPairings — pair candidates (spec §7.1: available App hosts with no entry on X)', () => {
  const base = { info: { hM: 'mini-lab:278cbm', hA: 'wakes-air-2026:oa6drb' }, settings: { hM: 'mini-lab', hA: 'air26' } }
  const RETURN = row({ alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm' })

  it('an available host with no entry on X is listed with returnEntry null, listError "" and costs exactly one extra list call', async () => {
    const a = fakeApi({ ...base, list: { hM: [], hA: [] } })
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], a, emit)
    expect(final.rows).toEqual([])
    expect(final.candidates).toEqual([
      { hostId: 'hA', name: 'Air 2026', url: 'http://100.64.0.4:7860', host_id: 'wakes-air-2026:oa6drb', returnEntry: null, listError: '' },
    ])
    expect(a.list).toHaveBeenCalledTimes(2)
    expect(a.list).toHaveBeenCalledWith('hM')
    expect(a.list).toHaveBeenCalledWith('hA')
    // candidates are in the pre-dial emit already
    expect(snaps[0].candidates).toEqual(final.candidates)
  })

  it('the repair case — Y already holds an entry for X → returnEntry is that row', async () => {
    const a = fakeApi({ ...base, list: { hM: [], hA: [RETURN] } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.candidates).toHaveLength(1)
    expect(final.candidates[0].hostId).toBe('hA')
    expect(final.candidates[0].returnEntry).toEqual(RETURN)
    expect(final.candidates[0].listError).toBe('')
    expect(a.verify).not.toHaveBeenCalled()
  })

  it("a host that is some row's counterpart is not a candidate (the D2 fixture → empty)", async () => {
    const a = fakeApi({ ...base, list: { hM: [row({})], hA: [RETURN] },
      verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb'), 'hA/mini-lab': ok('mini-lab', 'mini-lab', 'mini-lab:278cbm') } })
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], a, emit)
    expect(final.rows).toHaveLength(1)
    expect(final.candidates).toEqual([])
    for (const s of snaps) expect(s.candidates).toEqual([])
    expect(a.list).toHaveBeenCalledTimes(2)   // the shared memo: Y listed once, not once per role
  })

  it('an unavailable host is not a candidate and is never listed', async () => {
    const a = fakeApi({ ...base, list: { hM: [], hA: [] } })
    const final = await loadPairings(X, [{ ...AIR, status: 'disconnected' }], a, () => {})
    expect(final.candidates).toEqual([])
    expect(a.list).not.toHaveBeenCalledWith('hA')
    expect(a.list).toHaveBeenCalledTimes(1)
  })

  it('a connected host whose info() fails is not a candidate either (§7.1: info could not be fetched)', async () => {
    const a = fakeApi({ info: { hM: 'mini-lab:278cbm', hA: new Error('HTTP 502') }, settings: base.settings, list: { hM: [], hA: [] } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.candidates).toEqual([])
    expect(a.list).not.toHaveBeenCalledWith('hA')
  })

  it('a failing list(Y) lists Y with returnEntry null and listError = the message', async () => {
    const a = fakeApi({ ...base, list: { hM: [], hA: new Error('HTTP 500') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.candidates).toHaveLength(1)
    expect(final.candidates[0]).toMatchObject({ hostId: 'hA', returnEntry: null, listError: 'HTTP 500' })
  })

  it('a failing list(Y) that is a HostApiError keeps the daemon detail in listError', async () => {
    const a = fakeApi({ ...base, list: { hM: [], hA: new HostApiError(403, 'Forbidden', 'admin required') } })
    const final = await loadPairings(X, [AIR], a, () => {})
    expect(final.candidates[0]).toMatchObject({ hostId: 'hA', returnEntry: null, listError: 'admin required' })
  })

  it('a counterpart host and another available host with no entry: only the second is a candidate', async () => {
    const a = fakeApi({
      info: { ...base.info, hO: 'other:zzzzzz' }, settings: { ...base.settings, hO: 'other' },
      list: { hM: [row({})], hA: [RETURN], hO: [] },
      verify: { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb'), 'hA/mini-lab': ok('mini-lab', 'mini-lab', 'mini-lab:278cbm') },
    })
    const final = await loadPairings(X, [AIR, OTHER], a, () => {})
    expect(final.candidates.map((c) => c.hostId)).toEqual(['hO'])
    expect(a.list).toHaveBeenCalledTimes(3)
  })
})

/**
 * A fake whose `list` is a function of the call history, so a row can read
 * differently before and after a dial (the §8.4 mutation fixture: read the row
 * BEFORE the evidence dial and the page acts on stale evidence).
 */
function statefulApi(spec: {
  info: Record<string, string>
  settings: Record<string, string>
  verify: Record<string, PeerHostVerify | Error>
  list: (hostId: string, calls: readonly string[]) => PeerHostRow[] | Error
}): { api: PairingApi; calls: string[] } {
  const calls: string[] = []
  const settle = <T,>(v: T | Error): Promise<T> => (v instanceof Error ? Promise.reject(v) : Promise.resolve(v))
  const api: PairingApi = {
    info: vi.fn((h) => Promise.resolve({ host_id: spec.info[h] })),
    settings: vi.fn((h) => Promise.resolve({ deliver: true, alias: spec.settings[h] })),
    list: vi.fn((h) => { calls.push(`list:${h}`); return settle(spec.list(h, calls)) }),
    // `verify:` is logged when the dial is SENT (for order assertions);
    // `dialled:` only once it has SETTLED, one macrotask later — so a re-read
    // issued concurrently with the dials (mutation M-A1) still sees the
    // pre-dial reading, exactly as a real daemon would answer it.
    verify: vi.fn(async (h, a) => {
      calls.push(`verify:${h}:${a}`)
      const v = spec.verify[`${h}/${a}`]
      await new Promise((r) => setTimeout(r, 0))
      calls.push(`dialled:${h}:${a}`)
      if (v === undefined) throw new Error(`unexpected ${h}/${a}`)
      return settle(v)
    }),
  }
  return { api, calls }
}

describe('loadPairings — post-dial re-read of pending rotations (spec §7.3, step 5)', () => {
  const META = { info: { hM: 'mini-lab:278cbm', hA: 'wakes-air-2026:oa6drb' }, settings: { hM: 'mini-lab', hA: 'air26' } }
  const VERIFY = { 'hM/air': ok('air', 'air26', 'wakes-air-2026:oa6drb'), 'hA/mini-lab': ok('mini-lab', 'mini-lab', 'mini-lab:278cbm') }
  const RETURN = row({ alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm' })
  const nth = (calls: readonly string[], c: string) => calls.filter((k) => k === c).length

  it("a pending rotation on X's entry re-lists X after BOTH dials settle, and the row shows the fresh last_inbound_auth", async () => {
    // X's row reads 'current' until the peer has dialled us (verify(A, mini-lab)), then 'prev'.
    // Mutation M-A (re-read before the dial) makes the final row read 'current' → red.
    const { api, calls } = statefulApi({ ...META, verify: VERIFY,
      list: (h, seen) => h === 'hM'
        ? [row({ rotation_pending: true, last_inbound_auth: seen.includes('dialled:hA:mini-lab') ? 'prev' : 'current' })]
        : [RETURN] })
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], api, emit)
    expect(calls.filter((c) => !c.startsWith('dialled:'))).toEqual(['list:hM', 'list:hA', 'verify:hM:air', 'verify:hA:mini-lab', 'list:hM'])
    expect(calls.lastIndexOf('list:hM')).toBeGreaterThan(calls.indexOf('dialled:hA:mini-lab'))
    expect(calls.lastIndexOf('list:hM')).toBeGreaterThan(calls.indexOf('verify:hA:mini-lab'))
    expect(calls.lastIndexOf('list:hM')).toBeGreaterThan(calls.indexOf('verify:hM:air'))
    expect(final.rows[0].entry.last_inbound_auth).toBe('prev')
    expect(final.rows[0].entry.rotation_pending).toBe(true)
    expect(final.rows[0].gateStale).toEqual({ entry: false, returnEntry: false })
    // pre-dial emit: the row is pending and therefore stale by definition
    expect(snaps[0].rows[0].entry.last_inbound_auth).toBe('current')
    expect(snaps[0].rows[0].gateStale).toEqual({ entry: true, returnEntry: false })
    // every settle emit is still pre-re-read → still stale
    expect(snaps[1].rows[0].gateStale.entry).toBe(true)
    expect(snaps[2].rows[0].gateStale.entry).toBe(true)
    // exactly one more emit for the re-read, carrying the fresh row
    expect(snaps).toHaveLength(4)
    expect(snaps[3].rows[0].entry.last_inbound_auth).toBe('prev')
    expect(snaps[3].rows[0].gateStale.entry).toBe(false)
    // the verify outcomes survive the row replacement
    expect(final.rows[0].outbound).toMatchObject({ ok: true })
    expect(final.rows[0].inbound).toMatchObject({ ok: true })
  })

  it("pending on Y's entry re-lists Y, not X", async () => {
    const { api, calls } = statefulApi({ ...META, verify: VERIFY,
      list: (h, seen) => h === 'hM'
        ? [row({})]
        : [{ ...RETURN, rotation_pending: true, last_inbound_auth: seen.includes('verify:hM:air') ? 'current' : '' }] })
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], api, emit)
    expect(nth(calls, 'list:hM')).toBe(1)
    expect(nth(calls, 'list:hA')).toBe(2)
    expect(calls.lastIndexOf('list:hA')).toBeGreaterThan(calls.indexOf('verify:hA:mini-lab'))
    expect(calls.lastIndexOf('list:hA')).toBeGreaterThan(calls.indexOf('verify:hM:air'))
    expect(snaps[0].rows[0].gateStale).toEqual({ entry: false, returnEntry: true })
    expect(snaps[0].rows[0].returnEntry?.last_inbound_auth).toBe('')
    expect(final.rows[0].returnEntry?.last_inbound_auth).toBe('current')
    expect(final.rows[0].gateStale).toEqual({ entry: false, returnEntry: false })
  })

  it("both sides pending, only Y's re-list fails → gateStale {entry:false, returnEntry:true} and X's side is fresh (codex F5)", async () => {
    const { api, calls } = statefulApi({ ...META, verify: VERIFY,
      list: (h, seen) => {
        if (h === 'hM') return [row({ rotation_pending: true, last_inbound_auth: seen.includes('dialled:hA:mini-lab') ? 'prev' : '' })]
        if (nth(seen, 'list:hA') >= 2) return new Error('HTTP 500')
        return [{ ...RETURN, rotation_pending: true, last_inbound_auth: '' }]
      } })
    const final = await loadPairings(X, [AIR], api, () => {})
    expect(nth(calls, 'list:hM')).toBe(2)
    expect(nth(calls, 'list:hA')).toBe(2)
    expect(final.error).toBeNull()
    expect(final.rows[0].gateStale).toEqual({ entry: false, returnEntry: true })
    expect(final.rows[0].entry.last_inbound_auth).toBe('prev')
    // the pre-dial return row is kept, not dropped
    expect(final.rows[0].returnEntry).toEqual({ ...RETURN, rotation_pending: true, last_inbound_auth: '' })
    expect(final.rows[0].inbound).toMatchObject({ ok: true })
  })

  it('the second list(X) failing leaves that side stale and keeps the pre-dial row', async () => {
    const { api, calls } = statefulApi({ ...META, verify: VERIFY,
      list: (h, seen) => {
        if (h === 'hM') return nth(seen, 'list:hM') >= 2 ? new Error('HTTP 500') : [row({ rotation_pending: true, last_inbound_auth: 'current' })]
        return [RETURN]
      } })
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], api, emit)
    expect(nth(calls, 'list:hM')).toBe(2)
    expect(final.error).toBeNull()
    expect(final.rows[0].entry).toEqual(row({ rotation_pending: true, last_inbound_auth: 'current' }))
    expect(final.rows[0].gateStale).toEqual({ entry: true, returnEntry: false })
    expect(snaps).toHaveLength(4)
    expect(snaps[3].rows[0].gateStale.entry).toBe(true)
  })

  it('no pending rotation anywhere → no extra list call, both sides false everywhere', async () => {
    const { api, calls } = statefulApi({ ...META, verify: VERIFY, list: (h) => (h === 'hM' ? [row({})] : [RETURN]) })
    const { snaps, emit } = collect()
    const final = await loadPairings(X, [AIR], api, emit)
    expect(nth(calls, 'list:hM')).toBe(1)
    expect(nth(calls, 'list:hA')).toBe(1)
    expect(api.list).toHaveBeenCalledTimes(2)     // unchanged from the D2 test
    expect(snaps).toHaveLength(3)                  // no re-read emit
    for (const s of snaps) expect(s.rows[0].gateStale).toEqual({ entry: false, returnEntry: false })
    expect(final.rows[0].gateStale).toEqual({ entry: false, returnEntry: false })
  })

  it('the alias vanished between the two lists → old row kept, side stays stale', async () => {
    const { api, calls } = statefulApi({ ...META, verify: VERIFY,
      list: (h, seen) => {
        if (h === 'hM') return nth(seen, 'list:hM') >= 2 ? [] : [row({ rotation_pending: true, last_inbound_auth: 'current' })]
        return [RETURN]
      } })
    const final = await loadPairings(X, [AIR], api, () => {})
    expect(nth(calls, 'list:hM')).toBe(2)
    expect(final.rows).toHaveLength(1)
    expect(final.rows[0].entry).toEqual(row({ rotation_pending: true, last_inbound_auth: 'current' }))
    expect(final.rows[0].gateStale).toEqual({ entry: true, returnEntry: false })
  })

  it('two rows pending on X share ONE fresh list(X), and each takes the row of its own alias', async () => {
    const second = row({ alias: 'air-again', url: 'http://air.local:7860' })
    const { api, calls } = statefulApi({ ...META,
      verify: { ...VERIFY, 'hM/air-again': ok('air-again', 'air26', 'wakes-air-2026:oa6drb') },
      list: (h, seen) => {
        if (h !== 'hM') return [RETURN]
        const fresh = seen.includes('dialled:hA:mini-lab')
        return [
          row({ rotation_pending: true, last_inbound_auth: fresh ? 'prev' : '' }),
          { ...second, rotation_pending: true, last_inbound_auth: fresh ? 'current' : '' },
        ]
      } })
    const final = await loadPairings(X, [AIR], api, () => {})
    expect(nth(calls, 'list:hM')).toBe(2)
    expect(final.rows.map((r) => r.entry.last_inbound_auth)).toEqual(['prev', 'current'])
    expect(final.rows.map((r) => r.gateStale.entry)).toEqual([false, false])
  })

  it('a pending entry with no App counterpart still re-reads X; the returnEntry side is false (nothing there)', async () => {
    const stranger = row({ host_id: 'stranger:aaaaaa', url: 'http://10.0.0.1:7860', rotation_pending: true, last_inbound_auth: 'current' })
    const { api, calls } = statefulApi({ ...META, verify: { 'hM/air': ok('air', 'stranger', 'stranger:aaaaaa') },
      list: (h, seen) => (h === 'hM' ? [{ ...stranger, last_inbound_auth: seen.includes('verify:hM:air') ? 'prev' : 'current' }] : []) })
    const final = await loadPairings(X, [AIR], api, () => {})
    expect(nth(calls, 'list:hM')).toBe(2)
    expect(final.rows[0].counterpart).toBeNull()
    expect(final.rows[0].entry.last_inbound_auth).toBe('prev')
    expect(final.rows[0].gateStale).toEqual({ entry: false, returnEntry: false })
  })

  it('candidates are not re-read in step 5 (X cannot dial a host it has no entry for)', async () => {
    const { api, calls } = statefulApi({
      info: { ...META.info, hO: 'other:zzzzzz' }, settings: { ...META.settings, hO: 'other' }, verify: VERIFY,
      list: (h, seen) => {
        if (h === 'hM') return [row({ rotation_pending: true, last_inbound_auth: seen.includes('dialled:hA:mini-lab') ? 'prev' : '' })]
        if (h === 'hA') return [RETURN]
        return [row({ alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm', rotation_pending: true, last_inbound_auth: '' })]
      } })
    const final = await loadPairings(X, [AIR, OTHER], api, () => {})
    expect(nth(calls, 'list:hM')).toBe(2)
    expect(nth(calls, 'list:hO')).toBe(1)
    expect(final.candidates.map((c) => c.hostId)).toEqual(['hO'])
    expect(final.rows[0].gateStale.entry).toBe(false)
  })
})
