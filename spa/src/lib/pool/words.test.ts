import { describe, it, expect } from 'vitest'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'
import { reasonWord, headroomReasonLines, driverKey, zoneKey, errorTextKey, REASON_CODES, DRIVERS } from './words'
import { parseHostsAnswer } from './parse'
import reasonsJson from './__fixtures__/pool_hosts_response_reasons.json'
import { ERROR_CODES } from './errors'

const t = (key: string, opts?: Record<string, unknown>) => `${key}${opts ? JSON.stringify(opts) : ''}`
const has = (k: string) => k in (en as Record<string, string>) && k in (zhTW as Record<string, string>)

describe('reason words', () => {
  it('every code has a word in both locales', () => {
    for (const c of [...REASON_CODES, 'exception:x']) expect(has(reasonWord(c, (k) => k)), c).toBe(true)
  })
  it('exception:<name> carries the name', () => {
    expect(reasonWord('exception:dev-server', t)).toBe('pool.reason.exception{"name":"dev-server"}')
  })
  it('several exceptions keep their own names', () => {
    expect(['exception:a', 'exception:b'].map((c) => reasonWord(c, t))).toEqual(['pool.reason.exception{"name":"a"}', 'pool.reason.exception{"name":"b"}'])
  })
  it('disk_low has its own word, not the raw code', () => {
    expect(reasonWord('disk_low', t)).toBe('pool.reason.disk_low')
  })
  it('an unknown code is shown as it is', () => {
    expect(reasonWord('from_the_future', t)).toBe('from_the_future')
  })
})

describe('other tables', () => {
  it('drivers and zones have words in both locales; an unknown driver is not a key', () => {
    for (const d of DRIVERS) expect(has(driverKey(d)!), d).toBe(true)
    expect(driverKey('gpu')).toBeNull()
    for (const z of ['normal', 'warning', 'danger', 'unknown', 'offline'] as const) expect(has(zoneKey(z)), z).toBe(true)
  })
  it('every error code has a text in both locales', () => {
    for (const c of ERROR_CODES) {
      const k = errorTextKey(c)
      if (k) expect(has(k), c).toBe(true)
    }
  })
})

describe('headroom reason lines (the daemon\'s golden: mlab, mserv, an unreachable host)', () => {
  const [mlab, mserv, air19] = parseHostsAnswer(reasonsJson)!
  const line = (cls: string, reason: string) => `pool.card.reason_line{"cls":"pool.card.class.${cls}","reason":"${reason}"}`
  const words = (...codes: string[]) => codes.map((c) => `pool.reason.${c}`).join(' · ')

  it('a class at 0 says why, in the daemon\'s order; a class above 0 says nothing', () => {
    expect(headroomReasonLines({ heavy: 0, light: 2 }, mlab.headroom_reasons ?? null, t)).toEqual([line('heavy', words('zone_warning', 'slots_full'))])
  })

  it('an unreachable host sends no reasons: the plain 0, nothing invented', () => {
    expect(air19.headroom_reasons).toBeUndefined()
    expect(headroomReasonLines({ heavy: 0, light: 0 }, air19.headroom_reasons ?? null, t)).toEqual([])
  })

  it('reasons sent but none for a zero (an empty list) read as unknown, once for both classes', () => {
    expect(headroomReasonLines({ heavy: 0, light: 0 }, { heavy: [], light: [] }, t)).toEqual([line('both', words('unknown'))])
  })

  it('the daemon naming `unknown` for light only', () => {
    expect(mserv.headroom_reasons).toEqual({ heavy: [], light: ['unknown'] })
    expect(headroomReasonLines({ heavy: 2, light: 0 }, mserv.headroom_reasons ?? null, t)).toEqual([line('light', words('unknown'))])
  })

  it('different words for the two classes are two lines', () => {
    expect(headroomReasonLines({ heavy: 0, light: 0 }, { heavy: ['worker_off'], light: ['gate_full'] }, t)).toEqual([line('heavy', words('worker_off')), line('light', words('gate_full'))])
  })

  it('disk_low on heavy only: one heavy line with the disk_low word', () => {
    expect(headroomReasonLines({ heavy: 0, light: 2 }, { heavy: ['disk_low'], light: [] }, t)).toEqual([line('heavy', words('disk_low'))])
  })

  it('no headroom at all: nothing', () => {
    expect(headroomReasonLines(null, { heavy: ['worker_off'], light: [] }, t)).toEqual([])
  })
})
