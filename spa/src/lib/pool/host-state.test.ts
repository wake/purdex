// spa/src/lib/pool/host-state.test.ts — the one host state (monitoring tabs spec M21, A2, W3): ok / wwd_down / host_down / unknown.
import { describe, it, expect } from 'vitest'
import { hostIsOk, LIVE_MS, MAX_SKEW_MS, poolHostState } from './host-state'
import { parseFacts, parseHostsAnswer } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import hostsJson from './__fixtures__/pool_hosts_response.json'
import type { PoolHost } from './types'

const facts = parseFacts(darwinJson)!
const NOW = facts.at + 10_000
const CHECKED = NOW - 3_000
const SINCE = NOW - 600_000
const base = (): PoolHost => ({ ...parseHostsAnswer(hostsJson)![0], alias: 'mserv', host_id: '82b11c521df16c12', facts, reachable: true })
const peer = (over: Partial<PoolHost> = {}): PoolHost => ({ ...base(), liveness: 'ok', liveness_at: CHECKED, ...over })

describe('poolHostState — a peer row (it has `liveness`)', () => {
  it('ok with facts inside 30 s is ok, and carries the facts', () => {
    const s = poolHostState(peer(), facts.at, NOW)
    expect(s).toMatchObject({ state: 'ok', hostId: '82b11c521df16c12', alias: 'mserv' })
    if (s.state !== 'ok') throw new Error(s.state)
    expect(s.facts).toBe(facts)
  })

  it('facts exactly 30 s old are still ok, older than 30 s are not', () => {
    expect(poolHostState(peer(), facts.at, facts.at + LIVE_MS).state).toBe('ok')
    expect(poolHostState(peer(), facts.at, facts.at + LIVE_MS + 1).state).toBe('unknown')
  })

  it('ok but facts older than 30 s, or missing, is unknown and stalled (W3); the last facts time is passed through', () => {
    const old = poolHostState(peer(), facts.at, facts.at + LIVE_MS + 1)
    expect(old).toMatchObject({ state: 'unknown', stalled: true, lastFactsAt: facts.at, checkedAt: CHECKED })
    expect(poolHostState(peer({ facts: null }), facts.at, NOW)).toMatchObject({ state: 'unknown', stalled: true, lastFactsAt: facts.at })
  })

  it.each(['wwd_down', 'host_down', 'unknown'] as const)('%s is kept as it is, with the check time, since and last facts', (liveness) => {
    const s = poolHostState(peer({ liveness, liveness_since: SINCE, facts: null }), 1234, NOW)
    expect(s).toEqual({ state: liveness, hostId: '82b11c521df16c12', alias: 'mserv', pool: expect.anything(), lastFactsAt: 1234, checkedAt: CHECKED, since: SINCE, stalled: false })
  })

  it('wwd_down stays wwd_down even when the old facts still look fresh', () => {
    expect(poolHostState(peer({ liveness: 'wwd_down' }), facts.at, NOW).state).toBe('wwd_down')
  })

  it('a missing check time or since time is null, not invented', () => {
    const s = poolHostState(peer({ liveness: 'host_down', liveness_at: undefined }), null, NOW)
    expect(s).toMatchObject({ checkedAt: null, since: null, lastFactsAt: null })
  })
})

// codex attack (high): facts dated ahead of this clock were "fresh" for good, so a frozen host showed ok forever.
describe('poolHostState — facts dated in the future', () => {
  it('a little ahead (clock skew) is still fresh, further ahead than MAX_SKEW_MS is not', () => {
    expect(MAX_SKEW_MS).toBe(10_000)
    expect(poolHostState(peer(), facts.at, facts.at - MAX_SKEW_MS).state).toBe('ok')
    expect(poolHostState(peer(), facts.at, facts.at - MAX_SKEW_MS - 1)).toMatchObject({ state: 'unknown', stalled: true })
    expect(poolHostState(peer(), facts.at, 1_000)).toMatchObject({ state: 'unknown', stalled: true })
  })

  it('the same for a row without liveness', () => {
    expect(poolHostState(base(), facts.at, 1_000)).toMatchObject({ state: 'unknown', stalled: true })
  })
})

describe('poolHostState — a row without `liveness` (the local host, an older daemon)', () => {
  const local = (over: Partial<PoolHost> = {}): PoolHost => ({ ...base(), ...over })

  it('reachable with fresh facts is ok', () => {
    expect(poolHostState(local(), facts.at, NOW).state).toBe('ok')
  })

  it('reachable with stale or missing facts is unknown and stalled', () => {
    expect(poolHostState(local(), facts.at, facts.at + LIVE_MS + 1)).toMatchObject({ state: 'unknown', stalled: true })
    expect(poolHostState(local({ facts: null }), null, NOW)).toMatchObject({ state: 'unknown', stalled: true })
  })

  it('not reachable is unknown and not stalled (nothing answered), even with fresh facts', () => {
    expect(poolHostState(local({ reachable: false }), facts.at, NOW)).toMatchObject({ state: 'unknown', stalled: false, checkedAt: null })
  })
})

describe('hostIsOk', () => {
  it('is true only for an ok row', () => {
    expect(hostIsOk(peer(), NOW)).toBe(true)
    expect(hostIsOk(peer({ liveness: 'wwd_down' }), NOW)).toBe(false)
    expect(hostIsOk(peer(), facts.at + LIVE_MS + 1)).toBe(false)
    expect(hostIsOk({ ...base(), reachable: false }, NOW)).toBe(false)
  })
})
