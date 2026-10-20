// spa/src/lib/pool/words.liveness.test.ts — the words of the host states (monitoring tabs spec M17–M21, W3): `livenessWord`, `livenessLines`.
import { describe, it, expect } from 'vitest'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'
import { livenessLines, livenessWord, livenessWordOf, notOkLines } from './words'
import { poolHostState } from './host-state'
import { parseFacts, parseHostsAnswer } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import hostsJson from './__fixtures__/pool_hosts_response.json'
import type { PoolHost } from './types'

const facts = parseFacts(darwinJson)!
const NOW = facts.at + 10_000
const t = (key: string, opts?: Record<string, unknown>) => `${key}${opts ? JSON.stringify(opts) : ''}`
const clock = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
const base = (over: Partial<PoolHost> = {}): PoolHost => ({ ...parseHostsAnswer(hostsJson)![0], host_id: '82b11c521df16c12', alias: 'mserv', facts, reachable: true, ...over })
const state = (over: Partial<PoolHost>, lastFactsAt: number | null, now = NOW) => poolHostState(base(over), lastFactsAt, now)
const CHECK = NOW - 4_000

describe('livenessWord', () => {
  it.each(['ok', 'wwd_down', 'host_down', 'unknown'] as const)('%s has its own key', (name) => {
    const s = state(name === 'ok' ? { liveness: 'ok' } : { liveness: name, facts: null }, null)
    expect(livenessWord(t, s)).toBe(`pool.liveness.${name}`)
  })
})

describe('livenessLines', () => {
  it('a peer that is ok says one word and no last check (M20)', () => {
    expect(livenessLines(t, state({ liveness: 'ok', liveness_at: CHECK }, facts.at), NOW)).toEqual(['pool.liveness.ok'])
  })

  it('the local host that is ok says it is the local wwd', () => {
    expect(livenessLines(t, state({}, facts.at), NOW)).toEqual(['pool.liveness.ok_local'])
  })

  it('wwd_down: what it means for the work, the last reading with how long ago, the last check and the 5 s rhythm', () => {
    const s = state({ liveness: 'wwd_down', liveness_at: CHECK, facts: null }, NOW - 90_000)
    expect(livenessLines(t, s, NOW)).toEqual([
      'pool.liveness.explain_wwd_down',
      `pool.liveness.last_reading{"time":"${clock(NOW - 90_000)}","ago":"pool.liveness.ago_m{\\"n\\":1}"}`,
      `pool.liveness.checked{"time":"${clock(CHECK)}"}`,
    ])
  })

  it('host_down: both ports silent, the last reading, the last check', () => {
    const s = state({ liveness: 'host_down', liveness_at: CHECK, facts: null }, NOW - 5_000)
    expect(livenessLines(t, s, NOW)).toEqual([
      'pool.liveness.explain_host_down',
      `pool.liveness.last_reading{"time":"${clock(NOW - 5_000)}","ago":"pool.liveness.ago_s{\\"n\\":5}"}`,
      `pool.liveness.checked{"time":"${clock(CHECK)}"}`,
    ])
  })

  it('a host never read says so instead of a time', () => {
    const lines = livenessLines(t, state({ liveness: 'host_down', liveness_at: CHECK, facts: null }, null), NOW)
    expect(lines[1]).toBe('pool.liveness.no_reading')
  })

  it('unknown: what happens next, no last-reading line when nothing was read, the check when there is one', () => {
    const lines = livenessLines(t, state({ liveness: 'unknown', facts: null }, null), NOW)
    expect(lines).toEqual(['pool.liveness.explain_unknown', 'pool.liveness.no_reading'])
  })

  it('stalled (W3): wwd answers but the readings stopped at hh:mm:ss, with the last check; the stalled time is not repeated', () => {
    const s = state({ liveness: 'ok', liveness_at: CHECK }, facts.at, facts.at + 40_000)
    expect(s).toMatchObject({ state: 'unknown', stalled: true })
    expect(livenessLines(t, s, facts.at + 40_000)).toEqual([
      `pool.liveness.explain_stalled{"time":"${clock(facts.at)}"}`,
      `pool.liveness.checked{"time":"${clock(CHECK)}"}`,
    ])
  })

  it('stalled with no facts time at all says there were no readings', () => {
    const s = state({ liveness: 'ok', facts: null }, null)
    expect(livenessLines(t, s, NOW)).toEqual(['pool.liveness.explain_stalled_none'])
  })

  it('ages: seconds under a minute, minutes under an hour, hours after', () => {
    const line = (ago: number) => livenessLines(t, state({ liveness: 'host_down', facts: null }, NOW - ago), NOW)[1]
    expect(line(59_000)).toContain('ago_s{\\"n\\":59}')
    expect(line(60_000)).toContain('ago_m{\\"n\\":1}')
    expect(line(3_600_000)).toContain('ago_h{\\"n\\":1}')
  })

  it('every key the lines and word use exists in both locales', () => {
    const keys = ['ok', 'ok_local', 'wwd_down', 'host_down', 'unknown', 'explain_wwd_down', 'explain_host_down', 'explain_unknown', 'explain_stalled', 'explain_stalled_none',
      'last_reading', 'no_reading', 'checked', 'ago_s', 'ago_m', 'ago_h'].map((k) => `pool.liveness.${k}`)
    for (const k of keys) {
      expect(k in (en as Record<string, string>), `en ${k}`).toBe(true)
      expect(k in (zhTW as Record<string, string>), `zh-TW ${k}`).toBe(true)
    }
  })
})

// MON-1d: a cell model's pool part (not a PoolHostState) draws the same words.
describe('livenessWordOf, notOkLines', () => {
  it('livenessWordOf is the word of a bare state', () => {
    expect(livenessWordOf(t, 'host_down')).toBe('pool.liveness.host_down')
  })

  it.each([
    [{ liveness: 'wwd_down' as const, liveness_at: CHECK, facts: null }, NOW - 90_000],
    [{ liveness: 'host_down' as const, liveness_at: CHECK, facts: null }, null],
    [{ liveness: 'ok' as const, liveness_at: CHECK }, facts.at],
  ])('says the same lines as livenessLines for a state that is not ok (%#)', (over, last) => {
    const now = last === facts.at ? facts.at + 40_000 : NOW
    const s = state(over, last, now)
    if (s.state === 'ok') throw new Error('expected a state that is not ok')
    expect(notOkLines(t, { state: s.state, stalled: s.stalled, lastFactsAt: s.lastFactsAt, checkedAt: s.checkedAt }, now)).toEqual(livenessLines(t, s, now))
  })
})
