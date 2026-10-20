// spa/src/lib/pool/cell-model.test.ts — the sidebar cell's model built from the pool only (monitoring tabs MON-1c).
import { describe, it, expect } from 'vitest'
import { cellOf, factsToCellFast } from './cell-model'
import { poolHostState } from './host-state'
import { parseFacts, parseHostsAnswer } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import linuxJson from './__fixtures__/facts_linux.json'
import hostsJson from './__fixtures__/pool_hosts_response.json'
import type { MonitorHost } from './use-monitor-hosts'
import type { PoolHost } from './types'

const darwin = parseFacts(darwinJson)!
const linux = parseFacts(linuxJson)!
const base = parseHostsAnswer(hostsJson)![0]
const NOW = darwin.at + 5000
const look = { indicator: { shape: 'ring', data: ['load'] } } as const

const monitor = (pool: PoolHost, lastFactsAt: number | null = null): MonitorHost => ({
  hostId: pool.host_id, alias: pool.alias, daemonHostId: null, state: poolHostState(pool, lastFactsAt, NOW), look: look as never, poolFailure: null, count: null, local: false,
})
const poolHost = (over: Partial<PoolHost> = {}): PoolHost => ({ ...base, alias: 'mlab', reachable: true, facts: darwin, ...over })

describe('cellOf — the card\'s detail', () => {
  it('local comes from the monitor row, not from a missing liveness', () => {
    const peer = poolHost({ liveness: undefined })
    expect(cellOf(monitor(peer)).detail?.local).toBe(false)
    expect(cellOf({ ...monitor(peer), local: true }).detail?.local).toBe(true)
  })
})

describe('cellOf — an ok host', () => {
  it('the readings come from the facts, the load from the assess field', () => {
    const c = cellOf(monitor(poolHost({ load: { score: 91, driver: 'disk' } })))
    expect(c.state.state).toBe('ok')
    expect(c.fast).toMatchObject({ cpu: 100 - darwin.cpu.idle_pct, level: darwin.mem.pressure, load: 91, zone: 'normal' })
    expect(c.fast!.mem).toBeCloseTo(((16384 - 843) / 16384) * 100, 5)
    expect(c.load).toEqual({ score: 91, driver: 'disk' })
    expect(c.name).toBe('mlab')
    expect(c.worker).toEqual({ enabled: darwin.worker.enabled, slots: darwin.worker.slots, running: darwin.worker.running })
    expect(c.headroom).toEqual(darwin.derived.headroom)
    expect(c.psi).toBeNull()
  })
  it('the load is 0 without pool.assess.v1, and the zone unknown rides in zone, normal in fast', () => {
    const unknown = { ...darwin, derived: { ...darwin.derived, zone: 'unknown' as const } }
    const c = cellOf(monitor(poolHost({ facts: unknown })))
    expect(c.zone).toBe('unknown')
    expect(c.fast).toMatchObject({ zone: 'normal', load: 0 })
  })
  it('a Linux host carries its PSI', () => {
    const c = cellOf(monitor(poolHost({ facts: linux })))
    expect(c.psi).toEqual(linux.mem.psi ? { some10: linux.mem.psi.some10, full10: linux.mem.psi.full10 } : null)
  })
})

describe('cellOf — a host that is not ok', () => {
  it.each(['wwd_down', 'host_down', 'unknown'] as const)('%s has the state and no readings', (liveness) => {
    const c = cellOf(monitor(poolHost({ reachable: false, facts: null, liveness }), darwin.at - 60_000))
    expect(c.state).toMatchObject({ state: liveness, lastFactsAt: darwin.at - 60_000 })
    expect(c).toMatchObject({ fast: null, zone: null, load: null, worker: null, headroom: null, owner: null, psi: null })
  })
  it('wwd answers with old facts: unknown and stalled, still no readings', () => {
    const c = cellOf(monitor(poolHost({ liveness: 'ok', facts: { ...darwin, at: darwin.at - 120_000 } })))
    expect(c.state).toMatchObject({ state: 'unknown', stalled: true })
    expect(c.fast).toBeNull()
  })
})

describe('factsToCellFast', () => {
  it('takes the fullest disk', () => {
    const f = { ...darwin, disk: [{ mount: '/', used_pct: 40, free_gb: 100 }, { mount: '/data', used_pct: 90, free_gb: 12 }] } as never
    expect(factsToCellFast(f, 0)).toMatchObject({ disk: 90, disk_free_gb: 12 })
  })
})
