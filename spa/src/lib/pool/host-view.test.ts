import { describe, it, expect } from 'vitest'
import { poolHostView, LIVE_MS } from './host-view'
import { parseFacts, parseHostsAnswer } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import hostsJson from './__fixtures__/pool_hosts_response.json'
import type { PoolHost } from './types'
import type { FleetHost } from '../fleet/types'

const facts = parseFacts(darwinJson)!
const NOW = facts.at + 10_000
const poolHost = (over: Partial<PoolHost> = {}): PoolHost => ({ ...parseHostsAnswer(hostsJson)![0], alias: 'mlab', facts, reachable: true, ...over })
const fleetHost = (over: Partial<FleetHost> = {}): FleetHost => ({
  id: 'mlab:abc', kind: 'daemon', alias: 'mlab', name: 'mlab', icon: '', os: 'darwin', status: 'ok', error: null, last_ok_at: NOW - 2000, fast: {} as FleetHost['fast'], slow: null, ...over,
})
const input = (over: Record<string, unknown> = {}) => ({ alias: 'mlab', pool: poolHost(), fleet: fleetHost(), fleetStale: false, poolLastOk: null as number | null, ...over })

describe('poolHostView', () => {
  it('live: the pool reaches the host and its facts are fresh', () => {
    const v = poolHostView(input(), NOW)
    expect(v.state).toBe('live')
    if (v.state === 'live') expect(v.facts).toBe(facts)
  })

  it('facts exactly 30 s old are still live, older than 30 s are not', () => {
    expect(poolHostView(input(), facts.at + LIVE_MS).state).toBe('live')
    expect(poolHostView(input(), facts.at + LIVE_MS + 1).state).toBe('fallback')
  })

  it('a pool host that is unreachable, or has no facts, falls back to the fleet', () => {
    expect(poolHostView(input({ pool: poolHost({ reachable: false }) }), NOW).state).toBe('fallback')
    expect(poolHostView(input({ pool: poolHost({ facts: null }) }), NOW).state).toBe('fallback')
  })

  it('a host the pool does not list falls back to the fleet', () => {
    expect(poolHostView(input({ pool: null }), NOW).state).toBe('fallback')
  })

  it('fallback carries the fleet host and when the pool last read it', () => {
    const v = poolHostView(input({ pool: null, poolLastOk: NOW - 60_000 }), NOW)
    if (v.state !== 'fallback') throw new Error(v.state)
    expect(v.fleet.alias).toBe('mlab')
    expect(v.since).toBe(NOW - 60_000)
  })

  it('a stale fleet snapshot is not a reading: the host is offline', () => {
    expect(poolHostView(input({ pool: null, fleetStale: true }), NOW).state).toBe('offline')
  })

  it('a fleet host that is not ok is not a reading', () => {
    expect(poolHostView(input({ pool: null, fleet: fleetHost({ status: 'offline' }) }), NOW).state).toBe('offline')
  })

  it('ok without a fast reading is not a reading', () => {
    expect(poolHostView(input({ pool: null, fleet: fleetHost({ fast: null }) }), NOW).state).toBe('offline')
  })

  it('neither source: offline, with the newer last-ok of the two', () => {
    const v = poolHostView(input({ pool: null, fleet: fleetHost({ status: 'offline', last_ok_at: 5000 }), poolLastOk: 9000 }), NOW)
    expect(v).toEqual({ state: 'offline', alias: 'mlab', lastOkAt: 9000 })
    const w = poolHostView(input({ pool: null, fleet: fleetHost({ status: 'offline', last_ok_at: 12000 }), poolLastOk: 9000 }), NOW)
    expect(w).toMatchObject({ state: 'offline', lastOkAt: 12000 })
  })

  it('offline with no history at all has no last-ok', () => {
    expect(poolHostView(input({ pool: null, fleet: null }), NOW)).toEqual({ state: 'offline', alias: 'mlab', lastOkAt: null })
  })
})
