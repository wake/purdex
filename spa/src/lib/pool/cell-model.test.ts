import { describe, it, expect } from 'vitest'
import { poolCellModel, matchPoolHost } from './cell-model'
import { poolHostView, type PoolHostViewInput } from './host-view'
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
const model = (ph: PoolHost | null, f = fleet(), over: Partial<PoolHostViewInput> = {}) =>
  poolCellModel(f, poolHostView({ alias: 'mlab', pool: ph, fleet: f, fleetStale: false, poolLastOk: null, ...over }, NOW), ph)

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
    expect(m.pool?.state).toBe('live')
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

  it('worker, headroom, sessions room, owner and exceptions are carried', () => {
    const p = model(poolHost())!.pool!
    expect(p.worker).toEqual({ enabled: true, slots: { heavy: 1, light: 2 }, running: { heavy: 0, light: 1 } })
    expect(p.headroom).toEqual({ heavy: 1, light: 2 })
    expect(p.sessionsRoom).toBe(2)
    expect(p.owner).toEqual({ active: true, idle_s: 42, reason: 'hid' })
  })

  it('reasons are carried only when the daemon sent them', () => {
    expect(model(poolHost())!.pool!.reasons).toBeNull()
    expect(model(poolHost({ headroom_reasons: { heavy: ['gate_full'], light: [] } }))!.pool!.reasons).toEqual({ heavy: ['gate_full'], light: [] })
  })

  it('the zone unknown is kept for the cell, whatever the placeholder in the readings', () => {
    const m = model(poolHost({ facts: linux }), fleet(), {})
    expect(m.pool?.zone).toBe('unknown')
  })

  it('Linux: PSI some10 / full10; macOS has none', () => {
    expect(model(poolHost({ facts: linux }))!.pool!.psi).toEqual({ some10: 0.5, full10: 0 })
    expect(model(poolHost())!.pool!.psi).toBeNull()
  })

  it('the fullest disk gives the disk reading', () => {
    const f = { ...darwin, disk: [{ ...darwin.disk[0], used_pct: 40, free_gb: 100 }, { ...darwin.disk[0], mount: '/d', used_pct: 91, free_gb: 9 }] }
    expect(model(poolHost({ facts: f }))!.view.fast).toMatchObject({ disk: 91, disk_free_gb: 9 })
  })
})

describe('poolCellModel — fallback and offline', () => {
  it('fallback keeps the fleet host and its readings untouched, with the pool part saying what is unknown', () => {
    const f = fleet()
    const m = model(null, f, { poolLastOk: NOW - 60_000 })
    expect(m.view).toBe(f)
    expect(m.pool).toMatchObject({ state: 'fallback', since: NOW - 60_000, worker: null, headroom: null })
  })

  it('fallback carries why: no pool view of the host is unreachable; old facts from an answering wwd are stale', () => {
    expect(model(null, fleet(), { poolLastOk: NOW - 60_000 }).pool).toMatchObject({ state: 'fallback', reason: 'unreachable' })
    const old = poolHost({ facts: { ...darwin, at: NOW - 60_000 } })
    expect(model(old, fleet(), { poolLastOk: NOW - 60_000 }).pool).toMatchObject({ state: 'fallback', reason: 'stale' })
  })

  it('live and offline have no fallback reason', () => {
    expect(model(poolHost())!.pool!.reason).toBeNull()
    expect(model(null, fleet({ status: 'offline', fast: null }), {}).pool!.reason).toBeNull()
  })

  it('offline: the host reads as offline, with the newer last-ok', () => {
    const f = fleet({ status: 'offline', fast: null, last_ok_at: 1000 })
    const m = model(null, f, { poolLastOk: 9000 })
    expect(m.view).toMatchObject({ status: 'offline', last_ok_at: 9000 })
    expect(m.pool?.state).toBe('offline')
  })

  it('offline: a fleet host that says ok but whose snapshot is stale is shown offline', () => {
    const m = model(null, fleet(), { fleetStale: true })
    expect(m.view.status).toBe('offline')
  })
})

describe('poolCellModel — no pool', () => {
  it('without a pool view the fleet host is returned as it is, nothing added', () => {
    const f = fleet()
    expect(poolCellModel(f, null, null)).toEqual({ view: f, pool: null })
  })
})
