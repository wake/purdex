import { describe, it, expect } from 'vitest'
import { poolCellModel, matchPoolHost, unlistedState, fleetViewOfPoolHost } from './fleet-bridge'
import { hostExtraOf } from './host-extra'
import { poolHostState } from './host-state'
import { parseFacts, parseHostsAnswer } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import linuxJson from './__fixtures__/facts_linux.json'
import hostsJson from './__fixtures__/pool_hosts_response.json'
import type { PoolHost } from './types'
import { fast, host } from '../fleet/__fixtures__/builders'

const darwin = parseFacts(darwinJson)!
const linux = parseFacts(linuxJson)!
const NOW = darwin.at + 5000
const base = parseHostsAnswer(hostsJson)![0]
const poolHost = (over: Partial<PoolHost> = {}): PoolHost => ({ ...base, alias: 'mlab', host_id: 'h-mlab', facts: darwin, reachable: true, ...over })
const fleet = (over: Record<string, unknown> = {}) => ({ ...host('d-mlab', { name: 'mlab', fast: fast({ load: 33 }) }), alias: 'mlab', ...over })
// A host the pool does not list reads as `unknown` (MON-1d: no fleet fallback); `lastFactsAt` is what the store kept for the host_id.
const model = (ph: PoolHost | null, f = fleet(), lastFactsAt: number | null = null) =>
  poolCellModel(f, ph ? poolHostState(ph, lastFactsAt, NOW) : unlistedState(lastFactsAt), ph)

describe('fleetViewOfPoolHost', () => {
  const air = poolHost({ alias: 'air26', host_id: '0123456789abcdef' })
  const views = [
    { ...fleet({ alias: 'other', id: 'd-1' }), kind: 'daemon', hostConfigId: 'cfg-air26' },
    { ...fleet({ alias: 'air26', id: 'd-2' }), kind: 'daemon', hostConfigId: 'cfg-2' },
    { ...fleet({ alias: 'probe-x', id: 'p-1' }), kind: 'probe' },
  ] as never[]
  it('by the App host that is the daemon of the pool host, whatever the alias says', () => {
    expect(fleetViewOfPoolHost(views, air, { '0123456789abcdef': 'cfg-air26' })?.id).toBe('d-1')
  })
  it('by alias when no daemon is known', () => expect(fleetViewOfPoolHost(views, air, {})?.id).toBe('d-2'))
  it('two fleet hosts sharing the alias are no match (an alias is not unique), unless the daemon says which', () => {
    const twins = [{ ...fleet({ alias: 'air26', id: 'a' }), kind: 'daemon', hostConfigId: 'c1' }, { ...fleet({ alias: 'air26', id: 'b' }), kind: 'daemon', hostConfigId: 'c2' }] as never[]
    expect(fleetViewOfPoolHost(twins, air, {})).toBeNull()
    expect(fleetViewOfPoolHost(twins, air, { '0123456789abcdef': 'c2' })?.id).toBe('b')
  })
  it('a probe row of the same alias is never the fleet host of a pool host (a pool host is a daemon)', () => {
    const onlyProbe = [{ ...fleet({ alias: 'air26', id: 'p-9' }), kind: 'probe' }] as never[]
    expect(fleetViewOfPoolHost(onlyProbe, air, {})).toBeNull()
    const both = [...onlyProbe, { ...fleet({ alias: 'air26', id: 'd-9' }), kind: 'daemon', hostConfigId: 'c9' }] as never[]
    expect(fleetViewOfPoolHost(both, air, {})?.id).toBe('d-9')
  })
  it('a pool host with neither is null', () => expect(fleetViewOfPoolHost(views, poolHost({ alias: 'nope', host_id: 'zz' }), {})).toBeNull())
})

