import { describe, it, expect } from 'vitest'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'
import { reasonWord, driverKey, zoneKey, errorTextKey, REASON_CODES, DRIVERS } from './words'
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
