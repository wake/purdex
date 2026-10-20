import { describe, it, expect } from 'vitest'
import { hostExtraOf } from './host-extra'
import { poolHostState } from './host-state'
import { parseFacts, parseHostsAnswer } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import linuxJson from './__fixtures__/facts_linux.json'
import hostsJson from './__fixtures__/pool_hosts_response.json'
import type { PoolHost } from './types'

const darwin = parseFacts(darwinJson)!
const linux = parseFacts(linuxJson)!
const NOW = darwin.at + 5000
const base = parseHostsAnswer(hostsJson)![0]
const poolHost = (over: Partial<PoolHost> = {}): PoolHost => ({ ...base, alias: 'mlab', host_id: 'h-mlab', facts: darwin, reachable: true, ...over })
const extra = (ph: PoolHost, lastFactsAt: number | null = null) => hostExtraOf(poolHostState(ph, lastFactsAt, NOW), ph)

describe('hostExtraOf — live', () => {
  it('the daemon load is carried, and is null without the assess field', () => {
    expect(extra(poolHost({ load: { score: 91, driver: 'disk' } })).load).toEqual({ score: 91, driver: 'disk' })
    expect(extra(poolHost()).load).toBeNull()
    expect(extra(poolHost()).state).toBe('ok')
  })

  it('worker, headroom, sessions room and owner are carried', () => {
    const p = extra(poolHost())
    expect(p.worker).toEqual({ enabled: true, slots: { heavy: 1, light: 2 }, running: { heavy: 0, light: 1 } })
    expect(p.headroom).toEqual({ heavy: 1, light: 2 })
    expect(p.sessionsRoom).toBe(2)
    expect(p.owner).toEqual({ active: true, idle_s: 42, reason: 'hid' })
  })

  it('reasons are carried only when the daemon sent them', () => {
    expect(extra(poolHost()).reasons).toBeNull()
    expect(extra(poolHost({ headroom_reasons: { heavy: ['gate_full'], light: [] } })).reasons).toEqual({ heavy: ['gate_full'], light: [] })
  })

  it('the zone unknown is kept, whatever the placeholder in the readings', () => {
    expect(extra(poolHost({ facts: linux })).zone).toBe('unknown')
  })

  it('Linux: PSI some10 / full10; macOS has none', () => {
    expect(extra(poolHost({ facts: linux })).psi).toEqual({ some10: 0.5, full10: 0 })
    expect(extra(poolHost()).psi).toBeNull()
  })

  it('an ok host has no check time and is not stalled', () => {
    expect(extra(poolHost())).toMatchObject({ checkedAt: null, stalled: false })
  })
})

describe('hostExtraOf — a host that is not ok', () => {
  const peerDown = (liveness: 'wwd_down' | 'host_down' | 'unknown', over: Partial<PoolHost> = {}) =>
    poolHost({ liveness, liveness_at: NOW - 3000, liveness_since: NOW - 600_000, facts: null, ...over })

  it.each(['wwd_down', 'host_down', 'unknown'] as const)('%s: the state, what is unknown, the check time and the last facts time', (liveness) => {
    expect(extra(peerDown(liveness), NOW - 90_000)).toMatchObject({
      state: liveness, zone: null, worker: null, headroom: null, owner: null, psi: null, load: null, reasons: null, sessionsRoom: null,
      checkedAt: NOW - 3000, lastFactsAt: NOW - 90_000, stalled: false,
    })
  })

  it('stalled: wwd answers (ok) but the facts are old — unknown with stalled and the time the readings stopped', () => {
    const old = poolHost({ liveness: 'ok', liveness_at: NOW - 3000, facts: { ...darwin, at: NOW - 60_000 } })
    expect(extra(old, NOW - 60_000)).toMatchObject({ state: 'unknown', stalled: true, lastFactsAt: NOW - 60_000, checkedAt: NOW - 3000 })
  })

  it('a host the pool does not list is unknown with nothing read', () => {
    expect(hostExtraOf({ state: 'unknown', lastFactsAt: null }, null)).toMatchObject({ state: 'unknown', checkedAt: null, stalled: false, lastFactsAt: null, worker: null, zone: null })
  })
})
