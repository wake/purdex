import { describe, it, expect } from 'vitest'
import en from './en.json'
import zhTW from './zh-TW.json'

describe('locale completeness', () => {
  const enKeys = Object.keys(en).sort()
  const zhKeys = Object.keys(zhTW).sort()

  it('en.json and zh-TW.json have identical key sets', () => {
    const missingInZh = enKeys.filter((k) => !zhKeys.includes(k))
    const extraInZh = zhKeys.filter((k) => !enKeys.includes(k))
    expect(missingInZh, 'Keys in en.json but missing in zh-TW.json').toEqual([])
    expect(extraInZh, 'Keys in zh-TW.json but not in en.json').toEqual([])
  })

  it('no empty translation values in en.json', () => {
    const empty = Object.entries(en).filter(([, v]) => !v.trim())
    expect(empty.map(([k]) => k), 'Empty values in en.json').toEqual([])
  })

  it('no empty translation values in zh-TW.json', () => {
    const empty = Object.entries(zhTW).filter(([, v]) => !v.trim())
    expect(empty.map(([k]) => k), 'Empty values in zh-TW.json').toEqual([])
  })
})