describe('matchPoolHost', () => {
  const hosts = [poolHost(), poolHost({ alias: 'air26', host_id: '0123456789abcdef' })]
  const daemons = { '0123456789abcdef': 'cfg-air26' }
  it('by alias', () => expect(matchPoolHost(hosts, fleet({ alias: 'air26' }) as never, {})?.alias).toBe('air26'))
  it('a daemon host by the App host that is the daemon of the pool host, whatever the alias says', () => {
    expect(matchPoolHost(hosts, { alias: 'other', kind: 'daemon', hostConfigId: 'cfg-air26' }, daemons)?.alias).toBe('air26')
    expect(matchPoolHost(hosts, { alias: '', kind: 'daemon', hostConfigId: 'cfg-air26' }, daemons)?.alias).toBe('air26')
  })
  it('the daemon mapping wins over an earlier pool host whose alias collides', () => {
    const both = [poolHost({ alias: 'air26' }), poolHost({ alias: 'mserv', host_id: '0123456789abcdef' })]
    expect(matchPoolHost(both, { alias: 'air26', kind: 'daemon', hostConfigId: 'cfg-air26' }, daemons)?.alias).toBe('mserv')
  })
  it('the old host id and daemon id rules no longer match (a Purdex host id is never the wwd one)', () => {
    expect(matchPoolHost(hosts, fleet({ alias: '', id: '0123456789abcdef' }) as never, {})).toBeNull()
    expect(matchPoolHost(hosts, { alias: '', kind: 'daemon', hostConfigId: 'cfg-1', daemonId: '0123456789abcdef' } as never, {})).toBeNull()
  })
  it('no match is null, and an empty alias never matches', () => {
    expect(matchPoolHost(hosts, fleet({ alias: 'nope' }) as never, {})).toBeNull()
    expect(matchPoolHost([poolHost({ alias: '' })], fleet({ alias: '' }) as never, {})).toBeNull()
  })
})

describe('poolCellModel — live', () => {
  it('the readings come from the facts, the gate queue is dropped, the load from the assess field', () => {
    const m = model(poolHost({ load: { score: 91, driver: 'disk' } }))
    expect(m.pool?.state).toBe('ok')
    expect(m.view.status).toBe('ok')
    expect(m.view.fast).toMatchObject({ cpu: 100 - darwin.cpu.idle_pct, level: darwin.mem.pressure, load: 91, zone: 'normal' })
    expect(m.view.fast!.mem).toBeCloseTo(((16384 - 843) / 16384) * 100, 5)
    expect(m.view.slow).toBeNull()
    expect(m.pool?.load).toEqual({ score: 91, driver: 'disk' })
  })

  it('without the assess field the ring keeps the fleet score', () => {
    const m = model(poolHost())
    expect(m.view.fast!.load).toBe(33)
    expect(m.pool?.load).toBeNull()
  })

  it('the pool part is what hostExtraOf says (worker, headroom, owner, zone, PSI: see host-extra.test.ts)', () => {
    const ph = poolHost({ facts: linux })
    const st = poolHostState(ph, null, NOW)
    expect(model(ph).pool).toEqual(hostExtraOf(st, ph))
  })

  it('the zone unknown is kept for the cell, whatever the placeholder in the readings', () => {
    const m = model(poolHost({ facts: linux }), fleet())
    expect(m.view.fast!.zone).toBe('normal')
    expect(m.pool?.zone).toBe('unknown')
  })

  it('the fullest disk gives the disk reading', () => {
    const f = { ...darwin, disk: [{ ...darwin.disk[0], used_pct: 40, free_gb: 100 }, { ...darwin.disk[0], mount: '/d', used_pct: 91, free_gb: 9 }] }
    expect(model(poolHost({ facts: f }))!.view.fast).toMatchObject({ disk: 91, disk_free_gb: 9 })
  })
})

describe('poolCellModel — a host that is not ok (no fleet fallback, MON-1d)', () => {
  const peerDown = (liveness: 'wwd_down' | 'host_down' | 'unknown', over: Partial<PoolHost> = {}) =>
    poolHost({ liveness, liveness_at: NOW - 3000, liveness_since: NOW - 600_000, facts: null, ...over })

  it('the host reads as offline whatever the fleet says: no fast reading, the last facts time as last-ok', () => {
    const f = fleet()
    const m = model(peerDown('wwd_down'), f, NOW - 90_000)
    expect(m.view).toMatchObject({ status: 'offline', fast: null, last_ok_at: NOW - 90_000 })
    expect(m.view).not.toBe(f)
  })

  it('a host the pool does not list is unknown, not read from the fleet (even when the fleet says ok)', () => {
    const m = model(null, fleet(), null)
    expect(m.view).toMatchObject({ status: 'offline', fast: null })
    expect(m.pool).toMatchObject({ state: 'unknown', checkedAt: null, stalled: false, lastFactsAt: null })
  })
})

describe('poolCellModel — no pool', () => {
  it('without a pool view the fleet host is returned as it is, nothing added', () => {
    const f = fleet()
    expect(poolCellModel(f, null, null)).toEqual({ view: f, pool: null })
  })
})
