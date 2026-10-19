import { describe, it, expect } from 'vitest'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'
import { reasonWord, driverKey, zoneKey, errorTextKey, fallbackWord, REASON_CODES, DRIVERS } from './words'
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
  it('an unknown code is shown as it is', () => {
    expect(reasonWord('from_the_future', t)).toBe('from_the_future')
  })
})

describe('fallback words (8a-3b)', () => {
  it('the two reasons have their own words, with and without a time, in both locales', () => {
    for (const r of ['unreachable', 'stale'] as const) {
      expect(has(`pool.card.fallback_${r}`), r).toBe(true)
      expect(has(`pool.card.fallback_${r}_nosince`), r).toBe(true)
      expect(fallbackWord((k) => k, r, null)).toBe(`pool.card.fallback_${r}_nosince`)
      expect(fallbackWord((k) => k, r, 1_700_000_000_000)).toBe(`pool.card.fallback_${r}`)
    }
    expect(fallbackWord((k) => k, 'unreachable', null)).not.toBe(fallbackWord((k) => k, 'stale', null))
  })
  it('no reason given reads as unreachable (a row built without one)', () => {
    expect(fallbackWord((k) => k, null, null)).toBe('pool.card.fallback_unreachable_nosince')
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
